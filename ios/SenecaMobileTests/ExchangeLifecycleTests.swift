import SenecaEngine
import XCTest
@testable import SenecaMobile

/// When the app exchanges, what it shows while it cannot, and how it gets at
/// the folder: the journal model and the sync model over the real engine,
/// with the folder's faults scripted.
@MainActor
final class ExchangeLifecycleTests: XCTestCase {
    private var roundTrip: RoundTrip!
    private var desk: Desk!
    private var folder: FaultyFolder!
    private var location: JournalLocation!

    private let today = "2026-04-02"

    override func setUp() async throws {
        roundTrip = try RoundTrip.load()
        (desk, folder) = try await enrolledDesk(roundTrip, in: try scratchDirectory(self))
        location = roundTrip.location(in: try scratchDirectory(self))
        try FileManager.default.createDirectory(at: location.reflections.deletingLastPathComponent(), withIntermediateDirectories: true)
    }

    /// A launch of the app over the container, with the folder chosen.
    private func launch(file: StaticString = #filePath, line: UInt = #line) async throws -> (JournalModel, SyncModel) {
        let location = location!
        let folder = folder!
        let model = JournalModel(engine: SenecaEngine(), location: { location }, access: { _ in FaultyAccess(folder: folder) })
        await model.open()
        XCTAssertEqual(model.phase, .ready, file: file, line: line)
        let sync = try XCTUnwrap(model.sync, file: file, line: line)
        sync.retryDelay = { _ in .milliseconds(40) }
        await eventually("the launch exchange", file: file, line: line) {
            sync.exchanges.contains(.launch) && sync.phase != .exchanging
        }
        return (model, sync)
    }

    /// A phone that joined the Mac's journal and holds it.
    private func joined(file: StaticString = #filePath, line: UInt = #line) async throws -> (JournalModel, SyncModel) {
        let (model, sync) = try await launch(file: file, line: line)
        XCTAssertEqual(sync.phase, .setup(journalInFolder: true, arriving: false), file: file, line: line)
        await sync.join()
        XCTAssertTrue(sync.isEnrolled, file: file, line: line)
        XCTAssertEqual(model.entries.count, 4, "joining did not bring the journal to the screens", file: file, line: line)
        return (model, sync)
    }

    private func save(_ model: JournalModel, _ answers: [String], on date: String? = nil) async throws {
        let prepared = try await model.prepareReflection(on: date)
        _ = try await model.save(ReflectionWriting(draft: prepared.draft, answers: answers), requestID: RequestID())
    }

    private func settled(_ sync: SyncModel, after reason: SyncModel.Reason, file: StaticString = #filePath, line: UInt = #line) async {
        await eventually("the \(reason) exchange", file: file, line: line) {
            sync.exchanges.contains(reason) && sync.phase != .exchanging
        }
    }

    func testTheAppExchangesAtLaunchInFrontAfterASaveAndOnRefresh() async throws {
        let (model, sync) = try await joined()
        XCTAssertEqual(sync.folderName, "Seneca Exchange")

        // In front again: what the Mac wrote meanwhile is on the screens.
        try await desk.write(["Written on the Mac.", "", ""])
        try await desk.exchange()
        XCTAssertFalse(model.hasEntry(on: today))
        await model.cameForward()
        XCTAssertEqual(sync.exchanges.last, .foreground)
        XCTAssertTrue(model.hasEntry(on: today))

        // A save is on the screens at once and is sent without being asked.
        try await save(model, ["Saved on the phone.", "", ""], on: "2026-04-01")
        XCTAssertTrue(model.hasEntry(on: "2026-04-01"))
        await settled(sync, after: .save)
        XCTAssertEqual(sync.phase, .idle)
        XCTAssertEqual(sync.delivery?.published, sync.delivery?.recorded)
        let received = try await desk.exchange()
        XCTAssertEqual(received.applied.count, 1)
        XCTAssertTrue(try desk.text().contains("Saved on the phone."))

        // Pull to refresh waits for the exchange and shows what it brought.
        try await desk.write(["Another day on the Mac.", "", ""], on: "2026-03-31")
        try await desk.exchange()
        await model.refresh()
        XCTAssertEqual(sync.exchanges.last, .refresh)
        XCTAssertTrue(model.hasEntry(on: "2026-03-31"))
        let mac = try XCTUnwrap(sync.delivery?.peers.first { $0.name == "mac" })
        XCTAssertEqual(mac.applied, sync.delivery?.recorded, "the Mac's receipt did not reach the screens")

        // The next launch exchanges before anything is asked of it.
        try await desk.write(["While the app was closed.", "", ""], on: "2026-03-30")
        try await desk.exchange()
        let (again, relaunched) = try await launch()
        await eventually("the journal the launch exchange brought") { again.hasEntry(on: "2026-03-30") }
        XCTAssertEqual(relaunched.exchanges.first, .launch)
        XCTAssertTrue(relaunched.isEnrolled)

        XCTAssertEqual(folder.mostOpen, 1, "two exchanges had the folder open at once")
    }

