import SenecaEngine
import SwiftUI

/// The days written, newest first: all of them, one month at a time, a range
/// of days, or the earlier days written on today's date, and the days among
/// them a phrase is found in. The engine chooses the days, as `seneca show`
/// does; this screen only asks.
struct HistoryView: View {
    @Environment(JournalModel.self) private var journal

    enum Scope: String, CaseIterable, Identifiable {
        case all = "All"
        case month = "Month"
        case range = "Range"
        case onThisDay = "On This Day"
        var id: String { rawValue }
    }

    @State private var scope = Scope.all
    @State private var month: String?
    @State private var from = Date()
    @State private var to = Date()
    @State private var phrase = ""
    @State private var list: EntryList?
    @State private var hits: [SearchHit]?
    @State private var failure: String?
    @State private var openDay: String?
    @State private var choosingDay = false

    private var today: String { journal.status?.today ?? DayText.day(Date()) }
    private var shownMonth: String { month ?? DayText.month(of: today) }

    private var query: EntryQuery {
        switch scope {
        case .all: return .everything
        case .month: return .month(shownMonth)
        case .range:
            let first = DayText.day(from), last = DayText.day(to)
            return .range(from: min(first, last), to: max(first, last))
        case .onThisDay: return .onThisDay
        }
    }

    /// What the list depends on: when any of it changes it is asked again.
    private struct Request: Equatable {
        let query: EntryQuery
        let phrase: String
        let generation: Int
    }

