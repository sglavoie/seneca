[← Mobile contracts](README.md)

# Sync format

What two devices exchange, what each keeps, and when an arriving change is applied. The [sync invariants](sync-invariants.md) say what must stay true; this page is the format that holds them. No service is involved: the exchange is a directory, and nothing here reads Git.

## Artifacts

A journal is exchanged as artifacts, never as files.

| Artifact | Is | Lives in |
| --- | --- | --- |
| `entry:<YYYY-MM-DD>` | One day's entry, as its raw bytes. | The reflections file. |
| `record:<id>` | One archive record, as its raw bytes, named by the ID it already carries. A record of a kind this Seneca does not know carries no ID it can check, and is named `record:x<sha-256 of its bytes>`. | The review archive. |
| `settings` | The journal settings document, whole. | `settings.json`. |

An artifact's revision is `sha256:<digest of its bytes>`, or `absent`. A whole-file digest changes when a rewrite settles the spacing around a separator; an entry's bytes and a record's bytes do not, so an artifact has the same revision on every device.

The archive's header (its title, its `seneca:archive` marker, and any prose above the first record) is not an artifact. Each device keeps its own.

## Transactions

A transaction is one manifest, a JSON document that is never edited once written:

```json
{
  "protocol": 1,
  "journal": "9f1c…",
  "device": "52fd…",
  "sequence": 7,
  "parents": ["3b0a…"],
  "created": "2026-08-20T21:00:00Z",
  "origin": "local",
  "changes": [
    { "artifact": "entry:2026-08-20", "base": "absent", "new": "sha256:…" },
    { "artifact": "entry:2026-08-12", "base": "sha256:…", "deleted": true },
    { "artifact": "entry:2026-08-11", "base": "sha256:…", "new": "sha256:…", "resolves": ["sha256:…"] }
  ]
}
```

