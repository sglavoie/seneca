import Foundation

/// Picks the closest supported locale to a requested one.
///
/// A recording requested as `es-MX` must still transcribe on a device that only
/// installs `es-419` or `es`. Region is dropped before language, and never the
/// other way round: transcribing Mexican Spanish with a Spanish model is a
/// downgrade, transcribing it with a French one is wrong.
public enum LocaleMatcher {
    public static func bestMatch(for requested: String, among supported: [String]) -> String? {
        let target = normalize(requested)
        guard !target.isEmpty else { return nil }

        let candidates = supported.map { (normalized: normalize($0), original: $0) }

        if let exact = candidates.first(where: { $0.normalized == target }) {
            return exact.original
        }

        let language = languageCode(of: target)
        guard !language.isEmpty else { return nil }

        if let bare = candidates.first(where: { $0.normalized == language }) {
            return bare.original
        }
        return candidates.first { languageCode(of: $0.normalized) == language }?.original
    }

    private static func normalize(_ identifier: String) -> String {
        identifier.replacingOccurrences(of: "_", with: "-").lowercased()
    }

    private static func languageCode(of normalized: String) -> String {
        String(normalized.split(separator: "-").first ?? "")
    }
}

/// Tries transcribers in order and reports the first success.
///
/// A transcriber that cannot serve the request — no provider, wrong locale,
/// missing assets — hands off to the next one. Cancellation is not a hand-off:
/// it means the whole pipeline is being torn down, so it propagates instead of
/// waking up the fallback.
public struct FallbackTranscriber: Transcribing {
    private let transcribers: [any Transcribing]

    public init(_ transcribers: [any Transcribing]) {
        self.transcribers = transcribers
    }

    public func transcribe(_ request: TranscriptionRequest) async throws -> TranscriptionOutcome {
        var lastError: any Error = TranscriptionError.noProviderAvailable

        for transcriber in transcribers {
            do {
                return try await transcriber.transcribe(request)
            } catch TranscriptionError.cancelled {
                throw TranscriptionError.cancelled
            } catch {
                lastError = error
            }
        }

        throw lastError
    }
}

/// Refuses everything. Stands in for a provider the device does not have, and
/// lets a build with no transcription support at all still compile and run.
public struct UnavailableTranscriber: Transcribing {
    public init() {}

    public func transcribe(_ request: TranscriptionRequest) async throws -> TranscriptionOutcome {
        throw TranscriptionError.noProviderAvailable
    }
}
