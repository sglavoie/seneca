import Foundation

public protocol DateProviding: Sendable {
    var now: Date { get }
}

public protocol IdentifierProviding: Sendable {
    func nextIdentifier() -> EntryID
}

public struct SystemDateProvider: DateProviding {
    public init() {}
    public var now: Date { Date() }
}

public struct SystemIdentifierProvider: IdentifierProviding {
    public init() {}
    public func nextIdentifier() -> EntryID { EntryID(UUID()) }
}

public struct FixedDateProvider: DateProviding {
    public var now: Date

    public init(now: Date) {
        self.now = now
    }
}

/// Yields a deterministic sequence of identifiers so a test can assert on exact
/// archive paths.
public final class SequentialIdentifierProvider: IdentifierProviding, @unchecked Sendable {
    private let lock = NSLock()
    private var counter: UInt32

    public init(startingAt counter: UInt32 = 1) {
        self.counter = counter
    }

    public func nextIdentifier() -> EntryID {
        lock.lock()
        defer { lock.unlock() }
        let value = counter
        counter += 1
        let suffix = String(format: "%012x", value)
        let uuid = UUID(uuidString: "00000000-0000-4000-8000-\(suffix)")!
        return EntryID(uuid)
    }
}

/// Whether files under the stricter Data Protection classes can be opened right
/// now. False while the phone is locked, when a closed capture file cannot be
/// read, hashed or probed.
public protocol ProtectedDataAvailability: Sendable {
    var isAvailable: Bool { get async }
}

/// For hosts without Data Protection, and tests that do not model lock.
public struct AlwaysAvailableProtectedData: ProtectedDataAvailability {
    public init() {}
    public var isAvailable: Bool { true }
}
