import CaptureCore
import Foundation
import Testing

// Ported from an earlier app's coordinator tests. These drive the engine
// itself, with a context string standing in for what the host records.

@Suite("Audio capture engine")
struct AudioCaptureEngineTests {
    @Test("The entry and its directory exist before any audio is captured")
    func entryPrecedesAudio() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }

        let started = try await rig.engine.beginRecording(context: "evening")
        let persisted = try #require(await rig.records.record(id: started.id))

        #expect(!persisted.isSaved)
        #expect(persisted.context == "evening")
        #expect(persisted.timeZoneIdentifier == "America/Toronto")
        #expect(persisted.audio == nil)
        #expect(rig.sandbox.exists(rig.sandbox.store.directory(for: started.id)))
        #expect(await rig.engine.status.isRecording)
    }

    @Test("A finished recording is durable before anything downstream is told")
    func happyPath() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }

        let started = try await rig.engine.beginRecording(context: "evening")
        let saved = try await rig.engine.finishRecording()

        #expect(saved.id == started.id)
        #expect(saved.isSaved)
        #expect(saved.durationSeconds == 12.5)
        #expect(saved.audio?.byteCount == 16)
        #expect(saved.audio?.relativePath == "Entries/\(saved.id.description)/audio.m4a")
        #expect(saved.hasDurableAudio)
        #expect(saved.captureFailure == nil)

        let audioURL = rig.sandbox.store.finalURL(for: saved.id)
        #expect(rig.sandbox.exists(audioURL))
        #expect(!rig.sandbox.exists(rig.sandbox.store.partialURL(for: saved.id)))
        #expect(saved.audio?.sha256 == (try AudioFileStore.sha256(of: audioURL)))
        #expect(await rig.recorder.startedConfiguration == .standard)
    }

    @Test("48 kHz mono AAC at 128 kbps is what the recorder is asked for")
    func recordingConfiguration() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }

        _ = try await rig.engine.beginRecording(context: "evening")
        let configuration = try #require(await rig.recorder.startedConfiguration)
        #expect(configuration.sampleRate == 48_000)
        #expect(configuration.channelCount == 1)
        #expect(configuration.bitRate == 128_000)
    }

    @Test("A recording that produced no audio never becomes a saved entry")
    func emptyRecordingIsNeverSaved() async throws {
        let rig = CaptureRig(script: .init(payload: Data()))
        defer { rig.sandbox.destroy() }

        let started = try await rig.engine.beginRecording(context: "evening")
        await #expect(throws: AudioCaptureError.self) {
            _ = try await rig.engine.finishRecording()
        }

        let persisted = try #require(await rig.records.record(id: started.id))
        #expect(!persisted.isSaved)
        #expect(persisted.captureFailure == .audioMissing)
        #expect(!persisted.hasDurableAudio)
        #expect(!rig.sandbox.exists(rig.sandbox.store.finalURL(for: started.id)))
    }

    @Test("A denied microphone is reported precisely and leaves no audio behind")
    func microphoneDenied() async throws {
        let rig = CaptureRig(script: .init(startError: .microphoneDenied))
        defer { rig.sandbox.destroy() }

        await #expect(throws: AudioCaptureError.self) {
            _ = try await rig.engine.beginRecording(context: "evening")
        }

        let stranded = try await rig.records.unfinishedRecordings()
        #expect(stranded.count == 1)
        #expect(stranded.first?.captureFailure == .microphoneDenied)
        if case .failed(_, let failure, let retained) = await rig.engine.status {
            #expect(failure == .microphoneDenied)
            #expect(retained == nil)
        } else {
            Issue.record("status should report the failure")
        }
    }

    @Test("Audio captured before an interruption is kept and finished on the spot")
    func interruptionRetainsAudio() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }

        let started = try await rig.engine.beginRecording(context: "evening")
        let interruption = try #require(
            await rig.engine.endInterruptedRecording(failure: .interrupted)
        )

        #expect(interruption.entryID == started.id)
        #expect(interruption.failure == .interrupted)
        #expect(interruption.retainedAudio?.byteCount == 16)
        #expect(rig.sandbox.exists(rig.sandbox.store.finalURL(for: started.id)))
        #expect(await rig.recorder.cancelCount == 1)

        // Durable now rather than after the next launch.
        let persisted = try #require(await rig.records.record(id: started.id))
        #expect(persisted.isSaved)
        #expect(persisted.captureFailure == nil)
        #expect(persisted.hasDurableAudio)
        #expect(try await rig.records.unfinishedRecordings().isEmpty)

        // Idle, so the next tap starts a recording instead of reporting that
        // there is nothing to stop.
        #expect(await rig.engine.status == .idle)
    }

    @Test("A stopped recording that does not decode is kept and never saved")
    func stopWithUndecodableAudio() async throws {
        let rig = CaptureRig(script: .init(payload: StubAudioRecorder.unplayablePrefix + Data("-torn".utf8)))
        defer { rig.sandbox.destroy() }

        let started = try await rig.engine.beginRecording(context: "evening")
        await #expect(throws: AudioCaptureError.self) { try await rig.engine.finishRecording() }

        #expect(rig.sandbox.exists(rig.sandbox.store.finalURL(for: started.id)))
        let persisted = try #require(await rig.records.record(id: started.id))
        #expect(!persisted.isSaved)
        #expect(!persisted.hasDurableAudio)
        #expect(persisted.captureFailure == .finalizationFailed)
        #expect(persisted.captureFailure?.isRetryable == true)
    }

    @Test("A Stop the phone locked before checking leaves the decision to unlock")
    func stopCheckedAfterUnlock() async throws {
        let protectedData = LockableProtectedData()
        let rig = CaptureRig(
            script: .init(payload: StubAudioRecorder.unplayablePrefix + Data("-locked".utf8)),
            protectedData: protectedData
        )
        defer { rig.sandbox.destroy() }

        let started = try await rig.engine.beginRecording(context: "evening")
        protectedData.set(locked: true)
        await #expect(throws: AudioCaptureError.self) { try await rig.engine.finishRecording() }

        let waiting = try #require(await rig.records.record(id: started.id))
        #expect(waiting.captureFailure == .awaitingUnlock)
        #expect(rig.sandbox.exists(rig.sandbox.store.finalURL(for: started.id)))

        protectedData.set(locked: false)
        let outcomes = try await rig.engine.finishAfterUnlock()
        #expect(outcomes == [.unrecoverable(entryID: started.id, failure: .audioMissing)])
        #expect(rig.sandbox.exists(rig.sandbox.store.finalURL(for: started.id)))
    }

    @Test("An interruption that leaves unplayable audio is not reported as saved")
    func interruptionWithUnplayableAudio() async throws {
        let rig = CaptureRig(script: .init(payload: StubAudioRecorder.unplayablePrefix + Data("-torn".utf8)))
        defer { rig.sandbox.destroy() }

        let started = try await rig.engine.beginRecording(context: "evening")
        let interruption = try #require(
            await rig.engine.endInterruptedRecording(failure: .interrupted)
        )

        #expect(interruption.failure == .audioMissing)
        #expect(interruption.retainedAudio == nil)
        #expect(rig.sandbox.exists(rig.sandbox.store.partialURL(for: started.id)))
        #expect(!rig.sandbox.exists(rig.sandbox.store.finalURL(for: started.id)))

        let persisted = try #require(await rig.records.record(id: started.id))
        #expect(!persisted.isSaved)
        #expect(persisted.captureFailure == .audioMissing)
        #expect(await rig.engine.status == .idle)
    }

    @Test("An interruption reported when nothing is recording is ignored")
    func interruptionWithoutRecording() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }

        let started = try await rig.engine.beginRecording(context: "evening")
        _ = try await rig.engine.finishRecording()

        #expect(await rig.engine.endInterruptedRecording(failure: .interrupted) == nil)
        #expect(await rig.records.record(id: started.id)?.isSaved == true)
    }

    @Test("Recording is refused before it starts when the volume is too full")
    func lowStorageRefusal() async throws {
        let sandbox = CaptureSandbox()
        defer { sandbox.destroy() }
        var store = sandbox.store
        store.minimumFreeBytes = .max
        let rig = CaptureRig(sandbox: sandbox, store: store)

        await #expect(throws: AudioCaptureError.self) {
            _ = try await rig.engine.beginRecording(context: "evening")
        }
        #expect(await rig.records.all.isEmpty)
    }

    @Test("Same-day recordings never collide")
    func sameDayRecordings() async throws {
        let rig = CaptureRig()
        defer { rig.sandbox.destroy() }

        _ = try await rig.engine.beginRecording(context: "evening")
        let first = try await rig.engine.finishRecording()
        _ = try await rig.engine.beginRecording(context: "evening")
        let second = try await rig.engine.finishRecording()

        #expect(first.id != second.id)
        #expect(first.capturedAt == second.capturedAt)
        #expect(first.audio?.relativePath != second.audio?.relativePath)
        #expect(rig.sandbox.exists(rig.sandbox.store.finalURL(for: first.id)))
        #expect(rig.sandbox.exists(rig.sandbox.store.finalURL(for: second.id)))
        #expect(await rig.records.all.count == 2)
    }
}
