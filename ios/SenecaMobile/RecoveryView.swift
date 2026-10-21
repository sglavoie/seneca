import SenecaEngine
import SwiftUI
import UIKit
import UniformTypeIdentifiers

/// What this iPhone shares when asked for diagnostics: the engine's
/// content-free report and the app around it. No text, date, path, question,
/// or name: versions, counts, and codes.
struct DiagnosticsReport: Encodable {
    struct App: Encodable {
        var version: String
        var build: String
        var bridgeVersion: Int
        var system: String
        var model: String
        var draftsOnDevice: Int
        var remindersEnabled: Bool
        var remindersPermission: String
        var remindersScheduled: Int
        var remindersCustomTimes: Bool
        var folderChosen: Bool
        var syncState: String
        var ownIdentity: Bool
        var audioTakes: Int
        var audioBytes: Int
        var audioPendingTranscriptions: Int
        var speechModel: String
        var audioRetention: String
    }

    var diagnostics: Diagnostics
    var app: App

    @MainActor
    static func make(_ diagnostics: Diagnostics, journal: JournalModel, ownIdentity: Bool) -> DiagnosticsReport {
        let info = Bundle.main.infoDictionary ?? [:]
        let reminders = journal.reminders
        let voice = journal.voice?.summary
        return DiagnosticsReport(diagnostics: diagnostics, app: App(
            version: info["CFBundleShortVersionString"] as? String ?? "",
            build: info["CFBundleVersion"] as? String ?? "",
            bridgeVersion: supportedBridgeVersion,
            system: "\(UIDevice.current.systemName) \(UIDevice.current.systemVersion)",
            model: UIDevice.current.model,
            draftsOnDevice: journal.drafts.count,
            remindersEnabled: reminders?.preferences.enabled ?? false,
            remindersPermission: reminders?.permission.rawValue ?? "",
            remindersScheduled: reminders?.scheduled.count ?? 0,
            remindersCustomTimes: reminders?.preferences.times != nil,
            folderChosen: journal.sync?.folderName != nil,
            syncState: SyncText.code(journal.sync?.phase ?? .off),
            ownIdentity: ownIdentity,
            audioTakes: voice?.takes ?? 0,
            audioBytes: voice?.bytes ?? 0,
            audioPendingTranscriptions: voice?.pendingTranscriptions ?? 0,
            speechModel: voice?.speechModel ?? "unknown",
            audioRetention: voice?.retention ?? AudioRetention.keep.rawValue
        ))
    }

    var text: String {
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
        guard let data = try? encoder.encode(self) else { return "{}" }
        return String(decoding: data, as: UTF8.self)
    }
}

/// What a finding means on this iPhone, and what the author can do here.
struct PhoneAdvice: Equatable {
    enum Action: Equatable {
        case none, exchange, conflicts, rejoin, restore, sync, reminders
    }

    var code: String
    var severity: String
    var message: String
    var action: Action

