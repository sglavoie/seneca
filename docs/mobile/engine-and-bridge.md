[← Mobile contracts](README.md)

# Engine and bridge

## Who owns what

Go owns everything that decides what a journal means or what is written to it. Swift owns everything the reader sees and everything Apple provides.

| Go engine | Swift app |
| --- | --- |
| Parsing and rendering the reflections file and the review archive | Screens, navigation, text input |
| Entry rules: one entry per day, markers, anchor role, follow-through snapshots | App lifecycle and background tasks |
| Review and focus rules: records, revisions, the focus timeline | Apple file access: security-scoped bookmarks, file coordination, iCloud Drive availability |
| Settings validation: questions and anchor selection | Notifications and reminders |
| Persistence: validation, backup, atomic replacement | Device-local preferences |
| Reconciliation, conflicts, and sync history | Presenting a conflict and collecting the author's resolution |

Swift never parses a reflections file, an archive, or a marker, and never assembles Markdown for one. It asks the engine and renders the answer. If a screen needs something the engine does not return, the engine gains a field; the app does not gain a parser.

AI review generation stays on the desktop. The engine reads and renders saved reviews on both platforms, and no mobile operation starts a harness.

## The engine today

The engine is the `engine` package. The `cmd` package is the desktop adapter around it: Cobra commands, the terminal form, Git, the external editor, and the AI review harness. `engine` imports none of those, and a test holds it to that: its dependency tree has no terminal UI library, no command-line framework, and no subprocess API.

A caller opens an `engine.Journal` with everything it would otherwise have found for itself:

| Input | Field of `engine.Options` | The desktop supplies |
| --- | --- | --- |
| Where the reflections file is | `ReflectionsPath` | `SENECA_FILE`, then `seneca.reflections_path`, then the default path |
| Where the review archive is | `ArchivePath` | Left empty: the archive beside the reflections file |
| This device's questions and anchor | `Settings` | The validated `[seneca]` table of `config.toml`. In force only until the journal has settings of its own, and what those are seeded from |
| The current time | `Clock` | The instant the command started |
| The filesystem writes go through | `FS` | The real one; tests replace one stage to force a failure |
| How long a write waits for another writer | `LockWait` | Left empty: ten seconds |

Opening touches no file. Each operation reads what it needs when it runs.

| The caller wants to | `engine.Journal` |
| --- | --- |
| Read the entries, refusing a file a write would refuse | `Entries` |
| Read either document whatever its state, with diagnostics | `ReadReflections`, `ReadArchive` |
| Know whether each document would accept a write, and why not | `Validate` |
| Show a day's context: focus and the intention being followed through | `Context` |
| Start a reflection: stored answers, context, and the refusals that must come before any writing | `PrepareReflection` |
| Save it | `SaveReflection` |
| Know which questions and anchor are in force, and whether the journal or the device supplies them | `EffectiveSettings` |
| Give the journal settings of its own, seeded from the device's | `InitializeSettings` |
| Change the questions, the anchor, or both, as one unit | `UpdateSettings` |
| Make a practice pack the journal's practice from today | `ApplyPracticePack` |
| Reorder the journal or fold another file into it | `Sort`, `Merge` |
| Read the focus in effect and the focus timeline | `Focus` |
| Change the focus | `SetFocus`, `ClearFocus` |
| Read saved reviews | `Reviews` |
| Archive an approved review, with the focus it changed | `SaveReview` |
| Change several files as one unit: Markdown with settings, sync records, or receipts | `Commit` |
| Finish a commit a crash interrupted | `Recover`, which every read and every `Commit` already calls |
| Keep, find, and drop writing a save could not store | `RetainDraft`, `RetainedDraft`, `DiscardDraft`, `RetainReviewDraft` |
| Make this device the first of a synced journal, or another device of one | `EnrollSync`, `JoinSync` |
| Read this device's sync history: identities, heads, and what is pending | `SyncStatus` |
| Send what this device recorded; bring in what arrived | `PublishSync`, `ImportSync` |
| Record what an editor or Git changed, without an exchange | `RecordExternalEdits` |
| List what waits for the author, with every version held | `SyncConflicts`, `FocusDisputes` |
| Answer a conflict or a disputed focus | `ResolveSyncConflict`, `ResolveFocusDispute` |
| Restore a missing or damaged journal file from history, or accept its deletions | `RecoverSyncFile` |

The boundary has these properties.

