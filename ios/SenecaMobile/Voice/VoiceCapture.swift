import CaptureCore
import Foundation
import Observation

/// Speaking a reflection: recording a take into the day's audio directory,
/// transcribing it on the iPhone, and keeping every take that has no
/// transcript yet listed for another try. Nothing here writes inside the
/// journal; the takes and their sidecars are device-local.
@MainActor
@Observable
final class VoiceCapture {
    enum Phase: Equatable {
        case idle
        case starting
        case recording
        case finishing
        case transcribing
        /// The take is kept; its language's speech model is not on the
        /// iPhone yet. Transcription resumes once it is.
        case waitingForModel
        case transcribed
        /// The microphone is refused, in the prompt or in Settings.
        case denied
        case failed(String)
    }

    private(set) var phase: Phase = .idle
    /// Seconds since the take began.
    private(set) var elapsed: Double = 0
    /// The input level, 0...1.
    private(set) var level: Float = 0
    /// The take the record screen is about.
    private(set) var take: SpokenTake?
    /// Every take on this iPhone, newest first.
    private(set) var takes: [SpokenTake] = []
    /// Takes being transcribed now.
    private(set) var transcribing: Set<String> = []
    private(set) var modelState: SpeechModelState?
    /// Preparation's last reported fraction, while it runs.
    private(set) var modelProgress: Double?
    private(set) var preparingModel = false
    private(set) var language: SpeechLanguage
    private(set) var retention: AudioRetention

    let store: SpokenTakeStore
    private let devices: VoiceDevices
    private let preferences: VoicePreferencesStore
    private let clock: @Sendable () -> Date
    private var active: (date: String, id: String, startedAt: Date)?
    private var meterTask: Task<Void, Never>?
    private var modelTask: Task<Void, Never>?
    private var observing = false

    init(
        store: SpokenTakeStore,
        devices: VoiceDevices,
        preferences: VoicePreferencesStore,
        clock: @escaping @Sendable () -> Date = { Date() }
    ) {
        self.store = store
        self.devices = devices
        self.preferences = preferences
        self.clock = clock
        let stored = preferences.read()
        language = stored?.language ?? .preferred()
        retention = stored?.retention ?? .keep
        takes = store.all()
    }

    /// Takes with no transcript yet: failed, abandoned, waiting for the
    /// model, or finalized from a partial at launch.
    var untranscribed: [SpokenTake] {
        takes.filter { $0.state != .transcribed }
    }

    /// Takes still to be done with on Today: not transcribed yet, or
    /// transcribed with something heard and no answers accepted from them.
    var pending: [SpokenTake] {
        takes.filter { take in
            take.state != .transcribed
                || (take.transcript?.isEmpty == false && take.acceptedAt == nil && !take.answersSaved)
        }
    }

    var isRecording: Bool { phase == .recording || phase == .starting || phase == .finishing }

    func setLanguage(_ language: SpeechLanguage) {
        guard language != self.language else { return }
        self.language = language
        try? preferences.write(VoicePreferences(language: language, retention: retention))
        modelState = nil
        Task { await refreshModelState() }
    }

    /// Chooses what is kept of a take once its answers are saved. Choosing
    /// to delete applies at once to takes whose answers are already saved.
    func setRetention(_ retention: AudioRetention) {
        guard retention != self.retention else { return }
        self.retention = retention
        try? preferences.write(VoicePreferences(language: language, retention: retention))
        applyRetention()
    }

    /// Takes whose answers are saved, which deleting after saving removes.
    var saved: [SpokenTake] {
        takes.filter(\.answersSaved)
    }

    // MARK: Recording

