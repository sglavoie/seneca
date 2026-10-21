import SenecaEngine
import XCTest
@testable import SenecaMobile

/// The daily workflow under the screens: the form's session over the real
/// engine, and the drafts it keeps on the device. Each "relaunch" is a new
/// model, a new engine, and a new store over the same directories, which is
/// all a stopped app leaves behind.
@MainActor
final class DailyWorkflowTests: XCTestCase {
    private var roundTrip: RoundTrip!
    private var location: JournalLocation!

    private var archive: URL {
        location.reflections.deletingLastPathComponent().appendingPathComponent("seneca-reviews.md")
    }

    private var store: DraftStore {
        DraftStore(directory: AppPaths.draftsDirectory(for: location))
    }

    override func setUp() async throws {
        roundTrip = try RoundTrip.load()
        location = try roundTrip.seed(in: try scratchDirectory(self))
    }

    /// A launch of the app over the container.
    private func launch(file: StaticString = #filePath, line: UInt = #line) async -> JournalModel {
        let location = location!
        let model = JournalModel(engine: SenecaEngine(), location: { location })
        await model.open()
        XCTAssertEqual(model.phase, .ready, file: file, line: line)
        return model
    }

    private func write(_ session: ReflectionSession, _ answers: [String], followThrough: String? = nil) {
        for (index, answer) in answers.enumerated() {
            session.setAnswer(index, answer)
        }
        if let followThrough {
            session.setFollowThrough(followThrough)
        }
    }

    func testADraftKeepsItsDateBaseQuestionsAnswersAndContext() async throws {
        let scenario = roundTrip.scenario
        let model = await launch()
        let session = try XCTUnwrap(model.session(on: scenario.date))
        await session.load()
        XCTAssertEqual(session.phase, .writing)
        XCTAssertFalse(session.dirty)
        XCTAssertNil(try store.read(scenario.date), "a form nobody wrote in keeps no draft")

        write(session, scenario.answers, followThrough: scenario.followThrough)
        let kept = try XCTUnwrap(try store.read(scenario.date), "what is typed is kept as it is typed")
        XCTAssertEqual(kept.date, scenario.date)
        XCTAssertEqual(kept.draft, session.draft)
        XCTAssertEqual(kept.draft.base, "absent")
        XCTAssertFalse(kept.draft.settingsRevision.isEmpty)
        XCTAssertEqual(kept.draft.questions, scenario.questions)
        XCTAssertEqual(kept.draft.context.focus, roundTrip.expected.focus)
        XCTAssertEqual(kept.draft.context.priorText, "Walk before the dip, not after.")
        XCTAssertTrue(kept.draft.context.showFollowThrough)
        XCTAssertEqual(kept.answers, scenario.answers)
        XCTAssertEqual(kept.followThrough, scenario.followThrough)
        XCTAssertFalse(kept.changedUnderneath)
        XCTAssertEqual(try sha256(of: location.reflections), try sha256(of: roundTrip.directory.appendingPathComponent("journal/reflections.md")), "a draft writes nothing to the journal")
    }

    func testADraftSurvivesTerminationAndIsClearedOnlyByASaveOrADiscard() async throws {
        let scenario = roundTrip.scenario
        do {
            let model = await launch()
            let session = try XCTUnwrap(model.session(on: scenario.date))
            await session.load()
            write(session, scenario.answers, followThrough: scenario.followThrough)
            // The app stops here: nothing is told that it is ending.
        }

        let model = await launch()
        XCTAssertEqual(model.drafts.map(\.date), [scenario.date], "the launch lists what was being written")
        let session = try XCTUnwrap(model.session(on: scenario.date))
        await session.load()
        XCTAssertEqual(session.notice, .restored)
        XCTAssertEqual(session.answers, scenario.answers)
        XCTAssertEqual(session.followThrough, scenario.followThrough)
        XCTAssertEqual(session.draft?.context.focus, roundTrip.expected.focus)

        // Leaving the form, or the app leaving the foreground, keeps it.
        session.persist()
        XCTAssertNotNil(try store.read(scenario.date))

        let saved = await session.save()
        XCTAssertTrue(saved, session.failure ?? "")
        XCTAssertNil(try store.read(scenario.date), "a committed save clears the draft")
        XCTAssertEqual(try sha256(of: location.reflections), roundTrip.expected.reflectionsSha256, "the restored draft saved what the desktop writes")

        // Another day, discarded at the author's word.
        let other = try XCTUnwrap(model.session(on: "2026-03-20"))
        await other.load()
        other.setAnswer(0, "Synthetic: thrown away on purpose.")
        XCTAssertNotNil(try store.read("2026-03-20"))
        other.discard()
        XCTAssertNil(try store.read("2026-03-20"))
        XCTAssertEqual(try sha256(of: location.reflections), roundTrip.expected.reflectionsSha256)
    }

