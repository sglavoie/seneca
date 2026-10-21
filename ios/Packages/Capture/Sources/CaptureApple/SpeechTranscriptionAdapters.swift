import AVFoundation
import Foundation
import CaptureCore
import Speech

/// Transcribes a finished recording with Apple's on-device `SpeechTranscriber`.
///
/// The whole file is analyzed at once rather than streamed: the audio is already
/// durable on disk, so there is no live buffer to keep up with, and a batch pass
/// can be restarted from scratch after a crash without losing partial results
/// that were never committed anyway.
public struct AppleSpeechTranscriberAdapter: Transcribing {
    public init() {}

    public func transcribe(_ request: TranscriptionRequest) async throws -> TranscriptionOutcome {
        guard SpeechTranscriber.isAvailable else {
            throw TranscriptionError.noProviderAvailable
        }

        let requested = Locale(identifier: request.localeIdentifier)
        guard let locale = await SpeechTranscriber.supportedLocale(equivalentTo: requested) else {
            throw TranscriptionError.localeUnsupported(request.localeIdentifier)
        }

        let module = SpeechTranscriber(locale: locale, preset: .transcription)
        try await SpeechAssets.requireInstalled(module, localeIdentifier: locale.identifier)

        let text = try await SpeechAnalysis.run(
            module: module,
            results: module.results,
            text: { $0.text },
            audioURL: request.audioURL
        )
        return TranscriptionOutcome(
            text: text,
            localeIdentifier: locale.identifier,
            provider: .speechTranscriber
        )
    }
}

/// Fetches the model the adapters above would use for a locale, without
/// transcribing anything: `SpeechTranscriber`'s when it serves the locale,
/// otherwise `DictationTranscriber`'s. A prepared locale is also reserved, so
/// iOS keeps its model when storage runs low, within the app's reservation
/// limit.
public struct AppleSpeechModelPreparer: SpeechModelPreparing {
    public init() {}

    public func prepare(
        localeIdentifier: String,
        onProgress: @escaping @Sendable (Double) -> Void
    ) async -> Bool {
        guard let target = await Self.target(for: localeIdentifier) else { return false }
        // Best effort: past the limit the model still downloads, unreserved.
        _ = try? await AssetInventory.reserve(locale: target.locale)
        return await SpeechAssets.install(target.module, onProgress: onProgress)
    }

    public func state(localeIdentifier: String) async -> SpeechModelState {
        guard let target = await Self.target(for: localeIdentifier) else { return .unsupported }
        switch await AssetInventory.status(forModules: [target.module]) {
        case .installed: return .ready
        case .downloading: return .downloading(fraction: nil)
        case .supported: return .notDownloaded
        case .unsupported: return .unsupported
        @unknown default: return .notDownloaded
        }
    }

    public func keepReserved(localeIdentifiers: Set<String>) async {
        var kept: Set<String> = []
        for identifier in localeIdentifiers {
            if let target = await Self.target(for: identifier) { kept.insert(target.locale.identifier) }
        }
        for locale in await AssetInventory.reservedLocales where !kept.contains(locale.identifier) {
            await AssetInventory.release(reservedLocale: locale)
        }
    }

    private struct Target {
        let module: any LocaleDependentSpeechModule
        let locale: Locale
    }

    private static func target(for localeIdentifier: String) async -> Target? {
        let requested = Locale(identifier: localeIdentifier)
        if SpeechTranscriber.isAvailable,
           let locale = await SpeechTranscriber.supportedLocale(equivalentTo: requested) {
            return Target(module: SpeechTranscriber(locale: locale, preset: .transcription), locale: locale)
        }
        if let locale = await DictationTranscriber.supportedLocale(equivalentTo: requested) {
            return Target(module: DictationTranscriber(locale: locale, preset: .longDictation), locale: locale)
        }
        return nil
    }
}

/// The fallback for locales `SpeechTranscriber` will not serve.
///
/// `DictationTranscriber` reuses the keyboard dictation models, which are
/// installed for far more locales than the transcription assets are, so it
/// covers devices where the preferred path reports the locale unsupported.
public struct AppleDictationTranscriberAdapter: Transcribing {
    public init() {}

    public func transcribe(_ request: TranscriptionRequest) async throws -> TranscriptionOutcome {
        let requested = Locale(identifier: request.localeIdentifier)
        guard let locale = await DictationTranscriber.supportedLocale(equivalentTo: requested) else {
            throw TranscriptionError.localeUnsupported(request.localeIdentifier)
        }

        // Captures can run to minutes, beyond a phrase or a search query.
        let module = DictationTranscriber(locale: locale, preset: .longDictation)
        try await SpeechAssets.requireInstalled(module, localeIdentifier: locale.identifier)

        let text = try await SpeechAnalysis.run(
            module: module,
            results: module.results,
            text: { $0.text },
            audioURL: request.audioURL
        )
        return TranscriptionOutcome(
            text: text,
            localeIdentifier: locale.identifier,
            provider: .dictationTranscriber
        )
    }
}

