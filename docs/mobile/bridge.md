[← Mobile contracts](README.md)

# Bridge

The bridge is how the iPhone app reaches the engine: one JSON request in, one JSON response out. It is the Go package `mobile/senecabridge`, bound with gomobile into `SenecaBridgeCore.xcframework`, and its Swift face is the `SenecaEngine` package under `ios/`. The rules it is held to are on the [engine and bridge](engine-and-bridge.md) page; this page is version 8 of the contract itself.

Nothing in the bridge decides what a journal means. Every operation is a call into `engine.Journal`, the same call a desktop command makes, and every result is a view over what the engine returned.

## Requests and responses

A request names the bridge version it was written for and its operation. A mutation also carries a request identity.

```json
{"version": 8, "operation": "reflection.save", "request_id": "5c0e…", "params": {}}
```

A response repeats the version, the operation, and the identity, and is either a result or an error, never both.

```json
{"version": 8, "operation": "reflection.save", "request_id": "5c0e…", "ok": true, "result": {}}
{"version": 8, "operation": "reflection.save", "request_id": "5c0e…", "ok": false,
 "error": {"code": "stale-reflection", "message": "…", "retryable": false, "details": {"date": "2026-04-02"}}}
```

- **Strict.** An unknown field in the request or in its parameters, data after the request, a version other than 8, or an operation not listed below is an error response. Nothing is guessed.
- **Errors are data.** A failure has a stable `code`, the `message` the command line gives for the same refusal, and `retryable`, which is true only when sending the same request again can help. A panic inside the engine is caught and answered as `internal`.
- **A write that landed is a result.** When a change reached the journal and only a later step failed, the response is `ok` with the result and a `warning` beside it, coded `write-not-flushed` or `commit-pending`. The caller finishes as on success and does not send the change again.
- **One journal, one request at a time.** A bridge holds at most one open journal and answers one request at a time. Two bridges share nothing: the app holds one, and a test holds its own.
- **One instant per request.** Each request opens a fresh `engine.Journal` with the time the request arrived, as each desktop command does. Nothing is held between requests, and no lock outlives one.

## Operations

Version 8 is this set and no other. Adding an operation, a field, or an error code makes version 9. Version 7 was version 8 without `share.packet`. Version 6 was version 7 without `insights.get`. Version 5 was version 6 without `packs.list`, `packs.read`, `settings.apply_pack`, the `practice` of the settings shapes, and the code `invalid-pack`. Version 4 was version 5 without `entry.delete`. Version 3 was version 4 without `reminders.plan`, `diagnostics.collect`, `recovery.export`, `sync.leave`, and the codes `invalid-reminders`, `destination-exists`, and `sync-state-version`. Version 2 had the same operations without the history queries of `entries.list` and `entries.search`, the `period` and `revised_by` of `reviews.list`, the `conflicted` mark on a day, and the `conflicted` refusal of `reflection.save`. Version 1 was version 2 without `sync.delivery`, and its `sync.export` published no receipt. An engine speaks one version, and the app is built with the engine it links.

