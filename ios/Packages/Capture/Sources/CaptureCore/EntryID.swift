import Foundation

public struct EntryID: Hashable, Sendable, Codable, CustomStringConvertible {
    public let rawValue: UUID

    public init(_ rawValue: UUID) {
        self.rawValue = rawValue
    }

    public init?(string: String) {
        guard let parsed = UUID(uuidString: string) else { return nil }
        self.rawValue = parsed
    }

    /// Lowercased canonical form. The schema pattern and the on-disk directory
    /// name both require lowercase, but `UUID.uuidString` is uppercase.
    public var description: String { rawValue.uuidString.lowercased() }

    public var prefix: String { String(description.prefix(8)) }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.singleValueContainer()
        let raw = try container.decode(String.self)
        guard let parsed = UUID(uuidString: raw) else {
            throw DecodingError.dataCorruptedError(
                in: container,
                debugDescription: "\(raw) is not a UUID"
            )
        }
        self.rawValue = parsed
    }

    public func encode(to encoder: any Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encode(description)
    }
}
