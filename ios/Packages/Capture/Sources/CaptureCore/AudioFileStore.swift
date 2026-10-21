import CryptoKit
import Foundation

public struct StoredAudio: Hashable, Sendable {
    public var url: URL
    public var relativePath: String
    public var byteCount: Int
    public var sha256: String

    public init(url: URL, relativePath: String, byteCount: Int, sha256: String) {
        self.url = url
        self.relativePath = relativePath
        self.byteCount = byteCount
        self.sha256 = sha256
    }
}

public enum AudioStoreError: Error, Hashable, Sendable {
    case emptyRecording(URL)
    case missingFile(URL)
    case insufficientStorage(requiredBytes: Int64, availableBytes: Int64)
    case finalizationFailed(String)
}

/// Owns every path and byte of recorded audio.
///
/// Layout is `Application Support/Entries/<entry-id>/audio.m4a`, with the
/// in-flight recording at `audio.partial.m4a` in the same directory. A recording
/// only ever becomes `audio.m4a` through an atomic rename, so a crash leaves
/// either a complete file or a partial one — never a torn final file that a
/// later launch would mistake for a finished recording.
///
/// Directory names are entry UUIDs, which is what makes same-day recordings and
/// identical slugs collision-free without any naming rules at all.
public struct AudioFileStore: Sendable {
    public static let entriesDirectoryName = "Entries"
    public static let finalFileName = "audio.m4a"
    public static let partialFileName = "audio.partial.m4a"
    /// Refuse to start a recording that cannot plausibly complete. 128 kbps is
    /// 16 kB/s, so this is roughly five minutes of headroom.
    public static let defaultMinimumFreeBytes: Int64 = 5 * 1024 * 1024

    public var applicationSupportRoot: URL
    public var minimumFreeBytes: Int64

    public init(
        applicationSupportRoot: URL,
        minimumFreeBytes: Int64 = AudioFileStore.defaultMinimumFreeBytes
    ) {
        self.applicationSupportRoot = applicationSupportRoot
        self.minimumFreeBytes = minimumFreeBytes
    }

    private var fileManager: FileManager { .default }

    public var entriesRoot: URL {
        applicationSupportRoot.appendingPathComponent(
            Self.entriesDirectoryName,
            isDirectory: true
        )
    }

    public func directory(for entryID: EntryID) -> URL {
        entriesRoot.appendingPathComponent(entryID.description, isDirectory: true)
    }

    public func finalURL(for entryID: EntryID) -> URL {
        directory(for: entryID).appendingPathComponent(Self.finalFileName)
    }

    public func partialURL(for entryID: EntryID) -> URL {
        directory(for: entryID).appendingPathComponent(Self.partialFileName)
    }

    /// The in-flight sibling of a final audio URL. Lets a recorder that was
    /// handed a destination find its own partial without knowing the layout.
    ///
    /// `audio.m4a` gives `audio.partial.m4a`; a host that names files itself
    /// gets `<name>.partial.m4a` beside `<name>.m4a` the same way.
    public static func partialURL(forFinalURL url: URL) -> URL {
        let name = url.deletingPathExtension().lastPathComponent
        return url.deletingLastPathComponent().appendingPathComponent("\(name).partial.\(url.pathExtension)")
    }

    /// Recovers the entry identity from a path. The directory name *is* the
    /// entry id, so a file found by a filesystem scan still knows what it is
    /// even if the database row is gone.
    public static func entryID(forAudioURL url: URL) -> EntryID? {
        EntryID(string: url.deletingLastPathComponent().lastPathComponent)
    }

    /// The path persisted in the database. Storing it relative to Application
    /// Support survives the container path changing between launches, which iOS
    /// does on every reinstall.
    public func relativePath(for entryID: EntryID) -> String {
        "\(Self.entriesDirectoryName)/\(entryID.description)/\(Self.finalFileName)"
    }

    public func resolve(relativePath: String) -> URL {
        applicationSupportRoot.appendingPathComponent(relativePath)
    }