    static func of(_ finding: Diagnostics.Finding) -> PhoneAdvice {
        let n = finding.count
        func changes(_ count: Int) -> String { count == 1 ? "1 change" : "\(count) changes" }
        let (message, action): (String, Action) = switch finding.code {
        case "sync-state-newer":
            ("This journal’s sync history was written by a newer Seneca, so nothing is exchanged. Update Seneca on this iPhone, or rejoin as this iPhone.", .rejoin)
        case "sync-state-unreadable":
            ("This journal’s sync history cannot be read, so nothing is exchanged. Rejoin as this iPhone: everything written here is kept.", .rejoin)
        case "exchange-foreign":
            ("The exchange folder carries another journal. Choose this journal’s folder.", .sync)
        case "exchange-newer-protocol":
            ("The exchange folder was started by a newer Seneca. Update Seneca on this iPhone.", .none)
        case "exchange-unreachable":
            ("The exchange folder could not be read. Everything is saved here and is sent once it can be.", .sync)
        case "exchange-empty":
            ("The exchange folder holds no journal yet.", .sync)
        case "identity-reused":
            ("\(changes(n)) in the exchange folder carry this iPhone’s identity but were not made here: another installation is a copy of this one. Rejoin as this iPhone on the copy; nothing written is lost.", .rejoin)
        case "changes-incompatible":
            ("\(changes(n)) from another device need a newer Seneca. They wait, and nothing is lost: update Seneca on this iPhone.", .none)
        case "changes-unpublished":
            ("\(changes(n)) saved here \(n == 1 ? "is" : "are") not in the exchange folder yet.", .exchange)
        case "changes-unimported":
            ("\(changes(n)) from other devices \(n == 1 ? "has" : "have") not been brought in yet.", .exchange)
        case "changes-waiting":
            ("\(changes(n)) wait for the changes they follow to arrive. Nothing to do.", .none)
        case "changes-blocked":
            ("\(changes(n)) cannot be applied to the journal as it is. On your Mac, `seneca sync status` says why.", .none)
        case "file-suspended":
            ("Sync stopped reading \(n == 1 ? "a journal file that" : "\(n) journal files that") went missing or lost most of what was in \(n == 1 ? "it" : "them"). Restore what was lost from the journal’s history, or decide on your Mac.", .restore)
        case "conflicts-open":
            ("\(n == 1 ? "1 change was" : "\(n) changes were") made differently on two devices. Both versions are kept until you choose.", .conflicts)
        case "commit-interrupted":
            ("A save was interrupted; the next save finishes it.", .none)
        case "reflections-invalid", "reflections-unreadable":
            ("The reflections file holds something Seneca will not rewrite. Your Mac’s `seneca doctor` names it.", .none)
        case "archive-invalid":
            ("The review archive holds something Seneca will not append to. Your Mac’s `seneca doctor` names it.", .none)
        case "settings-invalid":
            ("The journal’s settings cannot be read. Your Mac’s `seneca doctor` names why.", .none)
        default:
            (finding.code, .none)
        }
        return PhoneAdvice(code: finding.code, severity: finding.severity, message: message, action: action)
    }

    /// Everything that needs saying: the engine's findings, the exchange
    /// folder's state, and the reminders' permission.
    @MainActor
    static func list(_ diagnostics: Diagnostics?, journal: JournalModel) -> [PhoneAdvice] {
        var found = (diagnostics?.findings ?? []).map(PhoneAdvice.of)
        if let sync = journal.sync, case .problem(let fault) = sync.phase {
            found.insert(PhoneAdvice(
                code: "exchange-fault", severity: fault.retryable ? "warn" : "error", message: fault.message,
                action: fault == .identityInUse ? .rejoin : .sync
            ), at: 0)
        }
        if let reminders = journal.reminders, reminders.preferences.enabled, reminders.state == .denied {
            found.append(PhoneAdvice(
                code: "notifications-denied", severity: "warn",
                message: "Reminders are on, but notifications are turned off for Seneca in Settings. Writing is not affected.",
                action: .reminders
            ))
        }
        return found
    }
}

/// Where the app says what is wrong and what to do, shares diagnostics that
/// hold nothing written, exports the whole journal when asked, and leaves or
/// rejoins sync.
struct RecoveryView: View {
    @Environment(JournalModel.self) private var journal
    @State private var diagnostics: Diagnostics?
    @State private var problem: String?
    @State private var ownIdentity = true
    @State private var exported: RecoveryKit?
    @State private var includingAudio = false
    @State private var choosingKit = false
    @State private var restoredAudio: String?
    @State private var exporting = false
    @State private var confirmingExport = false
    @State private var confirmingRejoin = false
    @State private var confirmingDisconnect = false
    @State private var working = false

    private var advice: [PhoneAdvice] {
        PhoneAdvice.list(diagnostics, journal: journal)
    }