| Operation | Changes the journal | What it does |
| --- | --- | --- |
| `bridge.describe` | no | The bridge version, its operations, and the schema versions the engine reads and writes. Needs no open journal. |
| `journal.open` | no | Opens the journal at `reflections_path` with this device's `state_dir`, questions, anchor, and `time_zone`, and returns its status. Reads; writes nothing. |
| `journal.status` | no | Today, whether each file exists and would accept a write and why not, the settings in force, and whether the journal is enrolled in sync. |
| `journal.close` | no | Forgets the open journal. |
| `entries.list` | no | The entries a query keeps (every one, one `month`, a `from`/`to` range of days, or `on_this_day`, as `seneca show` keeps them), in file order: date, revision, how long ago, the first line written, and whether the day is `conflicted`. With a month or a range, also the nearest days written on either side of it. |
| `entries.search` | no | The entries that mention a phrase, with the matching lines, as `seneca show --search` finds them, within the same query `entries.list` takes. |
| `entry.get` | no | One day: its revision, its bytes as stored, its lines as displayed, its sections, and whether another device's version of it waits beside this one. |
| `entry.delete` | yes | Removes the saved day named by `date`, conditional on its `base` revision from `entry.get` or `entries.list`. Returns `{date}`. A stale revision or unresolved conflict refuses the deletion. Backups and sync use the normal commit; local drafts and recordings are kept. |
| `reflection.prepare` | no | The draft for a day: the questions, the stored answers, the focus and the intention being followed through, the base revision, and any writing kept for that day. |
| `reflection.save` | yes | Saves the answers against the draft they were written on. Refused while the day is conflicted. |
| `draft.get` | no | The writing kept for a day that a save could not store. |
| `draft.retain` | device only | Keeps such writing, on this device. |
| `draft.discard` | device only | Drops it. |
| `settings.get` | no | The questions and anchor in force, whether the journal or the device supplies them, and their revision. When a practice pack was ever applied, also the `practice`: the last pack, the day it was applied, and whether the questions were changed since. |
| `settings.initialize` | yes | Gives the journal settings of its own, seeded from the device's. |
| `settings.update` | yes | Changes the questions, the anchor, or both as one unit, from the revision named. |
| `settings.apply_pack` | yes | Makes a practice pack the journal's practice from today: its questions and anchor replace the journal's as one unit, from the revision named, and the pack is recorded whole. A journal without settings of its own gets them in the same commit. Entries already written are not touched. |
| `packs.list` | no | The built-in practice packs, the default practice first. Needs no open journal. |
| `packs.read` | no | Checks the contents of a `.seneca-pack.json` file given as `data` and returns the pack as the engine reads it. Needs no open journal. |
| `insights.get` | no | What became of the intentions written in a window, the intentions written in the same words more than once, the words that recur in them, and how long each focus stood and what replaced it: [follow-through insights](../insights.md). The window is the last `days` days (30 when nothing is given), or `from` to `to`. A damaged archive leaves out the focus and says why in `focus_problem`. |
| `share.packet` | no | A packet to send to another person: the saved review `review_id`, the entries of `dates`, or both, as Markdown written for a reader. It names no file or record and carries none of Seneca's markers. A review or a day the journal does not have is `not-found`; choosing nothing is `invalid-query`. See [share packets](../export.md#share-packets). |
| `reviews.list` | no | The saved reviews, newest first, without their bodies, or those of one `period`. Each names the review it revises and the reviews that revise it. |
| `review.get` | no | One saved review with its Markdown. |
| `focus.get` | no | The focus in effect on a date, the whole timeline, and the timeline's revision. |
| `focus.set` | yes | Sets the focus from today, from the timeline revision named. |
| `focus.clear` | yes | Clears it, from the timeline revision named. |
| `sync.status` | no | This device's view of the journal's sync history. |
| `sync.enroll` | yes | Makes this device the first of a synced journal. |
| `sync.join` | yes | Makes this device another device of the journal an exchange directory carries. |
| `sync.import` | yes | Brings in what the exchange holds. |
| `sync.export` | exchange only | Sends what this device recorded and the exchange lacks, each transaction after its contents, and then a receipt of what this device has applied when that grew. |
| `sync.delivery` | no | Where this device's own changes have got to: how many are recorded here, how many of them the exchange holds, and how many each other device says it has applied. |
| `sync.recover` | yes | Restores a suspended journal file from history, or accepts its deletions. |
| `sync.leave` | yes | Takes the journal out of sync: the history and this device's identity are moved aside, never used again, and every journal file stays as it is. The journal can then enroll or join as a new device. |
| `conflicts.list` | no | Every conflict and every disputed focus, with each version held. |
| `conflict.resolve` | yes | Answers a conflict by keeping a version or writing another. |
| `focus_dispute.resolve` | yes | Settles a disputed focus. |
| `reminders.plan` | no | The evening reminders still ahead for the next `days` (default 7) at `times` (HH:MM; default the desktop's 20:00, 21:00, 21:30, 22:00), each with its instant in the journal's zone, skipping every day this device holds an entry for. Also the defaults, the zone, and the complete days. The app schedules what it returns; the engine schedules nothing. |
| `diagnostics.collect` | no | A content-free report: versions, counts, and stable finding codes about the files, sync, and, with `exchange_path`, the exchange (changes not sent or not imported, changes from a newer seneca, this device's identity used elsewhere). No text, date, path, or name from the journal. |
| `recovery.export` | no | Writes the whole journal into a new `destination` directory outside it: the reflections file and the archive byte for byte, the settings, every version of every open conflict, the engine's kept drafts and the files in `drafts_dir`, with a manifest. No sync identity. It appears whole or not at all. |

There is no operation that writes a review. Reviews are generated on the desktop; the phone reads them.

Paths are absolute and supplied by the app. The engine resolves nothing from the environment: no home directory, no `SENECA_FILE`, no `config.toml`. The exchange is a directory with the layout the desktop's `seneca sync` commands use. On the phone it is a mirror inside the app's container; the app's own adapter carries whole files between that mirror and the folder in iCloud Drive, as the [iCloud transport](icloud-transport.md) page describes. The engine is never given a path outside the container.

## Mutations are conditional and identified

A mutation names the state it was decided on, and the engine refuses it when that state is gone. Nothing is overwritten on the strength of an old read.

| Mutation | Decided on | Refused with |
| --- | --- | --- |
| `reflection.save` | The draft from `reflection.prepare`, returned unchanged: the day's entry revision and the settings revision. A day two devices wrote differently takes no new writing until the author chooses between them. | `stale-reflection`, `stale-settings`, `conflicted` |
| `entry.delete` | `base`, the saved entry revision shown before confirmation. Local drafts and recordings are kept. | `stale-reflection`, `conflicted` |
| `settings.update` | `base`, the settings revision. | `stale-settings` |
| `focus.set`, `focus.clear` | `base`, the focus timeline's revision. | `stale-focus` |
| `conflict.resolve`, `focus_dispute.resolve` | `id`, which names the versions competing. | `stale-conflict` |
| `sync.recover` | The file being suspended. | `nothing-to-recover` |
| `sync.leave` | The journal taking part in sync. | `sync-not-enrolled` |
| `settings.initialize`, `sync.enroll`, `sync.join`, `sync.import` | Nothing: each is reported unchanged when there is nothing left for it to do. | |

The draft is the read half of a reflection. The app shows it and hands it back whole with the answers, so the entry is written under the questions, the anchor, and the intention the author actually saw.

A mutation also carries a `request_id`, and one without it is refused. The bridge keeps the answer it gave each mutation it carried out in a request ledger, `requests.json` in the `state_dir` the app opened the journal with.

- The same identity with the same operation and parameters is answered again from the ledger, marked `"replayed": true`. The journal is not touched.
- The same identity with anything else is refused as `request-id-reused`.
- A refusal is not recorded. A request that was refused can be sent again once its cause is gone.
- The ledger names requests by what they asked, not by where the journal is. An app's container moves when a new build is installed, and a request repeated afterwards is still answered.
- The ledger is the installation's own. It is not in the journal directory, is never exchanged, and keeps the last 256 answers.

The ledger is written after the journal. If the app dies between the two, the repeated request is carried out again and meets its own precondition: the day it would write has changed, so it is refused as stale. Either way a save is never made twice.

## Error codes

| Code | Meaning |
| --- | --- |
| `malformed-request` | The request is not a bridge request. |
| `unsupported-version` | The request names a bridge version the engine does not speak. |
| `unknown-operation` | The operation is not in this version. |
| `invalid-params` | The parameters are not what the operation takes. |
| `missing-request-id` | A mutation without an identity. |
| `request-id-reused` | An identity already used for a different request. |
| `journal-not-open` | An operation that needs `journal.open` first. |
| `internal` | The engine failed unexpectedly. Nothing is promised about what was written. |
| `invalid-settings` | The device's questions or anchor are not valid. |
| `invalid-date` | Not a calendar date. |
| `invalid-query` | A history or review query that names no real days or period, or two ways of choosing days at once. |
| `not-found` | No entry for that day, or no review with that identity. |
| `missing-base` | A conditional mutation that names no state. |
| `future-date` | A reflection dated after the journal's today. |
| `empty-reflection` | Nothing was written. |
| `stale-reflection` | The day changed after the draft was prepared or the entry was read for deletion. |
| `stale-settings` | The questions or the anchor changed. |
| `stale-focus` | The focus timeline changed. |
| `stale-conflict` | The conflict is no longer the one that was listed. |
| `conflicted` | The day waits for the author to choose between two devices' versions; nothing was written. |
| `stale-file` | A metadata file changed after it was read. |
| `blank-focus` | A focus with no text. |
| `anchor-moved` | New questions without the anchor question, and no new anchor named. |
| `settings-not-initialized` | The journal has no settings of its own to change. |
| `invalid-settings-document` | The journal's settings document cannot be used. |
| `invalid-reflections` | The reflections file is one no command would rewrite. |
| `invalid-archive` | The review archive is one no command would append to. |
| `unreadable-context` | The archive cannot say what the focus is. |
| `symlink-destination` | A journal file is a symlink. |
| `journal-busy` | Another writer held the journal for the whole wait. The only retryable code. |
| `recovery-blocked` | An interrupted save could not be completed. |
| `file-unavailable` | A file could not be reached. |
| `sync-not-enrolled` | The journal does not take part in sync. |
| `sync-identity` | The exchange belongs to another journal. |
| `sync-claim` | Another journal in the directory is already enrolled. |
| `sync-protocol` | The exchange uses a sync protocol this engine does not understand. |
| `nothing-to-recover` | The file is being read normally. |
| `sync-state-version` | This device's sync state was written by a newer seneca, or cannot be read as any version; `sync.leave` moves it aside. |
| `invalid-reminders` | Reminder times that are not HH:MM, more than eight an evening, or a plan of no days or more than fourteen. |
| `destination-exists` | A recovery export into a directory that already exists. |
| `invalid-pack` | A practice pack that does not hold to the [pack format](../product/practice-packs.md). |
| `refused` | Any other refusal. The message says what; no code is promised for it. |
| `write-not-flushed` | Warning only: the change is in the journal and flushing it failed. |
| `commit-pending` | Warning only: the change is recorded and the next operation finishes it. |

## The Swift face

`SenecaEngine` is an actor with one method per operation and a `Codable` model per shape. It parses no Markdown.

- Calls run one at a time on a queue of the actor's own, never on the main thread.
- A refusal is thrown as `EngineProblem`, with the code, message, and details above. A reply that is not a response, or an engine that speaks another bridge version, is a `BridgeFailure`.
- A mutation returns `Outcome`: the result, whether it was replayed, and the warning if there was one.
- A mutation takes a `RequestID`. A screen keeps one for as long as the same save may be sent again, and draws a new one for a new save.

## The round trip

`mobile/testdata/roundtrip/` is one scenario every platform runs: open the synthetic journal, save a reflection, reopen it.

| File | What it is |
| --- | --- |
| `journal/` | The synthetic journal of the compatibility baseline, copied. A test fails when the copy drifts. |
| `scenario.json` | The clock, the time zone, the device's questions, the answers, and the request identity. |
| `expected.json` | What the journal holds afterwards, as the desktop engine writes it: digests of both files, the entry's revision and bytes. |

`mobile_roundtrip_test.go` holds `expected.json` to the desktop engine, holds the bridge to it, and has the `seneca` program read the day the bridge saved. The app's tests carry the same directory in their bundle and are held to the same file on a simulator and on a physical iPhone, where the reflection is saved by one installed build and reopened by the next. Regenerate `expected.json` with `SENECA_UPDATE_ROUNDTRIP=1 go test . -run TestRoundTripExpectation` and review the diff.

Two more journals sit beside it for the app's browsing, settings, and conflict suites. The engine writes both with fixed clocks and fixed identities, and `TestTheSessionFixturesAreWhatTheEngineWrites` holds the committed copies to what it writes; `SENECA_UPDATE_FIXTURES=1 go test ./mobile/senecabridge -run TestTheSessionFixtures` writes them again.

| Directory | What it is |
| --- | --- |
| `browsing/` | The round-trip journal with days written in January and February, and a yearly and two quarterly reviews saved, so every review period and a revision of each kind of window are there. |
| `conflicted/` | The round-trip journal synced between a Mac and the phone, as the phone holds it after both wrote 2026-04-02 differently and set a different focus that day: `journal/` with its `.seneca` history, and `exchange/` with what both published. |

`mobile_mutations_test.go` drives every mutation the phone can make through the bridge into an exchange that the real `seneca` program imports into Markdown and a Git commit, and the desktop's answers back, including a conflict settled while the phone exchanged nothing.

Building, testing, and installing the app are described in [`ios/README.md`](../../ios/README.md).
