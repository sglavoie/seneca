#if os(iOS)
import CaptureCore
import Speech

/// Discovers both on-device providers, regardless of whether assets are installed.
public struct SystemRecordingLocaleProvider: RecordingLocaleProviding {
    public init() {}
    public func supportedLocaleIdentifiers() async throws -> [String] {
        let speech = await SpeechTranscriber.supportedLocales.map(\.identifier)
        let dictation = await DictationTranscriber.supportedLocales.map(\.identifier)
        return TranscriptionLocaleCatalog.normalizedUnion([speech, dictation])
    }
}
#endif
