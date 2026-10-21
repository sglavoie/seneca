import Foundation

/// The journal names its days YYYY-MM-DD. These turn such a day into words
/// and back without placing it in any time zone: a journal day is a calendar
/// day, not an instant.
enum DayText {
    private static let calendar: Calendar = {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        return calendar
    }()

    static var timeZone: TimeZone { calendar.timeZone }

    private static var iso: Date.ISO8601FormatStyle {
        Date.ISO8601FormatStyle(timeZone: calendar.timeZone).year().month().day()
    }

    /// Noon of the day, so no zone arithmetic can move it to its neighbor.
    static func date(_ day: String) -> Date? {
        guard let start = try? Date(day, strategy: iso) else { return nil }
        return calendar.date(byAdding: .hour, value: 12, to: start)
    }

    static func day(_ date: Date) -> String {
        date.formatted(iso)
    }

    /// "Thursday, April 2, 2026", or the day as written when it is no date.
    static func title(_ day: String) -> String {
        guard let date = date(day) else { return day }
        var style = Date.FormatStyle(date: .complete, time: .omitted)
        style.timeZone = calendar.timeZone
        return date.formatted(style)
    }

    /// The month a day is in, YYYY-MM.
    static func month(of day: String) -> String {
        String(day.prefix(7))
    }

    /// The month `offset` months from `month`, YYYY-MM.
    static func month(_ month: String, offset: Int) -> String {
        guard let first = date(month + "-01"),
              let moved = calendar.date(byAdding: .month, value: offset, to: first)
        else { return month }
        return String(day(moved).prefix(7))
    }

    /// "March 2026", or the month as written when it is no month.
    static func monthTitle(_ month: String) -> String {
        guard let first = date(month + "-01") else { return month }
        var style = Date.FormatStyle().month(.wide).year()
        style.timeZone = calendar.timeZone
        return first.formatted(style)
    }
}
