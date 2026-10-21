import CaptureCore
import Foundation

/// One spoken reflection, as the sidecar beside its audio describes it:
/// `audio/<date>/<id>.json` next to `audio/<date>/<id>.m4a`. Device-local,
/// outside the journal, never exchanged.
struct SpokenTake: Codable, Equatable, Sendable, Identifiable {
    static let currentVersion = 1

    enum State: String, Codable, Sendable {
        /// Recorded and kept; not transcribed yet. A take finalized from a
        /// partial at launch starts here and is offered for transcription.
        case untranscribed
        /// The speech model for the take's language is not on the device yet.
        case waitingForModel = "waiting-for-model"
        /// Transcription failed or was abandoned; the take is kept for retry.
        case failed
        case transcribed
    }

    var version = SpokenTake.currentVersion
    /// The audio's file name without its extension: a lowercased UUID.
    var id: String
    /// The journal day the take was spoken for.
    var date: String
    var createdAt: Date
    var durationSeconds: Double
    var byteCount: Int
    var audioSha256: String
    /// The language the take is transcribed in.
    var locale: String
    var state: State
    var transcript: String?
    /// Which on-device transcriber produced the transcript.
    var transcriber: String?
    var transcribedAt: Date?
    /// Why the last transcription did not produce a transcript.
    var failure: String?
    /// Finalized from a partial a stopped app left behind.
    var recovered = false
    /// When the author accepted answers from this take into the day's
    /// draft. Nil until then; the draft is saved by the form as usual.
    var acceptedAt: Date?
    /// The answers proposed from this take were saved to the journal.
    var answersSaved = false
    /// When the author stopped the take. With `transcribedAt` and
    /// `proposalSeconds` it times a take becoming proposed answers, which
    /// device qualification measures (docs/product/apple-models.md).
    var stoppedAt: Date?
    /// How long the on-device model took to propose answers from the
    /// transcript, the last time it did.
    var proposalSeconds: Double?
    /// Why the on-device model could not propose answers, the last time it
    /// was asked and was not available (an iPhone without Apple
    /// Intelligence, for one).
    var proposalUnavailable: String?
}

/// The spoken takes on this iPhone: `Application Support/Seneca/audio`, one
/// directory per journal day. Audio is recorded straight into the day's
/// directory as `<id>.partial.m4a` and renamed to `<id>.m4a` when it is
/// finished; the sidecar is written whole and replaced in one rename.
struct SpokenTakeStore: Sendable {
    let root: URL

    /// The vendored store, rooted here, so a finished take is promoted by the
    /// same rules the recorder uses.
    var files: AudioFileStore { AudioFileStore(applicationSupportRoot: root) }

    static func isDate(_ date: String) -> Bool {
        date.wholeMatch(of: /\d{4}-\d{2}-\d{2}/) != nil
    }

    static func isID(_ id: String) -> Bool {
        UUID(uuidString: id) != nil && id == id.lowercased()
    }

    func dayDirectory(_ date: String) -> URL {
        root.appendingPathComponent(date, isDirectory: true)
    }

    func audioURL(date: String, id: String) -> URL {
        dayDirectory(date).appendingPathComponent("\(id).m4a")
    }

    func partialURL(date: String, id: String) -> URL {
        AudioFileStore.partialURL(forFinalURL: audioURL(date: date, id: id))
    }

    func sidecarURL(date: String, id: String) -> URL {
        dayDirectory(date).appendingPathComponent("\(id).json")
    }

    /// Creates the day's directory with the capture protection class, so a
    /// partial is protected from its first byte.
    func prepareDay(_ date: String) throws {
        guard Self.isDate(date) else { throw CocoaError(.fileWriteInvalidFileName) }
        let directory = dayDirectory(date)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try files.applyCaptureProtection(to: directory)
    }

