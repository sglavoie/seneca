import SenecaEngine
import UIKit
import XCTest
@testable import SenecaMobile

/// The phone's half of the end-to-end exchange with a Mac, one phase per
/// test. ios/scripts/sync-evidence.sh drives it: it runs a phase here, does
/// the Mac's part with the real `seneca` program in a Git repository, and
/// holds what each side reports against the other.
///
/// The journal is a synthetic one in a directory of the run's own inside the
/// app's container; the installation's journal is never opened. On an iPhone
/// the exchange folder is a folder of the run's own inside the one the
/// author chose in the app, reached through that grant and through iCloud
/// Drive. On a simulator it is the plain directory SENECA_SYNC_FOLDER names,
/// which rehearses the scenario and proves nothing about iCloud.
///
/// Every phase needs SENECA_SYNC_RUN and fails without it.
final class SyncEvidenceTests: XCTestCase {
    private static let conflictNote = " (phone version)"
    private static let mergeNote = "\n- added on the phone"

    // MARK: Phases

    func testProbe() async {
        await phase("probe", creating: true) { run in
            let (folder, close) = try run.access.base.open(ExchangeCancellation())
            defer { close() }
            let wrote = try folder.publish(Data("{\"run\": \"\(run.name)\"}\n".utf8), to: "probe.json")
            XCTAssertTrue(wrote, "run \(run.name) already has a folder")
            return [:]
        }
    }

    func testJoin() async {
        await phase("join") { run in
            try await run.exchangeUntil("the Mac's journal to arrive in the folder") { report in
                !report.enrolled && report.pulled.journalInFolder && report.settled
            }
            try await run.service.join(name: "phone")
            try await run.exchangeUntil("the Mac's journal to be applied") { report in
                guard report.enrolled, report.settled else { return false }
                return try await run.engine.entries().entries.count == 4
            }
            return [:]
        }
    }

    /// The reflection is saved with the folder never opened: nothing here
    /// can reach a network, and nothing leaves the phone.
    func testSaveOffline() async {
        await phase("save-offline") { run in
            let stood = try await run.service.delivery()
            let before = try XCTUnwrap(stood)
            let prepared = try await run.engine.prepareReflection()
            let saved = try await run.engine.saveReflection(run.roundTrip.writing(on: prepared.draft))
            let stands = try await run.service.delivery()
            let after = try XCTUnwrap(stands)
            XCTAssertEqual(after.recorded, before.recorded + 1)
            XCTAssertEqual(after.published, before.published, "a save was reported as published")
            XCTAssertEqual(run.access.plan.opened, 0, "the folder was opened by a save")
            return [
                "date": saved.value.entry.date,
                "revision": saved.value.entry.revision,
                "desktop_revision": run.roundTrip.expected.entryRevision,
                "folder_opened": run.access.plan.opened,
            ]
        }
    }

    func testPublish() async {
        await phase("publish") { run in
            try await run.exchangeUntil("the reflection to be in the folder") { report in
                guard let delivery = report.delivery else { return false }
                return delivery.published == delivery.recorded
            }
            return [:]
        }
    }

    /// The Mac's own word that the reflection is in its journal.
    func testConfirm() async {
        await phase("confirm") { run in
            try await run.exchangeUntil("the Mac's receipt") { report in
                guard let delivery = report.delivery else { return false }
                return delivery.peers.contains { $0.name == "mac" && $0.applied == delivery.recorded }
            }
            return [:]
        }
    }

    /// An edit made on the Mac arrives.
    func testReceive() async {
        await phase("receive") { run in
            let date = try run.expected("date"), revision = try run.expected("revision")
            try await run.exchangeUntil("the Mac's edit of \(date)") { _ in
                try await run.engine.entry(on: date).revision == revision
            }
            return ["date": date, "revision": revision]
        }
    }

