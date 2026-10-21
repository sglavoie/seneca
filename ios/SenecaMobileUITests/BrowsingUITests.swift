import XCTest

/// History, reviews, the focus, and the questions, through the app's own
/// screens, over the browsing fixture: writing in January, February, and
/// March, and a review of every period, the weekly and the quarterly one
/// each saved twice.
final class BrowsingUITests: StagedJournalTestCase {
    override class var fixture: String { "browsing" }

    private func choose(_ option: String, in picker: String) {
        tap(picker)
        let item = app.buttons[option].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 30), "\(picker) offers no \(option)")
        item.tap()
    }

    func testHistoryIsFilteredByMonthRangeAndDateAndSearched() throws {
        try launch()
        app.tabBars.buttons["History"].tap()
        for day in ["2026-03-05", "2026-02-02", "2026-01-02"] {
            assertExists("entry-\(day)")
        }

        // A month at a time, from today's, which has nothing written yet.
        choose("Month", in: "history-scope")
        XCTAssertEqual(reach("history-month").label, "April 2026")
        assertExists("history-empty")
        XCTAssertTrue(reach("history-nearest-before").label.contains("March 5, 2026"))
        XCTAssertFalse(element("history-nearest-after").exists)
        XCTAssertFalse(reach("history-next-month").isEnabled, "no month after today's")
        tap("history-previous-month")
        XCTAssertEqual(reach("history-month").label, "March 2026")
        assertExists("entry-2026-03-02")
        XCTAssertFalse(element("entry-2026-02-02").exists)
        tap("history-previous-month")
        XCTAssertEqual(reach("history-month").label, "February 2026")
        assertExists("entry-2026-02-02")
        XCTAssertFalse(element("entry-2026-03-02").exists)

        // The earlier days written on today's date.
        choose("On This Day", in: "history-scope")
        for day in ["2026-03-02", "2026-02-02", "2026-01-02"] {
            assertExists("entry-\(day)")
        }
        XCTAssertFalse(element("entry-2026-03-05").exists)

        // A search covers what is shown.
        choose("All", in: "history-scope")
        let search = app.searchFields.firstMatch
        if !search.waitForExistence(timeout: 5) {
            app.swipeDown(velocity: .slow)
        }
        XCTAssertTrue(search.waitForExistence(timeout: 30))
        search.tap()
        search.typeText("walk")
        assertExists("hit-2026-01-02")
        assertExists("hit-2026-03-05")
        tap("hit-2026-01-02")
        XCTAssertTrue(app.staticTexts["Synthetic: kept the walk."].waitForExistence(timeout: 30))
    }

    func testEveryReviewPeriodAndEachRevisionCanBeRead() throws {
        try launch()
        app.tabBars.buttons["Reviews"].tap()
        let rows = app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH 'review-'"))
        XCTAssertTrue(rows.firstMatch.waitForExistence(timeout: 30))
        XCTAssertEqual(rows.count, 6, "two weekly, one monthly, two quarterly, and one yearly review")

        for (period, count) in [("Yearly", 1), ("Monthly", 1), ("Weekly", 2), ("Quarterly", 2)] {
            choose(period, in: "reviews-period")
            let first = rows.firstMatch
            XCTAssertTrue(first.waitForExistence(timeout: 30))
            let shown = app.descendants(matching: .any)
                .matching(NSPredicate(format: "identifier BEGINSWITH 'review-' AND label CONTAINS %@", "\(period) review"))
            XCTAssertTrue(shown.firstMatch.waitForExistence(timeout: 30), period)
            XCTAssertEqual(shown.count, count, period)
        }

        // The first quarterly review, revised the next morning, and back.
        let revised = app.descendants(matching: .any)
            .matching(NSPredicate(format: "identifier BEGINSWITH 'review-' AND label CONTAINS 'Revised by a later review'")).firstMatch
        XCTAssertTrue(revised.waitForExistence(timeout: 30))
        revised.tap()
        XCTAssertEqual(reach("review-body").label, "Synthetic quarterly review.")
        tap("review-revised-by-0")
        XCTAssertEqual(reach("review-body").label, "Synthetic quarterly review, revised the next morning.")
        tap("review-revises")
        XCTAssertEqual(reach("review-body").label, "Synthetic quarterly review.")
    }

    func testTheFocusIsSetAndClearedAndItsHistoryKept() throws {
        try launch()
        tap("focus-link")
        XCTAssertEqual(reach("focus-text").label, "Finish before starting.")
        tap("focus-set")
        type("Synthetic: one thing at a time.", into: "focus-field")
        tap("focus-save")
        XCTAssertTrue(app.staticTexts["Synthetic: one thing at a time."].waitForExistence(timeout: 30))
        XCTAssertTrue(try archiveText().contains("Synthetic: one thing at a time."), "the archive holds the new focus")

        tap("focus-clear")
        confirm("Clear")
        assertExists("focus-none")
        XCTAssertTrue(try archiveText().contains("event=clear effective=\(scenario.date)"), "the archive holds the clear")
        // Both changes are in the history shown.
        XCTAssertTrue(app.staticTexts["Synthetic: one thing at a time."].exists)
        XCTAssertTrue(app.staticTexts["Focus cleared"].firstMatch.exists)
        back()
        XCTAssertTrue(reach("focus-link").label.contains("No focus"))
    }

    func testTheQuestionsAndTheAnchorAreEditedTogether() throws {
        try launch()
        tap("settings")
        tap("edit-questions")
        tap("add-question")
        type("Synthetic: what surprised me?", into: "question-field-3")
        choose("Synthetic: what surprised me?", in: "anchor-picker")
        tap("questions-save")

        let added = app.descendants(matching: .any)
            .matching(NSPredicate(format: "identifier == 'setting-question-3' AND label CONTAINS 'Synthetic: what surprised me?'")).firstMatch
        XCTAssertTrue(added.waitForExistence(timeout: 30), "the settings list the new question")
        XCTAssertTrue(added.label.contains("Carried into the next day"), "the new question carries the intention")
        let document = try settingsText()
        XCTAssertTrue(document.contains("Synthetic: what surprised me?"))
        XCTAssertTrue(document.contains("\"anchor_question_index\": 4"), document)
        tap("settings-done")

        // Today's form asks it.
        tap("write-today")
        XCTAssertEqual(reach("question-3").label, "Synthetic: what surprised me?")
    }
}
