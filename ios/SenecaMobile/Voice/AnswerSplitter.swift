import Foundation
import FoundationModels

/// Answers proposed from a transcript: one per configured question, in the
/// order the journal asks them, and what was said that answers none of
/// them. A proposal is shown for confirmation; it is never saved unseen.
struct ProposedAnswers: Equatable, Sendable {
    var answers: [String]
    var unassigned: String
}

/// Why no answers were proposed. The transcript is shown instead, with a
/// way to copy it into each question.
enum SplitFailure: Error, Equatable, Sendable {
    /// The on-device model cannot run here now: Apple Intelligence is off,
    /// the iPhone is not eligible, the model is not ready, or it does not
    /// speak the take's language.
    case unavailable(String)
    /// The model declined to sort the transcript.
    case refused
    /// The take is longer than the model can read at once.
    case tooLong
    case failed(String)
    /// This iPhone's hardware cannot run the model, so sorting is not
    /// offered at all: the transcript is copied into the questions instead.
    case notOffered

    var message: String {
        switch self {
        case .unavailable(let reason):
            return "Answers cannot be proposed on this iPhone: \(reason) Copy the transcript into your questions instead."
        case .refused:
            return "The on-device model declined to sort this transcript. Copy it into your questions instead."
        case .tooLong:
            return "This recording is too long for the on-device model to sort. Copy the transcript into your questions instead."
        case .failed(let detail):
            return "Answers could not be proposed: \(detail) Copy the transcript into your questions instead."
        case .notOffered:
            return "Copy the transcript into each question it answers."
        }
    }
}

/// Files a transcript under the journal's questions.
protocol AnswerSplitting: Sendable {
    /// False when this iPhone's hardware can never run the splitter, so the
    /// app does not offer sorting at all. A model that is only off or not
    /// ready yet is still offered, and says why when asked.
    var offered: Bool { get }
    /// Throws a `SplitFailure` when nothing can be proposed.
    func split(_ transcript: String, questions: [String], locale: String) async throws -> ProposedAnswers
}

extension AnswerSplitting {
    var offered: Bool { true }
}

/// Whether this iPhone's hardware runs Apple's on-device language model.
/// Apple Intelligence needs an A17 Pro or later with 8 GB of memory; on
/// anything older the model reports `deviceNotEligible` for good, and the
/// features built on it are left out rather than shown and refused.
enum LanguageModelSupport {
    /// False only for hardware that can never run the model.
    static func eligible(_ availability: SystemLanguageModel.Availability) -> Bool {
        if case .unavailable(.deviceNotEligible) = availability { return false }
        return true
    }

    /// This iPhone. A simulator stands in for an eligible one unless
    /// SENECA_UITEST_MODEL=ineligible says otherwise.
    static var onThisDevice: Bool {
        #if targetEnvironment(simulator)
        ProcessInfo.processInfo.environment["SENECA_UITEST_MODEL"] != "ineligible"
        #else
        eligible(SystemLanguageModel.default.availability)
        #endif
    }
}

/// Apple's on-device model, with a schema built from the questions the
/// journal asks now: one answer per question, plus "unassigned". It keeps
/// the speaker's words and language and removes fillers only
/// (scripts/apple-models/split.swift, docs/product/apple-models.md).
struct FoundationModelsSplitter: AnswerSplitting {
    static let instructions = """
        You file a spoken evening reflection under the journal's questions. \
        Keep the speaker's own words and language; only remove filler words, false starts and repetitions. \
        Never invent, add, summarize, rephrase, translate, or give advice. \
        Put each part of the transcript under the question it answers, and leave an answer empty when the question was not addressed. \
        Put anything said that answers none of the questions under unassigned.
        """

    static let unassigned = "unassigned"

    var offered: Bool { LanguageModelSupport.onThisDevice }

    static func property(_ index: Int) -> String { "answer_\(index + 1)" }

