import Foundation

/// One file or directory of the exchange folder, as the file service lists it.
struct CloudItem: Equatable, Sendable {
    var name: String
    var isDirectory = false
    /// False for a file the folder lists whose bytes are not on this iPhone.
    var isDownloaded = true
    /// Whether iCloud has taken a file this iPhone wrote. Nil where the
    /// folder is not in iCloud and there is nothing to wait for.
    var isUploaded: Bool?
}

/// What stops an exchange with the folder, apart from the engine refusing.
enum ExchangeFault: Error, Equatable {
    /// No folder was chosen on this iPhone.
    case notChosen
    /// The grant to the chosen folder no longer works: the folder is gone, or
    /// access to it was taken back. Only choosing again helps.
    case revoked(String)
    /// The folder cannot be reached right now. Trying again later can help.
    case unavailable(String)
    /// The exchange was stopped before it finished. Nothing is half-done:
    /// what was not exchanged is exchanged by the next one.
    case cancelled
    /// The folder carries a journal other than the one this iPhone exchanged
    /// with through it before.
    case differentJournal
    /// The journal here is a copy of another device's, restored from a
    /// backup or carried from an old iPhone: it carries that device's
    /// identity. It is not exchanged until this iPhone rejoins as itself.
    case identityInUse

    var message: String {
        switch self {
        case .notChosen:
            return "No exchange folder is chosen."
        case .revoked(let detail):
            return "Seneca can no longer open the exchange folder. Choose it again. (\(detail))"
        case .unavailable(let detail):
            return "The exchange folder cannot be reached right now. (\(detail))"
        case .cancelled:
            return "The exchange was interrupted and will be finished by the next one."
        case .differentJournal:
            return "The exchange folder now carries a different journal. Choose the folder again to exchange with it."
        case .identityInUse:
            return "This journal was restored from another iPhone, and still speaks as that device, so it is not exchanged. Rejoin as this iPhone in Diagnostics and Recovery: everything written here is kept."
        }
    }

    /// Whether the same exchange, tried again later, can succeed.
    var retryable: Bool {
        switch self {
        case .unavailable, .cancelled: return true
        case .notChosen, .revoked, .differentJournal, .identityInUse: return false
        }
    }
}

/// The exchange folder as the app reaches it: outside its container, owned by
/// a file service, and not always all there. Paths are relative to the
/// folder and slash-separated; the empty path is the folder itself.
///
/// Nothing of the journal is ever opened through this. The engine works on a
/// copy inside the app's container, and only whole, immutable files cross.
protocol ExchangeFolder: Sendable {
    /// What a directory holds. A directory that does not exist holds nothing;
    /// the folder itself not answering is a fault.
    func list(_ directory: String) throws -> [CloudItem]
    /// Asks the file service to bring a listed file's bytes to this iPhone.
    /// It returns at once; the file is there, or not, at a later look.
    func requestDownload(_ path: String) throws
    /// Reads a downloaded file whole.
    func read(_ path: String) throws -> Data
    /// Writes a new file whole, creating its directory. A file already there
    /// is left as it is: nothing in an exchange is replaced. Returns whether
    /// it wrote.
    func publish(_ data: Data, to path: String) throws -> Bool
}

/// Stops an exchange from outside it: when the author leaves, when background
/// time runs out, or when a test says so. It is checked between files, and it
/// interrupts a file coordination that is waiting on the file service.
final class ExchangeCancellation: @unchecked Sendable {
    private let lock = NSLock()
    private var cancelled = false
    private var interrupt: (@Sendable () -> Void)?

    var isCancelled: Bool {
        lock.withLock { cancelled }
    }

    func cancel() {
        let pending = lock.withLock { () -> (@Sendable () -> Void)? in
            cancelled = true
            return interrupt
        }
        pending?()
    }

    func check() throws {
        if isCancelled { throw ExchangeFault.cancelled }
    }

    /// Names what to interrupt while one blocking call is under way. The
    /// returned closure ends that.
    func interrupting(_ action: @escaping @Sendable () -> Void) -> @Sendable () -> Void {
        let already = lock.withLock { () -> Bool in
            interrupt = action
            return cancelled
        }
        if already { action() }
        return { [self] in lock.withLock { interrupt = nil } }
    }
}

/// The exchange folder through file coordination, which is how an app is
/// meant to touch files another process — here the iCloud Drive file
/// provider — is also reading and writing.
struct CoordinatedFolder: ExchangeFolder {
    let root: URL
    let cancellation: ExchangeCancellation
    /// How long one coordinated access may wait on the file service.
    var patience: TimeInterval = 30

    private static let placeholderSuffix = ".icloud"

    private func url(_ path: String) -> URL {
        path.isEmpty ? root : root.appendingPathComponent(path)
    }

    /// Whether the folder is in iCloud, by the file service's own account.
    var isUbiquitous: Bool {
        (try? root.resourceValues(forKeys: [.isUbiquitousItemKey]).isUbiquitousItem) ?? false
    }

