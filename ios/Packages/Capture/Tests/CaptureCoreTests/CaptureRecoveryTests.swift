import CaptureCore
import Foundation
import Testing

// Ported from an earlier app's recovery and unlock tests, against the engine
// and in-memory records.

@Suite("Capture recovery")
struct CaptureRecoveryTests {
    @Test("A recording interrupted by a crash is finalized on the next launch")
    func interruptedRecordingIsRecovered() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }
        let entryID = EntryID(string: "00000000-0000-4000-8000-0000000000f1")!

        // The row landed and bytes reached the partial; then the process died.
        try await rig.strand(entryID, bytes: Data("interrupted-audio".utf8))

        let outcomes = try await rig.engine.recoverOnLaunch()
        #expect(outcomes.compactMap(\.recoveredEntryID) == [entryID])

        let entry = try #require(await rig.records.record(id: entryID))
        #expect(entry.isSaved)
        #expect(entry.hasDurableAudio)
        #expect(entry.durationSeconds == 12.5)
        #expect(rig.sandbox.exists(rig.sandbox.store.finalURL(for: entryID)))
        #expect(!rig.sandbox.exists(rig.sandbox.store.partialURL(for: entryID)))
    }

    @Test("An entry whose audio is gone is flagged, not silently saved")
    func missingAudioIsUnrecoverable() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }
        let entryID = EntryID(string: "00000000-0000-4000-8000-0000000000f2")!

        _ = try await rig.records.createRecording(
            id: entryID, context: "evening", capturedAt: CaptureRig.now,
            timeZoneIdentifier: "America/Toronto", now: CaptureRig.now
        )

        let outcomes = try await rig.engine.recoverOnLaunch()
        #expect(outcomes.compactMap(\.unrecoverableEntryID) == [entryID])

        let entry = try #require(await rig.records.record(id: entryID))
        #expect(!entry.isSaved)
        #expect(entry.captureFailure == .audioMissing)
        #expect(!entry.hasDurableAudio)

        // Not retried forever: the bytes are gone and only the user can clear it.
        #expect(try await rig.engine.recoverOnLaunch().isEmpty)
    }

    @Test("Audio whose row never landed is adopted rather than orphaned")
    func orphanAudioIsAdopted() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }
        let entryID = EntryID(string: "00000000-0000-4000-8000-0000000000f3")!

        try rig.sandbox.writePartial(Data("orphaned-audio".utf8), for: entryID)

        let outcomes = try await rig.engine.recoverOnLaunch()
        #expect(outcomes.compactMap(\.adoptedEntryID) == [entryID])

        let entry = try #require(await rig.records.record(id: entryID))
        #expect(entry.isSaved)
        #expect(entry.context == nil)
        #expect(entry.audio?.relativePath == rig.sandbox.store.relativePath(for: entryID))
        #expect(rig.sandbox.exists(rig.sandbox.store.finalURL(for: entryID)))
    }

    @Test("Audio that does not play is kept but never reported as saved")
    func unplayablePartialIsFlagged() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }
        let entryID = EntryID(string: "00000000-0000-4000-8000-0000000000f6")!

        try await rig.strand(entryID, bytes: Data("unplayable-torn-audio".utf8))

        let outcomes = try await rig.engine.recoverOnLaunch()
        #expect(outcomes.compactMap(\.unrecoverableEntryID) == [entryID])

        let entry = try #require(await rig.records.record(id: entryID))
        #expect(!entry.isSaved)
        #expect(entry.captureFailure == .audioMissing)
        #expect(!entry.hasDurableAudio)

        // The bytes stay where they were; the final path only holds playable audio.
        #expect(rig.sandbox.exists(rig.sandbox.store.partialURL(for: entryID)))
        #expect(!rig.sandbox.exists(rig.sandbox.store.finalURL(for: entryID)))

        #expect(try await rig.engine.recoverOnLaunch().isEmpty)
    }

    @Test("Unplayable audio with no row gets a flagged row, not a saved one")
    func unplayableOrphanIsFlagged() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }
        let entryID = EntryID(string: "00000000-0000-4000-8000-0000000000f7")!

        try rig.sandbox.writePartial(Data("unplayable-orphan".utf8), for: entryID)

        let outcomes = try await rig.engine.recoverOnLaunch()
        #expect(outcomes.compactMap(\.unrecoverableEntryID) == [entryID])
        #expect(outcomes.compactMap(\.adoptedEntryID).isEmpty)

        // A row exists so the user can see and delete what the bytes occupy.
        let entry = try #require(await rig.records.record(id: entryID))
        #expect(!entry.isSaved)
        #expect(entry.captureFailure == .audioMissing)
        #expect(rig.sandbox.exists(rig.sandbox.store.partialURL(for: entryID)))

        #expect(try await rig.engine.recoverOnLaunch().isEmpty)
        #expect(await rig.records.all.count == 1)
    }

    @Test("Recovery is idempotent across repeated launches")
    func recoveryIsIdempotent() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }
        let entryID = EntryID(string: "00000000-0000-4000-8000-0000000000f4")!

        try await rig.strand(entryID, bytes: Data("interrupted-audio".utf8))

        #expect(try await rig.engine.recoverOnLaunch().count == 1)
        #expect(try await rig.engine.recoverOnLaunch().isEmpty)
        #expect(await rig.records.all.count == 1)
    }

    @Test("A recovered recording still resolves through the persisted relative path")
    func recoveredPathResolvesAfterRelaunch() async throws {
        let sandbox = CaptureSandbox()
        defer { sandbox.destroy() }
        let records = InMemoryCaptureRecords()
        let entryID = EntryID(string: "00000000-0000-4000-8000-0000000000f5")!

        do {
            let rig = CaptureRig(sandbox: sandbox, records: records)
            try await rig.strand(entryID, bytes: Data("interrupted-audio".utf8))
            _ = try await rig.engine.recoverOnLaunch()
        }

        // A new store over the same root is what a relaunch, in a container
        // whose absolute path may have moved, sees.
        let relaunched = AudioFileStore(applicationSupportRoot: sandbox.root)
        let entry = try #require(await records.record(id: entryID))
        let path = try #require(entry.audio?.relativePath)
        let resolved = relaunched.resolve(relativePath: path)
        #expect(sandbox.exists(resolved))
        #expect(try AudioFileStore.sha256(of: resolved) == entry.audio?.sha256)
    }
}

