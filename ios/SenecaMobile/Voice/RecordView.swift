import CaptureCore
import SwiftUI

/// Speaking a day's reflection: start and stop, how long it has run and how
/// loud it is, then the transcript. The take stays on this iPhone, outside
/// the journal.
struct RecordView: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    let voice: VoiceCapture
    let date: String
    /// The form's writing, when the take is spoken from the form; from
    /// Today, the day is opened when its answers are proposed.
    var session: ReflectionSession?
    /// Called once a take's answers are in the day's draft, before the
    /// sheet closes.
    var filled: () -> Void = {}

    var body: some View {
        NavigationStack {
            Form {
                if voice.phase == .denied {
                    denied
                } else {
                    recorder
                    outcome
                }
            }
            .navigationTitle("Speak")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button(voice.isRecording ? "Stop and Close" : "Done") {
                        Task {
                            if voice.phase == .recording { await voice.stop() }
                            dismiss()
                        }
                    }
                    .disabled(voice.phase == .starting || voice.phase == .finishing)
                    .accessibilityIdentifier("record-done")
                }
            }
        }
        .interactiveDismissDisabled(voice.isRecording)
        .onAppear { voice.reset() }
    }

    private var denied: some View {
        Section {
            VStack(alignment: .leading, spacing: 8) {
                Label("Seneca cannot use the microphone", systemImage: "mic.slash")
                    .font(.headline)
                Text("Microphone access was turned off for Seneca. To speak a reflection, allow it in Settings. You can still write your reflection.")
                    .font(.callout)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("record-denied")
            Button("Open Settings", systemImage: "gearshape") {
                if let url = URL(string: UIApplication.openSettingsURLString) { openURL(url) }
            }
            .accessibilityIdentifier("open-settings")
        }
    }

    @ViewBuilder
    private var recorder: some View {
        Section {
            VStack(spacing: 16) {
                Text(Self.clock(voice.elapsed))
                    .font(.system(.largeTitle, design: .rounded).monospacedDigit())
                    .accessibilityLabel("Elapsed time")
                    .accessibilityValue(Self.spoken(voice.elapsed))
                    .accessibilityIdentifier("record-elapsed")
                ProgressView(value: Double(voice.phase == .recording ? voice.level : 0))
                    .progressViewStyle(.linear)
                    .tint(.red)
                    .accessibilityLabel("Input level")
                    .accessibilityValue("\(Int((voice.phase == .recording ? voice.level : 0) * 100)) percent")
                    .accessibilityIdentifier("record-level")
                if voice.phase == .recording {
                    Button {
                        Task { await voice.stop() }
                    } label: {
                        Label("Stop Recording", systemImage: "stop.circle.fill")
                            .font(.title2)
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(.red)
                    .accessibilityIdentifier("record-stop")
                } else {
                    Button {
                        Task { await voice.start(on: date) }
                    } label: {
                        Label(voice.take == nil ? "Start Recording" : "Record Again", systemImage: "mic.circle.fill")
                            .font(.title2)
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(voice.phase == .starting || voice.phase == .finishing || voice.phase == .transcribing)
                    .accessibilityHint("Asks for the microphone the first time.")
                    .accessibilityIdentifier("record-start")
                }
            }
            .padding(.vertical, 8)
        } header: {
            Text(DayText.title(date)).textCase(nil)
        } footer: {
            Text("The recording and its transcript stay on this iPhone, outside the journal.")
        }
    }

    @ViewBuilder
    private var outcome: some View {
        switch voice.phase {
        case .finishing:
            Section { ProgressView("Keeping the recording…") }
        case .transcribing:
            Section { ProgressView("Transcribing…").accessibilityIdentifier("record-transcribing") }
        case .waitingForModel:
            Section {
                Label("Waiting for speech model", systemImage: "arrow.down.circle")
                    .accessibilityIdentifier("record-waiting-model")
            } footer: {
                Text("The recording is kept. It is transcribed once the speech model for \(voice.language.name) is on this iPhone.")
            }
        case .transcribed:
            if let take = voice.take, let transcript = take.transcript {
                Section("Transcript") {
                    Text(transcript.isEmpty ? "Nothing was heard." : transcript)
                        .textSelection(.enabled)
                        .accessibilityIdentifier("record-transcript")
                }
                if !transcript.isEmpty {
                    Section {
                        NavigationLink {
                            ProposalView(voice: voice, take: take, date: date, session: session) {
                                filled()
                                dismiss()
                            }
                        } label: {
                            Label(VoiceText.sortLabel(voice), systemImage: "list.bullet.rectangle")
                        }
                        .accessibilityIdentifier("record-propose")
                    } footer: {
                        Text("You check every answer before it goes into the form.")
                    }
                }
            }
        case .failed(let message):
            Section {
                Label(message, systemImage: "exclamationmark.triangle")
                    .foregroundStyle(.red)
                    .accessibilityIdentifier("record-failed")
                if let take = voice.take, take.state != .transcribed {
                    Button("Try Again") { Task { await voice.transcribe(take) } }
                        .accessibilityIdentifier("record-retry")
                }
            } footer: {
                if voice.take != nil {
                    Text("The recording is kept on this iPhone and listed on Today until it is transcribed.")
                }
            }
        case .idle, .starting, .recording, .denied:
            EmptyView()
        }
    }

    static func clock(_ seconds: Double) -> String {
        let whole = Int(seconds.rounded(.down))
        return String(format: "%d:%02d", whole / 60, whole % 60)
    }

    static func spoken(_ seconds: Double) -> String {
        Duration.seconds(Int(seconds.rounded(.down)))
            .formatted(.units(allowed: [.minutes, .seconds], width: .wide))
    }
}

/// Takes on this iPhone still to be done with: those with no transcript
/// yet, each with another try, and those transcribed whose answers are not
/// in a draft yet. Each opens on its transcript and its answers.
struct PendingTakes: View {
    let voice: VoiceCapture

    var body: some View {
        if !voice.pending.isEmpty {
            Section {
                ForEach(voice.pending) { take in
                    VStack(alignment: .leading, spacing: 6) {
                        NavigationLink {
                            TakeView(voice: voice, id: take.id)
                        } label: {
                            VStack(alignment: .leading, spacing: 6) {
                                Text("\(DayText.title(take.date)), \(RecordView.spoken(take.durationSeconds))")
                                Text(Self.state(take))
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                        }
                        .accessibilityIdentifier("take-open-\(take.id)")
                        if take.state != .transcribed {
                            Button(take.state == .failed ? "Try Again" : "Transcribe", systemImage: "text.bubble") {
                                Task { await voice.transcribe(take) }
                            }
                            .buttonStyle(.borderless)
                            .disabled(voice.transcribing.contains(take.id))
                            .accessibilityIdentifier("take-transcribe-\(take.id)")
                        }
                    }
                    .accessibilityIdentifier("take-\(take.id)")
                }
            } header: {
                Text("Recordings")
            } footer: {
                Text("Kept on this iPhone. Open one to read its transcript and sort it into your questions.")
            }
        }
    }

    static func state(_ take: SpokenTake) -> String {
        switch take.state {
        case .waitingForModel: return "Waiting for speech model"
        case .failed: return take.failure ?? "Transcription failed."
        case .untranscribed: return take.recovered ? "Recovered after the app stopped. Not transcribed yet." : "Not transcribed yet."
        case .transcribed: return "Transcribed. Not in your answers yet."
        }
    }
}

/// One take kept on this iPhone, however long ago it was spoken: its
/// transcript, another try when it has none, and the way to sort it into
/// its day's questions. Filling the answers opens the day's form, where
/// they are saved as typed answers are.
struct TakeView: View {
    @Environment(AppRouter.self) private var router
    let voice: VoiceCapture
    let id: String

    /// The take as it stands now: transcribing or deleting changes it.
    private var take: SpokenTake? { voice.takes.first { $0.id == id } }

    var body: some View {
        Form {
            if let take {
                Section {
                    LabeledContent("Length", value: VoiceText.duration(take.durationSeconds))
                    Text(take.state == .transcribed ? VoiceText.state(take) : PendingTakes.state(take))
                        .foregroundStyle(.secondary)
                        .accessibilityIdentifier("take-state")
                } header: {
                    Text(DayText.title(take.date)).textCase(nil)
                }
                if take.state == .transcribed, let transcript = take.transcript {
                    Section("Transcript") {
                        Text(transcript.isEmpty ? "Nothing was heard." : transcript)
                            .textSelection(.enabled)
                            .accessibilityIdentifier("take-transcript")
                    }
                    if !transcript.isEmpty {
                        Section {
                            NavigationLink {
                                ProposalView(voice: voice, take: take, date: take.date, session: nil) {
                                    router.write(take.date)
                                }
                            } label: {
                                Label(VoiceText.sortLabel(voice), systemImage: "list.bullet.rectangle")
                            }
                            .accessibilityIdentifier("take-propose")
                        } footer: {
                            Text("You check every answer before it goes into the form.")
                        }
                    }
                } else {
                    Section {
                        if voice.transcribing.contains(take.id) {
                            ProgressView("Transcribing…")
                        } else {
                            Button(take.state == .failed ? "Try Again" : "Transcribe", systemImage: "text.bubble") {
                                Task { await voice.transcribe(take) }
                            }
                            .accessibilityIdentifier("take-view-transcribe")
                        }
                    } footer: {
                        Text("The recording is kept on this iPhone until you delete it.")
                    }
                }
            } else {
                Text("This recording is no longer on this iPhone.")
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("take-gone")
            }
        }
        .navigationTitle("Recording")
        .navigationBarTitleDisplayMode(.inline)
    }
}

/// Where the speech model of the journal's language stands, and the way
/// to fetch it ahead of the first take.
struct SpeechSettings: View {
    let voice: VoiceCapture

    var body: some View {
        Section {
            Picker("Language", selection: Binding(get: { voice.language }, set: { voice.setLanguage($0) })) {
                ForEach(SpeechLanguage.allCases) { Text($0.name).tag($0) }
            }
            .accessibilityIdentifier("speech-language")
            LabeledContent("Speech model", value: modelLine)
                .accessibilityIdentifier("speech-model-state")
            Button("Prepare Speech Model", systemImage: "arrow.down.circle") { voice.prepareModel() }
                .disabled(voice.preparingModel || voice.modelState == .ready || voice.modelState == .unsupported)
                .accessibilityIdentifier("prepare-speech-model")
        } header: {
            Text("Transcription")
        } footer: {
            Text("Spoken reflections are transcribed on this iPhone. The model downloads once per language.")
        }
        .task { await voice.refreshModelState() }
    }

    private var modelLine: String {
        if voice.preparingModel {
            if let fraction = voice.modelProgress { return "Downloading, \(Int(fraction * 100))%" }
            return "Preparing…"
        }
        switch voice.modelState {
        case .ready: return "Ready"
        case .downloading: return "Downloading"
        case .notDownloaded: return "Not downloaded"
        case .unsupported: return "Not available on this iPhone"
        case nil: return "Checking…"
        }
    }
}
