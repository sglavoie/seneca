// Stand-in for the `claude` CLI: routes Seneca's review prompt to Apple's models.
// --model ondevice|pcc[,unguided][,light|moderate|deep]
import FoundationModels
import Foundation

func log(_ s: String) {
  guard let p = ProcessInfo.processInfo.environment["FM_LOG"] else { return }
  let line = s + "\n"
  if let h = FileHandle(forWritingAtPath: p) { h.seekToEndOfFile(); h.write(line.data(using: .utf8)!); h.closeFile() }
  else { FileManager.default.createFile(atPath: p, contents: line.data(using: .utf8)) }
}


var schemaCounter = 0
var allowedDates: [String] = []
var focusDates: [String] = []
var focusMustBeNull = false
func dyn(_ node: [String: Any], _ name: String, _ path: String = "") -> DynamicGenerationSchema {
  if name == "focus_assessment" && focusMustBeNull { return .null }
  if name == "date" && !allowedDates.isEmpty {
    let pool = path.hasPrefix("focus_assessment") ? focusDates : allowedDates
    if !pool.isEmpty { schemaCounter += 1; return DynamicGenerationSchema(name: "date_\(schemaCounter)", anyOf: pool) }
  }
  var types: [String] = []
  if let t = node["type"] as? String { types = [t] } else if let t = node["type"] as? [String] { types = t }
  let nullable = types.contains("null")
  let base = types.first { $0 != "null" } ?? "string"
  var schema: DynamicGenerationSchema
  switch base {
  case "object":
    let props = node["properties"] as? [String: Any] ?? [:]
    let order = (node["required"] as? [String]) ?? Array(props.keys)
    schemaCounter += 1
    schema = DynamicGenerationSchema(name: "\(name)_\(schemaCounter)", representNilExplicitlyInGeneratedContent: true,
      properties: order.map { k in .init(name: k, schema: dyn(props[k] as! [String: Any], k, path.isEmpty ? k : path)) })
  case "array":
    schema = DynamicGenerationSchema(arrayOf: dyn(node["items"] as! [String: Any], name + "_item", path))
  default:
    schema = DynamicGenerationSchema(type: String.self)
  }
  if nullable { schemaCounter += 1; return DynamicGenerationSchema(name: "\(name)_or_null_\(schemaCounter)", anyOf: [schema, .null]) }
  return schema
}

@main struct FakeClaude { static func main() async {
  var args = Array(CommandLine.arguments.dropFirst())
  let prompt = args.removeLast()
  var schemaText = "", modelSpec = "ondevice"
  var i = 0
  while i < args.count {
    if args[i] == "--json-schema" { schemaText = args[i+1]; i += 2; continue }
    if args[i] == "--model" { modelSpec = args[i+1]; i += 2; continue }
    i += 1
  }
  let opts = modelSpec.split(separator: ",").map(String.init)
  let guided = !opts.contains("unguided")
  var reasoning: ContextOptions.ReasoningLevel? = nil
  if opts.contains("light") { reasoning = .light }
  if opts.contains("moderate") { reasoning = .moderate }
  if opts.contains("deep") { reasoning = .deep }
  let constrained = !opts.contains("loose")
  if constrained {
    for line in prompt.split(separator: "\n") {
      if line.hasPrefix("Date: ") { allowedDates.append(String(line.dropFirst(6))) }
      if line.hasPrefix("End: ") { allowedDates.append(String(line.dropFirst(5))) }
      if line.contains("may appear in focus_assessment.evidence: "), let r = line.range(of: "evidence: ") {
        focusDates = line[r.upperBound...].trimmingCharacters(in: CharacterSet(charactersIn: ". ")).components(separatedBy: ", ") }
    }
    focusMustBeNull = prompt.contains("focus_assessment must be null")
    if focusDates.isEmpty { focusDates = allowedDates }
  }
  let start = Date()
  do {
    let session: LanguageModelSession
    if opts.first == "pcc" {
      session = LanguageModelSession(model: PrivateCloudComputeLanguageModel(), instructions: "Follow the user's instructions exactly. Output JSON only.")
    } else {
      let m = SystemLanguageModel(useCase: .general, guardrails: .permissiveContentTransformations)
      session = LanguageModelSession(model: m, instructions: "Follow the user's instructions exactly. Output JSON only.")
      if let n = try? await SystemLanguageModel.default.tokenCount(for: Prompt(prompt)) { log("prompt_tokens_ondevice=\(n)") }
    }
    var text: String
    if guided {
      let root = try JSONSerialization.jsonObject(with: schemaText.data(using: .utf8)!) as! [String: Any]
      let gs = try GenerationSchema(root: dyn(root, "review"), dependencies: [])
      let r = try await session.respond(to: prompt, schema: gs, options: GenerationOptions(temperature: 0.3),
                                        contextOptions: ContextOptions(includeSchemaInPrompt: false, reasoningLevel: reasoning))
      text = r.content.jsonString
    } else {
      let r = try await session.respond(to: prompt, options: GenerationOptions(temperature: 0.3),
                                        contextOptions: ContextOptions(includeSchemaInPrompt: nil, reasoningLevel: reasoning))
      text = r.content
    }
    log("model=\(modelSpec) seconds=\(String(format: "%.1f", Date().timeIntervalSince(start))) ok out_chars=\(text.count)")
    print(text)
  } catch {
    log("model=\(modelSpec) seconds=\(String(format: "%.1f", Date().timeIntervalSince(start))) ERROR \(error)")
    FileHandle.standardError.write("fm error: \(error)\n".data(using: .utf8)!)
    exit(1)
  }
}}
