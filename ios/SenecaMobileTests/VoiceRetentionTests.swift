import CaptureCore
import CryptoKit
import SenecaEngine
import XCTest
@testable import SenecaMobile

/// What is kept of a spoken take: kept or deleted once its answers are
/// saved, deleted by hand with nothing else, carried in a recovery kit as an
/// `audio/` folder beside an unchanged engine export and put back from it,
/// and counted in diagnostics without a word of what was said.
@MainActor
final class VoiceRetentionTests: XCTestCase {
    private var roundTrip: RoundTrip!
    private let spoken = "Synthetic: I kept my word and walked after dinner."

    override func setUp() async throws {
        roundTrip = try RoundTrip.load()
    }

    /// The app over a seeded journal under `root`, its takes recorded and
    /// transcribed by the simulated devices.
    private func launch(_ location: JournalLocation, file: StaticString = #filePath, line: UInt = #line) async -> JournalModel {
        let spoken = spoken
        let model = JournalModel(
            engine: SenecaEngine(), location: { location },
            reminderCenter: FakeReminderCenter(.allowed),
            voiceDevices: { root in
                let recorder = SimulatedAudioRecorder(store: AudioFileStore(applicationSupportRoot: root))
                return VoiceDevices(
                    recorder: recorder,
                    transcriber: SimulatedTranscriber(text: spoken, provider: .speechTranscriber),
                    models: ReadyModels(), protectedData: AlwaysAvailableProtectedData(),
                    observeInterruptions: { handler in await recorder.setInterruptionHandler(handler) }
                )
            }
        )
        await model.open()
        XCTAssertEqual(model.phase, .ready, file: file, line: line)
        return model
    }

    private func record(_ voice: VoiceCapture, on date: String) async throws -> SpokenTake {
        await voice.start(on: date)
        await voice.stop()
        XCTAssertEqual(voice.phase, .transcribed)
        let take = try XCTUnwrap(voice.take)
        voice.reset()
        return take
    }

    /// Every regular file under `root`, relative, with its bytes.
    private func files(under root: URL) -> [String: Data] {
        var found: [String: Data] = [:]
        let base = root.standardizedFileURL.path
        let walker = FileManager.default.enumerator(at: root, includingPropertiesForKeys: [.isRegularFileKey])
        while let file = walker?.nextObject() as? URL {
            if (try? file.resourceValues(forKeys: [.isRegularFileKey]).isRegularFile) == true {
                found[String(file.standardizedFileURL.path.dropFirst(base.count + 1))] = try? Data(contentsOf: file)
            }
        }
        return found
    }

