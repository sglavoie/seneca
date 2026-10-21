[← Mobile contracts](README.md)

# CLI compatibility

The command line is the desktop interface and stays one. Moving the engine behind a boundary must not change what a command accepts, prints, or touches.

## Surface

Every command and flag is listed in `commandSurface` in `cmd/command_test.go`. Adding, renaming, or removing one is a deliberate edit to that table and to the README.

| Command | Reads | Writes |
| --- | --- | --- |
| `seneca` | configuration, journal settings, reflections file, archive | reflections file |
| `seneca show` | reflections file | nothing |
| `seneca export` | reflections file | nothing |
| `seneca focus` | archive | nothing |
| `seneca focus history` | archive | nothing |
| `seneca insights` | configuration, journal settings, reflections, archive | nothing |
| `seneca focus set` | archive | archive |
| `seneca focus clear` | archive | archive, and nothing when no focus is active |
| `seneca review` | configuration, reflections file, archive, journal settings, the pricing table | archive, only when the author saves; one line in the usage ledger for every attempt that reached the harness |
| `seneca review history` | archive | nothing |
| `seneca usage` | configuration, the usage ledger | nothing |
| `seneca sort` | reflections file | reflections file, and nothing when already in order and in canonical form |
| `seneca delete` | reflections file | reflections file, only when confirmed |
| `seneca merge` | reflections file, source file | reflections file; the source file with `--delete` |
| `seneca settings` | configuration, journal settings | nothing |
| `seneca settings init` | configuration, journal settings | journal settings, and nothing when they already exist |
| `seneca settings set` | configuration, journal settings | journal settings, and nothing when they already say it |
| `seneca pack` | configuration, journal settings | nothing; it lists the built-in packs, as `seneca pack list` does |
| `seneca pack list` | configuration, journal settings | nothing |
| `seneca pack show` | the pack file named, if any | nothing |
| `seneca pack apply` | configuration, journal settings, the pack file named, if any | journal settings, and nothing when the pack is already in force with its questions |
| `seneca pack export` | nothing | nothing; the pack goes to standard output |
| `seneca sync` | nothing | nothing; it prints its subcommands |
| `seneca sync enroll` | configuration, journal settings, reflections file, archive, the exchange | sync history, the journal settings when there were none, the exchange; with `--join`, also whatever an import writes |
| `seneca sync once` | configuration, reflections file, archive, journal settings, sync history, the exchange, the repository's state | whatever an import writes, sync history, the exchange, a Git commit |
| `seneca sync status` | configuration, sync history, the exchange's file names, the repository's state | nothing |
| `seneca sync conflicts` | sync history, archive | nothing, unless an interrupted save was waiting to be completed |
| `seneca sync resolve` | the same as `seneca sync once` | the journal file holding the conflict, or the archive for a disputed focus; sync history; the exchange |
| `seneca sync recover` | sync history, the journal file named | that journal file, sync history, the exchange |
| `seneca sync leave` | sync history | sync history, moved aside under `.seneca/journals/<reflections filename>/sync-left-<time>/`, and the directory's sync claim; no journal file |
| `seneca sync watch` | the same as `seneca sync once`, repeatedly | the same as `seneca sync once`, repeatedly |
| `seneca sync service` | nothing | nothing; it prints its subcommands |
| `seneca sync service install` | configuration, sync history | the LaunchAgent, and launchd's list of agents |
| `seneca sync service remove` | nothing of the journal | the LaunchAgent, and launchd's list of agents |
| `seneca doctor` | configuration, journal settings, reflections file, archive, directory, `PATH`, sync history, the exchange, the usage ledger, the pricing table | nothing; `--diagnostics` prints a content-free report instead |
| `seneca backup` | configuration, reflections file, archive, journal settings, kept drafts, sync history | a new directory the author names, outside the journal; nothing in the journal |
| `seneca open` | configuration | nothing itself; it launches the editor |
| `seneca test-notification` | reflections file | nothing; it shows a macOS alert when today has no entry |

Each write is detailed in the [write-path inventory](write-path-inventory.md).

