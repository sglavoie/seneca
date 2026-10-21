import XCTest

/// The first run over a journal nothing was written to: a practice, its
/// reminder, and the way into the journal.
final class OnboardingUITests: StagedJournalTestCase {
    override class var fixture: String { "empty" }

    private var remindersFile: URL { root.appendingPathComponent("reminders.json") }

    func testOnboardingAppliesAPracticeAndOffersItsReminder() throws {
        try launch(
            environment: ["SENECA_UITEST_ONBOARDING": "1", "SENECA_UITEST_NOTIFICATIONS": "notDetermined"],
            awaiting: "onboarding-start"
        )
        tap("onboarding-start")
        tap("pack-step-10")
        XCTAssertTrue(reach("pack-question-3").label.contains("Carried into the next day"), "the anchor is shown")
        // Nothing is written yet, so applying asks nothing.
        tap("pack-apply")
        let remind = app.alerts.buttons["Remind Me"].firstMatch
        XCTAssertTrue(remind.waitForExistence(timeout: 30), "the pack's reminder time is offered")
        remind.tap()

        assertExists("onboarding-reminders-state")
        let reminders = String(decoding: try Data(contentsOf: remindersFile), as: UTF8.self)
        XCTAssertTrue(reminders.contains("21:30"), reminders)
        tap("onboarding-reminders-next")
        tap("onboarding-finish")
        XCTAssertTrue(app.tabBars.buttons["Today"].waitForExistence(timeout: 30), "onboarding ends in the journal")
        let settings = try settingsText()
        XCTAssertTrue(settings.contains("\"id\": \"step-10\""), settings)

        // Once set up, never again.
        kill()
        try launch(environment: ["SENECA_UITEST_ONBOARDING": "1", "SENECA_UITEST_NOTIFICATIONS": "allowed"])
        XCTAssertFalse(element("onboarding-start").exists)
    }

    func testEveryStepCanBeSkipped() throws {
        try launch(environment: ["SENECA_UITEST_ONBOARDING": "1"], awaiting: "onboarding-start")
        tap("onboarding-start")
        tap("onboarding-practice-next")
        tap("onboarding-reminders-next")
        tap("onboarding-finish")
        XCTAssertTrue(app.tabBars.buttons["Today"].waitForExistence(timeout: 30))
        // Skipping chose nothing: the journal has no settings of its own.
        let settings = root.appendingPathComponent("journal/.seneca/journals/reflections.md/settings.json")
        XCTAssertFalse(FileManager.default.fileExists(atPath: settings.path))
        XCTAssertFalse(FileManager.default.fileExists(atPath: remindersFile.path))
    }
}

/// Choosing a practice from the settings, over a journal already written in.
final class PracticeUITests: StagedJournalTestCase {
    func testAJournalAlreadyWrittenInIsNotOnboarded() throws {
        try launch(environment: ["SENECA_UITEST_ONBOARDING": "1"])
        XCTAssertFalse(element("onboarding-start").exists)
    }

    func testSwitchingPracticeKeepsEveryEntryAsWritten() throws {
        let before = try journalText()
        try launch()
        tap("settings")
        XCTAssertTrue(reach("practice-link").label.contains("No practice pack"))
        tap("practice-link")
        tap("pack-examen")
        tap("pack-apply")
        // Days are written, so applying says what happens to them first.
        confirm("Use It From Today")
        let notNow = app.alerts.buttons["Not Now"].firstMatch
        XCTAssertTrue(notNow.waitForExistence(timeout: 30))
        notNow.tap()

        assertExists("pack-in-force")
        XCTAssertTrue(try settingsText().contains("\"id\": \"examen\""))
        XCTAssertEqual(try journalText(), before, "no entry is rewritten")
        back()
        back()
        XCTAssertTrue(reach("practice-link").label.contains("Ignatian examen"))
        XCTAssertTrue(reach("setting-question-0").exists)
    }
}

/// The follow-through report, from the Reviews tab.
final class InsightsUITests: StagedJournalTestCase {
    func testTheReportCountsTheStagedJournal() throws {
        try launch()
        app.tabBars.buttons["Reviews"].tap()
        tap("insights-link")
        XCTAssertTrue(reach("insights-answered").label.contains("1"))
        XCTAssertTrue(reach("insights-open").label.contains("1"))
        XCTAssertTrue(reach("insights-intention-2026-03-05").label.contains("Waiting for the next day"))
        // The answered intention opens on the day that answered it.
        tap("insights-intention-2026-03-04")
        XCTAssertTrue(app.staticTexts["Synthetic: the draft was done by eleven."].waitForExistence(timeout: 30))
    }
}
