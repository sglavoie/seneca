#if os(iOS)
import Accelerate
@preconcurrency import AVFoundation
import Foundation
import CaptureCore
import Synchronization

/// One running recording: the engine tap, format conversion, and the writer.
///
/// The tap runs on the audio render thread and only copies buffers. Conversion
/// and writing happen on `queue`, which is the only place `writer` and
/// `converter` are touched. Engine start and stop come from `AudioRecorderAdapter`.
final class LiveAudioCapture: @unchecked Sendable {
    /// Roughly one second of 4096-frame tap buffers. Beyond that the writer is
    /// not keeping up, and the recording ends rather than dropping audio.
    private static let maximumPendingBuffers = 12

    private struct Shared {
        var accepting = true
        var failed = false
        var pending = 0
        var framesWritten: Int64 = 0
        var averagePowerDecibels = RecordingMeter.silenceFloorDecibels
    }

    let engine = AVAudioEngine()
    let sampleRate: Double
    private let queue = DispatchQueue(label: "capture.audio.writer", qos: .userInitiated)
    private let shared = Mutex(Shared())
    private let onFailure: @Sendable () -> Void
    private let writer: FragmentedAudioFileWriter
    private var converter: AVAudioConverter?
    private var hasTap = false
    private var finished = false

    init(
        url: URL,
        configuration: RecordingConfiguration,
        onFailure: @escaping @Sendable () -> Void
    ) throws {
        writer = try FragmentedAudioFileWriter(url: url, configuration: configuration)
        sampleRate = configuration.sampleRate
        self.onFailure = onFailure
    }

    func start() throws {
        try connectInput()
        do {
            try engine.start()
        } catch {
            removeTap()
            throw RecordingError.sessionUnavailable(error.localizedDescription)
        }
    }

    /// Reattaches to whatever input the system now routes, after a
    /// configuration change stopped the engine.
    func restartInput() throws {
        engine.stop()
        removeTap()
        try connectInput()
        try engine.start()
    }

    /// Stops input, writes every queued buffer, and closes the file. Returns
    /// the frames written. Safe to call more than once.
    func finish() async -> Result<Int64, RecordingError> {
        engine.stop()
        removeTap()
        shared.withLock { $0.accepting = false }

        return await withCheckedContinuation { continuation in
            queue.async { [self] in
                guard !finished else {
                    continuation.resume(returning: .failure(.noActiveRecording))
                    return
                }
                finished = true
                if !shared.withLock({ $0.failed }) {
                    try? flushConverter()
                }
                let frames = writer.framesWritten
                writer.finish { result in
                    continuation.resume(returning: result.map { frames })
                }
            }
        }
    }

    func meter() -> RecordingMeter {
        let (frames, power) = shared.withLock { ($0.framesWritten, $0.averagePowerDecibels) }
        return RecordingMeter(
            durationSeconds: Double(frames) / sampleRate,
            averagePowerDecibels: power
        )
    }

    // MARK: - Input

    private func connectInput() throws {
        let input = engine.inputNode.outputFormat(forBus: 0)
        guard input.sampleRate > 0, input.channelCount > 0,
              let converter = AVAudioConverter(from: input, to: writer.pcmFormat) else {
            throw RecordingError.sessionUnavailable("no usable audio input")
        }
        // Frames the old converter still holds belong before the new input's.
        try queue.sync {
            try flushConverter()
            self.converter = converter
        }
        engine.inputNode.installTap(onBus: 0, bufferSize: 4096, format: input) { [weak self] buffer, _ in
            self?.receive(buffer)
        }
        hasTap = true
    }

    private func removeTap() {
        guard hasTap else { return }
        engine.inputNode.removeTap(onBus: 0)
        hasTap = false
    }

    /// Render thread: copy and hand off. Never blocks, never touches the writer.
    private func receive(_ buffer: AVAudioPCMBuffer) {
        let admitted = shared.withLock { state -> Bool in
            guard state.accepting, state.pending < Self.maximumPendingBuffers else { return false }
            state.pending += 1
            return true
        }
        guard admitted else {
            if shared.withLock({ $0.accepting }) { fail() }
            return
        }
        guard let copy = Self.copy(buffer) else {
            shared.withLock { $0.pending -= 1 }
            fail()
            return
        }
        queue.async { [self] in
            defer { shared.withLock { $0.pending -= 1 } }
            guard !finished, !shared.withLock({ $0.failed }) else { return }
            do {
                try write(convert(copy))
            } catch {
                fail()
            }
        }
    }

