import SenecaEngine
import XCTest
@testable import SenecaMobile

/// The completion gate of the bridge: the app's own engine service opens a
/// synthetic journal, saves a reflection through Go, and reopens it holding
/// exactly what the desktop writes for the same scenario.
final class RoundTripTests: XCTestCase {
    func testSavesAReflectionThroughGoAndReopensItAsTheDesktopWroteIt() async throws {
        let roundTrip = try RoundTrip.load()
        let root = try scratchDirectory(self)
        let location = try roundTrip.seed(in: root)

        let (saved, sent) = try await saveScenario(roundTrip, at: location)
        try await assertReopened(roundTrip, at: location, sent: sent, saved: saved.entry)
    }

    /// The same round trip through the model the screens are built on.
    @MainActor
    func testTheAppModelShowsTheSavedReflection() async throws {
        let roundTrip = try RoundTrip.load()
        let root = try scratchDirectory(self)
        let location = try roundTrip.seed(in: root)

        let model = JournalModel(engine: SenecaEngine(), location: { location })
        await model.open()
        XCTAssertEqual(model.phase, .ready)
        XCTAssertEqual(model.entries.count, roundTrip.expected.entryCount - 1)
        XCTAssertEqual(model.focus?.text, roundTrip.expected.focus)

        let prepared = try await model.prepareReflection()
        XCTAssertEqual(prepared.intentionLabel, roundTrip.expected.intentionLabel)
        let requestID = RequestID()
        let saved = try await model.save(roundTrip.writing(on: prepared.draft), requestID: requestID)
        XCTAssertEqual(saved.entry.raw, roundTrip.expected.entryRaw)
        XCTAssertEqual(model.entries.first?.date, roundTrip.scenario.date, "the newest day is listed first")
        XCTAssertEqual(model.entries.count, roundTrip.expected.entryCount)

        // The form sent again, as after a lost answer: the same day, once.
        let again = try await model.save(roundTrip.writing(on: prepared.draft), requestID: requestID)
        XCTAssertEqual(again, saved)
        XCTAssertEqual(try sha256(of: location.reflections), roundTrip.expected.reflectionsSha256)

        // A second launch of the app over the same container.
        let relaunched = JournalModel(engine: SenecaEngine(), location: { location })
        await relaunched.open()
        XCTAssertEqual(relaunched.phase, .ready)
        let entry = try await relaunched.entry(on: roundTrip.scenario.date)
        XCTAssertEqual(entry, saved.entry)
    }
}

/// Opens the seeded journal, saves the scenario's reflection, and holds what
/// was written to the desktop's expectation.
func saveScenario(
    _ roundTrip: RoundTrip, at location: JournalLocation, file: StaticString = #filePath, line: UInt = #line
) async throws -> (saved: SavedReflection, sent: ReflectionWriting) {
    let expected = roundTrip.expected
    let engine = SenecaEngine()
    let status = try await engine.open(location)
    XCTAssertEqual(status.today, expected.today, "the journal's day follows the scenario's zone", file: file, line: line)
    XCTAssertEqual(status.entryCount, expected.entryCount - 1, file: file, line: line)

    let prepared = try await engine.prepareReflection()
    XCTAssertEqual(prepared.draft.date, roundTrip.scenario.date, file: file, line: line)
    XCTAssertFalse(prepared.draft.isEdit, file: file, line: line)
    XCTAssertEqual(prepared.draft.context.focus, expected.focus, file: file, line: line)
    XCTAssertEqual(prepared.intentionLabel, expected.intentionLabel, file: file, line: line)

    let requestID = RequestID(roundTrip.scenario.requestId)
    let writing = roundTrip.writing(on: prepared.draft)
    let saved = try await engine.saveReflection(writing, requestID: requestID)
    XCTAssertFalse(saved.replayed, file: file, line: line)
    XCTAssertNil(saved.warning, file: file, line: line)
    XCTAssertEqual(saved.value.entry.raw, expected.entryRaw, file: file, line: line)
    XCTAssertEqual(saved.value.entry.revision, expected.entryRevision, file: file, line: line)
    XCTAssertEqual(try sha256(of: location.reflections), expected.reflectionsSha256, file: file, line: line)

    // The same request again is answered, not carried out again.
    let repeated = try await engine.saveReflection(writing, requestID: requestID)
    XCTAssertTrue(repeated.replayed, file: file, line: line)
    XCTAssertEqual(repeated.value, saved.value, file: file, line: line)
    XCTAssertEqual(try sha256(of: location.reflections), expected.reflectionsSha256, file: file, line: line)
    return (saved.value, writing)
}

/// Opens the journal with a new engine, as a relaunched app does, and holds
/// what it reads to the desktop's expectation. `sent` is the save as it was
/// first sent.
func assertReopened(
    _ roundTrip: RoundTrip, at location: JournalLocation, sent: ReflectionWriting, saved: Entry? = nil,
    file: StaticString = #filePath, line: UInt = #line
) async throws {
    let expected = roundTrip.expected
    let engine = SenecaEngine()
    let status = try await engine.open(location)
    XCTAssertEqual(status.entryCount, expected.entryCount, file: file, line: line)
    XCTAssertTrue(status.writable, file: file, line: line)

    let entry = try await engine.entry(on: roundTrip.scenario.date)
    XCTAssertEqual(entry.raw, expected.entryRaw, file: file, line: line)
    XCTAssertEqual(entry.revision, expected.entryRevision, file: file, line: line)
    if let saved {
        XCTAssertEqual(entry, saved, file: file, line: line)
    }
    XCTAssertFalse(entry.lines.contains { $0.contains("seneca:") }, "markers are not displayed", file: file, line: line)
    XCTAssertEqual(try sha256(of: location.reflections), expected.reflectionsSha256, file: file, line: line)
    let archive = location.reflections.deletingLastPathComponent().appendingPathComponent("seneca-reviews.md")
    XCTAssertEqual(try sha256(of: archive), expected.archiveSha256, "saving a reflection leaves the archive alone", file: file, line: line)

    // The day is now an edit of what was saved.
    let prepared = try await engine.prepareReflection(on: roundTrip.scenario.date)
    XCTAssertTrue(prepared.draft.isEdit, file: file, line: line)
    XCTAssertEqual(prepared.draft.base, expected.entryRevision, file: file, line: line)
    XCTAssertEqual(prepared.draft.initial, roundTrip.scenario.answers, file: file, line: line)
    XCTAssertEqual(prepared.draft.context.followInitial, roundTrip.scenario.followThrough, file: file, line: line)

    // The request ledger came back with the container: the save sent once
    // more is still answered rather than refused or repeated.
    let repeated = try await engine.saveReflection(sent, requestID: RequestID(roundTrip.scenario.requestId))
    XCTAssertTrue(repeated.replayed, file: file, line: line)
    XCTAssertEqual(repeated.value.entry.raw, expected.entryRaw, file: file, line: line)
    XCTAssertEqual(try sha256(of: location.reflections), expected.reflectionsSha256, file: file, line: line)
}
