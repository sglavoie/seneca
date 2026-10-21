[← Product scope](scope.md)

# Practice packs

A practice pack is a way of keeping an evening journal, packaged: the questions
to ask, which of them carries tomorrow's intention, a suggested reminder time,
what reviews should listen for, and a description. It is how Seneca reaches
beyond Stoicism without becoming a different program for each practice. This
page is the contract for [F3](scope.md#f3--practice-packs).

## What a pack holds

A pack is one JSON document, written to a file named `*.seneca-pack.json`:

```json
{
  "seneca_pack": 1,
  "id": "step-10",
  "version": 1,
  "name": "Step 10 nightly inventory",
  "description": "The nightly review of the eleventh-step prayer and tenth step…",
  "questions": ["Where was I resentful, selfish, dishonest, or afraid today?", "…"],
  "anchor_question_index": 4,
  "reminder_time": "21:30",
  "guidance": "This journal keeps a Step 10 nightly inventory…"
}
```

| Field | Rule |
| --- | --- |
| `seneca_pack` | The format version. This engine reads and writes `1`; any other is refused. |
| `id` | 1 to 40 characters of lowercase letters, digits, and single hyphens, starting with a letter. It names the practice, not the file. |
| `version` | The pack's own revision, an integer of at least 1, raised by whoever edits it. |
| `name` | 1 to 60 characters on one line. |
| `description` | Up to 280 characters on one line. Shown when choosing a pack, never sent to a model. |
| `questions`, `anchor_question_index` | Held to the same rules as the journal settings: at least one question, none blank, none repeated; the anchor is 0 (none) or a 1-based index. |
| `reminder_time` | Optional. `HH:MM`, a time of day. |
| `guidance` | Optional. Up to 1,000 characters. It is added to every review prompt that covers a day the pack was in force, so it is written to a model: tone, vocabulary, what counts as progress. |

An unknown field, a file over 64 KiB, or text that is not valid UTF-8 is
refused, never guessed at. The same pack always encodes to the same bytes.

## Built-in packs

| ID | Name | Anchor |
| --- | --- | --- |
| `stoic` | Stoic evening review | 3 of 3 |
| `step-10` | Step 10 nightly inventory | 4 of 4 |
| `examen` | Ignatian examen | 5 of 5 |
| `gratitude` | Gratitude and intention | 3 of 3 |
| `virtues` | Virtue tracking | 4 of 4 |

`stoic` asks exactly the default questions, in the default order, with the
default anchor. Applying it to a journal that never changed them changes no
question, and adds only its guidance.

Built-in packs are compiled into the engine, so the desktop and the iPhone offer
the same ones. `seneca pack export <id>` writes one out as a file to start a
custom pack from.

## Applying a pack

Applying a pack goes through the journal settings: the pack's questions and
anchor replace the journal's, as one unit and on the revision they were chosen
against, exactly like `seneca settings set`. Nothing in the Markdown is
rewritten. Every entry already written keeps its answers under its own headings,
its follow-through snapshot, and its anchor, so it reads as it did under the
questions it was written with.

A journal without settings of its own gets them in the same commit: the
settings it was opened with are recorded as its `legacy` settings, exactly as
`seneca settings init` would have recorded them, and the pack's become the ones
in force.

The settings document then also records the practice: each pack applied, whole,
with the journal day it was applied on.

```json
{
  "version": 2,
  "reflection": {"questions": ["…"], "anchor_question_index": 4},
  "legacy": {"questions": ["…"], "anchor_question_index": 3},
  "practice": [
    {"from": "2026-10-17", "pack": {"seneca_pack": 1, "id": "step-10", "…": "…"}}
  ]
}
```

- **The pack is copied in.** The journal never needs the file again, a pack
  imported on one device is in force on every device the journal syncs to, and
  editing the file later changes nothing until it is applied again.
- **One pack a day.** Applying a second pack the same day replaces the first in
  the history; applying the pack already in force changes nothing.
- **Version 2 only when there is a practice.** A journal that never applied a
  pack keeps writing version 1, byte for byte, so nothing changes for a journal
  that does not use packs and an older Seneca can still read it. An engine that
  only reads version 1 refuses a version 2 document and says it was written by
  a newer Seneca.
- **Editing the questions afterwards keeps the practice.** `seneca settings set`
  after a pack leaves the pack in force, and its guidance in the reviews.
  `seneca settings` says the questions were changed since the pack was applied.
- **The reminder time is a suggestion.** Reminders are chosen on each device, so
  applying a pack schedules nothing. The desktop prints the suggested time; the
  iPhone offers it.

## Reviews

A weekly or monthly review covers a span of days, and a quarterly or yearly
review the span of its window. When no pack was in force on any day of it, the
prompt is exactly what it was before packs existed.

Otherwise the prompt names each pack in force during the window, with the days
it covered inside the window, and that pack's guidance:

```text
Practice in force during this period:
- 2026-10-12 to 2026-10-14: no practice pack.
- 2026-10-15 to 2026-10-18: Step 10 nightly inventory. Guidance: This journal keeps…
Read each entry under the guidance of the practice in force on its date.
```

and calls the journal a reflection journal rather than a Stoic one. The response
schema, the validation, the citations, and the archive record do not change.

## Where it is used

| Surface | What it does |
| --- | --- |
| `seneca pack list` | The built-in packs, marking the one in force. |
| `seneca pack show <id or file>` | A pack's questions, anchor, reminder time, and guidance. |
| `seneca pack apply <id or file>` | Applies it, commits the settings, and prints the suggested reminder time. |
| `seneca pack export <id>` | A built-in pack as a file, on standard output. |
| `seneca settings` | Adds the practice in force and since when. |
| Bridge `packs.list`, `packs.read`, `settings.apply_pack` | The same, for the iPhone. |
| iPhone onboarding and Settings, Practice | The built-in packs, each shown whole before it is applied, and **Open a Pack File**. |
| iPhone, a `.seneca-pack.json` opened from AirDrop, Files, Mail, or a link | Shows the pack, and applies it only when asked. |

The iPhone's screens call the bridge operations above and decide nothing the
engine does not. Applying a pack on a journal with days written first says
that those days keep their questions. After applying, the iPhone offers the
pack's reminder time, which replaces its reminder times only if accepted.

iOS types a file by its last extension, so Seneca offers to open any JSON file,
as an alternate handler that never takes over JSON from another app, and the
engine refuses one that is not a pack. The file is copied in and read; nothing
is applied until the author chooses to.

## Acceptance

- Switching packs mid-journal keeps every past entry readable under its original
  questions: its answers, its follow-through, and its anchor read the same
  before and after.
- The review prompt includes the guidance of the pack in force for each window,
  and is unchanged for a window no pack covered.
- A journal that never applies a pack writes the same settings document it
  always has.
