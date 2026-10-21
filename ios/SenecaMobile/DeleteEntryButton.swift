import SenecaEngine
import SwiftUI

/// Confirms deletion against the revision shown when the author taps it.
struct DeleteEntryButton: View {
    @Environment(JournalModel.self) private var journal
    let date: String
    let revision: String
    var title = "Delete Entry"
    var onDeleted: () -> Void = {}

    @State private var confirming = false
    @State private var deleting = false
    @State private var base: String?
    @State private var requestID = RequestID()
    @State private var failure: String?

    var body: some View {
        Button(title, systemImage: "trash", role: .destructive) {
            base = revision
            requestID = RequestID()
            confirming = true
        }
        .disabled(deleting || journal.status?.writable != true || journal.conflict(on: date) != nil)
        .accessibilityIdentifier("delete-entry")
        .alert("Delete the entry for \(DayText.title(date))?", isPresented: $confirming) {
            Button("Delete Entry", role: .destructive) {
                deleting = true
                Task { await delete() }
            }
            .accessibilityIdentifier("delete-entry-confirm")
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This removes the saved reflection from your journal and synced devices. Unsaved drafts and recordings on this iPhone are kept.")
        }
        .alert("Entry Could Not Be Deleted", isPresented: Binding(
            get: { failure != nil }, set: { if !$0 { failure = nil } }
        )) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(failure ?? "")
        }
    }

    private func delete() async {
        defer { deleting = false }
        guard let base else { return }
        do {
            try await journal.deleteEntry(on: date, base: base, requestID: requestID)
            onDeleted()
        } catch {
            if let problem = error as? EngineProblem, problem.code == "stale-reflection" {
                failure = "This entry changed since it was shown. Nothing was deleted. Review the updated entry before trying again."
            } else {
                failure = JournalModel.describe(error)
            }
            try? await journal.reload()
        }
    }
}
