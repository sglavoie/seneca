import SenecaEngine
import XCTest
@testable import SenecaMobile

/// The first run and the practice packs under the screens: when onboarding
/// is offered, and what opening and applying a pack does to the journal.
@MainActor
final class OnboardingTests: XCTestCase {
    private var roundTrip: RoundTrip!
    private var root: URL!

    override func setUp() async throws {
        roundTrip = try RoundTrip.load()
        root = try scratchDirectory(self)
    }

    private func open(_ location: JournalLocation, file: StaticString = #filePath, line: UInt = #line) async -> JournalModel {
        let model = JournalModel(engine: SenecaEngine(), location: { location })
        await model.open()
        XCTAssertEqual(model.phase, .ready, file: file, line: line)
        return model
    }

    func testAnUntouchedJournalIsOnboardedOnce() async throws {
        let location = roundTrip.location(in: root)
        try FileManager.default.createDirectory(at: location.reflections.deletingLastPathComponent(), withIntermediateDirectories: true)
        let first = await open(location)
        XCTAssertTrue(first.onboarding)
        first.finishOnboarding()
        XCTAssertFalse(first.onboarding)

        let again = await open(location)
        XCTAssertFalse(again.onboarding, "once finished, never offered again")
    }

    func testAJournalWithWritingIsNeverOnboarded() async throws {
        let location = try roundTrip.seed(in: root)
        let model = await open(location)
        XCTAssertFalse(model.onboarding)
        XCTAssertTrue(
            FileManager.default.fileExists(atPath: AppPaths.onboardingFile(for: location).path),
            "an installation already in use is recorded as set up"
        )
    }

    func testApplyingAPackKeepsEveryEntryAsWritten() async throws {
        await reportingOnMain {
            let location = try roundTrip.seed(in: root)
            let before = try Data(contentsOf: location.reflections)
            let model = await open(location)
            let packs = try await model.builtinPacks()
            XCTAssertEqual(packs.first?.id, "stoic", "the default practice comes first")
            let examen = try XCTUnwrap(packs.first { $0.id == "examen" })

            try await model.applyPack(examen, requestID: RequestID())
            let settings = try XCTUnwrap(model.status?.settings)
            XCTAssertEqual(settings.practice?.pack, examen)
            XCTAssertEqual(settings.questions, examen.questions)
            XCTAssertEqual(try Data(contentsOf: location.reflections), before, "no entry is rewritten")
        }
    }

    func testAFileThatIsNotAPackIsRefused() async throws {
        let location = try roundTrip.seed(in: root)
        let model = await open(location)
        let file = root.appendingPathComponent("notes.json")
        try Data(#"{"questions": ["What went well?"]}"#.utf8).write(to: file)
        await refusedOnMain("invalid-pack") { try await model.readPack(at: file) }
    }

    func testAPackFileReadsAsTheEngineWroteIt() async throws {
        await reportingOnMain {
            let location = try roundTrip.seed(in: root)
            let model = await open(location)
            let packs = try await model.builtinPacks()
            let stepTen = try XCTUnwrap(packs.first { $0.id == "step-10" })
            let encoder = JSONEncoder()
            encoder.keyEncodingStrategy = .convertToSnakeCase
            let file = root.appendingPathComponent("step-10.seneca-pack.json")
            try encoder.encode(stepTen).write(to: file)
            let read = try await model.readPack(at: file)
            XCTAssertEqual(read, stepTen)
        }
    }
}