    /// Both sides edit before either hears of the other: one day in parts
    /// that do not overlap, one day in the same part.
    func testSimultaneous() async {
        await phase("simultaneous") { run in
            let mergeDate = try run.expected("merge_date"), conflictDate = try run.expected("conflict_date")
            let macMerge = try run.expected("mac_merge_text"), macConflict = try run.expected("mac_conflict_text")

            let merging = try await run.engine.prepareReflection(on: mergeDate)
            var answers = try XCTUnwrap(merging.draft.initial)
            answers[1] += Self.mergeNote
            _ = try await run.engine.saveReflection(ReflectionWriting(
                draft: merging.draft, answers: answers, followThrough: merging.draft.context.followInitial
            ))
            let conflicting = try await run.engine.prepareReflection(on: conflictDate)
            answers = try XCTUnwrap(conflicting.draft.initial)
            answers[0] += Self.conflictNote
            _ = try await run.engine.saveReflection(ReflectionWriting(draft: conflicting.draft, answers: answers))
            let mine = try await run.engine.entry(on: conflictDate)
            XCTAssertFalse(mine.raw.contains(macConflict), "the Mac's edit was here before the phone's was made")

            try await run.exchangeUntil("the Mac's edits to meet the phone's") { report in
                guard report.settled else { return false }
                let merged = try await run.engine.entry(on: mergeDate)
                let held = try await run.engine.conflicts()
                return merged.raw.contains(macMerge) && held.conflicts.contains { $0.date == conflictDate }
            }

            let merged = try await run.engine.entry(on: mergeDate)
            XCTAssertTrue(merged.raw.contains(macMerge) && merged.raw.contains("- added on the phone"), "the merge lost a side")
            let kept = try await run.engine.entry(on: conflictDate)
            XCTAssertTrue(kept.raw.contains(Self.conflictNote), "the phone's version was replaced")
            let held = try await run.engine.conflicts()
            let conflict = try XCTUnwrap(held.conflicts.first { $0.date == conflictDate })
            XCTAssertTrue(conflict.ours.content.contains(Self.conflictNote))
            XCTAssertTrue(conflict.theirs.contains { $0.content.contains(macConflict) }, "the Mac's version is not held")
            return [
                "merge_date": mergeDate,
                "merged_revision": merged.revision,
                "merged_holds_both": true,
                "conflict_date": conflictDate,
                "conflict_id": conflict.id,
                "conflict_ours": conflict.ours.revision,
                "conflict_theirs": conflict.theirs.map(\.revision),
                "conflict_holds_both": true,
            ]
        }
    }

    /// A publication stopped after its first file: contents, and no manifest.
    func testInterrupt() async {
        await phase("interrupt") { run in
            let date = try run.expected("date")
            let prepared = try await run.engine.prepareReflection(on: date)
            let saved = try await run.engine.saveReflection(ReflectionWriting(
                draft: prepared.draft, answers: ["Synthetic: sent in two halves.", "", ""]
            ))
            run.access.plan.stopAfterPublishing = 1
            try await run.exchangeUntilCancelled("the publication to be stopped part-way")
            let sent = run.access.plan.published
            XCTAssertEqual(sent.count, 1)
            XCTAssertTrue(sent.allSatisfy { $0.hasPrefix("objects/") }, "a stopped publication left \(sent)")
            let stands = try await run.service.delivery()
            let delivery = try XCTUnwrap(stands)
            XCTAssertEqual(delivery.published, delivery.recorded - 1, "an unfinished publication was counted as published")
            return ["date": date, "revision": saved.value.entry.revision, "published": sent, "cancelled": true]
        }
    }

    func testResume() async {
        await phase("resume") { run in
            try await run.exchangeUntil("the interrupted publication to be finished") { report in
                guard let delivery = report.delivery else { return false }
                return delivery.published == delivery.recorded
            }
            return ["published": run.access.plan.published]
        }
    }

    /// A download stopped after its first file, and finished by the next
    /// exchange.
    func testReceiveInterrupted() async {
        await phase("receive-interrupted") { run in
            let date = try run.expected("date"), revision = try run.expected("revision")
            let before = try await run.engine.entry(on: date).revision
            run.access.plan.stopAfterReading = 1
            try await run.exchangeUntilCancelled("the download to be stopped part-way")
            let read = run.access.plan.read
            let between = try await run.engine.entry(on: date).revision
            XCTAssertEqual(between, before, "a download that was stopped changed the journal")
            try await run.exchangeUntil("the Mac's edit of \(date)") { _ in
                try await run.engine.entry(on: date).revision == revision
            }
            return ["date": date, "revision": revision, "read_before_cancel": read, "cancelled": true, "unchanged_between": true]
        }
    }

    func testFinal() async {
        await phase("final") { run in
            try await run.exchangeUntil("everything to be applied on the Mac") { report in
                guard report.settled, let delivery = report.delivery else { return false }
                let status = try await run.engine.syncStatus()
                return status.pending.isEmpty && delivery.published == delivery.recorded
                    && delivery.peers.contains { $0.name == "mac" && $0.applied == delivery.recorded }
            }
            return [:]
        }
    }

    // MARK: A run

    /// What a phase can stop, and what it saw of the folder.
    final class Plan: @unchecked Sendable {
        private let lock = NSLock()
        private var stopPublishing: Int?
        private var stopReading: Int?
        private var publishedPaths: [String] = []
        private var readPaths: [String] = []
        private var opens = 0

        var stopAfterPublishing: Int? {
            get { lock.withLock { stopPublishing } }
            set { lock.withLock { stopPublishing = newValue } }
        }

