import XCTest

/// Speaking a reflection, driven from outside: a refused microphone said so
/// with the way to Settings, a take recorded from the form and from Today,
/// transcribed by the staged transcriber, and kept beside the drafts,
/// outside the journal; its proposed answers confirmed before the form
/// saves them, and the transcript to copy from when the on-device model is
/// unavailable.
final class VoiceUITests: StagedJournalTestCase {
    var audio: URL { root.appendingPathComponent("audio", isDirectory: true) }

    private func takeFiles() -> [String] {
        let days = (try? FileManager.default.contentsOfDirectory(atPath: audio.path)) ?? []
        return days.flatMap { day in
            ((try? FileManager.default.contentsOfDirectory(atPath: audio.appendingPathComponent(day).path)) ?? [])
                .map { "\(day)/\($0)" }
        }
        .sorted()
    }

    func testARefusedMicrophoneIsExplainedWithTheWayToSettings() throws {
        try launch(environment: ["SENECA_UITEST_MICROPHONE": "denied"])
        let journal = try journalText()
        tap("speak-today")
        tap("record-start")
        assertExists("record-denied")
        let explanation = element("record-denied").label
        XCTAssertTrue(explanation.contains("microphone"), explanation)
        assertExists("open-settings")
        XCTAssertTrue(element("open-settings").isHittable)
        try audit([.contrast, .elementDetection, .hitRegion, .sufficientElementDescription, .textClipped, .trait])
        tap("record-done")

        // Writing is still there, and nothing was recorded.
        assertExists("write-today")
        XCTAssertEqual(takeFiles(), [])
        XCTAssertEqual(try journalText(), journal)
    }

    func testASpokenTakeIsTranscribedAndKeptOutsideTheJournal() throws {
        let spoken = "I kept my word and walked after dinner."
        try launch(environment: ["SENECA_UITEST_TRANSCRIPT": spoken])
        let journal = try journalText()

        // From the form's toolbar.
        tap("write-today")
        tap("speak-instead")
        tap("record-start")
        assertExists("record-stop")
        XCTAssertEqual(element("record-elapsed").label, "Elapsed time")
        XCTAssertEqual(element("record-level").label, "Input level")
        tap("record-stop")
        assertExists("record-transcript")
        XCTAssertEqual(element("record-transcript").label, spoken)

        let files = takeFiles()
        XCTAssertEqual(files.count, 2, "\(files)")
        XCTAssertTrue(files.allSatisfy { $0.hasPrefix(scenario.date + "/") }, "\(files)")
        XCTAssertEqual(files.filter { $0.hasSuffix(".m4a") }.count, 1)
        XCTAssertFalse(files.contains { $0.contains(".partial.") }, "\(files)")
        let sidecar = try XCTUnwrap(files.first { $0.hasSuffix(".json") })
        let json = try XCTUnwrap(
            JSONSerialization.jsonObject(with: Data(contentsOf: audio.appendingPathComponent(sidecar))) as? [String: Any]
        )
        XCTAssertEqual(json["transcript"] as? String, spoken)
        XCTAssertEqual(json["transcriber"] as? String, "speech-transcriber")
        XCTAssertEqual(json["answers_saved"] as? Bool, false)

        tap("record-done")
        // The form is as it was: speaking filled nothing in yet.
        assertExists("answer-0")
        XCTAssertEqual(try journalText(), journal, "speaking wrote inside the journal")
    }

    private func sidecar() throws -> [String: Any] {
        let name = try XCTUnwrap(takeFiles().first { $0.hasSuffix(".json") })
        return try XCTUnwrap(
            JSONSerialization.jsonObject(with: Data(contentsOf: audio.appendingPathComponent(name))) as? [String: Any]
        )
    }

    private func speak() {
        tap("record-start")
        assertExists("record-stop")
        tap("record-stop")
        assertExists("record-transcript")
        tap("record-propose")
    }

