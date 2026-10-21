#if os(iOS)
@preconcurrency import AVFoundation
import Foundation
import CaptureCore
import OSLog

/// The real capture device.
///
/// Records through `AVAudioEngine` into a `FragmentedAudioFileWriter` at
/// `audio.partial.m4a`, and promotes it through `AudioFileStore` on stop, so the
/// destination path only ever holds a closed file. A process killed mid-recording
/// leaves a partial that plays up to its last one-second fragment.
///
/// Interruptions end the recording rather than pausing it: the coordinator saves
/// what was captured, and a later tap starts a separate recording. They
/// are reported to `onInterruption` rather than handled here, because deciding
/// what a half-finished recording means needs the database, which this type
/// deliberately cannot see.
public actor AudioRecorderAdapter: AudioRecording {
    private static let logger = Logger(
        subsystem: Bundle.main.bundleIdentifier ?? "CaptureApple",
        category: "Capture"
    )
    private let store: AudioFileStore
    private var capture: LiveAudioCapture?
    private var destination: URL?
    private var observers: [any NSObjectProtocol] = []
    private var onInterruption: (@Sendable (CaptureFailure) -> Void)?

    public init(store: AudioFileStore) {
        self.store = store
    }

    public func setInterruptionHandler(_ handler: @escaping @Sendable (CaptureFailure) -> Void) {
        onInterruption = handler
    }

    public func startRecording(to destination: URL, configuration: RecordingConfiguration) async throws {
        guard capture == nil else {
            throw RecordingError.sessionUnavailable("a recording is already running")
        }
        try await requireMicrophonePermission()

        let session = AVAudioSession.sharedInstance()
        do {
            // `.spokenAudio` keeps the system from applying music-oriented
            // processing to a speech recording.
            try session.setCategory(
                .playAndRecord,
                mode: .spokenAudio,
                options: [.allowBluetoothHFP, .defaultToSpeaker]
            )
            try session.setActive(true)
        } catch {
            throw RecordingError.sessionUnavailable(error.localizedDescription)
        }

        let capture: LiveAudioCapture
        do {
            capture = try LiveAudioCapture(
                url: AudioFileStore.partialURL(forFinalURL: destination),
                configuration: configuration
            ) { [weak self] in
                Task { await self?.reportInterruption(.finalizationFailed) }
            }
            try capture.start()
        } catch {
            deactivateSession()
            throw error as? RecordingError ?? .sessionUnavailable(error.localizedDescription)
        }

        self.capture = capture
        self.destination = destination
        observeSessionEvents(engine: capture.engine)
        protectPartial(at: AudioFileStore.partialURL(forFinalURL: destination))
    }

    /// The partial normally inherits its directory's class. Setting it again
    /// covers a writer that replaced the file rather than creating it in place.
    /// Failure is logged, not fatal: the file still has the default class.
    private func protectPartial(at url: URL) {
        do {
            try store.applyCaptureProtection(to: url)
        } catch {
            Self.logger.error("capture protection not applied: \(error.localizedDescription, privacy: .public)")
        }
        let effective = store.protection(of: url)?.rawValue ?? "unknown"
        Self.logger.notice("capture protection: \(effective, privacy: .public)")
    }

    public func stopRecording() async throws -> RecordedAudio {
        guard let capture, let destination else {
            throw RecordingError.noActiveRecording
        }
        teardown()

        let frames = try await capture.finish().get()
        let stored = try store.finalize(finalURL: destination)
        let finished = store.protection(of: stored.url)?.rawValue ?? "unknown"
        Self.logger.notice("finished protection: \(finished, privacy: .public)")
        let measured = (try? await duration(of: stored.url)) ?? 0

        return RecordedAudio(
            url: stored.url,
            durationSeconds: max(Double(frames) / capture.sampleRate, measured),
            byteCount: stored.byteCount,
            sha256: stored.sha256
        )
    }

    /// Stops and closes the file without promoting it. What was captured stays
    /// at the partial path for the coordinator to verify and keep.
    public func cancelRecording() async {
        guard let capture else { return }
        teardown()
        _ = await capture.finish()
    }

    /// Throws unless the file opens with an audio track, so recovery never
    /// counts an index-less file from a killed recorder as a recording.
    public func duration(of url: URL) async throws -> Double {
        let asset = AVURLAsset(url: url)
        guard try await !asset.loadTracks(withMediaType: .audio).isEmpty else {
            throw RecordingError.emptyRecording
        }
        return try await asset.load(.duration).seconds
    }

    public func meter() async -> RecordingMeter? {
        capture?.meter()
    }

    private func requireMicrophonePermission() async throws {
        switch AVAudioApplication.shared.recordPermission {
        case .granted:
            return
        case .denied:
            throw RecordingError.microphoneDenied
        case .undetermined:
            guard await AVAudioApplication.requestRecordPermission() else {
                throw RecordingError.microphoneDenied
            }
        @unknown default:
            throw RecordingError.microphoneDenied
        }
    }

    private func observeSessionEvents(engine: AVAudioEngine) {
        let center = NotificationCenter.default

        observers.append(center.addObserver(
            forName: AVAudioSession.interruptionNotification,
            object: AVAudioSession.sharedInstance(),
            queue: nil
        ) { [weak self] notification in
            // Only the raw value crosses into the actor; the notification's
            // `userInfo` dictionary is not `Sendable`.
            let typeRaw = notification.userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt
            Task { await self?.handleInterruption(typeRaw: typeRaw) }
        })

        // A route change — headphones unplugged, a Bluetooth microphone gone —
        // stops the engine and may change the input format. Capture moves to the
        // new input and keeps writing the same timeline.
        observers.append(center.addObserver(
            forName: .AVAudioEngineConfigurationChange,
            object: engine,
            queue: nil
        ) { [weak self] _ in
            Task { await self?.handleConfigurationChange() }
        })

        observers.append(center.addObserver(
            forName: AVAudioSession.mediaServicesWereResetNotification,
            object: nil,
            queue: nil
        ) { [weak self] _ in
            Task { await self?.reportInterruption(.audioSessionUnavailable) }
        })
    }

    /// Any interruption ends the recording. The system has already
    /// stopped input; the coordinator keeps what was written before it.
    private func handleInterruption(typeRaw: UInt?) {
        guard capture != nil,
              let typeRaw,
              AVAudioSession.InterruptionType(rawValue: typeRaw) == .began else { return }
        reportInterruption(.interrupted)
    }

    private func handleConfigurationChange() {
        guard let capture else { return }
        do {
            try capture.restartInput()
        } catch {
            reportInterruption(.audioSessionUnavailable)
        }
    }

    private func reportInterruption(_ failure: CaptureFailure) {
        onInterruption?(failure)
    }

    private func teardown() {
        capture = nil
        destination = nil
        for observer in observers {
            NotificationCenter.default.removeObserver(observer)
        }
        observers.removeAll()
        deactivateSession()
    }

    private func deactivateSession() {
        // Letting other audio resume is part of ending a recording cleanly.
        try? AVAudioSession.sharedInstance().setActive(
            false,
            options: [.notifyOthersOnDeactivation]
        )
    }
}

#endif