    /// Files created in the directory inherit `captureProtection`, so the
    /// partial is protected from its first byte.
    @discardableResult
    public func prepareDirectory(for entryID: EntryID) throws -> URL {
        let directory = directory(for: entryID)
        try fileManager.createDirectory(at: directory, withIntermediateDirectories: true)
        try applyCaptureProtection(to: directory)
        return directory
    }

    /// Unfinished audio relies on iOS Data Protection through lock:
    /// the recorder keeps writing the file it already holds open, and nothing
    /// can reopen it after it closes until the phone is unlocked again.
    public static let captureProtection = FileProtectionType.completeUnlessOpen

    /// Finished audio goes back to a class that processing can still read
    /// after the user locks the phone.
    public static let finishedProtection = FileProtectionType.completeUntilFirstUserAuthentication

    public func applyCaptureProtection(to url: URL) throws {
        try apply(Self.captureProtection, to: url)
    }

    private func apply(_ protection: FileProtectionType, to url: URL) throws {
        #if os(iOS)
        let attributes: [FileAttributeKey: Any] = [.protectionKey: protection]
        try fileManager.setAttributes(attributes, ofItemAtPath: url.path)
        #endif
    }

    /// The class the system reports for `url`, for diagnostics.
    public func protection(of url: URL) -> FileProtectionType? {
        let attributes = try? fileManager.attributesOfItem(atPath: url.path)
        return attributes?[.protectionKey] as? FileProtectionType
    }

    func availableCapacityBytes() throws -> Int64 {
        try fileManager.createDirectory(at: entriesRoot, withIntermediateDirectories: true)
        let values = try entriesRoot.resourceValues(
            forKeys: [.volumeAvailableCapacityForImportantUsageKey]
        )
        return Int64(values.volumeAvailableCapacityForImportantUsage ?? 0)
    }

    public func requireCapacity(minimumBytes: Int64? = nil) throws {
        let required = minimumBytes ?? self.minimumFreeBytes
        let available = try availableCapacityBytes()
        guard available >= required else {
            throw AudioStoreError.insufficientStorage(
                requiredBytes: required,
                availableBytes: available
            )
        }
    }

    public func byteCount(at url: URL) throws -> Int {
        let values = try url.resourceValues(forKeys: [.fileSizeKey])
        guard let size = values.fileSize else {
            throw AudioStoreError.missingFile(url)
        }
        return size
    }

    public func describe(entryID: EntryID, url: URL) throws -> StoredAudio {
        guard fileManager.fileExists(atPath: url.path) else {
            throw AudioStoreError.missingFile(url)
        }
        let bytes = try byteCount(at: url)
        guard bytes > 0 else {
            throw AudioStoreError.emptyRecording(url)
        }
        return StoredAudio(
            url: url,
            relativePath: relativePath(for: entryID),
            byteCount: bytes,
            sha256: try Self.sha256(of: url)
        )
    }

    /// Promotes `audio.partial.m4a` to `audio.m4a` atomically and describes the
    /// result. Throws rather than promoting a zero-byte file, so a failed
    /// capture can never look like a successful one.
    @discardableResult
    public func finalize(entryID: EntryID) throws -> StoredAudio {
        let partial = partialURL(for: entryID)
        let destination = finalURL(for: entryID)

        if !fileManager.fileExists(atPath: partial.path), !fileManager.fileExists(atPath: destination.path) {
            throw AudioStoreError.missingFile(destination)
        }

        if !fileManager.fileExists(atPath: partial.path) {
            // Already promoted by an earlier launch; finalization is idempotent.
            try apply(Self.finishedProtection, to: destination)
            return try describe(entryID: entryID, url: destination)
        }

        guard try byteCount(at: partial) > 0 else {
            throw AudioStoreError.emptyRecording(partial)
        }

        if fileManager.fileExists(atPath: destination.path) {
            _ = try fileManager.replaceItemAt(destination, withItemAt: partial)
        } else {
            try fileManager.moveItem(at: partial, to: destination)
        }

        try apply(Self.finishedProtection, to: destination)
        return try describe(entryID: entryID, url: destination)
    }

