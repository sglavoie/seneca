import CaptureCore

/// One-shot barrier for deterministic interleavings. Cancellation deliberately
/// does not release it: tests can model a provider that is still finishing after
/// cancellation. Always release the gate and await workers before cleanup.
public actor AsyncTestGate {
    private var entered = false
    private var released = false
    private var observers: [CheckedContinuation<Void, Never>] = []
    private var waiters: [CheckedContinuation<Void, Never>] = []
    public init() {}
    public func pause() async {
        entered = true
        observers.forEach { $0.resume() }
        observers.removeAll()
        if !released { await withCheckedContinuation { waiters.append($0) } }
    }

    public func arrived() async {
        if !entered { await withCheckedContinuation { observers.append($0) } }
    }

    public func release() {
        released = true
        waiters.forEach { $0.resume() }
        waiters.removeAll()
    }
}

/// A lock-state simulation; does not change actual file protection.
public actor SimulatedProtectedData: ProtectedDataAvailability {
    public private(set) var isAvailable: Bool
    public init(isAvailable: Bool = true) { self.isAvailable = isAvailable }
    public func setAvailable(_ available: Bool) { isAvailable = available }
}
