import CaptureCore
import FoundationModels
import SenecaEngine
import XCTest
@testable import SenecaMobile

/// A spoken take on its way into the form: the split shaped from the
/// questions the journal asks, the transcript offered to copy when the
/// on-device model is unavailable, refuses or cannot read the take, the
/// rules that keep typed text, and a spoken entry saved through the bridge
/// byte for byte as a typed one.
@MainActor
final class ProposalTests: XCTestCase {
    private var roundTrip: RoundTrip!

    override func setUp() async throws {
        roundTrip = try RoundTrip.load()
    }

    private func take(_ transcript: String = "I noticed the quiet. I said no. Tomorrow, one thing.") -> SpokenTake {
        SpokenTake(
            id: UUID().uuidString.lowercased(), date: roundTrip.scenario.date, createdAt: Date(),
            durationSeconds: 12, byteCount: 4096, audioSha256: "", locale: "en-US",
            state: .transcribed, transcript: transcript
        )
    }

    /// A launch of the app over `root`, its takes recorded and transcribed
    /// by the simulated devices and split by `splitter`.
    private func launch(
        _ location: JournalLocation, transcript: String = "spoken", splitter: any AnswerSplitting,
        file: StaticString = #filePath, line: UInt = #line
    ) async -> JournalModel {
        let model = JournalModel(
            engine: SenecaEngine(), location: { location },
            voiceDevices: { root in
                let recorder = SimulatedAudioRecorder(store: AudioFileStore(applicationSupportRoot: root))
                return VoiceDevices(
                    recorder: recorder,
                    transcriber: SimulatedTranscriber(text: transcript, provider: .speechTranscriber),
                    models: ReadyModels(), protectedData: AlwaysAvailableProtectedData(),
                    observeInterruptions: { handler in await recorder.setInterruptionHandler(handler) },
                    splitter: splitter
                )
            }
        )
        await model.open()
        XCTAssertEqual(model.phase, .ready, file: file, line: line)
        return model
    }

    // MARK: The split

    func testTheSplitIsShapedForOneToFiveQuestions() async throws {
        await reportingOnMain {
            let questions = ["What did I notice today?", "What did I handle well?", "Where did I fall short?",
                             "What will I carry into tomorrow?", "Qu’est-ce qui m’a surpris ?"]
            for count in 1...5 {
                let asked = Array(questions.prefix(count))
                let names = (1...count).map { "answer_\($0)" } + ["unassigned"]

                // The schema the model is held to: one string per question, in
                // order, plus what answers none of them.
                let schema = try FoundationModelsSplitter.schema(for: asked)
                let json = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(schema)) as? [String: Any])
                let properties = try XCTUnwrap(json["properties"] as? [String: [String: Any]], "\(json)")
                XCTAssertEqual(Set(properties.keys), Set(names), "\(count) questions")
                XCTAssertEqual(json["x-order"] as? [String] ?? names, names, "\(count) questions")
                for (index, question) in asked.enumerated() {
                    let property = try XCTUnwrap(properties["answer_\(index + 1)"])
                    XCTAssertEqual(property["type"] as? String, "string")
                    XCTAssertTrue((property["description"] as? String)?.contains(question) == true, "\(property)")
                }
                XCTAssertEqual(properties["unassigned"]?["type"] as? String, "string")

                let prompt = FoundationModelsSplitter.prompt("the words", questions: asked)
                for (index, question) in asked.enumerated() {
                    XCTAssertTrue(prompt.contains("\(index + 1). \(question)"), prompt)
                }
                XCTAssertTrue(prompt.hasSuffix("Transcript:\nthe words"), prompt)

                // What the model generates is read back one answer per question.
                var generated: [String: String] = ["unassigned": " aside "]
                for index in 0..<count { generated["answer_\(index + 1)"] = "answer \(index + 1)\n" }
                let content = try GeneratedContent(json: String(decoding: JSONEncoder().encode(generated), as: UTF8.self))
                let read = try FoundationModelsSplitter.answers(from: content, questions: asked)
                XCTAssertEqual(read, ProposedAnswers(answers: (1...count).map { "answer \($0)" }, unassigned: "aside"))
            }
            XCTAssertTrue(FoundationModelsSplitter.instructions.contains("Never invent, add, summarize"))

