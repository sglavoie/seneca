import SenecaEngine
import XCTest
@testable import SenecaMobile

/// The exchange between this iPhone and the folder a file service carries:
/// what crosses, in what order, and what every fault of the transport leaves
/// behind. The journal on each side is the real engine's; the folder is a
/// directory with the faults scripted over it.
@MainActor
final class ExchangeTests: XCTestCase {
    private var roundTrip: RoundTrip!
    private var desk: Desk!
    private var folder: FaultyFolder!
    private var phone: SenecaEngine!
    private var location: JournalLocation!
    private var service: ExchangeService!

    private let today = "2026-04-02"

    override func setUp() async throws {
        roundTrip = try RoundTrip.load()
        (desk, folder) = try await enrolledDesk(roundTrip, in: try scratchDirectory(self))
        location = roundTrip.location(in: try scratchDirectory(self))
        try FileManager.default.createDirectory(at: location.reflections.deletingLastPathComponent(), withIntermediateDirectories: true)
        phone = SenecaEngine()
        try await phone.open(location)
        service = ExchangeService(
            engine: phone, access: FaultyAccess(folder: folder), directory: AppPaths.exchangeDirectory(for: location)
        )
    }

    private func exchange() async throws -> ExchangeReport {
        try await service.exchange(ExchangeCancellation())
    }

    /// The phone joins the journal in the folder and holds what the Mac does.
    private func join() async throws {
        let looked = try await exchange()
        XCTAssertFalse(looked.enrolled)
        XCTAssertTrue(looked.pulled.journalInFolder)
        try await service.join(name: "phone")
        let first = try await exchange()
        XCTAssertEqual(first.imported?.applied.count, 1)
        XCTAssertTrue(first.settled)
        folder.forget()
    }

    private func write(_ answers: [String], on date: String? = nil) async throws {
        let prepared = try await phone.prepareReflection(on: date)
        _ = try await phone.saveReflection(ReflectionWriting(draft: prepared.draft, answers: answers))
    }

    private func phoneText() throws -> String {
        try String(contentsOf: location.reflections, encoding: .utf8)
    }

    private func fault(_ body: () async throws -> Void, file: StaticString = #filePath, line: UInt = #line) async -> ExchangeFault? {
        do {
            try await body()
            XCTFail("the exchange went through", file: file, line: line)
        } catch let fault as ExchangeFault {
            return fault
        } catch {
            XCTFail("\(error)", file: file, line: line)
        }
        return nil
    }

    // MARK: What crosses, and in what order

    func testContentsArePublishedBeforeManifestsAndOnlyExchangeFilesLeaveThePhone() async throws {
        try await join()
        try await write(["Written on the phone.", "", ""])
        let report = try await exchange()
        XCTAssertEqual(report.pushed.manifests, 1)
        XCTAssertGreaterThanOrEqual(report.pushed.objects, 1)

        let sent = folder.published
        let lastObject = try XCTUnwrap(sent.lastIndex { $0.hasPrefix("objects/") })
        let firstManifest = try XCTUnwrap(sent.firstIndex { $0.hasPrefix("transactions/") })
        XCTAssertLessThan(lastObject, firstManifest, "a manifest was published before its contents: \(sent)")
        if let receipt = sent.firstIndex(where: { $0.hasPrefix("receipts/") }) {
            XCTAssertGreaterThan(receipt, firstManifest)
        }

        // Nothing of the live journal is in the folder: only the exchange's
        // own immutable files.
        for file in folder.files() {
            XCTAssertTrue(
                file == "journal.json" || file.hasPrefix("objects/") || file.hasPrefix("transactions/") || file.hasPrefix("receipts/"),
                "\(file) does not belong in an exchange"
            )
        }
        let container = location.stateDirectory.deletingLastPathComponent().standardizedFileURL.path
        XCTAssertTrue(service.mirrorDirectory.standardizedFileURL.path.hasPrefix(container), "the engine exchanges outside the container")

        // Exchanging again sends nothing and replaces nothing.
        let before = try folder.files().map { try sha256(of: folder.directory.appendingPathComponent($0)) }
        folder.forget()
        let again = try await exchange()
        XCTAssertEqual(again.pushed.files, 0)
        XCTAssertEqual(folder.published, [])
        XCTAssertEqual(try folder.files().map { try sha256(of: folder.directory.appendingPathComponent($0)) }, before)

        try await desk.exchange()
        XCTAssertTrue(try desk.text().contains("Written on the phone."))
    }

