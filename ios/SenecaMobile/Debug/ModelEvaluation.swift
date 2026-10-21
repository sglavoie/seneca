#if DEBUG
import CryptoKit
import Foundation
import FoundationModels
import UIKit

// The on-phone half of scripts/apple-models/run.sh: review prompts exported
// on the Mac through the stand-in harness are run here on Apple's models,
// and the responses go back to the Mac, where `run.sh replay` holds them to
// `seneca review`'s own validator. Debug builds only; nothing here opens the
// journal or writes into it (docs/product/apple-models.md).

/// What `run.sh export` writes: one review prompt per window, with the
/// schema the review asked the harness for.
struct EvaluationPrompts: Codable, Equatable, Sendable {
    static let format = "seneca-apple-models-prompts"

    struct Prompt: Codable, Equatable, Sendable {
        var id: String
        var prompt: String
        var schema: String
        var sha256: String
    }

    var format: String
    var formatVersion: Int
    var prompts: [Prompt]
}

/// Where a prompt ran.
enum EvaluationTarget: Equatable, Hashable, Sendable {
    case onDevice
    case privateCloud(Reasoning)

    enum Reasoning: String, CaseIterable, Codable, Sendable {
        case light, moderate, deep
    }

    var name: String {
        switch self {
        case .onDevice: "ondevice"
        case .privateCloud: "pcc"
        }
    }

    var reasoning: Reasoning? {
        if case .privateCloud(let level) = self { return level }
        return nil
    }
}

/// One prompt on one model: the response verbatim, or why there is none.
struct EvaluationResult: Codable, Equatable, Sendable {
    enum Outcome: String, Codable, Sendable {
        case ok
        case refused
        case tooLong = "too-long"
        case unavailable
        case quotaReached = "quota-reached"
        case failed
    }

    var promptID: String
    var promptSha256: String
    var target: String
    var reasoning: String?
    var outcome: Outcome
    var seconds: Double
    var text: String?
    var error: String?
    var promptTokens: Int?
}

/// What the screen writes for `devicectl` to copy back to the Mac.
struct EvaluationRun: Codable, Equatable, Sendable {
    static let format = "seneca-apple-models-responses"

    struct Device: Codable, Equatable, Sendable {
        var model: String
        var system: String
        var simulator: Bool
        var build: String
    }

    struct Conditions: Codable, Equatable, Sendable {
        var at: Date
        /// 0...1, or nil where iOS does not say.
        var battery: Double?
        var thermal: String
        var pccQuota: String?
    }

    var format = EvaluationRun.format
    var formatVersion = 1
    var promptsSha256: String
    var device: Device
    /// "available", or the reason the model gave for not being.
    var onDevice: String
    /// "available", "not-entitled", "requires-ios-27", or the reason.
    var privateCloud: String
    var started: Conditions
    var finished: Conditions?
    var results: [EvaluationResult]
}

/// Runs one prompt with one schema on one model.
protocol EvaluationModel: Sendable {
    func availability(_ target: EvaluationTarget) async -> String
    func promptTokens(_ prompt: String) async -> Int?
    func respond(_ prompt: String, schema: GenerationSchema, on target: EvaluationTarget) async throws -> String
    func quota() async -> String?
}

/// Seneca's review schema, as the stand-in harness gives it to Apple's
/// models: dates limited to those the prompt names and a focus assessment
/// forced to null when the prompt says none applies
/// (scripts/apple-models/review_harness.swift).
@available(iOS 26.4, *)
struct ReviewGenerationSchema {
    private var counter = 0
    private var allowedDates: [String] = []
    private var focusDates: [String] = []
    private var focusMustBeNull = false

    /// The dates and focus rule the prompt sets.
    static func constraints(of prompt: String) -> (dates: [String], focusDates: [String], focusNull: Bool) {
        var dates: [String] = []
        var focus: [String] = []
        for line in prompt.split(separator: "\n") {
            if line.hasPrefix("Date: ") { dates.append(String(line.dropFirst(6))) }
            if line.hasPrefix("End: ") { dates.append(String(line.dropFirst(5))) }
            if line.contains("may appear in focus_assessment.evidence: "), let range = line.range(of: "evidence: ") {
                focus = line[range.upperBound...].trimmingCharacters(in: CharacterSet(charactersIn: ". ")).components(separatedBy: ", ")
            }
        }
        return (dates, focus.isEmpty ? dates : focus, prompt.contains("focus_assessment must be null"))
    }

