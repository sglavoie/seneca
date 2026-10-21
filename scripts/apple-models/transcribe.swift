import Speech
import AVFoundation
import Foundation
@main struct T { static func main() async throws {
  let url = URL(fileURLWithPath: CommandLine.arguments[1]); let loc = Locale(identifier: CommandLine.arguments[2])
  guard let l = await SpeechTranscriber.supportedLocale(equivalentTo: loc) else { print("unsupported locale"); return }
  let tr = SpeechTranscriber(locale: l, preset: .transcription)
  if await AssetInventory.status(forModules: [tr]) != .installed {
    print("installing model for \(l.identifier)...")
    if let req = try await AssetInventory.assetInstallationRequest(supporting: [tr]) { try await req.downloadAndInstall() }
  }
  let start = Date()
  let file = try AVAudioFile(forReading: url)
  let analyzer = SpeechAnalyzer(modules: [tr])
  let collected = Task { () -> String in var p: [String] = []; for try await r in tr.results { let s = String(r.text.characters).trimmingCharacters(in: .whitespacesAndNewlines); if !s.isEmpty { p.append(s) } }; return p.joined(separator: " ") }
  if let last = try await analyzer.analyzeSequence(from: file) { try await analyzer.finalizeAndFinish(through: last) } else { await analyzer.cancelAndFinishNow() }
  let text = try await collected.value
  let dur = Double(file.length) / file.processingFormat.sampleRate
  FileHandle.standardError.write("audio=\(String(format: "%.1f", dur))s transcribe=\(String(format: "%.1f", Date().timeIntervalSince(start)))s\n".data(using: .utf8)!)
  print(text)
}}