@Suite("Capture across lock")
struct CaptureUnlockTests {
    @Test("An interruption while locked waits for unlock instead of reporting audio lost")
    func interruptionWhileLockedFinishesAfterUnlock() async throws {
        let protectedData = LockableProtectedData()
        let rig = CaptureRig(protectedData: protectedData)
        defer { rig.sandbox.destroy() }
        let started = try await rig.engine.beginRecording(context: "evening")

        protectedData.set(locked: true)
        let interruption = try #require(await rig.engine.endInterruptedRecording(failure: .interrupted))

        #expect(interruption.failure == .awaitingUnlock)
        #expect(interruption.retainedAudio == nil)
        let waiting = try #require(await rig.records.record(id: started.id))
        #expect(!waiting.isSaved)
        #expect(waiting.captureFailure == .awaitingUnlock)
        #expect(rig.sandbox.exists(rig.sandbox.store.partialURL(for: started.id)))

        protectedData.set(locked: false)
        let outcomes = try await rig.engine.finishAfterUnlock()

        #expect(outcomes.compactMap(\.recoveredEntryID) == [started.id])
        let saved = try #require(await rig.records.record(id: started.id))
        #expect(saved.isSaved)
        #expect(saved.captureFailure == nil)
        #expect(rig.sandbox.exists(rig.sandbox.store.finalURL(for: started.id)))
    }