    static func make(schema: String, prompt: String) throws -> GenerationSchema {
        guard let root = try JSONSerialization.jsonObject(with: Data(schema.utf8)) as? [String: Any] else {
            throw EvaluationError.badSchema
        }
        let found = constraints(of: prompt)
        var builder = ReviewGenerationSchema()
        builder.allowedDates = found.dates
        builder.focusDates = found.focusDates
        builder.focusMustBeNull = found.focusNull
        return try GenerationSchema(root: builder.node(root, "review"), dependencies: [])
    }

    private mutating func node(_ node: [String: Any], _ name: String, _ path: String = "") throws -> DynamicGenerationSchema {
        if name == "focus_assessment" && focusMustBeNull { return .null }
        if name == "date" && !allowedDates.isEmpty {
            let pool = path.hasPrefix("focus_assessment") ? focusDates : allowedDates
            if !pool.isEmpty {
                counter += 1
                return DynamicGenerationSchema(name: "date_\(counter)", anyOf: pool)
            }
        }
        var types: [String] = []
        if let type = node["type"] as? String { types = [type] } else if let list = node["type"] as? [String] { types = list }
        let nullable = types.contains("null")
        let base = types.first { $0 != "null" } ?? "string"
        var schema: DynamicGenerationSchema
        switch base {
        case "object":
            let properties = node["properties"] as? [String: Any] ?? [:]
            let order = (node["required"] as? [String]) ?? properties.keys.sorted()
            counter += 1
            let label = "\(name)_\(counter)"
            var members: [DynamicGenerationSchema.Property] = []
            for key in order {
                guard let child = properties[key] as? [String: Any] else { throw EvaluationError.badSchema }
                members.append(.init(name: key, schema: try self.node(child, key, path.isEmpty ? key : path)))
            }
            schema = DynamicGenerationSchema(name: label, representNilExplicitlyInGeneratedContent: true, properties: members)
        case "array":
            guard let items = node["items"] as? [String: Any] else { throw EvaluationError.badSchema }
            schema = DynamicGenerationSchema(arrayOf: try self.node(items, name + "_item", path))
        default:
            schema = DynamicGenerationSchema(type: String.self)
        }
        if nullable {
            counter += 1
            return DynamicGenerationSchema(name: "\(name)_or_null_\(counter)", anyOf: [schema, .null])
        }
        return schema
    }
}

enum EvaluationError: Error, Equatable, LocalizedError {
    case noPrompts
    case wrongFormat(String)
    case badSchema
    case changed(String)
    case requiresNewerSystem(String)

    var errorDescription: String? {
        switch self {
        case .noPrompts: "No prompts are on this iPhone. Copy them in with `run.sh push`."
        case .wrongFormat(let found): "The prompts file is \(found), not \(EvaluationPrompts.format) v1."
        case .badSchema: "The review schema could not be read."
        case .changed(let id): "Prompt \(id) does not match its digest."
        case .requiresNewerSystem(let version): "This needs iOS \(version) or later."
        }
    }
}

/// The evaluation's files on this iPhone, beside the app's other
/// device-local state and outside the journal.
struct EvaluationFiles: Sendable {
    let directory: URL

    var prompts: URL { directory.appendingPathComponent("prompts.json") }
    var responses: URL { directory.appendingPathComponent("responses.json") }

