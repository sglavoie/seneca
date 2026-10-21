#!/usr/bin/env bash
# Focused verification for the Seneca for iPhone plan.
#
#   bash scripts/verify_mobile_session.sh <session>
#
# Each session adds its own case below. A case passes only on behavioral
# evidence: every required tool must be present, every required suite must
# match at least one test, run without a skip, and report each named test as
# passed, and every piece of recorded evidence must exist and hold for the
# sources as they are now. Building or listing something is never enough on
# its own, and nothing missing is ever read as a pass.
#
# Environment (session 8):
#   SENECA_MOBILE_DEVICE=live     run the device round trip on the paired
#                                 iPhone now and hold that run, instead of
#                                 holding the committed device evidence
#   SENECA_IOS_DEVELOPMENT_TEAM   signing team for the device build (default:
#                                 the previous device build's team, else the
#                                 team the device evidence was signed by)
#   SENECA_IOS_SIMULATOR, SENECA_IOS_DEVICE, SENECA_IOS_CACHE
#                                 see ios/scripts
#
# Session 9 needs only a simulator: it stages a journal for the app, stops
# and relaunches it, and reads what it wrote, none of which a test can do to
# an app on a physical iPhone.
#
# Environment (session 10):
#   SENECA_MOBILE_DEVICE=live     run the end-to-end exchange on the paired
#                                 iPhone and this Mac through iCloud Drive
#                                 now and hold that run, instead of holding
#                                 the committed record of one
# Session 10 is never satisfied by a simulator: the transport faults are
# scripted there, and the exchange itself has to have happened between a
# physical iPhone and this Mac, built from the sources as they are now.
#
# Session 11 needs a simulator and Git: every change the phone can make is
# driven through the bridge into an exchange the real seneca program imports
# into Markdown and Git, and the app's browsing, settings, and conflict
# screens are driven over journals the engine wrote.
#
# Environment (session 12):
#   SENECA_MOBILE_DEVICE=live     have iOS deliver a reminder on the paired
#                                 iPhone now and hold that run, instead of
#                                 holding the committed record of one
# Session 12 is never satisfied by a simulator for delivery: whether iOS shows
# a reminder, and keeps quiet about one the app cancelled, has to have
# happened on a physical iPhone built from the sources as they are now.
#
# Environment (session 13):
#   SENECA_MOBILE_SUITES=live     run the UI suite, the simulator round trip,
#                                 and the sync rehearsal now and hold that run,
#                                 instead of the committed record of one
# Session 13 is the release gate. It runs the whole Go suite, vet, and the
# race detector, the bridge build and the hosted Swift suite, and holds the
# records only a person with the iPhone can make: the device checks, a trial
# over several days, the rollback rehearsal, and the rollout of the author's
# own journal (scripts/qualification.py, docs/mobile/qualification.md), as
# well as the device records of sessions 8, 10 and 12. Every one has to have
# been made from the sources as they are now on a physical iPhone. It names
# every step that is missing before running anything long.
#
# Session 15 is recording and transcription in the app. It needs only a
# simulator: the simulated recorder and transcribers stand in for the
# microphone and the speech models, through the vendored seams, in the hosted
# suite, and staged ones in the UI tests. A real take on the iPhone is the
# device qualification's.
#
# Session 16 is the proposed answers, their confirmation, and the save. It
# needs only a simulator: stand-in splitters play the on-device model in the
# hosted suite (available, unavailable, refusing, a take too long) and a
# staged one in the UI tests; the real model's answers are the device
# qualification's.
#
# Session 18 is voice capture and Apple's models on the author's iPhone. It
# holds the records only the author can make (scripts/voice_qualification.py):
# takes through a force-quit, a lock, a call and a headset change, English,
# French and mixed takes in quiet and noisy rooms, a three-minute take's way
# to proposed answers, ten takes' battery, and the debug-only Model
# Evaluation screen's responses replayed through `seneca review`'s validator,
# all from the sources as they are now on a physical iPhone. It names every
# record that is missing before running anything long.
#
# Session 14 is the iOS 26 baseline and the vendored capture package. It
# needs only a simulator: the deployment target everywhere it is set, the
# package's provenance, its own tests on an iOS simulator, the whole Go suite,
# and the hosted Swift suite with the package linked into the app.

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

fail() {
	echo "verify_mobile_session: $*" >&2
	exit 1
}

note() {
	echo "verify_mobile_session: $*"
}

# require_tools TOOL... fails unless every tool is on PATH.
require_tools() {
	local tool
	for tool in "$@"; do
		command -v "$tool" >/dev/null 2>&1 || fail "required tool not found: $tool"
	done
}

# require_files PATH... fails unless every path exists and is not empty.
require_files() {
	local path
	for path in "$@"; do
		[ -s "$path" ] || fail "required artifact missing or empty: $path"
	done
}

# run_go_suite LABEL PACKAGE RUN_REGEX REQUIRED_TEST...
#
# Runs one Go test selection verbosely and holds it to the evidence rules: the
# command must succeed, must match tests, must skip none, and must report
# every REQUIRED_TEST (a top-level name or a Test/subtest path) as passed.
run_go_suite() {
	local label=$1 package=$2 pattern=$3
	shift 3
	local log="$work_dir/$label.log"
	note "running $label: go test -count=1 -v -run '$pattern' $package"
	if ! go test -count=1 -v -run "$pattern" "$package" >"$log" 2>&1; then
		cat "$log" >&2
		fail "$label: go test failed"
	fi
	if grep -q 'no tests to run' "$log"; then
		cat "$log" >&2
		fail "$label: pattern '$pattern' matched no tests in $package"
	fi
	if grep -Eq -- '--- SKIP:' "$log"; then
		grep -E -- '--- SKIP:' "$log" >&2
		fail "$label: a required test was skipped"
	fi
	local passed
	passed=$(grep -Ec -- '--- PASS:' "$log" || true)
	[ "$passed" -gt 0 ] || fail "$label: zero tests passed"
	local test
	for test in "$@"; do
		grep -Fq -- "--- PASS: $test (" "$log" || fail "$label: missing required passing test $test"
	done
	note "$label: $passed passing tests and subtests"
}

# Session 8: the Go-to-Swift bridge and the iPhone shell.
verify_session_8() {
	[ "$(uname -s)" = Darwin ] || fail "session 8 builds and runs an iPhone app; it needs macOS with Xcode"
	require_tools go xcrun xcodebuild python3 shasum plutil codesign lipo
	xcrun simctl help >/dev/null 2>&1 || fail "required tool not found: simctl"
	xcrun devicectl --version >/dev/null 2>&1 || fail "required tool not found: devicectl"

	local device_evidence=docs/mobile/evidence/session-8-device-roundtrip.json
	local expected=mobile/testdata/roundtrip/expected.json
	require_files \
		docs/mobile/bridge.md ios/README.md ios/project.yml ios/XCODEGEN_VERSION \
		ios/SenecaEngine/Package.swift \
		mobile/testdata/roundtrip/scenario.json "$expected" \
		mobile/testdata/roundtrip/journal/reflections.md \
		mobile/testdata/roundtrip/journal/seneca-reviews.md

	# The toolchain that binds the engine is the one go.mod pins.
	local pinned
	pinned=$(go list -m -f '{{.Version}}' golang.org/x/mobile 2>/dev/null) || fail "go.mod pins no golang.org/x/mobile"
	[ -n "$pinned" ] || fail "go.mod pins no golang.org/x/mobile"
	local tool
	for tool in golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind; do
		go tool -n "$tool" >/dev/null 2>&1 || fail "go.mod does not pin $tool as a tool"
	done
	note "gomobile pinned at $pinned"

	# The bridge in Go: the contract, the conditions and identities mutations
	# carry, and the desktop's own reading of what it writes.
	go vet ./mobile/... || fail "go vet ./mobile/... failed"
	run_go_suite bridge ./mobile/senecabridge/ '^Test' \
		TestEveryRequestIsHeldToTheEnvelope \
		TestOperationsNeedAnOpenJournal \
		TestTheBridgeVersionIsAClosedSet \
		TestOpenReportsTheJournalWithoutWritingIt \
		TestTheJournalsDayFollowsItsTimeZone \
		TestFixtureReadsAgreeWithTheCommandLineBaseline \
		TestASavedReflectionIsWhatTheDesktopWouldHaveWritten \
		TestARepeatedSaveIsAnsweredNotRepeated \
		TestARepeatedSaveIsAnsweredAfterTheContainerMoved \
		TestARefusedRequestIsNotRemembered \
		TestReflectionRefusalsCarryStableCodes \
		TestAWriteThatLandedIsAnsweredAsOne \
		TestAFailureInsideTheEngineIsAnAnswer \
		TestFocusChangesAreConditionalAndNotRepeated \
		TestSettingsChangeAsOneConditionalUnit \
		TestWritingASaveCouldNotStoreIsKept \
		TestTwoDevicesExchangeAndSettleAConflict \
		TestTheContractPageListsEveryOperationAndCode \
		TestTheBridgeLinksOnlyTheEngine \
		TestTheRoundTripFixtureIsTheCompatibilityJournal
	run_go_suite desktop-parity . '^TestRoundTrip' \
		TestRoundTripExpectationIsWhatTheDesktopWrites \
		TestRoundTripThroughTheBridgeMatchesTheDesktop
	run_go_suite engine-boundary ./engine/ '^TestEngine(DependsOnTheStandardLibraryOnly|LinksNoTerminalOrSubprocessCode)$' \
		TestEngineDependsOnTheStandardLibraryOnly \
		TestEngineLinksNoTerminalOrSubprocessCode
	run_go_suite contract-docs ./cmd/ '^TestCompat(ContractPagesExistAndAreLinked|WritePathInventoryNamesEveryMutatingFile)$' \
		TestCompatContractPagesExistAndAreLinked \
		TestCompatWritePathInventoryNamesEveryMutatingFile

	# The XCFramework: built by the pinned toolchain from the current sources,
	# with a device slice and a simulator slice.
	ios/scripts/build-bridge.sh || fail "the bridge XCFramework did not build"
	local framework=ios/SenecaEngine/Frameworks/SenecaBridgeCore.xcframework
	require_files "$framework/Info.plist" ios/SenecaEngine/Frameworks/SenecaBridgeCore.fingerprint
	local bridge_fp source_fp libraries slice
	bridge_fp=$(ios/scripts/build-bridge.sh --fingerprint)
	[ "$(cat ios/SenecaEngine/Frameworks/SenecaBridgeCore.fingerprint)" = "$bridge_fp" ] ||
		fail "the XCFramework was not built from the current sources"
	libraries=$(plutil -extract AvailableLibraries json -o - "$framework/Info.plist")
	for slice in '"ios-arm64"' '"ios-arm64_x86_64-simulator"'; do
		case "$libraries" in *"$slice"*) ;; *) fail "the XCFramework has no $slice slice" ;; esac
	done
	source_fp=$(ios/scripts/source-fingerprint.sh)
	note "bridge $bridge_fp, sources $source_fp"

	# The simulator: the app's hosted suite against the bound engine, every
	# test executed and passed.
	ios/scripts/test.sh "$work_dir/suite" \
		'RoundTripTests/testSavesAReflectionThroughGoAndReopensItAsTheDesktopWroteIt()' \
		'RoundTripTests/testTheAppModelShowsTheSavedReflection()' \
		'BridgeOperationsTests/testTheLinkedEngineSpeaksTheBridgeTheseModelsWereWrittenFor()' \
		'BridgeOperationsTests/testReadsTheSyntheticJournal()' \
		'BridgeOperationsTests/testAStaleOrRepeatedSaveNeverWritesTwice()' \
		'BridgeOperationsTests/testFocusAndSettingsChangeOnlyFromTheStateTheyWereDecidedOn()' \
		'BridgeOperationsTests/testTwoDevicesExchangeAndSettleAConflict()' \
		'EngineServiceTests/testCallsRunOneAtATimeAwayFromTheMainThread()' \
		'EngineServiceTests/testEveryRequestStatesItsVersionAndAMutationItsIdentity()' \
		'EngineServiceTests/testRefusalsAndBrokenRepliesAreDifferentFailures()' ||
		fail "the simulator suite did not hold"

	# The simulator again, across a redeployment: saved by one build, reopened
	# by the next, held to what the desktop writes.
	ios/scripts/roundtrip.sh simulator "$work_dir/roundtrip-simulator" || fail "the simulator round trip did not hold"
	python3 -I ios/scripts/evidence.py check "$work_dir/roundtrip-simulator/roundtrip.json" simulator \
		"$expected" "$source_fp" "$bridge_fp" || fail "the simulator round trip record does not hold"

	# The device: a signed build of the current sources, and the round trip
	# on a physical iPhone across a signed redeployment.
	if [ "${SENECA_MOBILE_DEVICE:-}" = live ]; then
		ios/scripts/roundtrip.sh device "$work_dir/roundtrip-device" || fail "the device round trip did not hold"
		python3 -I ios/scripts/evidence.py check "$work_dir/roundtrip-device/roundtrip.json" device \
			"$expected" "$source_fp" "$bridge_fp" || fail "the device round trip record does not hold"
	else
		require_files "$device_evidence"
		python3 -I ios/scripts/evidence.py check "$device_evidence" device "$expected" "$source_fp" "$bridge_fp" ||
			fail "the device evidence does not hold for the current sources; record it again with \`just ios-device-evidence\`"
		if [ -z "${SENECA_IOS_DEVELOPMENT_TEAM:-}" ]; then
			# The team that signed the evidence signs this build, unless a
			# previous device build already names one.
			local recorded_team
			recorded_team=$(python3 -I -c 'import json, sys; print(json.load(open(sys.argv[1]))["device"]["signing_team"])' "$device_evidence")
			if [ ! -d "${SENECA_IOS_CACHE:-ios/.cache}/DeviceDerivedData/Build/Products/Debug-iphoneos/Seneca.app" ]; then
				export SENECA_IOS_DEVELOPMENT_TEAM="$recorded_team"
			fi
		fi
		local device_app
		device_app=$(ios/scripts/app.sh build-device) || fail "the signed device build failed"
		[ -d "$device_app" ] || fail "the signed device build left no app"
		note "signed device build: $device_app"
	fi

	note "session 8 verified"
}