    func write(_ take: SpokenTake) throws {
        guard Self.isDate(take.date), Self.isID(take.id) else { throw CocoaError(.fileWriteInvalidFileName) }
        try FileManager.default.createDirectory(at: dayDirectory(take.date), withIntermediateDirectories: true)
        try Self.encoder.encode(take).write(
            to: sidecarURL(date: take.date, id: take.id),
            options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication]
        )
    }

    func read(date: String, id: String) throws -> SpokenTake? {
        guard Self.isDate(date), Self.isID(id) else { return nil }
        let data: Data
        do {
            data = try Data(contentsOf: sidecarURL(date: date, id: id))
        } catch CocoaError.fileReadNoSuchFile {
            return nil
        }
        return try Self.decoder.decode(SpokenTake.self, from: data)
    }

    /// Deletes a take: its audio and its sidecar, nothing else. The day's
    /// directory goes too once it is empty.
    func delete(date: String, id: String) throws {
        guard Self.isDate(date), Self.isID(id) else { throw CocoaError(.fileWriteInvalidFileName) }
        for url in [audioURL(date: date, id: id), sidecarURL(date: date, id: id)] {
            do {
                try FileManager.default.removeItem(at: url)
            } catch CocoaError.fileNoSuchFile {
                continue
            }
        }
        let directory = dayDirectory(date)
        if (try? FileManager.default.contentsOfDirectory(atPath: directory.path))?.isEmpty == true {
            try? FileManager.default.removeItem(at: directory)
        }
    }

    /// The bytes a take's audio and sidecar hold on this iPhone.
    func bytes(date: String, id: String) -> Int {
        [audioURL(date: date, id: id), sidecarURL(date: date, id: id)].reduce(0) { total, url in
            total + (((try? FileManager.default.attributesOfItem(atPath: url.path))?[.size] as? Int) ?? 0)
        }
    }

    /// Every readable take, newest first.
    func all() -> [SpokenTake] {
        days().flatMap { date in
            names(in: date)
                .filter { $0.hasSuffix(".json") }
                .compactMap { try? read(date: date, id: String($0.dropLast(5))) }
        }
        .sorted { ($0.createdAt, $0.id) > ($1.createdAt, $1.id) }
    }

    /// Audio no finished take claims: a partial a stopped recording left, or
    /// a finished file whose sidecar was never written. As (date, id).
    func unclaimed() -> [(date: String, id: String)] {
        days().flatMap { date in
            names(in: date).compactMap { name -> (date: String, id: String)? in
                let id: String
                if name.hasSuffix(".partial.m4a") {
                    id = String(name.dropLast(".partial.m4a".count))
                } else if name.hasSuffix(".m4a") {
                    id = String(name.dropLast(".m4a".count))
                    if FileManager.default.fileExists(atPath: sidecarURL(date: date, id: id).path) { return nil }
                } else {
                    return nil
                }
                return Self.isID(id) ? (date, id) : nil
            }
        }
        .reduce(into: [(date: String, id: String)]()) { found, next in
            if !found.contains(where: { $0 == next }) { found.append(next) }
        }
    }

    /// The hidden name a restore copies `destination` to before renaming it
    /// into place.
    func restoringURL(for destination: URL) -> URL {
        destination.deletingLastPathComponent().appendingPathComponent(".\(destination.lastPathComponent).restoring")
    }

    /// Finishes or undoes every restore a stopped app left behind. A restore
    /// copies a take's sidecar and audio under hidden names, then renames the
    /// sidecar into place and the audio after it. Stopped between the two,
    /// the sidecar is in place and the audio is whole under its hidden name:
    /// it is renamed into place when its bytes are the ones the sidecar
    /// names. Anything else is undone, since nothing was here before a
    /// restore began. How many takes were settled.
    @discardableResult
    func settleRestores() -> Int {
        let manager = FileManager.default
        var settled = 0
        for date in days() {
            let ids = Set(names(in: date).compactMap { name -> String? in
                guard name.hasPrefix("."), name.hasSuffix(".restoring") else { return nil }
                let inner = name.dropFirst().dropLast(".restoring".count)
                for suffix in [".m4a", ".json"] where inner.hasSuffix(suffix) {
                    return String(inner.dropLast(suffix.count))
                }
                return nil
            }).filter(Self.isID)
            for id in ids.sorted() {
                let audio = audioURL(date: date, id: id)
                let sidecar = sidecarURL(date: date, id: id)
                let audioTemporary = restoringURL(for: audio)
                let sidecarTemporary = restoringURL(for: sidecar)
                settled += 1
                if !manager.fileExists(atPath: audio.path), !manager.fileExists(atPath: sidecarTemporary.path),
                   manager.fileExists(atPath: audioTemporary.path),
                   let take = try? read(date: date, id: id),
                   (try? AudioFileStore.sha256(of: audioTemporary)) == take.audioSha256,
                   (try? manager.moveItem(at: audioTemporary, to: audio)) != nil {
                    continue
                }
                try? manager.removeItem(at: audioTemporary)
                try? manager.removeItem(at: sidecarTemporary)
                if !manager.fileExists(atPath: audio.path) {
                    try? manager.removeItem(at: sidecar)
                }
            }
            let directory = dayDirectory(date)
            if (try? manager.contentsOfDirectory(atPath: directory.path))?.isEmpty == true {
                try? manager.removeItem(at: directory)
            }
        }
        return settled
    }

    private func days() -> [String] {
        ((try? FileManager.default.contentsOfDirectory(atPath: root.path)) ?? []).filter(Self.isDate).sorted()
    }

    private func names(in date: String) -> [String] {
        ((try? FileManager.default.contentsOfDirectory(atPath: dayDirectory(date).path)) ?? []).sorted()
    }

    private static var encoder: JSONEncoder {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        encoder.dateEncodingStrategy = .iso8601
        encoder.keyEncodingStrategy = .convertToSnakeCase
        return encoder
    }

    private static var decoder: JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return decoder
    }
}