    /// The shape of a proposal for these questions, in their order.
    static func schema(for questions: [String]) throws -> GenerationSchema {
        let answers = questions.enumerated().map { index, question in
            DynamicGenerationSchema.Property(
                name: property(index),
                description: "The speaker's answer to the question \"\(question)\", in their own words with fillers removed. Empty if they did not address it.",
                schema: DynamicGenerationSchema(type: String.self)
            )
        }
        let rest = DynamicGenerationSchema.Property(
            name: unassigned,
            description: "Anything said that answers none of the questions, in the speaker's own words. Empty if nothing.",
            schema: DynamicGenerationSchema(type: String.self)
        )
        return try GenerationSchema(
            root: DynamicGenerationSchema(name: "reflection_answers", properties: answers + [rest]),
            dependencies: []
        )
    }

    static func prompt(_ transcript: String, questions: [String]) -> String {
        let numbered = questions.enumerated().map { "\($0.offset + 1). \($0.element)" }.joined(separator: "\n")
        return "Questions:\n\(numbered)\n\nTranscript:\n\(transcript)"
    }

    /// Reads a generated proposal back, one answer per question.
    static func answers(from content: GeneratedContent, questions: [String]) throws -> ProposedAnswers {
        ProposedAnswers(
            answers: try questions.indices.map {
                try content.value(String.self, forProperty: property($0)).trimmingCharacters(in: .whitespacesAndNewlines)
            },
            unassigned: try content.value(String.self, forProperty: unassigned).trimmingCharacters(in: .whitespacesAndNewlines)
        )
    }

    /// Nil when the model can run.
    static func failure(for availability: SystemLanguageModel.Availability) -> SplitFailure? {
        switch availability {
        case .available:
            return nil
        case .unavailable(.appleIntelligenceNotEnabled):
            return .unavailable("Apple Intelligence is turned off.")
        case .unavailable(.deviceNotEligible):
            return .unavailable("this iPhone does not run Apple Intelligence.")
        case .unavailable(.modelNotReady):
            return .unavailable("the on-device model is not ready yet.")
        case .unavailable:
            return .unavailable("the on-device model is not available.")
        }
    }

    /// What a generation error means for the author.
    static func classify(_ error: Error) -> SplitFailure {
        if let failure = error as? SplitFailure { return failure }
        if #available(iOS 27.0, *) {
            if let error = error as? LanguageModelError {
                switch error {
                case .contextSizeExceeded: return .tooLong
                case .refusal, .guardrailViolation: return .refused
                case .unsupportedLanguageOrLocale: return .unavailable("the on-device model does not speak this language.")
                default: return .failed(error.localizedDescription)
                }
            }
            if let error = error as? SystemLanguageModel.Error {
                switch error {
                case .assetsUnavailable: return .unavailable("the on-device model is not ready yet.")
                default: return .failed(error.localizedDescription)
                }
            }
        }
        if let error = error as? LanguageModelSession.GenerationError {
            switch error {
            case .exceededContextWindowSize: return .tooLong
            case .refusal, .guardrailViolation: return .refused
            case .unsupportedLanguageOrLocale: return .unavailable("the on-device model does not speak this language.")
            case .assetsUnavailable: return .unavailable("the on-device model is not ready yet.")
            default: return .failed(error.localizedDescription)
            }
        }
        return .failed(error.localizedDescription)
    }

    func split(_ transcript: String, questions: [String], locale: String) async throws -> ProposedAnswers {
        guard !questions.isEmpty else { throw SplitFailure.failed("the journal asks no questions.") }
        let model = SystemLanguageModel(useCase: .general, guardrails: .permissiveContentTransformations)
        if let failure = Self.failure(for: model.availability) { throw failure }
        guard model.supportsLocale(Locale(identifier: locale)) else {
            throw SplitFailure.unavailable("the on-device model does not speak this language.")
        }
        let session = LanguageModelSession(model: model, instructions: Self.instructions)
        do {
            let response = try await session.respond(
                to: Self.prompt(transcript, questions: questions),
                schema: try Self.schema(for: questions),
                options: GenerationOptions(temperature: 0.2)
            )
            return try Self.answers(from: response.content, questions: questions)
        } catch {
            throw Self.classify(error)
        }
    }
}
