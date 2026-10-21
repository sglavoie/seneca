import Foundation

// Kept internal so the speech adapters need no shared work package; see
// PROVENANCE.md.

/// Thrown by `withDeadline` when the operation did not finish in time.
struct DeadlineExceeded: Error, Equatable, Sendable {}

/// Runs `operation` and returns its result, or throws `DeadlineExceeded` once
/// `limit` passes. On timeout the operation is cancelled but not awaited: a
/// system call that ignores cancellation (an asset download that never
/// starts, for example) must not keep the caller, and every worker queued
/// behind it, waiting forever. Cancelling the caller cancels the operation and
/// returns whatever it ends with.
func withDeadline<T: Sendable>(
    _ limit: Duration,
    _ operation: @escaping @Sendable () async throws -> T
) async throws -> T {
    let work = Task { try await operation() }
    return try await withTaskCancellationHandler {
        try await withCheckedThrowingContinuation { continuation in
            let once = ResumeOnce(continuation)
            Task { once.resume(with: await work.result) }
            Task {
                try? await Task.sleep(for: limit)
                if once.resume(with: .failure(DeadlineExceeded())) { work.cancel() }
            }
        }
    } onCancel: {
        work.cancel()
    }
}

/// Resumes a continuation exactly once, whichever side gets there first.
private final class ResumeOnce<T: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<T, any Error>?

    init(_ continuation: CheckedContinuation<T, any Error>) {
        self.continuation = continuation
    }

    /// True when this call decided the outcome.
    func resume(with result: Result<T, any Error>) -> Bool {
        let target = lock.withLock {
            defer { continuation = nil }
            return continuation
        }
        target?.resume(with: result)
        return target != nil
    }
}
