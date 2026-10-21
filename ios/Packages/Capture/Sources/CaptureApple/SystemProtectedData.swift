#if os(iOS)
import CaptureCore
import UIKit

/// The phone's lock state, as capture recovery needs it. A closed
/// capture file cannot be read while this is false.
public struct SystemProtectedData: ProtectedDataAvailability {
    public init() {}

    public var isAvailable: Bool {
        get async { await MainActor.run { UIApplication.shared.isProtectedDataAvailable } }
    }
}

#endif
