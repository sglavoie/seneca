import SenecaEngine
import SwiftUI

/// Today: whether the day is written, what it carries, and the way into
/// writing it, another day, the focus, and the settings.
struct TodayView: View {
    @Environment(JournalModel.self) private var journal
    @Environment(AppRouter.self) private var router
    @State private var choosingDay = false
    @State private var chosenDay: String?
    @State private var speaking: String?
    /// A day whose spoken answers went into its draft: the form opens on it
    /// once the record sheet is gone.
    @State private var filled: String?

    var body: some View {
        List {
            if let status = journal.status {
                if !status.writable, let reason = status.reflectionsReason {
                    Section("Read only") {
                        Label(reason, systemImage: "lock").accessibilityIdentifier("read-only")
                    }
                }
                today(status.today)
                if let voice = journal.voice {
                    PendingTakes(voice: voice)
                }
                conflicts
                sync
                context
                drafts(besides: status.today)
                Section {
                    Button("Write for Another Day", systemImage: "calendar.badge.plus") { choosingDay = true }
                        .accessibilityIdentifier("backfill")
                }
                if let due = status.reviewDue {
                    Section("Review") { Text(due) }
                }
            }
        }
        .navigationTitle("Today")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("Settings", systemImage: "gearshape") { router.showingSettings = true }
                    .accessibilityIdentifier("settings")
            }
        }
        .refreshable { await journal.refresh() }
        .sheet(item: Binding(get: { speaking.map(SpokenDay.init) }, set: { speaking = $0?.date })) {
            journal.refreshDrafts()
            if let day = filled {
                filled = nil
                router.write(day)
            }
        } content: { day in
            if let voice = journal.voice {
                RecordView(voice: voice, date: day.date) { filled = day.date }
            }
        }
        .sheet(isPresented: $choosingDay) {
            // The form opens once the chooser is gone: one sheet at a time.
            if let day = chosenDay {
                chosenDay = nil
                router.write(day)
            }
        } content: {
            if let today = journal.status?.today {
                DayChooser(today: today) { chosenDay = $0 }
            }
        }
    }

    @ViewBuilder
    private func today(_ day: String) -> some View {
        Section {
            if journal.draft(on: day) != nil {
                Button("Continue Today’s Draft", systemImage: "square.and.pencil") { router.write(day) }
                    .accessibilityHint("Your unsaved writing is kept on this iPhone.")
                    .accessibilityIdentifier("continue-draft")
            } else if journal.hasEntry(on: day) {
                Button("Edit Today’s Reflection", systemImage: "square.and.pencil") { router.write(day) }
                    .accessibilityIdentifier("edit-today")
            } else {
                Button("Write Today’s Reflection", systemImage: "square.and.pencil") { router.write(day) }
                    .accessibilityIdentifier("write-today")
            }
            if let entry = journal.entries.first(where: { $0.date == day }) {
                DeleteEntryButton(date: day, revision: entry.revision, title: "Delete Today’s Reflection")
            }
            if journal.voice != nil {
                Button("Speak Today’s Reflection", systemImage: "mic") { speaking = day }
                    .accessibilityHint("Records on this iPhone and transcribes it.")
                    .accessibilityIdentifier("speak-today")
            }
            if let problem = journal.contextProblem {
                Label(problem, systemImage: "exclamationmark.triangle")
                    .font(.footnote)
                    .accessibilityIdentifier("context-problem")
                Button("Write Without Context", systemImage: "eye.slash") { router.write(day, blank: true) }
                    .accessibilityIdentifier("write-blank")
            }
        } header: {
            Text(DayText.title(day)).textCase(nil).accessibilityIdentifier("today-date")
        }
    }

    /// What two devices did differently, waiting for the author to choose.
    @ViewBuilder
    private var conflicts: some View {
        if journal.conflictCount > 0 {
            Section("Needs your choice") {
                NavigationLink {
                    ConflictsView()
                } label: {
                    Label(
                        journal.conflictCount == 1
                            ? "1 change was made differently on two devices."
                            : "\(journal.conflictCount) changes were made differently on two devices.",
                        systemImage: "arrow.triangle.branch"
                    )
                }
                .accessibilityIdentifier("today-conflicts")
            }
        }
    }

    /// Where the writing has got to, once this iPhone takes part in sync, and
    /// anything about the exchange the author has to act on.
    @ViewBuilder
    private var sync: some View {
        if let sync = journal.sync, sync.folderName != nil, sync.isEnrolled || SyncText.problem(sync.phase) != nil {
            Section("Sync") {
                if let delivery = sync.delivery {
                    Label(SyncText.summary(delivery), systemImage: "arrow.triangle.2.circlepath")
                        .font(.footnote)
                        .accessibilityIdentifier("today-sync")
                }
                if let problem = SyncText.problem(sync.phase) {
                    Label(problem, systemImage: "exclamationmark.triangle")
                        .font(.footnote)
                        .accessibilityIdentifier("today-sync-problem")
                }
            }
        }
    }

    @ViewBuilder
    private var context: some View {
        Section("Focus") {
            NavigationLink {
                FocusView()
            } label: {
                if let focus = journal.focus, focus.disputed {
                    Label("Two devices set a different focus.", systemImage: "arrow.triangle.branch")
                } else if let focus = journal.focus, focus.active {
                    Text(focus.text)
                } else if journal.archiveProblem != nil {
                    Text("The focus cannot be read.").foregroundStyle(.secondary)
                } else {
                    Text("No focus set").foregroundStyle(.secondary)
                }
            }
            .accessibilityIdentifier("focus-link")
        }
        if let prepared = journal.today, prepared.draft.context.showFollowThrough {
            Section {
                Text(prepared.draft.context.priorText)
            } header: {
                Text(prepared.intentionLabel.isEmpty ? "Intention" : prepared.intentionLabel).textCase(nil)
            }
        }
    }

    @ViewBuilder
    private func drafts(besides day: String) -> some View {
        let others = journal.drafts.filter { $0.date != day }
        if !others.isEmpty {
            Section("Drafts") {
                ForEach(others) { draft in
                    Button {
                        router.write(draft.date)
                    } label: {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(DayText.title(draft.date))
                            if draft.changedUnderneath {
                                Text("The journal changed since you wrote this.")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                    .accessibilityIdentifier("draft-\(draft.date)")
                }
            }
        }
    }
}

/// A day a take is being spoken for.
struct SpokenDay: Identifiable {
    let date: String
    var id: String { date }
}

/// Picks a day up to today to write or edit.
private struct DayChooser: View {
    @Environment(\.dismiss) private var dismiss
    let today: String
    let choose: (String) -> Void
    @State private var chosen = Date()

    var body: some View {
        NavigationStack {
            Form {
                DatePicker(
                    "Day", selection: $chosen, in: ...(DayText.date(today) ?? Date()), displayedComponents: .date
                )
                .datePickerStyle(.graphical)
                .environment(\.timeZone, DayText.timeZone)
                .accessibilityIdentifier("day-picker")
            }
            .navigationTitle("Another Day")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Write") {
                        choose(DayText.day(chosen))
                        dismiss()
                    }
                    .accessibilityIdentifier("day-write")
                }
            }
            .onAppear { chosen = DayText.date(today) ?? Date() }
        }
    }
}
