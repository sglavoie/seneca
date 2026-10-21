[← Mobile contracts](README.md)

# Journal layout

A journal is a reflections file and what sits beside it.

```text
<dir>/
├── reflections.md                      the journal; any configured filename
├── reflections.md.bak                  what the last write replaced
├── seneca-reviews.md                   the review archive, one per directory
├── seneca-reviews.md.bak
└── .seneca/
    ├── journals/
    │   └── reflections.md/             this journal's private metadata
    │       ├── settings.json           the questions and anchor every device shares
    │       ├── drafts/                 writing a failed save could not store
    │       ├── desktop/                this desktop's own: Git owed for an import, the usage ledger
    │       └── sync/                   this device's sync history, once enrolled
    │           ├── state.json          identities, heads, and what was last recorded
    │           ├── transactions/       immutable manifests, one directory per device
    │           └── objects/            the contents they name, by digest
    └── directory/                      state shared by every journal in <dir>
        ├── sync.json                   which journal in <dir> is enrolled
        ├── lock                        held while one commit runs
        ├── commit/                     a multi-file commit in flight
        └── drafts/                     reviews the archive refused
```

Everything outside `.seneca/` exists today and does not move. The reflections file stays one file; it is not split into daily files, and no second store holds an authoritative copy of the journal.

## Finding the journal

The reflections path is resolved in this order, and everything else is derived from it:

1. `SENECA_FILE`, when set.
2. `seneca.reflections_path` in `~/.config/seneca/config.toml`.
3. The built-in default.

| Path | Derived as | In code |
| --- | --- | --- |
| Review archive | `seneca-reviews.md` in the reflections file's directory | `engine.ArchivePathFor` |
| Journal metadata | `.seneca/journals/<reflections filename>/` in that directory | `engine.JournalMetadataDirFor` |
| Directory metadata | `.seneca/directory/` in that directory | `engine.DirectoryMetadataDirFor` |

Pointing `SENECA_FILE` somewhere else moves all three with it.

The exchange a synced journal reaches other devices through is not derived from the journal. It is a path on this machine: `SENECA_SYNC_EXCHANGE` when set, otherwise `sync.exchange` in `config.toml`. Like the reflections path it is device-local and never part of the shared settings.

## The metadata directory

`.seneca/` is Seneca's own. The author is not expected to read or edit it, and nothing in it is needed to read the journal: a reflections file and an archive copied elsewhere are a complete, readable journal.

A journal's metadata directory is named after the reflections file, extension included. It holds, as later work introduces them:

- the journal's identity,
- the journal settings both devices share (questions and anchor selection),
- this device's identity and its record of sync history.

The exact files are named by the work that introduces each one. Three rules apply to all of them:

- **Created by a write, never by a read.** No read-only command creates `.seneca/` or anything in it, the same rule that already holds for the archive.
- **Written through the persistence layer.** The same atomic replacement and symlink refusal as the journal.
- **Passed to Git only by name.** Each command commits only the files it names, so nothing under `.seneca/` is staged unless a command names it deliberately. `seneca settings init` and `seneca settings set` name the settings document and nothing else.

Machine paths, the editor command, the AI harness, reminder preferences, and drafts are device-local and stay out of the shared settings.

### What is there today

One shared document, three transient device-local things, the desktop's usage ledger, and, for a journal enrolled in sync, its history. A journal that was never given settings, enrolled, or reviewed, and is written cleanly, has no `.seneca/` directory at all between writes.

