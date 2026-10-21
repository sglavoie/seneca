import SenecaEngine
import SwiftUI

/// One day's reflection: the questions the journal asks, above them what the
/// engine says the day carries, and a save that is conditional on the draft.
/// What is typed is kept on this device as it is typed.
struct ReflectionForm: View {
    @Environment(JournalModel.self) private var journal
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase

    let date: String
    var blank = false
    /// The version of the day the author did not keep when settling a
    /// conflict, shown while they edit the one they kept.
    var reference: [String] = []

    @State private var session: ReflectionSession?
    @State private var confirmingClose = false
    @State private var speaking = false
    @SwiftUI.FocusState private var focused: Field?

    private enum Field: Hashable {
        case followThrough
        case answer(Int)
    }

    var body: some View {
        NavigationStack {
            Group {
                if let session {
                    form(session)
                } else {
                    ProgressView("Opening the day…")
                }
            }
            .navigationTitle(DayText.title(date))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { toolbar }
        }
        .sheet(isPresented: $speaking) {
            if let voice = journal.voice {
                RecordView(voice: voice, date: date, session: session)
            }
        }
        .task {
            guard session == nil, let opened = journal.session(on: date, blank: blank) else { return }
            session = opened
            await opened.load()
        }
        .onChange(of: scenePhase) { _, phase in
            if phase != .active { session?.persist() }
        }
        .onDisappear {
            session?.persist()
            journal.refreshDrafts()
        }
    }

