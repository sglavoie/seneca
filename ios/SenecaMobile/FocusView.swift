import SenecaEngine
import SwiftUI

/// The focus in effect, how it came to be, and the way to set or clear it.
/// A change is decided on the timeline shown, and refused when another
/// device or a review changed it meanwhile.
struct FocusView: View {
    @Environment(JournalModel.self) private var journal

    @State private var editing = false
    @State private var text = ""
    @State private var confirmingClear = false
    @State private var working = false
    @State private var failure: String?
    // One identity per change, however many times it is sent.
    @State private var requestID = RequestID()

    var body: some View {
        List {
            if let failure {
                Section {
                    Label(failure, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("focus-failure")
                }
            }
            if let problem = journal.archiveProblem {
                Section {
                    Label(problem, systemImage: "exclamationmark.triangle").accessibilityIdentifier("focus-problem")
                }
            } else if let focus = journal.focus {
                Section("In effect") {
                    if focus.disputed {
                        Text("Two devices set a different focus for the same day.")
                        ForEach(Array(focus.candidates.enumerated()), id: \.offset) { _, candidate in
                            Text(candidate.text.isEmpty ? "No focus" : candidate.text)
                        }
                        if let dispute = journal.conflicts.focusDisputes.first {
                            NavigationLink {
                                FocusDisputeView(dispute: dispute)
                            } label: {
                                Text("Choose a Focus")
                            }
                            .accessibilityIdentifier("focus-choose")
                        }
                    } else if focus.active {
                        Text(focus.text).accessibilityIdentifier("focus-text")
                        Text("Since \(focus.effective) (\(focus.relative))").font(.caption).foregroundStyle(.secondary)
                    } else {
                        Text("No focus set").foregroundStyle(.secondary).accessibilityIdentifier("focus-none")
                    }
                }
                if !focus.disputed {
                    Section {
                        Button(focus.active ? "Change Focus" : "Set Focus", systemImage: "scope") {
                            text = ""
                            editing = true
                        }
                        .disabled(working)
                        .accessibilityIdentifier("focus-set")
                        if focus.active {
                            Button("Clear Focus", systemImage: "xmark.circle", role: .destructive) { confirmingClear = true }
                                .disabled(working)
                                .accessibilityIdentifier("focus-clear")
                        }
                    } footer: {
                        Text("A focus takes effect today and is carried to your other devices.")
                    }
                }
                if !journal.focusTimeline.isEmpty {
                    Section("History") {
                        ForEach(journal.focusTimeline.reversed()) { event in
                            VStack(alignment: .leading, spacing: 4) {
                                Text(event.text.isEmpty ? "Focus cleared" : event.text)
                                Text("\(event.effective) · \(FocusText.origin(event.origin))")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                            .accessibilityElement(children: .combine)
                            .accessibilityIdentifier("focus-event-\(event.id.prefix(12))")
                        }
                    }
                }
            }
        }
        .navigationTitle("Focus")
        .navigationBarTitleDisplayMode(.inline)
        .sheet(isPresented: $editing) {
            NavigationStack {
                Form {
                    TextField("What to keep in mind", text: $text, axis: .vertical)
                        .lineLimit(2...)
                        .accessibilityLabel("Focus")
                        .accessibilityIdentifier("focus-field")
                }
                .navigationTitle("Focus")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("Cancel") { editing = false }
                    }
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Set") {
                            editing = false
                            Task { await change { try await journal.setFocus(text, requestID: $0) } }
                        }
                        .disabled(text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                        .accessibilityIdentifier("focus-save")
                    }
                }
            }
            .presentationDetents([.medium, .large])
        }
        .confirmationDialog("Clear the focus?", isPresented: $confirmingClear, titleVisibility: .visible) {
            Button("Clear", role: .destructive) {
                Task { await change { try await journal.clearFocus(requestID: $0) } }
            }
        } message: {
            Text("From today there is no focus, on every device.")
        }
        .refreshable { await journal.refresh() }
    }

    private func change(_ make: (RequestID) async throws -> Void) async {
        working = true
        defer { working = false }
        do {
            try await make(requestID)
            failure = nil
        } catch {
            failure = JournalModel.describe(error)
            try? await journal.reload()
        }
        // Whatever became of it, the next change is a new one.
        requestID = RequestID()
    }
}

enum FocusText {
    static func origin(_ origin: String) -> String {
        switch origin {
        case "manual": return "set by hand"
        case "review": return "from a review"
        default: return origin
        }
    }
}
