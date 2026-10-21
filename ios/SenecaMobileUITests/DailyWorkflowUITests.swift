import XCTest

/// The completion gate of the daily workflow, through the app's own screens:
/// writing survives the app being stopped, a stale edit keeps every typed
/// word, and an unreadable archive still lets the day be written.
final class DailyWorkflowUITests: StagedJournalTestCase {
    func testWritingSurvivesForcedTermination() throws {
        try launch()
        tap("write-today")
        type("Synthetic: walked at noon, typed before the stop.", into: "follow-through")
        type("Synthetic: typed before the app was stopped.", into: "answer-0")
        type("Synthetic: finish the one thing.", into: "answer-2")
        XCTAssertEqual(draftFiles(), ["\(scenario.date).json"], "the writing is on disk before anything is told to save it")
        let before = try journalText()

        kill()
        XCTAssertEqual(try journalText(), before, "a draft is not the journal")

        try launch()
        tap("continue-draft")
        assertExists("notice-restored")
        XCTAssertEqual(text(of: "follow-through"), "Synthetic: walked at noon, typed before the stop.")
        XCTAssertEqual(text(of: "answer-0"), "Synthetic: typed before the app was stopped.")
        XCTAssertEqual(text(of: "answer-2"), "Synthetic: finish the one thing.")

        // Dismissed and opened again: still there.
        tap("close")
        app.buttons["Keep Draft"].firstMatch.tap()
        tap("continue-draft")
        XCTAssertEqual(text(of: "answer-0"), "Synthetic: typed before the app was stopped.")
        XCTAssertEqual(draftFiles().count, 1)

        tap("save")
        assertExists("edit-today")
        XCTAssertEqual(draftFiles(), [], "the committed save cleared the draft")
        let after = try journalText()
        XCTAssertTrue(after.contains("## \(scenario.date)"))
        XCTAssertTrue(after.contains("Synthetic: typed before the app was stopped."))
        XCTAssertTrue(after.contains("Synthetic: walked at noon, typed before the stop."))
        XCTAssertTrue(after.contains("Synthetic: finish the one thing."))

        app.tabBars.buttons["History"].tap()
        assertExists("entry-\(scenario.date)")
    }

    func testTodaysEntryCanBeDeletedAfterCancellingTheConfirmation() throws {
        try launch()
        tap("write-today")
        type("Synthetic: delete today's entry.", into: "answer-0")
        tap("save")
        assertExists("edit-today")
        let before = try journalText()

        tap("delete-entry")
        app.buttons["Cancel"].firstMatch.tap()
        XCTAssertEqual(try journalText(), before)
        assertExists("edit-today")

        tap("delete-entry")
        tap("delete-entry-confirm")
        assertExists("write-today")
        XCTAssertFalse(try journalText().contains("## \(scenario.date)"))
        XCTAssertTrue(try journalText().contains("## 2026-03-05"))
        kill()
        try launch()
        assertExists("write-today")
        app.tabBars.buttons["History"].tap()
        XCTAssertFalse(element("entry-\(scenario.date)").exists)
    }

    func testSavedEntryCanBeDeletedFromHistory() throws {
        try launch()
        app.tabBars.buttons["History"].tap()
        tap("entry-2026-03-05")
        tap("delete-entry")
        tap("delete-entry-confirm")
        assertExists("entry-2026-03-04")
        XCTAssertFalse(element("entry-2026-03-05").exists)
        XCTAssertFalse(try journalText().contains("## 2026-03-05"))
    }

    func testDeletionRefusesAnEntryChangedDuringConfirmation() throws {
        try launch()
        app.tabBars.buttons["History"].tap()
        tap("entry-2026-03-05")
        tap("delete-entry")
        let heading = "### \(scenario.questions[0])\n"
        let changed = try journalText().replacingOccurrences(
            of: heading, with: heading + "Synthetic: changed while confirmation was open.\n"
        )
        try Data(changed.utf8).write(to: reflections)
        tap("delete-entry-confirm")
        XCTAssertTrue(app.alerts["Entry Could Not Be Deleted"].waitForExistence(timeout: 10))
        XCTAssertEqual(try journalText(), changed)
        app.alerts.buttons["OK"].tap()
        assertExists("edit-entry")
    }

    func testADiscardedDraftIsGone() throws {
        try launch()
        tap("write-today")
        type("Synthetic: not worth keeping.", into: "answer-0")
        XCTAssertEqual(draftFiles().count, 1)
        tap("close")
        app.buttons["Discard Draft"].firstMatch.tap()
        assertExists("write-today")
        XCTAssertEqual(draftFiles(), [])
        kill()
        try launch()
        assertExists("write-today")
        XCTAssertFalse(element("continue-draft").exists)
    }

