import CryptoKit
import XCTest

/// The debug-only model evaluation screen, driven from outside: reached from
/// Settings, it reads the prompts copied beside the app's state, says
/// whether this build may reach Private Cloud Compute, and with no prompts
/// says so and runs nothing. It never touches the journal.
final class ModelEvaluationUITests: StagedJournalTestCase {
    private var evaluation: URL { root.appendingPathComponent("evaluation", isDirectory: true) }

    func testTheEvaluationScreenReadsItsPromptsAndLeavesTheJournalAlone() throws {
        try launch()
        let journal = try journalText()

        tap("settings")
        tap("evaluation-link")
        assertExists("evaluation-no-prompts")
        XCTAssertFalse(element("evaluation-run").isEnabled, "the run is offered with nothing to run")
        XCTAssertTrue(reach("evaluation-pcc").label.contains("Not entitled"), reach("evaluation-pcc").label)
        try audit([.contrast, .elementDetection, .hitRegion, .sufficientElementDescription, .trait])
        back()

        // Prompts copied in, as `run.sh push` does on an iPhone.
        try FileManager.default.createDirectory(at: evaluation, withIntermediateDirectories: true)
        let prompt = "Date: 2026-03-02\nSynthetic."
        let body: [String: Any] = [
            "format": "seneca-apple-models-prompts", "format_version": 1,
            "prompts": [["id": "2026-W10", "prompt": prompt, "schema": #"{"type":"object"}"#, "sha256": sha256Hex(prompt)]],
        ]
        try JSONSerialization.data(withJSONObject: body).write(to: evaluation.appendingPathComponent("prompts.json"))
        tap("evaluation-link")
        XCTAssertTrue(reach("evaluation-prompts").label.contains("1"), reach("evaluation-prompts").label)
        XCTAssertTrue(element("evaluation-run").isEnabled)
        XCTAssertEqual(try journalText(), journal, "the evaluation screen touched the journal")
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: evaluation.path), ["prompts.json"])
    }
}

private func sha256Hex(_ text: String) -> String {
    SHA256.hash(data: Data(text.utf8)).map { String(format: "%02x", $0) }.joined()
}
