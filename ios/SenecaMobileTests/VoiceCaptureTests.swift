import CaptureCore
import CryptoKit
import SenecaEngine
import XCTest
@testable import SenecaMobile

/// Speaking a reflection, under the record screen: a take recorded into the
/// day's audio directory and renamed when it stops, its sidecar, its
/// transcription with the dictation fallback, a take left for retry, the
/// speech model not waited for, and a partial a stopped app left behind.
/// The recorder and transcribers are the vendored simulated ones.
@MainActor
final class VoiceCaptureTests: XCTestCase {
    private var root: URL!
    private var audio: URL { root.appendingPathComponent("audio", isDirectory: true) }
    private let day = "2026-10-14"

    override func setUpWithError() throws {
        root = try scratchDirectory(self)
    }

    /// A model of its own over `audio/`, as a relaunch would make one.
    private func capture(
        recorder: SimulatedAudioRecorder? = nil,
        transcriber: any Transcribing = SimulatedTranscriber(text: "I kept my word today.", provider: .speechTranscriber),
        models: any SpeechModelPreparing = ReadyModels(),
        protectedData: any ProtectedDataAvailability = AlwaysAvailableProtectedData(),
        language: SpeechLanguage = .english
    ) throws -> (VoiceCapture, SimulatedAudioRecorder) {
        let store = SpokenTakeStore(root: audio)
        let recorder = recorder ?? SimulatedAudioRecorder(store: store.files)
        let preferences = VoicePreferencesStore(file: root.appendingPathComponent("voice.json"))
        try preferences.write(VoicePreferences(language: language))
        let devices = VoiceDevices(
            recorder: recorder, transcriber: transcriber, models: models, protectedData: protectedData,
            observeInterruptions: { handler in await recorder.setInterruptionHandler(handler) }
        )
        return (VoiceCapture(store: store, devices: devices, preferences: preferences), recorder)
    }

    private func files(in directory: URL) -> [String] {
        ((try? FileManager.default.contentsOfDirectory(atPath: directory.path)) ?? []).sorted()
    }

    private func sidecar(_ take: SpokenTake) throws -> [String: Any] {
        let url = audio.appendingPathComponent("\(take.date)/\(take.id).json")
        return try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
    }

    // MARK: Recording

    func testATakeIsRecordedIntoTheDayAndRenamedWhenItStopsWithItsSidecarAndNothingInTheJournal() async throws {
        await reportingOnMain {
            let roundTrip = try RoundTrip.load()
            let location = try roundTrip.seed(in: root)
            let journal = location.reflections.deletingLastPathComponent()
            let before = try snapshot(journal)

            // Through the app's model: the takes live beside the drafts.
            let model = JournalModel(
                engine: SenecaEngine(), location: { location },
                voiceDevices: { root in
                    let recorder = SimulatedAudioRecorder(store: AudioFileStore(applicationSupportRoot: root))
                    return VoiceDevices(
                        recorder: recorder,
                        transcriber: SimulatedTranscriber(text: "I kept my word today.", provider: .speechTranscriber),
                        models: ReadyModels(), protectedData: AlwaysAvailableProtectedData(),
                        observeInterruptions: { handler in await recorder.setInterruptionHandler(handler) }
                    )
                }
            )
            await model.open()
            XCTAssertEqual(model.phase, .ready)
            let voice = try XCTUnwrap(model.voice)
            let audio = AppPaths.audioDirectory(for: location)
            XCTAssertEqual(audio, root.appendingPathComponent("audio", isDirectory: true))
            XCTAssertFalse(audio.path.hasPrefix(journal.path + "/"), "the takes are kept inside the journal")

            await voice.start(on: day)
            XCTAssertEqual(voice.phase, .recording)
            let dayDirectory = audio.appendingPathComponent(day, isDirectory: true)
            let partials = files(in: dayDirectory)
            XCTAssertEqual(partials.count, 1)
            XCTAssertTrue(partials[0].hasSuffix(".partial.m4a"), "recording writes straight into the day: \(partials)")

            await voice.stop()
            XCTAssertEqual(voice.phase, .transcribed)
            let take = try XCTUnwrap(voice.take)
            XCTAssertEqual(files(in: dayDirectory), ["\(take.id).json", "\(take.id).m4a"])
            let final = dayDirectory.appendingPathComponent("\(take.id).m4a")
            XCTAssertEqual(take.state, .transcribed)
            XCTAssertEqual(take.transcript, "I kept my word today.")
            XCTAssertEqual(take.transcriber, TranscriptProvider.speechTranscriber.rawValue)
            XCTAssertEqual(take.locale, "en-US")
            XCTAssertEqual(take.audioSha256, try sha256(of: final))

            // The sidecar on disk, as the next session and a relaunch read it.
            let json = try XCTUnwrap(
                JSONSerialization.jsonObject(with: Data(contentsOf: dayDirectory.appendingPathComponent("\(take.id).json"))) as? [String: Any]
            )
            XCTAssertEqual(json["transcript"] as? String, "I kept my word today.")
            XCTAssertEqual(json["locale"] as? String, "en-US")
            XCTAssertEqual(json["transcriber"] as? String, "speech-transcriber")
            XCTAssertEqual(json["audio_sha256"] as? String, take.audioSha256)
            XCTAssertEqual(json["duration_seconds"] as? Double, 12)
            XCTAssertEqual(json["answers_saved"] as? Bool, false)
            XCTAssertEqual(json["date"] as? String, day)
            XCTAssertEqual(json["state"] as? String, "transcribed")
            XCTAssertNotNil(json["created_at"] as? String)
            XCTAssertEqual(json["version"] as? Int, 1)

            XCTAssertEqual(try snapshot(journal), before, "speaking wrote inside the journal")
            XCTAssertTrue(voice.untranscribed.isEmpty)
        }
    }