        var stopAfterReading: Int? {
            get { lock.withLock { stopReading } }
            set { lock.withLock { stopReading = newValue } }
        }

        var published: [String] { lock.withLock { publishedPaths } }
        var read: [String] { lock.withLock { readPaths } }
        var opened: Int { lock.withLock { opens } }

        func didOpen() { lock.withLock { opens += 1 } }

        /// Whether the exchange is to stop now that `path` was published.
        func didPublish(_ path: String) -> Bool {
            lock.withLock {
                publishedPaths.append(path)
                guard let limit = stopPublishing, publishedPaths.count >= limit else { return false }
                stopPublishing = nil
                return true
            }
        }

        func didRead(_ path: String) -> Bool {
            lock.withLock {
                readPaths.append(path)
                guard let limit = stopReading, readPaths.count >= limit else { return false }
                stopReading = nil
                return true
            }
        }
    }

    /// The real folder, with a transfer the phase can stop part-way the way
    /// a lost connection or the end of background time does: through the
    /// exchange's own cancellation.
    struct StoppingFolder: ExchangeFolder {
        let base: any ExchangeFolder
        let plan: Plan
        let cancellation: ExchangeCancellation

        func list(_ directory: String) throws -> [CloudItem] { try base.list(directory) }
        func requestDownload(_ path: String) throws { try base.requestDownload(path) }

        func read(_ path: String) throws -> Data {
            let data = try base.read(path)
            if plan.didRead(path) { cancellation.cancel() }
            return data
        }

        func publish(_ data: Data, to path: String) throws -> Bool {
            let wrote = try base.publish(data, to: path)
            if wrote, plan.didPublish(path) { cancellation.cancel() }
            return wrote
        }
    }

    struct StoppingAccess: ExchangeAccess {
        let base: any ExchangeAccess
        let plan: Plan

        var folderName: String? { base.folderName }

        func open(_ cancellation: ExchangeCancellation) throws -> (folder: any ExchangeFolder, close: @Sendable () -> Void) {
            let (folder, close) = try base.open(cancellation)
            plan.didOpen()
            return (StoppingFolder(base: folder, plan: plan, cancellation: cancellation), close)
        }
    }

    struct Run: Sendable {
        let name: String
        let root: URL
        let roundTrip: RoundTrip
        let location: JournalLocation
        let engine: SenecaEngine
        let access: StoppingAccess
        let service: ExchangeService
        let expectations: [String: String]

        func expected(_ key: String) throws -> String {
            try XCTUnwrap(expectations[key], "SENECA_SYNC_EXPECT names no \(key)")
        }

        /// Exchanges until `done` says so. A folder that cannot be reached
        /// for a moment, or an exchange stopped by the system, is tried
        /// again: that is what the app does.
        func exchangeUntil(
            _ what: String, timeout: TimeInterval = 420, _ done: (ExchangeReport) async throws -> Bool
        ) async throws {
            let deadline = Date().addingTimeInterval(timeout)
            var last = "nothing yet"
            while true {
                do {
                    let report = try await service.exchange(ExchangeCancellation())
                    if try await done(report) { return }
                    last = "arriving \(report.arriving), delivery \(String(describing: report.delivery))"
                } catch let fault as ExchangeFault where fault.retryable {
                    last = fault.message
                }
                guard Date() < deadline else {
                    throw EvidenceFailure(message: "timed out waiting for \(what): \(last)")
                }
                try await Task.sleep(for: .seconds(3))
            }
        }

        /// Exchanges until one is stopped by the plan.
        func exchangeUntilCancelled(_ what: String, timeout: TimeInterval = 420) async throws {
            let deadline = Date().addingTimeInterval(timeout)
            while true {
                do {
                    _ = try await service.exchange(ExchangeCancellation())
                } catch ExchangeFault.cancelled {
                    return
                } catch let fault as ExchangeFault where fault.retryable {
                    // Not reachable just now; the plan stays armed.
                }
                guard Date() < deadline else {
                    throw EvidenceFailure(message: "timed out waiting for \(what)")
                }
                try await Task.sleep(for: .seconds(3))
            }
        }
    }

    struct EvidenceFailure: Error, CustomStringConvertible {
        let message: String
        var description: String { message }
    }