    func testProposedAnswersAreConfirmedBeforeTheFormSavesThem() throws {
        let spoken = "Um, the afternoon went quiet. I said no to the meeting. Also the cat was sick."
        let split = #"{"answers": ["Synthetic: the afternoon went quiet.", "Synthetic: said no to the meeting.", ""], "unassigned": "The cat was sick."}"#
        try launch(environment: ["SENECA_UITEST_TRANSCRIPT": spoken, "SENECA_UITEST_SPLIT": split])
        let journal = try journalText()

        tap("write-today")
        type("Synthetic: typed first.", into: "answer-0")
        tap("keyboard-done")
        tap("speak-instead")
        speak()

        // Every proposal is shown and editable, typed text beside it, what
        // was set aside visible, and the transcript one tap away.
        assertExists("proposal-notice")
        XCTAssertEqual(text(of: "proposal-answer-0"), "Synthetic: the afternoon went quiet.")
        XCTAssertEqual(text(of: "proposal-answer-1"), "Synthetic: said no to the meeting.")
        assertExists("proposal-typed-0")
        assertExists("proposal-merge-0")
        XCTAssertFalse(element("proposal-merge-1").exists)
        type("Synthetic: added by hand.", into: "proposal-answer-2")
        XCTAssertEqual(reach("proposal-unassigned").label, "The cat was sick.")
        tap("proposal-show-transcript")
        XCTAssertEqual(reach("proposal-transcript").label, spoken)
        try audit([.contrast, .elementDetection, .hitRegion, .sufficientElementDescription, .trait])
        XCTAssertEqual(try journalText(), journal, "a proposal wrote to the journal")

        tap("proposal-accept")
        // Back in the form: typed text kept, the proposals filled in as edited.
        assertExists("answer-1")
        XCTAssertEqual(text(of: "answer-0"), "Synthetic: typed first.")
        XCTAssertEqual(text(of: "answer-1"), "Synthetic: said no to the meeting.")
        XCTAssertEqual(text(of: "answer-2"), "Synthetic: added by hand.")
        XCTAssertEqual(try journalText(), journal, "accepting saved without the form")
        XCTAssertEqual(try sidecar()["answers_saved"] as? Bool, false)

        tap("save")
        assertExists("edit-today")
        let after = try journalText()
        XCTAssertTrue(after.contains("Synthetic: typed first."))
        XCTAssertTrue(after.contains("Synthetic: said no to the meeting."))
        XCTAssertTrue(after.contains("Synthetic: added by hand."))
        XCTAssertFalse(after.contains("Synthetic: the afternoon went quiet."), "a proposal over typed text is dropped unless chosen")
        XCTAssertFalse(after.contains("The cat was sick."), "what was set aside is not saved unless moved")
        XCTAssertFalse(after.contains("Um,"), "the raw transcript is not the entry")
        XCTAssertEqual(try sidecar()["answers_saved"] as? Bool, true)
    }

    func testAnUnavailableModelOffersTheTranscriptToCopyIntoEachQuestion() throws {
        let spoken = "Synthetic: I walked at noon and kept my word."
        try launch(environment: ["SENECA_UITEST_TRANSCRIPT": spoken])
        let journal = try journalText()

        // From Today: no form is open yet.
        tap("speak-today")
        speak()
        assertExists("proposal-fallback")
        XCTAssertTrue(element("proposal-fallback").label.contains("Copy the transcript"), element("proposal-fallback").label)
        // An empty field reads as its placeholder.
        XCTAssertTrue(["", "Spoken answer"].contains(text(of: "proposal-answer-0")), "no model text when the model did not run")
        XCTAssertFalse(element("proposal-accept").isEnabled, "nothing to fill yet")
        try audit([.contrast, .elementDetection, .hitRegion, .sufficientElementDescription, .trait])

        tap("copy-transcript-1")
        XCTAssertEqual(text(of: "proposal-answer-1"), spoken)
        XCTAssertEqual(reach("proposal-transcript").label, spoken)
        tap("proposal-accept")

        // The form opens on the draft the answers went into.
        assertExists("notice-restored")
        XCTAssertEqual(text(of: "answer-1"), spoken)
        XCTAssertTrue(["", "Write here"].contains(text(of: "answer-0")), text(of: "answer-0"))
        XCTAssertEqual(try journalText(), journal, "nothing is saved unseen")
        XCTAssertEqual(draftFiles().count, 1)
    }

