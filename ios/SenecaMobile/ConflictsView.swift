import SenecaEngine
import SwiftUI

/// What waits for the author to choose: days two devices wrote differently,
/// and days two devices gave a different focus. Nothing here is decided for
/// the author; every version stays readable until one is chosen.
struct ConflictsView: View {
    @Environment(JournalModel.self) private var journal

    var body: some View {
        List {
            if let problem = journal.conflictsProblem {
                Section {
                    Label(problem, systemImage: "exclamationmark.triangle").accessibilityIdentifier("conflicts-problem")
                }
            }
            if journal.conflictCount == 0, journal.conflictsProblem == nil {
                ContentUnavailableView(
                    "Nothing to choose", systemImage: "checkmark.circle",
                    description: Text("Every day and every focus reads the same on all your devices.")
                )
                .accessibilityIdentifier("conflicts-none")
            }
            if !journal.conflicts.conflicts.isEmpty {
                Section {
                    ForEach(journal.conflicts.conflicts) { conflict in
                        NavigationLink {
                            ConflictView(conflict: conflict)
                        } label: {
                            VStack(alignment: .leading, spacing: 4) {
                                Text(conflict.date.isEmpty ? ConflictText.artifact(conflict.artifact) : DayText.title(conflict.date))
                                Text(ConflictText.versions(conflict)).font(.caption).foregroundStyle(.secondary)
                            }
                            .accessibilityElement(children: .combine)
                        }
                        .accessibilityIdentifier("conflict-\(conflict.date.isEmpty ? conflict.artifact : conflict.date)")
                    }
                } header: {
                    Text("Days written differently")
                } footer: {
                    Text("This iPhone shows its own version until you choose. Writing for these days is kept as a draft meanwhile.")
                }
            }
            if !journal.conflicts.focusDisputes.isEmpty {
                Section("Focus set differently") {
                    ForEach(journal.conflicts.focusDisputes) { dispute in
                        NavigationLink {
                            FocusDisputeView(dispute: dispute)
                        } label: {
                            VStack(alignment: .leading, spacing: 4) {
                                Text(DayText.title(dispute.effective))
                                Text("\(dispute.candidates.count) different focuses").font(.caption).foregroundStyle(.secondary)
                            }
                            .accessibilityElement(children: .combine)
                        }
                        .accessibilityIdentifier("dispute-\(dispute.effective)")
                    }
                }
            }
        }
        .navigationTitle("Choose a Version")
        .navigationBarTitleDisplayMode(.inline)
        .refreshable { await journal.refresh() }
    }
}

enum ConflictText {
    static func artifact(_ artifact: String) -> String {
        switch artifact {
        case "settings": return "The questions"
        default: return artifact.hasPrefix("record:") ? "A saved review or focus change" : artifact
        }
    }

    static func versions(_ conflict: SyncConflict) -> String {
        let count = conflict.theirs.count + 1
        return "\(count) versions"
    }

    /// The version as it is displayed: its lines without Seneca's markers.
    static func text(_ version: ConflictVersion) -> String {
        version.lines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
    }
}

/// One conflict: the version both started from, this iPhone's, and each
/// other device's, and the ways to settle it.
struct ConflictView: View {
    @Environment(JournalModel.self) private var journal
    @Environment(AppRouter.self) private var router
    @Environment(\.dismiss) private var dismiss
    let conflict: SyncConflict

    /// The version the author asked to keep, waiting for confirmation.
    @State private var keeping: Choice?
    @State private var working = false
    @State private var failure: String?
    // One identity per answer, however many times it is sent.
    @State private var requestID = RequestID()

    struct Choice: Identifiable, Equatable {
        let version: ConflictVersion
        let label: String
        /// Open the day for editing once it is kept.
        let edit: Bool
        /// What the author did not keep, shown beside the form.
        let reference: [String]
        var id: String { version.revision + (edit ? "-edit" : "") }
    }

