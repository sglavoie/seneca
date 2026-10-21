import SenecaEngine
import XCTest
@testable import SenecaMobile

/// History, reviews, the focus, and the questions, as the app's model reads
/// and changes them through the real engine. The journal is the browsing
/// fixture: four months of writing and a review of every period, two of them
/// revised.
@MainActor
final class BrowsingTests: XCTestCase {
    private var roundTrip: RoundTrip!
    private var location: JournalLocation!

    override func setUp() async throws {
        roundTrip = try RoundTrip.load()
        location = try StagedFixture.stage("browsing", in: try scratchDirectory(self), with: roundTrip).location
    }

    private func launch(file: StaticString = #filePath, line: UInt = #line) async -> JournalModel {
        let location = location!
        let model = JournalModel(engine: SenecaEngine(), location: { location })
        await model.open()
        XCTAssertEqual(model.phase, .ready, file: file, line: line)
        return model
    }

    private func days(_ list: EntryList) -> [String] { list.entries.map(\.date) }

    func testHistoryIsFilteredNavigatedAndSearchedByTheEnginesQueries() async {
        await reportingOnMain {
            let model = await launch()
            XCTAssertEqual(model.entries.map(\.date), ["2026-03-05", "2026-03-04", "2026-03-03", "2026-03-02", "2026-02-02", "2026-01-02"],
                           "newest first, whatever order the file keeps")

            let march = try await model.entries(.month("2026-03"))
            XCTAssertEqual(days(march), ["2026-03-05", "2026-03-04", "2026-03-03", "2026-03-02"])
            XCTAssertEqual(march.total, 6)
            XCTAssertEqual(march.nearestBefore, "2026-02-02")
            XCTAssertNil(march.nearestAfter)

            // Stepping a month at a time from today's.
            let april = try await model.entries(.month(DayText.month(of: roundTrip.scenario.date)))
            XCTAssertEqual(DayText.month(DayText.month(of: roundTrip.scenario.date), offset: -2), "2026-02")
            XCTAssertTrue(april.entries.isEmpty)
            XCTAssertEqual(april.nearestBefore, "2026-03-05", "an empty month says where the writing is")

            let range = try await model.entries(.range(from: "2026-02-01", to: "2026-03-03"))
            XCTAssertEqual(days(range), ["2026-03-03", "2026-03-02", "2026-02-02"])
            XCTAssertEqual(range.nearestBefore, "2026-01-02")
            XCTAssertEqual(range.nearestAfter, "2026-03-04")

            let onThisDay = try await model.entries(.onThisDay)
            XCTAssertEqual(days(onThisDay), ["2026-03-02", "2026-02-02", "2026-01-02"])

            let walks = try await model.search("WALK")
            XCTAssertEqual(walks.map(\.date), ["2026-03-05", "2026-01-02"], "hits are newest first")
            let januaryWalks = try await model.search("walk", in: .range(from: "2026-01-01", to: "2026-01-31"))
            XCTAssertEqual(januaryWalks.map(\.date), ["2026-01-02"])
            XCTAssertEqual(januaryWalks.first?.snippets, ["Synthetic: kept the walk.", "Walk again tomorrow."])

            await refusedOnMain("invalid-query") { try await model.entries(EntryQuery(from: "2026-03-05", to: "2026-03-01")) }
            await refusedOnMain("invalid-query") { try await model.entries(EntryQuery(month: "2026-03", onThisDay: true)) }
        }
    }

    func testDeletionRefreshesHistoryAndRefusesAStaleEntry() async {
        await reportingOnMain {
            let model = await launch()
            let day = "2026-03-05"
            let shown = try await model.entry(on: day)
            let elsewhere = SenecaEngine()
            try await elsewhere.open(location)
            let fresh = try await elsewhere.prepareReflection(on: day, blank: true)
            _ = try await elsewhere.saveReflection(ReflectionWriting(
                draft: fresh.draft, answers: ["Changed elsewhere.", "", ""], followThrough: ""
            ))
            await refusedOnMain("stale-reflection") {
                try await model.deleteEntry(on: day, base: shown.revision, requestID: RequestID())
            }
            let updated = try await model.entry(on: day)
            XCTAssertTrue(updated.raw.contains("Changed elsewhere."))
            let count = model.entries.count
            let id = RequestID()
            try await model.deleteEntry(on: day, base: updated.revision, requestID: id)
            XCTAssertFalse(model.hasEntry(on: day))
            XCTAssertEqual(model.entries.count, count - 1)
            XCTAssertEqual(model.status?.entryCount, count - 1)
            await refusedOnMain("not-found") { try await model.entry(on: day) }
            try await model.deleteEntry(on: day, base: updated.revision, requestID: id)
            XCTAssertEqual(model.entries.count, count - 1)
        }
    }

