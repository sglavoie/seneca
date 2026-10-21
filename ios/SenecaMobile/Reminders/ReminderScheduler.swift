import Foundation
import Observation
import SenecaEngine

/// What the author chose about reminders on this iPhone. Device-local, like
/// the desktop's LaunchAgent: never in the journal, never exchanged.
struct ReminderPreferences: Codable, Equatable, Sendable {
    /// Reminders are off until the author turns them on.
    var enabled = false
    /// The evening's times, HH:MM. Nil is the desktop's schedule.
    var times: [String]?
}

/// Keeps the preferences in one small file beside the engine's state.
struct ReminderPreferencesStore: Sendable {
    let file: URL

    func load() -> ReminderPreferences {
        guard let data = try? Data(contentsOf: file),
              let preferences = try? JSONDecoder().decode(ReminderPreferences.self, from: data)
        else { return ReminderPreferences() }
        return preferences
    }

    func save(_ preferences: ReminderPreferences) throws {
        try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        try JSONEncoder().encode(preferences).write(to: file, options: .atomic)
    }
}

/// The evening reminders on this iPhone. The engine plans them from what the
/// journal holds here; this schedules exactly that plan with iOS, and plans
/// again whenever what the plan was made from may have changed: a save, an
/// import, the app coming forward, the clock or the zone changing, or the
/// author changing the times.
///
/// A day this iPhone holds an entry for gets no reminder. A day written on
/// the Mac counts only once it has arrived here: until the phone has received
/// and imported it, its reminder is still scheduled, and iOS shows it.
///
/// Nothing here can stop writing. Without permission, or when the plan
/// cannot be made, reminders are simply not scheduled, and the screen says
/// why.
@MainActor
@Observable
final class ReminderScheduler {
    enum Reason: Equatable, Sendable {
        case launch, save, `import`, foreground, clock, preferences
    }

    enum State: Equatable {
        /// The author has not turned reminders on.
        case off
        /// On, and iOS has not asked the author for permission yet.
        case awaitingPermission
        /// On, and notifications are turned off for Seneca in Settings.
        case denied
        /// On and scheduled.
        case scheduled
        /// On, and the engine could not plan; what was scheduled stays.
        case problem(String)
    }

    private(set) var preferences: ReminderPreferences
    private(set) var state: State = .off
    private(set) var permission: ReminderPermission = .notDetermined
    /// What is scheduled now, earliest first.
    private(set) var scheduled: [PlannedReminder] = []
    /// The last plan: the times in force and the desktop's defaults.
    private(set) var plan: ReminderPlan?
    /// Every reconciliation and why, oldest first, for tests.
    private(set) var reconciliations: [Reason] = []

    /// How many days ahead to keep scheduled.
    static let days = 7

    private let engine: SenecaEngine
    private let center: any ReminderCenter
    private let store: ReminderPreferencesStore
    private var running: Task<Void, Never>?

    init(engine: SenecaEngine, center: any ReminderCenter, store: ReminderPreferencesStore) {
        self.engine = engine
        self.center = center
        self.store = store
        preferences = store.load()
    }

    /// The times in force: the author's, or the desktop's.
    var times: [String] {
        preferences.times ?? plan?.defaults ?? ["20:00", "21:00", "21:30", "22:00"]
    }

    var next: PlannedReminder? { scheduled.first }

    // MARK: The author's choices

    /// Turns reminders on, asking iOS for permission the first time.
    func enable() async {
        var changed = preferences
        changed.enabled = true
        try? store.save(changed)
        preferences = changed
        if await center.permission() == .notDetermined {
            _ = await center.requestPermission()
        }
        await reconcile(.preferences)
    }

    func disable() async {
        var changed = preferences
        changed.enabled = false
        try? store.save(changed)
        preferences = changed
        await reconcile(.preferences)
    }

    /// Sets the evening's times; nil goes back to the desktop's.
    func setTimes(_ times: [String]?) async {
        var changed = preferences
        changed.times = times.map { Array(Set($0)).sorted() }
        try? store.save(changed)
        preferences = changed
        await reconcile(.preferences)
    }

    // MARK: Reconciling

    /// Makes what iOS has pending exactly what the engine plans now. One
    /// reconciliation runs at a time; one asked for meanwhile runs after it.
    func reconcile(_ reason: Reason) async {
        let previous = running
        let task = Task { @MainActor in
            await previous?.value
            await self.once(reason)
        }
        running = task
        await task.value
    }

    private func once(_ reason: Reason) async {
        reconciliations.append(reason)
        preferences = store.load()
        permission = await center.permission()
        let pending = await center.pending()

        guard preferences.enabled else {
            await center.cancel(pending)
            scheduled = []
            state = .off
            return
        }
        guard permission == .allowed else {
            await center.cancel(pending)
            scheduled = []
            state = permission == .denied ? .denied : .awaitingPermission
            return
        }

        let plan: ReminderPlan
        do {
            plan = try await engine.planReminders(times: preferences.times, days: Self.days)
        } catch {
            // Better a reminder too many than none: what was scheduled stays.
            state = .problem(JournalModel.describe(error))
            return
        }
        self.plan = plan
        let zone = TimeZone(identifier: plan.timeZone) ?? .current
        let wanted = plan.reminders.compactMap { reminder -> ScheduledReminder? in
            guard let instant = reminder.instant else { return nil }
            return ScheduledReminder(date: reminder.date, time: reminder.time, fireDate: instant, timeZone: zone, prefix: center.prefix)
        }
        let wantedIDs = Set(wanted.map(\.identifier))
        await center.cancel(pending.filter { !wantedIDs.contains($0) })
        // Each is scheduled again, which replaces it: the same evening in a
        // new zone is another instant.
        var failed: String?
        for reminder in wanted {
            do {
                try await center.schedule(reminder)
            } catch {
                failed = error.localizedDescription
            }
        }
        scheduled = plan.reminders
        state = failed.map { .problem($0) } ?? .scheduled
    }

    /// Whether a reminder about `date` still has a reason to be shown: the
    /// app may have learned of the day's entry after it was scheduled.
    func stillDue(_ date: String) -> Bool {
        !(plan?.complete.contains(date) ?? false)
    }
}