# Session 9: the daily reflection workflow and durable drafts.
verify_session_9() {
	[ "$(uname -s)" = Darwin ] || fail "session 9 runs an iPhone app on a simulator; it needs macOS with Xcode"
	require_tools go xcrun xcodebuild python3 shasum
	xcrun simctl help >/dev/null 2>&1 || fail "required tool not found: simctl"
	require_files \
		ios/project.yml ios/scripts/test.sh ios/scripts/uitest.sh \
		ios/SenecaMobile/DraftStore.swift ios/SenecaMobile/ReflectionSession.swift \
		ios/SenecaMobile/ReflectionForm.swift ios/SenecaMobile/RootView.swift \
		ios/SenecaMobileTests/DailyWorkflowTests.swift \
		ios/SenecaMobileUITests/DailyWorkflowUITests.swift ios/SenecaMobileUITests/AccessibilityUITests.swift \
		mobile/testdata/roundtrip/scenario.json mobile/testdata/roundtrip/journal/reflections.md \
		mobile/testdata/roundtrip/journal/seneca-reviews.md

	# Offline by construction: neither the bridge nor the app has a way to
	# reach a network, so everything the suites below prove, they prove of a
	# phone with no connection.
	local networked
	networked=$(go list -deps ./mobile/senecabridge | grep -E '^net(/|$)' || true)
	[ -z "$networked" ] || fail "the bridge links network packages: $networked"
	networked=$(grep -rnE 'URLSession|URLRequest|import Network|NWConnection|CFNetwork' ios/SenecaMobile ios/SenecaEngine/Sources ios/Packages/Capture/Sources || true)
	[ -z "$networked" ] || fail "the app reaches for the network: $networked"

	# The engine behind the form, through the bridge: writing without context
	# beside an unreadable archive, edits that keep what the form never
	# showed, stale saves refused, and writing a save could not store kept.
	go vet ./mobile/... || fail "go vet ./mobile/... failed"
	run_go_suite bridge-daily ./mobile/senecabridge/ \
		'^Test(AnUnreadableArchiveStillPermitsABlankReflection|AnEditKeepsWhatNoQuestionNames|ReflectionRefusalsCarryStableCodes|WritingASaveCouldNotStoreIsKept|ARepeatedSaveIsAnsweredNotRepeated)$' \
		TestAnUnreadableArchiveStillPermitsABlankReflection \
		TestAnEditKeepsWhatNoQuestionNames \
		TestReflectionRefusalsCarryStableCodes \
		TestWritingASaveCouldNotStoreIsKept \
		TestARepeatedSaveIsAnsweredNotRepeated

	ios/scripts/build-bridge.sh || fail "the bridge XCFramework did not build"

	# The draft store and the form's session, hosted in the app on a
	# simulator against the bound engine. Each relaunch in them is a new
	# model, engine, and store over what the last one left on disk.
	ios/scripts/test.sh "$work_dir/suite" \
		'DailyWorkflowTests/testADraftKeepsItsDateBaseQuestionsAnswersAndContext()' \
		'DailyWorkflowTests/testADraftSurvivesTerminationAndIsClearedOnlyByASaveOrADiscard()' \
		'DailyWorkflowTests/testTheJournalAlreadyHoldingADraftClearsIt()' \
		'DailyWorkflowTests/testAStaleEditKeepsEveryTypedWord()' \
		'DailyWorkflowTests/testAFailedSaveKeepsTheDraftUntilTheJournalTakesIt()' \
		'DailyWorkflowTests/testAnUnreadableArchiveStillPermitsWritingWithoutContext()' \
		'DailyWorkflowTests/testEditingKeepsWhatNoQuestionNames()' \
		'DailyWorkflowTests/testAnEarlierDayIsWrittenInItsPlaceAndALaterOneRefused()' \
		'DailyWorkflowTests/testAnswersFollowTheirQuestionsWhenTheSettingsChange()' \
		'DailyWorkflowTests/testADraftThatCannotBeReadNeverStandsInTheWayOfWriting()' \
		'RoundTripTests/testTheAppModelShowsTheSavedReflection()' ||
		fail "the simulator suite did not hold"

	# The app itself, driven from outside on a simulator: stopped and
	# relaunched mid-writing, saved over a day that changed underneath,
	# written beside a torn archive, at the largest text size, by its
	# accessibility labels, and from the keyboard.
	ios/scripts/uitest.sh "$work_dir/uitest" \
		'DailyWorkflowUITests/testWritingSurvivesForcedTermination()' \
		'DailyWorkflowUITests/testADiscardedDraftIsGone()' \
		'DailyWorkflowUITests/testAStaleEditKeepsAllTypedText()' \
		'DailyWorkflowUITests/testACorruptArchiveStillPermitsWritingWithoutContext()' \
		'DailyWorkflowUITests/testAnExistingDayIsEditedAndKeepsWhatNoQuestionNames()' \
		'DailyWorkflowUITests/testAnotherDayCanBeWrittenFromToday()' \
		'AccessibilityUITests/testTheLargestTextSizeKeepsEverythingReadableAndReachable()' \
		'AccessibilityUITests/testEveryControlSaysWhatItIs()' \
		'AccessibilityUITests/testTheKeyboardMovesThroughTheFieldsAndGetsOutOfTheWay()' ||
		fail "the UI tests did not hold"

	note "session 9 verified"
}

