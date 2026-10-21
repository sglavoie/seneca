import SenecaEngine
import SwiftUI
import UniformTypeIdentifiers

/// How the practice in force reads on screen.
enum PracticeText {
    @MainActor
    static func summary(_ settings: SettingsState) -> String {
        guard let practice = settings.practice else {
            return "No practice pack. The journal asks its own questions."
        }
        let since = "Since \(DayText.title(practice.from))."
        return practice.questionsChanged
            ? "\(practice.pack.name). \(since) The questions were edited after it was applied."
            : "\(practice.pack.name). \(since)"
    }
}

/// The practice packs to choose from: the built-in ones, and any pack file
/// the author opens. Choosing one shows it before anything is applied.
struct PacksView: View {
    @Environment(JournalModel.self) private var journal
    /// Called once a pack is applied: onboarding moves on, settings go back.
    var applied: (PracticePack) -> Void = { _ in }

    @State private var packs: [PracticePack] = []
    @State private var failure: String?
    @State private var importing = false
    @State private var opened: PracticePack?

    private var inForce: String? { journal.status?.settings.practice?.pack.id }

    var body: some View {
        List {
            if let failure {
                Section {
                    Label(failure, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("packs-failure")
                }
            }
            Section {
                ForEach(packs) { pack in
                    NavigationLink {
                        PackDetailView(pack: pack, applied: applied)
                    } label: {
                        PackRow(pack: pack, inForce: pack.id == inForce)
                    }
                    .accessibilityIdentifier("pack-\(pack.id)")
                }
            } header: {
                Text("Practices")
            } footer: {
                Text("A practice sets the questions asked every evening and tells reviews what to listen for. Days already written keep the questions they were written with.")
            }
            Section {
                Button("Open a Pack File", systemImage: "doc.badge.plus") { importing = true }
                    .accessibilityIdentifier("packs-open-file")
            } footer: {
                Text("A .seneca-pack.json file from a practitioner, a friend, or your Mac.")
            }
        }
        .navigationTitle("Practice")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            guard packs.isEmpty else { return }
            do {
                packs = try await journal.builtinPacks()
            } catch {
                failure = JournalModel.describe(error)
            }
        }
        .fileImporter(isPresented: $importing, allowedContentTypes: [.json]) { result in
            Task {
                do {
                    opened = try await journal.readPack(at: try result.get())
                    failure = nil
                } catch {
                    failure = JournalModel.describe(error)
                }
            }
        }
        .navigationDestination(item: $opened) { pack in
            PackDetailView(pack: pack, applied: applied)
        }
    }
}

struct PackRow: View {
    let pack: PracticePack
    var inForce = false

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(pack.name)
                if inForce {
                    Spacer()
                    Text("In use").font(.caption).foregroundStyle(.secondary)
                }
            }
            if let description = pack.description {
                Text(description).font(.caption).foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// One pack, whole, before it is applied: its questions, the one carried
/// into tomorrow, its reminder time, and what reviews are told.
struct PackDetailView: View {
    @Environment(JournalModel.self) private var journal
    @Environment(\.dismiss) private var dismiss
    let pack: PracticePack
    var applied: (PracticePack) -> Void = { _ in }

    @State private var working = false
    @State private var confirming = false
    /// After applying: whether to offer the pack's reminder time.
    @State private var offeringReminder = false
    @State private var failure: String?
    @State private var requestID = RequestID()

    private var inForce: Bool {
        journal.status?.settings.practice?.pack == pack
    }

    /// Whether anything has been written under other questions, so applying
    /// needs saying what happens to it.
    private var hasWriting: Bool {
        (journal.status?.entryCount ?? 0) > 0
    }

    var body: some View {
        List {
            if let failure {
                Section {
                    Label(failure, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("pack-failure")
                }
            }
            if let description = pack.description {
                Section { Text(description) }
            }
            Section("Asked every evening") {
                ForEach(Array(pack.questions.enumerated()), id: \.offset) { index, question in
                    VStack(alignment: .leading, spacing: 4) {
                        Text(question)
                        if pack.anchorQuestionIndex == index + 1 {
                            Text("Carried into the next day as its intention")
                                .font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("pack-question-\(index)")
                }
            }
            if let time = pack.reminderTime {
                Section {
                    LabeledContent("Suggested reminder", value: ReminderText.time(time))
                } footer: {
                    Text("Reminders are set on this iPhone. Using the practice schedules nothing by itself.")
                }
            }
            if let guidance = pack.guidance {
                Section("What reviews are told") {
                    Text(guidance).font(.callout)
                }
            }
            Section {
                if inForce {
                    Label("This is the journal’s practice.", systemImage: "checkmark")
                        .accessibilityIdentifier("pack-in-force")
                } else {
                    Button("Use This Practice") {
                        if hasWriting { confirming = true } else { Task { await apply() } }
                    }
                    .disabled(working)
                    .accessibilityIdentifier("pack-apply")
                }
            }
        }
        .navigationTitle(pack.name)
        .navigationBarTitleDisplayMode(.inline)
        .confirmationDialog("Use \(pack.name)?", isPresented: $confirming, titleVisibility: .visible) {
            Button("Use It From Today") { Task { await apply() } }
                .accessibilityIdentifier("pack-apply-confirm")
        } message: {
            Text("From today the journal asks these questions on every device. Days already written keep the questions they were written with.")
        }
        .alert("Remind you at \(ReminderText.time(pack.reminderTime ?? ""))?", isPresented: $offeringReminder) {
            Button("Remind Me") {
                Task {
                    if let scheduler = journal.reminders, let time = pack.reminderTime {
                        await scheduler.setTimes([time])
                        await scheduler.enable()
                    }
                    applied(pack)
                }
            }
            .accessibilityIdentifier("pack-reminder-accept")
            Button("Not Now", role: .cancel) { applied(pack) }
                .accessibilityIdentifier("pack-reminder-decline")
        } message: {
            Text("A reminder comes only on an evening nothing is written for yet. You can change it in Reminders.")
        }
    }

    private func apply() async {
        working = true
        defer { working = false }
        do {
            try await journal.applyPack(pack, requestID: requestID)
            failure = nil
            if pack.reminderTime != nil, journal.reminders != nil {
                offeringReminder = true
            } else {
                applied(pack)
            }
        } catch {
            failure = JournalModel.describe(error)
            requestID = RequestID()
        }
    }
}
