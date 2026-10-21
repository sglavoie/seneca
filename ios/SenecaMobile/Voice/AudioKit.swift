import CaptureCore
import Foundation
import SenecaEngine

/// The spoken takes in a recovery kit: an `audio/` folder beside the
/// engine's export, `<date>/<id>.m4a` and `<date>/<id>.json` as they are on
/// the iPhone, and `MANIFEST.json` with each file's size and SHA-256. The
/// app writes and reads it; the engine's export does not know it is there.
struct AudioKitManifest: Codable, Equatable, Sendable {
    static let currentVersion = 1
    static let currentFormat = "seneca-audio"
    static let fileName = "MANIFEST.json"
    static let directoryName = "audio"

    struct File: Codable, Equatable, Sendable {
        /// `<date>/<id>.m4a` or `<date>/<id>.json`.
        var path: String
        var bytes: Int
        var sha256: String
    }

    /// Tells this manifest from the engine export's, which shares its name.
    var format = AudioKitManifest.currentFormat
    var version = AudioKitManifest.currentVersion
    var files: [File]

    /// How many takes the folder holds.
    var takes: Int { files.filter { $0.path.hasSuffix(".m4a") }.count }
}

/// What restoring a kit's audio did.
struct AudioRestore: Equatable, Sendable {
    /// Takes copied back onto this iPhone.
    var restored = 0
    /// Takes already here, byte for byte.
    var alreadyHere = 0
    /// Takes whose id is here with different bytes. The iPhone's copy is
    /// kept; the kit's is left in the kit.
    var kept = 0
}

enum AudioKitProblem: Error, Equatable, LocalizedError {
    /// No `audio/MANIFEST.json` where the kit was chosen.
    case noAudio
    case unreadableManifest
    case newerManifest(Int)
    /// A path in the manifest that is not a take's audio or sidecar.
    case foreignPath(String)
    /// A file missing from the kit, or whose bytes are not the manifest's.
    case damaged(String)

    var errorDescription: String? {
        switch self {
        case .noAudio: return "This folder holds no spoken reflections."
        case .unreadableManifest: return "The spoken reflections’ manifest cannot be read."
        case .newerManifest: return "These spoken reflections were saved by a newer Seneca."
        case .foreignPath: return "The spoken reflections’ manifest names a file that is not a recording."
        case .damaged: return "A recording in this folder is missing or damaged. Nothing was restored."
        }
    }
}

extension SpokenTakeStore {
    /// Copies every finished take, audio and sidecar, into `directory`, which
    /// must not exist yet, and writes its manifest last. Partials and audio
    /// with no sidecar are not finished takes and stay behind.
    @discardableResult
    func exportKit(to directory: URL) throws -> AudioKitManifest {
        let manager = FileManager.default
        try manager.createDirectory(at: directory, withIntermediateDirectories: false)
        var entries: [AudioKitManifest.File] = []
        for take in all().sorted(by: { ($0.date, $0.id) < ($1.date, $1.id) }) {
            let audio = audioURL(date: take.date, id: take.id)
            guard manager.fileExists(atPath: audio.path) else { continue }
            let day = directory.appendingPathComponent(take.date, isDirectory: true)
            try manager.createDirectory(at: day, withIntermediateDirectories: true)
            for source in [audio, sidecarURL(date: take.date, id: take.id)] {
                let copy = day.appendingPathComponent(source.lastPathComponent)
                try manager.copyItem(at: source, to: copy)
                entries.append(AudioKitManifest.File(
                    path: "\(take.date)/\(source.lastPathComponent)",
                    bytes: try files.byteCount(at: copy),
                    sha256: try AudioFileStore.sha256(of: copy)
                ))
            }
        }
        let manifest = AudioKitManifest(files: entries)
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
        try encoder.encode(manifest).write(to: directory.appendingPathComponent(AudioKitManifest.fileName), options: .atomic)
        return manifest
    }