    func testTakesAreKeptOrDeletedOnceTheirAnswersAreSavedAsTheAuthorChooses() async throws {
        await reportingOnMain {
            let root = try scratchDirectory(self)
            let location = try roundTrip.seed(in: root)
            let model = await launch(location)
            let voice = try XCTUnwrap(model.voice)
            let day = roundTrip.scenario.date
            let audio = AppPaths.audioDirectory(for: location)
            XCTAssertEqual(voice.retention, .keep, "takes are kept unless the author chooses otherwise")

            // Kept: a saved take stays, marked saved.
            let kept = try await record(voice, on: day)
            voice.accepted(kept)
            voice.markAnswersSaved(on: day)
            XCTAssertEqual(voice.takes.map(\.id), [kept.id])
            XCTAssertTrue(voice.takes[0].answersSaved)
            XCTAssertNotNil(files(under: audio)["\(day)/\(kept.id).m4a"])

            // Choosing to delete after saving takes the saved one at once,
            // and is remembered on this iPhone beside the language.
            let pending = try await record(voice, on: day)
            voice.setRetention(.deleteAfterSaving)
            XCTAssertEqual(voice.takes.map(\.id), [pending.id], "a take not saved yet is never deleted")
            XCTAssertEqual(files(under: audio).keys.sorted(), ["\(day)/\(pending.id).json", "\(day)/\(pending.id).m4a"])
            let stored = try JSONSerialization.jsonObject(with: Data(contentsOf: AppPaths.voiceFile(for: location))) as? [String: Any]
            XCTAssertEqual(stored?["retention"] as? String, "delete-after-saving")

            // The next saved take goes when its entry is saved.
            voice.accepted(pending)
            voice.markAnswersSaved(on: day)
            XCTAssertTrue(voice.takes.isEmpty)
            XCTAssertEqual(files(under: audio), [:])

            // A relaunch reads the choice; a file from before it was a choice
            // keeps every take.
            let relaunched = await launch(location)
            XCTAssertEqual(relaunched.voice?.retention, .deleteAfterSaving)
            try Data(#"{"language": "fr-CA"}"#.utf8).write(to: AppPaths.voiceFile(for: location))
            let older = await launch(location)
            XCTAssertEqual(older.voice?.retention, .keep)
            XCTAssertEqual(older.voice?.language, .quebecFrench)
        }
    }

    func testAnswersAcceptedIntoADiscardedDraftNeverDeleteTheirTakeWhenOtherWritingIsSaved() async throws {
        await reportingOnMain {
            let root = try scratchDirectory(self)
            let location = try roundTrip.seed(in: root)
            let model = await launch(location)
            let voice = try XCTUnwrap(model.voice)
            let day = roundTrip.scenario.date
            let audio = AppPaths.audioDirectory(for: location)
            voice.setRetention(.deleteAfterSaving)

            // Spoken answers accepted into the day's draft, then the draft
            // thrown away at the author's word.
            let take = try await record(voice, on: day)
            let filled = try XCTUnwrap(model.session(on: day))
            await filled.load()
            filled.setAnswer(0, spoken)
            voice.accepted(take)
            XCTAssertNotNil(voice.takes.first?.acceptedAt)
            filled.discard()
            XCTAssertNil(voice.takes.first?.acceptedAt, "a discarded draft still holds the take as accepted")

            // Typed writing for the same day, saved: the take is not its
            // answers, so it is neither marked saved nor deleted.
            let typed = try XCTUnwrap(model.session(on: day))
            await typed.load()
            typed.setAnswer(0, "Synthetic: written by hand after all.")
            let typedSaved = await typed.save()
            XCTAssertTrue(typedSaved, typed.failure ?? "")
            voice.markAnswersSaved(on: day)
            XCTAssertEqual(voice.takes.map(\.id), [take.id])
            XCTAssertFalse(voice.takes[0].answersSaved)
            XCTAssertEqual(files(under: audio).keys.sorted(), ["\(day)/\(take.id).json", "\(day)/\(take.id).m4a"])

            // Accepted again into a draft that is saved: then it goes.
            let again = try XCTUnwrap(model.session(on: day))
            await again.load()
            again.setAnswer(0, spoken)
            voice.accepted(take)
            let spokenSaved = await again.save()
            XCTAssertTrue(spokenSaved, again.failure ?? "")
            voice.markAnswersSaved(on: day)
            XCTAssertTrue(voice.takes.isEmpty)
            XCTAssertEqual(files(under: audio), [:])
        }
    }

    func testDeletingATakeRemovesItsAudioAndSidecarAndNothingElse() async throws {
        await reportingOnMain {
            let root = try scratchDirectory(self)
            let location = try roundTrip.seed(in: root)
            let model = await launch(location)
            let voice = try XCTUnwrap(model.voice)
            let day = roundTrip.scenario.date
            let audio = AppPaths.audioDirectory(for: location)
            let session = try XCTUnwrap(model.session(on: day))
            await session.load()
            session.setAnswer(0, "Synthetic: a draft that stays.")
            session.persist()

            let first = try await record(voice, on: day)
            let second = try await record(voice, on: day)
            let other = try await record(voice, on: "2026-10-01")
            // Something a stopped recording left, which no take claims yet.
            let stray = audio.appendingPathComponent("\(day)/\(UUID().uuidString.lowercased()).partial.m4a")
            try Data("partial".utf8).write(to: stray)
            let before = files(under: root)

            try voice.delete(first)
            var expected = before
            expected["audio/\(day)/\(first.id).m4a"] = nil
            expected["audio/\(day)/\(first.id).json"] = nil
            XCTAssertEqual(files(under: root), expected, "deleting a take touched something else")
            XCTAssertEqual(Set(voice.takes.map(\.id)), [second.id, other.id])

            // In bulk: every take, and still nothing else. A day left empty
            // goes; a day holding something else stays.
            XCTAssertEqual(voice.deleteAll(), 2)
            expected["audio/\(day)/\(second.id).m4a"] = nil
            expected["audio/\(day)/\(second.id).json"] = nil
            expected["audio/2026-10-01/\(other.id).m4a"] = nil
            expected["audio/2026-10-01/\(other.id).json"] = nil
            XCTAssertEqual(files(under: root), expected)
            XCTAssertTrue(voice.takes.isEmpty)
            XCTAssertFalse(FileManager.default.fileExists(atPath: audio.appendingPathComponent("2026-10-01").path))
            XCTAssertTrue(FileManager.default.fileExists(atPath: stray.path))
            XCTAssertTrue(expected.keys.contains { $0.hasPrefix("drafts/") }, "the draft is not a recording: \(expected.keys)")

            // A take the store did not write cannot be named into deleting
            // anything outside its day.
            XCTAssertThrowsError(try voice.store.delete(date: "../drafts", id: first.id))
            XCTAssertThrowsError(try voice.store.delete(date: day, id: "../../reflections"))
        }
    }

    func testARecoveryKitCarriesTheAudioBesideAnUnchangedExportAndPutsItBack() async throws {
        await reportingOnMain {
            let root = try scratchDirectory(self)
            let location = try roundTrip.seed(in: root)
            let model = await launch(location)
            let voice = try XCTUnwrap(model.voice)
            let day = roundTrip.scenario.date
            let audio = AppPaths.audioDirectory(for: location)
            let take = try await record(voice, on: day)
            let onPhone = files(under: audio)
            let parent = root.appendingPathComponent("exports", isDirectory: true)

            // Without the audio the kit is the engine's export, as before.
            let plain = try await model.exportRecoveryKit(into: parent, includingAudio: false)
            XCTAssertNil(plain.audio)
            XCTAssertEqual(plain.directory.path, plain.journal.directory)
            XCTAssertFalse(files(under: plain.directory).keys.contains { $0.hasSuffix(".m4a") })

            // With it: the same export, untouched, and an audio folder beside it.
            let kit = try await model.exportRecoveryKit(into: parent, includingAudio: true)
            let journalExport = URL(fileURLWithPath: kit.journal.directory, isDirectory: true)
            XCTAssertEqual(journalExport.deletingLastPathComponent().standardizedFileURL, kit.directory.standardizedFileURL)
            XCTAssertEqual(kit.journal.files, plain.journal.files, "including audio changed the engine's export")
            let exported = files(under: journalExport)
            XCTAssertEqual(Set(exported.keys), Set(files(under: plain.directory).keys))
            for file in kit.journal.files {
                XCTAssertEqual(exported[file.path].map { "sha256:" + sha256Hex($0) }, file.digest, file.path)
            }
            for (path, bytes) in files(under: plain.directory) where path != "MANIFEST.json" {
                XCTAssertEqual(exported[path], bytes, "\(path) differs from the export made alone")
            }
            let kitAudio = kit.directory.appendingPathComponent("audio", isDirectory: true)
            var carried = files(under: kitAudio)
            let manifest = try XCTUnwrap(carried.removeValue(forKey: "MANIFEST.json"))
            XCTAssertEqual(carried, onPhone, "the kit's audio is not the iPhone's, byte for byte")
            let listed = try JSONDecoder().decode(AudioKitManifest.self, from: manifest)
            XCTAssertEqual(listed, kit.audio)
            XCTAssertEqual(listed.takes, 1)
            XCTAssertEqual(Set(listed.files.map(\.path)), Set(onPhone.keys))
            for file in listed.files {
                XCTAssertEqual(onPhone[file.path].map(sha256Hex), file.sha256)
                XCTAssertEqual(onPhone[file.path]?.count, file.bytes)
            }
            XCTAssertEqual(Set(files(under: kit.directory).keys.map { String($0.split(separator: "/")[0]) }),
                           [journalExport.lastPathComponent, "audio"])
            XCTAssertFalse((try FileManager.default.contentsOfDirectory(atPath: parent.path)).contains { $0.hasPrefix(".") },
                           "a hidden partial kit was left behind")

            // Restoring puts the takes back where they were, byte for byte.
            XCTAssertEqual(voice.deleteAll(), 1)
            XCTAssertEqual(try voice.restore(from: kit.directory), AudioRestore(restored: 1))
            XCTAssertEqual(files(under: audio), onPhone)
            XCTAssertEqual(voice.takes.map(\.id), [take.id])
            XCTAssertEqual(voice.takes.first?.transcript, spoken)
            // Again, from the audio folder itself: already here, nothing replaced.
            XCTAssertEqual(try voice.restore(from: kitAudio), AudioRestore(alreadyHere: 1))
            XCTAssertEqual(files(under: audio), onPhone)

            // A damaged kit restores nothing.
            XCTAssertEqual(voice.deleteAll(), 1)
            let carriedAudio = kitAudio.appendingPathComponent("\(day)/\(take.id).m4a")
            var bytes = try Data(contentsOf: carriedAudio)
            bytes[0] ^= 0xff
            try bytes.write(to: carriedAudio)
            XCTAssertThrowsError(try voice.restore(from: kit.directory)) { error in
                XCTAssertEqual(error as? AudioKitProblem, .damaged("\(day)/\(take.id).m4a"))
            }
            XCTAssertEqual(files(under: audio), [:])
            XCTAssertThrowsError(try voice.restore(from: plain.directory)) { error in
                XCTAssertEqual(error as? AudioKitProblem, .noAudio)
            }
        }
    }

    func testAKitWithAnIncompleteTakeRestoresNothing() async throws {
        await reportingOnMain {
            let root = try scratchDirectory(self)
            let location = try roundTrip.seed(in: root)
            let model = await launch(location)
            let voice = try XCTUnwrap(model.voice)
            let audio = AppPaths.audioDirectory(for: location)
            let first = try await record(voice, on: roundTrip.scenario.date)
            let second = try await record(voice, on: "2026-10-01")
            let kit = try await model.exportRecoveryKit(into: root.appendingPathComponent("exports", isDirectory: true), includingAudio: true)
            let kitAudio = kit.directory.appendingPathComponent("audio", isDirectory: true)

            // The take restored last loses its sidecar, from the folder and
            // the manifest alike; the one before it is whole.
            let key = { (take: SpokenTake) in "\(take.date)/\(take.id)" }
            let last = [first, second].max { key($0) < key($1) }!
            let manifestURL = kitAudio.appendingPathComponent(AudioKitManifest.fileName)
            var manifest = try JSONDecoder().decode(AudioKitManifest.self, from: Data(contentsOf: manifestURL))
            manifest.files.removeAll { $0.path == "\(key(last)).json" }
            try JSONEncoder().encode(manifest).write(to: manifestURL)
            try FileManager.default.removeItem(at: kitAudio.appendingPathComponent("\(key(last)).json"))

            XCTAssertEqual(voice.deleteAll(), 2)
            XCTAssertThrowsError(try voice.restore(from: kit.directory)) { error in
                XCTAssertEqual(error as? AudioKitProblem, .damaged(key(last)))
            }
            XCTAssertEqual(files(under: audio), [:], "an incomplete kit restored part of itself")
            XCTAssertTrue(voice.takes.isEmpty)
        }
    }

    func testARestoreStoppedBetweenItsRenamesIsFinishedOrUndone() async throws {
        await reportingOnMain {
            let root = try scratchDirectory(self)
            let location = try roundTrip.seed(in: root)
            let model = await launch(location)
            let voice = try XCTUnwrap(model.voice)
            let day = roundTrip.scenario.date
            let audio = AppPaths.audioDirectory(for: location)
            let take = try await record(voice, on: day)
            let onPhone = files(under: audio)
            let kit = try await model.exportRecoveryKit(into: root.appendingPathComponent("exports", isDirectory: true), includingAudio: true)
            let kitDay = kit.directory.appendingPathComponent("audio/\(day)", isDirectory: true)
            let dayHere = audio.appendingPathComponent(day, isDirectory: true)
            let hidden = dayHere.appendingPathComponent(".\(take.id).m4a.restoring")

            // As a restore stopped between its renames leaves the take: its
            // sidecar in place, its audio under the hidden name.
            @MainActor func interrupted(audio bytes: Data? = nil) throws {
                XCTAssertEqual(voice.deleteAll(), 1)
                try FileManager.default.createDirectory(at: dayHere, withIntermediateDirectories: true)
                try FileManager.default.copyItem(
                    at: kitDay.appendingPathComponent("\(take.id).json"),
                    to: dayHere.appendingPathComponent("\(take.id).json")
                )
                try (bytes ?? Data(contentsOf: kitDay.appendingPathComponent("\(take.id).m4a"))).write(to: hidden)
                voice.reload()
            }

            // The next launch finishes it.
            try interrupted()
            await voice.recover()
            XCTAssertEqual(files(under: audio), onPhone, "the audio was left under its hidden name")
            XCTAssertEqual(voice.takes.map(\.id), [take.id])

            // So does the next restore, before taking it for a take already here.
            try interrupted()
            XCTAssertEqual(try voice.restore(from: kit.directory), AudioRestore(alreadyHere: 1))
            XCTAssertEqual(files(under: audio), onPhone)

            // Audio that is not the sidecar's is undone, and the restore
            // then puts the take back whole.
            try interrupted(audio: Data("cut short".utf8))
            XCTAssertEqual(try voice.restore(from: kit.directory), AudioRestore(restored: 1))
            XCTAssertEqual(files(under: audio), onPhone)

            // At launch, undone, nothing left behind.
            try interrupted(audio: Data("cut short".utf8))
            await voice.recover()
            XCTAssertEqual(files(under: audio), [:])
            XCTAssertTrue(voice.takes.isEmpty)
        }
    }

    func testDiagnosticsCountTheAudioWithoutAWordOfIt() async throws {
        await reportingOnMain {
            let root = try scratchDirectory(self)
            let location = try roundTrip.seed(in: root)
            let model = await launch(location)
            let voice = try XCTUnwrap(model.voice)
            let day = roundTrip.scenario.date
            let take = try await record(voice, on: day)
            await voice.refreshModelState()

            let report = DiagnosticsReport.make(try await model.diagnostics(), journal: model, ownIdentity: true)
            XCTAssertEqual(report.app.audioTakes, 1)
            XCTAssertEqual(report.app.audioBytes, voice.store.bytes(date: day, id: take.id))
            XCTAssertGreaterThan(report.app.audioBytes, 0)
            XCTAssertEqual(report.app.audioPendingTranscriptions, 0)
            XCTAssertEqual(report.app.speechModel, "ready")
            XCTAssertEqual(report.app.audioRetention, "keep")
            let text = report.text
            XCTAssertTrue(text.contains(#""audio_takes" : 1"#), text)
            for secret in [spoken, "Synthetic", day, take.id, take.audioSha256, root.path, "en-US"] {
                XCTAssertFalse(text.contains(secret), "the diagnostics carry \(secret)")
            }
        }
    }
}

private func sha256Hex(_ data: Data) -> String {
    SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
}
