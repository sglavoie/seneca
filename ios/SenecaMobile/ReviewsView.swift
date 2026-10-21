import SenecaEngine
import SwiftUI

/// The saved reviews, newest first, of every period or of one. Reviews are
/// written on the desktop; a review saved again for the same days revises
/// the earlier one, and both stay readable.
struct ReviewsView: View {
    @Environment(JournalModel.self) private var journal

    /// Nil shows every period.
    @State private var period: String?
    @State private var reviews: [ReviewSummary]?
    @State private var failure: String?

    private struct Request: Equatable {
        let period: String?
        let generation: Int
    }

    var body: some View {
        List {
            Section {
                NavigationLink {
                    InsightsView()
                } label: {
                    Label("Follow-Through", systemImage: "arrow.turn.down.right")
                }
                .accessibilityIdentifier("insights-link")
            } footer: {
                Text("What became of your intentions and how long each focus stood, over the last 30 days. Counted on this iPhone.")
            }
            Section {
                Picker("Period", selection: $period) {
                    Text("All").tag(String?.none)
                    ForEach(ReviewText.periods, id: \.self) { period in
                        Text(ReviewText.period(period)).tag(Optional(period))
                    }
                }
                .accessibilityIdentifier("reviews-period")
            }
            if let problem = journal.archiveProblem ?? failure {
                Section {
                    Label(problem, systemImage: "exclamationmark.triangle")
                }
                .accessibilityIdentifier("reviews-problem")
            } else if let reviews {
                if reviews.isEmpty {
                    ContentUnavailableView(
                        period == nil ? "No reviews yet" : "No \(ReviewText.period(period ?? "").lowercased()) reviews yet",
                        systemImage: "text.book.closed",
                        description: Text("Reviews are written with Seneca on your Mac and appear here.")
                    )
                    .accessibilityIdentifier("reviews-empty")
                }
                ForEach(reviews) { review in
                    NavigationLink(value: review.id) {
                        ReviewRow(review: review)
                    }
                    .accessibilityIdentifier("review-\(review.shortId)")
                }
            }
        }
        .navigationTitle("Reviews")
        .navigationDestination(for: String.self) { id in
            ReviewView(id: id)
        }
        .task(id: Request(period: period, generation: journal.generation)) { await load() }
        .refreshable { await journal.refresh() }
    }

    private func load() async {
        do {
            reviews = try await journal.reviews(period: period)
            failure = nil
        } catch {
            reviews = nil
            failure = JournalModel.describe(error)
        }
    }
}

enum ReviewText {
    static let periods = ["weekly", "monthly", "quarterly", "yearly"]

    static func period(_ period: String) -> String {
        switch period {
        case "weekly": return "Weekly"
        case "monthly": return "Monthly"
        case "quarterly": return "Quarterly"
        case "yearly": return "Yearly"
        default: return period.capitalized
        }
    }

    /// When a review was saved: "Saved April 2, 2026 at 7:00 AM".
    static func saved(_ createdAt: String) -> String {
        guard let date = try? Date(createdAt, strategy: .iso8601) else { return "Saved \(createdAt)" }
        return "Saved " + date.formatted(date: .long, time: .shortened)
    }
}

private struct ReviewRow: View {
    let review: ReviewSummary

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(review.title).font(.headline)
            Text("\(ReviewText.period(review.period)) · \(review.start) to \(review.end)")
                .font(.caption).foregroundStyle(.secondary)
            if review.superseded {
                Label("Revised by a later review", systemImage: "clock.arrow.circlepath")
                    .font(.caption).foregroundStyle(.secondary)
            } else if !review.revises.isEmpty {
                Label("Revises an earlier review", systemImage: "pencil.and.list.clipboard")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// One saved review, with the reviews it revises and is revised by.
private struct ReviewView: View {
    @Environment(JournalModel.self) private var journal
    let id: String

    @State private var review: Review?
    @State private var failure: String?
    @State private var sharing = false

    var body: some View {
        List {
            if let failure {
                Label(failure, systemImage: "exclamationmark.triangle").foregroundStyle(.red)
            }
            if let review {
                Section {
                    Text("\(ReviewText.period(review.period)) · \(review.start) to \(review.end)")
                        .font(.subheadline).foregroundStyle(.secondary)
                    Text(ReviewText.saved(review.createdAt)).font(.caption).foregroundStyle(.secondary)
                    Text(review.body).textSelection(.enabled).accessibilityIdentifier("review-body")
                }
                if !review.revises.isEmpty || !review.revisedBy.isEmpty {
                    Section("Revisions") {
                        if !review.revises.isEmpty {
                            NavigationLink(value: review.revises) {
                                Label("The earlier version this revises", systemImage: "arrow.uturn.backward")
                            }
                            .accessibilityIdentifier("review-revises")
                        }
                        ForEach(Array(review.revisedBy.enumerated()), id: \.offset) { index, later in
                            NavigationLink(value: later) {
                                Label(
                                    review.revisedBy.count == 1 ? "The later version" : "Later version \(index + 1)",
                                    systemImage: "arrow.uturn.forward"
                                )
                            }
                            .accessibilityIdentifier("review-revised-by-\(index)")
                        }
                    }
                }
            }
        }
        .navigationTitle(review?.title ?? "Review")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if review != nil {
                ToolbarItem(placement: .primaryAction) {
                    Button("Share", systemImage: "square.and.arrow.up") { sharing = true }
                        .accessibilityIdentifier("review-share")
                }
            }
        }
        .sheet(isPresented: $sharing) {
            if let review {
                ShareComposer(
                    reviewID: review.id,
                    days: journal.entries.map(\.date).filter { $0 >= review.start && $0 <= review.end }.sorted()
                )
            }
        }
        .task(id: id) {
            do {
                review = try await journal.review(id: id)
                failure = nil
            } catch {
                failure = JournalModel.describe(error)
            }
        }
    }
}