/// The languages a take can be transcribed in: the journal's, English and
/// Québec French at first (docs/product/scope.md).
enum SpeechLanguage: String, Codable, CaseIterable, Identifiable, Sendable {
    case english = "en-US"
    case quebecFrench = "fr-CA"

    var id: String { rawValue }

    var name: String {
        switch self {
        case .english: return "English"
        case .quebecFrench: return "Français (Québec)"
        }
    }

    /// The iPhone's own language, when it is one of these.
    static func preferred(_ languages: [String] = Locale.preferredLanguages) -> SpeechLanguage {
        languages.first?.lowercased().hasPrefix("fr") == true ? .quebecFrench : .english
    }
}

/// What happens to a take's audio once its answers are in the journal.
enum AudioRetention: String, Codable, CaseIterable, Identifiable, Sendable {
    /// Every take stays on this iPhone until it is deleted by hand.
    case keep
    /// A take is deleted, audio and sidecar, once the answers proposed from
    /// it are saved to the journal.
    case deleteAfterSaving = "delete-after-saving"

    var id: String { rawValue }

    var name: String {
        switch self {
        case .keep: return "Keep"
        case .deleteAfterSaving: return "Delete After Saving"
        }
    }
}

/// The language this iPhone transcribes in and what it keeps of a take:
/// device-local, never exchanged.
struct VoicePreferences: Codable, Equatable, Sendable {
    var language: SpeechLanguage
    var retention: AudioRetention = .keep
}

extension VoicePreferences {
    private enum CodingKeys: String, CodingKey {
        case language, retention
    }

    /// A file written before retention was a choice keeps every take.
    init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        language = try container.decode(SpeechLanguage.self, forKey: .language)
        retention = try container.decodeIfPresent(AudioRetention.self, forKey: .retention) ?? .keep
    }
}

struct VoicePreferencesStore: Sendable {
    let file: URL

    func read() -> VoicePreferences? {
        guard let data = try? Data(contentsOf: file) else { return nil }
        return try? JSONDecoder().decode(VoicePreferences.self, from: data)
    }

    func write(_ preferences: VoicePreferences) throws {
        try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        try encoder.encode(preferences).write(to: file, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }
}
