import XCTest

/// The form at the largest text size, read by its labels alone, and worked
/// from the keyboard.
final class AccessibilityUITests: StagedJournalTestCase {
    func testTheLargestTextSizeKeepsEverythingReadableAndReachable() throws {
        try launch()
        tap("write-today")
        let ordinary = reach("question-0").frame.height
        let ordinaryField = reach("answer-0").frame.height
        kill()

        try launch(arguments: Self.largestText)
        for tab in ["Today", "History", "Reviews"] {
            XCTAssertTrue(app.tabBars.buttons[tab].isHittable, tab)
        }
        try audit([.dynamicType])
        tap("write-today")
        XCTAssertGreaterThan(reach("question-0").frame.height, ordinary, "the question grew with the text size")
        XCTAssertGreaterThan(reach("answer-0").frame.height, ordinaryField, "the field grew with the text size")
        try audit([.dynamicType])

        // Every field can still be reached and written in, and the day saved.
        type("Synthetic: read at the largest size.", into: "follow-through")
        for index in scenario.questions.indices {
            type("Synthetic: large \(index).", into: "answer-\(index)")
        }
        tap("keyboard-done")
        tap("save")
        assertExists("edit-today")
        let journal = try journalText()
        for index in scenario.questions.indices {
            XCTAssertTrue(journal.contains("Synthetic: large \(index)."))
        }
    }

    func testEveryControlSaysWhatItIs() throws {
        try launch()
        for tab in ["Today", "History", "Reviews"] {
            XCTAssertTrue(app.tabBars.buttons[tab].exists, tab)
        }
        XCTAssertEqual(reach("settings").label, "Settings")
        XCTAssertEqual(reach("write-today").label, "Write Today’s Reflection")
        XCTAssertEqual(reach("backfill").label, "Write for Another Day")
        let focus = reach("focus-link").label
        XCTAssertTrue(focus.contains("Finish before starting."), focus)
        try audit([.sufficientElementDescription])

        tap("settings")
        let asked = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", scenario.questions[0])).firstMatch
        XCTAssertTrue(asked.waitForExistence(timeout: 30), "the settings list the questions")
        try audit([.sufficientElementDescription])
        tap("settings-done")

        tap("focus-link")
        XCTAssertEqual(reach("focus-text").label, "Finish before starting.")
        app.navigationBars.buttons.firstMatch.tap()

        tap("write-today")
        XCTAssertEqual(reach("close").label, "Close")
        XCTAssertEqual(reach("save").label, "Save")
        XCTAssertEqual(reach("context-focus").label, "Finish before starting.")
        XCTAssertEqual(reach("context-intention").label, "Intention: Walk before the dip, not after.")
        XCTAssertEqual(reach("follow-through").label, "Follow-through: how did it go?")
        try audit([.sufficientElementDescription])
        for (index, question) in scenario.questions.enumerated() {
            XCTAssertEqual(reach("answer-\(index)").label, question, "a field is named by its question")
        }
        tap("answer-0")
        XCTAssertEqual(reach("keyboard-previous").label, "Previous field")
        XCTAssertEqual(reach("keyboard-next").label, "Next field")
        XCTAssertEqual(reach("keyboard-done").label, "Hide keyboard")
        tap("keyboard-done")
        tap("close")

        app.tabBars.buttons["History"].tap()
        let day = reach("entry-2026-03-05").label
        XCTAssertTrue(day.contains("March 5, 2026"), day)
        try audit([.sufficientElementDescription])
        tap("entry-2026-03-05")
        XCTAssertEqual(reach("edit-entry").label, "Edit")
        XCTAssertTrue(app.staticTexts["Intention: Finish the draft before lunch."].exists)
        try audit([.sufficientElementDescription])

        app.tabBars.buttons["Reviews"].tap()
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'Weekly review'")).firstMatch.waitForExistence(timeout: 30)
            || app.buttons.matching(NSPredicate(format: "label CONTAINS 'Weekly review'")).firstMatch.exists)
        try audit([.sufficientElementDescription])
    }

    func testTheKeyboardMovesThroughTheFieldsAndGetsOutOfTheWay() throws {
        try launch()
        tap("write-today")
        tap("follow-through")
        XCTAssertFalse(reach("keyboard-previous").isEnabled, "nothing comes before the first field")
        XCTAssertTrue(reach("keyboard-next").isEnabled)
        app.typeText("first")

        tap("keyboard-next")
        app.typeText("second")
        tap("keyboard-next")
        app.typeText("third")
        tap("keyboard-previous")
        app.typeText(" again\nand a new line")
        tap("keyboard-next")
        tap("keyboard-next")
        XCTAssertFalse(reach("keyboard-next").isEnabled, "nothing comes after the last field")
        app.typeText("last")

        tap("keyboard-done")
        XCTAssertTrue(element("keyboard-done").waitForNonExistence(timeout: 30), "the keyboard got out of the way")
        XCTAssertEqual(app.keyboards.count, 0)

        XCTAssertEqual(text(of: "follow-through"), "first")
        XCTAssertEqual(text(of: "answer-0"), "second again\nand a new line", "Return writes a new line; it does not leave the field")
        XCTAssertEqual(text(of: "answer-1"), "third")
        XCTAssertEqual(text(of: "answer-2"), "last")
        XCTAssertTrue(reach("save").isHittable)
        tap("save")
        assertExists("edit-today")
        XCTAssertTrue(try journalText().contains("second again\nand a new line"))
    }
}
