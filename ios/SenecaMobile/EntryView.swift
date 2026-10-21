import SenecaEngine
import SwiftUI

/// One day, as the engine reads it.
struct EntryView: View {
    @Environment(JournalModel.self) private var journal
    @Environment(AppRouter.self) private var router
    @Environment(\.dismiss) private var dismiss
    let date: String

    @State private var entry: Entry?
    @State private var failure: String?
    @State private var sharing = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                if let failure {
                    Label(failure, systemImage: "exclamationmark.triangle").foregroundStyle(.red)
                }
                if let conflict = journal.conflict(on: date) {
                    VStack(alignment: .leading, spacing: 8) {
                        Label(
                            "This day was written differently on another device. This is this iPhone’s version.",
                            systemImage: "arrow.triangle.branch"
                        )
                        .font(.footnote)
                        .accessibilityIdentifier("entry-conflicted")
                        NavigationLink {
                            ConflictView(conflict: conflict)
                        } label: {
                            Text("Choose a Version")
                        }
                        .accessibilityIdentifier("entry-choose-version")
                    }
                }
                if journal.draft(on: date) != nil {
                    Label("You have an unsaved draft for this day.", systemImage: "tray.and.arrow.up")
                        .font(.footnote)
                        .accessibilityIdentifier("entry-has-draft")
                }
                if let voice = journal.voice, case let takes = voice.takes.filter({ $0.date == date }), !takes.isEmpty {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Recordings on This iPhone").font(.subheadline).accessibilityAddTraits(.isHeader)
                        ForEach(takes) { take in
                            TakeRow(voice: voice, take: take)
                        }
                    }
                    .accessibilityElement(children: .contain)
                    .accessibilityIdentifier("entry-recordings")
                }
                ForEach(Array((entry?.sections ?? []).enumerated()), id: \.offset) { _, section in
                    VStack(alignment: .leading, spacing: 6) {
                        Text(section.heading).font(.headline).accessibilityAddTraits(.isHeader)
                        if let follow = section.followThrough {
                            Text(follow.sourceIntention)
                                .font(.subheadline)
                                .foregroundStyle(.secondary)
                                .padding(.leading, 8)
                                .accessibilityLabel("Intention: \(follow.sourceIntention)")
                            Text(follow.response)
                        } else {
                            Text(section.body)
                        }
                    }
                    .textSelection(.enabled)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding()
        }
        .navigationTitle(DayText.title(date))
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItemGroup(placement: .primaryAction) {
                Button("Edit") { router.write(date) }
                    .accessibilityIdentifier("edit-entry")
                if let entry {
                    Button("Share", systemImage: "square.and.arrow.up") { sharing = true }
                        .accessibilityIdentifier("entry-share")
                    DeleteEntryButton(date: date, revision: entry.revision) { dismiss() }
                }
            }
        }
        .sheet(isPresented: $sharing) {
            ShareComposer(reviewID: nil, days: [date], chosen: [date])
        }
        .task(id: journal.generation) {
            do {
                entry = try await journal.entry(on: date)
                failure = nil
            } catch {
                failure = JournalModel.describe(error)
            }
        }
    }
}
