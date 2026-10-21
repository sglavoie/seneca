import CaptureCore
import FoundationModels
import XCTest
@testable import SenecaMobile

/// The debug-only model evaluation screen's run, with a stand-in for Apple's
/// models: the exported prompts held to their format and digests, every
/// prompt run on every target the build may reach, the responses written
/// beside the app's state and nowhere else, the review schema limited to
/// the prompt's dates, and Private Cloud Compute reached only when the
/// provisioning profile grants it.
@MainActor
final class ModelEvaluationTests: XCTestCase {
    private var root: URL!
    private var directory: URL { root.appendingPathComponent("evaluation", isDirectory: true) }

    override func setUpWithError() throws {
        root = try scratchDirectory(self)
    }

    static let schema = """
        {"type":"object","additionalProperties":false,"required":["summary","evidence","focus_assessment"],
         "properties":{"summary":{"type":"string"},
          "evidence":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["date","snippet"],
            "properties":{"date":{"type":"string"},"snippet":{"type":"string"}}}},
          "focus_assessment":{"type":["object","null"],"additionalProperties":false,"required":["evidence"],
            "properties":{"evidence":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["date"],
              "properties":{"date":{"type":"string"}}}}}}}}
        """

    private func prompt(_ id: String, _ text: String) -> [String: Any] {
        ["id": id, "prompt": text, "schema": Self.schema, "sha256": EvaluationFiles.sha256(Data(text.utf8))]
    }

