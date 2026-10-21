import Foundation
import Observation
import SenecaEngine
import UIKit

/// What the screens show of sync, and when an exchange runs: at launch, when
/// the app comes forward, after a save, on pull to refresh, on a retry while
/// something is still arriving, and once, as far as iOS allows, when the app
/// goes to the background.
@MainActor
@Observable
final class SyncModel {
    enum Phase: Equatable {
        /// No folder is chosen. Everything is saved on this iPhone only.
        case off
        /// A folder is chosen and the journal takes no part in sync yet.
        /// `journalInFolder` says whether the folder already carries one, and
        /// `arriving` whether its files are still on their way here.
        case setup(journalInFolder: Bool, arriving: Bool)
        case exchanging
        /// The last exchange finished and nothing is known to be on its way.
        case idle
        /// The last exchange finished, and files the folder lists have not
        /// reached this iPhone yet. It is tried again.
        case arriving(Int)
        case problem(ExchangeFault)
        /// The engine refused, with its own message.
        case refused(String)
    }

    enum Reason: Equatable {
        case launch, foreground, save, refresh, retry, background, setup
    }

    private(set) var phase: Phase = .off
    private(set) var folderName: String?
    private(set) var delivery: Delivery?
    private(set) var conflicts = 0
    private(set) var lastExchange: Date?
    /// Every exchange that ran and why, oldest first, for tests.
    private(set) var exchanges: [Reason] = []

    /// Called when an import replaced a journal file the screens show.
    var onJournalChanged: (@MainActor () async -> Void)?
    /// How long to wait before trying again, by how many tries came before.
    var retryDelay: (Int) -> Duration = { attempt in
        .seconds(min(60, 2 << min(attempt, 5)))
    }

    private let service: ExchangeService
    private let deviceName: String
    private var running: Task<Void, Never>?
    private var again = false
    private var current: ExchangeCancellation?
    private var retry: Task<Void, Never>?
    private var attempts = 0
    private var active = true

    init(service: ExchangeService, deviceName: String) {
        self.service = service
        self.deviceName = deviceName
        folderName = service.folderName
    }

    var isEnrolled: Bool {
        delivery != nil
    }

    /// Whether the journal's sync history is this iPhone's own, and not a
    /// copy restored from another device.
    func ownsIdentity() async -> Bool {
        await service.ownsIdentity()
    }

    /// Where the engine reads the exchange: this iPhone's mirror of it.
    var mirrorDirectory: URL {
        service.mirrorDirectory
    }

    /// Reads where things stood when the app last ran, then exchanges.
    func start() async {
        await refreshDelivery()
        await exchange(.launch)
    }

    /// Reads what is recorded now against what was last seen of the folder.
    /// It touches nothing outside the app's container.
    func refreshDelivery() async {
        if let stands = try? await service.delivery() {
            delivery = stands
        }
    }

    /// One exchange. A request made while one runs is not a second exchange
    /// beside it: it runs once more when the first is done, so what the
    /// request was about is not missed.
    func exchange(_ reason: Reason) async {
        guard service.folderName != nil else {
            phase = .off
            return
        }
        if let running {
            again = true
            await running.value
            return
        }
        let task = Task { @MainActor in
            repeat {
                again = false
                await once(reason)
            } while again
            running = nil
        }
        running = task
        await task.value
    }

    private func once(_ reason: Reason) async {
        retry?.cancel()
        retry = nil
        exchanges.append(reason)
        let cancellation = ExchangeCancellation()
        current = cancellation
        defer { current = nil }
        phase = .exchanging

        do {
            let report = try await service.exchange(cancellation)
            lastExchange = Date()
            if report.enrolled {
                delivery = report.delivery
                conflicts = report.conflicts
                phase = report.settled ? .idle : .arriving(report.arriving)
            } else {
                delivery = nil
                phase = .setup(journalInFolder: report.pulled.journalInFolder, arriving: !report.settled)
            }
            if report.journalChanged {
                await onJournalChanged?()
            }
            if report.settled {
                attempts = 0
            } else {
                scheduleRetry()
            }
        } catch let interrupted as ExchangeInterrupted {
            // What was imported is in the journal, whatever stopped the rest.
            await failed(interrupted.error)
            await onJournalChanged?()
        } catch {
            await failed(error)
        }
    }