# Session 10: the iCloud transport and the exchange end to end.
verify_session_10() {
	[ "$(uname -s)" = Darwin ] || fail "session 10 exchanges between an iPhone and a Mac; it needs macOS with Xcode"
	require_tools go git xcrun xcodebuild python3 shasum
	xcrun simctl help >/dev/null 2>&1 || fail "required tool not found: simctl"
	xcrun devicectl --version >/dev/null 2>&1 || fail "required tool not found: devicectl"

	local sync_evidence=docs/mobile/evidence/session-10-device-sync.json
	require_files \
		docs/mobile/icloud-transport.md docs/mobile/bridge.md docs/sync.md ios/project.yml \
		engine/sync_receipt.go cmd/sync_cloud.go \
		ios/SenecaMobile/Exchange/ExchangeFolder.swift ios/SenecaMobile/Exchange/ExchangeGrant.swift \
		ios/SenecaMobile/Exchange/ExchangeMirror.swift ios/SenecaMobile/Exchange/ExchangeService.swift \
		ios/SenecaMobile/Exchange/SyncModel.swift ios/SenecaMobile/SyncView.swift \
		ios/SenecaMobileTests/ExchangeTests.swift ios/SenecaMobileTests/ExchangeLifecycleTests.swift \
		ios/SenecaMobileTests/SyncEvidenceTests.swift \
		ios/scripts/sync-evidence.sh ios/scripts/sync_evidence.py

	# The phone's engine stays headless: file coordination, bookmarks, and
	# cloud downloads are the platforms' adapters, on each side.
	local leaked
	leaked=$(go list -deps ./mobile/senecabridge | grep -E '^(net(/|$)|os/exec$|github.com/sglavoie/seneca/cmd$)' || true)
	[ -z "$leaked" ] || fail "the bridge links what belongs to a platform adapter: $leaked"
	leaked=$(grep -rnE 'brctl|icloud|Mobile Documents|SF_DATALESS' --include='*.go' engine mobile | grep -v '_test.go' || true)
	[ -z "$leaked" ] || fail "the engine or the bridge knows about iCloud: $leaked"
	leaked=$(grep -rnE 'URLSession|URLRequest|import Network|NWConnection|CFNetwork|CloudKit' ios/SenecaMobile ios/SenecaEngine/Sources || true)
	[ -z "$leaked" ] || fail "the app reaches for the network itself: $leaked"

	go vet ./engine/... ./cmd/... ./mobile/... . || fail "go vet failed"

	# Receipts: saved here, in the exchange, and applied there are three
	# things, and each is said only once it is true.
	run_go_suite receipts ./engine/ '^Test(DeliveryTellsSavedFromPublishedFromApplied|AReceiptIsWrittenOnlyWhenWhatWasAppliedGrew|AReceiptDoesNotClaimWhatIsStillWaiting|DamagedForeignAndForgedReceiptsAreNotCounted|InspectingDeliveryChangesNothing)$' \
		TestDeliveryTellsSavedFromPublishedFromApplied \
		TestAReceiptIsWrittenOnlyWhenWhatWasAppliedGrew \
		TestAReceiptDoesNotClaimWhatIsStillWaiting \
		TestDamagedForeignAndForgedReceiptsAreNotCounted \
		TestInspectingDeliveryChangesNothing
	run_go_suite engine-boundary ./engine/ '^TestEngine(DependsOnTheStandardLibraryOnly|LinksNoTerminalOrSubprocessCode)$' \
		TestEngineDependsOnTheStandardLibraryOnly \
		TestEngineLinksNoTerminalOrSubprocessCode

	# The bridge the phone exchanges through, under what a cloud folder does:
	# any order, cut short, stopped between contents and manifest.
	run_go_suite bridge-transport ./mobile/senecabridge/ \
		'^Test(TheBridgeVersionIsAClosedSet|DeliveryTellsSavedFromPublishedFromApplied|AnExchangeArrivingOutOfOrderOrCutShortIsAppliedOnlyWhole|AnInterruptedPublicationIsInvisibleUntilItIsFinished|TwoDevicesExchangeAndSettleAConflict|TheContractPageListsEveryOperationAndCode|TheBridgeLinksOnlyTheEngine)$' \
		TestTheBridgeVersionIsAClosedSet \
		TestDeliveryTellsSavedFromPublishedFromApplied \
		TestAnExchangeArrivingOutOfOrderOrCutShortIsAppliedOnlyWhole \
		TestAnInterruptedPublicationIsInvisibleUntilItIsFinished \
		TestTwoDevicesExchangeAndSettleAConflict \
		TestTheContractPageListsEveryOperationAndCode \
		TestTheBridgeLinksOnlyTheEngine

	# The desktop's side of iCloud Drive: files listed and not downloaded are
	# asked for and never read, and a receipt follows the Markdown and Git.
	run_go_suite desktop-cloud ./cmd/ '^Test(ACloudOnlyFileHasNotArrived|AskingForCloudFilesIsBoundedAndSurvivesAMissingProgram|CompatWritePathInventoryNamesEveryMutatingFile|CompatContractPagesExistAndAreLinked)$' \
		TestACloudOnlyFileHasNotArrived \
		TestAskingForCloudFilesIsBoundedAndSurvivesAMissingProgram \
		TestCompatWritePathInventoryNamesEveryMutatingFile \
		TestCompatContractPagesExistAndAreLinked
	run_go_suite desktop-exchange . \
		'^Test(APlaceholderInTheExchangeIsAskedForAndImportedOnceItArrives|ADescriptorStillDownloadingNeverStartsASecondJournal|TheDesktopsReceiptFollowsItsMarkdownAndItsCommit|BackgroundImportReachesMarkdownAndAnIsolatedGitCommit|AGitFailureIsRetriedWithoutRepeatingTheImport|AMutationImportsFirstAndPublishesAfterward)$' \
		TestAPlaceholderInTheExchangeIsAskedForAndImportedOnceItArrives \
		TestADescriptorStillDownloadingNeverStartsASecondJournal \
		TestTheDesktopsReceiptFollowsItsMarkdownAndItsCommit \
		TestBackgroundImportReachesMarkdownAndAnIsolatedGitCommit \
		TestAGitFailureIsRetriedWithoutRepeatingTheImport \
		TestAMutationImportsFirstAndPublishesAfterward

	ios/scripts/build-bridge.sh || fail "the bridge XCFramework did not build"

	# The app's own adapter, hosted in the app on a simulator against the
	# bound engine, with the folder's faults scripted: placeholders, files cut
	# short, a folder that is gone, a grant taken back, a transfer stopped
	# part-way, and every moment the app exchanges by itself.
	ios/scripts/test.sh "$work_dir/suite" \
		'ExchangeTests/testContentsArePublishedBeforeManifestsAndOnlyExchangeFilesLeaveThePhone()' \
		'ExchangeTests/testAFileStillInTheCloudIsAskedForAndAppliedOnlyOnceItIsHere()' \
		'ExchangeTests/testAFileCutShortNeverEntersTheMirror()' \
		'ExchangeTests/testAnUnreachableFolderOrARevokedGrantNeverStopsWriting()' \
		'ExchangeTests/testAPublicationStoppedPartWayIsInvisibleAndTheNextOneFinishesIt()' \
		'ExchangeTests/testAnExchangeCancelledBeforeItStartsDoesNothing()' \
		'ExchangeTests/testDeliveryTellsSavedFromPublishedFromApplied()' \
		'ExchangeTests/testEditsMadeOnBothSidesAtOnceAreBothKept()' \
		'ExchangeTests/testAnEmptyFolderIsStartedFromThePhoneAndADescriptorOnItsWayIsWaitedFor()' \
		'ExchangeLifecycleTests/testTheAppExchangesAtLaunchInFrontAfterASaveAndOnRefresh()' \
		'ExchangeLifecycleTests/testRequestsMadeDuringAnExchangeNeverRunBesideIt()' \
		'ExchangeLifecycleTests/testWhatIsStillArrivingIsTriedAgainUntilItIsHere()' \
		'ExchangeLifecycleTests/testAFolderThatCannotBeReachedIsSaidSoAndNeverStopsWriting()' \
		'ExchangeLifecycleTests/testLeavingTheScreenExchangesOnceAndSchedulesNothing()' \
		'ExchangeLifecycleTests/testTheGrantToAFolderOutlastsTheAppAndSaysWhenItStoppedWorking()' \
		'ExchangeLifecycleTests/testTheCoordinatedFolderListsPlaceholdersAndNeverReplacesAFile()' \
		'BridgeOperationsTests/testTheLinkedEngineSpeaksTheBridgeTheseModelsWereWrittenFor()' \
		'BridgeOperationsTests/testTwoDevicesExchangeAndSettleAConflict()' ||
		fail "the simulator suite did not hold"

	# The exchange itself: a physical iPhone and this Mac, through iCloud
	# Drive, built from these sources. A simulator's record is refused.
	if [ "${SENECA_MOBILE_DEVICE:-}" = live ]; then
		ios/scripts/sync-evidence.sh device "$work_dir/sync-device" || fail "the device exchange did not hold"
		python3 -I ios/scripts/sync_evidence.py check "$work_dir/sync-device/sync.json" device ||
			fail "the device exchange's record does not hold"
	else
		[ -s "$sync_evidence" ] || fail "no real-device evidence at $sync_evidence; record it with \`just ios-sync-evidence\`"
		python3 -I ios/scripts/sync_evidence.py check "$sync_evidence" device ||
			fail "the real-device evidence does not hold for the current sources; record it again with \`just ios-sync-evidence\`"
	fi

	note "session 10 verified"
}

