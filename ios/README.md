[← Seneca](../README.md)

# Seneca for iPhone

A SwiftUI app over the same Go engine the `seneca` command uses. The engine is bound with gomobile; the app reaches it through the [bridge](../docs/mobile/bridge.md) and nothing else.

## Layout

| Path | What it is |
| --- | --- |
| `../mobile/senecabridge/` | The bridge, in Go: one JSON request in, one response out. |
| `Packages/Capture/` | The Swift package that records and transcribes on device, vendored from an earlier app: [PROVENANCE.md](Packages/Capture/PROVENANCE.md). |
| `SenecaEngine/` | The Swift package: `Codable` models and the `SenecaEngine` actor. `Frameworks/SenecaBridgeCore.xcframework` is generated. |
| `SenecaMobile/` | The app. |
| `SenecaMobileTests/` | Tests hosted in the app, so they run its engine service inside its container. |
| `SenecaMobileUITests/` | Tests that drive the built app from outside, on a simulator, over a journal they stage. |
| `project.yml` | The XcodeGen spec. `SenecaMobile.xcodeproj` is generated from it. |
| `scripts/` | Everything the recipes below run. |
| `.cache/` | The built gomobile, XcodeGen, derived data, and results. Safe to delete. |

Nothing generated is committed. Edit `project.yml`, not the project.

## Requirements

- macOS with Xcode and an iOS 26 or later simulator runtime. The app's deployment target is iOS 26, which Foundation Models, `SpeechTranscriber`, and the vendored capture package need.
- Go, at the version `go.mod` names or later.
- `just`, to run the recipes. Each one is a script under `scripts/` that runs without it.

gomobile and XcodeGen are not installed by hand. gomobile is the version `go.mod` pins as a tool dependency and is built into `.cache/`. XcodeGen is the release `XCODEGEN_VERSION` pins, downloaded once and checked against the digest beside it.

## Recipes

Run these from the repository's `seneca` directory.