    private final class Coordinator: @unchecked Sendable {
        let value = NSFileCoordinator(filePresenter: nil)
    }

    private func coordinate<T>(_ url: URL, writing: Bool, _ body: (URL) throws -> T) throws -> T {
        try cancellation.check()
        let coordinator = Coordinator()
        let release = cancellation.interrupting { coordinator.value.cancel() }
        let timedOut = DispatchWorkItem { coordinator.value.cancel() }
        DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + patience, execute: timedOut)
        defer {
            timedOut.cancel()
            release()
        }

        var failure: NSError?
        var outcome: Result<T, Error>?
        if writing {
            coordinator.value.coordinate(writingItemAt: url, options: .forReplacing, error: &failure) { granted in
                outcome = Result { try body(granted) }
            }
        } else {
            coordinator.value.coordinate(readingItemAt: url, options: .withoutChanges, error: &failure) { granted in
                outcome = Result { try body(granted) }
            }
        }
        if let failure {
            throw fault(failure)
        }
        guard let outcome else {
            throw cancellation.isCancelled ? ExchangeFault.cancelled : ExchangeFault.unavailable("the folder did not answer")
        }
        do {
            return try outcome.get()
        } catch let error as ExchangeFault {
            throw error
        } catch {
            throw fault(error as NSError)
        }
    }

    private func fault(_ error: NSError) -> ExchangeFault {
        if cancellation.isCancelled {
            return .cancelled
        }
        guard error.domain == NSCocoaErrorDomain else {
            if error.domain == NSPOSIXErrorDomain, error.code == Int(EACCES) || error.code == Int(EPERM) {
                return .revoked(error.localizedDescription)
            }
            return .unavailable(error.localizedDescription)
        }
        switch CocoaError.Code(rawValue: error.code) {
        case .fileReadNoPermission, .fileWriteNoPermission:
            return .revoked(error.localizedDescription)
        case .userCancelled:
            return .unavailable("the folder did not answer in time")
        default:
            return .unavailable(error.localizedDescription)
        }
    }

    private static func isMissing(_ error: Error) -> Bool {
        let error = error as NSError
        if error.domain == NSCocoaErrorDomain {
            return error.code == CocoaError.fileReadNoSuchFile.rawValue || error.code == CocoaError.fileNoSuchFile.rawValue
        }
        return error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT)
    }

    func list(_ directory: String) throws -> [CloudItem] {
        let keys: [URLResourceKey] = [
            .isDirectoryKey, .ubiquitousItemDownloadingStatusKey, .ubiquitousItemIsUploadedKey, .isUbiquitousItemKey,
        ]
        return try coordinate(url(directory), writing: false) { granted in
            let found: [URL]
            do {
                found = try FileManager.default.contentsOfDirectory(at: granted, includingPropertiesForKeys: keys, options: [])
            } catch where Self.isMissing(error) {
                if directory.isEmpty {
                    throw ExchangeFault.unavailable("the folder is not there")
                }
                return []
            }
            var items: [String: CloudItem] = [:]
            for file in found {
                let name = file.lastPathComponent
                if name.hasPrefix(".") {
                    // A placeholder stands for a file that is in iCloud and
                    // not here; any other hidden file is not the exchange's.
                    guard name.hasSuffix(Self.placeholderSuffix), name.count > Self.placeholderSuffix.count + 1 else { continue }
                    let real = String(name.dropFirst().dropLast(Self.placeholderSuffix.count))
                    if items[real] == nil {
                        items[real] = CloudItem(name: real, isDownloaded: false, isUploaded: true)
                    }
                    continue
                }
                let values = try? file.resourceValues(forKeys: Set(keys))
                var item = CloudItem(name: name, isDirectory: values?.isDirectory ?? false)
                if values?.isUbiquitousItem == true {
                    item.isDownloaded = values?.ubiquitousItemDownloadingStatus != .notDownloaded
                    item.isUploaded = values?.ubiquitousItemIsUploaded ?? false
                }
                items[name] = item
            }
            return items.values.sorted { $0.name < $1.name }
        }
    }

    func requestDownload(_ path: String) throws {
        try cancellation.check()
        // A request that fails is made again by the next exchange.
        try? FileManager.default.startDownloadingUbiquitousItem(at: url(path))
    }

    func read(_ path: String) throws -> Data {
        try coordinate(url(path), writing: false) { granted in
            try Data(contentsOf: granted)
        }
    }

    func publish(_ data: Data, to path: String) throws -> Bool {
        let target = url(path)
        do {
            try FileManager.default.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
        } catch {
            throw fault(error as NSError)
        }
        return try coordinate(target, writing: true) { granted in
            let placeholder = granted.deletingLastPathComponent()
                .appendingPathComponent("." + granted.lastPathComponent + Self.placeholderSuffix)
            if FileManager.default.fileExists(atPath: granted.path) || FileManager.default.fileExists(atPath: placeholder.path) {
                return false
            }
            // Whole or not at all: written beside the folder and moved in.
            try data.write(to: granted, options: .atomic)
            return true
        }
    }
}
