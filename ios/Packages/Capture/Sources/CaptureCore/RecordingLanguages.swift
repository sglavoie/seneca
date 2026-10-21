import Foundation

public protocol RecordingLocaleProviding: Sendable {
    func supportedLocaleIdentifiers() async throws -> [String]
}

public struct FixedRecordingLocaleProvider: RecordingLocaleProviding {
    public var identifiers: [String]
    public var failure: (any Error & Sendable)?

    public init(_ identifiers: [String], failure: (any Error & Sendable)? = nil) {
        self.identifiers = identifiers
        self.failure = failure
    }

    public func supportedLocaleIdentifiers() async throws -> [String] {
        if let failure { throw failure }
        return identifiers
    }
}

public protocol RecordingLanguagePreferenceStoring: Sendable {
    /// `nil` is different from an empty set: it means the user has never
    /// curated the list, so all currently supported locales begin enabled.
    func enabledLocaleIdentifiers() async -> Set<String>?
    func setEnabledLocaleIdentifiers(_ identifiers: Set<String>) async
}

public final class UserDefaultsRecordingLanguagePreferenceStore: @unchecked Sendable,
    RecordingLanguagePreferenceStoring {
    public static let key = "recordingLanguages.enabledIdentifiers.v1"

    private let defaults: UserDefaults
    private let preferenceKey: String

    public init(defaults: UserDefaults = .standard, key: String = UserDefaultsRecordingLanguagePreferenceStore.key) {
        self.defaults = defaults
        preferenceKey = key
    }

    public func enabledLocaleIdentifiers() async -> Set<String>? {
        guard defaults.object(forKey: preferenceKey) != nil else { return nil }
        return Set(defaults.stringArray(forKey: preferenceKey) ?? [])
    }

    public func setEnabledLocaleIdentifiers(_ identifiers: Set<String>) async {
        defaults.set(identifiers.sorted(), forKey: preferenceKey)
    }

    public func removeSelection() {
        defaults.removeObject(forKey: preferenceKey)
    }
}

public actor InMemoryRecordingLanguagePreferenceStore: RecordingLanguagePreferenceStoring {
    private var identifiers: Set<String>?

    public init(_ identifiers: Set<String>? = nil) {
        self.identifiers = identifiers
    }

    public func enabledLocaleIdentifiers() -> Set<String>? {
        identifiers
    }

    public func setEnabledLocaleIdentifiers(_ identifiers: Set<String>) {
        self.identifiers = identifiers
    }
}
