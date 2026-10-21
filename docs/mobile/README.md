[← Seneca](../../README.md)

# Mobile contracts

Seneca for iPhone shares the desktop's Go engine. These pages are the boundaries that work is held to. They describe the desktop as it behaves today, and they fix the few decisions the later work needs before any code moves.

| Page | What it fixes |
| --- | --- |
| [Write-path inventory](write-path-inventory.md) | Every way a journal's files change on disk, including the ways Seneca does not control. |
| [Engine and bridge](engine-and-bridge.md) | What Go owns, what Swift owns, and how the bridge between them is versioned. |
| [Bridge](bridge.md) | Version 4 of the bridge: its operations, its error codes, and how a mutation is made conditional and unrepeatable. |
| [Journal layout](journal-layout.md) | The files beside a reflections file, the private metadata directory, and journals that share a directory. |
| [Sync invariants](sync-invariants.md) | What must stay true of a journal however changes reach it. |
| [Sync format](sync-format.md) | What two devices exchange, what each keeps, and when an arriving change is applied. |
| [iCloud transport](icloud-transport.md) | How each side reaches the exchange folder in iCloud Drive: the grant, the mirror, placeholders, cancellation, and receipts. |
| [CLI compatibility](cli-compatibility.md) | The command surface, the output, and the read-only behavior that must not change. |
| [Reminders, diagnostics, and recovery](recovery.md) | Evening reminders on the iPhone and what a save on the Mac can do about them, doctor and status findings, content-free diagnostics, the recovery export, and replacing, joining, disconnecting, and restoring a device without reusing another's identity. |
| [Release qualification](qualification.md) | What qualifies a release on a physical iPhone and the Mac, the multi-day trial on disposable data, the personal rollout of the author's journal, and the rollback, each recorded by `scripts/qualification.py` and held by the session-13 gate. |

## Compatibility fixtures

The baseline is a synthetic journal under `cmd/testdata/compat/`. Nothing in it was copied from a real journal, and no baseline test reads one: each test works on a copy in a temporary directory, with `HOME` and `SENECA_FILE` pointing into temporary directories.

| Fixture | Covers |
| --- | --- |
| `journal/reflections.md` | Two legacy entries and two current (v2) entries: an unknown section, preamble prose, a foreign HTML comment, an unknown Seneca marker, a deeper heading, prose ending in three dashes, an anchor role marker, and a follow-through snapshot. |
| `journal/seneca-reviews.md` | A v3 review archive: author prose in the header, a weekly review whose body holds its own heading and rule, a revision of it, a monthly review, a record of an unknown kind, and a focus timeline of a manual set, a review-origin set, a clear, and a second set. |
| `journal/incoming.md` | A second journal with an earlier and a later day, for merge. |
| `legacy-framing.md` | The same four entries with a blank line ahead of each separator, the way older journals were written. |
| `legacy-archive-v2.md` | An archive written under schema v2. |
| `config.toml` | Every table Seneca reads (`[editor]`, `[seneca]`, `[review]`) and one it does not. |
| `golden/` | The exact output of the read-only commands for the fixture journal. `{{JOURNAL_DIR}}` and `{{HOME}}` stand for the temporary directories. |

The tests are `cmd/compat_*_test.go`. `cmd/engine_parity_test.go` runs each mutation twice over the same fixture, once through its command and once through `engine.Journal` alone, and requires the two directories to end up byte for byte the same. Record IDs in the archive fixtures are content hashes, so the archive files are frozen: edit one by hand and the tests report an ID that no longer matches its record.

`mobile/testdata/roundtrip/` is the scenario the phone is held to: a copy of `journal/`, one reflection to save, and what the desktop engine writes for it. The [bridge](bridge.md) page describes it, and [`ios/README.md`](../../ios/README.md) describes how it is run on a simulator and on a physical iPhone. `docs/mobile/evidence/` holds the records of the runs on a physical iPhone: the round trip, the exchange with a Mac through iCloud Drive, a reminder delivered by iOS and the next one cancelled by a save, and the [release qualification](qualification.md) with the simulator suites it ran in full.

A golden file changes only for a deliberate change to what a command prints. Regenerate with `SENECA_UPDATE_COMPAT_GOLDEN=1 go test ./cmd -run TestCompat` and review the diff.