    var body: some View {
        List {
            Section {
                if diagnostics == nil && problem == nil {
                    ProgressView()
                }
                if let problem {
                    Label(problem, systemImage: "exclamationmark.triangle")
                }
                let advice = advice
                if diagnostics != nil && advice.isEmpty {
                    Label("Nothing needs attention.", systemImage: "checkmark.circle")
                        .accessibilityIdentifier("recovery-healthy")
                }
                ForEach(advice, id: \.code) { item in
                    row(item)
                }
            } header: {
                Text("Status")
            }

            Section {
                if let diagnostics {
                    let report = DiagnosticsReport.make(diagnostics, journal: journal, ownIdentity: ownIdentity).text
                    ShareLink(item: report, subject: Text("Seneca diagnostics"), preview: SharePreview("Seneca diagnostics")) {
                        Label("Share Diagnostics", systemImage: "stethoscope")
                    }
                    .accessibilityIdentifier("recovery-share-diagnostics")
                    DisclosureGroup("What Is Shared") {
                        Text(report)
                            .font(.caption.monospaced())
                            .textSelection(.enabled)
                            .accessibilityIdentifier("recovery-diagnostics-report")
                    }
                }
            } header: {
                Text("Diagnostics")
            } footer: {
                Text("Versions, counts, and codes only: nothing you wrote, no dates, no questions, and no names.")
            }

            Section {
                Button("Export Journal…", systemImage: "square.and.arrow.up.on.square") {
                    confirmingExport = true
                }
                .disabled(exporting)
                .accessibilityIdentifier("recovery-export")
                if let voice = journal.voice, !voice.takes.isEmpty {
                    Toggle("Include Spoken Reflections", isOn: $includingAudio)
                        .accessibilityIdentifier("recovery-include-audio")
                }
                if let exported {
                    Text(summary(exported)).accessibilityIdentifier("recovery-exported")
                    ShareLink(item: exported.directory) {
                        Label("Save or Send the Export", systemImage: "folder")
                    }
                    .accessibilityIdentifier("recovery-share-export")
                }
            } header: {
                Text("Recovery Export")
            } footer: {
                Text("A copy of everything you wrote: every day, the reviews, the questions, both versions of anything two devices wrote differently, and writing not saved yet. It holds no device identity, so a journal restored from it joins sync as a new device. Spoken reflections, when included, go in their own audio folder beside it.")
            }

            if journal.voice != nil {
                Section {
                    Button("Restore Spoken Reflections…", systemImage: "waveform.badge.plus") {
                        choosingKit = true
                    }
                    .accessibilityIdentifier("recovery-restore-audio")
                    if let restoredAudio {
                        Text(restoredAudio).accessibilityIdentifier("recovery-restored-audio")
                    }
                } header: {
                    Text("Spoken Reflections")
                } footer: {
                    Text("Choose a recovery export that includes spoken reflections, or its audio folder. Recordings already on this iPhone are never replaced.")
                }
            }

            if let sync = journal.sync, sync.folderName != nil {
                Section {
                    Button("Rejoin as This iPhone", systemImage: "arrow.triangle.2.circlepath") {
                        confirmingRejoin = true
                    }
                    .accessibilityIdentifier("recovery-rejoin")
                    Button("Disconnect Sync", systemImage: "icloud.slash", role: .destructive) {
                        confirmingDisconnect = true
                    }
                    .accessibilityIdentifier("recovery-disconnect")
                } header: {
                    Text("Sync on This iPhone")
                } footer: {
                    Text("Both keep every word on this iPhone. Rejoining takes a new device identity and compares this iPhone’s writing with the other devices’; disconnecting stops exchanging and forgets the folder.")
                }
            }
        }
        .disabled(working)
        .navigationTitle("Diagnostics and Recovery")
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .refreshable { await load() }
        .fileImporter(isPresented: $choosingKit, allowedContentTypes: [.folder]) { result in
            guard case .success(let url) = result else { return }
            restoreAudio(from: url)
        }
        .confirmationDialog("Export the whole journal?", isPresented: $confirmingExport, titleVisibility: .visible) {
            Button("Export Journal") { Task { await export() } }
                .accessibilityIdentifier("recovery-export-confirm")
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The copy holds everything you wrote. Keep it somewhere private.")
        }
        .confirmationDialog("Rejoin as this iPhone?", isPresented: $confirmingRejoin, titleVisibility: .visible) {
            Button("Rejoin") { Task { await perform { await journal.sync?.rejoin() } } }
                .accessibilityIdentifier("recovery-rejoin-confirm")
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This iPhone leaves sync and joins again as a new device. Nothing written here is lost; anything written differently elsewhere waits for you to choose.")
        }
        .confirmationDialog("Disconnect sync?", isPresented: $confirmingDisconnect, titleVisibility: .visible) {
            Button("Disconnect", role: .destructive) {
                Task { await perform { await journal.sync?.disconnect(forgetting: journal.grantStore) } }
            }
            .accessibilityIdentifier("recovery-disconnect-confirm")
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The journal stays on this iPhone. It stops exchanging with your other devices; choosing a folder again later joins as a new device.")
        }
    }

