import SwiftUI

/// Speaking on this iPhone: the language takes are transcribed in, its
/// speech model, and what is kept of a take. Device-local, like reminders.
struct VoiceSettingsView: View {
    let voice: VoiceCapture
    @State private var confirmingRetention = false

    var body: some View {
        List {
            SpeechSettings(voice: voice)
            Section {
                Picker("After Saving", selection: Binding(get: { voice.retention }, set: choose)) {
                    ForEach(AudioRetention.allCases) { Text($0.name).tag($0) }
                }
                .accessibilityIdentifier("voice-retention")
                NavigationLink {
                    RecordingsView(voice: voice)
                } label: {
                    LabeledContent("Recordings", value: VoiceText.usage(voice.takes.count, bytes: voice.summary.bytes))
                }
                .accessibilityIdentifier("voice-recordings")
            } header: {
                Text("Audio")
            } footer: {
                Text(voice.retention == .keep
                    ? "Recordings stay on this iPhone until you delete them. They are never synced."
                    : "A recording and its transcript are deleted once the answers from it are saved. They are never synced.")
            }
        }
        .navigationTitle("Voice")
        .navigationBarTitleDisplayMode(.inline)
        .confirmationDialog("Delete recordings after saving?", isPresented: $confirmingRetention, titleVisibility: .visible) {
            Button("Delete \(VoiceText.recordings(voice.saved.count))", role: .destructive) {
                voice.setRetention(.deleteAfterSaving)
            }
            .accessibilityIdentifier("voice-retention-confirm")
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Recordings whose answers are already saved are deleted now. What you wrote in the journal is not touched.")
        }
    }

    private func choose(_ retention: AudioRetention) {
        if retention == .deleteAfterSaving, !voice.saved.isEmpty {
            confirmingRetention = true
        } else {
            voice.setRetention(retention)
        }
    }
}

enum VoiceText {
    /// What the way from a transcript into the form is called: sorting by
    /// the model where this iPhone runs it, copying by hand where it cannot.
    @MainActor static func sortLabel(_ voice: VoiceCapture) -> String {
        voice.splitter.offered ? "Sort Into Your Questions" : "Copy Into Your Questions"
    }

    static func recordings(_ count: Int) -> String {
        count == 1 ? "1 Recording" : "\(count) Recordings"
    }

    static func usage(_ count: Int, bytes: Int) -> String {
        count == 0 ? "None" : "\(count), \(ByteCountFormatter.string(fromByteCount: Int64(bytes), countStyle: .file))"
    }

    static func duration(_ seconds: Double) -> String {
        Duration.seconds(seconds.rounded()).formatted(.time(pattern: .minuteSecond))
    }

    static func state(_ take: SpokenTake) -> String {
        switch take.state {
        case .transcribed: return take.answersSaved ? "Answers saved" : "Transcribed"
        case .untranscribed: return "Not transcribed"
        case .waitingForModel: return "Waiting for speech model"
        case .failed: return "Transcription failed"
        }
    }
}

/// One take, opening on its transcript and answers, with the way to
/// delete it.
struct TakeRow: View {
    let voice: VoiceCapture
    let take: SpokenTake
    var showsDay = false
    @State private var confirming = false

    var body: some View {
        HStack {
            NavigationLink {
                TakeView(voice: voice, id: take.id)
            } label: {
                VStack(alignment: .leading, spacing: 4) {
                    if showsDay {
                        Text(DayText.title(take.date)).font(.subheadline)
                    }
                    Text("\(VoiceText.duration(take.durationSeconds)), \(VoiceText.state(take))")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            .accessibilityIdentifier("take-\(take.id)")
            Spacer()
            Button("Delete Recording", systemImage: "trash", role: .destructive) { confirming = true }
                .labelStyle(.iconOnly)
                .buttonStyle(.borderless)
                .disabled(!voice.canDelete(take))
                .accessibilityIdentifier("take-delete-\(take.id)")
        }
        .confirmationDialog("Delete this recording?", isPresented: $confirming, titleVisibility: .visible) {
            Button("Delete This Recording", role: .destructive) { try? voice.delete(take) }
                .accessibilityIdentifier("take-delete-confirm")
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The audio and its transcript are deleted from this iPhone. What you wrote in the journal is not touched.")
        }
    }
}

/// Every take on this iPhone, by day, and the ways to delete them in bulk.
struct RecordingsView: View {
    let voice: VoiceCapture
    @State private var confirmingAll = false
    @State private var confirmingSaved = false

    private var days: [String] {
        Array(Set(voice.takes.map(\.date))).sorted(by: >)
    }

    var body: some View {
        List {
            if voice.takes.isEmpty {
                Text("No recordings on this iPhone.")
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("recordings-empty")
            }
            ForEach(days, id: \.self) { day in
                Section(DayText.title(day)) {
                    ForEach(voice.takes.filter { $0.date == day }) { take in
                        TakeRow(voice: voice, take: take)
                    }
                }
            }
            if !voice.takes.isEmpty {
                Section {
                    Button("Delete Recordings With Saved Answers", role: .destructive) { confirmingSaved = true }
                        .disabled(voice.saved.isEmpty)
                        .accessibilityIdentifier("recordings-delete-saved")
                    Button("Delete All Recordings", role: .destructive) { confirmingAll = true }
                        .accessibilityIdentifier("recordings-delete-all")
                } footer: {
                    Text("Deleting removes each recording’s audio and transcript from this iPhone, and nothing else.")
                }
            }
        }
        .navigationTitle("Recordings")
        .navigationBarTitleDisplayMode(.inline)
        .confirmationDialog("Delete every recording?", isPresented: $confirmingAll, titleVisibility: .visible) {
            Button("Delete \(VoiceText.recordings(voice.takes.count))", role: .destructive) { voice.deleteAll() }
                .accessibilityIdentifier("recordings-delete-all-confirm")
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Recordings not transcribed yet are deleted too. What you wrote in the journal is not touched.")
        }
        .confirmationDialog("Delete recordings whose answers are saved?", isPresented: $confirmingSaved, titleVisibility: .visible) {
            Button("Delete \(VoiceText.recordings(voice.saved.count))", role: .destructive) { voice.deleteAll(voice.saved) }
                .accessibilityIdentifier("recordings-delete-saved-confirm")
            Button("Cancel", role: .cancel) {}
        }
    }
}
