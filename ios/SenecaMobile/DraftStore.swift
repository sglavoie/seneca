import Foundation
import SenecaEngine

/// One day's unsaved writing, as the form held it: the draft the engine
/// prepared (its date, base revision, settings revision, questions, and the
/// context shown) and what the author typed against it.
struct StoredDraft: Codable, Equatable, Sendable, Identifiable {
    static let currentVersion = 1

    var version = StoredDraft.currentVersion
    /// The engine's draft the writing was last based on. A save is
    /// conditional on its base and settings revisions.
    var draft: ReflectionDraft
    /// One answer per question of `draft`, in order.
    var answers: [String]
    var followThrough: String
    /// Answers kept from questions the journal no longer asks.
    var unasked: [UnaskedAnswer] = []
    /// The author chose to write without the focus and the prior intention.
    var blank = false
    /// The day or the questions changed in the journal after this writing
    /// began. It stays set until the writing is saved or discarded.
    var changedUnderneath = false
    /// Why the last save did not happen, as the engine reported it.
    var reason: String?
    var updatedAt: Date

    var id: String { draft.date }
    var date: String { draft.date }
}

/// Unsaved reflections, one file per day, in a directory of this device's
/// own. A draft is written whole and replaced in one rename, so a stopped
/// app leaves either the previous draft or the new one.
struct DraftStore: Sendable {
    let directory: URL

    private func url(for date: String) -> URL {
        directory.appendingPathComponent("\(date).json")
    }

    /// Whether `date` is safe to name a file by.
    private static func isDate(_ date: String) -> Bool {
        date.wholeMatch(of: /\d{4}-\d{2}-\d{2}/) != nil
    }

    func write(_ draft: StoredDraft) throws {
        guard Self.isDate(draft.date) else { throw CocoaError(.fileWriteInvalidFileName) }
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        encoder.dateEncodingStrategy = .iso8601
        try encoder.encode(draft).write(to: url(for: draft.date), options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    /// The draft kept for `date`, or nil when there is none.
    func read(_ date: String) throws -> StoredDraft? {
        guard Self.isDate(date) else { return nil }
        let data: Data
        do {
            data = try Data(contentsOf: url(for: date))
        } catch CocoaError.fileReadNoSuchFile {
            return nil
        }
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(StoredDraft.self, from: data)
    }

    func discard(_ date: String) throws {
        guard Self.isDate(date) else { return }
        do {
            try FileManager.default.removeItem(at: url(for: date))
        } catch CocoaError.fileNoSuchFile {
        }
    }

    /// Every readable draft, newest day first. A draft that cannot be read
    /// is left where it is and not listed.
    func all() -> [StoredDraft] {
        let names = (try? FileManager.default.contentsOfDirectory(atPath: directory.path)) ?? []
        return names
            .filter { $0.hasSuffix(".json") }
            .compactMap { try? read(String($0.dropLast(5))) }
            .sorted { $0.date > $1.date }
    }
}
