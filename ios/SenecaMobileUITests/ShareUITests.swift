import XCTest

/// Sharing with another person: what the composer previews is the packet,
/// the review alone until days are chosen.
final class ShareUITests: StagedJournalTestCase {
    func testAReviewIsSharedAloneUntilItsDaysAreChosen() throws {
        try launch()
        app.tabBars.buttons["Reviews"].tap()
        let weekly = app.descendants(matching: .any)
            .matching(NSPredicate(format: "identifier BEGINSWITH 'review-' AND label CONTAINS 'Weekly review'")).firstMatch
        XCTAssertTrue(weekly.waitForExistence(timeout: 30))
        weekly.tap()
        tap("review-share")

        let preview = reach("share-preview")
        XCTAssertTrue(preview.label.hasPrefix("# Weekly review"), preview.label)
        XCTAssertFalse(preview.label.contains("## 2026-03-04"), "the review alone by default")
        XCTAssertFalse(preview.label.contains("<!--"))
        assertExists("share-send")

        // The switch, at the row's trailing edge; the label does not flip it.
        reach("share-include-2026-03-04").coordinate(withNormalizedOffset: CGVector(dx: 0.92, dy: 0.5)).tap()
        let withDay = app.descendants(matching: .any)
            .matching(NSPredicate(format: "identifier == 'share-preview' AND label CONTAINS '## 2026-03-04'")).firstMatch
        XCTAssertTrue(withDay.waitForExistence(timeout: 30), "the chosen day goes with it")

        choose("PDF", in: "share-format")
        assertExists("share-preview-pdf")
        assertExists("share-send")
        tap("share-cancel")
    }

    func testOneDayIsSharedFromHistory() throws {
        try launch()
        app.tabBars.buttons["History"].tap()
        tap("entry-2026-03-05")
        tap("entry-share")
        let preview = reach("share-preview")
        XCTAssertTrue(preview.label.hasPrefix("# Reflections, 2026-03-05"), preview.label)
        XCTAssertTrue(preview.label.contains("Walk before the dip, not after."))
        XCTAssertFalse(preview.label.contains("seneca:"))
    }

    private func choose(_ option: String, in picker: String) {
        let segmented = element(picker)
        XCTAssertTrue(segmented.waitForExistence(timeout: 30))
        segmented.buttons[option].tap()
    }
}