    func testEverySavedReviewPeriodIsShownWithItsRevisions() async {
        await reportingOnMain {
            let model = await launch()
            XCTAssertEqual(model.reviews.count, 6)
            var counts: [String: Int] = [:]
            for period in ReviewText.periods {
                let reviews = try await model.reviews(period: period)
                XCTAssertTrue(reviews.allSatisfy { $0.period == period })
                counts[period] = reviews.count
            }
            XCTAssertEqual(counts, ["weekly": 2, "monthly": 1, "quarterly": 2, "yearly": 1])

            let quarterly = try await model.reviews(period: "quarterly")
            let revised = try XCTUnwrap(quarterly.first { $0.superseded })
            let revision = try XCTUnwrap(quarterly.first { !$0.superseded })
            XCTAssertEqual(revision.revises, revised.id)
            XCTAssertEqual(revised.revisedBy, [revision.id])
            let earlier = try await model.review(id: revision.revises)
            XCTAssertEqual(earlier.body, "Synthetic quarterly review.")
            let later = try await model.review(id: try XCTUnwrap(earlier.revisedBy.first))
            XCTAssertEqual(later.body, "Synthetic quarterly review, revised the next morning.")
            let yearly = try await model.reviews(period: "yearly")
            XCTAssertEqual(yearly.first?.start, "2025-01-01")
            await refusedOnMain("invalid-query") { try await model.reviews(period: "daily") }
        }
    }

    func testTheFocusIsSetAndClearedOnlyFromTheTimelineShown() async {
        await reportingOnMain {
            let model = await launch()
            let archive = location.reflections.deletingLastPathComponent().appendingPathComponent("seneca-reviews.md")
            XCTAssertEqual(model.focus?.text, "Finish before starting.")
            let events = model.focusTimeline.count

            try await model.setFocus("Synthetic: one thing at a time.", requestID: RequestID())
            XCTAssertEqual(model.focus?.text, "Synthetic: one thing at a time.")
            XCTAssertEqual(model.focusTimeline.count, events + 1)
            XCTAssertTrue(try String(contentsOf: archive, encoding: .utf8).contains("Synthetic: one thing at a time."))

            try await model.clearFocus(requestID: RequestID())
            XCTAssertEqual(model.focus?.active, false)
            XCTAssertEqual(model.focusTimeline.last?.event, "clear")

            // A change made elsewhere after the timeline was read is refused,
            // and nothing is written.
            let elsewhere = SenecaEngine()
            try await elsewhere.open(location)
            let shown = try await elsewhere.focus()
            _ = try await elsewhere.setFocus("Synthetic: set on another screen.", base: shown.revision)
            let before = try Data(contentsOf: archive)
            await refusedOnMain("stale-focus") { try await model.setFocus("Synthetic: decided on old news.", requestID: RequestID()) }
            XCTAssertEqual(try Data(contentsOf: archive), before)
            try await model.reload()
            XCTAssertEqual(model.focus?.text, "Synthetic: set on another screen.")
        }
    }

    func testTheQuestionsAndTheAnchorChangeAsOneUnit() async {
        await reportingOnMain {
            let model = await launch()
            let shown = try XCTUnwrap(model.status?.settings)
            XCTAssertFalse(shown.initialized)
            var questions = shown.questions
            questions.insert("What surprised me?", at: 0)

            // The journal gets settings of its own, then the change, at once.
            try await model.updateSettings(base: shown.revision, questions: questions, anchor: 4, requestID: RequestID())
            let changed = try XCTUnwrap(model.status?.settings)
            XCTAssertTrue(changed.initialized)
            XCTAssertEqual(changed.questions, questions)
            XCTAssertEqual(changed.anchorQuestionIndex, 4)
            let document = location.reflections.deletingLastPathComponent()
                .appendingPathComponent(".seneca/journals/reflections.md/settings.json")
            XCTAssertTrue(try String(contentsOf: document, encoding: .utf8).contains("What surprised me?"))

            // Today's form asks the new questions.
            let session = try XCTUnwrap(model.session(on: roundTrip.scenario.date))
            await session.load()
            XCTAssertEqual(session.draft?.questions, questions)

            // A change decided on the old questions is refused whole.
            await refusedOnMain("stale-settings") {
                try await model.updateSettings(base: shown.revision, questions: shown.questions, anchor: 1, requestID: RequestID())
            }
            XCTAssertEqual(model.status?.settings, changed)
            await refusedOnMain("refused") {
                try await model.updateSettings(base: changed.revision, questions: ["", "Only one"], anchor: 1, requestID: RequestID())
            }
        }
    }
}
