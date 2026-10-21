import SenecaEngine
import XCTest
@testable import SenecaMobile

/// A notification center that answers for the author and keeps what it was
/// asked to schedule, as iOS would.
final class FakeReminderCenter: ReminderCenter, @unchecked Sendable {
    let prefix = ScheduledReminder.prefix
    private let lock = NSLock()
    private var answer: ReminderPermission
    private var requests: [String: ScheduledReminder] = [:]
    private(set) var asked = 0
    /// What the author answers when asked.
    var answerWhenAsked: ReminderPermission = .allowed

    init(_ permission: ReminderPermission) {
        answer = permission
    }

    /// The author changing their mind in Settings.
    func set(_ permission: ReminderPermission) {
        lock.withLock { answer = permission }
    }

    var scheduled: [ScheduledReminder] {
        lock.withLock { requests.values.sorted { $0.fireDate < $1.fireDate } }
    }

    var days: Set<String> { Set(scheduled.map(\.date)) }

    func permission() async -> ReminderPermission {
        lock.withLock { answer }
    }

    func requestPermission() async -> ReminderPermission {
        lock.withLock {
            asked += 1
            if answer == .notDetermined { answer = answerWhenAsked }
            return answer
        }
    }

    func pending() async -> [String] {
        lock.withLock { Array(requests.keys) }
    }

    func schedule(_ reminder: ScheduledReminder) async throws {
        lock.withLock { requests[reminder.identifier] = reminder }
    }

    func cancel(_ identifiers: [String]) async {
        lock.withLock { identifiers.forEach { requests[$0] = nil } }
    }
}

/// An identity vault that keeps nothing outside the test.
final class MemoryVault: IdentityVault, @unchecked Sendable {
    private let lock = NSLock()
    private var held: [String: String] = [:]

    func read(journal: String) -> String? {
        lock.withLock { held[journal] }
    }

    @discardableResult
    func write(journal: String, device: String?) -> Bool {
        lock.withLock { held[journal] = device }
        return true
    }
}

/// A zone the test can move the iPhone to.
final class ZoneBox: @unchecked Sendable {
    private let lock = NSLock()
    private var zone: TimeZone

    init(_ zone: TimeZone) {
        self.zone = zone
    }

    var current: TimeZone {
        get { lock.withLock { zone } }
        set { lock.withLock { zone = newValue } }
    }
}

/// The evening reminders over the real engine: what is scheduled, when it is
/// planned again, and that nothing about them can stop writing. The journal
/// is the round trip's, on 2026-04-02 at 21:30 in Toronto.
@MainActor
final class ReminderTests: XCTestCase {
    private var roundTrip: RoundTrip!
    private var location: JournalLocation!
    private let today = "2026-04-02"

    override func setUp() async throws {
        roundTrip = try RoundTrip.load()
        location = try roundTrip.seed(in: try scratchDirectory(self))
    }

    private func launch(_ center: FakeReminderCenter, zone: ZoneBox? = nil, file: StaticString = #filePath, line: UInt = #line) async -> JournalModel {
        let base = location!
        let model = JournalModel(
            engine: SenecaEngine(),
            location: {
                var location = base
                if let zone { location.timeZone = zone.current }
                return location
            },
            reminderCenter: center,
            identityVault: MemoryVault()
        )
        await model.open()
        XCTAssertEqual(model.phase, .ready, file: file, line: line)
        await eventually("the launch's reminders", file: file, line: line) {
            model.reminders?.reconciliations.contains(.launch) == true
        }
        return model
    }

    private func save(_ model: JournalModel, on date: String? = nil) async throws {
        let prepared = try await model.prepareReflection(on: date)
        _ = try await model.save(ReflectionWriting(draft: prepared.draft, answers: ["Synthetic: written.", "", ""]), requestID: RequestID())
    }

