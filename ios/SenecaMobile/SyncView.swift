import SenecaEngine
import SwiftUI
import UniformTypeIdentifiers

/// One line saying where this iPhone's writing has got to.
enum SyncText {
    /// The furthest thing that is true of everything recorded here.
    static func summary(_ delivery: Delivery) -> String {
        guard delivery.recorded > 0 else { return "Nothing to send yet." }
        if let peer = delivery.peers.max(by: { $0.applied < $1.applied }), peer.applied >= delivery.recorded {
            return "Everything is on \(name(peer))."
        }
        if delivery.published >= delivery.recorded {
            return delivery.uploaded >= delivery.recorded
                ? "Everything is in iCloud, and not on another device yet."
                : "Everything is in the exchange folder, waiting to upload."
        }
        let waiting = delivery.recorded - delivery.published
        return waiting == 1
            ? "1 change is saved on this iPhone only."
            : "\(waiting) changes are saved on this iPhone only."
    }

    static func name(_ peer: SyncPeer) -> String {
        peer.name.isEmpty ? "device \(peer.device.prefix(8))" : peer.name
    }

    /// The phase as a code, for diagnostics: no folder name, no message.
    static func code(_ phase: SyncModel.Phase) -> String {
        switch phase {
        case .off: return "off"
        case .setup: return "setup"
        case .exchanging: return "exchanging"
        case .idle: return "idle"
        case .arriving: return "arriving"
        case .problem(let fault):
            switch fault {
            case .notChosen: return "not-chosen"
            case .revoked: return "revoked"
            case .unavailable: return "unavailable"
            case .cancelled: return "cancelled"
            case .differentJournal: return "different-journal"
            case .identityInUse: return "identity-in-use"
            }
        case .refused: return "refused"
        }
    }

    static func problem(_ phase: SyncModel.Phase) -> String? {
        switch phase {
        case .problem(let fault): return fault.message
        case .refused(let message): return message
        case .arriving(let count):
            return count == 1
                ? "1 file is still arriving from iCloud."
                : "\(count) files are still arriving from iCloud."
        default: return nil
        }
    }
}

/// The exchange with the journal's other devices: which folder, whether this
/// iPhone takes part, and where its writing has got to.
struct SyncView: View {
    @Environment(JournalModel.self) private var journal
    @State private var choosing = false

    var body: some View {
        List {
            if let sync = journal.sync {
                folder(sync)
                switch sync.phase {
                case .off:
                    EmptyView()
                case .setup(let journalInFolder, let arriving):
                    setup(sync, journalInFolder: journalInFolder, arriving: arriving)
                default:
                    if sync.isEnrolled {
                        standing(sync)
                    } else if sync.phase == .exchanging {
                        Section {
                            HStack {
                                ProgressView()
                                Text("Looking at the folder…")
                            }
                        }
                    }
                }
                if let problem = SyncText.problem(sync.phase) {
                    Section {
                        Label(problem, systemImage: "exclamationmark.triangle")
                            .accessibilityIdentifier("sync-problem")
                    }
                }
            }
        }
        .navigationTitle("Sync")
        .navigationBarTitleDisplayMode(.inline)
        .refreshable { await journal.refresh() }
        .fileImporter(isPresented: $choosing, allowedContentTypes: [.folder]) { result in
            guard case .success(let picked) = result, let sync = journal.sync, let store = journal.grantStore else { return }
            Task { await sync.choose(picked, in: store) }
        }
    }

    @ViewBuilder
    private func folder(_ sync: SyncModel) -> some View {
        Section {
            if let name = sync.folderName {
                LabeledContent("Folder", value: name).accessibilityIdentifier("sync-folder")
            }
            Button(sync.folderName == nil ? "Choose Exchange Folder" : "Choose Another Folder", systemImage: "folder") {
                choosing = true
            }
            .accessibilityIdentifier("sync-choose")
        } header: {
            Text("Exchange Folder")
        } footer: {
            Text("Your journal stays on this iPhone. Seneca copies each saved change into a folder you choose in iCloud Drive, and reads the changes your Mac puts there.")
        }
    }

    @ViewBuilder
    private func setup(_ sync: SyncModel, journalInFolder: Bool, arriving: Bool) -> some View {
        Section {
            if journalInFolder && arriving {
                Label("This folder carries a journal that is still arriving from iCloud.", systemImage: "icloud.and.arrow.down")
                    .accessibilityIdentifier("sync-arriving")
            } else if journalInFolder {
                Button("Join the Journal in This Folder", systemImage: "arrow.triangle.merge") {
                    Task { await sync.join() }
                }
                .accessibilityIdentifier("sync-join")
            } else {
                Button("Start Syncing from This iPhone", systemImage: "arrow.up.doc") {
                    Task { await sync.enroll() }
                }
                .accessibilityIdentifier("sync-enroll")
            }
        } footer: {
            Text(journalInFolder
                ? "Joining brings that journal here and keeps what is already written on this iPhone."
                : "This folder carries no journal yet. Starting here publishes this iPhone’s journal to it. If your Mac already syncs a journal, choose its folder instead.")
        }
    }

    @ViewBuilder
    private func standing(_ sync: SyncModel) -> some View {
        if let delivery = sync.delivery {
            Section {
                Text(SyncText.summary(delivery)).accessibilityIdentifier("sync-summary")
                LabeledContent("Saved on this iPhone", value: "\(delivery.recorded)")
                    .accessibilityIdentifier("sync-recorded")
                LabeledContent("In the exchange folder", value: "\(delivery.published) of \(delivery.recorded)")
                    .accessibilityIdentifier("sync-published")
                LabeledContent("Uploaded to iCloud", value: "\(delivery.uploaded) of \(delivery.recorded)")
                    .accessibilityIdentifier("sync-uploaded")
                ForEach(delivery.peers, id: \.device) { peer in
                    LabeledContent("Applied on \(SyncText.name(peer))", value: "\(peer.applied) of \(delivery.recorded)")
                        .accessibilityIdentifier("sync-applied-\(peer.device)")
                }
            } header: {
                Text("Changes")
            } footer: {
                Text("A change counts as applied on another device only when that device says it is in its own journal.")
            }
        }
        Section {
            if sync.conflicts > 0 || journal.conflictCount > 0 {
                NavigationLink {
                    ConflictsView()
                } label: {
                    Label(
                        journal.conflictCount == 1
                            ? "1 change was made differently on two devices. Both versions are kept until you choose."
                            : "\(max(journal.conflictCount, sync.conflicts)) changes were made differently on two devices. Both versions are kept until you choose.",
                        systemImage: "arrow.triangle.branch"
                    )
                }
                .accessibilityIdentifier("sync-conflicts")
            }
            if sync.phase == .exchanging {
                HStack {
                    ProgressView()
                    Text("Exchanging…")
                }
            } else {
                Button("Exchange Now", systemImage: "arrow.triangle.2.circlepath") {
                    Task { await journal.refresh() }
                }
                .accessibilityIdentifier("sync-now")
            }
            if let last = sync.lastExchange {
                LabeledContent("Last exchange") {
                    Text(last, style: .relative)
                }
            }
        }
    }
}
