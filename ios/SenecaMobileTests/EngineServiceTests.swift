import Foundation
import SenecaEngine
import XCTest

/// The service itself, over a transport that only watches how it is called.
final class EngineServiceTests: XCTestCase {
    /// Records how the engine was entered and answers every request the same.
    private final class Watcher: BridgeTransport, @unchecked Sendable {
        private let lock = NSLock()
        private var inside = 0
        private(set) var overlapped = false
        private(set) var onMainThread = false
        private(set) var calls = 0
        private(set) var requests: [String] = []
        var version = supportedBridgeVersion
        var reply = #"{"version":1,"ok":true,"result":{"today":"2026-04-02","total":0,"entries":[]}}"#

        var bridgeVersion: Int { version }

        func call(_ request: String) -> String {
            lock.lock()
            inside += 1
            calls += 1
            requests.append(request)
            if inside > 1 { overlapped = true }
            if Thread.isMainThread { onMainThread = true }
            lock.unlock()
            // Long enough for a second caller to arrive if nothing held it back.
            Thread.sleep(forTimeInterval: 0.01)
            lock.lock()
            inside -= 1
            lock.unlock()
            return reply
        }
    }

    @MainActor
    func testCallsRunOneAtATimeAwayFromTheMainThread() async throws {
        let watcher = Watcher()
        let engine = SenecaEngine(transport: watcher)

        try await withThrowingTaskGroup(of: Void.self) { group in
            for _ in 0..<24 {
                group.addTask { _ = try await engine.entries() }
            }
            // And from the main actor, as a screen calls it.
            _ = try await engine.entries()
            try await group.waitForAll()
        }

        XCTAssertEqual(watcher.calls, 25)
        XCTAssertFalse(watcher.overlapped, "two requests were inside the engine at once")
        XCTAssertFalse(watcher.onMainThread, "the engine was called on the main thread")
    }

    func testEveryRequestStatesItsVersionAndAMutationItsIdentity() async throws {
        let watcher = Watcher()
        let engine = SenecaEngine(transport: watcher)
        _ = try await engine.entries()
        watcher.reply = #"{"version":1,"ok":true,"replayed":true,"result":{"changed":true,"effective":"2026-04-02","text":"x","previous":"","revision":"r"},"warning":{"code":"write-not-flushed","message":"flush failed","retryable":false}}"#
        let outcome = try await engine.setFocus("x", base: "absent", requestID: RequestID("focus-1"))

        let read = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(watcher.requests[0].utf8)) as? [String: Any])
        XCTAssertEqual(read["version"] as? Int, supportedBridgeVersion)
        XCTAssertEqual(read["operation"] as? String, "entries.list")
        XCTAssertNil(read["request_id"], "a read carries no identity")

        let mutation = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(watcher.requests[1].utf8)) as? [String: Any])
        XCTAssertEqual(mutation["operation"] as? String, "focus.set")
        XCTAssertEqual(mutation["request_id"] as? String, "focus-1")
        XCTAssertEqual(mutation["params"] as? [String: String], ["base": "absent", "text": "x"])

        // A write that landed is a result, with its warning beside it.
        XCTAssertTrue(outcome.replayed)
        XCTAssertEqual(outcome.value.text, "x")
        XCTAssertEqual(outcome.warning?.code, "write-not-flushed")
    }

    func testRefusalsAndBrokenRepliesAreDifferentFailures() async throws {
        let watcher = Watcher()
        let engine = SenecaEngine(transport: watcher)

        watcher.reply = #"{"version":1,"ok":false,"error":{"code":"journal-busy","message":"try again","retryable":true}}"#
        let busy = await refusal("journal-busy") { try await engine.entries() }
        XCTAssertEqual(busy?.retryable, true)

        watcher.reply = "not a response"
        do {
            _ = try await engine.entries()
            XCTFail("a reply that is not a response was accepted")
        } catch let failure as BridgeFailure {
            guard case .malformedResponse = failure else { return XCTFail("\(failure)") }
        }

        // An engine that speaks another bridge is not opened at all.
        watcher.version = supportedBridgeVersion + 1
        let before = watcher.calls
        do {
            try await engine.open(JournalLocation(
                reflections: URL(fileURLWithPath: "/tmp/reflections.md"), stateDirectory: URL(fileURLWithPath: "/tmp/state")
            ))
            XCTFail("a newer engine was opened")
        } catch let failure as BridgeFailure {
            XCTAssertEqual(failure, .versionMismatch(engine: supportedBridgeVersion + 1, required: supportedBridgeVersion))
        }
        XCTAssertEqual(watcher.calls, before)
    }
}
