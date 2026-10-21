# Seneca

Evening Stoic reflection tool. Prompts configurable questions, appends answers
to a Markdown file, and auto-commits to git. Reviews turn a week or a month of
those entries — or a quarter or a year of the reviews themselves — into a
synthesis and, if you accept one, a single standing focus that comes back the
next evening.

## Install

```bash
go install github.com/sglavoie/seneca@latest
```

or, from a clone, `just install`. Requires Go 1.26.0+.

The iPhone app lives under [`ios/`](ios/README.md); the website, with its
privacy policy, under [`website/`](website).

## Usage

```bash
seneca                       # Record today's reflection
seneca --date 2026-03-01     # Backfill a past date
seneca --date yesterday      # Backfill last night (also: today)
seneca --blank               # Record today with no previous context shown
seneca show                  # Show latest entry
seneca show --last 5         # Show last 5 entries
seneca show --date 2026-03-01
seneca show --month=2026-03  # --month alone means the current month
seneca show --random         # Show one entry at random
seneca show --from 2026-03-01 --to 2026-03-07   # either bound may be left open
seneca show --search patience                   # lines mentioning a phrase
seneca show --on-this-day    # earlier entries on today's day of the month
seneca export --from 2026-03-01 --to 2026-03-07
seneca export --month=2026-03  # one whole month
seneca review --harness claude
seneca review --month --harness copilot
seneca review --quarter      # Synthesize the reviews saved this quarter
seneca review --year=2026    # …or across a whole year
seneca review history        # Show the most recent saved review
seneca review history --last 5
seneca review history --period monthly --last 3
seneca usage                 # What reviews cost, per month and review kind
seneca usage --json          # The same summary as data
seneca focus                 # Show the focus in effect today
seneca focus --date 2026-03-01
seneca focus set "Protect the first hour."
seneca focus clear
seneca focus history         # Show the focus timeline, newest first
seneca insights              # What became of the last 30 days' intentions, and the focus
seneca insights --days 90    # …or another window: --from/--to, or --all
seneca settings              # Show the questions and anchor in force
seneca settings init         # Record them in the journal, for every device
seneca settings set --anchor 2
seneca settings set --question "What did I notice?" --question "What will I carry?" --anchor 2
seneca pack                  # List the built-in practice packs (also: seneca pack list)
seneca pack show step-10     # A pack's questions, reminder time, and review guidance
seneca pack apply examen     # Make a pack the journal's practice (or a *.seneca-pack.json file)
seneca pack export stoic > mine.seneca-pack.json   # Start a custom pack from a built-in one
seneca sync enroll           # Exchange this journal with your other devices
seneca sync once             # Import what arrived, commit it, publish what is new
seneca sync status           # Where sync stands, without changing anything
seneca sync conflicts        # Changes that wait for you to choose
seneca sync service install  # Keep exchanging in the background (macOS)
seneca sync leave            # Stop syncing; every file stays, the device identity retires
seneca doctor                # Check everything without changing anything
seneca doctor --diagnostics  # A content-free report to share: versions, counts, codes
seneca backup <new-dir>      # Copy all writing into a new directory, for recovery
seneca sort                  # Sort entries chronologically
seneca delete --date 2026-03-01   # Delete one day's reflection (asks first; --yes skips)
seneca merge <file>          # Merge entries from another file
seneca merge --delete <file> # Merge and remove source file
seneca open                  # Open the folder holding the journal in your editor
seneca test-notification     # Test the daily reminder notification
```

Reflections are resolved in this order: the `SENECA_FILE` environment variable,
`[seneca].reflections_path` in `~/.config/seneca/config.toml`, then the
default `~/Documents/Seneca/reflections.md`.

## Reflection questions

Configure the reflections path and prompt questions in
`~/.config/seneca/config.toml`:

```toml
[editor]
command = ["code"]

[seneca]
reflections_path = "~/Documents/Seneca/reflections.md"
questions = [
  "What did I notice today?",
  "What did I handle well?",
  "What should I carry into tomorrow?",
]
anchor_question_index = 3

[review]
harness = "claude"
model = "opus"
```

