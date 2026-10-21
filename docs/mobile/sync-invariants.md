[← Mobile contracts](README.md)

# Sync invariants

What must stay true of a journal however changes reach it. These are requirements on the sync design, not a description of it; the format that holds them is on the [sync format](sync-format.md) page.

## Local first

1. **A save completes locally.** Writing a reflection, changing focus, or saving a review succeeds or fails on the device alone, through the same transaction the desktop uses today. Sync is never in the path of a save and is never required to read.
2. **The Markdown is the journal.** The reflections file and the archive on each device are complete and readable without `.seneca/`, without the other device, and without the exchange folder. Losing all sync state loses history of how the files came to be, never the writing.
3. **Devices exchange changes, not files.** The live reflections file and archive are never placed in a synced folder for another device to overwrite. Each device publishes immutable transaction manifests and content-addressed snapshots, and builds its own files from them.

## What is exchanged

1. **Published history is immutable.** A manifest or a snapshot, once written, is never edited or deleted by a peer. Each is named by its content, so two devices that write the same thing write identical bytes.
2. **A device writes only what is its own.** No device rewrites another device's manifests.
3. **Missing means not yet delivered.** A file that is absent or still downloading is never read as a deletion. Every deletion is explicit.
4. **Order comes from ancestry.** A change names the changes it was made on top of. Wall-clock time is recorded for the reader and never decides which change wins.

## Merging

1. **Reflections merge by entry.** The unit is one day in one journal. A change to one day never rewrites another day's bytes, the property the baseline tests hold `sort`, `merge`, and the daily edit to.
2. **One entry per day.** Two devices that each wrote a new entry for different days merge without asking. Two devices that each wrote an entry for the same day end with one entry only when the two say the same thing; otherwise the day is a conflict, never two entries.
3. **A day merges by section, and only by section.** Two devices that changed different sections of one day both keep their change. A section is taken whole with the markers bound to it; a follow-through's source, snapshot, and response are one unit. Seneca never combines text inside a section, and never merges an entry whose headings repeat or whose structure both devices changed.
4. **The archive merges by record.** Records are append-only and identified by a hash of their content. Merging two archives is the union of their records; the same record from two devices is one record, and no record's bytes change.
5. **The archive's order comes from history.** The archive as enrolled keeps its order. A record follows every record its author could see; records added independently are ordered the same way on every device, by a rule that does not depend on which arrived first.
6. **Focus is derived, never stored.** The focus in effect on a date is computed from the event stream. Two changes for one day made in sequence resolve to the later; two made independently are a dispute, reported as one and never decided by position.
7. **A record revises by reference.** A review that supersedes another names it by ID. A merge never drops the superseded record and never invents a reference: two revisions saved independently stay two branches.
8. **What Seneca does not understand travels with what it does.** Unknown sections, foreign comments, unknown markers, and records of an unknown kind are carried byte for byte with the entry or record they belong to.
9. **Delivery order decides nothing.** The same transactions, arriving in any order and in any grouping, leave a device with the same journal and the same conflicts.

## Conflicts

1. **Nothing is discarded.** When two changes overlap, both versions are kept until the author chooses. No change is dropped because another arrived first or last.
2. **A conflict is local to what it touches.** One conflicted day does not block reading, writing, or syncing any other day, or the archive.
3. **Resolution is a change.** Choosing is recorded and exchanged like any other change, so both devices converge on the same result, whichever of them chose.
4. **Ambiguity is reported, never resolved.** The rule `seneca merge` already follows for an overlapping date.
5. **A device keeps showing what it wrote.** Until a conflict is resolved, each device's file holds its own version and the other's is held beside it. Both devices agree on which versions compete.
6. **An answer is to the conflict that was shown.** A resolution names the versions it settles. If any of them changed before it was recorded, it is refused and the author is shown the conflict as it now is. Two devices that each resolve the same conflict differently have a conflict again, not a silent winner.
7. **A deletion beside an edit is a conflict.** Neither the deletion nor the edit wins by itself, and the bytes of both sides stay held whichever is chosen.

## Files

1. **The refusals hold.** A file a local command would refuse to write (a repeated date, an undatable entry, a foreign or newer archive, a record whose ID does not match its content, a symlink) is not written by sync either. The device reports it and keeps the incoming changes until the file is repaired.
2. **External edits are imported.** A change made by an editor or by Git is detected as a difference from the last recorded state and becomes a change of its own. It is never silently overwritten by the next import.
3. **A damaged file is recovered, not published.** A journal file that is missing, empty, unreadable, or has lost most of what was recorded of it is never turned into deletions on other devices. It is left as it is until the author restores it from history or confirms the deletions.
4. **Framing is not content.** A whole-file rewrite settles the spacing around entry separators. Comparing or addressing a journal by entry is stable across that; comparing whole files is not.
5. **A newer format is held, not guessed.** A device that meets a manifest, an entry schema, or an archive schema newer than it understands keeps it and waits. It does not apply part of it and does not rewrite it.
6. **Shared settings validate before they apply.** Questions and anchor selection arriving from another device pass the same validation as `config.toml`, as one unit: an anchor index is never applied to a list of questions it was not chosen against.

## Desktop

1. **Git records what sync wrote.** When the background agent imports changes, it commits the journal files it changed, with the same path-scoped staging the commands use. Git is a record of the result and never an input to a merge.
2. **Git never decides whether a change is saved.** An import is complete when the engine's commit is; the Git commit comes after and may fail. A failed commit is remembered and retried by itself: nothing is imported, recorded, or written a second time for it.
3. **Other people's staging is not ours.** A sync commit names the journal files it is for and nothing else. Changes the author staged stay staged, and sync's own history under `.seneca/` is never staged.
4. **A repository in motion is left alone.** While a merge, rebase, cherry-pick, revert, or bisect is under way, or a journal file is unmerged, nothing is imported and nothing is committed. What arrived waits in the exchange. Publishing, which changes nothing in the repository, goes on.
5. **Reading is not syncing.** `doctor`, `show`, the history listings, and `sync status` never import, recover, publish, or commit.
6. **The background service is optional and separable.** It is installed only on request, it is only the foreground watcher run by launchd, and removing it changes nothing about the journal: every command works, and `seneca sync once` exchanges by hand.
7. **The command line is unchanged for a journal that is not synced.** A journal with no `.seneca/` directory behaves exactly as the baseline describes.
8. **A file that is not here is not read.** A file of the exchange that iCloud Drive lists and has not downloaded is asked for and treated as not arrived. No exchange blocks on the network, and a descriptor that is on its way is never taken for an empty exchange.

## Transport

1. **The journal stays on the device.** Every operation on a journal runs on local storage. Only the exchange's own immutable files cross to the folder a file service carries, each whole.
2. **Contents before manifests, in any order after.** A publisher writes a transaction's contents before its manifest. A reader assumes nothing about order: a transaction is applied when all of it has arrived intact.
3. **Published is not applied.** A device says a change reached another only on that device's own receipt, written from the state its commit left. Delivery of a manifest proves nothing.
4. **An interrupted transfer loses nothing and shows nothing.** Stopped anywhere, a publication leaves no file another device can take for a whole change, a download leaves the journal untouched, and the next exchange finishes both.
5. **A transport fault never stops a save.** A folder that is unreachable, gone, or no longer granted is reported, and writing goes on locally.
