## AI review

`seneca review` turns recent reflections into a weekly or monthly synthesis with
recurring struggles, wins, carry-forward intentions, an assessment of how the
current focus actually went, and one recommended next focus.

```bash
seneca review --harness claude
seneca review --week --harness codex --model gpt-5.5
seneca review --month --harness copilot
seneca review --quarter --harness claude
seneca review --year=2026 --harness claude
seneca review --harness codex --timeout=30m
seneca review --harness anthropic --effort xhigh
seneca review --harness openai --model gpt-6-astra
SENECA_HARNESS=claude SENECA_MODEL=sonnet seneca review
```

- `--harness` must be one of `claude`, `copilot`, `codex`, `anthropic`, or
  `openai`. It overrides `SENECA_HARNESS`, which overrides `[review].harness`
  in `config.toml`. The first three run a CLI you have installed; `anthropic`
  and `openai` call the model's API directly with your own key.
- `--model` overrides `SENECA_MODEL`, which overrides `[review].model`.
- `--effort` (`low`, `medium`, `high`, `xhigh`, `max`) overrides
  `SENECA_EFFORT`, which overrides `[review].effort`. Only `anthropic` and
  `openai` can honor it; an effort from any source paired with a CLI harness is
  refused, naming the source that set it.
- `--timeout` bounds the harness run and takes a Go duration (`90s`, `15m`,
  `1h30m`). It defaults to **10 minutes**. A harness that runs past the limit is
  stopped — along with every process it spawned, including ones that detached
  into a process group of their own and ones whose parent exited and left them
  with no link back — and the run ends explaining which limit stopped it;
  nothing is written. Seneca can do that because it samples the process tree
  four times a second for the whole run rather than looking only once the
  deadline lands, so reparenting cannot hide work it has already seen; a process
  both started and detached inside a single sample is the one thing that can
  still slip through, and the run stops waiting on it either way. A zero or
  negative duration is refused when the flag is parsed. Raise it for a yearly
  arc review on a slow model; lower it if you would rather find out quickly that
  a harness is stuck.