    func testRequestsMadeDuringAnExchangeNeverRunBesideIt() async throws {
        let (_, sync) = try await joined()
        let before = sync.exchanges.count
        async let first: Void = sync.exchange(.refresh)
        async let second: Void = sync.exchange(.refresh)
        async let third: Void = sync.exchange(.foreground)
        async let fourth: Void = sync.exchange(.save)
        _ = await (first, second, third, fourth)
        XCTAssertEqual(folder.mostOpen, 1, "two exchanges had the folder open at once")
        XCTAssertLessThanOrEqual(sync.exchanges.count - before, 2, "requests made during an exchange each ran one of their own")
        XCTAssertGreaterThanOrEqual(sync.exchanges.count - before, 1)
    }

    func testWhatIsStillArrivingIsTriedAgainUntilItIsHere() async throws {
        let (model, sync) = try await joined()
        let had = Set(folder.files())
        try await desk.write(["Slow to arrive.", "", ""])
        try await desk.exchange()
        let contents = try XCTUnwrap(folder.files().first { !had.contains($0) && $0.hasPrefix("objects/") })

        folder.away = [contents]
        await model.refresh()
        XCTAssertEqual(sync.phase, .arriving(2), "one file asked for, and one change waiting on it")
        XCTAssertFalse(model.hasEntry(on: today))
        XCTAssertNotNil(SyncText.problem(sync.phase))

        // Nobody asks again; the app does, more than once.
        await eventually("the retries") { sync.exchanges.filter { $0 == .retry }.count >= 3 }
        XCTAssertFalse(model.hasEntry(on: today))
        XCTAssertGreaterThanOrEqual(folder.asked.filter { $0 == contents }.count, 3)

        folder.away = []
        await eventually("the entry once its contents arrived") { model.hasEntry(on: today) && sync.phase == .idle }
        // And once nothing is on its way, it stops asking.
        let retries = sync.exchanges.count
        try await Task.sleep(for: .milliseconds(400))
        XCTAssertEqual(sync.exchanges.count, retries)
    }

    func testImportRefreshesAfterPublicationFailure() async throws {
        let location = location!
        let folder = folder!
        let center = FakeReminderCenter(.allowed)
        let model = JournalModel(
            engine: SenecaEngine(), location: { location }, access: { _ in FaultyAccess(folder: folder) },
            reminderCenter: center, identityVault: MemoryVault()
        )
        await model.open()
        let sync = try XCTUnwrap(model.sync)
        sync.retryDelay = { _ in .milliseconds(40) }
        await settled(sync, after: .launch)
        await sync.join()
        XCTAssertTrue(sync.isEnrolled)
        let reminders = try XCTUnwrap(model.reminders)
        await reminders.enable()
        XCTAssertTrue(center.days.contains(today))

        // The Mac's reflection is imported, and then the folder takes nothing.
        try await desk.write(["Imported before the folder stopped.", "", ""], on: today)
        try await desk.exchange()
        folder.publishFault = .unavailable("the upload stopped")
        let imports = reminders.reconciliations.filter { $0 == .import }.count
        await sync.exchange(.save)
        XCTAssertEqual(sync.phase, .problem(.unavailable("the upload stopped")))
        XCTAssertTrue(model.hasEntry(on: today), "what was imported did not reach the screens")
        XCTAssertEqual(reminders.reconciliations.filter { $0 == .import }.count, imports + 1)
        XCTAssertFalse(center.days.contains(today), "tonight's reminder outlived the reflection that answers it")
        XCTAssertFalse(reminders.stillDue(today))

        // The retries find nothing more to import, still fail, then succeed.
        await eventually("a retry that still fails") {
            sync.exchanges.contains(.retry) && sync.phase == .problem(.unavailable("the upload stopped"))
        }
        XCTAssertTrue(model.hasEntry(on: today))
        folder.publishFault = nil
        await eventually("the retry that publishes") { sync.phase == .idle }
        XCTAssertEqual(sync.delivery?.published, sync.delivery?.recorded)
        XCTAssertTrue(model.hasEntry(on: today))
        XCTAssertFalse(center.days.contains(today))
        XCTAssertFalse(reminders.stillDue(today))
        let received = try await desk.exchange()
        XCTAssertTrue(received.applied.isEmpty, "the phone sent a change of its own")
    }