| Recipe | What it does |
| --- | --- |
| `just ios-bridge` | Binds the engine into the XCFramework, with a device slice and a simulator slice. Does nothing while the sources it was built from are unchanged. |
| `just ios-project` | Builds the bridge and generates the Xcode project. |
| `just ios-build` | Builds the app for the simulator. |
| `just ios-run` | Builds, boots a simulator, installs, and launches. |
| `just ios-test` | Runs the hosted suite on a simulator against the bound engine, then the round trip across a reinstall. |
| `just ios-capture-test` | Runs the capture package's own tests on a simulator. |
| `just ios-uitest` | Drives the app's screens on a simulator: drafts across a stop and relaunch, a stale save, a torn archive, the largest text size, labels, the keyboard, history, reviews, the focus, the questions, and conflicts. |
| `just ios-install TEAMID` | Builds for your iPhone, signed by your team, installs, and launches. |
| `just ios-testflight [--upload]` | Archives for the App Store and exports it; `--upload` sends it to App Store Connect for TestFlight. See [Distribution](#distribution). |
| `just ios-device-evidence` | Runs the round trip on your iPhone across a signed reinstall and records it. |
| `just mobile-verify 8` | The whole gate for the bridge and the shell. |
| `just mobile-verify 9` | The whole gate for the daily workflow and its drafts. Simulator only. |
| `just ios-sync-rehearsal` | Rehearses the end-to-end exchange between a simulator and this Mac over a plain folder. |
| `just ios-sync-evidence` | Runs the end-to-end exchange between your iPhone and this Mac through iCloud Drive and records it. |
| `just mobile-verify 10` | The whole gate for the iCloud transport, including the record of a real-device exchange. |
| `just ios-notify-evidence` | Has iOS deliver a reminder on your iPhone, and not deliver the next one after a save, and records it. |
| `just mobile-verify 12` | The whole gate for reminders, diagnostics, and recovery, including the record of real-device delivery. |
| `just mobile-qualify suites` | Runs the hosted suite, the UI suite, the round trip, and the sync rehearsal in full on a simulator and records them for the release. |
| `just mobile-qualify install` | Builds these sources for your iPhone and installs them for the release qualification; `just mobile-qualify status` says what remains. |
| `just mobile-verify 13` | The release gate: the Go suite with the race detector, the Swift suites, the device records of sessions 8, 10 and 12, and the [qualification](../docs/mobile/qualification.md) of the iPhone, the trial, the rollback, and the rollout. |
| `just mobile-verify 14` | The iOS 26 baseline and the capture package: the deployment target everywhere it is set, the package's provenance and its tests, the Go suite, and the hosted suite. Simulator only. |
| `just mobile-verify 15` | Recording and transcription in the app: the microphone and background-audio declarations, the capture package's tests, the hosted suite with the voice tests, and every UI test. Simulator only. |
| `just mobile-verify 17` | Voice settings, keeping and deleting recordings, the audio folder of a recovery kit, and the content-free audio diagnostics: the hosted suite and the UI tests of the screens involved. Simulator only. |
| `just mobile-verify 18` | Voice and Apple's models on the author's iPhone: the device records `just voice-qualify` keeps (docs/mobile/qualification.md), the debug-only Model Evaluation screen and its replay, then the hosted suite and the UI tests involved. |

`SENECA_IOS_SIMULATOR` picks the simulator by UDID. Without it, the first available iPhone on iOS 26 or later is used.

## Installing on your iPhone

Pair the iPhone with this Mac, turn on Developer Mode, and keep it unlocked. Your Apple ID must be signed in to Xcode so that it can make the provisioning profile.

```sh
just ios-install TEAMID
```

`TEAMID` is your developer team's ten-character identifier. It is needed once: later runs sign with the team of the previous build. `SENECA_IOS_DEVICE` picks the iPhone by UDID when more than one is paired.

Installing over an earlier build keeps the app's data, so the same command is the upgrade. The journal is `Documents/Journal/reflections.md` in the app's container, with the review archive beside it. Deleting the app deletes the journal.

## Distribution

```sh
SENECA_IOS_DEVELOPMENT_TEAM=TEAMID just ios-testflight           # the .ipa, to inspect
SENECA_IOS_DEVELOPMENT_TEAM=TEAMID just ios-testflight --upload  # to TestFlight
```

The archive is a Release build: the debug-only Model Evaluation screen is not in it, and the script refuses an archive that carries it, or that carries no privacy manifest. The build number is the UTC time, so each upload is newer than the last; `SENECA_IOS_BUILD_NUMBER` sets another. Uploading uses the Apple ID signed in to Xcode. It needs an app record for `io.sglavoie.seneca.mobile` in App Store Connect, made once by hand.

What App Store Connect asks, answered from the app:

- **Privacy nutrition label: Data Not Collected.** The journal, drafts, and recordings stay on the iPhone, or go to the exchange folder the author picks. `SenecaMobile/PrivacyInfo.xcprivacy` declares no collected data and no tracking, and gives Apple's reasons for the file-timestamp, disk-space, and user-defaults APIs.
- **Export compliance: exempt.** `ITSAppUsesNonExemptEncryption` is `NO`: only the encryption iOS provides, and hashing.

The privacy policy and support page App Review requires are in [`website/`](../website), published at <https://seneca.sglavoie.com> and linked from Settings. [App Store submission](../docs/app-store-submission.md) lists the rest.

## The app

On a journal nothing has been written to or set in yet, the app opens on onboarding: **Start a Journal** chooses a practice pack and offers its reminder time, then reminders, then sync; **I Keep Seneca on Another Device** goes straight to choosing the exchange folder, since that journal brings its own questions. Every step can be skipped and changed in Settings. Onboarding is offered once per installation (`Application Support/Seneca/onboarding.json`), and never over a journal that already holds writing, settings, an exchange, or a draft.

Settings, Practice lists the built-in packs and opens a `.seneca-pack.json`; one opened from AirDrop, Files, or Mail is shown the same way. See [Practice packs](../docs/product/practice-packs.md#where-it-is-used).

Three tabs: Today, History, and Reviews. Today leads to writing the day, to another day, to the focus, and to the settings. History lists every day, finds a phrase, and opens a day for editing. Reviews reads what the desktop wrote, and leads to **Follow-Through**: what became of the last 30 days' intentions, the wordings and words that return, and how long each focus stood ([Follow-through insights](../docs/insights.md)). A review, or a day in History, can be shared with another person: the review alone or with the entries of chosen days, previewed exactly, as Markdown or a PDF, through the share sheet ([Share packets](../docs/export.md#share-packets)).

To delete a saved reflection, tap **Delete Today’s Reflection** on Today, or open a day in History and tap **Delete Entry** (the trash button). Confirm the dated prompt to remove it from the journal and synced devices. Unsaved drafts and recordings are kept. If the entry changed since it was shown, deletion is refused; a sync conflict must be resolved first.

The writing form asks the journal's questions as plain text fields. Above them it shows what the engine says the day carries: the focus, and the intention being followed through. Hide Context, or Write Without Context when the archive cannot be read, shows the questions alone. Sections of an entry that no question names are shown under the questions to read, and a save keeps them as they are. No Markdown structure is shown or typed.

### Drafts

What is typed is written to `Application Support/Seneca/drafts/<date>.json` on every change, and again when the app leaves the foreground or the form closes. A draft holds the engine's draft it was written on (date, base revision, settings revision, questions, and the context shown), the answers, and the follow-through. It is device-local: outside the journal, never exchanged.

A draft is put back whenever its day is opened: after the form was dismissed, after the app was stopped, and after a save the journal refused. It is removed in two cases only: the journal took the writing, or you chose Discard Draft.

When the day or the questions changed in the journal after the writing began, the save is refused and the form says so. Every typed word stays; each answer follows its question by the question's text; an answer whose question is gone is kept and shown; and what the journal now holds is shown under the field it differs from. Saving again replaces the journal's answers to these questions.

## Sync

The journal is exchanged with a Mac through a folder in iCloud Drive, the one `seneca sync` uses there as its exchange directory. [iCloud transport](../docs/mobile/icloud-transport.md) is the design; this is the use of it.

1. On the Mac, put the exchange in iCloud Drive and enroll the journal: [docs/sync.md](../docs/sync.md).
2. On the iPhone, open Settings (the gear on Today), Sync, Choose Exchange Folder, and pick that folder in iCloud Drive.
3. Tap Join the Journal in This Folder. The Mac's journal arrives, and what was already written on the iPhone is kept.

A folder that carries no journal offers Start Syncing from This iPhone instead, which publishes the phone's journal for the Mac to join with `seneca sync enroll --join`.

The journal itself never leaves the app: each saved change is copied into the folder as immutable files, and the Mac's changes are read from it. The app exchanges when it launches, when it comes forward, after each save, and on pull to refresh, and tries once more when it leaves the screen if iOS allows.

Sync shows where the writing has got to, and Today shows it in one line:

| Shown | Means |
| --- | --- |
| Saved on this iPhone | In the journal here. True of every save, with or without a connection. |
| In the exchange folder | Written into the folder. |
| Uploaded to iCloud | Taken from the folder by iCloud. |
| Applied on *name* | In that device's own journal, by its own word. |

When the folder cannot be reached the app says so and keeps trying; writing goes on. When the folder is gone or access to it was taken back, choose it again. A day written differently on both devices keeps both versions. Today and Sync then say so, and Choose a Version shows the version both started from, this iPhone's, and the other device's: keep one, or start from one and edit the day with the other beside it. Until then the day reads as this iPhone wrote it, and writing for it is kept as a draft on this iPhone and saved once a version is chosen. A focus set differently on both is settled the same way. On the Mac, `seneca sync conflicts` lists the same choices.

## Speaking a reflection

"Speak Today’s Reflection" on Today and "Speak Instead" in the form's toolbar record a take on this iPhone. Microphone access is asked for the first time; if it is refused, the screen says so and opens Settings. A take is recorded as crash-safe fragmented m4a straight into `Application Support/Seneca/audio/<date>/<uuid>.partial.m4a` and renamed to `<uuid>.m4a` when it stops. Interruptions, route changes and the lock screen behave as the vendored recorder does (`Packages/Capture`). It is then transcribed on the iPhone in the language chosen under Settings › Voice, with `SpeechTranscriber` first and `DictationTranscriber` as the fallback. The sidecar `<uuid>.json` holds the transcript, locale, transcriber, duration, the audio's SHA-256, and when it was made, and is written whole.

The speech model can be fetched ahead of time from Settings, and is also requested when the first take starts. A take is never held up by a download that has not started: it waits as "Waiting for speech model" and is transcribed once the model arrives. A take whose transcription failed stays listed on Today until another try succeeds, and a transcribed take stays listed there until its answers are in a draft. Any take, from Today, the Recordings list, or its day in History, opens on its transcript and **Sort Into Your Questions**, which proposes its answers and, once they are confirmed, opens the day's form on them; discarding that draft means the take's answers were not accepted after all. A partial file found at launch is finalized and offered for transcription, but not while the phone is locked. Nothing is written inside the journal, and nothing is exchanged.

"Sort Into Your Questions" under a transcript asks Apple's on-device model (Foundation Models, with the `permissiveContentTransformations` guardrails) to file it under the questions the journal asks now. The schema is built at runtime from those questions: one answer per question, plus "unassigned"; the model keeps the speaker's words and language and removes fillers only. The confirmation screen shows each proposed answer editable, what was typed already beside it, what was set aside, and the raw transcript one tap away. "Fill Answers" puts them in the day's draft through the form's session: an empty field takes the proposal, and where both have text the typed answer stays unless the author chooses to replace it or add the proposal below. When the model is unavailable, refuses, or the take is too long for it, the screen says so and shows the transcript with "Copy Transcript Here" under each question instead. Saving is the form's ordinary save, so a spoken entry is byte for byte what typing the same answers writes, with no marker. The sidecar records `accepted_at` when answers go into the draft and `answers_saved` once the form saves the day.

Settings › Voice holds the transcription language, the speech model and **Prepare Speech Model**, and what is kept of a take: **Keep** (the default) leaves every recording on this iPhone until it is deleted; **Delete After Saving** deletes a take once the answers proposed from it are saved, and, when chosen, the takes whose answers are already saved. The choice is device-local, in `Application Support/Seneca/voice.json` beside the language. Recordings can be deleted one at a time from the day they belong to in History, or from the Recordings list (Settings › Voice, or History), one at a time, those with saved answers, or all. Deleting a take removes its `<uuid>.m4a` and its `<uuid>.json` and nothing else; a day directory left empty goes with them. A take being recorded or transcribed cannot be deleted.

**Export Journal…** in Diagnostics and Recovery can include the recordings ("Include Spoken Reflections"). The kit is then a directory holding the engine's export exactly as it is made alone and an `audio/` folder beside it: `<date>/<uuid>.m4a` and `<uuid>.json` as on the iPhone and `MANIFEST.json` (`"format": "seneca-audio"`) with each file's size and SHA-256. It is written into a hidden directory and renamed into place last. **Restore Spoken Reflections…** takes such a kit, or its `audio/` folder, checks every file, and that every take has both its audio and its sidecar, before writing anything, and puts the takes back in `Application Support/Seneca/audio/` all or nothing; a take already on this iPhone is never replaced, and a restore stopped part way is finished or undone at the next launch. Diagnostics count the takes, their bytes, the takes waiting for a transcript, the speech model's state, and the retention choice; never a transcript, a date, or an id.

On a simulator, a UI test can stand in for the microphone, transcriber and on-device model: `SENECA_UITEST_MICROPHONE=denied` refuses the microphone, `SENECA_UITEST_TRANSCRIPT` is what every take transcribes to, and `SENECA_UITEST_SPLIT` is the model's answer (a JSON object `{"answers": [...], "unassigned": "..."}`, or `refused` or `too-long`; unset, the model is unavailable).

Debug builds also offer Settings › Model Evaluation, which runs review prompts exported by `scripts/apple-models/run.sh` on Apple's models on this iPhone and writes the responses beside the app's state for `run.sh pull`; it never opens the journal ([Apple's models](../docs/product/apple-models.md#running-it-on-the-iphone)). Each take's sidecar also keeps when it stopped and how long its answers took to propose, which device qualification reads.

## Reminders, diagnostics, and recovery

Settings, Reminders turns the evening reminders on, at the Mac's times or your own. iOS asks for permission once; with notifications turned off the screen says so and links to Settings, and writing is not affected. Settings, Diagnostics and Recovery lists what needs attention with what to do, shares a content-free report, exports the whole journal, and rejoins or disconnects sync. A journal restored onto a new iPhone from a backup of another is not exchanged until it rejoins as this iPhone. [Reminders, diagnostics, and recovery](../docs/mobile/recovery.md) has the rules and the procedures.

## Tests

The hosted suite has these parts.

| Suite | What it holds |
| --- | --- |
| `RoundTripTests` | The app's engine service and its model open the synthetic journal, save a reflection through Go, and reopen it holding what the desktop writes. |
| `BridgeOperationsTests` | Every typed wrapper against the real engine, including two devices exchanging and settling a conflict. |
| `EngineServiceTests` | Calls run one at a time, away from the main thread; refusals and broken replies are different failures. |
| `DailyWorkflowTests` | The form's session and the draft store over the real engine: what a draft keeps, relaunches, stale and failed saves, a torn archive, edits that keep unnamed sections, earlier days, and changed questions. |
| `BrowsingTests` | Over the browsing fixture: history by month, range, and date, and searched, with the engine's queries; every review period and each revision; the focus set and cleared only from the timeline shown; the questions and anchor changed as one unit. |
| `ConflictTests` | Over the conflicted fixture: a conflicted day stays readable and its writing stays a draft across a relaunch until it is settled; an answer is given once and a stale one refused; a conflict settled offline converges with another engine once the exchange resumes. |
| `ExchangeTests` | The exchange with the folder over the real engine, with the folder's faults scripted: contents before manifests, files listed and not downloaded, files cut short, a folder that is gone, a grant taken back, a publication stopped part-way, receipts, and edits on both sides. |
| `ExchangeLifecycleTests` | When the app exchanges by itself (launch, foreground, save, refresh, retry, background), that two exchanges never overlap, the grant across launches, and the folder through file coordination. |
| `ReminderTests` | The reminders over the real engine with iOS's notification center stood in for: off until turned on, the Mac's times or the author's, planned again after a save, an import, a return to the app, and a change of clock or zone; a day written here or imported is not reminded; denied permission is said and never stops writing; a plan that cannot be made keeps what was scheduled. |
| `RecoveryTests` | Diagnostics that carry nothing written; a recovery export with every word and no identity, and one stopped part-way; an unavailable folder named while writing goes on; a fresh iPhone joining, and a copy restored from another iPhone kept from exchanging until it rejoins as itself, with every word kept; disconnecting. |
| `VoiceCaptureTests` | Speaking a reflection with the vendored simulated recorder and transcribers: a take recorded into `audio/<date>/` as a partial and renamed when it stops, its sidecar, nothing written in the journal; a refused microphone; an interruption; the dictation fallback; a failed take kept and tried again across a relaunch; a missing speech model never waited for; a transcription that finishes after the screen moved on leaving the new recording alone; a partial found at launch finalized and offered for transcription, but not while the phone is locked. |
| `VoiceRetentionTests` | What is kept of a take: kept, or deleted once its answers are saved as the author chooses, remembered across a relaunch, and never deleted by other writing saved after the draft its answers went into was discarded; a deletion that removes the audio and sidecar and nothing else, one at a time or all; a recovery kit whose `audio/` folder sits beside an engine export identical to one made alone, restored byte for byte, never over a take already here, refused whole when damaged or when a take lacks its sidecar, and a restore stopped between its renames finished or undone; diagnostics that count the audio and carry none of it. |
| `ProposalTests` | A transcript on its way into the form: the runtime schema shaped for one to five questions and read back, the unavailable, refusal and too-long paths falling back to the transcript with copy actions, the merge rules that keep typed text, and a spoken entry saved through the bridge byte for byte as a typed one with the same answers, its sidecar marked saved. |
| `RedeploymentTests` | The round trip in two phases, with a reinstall between them. Only `scripts/roundtrip.sh` runs it. |
| `SyncEvidenceTests` | The phone's half of the end-to-end exchange, one phase per test. Only `scripts/sync-evidence.sh` runs it. |
| `NotificationEvidenceTests` | Two reminders scheduled with iOS on a physical iPhone, the first delivered, the second cancelled by a save and never delivered. Only `scripts/notify-evidence.sh` runs it. |

The synthetic journal and the scenario come from `../mobile/testdata/roundtrip/`, which the Go tests hold to the desktop; `browsing/` and `conflicted/` beside it are written by the engine and held to it the same way. No test opens the installation's own journal: each works on a copy in a directory of its own.

The UI tests are a scheme of their own, `SenecaMobileUITests`, and run on a simulator only. Each stages the synthetic journal in the test runner's temporary directory and launches the app on it through `SENECA_UITEST_ROOT`, with the scenario's clock, zone, and questions. A simulator lets both processes see that directory, so a test reads what the app wrote and changes the journal behind its back. Only a simulator build reads those variables; an iPhone build has no such door.

| Suite | What it holds |
| --- | --- |
| `DailyWorkflowUITests` | Typed text survives the app being stopped and comes back with its context; a discarded draft is gone; a save over a day that changed keeps every typed word, across another stop; a torn archive still lets the day be written without context and is left untouched; an edit keeps the section no question names. |
| `BrowsingUITests` | Over the browsing fixture: history a month at a time and on this day, the nearest writing named for an empty month, a search; each review period and a revision followed both ways; the focus set and cleared; a question added and made the anchor. |
| `ConflictUITests` | Over the conflicted fixture with its exchange: a conflict read version by version and settled by keeping one, which is published; a focus dispute settled; writing for a conflicted day kept as a draft across a stop, then saved after starting from the other version. |
| `RecoveryUITests` | Reminders off until turned on, scheduled, and cancelled for an evening once it is written; denied notifications said and writing unaffected; diagnostics that show nothing written; a recovery export. |
| `VoiceUITests` | A refused microphone explained with the way to Settings, writing unaffected; a take spoken from the form, transcribed by a staged transcriber, and kept beside the drafts with its sidecar, the journal untouched; proposed answers edited and confirmed into the form beside typed text, saved by the form, the sidecar marked saved; an unavailable model offering the transcript to copy, from Today into the draft the form opens on; recordings deleted from History one at a time and in bulk, with nothing else touched; Voice settings deleting a take once its answers are saved; a recovery export carrying the recordings while the shared diagnostics only count them. |
| `AccessibilityUITests` | At the largest accessibility text size the questions and fields grow and everything is still reached and saved; every control is named for VoiceOver and the screens pass the system's audit; the keyboard bar moves between fields, Return writes a new line, and Hide Keyboard clears the way. |

The accessibility audit leaves out the system's tab bar and navigation bar, which keep their size and offer the large content viewer instead.

`scripts/roundtrip.sh simulator|device OUTPUT_DIR` proves that redeployment keeps data. It installs a build, saves the reflection inside it, installs a build with another build number over it, and reopens the journal from the second. It writes `OUTPUT_DIR/roundtrip.json` only when both phases ran exactly one passing test each, both report the journal the desktop writes, and each reports the build number and executables this Mac built for it.

The capture package, `Packages/Capture/`, has tests of its own, outside the app: `scripts/capture-test.sh OUTPUT_DIR` runs them on a simulator, and `swift test --package-path Packages/Capture` runs all but the iOS-only ones on this Mac.

| Suite | What it holds |
| --- | --- |
| `AudioCaptureEngineTests` | A recording's record and directory exist before any audio; a finished one is durable before it is reported; no audio, undecodable audio, a denied microphone, and a full disk are each recorded as such and never saved; an interruption keeps what was captured. |
| `CaptureRecoveryTests`, `CaptureUnlockTests` | The next launch finishes an interrupted recording, adopts audio whose record never landed, and flags audio that does not play, once; while the phone is locked nothing is decided, and unlocking finishes what waited. |
| `CaptureAudioFileStoreTests` | Where audio lives, the atomic promotion of the partial file, and what counts as interrupted. |
| `HostLayoutTests` | Seneca's own layout, `<date>/<uuid>.partial.m4a` promoted to `<uuid>.m4a` by path. |
| `TranscriberChainTests`, `LocaleMatcherTests` | The closest supported locale, the hand-off from one transcriber to the next, and cancellation. |
| `SimulatedDevicesTests` | The simulated recorder and transcriber later tests stand on. |
| `AudioRecorderAdapterTests`, `FragmentedAudioFileWriterTests` | The real encoder settings, and that a file abandoned mid-recording plays up to its last fragment. iOS only. |

## Device evidence

A simulator run is repeated every time the gate runs. A run on a physical iPhone needs the phone, so its record is committed: `docs/mobile/evidence/session-8-device-roundtrip.json`.

The record names the sources it was built from. `scripts/source-fingerprint.sh` prints that fingerprint: the Go sources the bridge compiles, the gomobile pin, the toolchains, the project spec, the Swift sources, the round-trip scenario, and these scripts. The gate refuses a record whose fingerprint is not the current one, so after changing any of those, record it again:

```sh
just ios-device-evidence
```

`SENECA_MOBILE_DEVICE=live just mobile-verify 8` runs the device round trip during the gate instead of reading the committed record.

### Sync evidence

The exchange through iCloud Drive cannot be had on a simulator, so its record is committed too: `docs/mobile/evidence/session-10-device-sync.json`.

`scripts/sync-evidence.sh device OUTPUT_DIR` alternates between the iPhone and this Mac. The phone's half is `SyncEvidenceTests`, hosted in the app over a synthetic journal in a directory of the run's own. The Mac's half is the `seneca` program built from these sources, over the same synthetic journal in a Git repository of the run's own. They share nothing but a folder of the run's own, `seneca-evidence-<run>`, inside the exchange folder chosen in the app: the phone reaches it through that grant, the Mac through `~/Library/Mobile Documents`. The installation's journal and its own exchange are never opened, and the run's folder is removed afterwards.

| Step | What has to hold |
| --- | --- |
| Join | The Mac enrolls and publishes; the phone joins and holds the same entries. |
| Offline save | The phone saves a reflection without opening the folder. It is reported as saved on the phone only, and the Mac does not have it. |
| Publish, import | The phone exchanges. The Mac's `seneca sync once` brings the reflection into its Markdown as one `seneca: sync import` commit, byte for byte what the phone saved. |
| Confirm | The phone reports it applied on the Mac only now, from the Mac's receipt. |
| Desktop edit | A day edited by hand on the Mac arrives on the phone. |
| Simultaneous edits | Both edit two days before either hears of the other. The day changed in different places merges to the same entry on both; the day changed in the same place is a conflict both name alike, each side keeping its own version and holding the other's. |
| Interrupted publication | The phone's exchange is cancelled after its first file. The Mac receives contents with no manifest and applies nothing. The next exchange finishes it. |
| Interrupted download, Mac | The arrived files are sent back to iCloud (`brctl evict`) before the Mac imports, so it has to ask for them again. |
| Interrupted download, phone | The phone's exchange is cancelled after its first read; its journal is unchanged, and the next exchange applies the Mac's edit. |
| Final | Both hold the same entries but for the conflicted day, every change of each is applied on the other by its receipt, and every import is in Git. |

`OUTPUT_DIR/sync.json` is written only when every one of those holds, each phase ran exactly one passing test on the device named, the app reports the build and executables this Mac built, and the folder is in iCloud on both sides. The record names the fingerprints of the app's sources, the bridge, and the desktop program; the gate refuses a record made from other sources or on a simulator. Before the first run, choose the exchange folder in the app. Then:

```sh
just ios-sync-evidence
```

`just ios-sync-rehearsal` runs the same scenario between a simulator and this Mac over a plain folder. It checks the scenario and the scripts and proves nothing about iCloud. `SENECA_MOBILE_DEVICE=live just mobile-verify 10` runs the device exchange during the gate.

### Notification evidence

Whether iOS delivers a reminder cannot be learned on a simulator, so its record is committed too: `docs/mobile/evidence/session-12-device-notification.json`.

`scripts/notify-evidence.sh OUTPUT_DIR` builds the app signed, runs `NotificationEvidenceTests` on the paired iPhone over a synthetic journal of the run's own, on the real clock and in the iPhone's zone, and collects what it reports. The app plans two reminders for this evening, a minute or two apart, and schedules them with iOS. The run holds when iOS delivers the first within its minute, a reflection saved before the second makes the app cancel it, and iOS never delivers the second, watched for half a minute past its time. `OUTPUT_DIR/notify.json` is written only then, from exactly one passing test on the device named, with the build and executables this Mac built; the gate refuses a record made from other sources. The iPhone must be unlocked and allow Seneca's notifications (iOS asks on the first run). The run schedules under a prefix of its own, so the installation's reminders are left alone. Then:

```sh
just ios-notify-evidence
```
