import SenecaEngine
import XCTest

/// The typed wrappers against the real gomobile-bound engine: every shape of
/// bridge version 1 decodes, and the conditions and identities mutations
/// carry hold through Swift as they do in Go.
final class BridgeOperationsTests: XCTestCase {
    private var roundTrip: RoundTrip!

    override func setUpWithError() throws {
        roundTrip = try RoundTrip.load()
    }

    /// A device of its own: an engine over a fresh copy of the journal.
    private func device(seeded: Bool = true) async throws -> (engine: SenecaEngine, location: JournalLocation) {
        let root = try scratchDirectory(self)
        let location: JournalLocation
        if seeded {
            location = try roundTrip.seed(in: root)
        } else {
            location = roundTrip.location(in: root)
            try FileManager.default.createDirectory(
                at: location.reflections.deletingLastPathComponent(), withIntermediateDirectories: true
            )
        }
        let engine = SenecaEngine()
        try await engine.open(location)
        return (engine, location)
    }

    func testTheLinkedEngineSpeaksTheBridgeTheseModelsWereWrittenFor() async throws {
        let engine = SenecaEngine()
        XCTAssertEqual(engine.bridgeVersion, supportedBridgeVersion)
        let description = try await engine.describe()
        XCTAssertEqual(description.bridgeVersion, supportedBridgeVersion)
        XCTAssertEqual(description.operations.count, 40)
        XCTAssertTrue(description.operations.contains("sync.delivery"))
        for operation in ["reminders.plan", "diagnostics.collect", "recovery.export", "sync.leave", "entry.delete",
                          "packs.list", "packs.read", "settings.apply_pack", "insights.get", "share.packet"] {
            XCTAssertTrue(description.operations.contains(operation), operation)
        }
        XCTAssertTrue(description.operations.contains("reflection.save"))
        XCTAssertEqual(description.archiveFileName, "seneca-reviews.md")
        XCTAssertEqual(description.defaultQuestions.count, 3)

        // Nothing is answered about a journal before one is open.
        await refusal("journal-not-open") { try await engine.entries() }
    }

    func testReadsTheSyntheticJournal() async throws {
        let (engine, location) = try await device()
        let before = try sha256(of: location.reflections)

        let status = try await engine.status()
        XCTAssertEqual(status.today, "2026-04-02")
        XCTAssertEqual(status.entryCount, 4)
        XCTAssertEqual(status.reviewCount, 3)
        XCTAssertTrue(status.writable)
        XCTAssertNil(status.reflectionsReason)
        XCTAssertFalse(status.settings.initialized)
        XCTAssertEqual(status.settings.questions, roundTrip.scenario.questions)
        XCTAssertFalse(status.syncEnrolled)

        let list = try await engine.entries()
        XCTAssertEqual(list.entries.map(\.date), ["2026-03-02", "2026-03-03", "2026-03-04", "2026-03-05"])
        XCTAssertEqual(list.entries.last?.relative, "28 days ago")
        XCTAssertFalse(list.entries[0].preview.isEmpty)

        let entry = try await engine.entry(on: "2026-03-05")
        XCTAssertEqual(entry.revision, list.entries.last?.revision)
        XCTAssertTrue(entry.raw.contains("<!-- seneca:entry v=2 -->"))
        XCTAssertFalse(entry.lines.contains { $0.contains("seneca:") })
        let follow = try XCTUnwrap(entry.sections.compactMap(\.followThrough).first)
        XCTAssertEqual(follow.sourceDate, "2026-03-04")
        XCTAssertEqual(follow.response, "Synthetic: the draft was done by eleven.")
        await refusal("not-found") { try await engine.entry(on: "2026-01-01") }
        await refusal("invalid-date") { try await engine.entry(on: "March 5") }

        let hits = try await engine.search("walk")
        XCTAssertEqual(hits.map(\.date), ["2026-03-05"])
        XCTAssertEqual(hits.first?.snippets.count, 2)

        let reviews = try await engine.reviews()
        XCTAssertEqual(reviews.map(\.period), ["monthly", "weekly", "weekly"])
        XCTAssertEqual(reviews[1].revises, reviews[2].id)
        XCTAssertTrue(reviews[2].superseded)
        let review = try await engine.review(id: reviews[1].id)
        XCTAssertTrue(review.body.hasPrefix("Synthetic weekly review, revised the next morning."))

        let focus = try await engine.focus()
        XCTAssertEqual(focus.focus.text, "Finish before starting.")
        XCTAssertEqual(focus.focus.origin, "manual")
        XCTAssertEqual(focus.timeline.count, 4)
        let earlier = try await engine.focus(on: "2026-03-10")
        XCTAssertEqual(earlier.focus.text, "Walk before the dip.")
        XCTAssertEqual(earlier.focus.origin, "review")

        let conflicts = try await engine.conflicts()
        XCTAssertTrue(conflicts.conflicts.isEmpty && conflicts.focusDisputes.isEmpty)
        let sync = try await engine.syncStatus()
        XCTAssertFalse(sync.enrolled)

        XCTAssertEqual(try sha256(of: location.reflections), before, "reading wrote the journal")
        try await engine.close()
        await refusal("journal-not-open") { try await engine.status() }
    }

