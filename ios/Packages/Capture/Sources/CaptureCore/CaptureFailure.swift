import Foundation

/// Why a capture stopped short of `saved`.
///
/// This is a reason attached to an unfinished capture record,
/// not a parallel state machine: the durable truth stays "this recording never
/// completed", and the reason tells the user and the recovery pass what to do
/// about it. Every case is recoverable in the sense that no audio is ever
/// discarded because of it.
public enum CaptureFailure: String, Codable, Hashable, Sendable, CaseIterable {
    case microphoneDenied
    case audioSessionUnavailable
    case insufficientStorage
    case interrupted
    case finalizationFailed
    case audioMissing
    /// The recording ended while the phone was locked, and its file cannot be
    /// read until it is unlocked. Not a loss: the audio is finished
    /// after unlock.
    case awaitingUnlock

    /// Whether launching again can plausibly finish the capture. `audioMissing`
    /// cannot: the bytes are gone or do not play, and only the user can clear
    /// the entry.
    public var isRetryable: Bool {
        switch self {
        case .audioMissing:
            return false
        case .microphoneDenied, .audioSessionUnavailable, .insufficientStorage,
             .interrupted, .finalizationFailed, .awaitingUnlock:
            return true
        }
    }
}

/// What the capture pipeline is doing right now. Derived from durable state on
/// launch, so a relaunch mid-recording resumes with the same vocabulary the UI
/// used before the process died.
public enum CaptureStatus: Hashable, Sendable {
    case idle
    case recording(entryID: EntryID, startedAt: Date)
    case saved(entryID: EntryID, audio: StoredAudio, durationSeconds: Double)
    case failed(entryID: EntryID, failure: CaptureFailure, retainedAudio: StoredAudio?)

    public var isRecording: Bool {
        if case .recording = self { return true }
        return false
    }
}

/// What a terminal interruption did to the recording that was in flight.
///
/// Returned rather than thrown because an interruption is not a caller's error:
/// nobody asked for it, and the screen that has to react to it needs the entry
/// and the reason more than it needs a failure to catch.
public struct RecordingInterruption: Hashable, Sendable {
    /// The entry the interrupted recording belonged to.
    public let entryID: EntryID
    /// Why capture stopped. `audioMissing` when the interruption cost the audio.
    public let failure: CaptureFailure
    /// The finalized recording, when there was one to keep.
    public let retainedAudio: StoredAudio?

    public init(entryID: EntryID, failure: CaptureFailure, retainedAudio: StoredAudio?) {
        self.entryID = entryID
        self.failure = failure
        self.retainedAudio = retainedAudio
    }
}

/// What launch recovery did with one interrupted entry.
public enum CaptureRecoveryOutcome: Hashable, Sendable {
    /// A non-empty interrupted recording was finalized and is now durable.
    case recovered(entryID: EntryID, audio: StoredAudio)
    /// Audio exists on disk with no entry pointing at it. Never deleted; the
    /// entry is rebuilt so the recording stays reachable.
    case adopted(entryID: EntryID, audio: StoredAudio)
    /// The entry survived but its audio did not.
    case unrecoverable(entryID: EntryID, failure: CaptureFailure)
    /// The audio could not be read because the phone is locked. Nothing was
    /// decided; the entry is finished after unlock.
    case awaitingUnlock(entryID: EntryID)
}

extension CaptureFailure {
    /// The reason a recorder or store error leaves on the entry.
    static func reason(for error: any Error) -> CaptureFailure {
        if let recording = error as? RecordingError {
            switch recording {
            case .microphoneDenied: return .microphoneDenied
            case .sessionUnavailable: return .audioSessionUnavailable
            case .interrupted: return .interrupted
            case .noActiveRecording, .emptyRecording: return .audioMissing
            }
        }
        if let storage = error as? AudioStoreError {
            switch storage {
            case .insufficientStorage: return .insufficientStorage
            case .emptyRecording, .missingFile: return .audioMissing
            case .finalizationFailed: return .finalizationFailed
            }
        }
        return .finalizationFailed
    }
}
