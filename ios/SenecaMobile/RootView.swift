import SenecaEngine
import SwiftUI

/// What the tabs ask the root to present over them.
@MainActor
@Observable
final class AppRouter {
    /// A day being written.
    struct Composition: Identifiable, Equatable {
        let date: String
        var blank = false
        /// A version of the day the author did not keep, shown beside the
        /// form while they edit the one they did.
        var reference: [String] = []
        var id: String { date }
    }

    enum Tab: Hashable {
        case today, history, reviews
    }

    var tab: Tab = .today
    var composing: Composition?
    var showingSettings = false
    /// A pack file opened from another app, shown before it is applied.
    var openedPack: PracticePack?
    /// Why a file opened from another app is not a pack Seneca can use.
    var openProblem: String?
    /// A file opened before the journal was, read once it is.
    var pendingFile: URL?

    /// A day asked for while the settings were shown, written once they
    /// are gone: one sheet at a time.
    private var waiting: Composition?

    func write(_ date: String, blank: Bool = false, reference: [String] = []) {
        let composition = Composition(date: date, blank: blank, reference: reference)
        if showingSettings {
            waiting = composition
            showingSettings = false
        } else {
            composing = composition
        }
    }

    func settingsClosed() {
        if let waiting {
            self.waiting = nil
            composing = waiting
        }
    }
}

struct RootView: View {
    @Environment(JournalModel.self) private var journal
    @Environment(\.scenePhase) private var scenePhase
    @State private var router = AppRouter()

    var body: some View {
        content
            .environment(router)
            .onChange(of: scenePhase) { _, phase in
                guard journal.phase == .ready else { return }
                switch phase {
                case .active:
                    Task { await journal.cameForward() }
                case .background:
                    Task { await journal.wentBack() }
                default:
                    break
                }
            }
            // The day turned, the clock was set, or the iPhone changed zone.
            .onReceive(NotificationCenter.default.publisher(for: UIApplication.significantTimeChangeNotification)) { _ in
                Task { await journal.clockChanged() }
            }
            .onReceive(NotificationCenter.default.publisher(for: .NSSystemTimeZoneDidChange)) { _ in
                Task { await journal.clockChanged() }
            }
            // A .seneca-pack.json from AirDrop, Files, Mail, or a link.
            .onOpenURL { url in
                router.pendingFile = url
                Task { await readPendingFile() }
            }
            .onChange(of: journal.phase) { _, phase in
                if phase == .ready { Task { await readPendingFile() } }
            }
    }

    private func readPendingFile() async {
        guard journal.phase == .ready, let url = router.pendingFile else { return }
        router.pendingFile = nil
        do {
            let pack = try await journal.readPack(at: url)
            router.showingSettings = false
            router.openedPack = pack
        } catch {
            router.openProblem = JournalModel.describe(error)
        }
    }

    @ViewBuilder
    private var content: some View {
        switch journal.phase {
        case .opening:
            ProgressView("Opening the journal…")
        case .failed(let message):
            ContentUnavailableView {
                Label("The journal could not be opened", systemImage: "exclamationmark.triangle")
            } description: {
                Text(message)
            } actions: {
                Button("Try again") { Task { await journal.open() } }
            }
        case .ready:
            @Bindable var router = router
            Group {
                if journal.onboarding {
                    OnboardingView()
                } else {
                    tabs
                }
            }
            .sheet(item: $router.openedPack) { pack in
                NavigationStack {
                    PackDetailView(pack: pack) { _ in router.openedPack = nil }
                        .toolbar {
                            ToolbarItem(placement: .cancellationAction) {
                                Button("Close") { router.openedPack = nil }
                                    .accessibilityIdentifier("pack-close")
                            }
                        }
                }
            }
            .alert("This file cannot be used", isPresented: Binding(
                get: { router.openProblem != nil }, set: { if !$0 { router.openProblem = nil } }
            )) {
                Button("OK", role: .cancel) {}
            } message: {
                Text(router.openProblem ?? "")
            }
        }
    }

    private var tabs: some View {
        @Bindable var router = router
        return TabView(selection: $router.tab) {
            Tab("Today", systemImage: "sun.max", value: AppRouter.Tab.today) {
                NavigationStack { TodayView() }
            }
            Tab("History", systemImage: "calendar", value: AppRouter.Tab.history) {
                NavigationStack { HistoryView() }
            }
            Tab("Reviews", systemImage: "text.book.closed", value: AppRouter.Tab.reviews) {
                NavigationStack { ReviewsView() }
            }
        }
        .sheet(item: $router.composing) { composition in
            ReflectionForm(date: composition.date, blank: composition.blank, reference: composition.reference)
        }
        .sheet(isPresented: $router.showingSettings, onDismiss: router.settingsClosed) {
            NavigationStack { SettingsView() }
        }
    }
}