    func testAStaleOrRepeatedSaveNeverWritesTwice() async throws {
        let (engine, location) = try await device()
        let prepared = try await engine.prepareReflection()
        let writing = ReflectionWriting(draft: prepared.draft, answers: ["Noticed.", "", ""])

        await refusal("empty-reflection") {
            try await engine.saveReflection(ReflectionWriting(draft: prepared.draft, answers: ["", "", ""]))
        }
        let future = await refusal("future-date") { try await engine.prepareReflection(on: "2026-04-03") }
        XCTAssertEqual(future?.details?["today"], "2026-04-02")

        let requestID = RequestID()
        let saved = try await engine.saveReflection(writing, requestID: requestID)
        let written = try sha256(of: location.reflections)
        XCTAssertFalse(saved.value.isEdit)

        let repeated = try await engine.saveReflection(writing, requestID: requestID)
        XCTAssertTrue(repeated.replayed)
        XCTAssertEqual(repeated.value, saved.value)

        // The same writing as a new request was made on a day that has since
        // been written.
        let stale = await refusal("stale-reflection") { try await engine.saveReflection(writing) }
        XCTAssertEqual(stale?.details?["date"], "2026-04-02")
        XCTAssertEqual(stale?.retryable, false)
        // And an identity names one request only.
        await refusal("request-id-reused") {
            try await engine.saveReflection(
                ReflectionWriting(draft: prepared.draft, answers: ["Something else.", "", ""]), requestID: requestID
            )
        }
        XCTAssertEqual(try sha256(of: location.reflections), written)

        // What the journal refused can be kept, and is offered again.
        try await engine.retainDraft(
            ReflectionWriting(draft: prepared.draft, answers: ["Kept.", "", "Carry."], followThrough: "Walked."),
            reason: stale?.message ?? ""
        )
        let again = try await engine.prepareReflection()
        XCTAssertTrue(again.draft.isEdit)
        XCTAssertEqual(again.draft.initial, ["Noticed.", "", ""])
        XCTAssertEqual(again.retained?.answers, ["Kept.", "", "Carry."])
        XCTAssertEqual(again.retained?.followThrough, "Walked.")
        let kept = try await engine.retainedDraft(on: "2026-04-02")
        XCTAssertEqual(kept, again.retained)
        try await engine.discardDraft(on: "2026-04-02")
        let none = try await engine.retainedDraft(on: "2026-04-02")
        XCTAssertNil(none)
    }

