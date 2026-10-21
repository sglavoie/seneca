[← Mobile contracts](README.md)

# Release qualification and personal rollout

This page is how Seneca for iPhone and the Mac's sync are qualified before the author's own journal takes part, how that journal is rolled out, and how to go back. `scripts/qualification.py` (run as `just mobile-qualify …`) keeps the record of each step; `bash scripts/verify_mobile_session.sh 13` is the gate that holds it.

Nothing here touches the author's journal until [Rolling out the journal](#rolling-out-the-journal), and even then the program only looks: backing up, enrolling, joining, and turning on background sync are done by hand, in the order below.

## What qualifies a release

A release qualifies when all of these hold for the sources as they are now:

1. **The suites.** `go vet`, the whole Go suite with none skipped, the Go suite under the race detector, the release tests (a journal nine times a few months' worth crossing both ways byte for byte, and the rollback below carried out on the Mac in order), the bridge build, and the hosted Swift suite all run in the gate. The UI suite, the simulator round trip across a reinstall, and the sync rehearsal take about an hour together, so `just mobile-qualify suites` runs them in full and records the result (`docs/mobile/evidence/session-13-suites.json`); the gate holds that record, or runs them itself with `SENECA_MOBILE_SUITES=live`.
2. **The device records of sessions 8, 10, and 12**, made again from these sources: `just ios-device-evidence`, `just ios-sync-evidence`, `just ios-notify-evidence`.
3. **The qualification record** (`docs/mobile/evidence/session-13-qualification.json`): a signed build of these sources installed on a physical iPhone, the installed `seneca` built from these sources, every [device check](#device-checks) passed on that build, no open [defect](#defects), a [trial](#the-multi-day-trial) of at least three checkpoints on three days over 48 hours with both copies agreeing every time, the [rollback rehearsed](#rehearsing-the-rollback) before the author's journal was enrolled, and every [rollout](#rolling-out-the-journal) step in order.

Every record names the fingerprints of the app, the bridge, and the `seneca` program it was made from. Any later change to them makes it stale, and the gate says so rather than holding it: a change during the trial means installing the new build and taking the trial's checkpoints again. A simulator never stands in for the iPhone; the checker refuses a record from one.

## The suites

```bash
just mobile-qualify suites        # hosted, UI, round trip, and sync rehearsal on a simulator (~1 h)
just mobile-verify 13             # the gate: the records first, then everything else
```

`suites` records the run only when every test the Swift sources declare ran and passed with none skipped, the round-trip and rehearsal records hold, and the sources did not change during the run.

## The iPhone build

```bash
just mobile-qualify install       # build these sources, signed, and install over the app (data kept)
just install                      # the Mac: install seneca from these sources
just mobile-qualify cli           # check the installed seneca is built from these sources
```

`install` builds the app the way `just ios-install` does, installs it on the paired iPhone, and records the build, the device, and the build it replaced. A record from other sources is kept aside under `ios/.cache/results/` and a new one starts. Each later step asks the iPhone (over the cable or the network, unlocked) which build it runs and refuses a build not in the record.

## Device checks

Do each one on the iPhone with the recorded build, then record what was done and seen:

```bash
just mobile-qualify mark airplane-mode passed --note "saved 3 days offline; all on the Mac 2 min after"
just mobile-qualify mark accessibility failed --note "Save not reachable with VoiceOver in Focus"
```

`just mobile-qualify checks` lists them. A failed check needs a [defect](#defects) and, once fixed, a new build and the check done again. The trial journal is the one to use for all of them.

### airplane-mode

Turn on Airplane Mode. Write and save a reflection, change an older day, and leave a draft open; force-quit and reopen; everything is there and Sync says what waits. Turn Airplane Mode off and open the app: the changes reach the Mac (`seneca sync status` with the trial's environment, and the Mac's Git history) within minutes.

### termination

Force-quit the app from the app switcher: once mid-sentence in the form, once right after Save, and once while an exchange runs (pull to refresh on Sync, then quit). Reopen each time: nothing typed is lost, nothing saved twice, and the next exchange finishes.

### cloud-delay

With the iPhone in Airplane Mode for at least two hours, save twice on the Mac (`seneca sync once`). Turn the network back on. Sync says files are still downloading until iCloud delivers them, then imports both days whole. On the Mac, `seneca sync status` shows the iPhone's receipt catching up.

### simultaneous-edits

Put the iPhone in Airplane Mode. Change the same answer of the same day on both devices. Bring the iPhone back and exchange on both: each shows the conflict (Choose a Version on the iPhone, `seneca sync conflicts` on the Mac). Settle it on the iPhone one way and on the Mac independently, then exchange again: both end with the same text and no conflict.

### access-revocation

Settings, Seneca: take iCloud Drive access away (or turn iCloud Drive off for the iPhone). Open Seneca: writing and saving still work, and Diagnostics and Recovery names the unreachable folder. Give access back, choose the folder again if asked, and the waiting changes go out.

### upgrade

Install a build of the previous commit, use it (save a day, leave a draft), then install these sources over it:

```bash
git worktree add /tmp/seneca-previous HEAD
(cd /tmp/seneca-previous && just ios-install)
# write and leave a draft on the iPhone
just mobile-qualify install       # records the build it replaced
git worktree remove /tmp/seneca-previous
```

The journal, the draft, the reminders, and sync are as they were. The checker accepts `upgrade` only on a build installed over a different one.

### mac-restart

With background sync installed for the trial (`seneca sync service install` in the trial's environment), restart the Mac and do not open a terminal. Save on the iPhone. The day reaches the trial journal and its Git history by itself (`~/Library/Logs/seneca-sync.log`).

### representative-size

The trial journal is the synthetic one: three years, 1,100 days, 150 reviews, two thirds legacy entries, CRLF days, and unknown content. Joining finishes, History scrolls through every year, a search answers at once, and saving takes no longer than with a small journal.

### accessibility

With VoiceOver on, write and save a reflection, find a day in History, read a review, settle a conflict, and export the journal. Then do the same at the largest accessibility text size. Every control is reachable and says what it does; no text is cut off.

### date-boundary

Start a reflection at about 23:55 and save it after midnight: it is saved to the day it was started, and the new day is empty and gets its reminders. With reminders on, a day written on the Mac before 20:00 gets none on the iPhone once it is imported.

### time-zone-travel

Settings, General, Date and Time: turn off Set Automatically and pick a zone several hours away. Open Seneca: Today and the reminders follow the new zone, and days already written stay on their dates in History, on both devices. Set it back.

### draft-restoration

Leave a draft unsaved and force-quit; restart the iPhone; install a new build. The draft comes back each time, and is cleared only by Save or Discard.

## The multi-day trial

The trial is ordinary use on disposable data for at least three days: write on both devices, edit by hand on the Mac, let background sync run, and take a checkpoint at least once a day. Nothing of the author's own journal is involved, and the program refuses a trial journal that is the one `seneca` opens by default.

```bash
just mobile-scale-journal ~/SenecaTrial/journal          # the synthetic three-year journal
cd ~/SenecaTrial && git init -q && git add -A && git commit -qm "trial journal"
trial() { SENECA_FILE=~/SenecaTrial/journal/reflections.md \
  SENECA_SYNC_EXCHANGE=~/Library/Mobile\ Documents/com~apple~CloudDocs/Seneca-Trial seneca "$@"; }
mkdir -p ~/Library/Mobile\ Documents/com~apple~CloudDocs/Seneca-Trial
trial sync enroll --name Mac
trial sync service install
```

On the iPhone: Settings, Sync, Choose Exchange Folder, pick `Seneca-Trial` in iCloud Drive, then Join the Journal in This Folder. Then:

```bash
just mobile-qualify trial start --journal ~/SenecaTrial/journal/reflections.md \
  --exchange ~/Library/Mobile\ Documents/com~apple~CloudDocs/Seneca-Trial
just mobile-qualify trial checkpoint                     # once a day, with the iPhone paired
```

A checkpoint copies the iPhone's journal and sync state off it, compares every day with the Mac's through `seneca show`, and records the counts only (no text, no dates): days on each side, days that differ, conflicts on each side, whether the iPhone applied everything the Mac recorded, what waits to be published or imported, and the Mac's import commits. Let the exchange settle before a checkpoint (exchange on the iPhone, wait for `seneca sync status` to show nothing waiting). The trial qualifies with three checkpoints on three dates over at least 48 hours, every one agreeing, more days at the end than at the start, and at least one of the iPhone's days committed on the Mac during the trial.

## Defects

Anything that goes wrong is a defect, recorded with whether any writing was lost:

```bash
just mobile-qualify defect open simultaneous-edits --data-loss no --summary "conflict listed twice on the iPhone"
just mobile-qualify defect close 1 --resolution "fixed in ConflictsView; check passed again on build 1792…"
```

The gate refuses any open defect. A fix changes the sources, so the build, the checks it touches, and the trial's checkpoints are done again on the new build. A data-loss defect stops the rollout until it is closed.

## Waiving a step

The author may skip a device check, the trial, the rollback rehearsal, or the rollout, for example when the journal was already rolled out by hand. The record then says so instead of claiming the step was done:

```bash
just mobile-qualify waive airplane-mode --note "why it is skipped"
just mobile-qualify waive trial --note "..."
```

The steps that can be waived are every device check, `trial`, `rollback`, and `rollout`. A waiver keeps the author's note and the build it was recorded on, and it goes stale with the build like everything else. `status` and `check` list waived steps as waived, not done. A step that was recorded is still checked in full; a waiver only stands in for a step that is absent.

On 2026-10-15 the author waived every device check, the trial, the rollback rehearsal, and the rollout: the journal had already been enrolled, joined from the iPhone, and put under background sync before this procedure was run, and the author chose to keep it rather than move it onto a trial journal for three days.

## Rehearsing the rollback

Before the author's journal is enrolled, carry out [Rolling back](#rolling-back) on the trial, with a conflict left open on purpose (as in simultaneous-edits, without settling it):

```bash
trial sync service remove
trial backup ~/SenecaTrial/rollback-mac
# iPhone: Settings, Diagnostics and Recovery, Export Journal…, Save or Send the Export, to the Mac (AirDrop or Files)
trial sync leave
just mobile-qualify rollback --mac-export ~/SenecaTrial/rollback-mac --phone-export ~/Downloads/<the iPhone's export>
```

The record holds when the LaunchAgent is gone, both exports match their manifests, the Mac's export holds the open conflict's versions, the trial journal has left sync with its history kept aside, and `seneca show` and `seneca sync status` work on it with no exchange configured. `TestTheDocumentedRollbackKeepsEveryWordAndALocalWorkflow` carries out the Mac's side of the same steps in the gate.

## Rolling out the journal

Proposed for a journal already kept on the Mac, here `~/Documents/Seneca/reflections.md` (what `seneca` opens with nothing set) with `seneca-reviews.md` beside it, exchanged through `~/Library/Mobile Documents/com~apple~CloudDocs/Seneca`. Only after everything above holds:

1. **Back up.** Back the journal up somewhere outside iCloud Drive, and record it:

   ```bash
   seneca backup ~/SenecaBackups/before-sync-$(date +%Y%m%d)
   just mobile-qualify rollout backup --journal ~/Documents/Seneca/reflections.md \
     --exchange ~/Library/Mobile\ Documents/com~apple~CloudDocs/Seneca --backup ~/SenecaBackups/before-sync-<date>
   ```

2. **The CLI.** `just install`, then `just mobile-qualify cli`.
3. **Enroll explicitly.** Add `[sync] exchange = "~/Library/Mobile Documents/com~apple~CloudDocs/Seneca"` to `~/.config/seneca/config.toml`, then `seneca sync enroll --name Mac` and `just mobile-qualify rollout enrolled`. Do not install background sync yet.
4. **An empty iPhone.** The iPhone still holds the trial journal, and a device that holds writing records it into the journal it joins ([Joining from a fresh installation](recovery.md#joining-from-a-fresh-installation)). Export it if wanted, delete the app, and `just mobile-qualify install` again. Then `just mobile-qualify rollout phone-fresh`: it refuses unless the iPhone holds no day and no sync history.
5. **Join.** On the iPhone: Settings, Sync, Choose Exchange Folder, `Seneca`, Join the Journal in This Folder. Wait for it to receive everything, then `just mobile-qualify rollout joined`.
6. **Verify both copies.** Exchange on the iPhone, `seneca sync once` on the Mac, then `just mobile-qualify rollout verified`: every day the same on both, no conflict, nothing waiting, the iPhone has applied everything, and background sync still off.
7. **Background sync.** `seneca sync service install`, then `just mobile-qualify rollout background`.

`just mobile-verify 13` then holds the whole record. The trial journal, its exchange folder, and its LaunchAgent are gone by then (the rehearsal removed the service); delete `~/SenecaTrial` and the `Seneca-Trial` folder once the rollout holds.

## Rolling back

To stop using sync and go back to the command line alone, without losing anything:

1. **Stop sync.** On the Mac, `seneca sync service remove`. On the iPhone, stop writing there.
2. **Export what is unsent or in conflict.** On the Mac, `seneca backup ~/SenecaBackups/rollback-<date>`: the Markdown as it is (with any change not yet exchanged) and every version of every open conflict under `conflicts/`. On the iPhone, Settings, Diagnostics and Recovery, Export Journal…, and save it to the Mac: it holds what the iPhone never sent and its drafts. Copy any day only the iPhone has into the Markdown by hand, with `seneca` or an editor, and commit it.
3. **Keep the backups.** The rollout's first backup and these exports stay where they are; nothing deletes them.
4. **Leave sync.** `seneca sync leave` on the Mac (the sync history moves aside under `.seneca/` and is kept), then remove `[sync]` from `config.toml`. On the iPhone, Diagnostics and Recovery, Disconnect Sync, or delete the app once its export is safe.
5. **The local workflow.** `seneca`, `seneca show`, `seneca review`, and Git work exactly as before sync, on the same Markdown. The journal's settings stay in `.seneca/` and keep deciding the questions; `seneca settings` shows them and `seneca settings set` changes them. A `seneca` from before sync reads the same Markdown too, if one is ever needed: `go install` it from the commit before sync.

## Where things stand

```bash
just mobile-qualify status
```

lists every gap, in the gate's words. The records hold counts, build numbers, device and journal identifiers, and the author's notes; they hold no entry text and no dates.

## Voice on the iPhone

Speaking a reflection and Apple's models are qualified on the author's own iPhone, apart from the release record above, by `scripts/voice_qualification.py` (run as `just voice-qualify …`). `bash scripts/verify_mobile_session.sh 18` is the gate that holds it. Two records under `docs/mobile/evidence/`:

- `session-18-device-voice.json`: the takes, the latency, and the battery;
- `session-18-model-evaluation.json`: the [Model Evaluation](../product/apple-models.md#running-it-on-the-iphone) screen's responses, replayed through `seneca review`'s validator.

Neither holds a word of a transcript or a response. A take is summarized by what its sidecar says of it (its id, duration, size, language, transcriber, state, and timings, and the length of its transcript), and the author's note says what was heard.

```bash
just voice-qualify install        # build these sources (Debug), signed, and install over the app (data kept)
just voice-qualify cases          # the takes to record and what each means
just voice-qualify status         # what is done and what remains
```

Use a trial journal or a day you do not mind writing; nothing here saves an entry by itself. After `install`, every step reads the build the iPhone runs and the newest take's sidecar off the iPhone (over the cable or the network, unlocked), and refuses a take recorded before that build was installed.

**Takes through an interruption.** Start speaking, do the thing, carry on if the app lets you, stop, then record how many seconds of what you said are missing from the take (at most one may be):

```bash
just voice-qualify take force-quit passed --lost-seconds 0.5 --note "swiped away at 40 s; relaunched, take recovered, transcribed"
just voice-qualify take lock passed --lost-seconds 0 --note "locked at 20 s, spoke 30 s more, unlocked; whole"
just voice-qualify take call passed --lost-seconds 1 --note "call at 30 s; everything before it kept"
just voice-qualify take headset passed --lost-seconds 0 --note "AirPods connected at 15 s"
```

`force-quit` holds only a take the next launch recovered from its partial.

**Transcription.** One take for each language and room, transcribed in the language Settings › Voice names (English as en-US, French as fr-CA, a mixed take in either). The note says how close the transcript came, and what it got wrong:

```bash
just voice-qualify take en-quiet passed --note "near verbatim; one name misspelled"
just voice-qualify take fr-noisy passed --note "kitchen fan on; two words dropped, gender endings right"
```

The cases are `en-quiet`, `en-noisy`, `fr-quiet`, `fr-noisy`, `mixed-quiet`, and `mixed-noisy`. A take stands for one case only.

**Latency.** Speak for three minutes or more, stop, and tap Sort Into Your Questions as soon as the transcript shows. The take's sidecar keeps when it stopped, when its transcript was ready, and how long the model took to propose answers, so the time you take to tap is not counted:

```bash
just voice-qualify latency
```

The target is under 15 seconds from stop to proposed answers. On an iPhone without Apple Intelligence the model is never asked, so the time is stop to transcript and the record says why there was no proposal. A slower result is recorded as it is and stated in [Apple's models](../product/apple-models.md#measured-on-the-iphone).

**Battery.** With the iPhone off the charger and the battery level from Settings › Battery:

```bash
just voice-qualify battery start --percent 92
# ten takes of about a minute, each transcribed
just voice-qualify battery finish --percent 89
```

**Waiving a check.** When the author has done a check by hand but no take is left to read (for example, the app deleted it after saving), or does not want the check at all, the record can say so instead of passing it off as measured:

```bash
just voice-qualify waive lock --note "locked mid-take and unlocked; it carried on"
just voice-qualify waive battery --note "not needed"
```

Any take case, `latency`, or `battery` can be waived. A waiver holds the author's note and the build it was made on, and it goes stale with the build like everything else. `status` and `check` list waived checks as waived, not measured.

**The model evaluation** is [Running it on the iPhone](../product/apple-models.md#running-it-on-the-iphone), ending with `just voice-qualify evaluation`.
