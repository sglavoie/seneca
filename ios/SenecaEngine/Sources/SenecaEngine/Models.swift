import Foundation

// The shapes of bridge version 5, as docs/mobile/bridge.md gives them. Each
// is a view over what the engine returned: nothing here parses Markdown or
// decides what a journal means.

// MARK: Bridge and journal

public struct BridgeDescription: Codable, Equatable, Sendable {
    public var bridgeVersion: Int
    public var operations: [String]
    public var reflectionSchema: Int
    public var archiveSchema: Int
    public var settingsVersion: Int
    public var syncProtocol: Int
    public var archiveFileName: String
    public var defaultQuestions: [String]
    public var reviewPeriods: [String]
}

/// Where a journal is and what this device brings to it.
public struct JournalLocation: Sendable {
    /// The reflections file. It need not exist yet; its directory must.
    public var reflections: URL
    /// The installation's own directory, for the engine's request ledger.
    public var stateDirectory: URL
    /// This device's questions and anchor, in force until the journal has
    /// settings of its own. Nil means Seneca's defaults.
    public var questions: [String]?
    public var anchorQuestionIndex: Int?
    /// The zone the journal's days are counted in.
    public var timeZone: TimeZone
    /// Pins the journal's clock, for tests and previews. The app leaves it nil.
    public var now: Date?

    public init(
        reflections: URL,
        stateDirectory: URL,
        questions: [String]? = nil,
        anchorQuestionIndex: Int? = nil,
        timeZone: TimeZone = .current,
        now: Date? = nil
    ) {
        self.reflections = reflections
        self.stateDirectory = stateDirectory
        self.questions = questions
        self.anchorQuestionIndex = anchorQuestionIndex
        self.timeZone = timeZone
        self.now = now
    }
}

public struct JournalStatus: Codable, Equatable, Sendable {
    public var reflectionsPath: String
    public var archivePath: String
    public var today: String
    public var reflectionsExist: Bool
    public var archiveExists: Bool
    public var entryCount: Int
    public var reviewCount: Int
    /// Whether both documents would accept a write; the reasons say why not.
    public var writable: Bool
    public var reflectionsReason: String?
    public var archiveReason: String?
    public var reviewDue: String?
    public var settings: SettingsState
    public var syncEnrolled: Bool
}

// MARK: Entries

public struct EntrySummary: Codable, Equatable, Sendable, Identifiable {
    public var date: String
    public var revision: String
    public var relative: String
    public var preview: String
    /// Two devices wrote the day differently and the author has not chosen
    /// yet. This device's version is the one summarized.
    public var conflicted: Bool

    public var id: String { date }
}

/// Which days a history list or a search covers, as `seneca show` chooses
/// them. A month, a range, and on this day exclude one another; the engine
/// refuses two at once with `invalid-query`.
public struct EntryQuery: Codable, Equatable, Hashable, Sendable {
    /// The first and last day of an inclusive range; either may be open.
    public var from: String?
    public var to: String?
    /// One month, YYYY-MM.
    public var month: String?
    /// The earlier days written on today's day of a month.
    public var onThisDay: Bool?

    public init(from: String? = nil, to: String? = nil, month: String? = nil, onThisDay: Bool? = nil) {
        self.from = from
        self.to = to
        self.month = month
        self.onThisDay = onThisDay
    }

    public static let everything = EntryQuery()
    public static func month(_ month: String) -> EntryQuery { EntryQuery(month: month) }
    public static func range(from: String?, to: String?) -> EntryQuery { EntryQuery(from: from, to: to) }
    public static let onThisDay = EntryQuery(onThisDay: true)
}

public struct EntryList: Codable, Equatable, Sendable {
    public var today: String
    /// Every entry of the journal, whatever the query kept.
    public var total: Int
    /// The entries the query kept, in the order the file holds them.
    public var entries: [EntrySummary]
    /// The nearest days written on either side of a month or range, when
    /// the query names one.
    public var nearestBefore: String?
    public var nearestAfter: String?
}

public struct FollowThrough: Codable, Equatable, Sendable {
    public var sourceDate: String
    public var sourceIntention: String
    public var response: String
}

public struct EntrySection: Codable, Equatable, Sendable {
    public var heading: String
    public var body: String
    public var followThrough: FollowThrough?
}

