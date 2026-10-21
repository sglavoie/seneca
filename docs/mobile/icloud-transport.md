[← Mobile contracts](README.md)

# iCloud transport

How the iPhone and the Mac reach one exchange folder in iCloud Drive. The [sync format](sync-format.md) says what the folder holds and when a change is applied; this page is only about getting whole files into and out of a folder a file service owns. Seneca talks to no server itself: iCloud Drive carries files, and each side has its own adapter for it.

| | iPhone | Mac |
| --- | --- | --- |
| The folder | Chosen by the author in a document picker; reached through a kept bookmark. | A path: `SENECA_SYNC_EXCHANGE` or `[sync].exchange`, usually under `~/Library/Mobile Documents/com~apple~CloudDocs/`. |
| What the engine exchanges with | A mirror of the folder in the app's container. | The folder itself. |
| A file listed and not downloaded | Asked for; not read; looked at again by the next exchange. | Asked for (`brctl download`); not read; reported to the engine as not arrived. |
| Code | `ios/SenecaMobile/Exchange/` | `cmd/sync_cloud.go` |

Neither adapter is in the engine. `engine` and `mobile/senecabridge` know a directory of immutable files and nothing about iCloud, bookmarks, file coordination, or `brctl`; the gate checks that.

## On the iPhone

### The grant

The exchange folder is outside the app's container, so the author picks it once, in Settings → Sync → Choose Exchange Folder. iOS hands the app a security-scoped URL for that folder; the app keeps a bookmark of it in `Application Support/Seneca/exchange/grant.json` (`GrantStore`). Every exchange resolves the bookmark, starts access, and stops it when the exchange ends. A bookmark iOS reports stale is renewed in place.

The grant is device-local: beside the engine's state, outside the journal, never exchanged. Choosing another folder replaces it and empties the mirror.

### The journal never leaves the container

The journal, its sync history, drafts, and the request ledger are all inside the app's container, and every engine operation works on them there. What crosses to the folder is only what an exchange is made of: the descriptor, content objects, manifests, and receipts, each immutable and whole.

Between the two sits the mirror, `Application Support/Seneca/exchange/mirror/`, with the exchange's own layout. The engine imports from the mirror and publishes into it through the ordinary directory transport. `ExchangeMirror` carries files between the mirror and the folder:

- **Pull** lists the folder and copies into the mirror each file it lacks that is downloaded *and is what its name says*. Everything but the descriptor is named by the SHA-256 of its bytes, so a file cut short on the way fails that check and is left for the next look. A file the folder lists whose bytes are not on the phone is asked for with `startDownloadingUbiquitousItem` and not read.
- **Push** writes into the folder each file of the mirror the folder does not list: the descriptor, then content objects, then this device's manifests oldest first, then its receipts. A file that is already there is never replaced.

Contents go before the manifest that names them, so a publication stopped part-way leaves nothing another device can take for a change. A file service delivers in whatever order it likes all the same; the engine applies a transaction only when its manifest and every content it names have arrived intact, and reports `incomplete` until then.

### Coordinated access

Every listing, read, and write of the folder goes through `NSFileCoordinator` (`CoordinatedFolder`), because the file provider is reading and writing the same files. A write is whole or absent: written beside the folder and moved in. One coordinated access waits at most 30 seconds for the file service.

### What an exchange is

`ExchangeService.exchange` is one pass, away from the main thread: open the folder, pull, import, publish into the mirror, push, read where things stand. Each step stands by itself. `SyncModel` decides when it runs:

| When | |
| --- | --- |
| Launch | After the journal is open; the journal does not wait for it. |
| Foreground | Each time the app becomes active. |
| Save | After the save is on the screens; the form does not wait for it. |
| Pull to refresh | Waits for the exchange, then shows what it brought. |
| Retry | While something is still arriving or the folder could not be reached: after 2 s, 4 s, 8 s, up to 60 s, and only while the app is in front. |
| Background | Once, in the time iOS grants after the app leaves the screen, and cancelled when that time runs out. Best effort: nothing depends on it. |

A request made while an exchange runs does not start another beside it; one more runs when the first is done.

### Faults

| What happens | What the app does |
| --- | --- |
| A file is listed and not downloaded | Asks for it, shows "still arriving", retries. |
| A file is here and is not what its name says | Leaves it out of the mirror, retries. |
| The folder cannot be reached | Says so, retries. Writing goes on; the changes are counted as saved on this iPhone only. |
| The folder is gone, or access was taken back | Says so and asks the author to choose the folder again. Not retried: only the author can fix it. |
| The exchange is cancelled (the author left, background time ended, the file service did not answer) | Stops between files. Nothing is half-done, and the next exchange does the rest. |
| The folder carries a different journal than the mirror | Refuses, and asks for the folder to be chosen again. |

None of these touches the journal, and none of them stops a save.

### Joining and starting

With a folder chosen and a journal that takes no part in sync yet, the app looks at the folder and offers one of two things, and does neither by itself:

- **Join the Journal in This Folder** when the folder's descriptor has arrived.
- **Start Syncing from This iPhone** when the folder holds no descriptor at all.

A descriptor that is listed and not downloaded is neither: the app waits for it, so an empty-looking folder is never mistaken for an empty one.

## Receipts: saved, published, applied

A manifest in the folder says a change was published. It does not say the other device has it. For that each device publishes a **receipt**, described in the [sync format](sync-format.md#receipts): how far it has applied each device's transactions, read from its own sync state.

The app shows the three separately (Settings → Sync, and a line on Today):

| Shown | Means | Known from |
| --- | --- | --- |
| Saved on this iPhone | The engine's commit finished. | The journal's own history. |
| In the exchange folder | The manifest is written into the folder. | The folder's listing. |
| Uploaded to iCloud | iCloud has taken that file. | The file service, per file. |
| Applied on *name* | It is in that device's journal. | That device's receipt. |

The Mac writes its receipt only after its Markdown has the change and, in a repository, after the `seneca: sync import` commit landed. While that commit is failing the receipt waits with it.

## On the Mac

The exchange directory can be any folder. Inside iCloud Drive, a file another device published can be listed on the Mac before its bytes are there, and a file the Mac had can go back to iCloud when macOS reclaims space. Such a file shows either as a `.<name>.icloud` placeholder or, on a current macOS, as a dataless file under its own name whose first read would block on the download.

`cloudExchange` in `cmd/sync_cloud.go` wraps the directory transport for every exchange:

- It reads directory listings and file flags only. A file that is not local is never opened, so an exchange never blocks on the network.
- Each such file is asked for with `brctl download`, at most 64 per exchange. `SENECA_BRCTL` names the program, for tests.
- To the engine the file has not arrived: a manifest is listed and unreadable, contents are missing, and the transaction is reported `incomplete` and looked at again.
- A descriptor that is still downloading is an error, not an empty exchange. `seneca sync enroll` then refuses instead of starting a second journal in the folder.

`seneca sync once` and the watcher report how many files are still downloading, and the watcher retries with its usual backoff. `seneca sync status` reports the count and asks iCloud for nothing.

## Evidence

The transport's faults are scripted in tests on both sides. The exchange itself is run between a physical iPhone and a Mac through iCloud Drive by `ios/scripts/sync-evidence.sh device`, and its record is `docs/mobile/evidence/session-10-device-sync.json`; [`ios/README.md`](../../ios/README.md#sync-evidence) describes the run. A simulator can rehearse the scenario over a plain folder, and its record is refused as evidence.