    // MARK: Placeholders and partial downloads

    func testAFileStillInTheCloudIsAskedForAndAppliedOnlyOnceItIsHere() async throws {
        try await join()
        let had = Set(folder.files())
        try await desk.write(["Written on the Mac.", "", ""])
        try await desk.exchange()
        let arrived = folder.files().filter { !had.contains($0) }
        let contents = try XCTUnwrap(arrived.first { $0.hasPrefix("objects/") })
        let manifest = try XCTUnwrap(arrived.first { $0.hasPrefix("transactions/") })
        let before = try phoneText()

        // Listed, and none of it delivered: each file is asked for and none
        // is read.
        folder.away = [contents, manifest]
        var report = try await exchange()
        XCTAssertEqual(report.pulled.waiting, 2)
        XCTAssertFalse(report.settled)
        XCTAssertEqual(report.imported?.applied, [])
        XCTAssertEqual(Set(folder.asked), [contents, manifest])
        XCTAssertFalse(folder.read.contains(contents) || folder.read.contains(manifest), "a file that is not here was read")
        XCTAssertEqual(try phoneText(), before)

        // The manifest arrives before the contents it names, which is the
        // order a publisher never uses and a file service is free to.
        folder.away = [contents]
        folder.forget()
        report = try await exchange()
        XCTAssertEqual(report.pulled.waiting, 1)
        XCTAssertEqual(folder.asked, [contents])
        XCTAssertFalse(report.settled)
        XCTAssertEqual(report.imported?.applied, [])
        XCTAssertEqual(report.imported?.diagnostics.map(\.code), ["incomplete"])
        XCTAssertEqual(try phoneText(), before)

        folder.away = []
        report = try await exchange()
        XCTAssertTrue(report.settled)
        XCTAssertEqual(report.imported?.applied.count, 1)
        XCTAssertTrue(report.journalChanged)
        XCTAssertTrue(try phoneText().contains("Written on the Mac."))
    }

    func testAFileCutShortNeverEntersTheMirror() async throws {
        try await join()
        let had = Set(folder.files())
        try await desk.write(["Written on the Mac.", "", ""])
        try await desk.exchange()
        let arrived = folder.files().filter { !had.contains($0) }
        let before = try phoneText()

        // Every file of the change arrives cut in half.
        folder.cutShort = Set(arrived)
        for _ in 0..<2 {
            let report = try await exchange()
            XCTAssertEqual(report.pulled.damaged, arrived.count)
            XCTAssertFalse(report.settled)
            XCTAssertEqual(report.imported?.applied, [])
            XCTAssertEqual(try phoneText(), before)
        }
        for file in arrived {
            XCTAssertFalse(
                FileManager.default.fileExists(atPath: service.mirrorDirectory.appendingPathComponent(file).path),
                "\(file) entered the mirror cut short"
            )
        }

        folder.cutShort = []
        let report = try await exchange()
        XCTAssertTrue(report.settled)
        XCTAssertEqual(report.imported?.applied.count, 1)
        XCTAssertTrue(try phoneText().contains("Written on the Mac."))
    }

    // MARK: A folder that is not there, and a grant taken back