public struct Entry: Codable, Equatable, Sendable {
    public var date: String
    public var revision: String
    public var relative: String
    /// The entry exactly as the file holds it.
    public var raw: String
    /// The entry as it is displayed: without Seneca's own markers.
    public var lines: [String]
    public var sections: [EntrySection]
    /// Another device's version of the day waits beside this one for the
    /// author to choose.
    public var conflicted: Bool
}

public struct SearchHit: Codable, Equatable, Sendable, Identifiable {
    public var date: String
    public var relative: String
    public var snippets: [String]

    public var id: String { date }
}

struct SearchResult: Codable {
    var hits: [SearchHit]
}

// MARK: Reflections

public struct ReflectionContext: Codable, Equatable, Sendable {
    public var focus: String
    public var focusDispute: String
    public var priorDate: String
    public var priorText: String
    public var priorAge: String
    public var showFollowThrough: Bool
    public var followInitial: String
}

/// The read half of a reflection. Show it, and hand it back unchanged with
/// what the author wrote: it is the state the save is conditional on.
public struct ReflectionDraft: Codable, Equatable, Sendable {
    public var date: String
    public var questions: [String]
    public var anchorQuestionIndex: Int
    /// The stored answer to each question when the day already has an entry.
    public var initial: [String]?
    public var isEdit: Bool
    public var context: ReflectionContext
    public var base: String
    public var settingsRevision: String
}

public struct UnaskedAnswer: Codable, Equatable, Sendable {
    public var question: String
    public var answer: String

    public init(question: String, answer: String) {
        self.question = question
        self.answer = answer
    }
}

/// Writing kept for a day that a save could not store.
public struct RetainedDraft: Codable, Equatable, Sendable {
    public var date: String
    public var retainedAt: String
    public var reason: String
    public var base: String
    public var settingsRevision: String
    public var answers: [String]
    public var followThrough: String
    public var unasked: [UnaskedAnswer]
}

public struct PreparedReflection: Codable, Equatable, Sendable {
    public var draft: ReflectionDraft
    public var today: String
    public var intentionLabel: String
    public var retained: RetainedDraft?
}

/// What the author wrote against a draft.
public struct ReflectionWriting: Codable, Equatable, Sendable {
    public var draft: ReflectionDraft
    /// One answer per question of the draft, in order.
    public var answers: [String]
    public var followThrough: String?

    public init(draft: ReflectionDraft, answers: [String], followThrough: String? = nil) {
        self.draft = draft
        self.answers = answers
        self.followThrough = followThrough
    }
}

public struct SavedReflection: Codable, Equatable, Sendable {
    public var date: String
    public var isEdit: Bool
    public var entry: Entry
}

struct DraftResult: Codable {
    var found: Bool
    var draft: RetainedDraft?
}

struct DatedResult: Codable {
    var date: String
}

// MARK: Settings

public struct SettingsState: Codable, Equatable, Sendable {
    /// Whether the journal has settings of its own. Until it does, these are
    /// the device's.
    public var initialized: Bool
    public var questions: [String]
    public var anchorQuestionIndex: Int
    public var revision: String
    /// The practice pack applied last, nil when none ever was.
    public var practice: PracticeState?
}

public struct SettingsOutcome: Codable, Equatable, Sendable {
    public var changed: Bool
    public var settings: SettingsState
}

// MARK: Practice packs

/// A practice pack, as its `.seneca-pack.json` file states it.
public struct PracticePack: Codable, Hashable, Sendable, Identifiable {
    public var senecaPack: Int
    public var id: String
    public var version: Int
    public var name: String
    public var description: String?
    public var questions: [String]
    public var anchorQuestionIndex: Int
    /// A suggested reminder time, HH:MM. Applying the pack schedules nothing.
    public var reminderTime: String?
    /// What reviews of the days the pack covers are told.
    public var guidance: String?
}

public struct PracticeState: Codable, Equatable, Sendable {
    /// The journal day the pack was applied on.
    public var from: String
    public var pack: PracticePack
    /// Whether the questions or the anchor were edited after the pack was
    /// applied. The pack is still the practice.
    public var questionsChanged: Bool
}

struct PackList: Codable, Sendable {
    var packs: [PracticePack]
}

struct PackResult: Codable, Sendable {
    var pack: PracticePack
}

// MARK: Follow-through insights