    func testRemindersAreOffUntilTurnedOnAndFollowTheDesktopSchedule() async {
        await reportingOnMain {
            let center = FakeReminderCenter(.notDetermined)
            let model = await launch(center)
            let reminders = try XCTUnwrap(model.reminders)
            XCTAssertEqual(reminders.state, .off)
            XCTAssertEqual(center.asked, 0, "nobody is asked before turning reminders on")
            XCTAssertTrue(center.scheduled.isEmpty)

            await reminders.enable()
            XCTAssertEqual(center.asked, 1, "turning them on asks iOS once")
            XCTAssertEqual(reminders.state, .scheduled)
            XCTAssertEqual(reminders.times, ["20:00", "21:00", "21:30", "22:00"], "the desktop's evening")
            // 21:30 in Toronto: tonight's 22:00 is still ahead, then a week of evenings.
            XCTAssertEqual(center.scheduled.count, 1 + 6 * 4)
            let first = try XCTUnwrap(center.scheduled.first)
            XCTAssertEqual(first.identifier, "seneca.reminder.2026-04-02.2200")
            XCTAssertEqual(first.fireDate, ISO8601DateFormatter().date(from: "2026-04-03T02:00:00Z"))
            XCTAssertEqual(reminders.next?.date, today)

            // The author's own times, kept on this iPhone only.
            let journalBefore = try String(contentsOf: location.reflections, encoding: .utf8)
            await reminders.setTimes(["22:15", "07:05"])
            XCTAssertEqual(reminders.times, ["07:05", "22:15"])
            XCTAssertEqual(center.scheduled.first?.identifier, "seneca.reminder.2026-04-02.2215")
            XCTAssertEqual(center.scheduled.count, 1 + 6 * 2)
            XCTAssertEqual(try String(contentsOf: location.reflections, encoding: .utf8), journalBefore)
            let saved = try JSONDecoder().decode(ReminderPreferences.self, from: Data(contentsOf: AppPaths.remindersFile(for: location)))
            XCTAssertEqual(saved, ReminderPreferences(enabled: true, times: ["07:05", "22:15"]))
            XCTAssertFalse(AppPaths.remindersFile(for: location).path.hasPrefix(location.reflections.deletingLastPathComponent().path))

            await reminders.setTimes(nil)
            XCTAssertEqual(center.scheduled.count, 25)

            // A relaunch keeps the choice and schedules the same.
            let relaunched = await launch(center)
            XCTAssertEqual(relaunched.reminders?.preferences.enabled, true)
            XCTAssertEqual(center.scheduled.count, 25)

            await reminders.disable()
            XCTAssertTrue(center.scheduled.isEmpty, "turning them off leaves nothing scheduled")
        }
    }

    func testASaveAForegroundAndAClockChangeEachPlanAgainAndAWrittenDayIsSilent() async {
        await reportingOnMain {
            let center = FakeReminderCenter(.allowed)
            let zone = ZoneBox(location.timeZone)
            let model = await launch(center, zone: zone)
            let reminders = try XCTUnwrap(model.reminders)
            await reminders.enable()
            XCTAssertTrue(center.days.contains(today))
            XCTAssertTrue(reminders.stillDue(today))

            try await save(model)
            XCTAssertTrue(reminders.reconciliations.contains(.save))
            XCTAssertFalse(center.days.contains(today), "a written day is not reminded")
            XCTAssertFalse(reminders.stillDue(today), "one that fires anyway is not shown")
            XCTAssertTrue(center.days.contains("2026-04-03"))

            // An earlier evening written later changes nothing ahead; a later
            // one written ahead of time silences that evening.
            try await save(model, on: "2026-03-30")
            XCTAssertTrue(center.days.contains("2026-04-03"))

            await model.cameForward()
            XCTAssertTrue(reminders.reconciliations.contains(.foreground))

            // The iPhone flies to Tokyo: the journal's today is another day,
            // and the evenings are Tokyo's.
            zone.current = TimeZone(identifier: "Asia/Tokyo")!
            await model.clockChanged()
            XCTAssertTrue(reminders.reconciliations.contains(.clock))
            XCTAssertEqual(model.status?.today, "2026-04-03")
            XCTAssertEqual(reminders.plan?.timeZone, "Asia/Tokyo")
            let tokyo = try XCTUnwrap(center.scheduled.first { $0.date == "2026-04-03" && $0.time == "20:00" })
            XCTAssertEqual(tokyo.fireDate, ISO8601DateFormatter().date(from: "2026-04-03T11:00:00Z"))
        }
    }