# Session 11: browsing, settings, and conflicts on the phone.
verify_session_11() {
	[ "$(uname -s)" = Darwin ] || fail "session 11 runs an iPhone app on a simulator; it needs macOS with Xcode"
	require_tools go git xcrun xcodebuild python3 shasum
	xcrun simctl help >/dev/null 2>&1 || fail "required tool not found: simctl"
	require_files \
		engine/history_query.go docs/mobile/bridge.md ios/project.yml \
		mobile/testdata/browsing/journal/reflections.md mobile/testdata/browsing/journal/seneca-reviews.md \
		mobile/testdata/conflicted/journal/reflections.md \
		mobile/testdata/conflicted/journal/.seneca/journals/reflections.md/sync/state.json \
		mobile/testdata/conflicted/exchange/journal.json \
		ios/SenecaMobile/HistoryView.swift ios/SenecaMobile/ReviewsView.swift ios/SenecaMobile/FocusView.swift \
		ios/SenecaMobile/QuestionsEditor.swift ios/SenecaMobile/ConflictsView.swift \
		ios/SenecaMobileTests/BrowsingTests.swift ios/SenecaMobileTests/ConflictTests.swift \
		ios/SenecaMobileUITests/BrowsingUITests.swift ios/SenecaMobileUITests/ConflictUITests.swift

	# Still offline by construction, and still headless.
	local leaked
	leaked=$(go list -deps ./mobile/senecabridge | grep -E '^(net(/|$)|os/exec$|github.com/sglavoie/seneca/cmd$)' || true)
	[ -z "$leaked" ] || fail "the bridge links what belongs to a platform adapter: $leaked"
	leaked=$(grep -rnE 'URLSession|URLRequest|import Network|NWConnection|CFNetwork|CloudKit' ios/SenecaMobile ios/SenecaEngine/Sources || true)
	[ -z "$leaked" ] || fail "the app reaches for the network itself: $leaked"

	go vet ./engine/... ./cmd/... ./mobile/... . || fail "go vet failed"

	# The queries the phone's history and reviews ask are the command line's:
	# one implementation in the engine, the same answers from `seneca show`
	# and `seneca review history`.
	run_go_suite cli-queries ./cmd/ \
		'^Test(ShowSearchNarrowsToAMonthOrRange|ShowRangeKeepsInclusiveBoundsAndOpenEnds|OnThisDayKeepsEarlierEntriesOnTheSameDayOfTheMonth|NearestEntriesHintNamesTheClosestDatesOnEachSide|ReviewHistoryShowsRevisionLinks|ReviewHistoryPeriodKeepsOnlyThatPeriodBeforeCounting|FocusSetShowClearLifecycle)$' \
		TestShowSearchNarrowsToAMonthOrRange \
		TestShowRangeKeepsInclusiveBoundsAndOpenEnds \
		TestOnThisDayKeepsEarlierEntriesOnTheSameDayOfTheMonth \
		TestNearestEntriesHintNamesTheClosestDatesOnEachSide \
		TestReviewHistoryShowsRevisionLinks \
		TestReviewHistoryPeriodKeepsOnlyThatPeriodBeforeCounting \
		TestFocusSetShowClearLifecycle

	# The bridge: history queries, every review period with its revisions, a
	# conflicted day that stays readable and takes no writing until settled,
	# conditional focus and settings changes, and the fixtures the app's
	# suites run on, held to what the engine writes.
	run_go_suite bridge-browsing ./mobile/senecabridge/ \
		'^Test(HistoryQueriesAnswerAsTheCommandLineDoes|SavedReviewsOfEveryPeriodAndTheirRevisions|AConflictedDayIsReadableAndKeepsNewWritingOutUntilItIsSettled|TheSessionFixturesAreWhatTheEngineWrites|FocusChangesAreConditionalAndNotRepeated|SettingsChangeAsOneConditionalUnit|TwoDevicesExchangeAndSettleAConflict|TheBridgeVersionIsAClosedSet|TheContractPageListsEveryOperationAndCode|EveryRequestIsHeldToTheEnvelope)$' \
		TestHistoryQueriesAnswerAsTheCommandLineDoes \
		TestSavedReviewsOfEveryPeriodAndTheirRevisions \
		TestAConflictedDayIsReadableAndKeepsNewWritingOutUntilItIsSettled \
		TestTheSessionFixturesAreWhatTheEngineWrites/browsing \
		TestTheSessionFixturesAreWhatTheEngineWrites/conflicted \
		TestFocusChangesAreConditionalAndNotRepeated \
		TestSettingsChangeAsOneConditionalUnit \
		TestTwoDevicesExchangeAndSettleAConflict \
		TestTheBridgeVersionIsAClosedSet \
		TestTheContractPageListsEveryOperationAndCode \
		TestEveryRequestIsHeldToTheEnvelope

	# Every change the phone can make, through the bridge, into the real
	# seneca program's Markdown and Git and back: a reflection, the questions
	# and the anchor, the focus set and cleared, a disputed focus and a
	# conflicted day settled, a journal started on the phone with its
	# settings, and a lost file restored. And a conflict settled while the
	# phone was offline, converging once the exchange resumes.
	run_go_suite desktop-roundtrip . \
		'^Test(EveryPhoneMutationRoundTripsThroughTheDesktop|AConflictSettledOfflineConvergesOnceTheExchangeResumes|AJournalStartedOnThePhoneReachesTheDesktop|RoundTripThroughTheBridgeMatchesTheDesktop)$' \
		TestEveryPhoneMutationRoundTripsThroughTheDesktop \
		TestAConflictSettledOfflineConvergesOnceTheExchangeResumes \
		TestAJournalStartedOnThePhoneReachesTheDesktop \
		TestRoundTripThroughTheBridgeMatchesTheDesktop

	ios/scripts/build-bridge.sh || fail "the bridge XCFramework did not build"

	# The app's model and the form's session over the bound engine, hosted
	# in the app on a simulator: the same queries and changes, a draft kept
	# for a conflicted day across a relaunch, and two engines converging
	# after one settled a conflict offline.
	ios/scripts/test.sh "$work_dir/suite" \
		'BrowsingTests/testHistoryIsFilteredNavigatedAndSearchedByTheEnginesQueries()' \
		'BrowsingTests/testEverySavedReviewPeriodIsShownWithItsRevisions()' \
		'BrowsingTests/testTheFocusIsSetAndClearedOnlyFromTheTimelineShown()' \
		'BrowsingTests/testTheQuestionsAndTheAnchorChangeAsOneUnit()' \
		'ConflictTests/testAConflictedDayStaysReadableAndItsWritingStaysADraftUntilItIsSettled()' \
		'ConflictTests/testAnAnswerIsGivenOnceAndAStaleOneIsRefused()' \
		'ConflictTests/testAConflictSettledOfflineConvergesOnceTheExchangeResumes()' \
		'BridgeOperationsTests/testTheLinkedEngineSpeaksTheBridgeTheseModelsWereWrittenFor()' \
		'DailyWorkflowTests/testAStaleEditKeepsEveryTypedWord()' ||
		fail "the simulator suite did not hold"

	# The screens, driven from outside on a simulator.
	ios/scripts/uitest.sh "$work_dir/uitest" \
		'BrowsingUITests/testHistoryIsFilteredByMonthRangeAndDateAndSearched()' \
		'BrowsingUITests/testEveryReviewPeriodAndEachRevisionCanBeRead()' \
		'BrowsingUITests/testTheFocusIsSetAndClearedAndItsHistoryKept()' \
		'BrowsingUITests/testTheQuestionsAndTheAnchorAreEditedTogether()' \
		'ConflictUITests/testAConflictIsInspectedAndSettledByKeepingAVersion()' \
		'ConflictUITests/testWritingForAConflictedDayStaysADraftUntilItIsSettledAndEdited()' \
		'DailyWorkflowUITests/testWritingSurvivesForcedTermination()' ||
		fail "the UI tests did not hold"

	note "session 11 verified"
}