    static func sha256(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    func load() throws -> (EvaluationPrompts, String) {
        guard let data = try? Data(contentsOf: prompts) else { throw EvaluationError.noPrompts }
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        let loaded = try decoder.decode(EvaluationPrompts.self, from: data)
        guard loaded.format == EvaluationPrompts.format, loaded.formatVersion == 1 else {
            throw EvaluationError.wrongFormat("\(loaded.format) v\(loaded.formatVersion)")
        }
        guard !loaded.prompts.isEmpty else { throw EvaluationError.noPrompts }
        for prompt in loaded.prompts where Self.sha256(Data(prompt.prompt.utf8)) != prompt.sha256 {
            throw EvaluationError.changed(prompt.id)
        }
        return (loaded, Self.sha256(data))
    }

    func write(_ run: EvaluationRun) throws {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        encoder.dateEncodingStrategy = .iso8601
        encoder.keyEncodingStrategy = .convertToSnakeCase
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try encoder.encode(run).write(to: responses, options: .atomic)
    }
}

/// Runs every prompt on every target the iPhone can reach and writes what
/// came back, as it goes, so a run stopped part way keeps what it had.
@MainActor @Observable
final class ModelEvaluation {
    enum Phase: Equatable {
        case idle
        case running(done: Int, of: Int)
        case finished(Int)
        case failed(String)
    }

    let files: EvaluationFiles
    let model: any EvaluationModel
    let entitledToPrivateCloud: Bool
    let device: EvaluationRun.Device
    let conditions: @MainActor () -> EvaluationRun.Conditions

    private(set) var phase: Phase = .idle
    private(set) var promptCount: Int?
    private(set) var loadProblem: String?

    init(
        files: EvaluationFiles, model: any EvaluationModel, entitledToPrivateCloud: Bool,
        device: EvaluationRun.Device, conditions: @escaping @MainActor () -> EvaluationRun.Conditions
    ) {
        self.files = files
        self.model = model
        self.entitledToPrivateCloud = entitledToPrivateCloud
        self.device = device
        self.conditions = conditions
    }

    func refresh() {
        do {
            promptCount = try files.load().0.prompts.count
            loadProblem = nil
        } catch {
            promptCount = nil
            loadProblem = error.localizedDescription
        }
    }

    /// The targets to run: the on-device model, then Private Cloud Compute
    /// at each reasoning level when this build may reach it.
    func targets(onDevice: String, privateCloud: String) -> [EvaluationTarget] {
        var targets: [EvaluationTarget] = []
        if onDevice == "available" { targets.append(.onDevice) }
        if privateCloud == "available" {
            targets += EvaluationTarget.Reasoning.allCases.map { .privateCloud($0) }
        }
        return targets
    }

    func run() async {
        let loaded: EvaluationPrompts
        let digest: String
        do {
            (loaded, digest) = try files.load()
        } catch {
            phase = .failed(error.localizedDescription)
            return
        }
        let onDevice = await model.availability(.onDevice)
        let privateCloud = entitledToPrivateCloud ? await model.availability(.privateCloud(.moderate)) : "not-entitled"
        var record = EvaluationRun(
            promptsSha256: digest, device: device, onDevice: onDevice, privateCloud: privateCloud,
            started: await conditions(asking: privateCloud == "available"), finished: nil, results: []
        )
        let targets = targets(onDevice: onDevice, privateCloud: privateCloud)
        let total = targets.count * loaded.prompts.count
        phase = .running(done: 0, of: total)
        do {
            try files.write(record)
            for target in targets {
                for prompt in loaded.prompts {
                    record.results.append(await evaluate(prompt, on: target))
                    try files.write(record)
                    phase = .running(done: record.results.count, of: total)
                }
            }
            record.finished = await conditions(asking: privateCloud == "available")
            try files.write(record)
        } catch {
            phase = .failed("The responses could not be written: \(error.localizedDescription)")
            return
        }
        phase = .finished(record.results.count)
    }

    /// The battery, the heat, and the Private Cloud Compute quota now.
    private func conditions(asking quota: Bool) async -> EvaluationRun.Conditions {
        var now = conditions()
        if quota { now.pccQuota = await model.quota() }
        return now
    }