    func testAFolderThatCannotBeReachedIsSaidSoAndNeverStopsWriting() async throws {
        let (model, sync) = try await joined()

        folder.fault = .unavailable("iCloud Drive is off")
        await model.refresh()
        XCTAssertEqual(sync.phase, .problem(.unavailable("iCloud Drive is off")))
        try await save(model, ["Written with no folder.", "", ""])
        XCTAssertTrue(model.hasEntry(on: today))
        await eventually("the save's own attempt") { sync.exchanges.contains(.save) && sync.phase != .exchanging }
        let delivery = try XCTUnwrap(sync.delivery)
        XCTAssertEqual(delivery.published, delivery.recorded - 1)
        XCTAssertEqual(SyncText.summary(delivery), "1 change is saved on this iPhone only.")
        // An unreachable folder is tried again without being asked.
        await eventually("a retry") { sync.exchanges.contains(.retry) }

        // A grant that no longer works is not: only the author can fix it.
        folder.fault = .revoked("the folder was removed")
        await model.refresh()
        XCTAssertEqual(sync.phase, .problem(.revoked("the folder was removed")))
        XCTAssertTrue(try XCTUnwrap(SyncText.problem(sync.phase)).contains("Choose it again"))
        let attempts = sync.exchanges.count
        try await Task.sleep(for: .milliseconds(400))
        XCTAssertEqual(sync.exchanges.count, attempts, "a revoked grant was retried")
        try await save(model, ["Written with no grant.", "", ""], on: "2026-04-01")
        XCTAssertTrue(model.hasEntry(on: "2026-04-01"))

        folder.fault = nil
        await model.refresh()
        XCTAssertEqual(sync.phase, .idle)
        XCTAssertEqual(sync.delivery?.published, sync.delivery?.recorded)
        try await desk.exchange()
        XCTAssertTrue(try desk.text().contains("Written with no folder."))
        XCTAssertTrue(try desk.text().contains("Written with no grant."))
    }

    func testLeavingTheScreenExchangesOnceAndSchedulesNothing() async throws {
        let (model, sync) = try await joined()
        try await save(model, ["Written just before leaving.", "", ""])
        await settled(sync, after: .save)

        folder.fault = .unavailable("no connection")
        await model.wentBack()
        XCTAssertEqual(sync.exchanges.last, .background)
        XCTAssertEqual(sync.phase, .problem(.unavailable("no connection")))
        let attempts = sync.exchanges.count
        try await Task.sleep(for: .milliseconds(400))
        XCTAssertEqual(sync.exchanges.count, attempts, "the app kept exchanging in the background")

        folder.fault = nil
        await model.cameForward()
        XCTAssertEqual(sync.exchanges.last, .foreground)
        XCTAssertEqual(sync.phase, .idle)
    }

    // MARK: The grant

