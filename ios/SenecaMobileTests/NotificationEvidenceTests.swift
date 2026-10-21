import SenecaEngine
import UIKit
import UserNotifications
import XCTest
@testable import SenecaMobile

/// Watches what iOS shows while the test runs, and what it keeps delivered.
private final class DeliveryWatcher: NSObject, UNUserNotificationCenterDelegate, @unchecked Sendable {
    private let lock = NSLock()
    private var presented: [String: Date] = [:]

    func userNotificationCenter(
        _ center: UNUserNotificationCenter, willPresent notification: UNNotification
    ) async -> UNNotificationPresentationOptions {
        let identifier = notification.request.identifier
        lock.withLock { presented[identifier] = presented[identifier] ?? Date() }
        return [.banner, .list, .sound]
    }

    func presentedAt(_ identifier: String) -> Date? {
        lock.withLock { presented[identifier] }
    }
}

/// The reminders on a physical iPhone, through iOS's own notification
/// center: the app plans two reminders for this evening and schedules them;
/// the first is delivered by iOS; a reflection saved before the second makes
/// the app cancel it, and iOS never delivers it. ios/scripts/notify-evidence.sh
/// runs this on the paired iPhone and keeps what it reports.
///
/// The journal is a synthetic one in a directory of the run's own; the
/// installation's journal is never opened, and its reminders are left alone:
/// the run schedules under a prefix of its own.
///
/// Needs SENECA_NOTIFY_RUN, and notifications allowed for Seneca: the first
/// run asks, and someone at the iPhone has to tap Allow.
@MainActor
final class NotificationEvidenceTests: XCTestCase {
    func testAReminderIsDeliveredAndASaveCancelsTheNext() async {
        await reportingOnMain { try await run() }
    }

    @MainActor
    private func run() async throws {
        let environment = ProcessInfo.processInfo.environment
        let name = try XCTUnwrap(
            environment["SENECA_NOTIFY_RUN"].flatMap { $0.isEmpty ? nil : $0 },
            "SENECA_NOTIFY_RUN is not set; run this through ios/scripts/notify-evidence.sh"
        )
        let support = try FileManager.default.url(
            for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true
        )
        let root = support.appendingPathComponent("SenecaEvidence/\(name)", isDirectory: true)
        XCTAssertFalse(FileManager.default.fileExists(atPath: root.path), "run \(name) was already started")
        var evidence: [String: Any] = ["run": name, "phase": "deliver"]
        defer {
            if let data = try? JSONSerialization.data(withJSONObject: evidence, options: [.prettyPrinted, .sortedKeys]) {
                try? FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
                try? data.write(to: root.appendingPathComponent("evidence-deliver.json"), options: .atomic)
            }
        }
        for (key, value) in try common() {
            evidence[key] = value
        }

        let system = UNUserNotificationCenter.current()
        let watcher = DeliveryWatcher()
        system.delegate = watcher
        defer { system.delegate = ReminderPresenter.shared }

        // A namespace of the run's own: the app hosting this test plans the
        // installation's reminders beside it, and cancels whatever of
        // `seneca.reminder.` it did not plan whenever it comes forward.
        let center = SystemReminderCenter(prefix: "seneca.evidence.")
        var permission = await center.permission()
        evidence["permission_before"] = permission.rawValue
        if permission == .notDetermined {
            permission = await center.requestPermission()
        }
        evidence["permission"] = permission.rawValue
        guard permission == .allowed else {
            return XCTFail("notifications are not allowed for Seneca on this iPhone: allow them in Settings, Notifications, Seneca, and run again")
        }

        // A synthetic journal on the real clock, in the iPhone's own zone.
        let roundTrip = try RoundTrip.load()
        var location = try roundTrip.seed(in: root)
        location.now = nil
        location.timeZone = .current
        let staged = location
        let model = JournalModel(
            engine: SenecaEngine(), location: { staged },
            access: { _ in UnchosenAccess() }, reminderCenter: center, identityVault: MemoryVault()
        )
        await model.open()
        XCTAssertEqual(model.phase, .ready)
        let today = try XCTUnwrap(model.status?.today)
        let reminders = try XCTUnwrap(model.reminders)

        // Two reminders tonight: the first soon, the second two minutes on.
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = .current
        var first = calendar.date(bySetting: .second, value: 0, of: Date().addingTimeInterval(80))!
        if first.timeIntervalSinceNow < 25 { first = first.addingTimeInterval(60) }
        let second = first.addingTimeInterval(120)
        guard calendar.isDate(second, inSameDayAs: Date()) else {
            throw XCTSkip("too close to midnight to plan two reminders tonight")
        }
        let times = [ReminderText.hhmm(first), ReminderText.hhmm(second)]
        await reminders.setTimes(times)
        await reminders.enable()
        XCTAssertEqual(reminders.state, .scheduled)
        let firstID = ScheduledReminder(date: today, time: times[0], fireDate: first, timeZone: .current, prefix: center.prefix).identifier
        let secondID = ScheduledReminder(date: today, time: times[1], fireDate: second, timeZone: .current, prefix: center.prefix).identifier
        let pendingAtStart = await center.pending()
        XCTAssertTrue(pendingAtStart.contains(firstID) && pendingAtStart.contains(secondID), "\(pendingAtStart)")
        evidence["time_zone"] = TimeZone.current.identifier
        evidence["today"] = today
        evidence["scheduled"] = [
            ["identifier": firstID, "fire_at": stamp(first)],
            ["identifier": secondID, "fire_at": stamp(second)],
        ]
        evidence["scheduled_count"] = reminders.scheduled.count

        // iOS delivers the first.
        let delivered = try await waitForDelivery(firstID, watcher: watcher, until: first.addingTimeInterval(90))
        evidence["delivered"] = [
            "identifier": firstID, "delivered_at": stamp(delivered.at), "presented": delivered.presented,
            "in_notification_center": delivered.listed,
        ] as [String: Any]

        // The evening is written before the second: the app cancels it.
        let prepared = try await model.prepareReflection()
        _ = try await model.save(ReflectionWriting(draft: prepared.draft, answers: ["Synthetic: written on the iPhone.", "", ""]), requestID: RequestID())
        let saved = Date()
        XCTAssertLessThan(saved, second, "the save came after the second reminder was due")
        let pendingAfterSave = await center.pending()
        XCTAssertFalse(pendingAfterSave.contains(secondID), "the save did not cancel tonight's next reminder")
        XCTAssertFalse(reminders.stillDue(today))
        evidence["saved_at"] = stamp(saved)
        evidence["pending_after_save"] = pendingAfterSave.filter { $0.contains(today) }
        evidence["complete"] = reminders.plan?.complete ?? []

        // Its time passes and iOS shows nothing.
        try await Task.sleep(for: .seconds(max(0, second.timeIntervalSinceNow) + 30))
        let listed = await system.deliveredNotifications().map(\.request.identifier)
        XCTAssertNil(watcher.presentedAt(secondID), "the cancelled reminder was shown")
        XCTAssertFalse(listed.contains(secondID), "the cancelled reminder was delivered")
        evidence["suppressed"] = [
            "identifier": secondID, "due_at": stamp(second), "checked_at": stamp(Date()),
            "presented": watcher.presentedAt(secondID) != nil, "in_notification_center": listed.contains(secondID),
        ] as [String: Any]

        // Leave nothing of the run scheduled or shown.
        await reminders.disable()
        system.removeDeliveredNotifications(withIdentifiers: [firstID, secondID])
        evidence["passed"] = true
    }

