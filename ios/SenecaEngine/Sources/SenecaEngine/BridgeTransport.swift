import Foundation
import SenecaBridgeCore

/// The surface the Go engine exports: a version, and one JSON request in for
/// one JSON response out. Tests may substitute their own.
public protocol BridgeTransport: Sendable {
    var bridgeVersion: Int { get }
    func call(_ request: String) -> String
}

/// One bridge of the gomobile-bound engine linked from
/// SenecaBridgeCore.xcframework. Each holds its own open journal; two of them
/// share nothing.
public final class GoBridgeTransport: BridgeTransport, @unchecked Sendable {
    // The Go bridge answers one request at a time behind its own lock.
    private let bridge: SenecabridgeBridge

    public init() {
        guard let bridge = SenecabridgeNewBridge() else {
            preconditionFailure("the Go engine returned no bridge")
        }
        self.bridge = bridge
    }

    public var bridgeVersion: Int { SenecabridgeBridgeVersion() }

    public func call(_ request: String) -> String {
        bridge.call(request)
    }
}
