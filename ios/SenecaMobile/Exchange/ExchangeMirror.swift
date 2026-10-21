import CryptoKit
import Foundation

/// The copy of the exchange inside the app's container, and the only one the
/// engine ever reads or writes. The mirror is what keeps every live operation
/// on local storage: an import reads whole local files, and a publication is
/// finished locally before any of it is offered to the folder.
///
///     <mirror>/journal.json
///     <mirror>/objects/<sha-256>
///     <mirror>/transactions/<device>/<sequence>-<id>.json
///     <mirror>/receipts/<device>/<sha-256>.json
///
/// Everything in an exchange but its descriptor is named by the digest of its
/// own bytes. `pull` uses that: a file enters the mirror only when it is here
/// whole and is what its name says, so a download cut short, or a file the
/// folder lists and has not delivered, is simply not there yet. `push` offers
/// the folder what it lacks, contents before the manifests that name them.
struct ExchangeMirror: Sendable {
    let directory: URL

    static let descriptor = "journal.json"
    static let objects = "objects"
    static let transactions = "transactions"
    static let receipts = "receipts"

    /// What one pull did.
    struct Pulled: Equatable, Sendable {
        /// Files that entered the mirror.
        var copied = 0
        /// Files the folder lists whose bytes are not on this iPhone yet.
        /// Each was asked for.
        var waiting = 0
        /// Files that are here and are not what their name says: cut short on
        /// the way, most likely. They are looked at again next time.
        var damaged = 0
        /// Whether the folder holds a journal's descriptor, here or not yet.
        var journalInFolder = false
    }

    /// What one push did, and where this device's own changes now stand.
    struct Pushed: Equatable, Sendable {
        var descriptor = false
        var objects = 0
        var manifests = 0
        var receipts = 0
        /// The sequence up to which this device's manifests are all in the
        /// folder, and up to which iCloud has taken them.
        var publishedThrough = 0
        var uploadedThrough = 0

        var files: Int { (descriptor ? 1 : 0) + objects + manifests + receipts }
    }

    // MARK: Names

    private static func isHex(_ text: Substring, count: Int) -> Bool {
        text.utf8.count == count && text.utf8.allSatisfy { ($0 >= 48 && $0 <= 57) || ($0 >= 97 && $0 <= 102) }
    }

    static func isDevice(_ name: String) -> Bool {
        isHex(Substring(name), count: 32)
    }

    static func isObject(_ name: String) -> Bool {
        isHex(Substring(name), count: 64)
    }

    /// The sequence and identity a manifest's file name states.
    static func manifest(_ name: String) -> (sequence: Int, id: String)? {
        guard name.hasSuffix(".json") else { return nil }
        let stem = name.dropLast(5)
        guard let dash = stem.firstIndex(of: "-") else { return nil }
        let digits = stem[..<dash]
        let id = stem[stem.index(after: dash)...]
        guard digits.count >= 8, let sequence = Int(digits), sequence >= 1, isHex(id, count: 64) else { return nil }
        return (sequence, String(id))
    }

    /// The identity a receipt's file name states.
    static func receipt(_ name: String) -> String? {
        guard name.hasSuffix(".json") else { return nil }
        let stem = name.dropLast(5)
        return isHex(stem, count: 64) ? String(stem) : nil
    }

