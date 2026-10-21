@preconcurrency import AVFoundation
import CoreMedia
import Foundation
import CaptureCore

/// Encodes 48 kHz mono float PCM to AAC in a fragmented MPEG-4 file.
///
/// Every `fragmentInterval` of media the writer appends a self-contained movie
/// fragment, so a process killed mid-recording leaves a file that plays up to
/// its last complete fragment. That bound is the whole reason this type exists
/// instead of `AVAudioRecorder`, whose file has no index until it is stopped.
///
/// Not thread-safe: the caller confines every call to one serial queue. It has
/// no audio session or engine dependency, so tests can feed it synthetic PCM.
public final class FragmentedAudioFileWriter {
    public static let fragmentInterval = CMTime(value: 1, timescale: 1)

    /// The PCM layout `append` accepts. The adapter converts hardware input to
    /// this before it reaches the writer.
    public let pcmFormat: AVAudioFormat
    private let writer: AVAssetWriter
    private let input: AVAssetWriterInput
    public private(set) var framesWritten: Int64 = 0

    /// Encoder settings for the configured AAC-LC stream. Kept separate so the
    /// one thing that silently changes the archive format can be asserted on.
    public static func settings(for configuration: RecordingConfiguration) -> [String: Any] {
        [
            AVFormatIDKey: Int(kAudioFormatMPEG4AAC),
            AVSampleRateKey: configuration.sampleRate,
            AVNumberOfChannelsKey: configuration.channelCount,
            AVEncoderBitRateKey: configuration.bitRate,
        ]
    }

    /// Creates the file and starts the writer session. `url` must not exist.
    public init(url: URL, configuration: RecordingConfiguration) throws {
        guard let pcmFormat = AVAudioFormat(
            commonFormat: .pcmFormatFloat32,
            sampleRate: configuration.sampleRate,
            channels: AVAudioChannelCount(configuration.channelCount),
            interleaved: false
        ) else {
            throw RecordingError.sessionUnavailable("unsupported PCM format")
        }

        // `.m4a` keeps the archive's file type; it accepts movie fragments the
        // same way `.mp4` does (checked on a Mac).
        let writer = try AVAssetWriter(outputURL: url, fileType: .m4a)
        let settings = Self.settings(for: configuration)
        guard writer.canApply(outputSettings: settings, forMediaType: .audio) else {
            throw RecordingError.sessionUnavailable("AAC settings unavailable")
        }
        let input = AVAssetWriterInput(mediaType: .audio, outputSettings: settings)
        input.expectsMediaDataInRealTime = true
        guard writer.canAdd(input) else {
            throw RecordingError.sessionUnavailable("audio writer input unavailable")
        }
        writer.add(input)
        writer.movieFragmentInterval = Self.fragmentInterval
        writer.initialMovieFragmentInterval = Self.fragmentInterval

        guard writer.startWriting() else {
            throw RecordingError.sessionUnavailable(
                "writer failed to start: \(String(describing: writer.error))"
            )
        }
        writer.startSession(atSourceTime: .zero)

        self.pcmFormat = pcmFormat
        self.writer = writer
        self.input = input
    }

    /// Appends one buffer in `pcmFormat`, timestamped by the frames already
    /// written so the timeline has no gaps. Throws instead of dropping audio
    /// when the encoder cannot keep up.
    public func append(_ buffer: AVAudioPCMBuffer) throws {
        guard buffer.frameLength > 0 else { return }
        guard writer.status == .writing, input.isReadyForMoreMediaData else {
            throw RecordingError.sessionUnavailable(
                "writer not accepting audio: \(String(describing: writer.error))"
            )
        }
        guard input.append(try sampleBuffer(for: buffer)) else {
            throw RecordingError.sessionUnavailable(
                "writer append failed: \(String(describing: writer.error))"
            )
        }
        framesWritten += Int64(buffer.frameLength)
    }

    /// Closes the file. A writer that already failed is left as it is: its
    /// fragments are still on disk, and recovery decides whether they play.
    public func finish(completion: @escaping @Sendable (Result<Void, RecordingError>) -> Void) {
        guard writer.status == .writing else {
            completion(.failure(.sessionUnavailable(
                "writer not writing: \(String(describing: writer.error))"
            )))
            return
        }
        input.markAsFinished()
        // The completion only reads the final status, after the writer has
        // stopped accepting calls from the owning queue.
        nonisolated(unsafe) let writer = writer
        writer.finishWriting {
            if writer.status == .completed {
                completion(.success(()))
            } else {
                completion(.failure(.sessionUnavailable(
                    "writer did not complete: \(String(describing: writer.error))"
                )))
            }
        }
    }

    private func sampleBuffer(for buffer: AVAudioPCMBuffer) throws -> CMSampleBuffer {
        let frames = Int(buffer.frameLength)
        let byteCount = frames * MemoryLayout<Float>.size
        guard buffer.format == pcmFormat, let samples = buffer.floatChannelData?[0] else {
            throw RecordingError.sessionUnavailable("PCM buffer is not in the writer's format")
        }

        var block: CMBlockBuffer?
        var status = CMBlockBufferCreateWithMemoryBlock(
            allocator: kCFAllocatorDefault,
            memoryBlock: nil,
            blockLength: byteCount,
            blockAllocator: kCFAllocatorDefault,
            customBlockSource: nil,
            offsetToData: 0,
            dataLength: byteCount,
            flags: 0,
            blockBufferOut: &block
        )
        guard status == noErr, let block else {
            throw RecordingError.sessionUnavailable("PCM block allocation failed (\(status))")
        }
        status = CMBlockBufferReplaceDataBytes(
            with: samples,
            blockBuffer: block,
            offsetIntoDestination: 0,
            dataLength: byteCount
        )
        guard status == noErr else {
            throw RecordingError.sessionUnavailable("PCM block copy failed (\(status))")
        }

        let timescale = CMTimeScale(pcmFormat.sampleRate)
        var timing = CMSampleTimingInfo(
            duration: CMTime(value: 1, timescale: timescale),
            presentationTimeStamp: CMTime(value: framesWritten, timescale: timescale),
            decodeTimeStamp: .invalid
        )
        var sample: CMSampleBuffer?
        status = CMSampleBufferCreateReady(
            allocator: kCFAllocatorDefault,
            dataBuffer: block,
            formatDescription: pcmFormat.formatDescription,
            sampleCount: frames,
            sampleTimingEntryCount: 1,
            sampleTimingArray: &timing,
            sampleSizeEntryCount: 0,
            sampleSizeArray: nil,
            sampleBufferOut: &sample
        )
        guard status == noErr, let sample else {
            throw RecordingError.sessionUnavailable("PCM sample creation failed (\(status))")
        }
        return sample
    }
}