/// What became of one intention: the anchor answer of the day `date`.
public struct IntentionOutcome: Codable, Equatable, Sendable, Identifiable {
    public var date: String
    public var text: String
    /// answered, unanswered, not-carried, open, or untracked.
    public var status: String
    /// The days whose follow-through carried it, oldest first.
    public var carriedOn: [String]
    /// The first of them with a response; empty unless answered.
    public var answeredOn: String
    public var response: String
    public var id: String { date }
}

/// Words with the days they were written on: an intention written the same
/// way more than once, or a word that recurs in intentions.
public struct DatedWords: Codable, Equatable, Sendable, Identifiable {
    public var text: String
    public var dates: [String]
    public var id: String { text }
}

/// One focus event and how long it stood.
public struct FocusSpan: Codable, Equatable, Sendable, Identifiable {
    /// set or clear.
    public var event: String
    public var text: String
    public var origin: String
    public var from: String
    /// The day it was replaced; empty while current.
    public var until: String
    public var days: Int
    /// set or clear; empty while current.
    public var replacedBy: String
    public var replacedByText: String
    public var id: String { "\(from)-\(event)-\(until)" }
    public var current: Bool { until.isEmpty }
}

/// The follow-through report over the days `from` to `to`, inclusive.
public struct Insights: Codable, Equatable, Sendable {
    public var from: String
    public var to: String
    public var intentions: [IntentionOutcome]
    public var answered: Int
    public var unanswered: Int
    public var notCarried: Int
    public var open: Int
    public var untracked: Int
    /// The first day whose intention was ever carried; empty if none was.
    public var tracked: String
    public var repeated: [DatedWords]
    public var recurringWords: [DatedWords]
    public var focus: [FocusSpan]
    /// Why the focus is left out: the archive cannot be read.
    public var focusProblem: String
}

// MARK: Share packets

/// What the author sends to another person: a review, days, or both, as
/// Markdown written for a reader. It names no file or record and carries
/// none of Seneca's markers.
public struct SharePacket: Codable, Equatable, Sendable {
    public var title: String
    /// A name for its file, without an extension.
    public var fileName: String
    public var markdown: String
    /// The review it holds, empty when it holds none.
    public var reviewId: String
    /// The days whose entries it holds, in order.
    public var dates: [String]
}

// MARK: Reviews

public struct ReviewSummary: Codable, Equatable, Sendable, Identifiable {
    public var id: String
    public var shortId: String
    public var createdAt: String
    public var period: String
    public var start: String
    public var end: String
    /// The review this one revises, empty for a first save.
    public var revises: String
    /// Whether a later review revises this one.
    public var superseded: Bool
    /// The reviews that revise this one, oldest first.
    public var revisedBy: [String]
    public var title: String
}

public struct Review: Codable, Equatable, Sendable, Identifiable {
    public var id: String
    public var shortId: String
    public var createdAt: String
    public var period: String
    public var start: String
    public var end: String
    /// The review this one revises, empty for a first save.
    public var revises: String
    /// Whether a later review revises this one.
    public var superseded: Bool
    /// The reviews that revise this one, oldest first.
    public var revisedBy: [String]
    public var title: String
    /// The review's Markdown, as saved.
    public var body: String
}

struct ReviewList: Codable {
    var reviews: [ReviewSummary]
}

// MARK: Focus

public struct FocusCandidate: Codable, Equatable, Sendable {
    public var record: String
    public var event: String
    public var text: String
}

public struct FocusState: Codable, Equatable, Sendable {
    public var active: Bool
    public var text: String
    public var effective: String
    public var relative: String
    public var origin: String
    public var source: String
    /// Two devices changed the focus for one day independently. There is
    /// then no text: the candidates wait to be settled.
    public var disputed: Bool
    public var candidates: [FocusCandidate]
}

public struct FocusEvent: Codable, Equatable, Sendable, Identifiable {
    public var id: String
    public var event: String
    public var effective: String
    public var origin: String
    public var source: String
    public var text: String
}

public struct FocusReading: Codable, Equatable, Sendable {
    public var date: String
    public var focus: FocusState
    public var timeline: [FocusEvent]
    /// Identifies the focus timeline; a focus change is conditional on it.
    public var revision: String
}

