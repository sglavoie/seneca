import Foundation

/// The bridge version these models were written for. Every request states it
/// and the engine refuses any other.
public let supportedBridgeVersion = 8

/// A refusal from the engine: a stable code, the message written for the
/// author, and whether sending the same request again can help.
public struct EngineProblem: Error, Codable, Equatable, Sendable {
    public var code: String
    public var message: String
    public var retryable: Bool
    public var details: [String: String]?

    public init(code: String, message: String, retryable: Bool = false, details: [String: String]? = nil) {
        self.code = code
        self.message = message
        self.retryable = retryable
        self.details = details
    }
}

extension EngineProblem: LocalizedError {
    public var errorDescription: String? { message }
}

/// Failures the adapter raises itself, before or after the engine answers.
public enum BridgeFailure: Error, Equatable, Sendable {
    /// The linked engine speaks a different bridge than these models.
    case versionMismatch(engine: Int, required: Int)
    /// The engine's reply could not be read as a response.
    case malformedResponse(String)
}

/// The identity of one mutation. A request sent again under the same
/// identity is answered from the engine's ledger instead of carried out
/// again, so keep it for as long as the same save may be retried.
public struct RequestID: Hashable, Sendable, CustomStringConvertible {
    public let value: String

    public init() { value = UUID().uuidString.lowercased() }
    public init(_ value: String) { self.value = value }

    public var description: String { value }
}

/// What a mutation did.
public struct Outcome<Value: Sendable>: Sendable {
    public let value: Value
    /// True when the engine had already carried this request out and gave
    /// its first answer again.
    public let replayed: Bool
    /// Set when the change reached the journal and a later step failed. The
    /// change is there: finish as on success, and do not send it again.
    public let warning: EngineProblem?
}

struct Request<Params: Encodable>: Encodable {
    let version: Int
    let operation: String
    let requestId: String?
    let params: Params
}

struct Response<Result: Decodable>: Decodable {
    let version: Int
    let ok: Bool
    let replayed: Bool?
    let result: Result?
    let warning: EngineProblem?
    let error: EngineProblem?
}

struct NoParams: Codable {}
