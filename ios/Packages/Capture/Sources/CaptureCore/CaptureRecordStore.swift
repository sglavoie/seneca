import Foundation

/// The recovery facts the engine needs, independent of the host's record schema.
public struct UnfinishedCapture: Sendable {
    public let id: EntryID
    public let captureFailure: CaptureFailure?

    public init(id: EntryID, captureFailure: CaptureFailure?) {
        self.id = id
        self.captureFailure = captureFailure
    }
}

/// Host-owned durable metadata. Implementations must commit before returning.
///
/// Context carries product associations, such as a journal day or inventory item;
/// the engine never interprets it. Snapshot is the host's own immutable result.
/// The host validates context during creation, including concurrent removals.
/// Orphans have no trustworthy context: the host decides how to adopt them.
/// Failures remain unfinished and saved records leave `unfinishedRecordings`.
/// Use one engine per recorder/store, and finish launch recovery before capture.
public protocol CaptureRecordStore: Sendable {
    associatedtype Context: Sendable
    associatedtype Snapshot: Sendable

    func createRecording(
        id: EntryID, context: Context, capturedAt: Date,
        timeZoneIdentifier: String, now: Date
    ) async throws -> Snapshot

    func createRecoveredRecording(
        id: EntryID, capturedAt: Date, timeZoneIdentifier: String, now: Date
    ) async throws -> Snapshot

    func markSaved(
        id: EntryID, audio: StoredAudio, durationSeconds: Double, now: Date
    ) async throws -> Snapshot

    func markCaptureFailure(id: EntryID, failure: CaptureFailure, now: Date) async throws
    func unfinishedRecordings() async throws -> [UnfinishedCapture]
    func contains(id: EntryID) async throws -> Bool
}
