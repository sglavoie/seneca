import CaptureCore
import Foundation
import Testing

// Ported from an earlier app's tests: the locale matcher and the transcriber
// chain.

@Suite("Locale matching")
struct LocaleMatcherTests {
    @Test("An exactly supported locale is used as written")
    func exactMatch() {
        #expect(LocaleMatcher.bestMatch(for: "es-MX", among: ["en-US", "es-MX"]) == "es-MX")
    }

    @Test("Case and separator differences are not a mismatch")
    func normalization() {
        #expect(LocaleMatcher.bestMatch(for: "es_mx", among: ["es-MX"]) == "es-MX")
    }

    @Test("Region is dropped before language")
    func regionDroppedFirst() {
        #expect(LocaleMatcher.bestMatch(for: "es-MX", among: ["es", "es-ES"]) == "es")
        #expect(LocaleMatcher.bestMatch(for: "fr-CA", among: ["fr-FR"]) == "fr-FR")
    }

    @Test("A different language is never a fallback")
    func noCrossLanguageFallback() {
        #expect(LocaleMatcher.bestMatch(for: "es-MX", among: ["fr-CA", "en-US"]) == nil)
        #expect(LocaleMatcher.bestMatch(for: "fr-CA", among: []) == nil)
    }

    @Test("An empty request has no locale to match")
    func emptyRequest() {
        #expect(LocaleMatcher.bestMatch(for: "", among: ["en-US"]) == nil)
    }
}

@Suite("Transcriber chain")
struct TranscriberChainTests {
    private let request = TranscriptionRequest(
        audioURL: URL(fileURLWithPath: "/tmp/audio.m4a"),
        localeIdentifier: "es-MX"
    )

    @Test("The preferred transcriber answers when it can")
    func preferredWins() async throws {
        let chain = FallbackTranscriber([
            LocaleBoundTranscriber(supportedLocales: ["es-MX"], provider: .speechTranscriber),
            LocaleBoundTranscriber(supportedLocales: ["es-MX"], provider: .dictationTranscriber),
        ])

        let outcome = try await chain.transcribe(request)
        #expect(outcome.provider == .speechTranscriber)
    }

    @Test("A transcriber that cannot serve the locale hands off to the next")
    func fallsBackOnUnsupportedLocale() async throws {
        let chain = FallbackTranscriber([
            LocaleBoundTranscriber(supportedLocales: ["en-US"], provider: .speechTranscriber),
            LocaleBoundTranscriber(supportedLocales: ["es-419"], provider: .dictationTranscriber),
        ])

        let outcome = try await chain.transcribe(request)
        #expect(outcome.provider == .dictationTranscriber)
        #expect(outcome.localeIdentifier == "es-419")
    }

    @Test("A chain that can serve nobody reports the last refusal")
    func exhaustedChain() async throws {
        let chain = FallbackTranscriber([
            UnavailableTranscriber(),
            LocaleBoundTranscriber(supportedLocales: ["en-US"], provider: .dictationTranscriber),
        ])

        await #expect(throws: TranscriptionError.localeUnsupported("es-MX")) {
            _ = try await chain.transcribe(request)
        }
    }

    @Test("An empty chain refuses rather than claiming success")
    func emptyChain() async throws {
        await #expect(throws: TranscriptionError.noProviderAvailable) {
            _ = try await FallbackTranscriber([]).transcribe(request)
        }
    }

    @Test("Cancellation tears the chain down instead of waking the fallback")
    func cancellationPropagates() async throws {
        let fallback = CountingTranscriber()
        let chain = FallbackTranscriber([
            CountingTranscriber(error: .cancelled),
            fallback,
        ])

        await #expect(throws: TranscriptionError.cancelled) {
            _ = try await chain.transcribe(request)
        }
        #expect(await fallback.callCount == 0)
    }

    @Test("A silent recording is an empty transcript, and speech is kept as spoken")
    func silenceIsEmpty() {
        #expect(TranscriptionOutcome(text: " \n\t", localeIdentifier: "en-CA", provider: .speechTranscriber).text == "")
        #expect(TranscriptionOutcome(text: " kept ", localeIdentifier: "en-CA", provider: .speechTranscriber).text == " kept ")
    }
}

private struct LocaleBoundTranscriber: Transcribing {
    var supportedLocales: [String]
    var provider: TranscriptProvider
    var text = "supported"

    func transcribe(_ request: TranscriptionRequest) async throws -> TranscriptionOutcome {
        guard let matched = LocaleMatcher.bestMatch(for: request.localeIdentifier, among: supportedLocales) else {
            throw TranscriptionError.localeUnsupported(request.localeIdentifier)
        }
        return TranscriptionOutcome(text: text, localeIdentifier: matched, provider: provider)
    }
}

private actor CountingTranscriber: Transcribing {
    private let error: TranscriptionError?
    private(set) var callCount = 0

    init(error: TranscriptionError? = nil) {
        self.error = error
    }

    func transcribe(_ request: TranscriptionRequest) async throws -> TranscriptionOutcome {
        callCount += 1
        if let error { throw error }
        return TranscriptionOutcome(text: "fallback", localeIdentifier: request.localeIdentifier, provider: .dictationTranscriber)
    }
}
