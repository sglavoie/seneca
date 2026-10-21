import Foundation
import Observation
import SenecaEngine

/// The writing of one day. It asks the engine what the day holds, lines up
/// any draft kept for it, keeps the writing on this device as it changes,
/// and saves it conditionally. The draft is cleared only when the journal
/// took the writing or the author discarded it.
@MainActor
@Observable
final class ReflectionSession {
    enum Phase: Equatable {
        case loading
        /// The form is shown and can be written in.
        case writing
        /// The day could not be prepared and nothing was kept for it.
        case unavailable
    }

    /// What the form says above the questions about where its text came from.
    enum Notice: Equatable {
        /// Writing kept on this device was put back.
        case restored
        /// The day or the questions changed in the journal after the writing
        /// began. The writing is intact; the journal's version is shown too.
        case changedUnderneath
    }

    let date: String

    private(set) var phase: Phase = .loading
    /// The engine's draft the form is based on.
    private(set) var draft: ReflectionDraft?
    private(set) var intentionLabel = ""
    private(set) var answers: [String] = []
    private(set) var followThrough = ""
    /// Answers kept from questions the journal no longer asks.
    private(set) var unasked: [UnaskedAnswer] = []
    /// Sections of the stored entry no question names. Saving keeps them.
    private(set) var additional: [EntrySection] = []
    private(set) var notice: Notice?
    /// Why the last prepare or save did not happen.
    private(set) var failure: String?
    /// Why the writing could not be kept on this device, when it could not.
    private(set) var draftProblem: String?
    /// Whether the form hides the focus and the prior intention.
    private(set) var blank: Bool
    /// Whether there is writing here the journal does not hold.
    private(set) var dirty = false
    private(set) var saving = false
    /// Two devices wrote this day differently and the author has not chosen
    /// between them. The writing stays a draft on this iPhone until then.
    private(set) var conflicted = false

    private let engine: SenecaEngine
    private let store: DraftStore
    /// Told the day once its draft is thrown away, so spoken answers
    /// accepted into it are no longer taken for answers about to be saved.
    private let discarded: @MainActor (String) -> Void
    // One identity for this writing's save, however many times it is sent.
    private var requestID = RequestID()
    private var changedUnderneath = false
    private var reason: String?

    init(
        date: String, blank: Bool = false, engine: SenecaEngine, store: DraftStore,
        discarded: @escaping @MainActor (String) -> Void = { _ in }
    ) {
        self.date = date
        self.blank = blank
        self.engine = engine
        self.store = store
        self.discarded = discarded
    }

    /// What the journal holds for each question, when it differs from what
    /// is typed after the day changed underneath.
    func journalAnswer(_ index: Int) -> String? {
        guard changedUnderneath, let initial = draft?.initial, initial.indices.contains(index),
              answers.indices.contains(index), !initial[index].isEmpty, initial[index] != answers[index]
        else { return nil }
        return initial[index]
    }

    /// The journal's follow-through response, under the same condition.
    var journalFollowThrough: String? {
        guard changedUnderneath, let context = draft?.context, context.showFollowThrough,
              !context.followInitial.isEmpty, context.followInitial != followThrough
        else { return nil }
        return context.followInitial
    }

    /// Whether preparing the day with its context failed, so writing
    /// without context is worth offering.
    private(set) var contextUnavailable = false

    // MARK: Loading

    /// Prepares the day and puts back what was kept for it. `blank` changes
    /// whether the context is shown; nil keeps the choice already made.
    func load(blank choice: Bool? = nil) async {
        if dirty {
            // Typed text goes through the store, so it is lined up with what
            // the journal holds now exactly as a relaunch would.
            persist()
        }
        let kept: StoredDraft?
        do {
            kept = try store.read(date)
            draftProblem = nil
        } catch {
            kept = nil
            draftProblem = "The draft kept for this day could not be read: \(error.localizedDescription)"
        }
        if let choice {
            blank = choice
        } else if let kept {
            blank = kept.blank
        }
        do {
            let prepared = try await engine.prepareReflection(on: date, blank: blank)
            failure = nil
            contextUnavailable = false
            await adopt(prepared, kept: kept)
            await checkConflict()
        } catch {
            failure = JournalModel.describe(error)
            contextUnavailable = !blank
            if draft == nil, let kept {
                // The journal cannot be read now; the writing still can.
                show(kept)
            }
            phase = draft == nil ? .unavailable : .writing
        }
    }

