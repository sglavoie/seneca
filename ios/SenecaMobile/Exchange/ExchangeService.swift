import Foundation
import SenecaEngine

/// Where this iPhone's own changes have got to. Three different things, and
/// the app says each only once it is true:
///
/// - `recorded`: saved in this iPhone's journal.
/// - `published`: written into the exchange folder; `uploaded`: taken from
///   there by iCloud.
/// - a peer's `applied`: in that device's own journal, by its own receipt.
struct Delivery: Codable, Equatable, Sendable {
    var recorded = 0
    var published = 0
    var uploaded = 0
    var peers: [SyncPeer] = []
}

/// What one exchange did.
struct ExchangeReport: Equatable, Sendable {
    /// False for a journal that takes no part in sync yet: the folder was
    /// looked at and nothing else was done.
    var enrolled = false
    var pulled = ExchangeMirror.Pulled()
    var imported: SyncImported?
    var pushed = ExchangeMirror.Pushed()
    var delivery: Delivery?
    var conflicts = 0

    /// Whether the import replaced a journal file the screens are showing.
    var journalChanged: Bool {
        !(imported?.written.isEmpty ?? true)
    }

    /// How many files or changes are still on their way to this iPhone.
    var arriving: Int {
        let incomplete = imported?.diagnostics.filter { $0.code == "incomplete" }.count ?? 0
        return pulled.waiting + pulled.damaged + incomplete
    }

    /// Whether a sooner look could find more than this one did.
    var settled: Bool {
        arriving == 0
    }
}

/// An exchange that stopped after its import had replaced a journal file the
/// screens show. `error` is why it stopped.
struct ExchangeInterrupted: Error {
    var error: any Error
}

/// One exchange with the folder, start to finish, away from the main thread:
///
///  1. open the folder through the author's grant;
///  2. pull what is there whole into the mirror, asking for the rest;
///  3. import from the mirror, as one engine commit;
///  4. publish into the mirror, with a receipt of what is now applied;
///  5. push what the folder lacks, contents before manifests;
///  6. read where this iPhone's changes stand.
///
/// Each step stands by itself. Stopped anywhere, the journal is whole, the
/// folder holds nothing another device can mistake for a whole change, and
/// the next exchange does what this one did not.
actor ExchangeService {
    private let queue: DispatchSerialQueue
    private let engine: SenecaEngine
    private let access: any ExchangeAccess
    private let mirror: ExchangeMirror
    private let stateFile: URL
    private let identity: IdentityGuard?

    nonisolated var unownedExecutor: UnownedSerialExecutor {
        queue.asUnownedSerialExecutor()
    }

    /// `directory` is this iPhone's own: the mirror and what was last seen of
    /// the folder are kept under it.
    init(engine: SenecaEngine, access: any ExchangeAccess, directory: URL, identity: IdentityGuard? = nil) {
        queue = DispatchSerialQueue(label: "io.sglavoie.seneca.exchange", qos: .utility)
        self.engine = engine
        self.access = access
        self.identity = identity
        mirror = ExchangeMirror(directory: directory.appendingPathComponent("mirror", isDirectory: true))
        stateFile = directory.appendingPathComponent("delivery.json")
    }

    nonisolated var folderName: String? {
        access.folderName
    }

    /// Where the engine exchanges: the mirror, never the folder.
    nonisolated var mirrorDirectory: URL {
        mirror.directory
    }

    // MARK: What was last seen of the folder

    private struct Seen: Codable {
        var publishedThrough = 0
        var uploadedThrough = 0
    }

    private func seen() -> Seen {
        guard let data = try? Data(contentsOf: stateFile), let seen = try? JSONDecoder().decode(Seen.self, from: data) else {
            return Seen()
        }
        return seen
    }

    private func remember(_ pushed: ExchangeMirror.Pushed) {
        let seen = Seen(publishedThrough: pushed.publishedThrough, uploadedThrough: pushed.uploadedThrough)
        try? FileManager.default.createDirectory(at: stateFile.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? JSONEncoder().encode(seen).write(to: stateFile, options: .atomic)
    }

    /// Forgets everything mirrored, for a folder that was just chosen.
    func reset() {
        mirror.clear()
        try? FileManager.default.removeItem(at: stateFile)
    }

    // MARK: Exchanging

    /// Where this iPhone's changes stand, without touching the folder: what
    /// is recorded now, against what was last seen published and confirmed.
    /// Nil for a journal that takes no part in sync.
    func delivery() async throws -> Delivery? {
        guard try await engine.syncStatus().enrolled else { return nil }
        let stands = try await engine.syncDelivery(exchange: mirror.directory)
        let seen = seen()
        return Delivery(
            recorded: stands.recorded,
            published: min(seen.publishedThrough, stands.recorded),
            uploaded: min(seen.uploadedThrough, stands.recorded),
            peers: stands.peers
        )
    }

    func exchange(_ cancellation: ExchangeCancellation) async throws -> ExchangeReport {
        var report = ExchangeReport()
        let (folder, close) = try access.open(cancellation)
        defer { close() }

        report.pulled = try mirror.pull(from: folder, cancellation: cancellation)
        let status = try await engine.syncStatus()
        guard status.enrolled else {
            return report
        }
        report.enrolled = true
        // A copy of another device's journal never exchanges as that device.
        if let identity, !identity.accepts(journal: status.journal, device: status.device) {
            throw ExchangeFault.identityInUse
        }

        try cancellation.check()
        report.imported = try await engine.importSync(exchange: mirror.directory).value
        do {
            // Publishing into the mirror is local and is not interrupted: what
            // the engine recorded is ready to send whenever the folder takes it.
            _ = try await engine.exportSync(exchange: mirror.directory)

            report.pushed = try mirror.push(to: folder, device: status.device, cancellation: cancellation)
            remember(report.pushed)
            report.delivery = try await delivery()
            report.conflicts = try await engine.syncStatus().conflicts.count
        } catch {
            // The import is committed: the next exchange finds nothing more
            // to import, so the screens hear of it now or never.
            guard !report.journalChanged else {
                throw ExchangeInterrupted(error: error)
            }
            throw error
        }
        return report
    }

    /// Makes this iPhone the first device of a synced journal. The journal's
    /// history is recorded here; the next exchange publishes it.
    func enroll(name: String) async throws {
        let status = try await engine.enrollSync(name: name).value
        identity?.adopt(journal: status.journal, device: status.device)
    }

    /// Makes this iPhone another device of the journal the folder carries.
    /// The folder's descriptor must have arrived in the mirror.
    func join(name: String) async throws {
        let status = try await engine.joinSync(exchange: mirror.directory, name: name).value
        identity?.adopt(journal: status.journal, device: status.device)
    }

    /// Takes the journal out of sync. Every file stays; this iPhone's
    /// identity is retired and forgotten, and what was mirrored is kept for a
    /// rejoin unless `forgetFolder` says the folder is done with too.
    func leave(forgetFolder: Bool) async throws -> SyncLeft {
        let left = try await engine.leaveSync().value
        if !left.journal.isEmpty {
            identity?.release(journal: left.journal)
        }
        try? FileManager.default.removeItem(at: stateFile)
        if forgetFolder {
            mirror.clear()
        }
        return left
    }

    /// Whether the journal's sync history is this iPhone's own: false for a
    /// copy restored from another device. True outside sync.
    func ownsIdentity() async -> Bool {
        guard let identity, let status = try? await engine.syncStatus(), status.enrolled else { return true }
        return identity.accepts(journal: status.journal, device: status.device)
    }
}