public struct FocusChange: Codable, Equatable, Sendable {
    public var changed: Bool
    public var effective: String
    public var text: String
    public var previous: String
    public var revision: String
}

// MARK: Sync

public struct SyncPending: Codable, Equatable, Sendable {
    public var transaction: String
    public var reason: String
    public var artifact: String
    public var detail: String
}

public struct SyncSuspended: Codable, Equatable, Sendable {
    public var file: String
    public var reason: String
}

public struct SyncStatus: Codable, Equatable, Sendable {
    public var enrolled: Bool
    public var journal: String
    public var device: String
    public var name: String
    public var genesis: String
    public var sequence: Int
    public var heads: [String]
    public var pending: [SyncPending]
    public var suspended: [SyncSuspended]
    public var conflicts: [String]
    public var disputedFocus: [String]
}

public struct SyncDiagnostic: Codable, Equatable, Sendable {
    public var code: String
    public var transaction: String
    public var subject: String
    public var detail: String
    public var message: String
}

public struct SyncImported: Codable, Equatable, Sendable {
    public var applied: [String]
    public var recorded: [String]
    /// The journal files the import replaced: reflections, archive, settings.
    public var written: [String]
    public var merged: [String]
    public var conflicts: [String]
    public var pending: [SyncPending]
    public var diagnostics: [SyncDiagnostic]
}

public struct SyncPublished: Codable, Equatable, Sendable {
    public var transactions: [String]
    /// The receipt that says what this device has applied, and whether this
    /// call put it in the exchange.
    public var receipt: String
    public var receiptWritten: Bool
}

/// Another device of the journal, and how far it says it has applied this
/// device's changes.
public struct SyncPeer: Codable, Equatable, Sendable {
    public var device: String
    /// Empty until the device has published a receipt.
    public var name: String
    public var applied: Int
}

/// Where this device's own changes have got to.
public struct SyncDelivery: Codable, Equatable, Sendable {
    /// How many changes this device recorded: all of them are saved here.
    public var recorded: Int
    /// The count up to which they are all in the exchange.
    public var published: Int
    public var peers: [SyncPeer]
}

public enum SyncFile: String, Codable, Sendable {
    case reflections, archive, settings
}

public enum SyncRecoveryAction: String, Codable, Sendable {
    case restore
    case acceptDeletions = "accept-deletions"
}

public struct SyncRecovered: Codable, Equatable, Sendable {
    public var restored: [String]
    public var deleted: [String]
    public var written: [String]
}

// MARK: Conflicts

public struct ConflictVersion: Codable, Equatable, Sendable {
    public var revision: String
    public var transaction: String
    public var device: String
    public var deleted: Bool
    public var content: String
    public var lines: [String]
}

public struct SyncConflict: Codable, Equatable, Sendable, Identifiable {
    /// Names the conflict by the versions competing in it.
    public var id: String
    public var artifact: String
    public var date: String
    public var reason: String
    public var base: ConflictVersion
    public var ours: ConflictVersion
    public var theirs: [ConflictVersion]
}

public struct FocusDispute: Codable, Equatable, Sendable, Identifiable {
    public var id: String
    public var effective: String
    public var candidates: [FocusCandidate]
}

public struct Conflicts: Codable, Equatable, Sendable {
    public var conflicts: [SyncConflict]
    public var focusDisputes: [FocusDispute]

    public init(conflicts: [SyncConflict] = [], focusDisputes: [FocusDispute] = []) {
        self.conflicts = conflicts
        self.focusDisputes = focusDisputes
    }
}

/// The author's answer to a conflict.
public enum ConflictAnswer: Equatable, Sendable {
    /// Keep one of the conflict's versions, by revision ("absent" keeps a
    /// deletion that competes in it).
    case keep(revision: String)
    /// Write this instead of any of them.
    case content(String)
}

public struct SyncResolved: Codable, Equatable, Sendable {
    public var artifact: String
    public var revision: String
    public var transaction: String
    public var written: [String]
}

/// The author's answer to a disputed focus.
public enum FocusDisputeAnswer: Equatable, Sendable {
    /// Keep what one of the competing changes said, by its record.
    case record(String)
    /// Set a focus of the author's own.
    case text(String)
    /// Leave the day without a focus.
    case clear
}

/// The saved day removed by entry.delete.
public struct DeletedEntry: Codable, Equatable, Sendable {
    public var date: String
}