    func testTheJournalAlreadyHoldingADraftClearsIt() async throws {
        let scenario = roundTrip.scenario
        let model = await launch()
        let session = try XCTUnwrap(model.session(on: scenario.date))
        await session.load()
        write(session, scenario.answers, followThrough: scenario.followThrough)
        let kept = try XCTUnwrap(try store.read(scenario.date))

        // The save lands and the app stops before the draft is cleared.
        let engine = SenecaEngine()
        try await engine.open(location)
        _ = try await engine.saveReflection(roundTrip.writing(on: kept.draft))
        XCTAssertNotNil(try store.read(scenario.date))

        let relaunched = await launch()
        let again = try XCTUnwrap(relaunched.session(on: scenario.date))
        await again.load()
        XCTAssertNil(again.notice)
        XCTAssertFalse(again.dirty)
        XCTAssertEqual(again.answers, scenario.answers)
        XCTAssertNil(try store.read(scenario.date))
    }

    func testAStaleEditKeepsEveryTypedWord() async throws {
        let scenario = roundTrip.scenario
        let typed = ["Synthetic: typed on the phone, one.", "Synthetic: typed on the phone, two.\n\nWith a second paragraph.", "Synthetic: typed on the phone, three."]
        let model = await launch()
        let session = try XCTUnwrap(model.session(on: scenario.date))
        await session.load()
        write(session, typed, followThrough: "Synthetic: phone follow-through.")

        // Meanwhile the day is written from somewhere else.
        let elsewhere = SenecaEngine()
        try await elsewhere.open(location)
        let theirs = try await elsewhere.prepareReflection(on: scenario.date)
        let written = try await elsewhere.saveReflection(roundTrip.writing(on: theirs.draft)).value

        let saved = await session.save()
        XCTAssertFalse(saved, "the save is refused: the day changed underneath")
        XCTAssertEqual(session.notice, .changedUnderneath)
        XCTAssertNotNil(session.failure)
        XCTAssertEqual(session.answers, typed, "every typed word is still in the form")
        XCTAssertEqual(session.followThrough, "Synthetic: phone follow-through.")
        XCTAssertEqual(session.journalAnswer(0), scenario.answers[0], "what the journal holds now is shown beside it")
        XCTAssertEqual(session.journalFollowThrough, scenario.followThrough)
        let untouched = try await elsewhere.entry(on: scenario.date)
        XCTAssertEqual(untouched, written.entry, "the refused save wrote nothing")

        var kept = try XCTUnwrap(try store.read(scenario.date))
        XCTAssertTrue(kept.changedUnderneath)
        XCTAssertEqual(kept.answers, typed)
        XCTAssertEqual(kept.draft.base, written.entry.revision, "the draft now stands on what the journal holds")
        XCTAssertNotNil(kept.reason)

        // Terminated and relaunched before the author decides.
        let relaunched = await launch()
        XCTAssertEqual(relaunched.draft(on: scenario.date)?.changedUnderneath, true)
        let again = try XCTUnwrap(relaunched.session(on: scenario.date))
        await again.load()
        XCTAssertEqual(again.notice, .changedUnderneath)
        XCTAssertEqual(again.answers, typed)
        XCTAssertEqual(again.followThrough, "Synthetic: phone follow-through.")
        kept = try XCTUnwrap(try store.read(scenario.date))
        XCTAssertEqual(kept.answers, typed)

        // The author saves over it, knowingly.
        let resaved = await again.save()
        XCTAssertTrue(resaved, again.failure ?? "")
        XCTAssertNil(try store.read(scenario.date))
        let entry = try await elsewhere.entry(on: scenario.date)
        for text in typed + ["Synthetic: phone follow-through."] {
            XCTAssertTrue(entry.raw.contains(text.components(separatedBy: "\n").first!), text)
        }
    }

