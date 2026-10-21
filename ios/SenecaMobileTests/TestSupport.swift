import CryptoKit
import Foundation
import SenecaEngine
import XCTest

/// The round-trip scenario and the synthetic journal, as the test bundle
/// carries them from mobile/testdata/roundtrip. The Go tests hold the same
/// directory to the desktop engine and to the seneca program.
struct RoundTrip {
    struct Scenario: Decodable {
        let now: String
        let timeZone: String
        let questions: [String]
        let anchorQuestionIndex: Int
        let date: String
        let answers: [String]
        let followThrough: String
        let requestId: String
    }

    /// What the journal holds after the scenario's save, as the desktop
    /// engine writes it.
    struct Expected: Decodable {
        let today: String
        let entryCount: Int
        let reflectionsSha256: String
        let archiveSha256: String
        let entryRevision: String
        let entryRaw: String
        let intentionLabel: String
        let focus: String
    }

    let directory: URL
    let scenario: Scenario
    let expected: Expected

    static func load() throws -> RoundTrip {
        let bundle = Bundle(for: BundleAnchor.self)
        let directory = try XCTUnwrap(
            bundle.url(forResource: "roundtrip", withExtension: nil),
            "the test bundle carries no roundtrip directory"
        )
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return RoundTrip(
            directory: directory,
            scenario: try decoder.decode(Scenario.self, from: Data(contentsOf: directory.appendingPathComponent("scenario.json"))),
            expected: try decoder.decode(Expected.self, from: Data(contentsOf: directory.appendingPathComponent("expected.json")))
        )
    }

    /// Copies the synthetic journal into `root/journal` and returns where a
    /// device with the scenario's settings and clock would open it.
    func seed(in root: URL) throws -> JournalLocation {
        let journal = root.appendingPathComponent("journal", isDirectory: true)
        try FileManager.default.createDirectory(at: journal, withIntermediateDirectories: true)
        for name in ["reflections.md", "seneca-reviews.md"] {
            try FileManager.default.copyItem(
                at: directory.appendingPathComponent("journal/\(name)"),
                to: journal.appendingPathComponent(name)
            )
        }
        return location(in: root)
    }

    func location(in root: URL) -> JournalLocation {
        JournalLocation(
            reflections: root.appendingPathComponent("journal/reflections.md"),
            stateDirectory: root.appendingPathComponent("state", isDirectory: true),
            questions: scenario.questions,
            anchorQuestionIndex: scenario.anchorQuestionIndex,
            timeZone: TimeZone(identifier: scenario.timeZone)!,
            now: ISO8601DateFormatter().date(from: scenario.now)
        )
    }

    func writing(on draft: ReflectionDraft) -> ReflectionWriting {
        ReflectionWriting(draft: draft, answers: scenario.answers, followThrough: scenario.followThrough)
    }
}

private final class BundleAnchor {}

/// A journal the Go tests write and hold the bundle's copy to, beside the
/// round trip: "browsing" (every review period, revisions, several months)
/// or "conflicted" (a synced journal holding a conflicted day and a disputed
/// focus, with the exchange both devices published to).
struct StagedFixture {
    let location: JournalLocation
    /// The exchange the fixture's devices published to, copied, when it has one.
    let exchange: URL?

    static func stage(_ name: String, in root: URL, with roundTrip: RoundTrip) throws -> StagedFixture {
        let source = try XCTUnwrap(
            Bundle(for: BundleAnchor.self).url(forResource: name, withExtension: nil),
            "the test bundle carries no \(name) directory"
        )
        try FileManager.default.copyItem(
            at: source.appendingPathComponent("journal", isDirectory: true),
            to: root.appendingPathComponent("journal", isDirectory: true)
        )
        var exchange: URL?
        let published = source.appendingPathComponent("exchange", isDirectory: true)
        if FileManager.default.fileExists(atPath: published.path) {
            exchange = root.appendingPathComponent("exchange", isDirectory: true)
            try FileManager.default.copyItem(at: published, to: exchange!)
            // The synced journal's history travels in a hidden directory.
            XCTAssertTrue(
                FileManager.default.fileExists(atPath: root.appendingPathComponent("journal/.seneca").path),
                "the bundle's \(name) journal lost its .seneca directory"
            )
        }
        return StagedFixture(location: roundTrip.location(in: root), exchange: exchange)
    }
}