    /// Starts a take for `date`. The first use asks for the microphone; a
    /// refusal is `.denied` and nothing is written.
    func start(on date: String) async {
        guard !isRecording else { return }
        await observeInterruptions()
        phase = .starting
        elapsed = 0
        level = 0
        take = nil
        let id = UUID().uuidString.lowercased()
        do {
            try store.prepareDay(date)
        } catch {
            phase = .failed("The recording could not be started: \(error.localizedDescription)")
            return
        }
        // Asked once, ahead of the first stop, so the model is usually on
        // the iPhone by the time it is needed. Never waited for here.
        prepareModel()
        do {
            try await devices.recorder.startRecording(to: store.audioURL(date: date, id: id), configuration: .standard)
        } catch RecordingError.microphoneDenied {
            removeEmptyDay(date)
            phase = .denied
            return
        } catch {
            removeEmptyDay(date)
            phase = .failed("The recording could not be started: \(Self.describe(error))")
            return
        }
        let startedAt = clock()
        active = (date, id, startedAt)
        phase = .recording
        meterTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self, self.phase == .recording, let active = self.active else { return }
                if let meter = await self.devices.recorder.meter() {
                    self.level = meter.level
                }
                self.elapsed = max(0, self.clock().timeIntervalSince(active.startedAt))
                try? await Task.sleep(for: .milliseconds(100))
            }
        }
    }

    /// Stops the take, keeps it, and transcribes it.
    func stop() async {
        guard phase == .recording, let active else { return }
        phase = .finishing
        meterTask?.cancel()
        meterTask = nil
        self.active = nil
        let audio: RecordedAudio
        do {
            audio = try await devices.recorder.stopRecording()
        } catch {
            // What was captured stays at the partial path; keep it if it plays.
            if let kept = await adopt(date: active.date, id: active.id, recovered: false) {
                take = kept
                reload()
                await transcribe(kept)
            } else {
                phase = .failed("The recording could not be finished: \(Self.describe(error))")
            }
            return
        }
        var take = SpokenTake(
            id: active.id, date: active.date, createdAt: active.startedAt,
            durationSeconds: audio.durationSeconds, byteCount: audio.byteCount, audioSha256: audio.sha256,
            locale: language.rawValue, state: .untranscribed
        )
        take.stoppedAt = clock()
        do {
            try store.write(take)
        } catch {
            phase = .failed("The recording is kept, but its details could not be written: \(error.localizedDescription)")
            return
        }
        self.take = take
        reload()
        await transcribe(take)
    }

    /// A call, a lost route, or the media services ending the take: what was
    /// captured is kept and transcribed, as a stop would. While the iPhone is
    /// locked the file cannot be read, so it is finished after unlock.
    func interrupted(_ failure: CaptureFailure) async {
        guard phase == .recording, let active else { return }
        meterTask?.cancel()
        meterTask = nil
        self.active = nil
        phase = .finishing
        await devices.recorder.cancelRecording()
        guard await devices.protectedData.isAvailable else {
            phase = .failed("The recording stopped while the iPhone was locked. It is kept and finished once the iPhone is unlocked.")
            return
        }
        guard let kept = await adopt(date: active.date, id: active.id, recovered: false) else {
            phase = .failed("The recording was interrupted before anything could be kept.")
            return
        }
        take = kept
        reload()
        await transcribe(kept)
    }

    /// Finishes what a stopped app or a locked iPhone left: every partial
    /// that plays is renamed to its final name and given a sidecar, and is
    /// offered for transcription. A partial that does not play is left where
    /// it is; an empty one is removed. Nothing is decided while locked.
    @discardableResult
    func recover() async -> [SpokenTake] {
        guard !isRecording, await devices.protectedData.isAvailable else { return [] }
        // A restore stopped between its renames is finished or undone first,
        // so its audio is never left hidden under a temporary name.
        store.settleRestores()
        var recovered: [SpokenTake] = []
        for (date, id) in store.unclaimed() {
            if let take = await adopt(date: date, id: id, recovered: true) {
                recovered.append(take)
            }
        }
        reload()
        return recovered
    }

    func cancel() async {
        guard phase == .recording || phase == .starting else { return }
        meterTask?.cancel()
        meterTask = nil
        await devices.recorder.cancelRecording()
        if let active, let kept = await adopt(date: active.date, id: active.id, recovered: false) {
            take = kept
        }
        active = nil
        reload()
        phase = .idle
    }

    /// Clears the record screen's state for the next take.
    func reset() {
        guard !isRecording else { return }
        phase = .idle
        take = nil
        elapsed = 0
        level = 0
    }

    // MARK: Transcription

    /// Transcribes a take in its language. A missing speech model is never
    /// waited for: the take is kept as waiting, the model is asked for, and
    /// transcription resumes once it arrives. A failure keeps the take for
    /// another try.
    func transcribe(_ take: SpokenTake) async {
        guard !transcribing.contains(take.id) else { return }
        transcribing.insert(take.id)
        defer { transcribing.remove(take.id) }
        // Whether the record screen shows this take: asked again after the
        // transcriber returns, since the screen may have been closed, reset,
        // or moved on to a new recording in the meantime.
        var current: Bool { take.id == self.take?.id }
        if current { phase = .transcribing }

        var updated = take
        do {
            let outcome = try await devices.transcriber.transcribe(
                TranscriptionRequest(audioURL: store.audioURL(date: take.date, id: take.id), localeIdentifier: take.locale)
            )
            updated.state = .transcribed
            updated.transcript = outcome.text
            updated.transcriber = outcome.provider.rawValue
            updated.transcribedAt = clock()
            updated.failure = nil
        } catch TranscriptionError.assetsNotInstalled {
            updated.state = .waitingForModel
            updated.failure = nil
        } catch {
            updated.state = .failed
            updated.failure = Self.describe(error)
        }
        do {
            try store.write(updated)
        } catch {
            if current { phase = .failed("The transcript could not be kept: \(error.localizedDescription)") }
            reload()
            return
        }
        if current { self.take = updated }
        reload()
        switch updated.state {
        case .transcribed:
            if current { phase = .transcribed }
        case .waitingForModel:
            if current { phase = .waitingForModel }
            prepareModel(locale: updated.locale)
        case .failed:
            if current { phase = .failed(updated.failure ?? "The recording could not be transcribed.") }
        case .untranscribed:
            break
        }
    }

    // MARK: Answers

    /// Files a transcript under the journal's questions.
    var splitter: any AnswerSplitting { devices.splitter }

    /// Notes on the take's sidecar that its answers went into the draft.
    func accepted(_ take: SpokenTake) {
        guard var current = try? store.read(date: take.date, id: take.id) else { return }
        current.acceptedAt = clock()
        try? store.write(current)
        if self.take?.id == current.id { self.take = current }
        reload()
    }

    /// Notes on the take's sidecar how long the model took to propose its
    /// answers, or why it could not.
    func proposed(_ take: SpokenTake, seconds: Double?, unavailable: String? = nil) {
        guard var current = try? store.read(date: take.date, id: take.id) else { return }
        if let seconds { current.proposalSeconds = seconds }
        current.proposalUnavailable = seconds == nil ? unavailable : nil
        try? store.write(current)
        if self.take?.id == current.id { self.take = current }
        reload()
    }

    /// The day's draft was thrown away at the author's word: answers
    /// accepted into it will never reach the journal, so no take is held as
    /// accepted any longer, and saving other writing for the day later never
    /// marks it saved or deletes it.
    func draftDiscarded(on date: String) {
        for var take in store.all() where take.date == date && take.acceptedAt != nil && !take.answersSaved {
            take.acceptedAt = nil
            try? store.write(take)
            if self.take?.id == take.id { self.take = take }
        }
        reload()
    }

    /// After the day's entry saved: every take whose answers were accepted
    /// into its draft is marked saved.
    func markAnswersSaved(on date: String) {
        for var take in store.all() where take.date == date && take.acceptedAt != nil && !take.answersSaved {
            take.answersSaved = true
            try? store.write(take)
            if self.take?.id == take.id { self.take = take }
        }
        reload()
        applyRetention()
    }

    // MARK: Retention and deletion

    /// A take can be deleted unless it is being recorded or transcribed.
    func canDelete(_ take: SpokenTake) -> Bool {
        !transcribing.contains(take.id) && active?.id != take.id
    }

    /// Deletes a take: its audio and its sidecar, nothing else.
    func delete(_ take: SpokenTake) throws {
        guard canDelete(take) else { return }
        try store.delete(date: take.date, id: take.id)
        if self.take?.id == take.id { self.take = nil }
        reload()
    }

    /// Deletes every take that can be. How many were deleted.
    @discardableResult
    func deleteAll(_ chosen: [SpokenTake]? = nil) -> Int {
        var deleted = 0
        for take in chosen ?? takes where canDelete(take) {
            if (try? store.delete(date: take.date, id: take.id)) != nil {
                deleted += 1
                if self.take?.id == take.id { self.take = nil }
            }
        }
        reload()
        return deleted
    }

    /// Deleting after saving: every take whose answers are saved goes.
    func applyRetention() {
        guard retention == .deleteAfterSaving else { return }
        let done = saved
        if !done.isEmpty { deleteAll(done) }
    }

    /// Puts the takes of a recovery kit back on this iPhone.
    func restore(from kit: URL) throws -> AudioRestore {
        defer { reload() }
        return try store.restoreKit(from: kit)
    }

    /// What diagnostics say about speaking: counts, sizes and states only.
    struct Summary: Encodable, Equatable {
        var takes: Int
        var bytes: Int
        var pendingTranscriptions: Int
        var speechModel: String
        var retention: String
    }

    var summary: Summary {
        Summary(
            takes: takes.count,
            bytes: takes.reduce(0) { $0 + store.bytes(date: $1.date, id: $1.id) },
            pendingTranscriptions: untranscribed.count,
            speechModel: preparingModel ? "preparing" : Self.code(modelState),
            retention: retention.rawValue
        )
    }

    static func code(_ state: SpeechModelState?) -> String {
        switch state {
        case .ready: return "ready"
        case .downloading: return "downloading"
        case .notDownloaded: return "not-downloaded"
        case .unsupported: return "unsupported"
        case nil: return "unknown"
        }
    }

    // MARK: Speech model

    /// Asks iOS for the speech model of the journal's language, in the
    /// background. Takes waiting for it are transcribed once it is ready.
    func prepareModel(locale: String? = nil) {
        guard modelTask == nil else { return }
        let locale = locale ?? language.rawValue
        preparingModel = true
        modelProgress = nil
        let models = devices.models
        // Held until preparation returns, which is bounded by the vendored
        // download limit.
        modelTask = Task {
            let ready = await models.prepare(localeIdentifier: locale) { fraction in
                Task { @MainActor in self.modelProgress = fraction }
            }
            await models.keepReserved(localeIdentifiers: [locale])
            self.preparingModel = false
            self.modelProgress = nil
            self.modelTask = nil
            await self.refreshModelState()
            guard ready else { return }
            for waiting in self.takes where waiting.state == .waitingForModel && waiting.locale == locale {
                await self.transcribe(waiting)
            }
        }
    }

    func refreshModelState() async {
        modelState = await devices.models.state(localeIdentifier: language.rawValue)
    }

    /// Waits for a preparation already asked for. For tests.
    func modelPreparationFinished() async {
        await modelTask?.value
    }

    // MARK: Helpers

    func reload() {
        takes = store.all()
    }

    private func observeInterruptions() async {
        guard !observing else { return }
        observing = true
        await devices.observeInterruptions { [weak self] failure in
            Task { @MainActor [weak self] in await self?.interrupted(failure) }
        }
    }

    /// Keeps the audio of `date`/`id` when it plays: renamed from its
    /// partial if it has one, and described by a new sidecar. Nil when there
    /// is nothing that plays.
    private func adopt(date: String, id: String, recovered: Bool) async -> SpokenTake? {
        if let existing = try? store.read(date: date, id: id),
           !FileManager.default.fileExists(atPath: store.partialURL(date: date, id: id).path) {
            return existing
        }
        let final = store.audioURL(date: date, id: id)
        let partial = store.partialURL(date: date, id: id)
        let candidate = FileManager.default.fileExists(atPath: partial.path) ? partial : final
        guard let duration = try? await devices.recorder.duration(of: candidate), duration.isFinite, duration > 0 else {
            if let size = try? store.files.byteCount(at: candidate), size == 0 {
                try? FileManager.default.removeItem(at: candidate)
                removeEmptyDay(date)
            }
            return nil
        }
        let created = (try? FileManager.default.attributesOfItem(atPath: candidate.path))?[.creationDate] as? Date
        guard let stored = try? store.files.finalize(finalURL: final) else { return nil }
        let take = SpokenTake(
            id: id, date: date, createdAt: created ?? clock(),
            durationSeconds: duration, byteCount: stored.byteCount, audioSha256: stored.sha256,
            locale: language.rawValue, state: .untranscribed, recovered: recovered
        )
        do {
            try store.write(take)
        } catch {
            return nil
        }
        return take
    }

    private func removeEmptyDay(_ date: String) {
        let directory = store.dayDirectory(date)
        if (try? FileManager.default.contentsOfDirectory(atPath: directory.path))?.isEmpty == true {
            try? FileManager.default.removeItem(at: directory)
        }
    }

    static func describe(_ error: Error) -> String {
        switch error {
        case RecordingError.microphoneDenied:
            return "Seneca is not allowed to use the microphone."
        case RecordingError.sessionUnavailable(let detail):
            return "The microphone is not available: \(detail)"
        case RecordingError.interrupted:
            return "The recording was interrupted."
        case RecordingError.emptyRecording, RecordingError.noActiveRecording:
            return "Nothing was recorded."
        case TranscriptionError.noProviderAvailable:
            return "On-device transcription is not available on this iPhone."
        case TranscriptionError.localeUnsupported(let locale):
            return "This iPhone cannot transcribe \(locale)."
        case TranscriptionError.assetsNotInstalled:
            return "Waiting for speech model."
        case TranscriptionError.cancelled:
            return "Transcription was stopped before it finished."
        case TranscriptionError.failed(let detail):
            return "Transcription failed: \(detail)"
        default:
            return error.localizedDescription
        }
    }
}
