import PDFKit
import SenecaEngine
import SwiftUI

/// Sharing with another person: a review alone by default, or with the
/// entries of its days, or one day. The author sees exactly what leaves the
/// iPhone before the share sheet sends it anywhere; Seneca sends nothing.
struct ShareComposer: View {
    @Environment(JournalModel.self) private var journal
    @Environment(\.dismiss) private var dismiss

    /// The review to share, nil for days alone.
    let reviewID: String?
    /// The days whose entries can go with it.
    let days: [String]

    @State private var chosen: Set<String>
    @State private var format: PacketDocument.Format = .markdown
    @State private var packet: SharePacket?
    @State private var file: URL?
    @State private var failure: String?

    init(reviewID: String?, days: [String], chosen: Set<String> = []) {
        self.reviewID = reviewID
        self.days = days
        _chosen = State(initialValue: chosen)
    }

    private struct Selection: Equatable {
        let chosen: Set<String>
        let format: PacketDocument.Format
    }

    /// One packet at a time, under the temporary directory: the share sheet
    /// copies what it sends, and the next packet replaces this one.
    private static let directory = FileManager.default.temporaryDirectory.appendingPathComponent("Share", isDirectory: true)

    var body: some View {
        NavigationStack {
            Form {
                if reviewID != nil, !days.isEmpty {
                    Section {
                        ForEach(days, id: \.self) { day in
                            Toggle(DayText.title(day), isOn: Binding(
                                get: { chosen.contains(day) },
                                set: { on in if on { chosen.insert(day) } else { chosen.remove(day) } }
                            ))
                            .accessibilityIdentifier("share-include-\(day)")
                        }
                    } header: {
                        Text("Entries of These Days")
                    } footer: {
                        Text("Off, only the review is shared. On, that day’s writing goes with it, as you wrote it.")
                    }
                }
                Section {
                    Picker("Format", selection: $format) {
                        ForEach(PacketDocument.Format.allCases) { format in
                            Text(format.rawValue).tag(format)
                        }
                    }
                    .pickerStyle(.segmented)
                    .accessibilityIdentifier("share-format")
                }
                Section {
                    if let failure {
                        Label(failure, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
                            .accessibilityIdentifier("share-failure")
                    } else if let packet {
                        preview(packet)
                    } else {
                        ProgressView()
                    }
                } header: {
                    Text("What Leaves This iPhone")
                } footer: {
                    Text("Exactly this, and only where you send it. It names no file on this iPhone and carries nothing Seneca keeps for itself.")
                }
            }
            .navigationTitle("Share")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .accessibilityIdentifier("share-cancel")
                }
                ToolbarItem(placement: .confirmationAction) {
                    if let file, let packet {
                        ShareLink(item: file, preview: SharePreview(packet.title)) {
                            Label("Send", systemImage: "square.and.arrow.up")
                        }
                        .accessibilityIdentifier("share-send")
                    }
                }
            }
            .task(id: Selection(chosen: chosen, format: format)) { await compose() }
        }
    }

    @ViewBuilder
    private func preview(_ packet: SharePacket) -> some View {
        switch format {
        case .markdown:
            Text(packet.markdown)
                .font(.system(.footnote, design: .monospaced))
                .textSelection(.enabled)
                .accessibilityIdentifier("share-preview")
        case .pdf:
            if let file {
                PDFPreview(url: file)
                    .frame(height: 480)
                    .accessibilityIdentifier("share-preview-pdf")
            }
        }
    }

    private func compose() async {
        file = nil
        do {
            let packet = try await journal.sharePacket(reviewID: reviewID, dates: days.filter(chosen.contains))
            self.packet = packet
            file = try PacketDocument.write(packet, as: format, into: Self.directory)
            failure = nil
        } catch {
            packet = nil
            failure = JournalModel.describe(error)
        }
    }
}

/// The PDF as it will be sent, page by page.
private struct PDFPreview: UIViewRepresentable {
    let url: URL

    func makeUIView(context: Context) -> PDFView {
        let view = PDFView()
        view.autoScales = true
        view.displayMode = .singlePageContinuous
        return view
    }

    func updateUIView(_ view: PDFView, context: Context) {
        if view.document?.documentURL != url {
            view.document = PDFDocument(url: url)
        }
    }
}
