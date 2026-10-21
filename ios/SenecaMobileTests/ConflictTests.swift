import SenecaEngine
import XCTest
@testable import SenecaMobile

/// Conflicts as the app's model and the form's session meet them, over the
/// conflicted fixture: a synced journal in which this iPhone and a Mac wrote
/// 2026-04-02 differently and set a different focus that day, with the
/// exchange both published to. Nothing here reaches the exchange unless a
/// test says so: the phone is offline until then.
@MainActor
final class ConflictTests: XCTestCase {
    private var roundTrip: RoundTrip!
    private var root: URL!
    private var fixture: StagedFixture!
    private let day = "2026-04-02"

    override func setUp() async throws {
        roundTrip = try RoundTrip.load()
        root = try scratchDirectory(self)
        fixture = try StagedFixture.stage("conflicted", in: root, with: roundTrip)
    }

    private var location: JournalLocation { fixture.location }

    private var store: DraftStore {
        DraftStore(directory: AppPaths.draftsDirectory(for: location))
    }

    private func launch(file: StaticString = #filePath, line: UInt = #line) async -> JournalModel {
        let location = location
        let model = JournalModel(engine: SenecaEngine(), location: { location })
        await model.open()
        XCTAssertEqual(model.phase, .ready, file: file, line: line)
        return model
    }

    private func journalText() throws -> String {
        try String(contentsOf: location.reflections, encoding: .utf8)
    }

    func testAConflictedDayStaysReadableAndItsWritingStaysADraftUntilItIsSettled() async {
        await reportingOnMain {
            let model = await launch()
            XCTAssertEqual(model.conflictCount, 2)
            let conflict = try XCTUnwrap(model.conflict(on: day))
            XCTAssertTrue(model.entries.first { $0.date == day }?.conflicted == true)
            let entry = try await model.entry(on: day)
            XCTAssertTrue(entry.conflicted)
            XCTAssertTrue(entry.raw.contains("from the phone"), "this iPhone's version stays readable")
            XCTAssertTrue(ConflictText.text(conflict.ours).contains("from the phone"))
            XCTAssertTrue(ConflictText.text(conflict.theirs[0]).contains("from the Mac"))
            XCTAssertTrue(conflict.base.deleted)

            // Writing for the day is kept, and not saved.
            let session = try XCTUnwrap(model.session(on: day))
            await session.load()
            XCTAssertTrue(session.conflicted)
            session.setAnswer(1, "Synthetic: written while the day was conflicted.")
            let before = try journalText()
            let saved = await session.save()
            XCTAssertFalse(saved)
            XCTAssertEqual(try journalText(), before, "nothing reaches the journal")
            let kept = try XCTUnwrap(try store.read(day))
            XCTAssertEqual(kept.answers[1], "Synthetic: written while the day was conflicted.")
            XCTAssertNotNil(kept.reason)

            // Settled by keeping the Mac's version, still offline.
            try await model.resolve(conflict, .keep(revision: conflict.theirs[0].revision), requestID: RequestID())
            XCTAssertNil(model.conflict(on: day))
            XCTAssertTrue(try journalText().contains("from the Mac"))
            XCTAssertNotNil(try store.read(day), "settling the day keeps the draft")

            // A relaunch puts the writing back over the version kept, and it saves.
            let relaunched = await launch()
            let again = try XCTUnwrap(relaunched.session(on: day))
            await again.load()
            XCTAssertFalse(again.conflicted)
            XCTAssertEqual(again.notice, .changedUnderneath)
            XCTAssertEqual(again.answers[1], "Synthetic: written while the day was conflicted.")
            XCTAssertNotNil(again.journalAnswer(1), "the version kept is shown beside the writing")
            let savedNow = await again.save()
            XCTAssertTrue(savedNow)
            XCTAssertNil(try store.read(day))
            XCTAssertTrue(try journalText().contains("Synthetic: written while the day was conflicted."))
        }
    }

