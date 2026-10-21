import Foundation

/// Canonicalization and presentation shared by every recording-language UI.
///
/// The supported identifiers themselves come from the device at runtime. This
/// type deliberately owns no fixed catalog: keeping the transformations here
/// makes provider results, saved preferences, and persisted capture languages use
/// the same BCP 47 spelling.
public enum TranscriptionLocaleCatalog {
    public static func canonicalIdentifier(_ identifier: String) -> String? {
        let trimmed = identifier.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }

        let locale = Locale(identifier: trimmed)
        let canonical = locale.identifier(.bcp47)
        guard !canonical.isEmpty,
              canonical != "und",
              locale.language.languageCode != .unidentified else {
            return nil
        }
        return canonical
    }

    /// Returns a stable, de-duplicated union while retaining source order.
    public static func normalizedUnion(_ sources: [[String]]) -> [String] {
        var seen: Set<String> = []
        var identifiers: [String] = []
        for source in sources {
            for rawIdentifier in source {
                guard let identifier = canonicalIdentifier(rawIdentifier),
                      seen.insert(identifier).inserted else {
                    continue
                }
                identifiers.append(identifier)
            }
        }
        return identifiers
    }

    public static func ordered(
        _ identifiers: some Sequence<String>,
        displayLocale: Locale = .current
    ) -> [String] {
        Set(identifiers.compactMap(canonicalIdentifier)).sorted { lhs, rhs in
            let lhsName = localizedName(for: lhs, displayLocale: displayLocale)
            let rhsName = localizedName(for: rhs, displayLocale: displayLocale)
            let comparison = lhsName.localizedStandardCompare(rhsName)
            if comparison == .orderedSame { return lhs < rhs }
            return comparison == .orderedAscending
        }
    }

    public static func localizedName(
        for identifier: String,
        displayLocale: Locale = .current
    ) -> String {
        displayLocale.localizedString(forIdentifier: identifier) ?? identifier
    }
}