    /// Puts a kit's takes back: `kit` is the kit, or its `audio/` folder.
    /// Every file is checked against the manifest, and every take for both
    /// its audio and its sidecar, before anything is written, so a damaged
    /// or incomplete kit restores nothing; a restore that fails part way
    /// takes back what it put back. A take already here is never replaced.
    /// Each take is copied under a temporary name and renamed into place,
    /// sidecar first, so recovery never mistakes a half-restored take for
    /// one a stopped recording left, and finishes or undoes it instead.
    func restoreKit(from kit: URL) throws -> AudioRestore {
        let manager = FileManager.default
        var folder = kit.appendingPathComponent(AudioKitManifest.directoryName, isDirectory: true)
        if !manager.fileExists(atPath: folder.appendingPathComponent(AudioKitManifest.fileName).path) {
            folder = kit
        }
        let manifestURL = folder.appendingPathComponent(AudioKitManifest.fileName)
        guard manager.fileExists(atPath: manifestURL.path) else { throw AudioKitProblem.noAudio }
        struct Probe: Decodable { var format: String? }
        let manifest: AudioKitManifest
        do {
            let data = try Data(contentsOf: manifestURL)
            // The engine's export alone: its manifest, not an audio one.
            if let probe = try? JSONDecoder().decode(Probe.self, from: data), probe.format != AudioKitManifest.currentFormat {
                throw AudioKitProblem.noAudio
            }
            manifest = try JSONDecoder().decode(AudioKitManifest.self, from: data)
        } catch AudioKitProblem.noAudio {
            throw AudioKitProblem.noAudio
        } catch {
            throw AudioKitProblem.unreadableManifest
        }
        guard manifest.version <= AudioKitManifest.currentVersion else {
            throw AudioKitProblem.newerManifest(manifest.version)
        }

        // Check everything first.
        var takes: [String: (date: String, id: String, audio: AudioKitManifest.File?, sidecar: AudioKitManifest.File?)] = [:]
        for file in manifest.files {
            guard let match = file.path.wholeMatch(of: #/(\d{4}-\d{2}-\d{2})/([0-9a-f-]+)\.(m4a|json)/#),
                  Self.isDate(String(match.1)), Self.isID(String(match.2)) else {
                throw AudioKitProblem.foreignPath(file.path)
            }
            let source = folder.appendingPathComponent(file.path)
            guard (try? files.byteCount(at: source)) == file.bytes,
                  (try? AudioFileStore.sha256(of: source)) == file.sha256 else {
                throw AudioKitProblem.damaged(file.path)
            }
            let key = "\(match.1)/\(match.2)"
            var take = takes[key] ?? (String(match.1), String(match.2), nil, nil)
            if match.3 == "m4a" { take.audio = file } else { take.sidecar = file }
            takes[key] = take
        }

        // Every take whole, audio and sidecar, before anything is copied.
        var complete: [(date: String, id: String, audio: AudioKitManifest.File, sidecar: AudioKitManifest.File)] = []
        for key in takes.keys.sorted() {
            guard let take = takes[key], let audio = take.audio, let sidecar = take.sidecar else {
                throw AudioKitProblem.damaged(key)
            }
            complete.append((take.date, take.id, audio, sidecar))
        }

        // A restore stopped between its renames is finished or undone before
        // anything here is taken for a take already on this iPhone.
        settleRestores()

        var result = AudioRestore()
        var placed: [(date: String, id: String)] = []
        do {
            for take in complete {
                let audioHere = audioURL(date: take.date, id: take.id)
                let sidecarHere = sidecarURL(date: take.date, id: take.id)
                if manager.fileExists(atPath: audioHere.path) || manager.fileExists(atPath: sidecarHere.path) {
                    let same = (try? AudioFileStore.sha256(of: audioHere)) == take.audio.sha256
                        && (try? AudioFileStore.sha256(of: sidecarHere)) == take.sidecar.sha256
                    if same { result.alreadyHere += 1 } else { result.kept += 1 }
                    continue
                }
                try prepareDay(take.date)
                let pending = [
                    (folder.appendingPathComponent(take.sidecar.path), sidecarHere),
                    (folder.appendingPathComponent(take.audio.path), audioHere),
                ].map { source, destination in
                    (source, restoringURL(for: destination), destination)
                }
                do {
                    for (source, temporary, _) in pending {
                        try? manager.removeItem(at: temporary)
                        try manager.copyItem(at: source, to: temporary)
                        try manager.setAttributes([.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication], ofItemAtPath: temporary.path)
                    }
                    for (_, temporary, destination) in pending {
                        try manager.moveItem(at: temporary, to: destination)
                    }
                } catch {
                    // Nothing was here before; leave nothing half-restored.
                    for (_, temporary, destination) in pending {
                        try? manager.removeItem(at: temporary)
                        try? manager.removeItem(at: destination)
                    }
                    try? delete(date: take.date, id: take.id)
                    throw error
                }
                placed.append((take.date, take.id))
                result.restored += 1
            }
        } catch {
            // All or nothing: the takes this restore already put back go too.
            for take in placed {
                try? delete(date: take.date, id: take.id)
            }
            throw error
        }
        return result
    }
}

/// What the phone shares to recover from: the engine's export alone, or a
/// directory holding it and the spoken takes' `audio/` folder.
struct RecoveryKit: Sendable {
    /// What to save or send.
    var directory: URL
    var journal: RecoveryExport
    /// The spoken takes, when they were included.
    var audio: AudioKitManifest?
}