    func testAFailedSaveKeepsTheDraftUntilTheJournalTakesIt() async throws {
        let scenario = roundTrip.scenario
        let model = await launch()
        let session = try XCTUnwrap(model.session(on: scenario.date))
        await session.load()
        write(session, scenario.answers, followThrough: scenario.followThrough)

        // The reflections file becomes one the engine refuses to write.
        let good = try Data(contentsOf: location.reflections)
        var broken = good
        broken.append(Data("## 2026-03-04\n\n### What did I notice today?\nSynthetic: a second entry for one day.\n---\n\n".utf8))
        try broken.write(to: location.reflections)

        let saved = await session.save()
        XCTAssertFalse(saved)
        XCTAssertNotNil(session.failure)
        XCTAssertEqual(session.answers, scenario.answers)
        XCTAssertEqual(try Data(contentsOf: location.reflections), broken, "the refused save wrote nothing")
        let kept = try XCTUnwrap(try store.read(scenario.date))
        XCTAssertEqual(kept.answers, scenario.answers)
        XCTAssertNotNil(kept.reason, "the draft says why it was not saved")

        // Relaunched while the journal is still unreadable: the writing is.
        let relaunched = await launch()
        XCTAssertNotNil(relaunched.entriesProblem)
        XCTAssertEqual(relaunched.drafts.map(\.date), [scenario.date])
        let again = try XCTUnwrap(relaunched.session(on: scenario.date))
        await again.load()
        XCTAssertEqual(again.phase, .writing)
        XCTAssertNotNil(again.failure)
        XCTAssertEqual(again.answers, scenario.answers)
        XCTAssertEqual(again.followThrough, scenario.followThrough)
        let refused = await again.save()
        XCTAssertFalse(refused)
        XCTAssertNotNil(try store.read(scenario.date))

        // The journal is repaired; the same draft saves, and only then clears.
        try good.write(to: location.reflections)
        await again.load()
        XCTAssertNil(again.failure)
        XCTAssertEqual(again.answers, scenario.answers)
        let resaved = await again.save()
        XCTAssertTrue(resaved, again.failure ?? "")
        XCTAssertNil(try store.read(scenario.date))
        XCTAssertEqual(try sha256(of: location.reflections), roundTrip.expected.reflectionsSha256)
    }

    func testAnUnreadableArchiveStillPermitsWritingWithoutContext() async throws {
        let scenario = roundTrip.scenario
        let garbage = try tornArchive(at: archive)

        let model = await launch()
        XCTAssertNotNil(model.archiveProblem, "the archive's trouble is reported")
        XCTAssertNotNil(model.contextProblem)
        XCTAssertNil(model.entriesProblem)
        XCTAssertEqual(model.entries.count, roundTrip.expected.entryCount - 1, "the reflections are still read")

        let session = try XCTUnwrap(model.session(on: scenario.date))
        await session.load()
        XCTAssertEqual(session.phase, .unavailable)
        XCTAssertTrue(session.contextUnavailable)
        XCTAssertNotNil(session.failure)

        await session.load(blank: true)
        XCTAssertEqual(session.phase, .writing)
        XCTAssertNil(session.failure)
        XCTAssertTrue(session.blank)
        XCTAssertEqual(session.draft?.context.focus, "")
        XCTAssertEqual(session.draft?.context.showFollowThrough, false)
        write(session, scenario.answers)
        XCTAssertEqual(try store.read(scenario.date)?.blank, true, "the draft remembers it is written without context")

        // Stopped and relaunched: the draft comes back without context again.
        let relaunched = await launch()
        let again = try XCTUnwrap(relaunched.session(on: scenario.date))
        await again.load()
        XCTAssertEqual(again.phase, .writing)
        XCTAssertTrue(again.blank)
        XCTAssertEqual(again.answers, scenario.answers)

        let saved = await again.save()
        XCTAssertTrue(saved, again.failure ?? "")
        XCTAssertNil(try store.read(scenario.date))
        XCTAssertEqual(try Data(contentsOf: archive), garbage, "the unreadable archive is left exactly as it was")
        await relaunched.refresh()
        XCTAssertTrue(relaunched.hasEntry(on: scenario.date))
        let entry = try await relaunched.entry(on: scenario.date)
        XCTAssertEqual(entry.sections.map(\.body), scenario.answers)
    }

