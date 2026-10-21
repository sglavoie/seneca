// Ported from an earlier app's tests. The recorder adapter is iOS-only, so these run on a simulator.
#if os(iOS)
import AVFoundation
import Foundation
import CaptureCore
import Testing

@testable import CaptureApple

@Suite("Capture encoder settings")
struct AudioRecorderAdapterTests {
    @Test("The encoder is asked for 48 kHz mono AAC-LC at 128 kbps")
    func standardSettings() {
        let settings = FragmentedAudioFileWriter.settings(for: .standard)

        #expect(settings[AVFormatIDKey] as? Int == Int(kAudioFormatMPEG4AAC))
        #expect(settings[AVSampleRateKey] as? Double == 48_000)
        #expect(settings[AVNumberOfChannelsKey] as? Int == 1)
        #expect(settings[AVEncoderBitRateKey] as? Int == 128_000)
    }

    @Test("Recording writes to the partial sibling of its destination")
    func partialPath() {
        let destination = URL(fileURLWithPath: "/tmp/Entries/abc/audio.m4a")
        #expect(
            AudioFileStore.partialURL(forFinalURL: destination).path
                == "/tmp/Entries/abc/audio.partial.m4a"
        )
    }

    @Test("Silence and full scale map to the ends of the meter range")
    func meterNormalization() {
        #expect(RecordingMeter(durationSeconds: 1, averagePowerDecibels: 0).level == 1)
        #expect(RecordingMeter(durationSeconds: 1, averagePowerDecibels: -60).level == 0)
        #expect(RecordingMeter(durationSeconds: 1, averagePowerDecibels: -120).level == 0)
        #expect(RecordingMeter(durationSeconds: 1, averagePowerDecibels: -30).level == 0.5)
    }

    @Test("Bytes that are not audio are reported as unplayable, not as zero seconds")
    func unplayableFileThrows() async throws {
        let url = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("capture-unplayable-\(UUID().uuidString).m4a")
        try Data("not an mpeg-4 file".utf8).write(to: url)
        defer { try? FileManager.default.removeItem(at: url) }

        let adapter = AudioRecorderAdapter(store: AudioFileStore(
            applicationSupportRoot: url.deletingLastPathComponent()
        ))
        await #expect(throws: (any Error).self) {
            _ = try await adapter.duration(of: url)
        }
    }
}

#endif