# Session 12: reminders, diagnostics, and recovery.
verify_session_12() {
	[ "$(uname -s)" = Darwin ] || fail "session 12 runs an iPhone app; it needs macOS with Xcode"
	require_tools go git xcrun xcodebuild python3 shasum
	xcrun simctl help >/dev/null 2>&1 || fail "required tool not found: simctl"
	xcrun devicectl --version >/dev/null 2>&1 || fail "required tool not found: devicectl"

	local notify_evidence=docs/mobile/evidence/session-12-device-notification.json
	require_files \
		docs/mobile/recovery.md docs/mobile/bridge.md docs/sync.md README.md ios/README.md \
		engine/reminders.go engine/diagnostics.go engine/recovery_export.go engine/sync_leave.go \
		cmd/sync_health.go cmd/backup.go mobile/senecabridge/recovery_ops.go \
		ios/SenecaMobile/Reminders/ReminderCenter.swift ios/SenecaMobile/Reminders/ReminderScheduler.swift \
		ios/SenecaMobile/Reminders/ReminderPresenter.swift ios/SenecaMobile/RemindersView.swift \
		ios/SenecaMobile/RecoveryView.swift ios/SenecaMobile/Exchange/IdentityGuard.swift \
		ios/SenecaMobileTests/ReminderTests.swift ios/SenecaMobileTests/RecoveryTests.swift \
		ios/SenecaMobileTests/NotificationEvidenceTests.swift ios/SenecaMobileUITests/RecoveryUITests.swift \
		ios/scripts/notify-evidence.sh ios/scripts/notify_evidence.py

	# The procedures are written down, every one of them.
	local section
	for section in '## Evening reminders' '### What a save on the Mac can do' '### When notifications are not allowed' \
		'## Doctor and status' '## Diagnostics to share' '## Backing up: the recovery export' '## Replacing a device' \
		'## Joining from a fresh installation' '## Disconnecting sync' '## Restoring'; do
		grep -Fqx -- "$section" docs/mobile/recovery.md || fail "docs/mobile/recovery.md has no section $section"
	done

	# Still offline by construction, and still headless: reminders are the
	# app's, through iOS, and the engine only plans them.
	local leaked
	leaked=$(go list -deps ./mobile/senecabridge | grep -E '^(net(/|$)|os/exec$|github.com/sglavoie/seneca/cmd$)' || true)
	[ -z "$leaked" ] || fail "the bridge links what belongs to a platform adapter: $leaked"
	leaked=$(grep -rnE 'URLSession|URLRequest|import Network|NWConnection|CFNetwork|CloudKit' ios/SenecaMobile ios/SenecaEngine/Sources || true)
	[ -z "$leaked" ] || fail "the app reaches for the network itself: $leaked"
	leaked=$(grep -rnE 'UserNotifications|osascript' --include='*.go' engine mobile | grep -v '_test.go' || true)
	[ -z "$leaked" ] || fail "the engine or the bridge shows notifications itself: $leaked"

	go vet ./engine/... ./cmd/... ./mobile/... . || fail "go vet failed"

	# The engine: one reminder rule for both devices, diagnostics that say
	# nothing written, an export that is whole or absent, leaving that keeps
	# every file, and a copy of a device kept from speaking as it.
	run_go_suite engine-recovery ./engine/ \
		'^Test(RemindersFollowTheDesktopScheduleAndSkipCompleteDays|RemindersKeepTheirWallClockAcrossZonesAndDaylightSaving|ReminderTimesAreValidatedAndOrdered|ARecoveryExportHoldsAllWritingAndNoIdentity|AnInterruptedRecoveryExportLeavesNothingAndIsDoneAgain|LeavingSyncKeepsTheWritingAndRetiresTheIdentity|ADeviceReplacedOrRestoredFromACopyNeverSpeaksAsAnother|DiagnosticsSayNothingWrittenInTheJournal|DiagnosticsNameIncompatibleVersionsAndPendingTransfers|InspectingSyncReadsWithoutChangingAnything|EngineDependsOnTheStandardLibraryOnly|EngineLinksNoTerminalOrSubprocessCode)$' \
		TestRemindersFollowTheDesktopScheduleAndSkipCompleteDays \
		TestRemindersKeepTheirWallClockAcrossZonesAndDaylightSaving \
		TestReminderTimesAreValidatedAndOrdered \
		TestARecoveryExportHoldsAllWritingAndNoIdentity \
		TestAnInterruptedRecoveryExportLeavesNothingAndIsDoneAgain \
		TestLeavingSyncKeepsTheWritingAndRetiresTheIdentity \
		TestADeviceReplacedOrRestoredFromACopyNeverSpeaksAsAnother \
		TestDiagnosticsSayNothingWrittenInTheJournal \
		TestDiagnosticsNameIncompatibleVersionsAndPendingTransfers \
		TestInspectingSyncReadsWithoutChangingAnything \
		TestEngineDependsOnTheStandardLibraryOnly \
		TestEngineLinksNoTerminalOrSubprocessCode

	# The desktop: doctor's sync findings and their actions, read-only; the
	# shared report; the backup and leaving; the reminder it always had; and
	# the command surface and contract pages that name all of it.
	run_go_suite desktop-recovery ./cmd/ \
		'^Test(DoctorSaysWhatWaitsInSyncAndHowToSettleIt|SharedDiagnosticsCarryNothingWritten|BackupAndLeavingSyncKeepEveryFile|DoctorNeverCreatesOrChangesAnything|CompatDoctorPrintsItsBaselineAndWritesNothing|ReflectionRecordedOnDateFindsSavedEntry|EveryCommandAndFlagIsAccountedFor|CompatWritePathInventoryNamesEveryMutatingFile|CompatContractPagesExistAndAreLinked|CompatCLIPageAccountsForEveryCommandAndGolden)$' \
		TestDoctorSaysWhatWaitsInSyncAndHowToSettleIt \
		TestSharedDiagnosticsCarryNothingWritten \
		TestBackupAndLeavingSyncKeepEveryFile \
		TestDoctorNeverCreatesOrChangesAnything \
		TestCompatDoctorPrintsItsBaselineAndWritesNothing \
		TestReflectionRecordedOnDateFindsSavedEntry \
		TestEveryCommandAndFlagIsAccountedFor \
		TestCompatWritePathInventoryNamesEveryMutatingFile \
		TestCompatContractPagesExistAndAreLinked \
		TestCompatCLIPageAccountsForEveryCommandAndGolden

	# The bridge the phone plans, diagnoses, exports, and leaves through.
	run_go_suite bridge-recovery ./mobile/senecabridge/ \
		'^Test(RemindersArePlannedFromWhatThisDeviceHolds|DiagnosticsRecoveryAndLeavingThroughTheBridge|TheBridgeVersionIsAClosedSet|TheContractPageListsEveryOperationAndCode|EveryRequestIsHeldToTheEnvelope|TheBridgeLinksOnlyTheEngine)$' \
		TestRemindersArePlannedFromWhatThisDeviceHolds \
		TestDiagnosticsRecoveryAndLeavingThroughTheBridge \
		TestTheBridgeVersionIsAClosedSet \
		TestTheContractPageListsEveryOperationAndCode \
		TestEveryRequestIsHeldToTheEnvelope \
		TestTheBridgeLinksOnlyTheEngine

	ios/scripts/build-bridge.sh || fail "the bridge XCFramework did not build"

	# The app's model over the bound engine, hosted in the app on a
	# simulator: reminders planned again at every moment that can change
	# them and never in the way of writing; diagnostics with nothing written;
	# an export whole or not at all; an unavailable folder; a new iPhone, a
	# copy restored from another, and disconnecting.
	ios/scripts/test.sh "$work_dir/suite" \
		'ReminderTests/testRemindersAreOffUntilTurnedOnAndFollowTheDesktopSchedule()' \
		'ReminderTests/testASaveAForegroundAndAClockChangeEachPlanAgainAndAWrittenDayIsSilent()' \
		'ReminderTests/testAReflectionFromTheMacSilencesTheEveningOnlyOnceItIsImported()' \
		'ReminderTests/testDeniedPermissionNeverStopsWritingAndIsSaidSo()' \
		'ReminderTests/testAJournalThatCannotBePlannedKeepsWhatWasScheduled()' \
		'RecoveryTests/testDiagnosticsShareNothingWritten()' \
		'RecoveryTests/testARecoveryExportHoldsEveryWordAndNoIdentityAndSurvivesAnInterruption()' \
		'RecoveryTests/testAnUnavailableFolderIsNamedAndNeverStopsWriting()' \
		'RecoveryTests/testANewIPhoneJoinsAsItselfAndACopyRestoredFromAnotherRejoinsKeepingEveryWord()' \
		'RecoveryTests/testDisconnectingKeepsEveryWordAndForgetsTheFolder()' \
		'ExchangeTests/testAnUnreachableFolderOrARevokedGrantNeverStopsWriting()' \
		'ExchangeLifecycleTests/testAFolderThatCannotBeReachedIsSaidSoAndNeverStopsWriting()' \
		'BridgeOperationsTests/testTheLinkedEngineSpeaksTheBridgeTheseModelsWereWrittenFor()' \
		'DailyWorkflowTests/testADraftSurvivesTerminationAndIsClearedOnlyByASaveOrADiscard()' ||
		fail "the simulator suite did not hold"

	# The screens, driven from outside on a simulator.
	SENECA_UITEST_ONLY="RecoveryUITests DailyWorkflowUITests/testWritingSurvivesForcedTermination" \
		ios/scripts/uitest.sh "$work_dir/uitest" \
		'RecoveryUITests/testRemindersAreOptInAndScheduledAroundWhatIsWritten()' \
		'RecoveryUITests/testDeniedNotificationsAreSaidSoAndNeverStopWriting()' \
		'RecoveryUITests/testDiagnosticsShareNothingWrittenAndTheJournalExportsWhole()' \
		'DailyWorkflowUITests/testWritingSurvivesForcedTermination()' ||
		fail "the UI tests did not hold"

	# Delivery itself: iOS on a physical iPhone, built from these sources.
	if [ "${SENECA_MOBILE_DEVICE:-}" = live ]; then
		ios/scripts/notify-evidence.sh "$work_dir/notify-device" || fail "delivery on the iPhone did not hold"
		python3 -I ios/scripts/notify_evidence.py check "$work_dir/notify-device/notify.json" ||
			fail "the iPhone's delivery record does not hold"
	else
		[ -s "$notify_evidence" ] || fail "no real-device evidence at $notify_evidence; record it with \`just ios-notify-evidence\`"
		python3 -I ios/scripts/notify_evidence.py check "$notify_evidence" ||
			fail "the real-device evidence does not hold for the current sources; record it again with \`just ios-notify-evidence\`"
	fi

	note "session 12 verified"
}

# Session 13: the release gate and the personal rollout.
verify_session_13() {
	[ "$(uname -s)" = Darwin ] || fail "session 13 qualifies an iPhone app; it needs macOS with Xcode"
	require_tools go git xcrun xcodebuild python3 shasum
	xcrun simctl help >/dev/null 2>&1 || fail "required tool not found: simctl"
	xcrun devicectl --version >/dev/null 2>&1 || fail "required tool not found: devicectl"
	require_files \
		docs/mobile/qualification.md docs/mobile/recovery.md docs/sync.md README.md ios/README.md \
		scripts/qualification.py release_qualification_test.go \
		ios/scripts/evidence.py ios/scripts/sync_evidence.py ios/scripts/notify_evidence.py

	# The procedure is written down, every part of it.
	local section
	for section in '## What qualifies a release' '## The suites' '## The iPhone build' '## Device checks' \
		'## The multi-day trial' '## Defects' '## Rehearsing the rollback' '## Rolling out the journal' \
		'## Rolling back' '## Where things stand'; do
		grep -Fqx -- "$section" docs/mobile/qualification.md || fail "docs/mobile/qualification.md has no section $section"
	done
	local check
	for check in $(python3 -I scripts/qualification.py checks | awk '{print $1}'); do
		grep -Fq -- "### $check" docs/mobile/qualification.md || fail "docs/mobile/qualification.md does not describe the device check $check"
	done

	# What only the author and the iPhone can do, first: every record made
	# from these sources on a physical iPhone, or a list of what is missing.
	local source_fp bridge_fp expected=mobile/testdata/roundtrip/expected.json gaps=0
	source_fp=$(ios/scripts/source-fingerprint.sh) || fail "the source fingerprint could not be computed"
	bridge_fp=$(ios/scripts/build-bridge.sh --fingerprint) || fail "the bridge fingerprint could not be computed"
	if [ "${SENECA_MOBILE_SUITES:-}" = live ]; then
		export SENECA_QUALIFICATION_SUITES="$work_dir/suites.json"
		python3 -I scripts/qualification.py suites || fail "the simulator suites did not hold"
	fi
	python3 -I scripts/qualification.py check || gaps=1
	python3 -I ios/scripts/evidence.py check docs/mobile/evidence/session-8-device-roundtrip.json device \
		"$expected" "$source_fp" "$bridge_fp" || {
		note "the round trip on the iPhone has to be recorded again from these sources: just ios-device-evidence"
		gaps=1
	}
	python3 -I ios/scripts/sync_evidence.py check docs/mobile/evidence/session-10-device-sync.json device || {
		note "the exchange through iCloud Drive has to be recorded again from these sources: just ios-sync-evidence"
		gaps=1
	}
	python3 -I ios/scripts/notify_evidence.py check docs/mobile/evidence/session-12-device-notification.json || {
		note "reminder delivery on the iPhone has to be recorded again from these sources: just ios-notify-evidence"
		gaps=1
	}
	[ "$gaps" -eq 0 ] || fail "session 13 is not qualified: the steps above are not done for these sources (docs/mobile/qualification.md)"

	# Everything that runs without the iPhone, run now.
	go vet ./... || fail "go vet failed"
	local log="$work_dir/go-test.log"
	note "running the whole Go suite"
	go test -count=1 -v ./... >"$log" 2>&1 || {
		grep -E -- '^(--- FAIL|FAIL|panic)' "$log" >&2 || tail -40 "$log" >&2
		fail "the Go suite failed"
	}
	if grep -Eq -- '--- SKIP:' "$log"; then
		grep -E -- '--- SKIP:' "$log" >&2
		fail "a Go test was skipped"
	fi
	note "go: $(grep -Ec -- '--- PASS:' "$log") tests and subtests passed, none skipped"
	note "running the Go suite under the race detector"
	go test -count=1 -race ./... >"$work_dir/go-race.log" 2>&1 || {
		grep -E -- '^(--- FAIL|FAIL|WARNING: DATA RACE)' "$work_dir/go-race.log" >&2 || tail -40 "$work_dir/go-race.log" >&2
		fail "the race detector failed"
	}

	# The release's own: a journal several times the author's crossing both
	# ways intact and in time, and the rollback in the order it is documented.
	run_go_suite release-qualification . \
		'^Test(ARepresentativeJournalCrossesBetweenTheMacAndThePhoneIntact|TheDocumentedRollbackKeepsEveryWordAndALocalWorkflow)$' \
		TestARepresentativeJournalCrossesBetweenTheMacAndThePhoneIntact \
		TestTheDocumentedRollbackKeepsEveryWordAndALocalWorkflow

	ios/scripts/build-bridge.sh || fail "the bridge XCFramework did not build"
	ios/scripts/test.sh "$work_dir/hosted" || fail "the hosted Swift suite did not hold"

	note "session 13 verified"
}

