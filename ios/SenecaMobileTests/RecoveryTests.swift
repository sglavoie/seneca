import SenecaEngine
import XCTest
@testable import SenecaMobile

/// Diagnostics, the recovery export, and the ways back: an unavailable
/// folder, an interrupted export, a new iPhone, a copy restored from another
/// iPhone, and leaving sync. Every path keeps every word.
@MainActor
final class RecoveryTests: XCTestCase {
    private var roundTrip: RoundTrip!

    override func setUp() async throws {
        roundTrip = try RoundTrip.load()
    }

    private func launch(
        _ location: JournalLocation, folder: FaultyFolder? = nil, vault: MemoryVault = MemoryVault(),
        file: StaticString = #filePath, line: UInt = #line
    ) async -> JournalModel {
        let model = JournalModel(
            engine: SenecaEngine(), location: { location },
            access: { location in folder.map { FaultyAccess(folder: $0) } ?? AppPaths.exchangeAccess(for: location) },
            reminderCenter: FakeReminderCenter(.allowed), identityVault: vault
        )
        await model.open()
        XCTAssertEqual(model.phase, .ready, file: file, line: line)
        if let sync = model.sync, folder != nil {
            sync.retryDelay = { _ in .seconds(600) }
            await eventually("the launch exchange", file: file, line: line) {
                sync.exchanges.contains(.launch) && sync.phase != .exchanging
            }
        }
        return model
    }

    private func save(_ model: JournalModel, _ text: String, on date: String? = nil) async throws {
        let prepared = try await model.prepareReflection(on: date)
        _ = try await model.save(ReflectionWriting(draft: prepared.draft, answers: [text, "", ""]), requestID: RequestID())
    }

    private func files(under root: URL) -> [String: Data] {
        var found: [String: Data] = [:]
        let base = root.standardizedFileURL.path
        let walker = FileManager.default.enumerator(at: root, includingPropertiesForKeys: [.isRegularFileKey])
        while let file = walker?.nextObject() as? URL {
            if (try? file.resourceValues(forKeys: [.isRegularFileKey]).isRegularFile) == true {
                found[String(file.standardizedFileURL.path.dropFirst(base.count + 1))] = try? Data(contentsOf: file)
            }
        }
        return found
    }

    func testDiagnosticsShareNothingWritten() async {
        await reportingOnMain {
            let root = try scratchDirectory(self)
            let fixture = try StagedFixture.stage("conflicted", in: root, with: roundTrip)
            let model = await launch(fixture.location)
            let diagnostics = try await model.diagnostics()
            XCTAssertTrue(diagnostics.sync.enrolled)
            XCTAssertEqual(diagnostics.finding("conflicts-open")?.count, 2)
            let report = DiagnosticsReport.make(diagnostics, journal: model, ownIdentity: true).text
            XCTAssertTrue(report.contains("\"format\" : \"seneca-diagnostics\""), report)
            XCTAssertTrue(report.contains("\"bridge_version\" : \(supportedBridgeVersion)"), report)

            let status = try XCTUnwrap(model.status)
            let journal = try String(contentsOf: fixture.location.reflections, encoding: .utf8)
                + (try String(contentsOf: fixture.location.reflections.deletingLastPathComponent().appendingPathComponent("seneca-reviews.md"), encoding: .utf8))
            var secrets = ["2026-", root.path, root.lastPathComponent, "from the phone", "from the Mac"]
            if UIDevice.current.name != UIDevice.current.model {
                secrets.append(UIDevice.current.name)
            }
            secrets += status.settings.questions
            secrets += model.conflicts.focusDisputes.flatMap { $0.candidates.map(\.text) }
            for secret in secrets where !secret.isEmpty {
                XCTAssertFalse(report.contains(secret), "the report carries \(secret)")
            }
            for line in journal.split(separator: "\n") where line.count > 12 && !line.hasPrefix("<!--") {
                XCTAssertFalse(report.contains(line), "the report quotes the journal: \(line)")
            }
            let advice = PhoneAdvice.list(diagnostics, journal: model)
            XCTAssertEqual(advice.first { $0.code == "conflicts-open" }?.action, .conflicts)
        }
    }