    func testAnUnreachableFolderOrARevokedGrantNeverStopsWriting() async throws {
        try await join()
        let published = try await service.delivery()

        folder.fault = .unavailable("iCloud Drive is off")
        let unreachable = await fault { _ = try await self.exchange() }
        XCTAssertEqual(unreachable, .unavailable("iCloud Drive is off"))
        XCTAssertEqual(unreachable?.retryable, true)

        // Writing goes on, and the app knows the change is here only.
        try await write(["Written with no folder.", "", ""])
        XCTAssertTrue(try phoneText().contains("Written with no folder."))
        var delivery = try await service.delivery()
        XCTAssertEqual(delivery?.recorded, (published?.recorded ?? 0) + 1)
        XCTAssertEqual(delivery?.published, published?.published)

        folder.fault = .revoked("the folder was removed")
        let revoked = await fault { _ = try await self.exchange() }
        XCTAssertEqual(revoked, .revoked("the folder was removed"))
        XCTAssertEqual(revoked?.retryable, false)
        try await write(["Written with no grant.", "", ""], on: "2026-04-01")
        XCTAssertEqual(folder.published, [], "something reached a folder that could not be opened")

        folder.fault = nil
        let report = try await exchange()
        XCTAssertEqual(report.pushed.manifests, 2)
        delivery = report.delivery
        XCTAssertEqual(delivery?.published, delivery?.recorded)
        try await desk.exchange()
        XCTAssertTrue(try desk.text().contains("Written with no folder."))
        XCTAssertTrue(try desk.text().contains("Written with no grant."))
    }

    // MARK: Cancellation and retry

    func testAPublicationStoppedPartWayIsInvisibleAndTheNextOneFinishesIt() async throws {
        try await join()
        let had = Set(folder.files())
        try await write(["Sent in two halves.", "", ""])
        let macBefore = try desk.text()

        folder.cancelAfter = 1
        let stopped = await fault { _ = try await self.exchange() }
        XCTAssertEqual(stopped, .cancelled)
        let partial = folder.files().filter { !had.contains($0) }
        XCTAssertEqual(partial.count, 1)
        XCTAssertTrue(partial.allSatisfy { $0.hasPrefix("objects/") }, "a stopped publication left \(partial)")

        // The Mac sees contents nothing names: nothing to apply, nothing wrong.
        let nothing = try await desk.exchange()
        XCTAssertEqual(nothing.applied, [])
        XCTAssertEqual(nothing.diagnostics, [])
        XCTAssertEqual(try desk.text(), macBefore)
        let unsent = try await service.delivery()
        XCTAssertEqual(unsent?.published, (unsent?.recorded ?? 0) - 1, "an unfinished publication was counted as published")

        folder.forget()
        let finished = try await exchange()
        XCTAssertEqual(finished.pushed.manifests, 1)
        XCTAssertFalse(folder.published.contains(partial[0]), "what was already sent was sent again")
        let applied = try await desk.exchange()
        XCTAssertEqual(applied.applied.count, 1)
        XCTAssertTrue(try desk.text().contains("Sent in two halves."))
    }

    func testAnExchangeCancelledBeforeItStartsDoesNothing() async throws {
        try await join()
        try await write(["Not sent yet.", "", ""])
        let cancellation = ExchangeCancellation()
        cancellation.cancel()
        let stopped = await fault { _ = try await self.service.exchange(cancellation) }
        XCTAssertEqual(stopped, .cancelled)
        XCTAssertEqual(folder.published, [])
        XCTAssertTrue(try phoneText().contains("Not sent yet."))
    }

    // MARK: Receipts

