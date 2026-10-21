import Foundation
import Observation
import SenecaEngine
import UIKit

/// What the screens show of the journal. Everything it knows came from the
/// engine; it parses nothing and assembles no Markdown.
@MainActor
@Observable
final class JournalModel {
    enum Phase: Equatable {
        case opening
        case ready
        case failed(String)
    }

    private(set) var phase: Phase = .opening
    private(set) var status: JournalStatus?
    private(set) var entries: [EntrySummary] = []
    /// Why the entries could not be listed, when they could not.
    private(set) var entriesProblem: String?
    private(set) var focus: FocusState?
    private(set) var focusTimeline: [FocusEvent] = []
    /// The focus timeline's revision: a focus change is decided on it.
    private(set) var focusRevision: String?
    private(set) var reviews: [ReviewSummary] = []
    /// Why the focus and the reviews could not be read: the archive is
    /// unreadable. Reflections stay readable and writable without them.
    private(set) var archiveProblem: String?
    /// What today's reflection would be shown: the focus in effect and the
    /// intention being followed through.
    private(set) var today: PreparedReflection?
    /// Why today's context could not be read. Writing without it still works.
    private(set) var contextProblem: String?
    /// Unsaved writing on this device, newest day first.
    private(set) var drafts: [StoredDraft] = []
    /// Days two devices wrote differently, and focus changes two devices
    /// made for one day, waiting for the author to choose.
    private(set) var conflicts = Conflicts(conflicts: [], focusDisputes: [])
    /// Why the conflicts could not be listed, when they could not.
    private(set) var conflictsProblem: String?
    /// Counts reloads, so a screen showing one day reads it again.
    private(set) var generation = 0
    /// Whether this installation shows its first-run setup: only over a
    /// journal nothing has been written to or set in yet.
    private(set) var onboarding = false
    private var onboardingFile: URL?

    private let engine: SenecaEngine
    private let location: @Sendable () throws -> JournalLocation
    private let access: @Sendable (JournalLocation) -> any ExchangeAccess
    private(set) var draftStore: DraftStore?
    /// The exchange with the journal's other devices. Nil until the journal
    /// is open.
    private(set) var sync: SyncModel?
    /// The author's grant to the exchange folder, for the screen that picks
    /// it. Nil until the journal is open.
    private(set) var grantStore: GrantStore?
    /// The evening reminders on this iPhone. Nil until the journal is open.
    private(set) var reminders: ReminderScheduler?
    /// Spoken reflections on this iPhone. Nil until the journal is open.
    private(set) var voice: VoiceCapture?
    /// The journal as it was opened, and the zone its days are counted in.
    private var opened: JournalLocation?
    private let reminderCenter: any ReminderCenter
    private let identityVault: any IdentityVault
    private let voiceDevices: @Sendable (URL) -> VoiceDevices

    init(
        engine: SenecaEngine = SenecaEngine(),
        location: @escaping @Sendable () throws -> JournalLocation = AppPaths.journal,
        access: @escaping @Sendable (JournalLocation) -> any ExchangeAccess = AppPaths.exchangeAccess,
        reminderCenter: any ReminderCenter = AppPaths.reminderCenter(),
        identityVault: any IdentityVault = KeychainIdentityVault(),
        voiceDevices: @escaping @Sendable (URL) -> VoiceDevices = AppPaths.voiceDevices
    ) {
        self.engine = engine
        self.location = location
        self.access = access
        self.reminderCenter = reminderCenter
        self.identityVault = identityVault
        self.voiceDevices = voiceDevices
    }

    /// Opens the journal and reads what the first screen shows.
    func open() async {
        do {
            let location = try location()
            draftStore = DraftStore(directory: AppPaths.draftsDirectory(for: location))
            status = try await engine.open(location)
            opened = location
            try await reload()
            decideOnboarding(at: location)
            phase = .ready
            startSync(at: location)
            startReminders(at: location)
            startVoice(at: location)
        } catch {
            phase = .failed(Self.describe(error))
        }
    }

