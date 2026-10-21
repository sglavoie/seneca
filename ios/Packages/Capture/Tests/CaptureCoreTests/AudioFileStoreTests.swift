import CaptureCore
import Foundation
import Testing

@Suite("Capture audio file store")
struct CaptureAudioFileStoreTests {
    private let entryID = EntryID(string: "6f1c2e40-9a3b-4d5e-8f70-1122334455aa")!

    @Test("Audio lives under the entry id, outside the database")
    func layout() {
        let sandbox = CaptureSandbox()
        defer { sandbox.destroy() }

        let store = sandbox.store
        #expect(store.finalURL(for: entryID).lastPathComponent == "audio.m4a")
        #expect(store.partialURL(for: entryID).lastPathComponent == "audio.partial.m4a")
        #expect(
            store.relativePath(for: entryID)
                == "Entries/6f1c2e40-9a3b-4d5e-8f70-1122334455aa/audio.m4a"
        )
        #expect(store.resolve(relativePath: store.relativePath(for: entryID))
            == store.finalURL(for: entryID))
        #expect(AudioFileStore.entryID(forAudioURL: store.finalURL(for: entryID)) == entryID)
    }

    @Test("Finalizing promotes the partial and reports size and hash")
    func finalizePromotes() throws {
        let sandbox = CaptureSandbox()
        defer { sandbox.destroy() }

        try sandbox.writePartial(Data("abc".utf8), for: entryID)
        let stored = try sandbox.store.finalize(entryID: entryID)

        #expect(stored.byteCount == 3)
        #expect(stored.sha256
            == "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
        #expect(stored.relativePath == sandbox.store.relativePath(for: entryID))
        #expect(sandbox.exists(sandbox.store.finalURL(for: entryID)))
        #expect(!sandbox.exists(sandbox.store.partialURL(for: entryID)))
    }

    @Test("A zero-byte partial is never promoted to a finished recording")
    func zeroByteRefused() throws {
        let sandbox = CaptureSandbox()
        defer { sandbox.destroy() }

        try sandbox.writePartial(Data(), for: entryID)
        #expect(throws: AudioStoreError.self) {
            try sandbox.store.finalize(entryID: entryID)
        }
        #expect(!sandbox.exists(sandbox.store.finalURL(for: entryID)))
    }

    @Test("Finalizing twice is a no-op rather than an error")
    func finalizeIsIdempotent() throws {
        let sandbox = CaptureSandbox()
        defer { sandbox.destroy() }

        try sandbox.writePartial(Data("abc".utf8), for: entryID)
        let first = try sandbox.store.finalize(entryID: entryID)
        let second = try sandbox.store.finalize(entryID: entryID)
        #expect(first == second)
    }

    @Test("Finalizing an entry with no audio at all fails")
    func finalizeMissing() {
        let sandbox = CaptureSandbox()
        defer { sandbox.destroy() }

        #expect(throws: AudioStoreError.self) {
            try sandbox.store.finalize(entryID: entryID)
        }
    }

    @Test("Only directories holding real audio count as interrupted")
    func interruptedScan() throws {
        let sandbox = CaptureSandbox()
        defer { sandbox.destroy() }

        let withPartial = EntryID(string: "00000000-0000-4000-8000-000000000001")!
        let withFinal = EntryID(string: "00000000-0000-4000-8000-000000000002")!
        let empty = EntryID(string: "00000000-0000-4000-8000-000000000003")!
        let bare = EntryID(string: "00000000-0000-4000-8000-000000000004")!

        try sandbox.writePartial(Data("a".utf8), for: withPartial)
        try sandbox.writeFinal(Data("b".utf8), for: withFinal)
        try sandbox.writePartial(Data(), for: empty)
        try sandbox.store.prepareDirectory(for: bare)

        #expect(try sandbox.store.interruptedEntryIDs() == [withPartial, withFinal])
    }

    @Test("Discarding only ever removes an empty partial")
    func discardKeepsAudio() throws {
        let sandbox = CaptureSandbox()
        defer { sandbox.destroy() }

        try sandbox.writePartial(Data(), for: entryID)
        try sandbox.store.discardEmptyPartial(for: entryID)
        #expect(!sandbox.exists(sandbox.store.partialURL(for: entryID)))

        try sandbox.writePartial(Data("keep me".utf8), for: entryID)
        try sandbox.store.discardEmptyPartial(for: entryID)
        #expect(sandbox.exists(sandbox.store.partialURL(for: entryID)))
    }

    @Test("Recording is refused when the volume cannot hold it")
    func capacityGuard() {
        let sandbox = CaptureSandbox()
        defer { sandbox.destroy() }

        #expect(throws: AudioStoreError.self) {
            try sandbox.store.requireCapacity(minimumBytes: .max)
        }
        #expect(throws: Never.self) {
            try sandbox.store.requireCapacity(minimumBytes: 1)
        }
    }
}
