import SenecaEngine
import SwiftUI

/// The journal's questions and the one whose answer is carried into the next
/// day, edited as one unit. They belong to the journal, so a change reaches
/// every device; the engine refuses it when the questions changed on another
/// device after they were read here.
struct QuestionsEditor: View {
    @Environment(JournalModel.self) private var journal
    @Environment(\.dismiss) private var dismiss
    let settings: SettingsState

    private struct Question: Identifiable, Equatable {
        let id = UUID()
        var text: String
    }

    @State private var questions: [Question] = []
    /// The question carried into the next day, by identity, so it stays the
    /// same question when the list is reordered. Nil carries none.
    @State private var anchor: UUID?
    @State private var base = ""
    @State private var working = false
    @State private var failure: String?
    @State private var requestID = RequestID()

    var body: some View {
        Form {
            if let failure {
                Section {
                    Label(failure, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("questions-failure")
                }
            }
            Section {
                ForEach($questions) { $question in
                    TextField("Question", text: $question.text, axis: .vertical)
                        .accessibilityLabel("Question \((questions.firstIndex { $0.id == question.id } ?? 0) + 1)")
                        .accessibilityIdentifier("question-field-\(questions.firstIndex { $0.id == question.id } ?? 0)")
                }
                .onDelete { questions.remove(atOffsets: $0) }
                .onMove { questions.move(fromOffsets: $0, toOffset: $1) }
                Button("Add Question", systemImage: "plus") {
                    questions.append(Question(text: ""))
                }
                .accessibilityIdentifier("add-question")
            } header: {
                Text("Asked every day, in this order")
            }
            Section {
                Picker("Carried into tomorrow", selection: $anchor) {
                    Text("None").tag(UUID?.none)
                    ForEach(questions) { question in
                        Text(question.text.isEmpty ? "Untitled question" : question.text).tag(Optional(question.id))
                    }
                }
                .accessibilityIdentifier("anchor-picker")
            } footer: {
                Text("The answer to this question is shown the next day as the intention to follow through on.")
            }
        }
        .navigationTitle("Questions")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Save") { Task { await save() } }
                    .disabled(working || questions.isEmpty)
                    .accessibilityIdentifier("questions-save")
            }
            ToolbarItem(placement: .bottomBar) {
                EditButton().accessibilityIdentifier("questions-reorder")
            }
        }
        .onAppear {
            guard base.isEmpty else { return }
            show(settings)
        }
    }

    private func show(_ settings: SettingsState) {
        questions = settings.questions.map { Question(text: $0) }
        let index = settings.anchorQuestionIndex - 1
        anchor = questions.indices.contains(index) ? questions[index].id : nil
        base = settings.revision
    }

    private func save() async {
        working = true
        defer { working = false }
        let texts = questions.map { $0.text.trimmingCharacters(in: .whitespacesAndNewlines) }
        let position = questions.firstIndex { $0.id == anchor }.map { $0 + 1 } ?? 0
        do {
            try await journal.updateSettings(base: base, questions: texts, anchor: position, requestID: requestID)
            dismiss()
        } catch {
            failure = JournalModel.describe(error)
            requestID = RequestID()
            if (error as? EngineProblem)?.code == "stale-settings", let current = journal.status?.settings {
                // Show what the journal holds now; the author decides again.
                try? await journal.reload()
                show(journal.status?.settings ?? current)
            }
        }
    }
}
