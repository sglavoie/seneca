import Foundation
import UserNotifications

/// Decides how a reminder that fires while Seneca is on screen is shown. A
/// reminder about a day the app has since learned is written, from this
/// iPhone or from an import, is not shown at all.
final class ReminderPresenter: NSObject, UNUserNotificationCenterDelegate, @unchecked Sendable {
    static let shared = ReminderPresenter()

    /// Asked on the main actor whether a reminder about a day is still due.
    @MainActor var isDue: (String) -> Bool = { _ in true }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification
    ) async -> UNNotificationPresentationOptions {
        let identifier = notification.request.identifier
        guard let date = ScheduledReminder.date(of: identifier) else { return [.banner, .list, .sound] }
        let due = await MainActor.run { isDue(date) }
        return due ? [.banner, .list, .sound] : []
    }
}