    @ViewBuilder
    private func row(_ item: PhoneAdvice) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Label(item.message, systemImage: item.severity == "info" ? "info.circle" : "exclamationmark.triangle")
                .accessibilityIdentifier("recovery-finding-\(item.code)")
            switch item.action {
            case .exchange:
                Button("Exchange Now") { Task { await perform { await journal.refresh() } } }
                    .accessibilityIdentifier("recovery-exchange-\(item.code)")
            case .conflicts:
                NavigationLink("Choose a Version") { ConflictsView() }
            case .rejoin:
                Button("Rejoin as This iPhone") { confirmingRejoin = true }
                    .accessibilityIdentifier("recovery-rejoin-\(item.code)")
            case .restore:
                ForEach(diagnostics?.sync.suspended ?? [], id: \.self) { file in
                    Button("Restore the \(file) file") {
                        Task { await perform { try? await journal.restore(file) } }
                    }
                }
            case .sync:
                NavigationLink("Sync") { SyncView() }
            case .reminders:
                NavigationLink("Reminders") { RemindersView() }
            case .none:
                EmptyView()
            }
        }
        .buttonStyle(.borderless)
    }

    private func summary(_ kit: RecoveryKit) -> String {
        let export = kit.journal
        var parts = ["\(export.files.count) files"]
        if export.conflictVersions > 0 {
            parts.append("\(export.conflictVersions) versions of open conflicts")
        }
        if export.drafts > 0 {
            parts.append(export.drafts == 1 ? "1 unsaved draft" : "\(export.drafts) unsaved drafts")
        }
        if let audio = kit.audio {
            parts.append(audio.takes == 1 ? "1 recording" : "\(audio.takes) recordings")
        }
        return "Exported " + parts.joined(separator: ", ") + "."
    }

    private func restoreAudio(from url: URL) {
        guard let voice = journal.voice else { return }
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        do {
            let restore = try voice.restore(from: url)
            var parts = [restore.restored == 1 ? "Restored 1 recording" : "Restored \(restore.restored) recordings"]
            if restore.alreadyHere > 0 { parts.append("\(restore.alreadyHere) already here") }
            if restore.kept > 0 { parts.append("\(restore.kept) kept as they are on this iPhone") }
            restoredAudio = parts.joined(separator: ", ") + "."
        } catch {
            restoredAudio = error.localizedDescription
        }
    }

    private func load() async {
        do {
            diagnostics = try await journal.diagnostics()
            problem = nil
        } catch {
            problem = JournalModel.describe(error)
        }
        ownIdentity = await journal.sync?.ownsIdentity() ?? true
        await journal.voice?.refreshModelState()
    }

    private func export() async {
        exporting = true
        defer { exporting = false }
        do {
            exported = try await journal.exportRecoveryKit(includingAudio: includingAudio)
        } catch {
            problem = JournalModel.describe(error)
        }
    }

    private func perform(_ step: @escaping () async -> Void) async {
        working = true
        await step()
        working = false
        await load()
    }
}
