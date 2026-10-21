import Foundation

public enum AudioCaptureError: Error, Hashable, Sendable {
    case notRecording
    case failed(entryID: EntryID, failure: CaptureFailure, detail: String?)
}

/// Drives one recording from tap to durable entry.
///
/// The ordering here is the whole point: the directory and the database row
/// exist before a single sample is captured, and the audio facts are committed
/// before anything downstream is told the entry is ready. Every exit that is not
/// `saved` leaves whatever audio was captured on disk and records a reason.
public actor AudioCaptureEngine<Records: CaptureRecordStore> {
    private let store: AudioFileStore
    private let recorder: any AudioRecording
    private let entries: Records
    private let clock: any DateProviding
    private let identifiers: any IdentifierProviding
    private let configuration: RecordingConfiguration
    private let timeZone: TimeZone
    private let protectedData: any ProtectedDataAvailability

    public private(set) var status: CaptureStatus = .idle
    /// Launch recovery found the phone locked; the first unlock pass runs it.
    private var launchRecoveryDeferred = false

    public init(
        store: AudioFileStore,
        recorder: any AudioRecording,
        entries: Records,
        clock: any DateProviding = SystemDateProvider(),
        identifiers: any IdentifierProviding = SystemIdentifierProvider(),
        configuration: RecordingConfiguration = .standard,
        timeZone: TimeZone = .current,
        protectedData: any ProtectedDataAvailability = AlwaysAvailableProtectedData()
    ) {
        self.store = store
        self.recorder = recorder
        self.entries = entries
        self.clock = clock
        self.identifiers = identifiers
        self.configuration = configuration
        self.timeZone = timeZone
        self.protectedData = protectedData
    }

    public func beginRecording(context: Records.Context) async throws -> Records.Snapshot {
        let now = clock.now
        let entryID = identifiers.nextIdentifier()

        // Capacity is checked before the entry exists, so a device too full to
        // record never accumulates entries it cannot fill.
        do {
            try store.requireCapacity()
        } catch {
            throw AudioCaptureError.failed(
                entryID: entryID,
                failure: .insufficientStorage,
                detail: String(describing: error)
            )
        }

        do {
            try store.prepareDirectory(for: entryID)
        } catch {
            throw AudioCaptureError.failed(
                entryID: entryID,
                failure: .finalizationFailed,
                detail: String(describing: error)
            )
        }

        let snapshot = try await entries.createRecording(
            id: entryID,
            context: context,
            capturedAt: now,
            timeZoneIdentifier: timeZone.identifier,
            now: now
        )

        do {
            try await recorder.startRecording(
                to: store.finalURL(for: entryID),
                configuration: configuration
            )
        } catch {
            throw await recordFailure(
                entryID: entryID,
                failure: CaptureFailure.reason(for: error),
                detail: String(describing: error)
            )
        }

        status = .recording(entryID: entryID, startedAt: now)
        return snapshot
    }

    /// Stops, finalizes, and commits. The returned snapshot is durable: the
    /// audio sits at its final path and its size, duration, and hash are written
    /// before any caller is told to start transcription.
    public func finishRecording() async throws -> Records.Snapshot {
        guard case .recording(let entryID, _) = status else {
            throw AudioCaptureError.notRecording
        }

        let recorded: RecordedAudio
        do {
            recorded = try await recorder.stopRecording()
        } catch {
            throw await recordFailure(
                entryID: entryID,
                failure: CaptureFailure.reason(for: error),
                detail: String(describing: error)
            )
        }

        guard recorded.byteCount > 0 else {
            throw await recordFailure(entryID: entryID, failure: .audioMissing, detail: nil)
        }

        let audio = StoredAudio(
            url: recorded.url,
            relativePath: store.relativePath(for: entryID),
            byteCount: recorded.byteCount,
            sha256: recorded.sha256
        )

        // "Saved" only for audio that decodes, by the probe recovery uses.
        // Undecodable bytes are kept and retried at the next launch, never
        // reported saved. A phone that
        // locked before the probe could read the file decides after unlock.
        let decodes = await (try? recorder.duration(of: recorded.url)).map { $0.isFinite && $0 > 0 } ?? false
        guard decodes else {
            guard await protectedData.isAvailable else {
                _ = try? await entries.markCaptureFailure(id: entryID, failure: .awaitingUnlock, now: clock.now)
                status = .idle
                throw AudioCaptureError.failed(entryID: entryID, failure: .awaitingUnlock, detail: nil)
            }
            throw await recordFailure(
                entryID: entryID,
                failure: .finalizationFailed,
                detail: "the recorded audio does not decode",
                retainedAudio: audio
            )
        }

        do {
            let snapshot = try await entries.markSaved(
                id: entryID,
                audio: audio,
                durationSeconds: recorded.durationSeconds,
                now: clock.now
            )
            status = .saved(
                entryID: entryID,
                audio: audio,
                durationSeconds: recorded.durationSeconds
            )
            return snapshot
        } catch {
            // The bytes are on disk and correct; only the row failed. Recovery
            // on the next launch finds the file and finishes the job.
            throw await recordFailure(
                entryID: entryID,
                failure: .finalizationFailed,
                detail: String(describing: error),
                retainedAudio: audio
            )
        }
    }

    /// Ends a recording that cannot continue — an interruption that never
    /// resumed, a revoked permission, a full disk.
    ///
    /// Whatever audio was already captured is finalized, kept, and put through
    /// the same recovery the next launch would have run, so the recording is
    /// durable and processable in this session instead of the next one. Returns
    /// `nil` when nothing was recording, which is how a report that arrives
    /// after the user already stopped is ignored.
    @discardableResult
    public func endInterruptedRecording(failure: CaptureFailure) async -> RecordingInterruption? {
        guard case .recording(let entryID, _) = status else { return nil }
        await recorder.cancelRecording()

        // Locked: the file just closed and cannot be reopened to hash or probe
        // it. Reading it anyway would report the audio as lost.
        guard await protectedData.isAvailable else {
            _ = try? await entries.markCaptureFailure(id: entryID, failure: .awaitingUnlock, now: clock.now)
            status = .idle
            return RecordingInterruption(entryID: entryID, failure: .awaitingUnlock, retainedAudio: nil)
        }

        _ = await recordFailure(entryID: entryID, failure: failure, detail: nil)

        // The recorder is stopped and `recordFailure` already promoted the
        // partial, so recovery is handed that description rather than hashing
        // the same file a second time.
        var retained: StoredAudio?
        if case .failed(_, _, let audio) = status { retained = audio }

        let outcome = await recover(entryID: entryID, isKnown: true, finalized: retained)
        status = .idle

        switch outcome {
        case .recovered(_, let audio), .adopted(_, let audio):
            return RecordingInterruption(
                entryID: entryID,
                failure: failure,
                retainedAudio: audio
            )
        case .unrecoverable(_, let lost):
            return RecordingInterruption(entryID: entryID, failure: lost, retainedAudio: nil)
        case .awaitingUnlock:
            return RecordingInterruption(entryID: entryID, failure: .awaitingUnlock, retainedAudio: nil)
        }
    }

    /// Finishes the work an earlier launch could not.
    ///
    /// Runs before the UI is interactive: every entry left in `recording` and
    /// every entry directory holding unclaimed audio is reconciled. Nothing on
    /// disk is ever deleted here.
    ///
    /// `pendingDeletionIDs` are the entries a purge still owes work on. They are
    /// excluded rather than recovered because recovery is adoption: a cleanup
    /// that could not finish leaves audio behind, and re-adopting it would hand
    /// the user back the recording they asked to destroy.
    ///
    /// While the phone is locked nothing can be read, so nothing is decided:
    /// the pass is left to the first `finishAfterUnlock`.
    @discardableResult
    public func recoverOnLaunch(
        excluding pendingDeletionIDs: Set<EntryID> = []
    ) async throws -> [CaptureRecoveryOutcome] {
        guard await protectedData.isAvailable else {
            launchRecoveryDeferred = true
            return []
        }
        return try await reconcile(excluding: pendingDeletionIDs)
    }

    /// Finishes recordings left `awaitingUnlock`, plus the launch pass
    /// if lock put it off. Safe on every activation: a recording being started
    /// is never `awaitingUnlock`.
    @discardableResult
    public func finishAfterUnlock(
        excluding pendingDeletionIDs: Set<EntryID> = []
    ) async throws -> [CaptureRecoveryOutcome] {
        guard await protectedData.isAvailable else { return [] }
        if launchRecoveryDeferred {
            launchRecoveryDeferred = false
            return try await reconcile(excluding: pendingDeletionIDs)
        }
        var outcomes: [CaptureRecoveryOutcome] = []
        for waiting in try await entries.unfinishedRecordings()
            where waiting.captureFailure == .awaitingUnlock && !pendingDeletionIDs.contains(waiting.id) {
            outcomes.append(await recover(entryID: waiting.id, isKnown: true))
        }
        return outcomes
    }

    private func reconcile(excluding pendingDeletionIDs: Set<EntryID>) async throws -> [CaptureRecoveryOutcome] {
        var outcomes: [CaptureRecoveryOutcome] = []

        // Seeded rather than checked twice: both passes below take an entry only
        // once, and a doomed entry is one no pass may take at all. So is the
        // recording in progress, whose partial is still being written.
        var handled: Set<EntryID> = pendingDeletionIDs
        if case .recording(let active, _) = status { handled.insert(active) }

        for stranded in try await entries.unfinishedRecordings()
            where !handled.contains(stranded.id) {
            handled.insert(stranded.id)
            // An entry already known to have lost its audio is not retried on
            // every launch; only the user can clear it.
            guard stranded.captureFailure?.isRetryable ?? true else { continue }
            outcomes.append(await recover(entryID: stranded.id, isKnown: true))
        }

        // Audio whose row never landed. The directory name carries the entry id,
        // so the recording is still identifiable and is re-adopted.
        for entryID in try store.interruptedEntryIDs() where !handled.contains(entryID) {
            guard try await !entries.contains(id: entryID) else { continue }
            outcomes.append(await recover(entryID: entryID, isKnown: false))
        }

        if !status.isRecording { status = .idle }
        return outcomes
    }

    private func recover(
        entryID: EntryID,
        isKnown: Bool,
        finalized: StoredAudio? = nil
    ) async -> CaptureRecoveryOutcome {
        let playable: PlayableAudio? = if let finalized {
            PlayableAudio(
                audio: finalized,
                durationSeconds: (try? await recorder.duration(of: finalized.url)) ?? 0
            )
        } else {
            await promotePlayableAudio(for: entryID)
        }

        guard let playable else {
            // The phone locked between the caller's check and the read. The
            // bytes were not judged, so they are not reported as lost.
            guard await protectedData.isAvailable else {
                if isKnown {
                    _ = try? await entries.markCaptureFailure(id: entryID, failure: .awaitingUnlock, now: clock.now)
                }
                return .awaitingUnlock(entryID: entryID)
            }
            try? store.discardEmptyPartial(for: entryID)
            // Unplayable bytes are kept, but the entry is flagged so it is never
            // reported as saved and not retried on every launch. An orphan gets a
            // row too, so the user can see and delete what it occupies.
            var hasRow = isKnown
            if !hasRow {
                hasRow = (try? await createRecoveredEntry(entryID: entryID)) != nil
            }
            if hasRow {
                _ = try? await entries.markCaptureFailure(
                    id: entryID,
                    failure: .audioMissing,
                    now: clock.now
                )
            }
            return .unrecoverable(entryID: entryID, failure: .audioMissing)
        }

        let audio = playable.audio
        do {
            if isKnown == false {
                try await createRecoveredEntry(entryID: entryID)
            }
            _ = try await entries.markSaved(
                id: entryID,
                audio: audio,
                durationSeconds: playable.durationSeconds,
                now: clock.now
            )
        } catch {
            return .unrecoverable(entryID: entryID, failure: .finalizationFailed)
        }

        return isKnown
            ? .recovered(entryID: entryID, audio: audio)
            : .adopted(entryID: entryID, audio: audio)
    }

    /// Rebuilds the row for audio whose row never landed, dated by the file.
    private func createRecoveredEntry(entryID: EntryID) async throws {
        let now = clock.now
        let file = [store.partialURL(for: entryID), store.finalURL(for: entryID)]
            .first { FileManager.default.fileExists(atPath: $0.path) }
        let created = file
            .flatMap { try? FileManager.default.attributesOfItem(atPath: $0.path) }
            .flatMap { $0[.creationDate] as? Date }
        _ = try await entries.createRecoveredRecording(
            id: entryID,
            capturedAt: created ?? now,
            timeZoneIdentifier: timeZone.identifier,
            now: now
        )
    }

    /// Promotes the entry's audio only when it decodes.
    ///
    /// A recorder killed mid-write can leave bytes that no player can open. Those
    /// stay at the partial path: the final path only ever holds audio the user
    /// can play, so "saved" never describes a file that is not.
    private func promotePlayableAudio(for entryID: EntryID) async -> PlayableAudio? {
        let partial = store.partialURL(for: entryID)
        let candidate = FileManager.default.fileExists(atPath: partial.path)
            ? partial
            : store.finalURL(for: entryID)
        guard let duration = try? await recorder.duration(of: candidate),
              duration.isFinite, duration > 0,
              let audio = try? store.finalize(entryID: entryID) else {
            return nil
        }
        return PlayableAudio(audio: audio, durationSeconds: duration)
    }

    /// Persists the reason, keeps any audio, and produces the error to throw.
    private func recordFailure(
        entryID: EntryID,
        failure: CaptureFailure,
        detail: String?,
        retainedAudio: StoredAudio? = nil
    ) async -> AudioCaptureError {
        // A partial that AVFoundation never closed may still be playable, and
        // it is the user's only copy. One that is not stays a partial.
        var retained = retainedAudio
        if retained == nil {
            retained = await promotePlayableAudio(for: entryID)?.audio
        }
        if retained == nil {
            try? store.discardEmptyPartial(for: entryID)
        }

        _ = try? await entries.markCaptureFailure(id: entryID, failure: failure, now: clock.now)
        status = .failed(entryID: entryID, failure: failure, retainedAudio: retained)
        return AudioCaptureError.failed(entryID: entryID, failure: failure, detail: detail)
    }
}

private struct PlayableAudio {
    let audio: StoredAudio
    let durationSeconds: Double
}