    /// Reads whether the day waits for the author to choose a version. A
    /// journal that does not sync has nothing to choose between.
    private func checkConflict() async {
        guard let listed = try? await engine.conflicts() else { return }
        conflicted = listed.conflicts.contains { $0.artifact == "entry:\(date)" }
    }

    private func adopt(_ prepared: PreparedReflection, kept: StoredDraft?) async {
        let fresh = prepared.draft
        let stored = fresh.initial ?? Array(repeating: "", count: fresh.questions.count)
        draft = fresh
        intentionLabel = prepared.intentionLabel
        additional = await additionalSections(of: fresh)

        if let kept {
            let lined = Self.line(kept, upWith: fresh.questions)
            let based = Self.keepingFollowThrough(of: kept, over: fresh)
            let follow = fresh.context.showFollowThrough ? kept.followThrough : ""
            // A response held to its own intention is not in the journal,
            // whatever the journal shows or hides now.
            let sameFollow = !based.held
                && (!fresh.context.showFollowThrough || follow == fresh.context.followInitial)
            if lined.answers == stored && lined.unasked.isEmpty && sameFollow {
                // The journal already holds this writing: the save landed and
                // the app stopped before the draft was cleared.
                try? store.discard(date)
                start(from: fresh, stored: stored)
                return
            }
            draft = based.draft
            if based.held {
                intentionLabel = "Intention \(based.draft.context.priorDate)"
            }
            answers = lined.answers
            followThrough = kept.followThrough
            unasked = lined.unasked
            reason = kept.reason
            changedUnderneath = kept.changedUnderneath
                || kept.draft.base != fresh.base
                || kept.draft.settingsRevision != fresh.settingsRevision
                || based.held
            notice = changedUnderneath ? .changedUnderneath : .restored
            dirty = true
            // Keep the draft on what the journal holds now, flagged, so the
            // next launch says the same thing.
            persist()
        } else if let retained = prepared.retained {
            // Writing the engine kept after a save it refused.
            answers = retained.answers
            followThrough = retained.followThrough
            unasked = retained.unasked
            reason = retained.reason
            changedUnderneath = retained.base != fresh.base || retained.settingsRevision != fresh.settingsRevision
            notice = changedUnderneath ? .changedUnderneath : .restored
            dirty = true
            persist()
        } else {
            start(from: fresh, stored: stored)
        }
        phase = .writing
    }

    private func start(from fresh: ReflectionDraft, stored: [String]) {
        answers = stored
        followThrough = fresh.context.followInitial
        unasked = []
        reason = nil
        changedUnderneath = false
        notice = nil
        dirty = false
        phase = .writing
    }

    /// A follow-through response answers the intention the author saw when
    /// writing it, and is saved against that intention. When the journal now
    /// offers another one, or none, the fresh draft carries the kept source
    /// date and intention instead, so the response is neither moved onto an
    /// intention it was not written to nor hidden. `held` says it did.
    static func keepingFollowThrough(of kept: StoredDraft, over fresh: ReflectionDraft) -> (draft: ReflectionDraft, held: Bool) {
        let was = kept.draft.context
        let now = fresh.context
        guard was.showFollowThrough, !was.priorDate.isEmpty, !kept.followThrough.isEmpty,
              !(now.showFollowThrough && now.priorDate == was.priorDate && now.priorText == was.priorText)
        else { return (fresh, false) }
        var draft = fresh
        draft.context.showFollowThrough = true
        draft.context.priorDate = was.priorDate
        draft.context.priorText = was.priorText
        // How long ago it was is no longer what it said; the date stays true.
        draft.context.priorAge = ""
        return (draft, true)
    }