    func testARefusedMicrophoneIsSaidSoAndWritesNothing() async throws {
        await reportingOnMain {
            let store = SpokenTakeStore(root: audio)
            let (voice, _) = try capture(recorder: SimulatedAudioRecorder(store: store.files, refusesToStart: true))
            await voice.start(on: day)
            XCTAssertEqual(voice.phase, .denied)
            XCTAssertFalse(voice.isRecording)
            XCTAssertEqual(files(in: audio), [], "a refused microphone left something behind")
            XCTAssertTrue(voice.takes.isEmpty)
        }
    }

    func testAnInterruptionKeepsWhatWasCapturedAndTranscribesIt() async throws {
        await reportingOnMain {
            let (voice, recorder) = try capture()
            await voice.start(on: day)
            await recorder.interrupt(.interrupted)
            // The interruption arrives on its own task, as a phone call's does.
            for _ in 0..<100 where voice.phase != .transcribed {
                try await Task.sleep(for: .milliseconds(20))
            }
            XCTAssertEqual(voice.phase, .transcribed)
            let take = try XCTUnwrap(voice.take)
            XCTAssertEqual(files(in: audio.appendingPathComponent(day)), ["\(take.id).json", "\(take.id).m4a"])
            XCTAssertEqual(take.transcript, "I kept my word today.")
        }
    }

    // MARK: Transcription

    func testTheDictationTranscriberTakesOverWhenTheSpeechTranscriberCannotServeTheLanguage() async throws {
        await reportingOnMain {
            let speech = SimulatedTranscriber(text: "", provider: .speechTranscriber, failure: .localeUnsupported("fr-CA"), alwaysFails: true)
            let dictation = SimulatedTranscriber(text: "J’ai tenu parole.", provider: .dictationTranscriber)
            let (voice, _) = try capture(transcriber: FallbackTranscriber([speech, dictation]), language: .quebecFrench)
            await voice.start(on: day)
            await voice.stop()

            XCTAssertEqual(voice.phase, .transcribed)
            let take = try XCTUnwrap(voice.take)
            XCTAssertEqual(take.transcript, "J’ai tenu parole.")
            XCTAssertEqual(take.transcriber, "dictation-transcriber")
            XCTAssertEqual(take.locale, "fr-CA")
            let asked = await speech.requests
            let answered = await dictation.requests
            XCTAssertEqual(asked.map(\.localeIdentifier), ["fr-CA"], "the speech transcriber was not tried first")
            XCTAssertEqual(answered.map(\.localeIdentifier), ["fr-CA"])
            XCTAssertEqual(try sidecar(take)["transcriber"] as? String, "dictation-transcriber")
        }
    }

    func testAFailedTranscriptionStaysListedAndIsTriedAgain() async throws {
        await reportingOnMain {
            let flaky = SimulatedTranscriber(
                text: "Second time lucky.", provider: .speechTranscriber,
                failuresBeforeSuccess: 1, failure: .failed("the analyzer stopped")
            )
            let (voice, _) = try capture(transcriber: flaky)
            await voice.start(on: day)
            await voice.stop()

            guard case .failed = voice.phase else { return XCTFail("expected a failure, got \(voice.phase)") }
            let failed = try XCTUnwrap(voice.take)
            XCTAssertEqual(failed.state, .failed)
            XCTAssertNil(failed.transcript)
            XCTAssertEqual(voice.untranscribed.map(\.id), [failed.id], "a failed take is not offered again")
            XCTAssertEqual(try sidecar(failed)["state"] as? String, "failed")

            // A relaunch still lists it, from the sidecar alone.
            let (relaunched, _) = try capture(transcriber: flaky)
            XCTAssertEqual(relaunched.untranscribed.map(\.id), [failed.id])
            await relaunched.transcribe(try XCTUnwrap(relaunched.untranscribed.first))
            XCTAssertTrue(relaunched.untranscribed.isEmpty)
            XCTAssertEqual(relaunched.takes.first?.transcript, "Second time lucky.")
            XCTAssertEqual(try sidecar(failed)["transcript"] as? String, "Second time lucky.")
        }
    }

