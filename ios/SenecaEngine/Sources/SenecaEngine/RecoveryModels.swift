import Foundation

// The shapes of bridge version 4's reminders, diagnostics, recovery export,
// and leaving sync.

// MARK: Reminders

/// One evening reminder the engine planned.
public struct PlannedReminder: Codable, Equatable, Sendable, Identifiable {
    /// The journal day it is about.
    public var date: String
    /// The time of day it was planned for, HH:MM.
    public var time: String
    /// The instant it fires, RFC 3339.
    public var at: String

    public var id: String { "\(date) \(time)" }

    /// `at` as an instant.
    public var instant: Date? {
        ISO8601DateFormatter().date(from: at)
    }
}

/// What to schedule from now on: every planned time of every day this device
/// holds no entry for.
public struct ReminderPlan: Codable, Equatable, Sendable {
    public var today: String
    /// The zone the plan was made in.
    public var timeZone: String
    public var times: [String]
    /// The desktop's evening schedule, used when no times are chosen.
    public var defaults: [String]
    /// Planned days that need no reminder: this device holds their entry.
    public var complete: [String]
    public var reminders: [PlannedReminder]
}

// MARK: Diagnostics

/// A content-free report on the journal: versions, counts, and codes.
public struct Diagnostics: Codable, Equatable, Sendable {
    public struct Finding: Codable, Equatable, Sendable {
        public var code: String
        /// info, warn, or error.
        public var severity: String
        public var count: Int
    }

    public struct Versions: Codable, Equatable, Sendable {
        public var reflectionSchema: Int
        public var archiveSchema: Int
        public var settings: Int
        public var syncProtocol: Int
        public var syncState: Int
    }

    public struct Journal: Codable, Equatable, Sendable {
        public var reflectionsPresent: Bool
        public var entries: Int
        public var legacyEntries: Int
        public var parseProblems: Int
        public var archivePresent: Bool
        public var reviews: Int
        public var focusEvents: Int
        public var archiveProblems: Int
        public var ownSettings: Bool
        public var questions: Int
        public var retainedDrafts: Int
        public var interruptedCommit: Bool
    }

    public struct Exchange: Codable, Equatable, Sendable {
        public var checked: Bool
        public var found: Bool
        public var unpublished: Int
        public var unimported: Int
        public var incompatible: Int
        public var impersonated: Int
    }

    public struct Sync: Codable, Equatable, Sendable {
        public var enrolled: Bool
        public var sequence: Int
        public var heads: Int
        public var pending: [String: Int]
        public var suspended: [String]
        public var conflicts: Int
        public var disputedFocus: Int
        public var exchange: Exchange
    }

    public var format: String
    public var version: Int
    public var versions: Versions
    public var journal: Journal
    public var sync: Sync
    public var findings: [Finding]

    public func finding(_ code: String) -> Finding? {
        findings.first { $0.code == code }
    }
}

// MARK: Recovery

public struct RecoveryFile: Codable, Equatable, Sendable {
    public var path: String
    public var bytes: Int
    public var digest: String
    /// reflections, archive, settings, conflict, draft, or review-draft.
    public var kind: String
}

/// A recovery export that was written: the whole journal, with no identity.
public struct RecoveryExport: Codable, Equatable, Sendable {
    public var directory: String
    public var files: [RecoveryFile]
    public var conflictVersions: Int
    public var drafts: Int
}

/// What leaving sync did.
public struct SyncLeft: Codable, Equatable, Sendable {
    public var journal: String
    /// The identity this device had. It is retired; empty when the sync
    /// state could not be read.
    public var device: String
    public var kept: String
    public var conflicts: Int
}