    private func writePrompts(_ prompts: [[String: Any]], format: String = EvaluationPrompts.format) throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let body: [String: Any] = ["format": format, "format_version": 1, "prompts": prompts]
        try JSONSerialization.data(withJSONObject: body).write(to: directory.appendingPathComponent("prompts.json"))
    }

    private func evaluation(_ model: StandInModel, entitled: Bool) -> ModelEvaluation {
        ModelEvaluation(
            files: EvaluationFiles(directory: directory), model: model, entitledToPrivateCloud: entitled,
            device: .init(model: "iPhone99,1", system: "iOS 27.0", simulator: true, build: "7"),
            conditions: { .init(at: Date(), battery: 0.8, thermal: "nominal", pccQuota: nil) }
        )
    }

    private func responses() throws -> [String: Any] {
        try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: directory.appendingPathComponent("responses.json"))) as? [String: Any])
    }

    func testThePromptsAreHeldToTheirFormatAndDigests() async throws {
        await reportingOnMain {
            let files = EvaluationFiles(directory: directory)
            XCTAssertThrowsError(try files.load()) { XCTAssertEqual($0 as? EvaluationError, .noPrompts) }

            try writePrompts([prompt("2026-W14", "Date: 2026-04-01\nWrite.")])
            let (loaded, digest) = try files.load()
            XCTAssertEqual(loaded.prompts.map(\.id), ["2026-W14"])
            XCTAssertEqual(digest, EvaluationFiles.sha256(try Data(contentsOf: files.prompts)))

            var changed = prompt("2026-W15", "Date: 2026-04-08\nWrite.")
            changed["prompt"] = "Date: 2026-04-09\nWrite."
            try writePrompts([changed])
            XCTAssertThrowsError(try files.load()) { XCTAssertEqual($0 as? EvaluationError, .changed("2026-W15")) }

            try writePrompts([prompt("2026-W14", "x")], format: "something-else")
            XCTAssertThrowsError(try files.load()) {
                XCTAssertEqual($0 as? EvaluationError, .wrongFormat("something-else v1"))
            }
        }
    }

    func testEveryPromptRunsOnEveryReachableTargetAndOnlyTheResponsesAreWritten() async throws {
        await reportingOnMain {
            try writePrompts([prompt("2026-W14", "Date: 2026-04-01\nWrite."), prompt("2026-W15", "Date: 2026-04-08\nHeavy.")])
            let model = StandInModel(onDevice: "available", privateCloud: "available", refusing: "Heavy")
            let run = evaluation(model, entitled: true)
            run.refresh()
            XCTAssertEqual(run.promptCount, 2)
            await run.run()
            XCTAssertEqual(run.phase, .finished(8))

            let written = try responses()
            XCTAssertEqual(written["format"] as? String, EvaluationRun.format)
            XCTAssertEqual(written["on_device"] as? String, "available")
            XCTAssertEqual(written["private_cloud"] as? String, "available")
            XCTAssertNotNil(written["finished"] as? [String: Any])
            XCTAssertEqual((written["started"] as? [String: Any])?["pcc_quota"] as? String, "below-limit")
            let results = try XCTUnwrap(written["results"] as? [[String: Any]])
            let shape = results.map { "\($0["target"] as! String)/\($0["reasoning"] as? String ?? "-")/\($0["prompt_id"] as! String)/\($0["outcome"] as! String)" }
            XCTAssertEqual(shape, [
                "ondevice/-/2026-W14/ok", "ondevice/-/2026-W15/refused",
                "pcc/light/2026-W14/ok", "pcc/light/2026-W15/refused",
                "pcc/moderate/2026-W14/ok", "pcc/moderate/2026-W15/refused",
                "pcc/deep/2026-W14/ok", "pcc/deep/2026-W15/refused",
            ])
            XCTAssertEqual(results[0]["text"] as? String, #"{"summary":"ok"}"#)
            XCTAssertEqual(results[0]["prompt_tokens"] as? Int, 2)
            XCTAssertNil(results[2]["prompt_tokens"], "tokens are counted with the on-device tokenizer only")
            XCTAssertNotNil(results[1]["error"] as? String)
            XCTAssertEqual(model.reached, [.onDevice, .privateCloud(.light), .privateCloud(.moderate), .privateCloud(.deep)])

            // Nothing else is written: no journal, no state beside it.
            XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: root.path).sorted(), ["evaluation"])
            XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path).sorted(), ["prompts.json", "responses.json"])
        }
    }

    func testAnIPhoneThatCannotRunTheModelRecordsItsAnswerOnceWithoutTheScreen() async throws {
        await reportingOnMain {
            let model = StandInModel(onDevice: "deviceNotEligible", privateCloud: "available", refusing: nil)
            var made = 0
            let make: (URL) -> ModelEvaluation = { _ in made += 1; return self.evaluation(model, entitled: false) }

            // No prompts pushed: nothing to record.
            let nothing = await ModelEvaluation.recordIneligible(directory: directory, eligible: false, build: "7", make: make)
            XCTAssertFalse(nothing)

            try writePrompts([prompt("2026-W14", "Date: 2026-04-01\nWrite.")])
            // An eligible iPhone leaves it to the author and the screen.
            let eligible = await ModelEvaluation.recordIneligible(directory: directory, eligible: true, build: "7", make: make)
            XCTAssertFalse(eligible)
            XCTAssertFalse(FileManager.default.fileExists(atPath: directory.appendingPathComponent("responses.json").path))

            let recorded = await ModelEvaluation.recordIneligible(directory: directory, eligible: false, build: "7", make: make)
            XCTAssertTrue(recorded)
            XCTAssertEqual(try responses()["on_device"] as? String, "deviceNotEligible")
            XCTAssertTrue(model.reached.isEmpty, "a model was called on hardware that cannot run it")

            // Recorded for these prompts on this build: not again.
            let again = await ModelEvaluation.recordIneligible(directory: directory, eligible: false, build: "7", make: make)
            XCTAssertFalse(again)
            // A new build records its own answer.
            let newer = await ModelEvaluation.recordIneligible(directory: directory, eligible: false, build: "8", make: make)
            XCTAssertTrue(newer)
            XCTAssertEqual(made, 2)
        }
    }

    func testPrivateCloudComputeIsNeverCalledWithoutTheEntitlementAndAnIneligibleIPhoneSaysWhy() async throws {
        await reportingOnMain {
            try writePrompts([prompt("2026-W14", "Date: 2026-04-01\nWrite.")])
            let model = StandInModel(onDevice: "deviceNotEligible", privateCloud: "available", refusing: nil)
            let run = evaluation(model, entitled: false)
            await run.run()
            XCTAssertEqual(run.phase, .finished(0))
            let written = try responses()
            XCTAssertEqual(written["on_device"] as? String, "deviceNotEligible")
            XCTAssertEqual(written["private_cloud"] as? String, "not-entitled")
            XCTAssertEqual((written["results"] as? [Any])?.count, 0)
            XCTAssertTrue(model.reached.isEmpty)
            XCTAssertFalse(model.askedPrivateCloud, "the screen asked a model it is not entitled to")
        }
    }

    func testTheReviewSchemaOffersOnlyThePromptsDatesAndANullFocusWhenNoneApplies() throws {
        guard #available(iOS 26.4, *) else { throw XCTSkip("dynamic schemas with null need iOS 26.4") }
        let prompt = """
            Date: 2026-04-01
            Date: 2026-04-02
            End: 2026-04-07
            Only these dates may appear in focus_assessment.evidence: 2026-04-02.
            """
        let found = ReviewGenerationSchema.constraints(of: prompt)
        XCTAssertEqual(found.dates, ["2026-04-01", "2026-04-02", "2026-04-07"])
        XCTAssertEqual(found.focusDates, ["2026-04-02"])
        XCTAssertFalse(found.focusNull)

        let encoded = String(decoding: try JSONEncoder().encode(ReviewGenerationSchema.make(schema: Self.schema, prompt: prompt)), as: UTF8.self)
        for date in found.dates {
            XCTAssertTrue(encoded.contains(date), "the schema does not offer \(date): \(encoded)")
        }
        XCTAssertFalse(encoded.contains("2026-04-03"))

        let none = ReviewGenerationSchema.constraints(of: "Date: 2026-04-01\nfocus_assessment must be null.")
        XCTAssertTrue(none.focusNull)
        XCTAssertEqual(none.focusDates, ["2026-04-01"])
        XCTAssertThrowsError(try ReviewGenerationSchema.make(schema: "[]", prompt: prompt))
    }

    func testAProvisioningProfileGrantsOnlyWhatItLists() {
        func profile(_ entitlements: String) -> Data {
            var data = Data([0x30, 0x82, 0x01, 0x00])
            data.append(Data("""
                <?xml version="1.0" encoding="UTF-8"?>
                <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
                <plist version="1.0"><dict><key>Entitlements</key><dict>\(entitlements)</dict></dict></plist>
                """.utf8))
            data.append(Data([0x00, 0xA0, 0x82]))
            return data
        }
        let key = "com.apple.developer.private-cloud-compute"
        XCTAssertTrue(ProvisioningProfile.grants(key, profile: profile("<key>\(key)</key><true/>")))
        XCTAssertFalse(ProvisioningProfile.grants(key, profile: profile("<key>\(key)</key><false/>")))
        XCTAssertFalse(ProvisioningProfile.grants(key, profile: profile("<key>aps-environment</key><string>development</string>")))
        XCTAssertFalse(ProvisioningProfile.grants(key, profile: Data("not a profile".utf8)))
    }
}

