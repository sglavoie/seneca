import XCTest

/// Conflicts through the app's own screens, over the conflicted fixture: this
/// iPhone and a Mac wrote 2026-04-02 differently and set a different focus
/// that day, and the exchange both published to is the folder the app is
/// given.
final class ConflictUITests: StagedJournalTestCase {
    override class var fixture: String { "conflicted" }

    private var day: String { scenario.date }

    /// This iPhone's manifests in the exchange.
    private func published() -> [String] {
        let phone = exchange.appendingPathComponent("transactions/c9cacbcccdcecfd0d1d2d3d4d5d6d7d8", isDirectory: true)
        return ((try? FileManager.default.contentsOfDirectory(atPath: phone.path)) ?? []).sorted()
    }

    func testAConflictIsInspectedAndSettledByKeepingAVersion() throws {
        try launch()
        let before = published()
        tap("today-conflicts")
        tap("conflict-\(day)")
        XCTAssertTrue(reach("version-ours").label.contains("from the phone"))
        XCTAssertTrue(reach("version-theirs-0").label.contains("from the Mac"))
        XCTAssertEqual(reach("version-base").label, "Neither device had written this before.")

        tap("keep-theirs-0")
        confirm("Keep")
        assertExists("dispute-\(day)")
        XCTAssertFalse(element("conflict-\(day)").exists, "the settled day is no longer listed")
        let journal = try journalText()
        XCTAssertTrue(journal.contains("noticed the light at five, from the Mac."))
        XCTAssertFalse(journal.contains("noticed the rain, from the phone."))

        // The answer goes to the other devices.
        let deadline = Date().addingTimeInterval(60)
        while published() == before, Date() < deadline {
            Thread.sleep(forTimeInterval: 0.2)
        }
        XCTAssertGreaterThan(published().count, before.count, "the resolution was published to the exchange")

        let focusRecords = try archiveText().components(separatedBy: "<!-- seneca:focus ").count
        tap("dispute-\(day)")
        tap("keep-focus-0")
        assertExists("conflicts-none")
        XCTAssertEqual(try archiveText().components(separatedBy: "<!-- seneca:focus ").count, focusRecords + 1,
                       "settling the focus appends one record and rewrites none")
    }

    func testWritingForAConflictedDayStaysADraftUntilItIsSettledAndEdited() throws {
        try launch()
        app.tabBars.buttons["History"].tap()
        XCTAssertTrue(reach("entry-\(day)").label.contains("Two versions"))
        tap("entry-\(day)")
        assertExists("entry-conflicted")
        XCTAssertTrue(app.staticTexts["Synthetic: noticed the rain, from the phone."].waitForExistence(timeout: 30),
                      "this iPhone's version stays readable")

        tap("edit-entry")
        assertExists("notice-conflicted")
        XCTAssertFalse(reach("save").isEnabled, "nothing is saved for a day waiting for a choice")
        append(" Typed while conflicted.", into: "answer-1")
        XCTAssertEqual(draftFiles(), ["\(day).json"])
        let before = try journalText()
        kill()
        XCTAssertEqual(try journalText(), before, "the writing is a draft, not the journal")

        // After a relaunch the author settles the day by editing, starting
        // from the Mac's version.
        try launch()
        app.tabBars.buttons["History"].tap()
        tap("entry-\(day)")
        tap("entry-choose-version")
        tap("edit-theirs-0")
        confirm("Keep and Edit")
        assertExists("reference")
        assertExists("notice-changed")
        XCTAssertFalse(element("notice-conflicted").exists)
        let kept = text(of: "answer-1")
        XCTAssertTrue(kept.contains("Typed while conflicted.") && kept.count > "Typed while conflicted.".count + 10,
                      "the draft came back whole: \(kept)")
        XCTAssertTrue(reach("journal-now-1").label.contains("Synthetic: closed the laptop on time."), "the version kept is shown beside it")
        tap("save")
        XCTAssertTrue(element("save").waitForNonExistence(timeout: 30))
        XCTAssertEqual(draftFiles(), [])
        let journal = try journalText()
        XCTAssertTrue(journal.contains("Typed while conflicted."))
        app.tabBars.buttons["History"].tap()
        XCTAssertFalse(reach("entry-\(day)").label.contains("Two versions"))
    }
}
