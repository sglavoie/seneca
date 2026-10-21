import CaptureCore
import Foundation

// Ported from an earlier app's tests, which drove the engine through that
// app's database; these drive it through
// InMemoryCaptureRecords, the smallest record store the engine accepts.

/// A disposable Application Support root. Each suite gets its own so tests never
/// see one another's entries.
struct CaptureSandbox {
    let root: URL
    let store: AudioFileStore

    init() {
        root = URL(fileURLWithPath: NSTemporaryDirectory(), isDirectory: true)
            .appendingPathComponent("seneca-capture-\(UUID().uuidString)", isDirectory: true)
        try? FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        store = AudioFileStore(applicationSupportRoot: root)
    }

    func destroy() {
        try? FileManager.default.removeItem(at: root)
    }

    func writePartial(_ bytes: Data, for entryID: EntryID) throws {
        try store.prepareDirectory(for: entryID)
        try bytes.write(to: store.partialURL(for: entryID))
    }

    func writeFinal(_ bytes: Data, for entryID: EntryID) throws {
        try store.prepareDirectory(for: entryID)
        try bytes.write(to: store.finalURL(for: entryID))
    }

    func exists(_ url: URL) -> Bool {
        FileManager.default.fileExists(atPath: url.path)
    }
}

/// Stands in for `AudioRecorderAdapter`.
///
/// It writes real bytes to the real partial path and finalizes through the real
/// `AudioFileStore`, so everything the engine depends on — atomic rename,
/// hashing, zero-byte refusal — is exercised without an audio device.
actor StubAudioRecorder: AudioRecording {
    struct Script: Sendable {
        var startError: RecordingError?
        var stopError: RecordingError?
        var payload: Data = Data("fake-aac-payload".utf8)
        var durationSeconds: Double = 12.5
    }

    private let store: AudioFileStore
    private let script: Script
    private var destination: URL?

    private(set) var startedConfiguration: RecordingConfiguration?
    private(set) var cancelCount = 0

    init(store: AudioFileStore, script: Script = Script()) {
        self.store = store
        self.script = script
    }

    func startRecording(to destination: URL, configuration: RecordingConfiguration) async throws {
        if let error = script.startError { throw error }
        startedConfiguration = configuration
        self.destination = destination
        try script.payload.write(to: AudioFileStore.partialURL(forFinalURL: destination))
    }

    func stopRecording() async throws -> RecordedAudio {
        if let error = script.stopError { throw error }
        guard let destination else { throw RecordingError.noActiveRecording }
        self.destination = nil

        let stored = try store.finalize(finalURL: destination)
        return RecordedAudio(
            url: stored.url,
            durationSeconds: script.durationSeconds,
            byteCount: stored.byteCount,
            sha256: stored.sha256
        )
    }

    func cancelRecording() async {
        cancelCount += 1
        destination = nil
    }

    /// Bytes beginning with `unplayablePrefix` stand in for a file a killed
    /// recorder left without an index: present, non-empty, and undecodable.
    func duration(of url: URL) async throws -> Double {
        let bytes = try Data(contentsOf: url)
        if bytes.starts(with: Self.unplayablePrefix) {
            throw CocoaError(.fileReadCorruptFile)
        }
        return script.durationSeconds
    }

    static let unplayablePrefix = Data("unplayable".utf8)
}

