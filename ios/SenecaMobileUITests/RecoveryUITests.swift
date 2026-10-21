import XCTest

/// Reminders, diagnostics, and the recovery export, driven from outside on a
/// simulator over a staged journal. The author's answer to the notification
/// prompt is staged too, and what the app scheduled is read back from
/// notifications.json beside the journal.
final class RecoveryUITests: StagedJournalTestCase {
    private func scheduled() -> [String] {
        let file = root.appendingPathComponent("notifications.json")
        guard let data = try? Data(contentsOf: file) else { return [] }
        return (try? JSONDecoder().decode([String].self, from: data)) ?? []
    }

    private func waitFor(_ what: String, timeout: TimeInterval = 30, _ condition: () -> Bool) {
        let deadline = Date().addingTimeInterval(timeout)
        while !condition() {
            if Date() > deadline {
                return XCTFail("timed out waiting for \(what)")
            }
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        }
    }

    private func flip(_ identifier: String) {
        let toggle = reach(identifier)
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.92, dy: 0.5)).tap()
    }

    private func openSettings(_ link: String) {
        tap("settings")
        tap(link)
    }

    private func writeToday(_ text: String) {
        tap("write-today")
        type(text, into: "answer-0")
        tap("save")
        assertExists("edit-today")
    }

    func testRemindersAreOptInAndScheduledAroundWhatIsWritten() throws {
        try launch(environment: ["SENECA_UITEST_NOTIFICATIONS": "notDetermined"])
        openSettings("reminders-link")
        let toggle = reach("reminders-toggle")
        XCTAssertEqual(toggle.value as? String, "0", "reminders are off until turned on")
        XCTAssertTrue(reach("reminders-delivery-note").label.contains("only after the iPhone has received it"))
        XCTAssertEqual(scheduled(), [])

        flip("reminders-toggle")
        waitFor("tonight's reminder") { self.scheduled().contains("seneca.reminder.\(self.scenario.date).2200") }
        XCTAssertTrue(reach("reminders-state").label.contains("Tonight"), reach("reminders-state").label)
        XCTAssertTrue(element("reminders-time-2000").exists && element("reminders-time-2200").exists, "the desktop's times")

        // Writing the evening cancels its reminders; the next evenings stay.
        back()
        tap("settings-done")
        writeToday("Synthetic: written before the reminder.")
        waitFor("tonight's reminders cancelled") {
            !self.scheduled().contains { $0.contains(self.scenario.date) } && !self.scheduled().isEmpty
        }
    }

    func testDeniedNotificationsAreSaidSoAndNeverStopWriting() throws {
        try launch(environment: ["SENECA_UITEST_NOTIFICATIONS": "denied"])
        openSettings("reminders-link")
        flip("reminders-toggle")
        assertExists("reminders-open-settings")
        XCTAssertTrue(reach("reminders-state").label.contains("turned off for Seneca"))
        XCTAssertEqual(scheduled(), [])
        back()
        tap("recovery-link")
        assertExists("recovery-finding-notifications-denied")
        back()
        tap("settings-done")

        writeToday("Synthetic: written with notifications off.")
        XCTAssertTrue(try journalText().contains("Synthetic: written with notifications off."))
    }

    func testDiagnosticsShareNothingWrittenAndTheJournalExportsWhole() throws {
        try launch(environment: ["SENECA_UITEST_NOTIFICATIONS": "allowed"])
        openSettings("recovery-link")
        assertExists("recovery-healthy")
        reach("recovery-share-diagnostics")
        app.buttons["What Is Shared"].firstMatch.tap()
        let report = reach("recovery-diagnostics-report").label
        XCTAssertTrue(report.contains("seneca-diagnostics"), report)
        for secret in ["Synthetic", "2026-", root.path, "What did I"] {
            XCTAssertFalse(report.contains(secret), "the diagnostics carry \(secret)")
        }

        let before = try journalText()
        tap("recovery-export")
        confirm("Export Journal")
        assertExists("recovery-exported")
        XCTAssertTrue(reach("recovery-exported").label.hasPrefix("Exported"))
        assertExists("recovery-share-export")
        XCTAssertEqual(try journalText(), before, "exporting changes nothing")
    }
}
