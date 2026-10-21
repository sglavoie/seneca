import SenecaEngine
import SwiftUI

/// The first run: a journal on this iPhone, a practice to keep, reminders if
/// the author wants them, and sync if they keep the journal elsewhere too.
/// Every step can be skipped and changed later in Settings; none of it is
/// needed to write.
struct OnboardingView: View {
    @Environment(JournalModel.self) private var journal

    enum Step: Hashable {
        case practice, reminders, sync
        /// Joining a journal another device already keeps: no practice to
        /// choose, since the journal brings its own.
        case join
    }

    @State private var path: [Step] = []

    var body: some View {
        NavigationStack(path: $path) {
            welcome
                .navigationDestination(for: Step.self) { step in
                    switch step {
                    case .practice: practice
                    case .reminders: reminders
                    case .sync: sync
                    case .join: join
                    }
                }
        }
    }

    // MARK: Steps

    private var welcome: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                Text("Seneca")
                    .font(.largeTitle.bold())
                    .accessibilityAddTraits(.isHeader)
                Text("An evening journal. Each night, a few questions about the day. The next day, the intention you set, to follow through on.")
                Text("The journal is a plain Markdown file on this iPhone. Nothing leaves it unless you choose to sync it with your other devices.")
                    .foregroundStyle(.secondary)
            }
            .padding()
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .safeAreaInset(edge: .bottom) {
            VStack(spacing: 12) {
                Button {
                    path.append(.practice)
                } label: {
                    Text("Start a Journal").frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .accessibilityIdentifier("onboarding-start")
                Button("I Keep Seneca on Another Device") { path.append(.join) }
                    .accessibilityIdentifier("onboarding-join")
            }
            .padding()
        }
        .toolbar(.hidden, for: .navigationBar)
    }

    private var practice: some View {
        PacksView { _ in path.append(.reminders) }
            .navigationTitle("Choose a Practice")
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button(journal.status?.settings.practice == nil ? "Skip" : "Next") {
                        path.append(.reminders)
                    }
                    .accessibilityIdentifier("onboarding-practice-next")
                }
            }
    }

    private var reminders: some View {
        List {
            Section {
                Text("Seneca can remind you in the evening, only on a day nothing is written for yet. The reminder shows nothing of the journal.")
                if let scheduler = journal.reminders {
                    if scheduler.preferences.enabled {
                        Text(ReminderText.state(scheduler, today: journal.status?.today))
                            .accessibilityIdentifier("onboarding-reminders-state")
                    } else {
                        Button("Remind Me in the Evening", systemImage: "bell") {
                            Task {
                                await scheduler.enable()
                                path.append(.sync)
                            }
                        }
                        .accessibilityIdentifier("onboarding-reminders-enable")
                    }
                }
            } footer: {
                Text("Change the times or turn them off in Settings, Reminders.")
            }
        }
        .navigationTitle("Reminders")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button(journal.reminders?.preferences.enabled == true ? "Next" : "Not Now") {
                    path.append(.sync)
                }
                .accessibilityIdentifier("onboarding-reminders-next")
            }
        }
    }

    private var sync: some View {
        List {
            Section {
                Text("To keep the journal on your Mac or another iPhone too, choose a folder in iCloud Drive that each device exchanges through. The journal itself stays on each device.")
                NavigationLink {
                    SyncView()
                } label: {
                    Label("Set Up Sync", systemImage: "icloud")
                }
                .accessibilityIdentifier("onboarding-sync-setup")
            } footer: {
                Text("Optional. You can set it up any time in Settings, Sync.")
            }
            Section {
                finishButton
            }
        }
        .navigationTitle("Sync")
        .navigationBarTitleDisplayMode(.inline)
    }

    private var join: some View {
        List {
            Section {
                Text("Choose the exchange folder your other device uses. Its journal, with its questions and practice, comes to this iPhone.")
                NavigationLink {
                    SyncView()
                } label: {
                    Label("Choose the Exchange Folder", systemImage: "icloud")
                }
                .accessibilityIdentifier("onboarding-join-setup")
            }
            Section {
                finishButton
            }
        }
        .navigationTitle("Join a Journal")
        .navigationBarTitleDisplayMode(.inline)
    }

    private var finishButton: some View {
        Button {
            journal.finishOnboarding()
        } label: {
            Text("Start Writing").frame(maxWidth: .infinity)
        }
        .buttonStyle(.borderedProminent)
        .accessibilityIdentifier("onboarding-finish")
    }
}
