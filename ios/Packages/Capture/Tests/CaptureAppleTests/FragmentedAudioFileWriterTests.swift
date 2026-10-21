// Ported from an earlier app's tests. The recorder adapter is iOS-only, so these run on a simulator.
#if os(iOS)
import AVFoundation
import Foundation
import CaptureCore
import Testing

@testable import CaptureApple

/// Feeds synthetic PCM through the production writer and decodes what lands on
/// disk. "Abandoned" cases copy the file while the writer is still open, which
/// is what a killed process leaves behind.
@Suite("Fragmented audio writer", .serialized)
struct FragmentedAudioFileWriterTests {
    private static let framesPerBuffer: AVAudioFrameCount = 960

    @Test("A finished file decodes its full duration as 48 kHz mono AAC")
    func normalFinish() async throws {
        let directory = try Self.makeDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let url = directory.appendingPathComponent("audio.m4a")

        let writer = try FragmentedAudioFileWriter(url: url, configuration: .standard)
        try Self.append(seconds: 1.5, to: writer)
        try await Self.finish(writer)

        let asset = AVURLAsset(url: url)
        let track = try #require(try await asset.loadTracks(withMediaType: .audio).first)
        let description = try #require(try await track.load(.formatDescriptions).first)
        let stream = try #require(CMAudioFormatDescriptionGetStreamBasicDescription(description)?.pointee)
        #expect(stream.mFormatID == kAudioFormatMPEG4AAC)
        #expect(stream.mSampleRate == 48_000)
        #expect(stream.mChannelsPerFrame == 1)
        #expect(abs(try await asset.load(.duration).seconds - 1.5) < 0.1)
    }

    @Test("A file abandoned after 2.5 s still plays at least 1.4 s")
    func abandonedAfterFragments() async throws {
        let directory = try Self.makeDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let url = directory.appendingPathComponent("audio.partial.m4a")

        let writer = try FragmentedAudioFileWriter(url: url, configuration: .standard)
        try Self.append(seconds: 2.5, to: writer)
        let snapshot = try Self.snapshot(url, in: directory)

        let seconds = try await Self.adapter(directory).duration(of: snapshot)
        #expect(seconds >= 1.4)
        withExtendedLifetime(writer) {}
    }

    @Test("A file abandoned before its first fragment is reported unplayable")
    func abandonedBeforeFirstFragment() async throws {
        let directory = try Self.makeDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let url = directory.appendingPathComponent("audio.partial.m4a")

        let writer = try FragmentedAudioFileWriter(url: url, configuration: .standard)
        try Self.append(seconds: 0.4, to: writer)
        let snapshot = try Self.snapshot(url, in: directory)

        await #expect(throws: (any Error).self) {
            _ = try await Self.adapter(directory).duration(of: snapshot)
        }
        withExtendedLifetime(writer) {}
    }

    // MARK: - Helpers

    /// Appends a tone at roughly twice real time; the writer expects live media
    /// and refuses buffers it is not ready for.
    private static func append(seconds: Double, to writer: FragmentedAudioFileWriter) throws {
        let total = Int64(seconds * writer.pcmFormat.sampleRate)
        while writer.framesWritten < total {
            let buffer = try #require(AVAudioPCMBuffer(
                pcmFormat: writer.pcmFormat,
                frameCapacity: framesPerBuffer
            ))
            buffer.frameLength = framesPerBuffer
            let samples = try #require(buffer.floatChannelData?[0])
            for index in 0 ..< Int(framesPerBuffer) {
                let frame = Float(writer.framesWritten + Int64(index))
                samples[index] = sin(frame * 0.05) * 0.3
            }
            try writer.append(buffer)
            Thread.sleep(forTimeInterval: 0.01)
        }
    }

    private static func finish(_ writer: FragmentedAudioFileWriter) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, any Error>) in
            writer.finish { continuation.resume(with: $0) }
        }
    }

    private static func snapshot(_ url: URL, in directory: URL) throws -> URL {
        let copy = directory.appendingPathComponent("snapshot.m4a")
        try FileManager.default.copyItem(at: url, to: copy)
        return copy
    }

    private static func adapter(_ directory: URL) -> AudioRecorderAdapter {
        AudioRecorderAdapter(store: AudioFileStore(applicationSupportRoot: directory))
    }

    private static func makeDirectory() throws -> URL {
        let url = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("capture-writer-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        return url
    }
}

#endif
