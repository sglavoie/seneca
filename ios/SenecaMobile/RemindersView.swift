import SenecaEngine
import SwiftUI
import UIKit

/// How the reminders' times and state read on screen.
enum ReminderText {
    /// "20:00" as this iPhone writes times of day.
    static func time(_ value: String) -> String {
        let parts = value.split(separator: ":").compactMap { Int($0) }
        guard parts.count == 2,
              let date = Calendar.current.date(from: DateComponents(hour: parts[0], minute: parts[1]))
        else { return value }
        return date.formatted(date: .omitted, time: .shortened)
    }

    static func hhmm(_ date: Date) -> String {
        let parts = Calendar.current.dateComponents([.hour, .minute], from: date)
        return String(format: "%02d:%02d", parts.hour ?? 0, parts.minute ?? 0)
    }

    @MainActor
    static func state(_ scheduler: ReminderScheduler, today: String?) -> String {
        switch scheduler.state {
        case .off:
            return "Off. Seneca does not remind you."
        case .awaitingPermission:
            return "Seneca needs your permission to show notifications."
        case .denied:
            return "Notifications are turned off for Seneca in Settings, so no reminder can appear. Writing is not affected."
        case .problem(let message):
            return "The reminders could not be planned again: \(message) The ones already scheduled stay."
        case .scheduled:
            guard let next = scheduler.next else {
                return "Nothing to remind: every evening ahead is already written."
            }
            let day = next.date == today ? "Tonight" : DayText.title(next.date)
            return "\(day) at \(time(next.time)), unless you write first."
        }
    }

    /// What a save on another device can and cannot do about a reminder
    /// already scheduled here.
    static let delivery = "Reminders are scheduled on this iPhone. A reflection written on your Mac cancels this iPhone’s reminder only after the iPhone has received it through the exchange folder and Seneca has opened since. Until then the reminder can still appear."
}

/// The evening reminders on this iPhone: off unless the author turns them on,
/// at the desktop's times unless they choose others.
struct RemindersView: View {
    @Environment(JournalModel.self) private var journal
    @State private var adding = Calendar.current.date(from: DateComponents(hour: 20, minute: 30)) ?? Date()

    var body: some View {
        List {
            if let scheduler = journal.reminders {
                Section {
                    Toggle("Evening Reminders", isOn: Binding(
                        get: { scheduler.preferences.enabled },
                        set: { on in Task { on ? await scheduler.enable() : await scheduler.disable() } }
                    ))
                    .accessibilityIdentifier("reminders-toggle")
                    Text(ReminderText.state(scheduler, today: journal.status?.today))
                        .accessibilityIdentifier("reminders-state")
                    if scheduler.state == .denied {
                        Button("Open Settings", systemImage: "gear") {
                            if let url = URL(string: UIApplication.openNotificationSettingsURLString) {
                                UIApplication.shared.open(url)
                            }
                        }
                        .accessibilityIdentifier("reminders-open-settings")
                    } else if scheduler.state == .awaitingPermission {
                        Button("Allow Notifications", systemImage: "bell.badge") {
                            Task { await scheduler.enable() }
                        }
                        .accessibilityIdentifier("reminders-allow")
                    }
                } footer: {
                    Text("A reminder comes only on an evening this iPhone holds nothing written for.")
                }

                if scheduler.preferences.enabled {
                    Section {
                        ForEach(scheduler.times, id: \.self) { time in
                            Text(ReminderText.time(time))
                                .accessibilityIdentifier("reminders-time-\(time.replacingOccurrences(of: ":", with: ""))")
                        }
                        .onDelete { offsets in
                            var times = scheduler.times
                            times.remove(atOffsets: offsets)
                            guard !times.isEmpty else { return }
                            Task { await scheduler.setTimes(times) }
                        }
                        DatePicker("Add a Time", selection: $adding, displayedComponents: .hourAndMinute)
                            .accessibilityIdentifier("reminders-new-time")
                        Button("Add", systemImage: "plus") {
                            Task { await scheduler.setTimes(scheduler.times + [ReminderText.hhmm(adding)]) }
                        }
                        .accessibilityIdentifier("reminders-add")
                        .disabled(scheduler.times.contains(ReminderText.hhmm(adding)) || scheduler.times.count >= 8)
                        if scheduler.preferences.times != nil {
                            Button("Use the Usual Times", systemImage: "arrow.uturn.backward") {
                                Task { await scheduler.setTimes(nil) }
                            }
                            .accessibilityIdentifier("reminders-defaults")
                        }
                    } header: {
                        Text("Times")
                    } footer: {
                        Text("The usual times are your Mac’s: 8:00, 9:00, 9:30, and 10:00 in the evening. Swipe a time to remove it.")
                    }
                }

                Section {
                    Text(ReminderText.delivery)
                        .font(.footnote)
                        .accessibilityIdentifier("reminders-delivery-note")
                } header: {
                    Text("Writing on Your Mac")
                }
            }
        }
        .navigationTitle("Reminders")
        .navigationBarTitleDisplayMode(.inline)
        .task { await journal.reminders?.reconcile(.foreground) }
    }
}