/// A take times itself from the moment it stops to its transcript and its
/// proposed answers, which device qualification reads off the sidecar.
@MainActor
final class TakeTimingTests: XCTestCase {
    func testATakeKeepsWhenItStoppedAndHowLongItsAnswersTook() async throws {
        await reportingOnMain {
            let root = try scratchDirectory(self)
            let store = SpokenTakeStore(root: root.appendingPathComponent("audio", isDirectory: true))
            let recorder = SimulatedAudioRecorder(store: store.files)
            let preferences = VoicePreferencesStore(file: root.appendingPathComponent("voice.json"))
            try preferences.write(VoicePreferences(language: .english))
            let voice = VoiceCapture(
                store: store,
                devices: VoiceDevices(
                    recorder: recorder, transcriber: SimulatedTranscriber(text: "Spoken.", provider: .speechTranscriber),
                    models: ReadyModels(), protectedData: AlwaysAvailableProtectedData(),
                    observeInterruptions: { handler in await recorder.setInterruptionHandler(handler) }
                ),
                preferences: preferences
            )
            await voice.start(on: "2026-10-14")
            await voice.stop()
            let take = try XCTUnwrap(voice.take)
            let stopped = try XCTUnwrap(take.stoppedAt)
            XCTAssertLessThanOrEqual(stopped, try XCTUnwrap(take.transcribedAt))
            XCTAssertNil(take.proposalSeconds)

            voice.proposed(take, seconds: 2.5)
            let url = store.dayDirectory(take.date).appendingPathComponent("\(take.id).json")
            let json = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
            XCTAssertEqual(json["proposal_seconds"] as? Double, 2.5)
            XCTAssertNotNil(json["stopped_at"] as? String)
            XCTAssertEqual(voice.take?.proposalSeconds, 2.5)

            // An iPhone without Apple Intelligence: said so, nothing timed.
            voice.proposed(take, seconds: nil, unavailable: "This iPhone is not eligible.")
            XCTAssertEqual(voice.take?.proposalUnavailable, "This iPhone is not eligible.")
            voice.proposed(take, seconds: 3)
            XCTAssertNil(voice.take?.proposalUnavailable)
            XCTAssertEqual(voice.take?.proposalSeconds, 3)
            let latest = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
            XCTAssertEqual(latest["proposal_seconds"] as? Double, 3)

            // A sidecar written before the timings existed still reads.
            var older = latest
            older.removeValue(forKey: "stopped_at")
            older.removeValue(forKey: "proposal_seconds")
            older.removeValue(forKey: "proposal_unavailable")
            try JSONSerialization.data(withJSONObject: older).write(to: url)
            let read = try XCTUnwrap(store.read(date: take.date, id: take.id))
            XCTAssertNil(read.stoppedAt)
            XCTAssertNil(read.proposalSeconds)
        }
    }