    @ViewBuilder
    private func form(_ session: ReflectionSession) -> some View {
        Form {
            notices(session)
            if !reference.isEmpty {
                Section {
                    DisclosureGroup("The version you did not keep") {
                        Text(reference.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines))
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .textSelection(.enabled)
                            .accessibilityIdentifier("reference-text")
                    }
                    .accessibilityIdentifier("reference")
                }
            }
            if let draft = session.draft {
                context(draft, session)
                ForEach(Array(draft.questions.enumerated()), id: \.offset) { index, question in
                    Section {
                        TextField("Write here", text: answer(session, index), axis: .vertical)
                            .lineLimit(3...)
                            .focused($focused, equals: .answer(index))
                            .accessibilityLabel(question)
                            .accessibilityIdentifier("answer-\(index)")
                        if let stored = session.journalAnswer(index) {
                            inJournal(stored).accessibilityIdentifier("journal-now-\(index)")
                        }
                    } header: {
                        Text(question).textCase(nil).accessibilityIdentifier("question-\(index)")
                    }
                }
                kept(session)
            } else if session.phase == .loading {
                Section { ProgressView() }
            }
        }
        .scrollDismissesKeyboard(.interactively)
    }

    @ViewBuilder
    private func notices(_ session: ReflectionSession) -> some View {
        if session.conflicted {
            Section {
                Label(
                    "This day was written differently on another device. What you write here is kept on this iPhone as a draft, and can be saved once you choose a version.",
                    systemImage: "arrow.triangle.branch"
                )
                .accessibilityIdentifier("notice-conflicted")
            }
        }
        if session.notice == .changedUnderneath {
            Section {
                Label(
                    "This day changed in the journal after you began. Everything you wrote is here; saving replaces what the journal holds for these questions.",
                    systemImage: "exclamationmark.arrow.triangle.2.circlepath"
                )
                .accessibilityIdentifier("notice-changed")
            }
        } else if session.notice == .restored {
            Section {
                Label("Your draft was kept on this iPhone.", systemImage: "tray.and.arrow.up")
                    .accessibilityIdentifier("notice-restored")
            }
        }
        if let failure = session.failure, !session.conflicted {
            Section {
                Label(failure, systemImage: "exclamationmark.triangle")
                    .foregroundStyle(.red)
                    .accessibilityIdentifier("failure")
                if session.contextUnavailable {
                    Button("Write without context") { Task { await session.load(blank: true) } }
                        .accessibilityIdentifier("form-write-blank")
                    Button("Try again") { Task { await session.load() } }
                }
            }
        }
        if let problem = session.draftProblem {
            Section {
                Label(problem, systemImage: "externaldrive.badge.exclamationmark")
                    .foregroundStyle(.red)
                    .accessibilityIdentifier("draft-problem")
            }
        }
    }

    @ViewBuilder
    private func context(_ draft: ReflectionDraft, _ session: ReflectionSession) -> some View {
        let context = draft.context
        if !context.focusDispute.isEmpty {
            Section("Focus (disputed)") { Text(context.focusDispute) }
        } else if !context.focus.isEmpty {
            Section("Focus") { Text(context.focus).accessibilityIdentifier("context-focus") }
        }
        if context.showFollowThrough {
            Section {
                Text(context.priorText)
                    .foregroundStyle(.secondary)
                    .accessibilityLabel("Intention: \(context.priorText)")
                    .accessibilityIdentifier("context-intention")
                TextField("How did it go?", text: followThrough(session), axis: .vertical)
                    .lineLimit(2...)
                    .focused($focused, equals: .followThrough)
                    .accessibilityLabel("Follow-through: how did it go?")
                    .accessibilityIdentifier("follow-through")
                if let stored = session.journalFollowThrough {
                    inJournal(stored).accessibilityIdentifier("journal-now-follow-through")
                }
            } header: {
                Text(session.intentionLabel.isEmpty ? "Intention" : session.intentionLabel).textCase(nil)
            }
        }
    }

    /// What the form keeps but does not ask about: answers to questions no
    /// longer asked, and sections of the entry no question names.
    @ViewBuilder
    private func kept(_ session: ReflectionSession) -> some View {
        if !session.unasked.isEmpty {
            Section {
                ForEach(Array(session.unasked.enumerated()), id: \.offset) { _, unasked in
                    VStack(alignment: .leading, spacing: 4) {
                        Text(unasked.question).font(.subheadline.weight(.semibold))
                        Text(unasked.answer).textSelection(.enabled)
                    }
                    .accessibilityElement(children: .combine)
                }
            } header: {
                Text("No longer asked").textCase(nil)
            } footer: {
                Text("You answered these before the questions changed. They stay in the draft and are not saved to the journal.")
            }
        }
        if !session.additional.isEmpty {
            Section {
                ForEach(Array(session.additional.enumerated()), id: \.offset) { index, section in
                    VStack(alignment: .leading, spacing: 4) {
                        Text(section.heading).font(.subheadline.weight(.semibold))
                        Text(section.body).textSelection(.enabled)
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("additional-\(index)")
                }
            } header: {
                Text("Also in this entry").textCase(nil)
            } footer: {
                Text("Written outside these questions. Saving keeps it as it is.")
            }
        }
    }

    private func inJournal(_ text: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("In the journal now").font(.caption.weight(.semibold))
            Text(text).font(.callout).textSelection(.enabled)
        }
        .foregroundStyle(.secondary)
        .accessibilityElement(children: .combine)
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        ToolbarItem(placement: .cancellationAction) {
            Button("Close") {
                if session?.dirty == true {
                    confirmingClose = true
                } else {
                    dismiss()
                }
            }
            .accessibilityIdentifier("close")
            .confirmationDialog("Keep this draft?", isPresented: $confirmingClose, titleVisibility: .visible) {
                Button("Keep Draft") { dismiss() }
                Button("Discard Draft", role: .destructive) {
                    session?.discard()
                    dismiss()
                }
                Button("Keep Writing", role: .cancel) {}
            } message: {
                Text("A kept draft is offered again the next time you open this day.")
            }
        }
        ToolbarItem(placement: .confirmationAction) {
            Button("Save") { Task { await save() } }
                .disabled(session?.draft == nil || session?.saving == true || session?.conflicted == true)
                .accessibilityIdentifier("save")
        }
        ToolbarItem(placement: .primaryAction) {
            if journal.voice != nil {
                Button("Speak Instead", systemImage: "mic") {
                    focused = nil
                    session?.persist()
                    speaking = true
                }
                .accessibilityIdentifier("speak-instead")
            }
        }
        ToolbarItem(placement: .secondaryAction) {
            if let session, session.draft != nil {
                Button(
                    session.blank ? "Show Context" : "Hide Context",
                    systemImage: session.blank ? "eye" : "eye.slash"
                ) {
                    Task { await session.load(blank: !session.blank) }
                }
                .accessibilityIdentifier("toggle-context")
            }
        }
        ToolbarItemGroup(placement: .keyboard) {
            Button("Previous", systemImage: "chevron.up") { move(-1) }
                .disabled(!canMove(-1))
                .accessibilityLabel("Previous field")
                .accessibilityIdentifier("keyboard-previous")
            Button("Next", systemImage: "chevron.down") { move(1) }
                .disabled(!canMove(1))
                .accessibilityLabel("Next field")
                .accessibilityIdentifier("keyboard-next")
            Spacer()
            Button("Done") { focused = nil }
                .accessibilityLabel("Hide keyboard")
                .accessibilityIdentifier("keyboard-done")
        }
    }

    // MARK: Fields

    /// The fields in the order they appear.
    private var fields: [Field] {
        guard let draft = session?.draft else { return [] }
        let answers = draft.questions.indices.map { Field.answer($0) }
        return draft.context.showFollowThrough ? [.followThrough] + answers : answers
    }

    private func canMove(_ step: Int) -> Bool {
        guard let focused, let index = fields.firstIndex(of: focused) else { return false }
        return fields.indices.contains(index + step)
    }

    private func move(_ step: Int) {
        guard let focused, let index = fields.firstIndex(of: focused), fields.indices.contains(index + step) else { return }
        self.focused = fields[index + step]
    }

    /// The answer to one question. The questions can change under the form
    /// when it is prepared again, so an index is never trusted.
    private func answer(_ session: ReflectionSession, _ index: Int) -> Binding<String> {
        Binding(
            get: { session.answers.indices.contains(index) ? session.answers[index] : "" },
            set: { session.setAnswer(index, $0) }
        )
    }

    private func followThrough(_ session: ReflectionSession) -> Binding<String> {
        Binding(get: { session.followThrough }, set: { session.setFollowThrough($0) })
    }

    private func save() async {
        guard let session else { return }
        focused = nil
        if await session.save() {
            journal.voice?.markAnswersSaved(on: date)
            await journal.saved()
            dismiss()
        }
    }
}
