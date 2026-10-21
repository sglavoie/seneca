import SenecaEngine
import UIKit
import XCTest

/// The round trip split across two installations of the app, to prove that
/// installing a newer signed build over an older one keeps its data.
/// ios/scripts/roundtrip.sh drives it: it runs the seed phase, installs a
/// build with another build number, runs the reopen phase, and collects what
/// each wrote under Application Support/SenecaEvidence/RUN in the app's
/// container.
///
/// Both tests need SENECA_ROUNDTRIP_RUN (xcodebuild passes it as
/// TEST_RUNNER_SENECA_ROUNDTRIP_RUN) and fail without it: run on their own
/// they prove nothing.
final class RedeploymentTests: XCTestCase {
    func testSeedSavesAReflectionThroughGo() async {
        await reporting {
            let roundTrip = try RoundTrip.load()
            let root = try runDirectory(creating: true)
            let location = try roundTrip.seed(in: root)

            let (saved, sent) = try await saveScenario(roundTrip, at: location)
            try await assertReopened(roundTrip, at: location, sent: sent, saved: saved.entry)

            try JSONEncoder().encode(sent).write(to: root.appendingPathComponent("writing.json"), options: .atomic)
            try await writeEvidence(phase: "seed", roundTrip: roundTrip, location: location, root: root)
        }
    }

    func testReopenFindsTheReflectionAfterRedeployment() async {
        await reporting {
            let roundTrip = try RoundTrip.load()
            let root = try runDirectory(creating: false)
            let location = roundTrip.location(in: root)
            XCTAssertTrue(
                FileManager.default.fileExists(atPath: location.reflections.path),
                "the journal the seed phase wrote is gone from the app's container"
            )
            let sent = try JSONDecoder().decode(
                ReflectionWriting.self, from: Data(contentsOf: root.appendingPathComponent("writing.json"))
            )

            try await assertReopened(roundTrip, at: location, sent: sent)
            try await writeEvidence(phase: "reopen", roundTrip: roundTrip, location: location, root: root)
        }
    }

    private func runDirectory(creating: Bool) throws -> URL {
        let run = try XCTUnwrap(
            ProcessInfo.processInfo.environment["SENECA_ROUNDTRIP_RUN"].flatMap { $0.isEmpty ? nil : $0 },
            "SENECA_ROUNDTRIP_RUN is not set; run this through ios/scripts/roundtrip.sh"
        )
        let support = try FileManager.default.url(
            for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true
        )
        let root = support.appendingPathComponent("SenecaEvidence/\(run)", isDirectory: true)
        if creating {
            XCTAssertFalse(FileManager.default.fileExists(atPath: root.path), "run \(run) was already seeded")
            try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        }
        return root
    }

    private struct Evidence: Encodable {
        let run: String
        let phase: String
        let bundleIdentifier: String
        let bundleVersion: String
        let bundleBuild: String
        /// The Mach-O files this process loaded from its bundles, by digest:
        /// which build ran, in the build's own words.
        let executables: [String: String]
        let deviceModel: String
        let deviceName: String
        let systemName: String
        let systemVersion: String
        let simulator: Bool
        let bridgeVersion: Int
        let today: String
        let entryCount: Int
        let entryRevision: String
        let reflectionsSha256: String
        let archiveSha256: String
        let replayedAfterReopen: Bool
        let containerRelativeJournal: String
    }

    private func writeEvidence(phase: String, roundTrip: RoundTrip, location: JournalLocation, root: URL) async throws {
        let engine = SenecaEngine()
        let status = try await engine.open(location)
        let entry = try await engine.entry(on: roundTrip.scenario.date)
        let (model, name, systemName, systemVersion) = await MainActor.run {
            (UIDevice.current.model, UIDevice.current.name, UIDevice.current.systemName, UIDevice.current.systemVersion)
        }
        var machine = utsname()
        uname(&machine)
        let identifier = withUnsafePointer(to: &machine.machine) {
            $0.withMemoryRebound(to: CChar.self, capacity: 1) { String(cString: $0) }
        }
        #if targetEnvironment(simulator)
        let simulator = true
        #else
        let simulator = false
        #endif
        let info = Bundle.main.infoDictionary ?? [:]
        let archive = location.reflections.deletingLastPathComponent().appendingPathComponent("seneca-reviews.md")
        let evidence = Evidence(
            run: root.lastPathComponent,
            phase: phase,
            bundleIdentifier: Bundle.main.bundleIdentifier ?? "",
            bundleVersion: info["CFBundleShortVersionString"] as? String ?? "",
            bundleBuild: info["CFBundleVersion"] as? String ?? "",
            executables: try loadedExecutables(with: RedeploymentTests.self),
            deviceModel: "\(model) (\(identifier))",
            deviceName: name,
            systemName: systemName,
            systemVersion: systemVersion,
            simulator: simulator,
            bridgeVersion: engine.bridgeVersion,
            today: status.today,
            entryCount: status.entryCount,
            entryRevision: entry.revision,
            reflectionsSha256: try sha256(of: location.reflections),
            archiveSha256: try sha256(of: archive),
            // assertReopened ran before this and failed the test otherwise.
            replayedAfterReopen: true,
            containerRelativeJournal: "Library/Application Support/SenecaEvidence/\(root.lastPathComponent)/journal"
        )
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        try encoder.encode(evidence).write(to: root.appendingPathComponent("evidence-\(phase).json"), options: .atomic)
    }
}
