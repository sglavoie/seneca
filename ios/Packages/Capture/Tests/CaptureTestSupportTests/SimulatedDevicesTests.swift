import CaptureCore
import CaptureTestSupport
import Foundation
import Testing

@Suite("Reusable simulated capture devices")
struct SimulatedDevicesTests {
    @Test("Denied capture writes nothing and can be configured independently per instance")
    func permissionDenied() async throws {
        let root = temporaryRoot()
        defer { try? FileManager.default.removeItem(at: root) }
        let store = AudioFileStore(applicationSupportRoot: root)
        let recorder = SimulatedAudioRecorder(store: store, refusesToStart: true)
        let url = store.finalURL(for: EntryID(UUID()))
        await #expect(throws: RecordingError.microphoneDenied) {
            try await recorder.startRecording(to: url, configuration: .standard)
        }
        #expect(await recorder.startCount == 0)
        #expect(!FileManager.default.fileExists(atPath: root.path))
    }

    @Test("Partial bytes survive cancellation and a fresh instance validates them for recovery")
    func restartRecoveryFixture() async throws {
        let root = temporaryRoot()
        defer { try? FileManager.default.removeItem(at: root) }
        let store = AudioFileStore(applicationSupportRoot: root)
        let fixture = SimulatedAudioFixture(bytes: Data("custom-capture".utf8), durationSeconds: 3)
        let recorder = SimulatedAudioRecorder(store: store, fixture: fixture, meterStep: 0.25)
        let url = store.finalURL(for: EntryID(UUID()))
        try await recorder.startRecording(to: url, configuration: .standard)
        #expect(await recorder.meter()?.durationSeconds == 0.25)
        #expect(await recorder.meter()?.durationSeconds == 0.5)
        await recorder.cancelRecording()
        #expect(await recorder.meter() == nil)
        let restarted = SimulatedAudioRecorder(store: store, fixture: fixture)
        let partial = AudioFileStore.partialURL(forFinalURL: url)
        #expect(try await restarted.duration(of: partial) == 3)
        let saved = try store.finalize(finalURL: url)
        #expect(try Data(contentsOf: saved.url) == fixture.bytes)
        #expect(try await restarted.duration(of: saved.url) == 3)
        try Data("corrupt".utf8).write(to: saved.url)
        await #expect(throws: RecordingError.emptyRecording) { try await restarted.duration(of: saved.url) }
    }

    @Test("Interruption reaches the host and the host can still finish retained audio")
    func interruption() async throws {
        let root = temporaryRoot()
        defer { try? FileManager.default.removeItem(at: root) }
        let store = AudioFileStore(applicationSupportRoot: root)
        let recorder = SimulatedAudioRecorder(store: store)
        let signal = AsyncStream<CaptureFailure>.makeStream()
        await recorder.setInterruptionHandler { signal.continuation.yield($0) }
        try await recorder.startRecording(to: store.finalURL(for: EntryID(UUID())), configuration: .standard)
        await recorder.interrupt(.interrupted)
        var events = signal.stream.makeAsyncIterator()
        #expect(await events.next() == .interrupted)
        let audio = try await recorder.stopRecording()
        #expect(audio.byteCount == 4096)
        #expect(await recorder.meter() == nil)
        signal.continuation.finish()
    }

    @Test("Scripted transcription fails first, then preserves the request language")
    func retry() async throws {
        let transcriber = SimulatedTranscriber(text: "12 boxes", failuresBeforeSuccess: 1)
        let request = TranscriptionRequest(audioURL: temporaryRoot(), localeIdentifier: "fr-CA")
        await #expect(throws: TranscriptionError.noProviderAvailable) { try await transcriber.transcribe(request) }
        let result = try await transcriber.transcribe(request)
        #expect(result.text == "12 boxes")
        #expect(result.localeIdentifier == "fr-CA")
        #expect(await transcriber.requests == [request, request])
    }

    @Test("A delayed provider observes cancellation after the test releases it")
    func delayedCancellation() async throws {
        let gate = AsyncTestGate()
        let transcriber = SimulatedTranscriber(text: "late result", gate: gate)
        let request = TranscriptionRequest(audioURL: temporaryRoot(), localeIdentifier: "en-US")
        let worker = Task { try await transcriber.transcribe(request) }
        await gate.arrived()
        worker.cancel()
        await gate.release()
        await #expect(throws: CancellationError.self) { try await worker.value }
        #expect(await transcriber.requests.count == 1)
    }

    private func temporaryRoot() -> URL {
        FileManager.default.temporaryDirectory.appendingPathComponent("capture-support-\(UUID())")
    }
}