# Session 14: the iOS 26 baseline and the vendored capture package.
verify_session_14() {
	[ "$(uname -s)" = Darwin ] || fail "session 14 builds an iPhone app; it needs macOS with Xcode"
	require_tools go xcrun xcodebuild python3 shasum
	xcrun simctl help >/dev/null 2>&1 || fail "required tool not found: simctl"
	local package=ios/Packages/Capture
	require_files \
		ios/project.yml ios/SenecaEngine/Package.swift ios/scripts/build-bridge.sh ios/scripts/simulator.sh \
		ios/scripts/test.sh ios/scripts/capture-test.sh ios/README.md \
		"$package/Package.swift" "$package/PROVENANCE.md"

	# iOS 26 everywhere the deployment target is set: the app, the engine
	# package, the capture package, and the bridge's iOS slices.
	grep -Eq '^    iOS: "26\.0"$' ios/project.yml || fail "ios/project.yml does not deploy to iOS 26.0"
	grep -Eq '^    IPHONEOS_DEPLOYMENT_TARGET: "26\.0"$' ios/project.yml ||
		fail "ios/project.yml does not set IPHONEOS_DEPLOYMENT_TARGET to 26.0"
	if grep -nE 'DEPLOYMENT_TARGET|iOS: "' ios/project.yml | grep -v '"26\.0"'; then
		fail "ios/project.yml sets another deployment target"
	fi
	grep -Fq 'platforms: [.iOS("26.0")]' ios/SenecaEngine/Package.swift ||
		fail "ios/SenecaEngine/Package.swift does not target iOS 26.0"
	grep -Fq 'platforms: [.iOS("26.0"), .macOS("26.0")]' "$package/Package.swift" ||
		fail "$package/Package.swift does not target iOS 26.0"
	grep -qx 'ios_version=26.0' ios/scripts/build-bridge.sh || fail "the bridge is not built for iOS 26.0"
	! grep -rnE 'iOS 18|\.v18|"18\.0"' ios/project.yml ios/SenecaEngine/Package.swift ios/scripts ios/README.md ||
		fail "an iOS 18 baseline is still stated"

	# The package is wired into the app, from its path, and depends on nothing
	# outside Apple's SDKs.
	grep -Fq 'path: Packages/Capture' ios/project.yml || fail "ios/project.yml does not declare the capture package"
	local product
	for product in CaptureCore CaptureApple; do
		grep -Fq "product: $product" ios/project.yml || fail "the app does not link $product"
	done
	! grep -nF '.package(' "$package/Package.swift" || fail "the capture package depends on another package"
	! grep -rnE '^(@testable )?import (WorkCore|CapturePresentation|BackgroundTasks|FoundationModels)$' \
		"$package/Sources" "$package/Tests" || fail "the capture package imports what it was vendored without"

	# Provenance: where the package came from is written down.
	[ -s "$package/PROVENANCE.md" ] || fail "$package/PROVENANCE.md is missing"

	# Still offline by construction, the vendored code included. Speech models
	# are downloaded by iOS itself (AssetInventory), not by the app.
	local networked
	networked=$(go list -deps ./mobile/senecabridge | grep -E '^net(/|$)' || true)
	[ -z "$networked" ] || fail "the bridge links network packages: $networked"
	networked=$(grep -rnE 'URLSession|URLRequest|import Network|NWConnection|CFNetwork' \
		ios/SenecaMobile ios/SenecaEngine/Sources "$package/Sources" || true)
	[ -z "$networked" ] || fail "the app reaches for the network: $networked"

	# The package's own tests, on an iOS simulator: the engine and its
	# recovery, the file store, the transcriber chain, the simulated devices,
	# and the real recorder adapter and fragmented writer.
	ios/scripts/capture-test.sh "$work_dir/capture" \
		'CaptureRecoveryTests/interruptedRecordingIsRecovered()' \
		'CaptureRecoveryTests/orphanAudioIsAdopted()' \
		'CaptureRecoveryTests/unplayablePartialIsFlagged()' \
		'CaptureUnlockTests/interruptionWhileLockedFinishesAfterUnlock()' \
		'CaptureUnlockTests/launchWhileLockedTouchesNothing()' \
		'AudioCaptureEngineTests/happyPath()' \
		'AudioCaptureEngineTests/interruptionRetainsAudio()' \
		'AudioCaptureEngineTests/stopWithUndecodableAudio()' \
		'CaptureAudioFileStoreTests/finalizePromotes()' \
		'CaptureAudioFileStoreTests/zeroByteRefused()' \
		'TranscriberChainTests/fallsBackOnUnsupportedLocale()' \
		'TranscriberChainTests/cancellationPropagates()' \
		'LocaleMatcherTests/regionDroppedFirst()' \
		'SimulatedDevicesTests/restartRecoveryFixture()' \
		'AudioRecorderAdapterTests/standardSettings()' \
		'AudioRecorderAdapterTests/unplayableFileThrows()' \
		'FragmentedAudioFileWriterTests/normalFinish()' \
		'FragmentedAudioFileWriterTests/abandonedAfterFragments()' \
		'FragmentedAudioFileWriterTests/abandonedBeforeFirstFragment()' ||
		fail "the capture package's tests did not hold"
	ran_on_ios_26 "$work_dir/capture/summary.json"

	# The whole Go suite, then the bridge and the hosted Swift suite, which
	# builds the app with the capture package linked.
	go vet ./... || fail "go vet failed"
	local log="$work_dir/go-test.log"
	note "running the whole Go suite"
	go test -count=1 -v ./... >"$log" 2>&1 || {
		grep -E -- '^(--- FAIL|FAIL|panic)' "$log" >&2 || tail -40 "$log" >&2
		fail "the Go suite failed"
	}
	if grep -Eq -- '--- SKIP:' "$log"; then
		grep -E -- '--- SKIP:' "$log" >&2
		fail "a Go test was skipped"
	fi
	note "go: $(grep -Ec -- '--- PASS:' "$log") tests and subtests passed, none skipped"

	ios/scripts/build-bridge.sh || fail "the bridge XCFramework did not build"
	ios/scripts/test.sh "$work_dir/hosted" || fail "the hosted Swift suite did not hold"
	ran_on_ios_26 "$work_dir/hosted/summary.json"

	# The project as generated, not only as written, deploys to iOS 26.
	local target
	target=$(xcodebuild -project ios/SenecaMobile.xcodeproj -target SenecaMobile -showBuildSettings 2>/dev/null |
		awk -F' = ' '$1 ~ /^ *IPHONEOS_DEPLOYMENT_TARGET$/ { print $2; exit }')
	[ "$target" = 26.0 ] || fail "the generated app target deploys to iOS '${target}', not 26.0"

	note "session 14 verified"
}

# Session 15: recording and transcription in the app.
verify_session_15() {
	[ "$(uname -s)" = Darwin ] || fail "session 15 records on an iPhone app; it needs macOS with Xcode"
	require_tools go xcrun xcodebuild python3 shasum plutil
	xcrun simctl help >/dev/null 2>&1 || fail "required tool not found: simctl"
	local package=ios/Packages/Capture voice=ios/SenecaMobile/Voice
	require_files \
		ios/project.yml ios/SenecaMobile/Info.plist ios/SenecaMobile/AppPaths.swift \
		"$voice/SpokenTakes.swift" "$voice/VoiceCapture.swift" "$voice/VoiceDevices.swift" "$voice/RecordView.swift" \
		ios/SenecaMobileTests/VoiceCaptureTests.swift ios/SenecaMobileUITests/VoiceUITests.swift \
		"$package/PROVENANCE.md" "$package/Tests/CaptureCoreTests/HostLayoutTests.swift" \
		docs/mobile/write-path-inventory.md

	# The microphone is asked for with a reason, and a take keeps recording
	# when the screen locks.
	grep -Eq '^        INFOPLIST_KEY_NSMicrophoneUsageDescription: ".+"$' ios/project.yml ||
		fail "ios/project.yml gives no microphone usage description"
	grep -Fq 'INFOPLIST_FILE: SenecaMobile/Info.plist' ios/project.yml || fail "ios/project.yml does not merge SenecaMobile/Info.plist"
	[ "$(plutil -extract UIBackgroundModes.0 raw ios/SenecaMobile/Info.plist 2>/dev/null)" = audio ] ||
		fail "ios/SenecaMobile/Info.plist does not declare the audio background mode"

	# The takes live beside the drafts, outside the journal, and the record
	# flow is reachable from the form and from Today.
	grep -Fq 'appendingPathComponent("audio", isDirectory: true)' ios/SenecaMobile/AppPaths.swift ||
		fail "AppPaths has no audio directory"
	grep -Fq 'accessibilityIdentifier("speak-instead")' ios/SenecaMobile/ReflectionForm.swift ||
		fail "the form's toolbar has no way to speak instead"
	grep -Fq 'accessibilityIdentifier("speak-today")' ios/SenecaMobile/TodayView.swift ||
		fail "Today has no way to speak the day"
	grep -Fq 'accessibilityIdentifier("prepare-speech-model")' "$voice/RecordView.swift" ||
		fail "the settings have no way to prepare the speech model"

	# The vendored change is recorded, and the simulated devices are compiled
	# into the hosted suite rather than linked into the app.
	grep -Fq '`Sources/CaptureCore/AudioFileStore.swift` | Seneca session 5' "$package/PROVENANCE.md" ||
		fail "$package/PROVENANCE.md does not record the AudioFileStore change"
	! grep -nF 'product: CaptureTestSupport' ios/project.yml || fail "a target links CaptureTestSupport"
	! grep -rnE '^import CaptureTestSupport$' ios/SenecaMobile || fail "the app imports the test support"

	# Still offline by construction: no bridge or engine change, and the app
	# and the vendored code reach for no network.
	local networked
	networked=$(go list -deps ./mobile/senecabridge | grep -E '^net(/|$)' || true)
	[ -z "$networked" ] || fail "the bridge links network packages: $networked"
	networked=$(grep -rnE 'URLSession|URLRequest|import Network|NWConnection|CFNetwork' \
		ios/SenecaMobile ios/SenecaEngine/Sources "$package/Sources" || true)
	[ -z "$networked" ] || fail "the app reaches for the network: $networked"

	# The package's own tests, the host layout among them.
	ios/scripts/capture-test.sh "$work_dir/capture" \
		'HostLayoutTests/partialBesideHostName()' \
		'HostLayoutTests/hostLayoutFinalizes()' \
		'HostLayoutTests/hostLayoutRefuses()' \
		'CaptureAudioFileStoreTests/finalizePromotes()' \
		'CaptureRecoveryTests/interruptedRecordingIsRecovered()' \
		'TranscriberChainTests/fallsBackOnUnsupportedLocale()' \
		'AudioRecorderAdapterTests/unplayableFileThrows()' \
		'FragmentedAudioFileWriterTests/abandonedAfterFragments()' ||
		fail "the capture package's tests did not hold"
	ran_on_ios_26 "$work_dir/capture/summary.json"

	# The whole hosted suite, the voice tests named: a take recorded to a
	# finalized file with its sidecar and nothing in the journal, a refused
	# microphone, an interruption, the dictation fallback, a failed take kept
	# for retry, the speech model never waited for, and a partial recovered
	# at launch and offered for transcription.
	ios/scripts/build-bridge.sh || fail "the bridge XCFramework did not build"
	ios/scripts/test.sh "$work_dir/hosted" \
		'VoiceCaptureTests/testATakeIsRecordedIntoTheDayAndRenamedWhenItStopsWithItsSidecarAndNothingInTheJournal()' \
		'VoiceCaptureTests/testARefusedMicrophoneIsSaidSoAndWritesNothing()' \
		'VoiceCaptureTests/testAnInterruptionKeepsWhatWasCapturedAndTranscribesIt()' \
		'VoiceCaptureTests/testTheDictationTranscriberTakesOverWhenTheSpeechTranscriberCannotServeTheLanguage()' \
		'VoiceCaptureTests/testAFailedTranscriptionStaysListedAndIsTriedAgain()' \
		'VoiceCaptureTests/testAMissingSpeechModelIsNeverWaitedForAndTheTakeIsTranscribedOnceItArrives()' \
		'VoiceCaptureTests/testAPartialFoundAtLaunchIsFinalizedAndOfferedForTranscription()' \
		'VoiceCaptureTests/testNothingIsRecoveredWhileTheIPhoneIsLocked()' \
		'VoiceCaptureTests/testTheSidecarIsReplacedWholeAndLeavesNoTemporaryFiles()' \
		'DailyWorkflowTests/testADraftSurvivesTerminationAndIsClearedOnlyByASaveOrADiscard()' ||
		fail "the hosted Swift suite did not hold"
	ran_on_ios_26 "$work_dir/hosted/summary.json"

	# The app as built says what it needs.
	local plist
	plist=$(find ios/.cache/SimDerivedData/Build/Products/Debug-iphonesimulator/Seneca.app -maxdepth 1 -name Info.plist | head -1)
	[ -n "$plist" ] || fail "the simulator build has no Info.plist"
	plutil -extract NSMicrophoneUsageDescription raw "$plist" >/dev/null || fail "the built app has no microphone usage description"
	[ "$(plutil -extract UIBackgroundModes.0 raw "$plist")" = audio ] || fail "the built app has no audio background mode"

	# Every screen, driven from outside: the refused microphone and a staged
	# transcript among them, and every earlier screen around the new buttons.
	ios/scripts/uitest.sh "$work_dir/uitest" \
		'VoiceUITests/testARefusedMicrophoneIsExplainedWithTheWayToSettings()' \
		'VoiceUITests/testASpokenTakeIsTranscribedAndKeptOutsideTheJournal()' \
		'DailyWorkflowUITests/testWritingSurvivesForcedTermination()' ||
		fail "the UI tests did not hold"
	ran_on_ios_26 "$work_dir/uitest/summary.json"

	note "session 15 verified"
}