    private func evaluate(_ prompt: EvaluationPrompts.Prompt, on target: EvaluationTarget) async -> EvaluationResult {
        var result = EvaluationResult(
            promptID: prompt.id, promptSha256: prompt.sha256, target: target.name,
            reasoning: target.reasoning?.rawValue, outcome: .failed, seconds: 0
        )
        if target == .onDevice { result.promptTokens = await model.promptTokens(prompt.prompt) }
        let started = ContinuousClock.now
        do {
            guard #available(iOS 26.4, *) else { throw EvaluationError.requiresNewerSystem("26.4") }
            let schema = try ReviewGenerationSchema.make(schema: prompt.schema, prompt: prompt.prompt)
            result.text = try await model.respond(prompt.prompt, schema: schema, on: target)
            result.outcome = .ok
        } catch {
            (result.outcome, result.error) = Self.classify(error)
        }
        let elapsed = ContinuousClock.now - started
        result.seconds = ((Double(elapsed.components.seconds) + Double(elapsed.components.attoseconds) / 1e18) * 10).rounded() / 10
        return result
    }

    /// The outcome an error stands for, and what it said.
    nonisolated static func classify(_ error: any Error) -> (EvaluationResult.Outcome, String) {
        let said = String(describing: error)
        if #available(iOS 27, *), let cloud = error as? PrivateCloudComputeLanguageModel.Error {
            switch cloud {
            case .quotaLimitReached: return (.quotaReached, said)
            default: return (.unavailable, said)
            }
        }
        switch FoundationModelsSplitter.classify(error) {
        case .refused: return (.refused, said)
        case .tooLong: return (.tooLong, said)
        case .unavailable, .notOffered: return (.unavailable, said)
        case .failed: return (.failed, said)
        }
    }
}

/// Apple's models, as the stand-in harness calls them.
struct SystemEvaluationModel: EvaluationModel {
    static let instructions = "Follow the user's instructions exactly. Output JSON only."

    func availability(_ target: EvaluationTarget) async -> String {
        switch target {
        case .onDevice:
            switch SystemLanguageModel.default.availability {
            case .available: return "available"
            case .unavailable(let reason): return String(describing: reason)
            }
        case .privateCloud:
            guard #available(iOS 27, *) else { return "requires-ios-27" }
            switch PrivateCloudComputeLanguageModel().availability {
            case .available: return "available"
            case .unavailable(let reason): return String(describing: reason)
            }
        }
    }

    func promptTokens(_ prompt: String) async -> Int? {
        guard #available(iOS 26.4, *) else { return nil }
        return try? await SystemLanguageModel.default.tokenCount(for: Prompt(prompt))
    }

    func respond(_ prompt: String, schema: GenerationSchema, on target: EvaluationTarget) async throws -> String {
        let onDevice = SystemLanguageModel(useCase: .general, guardrails: .permissiveContentTransformations)
        let options = GenerationOptions(temperature: 0.3)
        guard #available(iOS 27, *) else {
            guard target == .onDevice else { throw EvaluationError.requiresNewerSystem("27") }
            let session = LanguageModelSession(model: onDevice, instructions: Self.instructions)
            return try await session.respond(to: prompt, schema: schema, includeSchemaInPrompt: false, options: options).content.jsonString
        }
        let session: LanguageModelSession
        var reasoning: ContextOptions.ReasoningLevel?
        switch target {
        case .onDevice:
            session = LanguageModelSession(model: onDevice, instructions: Self.instructions)
        case .privateCloud(let level):
            session = LanguageModelSession(model: PrivateCloudComputeLanguageModel(), instructions: Self.instructions)
            switch level {
            case .light: reasoning = .light
            case .moderate: reasoning = .moderate
            case .deep: reasoning = .deep
            }
        }
        let response = try await session.respond(
            to: prompt, schema: schema, options: options,
            contextOptions: ContextOptions(includeSchemaInPrompt: false, reasoningLevel: reasoning)
        )
        return response.content.jsonString
    }

    func quota() async -> String? {
        guard #available(iOS 27, *) else { return nil }
        let usage = PrivateCloudComputeLanguageModel().quotaUsage
        var said: String
        switch usage.status {
        case .belowLimit(let below): said = below.isApproachingLimit ? "approaching-limit" : "below-limit"
        case .limitReached: said = "limit-reached"
        @unknown default: said = "unknown"
        }
        if let reset = usage.resetDate { said += " resets \(reset.ISO8601Format())" }
        return said
    }
}