- The selected harness CLI must already be installed and authenticated.
  `anthropic` and `openai` are not CLIs: see [Direct providers](#direct-providers)
  below.
- Weekly review is the default window; `--month` reviews the current month so
  far, or accepts `--month=YYYY-MM` for a specific month.
- `--week`, `--month`, `--quarter`, and `--year` are mutually exclusive. Setting
  more than one is refused in a single message naming every one you set.

### Harness configuration

The harness is the one setting every review needs. Put it in
`~/.config/seneca/config.toml` and stop re-supplying it:

```toml
[review]
harness = "claude"
model = "opus"
```

Both settings resolve **flag, then environment, then config file**:

| Setting | Flag | Environment | Config |
| --- | --- | --- | --- |
| Harness | `--harness` | `SENECA_HARNESS` | `[review].harness` |
| Model | `--model` | `SENECA_MODEL` | `[review].model` |
| Effort | `--effort` | `SENECA_EFFORT` | `[review].effort` |

With none of the three set, `seneca review` refuses to run and names all three.
`--timeout` is deliberately flag-only and has no config or environment source.

`harness` is validated when the config is read, not when a review runs, so a
typo is something `seneca doctor` reports rather than something you discover
mid-review. Omit either key to leave it unset; writing one and leaving it blank
is refused, exactly as a blank `reflections_path` is. `effort` is validated the
same way.

### Direct providers

`--harness anthropic` and `--harness openai` call a model API directly through
the provider's official Go SDK instead of running a CLI. The reply goes through
the same validation, decision, and save as a harness reply, and saves an
identical record.

| Harness | Default model | Default effort | Key: environment | Key: Keychain service |
| --- | --- | --- | --- | --- |
| `anthropic` | `claude-opus-5-5` | `high` | `ANTHROPIC_API_KEY` | `seneca-anthropic` |
| `openai` | `gpt-6-astra` | `high` | `OPENAI_API_KEY` | `seneca-openai` |

```toml
[review]
harness = "openai"
model = "gpt-6-astra"   # optional; the default above applies when omitted
effort = "xhigh"        # optional; low, medium, high, xhigh, max
```

**Anthropic** uses the Messages API:

- The request streams, uses adaptive thinking with the configured effort, and
  asks for Seneca's review schema as structured output.
- Server-side refusal fallback is on: a policy decline is retried on the
  server's default fallback model in the same call.

**OpenAI** uses the Responses API:

- The review schema is sent as a strict JSON-schema format, and the effort is
  sent as the reasoning effort.
- The request sets `store: false`, so OpenAI is not asked to keep the response
  for a later turn.

For both, a decline that survives (an OpenAI refusal or content-filter stop
counts as one), a reply cut off at the output limit, or a rejected key ends the
run with an error that says what to change, and nothing is written. `--timeout`
bounds the whole call, as it bounds a CLI harness, and a call that runs past it
reads exactly like a CLI timeout.

#### Key storage

API keys are read from the provider's environment variable, then from the macOS
Keychain, and never from `config.toml`; the `[review]` table has no key setting
at all. To keep a key out of your shell environment, store it once:

```bash
security add-generic-password -a "$USER" -s seneca-anthropic -w
security add-generic-password -a "$USER" -s seneca-openai -w
```

`-w` with no value prompts for the key, so it never lands in your shell history.
`seneca doctor` reports which source supplies the configured provider's key —
`ANTHROPIC_API_KEY`, `Keychain (seneca-openai)`, and so on — never the key, and
never calls the API.

#### What leaves the machine

Only the review prompt described in [What is sent to the AI](#what-is-sent-to-the-ai)
— the selected window's reflections (or, for an arc review, the qualifying saved
reviews), the focus under assessment, and the window's dates — is sent, to the
one provider you configured, together with your key. Nothing else from the
journal, the archive, or your config is uploaded, and no other command contacts
a provider.

### Arc reviews

`--quarter` and `--year` are *arc* reviews: they synthesize the reviews Seneca
already saved rather than the raw reflections underneath them.

```bash
seneca review --quarter          # the quarter today falls in
seneca review --quarter=2026-Q3  # a specific quarter
seneca review --year             # this year so far
seneca review --year=2026        # a specific year
```

A value needs the `=` form, exactly as `--month` does: the flag is optional-value,
so `--quarter 2026-Q3` would read the value as a stray argument.

- The reflections file is never opened for an arc review; the review archive is.
- **Only strictly shorter periods count as sources.** A quarterly review reads
  the weekly and monthly reviews whose windows closed inside the quarter; a
  yearly review reads those plus the quarterly ones. A review never ingests its
  own period, so it can never treat its own prior output as evidence.
- A period still running is reviewed up to today, never past it.
- Evidence is cited by a source review's *window end date*, not by a reflection
  date, and a citation that names a date Seneca never sent is refused.
- Coverage is reported as a count of source reviews, not a count of days: a
  quarter is not expected to hold ninety entries.
- With no qualifying source review, `seneca review --quarter` says so and stops.
  Nothing is sent to a harness, and nothing is written. The window is selected
  before a harness is required, so this answer arrives even with no harness
  configured at all — you are told your archive is empty rather than told to
  set `--harness` first.
- A theme normally has to *recur across* the source reviews. With exactly one
  source that rule cannot be met, so the prompt instead asks for what that
  single review supports and forbids only the claim that a pattern recurred
  across reviews.

Arc reviews are useful once a few months of weekly reviews have accumulated —
a quarter with one saved review is a synthesis of one review.

### Decisions

When the review is ready, you choose what happens to it:

| Key | Decision | Effect |
| --- | --- | --- |
| `a` | accept focus | Saves the review and makes the recommended focus current. |
| `e` | edit focus | Opens the recommendation for editing, then saves both. |
| `s` | save only | Saves the review; the existing focus is left alone. |
| `d` | discard | Writes nothing at all. |

Leaving without choosing — `Esc`, `Ctrl+C`, a closed terminal — discards. The
recommendation text is kept inside the saved review either way, so declining it
as a focus does not throw it away.

Reviewing the same window twice does not overwrite the first result. The newer
review is appended and records that it *revises* the older one; `seneca review
history` shows the current one.

### History

```bash
seneca review history            # the most recent saved review
seneca review history --last 5   # the five most recent, newest first
seneca review history --period monthly --last 3   # the last three monthly reviews
```

`--period` takes `weekly`, `monthly`, `quarterly`, or `yearly`, and completes in
the shell. It filters before `--last` counts, so `--period monthly --last 3` is
always three monthly reviews when three exist.

### Usage and cost

Every review attempt that reaches its harness or provider appends one line to a
usage ledger, whether it is saved, discarded, piped, refused, cut short, timed
out, or answered with something that is not a valid review:

```text
<journal dir>/.seneca/journals/<reflections filename>/desktop/usage.jsonl
```

A line holds the time, the review kind and window, the harness, provider, model
and effort, the input, cache-read, cache-write and output tokens, thinking
tokens where the provider reports them, the cost in USD, the latency, and the
outcome. It holds nothing you wrote and nothing the model wrote. The ledger is
created by the first review that needs it, is private to this computer, and is
never committed or synced. A ledger that cannot be written is a warning; the
review goes on.

What each harness reports:

| Harness | Tokens | Cost |
| --- | --- | --- |
| `anthropic`, `openai` | From the API's usage, thinking included | Priced from the pricing table |
| `claude` | From its JSON result event, when present | The `total_cost_usd` it reports |
| `codex`, `copilot` | None | None; the line records outcome and latency only |

The pricing table is built into Seneca: one row per provider and model, with the
input, output, cache-read and cache-write (5-minute and 1-hour) rates in USD per
million tokens, the batch discount, the date the rates were verified, and the
page they came from. A row in `~/.config/seneca/pricing.toml` with the same
`provider` and `model` replaces the built-in row whole, and a new pair adds one:

```toml
[[price]]
provider = "anthropic"
model = "claude-opus-5-5"
input = 4.00
output = 20.00
cache_read = 0.20
cache_write_5m = 5.00
cache_write_1h = 8.00
batch_discount = 0.5
verified = 2026-10-14
source = "https://platform.claude.com/docs/en/about-claude/pricing"
```

A model with no row is recorded without a cost rather than guessed at. With a
provider harness configured, `seneca doctor` warns when the oldest `verified`
date in force is more than 45 days old and when the configured model has no
row, and counts the ledger's lines without printing any.

```bash
seneca usage          # per month and review kind: attempts, total cost, p50/p90 cost and thinking tokens
seneca usage --json   # the same summary as data
```

Percentiles are nearest-rank over the attempts that reported the value; `—` (or
`null` in JSON) means none did. `seneca usage` only reads the ledger and never
creates it.

### What is sent to the AI

For a weekly or monthly review, the prompt contains only the reflections inside
the selected window, plus the focus text under assessment and the window's
dates. Every parsed section of those entries is included — including headings
you added by hand and the follow-through narrative — because dropping them would
starve the review of the continuity it is asked to judge. Nothing outside the
window is sent: not older entries, not the archive, not previous reviews, not
your config.

For a quarterly or yearly review, the prompt contains the qualifying saved
reviews instead — their titles, periods, bounds, and bodies — plus the focus
under assessment and how the focus moved during the period. No reflection ever
reaches an arc prompt.

Either way the model is told to use only the material supplied, and every
evidence date it returns is checked against what was actually sent; a review
citing a day that was not supplied is rejected rather than saved.

The reply is held to the same JSON schema Seneca hands the harness, checked
locally before anything is shown or saved: an unknown property, an omitted
required one, a value of the wrong type, or a second JSON value after the
response is refused with the offending field named. `focus_assessment` must be
answered explicitly — `null` when no focus applied to the window, an object with
a summary when one did — so an omitted verdict can never read as "no focus".

Reviews run only when you ask for one. No other command contacts a harness or a
provider — including `seneca doctor`, which checks that a CLI harness exists on
`PATH` without running it, and that a provider has a key without calling it.

## The review archive

Saved reviews and focus events live in `seneca-reviews.md`, beside the resolved
reflections file. It is append-only and plain Markdown:

```markdown
# Seneca Review Archive
<!-- seneca:archive v=3 -->

## Weekly review — 2026-08-17 to 2026-08-23
<!-- seneca:review v=3 id=… created=… period=weekly start=… end=… -->

### Struggles
…

---

## Focus set — 2026-08-24
<!-- seneca:focus v=3 id=… created=… event=set effective=2026-08-24 origin=review -->

Protect the first hour.

---
```

A record begins at a `## ` heading whose next line is a Seneca marker. Anything
else — prose you write at the top, `## ` headings and `---` rules inside a
review body — is left byte-identical. Each record carries an `id` derived from
its own content, which is how `seneca doctor` can tell you a record was edited
after Seneca wrote it. Saving a review and changing focus recompute the same
ids, so they refuse to append to an archive doctor calls corrupt rather than
building on a record that no longer says what it was signed for. Reading is
unaffected: an edited record still displays, and nothing rewrites it for you.

The archive is optional. It does not exist until the first saved review or the
first focus, and no command creates it just to read it.