    /// Reads the journal again: after a save, or when the app comes forward.
    /// Only the journal's own status can fail it; an unreadable archive or
    /// reflections file is reported beside what could still be read.
    func reload() async throws {
        status = try await engine.status()
        do {
            entries = try await engine.entries().entries.sorted { $0.date > $1.date }
            entriesProblem = nil
        } catch {
            entries = []
            entriesProblem = Self.describe(error)
        }
        do {
            let reading = try await engine.focus()
            focus = reading.focus
            focusTimeline = reading.timeline
            focusRevision = reading.revision
            reviews = try await engine.reviews()
            archiveProblem = nil
        } catch {
            focus = nil
            focusTimeline = []
            focusRevision = nil
            reviews = []
            archiveProblem = Self.describe(error)
        }
        if status?.syncEnrolled == true {
            do {
                conflicts = try await engine.conflicts()
                conflictsProblem = nil
            } catch {
                conflicts = Conflicts(conflicts: [], focusDisputes: [])
                conflictsProblem = Self.describe(error)
            }
        } else {
            conflicts = Conflicts(conflicts: [], focusDisputes: [])
            conflictsProblem = nil
        }
        do {
            today = try await engine.prepareReflection()
            contextProblem = nil
        } catch {
            today = nil
            contextProblem = Self.describe(error)
        }
        refreshDrafts()
        generation += 1
    }

    /// Onboarding is offered once, to an installation over an untouched
    /// journal. One that already holds writing, settings, or an exchange
    /// is recorded as set up without asking.
    private func decideOnboarding(at location: JournalLocation) {
        let file = AppPaths.onboardingFile(for: location)
        onboardingFile = file
        guard !FileManager.default.fileExists(atPath: file.path) else { return }
        let untouched = status.map { $0.entryCount == 0 && !$0.settings.initialized && !$0.syncEnrolled } ?? false
        if untouched && drafts.isEmpty && AppPaths.offersOnboarding {
            onboarding = true
        } else {
            finishOnboarding()
        }
    }

