import CaptureApple
import CaptureCore
import Foundation

/// What speaking a reflection needs from the iPhone, through the vendored
/// seams: the microphone, the on-device transcribers, the speech models,
/// and the lock state. The app's are Apple's; tests give simulated ones.
struct VoiceDevices: Sendable {
    var recorder: any AudioRecording
    var transcriber: any Transcribing
    var models: any SpeechModelPreparing
    var protectedData: any ProtectedDataAvailability
    /// Routes the recorder's interruptions (a call, a lost route, media
    /// services reset) to the handler given.
    var observeInterruptions: @Sendable (@escaping @Sendable (CaptureFailure) -> Void) async -> Void
    /// Files a transcript under the journal's questions: Apple's on-device
    /// model unless a test stands in its own.
    var splitter: any AnswerSplitting = FoundationModelsSplitter()

    /// Apple's: the AVAudioEngine recorder writing fragmented m4a under
    /// `root`, `SpeechTranscriber` falling back to `DictationTranscriber`,
    /// AssetInventory for the models, and the phone's lock state.
    static func system(root: URL) -> VoiceDevices {
        let recorder = AudioRecorderAdapter(store: AudioFileStore(applicationSupportRoot: root))
        return VoiceDevices(
            recorder: recorder,
            transcriber: FallbackTranscriber([AppleSpeechTranscriberAdapter(), AppleDictationTranscriberAdapter()]),
            models: AppleSpeechModelPreparer(),
            protectedData: SystemProtectedData(),
            observeInterruptions: { handler in await recorder.setInterruptionHandler(handler) }
        )
    }
}

#if targetEnvironment(simulator)
/// A UI test's microphone, transcriber and splitter:
/// SENECA_UITEST_MICROPHONE=denied refuses the microphone,
/// SENECA_UITEST_TRANSCRIPT is what any take transcribes to, and
/// SENECA_UITEST_SPLIT is what the on-device model makes of it: a JSON
/// object `{"answers": [...], "unassigned": "..."}`, or `refused` or
/// `too-long`; unset, the model is unavailable. SENECA_UITEST_MODEL=ineligible
/// stands for an iPhone too old to run the model at all. The audio is a few
/// synthetic bytes, never real AAC.
/// Simulator builds only; an iPhone build has no such door.
enum StagedVoice {
    static func devices(root: URL, environment: [String: String] = ProcessInfo.processInfo.environment) -> VoiceDevices? {
        let microphone = environment["SENECA_UITEST_MICROPHONE"]
        let transcript = environment["SENECA_UITEST_TRANSCRIPT"]
        guard microphone != nil || transcript != nil else { return nil }
        return VoiceDevices(
            recorder: StagedRecorder(store: AudioFileStore(applicationSupportRoot: root), denied: microphone == "denied"),
            transcriber: StagedTranscriber(text: transcript ?? ""),
            models: StagedModels(),
            protectedData: AlwaysAvailableProtectedData(),
            observeInterruptions: { _ in },
            splitter: StagedSplitter(staged: environment["SENECA_UITEST_SPLIT"])
        )
    }

    static let bytes = Data(repeating: 0x21, count: 4096)

    actor StagedRecorder: AudioRecording {
        let store: AudioFileStore
        let denied: Bool
        private var destination: URL?
        private var startedAt = Date()

        init(store: AudioFileStore, denied: Bool) {
            self.store = store
            self.denied = denied
        }

        func startRecording(to destination: URL, configuration: RecordingConfiguration) async throws {
            guard !denied else { throw RecordingError.microphoneDenied }
            try StagedVoice.bytes.write(to: AudioFileStore.partialURL(forFinalURL: destination))
            self.destination = destination
            startedAt = Date()
        }

        func stopRecording() async throws -> RecordedAudio {
            guard let destination else { throw RecordingError.noActiveRecording }
            self.destination = nil
            let stored = try store.finalize(finalURL: destination)
            return RecordedAudio(
                url: stored.url, durationSeconds: max(Date().timeIntervalSince(startedAt), 1),
                byteCount: stored.byteCount, sha256: stored.sha256
            )
        }

        func cancelRecording() async { destination = nil }

        func duration(of url: URL) async throws -> Double {
            guard try Data(contentsOf: url) == StagedVoice.bytes else { throw RecordingError.emptyRecording }
            return 3
        }

        func meter() async -> RecordingMeter? {
            guard destination != nil else { return nil }
            return RecordingMeter(durationSeconds: Date().timeIntervalSince(startedAt), level: 0.4)
        }
    }

    struct StagedTranscriber: Transcribing {
        let text: String
        func transcribe(_ request: TranscriptionRequest) async throws -> TranscriptionOutcome {
            TranscriptionOutcome(text: text, localeIdentifier: request.localeIdentifier, provider: .speechTranscriber)
        }
    }

    struct StagedSplitter: AnswerSplitting {
        let staged: String?

        var offered: Bool { LanguageModelSupport.onThisDevice }

        private struct Staged: Decodable {
            let answers: [String]
            let unassigned: String
        }

        func split(_ transcript: String, questions: [String], locale: String) async throws -> ProposedAnswers {
            switch staged {
            case nil, "unavailable":
                throw SplitFailure.unavailable("Apple Intelligence is turned off.")
            case "refused":
                throw SplitFailure.refused
            case "too-long":
                throw SplitFailure.tooLong
            case let json?:
                let staged = try JSONDecoder().decode(Staged.self, from: Data(json.utf8))
                return ProposedAnswers(answers: staged.answers, unassigned: staged.unassigned)
            }
        }
    }

    struct StagedModels: SpeechModelPreparing {
        func prepare(localeIdentifier: String, onProgress: @escaping @Sendable (Double) -> Void) async -> Bool { true }
        func state(localeIdentifier: String) async -> SpeechModelState { .ready }
        func keepReserved(localeIdentifiers: Set<String>) async {}
    }
}
#endif