func sha256(of url: URL) throws -> String {
    SHA256.hash(data: try Data(contentsOf: url)).map { String(format: "%02x", $0) }.joined()
}

/// A directory of the test's own inside the app's container, removed when
/// the test ends.
func scratchDirectory(_ test: XCTestCase) throws -> URL {
    let root = FileManager.default.temporaryDirectory
        .appendingPathComponent("seneca-tests-\(UUID().uuidString)", isDirectory: true)
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    test.addTeardownBlock { try? FileManager.default.removeItem(at: root) }
    return root
}

/// Runs `body` and returns the engine's refusal, failing when it succeeds or
/// fails some other way.
@discardableResult
func refusal<T: Sendable>(
    _ code: String, file: StaticString = #filePath, line: UInt = #line, _ body: () async throws -> T
) async -> EngineProblem? {
    do {
        _ = try await body()
        XCTFail("expected the engine to refuse with \(code)", file: file, line: line)
    } catch let problem as EngineProblem {
        XCTAssertEqual(problem.code, code, problem.message, file: file, line: line)
        XCTAssertFalse(problem.message.isEmpty, file: file, line: line)
        return problem
    } catch {
        XCTFail("expected refusal \(code), got \(error)", file: file, line: line)
    }
    return nil
}

/// Runs a test body and reports what it throws as a failure that says what
/// was thrown. Xcode 27 reports the first error a hosted test throws as an
/// `InvalidTransition` of its own instead of the error.
func reporting(file: StaticString = #filePath, line: UInt = #line, _ body: () async throws -> Void) async {
    do {
        try await body()
    } catch {
        XCTFail("\(error)", file: file, line: line)
    }
}

/// `refusal` for a test on the main actor, whose closures stay on it.
@MainActor
@discardableResult
func refusedOnMain<T>(
    _ code: String, file: StaticString = #filePath, line: UInt = #line, _ body: () async throws -> T
) async -> EngineProblem? {
    do {
        _ = try await body()
        XCTFail("expected the engine to refuse with \(code)", file: file, line: line)
    } catch let problem as EngineProblem {
        XCTAssertEqual(problem.code, code, problem.message, file: file, line: line)
        XCTAssertFalse(problem.message.isEmpty, file: file, line: line)
        return problem
    } catch {
        XCTFail("expected refusal \(code), got \(error)", file: file, line: line)
    }
    return nil
}

/// `reporting` for a test on the main actor.
@MainActor
func reportingOnMain(file: StaticString = #filePath, line: UInt = #line, _ body: () async throws -> Void) async {
    do {
        try await body()
    } catch {
        XCTFail("\(error)", file: file, line: line)
    }
}

/// Leaves the archive at `url` as an interrupted copy would: cut off inside
/// its first record's marker, with bytes that are no text after it. Returns
/// what the file now holds.
@discardableResult
func tornArchive(at url: URL) throws -> Data {
    let whole = String(decoding: try Data(contentsOf: url), as: UTF8.self)
    let marker = try XCTUnwrap(whole.range(of: "<!-- seneca:focus v=3 id="), "the synthetic archive has no focus record")
    var torn = Data(whole[..<marker.upperBound].utf8)
    torn.append(contentsOf: [0x39, 0x33, 0x65, 0x00, 0xff, 0xfe])
    try torn.write(to: url)
    return torn
}

/// The app's executable, the debug dylib a Debug build keeps its code in,
/// and the test bundle's executable, each named relative to the app: which
/// build ran, in the build's own words.
func loadedExecutables(with tests: AnyClass) throws -> [String: String] {
    var digests: [String: String] = [:]
    let app = Bundle.main.bundleURL
    var files: [URL] = []
    if let executable = Bundle.main.executableURL {
        files.append(executable)
    }
    let siblings = try FileManager.default.contentsOfDirectory(at: app, includingPropertiesForKeys: nil)
    files.append(contentsOf: siblings.filter { $0.pathExtension == "dylib" })
    if let tests = Bundle(for: tests).executableURL {
        files.append(tests)
    }
    for file in files {
        let path = file.standardizedFileURL.path
        let base = app.standardizedFileURL.path
        let name = path.hasPrefix(base + "/") ? String(path.dropFirst(base.count + 1)) : file.lastPathComponent
        digests[name] = try sha256(of: file)
    }
    return digests
}