    func testEditingKeepsWhatNoQuestionNames() async throws {
        let model = await launch()
        let before = try await model.entry(on: "2026-03-05")
        let session = try XCTUnwrap(model.session(on: "2026-03-05"))
        await session.load()
        XCTAssertEqual(session.draft?.isEdit, true)
        XCTAssertEqual(session.additional.map(\.heading), ["Gratitude"], "what no question names can be read")
        XCTAssertEqual(session.additional.first?.body, "Synthetic: a section no configured question names.")
        XCTAssertEqual(session.answers, ["Synthetic: energy dipped after lunch.", "Synthetic: took the walk anyway.", "Walk before the dip, not after."])
        XCTAssertEqual(session.followThrough, "Synthetic: the draft was done by eleven.")
        let shown = session.answers + [session.followThrough] + session.additional.map(\.body)
        XCTAssertFalse(shown.contains { $0.contains("seneca:") || $0.contains("###") }, "the form shows no Markdown structure")
        XCTAssertNil(try store.read("2026-03-05"), "opening an entry keeps no draft")

        session.setAnswer(0, "Synthetic: edited on the phone.")
        let saved = await session.save()
        XCTAssertTrue(saved, session.failure ?? "")

        let after = try await model.entry(on: "2026-03-05")
        XCTAssertEqual(after.sections.map(\.heading), before.sections.map(\.heading))
        XCTAssertEqual(after.sections.first { $0.heading == "Gratitude" }, before.sections.first { $0.heading == "Gratitude" })
        XCTAssertEqual(after.sections.first { $0.followThrough != nil }, before.sections.first { $0.followThrough != nil }, "the stored follow-through is kept as it was")
        XCTAssertEqual(after.sections.first { $0.heading == "What did I notice today?" }?.body, "Synthetic: edited on the phone.")
        XCTAssertEqual(after.raw, before.raw.replacingOccurrences(of: "Synthetic: energy dipped after lunch.", with: "Synthetic: edited on the phone."))
    }

    func testAnEarlierDayIsWrittenInItsPlaceAndALaterOneRefused() async throws {
        let model = await launch()
        let session = try XCTUnwrap(model.session(on: "2026-03-20"))
        await session.load()
        XCTAssertEqual(session.phase, .writing)
        XCTAssertEqual(session.draft?.isEdit, false)
        session.setAnswer(0, "Synthetic: written two weeks late.")
        let saved = await session.save()
        XCTAssertTrue(saved, session.failure ?? "")
        await model.refresh()
        let days = model.entries.map(\.date)
        XCTAssertEqual(days, days.sorted(by: >), "the earlier day sits in its place")
        XCTAssertTrue(days.contains("2026-03-20"))

        let tomorrow = try XCTUnwrap(model.session(on: "2026-04-03"))
        await tomorrow.load()
        XCTAssertEqual(tomorrow.phase, .unavailable)
        XCTAssertNotNil(tomorrow.failure)
    }

    func testAnswersFollowTheirQuestionsWhenTheSettingsChange() async throws {
        let scenario = roundTrip.scenario
        let model = await launch()
        let session = try XCTUnwrap(model.session(on: scenario.date))
        await session.load()
        write(session, scenario.answers)
        let before = try XCTUnwrap(try store.read(scenario.date))

        // The journal's questions change: one is dropped, the others swap.
        let engine = SenecaEngine()
        try await engine.open(location)
        let initialized = try await engine.initializeSettings().value.settings
        let asked = [scenario.questions[2], scenario.questions[0]]
        _ = try await engine.updateSettings(base: initialized.revision, questions: asked, anchorQuestionIndex: 1)

        let relaunched = await launch()
        let again = try XCTUnwrap(relaunched.session(on: scenario.date))
        await again.load()
        XCTAssertEqual(again.draft?.questions, asked)
        XCTAssertNotEqual(again.draft?.settingsRevision, before.draft.settingsRevision)
        XCTAssertEqual(again.notice, .changedUnderneath)
        XCTAssertEqual(again.answers, [scenario.answers[2], scenario.answers[0]], "each answer stays with its question")
        XCTAssertEqual(again.unasked, [UnaskedAnswer(question: scenario.questions[1], answer: scenario.answers[1])], "an answer with no question left is kept and shown")
        XCTAssertEqual(try store.read(scenario.date)?.unasked, again.unasked)

        let saved = await again.save()
        XCTAssertTrue(saved, again.failure ?? "")
        XCTAssertNil(try store.read(scenario.date))
    }