    private func waitForDelivery(
        _ identifier: String, watcher: DeliveryWatcher, until deadline: Date
    ) async throws -> (at: Date, presented: Bool, listed: Bool) {
        let system = UNUserNotificationCenter.current()
        while Date() < deadline {
            let listed = await system.deliveredNotifications().first { $0.request.identifier == identifier }
            if let presented = watcher.presentedAt(identifier) {
                return (presented, true, listed != nil)
            }
            if let listed {
                return (listed.date, false, true)
            }
            try await Task.sleep(for: .seconds(1))
        }
        XCTFail("iOS did not deliver \(identifier) by \(stamp(deadline))")
        throw CancellationError()
    }

    private func stamp(_ date: Date) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.string(from: date)
    }

    @MainActor
    private func common() throws -> [String: Any] {
        var machine = utsname()
        uname(&machine)
        let identifier = withUnsafePointer(to: &machine.machine) {
            $0.withMemoryRebound(to: CChar.self, capacity: 1) { String(cString: $0) }
        }
        #if targetEnvironment(simulator)
        let simulator = true
        #else
        let simulator = false
        #endif
        let info = Bundle.main.infoDictionary ?? [:]
        return [
            "bundle_identifier": Bundle.main.bundleIdentifier ?? "",
            "bundle_build": info["CFBundleVersion"] as? String ?? "",
            "executables": try loadedExecutables(with: NotificationEvidenceTests.self),
            "device_model": "\(UIDevice.current.model) (\(identifier))",
            "system_version": UIDevice.current.systemVersion,
            "simulator": simulator,
            "bridge_version": supportedBridgeVersion,
        ]
    }
}

/// No exchange folder: the run never touches the author's.
private struct UnchosenAccess: ExchangeAccess {
    var folderName: String? { nil }

    func open(_ cancellation: ExchangeCancellation) throws -> (folder: any ExchangeFolder, close: @Sendable () -> Void) {
        throw ExchangeFault.notChosen
    }
}