# Session 16: proposed answers, confirmation, and save.
verify_session_16() {
	[ "$(uname -s)" = Darwin ] || fail "session 16 runs on an iPhone app; it needs macOS with Xcode"
	require_tools go xcrun xcodebuild python3 shasum
	xcrun simctl help >/dev/null 2>&1 || fail "required tool not found: simctl"
	local voice=ios/SenecaMobile/Voice
	require_files \
		"$voice/AnswerSplitter.swift" "$voice/AnswerProposal.swift" "$voice/ProposalView.swift" \
		"$voice/RecordView.swift" "$voice/VoiceCapture.swift" "$voice/SpokenTakes.swift" \
		ios/SenecaMobile/ReflectionForm.swift ios/SenecaMobile/TodayView.swift \
		ios/SenecaMobileTests/ProposalTests.swift ios/SenecaMobileUITests/VoiceUITests.swift

	# The split: Foundation Models with the permissive guardrails, held to a
	# schema built at runtime from the configured questions, not a fixed one.
	grep -Fq 'guardrails: .permissiveContentTransformations' "$voice/AnswerSplitter.swift" ||
		fail "the splitter does not use the permissive content transformation guardrails"
	grep -Fq 'DynamicGenerationSchema(name: "reflection_answers"' "$voice/AnswerSplitter.swift" ||
		fail "the splitter's schema is not built from the questions"
	! grep -rnF '@Generable' ios/SenecaMobile || fail "the app splits with a fixed @Generable shape"

	# Confirmation before anything reaches the draft, the transcript to copy
	# when there is no proposal, the ordinary save, and the sidecar marked.
	grep -Fq 'accessibilityIdentifier("record-propose")' "$voice/RecordView.swift" ||
		fail "the record screen offers no proposal"
	local id
	for id in proposal-accept proposal-fallback proposal-unassigned proposal-show-transcript copy-transcript- proposal-merge-; do
		grep -Fq "accessibilityIdentifier(\"$id" "$voice/ProposalView.swift" || fail "the confirmation screen has no $id"
	done
	grep -Fq 'journal.voice?.markAnswersSaved(on: date)' ios/SenecaMobile/ReflectionForm.swift ||
		fail "the form's save does not mark the take saved"
	grep -Fq 'session.setAnswer(index, answer)' "$voice/AnswerProposal.swift" ||
		fail "accepted answers do not go through the session"

	# No bridge, engine or Markdown change: the app alone, offline.
	! grep -rnE '^import FoundationModels$' ios/SenecaEngine mobile || fail "the engine or bridge reaches for Foundation Models"
	local networked
	networked=$(go list -deps ./mobile/senecabridge | grep -E '^net(/|$)' || true)
	[ -z "$networked" ] || fail "the bridge links network packages: $networked"
	networked=$(grep -rnE 'URLSession|URLRequest|import Network|NWConnection|CFNetwork' \
		ios/SenecaMobile ios/SenecaEngine/Sources ios/Packages/Capture/Sources || true)
	[ -z "$networked" ] || fail "the app reaches for the network: $networked"

	# The whole hosted suite, the proposal tests named: the split shaped for
	# one to five questions, the unavailable and refusal paths, the merge
	# rules, and a spoken entry byte-identical to a typed one through the
	# bridge round trip.
	ios/scripts/build-bridge.sh || fail "the bridge XCFramework did not build"
	ios/scripts/test.sh "$work_dir/hosted" \
		'ProposalTests/testTheSplitIsShapedForOneToFiveQuestions()' \
		'ProposalTests/testAnUnavailableModelShowsTheTranscriptToCopyIntoEachQuestion()' \
		'ProposalTests/testARefusalOrATakeTooLongForTheModelFallsBackToTheTranscript()' \
		'ProposalTests/testProposedAnswersMergeIntoTheDraftWithoutOverwritingTypedText()' \
		'ProposalTests/testASpokenEntryIsByteIdenticalToATypedEntryWithTheSameAnswers()' \
		'RoundTripTests/testSavesAReflectionThroughGoAndReopensItAsTheDesktopWroteIt()' \
		'VoiceCaptureTests/testATakeIsRecordedIntoTheDayAndRenamedWhenItStopsWithItsSidecarAndNothingInTheJournal()' \
		'DailyWorkflowTests/testADraftSurvivesTerminationAndIsClearedOnlyByASaveOrADiscard()' ||
		fail "the hosted Swift suite did not hold"
	ran_on_ios_26 "$work_dir/hosted/summary.json"

	# The screens this session changed, driven from outside: the speaking
	# flow with its confirmation and its unavailable-model path, the daily
	# form around it, and the accessibility audits.
	SENECA_UITEST_ONLY="VoiceUITests DailyWorkflowUITests AccessibilityUITests" ios/scripts/uitest.sh "$work_dir/uitest" \
		'VoiceUITests/testProposedAnswersAreConfirmedBeforeTheFormSavesThem()' \
		'VoiceUITests/testAnUnavailableModelOffersTheTranscriptToCopyIntoEachQuestion()' \
		'VoiceUITests/testASpokenTakeIsTranscribedAndKeptOutsideTheJournal()' \
		'DailyWorkflowUITests/testWritingSurvivesForcedTermination()' ||
		fail "the UI tests did not hold"
	ran_on_ios_26 "$work_dir/uitest/summary.json"

	note "session 16 verified"
}