    @discardableResult
    public func finalize(finalURL url: URL) throws -> StoredAudio {
        if let entryID = Self.entryID(forAudioURL: url),
           url.standardizedFileURL == finalURL(for: entryID).standardizedFileURL {
            return try finalize(entryID: entryID)
        }
        // A host with a layout of its own, under this store's root: the same
        // promotion, by path rather than by entry directory.
        let root = applicationSupportRoot.standardizedFileURL.path + "/"
        let path = url.standardizedFileURL.path
        guard path.hasPrefix(root) else {
            throw AudioStoreError.finalizationFailed("\(url.path) is not inside \(applicationSupportRoot.path)")
        }
        let relativePath = String(path.dropFirst(root.count))
        let partial = Self.partialURL(forFinalURL: url)

        if !fileManager.fileExists(atPath: partial.path) {
            guard fileManager.fileExists(atPath: url.path) else { throw AudioStoreError.missingFile(url) }
            try apply(Self.finishedProtection, to: url)
            return try describe(url: url, relativePath: relativePath)
        }
        guard try byteCount(at: partial) > 0 else {
            throw AudioStoreError.emptyRecording(partial)
        }
        if fileManager.fileExists(atPath: url.path) {
            _ = try fileManager.replaceItemAt(url, withItemAt: partial)
        } else {
            try fileManager.moveItem(at: partial, to: url)
        }
        try apply(Self.finishedProtection, to: url)
        return try describe(url: url, relativePath: relativePath)
    }

    private func describe(url: URL, relativePath: String) throws -> StoredAudio {
        let bytes = try byteCount(at: url)
        guard bytes > 0 else { throw AudioStoreError.emptyRecording(url) }
        return StoredAudio(url: url, relativePath: relativePath, byteCount: bytes, sha256: try Self.sha256(of: url))
    }

    /// Entry directories holding audio that no completed capture claimed yet:
    /// either a non-empty partial from an interrupted run, or a final file whose
    /// database row never got written.
    public func interruptedEntryIDs() throws -> [EntryID] {
        guard fileManager.fileExists(atPath: entriesRoot.path) else { return [] }

        let directories = try fileManager.contentsOfDirectory(
            at: entriesRoot,
            includingPropertiesForKeys: [.isDirectoryKey],
            options: [.skipsHiddenFiles]
        )

        return directories.compactMap { directory -> EntryID? in
            guard let entryID = EntryID(string: directory.lastPathComponent) else { return nil }
            let hasAudio = [finalURL(for: entryID), partialURL(for: entryID)].contains {
                (try? byteCount(at: $0)).map { $0 > 0 } ?? false
            }
            return hasAudio ? entryID : nil
        }
        .sorted { $0.description < $1.description }
    }

    /// Removes every local byte an entry holds, by removing the entry's own
    /// directory: the finished recording, any partial, and anything else that
    /// landed inside it.
    ///
    /// The target is derived from the entry id rather than accepted from the
    /// caller, so the only directory this can ever remove is the one named after
    /// that entry — neighbouring entries share no path with it. A directory that
    /// is already gone is a success, because cleanup is retried until it stops
    /// finding anything to do.
    public func removeAudio(for entryID: EntryID) throws {
        let directory = directory(for: entryID)
        guard fileManager.fileExists(atPath: directory.path) else { return }
        try fileManager.removeItem(at: directory)
    }

    /// Removes an in-flight partial. Only ever called for a capture that never
    /// produced audio; a non-empty partial is finalized instead of discarded.
    public func discardEmptyPartial(for entryID: EntryID) throws {
        let partial = partialURL(for: entryID)
        guard fileManager.fileExists(atPath: partial.path) else { return }
        guard try byteCount(at: partial) == 0 else { return }
        try fileManager.removeItem(at: partial)
    }

    public static func sha256(of url: URL) throws -> String {
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }

        var hasher = SHA256()
        while let chunk = try handle.read(upToCount: 1 << 20), !chunk.isEmpty {
            hasher.update(data: chunk)
        }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }
}
