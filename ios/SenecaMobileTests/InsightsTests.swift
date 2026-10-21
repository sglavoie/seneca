import SenecaEngine
import XCTest
@testable import SenecaMobile

/// The follow-through report under its screen: the engine's, over the last
/// 30 days of the journal's today, read without writing.
@MainActor
final class InsightsTests: XCTestCase {
    func testTheLastThirtyDaysAreTheEnginesReport() async throws {
        await reportingOnMain {
            let roundTrip = try RoundTrip.load()
            let location = try roundTrip.seed(in: try scratchDirectory(self))
            let before = try Data(contentsOf: location.reflections)
            let model = JournalModel(engine: SenecaEngine(), location: { location })
            await model.open()
            XCTAssertEqual(model.phase, .ready)

            let report = try await model.insights()
            XCTAssertEqual(report.to, roundTrip.scenario.date)
            XCTAssertEqual(report.intentions.map(\.status), ["answered", "open"])
            XCTAssertEqual(report.intentions.first?.answeredOn, "2026-03-05")
            XCTAssertEqual(report.answered, 1)
            XCTAssertEqual(report.open, 1)
            XCTAssertEqual(report.focus.last?.current, true)
            XCTAssertTrue(report.focusProblem.isEmpty)
            XCTAssertEqual(try Data(contentsOf: location.reflections), before, "reading insights writes nothing")
        }
    }
}