    @Test("A launch while locked leaves every entry and file as it found them")
    func launchWhileLockedTouchesNothing() async throws {
        let protectedData = LockableProtectedData(locked: true)
        let rig = CaptureRig(protectedData: protectedData)
        defer { rig.sandbox.destroy() }
        let stranded = EntryID(string: "00000000-0000-4000-8000-0000000000a1")!
        let orphan = EntryID(string: "00000000-0000-4000-8000-0000000000a2")!
        try await rig.strand(stranded, bytes: Data("unplayable-until-unlock".utf8))
        try rig.sandbox.writePartial(Data("orphan-audio".utf8), for: orphan)

        #expect(try await rig.engine.recoverOnLaunch().isEmpty)

        let entry = try #require(await rig.records.record(id: stranded))
        #expect(!entry.isSaved)
        #expect(entry.captureFailure == nil)
        #expect(await rig.records.record(id: orphan) == nil)
        #expect(rig.sandbox.exists(rig.sandbox.store.partialURL(for: stranded)))
        #expect(rig.sandbox.exists(rig.sandbox.store.partialURL(for: orphan)))

        protectedData.set(locked: false)
        let outcomes = try await rig.engine.finishAfterUnlock()
        #expect(outcomes.compactMap(\.unrecoverableEntryID) == [stranded])
        #expect(outcomes.compactMap(\.adoptedEntryID) == [orphan])
    }

    @Test("A read that fails because the phone locked mid-pass is not a loss")
    func lockDuringReadDefers() async throws {
        let rig = CaptureRig(protectedData: LockableProtectedData(availableChecks: 1))
        defer { rig.sandbox.destroy() }
        let entryID = EntryID(string: "00000000-0000-4000-8000-0000000000a3")!
        try await rig.strand(entryID, bytes: StubAudioRecorder.unplayablePrefix)

        let outcomes = try await rig.engine.recoverOnLaunch()

        #expect(outcomes == [.awaitingUnlock(entryID: entryID)])
        let entry = try #require(await rig.records.record(id: entryID))
        #expect(entry.captureFailure == .awaitingUnlock)
        #expect(rig.sandbox.exists(rig.sandbox.store.partialURL(for: entryID)))
    }

    @Test("Finishing after unlock never touches the recording in progress")
    func finishAfterUnlockSkipsActiveRecording() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }
        let waiting = EntryID(string: "00000000-0000-4000-8000-0000000000a4")!
        try await rig.strand(waiting, bytes: Data("locked-interruption".utf8))
        try await rig.records.markCaptureFailure(id: waiting, failure: .awaitingUnlock, now: CaptureRig.now)
        let active = try await rig.engine.beginRecording(context: "evening")

        let outcomes = try await rig.engine.finishAfterUnlock()

        #expect(outcomes.compactMap(\.recoveredEntryID) == [waiting])
        #expect(await rig.engine.status.isRecording)
        let recording = try #require(await rig.records.record(id: active.id))
        #expect(!recording.isSaved)
        #expect(rig.sandbox.exists(rig.sandbox.store.partialURL(for: active.id)))
        #expect(!rig.sandbox.exists(rig.sandbox.store.finalURL(for: active.id)))
    }

    @Test("Without a deferred launch, unlock takes only entries waiting for it")
    func unlockTakesOnlyWaitingEntries() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }
        let waiting = EntryID(string: "00000000-0000-4000-8000-0000000000a6")!
        let starting = EntryID(string: "00000000-0000-4000-8000-0000000000a7")!
        try await rig.strand(waiting, bytes: Data("locked-interruption".utf8))
        try await rig.records.markCaptureFailure(id: waiting, failure: .awaitingUnlock, now: CaptureRig.now)
        // A recording whose row exists but whose recorder has not started yet.
        try await rig.strand(starting, bytes: Data())

        let outcomes = try await rig.engine.finishAfterUnlock()

        #expect(outcomes.compactMap(\.recoveredEntryID) == [waiting])
        let untouched = try #require(await rig.records.record(id: starting))
        #expect(untouched.captureFailure == nil)
        #expect(rig.sandbox.exists(rig.sandbox.store.partialURL(for: starting)))
    }

    @Test("Audio already known to be lost is not retried on unlock")
    func audioMissingIsNotRetried() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }
        let entryID = EntryID(string: "00000000-0000-4000-8000-0000000000a5")!
        try await rig.strand(entryID, bytes: StubAudioRecorder.unplayablePrefix)
        try await rig.records.markCaptureFailure(id: entryID, failure: .audioMissing, now: CaptureRig.now)

        #expect(try await rig.engine.finishAfterUnlock().isEmpty)
        let entry = try #require(await rig.records.record(id: entryID))
        #expect(entry.captureFailure == .audioMissing)
    }
}
