import CaptureCore
import Foundation
import Testing

/// Seneca's own: a host that names its files `<uuid>.m4a` in a directory of
/// its choosing, under the store's root, keeps the store's promotion rules.
@Suite("Capture host layout")
struct HostLayoutTests {
    @Test("A host-named file's partial sits beside it")
    func partialBesideHostName() {
        let url = URL(fileURLWithPath: "/tmp/audio/2026-10-14/6f1c2e40.m4a")
        #expect(AudioFileStore.partialURL(forFinalURL: url).lastPathComponent == "6f1c2e40.partial.m4a")
        #expect(AudioFileStore.partialURL(forFinalURL: url).deletingLastPathComponent() == url.deletingLastPathComponent())
    }

    @Test("Finalizing a host-named file promotes its partial by path")
    func hostLayoutFinalizes() throws {
        let sandbox = CaptureSandbox()
        defer { sandbox.destroy() }

        let day = sandbox.root.appendingPathComponent("2026-10-14", isDirectory: true)
        try FileManager.default.createDirectory(at: day, withIntermediateDirectories: true)
        let final = day.appendingPathComponent("6f1c2e40-9a3b-4d5e-8f70-1122334455aa.m4a")
        try Data("abc".utf8).write(to: AudioFileStore.partialURL(forFinalURL: final))

        let stored = try sandbox.store.finalize(finalURL: final)
        #expect(stored.byteCount == 3)
        #expect(stored.relativePath == "2026-10-14/6f1c2e40-9a3b-4d5e-8f70-1122334455aa.m4a")
        #expect(sandbox.exists(final))
        #expect(!sandbox.exists(AudioFileStore.partialURL(forFinalURL: final)))
        // Idempotent, as for an entry directory.
        #expect(try sandbox.store.finalize(finalURL: final).sha256 == stored.sha256)
    }

    @Test("A zero-byte host partial is refused and a file outside the root is too")
    func hostLayoutRefuses() throws {
        let sandbox = CaptureSandbox()
        defer { sandbox.destroy() }

        let final = sandbox.root.appendingPathComponent("2026-10-14/empty.m4a")
        try FileManager.default.createDirectory(at: final.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data().write(to: AudioFileStore.partialURL(forFinalURL: final))
        #expect(throws: AudioStoreError.self) { try sandbox.store.finalize(finalURL: final) }
        #expect(!sandbox.exists(final))

        let outside = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("elsewhere-\(UUID()).m4a")
        #expect(throws: AudioStoreError.self) { try sandbox.store.finalize(finalURL: outside) }
    }
}
