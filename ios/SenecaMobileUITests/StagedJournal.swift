import XCTest

/// A UI test over a journal of its own. The synthetic journal the Go and
/// hosted suites use is copied into the runner's temporary directory and the
/// app is launched on it, with the scenario's clock and questions. On a
/// simulator both processes see that directory, so the test can read what the
/// app wrote and change the journal behind the app's back.
class StagedJournalTestCase: XCTestCase {
    struct Scenario: Decodable {
        let now: String
        let timeZone: String
        let questions: [String]
        let anchorQuestionIndex: Int
        let date: String
    }

    static let largestText = ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]

    /// The journal the test stages: the round trip's, or "browsing" or
    /// "conflicted", which the Go tests write beside it, or "empty", a
    /// journal nothing was ever written to. A fixture with an exchange
    /// stages that too, as the folder the app exchanges with.
    class var fixture: String { "roundtrip" }

    private(set) var root: URL!
    private(set) var scenario: Scenario!
    private(set) var app: XCUIApplication!

    var reflections: URL { root.appendingPathComponent("journal/reflections.md") }
    var archive: URL { root.appendingPathComponent("journal/seneca-reviews.md") }
    var drafts: URL { root.appendingPathComponent("drafts", isDirectory: true) }
    var exchange: URL { root.appendingPathComponent("exchange", isDirectory: true) }

    override func setUpWithError() throws {
        continueAfterFailure = false
        let fixture = try XCTUnwrap(
            Bundle(for: StagedJournalTestCase.self).url(forResource: "roundtrip", withExtension: nil),
            "the test bundle carries no roundtrip directory"
        )
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        scenario = try decoder.decode(Scenario.self, from: Data(contentsOf: fixture.appendingPathComponent("scenario.json")))

        root = FileManager.default.temporaryDirectory
            .appendingPathComponent("seneca-ui-\(UUID().uuidString)", isDirectory: true)
        let journal = root.appendingPathComponent("journal", isDirectory: true)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        if Self.fixture == "empty" {
            try FileManager.default.createDirectory(at: journal, withIntermediateDirectories: true)
        } else if Self.fixture == "roundtrip" {
            try FileManager.default.createDirectory(at: journal, withIntermediateDirectories: true)
            for name in ["reflections.md", "seneca-reviews.md"] {
                try FileManager.default.copyItem(
                    at: fixture.appendingPathComponent("journal/\(name)"), to: journal.appendingPathComponent(name)
                )
            }
        } else {
            let staged = try XCTUnwrap(
                Bundle(for: StagedJournalTestCase.self).url(forResource: Self.fixture, withExtension: nil),
                "the test bundle carries no \(Self.fixture) directory"
            )
            // Whole, with a synced journal's hidden history.
            try FileManager.default.copyItem(at: staged.appendingPathComponent("journal", isDirectory: true), to: journal)
            let published = staged.appendingPathComponent("exchange", isDirectory: true)
            if FileManager.default.fileExists(atPath: published.path) {
                try FileManager.default.copyItem(at: published, to: exchange)
            }
        }
    }

    override func tearDownWithError() throws {
        app?.terminate()
        if let root {
            try? FileManager.default.removeItem(at: root)
        }
    }

    /// Launches the app on the staged journal. A second call is a relaunch
    /// over everything the first run left on disk.
    func launch(arguments: [String] = [], environment: [String: String] = [:], awaiting screen: String? = nil) throws {
        app = XCUIApplication()
        // The labels the tests read are the English ones, whatever the
        // simulator was last set to.
        app.launchArguments = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"] + arguments
        app.launchEnvironment = [
            "SENECA_UITEST_ROOT": root.path,
            "SENECA_UITEST_NOW": scenario.now,
            "SENECA_UITEST_TIME_ZONE": scenario.timeZone,
            "SENECA_UITEST_QUESTIONS": String(decoding: try JSONEncoder().encode(scenario.questions), as: UTF8.self),
            "SENECA_UITEST_ANCHOR": String(scenario.anchorQuestionIndex),
        ]
        if FileManager.default.fileExists(atPath: exchange.path) {
            app.launchEnvironment["SENECA_UITEST_EXCHANGE"] = exchange.path
        }
        for (key, value) in environment {
            app.launchEnvironment[key] = value
        }
        app.launch()
        if let screen {
            assertExists(screen)
        } else {
            XCTAssertTrue(app.tabBars.buttons["Today"].waitForExistence(timeout: 30), "the app did not open the staged journal")
        }
    }

    /// Stops the app the way the system or the author does: without a word.
    func kill() {
        app.terminate()
        XCTAssertTrue(app.wait(for: .notRunning, timeout: 15))
    }

    // MARK: Elements

    func element(_ identifier: String) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: identifier).firstMatch
    }

    /// Waits for an element, scrolling the form toward it when it is not on
    /// screen yet.
    @discardableResult
    func reach(_ identifier: String, file: StaticString = #filePath, line: UInt = #line) -> XCUIElement {
        let found = element(identifier)
        _ = found.waitForExistence(timeout: 30)
        var reachable = found.exists && found.isHittable
        var swipes = 0
        while !reachable, swipes < 12 {
            app.swipeUp(velocity: .slow)
            swipes += 1
            reachable = found.exists && found.isHittable
        }
        XCTAssertTrue(reachable, "\(identifier) cannot be reached", file: file, line: line)
        return found
    }

    func tap(_ identifier: String, file: StaticString = #filePath, line: UInt = #line) {
        reach(identifier, file: file, line: line).tap()
    }

    func type(_ text: String, into identifier: String, file: StaticString = #filePath, line: UInt = #line) {
        let field = reach(identifier, file: file, line: line)
        field.tap()
        field.typeText(text)
    }

    /// Types after what a field already holds.
    func append(_ text: String, into identifier: String, file: StaticString = #filePath, line: UInt = #line) {
        let field = reach(identifier, file: file, line: line)
        // Away from the bottom edge, where the keyboard is about to rise.
        var swipes = 0
        while field.frame.maxY > app.frame.height * 0.6, swipes < 4 {
            app.swipeUp(velocity: .slow)
            swipes += 1
        }
        // A tap past a short text's end leaves the cursor after it.
        field.tap()
        if !app.keyboards.firstMatch.waitForExistence(timeout: 5) {
            field.tap()
        }
        field.typeText(text)
    }

    func text(of identifier: String, file: StaticString = #filePath, line: UInt = #line) -> String {
        reach(identifier, file: file, line: line).value as? String ?? ""
    }

    func assertExists(_ identifier: String, timeout: TimeInterval = 30, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertTrue(element(identifier).waitForExistence(timeout: timeout), "\(identifier) is not shown", file: file, line: line)
    }

    /// Audits the screen as it is shown. The system's own bars are left
    /// out: a tab bar and a navigation bar keep their size at every text
    /// size and offer the large content viewer instead. Everything the app
    /// lays out itself is held to the audit.
    func audit(_ types: XCUIAccessibilityAuditType, file: StaticString = #filePath, line: UInt = #line) throws {
        let bars = (app.tabBars.allElementsBoundByIndex + app.navigationBars.allElementsBoundByIndex + app.toolbars.allElementsBoundByIndex)
            .filter(\.exists).map(\.frame)
        try app.performAccessibilityAudit(for: types) { issue in
            guard let element = issue.element else { return false }
            let frame = element.frame
            let chrome = bars.contains { $0.contains(frame) }
            if !chrome {
                print("audit: \(issue.compactDescription): \(element.debugDescription)")
            }
            return chrome
        }
    }

    // MARK: The staged files

    func journalText() throws -> String {
        String(decoding: try Data(contentsOf: reflections), as: UTF8.self)
    }

    func archiveText() throws -> String {
        String(decoding: try Data(contentsOf: archive), as: UTF8.self)
    }

    func settingsText() throws -> String {
        let document = root.appendingPathComponent("journal/.seneca/journals/reflections.md/settings.json")
        return String(decoding: try Data(contentsOf: document), as: UTF8.self)
    }

    /// Taps a button of the confirmation dialog shown.
    func confirm(_ label: String, file: StaticString = #filePath, line: UInt = #line) {
        let button = app.buttons[label].firstMatch
        XCTAssertTrue(button.waitForExistence(timeout: 30), "no \(label) to confirm with", file: file, line: line)
        button.tap()
    }

    /// Goes back one screen in the navigation stack shown.
    func back() {
        app.navigationBars.buttons.element(boundBy: 0).tap()
    }

    func draftFiles() -> [String] {
        ((try? FileManager.default.contentsOfDirectory(atPath: drafts.path)) ?? []).filter { $0.hasSuffix(".json") }
    }
}