    /// Shows a kept draft as it was written, when the day cannot be prepared.
    private func show(_ kept: StoredDraft) {
        draft = kept.draft
        intentionLabel = ""
        answers = kept.answers
        followThrough = kept.followThrough
        unasked = kept.unasked
        reason = kept.reason
        changedUnderneath = kept.changedUnderneath
        notice = .restored
        dirty = true
    }

    /// Lines kept answers up with the questions asked now, by their text, so
    /// a draft survives the questions being reordered or reworded. Answers
    /// with no question left are returned apart, never dropped, and return
    /// to their question when it is asked again.
    static func line(_ kept: StoredDraft, upWith questions: [String]) -> (answers: [String], unasked: [UnaskedAnswer]) {
        var byQuestion: [String: String] = [:]
        for aside in kept.unasked {
            byQuestion[aside.question] = aside.answer
        }
        for (question, answer) in zip(kept.draft.questions, kept.answers) where !answer.isEmpty || byQuestion[question] == nil {
            byQuestion[question] = answer
        }
        let answers = questions.map { byQuestion[$0] ?? "" }
        var unasked = kept.unasked.filter { !questions.contains($0.question) }
        for (question, answer) in zip(kept.draft.questions, kept.answers)
        where !questions.contains(question) && !answer.isEmpty && !unasked.contains(where: { $0.question == question }) {
            unasked.append(UnaskedAnswer(question: question, answer: answer))
        }
        return (answers, unasked)
    }

    private func additionalSections(of draft: ReflectionDraft) async -> [EntrySection] {
        guard draft.isEdit, let entry = try? await engine.entry(on: draft.date) else { return [] }
        return entry.sections.filter { $0.followThrough == nil && !draft.questions.contains($0.heading) }
    }

    // MARK: Writing

    func setAnswer(_ index: Int, _ text: String) {
        guard answers.indices.contains(index), answers[index] != text else { return }
        answers[index] = text
        edited()
    }

    func setFollowThrough(_ text: String) {
        guard followThrough != text else { return }
        followThrough = text
        edited()
    }

    private func edited() {
        dirty = true
        persist()
    }

    /// Writes the draft now. Called on every change, and again when the app
    /// leaves the foreground or the form goes away.
    func persist() {
        guard dirty, let draft else { return }
        do {
            try store.write(StoredDraft(
                draft: draft, answers: answers, followThrough: followThrough, unasked: unasked,
                blank: blank, changedUnderneath: changedUnderneath, reason: reason, updatedAt: Date()
            ))
            draftProblem = nil
        } catch {
            draftProblem = "This writing could not be kept on this device: \(error.localizedDescription)"
        }
    }

    /// Throws the writing away, at the author's word.
    func discard() {
        do {
            try store.discard(date)
            dirty = false
            discarded(date)
        } catch {
            draftProblem = "The draft could not be removed: \(error.localizedDescription)"
        }
    }

    // MARK: Saving

    /// Saves the writing. True when the journal holds it; on any refusal the
    /// draft stays, with every typed word.
    func save() async -> Bool {
        guard let draft, !saving else { return false }
        saving = true
        defer { saving = false }
        // Kept before it is sent, whatever becomes of the save.
        dirty = true
        persist()
        let writing = ReflectionWriting(
            draft: draft, answers: answers,
            followThrough: draft.context.showFollowThrough ? followThrough : nil
        )
        do {
            // A warning means the write landed: finish as on success.
            _ = try await engine.saveReflection(writing, requestID: requestID)
            try? store.discard(date)
            dirty = false
            return true
        } catch let problem as EngineProblem where problem.code == "conflicted" {
            // The day waits for a choice between two devices' versions. The
            // writing stays here, as a draft, with the same identity: the
            // refusal wrote nothing and is not remembered.
            reason = problem.message
            conflicted = true
            persist()
            failure = problem.message
            return false
        } catch let problem as EngineProblem where problem.code.hasPrefix("stale-") {
            // The day or the questions changed underneath: keep the writing
            // and show the form again over what the journal holds now.
            reason = problem.message
            changedUnderneath = true
            persist()
            requestID = RequestID()
            await load()
            failure = problem.message
            return false
        } catch {
            reason = JournalModel.describe(error)
            persist()
            failure = reason
            return false
        }
    }
}