    private func failed(_ error: any Error) async {
        switch error {
        case let fault as ExchangeFault:
            if fault == .notChosen {
                folderName = nil
                phase = .off
                return
            }
            phase = .problem(fault)
            await refreshDelivery()
            if fault.retryable {
                scheduleRetry()
            }
        case let problem as EngineProblem:
            phase = .refused(problem.message)
            if problem.retryable {
                scheduleRetry()
            }
        default:
            phase = .refused(error.localizedDescription)
        }
    }

    /// Tries again later, with a wait that grows, while the app is in front.
    /// In the background nothing is scheduled: the next launch or return
    /// exchanges anyway.
    private func scheduleRetry() {
        guard active else { return }
        let delay = retryDelay(attempts)
        attempts += 1
        retry = Task { @MainActor [weak self] in
            try? await Task.sleep(for: delay)
            guard !Task.isCancelled else { return }
            await self?.exchange(.retry)
        }
    }

    /// Stops the exchange under way. What it did not finish, the next does.
    func cancel() {
        current?.cancel()
    }

    // MARK: Lifecycle

    func enterForeground() async {
        active = true
        await exchange(.foreground)
    }

    /// One exchange in whatever time iOS grants after the app leaves the
    /// screen. It is cancelled when that time runs out, which loses nothing.
    func enterBackground() async {
        active = false
        retry?.cancel()
        retry = nil
        guard service.folderName != nil, isEnrolled else { return }
        var identifier = UIBackgroundTaskIdentifier.invalid
        identifier = UIApplication.shared.beginBackgroundTask(withName: "seneca.exchange") { [weak self] in
            MainActor.assumeIsolated {
                self?.cancel()
            }
        }
        await exchange(.background)
        if identifier != .invalid {
            UIApplication.shared.endBackgroundTask(identifier)
        }
    }

    // MARK: Setting up

    /// Takes the folder the author picked as the exchange folder.
    func choose(_ picked: URL, in store: GrantStore) async {
        cancel()
        await running?.value
        do {
            try store.choose(picked)
        } catch let fault as ExchangeFault {
            phase = .problem(fault)
            return
        } catch {
            phase = .refused(error.localizedDescription)
            return
        }
        // The mirror was the last folder's.
        await service.reset()
        folderName = service.folderName
        attempts = 0
        await exchange(.setup)
    }

    /// Makes this iPhone the first device of a synced journal, and publishes
    /// the journal to the folder.
    func enroll() async {
        await settingUp { try await self.service.enroll(name: self.deviceName) }
    }

    /// Makes this iPhone another device of the journal the folder carries.
    func join() async {
        await settingUp { try await self.service.join(name: self.deviceName) }
    }

    // MARK: Leaving

    /// Stops syncing on this iPhone. The journal stays whole here, its device
    /// identity is retired, and the folder is forgotten; choosing a folder
    /// again later joins as a new device.
    func disconnect(forgetting store: GrantStore?) async {
        cancel()
        await running?.value
        do {
            _ = try await service.leave(forgetFolder: true)
        } catch {
            phase = .refused(JournalModel.describe(error))
            return
        }
        store?.forget()
        folderName = service.folderName
        delivery = nil
        conflicts = 0
        attempts = 0
        phase = folderName == nil ? .off : .setup(journalInFolder: true, arriving: false)
        await onJournalChanged?()
    }

    /// Leaves and joins the folder's journal again as a new device: for a
    /// copy restored from another iPhone, or a sync history this version
    /// cannot read. Everything written here is kept and compared with what
    /// the other devices hold, never written over.
    func rejoin() async {
        cancel()
        await running?.value
        do {
            _ = try await service.leave(forgetFolder: false)
        } catch {
            phase = .refused(JournalModel.describe(error))
            return
        }
        delivery = nil
        conflicts = 0
        attempts = 0
        await join()
    }

    private func settingUp(_ step: @escaping () async throws -> Void) async {
        await running?.value
        do {
            try await step()
        } catch let problem as EngineProblem {
            phase = .refused(problem.message)
            return
        } catch {
            phase = .refused(error.localizedDescription)
            return
        }
        await refreshDelivery()
        await exchange(.setup)
        await onJournalChanged?()
    }
}
