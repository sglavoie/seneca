import CaptureCore
import Foundation

/// Synthetic bytes, never real AAC. A fresh recorder with the same fixture can
/// validate saved/partial files during a host's restart-recovery tests.
public struct SimulatedAudioFixture: Sendable {
    public let bytes: Data
    public let durationSeconds: Double
    public init(bytes: Data = Data(repeating: 0x21, count: 4096), durationSeconds: Double = 12) {
        precondition(!bytes.isEmpty && durationSeconds.isFinite && durationSeconds > 0)
        self.bytes = bytes
        self.durationSeconds = durationSeconds
    }
}

public actor SimulatedAudioRecorder: AudioRecording {
    private let store: AudioFileStore
    private let refusesToStart: Bool
    private let fixture: SimulatedAudioFixture
    private let meterStep: Double
    private var destination: URL?
    private var elapsed: Double = 0
    private var onInterruption: (@Sendable (CaptureFailure) -> Void)?
    public private(set) var startCount = 0

    public init(
        store: AudioFileStore, refusesToStart: Bool = false,
        fixture: SimulatedAudioFixture = SimulatedAudioFixture(), meterStep: Double = 1
    ) {
        precondition(meterStep.isFinite && meterStep >= 0)
        self.store = store
        self.refusesToStart = refusesToStart
        self.fixture = fixture
        self.meterStep = meterStep
    }

    public func setInterruptionHandler(_ handler: @escaping @Sendable (CaptureFailure) -> Void) {
        onInterruption = handler
    }

    /// Reports the interruption; the host must route it to its capture engine.
    public func interrupt(_ failure: CaptureFailure) { onInterruption?(failure) }

    public func startRecording(to destination: URL, configuration: RecordingConfiguration) throws {
        guard !refusesToStart else { throw RecordingError.microphoneDenied }
        guard self.destination == nil else { throw RecordingError.sessionUnavailable("Already recording") }
        let partial = AudioFileStore.partialURL(forFinalURL: destination)
        try FileManager.default.createDirectory(at: partial.deletingLastPathComponent(), withIntermediateDirectories: true)
        try fixture.bytes.write(to: partial, options: .atomic)
        self.destination = destination
        startCount += 1
        elapsed = 0
    }

    public func stopRecording() throws -> RecordedAudio {
        guard let destination else { throw RecordingError.noActiveRecording }
        let audio = try store.finalize(finalURL: destination)
        self.destination = nil
        return RecordedAudio(url: audio.url, durationSeconds: fixture.durationSeconds, byteCount: audio.byteCount, sha256: audio.sha256)
    }

    /// Retains partial bytes so the host can exercise recovery or deletion.
    public func cancelRecording() { destination = nil }

    public func duration(of url: URL) throws -> Double {
        guard try Data(contentsOf: url) == fixture.bytes else { throw RecordingError.emptyRecording }
        return fixture.durationSeconds
    }

    public func meter() async -> RecordingMeter? {
        guard destination != nil else { return nil }
        elapsed += meterStep
        return RecordingMeter(durationSeconds: elapsed, level: 0.5)
    }
}