    func testAStaleEditKeepsAllTypedText() throws {
        try launch()
        tap("write-today")
        type("Synthetic: typed on the phone, one.", into: "answer-0")
        type("Synthetic: typed on the phone, two.", into: "answer-1")

        // The day is written from elsewhere while the form is open.
        let theirs = "## \(scenario.date)\n\n### \(scenario.questions[0])\nSynthetic: written on the Mac meanwhile.\n---\n\n"
        let handle = try FileHandle(forWritingTo: reflections)
        try handle.seekToEnd()
        try handle.write(contentsOf: Data(theirs.utf8))
        try handle.close()

        tap("save")
        assertExists("notice-changed")
        XCTAssertEqual(text(of: "answer-0"), "Synthetic: typed on the phone, one.")
        XCTAssertEqual(text(of: "answer-1"), "Synthetic: typed on the phone, two.")
        XCTAssertTrue(reach("journal-now-0").label.contains("Synthetic: written on the Mac meanwhile."), "the journal's version is shown beside the typed one")
        var journal = try journalText()
        XCTAssertTrue(journal.contains("Synthetic: written on the Mac meanwhile."), "the refused save wrote nothing")
        XCTAssertFalse(journal.contains("typed on the phone"))
        XCTAssertEqual(draftFiles().count, 1)

        // Stopped before the author decides: the text and the warning return.
        kill()
        try launch()
        tap("continue-draft")
        assertExists("notice-changed")
        XCTAssertEqual(text(of: "answer-0"), "Synthetic: typed on the phone, one.")
        XCTAssertEqual(text(of: "answer-1"), "Synthetic: typed on the phone, two.")

        tap("save")
        assertExists("edit-today")
        journal = try journalText()
        XCTAssertTrue(journal.contains("Synthetic: typed on the phone, one."))
        XCTAssertTrue(journal.contains("Synthetic: typed on the phone, two."))
        XCTAssertEqual(draftFiles(), [])
    }

    func testACorruptArchiveStillPermitsWritingWithoutContext() throws {
        // The archive as an interrupted copy leaves it.
        let whole = String(decoding: try Data(contentsOf: archive), as: UTF8.self)
        let marker = try XCTUnwrap(whole.range(of: "<!-- seneca:focus v=3 id="))
        var torn = Data(whole[..<marker.upperBound].utf8)
        torn.append(contentsOf: [0x39, 0x33, 0x65, 0x00, 0xff, 0xfe])
        try torn.write(to: archive)

        try launch()
        assertExists("context-problem")

        // The ordinary way in says why and offers the way around.
        tap("write-today")
        assertExists("failure")
        tap("form-write-blank")
        assertExists("answer-0")
        XCTAssertFalse(element("context-focus").exists)
        XCTAssertFalse(element("follow-through").exists)
        type("Synthetic: written with the archive unreadable.", into: "answer-0")

        // Stopped: the draft comes back, still without context.
        kill()
        try launch()
        tap("continue-draft")
        XCTAssertEqual(text(of: "answer-0"), "Synthetic: written with the archive unreadable.")
        XCTAssertFalse(element("failure").exists)
        tap("save")
        assertExists("edit-today")
        XCTAssertTrue(try journalText().contains("Synthetic: written with the archive unreadable."))
        XCTAssertEqual(try Data(contentsOf: archive), torn, "the unreadable archive is left as it was")
        XCTAssertEqual(draftFiles(), [])

        // The rest of the journal is still there to read.
        app.tabBars.buttons["History"].tap()
        assertExists("entry-\(scenario.date)")
        assertExists("entry-2026-03-05")
        app.tabBars.buttons["Reviews"].tap()
        assertExists("reviews-problem")
    }

    func testAnExistingDayIsEditedAndKeepsWhatNoQuestionNames() throws {
        try launch()
        app.tabBars.buttons["History"].tap()
        tap("entry-2026-03-05")
        tap("edit-entry")
        XCTAssertEqual(text(of: "answer-0"), "Synthetic: energy dipped after lunch.")
        XCTAssertEqual(text(of: "follow-through"), "Synthetic: the draft was done by eleven.")
        XCTAssertTrue(reach("additional-0").label.contains("Synthetic: a section no configured question names."))
        // Nothing of the file's structure reaches the form.
        let structure = NSPredicate(format: "label CONTAINS 'seneca:' OR label CONTAINS '###' OR value CONTAINS 'seneca:' OR value CONTAINS '###'")
        XCTAssertEqual(app.descendants(matching: .any).matching(structure).count, 0)

        type(" Synthetic: added on the phone.", into: "answer-1")
        tap("save")
        XCTAssertTrue(element("edit-entry").waitForExistence(timeout: 30))
        let journal = try journalText()
        XCTAssertTrue(journal.contains("Synthetic: added on the phone."))
        XCTAssertTrue(journal.contains("### Gratitude\nSynthetic: a section no configured question names."))
        XCTAssertTrue(journal.contains("<!-- seneca:follow-through source=2026-03-04 -->"))
    }

    func testAnotherDayCanBeWrittenFromToday() throws {
        try launch()
        tap("backfill")
        assertExists("day-picker")
        tap("day-write")
        assertExists("answer-0")
        tap("close")
        assertExists("backfill")
    }
}