    func testAProposalTimesTheModelOnlyWhenItProposes() async throws {
        await reportingOnMain {
            let take = SpokenTake(
                id: UUID().uuidString.lowercased(), date: "2026-10-14", createdAt: Date(), durationSeconds: 180,
                byteCount: 1, audioSha256: "", locale: "en-US", state: .transcribed, transcript: "I did well."
            )
            let proposal = AnswerProposal(take: take, questions: ["Well?"], typed: [""])
            await proposal.propose(with: FixedSplitter(.success(ProposedAnswers(answers: ["I did well."], unassigned: ""))))
            XCTAssertEqual(proposal.phase, .proposed)
            XCTAssertNotNil(proposal.seconds)
            await proposal.propose(with: FixedSplitter(.failure(SplitFailure.refused)))
            XCTAssertNil(proposal.seconds)
        }
    }
}

/// Apple's models, played: availability as given, a response for every
/// prompt but those holding `refusing`, which it declines.
private final class StandInModel: EvaluationModel, @unchecked Sendable {
    let onDevice: String
    let privateCloud: String
    let refusing: String?
    private let lock = NSLock()
    private var _reached: [EvaluationTarget] = []
    private var _askedPrivateCloud = false

    init(onDevice: String, privateCloud: String, refusing: String?) {
        self.onDevice = onDevice
        self.privateCloud = privateCloud
        self.refusing = refusing
    }

    var reached: [EvaluationTarget] { lock.withLock { _reached } }
    var askedPrivateCloud: Bool { lock.withLock { _askedPrivateCloud } }

    func availability(_ target: EvaluationTarget) async -> String {
        if target == .onDevice { return onDevice }
        lock.withLock { _askedPrivateCloud = true }
        return privateCloud
    }

    func promptTokens(_ prompt: String) async -> Int? {
        prompt.split(separator: "\n").count
    }

    func respond(_ prompt: String, schema: GenerationSchema, on target: EvaluationTarget) async throws -> String {
        lock.withLock {
            if _reached.last != target { _reached.append(target) }
        }
        if let refusing, prompt.contains(refusing) {
            throw LanguageModelSession.GenerationError.refusal(
                .init(transcriptEntries: []), .init(debugDescription: "declined")
            )
        }
        return #"{"summary":"ok"}"#
    }

    func quota() async -> String? {
        "below-limit"
    }
}
