import Foundation

public struct RecordingConfiguration: Hashable, Sendable {
    public var sampleRate: Double
    public var channelCount: Int
    public var bitRate: Int

    public init(sampleRate: Double, channelCount: Int, bitRate: Int) {
        self.sampleRate = sampleRate
        self.channelCount = channelCount
        self.bitRate = bitRate
    }

    public static let standard = RecordingConfiguration(
        sampleRate: 48_000,
        channelCount: 1,
        bitRate: 128_000
    )
}

public struct RecordedAudio: Hashable, Sendable {
    public var url: URL
    public var durationSeconds: Double
    public var byteCount: Int
    public var sha256: String

    public init(url: URL, durationSeconds: Double, byteCount: Int, sha256: String) {
        self.url = url
        self.durationSeconds = durationSeconds
        self.byteCount = byteCount
        self.sha256 = sha256
    }
}

/// A live reading while recording: how long it has run and how loud it is.
/// `level` is normalized to 0...1 from the recorder's dBFS average power.
public struct RecordingMeter: Hashable, Sendable {
    public static let silenceFloorDecibels: Float = -60

    public var durationSeconds: Double
    public var level: Float

    public init(durationSeconds: Double, level: Float) {
        self.durationSeconds = durationSeconds
        self.level = level
    }

    public init(durationSeconds: Double, averagePowerDecibels: Float) {
        self.durationSeconds = durationSeconds
        let clamped = max(averagePowerDecibels, Self.silenceFloorDecibels)
        self.level = (clamped - Self.silenceFloorDecibels) / -Self.silenceFloorDecibels
    }
}

public enum RecordingError: Error, Hashable, Sendable {
    case microphoneDenied
    case sessionUnavailable(String)
    case interrupted
    case noActiveRecording
    case emptyRecording
}

/// Captures audio to a temporary file and finalizes with an atomic rename, so a
/// crash mid-recording can never leave a torn file at the destination path.
///
/// `destination` is the final `audio.m4a` path; the implementation records to
/// its `AudioFileStore` partial sibling and promotes it on stop. Deciding which
/// interrupted recordings to keep is not the recorder's job — that needs the
/// database — so recovery only asks it for the one thing that needs an audio
/// framework: whether a file on disk plays, and for how long.
public protocol AudioRecording: Sendable {
    func startRecording(
        to destination: URL,
        configuration: RecordingConfiguration
    ) async throws

    func stopRecording() async throws -> RecordedAudio

    func cancelRecording() async

    /// The decoded duration of the audio at `url`. Throws when the file cannot
    /// be opened as audio, which is how recovery tells a playable partial from
    /// bytes a killed recorder left behind.
    func duration(of url: URL) async throws -> Double

    /// Live progress, or `nil` when nothing is recording.
    func meter() async -> RecordingMeter?
}

extension AudioRecording {
    public func meter() async -> RecordingMeter? { nil }
}