    func testARecoveryExportHoldsEveryWordAndNoIdentityAndSurvivesAnInterruption() async {
        await reportingOnMain {
            let root = try scratchDirectory(self)
            let fixture = try StagedFixture.stage("conflicted", in: root, with: roundTrip)
            let model = await launch(fixture.location)
            let session = try XCTUnwrap(model.session(on: "2026-04-01"))
            await session.load()
            session.setAnswer(0, "Synthetic: an unsaved line, kept as a draft.")
            let journalBefore = files(under: fixture.location.reflections.deletingLastPathComponent())

            let parent = root.appendingPathComponent("exports", isDirectory: true)
            let export = try await model.exportRecovery(into: parent)
            XCTAssertEqual(files(under: fixture.location.reflections.deletingLastPathComponent()), journalBefore, "exporting changes nothing")
            let exported = files(under: URL(fileURLWithPath: export.directory))
            let all = exported.values.map { String(decoding: $0, as: UTF8.self) }.joined()
            XCTAssertTrue(all.contains("from the phone") && all.contains("from the Mac"), "both versions of the conflicted day")
            XCTAssertTrue(all.contains("Synthetic: an unsaved line, kept as a draft."), "the draft on this iPhone")
            XCTAssertEqual(exported["reflections.md"], try Data(contentsOf: fixture.location.reflections))
            XCTAssertGreaterThanOrEqual(export.conflictVersions, 2)
            XCTAssertEqual(export.drafts, 1)
            let device = try await SenecaEngine().syncStatus(of: fixture.location).device
            XCTAssertFalse(all.contains(device), "no device identity leaves with the export")
            XCTAssertFalse(exported.keys.contains { $0.contains(".seneca") })

            // A second export never lands over the first.
            let again = try await model.exportRecovery(into: parent)
            XCTAssertNotEqual(again.directory, export.directory)

            // An export stopped part-way leaves only a hidden directory that
            // the next export to that name removes; until then nothing looks
            // finished.
            let destination = parent.appendingPathComponent("Interrupted", isDirectory: true)
            let abandoned = parent.appendingPathComponent(".Interrupted.partial-0123456789ab", isDirectory: true)
            try FileManager.default.createDirectory(at: abandoned, withIntermediateDirectories: true)
            try Data("half".utf8).write(to: abandoned.appendingPathComponent("reflections.md"))
            XCTAssertFalse(FileManager.default.fileExists(atPath: destination.path))
            let engine = SenecaEngine()
            try await engine.open(fixture.location)
            let finished = try await engine.exportRecovery(to: destination)
            XCTAssertFalse(FileManager.default.fileExists(atPath: abandoned.path))
            XCTAssertEqual(try Data(contentsOf: URL(fileURLWithPath: finished.directory).appendingPathComponent("reflections.md")),
                           try Data(contentsOf: fixture.location.reflections))
            // One that cannot even start writes nothing anywhere.
            await refusedOnMain("refused") {
                _ = try await engine.exportRecovery(to: root.appendingPathComponent("missing/inside", isDirectory: true))
            }
            XCTAssertFalse(FileManager.default.fileExists(atPath: root.appendingPathComponent("missing").path))
            await refusedOnMain("destination-exists") {
                _ = try await engine.exportRecovery(to: destination)
            }
            XCTAssertEqual(files(under: fixture.location.reflections.deletingLastPathComponent()), journalBefore)
        }
    }

    func testAnUnavailableFolderIsNamedAndNeverStopsWriting() async {
        await reportingOnMain {
            let (_, folder) = try await enrolledDesk(roundTrip, in: try scratchDirectory(self))
            let location = roundTrip.location(in: try scratchDirectory(self))
            try FileManager.default.createDirectory(at: location.reflections.deletingLastPathComponent(), withIntermediateDirectories: true)
            let model = await launch(location, folder: folder)
            let sync = try XCTUnwrap(model.sync)
            await sync.join()

            folder.fault = .unavailable("iCloud Drive is turned off")
            await model.refresh()
            XCTAssertEqual(SyncText.code(sync.phase), "unavailable")
            try await save(model, "Synthetic: written with iCloud away.")
            XCTAssertTrue(try String(contentsOf: location.reflections, encoding: .utf8).contains("Synthetic: written with iCloud away."))
            let diagnostics = try await model.diagnostics()
            XCTAssertNotNil(diagnostics.finding("changes-unpublished"), "what waits is counted")
            let advice = PhoneAdvice.list(diagnostics, journal: model)
            XCTAssertEqual(advice.first?.code, "exchange-fault")
            XCTAssertTrue(advice.first?.message.contains("cannot be reached") == true)

            folder.fault = nil
            await model.refresh()
            XCTAssertEqual(sync.phase, .idle)
            let after = try await model.diagnostics()
            XCTAssertNil(after.finding("changes-unpublished"))
        }
    }