    func testAMissingSpeechModelIsNeverWaitedForAndTheTakeIsTranscribedOnceItArrives() async throws {
        await reportingOnMain {
            let gate = AsyncTestGate()
            let models = GatedModels(gate: gate)
            let transcriber = ModelBoundTranscriber(models: models)
            let (voice, _) = try capture(transcriber: transcriber, models: models)
            await voice.start(on: day)
            await voice.stop()

            // Stopping returned with the download still held back.
            XCTAssertEqual(voice.phase, .waitingForModel)
            let waiting = try XCTUnwrap(voice.take)
            XCTAssertEqual(waiting.state, .waitingForModel)
            XCTAssertEqual(try sidecar(waiting)["state"] as? String, "waiting-for-model")
            XCTAssertEqual(voice.untranscribed.map(\.id), [waiting.id])
            XCTAssertTrue(voice.preparingModel)

            await gate.release()
            await voice.modelPreparationFinished()
            XCTAssertEqual(voice.phase, .transcribed)
            XCTAssertEqual(voice.take?.transcript, "The model arrived.")
            XCTAssertTrue(voice.untranscribed.isEmpty)
        }
    }

    func testATranscriptionFinishingAfterTheScreenMovedOnLeavesTheNewRecordingAlone() async throws {
        await reportingOnMain {
            let gate = AsyncTestGate()
            let (voice, _) = try capture(transcriber: GatedTranscriber(gate: gate))
            await voice.start(on: day)
            // Stopping waits on the first transcription, held at the gate.
            let stopping = Task { await voice.stop() }
            await gate.arrived()
            XCTAssertEqual(voice.phase, .transcribing)
            let earlier = try XCTUnwrap(voice.take)

            // The sheet is closed and opened again, and a new take begun.
            voice.reset()
            await voice.start(on: day)
            XCTAssertEqual(voice.phase, .recording)

            // The earlier transcription lands: kept on its take, the screen untouched.
            await gate.release()
            await stopping.value
            XCTAssertEqual(voice.phase, .recording, "the earlier take's transcript took over the new recording")
            XCTAssertTrue(voice.isRecording)
            XCTAssertNil(voice.take)
            XCTAssertEqual(voice.takes.first { $0.id == earlier.id }?.transcript, "Said once the gate opened.")

            // The new recording still stops, and is the one the screen shows.
            await voice.stop()
            XCTAssertEqual(voice.phase, .transcribed)
            let later = try XCTUnwrap(voice.take)
            XCTAssertNotEqual(later.id, earlier.id)
            XCTAssertEqual(Set(voice.takes.map(\.id)), [earlier.id, later.id])
        }
    }

    // MARK: Recovery

    func testAPartialFoundAtLaunchIsFinalizedAndOfferedForTranscription() async throws {
        await reportingOnMain {
            // A recording the app was stopped in the middle of.
            let (first, recorder) = try capture()
            await first.start(on: day)
            XCTAssertEqual(first.phase, .recording)
            let dayDirectory = audio.appendingPathComponent(day, isDirectory: true)
            let partial = try XCTUnwrap(files(in: dayDirectory).first)
            XCTAssertTrue(partial.hasSuffix(".partial.m4a"))
            let id = String(partial.dropLast(".partial.m4a".count))
            // An empty partial from a recorder killed before its first byte.
            let empty = UUID().uuidString.lowercased()
            try Data().write(to: dayDirectory.appendingPathComponent("\(empty).partial.m4a"))

            // The next launch.
            let (relaunched, _) = try capture()
            let recovered = await relaunched.recover()
            XCTAssertEqual(recovered.map(\.id), [id])
            XCTAssertEqual(files(in: dayDirectory), ["\(id).json", "\(id).m4a"], "the partial was not finalized")
            let take = try XCTUnwrap(relaunched.untranscribed.first)
            XCTAssertEqual(take.id, id)
            XCTAssertEqual(take.state, .untranscribed)
            XCTAssertTrue(take.recovered)
            XCTAssertEqual(take.audioSha256, try sha256(of: dayDirectory.appendingPathComponent("\(id).m4a")))
            XCTAssertEqual(try sidecar(take)["recovered"] as? Bool, true)

            // Offered, then transcribed when asked.
            await relaunched.transcribe(take)
            XCTAssertTrue(relaunched.untranscribed.isEmpty)
            XCTAssertEqual(try sidecar(take)["transcript"] as? String, "I kept my word today.")

            // Recovery again finds nothing more to do.
            let again = await relaunched.recover()
            XCTAssertTrue(again.isEmpty)
            await recorder.cancelRecording()
        }
    }

