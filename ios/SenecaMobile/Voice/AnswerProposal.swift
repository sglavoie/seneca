import Foundation
import Observation

/// A transcript on its way into the form: the answers the on-device model
/// proposes for each question, editable, and what it set aside. When no
/// answers can be proposed, the fields start empty and the transcript can
/// be copied into any of them. Nothing reaches the draft until the author
/// accepts, and typed text is kept unless the author chooses otherwise.
@MainActor
@Observable
final class AnswerProposal {
    enum Phase: Equatable {
        case proposing
        /// The model's answers are in the fields, for the author to edit.
        case proposed
        /// No answers were proposed; the transcript is shown to copy from.
        case fallback(SplitFailure)
    }

    /// What becomes of an answer already typed when a different one is
    /// proposed for the same question.
    enum Merge: String, CaseIterable, Identifiable, Sendable {
        /// The typed answer stays; the proposal is dropped.
        case keep
        /// The proposal takes the typed answer's place.
        case replace
        /// The proposal is added below the typed answer.
        case append

        var id: String { rawValue }

        var label: String {
            switch self {
            case .keep: return "Keep What I Wrote"
            case .replace: return "Use the Spoken Answer"
            case .append: return "Add Below What I Wrote"
            }
        }
    }

    let take: SpokenTake
    let questions: [String]
    /// The draft's answers when the proposal was opened.
    let typed: [String]

    private(set) var phase: Phase = .proposing
    /// One answer per question, as the author has edited them.
    var answers: [String]
    var unassigned = ""
    /// One choice per question; it matters only where both have text.
    var merges: [Merge]
    /// How long the model took to answer, once it has.
    private(set) var seconds: Double?

    init(take: SpokenTake, questions: [String], typed: [String]) {
        self.take = take
        self.questions = questions
        self.typed = questions.indices.map { typed.indices.contains($0) ? typed[$0] : "" }
        answers = Array(repeating: "", count: questions.count)
        merges = Array(repeating: .keep, count: questions.count)
    }

    var transcript: String { take.transcript ?? "" }

    /// Asks the model for answers. Anything but a well-shaped proposal is a
    /// fallback, said so, with the transcript to copy from.
    func propose(with splitter: any AnswerSplitting) async {
        phase = .proposing
        seconds = nil
        let text = transcript.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else {
            phase = .fallback(.failed("nothing was heard."))
            return
        }
        guard splitter.offered else {
            phase = .fallback(.notOffered)
            return
        }
        do {
            let started = ContinuousClock.now
            let proposed = try await splitter.split(text, questions: questions, locale: take.locale)
            let elapsed = ContinuousClock.now - started
            seconds = Double(elapsed.components.seconds) + Double(elapsed.components.attoseconds) / 1e18
            guard proposed.answers.count == questions.count else {
                phase = .fallback(.failed("the model's answers did not match the questions."))
                return
            }
            answers = proposed.answers
            unassigned = proposed.unassigned
            phase = .proposed
        } catch {
            phase = .fallback(FoundationModelsSplitter.classify(error))
        }
    }

    /// Puts the whole transcript into one question's field, below anything
    /// already there, to be trimmed by hand.
    func copyTranscript(to index: Int) {
        copy(transcript, to: index)
    }

    /// Moves what the model set aside into one question's field.
    func copyUnassigned(to index: Int) {
        copy(unassigned, to: index)
    }

    private func copy(_ text: String, to index: Int) {
        let text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard answers.indices.contains(index), !text.isEmpty else { return }
        answers[index] = Self.joined(answers[index], text)
    }

    /// Whether the author has to choose between typed text and a proposal.
    func conflicts(_ index: Int) -> Bool {
        guard answers.indices.contains(index) else { return false }
        return Self.conflict(typed: typed[index], proposed: answers[index])
    }

    /// The answers the form gets on acceptance.
    var merged: [String] {
        Self.merge(typed: typed, proposed: answers, choices: merges)
    }

    /// Whether accepting would change anything in the draft.
    var changesDraft: Bool { merged != typed }

    /// Fills the form through the session, so the draft is kept as typed
    /// text is. False when the day's questions changed since the proposal
    /// was opened; nothing is filled then.
    @discardableResult
    func accept(into session: ReflectionSession) -> Bool {
        guard session.draft?.questions == questions else { return false }
        for (index, answer) in merged.enumerated() {
            session.setAnswer(index, answer)
        }
        return true
    }

    // MARK: Merge rules

    static func conflict(typed: String, proposed: String) -> Bool {
        let typed = typed.trimmingCharacters(in: .whitespacesAndNewlines)
        let proposed = proposed.trimmingCharacters(in: .whitespacesAndNewlines)
        return !typed.isEmpty && !proposed.isEmpty && typed != proposed
    }

    /// For each question: an empty proposal leaves the typed answer as it
    /// is; an empty typed answer takes the proposal; where both have text,
    /// the author's choice decides, and typed text is kept by default.
    static func merge(typed: [String], proposed: [String], choices: [Merge]) -> [String] {
        typed.indices.map { index in
            let mine = typed[index]
            let spoken = proposed.indices.contains(index)
                ? proposed[index].trimmingCharacters(in: .whitespacesAndNewlines) : ""
            if spoken.isEmpty { return mine }
            if mine.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return spoken }
            if !conflict(typed: mine, proposed: spoken) { return mine }
            switch choices.indices.contains(index) ? choices[index] : .keep {
            case .keep: return mine
            case .replace: return spoken
            case .append: return joined(mine, spoken)
            }
        }
    }

    private static func joined(_ first: String, _ second: String) -> String {
        let first = first.replacing(/\s+$/, with: "")
        return first.isEmpty ? second : "\(first)\n\(second)"
    }
}
