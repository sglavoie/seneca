# Sync

Seneca can exchange a journal between your own devices. Every device keeps its
own Markdown, works offline, and saves locally first. Changes travel as small
immutable files through a directory a file service (iCloud Drive, for one)
carries between the devices. There is no server.

Sync is opt-in. A journal that was never enrolled behaves exactly as it always
has, and nothing below applies to it.

## Setting it up

Name the exchange directory on each machine in `~/.config/seneca/config.toml`:

```toml
[sync]
exchange = "~/Library/Mobile Documents/com~apple~CloudDocs/Seneca"
```

It must be an absolute path or start with `~/`. The `SENECA_SYNC_EXCHANGE`
environment variable overrides it, the way `SENECA_FILE` overrides the
reflections path. The path is this machine's own and is never shared.

Then, on the device that holds the journal:

```bash
seneca sync enroll                 # start the synced journal and publish it
seneca sync enroll --name "Mac"    # …with a label for this device
```

and on every other device, once the first publish has arrived:

```bash
seneca sync enroll --join
```

`seneca sync enroll` refuses to start a second journal in an exchange that
already carries one, and `--join` refuses when the exchange is still empty, so
the two cannot be confused by a slow delivery. Enrolling gives the journal
[settings of its own](../README.md#journal-settings) when it has none, so every
device asks the same questions. One journal per directory can be enrolled.

## Exchanging

```bash
seneca sync once      # import what arrived, commit it, publish what is new here
seneca sync status    # where things stand; changes nothing
```

One exchange is four steps, each standing by itself:

1. **Look at the repository.** While Git is in the middle of a merge, a rebase,
   a cherry-pick, a revert, or a bisect, or a journal file is unmerged, nothing
   is imported. What arrived waits in the exchange.
2. **Import.** Changes from other devices are applied to the Markdown. An edit
   you made by hand is recorded first and is never overwritten.
3. **Commit.** The journal files the import replaced are committed as
   `seneca: sync import`, and only those files: anything else you have staged
   stays staged and uncommitted, and the history Seneca keeps under `.seneca/`
   is never staged. If the commit fails, the import still stands; the commit is
   retried by the next exchange and nothing is imported twice.
4. **Publish.** This device's own changes are written into the exchange
   directory. Seneca does not wait for the file service to deliver them.
5. **Say what arrived.** Once everything imported is in the Markdown, and in Git
   when the journal is in a repository, this device writes a small receipt into
   the exchange saying how far it has applied each device's changes. That is how
   your phone knows a reflection reached the Mac. While an import's commit is
   failing, the receipt waits with it.

Every command that changes the journal — the daily form, `focus set`,
`focus clear`, `review`, `sort`, `merge`, `settings init`, `settings set` — runs
steps 1 to 3 before it prepares its edit and step 4 after it saves. A problem
in any of them is a warning; it never stops the command or its own commit.

The commands that only read — `show`, `export`, `doctor`, `focus`,
`focus history`, `review history`, `settings`, and `sync status` — never
import, recover, publish, or commit.

`seneca sync status` shows where this device's own changes stand:

```text
Exchange: ~/Library/Mobile Documents/com~apple~CloudDocs/Seneca
  not published yet: 0
  not imported yet: 0
  applied on "iPhone": 12 of 12 recorded here
```

A device is listed once it has written a receipt. "Applied" is that device's own
word that the change is in its journal; a file merely reaching its folder does
not count.

## An exchange folder in iCloud Drive

The exchange can be a folder in iCloud Drive, which is how the
[iPhone app](../ios/README.md#sync) reaches it:

```toml
[sync]
exchange = "~/Library/Mobile Documents/com~apple~CloudDocs/Seneca"
```

iCloud Drive lists a file before it has downloaded it, and may send a file back
to iCloud to reclaim space. Seneca never reads such a file. Each exchange asks
iCloud for the ones it finds (`brctl download`) and imports what they carry once
they are on this Mac:

```text
warning: sync: 2 files of the exchange are still downloading from iCloud; what they carry is imported once they are here
```

`seneca sync status` shows the same count and asks for nothing. The watcher
retries by itself. If the folder's own description of the journal is still
downloading, nothing is exchanged yet, and `seneca sync enroll` refuses rather
than start a second journal in the folder.

## Leaving, replacing a device, and restoring

`seneca sync leave` takes the journal out of sync: every file stays as it is,
the sync history moves aside under `.seneca/`, and this device's identity is
retired. `seneca sync enroll --join` then makes it a new device of the journal.
Do that on any Mac that got its `.seneca/` by copying another's, so two devices
never share an identity; `seneca doctor` reports one that does. Backing up with
`seneca backup`, replacing a device, and restoring are in
[Reminders, diagnostics, and recovery](mobile/recovery.md).

## In the background

```bash
seneca sync watch                           # exchange until interrupted
seneca sync watch --poll 5s --interval 10m
seneca sync service install                 # keep it running with launchd
seneca sync service remove
```

`seneca sync watch` stays in the foreground and runs an exchange whenever the
journal's files or the exchange directory change, checked every `--poll`
(default 2s), and every `--interval` (default 5m) whether anything was seen to
change or not. A step that fails, an import that was deferred, and a delivery
that is still arriving are retried after one poll, then two, then four, and so
on, never waiting longer than the interval. It holds the journal's lock only
for the instant of each write, like any command.

`seneca sync service install` writes a LaunchAgent
(`~/Library/LaunchAgents/io.sglavoie.seneca.sync.plist`) that runs
`seneca sync watch` while you are logged in and logs to
`~/Library/Logs/seneca-sync.log`. The journal must be enrolled and an exchange
configured. The agent records `SENECA_FILE`, `SENECA_SYNC_EXCHANGE`, and `PATH`
as they are when you install it; reinstall after changing them. macOS may ask
you to allow `seneca` to reach the exchange folder the first time.

`seneca sync service remove` stops the agent and deletes that file, and nothing
else. The journal, its history, and every command work as before, and
`seneca sync once` exchanges by hand.

## Conflicts

Two devices that change different days, or different answers of the same day,
are merged without asking. When both change the same answer, your file keeps
this device's version and the other is held beside it:

```bash
seneca sync conflicts                       # every version of each conflict
seneca sync resolve 1a2b3c --keep ours
seneca sync resolve 1a2b3c --keep theirs    # or the start of a version's id
seneca sync resolve 1a2b3c --file merged.md # write something else instead
```

A focus two devices set for the same day is disputed rather than decided by
whichever arrived last:

```bash
seneca sync resolve 4d5e6f --keep 9a8b7c    # one of the competing changes
seneca sync resolve 4d5e6f --text "Protect the first hour."
seneca sync resolve 4d5e6f --clear          # leave the day with no focus
```

`seneca focus set` settles it too. Whichever version is kept, the others stay
in the journal's history, and the answer travels to the other devices. An
answer to a conflict that changed since you looked is refused; list the
conflicts again.

## A file that went missing

A journal file that disappears, is emptied, or loses most of its entries at
once is not turned into deletions on your other devices. `seneca sync status`
names it, and you say which it was:

```bash
seneca sync recover reflections --restore            # put back what it lost
seneca sync recover archive --accept-deletions       # they really were deleted
```

The file is one of `reflections`, `archive`, or `settings`; the settings can
only be restored.

## Git

Sync commits are ordinary commits naming only the reflections file, the review
archive, and the journal's `settings.json`. Everything else under `.seneca/` is
this device's own bookkeeping; add it to `.gitignore` if the untracked files
bother you, but keep `settings.json` tracked:

```gitignore
.seneca/directory/
.seneca/journals/*/sync/
.seneca/journals/*/drafts/
.seneca/journals/*/desktop/
```

Git is a record of what sync wrote, never an input to it: pulling a commit
that changes the journal is read as an edit made on this device, like any
other.
