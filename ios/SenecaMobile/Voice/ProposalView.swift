import SwiftUI

/// A spoken take's answers, before they go into the form: what the
/// on-device model proposes under each question, every one editable, what
/// it set aside, and the transcript one tap away. When the model cannot
/// propose anything, the transcript is shown with a way to copy it into
/// each question. Accepting fills the day's draft; saving is the form's.
struct ProposalView: View {
    @Environment(JournalModel.self) private var journal
    @Environment(\.dismiss) private var dismiss
    let voice: VoiceCapture
    let take: SpokenTake
    let date: String
    /// The form's writing, when the take was spoken from the form.
    let session: ReflectionSession?
    /// Called once the answers are in the draft.
    let filled: () -> Void

    @State private var writing: ReflectionSession?
    @State private var proposal: AnswerProposal?
    @State private var problem: String?
    @State private var showingTranscript = false

    var body: some View {
        Form {
            if let proposal {
                content(proposal)
            } else if let problem {
                Section {
                    Label(problem, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("proposal-problem")
                }
            } else {
                Section { ProgressView("Opening the day…") }
            }
        }
        .navigationTitle("Your Answers")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Fill Answers") { accept() }
                    .disabled(proposal == nil || proposal?.phase == .proposing || proposal?.changesDraft != true)
                    .accessibilityHint("Puts these answers in the day’s draft. Nothing is saved until you save the form.")
                    .accessibilityIdentifier("proposal-accept")
            }
        }
        .task { await open() }
    }

    @ViewBuilder
    private func content(_ proposal: AnswerProposal) -> some View {
        if proposal.phase == .proposing {
            Section {
                ProgressView("Sorting into your questions…").accessibilityIdentifier("proposal-proposing")
            }
        } else {
            if case .fallback(let failure) = proposal.phase {
                Section {
                    Label(failure.message, systemImage: "text.bubble")
                        .accessibilityIdentifier("proposal-fallback")
                }
                // What the author copies from, above the questions.
                transcript(proposal)
            } else {
                Section {
                    Text("Proposed on this iPhone from your words. Check and edit each answer; nothing is saved until you save the form.")
                        .font(.callout)
                        .accessibilityIdentifier("proposal-notice")
                }
            }
            ForEach(Array(proposal.questions.enumerated()), id: \.offset) { index, question in
                answer(proposal, index, question)
            }
            if proposal.phase == .proposed {
                unassigned(proposal)
                transcript(proposal)
            }
        }
    }

    @ViewBuilder
    private func answer(_ proposal: AnswerProposal, _ index: Int, _ question: String) -> some View {
        @Bindable var proposal = proposal
        Section {
            TextField("Spoken answer", text: $proposal.answers[index], axis: .vertical)
                .lineLimit(2...)
                .accessibilityLabel(question)
                .accessibilityIdentifier("proposal-answer-\(index)")
            if !proposal.typed[index].isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Already written").font(.caption.weight(.semibold))
                    Text(proposal.typed[index]).font(.callout).textSelection(.enabled)
                }
                .foregroundStyle(.secondary)
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("proposal-typed-\(index)")
            }
            if proposal.conflicts(index) {
                Picker("Both have text", selection: $proposal.merges[index]) {
                    ForEach(AnswerProposal.Merge.allCases) { Text($0.label).tag($0) }
                }
                .accessibilityIdentifier("proposal-merge-\(index)")
            }
            if case .fallback = proposal.phase, !proposal.transcript.isEmpty {
                Button("Copy Transcript Here", systemImage: "doc.on.doc") { proposal.copyTranscript(to: index) }
                    .accessibilityLabel("Copy the transcript into \(question)")
                    .accessibilityIdentifier("copy-transcript-\(index)")
            }
        } header: {
            Text(question).textCase(nil).accessibilityIdentifier("proposal-question-\(index)")
        }
    }

    @ViewBuilder
    private func unassigned(_ proposal: AnswerProposal) -> some View {
        Section {
            Text(proposal.unassigned.isEmpty ? "Nothing was set aside." : proposal.unassigned)
                .textSelection(.enabled)
                .foregroundStyle(proposal.unassigned.isEmpty ? .secondary : .primary)
                .accessibilityIdentifier("proposal-unassigned")
            if !proposal.unassigned.isEmpty {
                ForEach(Array(proposal.questions.enumerated()), id: \.offset) { index, question in
                    Button("Add to “\(question)”", systemImage: "arrow.up.doc") { proposal.copyUnassigned(to: index) }
                        .accessibilityIdentifier("copy-unassigned-\(index)")
                }
            }
        } header: {
            Text("Under no question").textCase(nil)
        } footer: {
            Text("This is not put in the draft unless you add it to an answer.")
        }
    }

    @ViewBuilder
    private func transcript(_ proposal: AnswerProposal) -> some View {
        let text = Text(proposal.transcript.isEmpty ? "Nothing was heard." : proposal.transcript)
            .textSelection(.enabled)
            .accessibilityIdentifier("proposal-transcript")
        if case .fallback = proposal.phase {
            Section("Transcript") { text }
        } else {
            Section {
                Button(
                    showingTranscript ? "Hide Transcript" : "Show Transcript",
                    systemImage: showingTranscript ? "chevron.up" : "text.quote"
                ) { showingTranscript.toggle() }
                    .accessibilityIdentifier("proposal-show-transcript")
                if showingTranscript { text }
            } footer: {
                Text("As it was heard. It stays on this iPhone with the recording.")
            }
        }
    }

    private func open() async {
        guard proposal == nil, problem == nil else { return }
        let opened: ReflectionSession
        if let session {
            opened = session
        } else {
            guard let fresh = journal.session(on: date) else {
                problem = "The journal is not open."
                return
            }
            await fresh.load()
            opened = fresh
        }
        writing = opened
        guard let draft = opened.draft else {
            problem = opened.failure ?? "The day could not be opened."
            return
        }
        let proposal = AnswerProposal(take: take, questions: draft.questions, typed: opened.answers)
        self.proposal = proposal
        await proposal.propose(with: voice.splitter)
        switch proposal.phase {
        case .proposed:
            if let seconds = proposal.seconds { voice.proposed(take, seconds: seconds) }
        case .fallback(.unavailable(let reason)):
            voice.proposed(take, seconds: nil, unavailable: reason)
        case .fallback(.notOffered):
            voice.proposed(take, seconds: nil, unavailable: "this iPhone does not run Apple Intelligence.")
        default:
            break
        }
    }

    private func accept() {
        guard let writing, let proposal else { return }
        guard proposal.accept(into: writing) else {
            problem = "The day’s questions changed while you were looking. Open the day again to speak or write."
            self.proposal = nil
            return
        }
        voice.accepted(take)
        filled()
        // Opened from a recording, this screen holds its own copy of the
        // day's writing, which goes stale once the form takes over: a draft
        // discarded there would leave it believing the answers are already
        // in. Leaving now means the next acceptance opens the day afresh.
        if session == nil { dismiss() }
    }
}