            // A proposal that does not match the questions is not shown as one.
            let proposal = AnswerProposal(take: take(), questions: roundTrip.scenario.questions, typed: [])
            await proposal.propose(with: FixedSplitter(.success(ProposedAnswers(answers: ["one"], unassigned: ""))))
            guard case .fallback = proposal.phase else { return XCTFail("\(proposal.phase)") }
            XCTAssertEqual(proposal.answers, ["", "", ""])
        }
    }

    // MARK: When nothing is proposed

    func testAnIPhoneThatCannotRunTheModelIsNeverAskedAndCopiesTheTranscript() async throws {
        await reportingOnMain {
            XCTAssertFalse(LanguageModelSupport.eligible(.unavailable(.deviceNotEligible)))
            XCTAssertTrue(LanguageModelSupport.eligible(.available))
            XCTAssertTrue(LanguageModelSupport.eligible(.unavailable(.appleIntelligenceNotEnabled)),
                          "a model that is only turned off is still offered, and says why")
            XCTAssertTrue(LanguageModelSupport.eligible(.unavailable(.modelNotReady)))

            let location = try roundTrip.seed(in: try scratchDirectory(self))
            // A splitter that would answer, on hardware that cannot run it.
            let splitter = IneligibleSplitter()
            let model = await launch(location, splitter: splitter)
            let session = try XCTUnwrap(model.session(on: roundTrip.scenario.date))
            await session.load()
            let spoken = take()
            let proposal = AnswerProposal(take: spoken, questions: try XCTUnwrap(session.draft).questions, typed: session.answers)
            await proposal.propose(with: try XCTUnwrap(model.voice).splitter)

            XCTAssertEqual(proposal.phase, .fallback(.notOffered), "the model was asked on hardware that cannot run it")
            XCTAssertFalse(SplitFailure.notOffered.message.contains("Apple Intelligence"), SplitFailure.notOffered.message)
            XCTAssertEqual(VoiceText.sortLabel(try XCTUnwrap(model.voice)), "Copy Into Your Questions")
            proposal.copyTranscript(to: 0)
            XCTAssertEqual(proposal.answers[0], spoken.transcript)
        }
    }

    func testAnUnavailableModelShowsTheTranscriptToCopyIntoEachQuestion() async throws {
        await reportingOnMain {
            XCTAssertNil(FoundationModelsSplitter.failure(for: .available))
            for reason in [SystemLanguageModel.Availability.UnavailableReason.appleIntelligenceNotEnabled, .deviceNotEligible, .modelNotReady] {
                guard case .unavailable = FoundationModelsSplitter.failure(for: .unavailable(reason)) else {
                    return XCTFail("\(reason) is not said to be unavailable")
                }
            }
            if #available(iOS 27.0, *) {
                XCTAssertEqual(
                    FoundationModelsSplitter.classify(SystemLanguageModel.Error.assetsUnavailable(.init(debugDescription: "no assets"))),
                    .unavailable("the on-device model is not ready yet.")
                )
            }

            let location = try roundTrip.seed(in: try scratchDirectory(self))
            let model = await launch(location, splitter: FixedSplitter(.failure(SplitFailure.unavailable("Apple Intelligence is turned off."))))
            let session = try XCTUnwrap(model.session(on: roundTrip.scenario.date))
            await session.load()
            session.setAnswer(0, "Typed before speaking.")
            let spoken = take()
            let proposal = AnswerProposal(take: spoken, questions: try XCTUnwrap(session.draft).questions, typed: session.answers)
            await proposal.propose(with: try XCTUnwrap(model.voice).splitter)

            XCTAssertEqual(proposal.phase, .fallback(.unavailable("Apple Intelligence is turned off.")))
            if case .fallback(let failure) = proposal.phase {
                XCTAssertTrue(failure.message.contains("Copy the transcript"), failure.message)
            }
            XCTAssertEqual(proposal.answers, ["", "", ""], "no model text when the model did not run")
            XCTAssertFalse(proposal.changesDraft, "accepting an empty fallback changes nothing")

            proposal.copyTranscript(to: 1)
            XCTAssertEqual(proposal.answers[1], spoken.transcript)
            proposal.copyTranscript(to: 0)
            XCTAssertTrue(proposal.conflicts(0))
            XCTAssertTrue(proposal.accept(into: session))
            XCTAssertEqual(session.answers, ["Typed before speaking.", spoken.transcript!, ""], "typed text is kept by default")

            // An empty take has nothing to sort.
            let silent = AnswerProposal(take: take(""), questions: roundTrip.scenario.questions, typed: [])
            await silent.propose(with: FixedSplitter(.success(ProposedAnswers(answers: ["a", "b", "c"], unassigned: ""))))
            XCTAssertEqual(silent.phase, .fallback(.failed("nothing was heard.")))
        }
    }

    func testARefusalOrATakeTooLongForTheModelFallsBackToTheTranscript() async throws {
        await reportingOnMain {
            let context = LanguageModelSession.GenerationError.Context(debugDescription: "test")
            XCTAssertEqual(
                FoundationModelsSplitter.classify(LanguageModelSession.GenerationError.refusal(.init(transcriptEntries: []), context)),
                .refused
            )
            XCTAssertEqual(FoundationModelsSplitter.classify(LanguageModelSession.GenerationError.guardrailViolation(context)), .refused)
            XCTAssertEqual(FoundationModelsSplitter.classify(LanguageModelSession.GenerationError.exceededContextWindowSize(context)), .tooLong)
            if #available(iOS 27.0, *) {
                XCTAssertEqual(
                    FoundationModelsSplitter.classify(LanguageModelError.refusal(.init(explanation: "no", debugDescription: "test"))),
                    .refused
                )
                XCTAssertEqual(
                    FoundationModelsSplitter.classify(LanguageModelError.contextSizeExceeded(.init(contextSize: 4096, tokenCount: 9000, debugDescription: "test"))),
                    .tooLong
                )
            }
            guard case .failed = FoundationModelsSplitter.classify(CocoaError(.fileReadUnknown)) else {
                return XCTFail("an unknown error is not a failure")
            }

            for (thrown, expected) in [
                (LanguageModelSession.GenerationError.refusal(.init(transcriptEntries: []), context) as Error, SplitFailure.refused),
                (LanguageModelSession.GenerationError.exceededContextWindowSize(context), .tooLong),
                (SplitFailure.refused, .refused),
            ] {
                let proposal = AnswerProposal(take: take(), questions: roundTrip.scenario.questions, typed: ["", "", ""])
                await proposal.propose(with: FixedSplitter(.failure(thrown)))
                XCTAssertEqual(proposal.phase, .fallback(expected))
                XCTAssertEqual(proposal.answers, ["", "", ""])
                XCTAssertEqual(proposal.unassigned, "")
                XCTAssertFalse(proposal.changesDraft)
            }
        }
    }

    // MARK: Merging into the draft

    func testProposedAnswersMergeIntoTheDraftWithoutOverwritingTypedText() async throws {
        await reportingOnMain {
            typealias Merge = AnswerProposal.Merge
            let typed = ["Typed one.", "", "Typed three.  \n", "Same."]
            let proposed = ["Spoken one.", "Spoken two.", "  ", " Same. "]
            XCTAssertEqual(
                AnswerProposal.merge(typed: typed, proposed: proposed, choices: [.keep, .keep, .keep, .keep]),
                ["Typed one.", "Spoken two.", "Typed three.  \n", "Same."],
                "an empty field takes the proposal; an empty proposal and typed text are left alone"
            )
            XCTAssertEqual(
                AnswerProposal.merge(typed: typed, proposed: proposed, choices: [.replace, .replace, .replace, .replace]),
                ["Spoken one.", "Spoken two.", "Typed three.  \n", "Same."],
                "replacing is per question, and only where there is something to replace with"
            )
            XCTAssertEqual(
                AnswerProposal.merge(typed: typed, proposed: proposed, choices: [.append, .append, .append, .append]),
                ["Typed one.\nSpoken one.", "Spoken two.", "Typed three.  \n", "Same."]
            )
            XCTAssertEqual(AnswerProposal.merge(typed: ["a"], proposed: ["b"], choices: []), ["a"], "typed text is kept by default")
            XCTAssertTrue(AnswerProposal.conflict(typed: "a", proposed: "b"))
            XCTAssertFalse(AnswerProposal.conflict(typed: "a", proposed: " a\n"))
            XCTAssertFalse(AnswerProposal.conflict(typed: "", proposed: "b"))

            // Through the session: the draft is kept as typed text is.
            let location = try roundTrip.seed(in: try scratchDirectory(self))
            let questions = roundTrip.scenario.questions
            let model = await launch(
                location,
                splitter: FixedSplitter(.success(ProposedAnswers(answers: ["Spoken one.", "Spoken two.", ""], unassigned: "Aside."))))
            let session = try XCTUnwrap(model.session(on: roundTrip.scenario.date))
            await session.load()
            session.setAnswer(0, "Typed one.")
            session.setAnswer(2, "Typed three.")
            let proposal = AnswerProposal(take: take(), questions: questions, typed: session.answers)
            await proposal.propose(with: try XCTUnwrap(model.voice).splitter)
            XCTAssertEqual(proposal.phase, .proposed)
            XCTAssertEqual(proposal.unassigned, "Aside.")
            XCTAssertEqual([0, 1, 2].map(proposal.conflicts), [true, false, false])

            proposal.answers[1] += " Edited."
            proposal.merges[0] = .append
            XCTAssertTrue(proposal.accept(into: session))
            XCTAssertEqual(session.answers, ["Typed one.\nSpoken one.", "Spoken two. Edited.", "Typed three."])
            XCTAssertTrue(session.dirty)
            let kept = try XCTUnwrap(try DraftStore(directory: AppPaths.draftsDirectory(for: location)).read(roundTrip.scenario.date))
            XCTAssertEqual(kept.answers, session.answers, "the merged answers are a draft like typed ones")
            XCTAssertEqual(try sha256(of: location.reflections), try sha256(of: roundTrip.directory.appendingPathComponent("journal/reflections.md")), "accepting saves nothing")

            // Moving what was set aside is the author's call.
            proposal.copyUnassigned(to: 2)
            XCTAssertEqual(proposal.answers[2], "Aside.")

            // Questions changed under the proposal: nothing is filled.
            let stale = AnswerProposal(take: take(), questions: ["Another question?"], typed: [])
            stale.answers = ["Spoken."]
            XCTAssertFalse(stale.accept(into: session))
            XCTAssertEqual(session.answers, ["Typed one.\nSpoken one.", "Spoken two. Edited.", "Typed three."])
        }
    }

    // MARK: Byte identity

    func testASpokenEntryIsByteIdenticalToATypedEntryWithTheSameAnswers() async throws {
        await reportingOnMain {
            let scenario = roundTrip.scenario
            let expected = roundTrip.expected

            // Typed, through the form's session.
            let typedLocation = try roundTrip.seed(in: try scratchDirectory(self))
            let typedModel = await launch(typedLocation, splitter: FixedSplitter(.failure(SplitFailure.refused)))
            let typed = try XCTUnwrap(typedModel.session(on: scenario.date))
            await typed.load()
            for (index, answer) in scenario.answers.enumerated() { typed.setAnswer(index, answer) }
            typed.setFollowThrough(scenario.followThrough)
            let typedSaved = await typed.save()
            XCTAssertTrue(typedSaved)

            // Spoken from Today: recorded, transcribed, split, confirmed into the
            // draft, then saved by the form that opens on the kept draft.
            let root = try scratchDirectory(self)
            let spokenLocation = try roundTrip.seed(in: root)
            let model = await launch(
                spokenLocation, transcript: scenario.answers.joined(separator: " "),
                splitter: FixedSplitter(.success(ProposedAnswers(answers: scenario.answers, unassigned: "")))
            )
            let voice = try XCTUnwrap(model.voice)
            await voice.start(on: scenario.date)
            await voice.stop()
            let spoken = try XCTUnwrap(voice.take)
            XCTAssertEqual(spoken.state, .transcribed)
            await voice.start(on: scenario.date)
            await voice.stop()
            let unused = try XCTUnwrap(voice.take)
            XCTAssertNotEqual(unused.id, spoken.id)

            let opened = try XCTUnwrap(model.session(on: scenario.date))
            await opened.load()
            let proposal = AnswerProposal(take: spoken, questions: try XCTUnwrap(opened.draft).questions, typed: opened.answers)
            await proposal.propose(with: voice.splitter)
            XCTAssertEqual(proposal.phase, .proposed)
            XCTAssertTrue(proposal.accept(into: opened))
            voice.accepted(spoken)
            XCTAssertEqual(try sidecar(spoken, root)["answers_saved"] as? Bool, false, "accepted is not saved")
            XCTAssertNotNil(try sidecar(spoken, root)["accepted_at"] as? String)

            let form = try XCTUnwrap(model.session(on: scenario.date))
            await form.load()
            XCTAssertEqual(form.notice, .restored)
            XCTAssertEqual(form.answers, scenario.answers)
            form.setFollowThrough(scenario.followThrough)
            let spokenSaved = await form.save()
            XCTAssertTrue(spokenSaved)
            voice.markAnswersSaved(on: scenario.date)

            // The same bytes, and the desktop's.
            XCTAssertEqual(try Data(contentsOf: spokenLocation.reflections), try Data(contentsOf: typedLocation.reflections))
            XCTAssertEqual(try sha256(of: spokenLocation.reflections), expected.reflectionsSha256)
            let reopened = SenecaEngine()
            _ = try await reopened.open(spokenLocation)
            let entry = try await reopened.entry(on: scenario.date)
            XCTAssertEqual(entry.raw, expected.entryRaw)
            XCTAssertFalse(entry.raw.contains("spoken"), "the entry carries no marker")

            XCTAssertEqual(try sidecar(spoken, root)["answers_saved"] as? Bool, true)
            XCTAssertEqual(try sidecar(unused, root)["answers_saved"] as? Bool, false, "a take not accepted is not marked")
            XCTAssertNil(try sidecar(unused, root)["accepted_at"])
        }
    }

    private func sidecar(_ take: SpokenTake, _ root: URL) throws -> [String: Any] {
        let url = root.appendingPathComponent("audio/\(take.date)/\(take.id).json")
        return try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
    }
}

/// A splitter that answers the same way every time.
/// Hardware that cannot run the model: offered nothing, and an answer it
/// would give if asked, so asking it shows.
struct IneligibleSplitter: AnswerSplitting {
    var offered: Bool { false }

    func split(_ transcript: String, questions: [String], locale: String) async throws -> ProposedAnswers {
        ProposedAnswers(answers: questions.map { _ in "asked anyway" }, unassigned: "")
    }
}

struct FixedSplitter: AnswerSplitting {
    let outcome: Result<ProposedAnswers, any Error>

    init(_ outcome: Result<ProposedAnswers, any Error>) {
        self.outcome = outcome
    }

    func split(_ transcript: String, questions: [String], locale: String) async throws -> ProposedAnswers {
        try outcome.get()
    }
}
