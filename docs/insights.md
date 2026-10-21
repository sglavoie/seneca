[← Seneca](../README.md)

# Follow-through insights

What became of the intentions the journal carried from one day to the next,
and how long each focus stood. It is structural reporting over what the journal
already holds: no model reads anything, nothing is scored, and nothing is
written. This page is the contract for [F4](product/scope.md#f4--follow-through-insights).

| Surface | Window |
| --- | --- |
| `seneca insights` | The last 30 days, today included. `--days N`, `--from`/`--to`, or `--all` for another. |
| iPhone, Reviews, Follow-Through | The last 30 days. |
| Bridge `insights.get` | `days`, or `from` and `to`; 30 days when nothing is given. |

## Intentions

An intention is a day's anchor answer, the answer carried into the next day.
Every intention written in the window is counted once, by what the entries
written after it did, inside the window or later:

| Status | The CLI says | When |
| --- | --- | --- |
| `answered` | answered | A later entry's follow-through carried it and wrote a response. |
| `unanswered` | unanswered | A later entry carried it and left the response blank. |
| `not-carried` | not carried | Entries were written after it and none carried it, as when they were written with `--blank`, or carried an older day. |
| `open` | open | Nothing has been written since. The next entry carries it. |
| `untracked` | untracked | Written before the journal carried any intention, so no later day could have answered it. |

`untracked` is how a journal kept before follow-through existed reads: the
first day any follow-through answers marks where tracking starts, and the
intentions before it are counted apart rather than as not carried.

To check one by hand, `seneca show --date <day>` shows the intention, and the
follow-through of a later day names the date it answers
(`<!-- seneca:follow-through source=<day> -->`).

## Words that return

- **Written more than once.** Intentions whose words are the same once case,
  punctuation, and spacing are set aside. `Walk after lunch.` and
  `walk after LUNCH` are one intention written twice.
- **Words that return.** Words of four letters or more found in the intentions
  of three days or more, the most frequent first, at most twelve. Common English
  and French words that say nothing about an intention (`about`, `today`,
  `make`, `sure`, `avec`, `faire`, …) are left out. `seneca show --search <word>`
  finds every line that holds one.

Both are literal and lexical: two intentions about the same thing in different
words are not matched.

## Focus

Each focus event in the archive, paired with the one after it exactly as
`seneca focus history` pairs them: when it took effect, the day it was
replaced (or that it is current), the calendar days it stood, and what replaced
it, another focus or a clear. A span is shown when it stood on any day of the
window. A damaged archive leaves the focus out and says why; the intentions are
still reported.

## Editions

Free covers the last 30 days and Plus the full history, once there is a Plus
([F10](product/scope.md#f10--entitlements-and-billing)). Until then the phone
shows 30 days and the desktop, which is the author's own, takes any window.