| Path | In code | Lifetime |
| --- | --- | --- |
| `.seneca/journals/<reflections filename>/settings.json` | `engine.SettingsPathFor` | Created by an explicit initialization or by applying a practice pack, never by a read or by another write, and kept from then on. Shared: it is the one file here that every device must agree on. |
| `.seneca/directory/lock` | `engine.LockPathFor` | Exists while one commit, or one usage ledger append, runs. Removed on release; a leftover file from a process that died holds no lock and blocks nobody. |
| `.seneca/directory/commit/` | `engine.CommitDirFor` | Exists while a commit that changes several files is being written: the staged contents, named by digest, and `record.json`. Left behind only by a crash, and settled by the next operation on the journal. |
| `.seneca/journals/<reflections filename>/drafts/<date>.json` | `engine.DraftPathFor` | Written when a daily save fails or is refused as stale. Restored into the form by the next run for that date and removed when the day is saved. |
| `.seneca/directory/drafts/review-<period>-<start>-to-<end>.md` | `engine.ReviewDraftPathFor` | Written when an approved review cannot be archived, so the harness run is not lost. Never read back by Seneca. |
| `Application Support/Seneca/drafts/<date>.json` (iPhone, in the app's container) | `DraftStore` in `ios/SenecaMobile/DraftStore.swift` | Written by the app, not the engine, on every change to the writing form: the engine's draft (date, base revision, settings revision, questions, context) and what was typed against it. Outside the journal directory, so it is never exchanged. Removed when the day is saved or the author discards it. |
| `Application Support/Seneca/exchange/grant.json` (iPhone, in the app's container) | `GrantStore` in `ios/SenecaMobile/Exchange/ExchangeGrant.swift` | Written when the author chooses the exchange folder: a bookmark of it and its name. Device-local; replaced by choosing another folder. |
| `Application Support/Seneca/exchange/mirror/` and `delivery.json` (iPhone) | `ExchangeMirror`, `ExchangeService` in `ios/SenecaMobile/Exchange/` | The phone's own copy of the exchange, which is all the engine imports from and publishes into, and what was last seen published in the folder. Whole, verified files only. Emptied when another folder is chosen. |
| `.seneca/journals/<reflections filename>/sync/state.json` | `engine.SyncStatePathFor` | Created by enrolling or joining, and rewritten by every commit that records or applies a transaction. Device-local: it is this device's view and is never exchanged. |
| `.seneca/journals/<reflections filename>/sync/transactions/` and `objects/` | `engine.SyncDirFor` | Manifests and the contents they name. Written once and never changed; kept for good. |
| `.seneca/journals/<reflections filename>/desktop/git-pending.json` | `gitPendingPathFor` in `cmd/sync_cycle.go` | Exists from the moment an import replaced a journal file until Git has committed it. Desktop-only and device-local; the engine never reads it. |
| `.seneca/journals/<reflections filename>/desktop/usage.jsonl` | `usageLedgerPathFor` in `cmd/review_usage.go` | Created by the first `seneca review` that reaches its harness, never by a read, and one line appended per review attempt after that: the review kind and window, harness, provider, model, effort, token counts, cost, latency, and outcome. No entry, answer, review text, or path. Desktop-only and device-local; never committed, synced, or read by the engine. `seneca usage` and `seneca doctor` read it. |
| `.seneca/directory/sync.json` | `engine.SyncClaimPathFor` | Created by enrolling or joining. Names the one journal in the directory that takes part in sync. |

### Journal settings

`settings.json` holds the questions and the anchor selection, the practice packs applied, and nothing that differs between devices or runs: no timestamp, no device name. Two devices that record the same settings write the same bytes.

```json
{
  "version": 1,
  "reflection": {
    "questions": ["…", "…", "…"],
    "anchor_question_index": 3
  },
  "legacy": {
    "questions": ["…", "…", "…"],
    "anchor_question_index": 3
  }
}
```

- **`version`** is checked first. A newer version is refused, not guessed at, and so is a field this version does not define.
- **`reflection`** is one unit. The anchor index means nothing without the list it points into, so the two are validated together, replaced together, and identified by one revision: a digest of this object alone. A reflection draft and a settings change both carry that revision back, and are refused when it no longer matches.
- **`legacy`** is `reflection` as it was when the document was created, and never changes. An entry written without an anchor role marker has its anchor at a position; this records which position, in which list, so that reordering the questions later cannot make a different answer the intention.
- **`practice`** appears only once a [practice pack](../product/practice-packs.md) has been applied, and makes the document version 2: every pack applied, whole, oldest first, each with the journal day `from` which it was in force, one a day. A document without it is version 1, so a journal that never applies a pack writes what it always has. Reviews read it to give each day the guidance of the pack it was written under; the questions are `reflection`'s, as ever.

Precedence is all or nothing. Without the document, the settings the journal was opened with — `config.toml` on the desktop — are in force. With it, the document is, and the opened settings are used only to seed it. A document that cannot be read is an error, never a reason to fall back.

The lock and the commit record are scoped to the directory, not to one journal, for the reason the archive is: every journal in a directory can write the same `seneca-reviews.md`, so they must exclude one another.

## Journals that share a directory

Two reflections files may sit in one directory, for example `reflections.md` and `work.md`, selected by `SENECA_FILE` or by the configured path.

**Private metadata never collides.** Each journal has its own directory under `.seneca/journals/`, keyed by its filename, so the two never read or write each other's identity, settings, or sync state.

**The archive is shared.** The archive path depends only on the directory, so both journals read and append to the same `seneca-reviews.md`. A focus set while writing in one is the focus shown in the other, and their reviews form one history. This is how Seneca behaves today, and it does not change: scoping the archive to the journal would orphan every existing archive.

**At most one journal per directory takes part in sync.** A synced journal exchanges its reflections and its archive together. Two synced journals over one archive would each claim the same records, so the second enrollment in a directory is refused. The claim is `.seneca/directory/sync.json`, which is scoped to the directory and which no journal filename can reach. A change another journal in the directory makes to the shared archive is recorded in the enrolled journal's history like any other. Journals that are not synced go on sharing the directory, and the archive, exactly as before.

To sync two journals, keep them in two directories.