    func testAnIPhoneThatCannotRunTheModelOffersCopyingAndNoModelEvaluation() throws {
        let spoken = "Synthetic: I kept my word at noon."
        // A model that would sort it, on an iPhone too old to run one.
        let split = #"{"answers": ["sorted anyway", "", ""], "unassigned": ""}"#
        try launch(environment: ["SENECA_UITEST_TRANSCRIPT": spoken, "SENECA_UITEST_SPLIT": split,
                                 "SENECA_UITEST_MODEL": "ineligible"])

        tap("settings")
        assertExists("recovery-link")
        XCTAssertFalse(element("evaluation-link").exists, "Model Evaluation is offered on an iPhone that cannot run it")
        back()

        tap("speak-today")
        tap("record-start")
        assertExists("record-stop")
        tap("record-stop")
        assertExists("record-transcript")
        XCTAssertEqual(element("record-propose").label, "Copy Into Your Questions")
        tap("record-propose")
        assertExists("proposal-fallback")
        XCTAssertFalse(element("proposal-fallback").label.contains("Apple Intelligence"), element("proposal-fallback").label)
        XCTAssertTrue(["", "Spoken answer"].contains(text(of: "proposal-answer-0")), "the model was asked anyway")
        tap("copy-transcript-0")
        XCTAssertEqual(text(of: "proposal-answer-0"), spoken)
    }

    func testFillingAnswersFromARecordingLeavesNoStaleProposalBehind() throws {
        let spoken = "Synthetic: spoken on the way home."
        try launch(environment: ["SENECA_UITEST_TRANSCRIPT": spoken])
        recordFromToday()
        waitForTakeFiles(2)
        let id = try XCTUnwrap(takeFiles().first { $0.hasSuffix(".m4a") }).dropFirst(scenario.date.count + 1).dropLast(4)
        let journal = try journalText()

        app.tabBars.buttons["History"].tap()
        tap("history-recordings")
        tap("take-\(id)")
        tap("take-propose")
        assertExists("proposal-fallback")
        tap("copy-transcript-0")
        tap("proposal-accept")
        assertExists("answer-0")
        XCTAssertEqual(text(of: "answer-0"), spoken)

        // Discarding that draft brings back the recording, not a proposal
        // that still believes its answers are in the form.
        type(" More.", into: "answer-1")
        tap("close")
        confirm("Discard Draft")
        assertExists("take-propose")
        XCTAssertFalse(element("proposal-accept").exists, "the proposal stayed open under the form")
        XCTAssertNil(try sidecar()["accepted_at"], "a discarded draft still counts the take as accepted")
        XCTAssertEqual(try journalText(), journal)
    }

    // MARK: Keeping and deleting

    /// Records a take from Today and comes back to it.
    private func recordFromToday() {
        tap("speak-today")
        tap("record-start")
        assertExists("record-stop")
        tap("record-stop")
        assertExists("record-transcript")
        tap("record-done")
        assertExists("speak-today")
    }

    private func waitForTakeFiles(_ count: Int, file: StaticString = #filePath, line: UInt = #line) {
        let deadline = Date().addingTimeInterval(30)
        while takeFiles().count != count, Date() < deadline {
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        }
        XCTAssertEqual(takeFiles().count, count, "\(takeFiles())", file: file, line: line)
    }

