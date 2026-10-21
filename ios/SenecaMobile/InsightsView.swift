import SenecaEngine
import SwiftUI

/// How the follow-through report reads on screen.
enum InsightsText {
    static func status(_ status: String) -> String {
        switch status {
        case "answered": return "Answered"
        case "unanswered": return "Left blank"
        case "not-carried": return "Not carried"
        case "open": return "Waiting for the next day"
        default: return "Before follow-through"
        }
    }

    static func days(_ count: Int) -> String {
        count == 1 ? "1 day" : "\(count) days"
    }

    /// "Sep 18 to Oct 17", in the journal's zone.
    static func span(_ from: String, _ to: String) -> String {
        "\(short(from)) to \(short(to))"
    }

    static func short(_ day: String) -> String {
        guard let date = DayText.date(day) else { return day }
        var style = Date.FormatStyle(date: .abbreviated, time: .omitted)
        style.timeZone = DayText.timeZone
        return date.formatted(style)
    }

    static func dates(_ days: [String]) -> String {
        let shown = days.suffix(5).map(short)
        return (days.count > 5 ? "…, " : "") + shown.joined(separator: ", ")
    }
}

/// What became of the intentions of the last 30 days, the wordings and words
/// that return, and how long each focus stood. Counted, never scored: each
/// line is a day the journal holds.
struct InsightsView: View {
    @Environment(JournalModel.self) private var journal

    @State private var report: Insights?
    @State private var failure: String?

    var body: some View {
        List {
            if let failure {
                Section {
                    Label(failure, systemImage: "exclamationmark.triangle")
                        .accessibilityIdentifier("insights-failure")
                }
            } else if let report {
                intentions(report)
                if !report.repeated.isEmpty {
                    Section {
                        ForEach(report.repeated) { repeated in
                            VStack(alignment: .leading, spacing: 4) {
                                Text(repeated.text)
                                Text("\(repeated.dates.count) times: \(InsightsText.dates(repeated.dates))")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                            .accessibilityElement(children: .combine)
                        }
                    } header: {
                        Text("Written More Than Once")
                    } footer: {
                        Text("The same words, whatever their case or punctuation.")
                    }
                }
                if !report.recurringWords.isEmpty {
                    Section("Words That Return") {
                        ForEach(report.recurringWords) { word in
                            LabeledContent(word.text, value: InsightsText.days(word.dates.count))
                                .accessibilityIdentifier("insights-word-\(word.text)")
                        }
                    }
                }
                focus(report)
            } else {
                ProgressView()
            }
        }
        .navigationTitle("Follow-Through")
        .navigationBarTitleDisplayMode(.inline)
        .task(id: journal.generation) { await load() }
    }

    @ViewBuilder
    private func intentions(_ report: Insights) -> some View {
        Section {
            if report.intentions.isEmpty {
                Text("No intention was written in these days.")
                    .accessibilityIdentifier("insights-none")
            } else {
                count("Answered", report.answered, id: "answered")
                count("Left blank", report.unanswered, id: "unanswered")
                count("Not carried", report.notCarried, id: "not-carried")
                count("Waiting for the next day", report.open, id: "open")
                if report.untracked > 0 {
                    count("Before follow-through", report.untracked, id: "untracked")
                }
            }
        } header: {
            Text("Intentions, \(InsightsText.span(report.from, report.to))")
        } footer: {
            Text("An intention is answered when a later day’s follow-through says what happened. Left blank: a later day carried it and wrote nothing. Not carried: days were written after it without it.")
        }
        if !report.intentions.isEmpty {
            Section("Each Intention") {
                ForEach(report.intentions.reversed()) { intention in
                    NavigationLink {
                        EntryView(date: intention.answeredOn.isEmpty ? intention.date : intention.answeredOn)
                    } label: {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(intention.text).lineLimit(3)
                            Text("\(InsightsText.short(intention.date)) · \(InsightsText.status(intention.status))")
                                .font(.caption).foregroundStyle(.secondary)
                        }
                        .accessibilityElement(children: .combine)
                    }
                    .accessibilityIdentifier("insights-intention-\(intention.date)")
                }
            }
        }
    }

    private func count(_ label: String, _ value: Int, id: String) -> some View {
        LabeledContent(label, value: "\(value)")
            .accessibilityIdentifier("insights-\(id)")
    }

    @ViewBuilder
    private func focus(_ report: Insights) -> some View {
        Section("Focus") {
            if !report.focusProblem.isEmpty {
                Label(report.focusProblem, systemImage: "exclamationmark.triangle")
                    .accessibilityIdentifier("insights-focus-problem")
            } else if report.focus.isEmpty {
                Text("No focus was set in these days.")
            } else {
                ForEach(report.focus.reversed()) { span in
                    VStack(alignment: .leading, spacing: 4) {
                        Text(span.event == "set" ? span.text : "No focus")
                        Text(spanLine(span)).font(.caption).foregroundStyle(.secondary)
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("insights-focus-\(span.from)")
                }
            }
        }
    }

    private func spanLine(_ span: FocusSpan) -> String {
        if span.current {
            return "Since \(InsightsText.short(span.from)), \(InsightsText.days(span.days))"
        }
        let then = span.replacedBy == "clear" ? "then cleared" : "then: \(span.replacedByText)"
        return "\(InsightsText.span(span.from, span.until)), \(InsightsText.days(span.days)); \(then)"
    }

    private func load() async {
        do {
            report = try await journal.insights()
            failure = nil
        } catch {
            failure = JournalModel.describe(error)
        }
    }
}