    func testTheGrantToAFolderOutlastsTheAppAndSaysWhenItStoppedWorking() async throws {
        let scratch = try scratchDirectory(self)
        let file = scratch.appendingPathComponent("exchange/grant.json")
        let store = GrantStore(file: file)
        XCTAssertNil(store.folderName)
        XCTAssertThrowsError(try store.open(ExchangeCancellation())) { XCTAssertEqual($0 as? ExchangeFault, .notChosen) }

        try store.choose(desk.folder)
        XCTAssertEqual(store.folderName, "Seneca Exchange")

        // Another launch: nothing in memory, the same folder.
        let later = GrantStore(file: file)
        let (opened, close) = try later.open(ExchangeCancellation())
        XCTAssertTrue(try opened.list("").contains { $0.name == "journal.json" })
        close()

        // The whole exchange through the grant, as the app runs it.
        let phone = SenecaEngine()
        try await phone.open(location)
        let service = ExchangeService(engine: phone, access: later, directory: scratch.appendingPathComponent("exchange"))
        let looked = try await service.exchange(ExchangeCancellation())
        XCTAssertTrue(looked.pulled.journalInFolder)
        try await service.join(name: "phone")
        let first = try await service.exchange(ExchangeCancellation())
        XCTAssertEqual(first.imported?.applied.count, 1)

        // A folder inside the granted one is reached through the same grant.
        let inside = GrantStore(file: file, subfolder: "inner")
        let (inner, closeInner) = try inside.open(ExchangeCancellation())
        XCTAssertTrue(try inner.publish(Data("x".utf8), to: "objects/one"))
        closeInner()
        XCTAssertTrue(FileManager.default.fileExists(atPath: desk.folder.appendingPathComponent("inner/objects/one").path))

        // The folder is gone: the grant says so, and says it cannot be retried.
        try FileManager.default.removeItem(at: desk.folder)
        do {
            _ = try await service.exchange(ExchangeCancellation())
            XCTFail("exchanged with a folder that is gone")
        } catch let fault as ExchangeFault {
            guard case .revoked = fault else {
                return XCTFail("a folder that is gone was reported as \(fault)")
            }
            XCTAssertFalse(fault.retryable)
        }
        _ = try await phone.entries()

        later.forget()
        XCTAssertNil(GrantStore(file: file).folderName)
        do {
            _ = try await service.exchange(ExchangeCancellation())
            XCTFail("exchanged with no folder chosen")
        } catch let fault as ExchangeFault {
            XCTAssertEqual(fault, .notChosen)
        }
    }

    // MARK: The folder through file coordination

    func testTheCoordinatedFolderListsPlaceholdersAndNeverReplacesAFile() async throws {
        let root = try scratchDirectory(self).appendingPathComponent("folder", isDirectory: true)
        let objects = root.appendingPathComponent("objects", isDirectory: true)
        try FileManager.default.createDirectory(at: objects, withIntermediateDirectories: true)
        try Data("here".utf8).write(to: objects.appendingPathComponent("here"))
        try Data().write(to: objects.appendingPathComponent(".away.icloud"))
        try Data().write(to: objects.appendingPathComponent(".DS_Store"))

        let cancellation = ExchangeCancellation()
        let folder = CoordinatedFolder(root: root, cancellation: cancellation)
        XCTAssertFalse(folder.isUbiquitous)
        XCTAssertEqual(try folder.list(""), [CloudItem(name: "objects", isDirectory: true)])
        XCTAssertEqual(try folder.list("objects"), [
            CloudItem(name: "away", isDownloaded: false, isUploaded: true),
            CloudItem(name: "here"),
        ])
        XCTAssertEqual(try folder.list("transactions"), [], "a directory that is not there holds nothing")
        XCTAssertEqual(try folder.read("objects/here"), Data("here".utf8))

        XCTAssertTrue(try folder.publish(Data("new".utf8), to: "transactions/device/one.json"))
        XCTAssertEqual(try folder.read("transactions/device/one.json"), Data("new".utf8))
        XCTAssertFalse(try folder.publish(Data("other".utf8), to: "transactions/device/one.json"))
        XCTAssertEqual(try folder.read("transactions/device/one.json"), Data("new".utf8), "a published file was replaced")
        XCTAssertFalse(try folder.publish(Data("again".utf8), to: "objects/away"), "a file that is in the cloud was published over")
        XCTAssertFalse(FileManager.default.fileExists(atPath: objects.appendingPathComponent("away").path))
        // Nothing half-written is left beside what was published.
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: root.appendingPathComponent("transactions/device").path), ["one.json"])

        let gone = CoordinatedFolder(root: root.appendingPathComponent("missing"), cancellation: cancellation)
        XCTAssertThrowsError(try gone.list("")) {
            guard case .unavailable = $0 as? ExchangeFault else { return XCTFail("\($0)") }
        }

        cancellation.cancel()
        XCTAssertThrowsError(try folder.list("")) { XCTAssertEqual($0 as? ExchangeFault, .cancelled) }
        XCTAssertThrowsError(try folder.publish(Data("late".utf8), to: "objects/late")) { XCTAssertEqual($0 as? ExchangeFault, .cancelled) }
        XCTAssertFalse(FileManager.default.fileExists(atPath: objects.appendingPathComponent("late").path))
    }
}