    func testNothingIsRecoveredWhileTheIPhoneIsLocked() async throws {
        await reportingOnMain {
            let dayDirectory = audio.appendingPathComponent(day, isDirectory: true)
            try FileManager.default.createDirectory(at: dayDirectory, withIntermediateDirectories: true)
            let id = UUID().uuidString.lowercased()
            try SimulatedAudioFixture().bytes.write(to: dayDirectory.appendingPathComponent("\(id).partial.m4a"))

            let locked = SimulatedProtectedData(isAvailable: false)
            let (voice, _) = try capture(protectedData: locked)
            let whileLocked = await voice.recover()
            XCTAssertTrue(whileLocked.isEmpty)
            XCTAssertEqual(files(in: dayDirectory), ["\(id).partial.m4a"])

            await locked.setAvailable(true)
            let unlocked = await voice.recover()
            XCTAssertEqual(unlocked.map(\.id), [id])
        }
    }

    func testTheSidecarIsReplacedWholeAndLeavesNoTemporaryFiles() async throws {
        await reportingOnMain {
            let (voice, _) = try capture()
            await voice.start(on: day)
            await voice.stop()
            let take = try XCTUnwrap(voice.take)
            let store = SpokenTakeStore(root: audio)
            var edited = take
            edited.answersSaved = true
            try store.write(edited)
            XCTAssertEqual(try store.read(date: day, id: take.id)?.answersSaved, true)
            XCTAssertEqual(files(in: audio.appendingPathComponent(day)), ["\(take.id).json", "\(take.id).m4a"])
            // Nothing outside a day and an id is ever named.
            edited.date = "../journal"
            XCTAssertThrowsError(try store.write(edited))
        }
    }

    // MARK: Helpers

    /// Every file under `directory` with its hash.
    private func snapshot(_ directory: URL) throws -> [String: String] {
        var found: [String: String] = [:]
        let enumerator = FileManager.default.enumerator(at: directory, includingPropertiesForKeys: [.isRegularFileKey])
        while let url = enumerator?.nextObject() as? URL {
            guard (try url.resourceValues(forKeys: [.isRegularFileKey])).isRegularFile == true else { continue }
            found[String(url.path.dropFirst(directory.path.count))] = try sha256(of: url)
        }
        return found
    }
}

/// A speech model that is always on the device.
struct ReadyModels: SpeechModelPreparing {
    func prepare(localeIdentifier: String, onProgress: @escaping @Sendable (Double) -> Void) async -> Bool { true }
    func state(localeIdentifier: String) async -> SpeechModelState { .ready }
    func keepReserved(localeIdentifiers: Set<String>) async {}
}

/// A speech model whose download is held until the test releases the gate.
actor GatedModels: SpeechModelPreparing {
    let gate: AsyncTestGate
    private(set) var installed = false

    init(gate: AsyncTestGate) { self.gate = gate }

    func prepare(localeIdentifier: String, onProgress: @escaping @Sendable (Double) -> Void) async -> Bool {
        onProgress(0.5)
        await gate.pause()
        installed = true
        return true
    }

    func state(localeIdentifier: String) async -> SpeechModelState {
        installed ? .ready : .downloading(fraction: nil)
    }

    func keepReserved(localeIdentifiers: Set<String>) async {}
}

/// Holds its first transcription at the gate until the test releases it;
/// every later one answers at once.
actor GatedTranscriber: Transcribing {
    let gate: AsyncTestGate
    private var calls = 0

    init(gate: AsyncTestGate) { self.gate = gate }

    func transcribe(_ request: TranscriptionRequest) async throws -> TranscriptionOutcome {
        calls += 1
        if calls == 1 { await gate.pause() }
        return TranscriptionOutcome(text: "Said once the gate opened.", localeIdentifier: request.localeIdentifier, provider: .speechTranscriber)
    }
}

/// Transcribes only once the gated model is installed, as the vendored
/// adapters do: otherwise it reports the assets missing at once.
struct ModelBoundTranscriber: Transcribing {
    let models: GatedModels
    func transcribe(_ request: TranscriptionRequest) async throws -> TranscriptionOutcome {
        guard await models.installed else { throw TranscriptionError.assetsNotInstalled(request.localeIdentifier) }
        return TranscriptionOutcome(text: "The model arrived.", localeIdentifier: request.localeIdentifier, provider: .speechTranscriber)
    }
}
