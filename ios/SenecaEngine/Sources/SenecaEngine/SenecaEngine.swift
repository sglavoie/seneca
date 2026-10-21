import Foundation

/// The Go engine behind one actor. Every call is one bridge request, and the
/// actor runs them one at a time on a queue of its own: the engine never sees
/// two requests at once from here, and none of them runs on the main thread.
public actor SenecaEngine {
    private let queue: DispatchSerialQueue
    private let transport: BridgeTransport
    private let encoder: JSONEncoder
    private let decoder: JSONDecoder

    public nonisolated var unownedExecutor: UnownedSerialExecutor {
        queue.asUnownedSerialExecutor()
    }

    public init(transport: BridgeTransport = GoBridgeTransport()) {
        queue = DispatchSerialQueue(label: "io.sglavoie.seneca.engine", qos: .userInitiated)
        self.transport = transport
        encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        encoder.outputFormatting = [.sortedKeys]
        decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
    }

    /// The highest bridge version the linked engine speaks.
    public nonisolated var bridgeVersion: Int { transport.bridgeVersion }

    // MARK: Bridge and journal

    public func describe() throws -> BridgeDescription {
        try read("bridge.describe")
    }

    /// Opens the journal, reading it without writing anything. The journal
    /// stays open until `close` or the next `open`.
    @discardableResult
    public func open(_ location: JournalLocation) throws -> JournalStatus {
        guard transport.bridgeVersion == supportedBridgeVersion else {
            throw BridgeFailure.versionMismatch(engine: transport.bridgeVersion, required: supportedBridgeVersion)
        }
        struct Params: Encodable {
            let reflectionsPath: String
            let stateDir: String
            let questions: [String]?
            let anchorQuestionIndex: Int?
            let timeZone: String
            let now: String?
        }
        return try read("journal.open", Params(
            reflectionsPath: location.reflections.path,
            stateDir: location.stateDirectory.path,
            questions: location.questions,
            anchorQuestionIndex: location.anchorQuestionIndex,
            timeZone: location.timeZone.identifier,
            now: location.now.map { ISO8601DateFormatter().string(from: $0) }
        ))
    }

    public func status() throws -> JournalStatus {
        try read("journal.status")
    }

    public func close() throws {
        struct Closed: Decodable { let closed: Bool }
        let _: Closed = try read("journal.close")
    }

    // MARK: Entries

    /// The entries `query` keeps, in the order the file holds them.
    public func entries(_ query: EntryQuery = .everything) throws -> EntryList {
        try read("entries.list", query)
    }

    public func entry(on date: String) throws -> Entry {
        try read("entry.get", DateParams(date: date))
    }

    /// The days among those `query` keeps that mention `phrase`.
    public func search(_ phrase: String, in query: EntryQuery = .everything) throws -> [SearchHit] {
        struct Params: Encodable {
            let phrase: String
            let from: String?
            let to: String?
            let month: String?
            let onThisDay: Bool?
        }
        let result: SearchResult = try read("entries.search", Params(
            phrase: phrase, from: query.from, to: query.to, month: query.month, onThisDay: query.onThisDay
        ))
        return result.hits
    }

    /// Deletes the saved revision the author saw, with replay protection.
    public func deleteEntry(on date: String, base: String, requestID: RequestID = RequestID()) throws -> Outcome<DeletedEntry> {
        struct Params: Encodable {
            let date: String
            let base: String
        }
        return try mutate("entry.delete", Params(date: date, base: base), requestID)
    }

    // MARK: Reflections

    /// Reads the journal for a reflection on `date`, or on its today.
    public func prepareReflection(on date: String? = nil, blank: Bool = false) throws -> PreparedReflection {
        struct Params: Encodable {
            let date: String?
            let blank: Bool
        }
        return try read("reflection.prepare", Params(date: date, blank: blank))
    }

    /// Saves what the author wrote. The engine refuses when the day or the
    /// questions changed after the draft was prepared, and answers a request
    /// it already carried out instead of saving twice.
    public func saveReflection(_ writing: ReflectionWriting, requestID: RequestID = RequestID()) throws -> Outcome<SavedReflection> {
        try mutate("reflection.save", writing, requestID)
    }

    public func retainedDraft(on date: String) throws -> RetainedDraft? {
        let result: DraftResult = try read("draft.get", DateParams(date: date))
        return result.found ? result.draft : nil
    }

    /// Keeps writing a save could not store, on this device only.
    public func retainDraft(_ writing: ReflectionWriting, reason: String) throws {
        struct Params: Encodable {
            let draft: ReflectionDraft
            let answers: [String]
            let followThrough: String?
            let reason: String
        }
        let _: DatedResult = try read("draft.retain", Params(
            draft: writing.draft, answers: writing.answers, followThrough: writing.followThrough, reason: reason
        ))
    }

    public func discardDraft(on date: String) throws {
        let _: DatedResult = try read("draft.discard", DateParams(date: date))
    }

    // MARK: Settings

    public func settings() throws -> SettingsState {
        try read("settings.get")
    }

    /// Gives the journal settings of its own, seeded from this device's.
    public func initializeSettings(requestID: RequestID = RequestID()) throws -> Outcome<SettingsOutcome> {
        try mutate("settings.initialize", NoParams(), requestID)
    }

    /// Changes the questions, the anchor, or both as one unit, decided on
    /// the settings at `base`.
    public func updateSettings(
        base: String,
        questions: [String]? = nil,
        anchorQuestionIndex: Int? = nil,
        requestID: RequestID = RequestID()
    ) throws -> Outcome<SettingsOutcome> {
        struct Params: Encodable {
            let base: String
            let questions: [String]?
            let anchorQuestionIndex: Int?
        }
        return try mutate("settings.update", Params(base: base, questions: questions, anchorQuestionIndex: anchorQuestionIndex), requestID)
    }

    // MARK: Practice packs

    /// The built-in practice packs, the default practice first. Needs no
    /// open journal.
    public func builtinPacks() throws -> [PracticePack] {
        let list: PackList = try read("packs.list")
        return list.packs
    }

    /// Checks the contents of a `.seneca-pack.json` file and returns the pack
    /// as the engine reads it. Writes nothing; a pack the engine refuses
    /// throws `invalid-pack`.
    public func readPack(_ data: String) throws -> PracticePack {
        struct Params: Encodable { let data: String }
        let result: PackResult = try read("packs.read", Params(data: data))
        return result.pack
    }

    /// Makes `pack` the journal's practice from today: its questions and
    /// anchor replace the journal's as one unit, decided on the settings at
    /// `base`. Entries already written are not touched.
    public func applyPack(
        _ pack: PracticePack,
        base: String,
        requestID: RequestID = RequestID()
    ) throws -> Outcome<SettingsOutcome> {
        struct Params: Encodable {
            let base: String
            let pack: PracticePack
        }
        return try mutate("settings.apply_pack", Params(base: base, pack: pack), requestID)
    }

    // MARK: Follow-through insights

    /// What became of the intentions of the last `days` days, today included,
    /// and how long each focus stood. Reads only.
    public func insights(days: Int = 30) throws -> Insights {
        struct Params: Encodable { let days: Int }
        return try read("insights.get", Params(days: days))
    }

    // MARK: Share packets

    /// The packet of a saved review, the entries of `dates`, or both. Reads
    /// only: the app previews it and hands it to the share sheet.
    public func sharePacket(reviewID: String? = nil, dates: [String] = []) throws -> SharePacket {
        struct Params: Encodable {
            let reviewId: String?
            let dates: [String]
        }
        return try read("share.packet", Params(reviewId: reviewID, dates: dates))
    }

    // MARK: Reviews

    /// The saved reviews, newest first, or those of one period: weekly,
    /// monthly, quarterly, or yearly. Reviews are written on the desktop.
    public func reviews(period: String? = nil) throws -> [ReviewSummary] {
        struct Params: Encodable { let period: String? }
        let result: ReviewList = try read("reviews.list", Params(period: period))
        return result.reviews
    }

    public func review(id: String) throws -> Review {
        struct Params: Encodable { let id: String }
        return try read("review.get", Params(id: id))
    }

    // MARK: Focus

    /// The focus in effect on `date`, or on the journal's today, with the
    /// whole timeline.
    public func focus(on date: String? = nil) throws -> FocusReading {
        struct Params: Encodable { let date: String? }
        return try read("focus.get", Params(date: date))
    }

    /// Sets the focus from today, decided on the timeline at `base`.
    public func setFocus(_ text: String, base: String, requestID: RequestID = RequestID()) throws -> Outcome<FocusChange> {
        struct Params: Encodable {
            let base: String
            let text: String
        }
        return try mutate("focus.set", Params(base: base, text: text), requestID)
    }

    public func clearFocus(base: String, requestID: RequestID = RequestID()) throws -> Outcome<FocusChange> {
        struct Params: Encodable { let base: String }
        return try mutate("focus.clear", Params(base: base), requestID)
    }

    // MARK: Sync

    public func syncStatus() throws -> SyncStatus {
        try read("sync.status")
    }

    /// Makes this device the first of a synced journal.
    public func enrollSync(name: String, requestID: RequestID = RequestID()) throws -> Outcome<SyncStatus> {
        struct Params: Encodable { let name: String }
        return try mutate("sync.enroll", Params(name: name), requestID)
    }

    /// Makes this device another device of the journal `exchange` carries.
    public func joinSync(exchange: URL, name: String, requestID: RequestID = RequestID()) throws -> Outcome<SyncStatus> {
        struct Params: Encodable {
            let exchangePath: String
            let name: String
        }
        return try mutate("sync.join", Params(exchangePath: exchange.path, name: name), requestID)
    }

    /// Brings in what the exchange holds.
    public func importSync(exchange: URL, requestID: RequestID = RequestID()) throws -> Outcome<SyncImported> {
        try mutate("sync.import", ExchangeParams(exchangePath: exchange.path), requestID)
    }

    /// Sends what this device recorded and the exchange lacks, then a
    /// receipt of what it has applied. It changes nothing in the journal.
    public func exportSync(exchange: URL) throws -> SyncPublished {
        try read("sync.export", ExchangeParams(exchangePath: exchange.path))
    }

    /// Where this device's own changes have got to, as `exchange` shows it.
    public func syncDelivery(exchange: URL) throws -> SyncDelivery {
        try read("sync.delivery", ExchangeParams(exchangePath: exchange.path))
    }

    /// Ends the suspension of a journal file that went missing or lost most
    /// of what was recorded of it.
    public func recoverSync(_ file: SyncFile, action: SyncRecoveryAction, requestID: RequestID = RequestID()) throws -> Outcome<SyncRecovered> {
        struct Params: Encodable {
            let file: SyncFile
            let action: SyncRecoveryAction
        }
        return try mutate("sync.recover", Params(file: file, action: action), requestID)
    }

    /// Takes the journal out of sync: its files stay, and this device's
    /// identity is retired. It can then enroll or join as a new device.
    public func leaveSync(requestID: RequestID = RequestID()) throws -> Outcome<SyncLeft> {
        try mutate("sync.leave", NoParams(), requestID)
    }

    // MARK: Reminders, diagnostics, recovery

    /// The evening reminders still ahead: `times` (HH:MM; nil for the
    /// desktop's schedule) on each of `days` days from today, skipping every
    /// day this device holds an entry for. It schedules nothing.
    public func planReminders(times: [String]? = nil, days: Int? = nil) throws -> ReminderPlan {
        struct Params: Encodable {
            let times: [String]?
            let days: Int?
        }
        return try read("reminders.plan", Params(times: times, days: days))
    }

    /// A content-free report on the journal, and on the exchange when one is
    /// given. It changes nothing.
    public func diagnostics(exchange: URL? = nil) throws -> Diagnostics {
        struct Params: Encodable { let exchangePath: String? }
        return try read("diagnostics.collect", Params(exchangePath: exchange?.path))
    }

    /// Writes the whole journal, and the drafts in `drafts`, into the new
    /// directory `destination`. It appears whole or not at all.
    public func exportRecovery(to destination: URL, drafts: URL? = nil) throws -> RecoveryExport {
        struct Params: Encodable {
            let destination: String
            let draftsDir: String?
        }
        return try read("recovery.export", Params(destination: destination.path, draftsDir: drafts?.path))
    }

    // MARK: Conflicts

    public func conflicts() throws -> Conflicts {
        try read("conflicts.list")
    }

    /// Answers the conflict `id` names. The engine refuses when the conflict
    /// is no longer the one that was listed.
    public func resolveConflict(id: String, answer: ConflictAnswer, requestID: RequestID = RequestID()) throws -> Outcome<SyncResolved> {
        struct Params: Encodable {
            let id: String
            let keep: String?
            let content: String?
        }
        let params: Params
        switch answer {
        case .keep(let revision): params = Params(id: id, keep: revision, content: nil)
        case .content(let content): params = Params(id: id, keep: nil, content: content)
        }
        return try mutate("conflict.resolve", params, requestID)
    }

    public func resolveFocusDispute(id: String, answer: FocusDisputeAnswer, requestID: RequestID = RequestID()) throws -> Outcome<FocusChange> {
        struct Params: Encodable {
            let id: String
            let record: String?
            let text: String?
        }
        let params: Params
        switch answer {
        case .record(let record): params = Params(id: id, record: record, text: nil)
        case .text(let text): params = Params(id: id, record: nil, text: text)
        case .clear: params = Params(id: id, record: nil, text: nil)
        }
        return try mutate("focus_dispute.resolve", params, requestID)
    }

    // MARK: Transport

    private struct DateParams: Encodable { let date: String }
    private struct ExchangeParams: Encodable { let exchangePath: String }

    private func read<Result: Decodable & Sendable>(_ operation: String) throws -> Result {
        try send(operation, NoParams(), requestID: nil).value
    }

    private func read<Params: Encodable, Result: Decodable & Sendable>(_ operation: String, _ params: Params) throws -> Result {
        try send(operation, params, requestID: nil).value
    }

    private func mutate<Params: Encodable, Result: Decodable & Sendable>(
        _ operation: String, _ params: Params, _ requestID: RequestID
    ) throws -> Outcome<Result> {
        try send(operation, params, requestID: requestID.value)
    }

    private func send<Params: Encodable, Result: Decodable & Sendable>(
        _ operation: String, _ params: Params, requestID: String?
    ) throws -> Outcome<Result> {
        let request = Request(version: supportedBridgeVersion, operation: operation, requestId: requestID, params: params)
        let body = String(decoding: try encoder.encode(request), as: UTF8.self)
        let reply = Data(transport.call(body).utf8)
        let response: Response<Result>
        do {
            response = try decoder.decode(Response<Result>.self, from: reply)
        } catch {
            // A refusal has no result to decode; read it on its own.
            if let refusal = try? decoder.decode(Response<NoParams>.self, from: reply), let problem = refusal.error {
                throw problem
            }
            throw BridgeFailure.malformedResponse("\(operation): \(error)")
        }
        if let problem = response.error {
            throw problem
        }
        guard response.ok, let value = response.result else {
            throw BridgeFailure.malformedResponse("\(operation): the response carries neither a result nor an error")
        }
        return Outcome(value: value, replayed: response.replayed ?? false, warning: response.warning)
    }
}
