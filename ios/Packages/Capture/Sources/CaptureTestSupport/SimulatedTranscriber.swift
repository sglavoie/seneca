import CaptureCore
import Foundation

public actor SimulatedTranscriber: Transcribing {
    private let text: String
    private let provider: TranscriptProvider
    private let failuresBeforeSuccess: Int
    private let failure: TranscriptionError
    private let alwaysFails: Bool
    private let gate: AsyncTestGate?
    public private(set) var requests: [TranscriptionRequest] = []

    public init(
        text: String, provider: TranscriptProvider = .none, failuresBeforeSuccess: Int = 0,
        failure: TranscriptionError = .noProviderAvailable, alwaysFails: Bool = false, gate: AsyncTestGate? = nil
    ) {
        precondition(failuresBeforeSuccess >= 0)
        self.text = text
        self.provider = provider
        self.failuresBeforeSuccess = failuresBeforeSuccess
        self.failure = failure
        self.alwaysFails = alwaysFails
        self.gate = gate
    }

    public func transcribe(_ request: TranscriptionRequest) async throws -> TranscriptionOutcome {
        try Task.checkCancellation()
        requests.append(request)
        let shouldFail = alwaysFails || requests.count <= failuresBeforeSuccess
        await gate?.pause()
        try Task.checkCancellation()
        if shouldFail { throw failure }
        return TranscriptionOutcome(text: text, localeIdentifier: request.localeIdentifier, provider: provider)
    }
}
