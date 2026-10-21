import Foundation
import SenecaEngine

/// Where this installation keeps its journal and the engine's own state.
/// Both are inside the app's container, so installing a newer build over
/// this one keeps them.
enum AppPaths {
    /// Documents/Journal/reflections.md, with the review archive beside it.
    static func journalDirectory() throws -> URL {
        let documents = try FileManager.default.url(
            for: .documentDirectory, in: .userDomainMask, appropriateFor: nil, create: true
        )
        return documents.appendingPathComponent("Journal", isDirectory: true)
    }

    /// Application Support/Seneca/engine: the request ledger. Device-local.
    static func stateDirectory() throws -> URL {
        let support = try FileManager.default.url(
            for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true
        )
        return support.appendingPathComponent("Seneca/engine", isDirectory: true)
    }

    /// Where unsaved writing is kept: beside the engine's state, so it is
    /// device-local, outside the journal, and never exchanged.
    static func draftsDirectory(for location: JournalLocation) -> URL {
        location.stateDirectory.deletingLastPathComponent().appendingPathComponent("drafts", isDirectory: true)
    }

    /// Where this iPhone keeps what it needs to exchange: its grant to the
    /// folder the author chose, and its own copy of the exchange. Beside the
    /// engine's state, so it is device-local and outside the journal.
    static func exchangeDirectory(for location: JournalLocation) -> URL {
        location.stateDirectory.deletingLastPathComponent().appendingPathComponent("exchange", isDirectory: true)
    }

    /// Spoken reflections: `audio/<date>/<uuid>.m4a` and its sidecar
    /// `<uuid>.json`, beside the drafts. Device-local, outside the journal,
    /// never exchanged.
    static func audioDirectory(for location: JournalLocation) -> URL {
        location.stateDirectory.deletingLastPathComponent().appendingPathComponent("audio", isDirectory: true)
    }

    #if DEBUG
    /// The model evaluation's prompts and responses (Debug/ModelEvaluation):
    /// Application Support/Seneca/evaluation, beside the engine's state and
    /// outside the journal. `run.sh push` and `pull` copy them with devicectl.
    static func evaluationDirectory() throws -> URL {
        try journal().stateDirectory.deletingLastPathComponent().appendingPathComponent("evaluation", isDirectory: true)
    }
    #endif

    /// The language spoken reflections are transcribed in on this iPhone.
    static func voiceFile(for location: JournalLocation) -> URL {
        location.stateDirectory.deletingLastPathComponent().appendingPathComponent("voice.json")
    }

    /// The microphone, the transcribers and the speech models; on a
    /// simulator, a UI test can stand in its own.
    static func voiceDevices(root: URL) -> VoiceDevices {
        #if targetEnvironment(simulator)
        if let staged = StagedVoice.devices(root: root) {
            return staged
        }
        #endif
        return VoiceDevices.system(root: root)
    }

    /// The author's reminder preferences: device-local, never exchanged.
    static func remindersFile(for location: JournalLocation) -> URL {
        location.stateDirectory.deletingLastPathComponent().appendingPathComponent("reminders.json")
    }

    /// Whether the first-run setup has been offered on this installation.
    /// Device-local: a second device joins a journal already set up.
    static func onboardingFile(for location: JournalLocation) -> URL {
        location.stateDirectory.deletingLastPathComponent().appendingPathComponent("onboarding.json")
    }

    /// Whether an untouched journal is shown the first-run setup. A UI test
    /// staging a journal sees it only when it asks to.
    static var offersOnboarding: Bool {
        #if targetEnvironment(simulator)
        let environment = ProcessInfo.processInfo.environment
        if let root = environment["SENECA_UITEST_ROOT"], !root.isEmpty {
            return environment["SENECA_UITEST_ONBOARDING"] == "1"
        }
        #endif
        return true
    }

    /// iOS's notification center; on a simulator, a UI test can stand in an
    /// answer of its own for the author's.
    static func reminderCenter() -> any ReminderCenter {
        #if targetEnvironment(simulator)
        if let answer = ProcessInfo.processInfo.environment["SENECA_UITEST_NOTIFICATIONS"],
           let permission = ReminderPermission(rawValue: answer) {
            return StagedReminderCenter(permission: permission)
        }
        #endif
        return SystemReminderCenter()
    }

    /// The author's grant to the exchange folder.
    static func grantStore(for location: JournalLocation) -> GrantStore {
        GrantStore(file: exchangeDirectory(for: location).appendingPathComponent("grant.json"))
    }

    /// How the app reaches the exchange folder: through the author's grant.
    static func exchangeAccess(for location: JournalLocation) -> any ExchangeAccess {
        #if targetEnvironment(simulator)
        // A folder a UI test staged stands in for the one an author picks.
        if let staged = ProcessInfo.processInfo.environment["SENECA_UITEST_EXCHANGE"], !staged.isEmpty {
            return DirectoryAccess(directory: URL(fileURLWithPath: staged, isDirectory: true))
        }
        #endif
        return grantStore(for: location)
    }

    /// The installation's journal. Its directory is created; the reflections
    /// file is not: a journal that does not exist yet is an ordinary journal.
    static func journal() throws -> JournalLocation {
        #if targetEnvironment(simulator)
        if let staged = stagedJournal() {
            return staged
        }
        #endif
        let directory = try journalDirectory()
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        return JournalLocation(
            reflections: directory.appendingPathComponent("reflections.md"),
            stateDirectory: try stateDirectory()
        )
    }

    #if targetEnvironment(simulator)
    /// A journal a UI test staged for this launch: `journal/` and `state/`
    /// under SENECA_UITEST_ROOT, with the clock, zone, and questions the test
    /// names. Simulator builds only; an iPhone build has no such door.
    private static func stagedJournal() -> JournalLocation? {
        let environment = ProcessInfo.processInfo.environment
        guard let root = environment["SENECA_UITEST_ROOT"], !root.isEmpty else { return nil }
        let directory = URL(fileURLWithPath: root, isDirectory: true)
        var location = JournalLocation(
            reflections: directory.appendingPathComponent("journal/reflections.md"),
            stateDirectory: directory.appendingPathComponent("state", isDirectory: true)
        )
        if let questions = environment["SENECA_UITEST_QUESTIONS"] {
            location.questions = try? JSONDecoder().decode([String].self, from: Data(questions.utf8))
        }
        if let anchor = environment["SENECA_UITEST_ANCHOR"] {
            location.anchorQuestionIndex = Int(anchor)
        }
        if let zone = environment["SENECA_UITEST_TIME_ZONE"], let timeZone = TimeZone(identifier: zone) {
            location.timeZone = timeZone
        }
        if let now = environment["SENECA_UITEST_NOW"] {
            location.now = ISO8601DateFormatter().date(from: now)
        }
        return location
    }
    #endif
}