The `[review]` table is documented under
[Harness configuration](docs/review.md#harness-configuration).

If the config file, `[seneca]` table, or `questions` value is missing, Seneca
uses the default three questions. When `questions` is present, it replaces the
defaults everywhere: the TUI, saved Markdown headings, edit prefill, the
`Intention` anchor, and AI review prompts.

`reflections_path` must be an absolute path or start with `~/`. `SENECA_FILE`
still overrides it for one-off runs and scripts.

Configured questions must be non-empty, non-blank, and unique. Invalid config
fails with an error instead of falling back silently.

`anchor_question_index` is 1-based and controls which previous answer appears
in the `Intention` line. Omit it to use question 3 when available, or hide the
anchor when fewer than three questions are configured. Set it to `0` to disable
the anchor. Out-of-range indexes fail with a config error.

### Journal settings

`config.toml` belongs to one machine. To make every device that writes the
journal ask the same questions, record them in the journal itself:

```bash
seneca settings init
```

This writes `.seneca/journals/<reflections filename>/settings.json` beside the
reflections file, seeded from the questions and anchor in force at that moment
— `config.toml`, or the defaults. Nothing else is touched: not `config.toml`,
not a line of Markdown. Running it again changes nothing.

From then on the journal's settings decide the questions and the anchor.
`seneca.questions` and `seneca.anchor_question_index` stay in `config.toml`
but are no longer read for this journal; `seneca settings` and `seneca doctor`
report them as superseded. Everything else in `config.toml` — the reflections
path, the editor, the review harness — is still the machine's own. A journal
that was never initialized keeps following `config.toml` exactly as before.

`seneca settings` shows what is in force and where it comes from.
`seneca settings set` changes it:

- `--question` replaces the whole list; repeat it once per question, in order.
- `--anchor` selects the anchor question, 1-based; `0` disables it.

The questions and the anchor are validated together, by the same rules as
`config.toml`, and written together. New questions without `--anchor` keep the
anchor on the same question wherever it moved; if that question is gone, the
change is refused until `--anchor` says which one takes over.

Changing the questions never rewrites an entry. Answers stay under the
headings they were written under, and a stored follow-through keeps the
intention it answered. Entries written before `seneca settings init` go on
being read with the questions and anchor recorded at that moment.

If the questions change while a reflection is open — on another device, say —
the save is refused and the writing is kept, like any other
[refused save](#recovery). The next `seneca --date` run for that day shows the
current questions with the kept answers beside the ones that still exist, and
prints any answer whose question is no longer asked.

### Practice packs

A practice pack is a way of keeping the evening journal, packaged: the
questions, the anchor, a suggested reminder time, and guidance for reviews.
Seneca ships five — `stoic` (the default questions), `step-10`, `examen`,
`gratitude`, and `virtues` — and any other is a `*.seneca-pack.json` file.

```bash
seneca pack apply step-10
seneca pack apply ./evening.seneca-pack.json
```

Applying a pack is a settings change: the pack's questions and anchor replace
the journal's, and the pack is recorded in the journal's settings as in force
from today, on every device. A journal without settings of its own gets them in
the same step, as `seneca settings init` would have recorded them. Entries
already written keep their own questions. The reminder time is printed, not
scheduled: reminders are each device's own.

Reviews read each day under the pack in force on it: the prompt names every
pack the period covered, and its guidance. A period no pack covered gets the
same prompt as before packs existed. `seneca settings set` after a pack keeps
it as the practice, and `seneca settings` notes that the questions changed.
To make a pack of your own, `seneca pack export` a built-in one, give it a new
`id`, and edit it. [Practice packs](docs/product/practice-packs.md) has the
file format and the rules.

## The daily form

Above the questions, the form shows what carries over:

- **Focus** — the standing focus in effect on the entry's date, if any.
- **Intention** — the most recent anchor answer written *before* this date,
  labelled with the date it was written and how long ago that was
  (`Intention 2026-08-18 (3 days ago)`). It is found by date, not by file
  position, so a skipped day reaches further back rather than showing nothing.
  The age is counted from the real today, so it stays true while backfilling.
- **Follow-through** — a free-text answer to that intention. It is saved with
  the day it answers, so it keeps answering that day even after the journal
  moves on around it.

When focus or intention context is present, press `Ctrl+O` while editing or on
the pre-save review to read the complete text. Use `↑`/`k` and `↓`/`j` to
scroll; `Esc` or `Ctrl+O` returns to the form without changing it.

`--blank` hides all three for one run. It also skips reading the review archive
entirely, so a damaged archive can never block writing. Nothing is deleted:
a follow-through already stored in the entry stays on disk untouched.

`Esc` or `Ctrl+C` leaves the form. If you have written or changed anything, it
asks first — `y` discards, `n` or `Esc` goes back to writing, and a second
`Ctrl+C` discards — so one stray keystroke never costs an evening's writing.
Leaving a form you have not touched never asks.

`--date` takes `YYYY-MM-DD`, `today`, or `yesterday`, and refuses a date after
today: a reflection looks back on a day, so a future date is a typo. `seneca
show --date` and `seneca focus --date` accept the same words.

Editing an existing day reopens it with its saved answers. Only the answered
sections are rewritten; headings you added by hand stay exactly where you left
them.

## Focus

Focus is a standing intention, not a per-entry field. It is an *event stream*
in the review archive: each `set` and `clear` is a record with an effective
date, and the focus in effect on any date is the latest event on or before it.
Nothing is ever overwritten, so the history of what you were working on stays
readable.

```bash
seneca focus                       # what is in effect today
seneca focus --date 2026-03-01     # what was in effect then
seneca focus set "Protect the first hour."
seneca focus clear                 # ends the current focus; no-op if none
seneca focus history               # the whole timeline, newest first
```

`seneca focus` says when the focus was set and how long ago, so a focus that has
quietly stood for six weeks reads that way:

```console
$ seneca focus
Focus (set 2026-08-25, 40 days ago, review):
Practice patience during the morning commute.
```

A focus can be set by hand or accepted from a review. Both are recorded the
same way and differ only in an `origin` attribute.

### History

Because focus is an event stream, it can be read back *as* a timeline rather
than one date at a time.

```bash
seneca focus history             # the ten most recent events, newest first
seneca focus history --last 50   # further back
```

Each event shows its effective date, whether it was a `set` or a `clear`, where
it came from (`manual`, or `review` with the abbreviated ID of the review that
recommended it), the focus text, and the span it stood for — up to the effective
date of the event that replaced it, or `current` for the newest. Two events
sharing an effective date are both shown, with the later one winning the day
exactly as `seneca focus` resolves it. The spans are structural, not a score:
there are no counts, no percentages, and no streaks.

### Follow-through insights

```bash
seneca insights                  # the last 30 days, today included
seneca insights --days 90
seneca insights --from 2026-09-01 --to 2026-09-30
seneca insights --all            # the whole journal
```

`seneca insights` reports what became of each intention written in the window:
answered by a later day's follow-through, carried with the response left blank,
not carried by the days written after it, or still waiting for the next day. It
lists the intentions written in the same words more than once, the words that
return in the intentions of three days or more, and how long each focus stood
and what replaced it. No AI is involved and nothing is written; every line can
be checked with `seneca show --date` and `seneca focus history`. See
[Follow-through insights](docs/insights.md).

## Searching

`seneca show --search` lists the lines that mention a phrase, so a theme can
be followed through months of writing:

```console
$ seneca show --search patience
2026-08-11
  …ran late again and I lost my patience in traffic when the bridge closed…

2026-09-02
  More patience than last week.

2 entries mention "patience" in 2 lines; `seneca show --date DATE` reads one in full.
```

- Matching ignores case and treats the phrase literally; `patien` also finds
  `patient`.
- Only what you wrote is searched. Entry and question headings are skipped —
  the questions repeat in every entry — and so are Seneca's markers.
- A long line is cut to the text around its first match.
- Combine it with `--month` or `--from`/`--to` to search part of the journal.
  It cannot be combined with `--last`, `--date`, or `--random`.

`--on-this-day` shows the entries written on today's day of the month in
earlier months and years — on October 4th, every earlier 4th. Today's own entry
is left out, and a day some months lack, such as the 31st, simply matches less
often.

`--from` and `--to` select an inclusive range for `show` and accept the same
`YYYY-MM-DD`, `today`, and `yesterday` as `--date`. Either may be given alone:
`--from 2026-08-01` reads everything since then.

## Export

See [docs/export.md](docs/export.md) for exporting a date range or month as a packet.

## Sync

See [docs/sync.md](docs/sync.md) for exchanging a journal between your devices:
enrolling, the background service, conflicts, recovery, how it uses Git, and
keeping the exchange in iCloud Drive so the [iPhone app](ios/README.md#sync)
can reach it. Sync is opt-in; a journal that was never enrolled is unaffected.
[Release qualification](docs/mobile/qualification.md) is how a journal is
rolled out to sync after a trial on disposable data, and how to roll back.

## AI review and the review archive

See [docs/review.md](docs/review.md) for AI reviews, harness configuration, arc reviews, decisions, and the review archive.


## Migration and compatibility

Entries written by earlier versions have no markers. They are read as written
and are **never migrated in bulk** — `seneca doctor` reports how many there are,
and nothing rewrites them.

A legacy entry gains the v2 marker only when you edit that specific day and the
edit needs it: recording a follow-through requires markers, so that entry
becomes canonical v2 while every other day on disk stays exactly as it was.
Newly created entries are v2 from the start.

An entry marked with a schema version newer than this binary understands is
reported as a warning; a newer *archive* version is refused outright rather than
half-understood. The archive's `v=` is also required: if the
`<!-- seneca:archive -->` marker loses its version or carries one that is not a
number, writes are refused too, because a version Seneca had to guess is not a
promise anyone made about the records below it. `seneca doctor` reports it the
same way. Restore `v=3` on that marker to fix it.

An archive left at an *older* version is read as written. The first append
restamps the `<!-- seneca:archive -->` marker to the current version — that one
line, and nothing else: your prose, and every record already in the file, stay
byte-identical. A marker with no usable version is never restamped, only
refused.

The files, write paths, and command output that the iPhone work must leave
unchanged are written down in [docs/mobile](docs/mobile/README.md), together
with the synthetic journal the compatibility tests run against.

The iPhone app lives under [`ios/`](ios/README.md). It shares this engine
through a versioned bridge and is built, tested, and installed with the
`just ios-*` recipes described there.

## Recovery

Seneca is built to fail without losing writing.

- **Fail closed, and early.** The reflections file is parsed and validated
  *before* the editor opens. A file Seneca cannot safely rewrite is refused
  while you have typed nothing, never after.
- **Atomic writes.** Every write goes to a temporary file in the same directory
  and is renamed into place, so an interrupted write leaves either the old file
  or the new one — never a half-written journal. A `.bak` sibling holds the
  previous contents.
- **A landed write is never reported as a failure to write.** Flushing the
  change to disk is the only step that happens after the new file is in place.
  If it fails, Seneca still tells you what it saved and still commits it, and
  the error says the write happened and that you must not rerun the command —
  a rerun would record the same review or focus event twice.
- **Two writers never overwrite each other.** Each save takes a lock on the
  journal's directory for the instant it writes and re-reads the file under it.
  The lock is not held while you write in the form or while a review is being
  generated. If the day you are writing was saved, changed, or removed by
  another Seneca process in the meantime, your save is refused rather than
  written over it.
- **Writing a save could not store is kept.** When a daily save fails or is
  refused, your answers are kept under `.seneca/` beside the journal and the
  message says where. Run `seneca --date <day>` again and they are back in the
  form; they are removed once the day is saved. A review the archive refused
  keeps its text the same way.
- **Symlinks are refused.** Seneca will not write through a symlink; it reports
  the destination instead.
- **Nothing partial.** A refused merge, a rejected review response, and a
  cancelled decision all leave every file byte-identical, including the source
  file of a merge.
- **Duplicate dates block writing.** Two `## YYYY-MM-DD` headings for the same
  day make an edit ambiguous, so writes are refused until you resolve it by
  hand. Reads still work.
- **Only exact date headings name a day.** An entry heading must be exactly
  `## YYYY-MM-DD`. A heading such as `## 2026-05-26 travel notes` is not read as
  that day: Seneca reports it and refuses to write the file rather than guess,
  so `seneca --date 2026-05-26` can never rewrite it. Its text is
  preserved exactly as you wrote it. This includes an invisible suffix — a
  trailing space or tab after the date is still extra text, and the reported
  heading is quoted so you can see what is actually on the line. The rule holds
  wherever the heading sits, including as the last line of the file or of an
  entry with nothing written under it yet: Seneca trims the blank lines that
  separate entries, never the bytes of the heading itself.
- **Git is optional.** Without git, or outside a repository, files are still
  written and the commit is skipped with a warning.
- **A whole copy, when you ask for one.** `seneca backup <new-dir>` writes the
  reflections file and the archive byte for byte, the journal's settings, every
  version of every open sync conflict, and unsaved drafts into a new directory,
  with a manifest of digests. It appears whole or not at all, and carries no
  sync identity: a journal restored from it joins sync as a new device. See
  [recovery](docs/mobile/recovery.md) for restoring and for replacing a device.
- **Sync never stands between you and a save.** On a synced journal, importing,
  committing what was imported, and publishing are each separate from your own
  write: a failure in any of them is a warning, and is retried later. See
  [docs/sync.md](docs/sync.md).

## Doctor

`seneca doctor` inspects configuration, the reflections file, and the review
archive, and reports what it finds. It is strictly read-only: it creates
nothing, repairs nothing, commits nothing, and runs no AI harness.

```console
$ seneca doctor
Seneca doctor

  ok    config       3 questions configured in ~/.config/seneca/config.toml; anchor question 3 is "What could I do differently tomorrow?"
  ok    config       reflections path ~/reflections.md (from seneca.reflections_path)
  ok    paths        directory ~ exists with mode 0755
  ok    reflections  412 entries parsed from ~/reflections.md
  info  reflections  7 entries predate the v2 marker; they are read as written and are never migrated in bulk
  info  reflections  latest entry 2026-09-30 (4 days ago)
  ok    archive      12 saved reviews and 9 focus events in ~/seneca-reviews.md
  info  archive      latest saved review: weekly review, 2026-08-17 to 2026-08-23 (42 days ago)
  ok    archive      every record id matches its content
  ok    archive      focus active since 2026-08-24 (41 days ago, review): Protect the first hour.
  info  archive      18 entries written since the latest review ended 2026-08-23 (42 days ago); `seneca review --month=2026-09` takes stock of the latest
  warn  git          git is at /opt/homebrew/bin/git but ~ is not in a repository; commits are skipped
  info  harness      no harness configured; `seneca review` needs --harness, SENECA_HARNESS, or review.harness

No errors, 1 warning
```

Severities read the way they sound:

- **error** — corrupt or ambiguous data, or invalid configuration. Something is
  refusing to work. `doctor` exits non-zero.
- **warn** — an optional integration is unavailable (git, the harness), or a
  file's permissions may refuse a later write.
- **info** — normal, worth knowing: legacy entries, no archive yet, no active
  focus, entries out of date order, writing no review has seen yet.

Dates come with how long ago they were. When the newest saved review's window
ended more than 31 days ago and entries were written after it, doctor says how
many and names the month to review — a monthly cadence never triggers it. The
same line is printed after saving a reflection (except under `--blank`, which
never reads the archive), so the reminder arrives where the writing happens.

When the journal has [settings of its own](#journal-settings), a second
`config` line names them as the ones in force, and an `info` line follows for
each `config.toml` key they supersede. A settings document that cannot be read
is an error.

Exit status is `0` for a clean or warning-only report and non-zero when there is
at least one error, so it can be used in a health check.

On a journal that takes part in sync, a `sync` group says where it stands and
what to do about each thing that waits: changes not sent or not imported,
changes from a newer seneca, conflicts, a file sync stopped reading, a sync
state or exchange written by a newer seneca, and this device's identity used by
another installation. A journal outside sync reads as it always has. The table
is in [Reminders, diagnostics, and recovery](docs/mobile/recovery.md#doctor-and-status).

`seneca doctor --diagnostics` prints a report to share instead: format versions,
counts, and stable codes, with no path, question, date, name, or anything
written in the journal. The iPhone's Share Diagnostics sends the same report.

The `harness` line names the source that supplied the harness — `SENECA_HARNESS`
or `review.harness` — the way the `config` line names the source of the
reflections path. Doctor has no `--harness` flag of its own, so it reports what a
`seneca review` with no flags would use; a `--harness` on the review still
outranks both. An invalid `[review].harness` is reported as an error here.

## Daily reminder

A LaunchAgent asks Seneca to show a native macOS alert at 20:00, 21:00, 21:30,
and 22:00 if the configured reflections file has no entry for the day. Once an
entry is saved, later reminders are suppressed even if it has not been committed.

The iPhone app has the same reminders, off until turned on, at the same times
unless you choose others. A reflection saved on the Mac cancels the phone's
reminder only once the phone has received it through sync; see
[Reminders, diagnostics, and recovery](docs/mobile/recovery.md).

### Setup

`seneca test-notification` shows the alert only when today has no entry, so the
reminder is a LaunchAgent that runs it at each time. Save this as
`~/Library/LaunchAgents/io.sglavoie.seneca.remind.plist`, adjusting the path if
`go env GOPATH` is not `~/go`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>io.sglavoie.seneca.remind</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/sh</string>
        <string>-c</string>
        <string>"$HOME/go/bin/seneca" test-notification</string>
    </array>
    <key>StartCalendarInterval</key>
    <array>
        <dict><key>Hour</key><integer>20</integer><key>Minute</key><integer>0</integer></dict>
        <dict><key>Hour</key><integer>21</integer><key>Minute</key><integer>0</integer></dict>
        <dict><key>Hour</key><integer>21</integer><key>Minute</key><integer>30</integer></dict>
        <dict><key>Hour</key><integer>22</integer><key>Minute</key><integer>0</integer></dict>
    </array>
    <key>StandardOutPath</key>
    <string>/tmp/seneca-remind.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/seneca-remind.log</string>
</dict>
</plist>
```

Then load it:

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/io.sglavoie.seneca.remind.plist
```

### Verify

```bash
launchctl list | grep seneca        # confirm agent is loaded
seneca test-notification            # the same check the agent runs
cat /tmp/seneca-remind.log          # check logs after a scheduled run
```

## License

[MIT](LICENSE). The website's fonts, Literata and Marcellus, are under the SIL
Open Font License; their licenses are beside them in `website/fonts/`.