    // MARK: - Writer queue

    private func convert(_ input: AVAudioPCMBuffer?) throws -> AVAudioPCMBuffer? {
        guard let converter else { return nil }
        let ratio = writer.pcmFormat.sampleRate / converter.inputFormat.sampleRate
        let inputFrames = Double(input?.frameLength ?? 0)
        let capacity = AVAudioFrameCount((inputFrames * ratio).rounded(.up)) + 1024
        guard let output = AVAudioPCMBuffer(pcmFormat: writer.pcmFormat, frameCapacity: capacity) else {
            throw RecordingError.sessionUnavailable("conversion buffer allocation failed")
        }

        let feed = ConverterFeed(input)
        var error: NSError?
        let status = converter.convert(to: output, error: &error) { _, inputStatus in
            feed.next(inputStatus)
        }
        if status == .error || error != nil {
            throw RecordingError.sessionUnavailable(
                "conversion failed: \(String(describing: error))"
            )
        }
        return output
    }

    private func flushConverter() throws {
        guard converter != nil else { return }
        try write(convert(nil))
        converter = nil
    }

    private func write(_ buffer: AVAudioPCMBuffer?) throws {
        guard let buffer, buffer.frameLength > 0 else { return }
        try writer.append(buffer)
        let power = Self.averagePowerDecibels(buffer)
        let frames = writer.framesWritten
        shared.withLock {
            $0.framesWritten = frames
            $0.averagePowerDecibels = power
        }
    }

    /// Stops accepting input and reports once. Written fragments stay on disk.
    private func fail() {
        let first = shared.withLock { state -> Bool in
            guard !state.failed else { return false }
            state.failed = true
            state.accepting = false
            return true
        }
        if first { onFailure() }
    }

    private static func copy(_ source: AVAudioPCMBuffer) -> AVAudioPCMBuffer? {
        guard let copy = AVAudioPCMBuffer(pcmFormat: source.format, frameCapacity: source.frameLength) else {
            return nil
        }
        copy.frameLength = source.frameLength
        let input = UnsafeMutableAudioBufferListPointer(UnsafeMutablePointer(mutating: source.audioBufferList))
        let output = UnsafeMutableAudioBufferListPointer(copy.mutableAudioBufferList)
        guard input.count == output.count else { return nil }
        for index in input.indices {
            guard let from = input[index].mData, let to = output[index].mData,
                  input[index].mDataByteSize <= output[index].mDataByteSize else { return nil }
            memcpy(to, from, Int(input[index].mDataByteSize))
            output[index].mDataByteSize = input[index].mDataByteSize
        }
        return copy
    }

    private static func averagePowerDecibels(_ buffer: AVAudioPCMBuffer) -> Float {
        guard let samples = buffer.floatChannelData?[0] else {
            return RecordingMeter.silenceFloorDecibels
        }
        var rms: Float = 0
        vDSP_rmsqv(samples, 1, &rms, vDSP_Length(buffer.frameLength))
        return rms > 0 ? 20 * log10(rms) : RecordingMeter.silenceFloorDecibels
    }
}

/// Hands one buffer to `AVAudioConverter` per call, or signals end of stream
/// when there is none so a sample-rate converter releases the frames it holds.
private final class ConverterFeed: @unchecked Sendable {
    private let buffer: AVAudioPCMBuffer?
    private var supplied = false

    init(_ buffer: AVAudioPCMBuffer?) {
        self.buffer = buffer
    }

    func next(_ status: UnsafeMutablePointer<AVAudioConverterInputStatus>) -> AVAudioBuffer? {
        guard let buffer else {
            status.pointee = .endOfStream
            return nil
        }
        guard !supplied else {
            status.pointee = .noDataNow
            return nil
        }
        supplied = true
        status.pointee = .haveData
        return buffer
    }
}

#endif