    func testAnAnswerIsGivenOnceAndAStaleOneIsRefused() async {
        await reportingOnMain {
            let model = await launch()
            let conflict = try XCTUnwrap(model.conflict(on: day))
            let id = RequestID()
            try await model.resolve(conflict, .keep(revision: conflict.ours.revision), requestID: id)
            let settled = try journalText()
            XCTAssertTrue(settled.contains("from the phone"))
            // The same answer again is answered, not carried out again.
            try await model.resolve(conflict, .keep(revision: conflict.ours.revision), requestID: id)
            XCTAssertEqual(try journalText(), settled)
            // Another answer to a conflict that is gone is refused.
            await refusedOnMain("stale-conflict") {
                try await model.resolve(conflict, .keep(revision: conflict.theirs[0].revision), requestID: RequestID())
            }
            XCTAssertEqual(try journalText(), settled)

            let dispute = try XCTUnwrap(model.conflicts.focusDisputes.first)
            XCTAssertEqual(Set(dispute.candidates.map(\.text)), ["Leave the desk by six.", "Walk in any weather."])
            XCTAssertTrue(model.focus?.disputed == true)
            try await model.resolve(dispute, .text("Synthetic: walk, then leave."), requestID: RequestID())
            XCTAssertEqual(model.focus?.text, "Synthetic: walk, then leave.")
            XCTAssertEqual(model.conflictCount, 0)
        }
    }

    func testAConflictSettledOfflineConvergesOnceTheExchangeResumes() async {
        await reportingOnMain {
            let exchange = try XCTUnwrap(fixture.exchange)
            // The Mac: another device that joins the same exchange and holds
            // both versions, exactly as the phone does.
            let macLocation = roundTrip.location(in: root.appendingPathComponent("mac", isDirectory: true))
            try FileManager.default.createDirectory(
                at: macLocation.reflections.deletingLastPathComponent(), withIntermediateDirectories: true
            )
            let mac = SenecaEngine()
            try await mac.open(macLocation)
            _ = try await mac.joinSync(exchange: exchange, name: "mac")
            _ = try await mac.importSync(exchange: exchange)
            let macConflicts = try await mac.conflicts()
            XCTAssertEqual(macConflicts.conflicts.count, 1)

            // Offline: the phone settles the day by editing, and writes on.
            let model = await launch()
            let conflict = try XCTUnwrap(model.conflict(on: day))
            XCTAssertEqual(conflict.id, macConflicts.conflicts[0].id, "both devices name the conflict alike")
            let dispute = try XCTUnwrap(model.conflicts.focusDisputes.first)
            try await model.resolve(conflict, .keep(revision: conflict.ours.revision), requestID: RequestID())
            try await model.resolve(dispute, .record(dispute.candidates[0].record), requestID: RequestID())
            let session = try XCTUnwrap(model.session(on: day))
            await session.load()
            session.setAnswer(0, "Synthetic: noticed the rain, and the Mac's light at five.")
            let saved = await session.save()
            XCTAssertTrue(saved)
            // The Mac goes on meanwhile.
            let macDay = try await mac.prepareReflection(on: "2026-04-01")
            _ = try await mac.saveReflection(ReflectionWriting(draft: macDay.draft, answers: ["Synthetic: the Mac, offline too.", "", ""]))

            // The exchange resumes, the Mac first.
            _ = try await mac.exportSync(exchange: exchange)
            let phone = SenecaEngine()
            try await phone.open(location)
            _ = try await phone.importSync(exchange: exchange)
            _ = try await phone.exportSync(exchange: exchange)
            _ = try await mac.importSync(exchange: exchange)

            let settledOnMac = try await mac.conflicts()
            let settledOnPhone = try await phone.conflicts()
            XCTAssertEqual(settledOnMac, Conflicts())
            XCTAssertEqual(settledOnPhone, Conflicts())
            for date in ["2026-04-01", day] {
                let onMac = try await mac.entry(on: date)
                let onPhone = try await phone.entry(on: date)
                XCTAssertEqual(onMac.raw, onPhone.raw, date)
            }
            let macToday = try await mac.entry(on: day)
            XCTAssertTrue(macToday.raw.contains("and the Mac's light at five"))
            let macFocus = try await mac.focus(on: day)
            let phoneFocus = try await phone.focus(on: day)
            XCTAssertEqual(macFocus.focus, phoneFocus.focus)
            XCTAssertFalse(macFocus.focus.disputed)
        }
    }
}