extension ModelEvaluation {
    /// On an iPhone that cannot run Apple's model the screen is not offered,
    /// so the app records the device's answer itself: when prompts are on
    /// the iPhone and no responses were written for them, it runs the
    /// evaluation, which calls no model and writes the reason. Nothing
    /// happens on an eligible iPhone, where the author runs the screen.
    /// Whether it ran.
    @discardableResult
    static func recordIneligible(
        directory: URL, eligible: Bool = LanguageModelSupport.onThisDevice,
        build: String? = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String,
        make: (URL) -> ModelEvaluation = { system(directory: $0) }
    ) async -> Bool {
        guard !eligible else { return false }
        let files = EvaluationFiles(directory: directory)
        guard let (_, digest) = try? files.load() else { return false }
        if let data = try? Data(contentsOf: files.responses),
           let written = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           written["prompts_sha256"] as? String == digest,
           (written["device"] as? [String: Any])?["build"] as? String == build {
            return false
        }
        await make(directory).run()
        return true
    }

    /// The screen's evaluation on this iPhone.
    static func system(directory: URL) -> ModelEvaluation {
        let model = SystemEvaluationModel()
        let entitled = ProvisioningProfile.grants("com.apple.developer.private-cloud-compute")
        UIDevice.current.isBatteryMonitoringEnabled = true
        let device = EvaluationRun.Device(
            model: Self.hardwareModel(), system: "\(UIDevice.current.systemName) \(UIDevice.current.systemVersion)",
            simulator: Self.simulator,
            build: Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? ""
        )
        return ModelEvaluation(
            files: EvaluationFiles(directory: directory), model: model, entitledToPrivateCloud: entitled, device: device,
            conditions: {
                let level = UIDevice.current.batteryLevel
                return EvaluationRun.Conditions(
                    at: Date(), battery: level < 0 ? nil : Double(level),
                    thermal: Self.thermal(ProcessInfo.processInfo.thermalState), pccQuota: nil
                )
            }
        )
    }

    nonisolated static var simulator: Bool {
        #if targetEnvironment(simulator)
        true
        #else
        false
        #endif
    }

    nonisolated static func hardwareModel() -> String {
        var info = utsname()
        uname(&info)
        return withUnsafeBytes(of: &info.machine) { raw in
            String(decoding: raw.prefix { $0 != 0 }, as: UTF8.self)
        }
    }

    nonisolated static func thermal(_ state: ProcessInfo.ThermalState) -> String {
        switch state {
        case .nominal: "nominal"
        case .fair: "fair"
        case .serious: "serious"
        case .critical: "critical"
        @unknown default: "unknown"
        }
    }
}

/// What the build's provisioning profile grants. A managed entitlement Apple
/// has not granted is absent, and the screen then never calls the model it
/// guards.
enum ProvisioningProfile {
    static func grants(_ entitlement: String, bundle: Bundle = .main) -> Bool {
        guard let url = bundle.url(forResource: "embedded", withExtension: "mobileprovision"),
              let data = try? Data(contentsOf: url) else { return false }
        return grants(entitlement, profile: data)
    }

    /// The profile is a signed message around a property list; the list is
    /// read where it starts and ends.
    static func grants(_ entitlement: String, profile data: Data) -> Bool {
        guard let start = data.range(of: Data("<?xml".utf8)),
              let end = data.range(of: Data("</plist>".utf8), in: start.lowerBound..<data.endIndex),
              let plist = try? PropertyListSerialization.propertyList(from: data[start.lowerBound..<end.upperBound], format: nil) as? [String: Any],
              let entitlements = plist["Entitlements"] as? [String: Any],
              let value = entitlements[entitlement] else { return false }
        if let flag = value as? Bool { return flag }
        if let list = value as? [Any] { return !list.isEmpty }
        return true
    }
}
#endif