    func testReaddedQuestionRestoresUnaskedAnswer() async throws {
        let scenario = roundTrip.scenario
        let model = await launch()
        let session = try XCTUnwrap(model.session(on: scenario.date))
        await session.load()
        write(session, scenario.answers)

        // One question is dropped, so its answer is set aside.
        let engine = SenecaEngine()
        try await engine.open(location)
        let initialized = try await engine.initializeSettings().value.settings
        let fewer = [scenario.questions[0], scenario.questions[2]]
        let removed = try await engine.updateSettings(base: initialized.revision, questions: fewer, anchorQuestionIndex: 1).value.settings

        let dropped = await launch()
        let aside = try XCTUnwrap(dropped.session(on: scenario.date))
        await aside.load()
        XCTAssertEqual(aside.answers, [scenario.answers[0], scenario.answers[2]])
        XCTAssertEqual(aside.unasked, [UnaskedAnswer(question: scenario.questions[1], answer: scenario.answers[1])])
        XCTAssertEqual(try store.read(scenario.date)?.unasked, aside.unasked)

        // The question returns: its answer goes back where it was.
        _ = try await engine.updateSettings(base: removed.revision, questions: scenario.questions, anchorQuestionIndex: initialized.anchorQuestionIndex)

        let readded = await launch()
        let back = try XCTUnwrap(readded.session(on: scenario.date))
        await back.load()
        XCTAssertEqual(back.draft?.questions, scenario.questions)
        XCTAssertEqual(back.answers, scenario.answers, "the set-aside answer returns to its question")
        XCTAssertEqual(back.unasked, [])
        let onDisk = try XCTUnwrap(try store.read(scenario.date))
        XCTAssertEqual(onDisk.draft.questions, scenario.questions)
        XCTAssertEqual(onDisk.answers, scenario.answers)
        XCTAssertEqual(onDisk.unasked, [])

        // And it is still there the next time the day is opened.
        let reopened = await launch()
        let again = try XCTUnwrap(reopened.session(on: scenario.date))
        await again.load()
        XCTAssertEqual(again.answers, scenario.answers)
        XCTAssertEqual(again.unasked, [])
        XCTAssertEqual(try store.read(scenario.date)?.answers, scenario.answers)

        let saved = await again.save()
        XCTAssertTrue(saved, again.failure ?? "")
        XCTAssertNil(try store.read(scenario.date))
    }