    func testAReflectionFromTheMacSilencesTheEveningOnlyOnceItIsImported() async {
        await reportingOnMain {
            let root = try scratchDirectory(self)
            let (desk, folder) = try await enrolledDesk(roundTrip, in: root)
            let phoneLocation = roundTrip.location(in: try scratchDirectory(self))
            try FileManager.default.createDirectory(at: phoneLocation.reflections.deletingLastPathComponent(), withIntermediateDirectories: true)
            let center = FakeReminderCenter(.allowed)
            let model = JournalModel(
                engine: SenecaEngine(), location: { phoneLocation }, access: { _ in FaultyAccess(folder: folder) },
                reminderCenter: center, identityVault: MemoryVault()
            )
            await model.open()
            let sync = try XCTUnwrap(model.sync)
            await eventually("the launch exchange") { sync.exchanges.contains(.launch) && sync.phase != .exchanging }
            await sync.join()
            let reminders = try XCTUnwrap(model.reminders)
            await reminders.enable()
            XCTAssertTrue(center.days.contains(today))

            // Written on the Mac: the phone's reminder stands until it arrives.
            try await desk.write(["Synthetic: written at the desk.", "", ""], on: today)
            try await desk.exchange()
            XCTAssertTrue(center.days.contains(today), "a Mac's save cannot reach a reminder already here")

            await model.refresh()
            await eventually("the import's reminders") { reminders.reconciliations.contains(.import) }
            await eventually("tonight's reminders cancelled") { !center.days.contains(self.today) }
            XCTAssertFalse(reminders.stillDue(today))
        }
    }

    func testDeniedPermissionNeverStopsWritingAndIsSaidSo() async {
        await reportingOnMain {
            let center = FakeReminderCenter(.notDetermined)
            center.answerWhenAsked = .denied
            let model = await launch(center)
            let reminders = try XCTUnwrap(model.reminders)
            await reminders.enable()
            XCTAssertEqual(reminders.state, .denied)
            XCTAssertTrue(center.scheduled.isEmpty)
            XCTAssertTrue(ReminderText.state(reminders, today: today).contains("turned off for Seneca"))
            let advice = PhoneAdvice.list(nil, journal: model)
            XCTAssertEqual(advice.map(\.code), ["notifications-denied"])

            // Writing goes on as if nothing were the matter.
            try await save(model)
            XCTAssertTrue(try String(contentsOf: location.reflections, encoding: .utf8).contains("Synthetic: written."))
            XCTAssertEqual(reminders.state, .denied)

            // Allowed later in Settings: the next return to the app schedules.
            center.set(.allowed)
            await model.cameForward()
            XCTAssertEqual(reminders.state, .scheduled)
            XCTAssertFalse(center.scheduled.isEmpty)
            XCTAssertFalse(center.days.contains(today), "today is written")
            XCTAssertTrue(PhoneAdvice.list(nil, journal: model).isEmpty)

            // Taken back again: everything pending goes.
            center.set(.denied)
            await model.cameForward()
            XCTAssertTrue(center.scheduled.isEmpty)
        }
    }

    func testAJournalThatCannotBePlannedKeepsWhatWasScheduled() async {
        await reportingOnMain {
            let center = FakeReminderCenter(.allowed)
            let model = await launch(center)
            let reminders = try XCTUnwrap(model.reminders)
            await reminders.enable()
            let before = center.scheduled.count
            XCTAssertGreaterThan(before, 0)

            // The reflections file cannot be read at all.
            try FileManager.default.removeItem(at: location.reflections)
            try FileManager.default.createDirectory(at: location.reflections, withIntermediateDirectories: false)
            await model.cameForward()
            guard case .problem = reminders.state else {
                return XCTFail("expected the plan to fail, got \(reminders.state)")
            }
            XCTAssertEqual(center.scheduled.count, before, "better a reminder too many than none")
        }
    }
}