verify_session_17() {
	[ "$(uname -s)" = Darwin ] || fail "session 17 runs on an iPhone app; it needs macOS with Xcode"
	require_tools go xcrun xcodebuild python3 shasum
	xcrun simctl help >/dev/null 2>&1 || fail "required tool not found: simctl"
	local voice=ios/SenecaMobile/Voice
	require_files \
		"$voice/VoiceSettingsView.swift" "$voice/AudioKit.swift" "$voice/VoiceCapture.swift" "$voice/SpokenTakes.swift" \
		ios/SenecaMobile/SettingsView.swift ios/SenecaMobile/RecoveryView.swift ios/SenecaMobile/EntryView.swift \
		ios/SenecaMobile/HistoryView.swift ios/SenecaMobile/JournalModel.swift \
		ios/SenecaMobileTests/VoiceRetentionTests.swift ios/SenecaMobileUITests/VoiceUITests.swift \
		docs/mobile/write-path-inventory.md docs/mobile/recovery.md ios/README.md

	# The Voice section beside Reminders, the retention choice, deletion per
	# take and in bulk from history, the kit's audio folder and its restore.
	grep -Fq 'accessibilityIdentifier("voice-link")' ios/SenecaMobile/SettingsView.swift ||
		fail "Settings has no Voice section"
	local id
	for id in voice-retention voice-recordings take-delete- recordings-delete-all recordings-delete-saved; do
		grep -Fq "accessibilityIdentifier(\"$id" "$voice/VoiceSettingsView.swift" || fail "the voice settings have no $id"
	done
	grep -Fq 'TakeRow(voice: voice, take: take)' ios/SenecaMobile/EntryView.swift || fail "a day in history cannot delete its recordings"
	grep -Fq 'accessibilityIdentifier("history-recordings")' ios/SenecaMobile/HistoryView.swift || fail "history does not list the recordings"
	for id in recovery-include-audio recovery-restore-audio; do
		grep -Fq "accessibilityIdentifier(\"$id\")" ios/SenecaMobile/RecoveryView.swift || fail "recovery has no $id"
	done
	grep -Fq 'try await engine.exportRecovery(' ios/SenecaMobile/JournalModel.swift ||
		fail "the kit does not hold the engine's own export"
	! grep -rnF 'audio' mobile/senecabridge/recovery_ops.go engine/recovery_export.go ||
		fail "the engine's export knows about the audio; it must stay byte-identical to seneca backup"

	# The app's own files are documented where the write paths are.
	grep -Fq 'Seneca/audio' docs/mobile/write-path-inventory.md || fail "the write-path inventory does not list the audio"
	grep -Fq 'audio/' docs/mobile/recovery.md || fail "the recovery procedures do not name the audio folder"
	grep -Fq 'Delete After Saving' ios/README.md || fail "the iOS README does not describe retention"

	# No bridge, engine or Markdown change, and offline.
	local networked
	networked=$(grep -rnE 'URLSession|URLRequest|import Network|NWConnection|CFNetwork' \
		ios/SenecaMobile ios/SenecaEngine/Sources ios/Packages/Capture/Sources || true)
	[ -z "$networked" ] || fail "the app reaches for the network: $networked"

	# The whole hosted suite, the retention, deletion, kit and diagnostics
	# tests named, with the recovery export and the capture tests around them.
	ios/scripts/build-bridge.sh || fail "the bridge XCFramework did not build"
	ios/scripts/test.sh "$work_dir/hosted" \
		'VoiceRetentionTests/testTakesAreKeptOrDeletedOnceTheirAnswersAreSavedAsTheAuthorChooses()' \
		'VoiceRetentionTests/testDeletingATakeRemovesItsAudioAndSidecarAndNothingElse()' \
		'VoiceRetentionTests/testARecoveryKitCarriesTheAudioBesideAnUnchangedExportAndPutsItBack()' \
		'VoiceRetentionTests/testDiagnosticsCountTheAudioWithoutAWordOfIt()' \
		'RecoveryTests/testARecoveryExportHoldsEveryWordAndNoIdentityAndSurvivesAnInterruption()' \
		'RecoveryTests/testDiagnosticsShareNothingWritten()' \
		'VoiceCaptureTests/testATakeIsRecordedIntoTheDayAndRenamedWhenItStopsWithItsSidecarAndNothingInTheJournal()' \
		'ProposalTests/testASpokenEntryIsByteIdenticalToATypedEntryWithTheSameAnswers()' ||
		fail "the hosted Swift suite did not hold"
	ran_on_ios_26 "$work_dir/hosted/summary.json"

	# The screens this session changed, driven from outside: speaking and
	# its settings, history, recovery and diagnostics, and the audits.
	SENECA_UITEST_ONLY="VoiceUITests RecoveryUITests BrowsingUITests AccessibilityUITests" ios/scripts/uitest.sh "$work_dir/uitest" \
		'VoiceUITests/testRecordingsAreDeletedFromHistoryOneAtATimeOrAllWithNothingElse()' \
		'VoiceUITests/testVoiceSettingsCanDeleteATakeOnceItsAnswersAreSaved()' \
		'VoiceUITests/testARecoveryKitCanCarryTheRecordingsAndDiagnosticsOnlyCountThem()' \
		'VoiceUITests/testProposedAnswersAreConfirmedBeforeTheFormSavesThem()' \
		'RecoveryUITests/testDiagnosticsShareNothingWrittenAndTheJournalExportsWhole()' ||
		fail "the UI tests did not hold"
	ran_on_ios_26 "$work_dir/uitest/summary.json"

	note "session 17 verified"
}

# Session 18: voice capture and Apple's models on the author's iPhone.
verify_session_18() {
	[ "$(uname -s)" = Darwin ] || fail "session 18 qualifies an iPhone app; it needs macOS with Xcode"
	require_tools go xcrun xcodebuild python3 shasum
	xcrun simctl help >/dev/null 2>&1 || fail "required tool not found: simctl"
	xcrun devicectl --version >/dev/null 2>&1 || fail "required tool not found: devicectl"
	local debug=ios/SenecaMobile/Debug
	require_files \
		"$debug/ModelEvaluation.swift" "$debug/ModelEvaluationView.swift" \
		scripts/apple-models/run.sh scripts/apple-models/evaluation.py scripts/apple-models/testdata/reflections.md \
		scripts/voice_qualification.py docs/product/apple-models.md docs/mobile/qualification.md \
		ios/SenecaMobileTests/ModelEvaluationTests.swift ios/SenecaMobileUITests/ModelEvaluationUITests.swift

	# What only the author and the iPhone can do, first: every take, the
	# latency, the battery, and the evaluation, recorded from these sources
	# on a physical iPhone, or a list of what is missing.
	python3 -I scripts/voice_qualification.py check ||
		fail "session 18 is not qualified: record the steps above on the iPhone (docs/mobile/qualification.md, Voice on the iPhone)"

	# The evaluation screen is in debug builds only, and never near the journal.
	local file
	for file in "$debug/ModelEvaluation.swift" "$debug/ModelEvaluationView.swift"; do
		[ "$(head -1 "$file")" = '#if DEBUG' ] && [ "$(tail -1 "$file")" = '#endif' ] ||
			fail "$file is not wrapped in #if DEBUG"
		! grep -nE 'JournalModel|SenecaEngine|ReflectionSession|setAnswer|journalDirectory|reflections\.md' "$file" ||
			fail "$file reaches for the journal"
	done
	grep -B8 -A8 'ModelEvaluationView()' ios/SenecaMobile/SettingsView.swift | grep -Fq '#if DEBUG' ||
		fail "Settings offers the evaluation outside debug builds"
	grep -Fq 'com.apple.developer.private-cloud-compute' "$debug/ModelEvaluation.swift" ||
		fail "the screen does not ask the provisioning profile for Private Cloud Compute"
	local mode
	for mode in export push pull replay; do
		grep -Eq "^$mode\) " scripts/apple-models/run.sh || fail "run.sh has no $mode mode"
	done

	# The procedure and the measured numbers are written down.
	grep -Fqx '## Running it on the iPhone' docs/product/apple-models.md || fail "apple-models.md does not say how to run it on the iPhone"
	grep -Fqx '## Measured on the iPhone' docs/product/apple-models.md || fail "apple-models.md states no iPhone numbers"
	grep -Fqx '## Comparing blind against Opus' docs/product/apple-models.md || fail "apple-models.md has no blind comparison procedure"
	grep -Fqx '## Voice on the iPhone' docs/mobile/qualification.md || fail "qualification.md does not describe voice on the iPhone"
	python3 -I - <<'PY' || fail "docs/product/apple-models.md does not state what the evaluation record says"
import json, re
record = json.load(open("docs/mobile/evidence/session-18-model-evaluation.json"))
text = open("docs/product/apple-models.md", encoding="utf-8").read()
section = text.split("## Measured on the iPhone", 1)[1].split("\n## ", 1)[0]
model = (record.get("device") or {}).get("model") or ""
assert model and model in section, f"the iPhone section does not name the device {model}"
if record.get("private_cloud") != "available":
    assert re.search(r"(?i)entitlement[^.]*not been granted|not been granted[^.]*entitlement", section), \
        "Private Cloud Compute was not reached and the iPhone section does not say the entitlement has not been granted"
if record.get("on_device") != "available":
    assert record["on_device"] in section, f"the on-device model was {record['on_device']} and the iPhone section does not say so"
PY

	# The rig's export and replay, on a synthetic week, without an iPhone.
	python3 -I scripts/apple-models/evaluation.py selftest || fail "the rig's export and replay did not hold"

	# The app: the evaluation's run and the take timings in the hosted suite,
	# the screen and its neighbours from outside.
	ios/scripts/build-bridge.sh || fail "the bridge XCFramework did not build"
	ios/scripts/test.sh "$work_dir/hosted" \
		'ModelEvaluationTests/testThePromptsAreHeldToTheirFormatAndDigests()' \
		'ModelEvaluationTests/testEveryPromptRunsOnEveryReachableTargetAndOnlyTheResponsesAreWritten()' \
		'ModelEvaluationTests/testPrivateCloudComputeIsNeverCalledWithoutTheEntitlementAndAnIneligibleIPhoneSaysWhy()' \
		'ModelEvaluationTests/testTheReviewSchemaOffersOnlyThePromptsDatesAndANullFocusWhenNoneApplies()' \
		'ModelEvaluationTests/testAProvisioningProfileGrantsOnlyWhatItLists()' \
		'TakeTimingTests/testATakeKeepsWhenItStoppedAndHowLongItsAnswersTook()' \
		'TakeTimingTests/testAProposalTimesTheModelOnlyWhenItProposes()' \
		'VoiceCaptureTests/testATakeIsRecordedIntoTheDayAndRenamedWhenItStopsWithItsSidecarAndNothingInTheJournal()' \
		'ProposalTests/testASpokenEntryIsByteIdenticalToATypedEntryWithTheSameAnswers()' ||
		fail "the hosted Swift suite did not hold"
	ran_on_ios_26 "$work_dir/hosted/summary.json"
	SENECA_UITEST_ONLY="ModelEvaluationUITests VoiceUITests AccessibilityUITests" ios/scripts/uitest.sh "$work_dir/uitest" \
		'ModelEvaluationUITests/testTheEvaluationScreenReadsItsPromptsAndLeavesTheJournalAlone()' \
		'VoiceUITests/testProposedAnswersAreConfirmedBeforeTheFormSavesThem()' ||
		fail "the UI tests did not hold"
	ran_on_ios_26 "$work_dir/uitest/summary.json"

	go vet ./... || fail "go vet failed"
	note "session 18 verified"
}

# ran_on_ios_26 SUMMARY_JSON fails unless every device in an xcresult summary
# ran iOS 26 or later.
ran_on_ios_26() {
	python3 -I - "$1" <<'PY' || fail "$1: the suite did not run on iOS 26 or later"
import json, sys
devices = json.load(open(sys.argv[1])).get("devicesAndConfigurations") or []
versions = [d.get("device", {}).get("osVersion", "") for d in devices]
assert versions and all(int(v.split(".")[0]) >= 26 for v in versions), versions
print(f"verify_mobile_session: ran on iOS {', '.join(versions)}")
PY
}

session=${1:-}
case "$session" in
8) verify_session_8 ;;
9) verify_session_9 ;;
10) verify_session_10 ;;
11) verify_session_11 ;;
12) verify_session_12 ;;
13) verify_session_13 ;;
14) verify_session_14 ;;
15) verify_session_15 ;;
16) verify_session_16 ;;
17) verify_session_17 ;;
18) verify_session_18 ;;
"") fail "usage: $0 <session>" ;;
*) fail "no verification is defined for session $session" ;;
esac