A write that succeeds leaves the same files it always has. Two things are new, and both appear only when a save does not go through: a daily save refused because its day changed while the form was open, or failed outright, keeps the author's answers as a draft under `.seneca/` and prints where; the next `seneca` run for that day prints that it restored them. A review the archive refused keeps its text the same way. A command that cannot take the journal lock within ten seconds fails with nothing written.

One thing is new on the desktop for every review: `seneca review` appends a content-free line to `.seneca/journals/<reflections filename>/desktop/usage.jsonl` for each attempt that reached its harness, saved or not, and warns rather than fails when it cannot. Nothing in the journal changes because of it, and `seneca usage` reads it without writing.

Journal settings change nothing for a journal that has none: `config.toml` supplies the questions and the anchor, every command prints what it printed, and no command creates the settings as a side effect. Only `seneca settings init` does, and `seneca pack apply`, which records the settings the journal had as `settings init` would and the pack's as the ones in force. A journal that never applies a pack keeps a version 1 settings document, byte for byte. After it, the daily form asks the journal's questions, `seneca doctor` adds a line naming the settings in force and one for each `config.toml` key they supersede, and a daily save made under questions that changed while the form was open is refused and kept like any other stale save.

## Output

The files under `cmd/testdata/compat/golden/` are the output of the read-only commands for the fixture journal, byte for byte:

| Golden | Command |
| --- | --- |
| `show.txt` | `seneca show` |
| `show-last-2.txt` | `seneca show --last 2` |
| `show-date.txt` | `seneca show --date 2026-03-05` |
| `show-month.txt` | `seneca show --month=2026-03` |
| `show-range.txt` | `seneca show --from 2026-03-03 --to 2026-03-04` |
| `show-search.txt` | `seneca show --search walk` |
| `export-month.md` | `seneca export --month 2026-03` |
| `export-range.md` | `seneca export --from 2026-03-04 --to 2026-03-05` |
| `focus.txt` | `seneca focus --date DATE` for four dates, read on 2026-04-02 |
| `focus-history.txt` | `seneca focus history --last 10` |
| `review-history.txt` | `seneca review history --last 3` |
| `review-history-monthly.txt` | `seneca review history --period monthly` |
| `doctor.txt` | `seneca doctor`, read on 2026-04-02 with no Git and no harness |

These are the plain forms, what a pipe or a file receives. On a terminal `show` hides Seneca's markers and adds styling, and `focus history` and `review history` add styling; that rendering is covered by the existing tests for each command.

Other behavior that scripts and habits rely on, each already held by an existing test:

- Errors are printed once, on standard error, by `main`, and the process exits 1. A failure about the journal prints no usage text.
- `seneca export` writes its packet to standard output and nothing else there, so a refusal can never be mistaken for a packet.
- `seneca doctor` exits nonzero only when it reports an error. Warnings and information are a success.
- Commit messages have the forms listed in the write-path inventory.
- A refused write says that nothing was written.

## Read-only behavior

The commands in the table that write nothing hold to that in every case the baseline tests:

- They create no file and no directory: no journal, no archive, no `.bak`, no configuration file, no `.seneca/`.
- They do so whether the journal exists or not. A missing archive is reported as a fact, never created to make the report possible.
- They run no AI harness and make no Git commit.
- `seneca focus clear` with no active focus, and `seneca sort` on a journal already in order and in canonical form, are reads in effect and leave the directory untouched.

A read-only command stays read-only once sync exists. Showing an entry, a review, or the focus never imports, publishes, or records anything, and neither do `seneca doctor`, the history listings, `seneca settings`, or `seneca sync status`: a change waiting in the exchange, an edit waiting in a file, a save that was interrupted, and an import Git has not committed are all left exactly as they are. `seneca sync status` reads the sync state file directly for that reason, and says so when an interrupted save means its report is one save behind.

The commands that write do more on a synced journal, and only there. Each one first imports what other devices have already delivered, commits that to Git as `seneca: sync import`, then does what it always did, then publishes. None of the three added steps can fail the command: a problem is a warning on standard error, and the command's own write and commit are what they were. On a journal that is not enrolled the added steps are one failed `stat`, and the output is the baseline's.

On a synced journal, `seneca focus` and the daily form report a focus two devices changed independently for the same day as disputed, with what each of them set, instead of the focus text; `seneca focus set` settles it. A journal that is not synced never has a disputed focus, and its output is the baseline's.
