import Foundation

/// How the app gets at the exchange folder for the length of one exchange.
protocol ExchangeAccess: Sendable {
    /// The chosen folder's name, for the author to recognize. Nil when none
    /// is chosen.
    var folderName: String? { get }
    /// Opens the folder. `close` gives the access back and must be called.
    func open(_ cancellation: ExchangeCancellation) throws -> (folder: any ExchangeFolder, close: @Sendable () -> Void)
}

/// The author's grant to a folder outside the app's container, kept across
/// launches as a bookmark. iOS hands the app a security-scoped URL when the
/// author picks the folder; the bookmark is what lets the app open the same
/// folder again later without asking, until the folder is deleted or the
/// grant is taken back.
///
/// The grant is this iPhone's: it lives beside the engine's state, outside
/// the journal, and is never exchanged.
struct GrantStore: ExchangeAccess {
    /// Where the grant is kept.
    let file: URL
    /// A folder inside the granted one to exchange through instead of the
    /// granted folder itself. The app leaves it empty.
    var subfolder = ""

    private struct Grant: Codable {
        var bookmark: Data
        var name: String
        var chosenAt: Date
    }

    private func load() -> Grant? {
        guard let data = try? Data(contentsOf: file) else { return nil }
        return try? JSONDecoder().decode(Grant.self, from: data)
    }

    private func store(_ grant: Grant) throws {
        try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        try JSONEncoder().encode(grant).write(to: file, options: .atomic)
    }

    var folderName: String? {
        load()?.name
    }

    /// Keeps the grant to the folder the author just picked.
    func choose(_ picked: URL) throws {
        let scoped = picked.startAccessingSecurityScopedResource()
        defer {
            if scoped { picked.stopAccessingSecurityScopedResource() }
        }
        let bookmark: Data
        do {
            bookmark = try picked.bookmarkData(options: [], includingResourceValuesForKeys: nil, relativeTo: nil)
        } catch {
            throw ExchangeFault.revoked(error.localizedDescription)
        }
        try store(Grant(bookmark: bookmark, name: picked.lastPathComponent, chosenAt: Date()))
    }

    /// Forgets the folder. Nothing in it is touched.
    func forget() {
        try? FileManager.default.removeItem(at: file)
    }

    func open(_ cancellation: ExchangeCancellation) throws -> (folder: any ExchangeFolder, close: @Sendable () -> Void) {
        guard var grant = load() else {
            throw ExchangeFault.notChosen
        }
        var stale = false
        let url: URL
        do {
            url = try URL(resolvingBookmarkData: grant.bookmark, options: [], relativeTo: nil, bookmarkDataIsStale: &stale)
        } catch {
            throw ExchangeFault.revoked(error.localizedDescription)
        }
        guard url.startAccessingSecurityScopedResource() else {
            throw ExchangeFault.revoked("access to \(grant.name) was not granted")
        }
        if stale, let renewed = try? url.bookmarkData(options: [], includingResourceValuesForKeys: nil, relativeTo: nil) {
            // The folder moved or was renamed; the grant follows it.
            grant.bookmark = renewed
            grant.name = url.lastPathComponent
            try? store(grant)
        }
        let root = subfolder.isEmpty ? url : url.appendingPathComponent(subfolder, isDirectory: true)
        return (CoordinatedFolder(root: root, cancellation: cancellation), { url.stopAccessingSecurityScopedResource() })
    }
}

/// A plain directory the app can already reach, used as the exchange folder:
/// by tests, and by a simulator build a UI test stages. It goes through the
/// same file coordination as a granted folder.
struct DirectoryAccess: ExchangeAccess {
    let directory: URL

    var folderName: String? {
        directory.lastPathComponent
    }

    func open(_ cancellation: ExchangeCancellation) throws -> (folder: any ExchangeFolder, close: @Sendable () -> Void) {
        (CoordinatedFolder(root: directory, cancellation: cancellation), {})
    }
}