    func testRecordingsAreDeletedFromHistoryOneAtATimeOrAllWithNothingElse() throws {
        try launch(environment: ["SENECA_UITEST_TRANSCRIPT": "Synthetic: spoken twice."])
        recordFromToday()
        recordFromToday()
        waitForTakeFiles(4)
        let journal = try journalText()
        let drafts = draftFiles()
        let ids = takeFiles().filter { $0.hasSuffix(".m4a") }.map { String($0.dropFirst(scenario.date.count + 1).dropLast(4)) }

        app.tabBars.buttons["History"].tap()
        tap("history-recordings")
        assertExists("take-\(ids[0])")
        try audit([.contrast, .elementDetection, .hitRegion, .sufficientElementDescription, .trait])

        // One: its audio and its sidecar, nothing else.
        tap("take-delete-\(ids[0])")
        confirm("Delete This Recording")
        waitForTakeFiles(2)
        XCTAssertEqual(takeFiles(), ["\(scenario.date)/\(ids[1]).json", "\(scenario.date)/\(ids[1]).m4a"])

        // The rest, in bulk.
        tap("recordings-delete-all")
        confirm("Delete 1 Recording")
        assertExists("recordings-empty")
        XCTAssertEqual(takeFiles(), [])
        XCTAssertEqual(try journalText(), journal, "deleting a recording touched the journal")
        XCTAssertEqual(draftFiles(), drafts)
    }

    func testVoiceSettingsCanDeleteATakeOnceItsAnswersAreSaved() throws {
        let spoken = "Synthetic: I said no and kept the evening."
        try launch(environment: ["SENECA_UITEST_TRANSCRIPT": spoken])

        tap("settings")
        tap("voice-link")
        assertExists("speech-language")
        assertExists("speech-model-state")
        assertExists("prepare-speech-model")
        XCTAssertTrue(reach("voice-recordings").label.contains("None"), reach("voice-recordings").label)
        try audit([.contrast, .elementDetection, .hitRegion, .sufficientElementDescription, .trait])
        tap("voice-retention")
        let option = app.buttons["Delete After Saving"].firstMatch
        XCTAssertTrue(option.waitForExistence(timeout: 30))
        option.tap()
        back()
        XCTAssertTrue(reach("voice-link").label.contains("deleted after saving"), reach("voice-link").label)
        tap("settings-done")

        // Spoken, accepted into the form: kept until the entry is saved.
        tap("write-today")
        tap("speak-instead")
        speak()
        assertExists("proposal-fallback")
        tap("copy-transcript-0")
        tap("proposal-accept")
        assertExists("answer-0")
        XCTAssertEqual(takeFiles().count, 2, "a take went before its answers were saved")
        tap("save")
        assertExists("edit-today")
        waitForTakeFiles(0)
        XCTAssertTrue(try journalText().contains(spoken), "the answers were not saved")
    }

    func testARecoveryKitCanCarryTheRecordingsAndDiagnosticsOnlyCountThem() throws {
        let spoken = "Synthetic: words that stay on this iPhone."
        try launch(environment: ["SENECA_UITEST_TRANSCRIPT": spoken])
        recordFromToday()
        waitForTakeFiles(2)
        let id = try XCTUnwrap(takeFiles().first { $0.hasSuffix(".m4a") }).dropFirst(scenario.date.count + 1).dropLast(4)

        tap("settings")
        tap("recovery-link")
        reach("recovery-share-diagnostics")
        app.buttons["What Is Shared"].firstMatch.tap()
        let report = reach("recovery-diagnostics-report").label
        XCTAssertTrue(report.contains(#""audio_takes" : 1"#), report)
        XCTAssertTrue(report.contains(#""audio_pending_transcriptions" : 0"#), report)
        XCTAssertTrue(report.contains(#""speech_model" : "#), report)
        for secret in [spoken, "Synthetic", scenario.date, String(id), root.path] {
            XCTAssertFalse(report.contains(secret), "the diagnostics carry \(secret)")
        }

        let journal = try journalText()
        let include = reach("recovery-include-audio")
        include.switches.firstMatch.exists ? include.switches.firstMatch.tap() : include.tap()
        tap("recovery-export")
        confirm("Export Journal")
        assertExists("recovery-exported")
        let exported = reach("recovery-exported").label
        XCTAssertTrue(exported.hasPrefix("Exported") && exported.contains("1 recording"), exported)
        assertExists("recovery-share-export")
        reach("recovery-restore-audio")
        XCTAssertEqual(takeFiles().count, 2, "exporting moved the recording")
        XCTAssertEqual(try journalText(), journal, "exporting changes nothing")
    }
}
