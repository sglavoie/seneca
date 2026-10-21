import Foundation
import UserNotifications

/// Whether iOS lets Seneca show its reminders.
enum ReminderPermission: String, Codable, Equatable, Sendable {
    /// The author has not been asked yet.
    case notDetermined
    /// The author said no, in the prompt or in Settings. Only Settings can
    /// change it.
    case denied
    case allowed
}

/// One local notification to schedule.
struct ScheduledReminder: Equatable, Sendable {
    static let prefix = "seneca.reminder."

    let date: String
    let time: String
    let fireDate: Date
    let timeZone: TimeZone
    /// The namespace of the center it is scheduled with.
    var prefix: String = Self.prefix

    var identifier: String {
        "\(prefix)\(date).\(time.replacingOccurrences(of: ":", with: ""))"
    }

    /// The day a delivered or pending reminder is about, from its identifier:
    /// `<prefix><date>.<HHMM>`.
    static func date(of identifier: String) -> String? {
        guard identifier.hasPrefix("seneca.") else { return nil }
        let parts = identifier.split(separator: ".")
        guard parts.count >= 3 else { return nil }
        return String(parts[parts.count - 2])
    }

    /// What the notification says. Nothing from the journal: a reminder can
    /// be read on a locked screen.
    static let title = "Evening reflection"
    static let body = "Nothing is written for today yet."
}

/// The part of the notification center the reminders use. The app's is
/// iOS's own; tests give their own to say yes, say no, or watch.
protocol ReminderCenter: AnyObject, Sendable {
    /// The identifiers this center schedules under. Reminders under another
    /// prefix are not this center's to list or cancel.
    var prefix: String { get }
    func permission() async -> ReminderPermission
    /// Asks the author, once; afterwards iOS answers without asking.
    func requestPermission() async -> ReminderPermission
    /// Seneca's own pending reminders.
    func pending() async -> [String]
    func schedule(_ reminder: ScheduledReminder) async throws
    func cancel(_ identifiers: [String]) async
}

/// iOS's notification center.
final class SystemReminderCenter: ReminderCenter {
    private var center: UNUserNotificationCenter { .current() }
    let prefix: String

    init(prefix: String = ScheduledReminder.prefix) {
        self.prefix = prefix
    }

    func permission() async -> ReminderPermission {
        Self.permission(await center.notificationSettings().authorizationStatus)
    }

    func requestPermission() async -> ReminderPermission {
        _ = try? await center.requestAuthorization(options: [.alert, .sound, .badge])
        return await permission()
    }

    static func permission(_ status: UNAuthorizationStatus) -> ReminderPermission {
        switch status {
        case .notDetermined: return .notDetermined
        case .denied: return .denied
        default: return .allowed
        }
    }

    func pending() async -> [String] {
        await center.pendingNotificationRequests()
            .map(\.identifier)
            .filter { $0.hasPrefix(prefix) }
    }

    func schedule(_ reminder: ScheduledReminder) async throws {
        let content = UNMutableNotificationContent()
        content.title = ScheduledReminder.title
        content.body = ScheduledReminder.body
        content.sound = .default
        content.threadIdentifier = "seneca.reminders"
        content.userInfo = ["date": reminder.date]
        // The wall-clock time in the journal's zone: a new zone is a new plan.
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = reminder.timeZone
        let components = calendar.dateComponents([.year, .month, .day, .hour, .minute, .second], from: reminder.fireDate)
        let trigger = UNCalendarNotificationTrigger(dateMatching: components, repeats: false)
        try await center.add(UNNotificationRequest(identifier: reminder.identifier, content: content, trigger: trigger))
    }

    func cancel(_ identifiers: [String]) async {
        center.removePendingNotificationRequests(withIdentifiers: identifiers)
    }
}

#if targetEnvironment(simulator)
/// A UI test's stand-in for the author's answer to the permission prompt,
/// and its record of what was scheduled: `notifications.json` under
/// SENECA_UITEST_ROOT. Simulator builds only.
final class StagedReminderCenter: ReminderCenter, @unchecked Sendable {
    let prefix = ScheduledReminder.prefix
    private let lock = NSLock()
    private var answer: ReminderPermission
    private var requests: [String: Date] = [:]

    init(permission: ReminderPermission) {
        answer = permission
    }

    func permission() async -> ReminderPermission {
        lock.withLock { answer }
    }

    func requestPermission() async -> ReminderPermission {
        lock.withLock {
            if answer == .notDetermined { answer = .allowed }
            return answer
        }
    }

    func pending() async -> [String] {
        lock.withLock { Array(requests.keys).sorted() }
    }

    func schedule(_ reminder: ScheduledReminder) async throws {
        lock.withLock { requests[reminder.identifier] = reminder.fireDate }
        record()
    }

    func cancel(_ identifiers: [String]) async {
        lock.withLock { identifiers.forEach { requests[$0] = nil } }
        record()
    }

    private func record() {
        guard let root = ProcessInfo.processInfo.environment["SENECA_UITEST_ROOT"], !root.isEmpty else { return }
        let ids = lock.withLock { Array(requests.keys).sorted() }
        let file = URL(fileURLWithPath: root).appendingPathComponent("notifications.json")
        try? JSONEncoder().encode(ids).write(to: file, options: .atomic)
    }
}
#endif