/// The host's durable records, kept in memory. A record is `saved` once the
/// engine has committed its audio; until then it is unfinished, with the reason
/// the last capture attempt left on it.
actor InMemoryCaptureRecords: CaptureRecordStore {
    typealias Context = String

    struct Record: Hashable, Sendable {
        var id: EntryID
        /// The host's association for the recording; nil for one recovery rebuilt.
        var context: String?
        var capturedAt: Date
        var timeZoneIdentifier: String
        var isSaved = false
        var audio: StoredAudio?
        var durationSeconds: Double?
        var captureFailure: CaptureFailure?

        var hasDurableAudio: Bool { isSaved && audio != nil }
    }

    typealias Snapshot = Record

    private var records: [EntryID: Record] = [:]

    func record(id: EntryID) -> Record? { records[id] }
    var all: [Record] { records.values.sorted { $0.id.description < $1.id.description } }

    func createRecording(
        id: EntryID, context: String, capturedAt: Date,
        timeZoneIdentifier: String, now: Date
    ) throws -> Record {
        let record = Record(id: id, context: context, capturedAt: capturedAt, timeZoneIdentifier: timeZoneIdentifier)
        records[id] = record
        return record
    }

    func createRecoveredRecording(
        id: EntryID, capturedAt: Date, timeZoneIdentifier: String, now: Date
    ) throws -> Record {
        let record = Record(id: id, context: nil, capturedAt: capturedAt, timeZoneIdentifier: timeZoneIdentifier)
        records[id] = record
        return record
    }

    func markSaved(id: EntryID, audio: StoredAudio, durationSeconds: Double, now: Date) throws -> Record {
        guard var record = records[id] else { throw CocoaError(.fileNoSuchFile) }
        record.isSaved = true
        record.audio = audio
        record.durationSeconds = durationSeconds
        record.captureFailure = nil
        records[id] = record
        return record
    }

    func markCaptureFailure(id: EntryID, failure: CaptureFailure, now: Date) throws {
        guard var record = records[id] else { throw CocoaError(.fileNoSuchFile) }
        record.captureFailure = failure
        records[id] = record
    }

    func unfinishedRecordings() throws -> [UnfinishedCapture] {
        all.filter { !$0.isSaved }.map { UnfinishedCapture(id: $0.id, captureFailure: $0.captureFailure) }
    }

    func contains(id: EntryID) throws -> Bool { records[id] != nil }
}

/// Stands in for the phone's lock state. `availableChecks` lets a test lock
/// the phone partway through a pass: the first checks see it unlocked, the
/// rest locked.
final class LockableProtectedData: ProtectedDataAvailability, @unchecked Sendable {
    private let lock = NSLock()
    private var locked: Bool
    private var availableChecks: Int?

    init(locked: Bool = false, availableChecks: Int? = nil) {
        self.locked = locked
        self.availableChecks = availableChecks
    }

    func set(locked: Bool) {
        lock.lock()
        defer { lock.unlock() }
        self.locked = locked
        availableChecks = nil
    }

    var isAvailable: Bool {
        lock.lock()
        defer { lock.unlock() }
        if let remaining = availableChecks {
            availableChecks = remaining - 1
            return remaining > 0
        }
        return !locked
    }
}

/// One engine over a sandbox, a stub recorder and in-memory records.
struct CaptureRig {
    static let now = Date(timeIntervalSince1970: 1_770_000_000)
    static let timeZone = TimeZone(identifier: "America/Toronto")!

    let sandbox: CaptureSandbox
    let recorder: StubAudioRecorder
    let records: InMemoryCaptureRecords
    let engine: AudioCaptureEngine<InMemoryCaptureRecords>

    init(
        sandbox: CaptureSandbox = CaptureSandbox(),
        store: AudioFileStore? = nil,
        records: InMemoryCaptureRecords = InMemoryCaptureRecords(),
        script: StubAudioRecorder.Script = .init(),
        protectedData: any ProtectedDataAvailability = AlwaysAvailableProtectedData()
    ) {
        let store = store ?? sandbox.store
        self.sandbox = sandbox
        recorder = StubAudioRecorder(store: store, script: script)
        self.records = records
        engine = AudioCaptureEngine(
            store: store,
            recorder: recorder,
            entries: records,
            clock: FixedDateProvider(now: Self.now),
            identifiers: SequentialIdentifierProvider(),
            timeZone: Self.timeZone,
            protectedData: protectedData
        )
    }

    /// A row left unfinished with bytes at the partial path, as a killed or
    /// locked-out recording leaves it.
    func strand(_ entryID: EntryID, bytes: Data) async throws {
        _ = try await records.createRecording(
            id: entryID, context: "evening", capturedAt: Self.now,
            timeZoneIdentifier: Self.timeZone.identifier, now: Self.now
        )
        try sandbox.writePartial(bytes, for: entryID)
    }
}

extension CaptureRecoveryOutcome {
    var recoveredEntryID: EntryID? {
        if case .recovered(let id, _) = self { return id }
        return nil
    }

    var adoptedEntryID: EntryID? {
        if case .adopted(let id, _) = self { return id }
        return nil
    }

    var unrecoverableEntryID: EntryID? {
        if case .unrecoverable(let id, _) = self { return id }
        return nil
    }
}
