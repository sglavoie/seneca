import Foundation

public struct TranscriptionRequest: Hashable, Sendable {
    public var audioURL: URL
    public var localeIdentifier: String

    public init(audioURL: URL, localeIdentifier: String) {
        self.audioURL = audioURL
        self.localeIdentifier = localeIdentifier
    }
}

public struct TranscriptionOutcome: Hashable, Sendable {
    public var text: String
    public var localeIdentifier: String
    public var provider: TranscriptProvider

    public init(text: String, localeIdentifier: String, provider: TranscriptProvider) {
        // Normalize silence to an empty transcript while preserving the text
        // of every nonempty outcome.
        self.text = text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "" : text
        self.localeIdentifier = localeIdentifier
        self.provider = provider
    }
}

public enum TranscriptionError: Error, Hashable, Sendable {
    case noProviderAvailable
    case localeUnsupported(String)
    case assetsNotInstalled(String)
    case cancelled
    case failed(String)
}

/// Transcribes on device. Implementations prefer `SpeechTranscriber` and fall
/// back to `DictationTranscriber`; both are runtime-probed, never assumed from
/// the SDK version.
public protocol Transcribing: Sendable {
    func transcribe(_ request: TranscriptionRequest) async throws -> TranscriptionOutcome
}

/// Where a locale's on-device speech model stands.
public enum SpeechModelState: Hashable, Sendable {
    /// No on-device model serves this locale.
    case unsupported
    /// The model can be downloaded but is not on the device.
    case notDownloaded
    /// A download is under way; the fraction is nil until iOS reports one.
    case downloading(fraction: Double?)
    case ready
}

/// Fetches the on-device model a locale needs. Transcription does not wait for
/// a missing model; it fails as retryable, and the host prepares the model and
/// resumes the entry once this reports it ready.
public protocol SpeechModelPreparing: Sendable {
    /// Downloads the model if needed, calling `onProgress` with the completed
    /// fraction while it downloads. True when it is installed; false when it is
    /// unavailable or did not arrive in time, so the host may ask again.
    func prepare(localeIdentifier: String, onProgress: @escaping @Sendable (Double) -> Void) async -> Bool

    /// The model's current state, without downloading anything.
    func state(localeIdentifier: String) async -> SpeechModelState

    /// Keeps the models of these locales from being removed when storage runs
    /// low, and lets go of any other this app was keeping.
    func keepReserved(localeIdentifiers: Set<String>) async
}

extension SpeechModelPreparing {
    public func prepare(localeIdentifier: String) async -> Bool {
        await prepare(localeIdentifier: localeIdentifier, onProgress: { _ in })
    }
}

/// Prepares nothing and never reports a model ready. For hosts and tests
/// without on-device speech.
public struct NoSpeechModelPreparation: SpeechModelPreparing {
    public init() {}
    public func prepare(localeIdentifier: String, onProgress: @escaping @Sendable (Double) -> Void) async -> Bool {
        false
    }

    public func state(localeIdentifier: String) async -> SpeechModelState { .unsupported }
    public func keepReserved(localeIdentifiers: Set<String>) async {}
}
