import SwiftUI
import UserNotifications

@main
struct SenecaMobileApp: App {
    @State private var journal = JournalModel()

    init() {
        UNUserNotificationCenter.current().delegate = ReminderPresenter.shared
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(journal)
                .task {
                    let journal = journal
                    ReminderPresenter.shared.isDue = { date in
                        journal.reminders?.stillDue(date) ?? true
                    }
                    await journal.open()
                    #if DEBUG
                    if let directory = try? AppPaths.evaluationDirectory() {
                        await ModelEvaluation.recordIneligible(directory: directory)
                    }
                    #endif
                }
        }
    }
}