    var body: some View {
        List {
            if let failure {
                Section {
                    Label(failure, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("conflict-failure")
                }
            }
            Section {
                Text("Two devices changed this before either heard from the other. Choose the version to keep on every device, or start from one and edit it.")
                    .font(.callout)
            }
            Section {
                version(conflict.ours, identifier: "ours")
                Button("Keep This iPhone’s Version", systemImage: "iphone") {
                    keeping = Choice(version: conflict.ours, label: "this iPhone’s version", edit: false, reference: [])
                }
                .disabled(working)
                .accessibilityIdentifier("keep-ours")
            } header: {
                Text("This iPhone").textCase(nil)
            }
            ForEach(Array(conflict.theirs.enumerated()), id: \.offset) { index, theirs in
                Section {
                    version(theirs, identifier: "theirs-\(index)")
                    Button("Keep This Version", systemImage: "desktopcomputer") {
                        keeping = Choice(version: theirs, label: "the other device’s version", edit: false, reference: [])
                    }
                    .disabled(working)
                    .accessibilityIdentifier("keep-theirs-\(index)")
                } header: {
                    Text(conflict.theirs.count == 1 ? "The other device" : "Another device (\(index + 1))").textCase(nil)
                }
            }
            if !conflict.date.isEmpty {
                Section {
                    if !conflict.ours.deleted {
                        Button("Start From This iPhone’s Version", systemImage: "square.and.pencil") {
                            keeping = Choice(
                                version: conflict.ours, label: "this iPhone’s version", edit: true,
                                reference: conflict.theirs.filter { !$0.deleted }.flatMap(\.lines)
                            )
                        }
                        .disabled(working)
                        .accessibilityIdentifier("edit-ours")
                    }
                    ForEach(Array(conflict.theirs.enumerated()), id: \.offset) { index, theirs in
                        if !theirs.deleted {
                            Button("Start From the Other Version", systemImage: "square.and.pencil") {
                                keeping = Choice(
                                    version: theirs, label: "the other device’s version", edit: true,
                                    reference: conflict.ours.deleted ? [] : conflict.ours.lines
                                )
                            }
                            .disabled(working)
                            .accessibilityIdentifier("edit-theirs-\(index)")
                        }
                    }
                } header: {
                    Text("Edit")
                } footer: {
                    Text("The version you start from is kept on every device, then the day opens for editing with the other version beside it.")
                }
            }
            Section {
                if conflict.base.deleted {
                    Text("Neither device had written this before.").foregroundStyle(.secondary)
                        .accessibilityIdentifier("version-base")
                } else {
                    Text(ConflictText.text(conflict.base)).textSelection(.enabled).accessibilityIdentifier("version-base")
                }
            } header: {
                Text("Before both changes").textCase(nil)
            }
        }
        .navigationTitle(conflict.date.isEmpty ? ConflictText.artifact(conflict.artifact) : DayText.title(conflict.date))
        .navigationBarTitleDisplayMode(.inline)
        .confirmationDialog(
            "Keep \(keeping?.label ?? "this version")?", isPresented: confirming, titleVisibility: .visible,
            presenting: keeping
        ) { choice in
            Button(choice.edit ? "Keep and Edit" : "Keep") { Task { await keep(choice) } }
            Button("Cancel", role: .cancel) {}
        } message: { _ in
            Text("Every device will hold it. The other version is replaced.")
        }
    }

    private var confirming: Binding<Bool> {
        Binding(get: { keeping != nil }, set: { if !$0 { keeping = nil } })
    }

    @ViewBuilder
    private func version(_ version: ConflictVersion, identifier: String) -> some View {
        if version.deleted {
            Text("Deleted").foregroundStyle(.secondary).accessibilityIdentifier("version-\(identifier)")
        } else {
            Text(ConflictText.text(version))
                .textSelection(.enabled)
                .accessibilityIdentifier("version-\(identifier)")
        }
    }

    private func keep(_ choice: Choice) async {
        working = true
        defer { working = false }
        do {
            try await journal.resolve(conflict, .keep(revision: choice.version.revision), requestID: requestID)
            dismiss()
            if choice.edit {
                router.write(conflict.date, reference: choice.reference)
            }
        } catch {
            failure = JournalModel.describe(error)
            // The answer was refused and not remembered: the next one is new.
            requestID = RequestID()
            try? await journal.reload()
        }
    }
}

/// A day two devices gave a different focus.
struct FocusDisputeView: View {
    @Environment(JournalModel.self) private var journal
    @Environment(\.dismiss) private var dismiss
    let dispute: FocusDispute

    @State private var text = ""
    @State private var working = false
    @State private var failure: String?
    @State private var requestID = RequestID()

    var body: some View {
        Form {
            if let failure {
                Section {
                    Label(failure, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("dispute-failure")
                }
            }
            Section {
                ForEach(Array(dispute.candidates.enumerated()), id: \.offset) { index, candidate in
                    VStack(alignment: .leading, spacing: 8) {
                        Text(candidate.text.isEmpty ? "No focus" : candidate.text)
                        Button("Keep This Focus") { Task { await settle(.record(candidate.record)) } }
                            .disabled(working)
                            .accessibilityIdentifier("keep-focus-\(index)")
                    }
                }
            } header: {
                Text("Set on \(DayText.title(dispute.effective))").textCase(nil)
            }
            Section("Something else") {
                TextField("Another focus", text: $text, axis: .vertical)
                    .accessibilityIdentifier("dispute-text")
                Button("Set This Focus") { Task { await settle(.text(text)) } }
                    .disabled(working || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    .accessibilityIdentifier("dispute-set")
                Button("Leave Without a Focus", role: .destructive) { Task { await settle(.clear) } }
                    .disabled(working)
                    .accessibilityIdentifier("dispute-clear")
            }
        }
        .navigationTitle("Focus")
        .navigationBarTitleDisplayMode(.inline)
    }

    private func settle(_ answer: FocusDisputeAnswer) async {
        working = true
        defer { working = false }
        do {
            try await journal.resolve(dispute, answer, requestID: requestID)
            dismiss()
        } catch {
            failure = JournalModel.describe(error)
            requestID = RequestID()
            try? await journal.reload()
        }
    }
}