- **No terminal.** Engine functions take their inputs as arguments and return values or errors. They do not read standard input, print, or consult the terminal. `ensureDir`, which asks before creating a directory, stays in `cmd`; so does every line a command prints.
- **No ambient environment.** The reflections path, the clock, and the settings are passed in. `cmd` reads `HOME`, `SENECA_FILE`, `config.toml`, and `time.Now()` and hands the results over.
- **No Git.** Committing is a desktop concern, performed by the caller after the engine reports what it wrote. `gitCommitPaths` is called by the commands, never by the engine.
- **One mutation authority.** The transaction types in `engine/persistence.go`, `engine/persistence_validate.go`, and `engine/archive_transaction.go` stay the only code that writes a journal file. The bridge calls the same `engine.Journal` operations the commands call. There is no bridge-only write path.
- **One writer at a time.** Every mutation runs inside `Journal.Commit`, which takes the journal lock, completes any interrupted commit, re-reads every file it will replace, and releases the lock before it returns. Nothing a journal hands out holds the lock, so it is never held while a person writes or a harness runs. A writer that cannot get it in time fails with nothing written.
- **Edits carry their base.** `PrepareReflection` returns the revision of the day's entry as it was shown, and `SaveReflection` refuses to write over any other: a `StaleReflectionError`, with nothing written. The revision is a digest of the entry's own bytes, so changes to other days, a re-sort, or a reframing of the file do not make a draft stale. `FileTransaction.RequireBase` is the same check for a whole file, for metadata.
- **Drafts carry their questions.** `PrepareReflection` also returns the revision of the questions and anchor the form is shown under, and `SaveReflection` refuses a draft when they have changed: a `StaleSettingsError`, with nothing written. The caller keeps the writing and prepares again, which lines the answers up with the current questions by their text; `RetainedDraft.Unasked` lists the answers that no longer have a question. The revision is a digest of the settings' content, so initializing a journal with the questions a form was already opened under does not make it stale.
- **Questions and anchor are one unit.** `UpdateSettings` takes the revision the change was decided on and refuses any other, validates the result whole, and writes it whole. A change to the questions that says nothing about the anchor keeps the anchor on the same question, and is refused with an `AnchorMovedError` when that question is gone. Sync treats the unit the same way: two devices that changed it from one base conflict as a whole, never field by field.
- **Settings change no history.** No settings operation reads or writes Markdown. Answers are found by their heading text and follow-through snapshots are stored in their entry; the anchor of an entry without a role marker is read by the settings the journal was initialized with, not the current ones.
- **Several files commit together or not at all.** A commit that stages more than one file records its intent before replacing any of them. A failure before that record leaves every file untouched; a crash after it is finished by the next operation on the journal. A commit that passed its commit point and could not finish reports a `PendingCommitError`, which, like a committed-write error, means "do not retry".
- **A save records its own transaction.** On an enrolled journal every `Commit` adds the manifest of what it changed, and the state that follows, to the same commit. Recording never fails a save: when the history cannot be read, the files are written without it and the change is recorded later as one found in the files. The [sync format](sync-format.md) page has the rules.
- **The exchange is an interface.** `ImportSync` and `PublishSync` take a `SyncTransport`; `DirectoryTransport` is the one implementation, a directory. The engine reads the exchange before it takes the journal lock and never holds the lock across it.
- **Conflicts are answered by identity.** `SyncConflicts` and `FocusDisputes` name each conflict by the versions competing in it. `ResolveSyncConflict` and `ResolveFocusDispute` take that name and refuse, with a `StaleConflictError` and the journal untouched, when the versions are no longer those: the caller lists again and asks again. A disputed focus is reported by `Focus` and `Context` as disputed, with its candidates and no text; no caller is handed a winner.
- **No process-wide state.** The filesystem seams travel with each journal and each transaction, so two journals in one process cannot affect each other.
- **The same refusals.** A file the desktop refuses to write, the phone refuses to write, with the same reason.
- **A write that landed is reported as one.** When a write reaches the file and only the final directory flush fails, the operation returns both what it wrote and a committed-write error, so the caller finishes as it would on success instead of writing twice.

Two things are deliberately still on the desktop side. The read-only commands (`show`, `export`, `doctor`, `test-notification`, and the history listings) read the file themselves and hand the bytes to the engine's parsers, which keeps each command's own wording for a file it cannot read. Review generation — window selection, the prompt, the harness, and parsing its answer — is not engine code at all; the engine only archives the approved result.

The command line keeps its output and its exit behavior exactly; see [CLI compatibility](cli-compatibility.md).

## Bridge versioning

The bridge is the only way Swift reaches the engine. It is narrow and versioned as one thing.

- **One integer.** The engine reports the highest bridge version it speaks. Every request states the version it was written for, and every response repeats it.
- **Requests and responses are documents.** One request in, one response out, each a self-describing value that names its operation. No Go type crosses the boundary.
- **A bridge version is a closed set of operations and shapes.** Adding an operation, a field, or an error code makes a new version. A version, once shipped, is not edited.
- **Strict decoding.** An unknown operation, an unknown field, or a version higher than the engine speaks is an error response, never a guess. The app and the engine ship together, so strictness costs nothing and catches drift.
- **Errors are data.** The engine never panics across the boundary. Each failure has a stable code, a message written for the author, and whether retrying can help. The refusals the command line already makes keep their wording as the message.
- **Mutations are conditional.** A mutating request names the state it was computed against. If the file has changed since, the engine refuses rather than overwriting, the same re-read-before-write rule the daily form already follows.
- **The bridge version is not a file format.** The reflections entry schema, the archive schema, and the sync format each have their own version and change on their own schedule. A device advertises all of them.

The operations themselves, with their error codes, are on the [bridge](bridge.md) page. They are held to this page and to the baseline: whatever the bridge returns for the fixture journal must agree with what the commands print for it.

## Identity

| Thing | Identified by | Notes |
| --- | --- | --- |
| A journal | A journal ID assigned once and stored in its metadata directory | Independent of the file's name and path, so a renamed or moved journal is the same journal. |
| A device | A device ID assigned once per installation | Never shared between installations. |
| A reflection | Journal and date | One entry per day is an existing rule; a second entry for a date is a conflict, never a second record. |
| A saved review or a focus event | Its existing archive record ID | A SHA-256 of the record's kind, creation time, metadata, and content. Two devices that wrote the same record wrote the same ID. |
