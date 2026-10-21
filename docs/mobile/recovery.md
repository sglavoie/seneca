[← Mobile contracts](README.md)

# Reminders, diagnostics, and recovery

This page covers the evening reminders on the iPhone, what each device can say about itself without saying anything written, and the procedures that bring a journal back: backing it up, replacing a device, joining from a fresh installation, disconnecting sync, and restoring. Every procedure keeps every word. None of them lets one device speak as another.

## Evening reminders

The Mac shows an alert at 20:00, 21:00, 21:30, and 22:00 when the day has no entry ([Daily reminder](../../README.md#daily-reminder)). The iPhone uses the same times by default and the same rule: a day the journal on that device holds an entry for is complete and gets no reminder. The rule lives in the engine (`engine/reminders.go`), so `seneca test-notification` and the phone cannot disagree about it.

- **Off until turned on.** Settings, Reminders. iOS asks for permission the first time. The times are the author's choice on that iPhone; **Use the Usual Times** goes back to the Mac's. Preferences are device-local (`Application Support/Seneca/reminders.json`) and never exchanged.
- **Planned by the engine, scheduled by the app.** The bridge's `reminders.plan` returns the reminders still ahead for the next seven days, each with its instant in the journal's zone. The app makes iOS's pending reminders exactly that list: it cancels the ones no longer planned and schedules the rest again.
- **Planned again whenever it may have changed:** after every save on the phone, after an import brings a change from another device, when the app comes forward, when the day turns or the clock or the zone changes (the journal is reopened in the new zone first), and when the author changes the times.
- **A week ahead.** Each plan covers seven days, so an iPhone on which Seneca is not opened for a week runs out of reminders until it is.
- **A reminder says nothing written.** "Evening reflection: Nothing is written for today yet." It can be read on a locked screen.
- **One that fires while Seneca is open** is not shown when the app has since learned the day is written.

### What a save on the Mac can do

A reminder is scheduled on the iPhone, by the iPhone. A reflection saved on the Mac cancels it only after the iPhone has received that reflection through the exchange folder, imported it, and planned again: when Seneca next opens or comes forward, or when an exchange it runs brings the change in. Until then the reminder is still scheduled, and iOS shows it. Nothing on the Mac can reach a notification already pending on the phone, and the app does not run in the background to look. The Reminders screen says this in so many words.

### When notifications are not allowed

Turning reminders on with notifications denied schedules nothing and says so: "Notifications are turned off for Seneca in Settings, so no reminder can appear. Writing is not affected." **Open Settings** goes to Seneca's notification settings. Allowing them later takes effect the next time the app comes forward. Diagnostics and Recovery lists the same finding. Nothing about reminders, permission, or a plan that cannot be made ever blocks writing: a plan the engine cannot make leaves what was scheduled in place.

## Doctor and status

`seneca doctor` adds a `sync` group on a journal that takes part in sync, read-only like the rest: the journal and this device, and each of these with what to do about it.

| Finding | Severity | What to do |
| --- | --- | --- |
| Changes recorded here and not in the exchange | info | `seneca sync once` |
| Changes in the exchange not imported here | info | `seneca sync once` |
| Changes held until what they follow arrives | info | nothing |
| Changes from a newer seneca | warn | update seneca on this device; they wait |
| Changes that cannot be applied to the files as they are | warn | `seneca sync status` says why |
| Conflicts and disputed days | warn | `seneca sync conflicts` |
| A journal file sync stopped reading | error | `seneca sync recover` |
| The sync state written by a newer seneca | error | update seneca, or `seneca sync leave` and join again |
| The exchange started by a newer seneca | error | update seneca |
| The exchange carries another journal | error | point `[sync].exchange` at this journal's folder |
| This device's identity used by another installation | error | on the copy: `seneca sync leave`, then `seneca sync enroll --join` |
| Imported changes not committed to Git | warn | the next sync retries |

`seneca sync status` repeats the ones it does not already show. A journal outside sync reads exactly as before. On the iPhone, Settings, Diagnostics and Recovery shows the same findings in the phone's words, with the action that settles each: Exchange Now, Choose a Version, Restore the file, Rejoin as This iPhone, Sync, or Reminders. It adds what only the phone knows: an exchange folder iCloud cannot reach right now, a grant taken back, a journal restored from another iPhone, and notifications turned off.

## Diagnostics to share

`seneca doctor --diagnostics` on the Mac and **Share Diagnostics** on the iPhone produce the same content-free report: format versions, counts, and the stable finding codes above, plus the program around it (platform, whether Git, a harness, an exchange, and the background service are there; on the phone the app's version, iOS's, the reminder state, and the sync state as a code). It holds no entry text, question, focus, review, date, path, device name, or error message, and tests hold both reports to that by searching them for the journal's own words. **What Is Shared** on the phone shows the report before it leaves. On the phone it also counts the spoken reflections kept there: how many, their total size, how many wait for a transcript, the speech model's state as a code, and whether recordings are kept or deleted after saving. Never a transcript, a date, or a recording's id.

## Backing up: the recovery export

A recovery export is the whole journal in a new directory: the reflections file and the review archive byte for byte, the journal's settings, every version of every conflict still open (they exist nowhere else but the sync history), unsaved writing, `MANIFEST.json` with each file's SHA-256, and `README.txt`. It is made only when asked, because it holds everything written.

- On the Mac: `seneca backup <new-directory>`. Git already keeps the Markdown's history; the export adds what Git does not have and what is not committed.
- On the iPhone: Settings, Diagnostics and Recovery, **Export Journal…**, then **Save or Send the Export** to put it in Files or anywhere else. It includes the drafts kept on that iPhone.

### Spoken reflections in a recovery kit

Recordings and their transcripts are device-local (`Application Support/Seneca/audio/`), never synced and never in the journal, so the engine's export does not hold them. On the iPhone, **Include Spoken Reflections** before **Export Journal…** makes the export a kit: a directory holding the engine's export, exactly as it is made alone and as `seneca backup` makes it, and beside it an `audio/` folder the app writes:

```
Seneca Recovery <time>/
  Seneca Journal <time>/      the engine's export, unchanged
  audio/
    MANIFEST.json             {"format": "seneca-audio", "version": 1, "files": [{"path", "bytes", "sha256"}]}
    <date>/<uuid>.m4a
    <date>/<uuid>.json
```

The kit appears whole or not at all: it is written into a hidden directory beside it and renamed into place last. Only finished takes go in; a partial a stopped recording left does not.

To put the recordings back, Settings, Diagnostics and Recovery, **Restore Spoken Reflections…**, and choose the kit or its `audio/` folder. Every file is checked against `MANIFEST.json`, and every take for both its audio and its sidecar, before anything is written, so a damaged or incomplete kit restores nothing, and a restore that fails part way takes back what it put back. Each take is copied under a hidden name and renamed into place; a restore the app stopped in the middle of is finished, or undone, at the next launch or restore. Each take goes back to `Application Support/Seneca/audio/<date>/`; one already there is never replaced. Restoring recordings changes nothing in the journal; the answers they gave are already in it.

An export appears whole or not at all: it is written into a hidden `.<name>.partial-<random>` directory beside the destination and renamed into place last. A failure removes the hidden directory; a process stopped part-way leaves it, nothing looks finished, and the next export to that name removes it. An export never lands over an existing directory. It holds no sync identity and no sync history.

## Replacing a device

**A new iPhone, set up fresh.** Install Seneca, choose the same exchange folder in iCloud Drive (Settings, Sync, Choose Exchange Folder), and **Join the Journal in This Folder**. The new iPhone draws its own device identity and receives everything the other devices published. The old iPhone can simply stop being used; if it is still around, disconnect it (below) so it publishes nothing more.

**A new iPhone set up from a backup of the old one, or by transfer.** The app's container comes along whole, sync history and old identity included. Two devices under one identity would each publish changes the other never made under the same numbers, so the app keeps the identity a device enrolled or joined with in the keychain as an item for that iPhone only, which no backup or transfer carries to another iPhone. On the new iPhone the history's identity is not in the keychain: the app does not exchange, keeps every save, and says "This journal was restored from another iPhone". Diagnostics and Recovery, **Rejoin as This iPhone** leaves sync and joins again as a new device. Everything written on either iPhone is kept: the copy's writing is recorded by its first import beside what the others hold, never over it, and a day written differently becomes a conflict to choose. If the old iPhone kept exchanging after the backup was taken, the engine also sees changes under this device's identity in the exchange that it never recorded, and both devices report it (`identity-reused`); one of them rejoins.

**A new Mac.** Clone or copy the journal's Git repository, and before any `seneca sync` there, run `seneca sync leave` if `.seneca/` came along, then `seneca sync enroll --join` with `[sync].exchange` pointing at the same folder. Leaving first is what keeps the new Mac from speaking as the old one; `seneca doctor` reports `identity-reused` if it ever does.

## Joining from a fresh installation

A device with no journal joins and receives the journal. A device that already holds writing (an export restored into it, or a journal written before syncing) joins the same way: joining records nothing of its own at first, and its first import records what its files hold as a change of its own, compared with what the others hold. Matching days are one; days written differently are conflicts to choose. Nothing is silently replaced.

## Disconnecting sync

- **iPhone:** Diagnostics and Recovery, **Disconnect Sync**. The journal stays on the iPhone with every word, its device identity is retired, and the folder is forgotten. Choosing a folder later joins as a new device.
- **Mac:** `seneca sync leave`, then remove `[sync].exchange` and run `seneca sync service remove`.

Leaving moves the sync history aside, to `.seneca/journals/<reflections filename>/sync-left-<time>/`, in one rename. It is never deleted: the competing versions of any conflict that was open are still there, and the journal keeps the version it showed. The retired identity is never used again.

## Restoring

1. On the device to restore to, start from a fresh installation (or `seneca sync leave` first on a Mac that has `.seneca/`).
2. Put the export's reflections file and review archive in the journal's place: the Mac's journal directory, or, on the iPhone, an installation that has written nothing yet. `settings/settings.json` is the journal's questions; on the Mac `seneca settings init` from a `config.toml` with the same questions is the supported way back. `conflicts/` and `drafts/` are for reading and copying from by hand.
3. Join the journal the other devices exchange, or, if none remains, start syncing from this one. Either way it takes part as a new device.

Restoring never reuses the identity of the device the export or backup came from: an export carries none, a Mac leaves before joining, and an iPhone restored from a backup is stopped until it rejoins.
