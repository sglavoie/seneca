import Foundation

public enum TranscriptProvider: String, Codable, Hashable, Sendable, CaseIterable {
    case speechTranscriber = "speech-transcriber"
    case dictationTranscriber = "dictation-transcriber"
    case whisperKit = "whisper-kit"
    case none
}
