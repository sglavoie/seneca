import Foundation
import SenecaEngine
import XCTest
@testable import SenecaMobile

/// A directory standing in for the folder iCloud Drive carries, with the
/// faults a file service has: files listed and not delivered, files cut
/// short, a folder that does not answer, a grant taken back, and a transfer
/// stopped part-way. Everything else is the real file system.
final class FaultyFolder: ExchangeFolder, @unchecked Sendable {
    let directory: URL
    private let lock = NSLock()
    private var state = State()

    private struct State {
        var away: Set<String> = []
        var cutShort: Set<String> = []
        var fault: ExchangeFault?
        var publishFault: ExchangeFault?
        var cancelAfter: Int?
        var cancellation: ExchangeCancellation?
        var asked: [String] = []
        var published: [String] = []
        var read: [String] = []
        var open = 0
        var mostOpen = 0
    }

    init(directory: URL) {
        self.directory = directory
    }

    private func with<T>(_ body: (inout State) -> T) -> T {
        lock.withLock { body(&state) }
    }

    /// Paths the folder lists whose bytes are not on this device.
    var away: Set<String> {
        get { with { $0.away } }
        set { with { $0.away = newValue } }
    }

    /// Paths whose bytes arrive cut in half.
    var cutShort: Set<String> {
        get { with { $0.cutShort } }
        set { with { $0.cutShort = newValue } }
    }

    /// What every access fails with, while set.
    var fault: ExchangeFault? {
        get { with { $0.fault } }
        set { with { $0.fault = newValue } }
    }

    /// What publishing fails with, while set: a folder that still lists and
    /// delivers files but takes none.
    var publishFault: ExchangeFault? {
        get { with { $0.publishFault } }
        set { with { $0.publishFault = newValue } }
    }

    /// Stops the exchange once this many files were published.
    var cancelAfter: Int? {
        get { with { $0.cancelAfter } }
        set { with { $0.cancelAfter = newValue } }
    }

    var asked: [String] { with { $0.asked } }
    var published: [String] { with { $0.published } }
    var read: [String] { with { $0.read } }
    /// The most exchanges that ever had the folder open at once.
    var mostOpen: Int { with { $0.mostOpen } }

    func forget() {
        with {
            $0.asked = []
            $0.published = []
            $0.read = []
        }
    }

    fileprivate func opened(_ cancellation: ExchangeCancellation) {
        with {
            $0.cancellation = cancellation
            $0.open += 1
            $0.mostOpen = max($0.mostOpen, $0.open)
        }
    }

    fileprivate func closed() {
        with { $0.open -= 1 }
    }

    private func check() throws {
        if let fault = with({ $0.fault }) { throw fault }
        try with { $0.cancellation }?.check()
    }

    private func url(_ path: String) -> URL {
        path.isEmpty ? directory : directory.appendingPathComponent(path)
    }

    func list(_ path: String) throws -> [CloudItem] {
        try check()
        let names = (try? FileManager.default.contentsOfDirectory(atPath: url(path).path)) ?? []
        let away = self.away
        return names.filter { !$0.hasPrefix(".") }.sorted().map { name in
            var isDirectory: ObjCBool = false
            FileManager.default.fileExists(atPath: url(path).appendingPathComponent(name).path, isDirectory: &isDirectory)
            let full = path.isEmpty ? name : "\(path)/\(name)"
            return CloudItem(name: name, isDirectory: isDirectory.boolValue, isDownloaded: !away.contains(full), isUploaded: true)
        }
    }

    func requestDownload(_ path: String) throws {
        try check()
        with { $0.asked.append(path) }
    }

    func read(_ path: String) throws -> Data {
        try check()
        with { $0.read.append(path) }
        let data = try Data(contentsOf: url(path))
        return cutShort.contains(path) ? data.prefix(data.count / 2) : data
    }

    func publish(_ data: Data, to path: String) throws -> Bool {
        try check()
        if let fault = with({ $0.publishFault }) { throw fault }
        let target = url(path)
        if FileManager.default.fileExists(atPath: target.path) {
            return false
        }
        try FileManager.default.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
        try data.write(to: target, options: .atomic)
        let stop = with { state -> ExchangeCancellation? in
            state.published.append(path)
            if let limit = state.cancelAfter, state.published.count >= limit {
                state.cancelAfter = nil
                return state.cancellation
            }
            return nil
        }
        stop?.cancel()
        return true
    }

    /// Every file under the folder, by its path from it.
    func files() -> [String] {
        let base = directory.standardizedFileURL.path
        var found: [String] = []
        let walker = FileManager.default.enumerator(at: directory, includingPropertiesForKeys: [.isRegularFileKey])
        while let file = walker?.nextObject() as? URL {
            if (try? file.resourceValues(forKeys: [.isRegularFileKey]).isRegularFile) == true {
                found.append(String(file.standardizedFileURL.path.dropFirst(base.count + 1)))
            }
        }
        return found.sorted()
    }
}

struct FaultyAccess: ExchangeAccess {
    let folder: FaultyFolder

    var folderName: String? { folder.directory.lastPathComponent }

    func open(_ cancellation: ExchangeCancellation) throws -> (folder: any ExchangeFolder, close: @Sendable () -> Void) {
        if let fault = folder.fault, !fault.retryable {
            throw fault
        }
        folder.opened(cancellation)
        return (folder, { folder.closed() })
    }
}

/// The Mac: the same engine over its own journal, exchanging straight through
/// its copy of the folder the way `seneca sync` does.
struct Desk {
    let engine: SenecaEngine
    let location: JournalLocation
    let folder: URL

    /// What `seneca sync once` does, short of Git: import, publish, and say
    /// what is now applied.
    @discardableResult
    func exchange() async throws -> SyncImported {
        let imported = try await engine.importSync(exchange: folder).value
        _ = try await engine.exportSync(exchange: folder)
        return imported
    }

    func write(_ answers: [String], on date: String? = nil) async throws {
        let prepared = try await engine.prepareReflection(on: date)
        _ = try await engine.saveReflection(ReflectionWriting(draft: prepared.draft, answers: answers))
    }

    func text() throws -> String {
        try String(contentsOf: location.reflections, encoding: .utf8)
    }
}

/// A Mac that enrolled the synthetic journal and published it to a new folder
/// under `root`, and the folder with a phone's faults over it.
func enrolledDesk(_ roundTrip: RoundTrip, in root: URL) async throws -> (Desk, FaultyFolder) {
    let folder = root.appendingPathComponent("Seneca Exchange", isDirectory: true)
    try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
    let location = try roundTrip.seed(in: root.appendingPathComponent("mac", isDirectory: true))
    let engine = SenecaEngine()
    try await engine.open(location)
    _ = try await engine.enrollSync(name: "mac")
    _ = try await engine.exportSync(exchange: folder)
    return (Desk(engine: engine, location: location, folder: folder), FaultyFolder(directory: folder))
}

extension XCTestCase {
    /// Waits until `condition` holds, failing after a while.
    @MainActor
    func eventually(
        _ what: String, timeout: TimeInterval = 20, file: StaticString = #filePath, line: UInt = #line,
        _ condition: @MainActor () -> Bool
    ) async {
        let deadline = Date().addingTimeInterval(timeout)
        while !condition() {
            if Date() > deadline {
                XCTFail("timed out waiting for \(what)", file: file, line: line)
                return
            }
            try? await Task.sleep(for: .milliseconds(20))
        }
    }
}