/// Speech models a locale needs.
///
/// A locale the device *supports* is not the same as a locale whose assets are
/// on disk, and iOS may take minutes to fetch them, or hold the download for
/// later. Transcription therefore never waits for one: it asks for the model
/// and fails as retryable, and the host resumes the entry once preparation
/// reports the model ready.
private enum SpeechAssets {
    /// How long preparation waits for a download before reporting the model not
    /// ready yet. iOS can accept an install request and never start the
    /// download, and a request that never returns could not be made again.
    static let downloadLimit: Duration = .seconds(15 * 60)

    /// For a transcription pass: returns when the model is installed, otherwise
    /// starts its download in the background and throws `assetsNotInstalled`.
    static func requireInstalled(
        _ module: any LocaleDependentSpeechModule,
        localeIdentifier: String
    ) async throws {
        switch await AssetInventory.status(forModules: [module]) {
        case .installed:
            return
        case .unsupported:
            throw TranscriptionError.localeUnsupported(localeIdentifier)
        case .supported, .downloading:
            break
        @unknown default:
            break
        }
        let installation = try? await AssetInventory.assetInstallationRequest(supporting: [module])
        if let installation {
            Task.detached(priority: .utility) {
                _ = try? await withDeadline(downloadLimit) { try await installation.downloadAndInstall() }
            }
        }
        throw TranscriptionError.assetsNotInstalled(localeIdentifier)
    }

    /// For preparation: downloads the model if needed and reports whether it is
    /// installed, waiting at most `downloadLimit`.
    static func install(
        _ module: any LocaleDependentSpeechModule,
        onProgress: @escaping @Sendable (Double) -> Void
    ) async -> Bool {
        switch await AssetInventory.status(forModules: [module]) {
        case .installed: return true
        case .unsupported: return false
        case .supported, .downloading: break
        @unknown default: break
        }
        guard let installation = try? await AssetInventory.assetInstallationRequest(supporting: [module]) else {
            return false
        }
        let reporting = Task {
            while !Task.isCancelled {
                onProgress(installation.progress.fractionCompleted)
                try? await Task.sleep(for: .milliseconds(500))
            }
        }
        defer { reporting.cancel() }
        do {
            try await withDeadline(downloadLimit) { try await installation.downloadAndInstall() }
        } catch {
            return false
        }
        return await AssetInventory.status(forModules: [module]) == .installed
    }
}

/// Drives one `SpeechAnalyzer` pass over a file and joins what came back.
private enum SpeechAnalysis {
    /// `SpeechModuleResult` carries timings but not text, so each adapter passes
    /// the accessor for its own result type.
    static func run<Results: AsyncSequence & Sendable>(
        module: any SpeechModule,
        results: Results,
        text: @escaping @Sendable (Results.Element) -> AttributedString,
        audioURL: URL
    ) async throws -> String {
        let file: AVAudioFile
        do {
            file = try AVAudioFile(forReading: audioURL)
        } catch {
            throw TranscriptionError.failed("could not open \(audioURL.lastPathComponent)")
        }

        let analyzer = SpeechAnalyzer(modules: [module])

        // Collection starts before analysis so no result is dropped between the
        // last sample and the sequence terminating.
        let collected = Task { try await join(results, text: text) }

        do {
            if let last = try await analyzer.analyzeSequence(from: file) {
                try await analyzer.finalizeAndFinish(through: last)
            } else {
                await analyzer.cancelAndFinishNow()
            }
        } catch is CancellationError {
            collected.cancel()
            throw TranscriptionError.cancelled
        } catch {
            collected.cancel()
            throw TranscriptionError.failed(String(describing: error))
        }

        do {
            return try await collected.value
        } catch is CancellationError {
            throw TranscriptionError.cancelled
        } catch {
            throw TranscriptionError.failed(String(describing: error))
        }
    }

    private static func join<Results: AsyncSequence>(
        _ results: Results,
        text: @Sendable (Results.Element) -> AttributedString
    ) async throws -> String {
        var pieces: [String] = []
        for try await result in results {
            let piece = String(text(result).characters)
                .trimmingCharacters(in: .whitespacesAndNewlines)
            guard !piece.isEmpty else { continue }
            pieces.append(piece)
        }
        return pieces.joined(separator: " ")
    }
}
