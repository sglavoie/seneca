import Foundation
import SenecaEngine
import UIKit

/// A share packet as a reader opens it: its Markdown as a file, or the same
/// text laid out on pages. Both are made from the engine's packet and
/// nothing else, so the preview is what leaves the iPhone.
enum PacketDocument {
    enum Format: String, CaseIterable, Identifiable {
        case markdown = "Markdown"
        case pdf = "PDF"
        var id: String { rawValue }
        var fileExtension: String { self == .markdown ? "md" : "pdf" }
    }

    /// Writes the packet into a directory of its own under `parent`, named
    /// for what it holds, and returns the file. The previous packet goes.
    @MainActor
    static func write(_ packet: SharePacket, as format: Format, into parent: URL) throws -> URL {
        let manager = FileManager.default
        try? manager.removeItem(at: parent)
        // A directory per packet, so a packet rewritten under the same name
        // is a new file to whatever shows it.
        let directory = parent.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try manager.createDirectory(at: directory, withIntermediateDirectories: true)
        let file = directory.appendingPathComponent(packet.fileName).appendingPathExtension(format.fileExtension)
        switch format {
        case .markdown:
            try Data(packet.markdown.utf8).write(to: file, options: .atomic)
        case .pdf:
            try pdf(packet).write(to: file, options: .atomic)
        }
        return file
    }

    // MARK: Pages

    /// US Letter, with an inch less three quarters on every side.
    static let page = CGRect(x: 0, y: 0, width: 612, height: 792)
    static let margin: CGFloat = 54

    @MainActor
    static func pdf(_ packet: SharePacket) -> Data {
        let formatter = UISimpleTextPrintFormatter(attributedText: attributed(packet.markdown))
        let renderer = UIPrintPageRenderer()
        renderer.addPrintFormatter(formatter, startingAtPageAt: 0)
        // The renderer has no setters for its page; these keys are how it is
        // given one outside a print job.
        renderer.setValue(page, forKey: "paperRect")
        renderer.setValue(page.insetBy(dx: margin, dy: margin), forKey: "printableRect")

        let format = UIGraphicsPDFRendererFormat()
        format.documentInfo = [kCGPDFContextTitle as String: packet.title, kCGPDFContextCreator as String: "Seneca"]
        return UIGraphicsPDFRenderer(bounds: page, format: format).pdfData { context in
            renderer.prepare(forDrawingPages: NSRange(location: 0, length: renderer.numberOfPages))
            for index in 0..<renderer.numberOfPages {
                context.beginPage()
                renderer.drawPage(at: index, in: context.pdfContextBounds)
            }
        }
    }

    // MARK: Text

    /// The Markdown laid out line by line: headings set larger, quotes
    /// indented, list items marked, a rule as space, and bold and italic
    /// kept. Nothing is added or left out.
    static func attributed(_ markdown: String) -> NSAttributedString {
        let text = NSMutableAttributedString()
        let body = UIFont.systemFont(ofSize: 11)
        for line in markdown.components(separatedBy: "\n") {
            let paragraph = NSMutableParagraphStyle()
            paragraph.paragraphSpacing = 2
            var content = line
            var font = body
            var color = UIColor.black
            if let heading = line.firstMatch(of: #/^(#{1,6}) (.*)$/#) {
                content = String(heading.2)
                font = .systemFont(ofSize: [20, 15, 12.5, 11, 11, 11][heading.1.count - 1], weight: .semibold)
                paragraph.paragraphSpacingBefore = heading.1.count == 1 ? 0 : 8
                paragraph.paragraphSpacing = 4
            } else if line.trimmingCharacters(in: .whitespaces) == "---" {
                content = ""
                paragraph.paragraphSpacing = 10
            } else if line.hasPrefix(">") {
                content = String(line.dropFirst()).trimmingCharacters(in: .whitespaces)
                font = .italicSystemFont(ofSize: 11)
                color = .darkGray
                paragraph.firstLineHeadIndent = 14
                paragraph.headIndent = 14
            } else if let item = line.firstMatch(of: #/^(\s*)[-*] (.*)$/#) {
                content = "•\t" + item.2
                let indent = CGFloat(14 + 10 * item.1.count)
                paragraph.headIndent = indent
                paragraph.tabStops = [NSTextTab(textAlignment: .left, location: indent)]
                paragraph.firstLineHeadIndent = indent - 14
            }
            text.append(inline(content, font: font, color: color, paragraph: paragraph))
            text.append(NSAttributedString(string: "\n", attributes: [.font: font, .paragraphStyle: paragraph]))
        }
        return text
    }

    /// One line, with its **bold** and _italic_ set as such.
    private static func inline(_ line: String, font: UIFont, color: UIColor, paragraph: NSParagraphStyle) -> NSAttributedString {
        let options = AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        guard let parsed = try? AttributedString(markdown: line, options: options) else {
            return NSAttributedString(string: line, attributes: [.font: font, .foregroundColor: color, .paragraphStyle: paragraph])
        }
        let result = NSMutableAttributedString()
        for run in parsed.runs {
            var traits = font.fontDescriptor.symbolicTraits
            if let intent = run.inlinePresentationIntent {
                if intent.contains(.stronglyEmphasized) { traits.insert(.traitBold) }
                if intent.contains(.emphasized) { traits.insert(.traitItalic) }
            }
            let styled = font.fontDescriptor.withSymbolicTraits(traits).map { UIFont(descriptor: $0, size: font.pointSize) } ?? font
            result.append(NSAttributedString(
                string: String(parsed[run.range].characters),
                attributes: [.font: styled, .foregroundColor: color, .paragraphStyle: paragraph]
            ))
        }
        return result
    }
}
