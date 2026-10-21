import PDFKit
import SenecaEngine
import XCTest
@testable import SenecaMobile

/// Share packets under the composer: the engine's packet, written as the
/// file the share sheet sends, in Markdown or on pages.
@MainActor
final class ShareTests: XCTestCase {
    func testThePacketIsWrittenAsMarkdownAndAsPages() async throws {
        await reportingOnMain {
            let roundTrip = try RoundTrip.load()
            let root = try scratchDirectory(self)
            let location = try roundTrip.seed(in: root)
            let model = JournalModel(engine: SenecaEngine(), location: { location })
            await model.open()
            let weekly = try await model.reviews(period: "weekly")
            let review = try XCTUnwrap(weekly.first)

            let packet = try await model.sharePacket(reviewID: review.id, dates: ["2026-03-04"])
            XCTAssertEqual(packet.dates, ["2026-03-04"])
            XCTAssertFalse(packet.markdown.contains("<!--"))

            let share = root.appendingPathComponent("Share", isDirectory: true)
            let markdown = try PacketDocument.write(packet, as: .markdown, into: share)
            XCTAssertEqual(markdown.lastPathComponent, packet.fileName + ".md")
            XCTAssertEqual(try String(contentsOf: markdown, encoding: .utf8), packet.markdown)

            let pdf = try PacketDocument.write(packet, as: .pdf, into: share)
            XCTAssertFalse(FileManager.default.fileExists(atPath: markdown.path), "one packet at a time")
            let document = try XCTUnwrap(PDFDocument(url: pdf))
            XCTAssertGreaterThanOrEqual(document.pageCount, 1)
            let text = try XCTUnwrap(document.string)
            XCTAssertTrue(text.contains(review.title), text)
            XCTAssertTrue(text.contains("2026-03-04"), text)
            XCTAssertFalse(text.contains("###"), "headings are set as headings, not as Markdown")
        }
    }

    func testAPacketOfNothingIsRefused() async throws {
        let roundTrip = try RoundTrip.load()
        let location = try roundTrip.seed(in: try scratchDirectory(self))
        let model = JournalModel(engine: SenecaEngine(), location: { location })
        await model.open()
        await refusedOnMain("invalid-query") { try await model.sharePacket(reviewID: nil, dates: []) }
        await refusedOnMain("not-found") { try await model.sharePacket(reviewID: nil, dates: ["2026-03-06"]) }
    }
}