    func testANewIPhoneJoinsAsItselfAndACopyRestoredFromAnotherRejoinsKeepingEveryWord() async {
        await reportingOnMain {
            let root = try scratchDirectory(self)
            let (desk, folder) = try await enrolledDesk(roundTrip, in: root)

            // The old iPhone joins and writes.
            let oldRoot = root.appendingPathComponent("old-phone", isDirectory: true)
            let oldLocation = roundTrip.location(in: oldRoot)
            try FileManager.default.createDirectory(at: oldLocation.reflections.deletingLastPathComponent(), withIntermediateDirectories: true)
            let oldVault = MemoryVault()
            let old = await launch(oldLocation, folder: folder, vault: oldVault)
            await old.sync?.join()
            try await save(old, "Synthetic: written on the old iPhone.", on: "2026-04-01")
            await old.refresh()
            let oldDevice = try await SenecaEngine().syncStatus(of: oldLocation).device

            // A new iPhone set up from the old one's backup: the same
            // container, a keychain of its own.
            let copyRoot = root.appendingPathComponent("restored-phone", isDirectory: true)
            try FileManager.default.copyItem(at: oldRoot, to: copyRoot)
            let copyLocation = roundTrip.location(in: copyRoot)
            let published = Set(folder.files())
            let copy = await launch(copyLocation, folder: folder, vault: MemoryVault())
            let copySync = try XCTUnwrap(copy.sync)
            XCTAssertEqual(copySync.phase, .problem(.identityInUse))
            let ownsBefore = await copySync.ownsIdentity()
            XCTAssertFalse(ownsBefore)
            try await save(copy, "Synthetic: written on the new iPhone before it rejoined.")
            await copy.refresh()
            XCTAssertEqual(copySync.phase, .problem(.identityInUse), "a copy never exchanges as another device")
            XCTAssertEqual(Set(folder.files()), published, "nothing was published under the old iPhone's identity")
            XCTAssertEqual(PhoneAdvice.list(nil, journal: copy).first?.action, .rejoin)

            // Rejoining keeps every word and takes a new identity.
            await copySync.rejoin()
            let copyStatus = try await SenecaEngine().syncStatus(of: copyLocation)
            XCTAssertTrue(copyStatus.enrolled)
            XCTAssertNotEqual(copyStatus.device, oldDevice)
            let ownsAfter = await copySync.ownsIdentity()
            XCTAssertTrue(ownsAfter)
            await copy.refresh()
            XCTAssertEqual(copySync.phase, .idle)
            let copyText = try String(contentsOf: copyLocation.reflections, encoding: .utf8)
            XCTAssertTrue(copyText.contains("Synthetic: written on the old iPhone."))
            XCTAssertTrue(copyText.contains("Synthetic: written on the new iPhone before it rejoined."))
            try await desk.exchange()
            XCTAssertTrue(try desk.text().contains("Synthetic: written on the new iPhone before it rejoined."))
            XCTAssertTrue(try desk.text().contains("Synthetic: written on the old iPhone."))

            // The old iPhone, still in use, exchanges on as itself.
            await old.refresh()
            XCTAssertEqual(old.sync?.phase, .idle)
            XCTAssertTrue(try String(contentsOf: oldLocation.reflections, encoding: .utf8)
                .contains("Synthetic: written on the new iPhone before it rejoined."))

            // A replacement set up fresh, with nothing restored, joins and
            // receives everything.
            let freshLocation = roundTrip.location(in: root.appendingPathComponent("fresh-phone", isDirectory: true))
            try FileManager.default.createDirectory(at: freshLocation.reflections.deletingLastPathComponent(), withIntermediateDirectories: true)
            let fresh = await launch(freshLocation, folder: folder)
            await fresh.sync?.join()
            let freshDevice = try await SenecaEngine().syncStatus(of: freshLocation).device
            XCTAssertFalse([oldDevice, copyStatus.device].contains(freshDevice))
            let freshText = try String(contentsOf: freshLocation.reflections, encoding: .utf8)
            XCTAssertTrue(freshText.contains("Synthetic: written on the old iPhone."))
            XCTAssertTrue(freshText.contains("Synthetic: written on the new iPhone before it rejoined."))
        }
    }

    func testDisconnectingKeepsEveryWordAndForgetsTheFolder() async {
        await reportingOnMain {
            let (_, folder) = try await enrolledDesk(roundTrip, in: try scratchDirectory(self))
            let location = roundTrip.location(in: try scratchDirectory(self))
            try FileManager.default.createDirectory(at: location.reflections.deletingLastPathComponent(), withIntermediateDirectories: true)
            let model = await launch(location, folder: folder)
            let sync = try XCTUnwrap(model.sync)
            await sync.join()
            try await save(model, "Synthetic: written before disconnecting.")
            let before = try Data(contentsOf: location.reflections)

            await sync.disconnect(forgetting: model.grantStore)
            XCTAssertFalse(sync.isEnrolled)
            XCTAssertEqual(model.status?.syncEnrolled, false)
            XCTAssertEqual(try Data(contentsOf: location.reflections), before)
            let status = try await SenecaEngine().syncStatus(of: location)
            XCTAssertFalse(status.enrolled)
            try await save(model, "Synthetic: written after disconnecting.", on: "2026-04-01")
            XCTAssertTrue(try String(contentsOf: location.reflections, encoding: .utf8).contains("Synthetic: written after disconnecting."))
        }
    }
}

extension SenecaEngine {
    /// Opens `location` on this engine and reads its sync status.
    func syncStatus(of location: JournalLocation) async throws -> SyncStatus {
        try open(location)
        return try syncStatus()
    }
}