    static func digest(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    // MARK: The mirror's own files

    private func local(_ path: String) -> URL {
        directory.appendingPathComponent(path)
    }

    private func names(in path: String) -> [String] {
        ((try? FileManager.default.contentsOfDirectory(atPath: local(path).path)) ?? []).filter { !$0.hasPrefix(".") }.sorted()
    }

    func holds(_ path: String) -> Bool {
        FileManager.default.fileExists(atPath: local(path).path)
    }

    /// Whether the mirror holds a journal's descriptor.
    var holdsJournal: Bool {
        holds(Self.descriptor)
    }

    private func keep(_ data: Data, at path: String) throws {
        let target = local(path)
        try FileManager.default.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
        try data.write(to: target, options: .atomic)
    }

    /// Empties the mirror, for a folder that is no longer the one mirrored.
    func clear() {
        try? FileManager.default.removeItem(at: directory)
    }

    // MARK: Pull

    /// Brings into the mirror every file of the folder that is here whole,
    /// and asks for the ones that are not.
    func pull(from folder: any ExchangeFolder, cancellation: ExchangeCancellation) throws -> Pulled {
        var pulled = Pulled()

        /// Takes one listed file, when `accept` says its bytes are what the
        /// name promises.
        func take(_ item: CloudItem, at path: String, accept: (Data) -> Bool) throws {
            guard !holds(path) else { return }
            try cancellation.check()
            guard item.isDownloaded else {
                try folder.requestDownload(path)
                pulled.waiting += 1
                return
            }
            let data = try folder.read(path)
            guard accept(data) else {
                pulled.damaged += 1
                return
            }
            try keep(data, at: path)
            pulled.copied += 1
        }

        let root = try folder.list("")
        if let item = root.first(where: { $0.name == Self.descriptor && !$0.isDirectory }) {
            pulled.journalInFolder = true
            if holdsJournal {
                // The folder is mirrored as one journal's. Another journal's
                // descriptor in it is not merged into that.
                if item.isDownloaded, let held = try? Data(contentsOf: local(Self.descriptor)),
                   try folder.read(Self.descriptor) != held {
                    throw ExchangeFault.differentJournal
                }
            } else {
                try take(item, at: Self.descriptor) { data in
                    (try? JSONSerialization.jsonObject(with: data)) is [String: Any]
                }
            }
        }
        let directories = Set(root.filter(\.isDirectory).map(\.name))

        // Contents before the manifests that name them, so that a manifest in
        // the mirror usually finds its contents there. The engine waits for
        // them either way.
        if directories.contains(Self.objects) {
            for item in try folder.list(Self.objects) where !item.isDirectory && Self.isObject(item.name) {
                try take(item, at: "\(Self.objects)/\(item.name)") { Self.digest($0) == item.name }
            }
        }
        if directories.contains(Self.transactions) {
            for device in try folder.list(Self.transactions) where device.isDirectory && Self.isDevice(device.name) {
                let path = "\(Self.transactions)/\(device.name)"
                for item in try folder.list(path) where !item.isDirectory {
                    guard let named = Self.manifest(item.name) else { continue }
                    try take(item, at: "\(path)/\(item.name)") { Self.digest($0) == named.id }
                }
            }
        }
        if directories.contains(Self.receipts) {
            for device in try folder.list(Self.receipts) where device.isDirectory && Self.isDevice(device.name) {
                let path = "\(Self.receipts)/\(device.name)"
                for item in try folder.list(path) where !item.isDirectory {
                    guard let id = Self.receipt(item.name) else { continue }
                    try take(item, at: "\(path)/\(item.name)") { Self.digest($0) == id }
                }
            }
        }
        return pulled
    }

    // MARK: Push

    /// Offers the folder what the mirror holds and it lacks: the descriptor,
    /// then contents, then this device's manifests oldest first, then its
    /// receipts. Stopped anywhere, it leaves nothing another device can
    /// mistake for a whole change, and the next push sends the rest.
    func push(to folder: any ExchangeFolder, device: String, cancellation: ExchangeCancellation) throws -> Pushed {
        var pushed = Pushed()
        let root = try folder.list("")
        let directories = Set(root.filter(\.isDirectory).map(\.name))

        func listed(_ path: String, under parent: String) throws -> [String: CloudItem] {
            guard directories.contains(parent) else { return [:] }
            return Dictionary(try folder.list(path).map { ($0.name, $0) }, uniquingKeysWith: { first, _ in first })
        }

        /// Sends one file the folder does not list. Returns whether it is in
        /// the folder afterwards.
        func send(_ path: String) throws -> Bool {
            try cancellation.check()
            return try folder.publish(try Data(contentsOf: local(path)), to: path)
        }

        if holdsJournal, !root.contains(where: { $0.name == Self.descriptor }) {
            pushed.descriptor = try send(Self.descriptor)
        }

        let objectsThere = try listed(Self.objects, under: Self.objects)
        for name in names(in: Self.objects) where Self.isObject(name) && objectsThere[name] == nil {
            if try send("\(Self.objects)/\(name)") {
                pushed.objects += 1
            }
        }

        let manifestPath = "\(Self.transactions)/\(device)"
        let manifestsThere = try listed(manifestPath, under: Self.transactions)
        var inFolder = Set<Int>()
        var uploaded = Set<Int>()
        for (name, item) in manifestsThere {
            guard let named = Self.manifest(name) else { continue }
            inFolder.insert(named.sequence)
            if item.isUploaded ?? true {
                uploaded.insert(named.sequence)
            }
        }
        // Names sort by sequence, so the oldest goes first.
        for name in names(in: manifestPath) where manifestsThere[name] == nil {
            guard let named = Self.manifest(name) else { continue }
            if try send("\(manifestPath)/\(name)") {
                pushed.manifests += 1
            }
            inFolder.insert(named.sequence)
        }

        let receiptPath = "\(Self.receipts)/\(device)"
        let receiptsThere = try listed(receiptPath, under: Self.receipts)
        for name in names(in: receiptPath) where Self.receipt(name) != nil && receiptsThere[name] == nil {
            if try send("\(receiptPath)/\(name)") {
                pushed.receipts += 1
            }
        }

        pushed.publishedThrough = Self.through(inFolder)
        pushed.uploadedThrough = Self.through(uploaded)
        return pushed
    }

    /// The count up to which `present` holds every sequence from 1.
    static func through(_ present: Set<Int>) -> Int {
        var count = 0
        while present.contains(count + 1) {
            count += 1
        }
        return count
    }
}