    var body: some View {
        List {
            Section {
                Picker("Show", selection: $scope) {
                    ForEach(Scope.allCases) { Text($0.rawValue).tag($0) }
                }
                .accessibilityIdentifier("history-scope")
                switch scope {
                case .all, .onThisDay:
                    EmptyView()
                case .month:
                    monthStepper
                case .range:
                    DatePicker("From", selection: $from, in: ...(DayText.date(today) ?? Date()), displayedComponents: .date)
                        .environment(\.timeZone, DayText.timeZone)
                        .accessibilityIdentifier("history-from")
                    DatePicker("To", selection: $to, in: ...(DayText.date(today) ?? Date()), displayedComponents: .date)
                        .environment(\.timeZone, DayText.timeZone)
                        .accessibilityIdentifier("history-to")
                }
            }
            if let voice = journal.voice, !voice.takes.isEmpty {
                Section {
                    NavigationLink {
                        RecordingsView(voice: voice)
                    } label: {
                        LabeledContent("Recordings on This iPhone", value: "\(voice.takes.count)")
                    }
                    .accessibilityIdentifier("history-recordings")
                }
            }
            if let problem = failure ?? journal.entriesProblem {
                Section {
                    Label(problem, systemImage: "exclamationmark.triangle").accessibilityIdentifier("history-problem")
                }
            }
            results
        }
        .navigationTitle("History")
        .navigationDestination(item: $openDay) { date in
            EntryView(date: date)
        }
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("Go to Day", systemImage: "calendar") { choosingDay = true }
                    .accessibilityIdentifier("history-go-to-day")
            }
        }
        .sheet(isPresented: $choosingDay) {
            DayPicker(today: today) { day in go(to: day) }
        }
        .onChange(of: scope) { _, scope in
            // A range starts as this month so far, in the journal's days.
            if scope == .range {
                from = DayText.date(DayText.month(of: today) + "-01") ?? Date()
                to = DayText.date(today) ?? Date()
            }
        }
        .searchable(text: $phrase, prompt: "Search reflections")
        .task(id: Request(query: query, phrase: phrase, generation: journal.generation)) { await load() }
        .refreshable { await journal.refresh() }
    }

    private var monthStepper: some View {
        HStack {
            Button("Previous Month", systemImage: "chevron.left") { month = DayText.month(shownMonth, offset: -1) }
                .labelStyle(.iconOnly)
                .accessibilityIdentifier("history-previous-month")
            Spacer()
            Text(DayText.monthTitle(shownMonth))
                .font(.headline)
                .accessibilityIdentifier("history-month")
            Spacer()
            Button("Next Month", systemImage: "chevron.right") { month = DayText.month(shownMonth, offset: 1) }
                .labelStyle(.iconOnly)
                .disabled(shownMonth >= DayText.month(of: today))
                .accessibilityIdentifier("history-next-month")
        }
        .buttonStyle(.borderless)
    }

    @ViewBuilder
    private var results: some View {
        if let hits {
            Section {
                if hits.isEmpty {
                    Text(scope == .all ? "No day mentions that." : "No day here mentions that.")
                        .foregroundStyle(.secondary)
                        .accessibilityIdentifier("history-no-hits")
                }
                ForEach(hits) { hit in
                    Button { openDay = hit.date } label: {
                        row(hit.date, hit.relative, hit.snippets.first ?? "", conflicted: false)
                    }
                    .accessibilityIdentifier("hit-\(hit.date)")
                }
            }
        } else if let list {
            Section {
                if list.entries.isEmpty {
                    empty(list)
                }
                ForEach(list.entries) { entry in
                    Button { openDay = entry.date } label: {
                        row(entry.date, entry.relative, entry.preview, conflicted: entry.conflicted)
                    }
                    .accessibilityIdentifier("entry-\(entry.date)")
                }
            } footer: {
                if scope != .all, !list.entries.isEmpty {
                    Text("\(list.entries.count) of \(list.total) days")
                }
            }
        }
    }

    /// Says what the query kept none of, and where the nearest writing is.
    @ViewBuilder
    private func empty(_ list: EntryList) -> some View {
        switch scope {
        case .all:
            Text("Nothing written yet.").foregroundStyle(.secondary)
        case .month:
            Text("Nothing written in \(DayText.monthTitle(shownMonth)).").foregroundStyle(.secondary)
                .accessibilityIdentifier("history-empty")
        case .range:
            Text("Nothing written in these days.").foregroundStyle(.secondary)
                .accessibilityIdentifier("history-empty")
        case .onThisDay:
            Text("Nothing written on this date in an earlier month.").foregroundStyle(.secondary)
                .accessibilityIdentifier("history-empty")
        }
        if let before = list.nearestBefore {
            Button("Previous: \(DayText.title(before))", systemImage: "arrow.backward") { go(to: before) }
                .accessibilityIdentifier("history-nearest-before")
        }
        if let after = list.nearestAfter {
            Button("Next: \(DayText.title(after))", systemImage: "arrow.forward") { go(to: after) }
                .accessibilityIdentifier("history-nearest-after")
        }
    }

    private func row(_ date: String, _ relative: String, _ preview: String, conflicted: Bool) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(DayText.title(date)).font(.headline)
            Text(relative).font(.caption).foregroundStyle(.secondary)
            if conflicted {
                Label("Two versions: choose one", systemImage: "arrow.triangle.branch")
                    .font(.caption)
            }
            if !preview.isEmpty {
                Text(preview).font(.subheadline).lineLimit(2)
            }
        }
        .foregroundStyle(.primary)
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
    }

    /// Opens a day that is written; otherwise shows its month, and the days
    /// written nearest to it.
    private func go(to day: String) {
        if journal.hasEntry(on: day) {
            openDay = day
        } else {
            month = DayText.month(of: day)
            scope = .month
        }
    }

    private func load() async {
        let wanted = phrase.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            if wanted.isEmpty {
                list = try await journal.entries(query)
                hits = nil
            } else {
                hits = try await journal.search(wanted, in: query)
            }
            failure = nil
        } catch {
            list = nil
            hits = nil
            failure = JournalModel.describe(error)
        }
    }
}

/// Picks a day up to today to go to.
private struct DayPicker: View {
    @Environment(\.dismiss) private var dismiss
    let today: String
    let choose: (String) -> Void
    @State private var chosen = Date()

    var body: some View {
        NavigationStack {
            Form {
                DatePicker("Day", selection: $chosen, in: ...(DayText.date(today) ?? Date()), displayedComponents: .date)
                    .datePickerStyle(.graphical)
                    .environment(\.timeZone, DayText.timeZone)
                    .accessibilityIdentifier("history-day-picker")
            }
            .navigationTitle("Go to Day")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Go") {
                        dismiss()
                        choose(DayText.day(chosen))
                    }
                    .accessibilityIdentifier("history-day-go")
                }
            }
            .onAppear { chosen = DayText.date(today) ?? Date() }
        }
    }
}
