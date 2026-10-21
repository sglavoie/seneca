import SenecaEngine
import SwiftUI

/// What the journal asks and where it stands on this iPhone.
struct SettingsView: View {
    @Environment(JournalModel.self) private var journal
    @Environment(\.dismiss) private var dismiss

    private var syncLine: String {
        guard let sync = journal.sync, sync.folderName != nil else {
            return "Off. The journal is on this iPhone only."
        }
        if let problem = SyncText.problem(sync.phase) {
            return problem
        }
        if let delivery = sync.delivery {
            return SyncText.summary(delivery)
        }
        return "A folder is chosen. Finish setting up."
    }

    var body: some View {
        List {
            if let status = journal.status {
                Section {
                    NavigationLink {
                        PacksView()
                    } label: {
                        VStack(alignment: .leading, spacing: 4) {
                            Text("Practice")
                            Text(PracticeText.summary(status.settings))
                                .font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    .accessibilityIdentifier("practice-link")
                }
                Section {
                    ForEach(Array(status.settings.questions.enumerated()), id: \.offset) { index, question in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(question)
                            if status.settings.anchorQuestionIndex == index + 1 {
                                Text("Carried into the next day as its intention")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                        }
                        .accessibilityElement(children: .combine)
                        .accessibilityIdentifier("setting-question-\(index)")
                    }
                    NavigationLink {
                        QuestionsEditor(settings: status.settings)
                    } label: {
                        Label("Edit Questions", systemImage: "pencil")
                    }
                    .accessibilityIdentifier("edit-questions")
                } header: {
                    Text("Questions")
                } footer: {
                    Text(status.settings.initialized
                        ? "These questions belong to the journal and are shared by its devices."
                        : "These questions are this iPhone’s until the journal has settings of its own.")
                }
                Section {
                    NavigationLink {
                        SyncView()
                    } label: {
                        VStack(alignment: .leading, spacing: 4) {
                            Text("Sync")
                            Text(syncLine).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    .accessibilityIdentifier("sync-link")
                }
                Section {
                    NavigationLink {
                        RemindersView()
                    } label: {
                        VStack(alignment: .leading, spacing: 4) {
                            Text("Reminders")
                            if let reminders = journal.reminders {
                                Text(reminders.preferences.enabled ? ReminderText.state(reminders, today: status.today) : "Off")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                    .accessibilityIdentifier("reminders-link")
                    if let voice = journal.voice {
                        NavigationLink {
                            VoiceSettingsView(voice: voice)
                        } label: {
                            VStack(alignment: .leading, spacing: 4) {
                                Text("Voice")
                                Text("\(voice.language.name), \(voice.retention == .keep ? "recordings kept" : "recordings deleted after saving")")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                        }
                        .accessibilityIdentifier("voice-link")
                    }
                    NavigationLink {
                        RecoveryView()
                    } label: {
                        Text("Diagnostics and Recovery")
                    }
                    .accessibilityIdentifier("recovery-link")
                    #if DEBUG
                    // Only where this iPhone can run Apple's model at all;
                    // elsewhere the app records the device's answer by
                    // itself (ModelEvaluation.recordIneligible).
                    if LanguageModelSupport.onThisDevice {
                        NavigationLink {
                            ModelEvaluationView()
                        } label: {
                            Text("Model Evaluation")
                        }
                        .accessibilityIdentifier("evaluation-link")
                    }
                    #endif
                }
                Section("Journal") {
                    LabeledContent("Days written", value: "\(status.entryCount)")
                    LabeledContent("Reviews", value: "\(status.reviewCount)")
                    LabeledContent("Drafts on this iPhone", value: "\(journal.drafts.count)")
                    if let reason = status.reflectionsReason {
                        Label(reason, systemImage: "exclamationmark.triangle")
                    }
                    if let reason = status.archiveReason {
                        Label(reason, systemImage: "exclamationmark.triangle")
                    }
                }
            }
            Section {
                Link(destination: URL(string: "https://seneca.sglavoie.com/privacy/")!) {
                    Label("Privacy Policy", systemImage: "hand.raised")
                }
                .accessibilityIdentifier("privacy-link")
                Link(destination: URL(string: "https://seneca.sglavoie.com/support/")!) {
                    Label("Support", systemImage: "questionmark.circle")
                }
                .accessibilityIdentifier("support-link")
            } footer: {
                Text("Seneca collects no data. What you write stays on this iPhone and in the folder you choose for sync.")
            }
        }
        .navigationTitle("Settings")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Done") { dismiss() }.accessibilityIdentifier("settings-done")
            }
        }
    }
}