    private func open(creating: Bool) async throws -> Run {
        let environment = ProcessInfo.processInfo.environment
        let name = try XCTUnwrap(
            environment["SENECA_SYNC_RUN"].flatMap { $0.isEmpty ? nil : $0 },
            "SENECA_SYNC_RUN is not set; run this through ios/scripts/sync-evidence.sh"
        )
        let support = try FileManager.default.url(
            for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true
        )
        let root = support.appendingPathComponent("SenecaEvidence/\(name)", isDirectory: true)
        if creating {
            XCTAssertFalse(FileManager.default.fileExists(atPath: root.path), "run \(name) was already started")
        } else {
            XCTAssertTrue(FileManager.default.fileExists(atPath: root.path), "run \(name) was never started on this device")
        }
        let roundTrip = try RoundTrip.load()
        let location = roundTrip.location(in: root)
        try FileManager.default.createDirectory(at: location.reflections.deletingLastPathComponent(), withIntermediateDirectories: true)

        let base: any ExchangeAccess
        #if targetEnvironment(simulator)
        let rehearsal = try XCTUnwrap(environment["SENECA_SYNC_FOLDER"], "SENECA_SYNC_FOLDER is not set")
        base = DirectoryAccess(directory: URL(fileURLWithPath: rehearsal, isDirectory: true))
        #else
        // The author's own grant, and a folder of the run's own inside it.
        let installed = try AppPaths.journal()
        base = GrantStore(file: AppPaths.grantStore(for: installed).file, subfolder: "seneca-evidence-\(name)")
        XCTAssertNotNil(
            base.folderName,
            "no exchange folder is chosen on this iPhone: open Seneca, Settings, Sync, and choose one in iCloud Drive"
        )
        #endif
        let access = StoppingAccess(base: base, plan: Plan())

        let engine = SenecaEngine()
        try await engine.open(location)
        var expectations: [String: String] = [:]
        if let raw = environment["SENECA_SYNC_EXPECT"], !raw.isEmpty {
            expectations = try JSONDecoder().decode([String: String].self, from: Data(raw.utf8))
        }
        return Run(
            name: name, root: root, roundTrip: roundTrip, location: location, engine: engine, access: access,
            service: ExchangeService(engine: engine, access: access, directory: root.appendingPathComponent("exchange", isDirectory: true)),
            expectations: expectations
        )
    }

    /// Runs one phase and leaves what it found in the app's container.
    private func phase(_ name: String, creating: Bool = false, _ body: (Run) async throws -> [String: Any]) async {
        await reporting {
            let run = try await open(creating: creating)
            var evidence = try await body(run)
            for (key, value) in try await common(name, run) {
                evidence[key] = value
            }
            let data = try JSONSerialization.data(withJSONObject: evidence, options: [.prettyPrinted, .sortedKeys])
            try data.write(to: run.root.appendingPathComponent("evidence-\(name).json"), options: .atomic)
        }
    }

    /// What every phase reports: which build ran on what, through which
    /// folder, and where the journal and its sync history stand.
    private func common(_ phase: String, _ run: Run) async throws -> [String: Any] {
        let (model, system) = await MainActor.run { (UIDevice.current.model, UIDevice.current.systemVersion) }
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

        var folder: [String: Any] = ["name": run.access.folderName ?? "", "opened": run.access.plan.opened]
        if let (opened, close) = try? run.access.base.open(ExchangeCancellation()) {
            if let coordinated = opened as? CoordinatedFolder {
                folder["ubiquitous"] = coordinated.isUbiquitous
                folder["run_folder"] = coordinated.root.lastPathComponent
                // Where in iCloud Drive the folder is, which is the same on
                // every device of the account.
                let path = coordinated.root.standardizedFileURL.path
                if let drive = path.range(of: "/com~apple~CloudDocs/") {
                    folder["cloud_path"] = String(path[drive.upperBound...])
                }
            }
            close()
        }
        folder["granted"] = run.access.base is GrantStore

        var evidence: [String: Any] = [
            "run": run.name,
            "phase": phase,
            "bundle_identifier": Bundle.main.bundleIdentifier ?? "",
            "bundle_build": info["CFBundleVersion"] as? String ?? "",
            "executables": try loadedExecutables(with: SyncEvidenceTests.self),
            "device_model": "\(model) (\(identifier))",
            "system_version": system,
            "simulator": simulator,
            "bridge_version": run.engine.bridgeVersion,
            "folder": folder,
        ]
        let entries = try await run.engine.entries()
        evidence["entries"] = Dictionary(uniqueKeysWithValues: entries.entries.map { ($0.date, $0.revision) })
        let status = try await run.engine.syncStatus()
        if status.enrolled {
            evidence["sync"] = [
                "journal": status.journal, "device": status.device, "sequence": status.sequence,
                "heads": status.heads, "pending": status.pending.count, "conflicts": status.conflicts,
            ] as [String: Any]
            if let delivery = try await run.service.delivery() {
                evidence["delivery"] = [
                    "recorded": delivery.recorded, "published": delivery.published, "uploaded": delivery.uploaded,
                    "peers": delivery.peers.map { ["device": $0.device, "name": $0.name, "applied": $0.applied] as [String: Any] },
                ] as [String: Any]
            }
        }
        return evidence
    }
}
