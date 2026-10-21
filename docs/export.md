## Export

`seneca export` prints the entries of a date range, complete and unchanged, as
one Markdown packet on standard output. It exists for the case where something
else — a person, a skill, a script — needs the writing itself rather than a
summary of it.

```bash
seneca export --from 2026-03-01 --to 2026-03-07
seneca export --from 2026-03-01 --to 2026-03-01 > day.md
seneca export --month=2026-03   # same as --from 2026-03-01 --to 2026-03-31
```

Give the range one of two ways. `--from` and `--to` are used together and both
take a canonical `YYYY-MM-DD` calendar date. **Both ends are included.** A date
must be zero-padded and real: `2026-3-1` and `2026-02-29` are refused rather
than guessed at. The range must run forward; `--from` after `--to` is an error.
Dates are not positional arguments.

`--month=YYYY-MM` is shorthand for that month's first and last day, and the
packet's `Range` line shows those two dates. It cannot be combined with
`--from` or `--to`. Unlike `show --month` and `review --month`, it has no bare
form meaning "this month", and neither bound defaults to today: the same command
always selects the same days, whenever it runs.

A bound in the future is allowed. It selects the entries that exist and invents
nothing: days you never wrote are simply absent, and gaps inside the range are
not filled.

The packet is a provenance header followed by the entries in date order:

```markdown
# Seneca journal export

- Source: /Users/you/Documents/Seneca/reflections.md
- Range: 2026-03-01 to 2026-03-02 (inclusive)
- Entries: 2
- Dates: 2026-03-01, 2026-03-02
---

## 2026-03-01

### What did I notice today?
…
---

## 2026-03-02

### What did I notice today?
…
---

```

`Source` is always an absolute path, even when `SENECA_FILE` holds a relative
one, so the packet says where its evidence came from no matter where it is read.

Each entry is reproduced byte for byte: its own headings, any sections you added
by hand, Seneca's markers, and its own line endings. Nothing is translated,
truncated, summarized, reordered inside an entry, styled, or time-stamped, so
the same journal and the same range always produce the same packet.

Export is strictly read-only. It creates nothing, writes no `.bak`, commits
nothing, reads no review archive, and calls no AI harness.

It refuses, before printing anything, when:

- the journal holds an entry Seneca cannot date, or a date used twice — checked
  across the whole file, not only inside the range, because either one means the
  entries in the range are not the entries you wrote there;
- the file cannot be read;
- the range selects no entry at all.

Refusals go to standard error and exit nonzero; standard output stays empty, so
a redirected packet is never a half-written one.

## Share packets

An export is evidence for a tool: the entries byte for byte, with the file they
came from. A **share packet** is for a person: a sponsor, a spiritual director,
a friend. The iPhone makes one from a saved review, the entries of chosen days,
or both, through the bridge's `share.packet`, and sends it with the iOS share
sheet as Markdown or as a PDF. It is the free half of
[F9](product/scope.md#f9--share-packets-free-and-practitioner-inbox-practitioner).

- **Summary first.** Sharing a review shares the review alone. The days of its
  window are listed, off, and each one turned on adds that day's entry.
  **Share** on a day in History shares that day.
- **Previewed exactly.** The composer shows the packet's Markdown, or the PDF
  page by page, as it will be sent. Changing the days or the format makes it
  again.
- **Written for a reader.** The packet names no file on the device and no
  record identity, and carries none of Seneca's markers. Each entry keeps its
  own headings, sections, and words; a follow-through reads as the quoted
  intention and the response.
- **Sent only where you send it.** Seneca sends nothing: the share sheet does,
  to the app and person you choose. The file is made in the app's temporary
  directory and replaced by the next packet.

```markdown
# Weekly review — 2026-03-02 to 2026-03-08

Shared from a Seneca journal on 2026-03-09. It holds a weekly review, and the entries of 1 day: 2026-03-04.

---

The review, as saved.

---

## 2026-03-04

### What will I carry into tomorrow?
Finish the draft before lunch.
```

A packet that would hold nothing, or names a review or a day the journal does
not have, is refused: a day is never left out in silence.