    func testRestoredFollowThroughKeepsOriginalContext() async throws {
        let scenario = roundTrip.scenario
        let model = await launch()
        let session = try XCTUnwrap(model.session(on: scenario.date))
        await session.load()
        let seen = try XCTUnwrap(session.draft?.context)
        XCTAssertTrue(seen.showFollowThrough)
        XCTAssertEqual(seen.priorText, "Walk before the dip, not after.")
        write(session, scenario.answers, followThrough: scenario.followThrough)

        // Another day with an intention is written between the two before
        // this draft is opened again.
        let intervening = try XCTUnwrap(model.session(on: "2026-03-20"))
        await intervening.load()
        intervening.setAnswer(2, "Synthetic: finish the tax return.")
        let wrote = await intervening.save()
        XCTAssertTrue(wrote, intervening.failure ?? "")
        let engine = SenecaEngine()
        try await engine.open(location)
        let fresh = try await engine.prepareReflection(on: scenario.date).draft.context
        XCTAssertEqual(fresh.priorDate, "2026-03-20", "the journal now offers the later intention")

        let relaunched = await launch()
        let again = try XCTUnwrap(relaunched.session(on: scenario.date))
        await again.load()
        XCTAssertEqual(again.notice, .changedUnderneath, "the newer intention is not taken over silently")
        XCTAssertEqual(again.followThrough, scenario.followThrough)
        XCTAssertEqual(again.draft?.context.showFollowThrough, true)
        XCTAssertEqual(again.draft?.context.priorDate, seen.priorDate, "the response stays with the day it answered")
        XCTAssertEqual(again.draft?.context.priorText, seen.priorText)
        let onDisk = try XCTUnwrap(try store.read(scenario.date))
        XCTAssertEqual(onDisk.followThrough, scenario.followThrough)
        XCTAssertEqual(onDisk.draft.context.priorDate, seen.priorDate)
        XCTAssertEqual(onDisk.draft.context.priorText, seen.priorText)

        let saved = await again.save()
        XCTAssertTrue(saved, again.failure ?? "")
        XCTAssertNil(try store.read(scenario.date))
        let entry = try await engine.entry(on: scenario.date)
        let follow = try XCTUnwrap(entry.sections.compactMap(\.followThrough).first)
        XCTAssertEqual(follow.sourceDate, seen.priorDate)
        XCTAssertEqual(follow.sourceIntention, seen.priorText)
        XCTAssertEqual(follow.response, scenario.followThrough)
    }

    func testHiddenFollowThroughDoesNotDiscardDraft() async throws {
        let scenario = roundTrip.scenario
        let model = await launch()
        let session = try XCTUnwrap(model.session(on: scenario.date))
        await session.load()
        let seen = try XCTUnwrap(session.draft?.context)
        XCTAssertTrue(seen.showFollowThrough)
        session.setFollowThrough(scenario.followThrough)
        XCTAssertEqual(try store.read(scenario.date)?.followThrough, scenario.followThrough)

        // The anchor is turned off, so the journal offers no follow-through.
        let engine = SenecaEngine()
        try await engine.open(location)
        let initialized = try await engine.initializeSettings().value.settings
        _ = try await engine.updateSettings(base: initialized.revision, questions: scenario.questions, anchorQuestionIndex: 0)
        let fresh = try await engine.prepareReflection(on: scenario.date).draft.context
        XCTAssertFalse(fresh.showFollowThrough)

        for _ in 0..<2 {
            let relaunched = await launch()
            let again = try XCTUnwrap(relaunched.session(on: scenario.date))
            await again.load()
            XCTAssertEqual(again.notice, .changedUnderneath)
            XCTAssertTrue(again.dirty, "a hidden field is no sign the journal holds the response")
            XCTAssertEqual(again.answers, ["", "", ""])
            XCTAssertEqual(again.followThrough, scenario.followThrough)
            XCTAssertEqual(again.draft?.context.showFollowThrough, true)
            XCTAssertEqual(again.draft?.context.priorDate, seen.priorDate)
            XCTAssertEqual(again.draft?.context.priorText, seen.priorText)
            let onDisk = try XCTUnwrap(try store.read(scenario.date), "the response is still kept on the device")
            XCTAssertEqual(onDisk.followThrough, scenario.followThrough)
            XCTAssertEqual(onDisk.draft.context.showFollowThrough, true)
            XCTAssertEqual(onDisk.draft.context.priorDate, seen.priorDate)
            XCTAssertEqual(onDisk.draft.context.priorText, seen.priorText)
        }
        let entry = try? await engine.entry(on: scenario.date)
        XCTAssertNil(entry, "nothing was saved")
    }

    func testADraftThatCannotBeReadNeverStandsInTheWayOfWriting() async throws {
        let scenario = roundTrip.scenario
        let drafts = AppPaths.draftsDirectory(for: location)
        try FileManager.default.createDirectory(at: drafts, withIntermediateDirectories: true)
        try Data("{ not a draft".utf8).write(to: drafts.appendingPathComponent("\(scenario.date).json"))

        let model = await launch()
        XCTAssertTrue(model.drafts.isEmpty)
        let session = try XCTUnwrap(model.session(on: scenario.date))
        await session.load()
        XCTAssertEqual(session.phase, .writing)
        XCTAssertNotNil(session.draftProblem, "the unreadable draft is reported")
        XCTAssertEqual(session.answers, ["", "", ""])
    }
}