- **`protocol`** is checked first. A manifest under a newer protocol is left where it is and reported; nothing of it is applied or guessed at. A field this protocol does not define is an error.
- **`journal`** and **`device`** are random identities, independent of each other and of every record ID. A device never writes under another's.
- **`sequence`** counts the device's own transactions from 1.
- **`parents`** are the transactions this one was made on top of: every head of the history its device had applied. Order comes from these alone. **`created`** is for a reader and decides nothing.
- **`origin`** is `genesis` (the snapshot a journal was enrolled with), `local` (a change made through the engine), `external` (a change found in the files), `merge` (a device reconciled two independent changes that did not overlap), or `resolution` (the author's answer to a conflict or a disputed focus).
- **`changes`** names each artifact changed, the revision it replaced, and the revision written, or an explicit deletion. An artifact a transaction does not mention is unchanged.
- **`resolves`**, on a change, names the other revisions it considered besides its base: the competing versions a merge or a resolution settled, the one it kept among them. A change *covers* its base and everything it resolves. The list is sorted, names each revision once, and may name `absent` for a competing deletion.

A transaction's identity is the SHA-256 of its manifest's bytes. It is stored as `<sequence, eight digits>-<identity>.json` in a directory named after its device, in a device's history and in the exchange alike.

## What a device keeps

Under `.seneca/journals/<reflections filename>/sync/`:

| Path | Holds |
| --- | --- |
| `state.json` | The journal and device identities, this device's sequence, the heads of the history applied here, the revision last recorded of every artifact and the transaction that wrote it, where history places each archive record, each stored transaction that is not applied, with why, each conflict with the versions held in it, each day whose focus is disputed, and each suspended file. Device-local; never exchanged. |
| `transactions/<device>/<sequence>-<id>.json` | Every manifest this device wrote or received whole. Immutable. |
| `objects/<sha-256>` | The contents every stored manifest names, including revisions a later change replaced. These are the bytes a reconciliation needs for its base; they do not come from Git or from a `.bak`. Nothing here is pruned yet. |

`.seneca/directory/sync.json` names the one journal in the directory that takes part in sync.

The Markdown is the journal. The state is only what was last *recorded* of it, so a difference between the two is a change nobody has recorded yet, never an error.

## Enrolling and joining

**Enrolling** draws the journal's identity and the device's, gives the journal settings of its own when it has none, and records everything the journal holds as the genesis transaction: every entry, every archive record in the order the archive has them, and the settings. No Markdown is written.

**Joining** reads the journal's identity and the identity of its genesis from the exchange, and draws a device identity. It writes nothing else. A device that joins with writing of its own records that writing by its first import as a transaction beside the genesis, not on top of it, so a day both devices hold is compared and never silently replaced.

A device enrolled in one journal refuses an exchange that carries another, or the same journal identity over a different genesis.

## Recording a local change

Every `Journal.Commit` on an enrolled journal records what it changes in the commit itself: the manifest, the state, and the Markdown are replaced as one recoverable unit. A crash before the commit point leaves none of them; a crash after it is finished by the next operation on the journal.

The commit first compares the files with the state. What an editor or Git changed since is recorded as an `external` transaction ahead of the commit's own, so a save never claims writing it did not make.

Recording never fails a save. When the history cannot be read or added to, the files are written without it, and the change is found in them and recorded the next time the history is.

## External edits

The files are compared with the state before anything that arrived is applied, and at every local commit.

- A file that reads as a journal file has its differences recorded: a new or changed artifact as a change, an artifact no longer in it as an explicit deletion.
- A file a local command would refuse (a repeated date, an undatable entry, an archive record whose ID does not match its content, settings that do not validate) is left exactly as it is. Nothing of it is recorded, and a transaction that would change it is kept until it is repaired.
- A file that is **missing, or holds nothing, while history holds artifacts for it** is not a deletion of them. That file is suspended: nothing is read from it, and nothing arriving is written to it, until it holds every recorded artifact again. A save made through the engine into such a file records the day it saved and deletes nothing.

## The exchange

```text
<exchange>/
├── journal.json                                which journal this is, and its genesis
├── transactions/<device>/<sequence>-<id>.json
├── objects/<sha-256>
└── receipts/<device>/<sha-256>.json            what a device has applied
```

Everything in it is immutable and named by what it holds. Each file is written under a temporary name and renamed into place.

**Publishing** copies every transaction this device recorded that the exchange does not hold, each after the contents it names. A device publishes only its own. Nothing remembers what was sent: an interrupted publish, or an exchange that lost files, is repaired by publishing again.

**Importing** takes a transaction only whole.

| Found | Reported as | Then |
| --- | --- | --- |
| A manifest or contents not there, cut short, or not matching the digest in their name | `incomplete` | Looked at again next time. |
| A manifest that is not one, or that breaks a rule of the protocol | `malformed` | Ignored. |
| A newer protocol | `newer-protocol` | Left where it is. |
| Another journal's manifest | `foreign-journal` | Ignored. |
| Contents a local command would refuse to write | `invalid-content` | Ignored. |
| A second transaction at a device sequence already used | `sequence-reuse` | Ignored. |

None of these is stored or applied. A transaction already held is not read again, so delivering it twice does nothing. A file that is absent is never a deletion.

## Receipts

A manifest in the exchange says its author published a change. A **receipt** is the other half: a device saying how far it has applied each device's transactions, so the author of a change can tell *saved here* from *in the exchange* from *in the other device's journal*.

```json
{
  "protocol": 1,
  "journal": "5eb2ff7550b6…",
  "device": "6190e63ca1d4…",
  "name": "mac",
  "applied": {"6190e63ca1d4…": 5, "9a0c41d7e2b3…": 7}
}
```

`applied` gives, per device, the sequence up to which that device's transactions are all applied on the receipt's author. A device's transactions form a chain, so one number describes them. It is read from the author's own sync state, where a transaction is applied exactly when the commit that wrote its changes into the Markdown finished; a transaction that is stored and waiting is not counted.

A receipt carries no time, so the same state gives the same bytes. Like everything else in the exchange it is immutable and named by the SHA-256 of its bytes. A device publishes a new one only when what it has applied grew, and never replaces an old one. A reader takes, per author, the highest number any whole receipt of that author states, never more than it recorded itself. Their order of arrival does not matter. A receipt that is cut short, does not match its name, belongs to another journal, or sits under a device it does not name is not counted.

Receipts change nothing in a journal. They are read only to report where a device's own changes have got to (`engine.SyncDelivery`, `seneca sync status`, the phone's `sync.delivery`).

## Applying

A stored transaction is applied when every parent is applied. Order of arrival does not matter: a transaction that arrives before its parents waits for them, so one device's transactions are always applied in the order it wrote them.

Each change in it is then taken on its own:

| The change | Then |
| --- | --- |
| writes what the artifact already holds | Nothing; two devices that wrote the same bytes agree. |
| covers the revision this device holds, and its author had seen the transaction that wrote it | It follows from it and replaces it. |
| anything else | It was made independently, and is [reconciled](#reconciling). |

A transaction that cannot be applied stays stored, with its contents, as pending:

| Reason | Means |
| --- | --- |
| `waiting` | A parent has not arrived or is itself pending. |
| `local-file` | The file it would change is invalid or suspended here. |
| `refused` | Applying it would produce a file a local command would refuse to write. |

A transaction is applied whole or not at all, together with the receipt of it in the state and the Markdown it changes, in one commit. A new entry goes where its date belongs; a new record goes where [history places it](#archive-order). A change that ends in a conflict does not hold the transaction back: the transaction is applied, and only that artifact waits.

## Reconciling

A change made independently of this device's version is merged with it when the two do not overlap, and held beside it as a conflict when they do.

What each side replaced says how the two relate, where the bytes alone cannot. With *mine* the revisions this device's last change to the artifact covered:

| Found | Outcome |
| --- | --- |
| Each side covered the other's result | Both settled the same versions differently: a conflict. |
| Only this device covered the incoming result | The incoming version is one this device already moved on from; its own stands. |
| Only the incoming change covered this device's revision | The incoming version follows from it and replaces it. |
| Both covered exactly one revision in common | That is the base; the two are merged on it. |
| Both covered several in common | Two settlements again: a conflict. |
| Nothing in common | The base is the one of the conflict the change continues, or else the base the change names. |

Merging on a base:

- A side that left the base alone yields to the other. This holds for every artifact.
- **An entry** is merged by its parts: the lines above the first section, and each `### ` section with the markers bound to its heading. A part is taken whole from the side that changed it; nothing inside a part is combined, so an answer keeps its markers and a follow-through keeps its source, snapshot, and response together. Both sides may change different parts; one side may add or remove sections while the other keeps the base's. The merged entry is its parts separated by one blank line.
- An entry is **not** merged when a heading repeats, a marker cannot be read, the line endings are not LF, both sides changed which sections there are, both changed one part differently, or one removed a section the other changed.
- **Two entries written independently for one day** are one only when they have the same parts, apart from the `seneca:entry` marker; the bytes that sort first are kept on every device.
- **A deletion beside an edit**, **two different revisions of an archive record**, and **two revisions of the settings** are never merged.

A merge is recorded as a `merge` transaction of the device's own that covers both versions. Every device computes the same merge, so the transactions agree.

A conflict keeps this device's version in its file and holds every other version, with the base, in the state and in `objects/`. A device that wrote none of the competing versions shows the one whose transaction identity sorts first, so the order they arrived in decides nothing. A conflict is identified by the SHA-256 of the artifact and the sorted competing revisions: the same on every device that holds the same versions, and different once any of them changes.

A `resolution` transaction covers every competing version and writes the result: one of them, a deletion that competed, or something new. A resolution that names a conflict whose versions have changed since is refused and writes nothing to the journal.

## Archive order

A record is placed by the earliest transaction that wrote it:

1. by that transaction's depth, the length of the longest path to it from the start of history, so a record always follows every record its author could see;
2. among transactions of one depth, the genesis first, then by transaction identity;
3. within one transaction, by position in its changes, which is file order.

The archive as enrolled therefore stays as it was, a device's own records keep the order it wrote them in, and records two devices added independently land in the same order on both. A record is never rewritten: a review saved on two devices for one period is two records, each naming only the review it was saved as a revision of.

## Disputed focus

Among the focus changes effective one day, a change is superseded by any other whose transaction was made with it in sight. When more than one is left and they do not say the same thing, that day's focus is disputed: reading the focus for a date it decides reports the competing changes and no focus. A later focus change for that day, made after all of them, settles it; so does any focus change effective on a later day, from that day on.

## Recovery

A journal file is *suspended* when it is missing or empty while history holds artifacts for it, or when it lost at least three artifacts and more than half of what was recorded of it at once (`mass-deletion`). Nothing is read from a suspended file as a deletion and nothing arriving is written to it. It is read again when it holds every recorded artifact, or when the author recovers it:

- **restore** writes back, from `objects/`, every recorded artifact the file lacks, and records no change. A file that cannot be read at all is replaced whole; its bytes stay in its `.bak`.
- **accept-deletions** records what is missing as explicit deletions, which then reach the other devices.
