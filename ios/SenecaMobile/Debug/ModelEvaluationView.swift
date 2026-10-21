#if DEBUG
import SwiftUI

/// Debug builds only: runs the review prompts `run.sh export` made on the
/// Mac on Apple's models here, and keeps the responses for `run.sh pull`.
/// It never opens the journal.
struct ModelEvaluationView: View {
    @State private var evaluation: ModelEvaluation?
    @State private var problem: String?

    var body: some View {
        List {
            if let evaluation {
                Section {
                    if let count = evaluation.promptCount {
                        LabeledContent("Prompts", value: "\(count)")
                            .accessibilityIdentifier("evaluation-prompts")
                    } else {
                        Text(evaluation.loadProblem ?? "No prompts.")
                            .accessibilityIdentifier("evaluation-no-prompts")
                    }
                    LabeledContent("Private Cloud Compute",
                                   value: evaluation.entitledToPrivateCloud ? "Entitled" : "Not entitled")
                        .accessibilityIdentifier("evaluation-pcc")
                } footer: {
                    Text("Runs every prompt on the on-device model, then on Private Cloud Compute at each reasoning level when this build is entitled. Responses stay on this iPhone until copied off; nothing is written to the journal.")
                }
                Section {
                    Button("Run Evaluation") {
                        Task { await evaluation.run() }
                    }
                    .disabled(evaluation.promptCount == nil || isRunning(evaluation.phase))
                    .accessibilityIdentifier("evaluation-run")
                    switch evaluation.phase {
                    case .idle:
                        EmptyView()
                    case .running(let done, let total):
                        ProgressView(value: Double(done), total: Double(max(total, 1))) {
                            Text("\(done) of \(total)")
                        }
                        .accessibilityIdentifier("evaluation-progress")
                    case .finished(let count):
                        Text("\(count) responses written.")
                            .accessibilityIdentifier("evaluation-written")
                    case .failed(let reason):
                        Label(reason, systemImage: "exclamationmark.triangle")
                            .accessibilityIdentifier("evaluation-failed")
                    }
                }
            } else if let problem {
                Text(problem)
            }
        }
        .navigationTitle("Model Evaluation")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            guard evaluation == nil else { return }
            do {
                let made = ModelEvaluation.system(directory: try AppPaths.evaluationDirectory())
                made.refresh()
                evaluation = made
            } catch {
                problem = error.localizedDescription
            }
        }
    }

    private func isRunning(_ phase: ModelEvaluation.Phase) -> Bool {
        if case .running = phase { return true }
        return false
    }
}
#endif