    func testDeliveryTellsSavedFromPublishedFromApplied() async throws {
        try await join()
        let stood = try await service.delivery()
        let start = try XCTUnwrap(stood)

        try await write(["Written offline.", "", ""])
        let saved = try await service.delivery()
        var delivery = try XCTUnwrap(saved)
        XCTAssertEqual(delivery.recorded, start.recorded + 1)
        XCTAssertEqual(delivery.published, start.published, "a save was reported as published")

        let sent = try await exchange()
        delivery = try XCTUnwrap(sent.delivery)
        XCTAssertEqual(delivery.published, delivery.recorded)
        XCTAssertEqual(delivery.uploaded, start.uploaded, "a file just written to the folder was reported as uploaded")
        XCTAssertEqual(SyncText.summary(delivery), "Everything is in the exchange folder, waiting to upload.")
        XCTAssertTrue(delivery.peers.allSatisfy { $0.applied < delivery.recorded }, "published was reported as applied: \(delivery)")

        // The Mac applies it and says so; only then does the phone.
        let applied = try await desk.exchange()
        XCTAssertEqual(applied.applied.count, 1)
        XCTAssertTrue(try desk.text().contains("Written offline."))
        let confirmed = try await exchange()
        delivery = try XCTUnwrap(confirmed.delivery)
        let mac = try XCTUnwrap(delivery.peers.first { $0.name == "mac" })
        XCTAssertEqual(mac.applied, delivery.recorded)
        XCTAssertEqual(delivery.uploaded, delivery.recorded)
        XCTAssertEqual(SyncText.summary(delivery), "Everything is on mac.")

        // And the phone's own receipt tells the Mac the same of its changes.
        try await desk.write(["Edited on the Mac.", "", ""], on: "2026-03-05")
        try await desk.exchange()
        let back = try await exchange()
        XCTAssertTrue(back.journalChanged)
        let seenFromMac = try await desk.engine.syncDelivery(exchange: desk.folder)
        let phonePeer = try XCTUnwrap(seenFromMac.peers.first { $0.name == "phone" })
        XCTAssertEqual(phonePeer.applied, seenFromMac.recorded)
    }

    // MARK: Simultaneous edits

    func testEditsMadeOnBothSidesAtOnceAreBothKept() async throws {
        try await join()
        // The same day, written on both before either hears of the other.
        try await desk.write(["From the Mac.", "", ""])
        try await write(["From the phone.", "", ""])
        try await desk.exchange()
        let report = try await exchange()
        XCTAssertEqual(report.imported?.conflicts, ["entry:\(today)"])
        XCTAssertEqual(report.conflicts, 1)
        try await desk.exchange()

        XCTAssertTrue(try phoneText().contains("From the phone."))
        XCTAssertTrue(try desk.text().contains("From the Mac."))
        let phoneHolds = try await phone.conflicts()
        let macHolds = try await desk.engine.conflicts()
        let onPhone = try XCTUnwrap(phoneHolds.conflicts.first)
        let onMac = try XCTUnwrap(macHolds.conflicts.first)
        XCTAssertEqual(onPhone.id, onMac.id)
        XCTAssertTrue(onPhone.theirs.contains { $0.content.contains("From the Mac.") })
        XCTAssertTrue(onMac.theirs.contains { $0.content.contains("From the phone.") })
    }

    // MARK: Setting up

    func testAnEmptyFolderIsStartedFromThePhoneAndADescriptorOnItsWayIsWaitedFor() async throws {
        // A folder whose descriptor is listed and not here is not empty.
        folder.away = ["journal.json"]
        var looked = try await exchange()
        XCTAssertTrue(looked.pulled.journalInFolder)
        XCTAssertFalse(looked.settled)
        XCTAssertEqual(folder.asked, ["journal.json"])
        do {
            try await service.join(name: "phone")
            XCTFail("joined a journal whose descriptor has not arrived")
        } catch let problem as EngineProblem {
            XCTAssertFalse(problem.message.isEmpty)
        }

        // An empty folder: the phone's own journal is published to it.
        let empty = FaultyFolder(directory: try scratchDirectory(self))
        let alone = ExchangeService(
            engine: phone, access: FaultyAccess(folder: empty), directory: try scratchDirectory(self)
        )
        looked = try await alone.exchange(ExchangeCancellation())
        XCTAssertFalse(looked.enrolled)
        XCTAssertFalse(looked.pulled.journalInFolder)
        XCTAssertEqual(empty.files(), [])
        try await write(["The phone's own.", "", ""])
        try await alone.enroll(name: "phone")
        let first = try await alone.exchange(ExchangeCancellation())
        XCTAssertTrue(first.enrolled)
        XCTAssertTrue(first.pushed.descriptor)
        XCTAssertTrue(empty.files().contains("journal.json"))
        XCTAssertEqual(first.delivery?.published, first.delivery?.recorded)
    }
}