    func testAPackIsReadAndAppliedWithoutRewritingAnEntry() async throws {
        let (engine, location) = try await device()
        let before = try sha256(of: location.reflections)

        let packs = try await engine.builtinPacks()
        XCTAssertEqual(packs.map(\.id), ["stoic", "step-10", "examen", "gratitude", "virtues"])
        let examen = try XCTUnwrap(packs.first { $0.id == "examen" })

        // A file handed to the app is read back as the same pack.
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        let file = try String(decoding: encoder.encode(examen), as: UTF8.self)
        let read = try await engine.readPack(file)
        XCTAssertEqual(read, examen)
        await refusal("invalid-pack") { try await engine.readPack(#"{"seneca_pack": 1, "id": "x"}"#) }

        let settings = try await engine.settings()
        XCTAssertNil(settings.practice)
        let applied = try await engine.applyPack(examen, base: settings.revision)
        XCTAssertTrue(applied.value.changed)
        XCTAssertEqual(applied.value.settings.questions, examen.questions)
        XCTAssertEqual(applied.value.settings.practice?.pack, examen)
        XCTAssertEqual(applied.value.settings.practice?.from, "2026-04-02")
        XCTAssertEqual(applied.value.settings.practice?.questionsChanged, false)
        await refusal("stale-settings") { try await engine.applyPack(examen, base: settings.revision) }
        XCTAssertEqual(try sha256(of: location.reflections), before, "applying a pack rewrites no entry")
    }

    func testFocusAndSettingsChangeOnlyFromTheStateTheyWereDecidedOn() async throws {
        let (engine, _) = try await device()

        let focus = try await engine.focus()
        await refusal("blank-focus") { try await engine.setFocus("  ", base: focus.revision) }
        let requestID = RequestID()
        let set = try await engine.setFocus("One thing at a time.", base: focus.revision, requestID: requestID)
        XCTAssertTrue(set.value.changed)
        XCTAssertEqual(set.value.effective, "2026-04-02")
        let repeated = try await engine.setFocus("One thing at a time.", base: focus.revision, requestID: requestID)
        XCTAssertTrue(repeated.replayed)
        await refusal("stale-focus") { try await engine.setFocus("One thing at a time.", base: focus.revision) }
        let after = try await engine.focus()
        XCTAssertEqual(after.timeline.count, focus.timeline.count + 1, "the focus was set once")
        XCTAssertEqual(after.revision, set.value.revision)
        let cleared = try await engine.clearFocus(base: after.revision)
        XCTAssertEqual(cleared.value.previous, "One thing at a time.")
        XCTAssertEqual(cleared.value.text, "")

        let settings = try await engine.settings()
        await refusal("settings-not-initialized") { try await engine.updateSettings(base: settings.revision, anchorQuestionIndex: 1) }
        let initialized = try await engine.initializeSettings()
        XCTAssertTrue(initialized.value.changed)
        XCTAssertTrue(initialized.value.settings.initialized)
        let questions = ["What did I notice today?", "What will I carry into tomorrow?"]
        let updated = try await engine.updateSettings(base: settings.revision, questions: questions)
        XCTAssertEqual(updated.value.settings.questions, questions)
        XCTAssertEqual(updated.value.settings.anchorQuestionIndex, 2, "the anchor stays on its question")
        await refusal("stale-settings") { try await engine.updateSettings(base: settings.revision, questions: questions) }
        await refusal("anchor-moved") {
            try await engine.updateSettings(base: updated.value.settings.revision, questions: ["What did I notice today?"])
        }
        let prepared = try await engine.prepareReflection()
        XCTAssertEqual(prepared.draft.questions, questions)
    }

    func testTwoDevicesExchangeAndSettleAConflict() async throws {
        let exchange = try scratchDirectory(self).appendingPathComponent("exchange", isDirectory: true)
        let (mac, _) = try await device()
        let (phone, phoneLocation) = try await device(seeded: false)

        await refusal("sync-not-enrolled") { try await mac.exportSync(exchange: exchange) }
        let enrolled = try await mac.enrollSync(name: "mac")
        XCTAssertTrue(enrolled.value.enrolled)
        XCTAssertEqual(enrolled.value.sequence, 1)
        let published = try await mac.exportSync(exchange: exchange)
        XCTAssertEqual(published.transactions.count, 1)

        let joined = try await phone.joinSync(exchange: exchange, name: "phone")
        XCTAssertEqual(joined.value.journal, enrolled.value.journal)
        XCTAssertNotEqual(joined.value.device, enrolled.value.device)
        let first = try await phone.importSync(exchange: exchange)
        XCTAssertEqual(first.value.applied.count, 1)
        XCTAssertEqual(first.value.written, ["reflections", "archive", "settings"])
        XCTAssertTrue(FileManager.default.fileExists(atPath: phoneLocation.reflections.path))
        let phoneEntries = try await phone.entries()
        XCTAssertEqual(phoneEntries.entries.count, 4)

        // Both write the same day before either hears from the other.
        let macDraft = try await mac.prepareReflection()
        _ = try await mac.saveReflection(ReflectionWriting(draft: macDraft.draft, answers: ["From the Mac.", "", ""]))
        let phoneDraft = try await phone.prepareReflection()
        _ = try await phone.saveReflection(ReflectionWriting(draft: phoneDraft.draft, answers: ["From the phone.", "", ""]))
        _ = try await mac.exportSync(exchange: exchange)
        _ = try await phone.exportSync(exchange: exchange)
        let macImport = try await mac.importSync(exchange: exchange)
        XCTAssertEqual(macImport.value.conflicts, ["entry:2026-04-02"])
        _ = try await phone.importSync(exchange: exchange)

        let status = try await phone.syncStatus()
        XCTAssertEqual(status.conflicts, ["entry:2026-04-02"])
        let listed = try await phone.conflicts()
        let conflict = try XCTUnwrap(listed.conflicts.first)
        XCTAssertEqual(conflict.date, "2026-04-02")
        XCTAssertTrue(conflict.base.deleted)
        XCTAssertTrue(conflict.ours.content.contains("From the phone."))
        XCTAssertFalse(conflict.ours.lines.contains { $0.contains("seneca:") })
        let theirs = try XCTUnwrap(conflict.theirs.first)
        XCTAssertTrue(theirs.content.contains("From the Mac."))

        await refusal("stale-conflict") { try await phone.resolveConflict(id: "0000", answer: .keep(revision: theirs.revision)) }
        let requestID = RequestID()
        let resolved = try await phone.resolveConflict(id: conflict.id, answer: .keep(revision: theirs.revision), requestID: requestID)
        XCTAssertEqual(resolved.value.artifact, "entry:2026-04-02")
        XCTAssertEqual(resolved.value.written, ["reflections"])
        let repeated = try await phone.resolveConflict(id: conflict.id, answer: .keep(revision: theirs.revision), requestID: requestID)
        XCTAssertTrue(repeated.replayed)

        _ = try await phone.exportSync(exchange: exchange)
        _ = try await mac.importSync(exchange: exchange)
        let settled = try await mac.conflicts()
        XCTAssertTrue(settled.conflicts.isEmpty)
        let onMac = try await mac.entry(on: "2026-04-02")
        let onPhone = try await phone.entry(on: "2026-04-02")
        XCTAssertEqual(onMac.raw, onPhone.raw)

        await refusal("nothing-to-recover") { try await phone.recoverSync(.reflections, action: .restore) }
    }
}