    /// Ends the first-run setup on this iPhone, for good.
    func finishOnboarding() {
        onboarding = false
        guard let onboardingFile else { return }
        try? FileManager.default.createDirectory(at: onboardingFile.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? Data("{\"completed\":true}\n".utf8).write(to: onboardingFile, options: .atomic)
    }

    /// Everything of the journal lives on this iPhone; the exchange only
    /// carries whole changes to and from the folder the author chose. It
    /// starts with the journal and never holds it up.
    private func startSync(at location: JournalLocation) {
        guard sync == nil else { return }
        let directory = AppPaths.exchangeDirectory(for: location)
        let identity = IdentityGuard(vault: identityVault, marker: directory.appendingPathComponent("identity-guard"))
        let service = ExchangeService(engine: engine, access: access(location), directory: directory, identity: identity)
        let model = SyncModel(service: service, deviceName: UIDevice.current.name)
        model.onJournalChanged = { [weak self] in
            try? await self?.reload()
            await self?.reminders?.reconcile(.import)
        }
        sync = model
        grantStore = AppPaths.grantStore(for: location)
        Task { await model.start() }
    }

    /// Plans the evening reminders from what the journal holds, now and
    /// whenever that may change.
    private func startReminders(at location: JournalLocation) {
        guard reminders == nil else { return }
        let scheduler = ReminderScheduler(
            engine: engine, center: reminderCenter,
            store: ReminderPreferencesStore(file: AppPaths.remindersFile(for: location))
        )
        reminders = scheduler
        Task { await scheduler.reconcile(.launch) }
    }

    /// Takes a stopped app left half-written are finished and offered for
    /// transcription before anything is recorded.
    private func startVoice(at location: JournalLocation) {
        guard voice == nil else { return }
        let root = AppPaths.audioDirectory(for: location)
        let capture = VoiceCapture(
            store: SpokenTakeStore(root: root),
            devices: voiceDevices(root),
            preferences: VoicePreferencesStore(file: AppPaths.voiceFile(for: location))
        )
        voice = capture
        Task { await capture.recover() }
    }

    /// Exchanges with the other devices, then reads the journal again: for
    /// pull to refresh, which waits for both.
    func refresh() async {
        await sync?.exchange(.refresh)
        try? await reload()
    }

    /// After a save: shows it at once, and sends it without making the
    /// author wait for the folder.
    func saved() async {
        try? await reload()
        await reminders?.reconcile(.save)
        await sync?.refreshDelivery()
        if let sync {
            Task { await sync.exchange(.save) }
        }
    }

    /// When the app comes forward: another device, or the author on a Mac,
    /// may have written while it was away.
    func cameForward() async {
        try? await reload()
        await reminders?.reconcile(.foreground)
        // A take that ended while the iPhone was locked is finished now.
        await voice?.recover()
        await sync?.enterForeground()
    }

    /// When the day turns, the clock is set, or the iPhone moves to another
    /// zone: the journal's today may be another day, so the journal is
    /// opened again in the zone the iPhone is in now, and the reminders are
    /// planned again from it.
    func clockChanged() async {
        guard phase == .ready else { return }
        NSTimeZone.resetSystemTimeZone()
        if let current = try? location(), let opened, current.timeZone.identifier != opened.timeZone.identifier {
            if let reopened = try? await engine.open(current) {
                status = reopened
                self.opened = current
            }
        }
        try? await reload()
        await reminders?.reconcile(.clock)
    }

    /// The zone the journal's days are counted in now.
    var timeZone: TimeZone {
        opened?.timeZone ?? .current
    }

    // MARK: Diagnostics and recovery

    /// A content-free report on the journal, and on the exchange as this
    /// iPhone last mirrored it.
    func diagnostics() async throws -> Diagnostics {
        try await engine.diagnostics(exchange: sync?.folderName == nil ? nil : sync?.mirrorDirectory)
    }

    /// Writes everything written in the journal, and the unsaved writing
    /// kept on this iPhone, into a new directory to share. Nothing in the
    /// journal changes, and no identity goes with it.
    func exportRecovery(into parent: URL = FileManager.default.temporaryDirectory.appendingPathComponent("Recovery", isDirectory: true)) async throws -> RecoveryExport {
        guard let opened else {
            throw EngineProblem(code: "journal-not-open", message: "The journal is not open.")
        }
        try FileManager.default.createDirectory(at: parent, withIntermediateDirectories: true)
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd HH.mm.ss"
        formatter.locale = Locale(identifier: "en_US_POSIX")
        let name = "Seneca Journal \(formatter.string(from: Date()))"
        var destination = parent.appendingPathComponent(name, isDirectory: true)
        var n = 1
        while FileManager.default.fileExists(atPath: destination.path) {
            n += 1
            destination = parent.appendingPathComponent("\(name) \(n)", isDirectory: true)
        }
        return try await engine.exportRecovery(to: destination, drafts: AppPaths.draftsDirectory(for: opened))
    }

    /// The recovery export, and, when the author includes them, the spoken
    /// takes: then the kit is a directory holding the engine's export as it
    /// would be alone and an `audio/` folder with its own manifest beside it,
    /// written into a hidden directory and renamed into place last.
    func exportRecoveryKit(
        into parent: URL = FileManager.default.temporaryDirectory.appendingPathComponent("Recovery", isDirectory: true),
        includingAudio: Bool
    ) async throws -> RecoveryKit {
        guard includingAudio, let voice else {
            let export = try await exportRecovery(into: parent)
            return RecoveryKit(directory: URL(fileURLWithPath: export.directory, isDirectory: true), journal: export, audio: nil)
        }
        guard let opened else {
            throw EngineProblem(code: "journal-not-open", message: "The journal is not open.")
        }
        let manager = FileManager.default
        try manager.createDirectory(at: parent, withIntermediateDirectories: true)
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd HH.mm.ss"
        formatter.locale = Locale(identifier: "en_US_POSIX")
        let stamp = formatter.string(from: Date())
        var destination = parent.appendingPathComponent("Seneca Recovery \(stamp)", isDirectory: true)
        var n = 1
        while manager.fileExists(atPath: destination.path) {
            n += 1
            destination = parent.appendingPathComponent("Seneca Recovery \(stamp) \(n)", isDirectory: true)
        }
        let partial = parent.appendingPathComponent(
            ".\(destination.lastPathComponent).partial-\(UUID().uuidString.prefix(12).lowercased())", isDirectory: true
        )
        try manager.createDirectory(at: partial, withIntermediateDirectories: false)
        do {
            let name = "Seneca Journal \(stamp)"
            var export = try await engine.exportRecovery(
                to: partial.appendingPathComponent(name, isDirectory: true), drafts: AppPaths.draftsDirectory(for: opened)
            )
            let audio = try voice.store.exportKit(to: partial.appendingPathComponent(AudioKitManifest.directoryName, isDirectory: true))
            try manager.moveItem(at: partial, to: destination)
            export.directory = destination.appendingPathComponent(name, isDirectory: true).path
            return RecoveryKit(directory: destination, journal: export, audio: audio)
        } catch {
            try? manager.removeItem(at: partial)
            throw error
        }
    }

    /// When the app leaves the screen: one more exchange, if iOS allows it.
    func wentBack() async {
        await sync?.enterBackground()
    }

    func refreshDrafts() {
        drafts = draftStore?.all() ?? []
    }

    func draft(on date: String) -> StoredDraft? {
        drafts.first { $0.date == date }
    }

    func hasEntry(on date: String) -> Bool {
        entries.contains { $0.date == date }
    }

    func entry(on date: String) async throws -> Entry {
        try await engine.entry(on: date)
    }

    func deleteEntry(on date: String, base: String, requestID: RequestID) async throws {
        _ = try await engine.deleteEntry(on: date, base: base, requestID: requestID)
        await saved()
    }

    // MARK: Browsing

    /// The days `query` keeps, newest first, with the nearest days written
    /// around a month or range that keeps none.
    func entries(_ query: EntryQuery) async throws -> EntryList {
        var list = try await engine.entries(query)
        list.entries.sort { $0.date > $1.date }
        return list
    }

    func search(_ phrase: String, in query: EntryQuery = .everything) async throws -> [SearchHit] {
        try await engine.search(phrase, in: query).sorted { $0.date > $1.date }
    }

    func insights(days: Int = 30) async throws -> Insights {
        try await engine.insights(days: days)
    }

    func sharePacket(reviewID: String?, dates: [String]) async throws -> SharePacket {
        try await engine.sharePacket(reviewID: reviewID, dates: dates)
    }

    func reviews(period: String?) async throws -> [ReviewSummary] {
        try await engine.reviews(period: period)
    }

    func review(id: String) async throws -> Review {
        try await engine.review(id: id)
    }

    // MARK: Conflicts

    /// The conflict on one day, when two devices wrote it differently.
    func conflict(on date: String) -> SyncConflict? {
        conflicts.conflicts.first { $0.date == date && $0.artifact == "entry:\(date)" }
    }

    var conflictCount: Int { conflicts.conflicts.count + conflicts.focusDisputes.count }

    /// Settles a conflict, then shows the journal as it is and sends the
    /// answer to the other devices.
    func resolve(_ conflict: SyncConflict, _ answer: ConflictAnswer, requestID: RequestID) async throws {
        _ = try await engine.resolveConflict(id: conflict.id, answer: answer, requestID: requestID)
        await saved()
    }

    func resolve(_ dispute: FocusDispute, _ answer: FocusDisputeAnswer, requestID: RequestID) async throws {
        _ = try await engine.resolveFocusDispute(id: dispute.id, answer: answer, requestID: requestID)
        await saved()
    }

    /// Puts back what a suspended journal file lost, from the journal's
    /// history, and sends it on.
    func restore(_ file: String) async throws {
        guard let file = SyncFile(rawValue: file) else { return }
        _ = try await engine.recoverSync(file, action: .restore, requestID: RequestID())
        await saved()
    }

    // MARK: Focus and settings

    /// Sets the focus from today, decided on the timeline this model shows.
    func setFocus(_ text: String, requestID: RequestID) async throws {
        _ = try await engine.setFocus(text, base: try currentFocusRevision(), requestID: requestID)
        await saved()
    }

    func clearFocus(requestID: RequestID) async throws {
        _ = try await engine.clearFocus(base: try currentFocusRevision(), requestID: requestID)
        await saved()
    }

    private func currentFocusRevision() throws -> String {
        guard let focusRevision else {
            throw EngineProblem(code: "unreadable-context", message: archiveProblem ?? "The focus cannot be read.")
        }
        return focusRevision
    }

    /// Changes the questions and the anchor as one unit, decided on the
    /// settings `base` names. A journal without settings of its own is
    /// given them first, from this device's.
    func updateSettings(base: String, questions: [String], anchor: Int, requestID: RequestID) async throws {
        var base = base
        if status?.settings.initialized == false {
            let initialized = try await engine.initializeSettings(requestID: RequestID("\(requestID.value)-initialize"))
            guard initialized.value.settings.revision == base else {
                await saved()
                throw EngineProblem(
                    code: "stale-settings",
                    message: "The questions changed after they were read; nothing was written."
                )
            }
            base = initialized.value.settings.revision
        }
        _ = try await engine.updateSettings(base: base, questions: questions, anchorQuestionIndex: anchor, requestID: requestID)
        await saved()
    }

    // MARK: Practice packs

    /// The packs built into the engine, the default practice first.
    func builtinPacks() async throws -> [PracticePack] {
        try await engine.builtinPacks()
    }

    /// The pack a `.seneca-pack.json` file holds, as the engine reads it.
    /// Nothing is applied.
    func readPack(at url: URL) async throws -> PracticePack {
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        let data = try Data(contentsOf: url)
        guard data.count <= 64 * 1024, let text = String(data: data, encoding: .utf8) else {
            throw EngineProblem(code: "invalid-pack", message: "This file is not a Seneca practice pack.")
        }
        return try await engine.readPack(text)
    }

    /// Makes `pack` the journal's practice from today, decided on the
    /// settings this model shows. Entries already written are not touched.
    func applyPack(_ pack: PracticePack, requestID: RequestID) async throws {
        guard let base = status?.settings.revision else {
            throw EngineProblem(code: "journal-not-open", message: "The journal is not open.")
        }
        _ = try await engine.applyPack(pack, base: base, requestID: requestID)
        await saved()
    }

    func prepareReflection(on date: String? = nil, blank: Bool = false) async throws -> PreparedReflection {
        try await engine.prepareReflection(on: date, blank: blank)
    }

    /// The writing of one day: the form's state, kept as a draft while it
    /// is being written. Nil until the journal is open.
    func session(on date: String, blank: Bool = false) -> ReflectionSession? {
        guard let draftStore else { return nil }
        return ReflectionSession(date: date, blank: blank, engine: engine, store: draftStore) { [weak self] date in
            self?.voice?.draftDiscarded(on: date)
        }
    }

    /// Saves a reflection. `requestID` is the form's: sending the same form
    /// again after an answer was lost cannot save it twice.
    func save(_ writing: ReflectionWriting, requestID: RequestID) async throws -> SavedReflection {
        let outcome = try await engine.saveReflection(writing, requestID: requestID)
        await saved()
        return outcome.value
    }

    static func describe(_ error: Error) -> String {
        if let problem = error as? EngineProblem {
            return problem.message
        }
        return error.localizedDescription
    }
}
