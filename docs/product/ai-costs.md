[← Product scope](scope.md)

# AI costs

What a review costs on a high-quality model, how much of a subscription that
consumes, and how the numbers stay true as prices move.

**Prices verified 2026-10-14** against the providers' official pages (sources
at the bottom). Every figure below that is not a price is an *estimate* until
the usage ledger described in [scope § P0](scope.md#p0--measure-personal) has
recorded real reviews. The single biggest unknown is how many **thinking
tokens** each model spends on a review; it dominates the bill.

## Price table (USD per million tokens, standard tier)

| Model | Input | Output | Cache read | Batch (in / out) | Notes |
| --- | --- | --- | --- | --- | --- |
| Claude Opus 5.5 | $4.00 | $20.00 | $0.20 | $2.00 / $10.00 | Thinking always on; effort defaults to `medium`. 1M context at one rate. |
| Claude Fable 5.1 | $10.00 | $50.00 | $0.25 | $5.00 / $25.00 | **Not available under zero data retention** without Anthropic's express authorization: 30-day retention required. |
| Claude Sonnet 5.5 | $2.00 | $10.00 | $0.10 | $1.00 / $5.00 | |
| OpenAI GPT-6 Astra | $10.00 | $50.00 | $1.00 | $5.00 / $25.00 | Above 272K input: $20 / $75 for the whole request. |
| OpenAI GPT-6.1 Sol | $2.00 | $10.00 | $0.10 | $1.00 / $5.00 | Above 272K input: $4 / $15. |
| Google Gemini 3.1 Pro (preview) | $2.00 | $12.00 | — | $1.00 / $6.00 | Above 200K input: $4 / $18. Paid-tier data not used for training. |

Modifiers that apply to the Claude rows:

- **Prompt caching:** writing to a 5-minute cache costs 1.25× input, a 1-hour
  cache 2×. Reads are 0.05× input on Opus 5.5 and Sonnet 5.5.
- **Batch:** 50% off, and it stacks with caching.
- **US-only inference** (`inference_geo: "us"`): 1.1× on everything.
- OpenAI regional endpoints add 10%.

## What one review sends

Measured from a real journal of about 130 entries and
`cmd/review_prompt.go`:

- **Fixed overhead per request:** about 1.5K tokens of instructions plus the
  JSON schema (`reviewSchema`).
- **Entry size:** about 676 characters on average (p90 904, max 1,737), or
  about 200 tokens. Each answer is capped at 500 runes before it reaches the
  prompt (`maxPromptAnswerRunes`), so even a heavy writer tops out around 600
  tokens per entry.
- **Weekly review:** 7 entries, about 3K tokens in.
- **Monthly review:** about 30 entries, about 7.5K tokens in.
- **Arc review:** saved review bodies, each capped at 2,000 runes
  (`maxPromptReviewBodyRunes`), about 12 × 800 tokens for a quarter.
- **Visible output:** about 1K tokens of JSON.
- **Thinking: unknown.** Modeled as 3K tokens (medium effort) and 8K tokens
  (high effort). This needs measuring.
- **"Ask your journal":** a year of entries (about 73K tokens typical, 220K
  heavy), cached with a 1-hour TTL. Each question adds about 300 tokens in and
  2.8K out, thinking included.

## Monthly cost per user (estimates)

"Core" means 4.33 weekly reviews, one monthly review, and a third of a
quarterly review. "Ask" means 10 questions a month over a year of entries.

| Model, effort | Core | Core, batched | Ask ×10 (typical / heavy writer) |
| --- | --- | --- | --- |
| Opus 5.5, medium | $0.55 | $0.27 | $1.08 / $2.10 |
| Opus 5.5, high | $1.11 | $0.56 | $1.08 / $2.10 |
| Fable 5.1, high | $2.79 | $1.39 | $2.52 / $4.71 |
| GPT-6 Astra, high | $2.79 | $1.39 | $3.07 / $6.36 |
| Sonnet 5.5 / GPT-6.1 Sol, high | $0.56 | $0.28 | $0.54 / $1.05 |
| Gemini 3.1 Pro, high | $0.66 | $0.33 | $0.67 / $1.33 |

The script that produced these numbers is reproduced in
[Recomputing](#recomputing).

## What a subscription can carry

| Plan | List price | Net after Apple (15%, Small Business Program) | Net per month | AI budget at 30% of net |
| --- | --- | --- | --- | --- |
| Plus, annual | $69.99/yr | $59.49 | $4.96 | **$1.49** |
| Plus, monthly | $7.99/mo | $6.79 | $6.79 | **$2.04** |
| Practitioner (web, Stripe ~3%) | $29/mo | ~$28.10 | $28.10 | ~$8.40, shared across the practitioner's client seats |

Reading the two tables together:

- **Opus 5.5 is affordable as the default for every review.** Batched core
  reviews at high effort (about $0.56) plus a typical writer's 10 questions
  (about $1.08) come to about $1.64 a month. That is at the annual budget.
  The cap and the batching below keep it there.
- **GPT-6 Astra and Fable 5.1 cost about 2.5× Opus.** Use them only where the
  output is rare and valuable: the quarterly and yearly arc review, about
  $0.15–0.30 per run. Never use them for Ask. Fable 5.1 additionally conflicts
  with the zero-retention promise (see [scope § privacy](scope.md#privacy-and-safety)),
  so it is out unless Anthropic authorizes it under zero data retention.
- **A lifetime price cannot include hosted AI.** A year of a heavy writer's AI
  usage on Opus costs about $25–40.

## Guardrails (requirements for the hosted service)

1. **The price table is data, not code.** The service reads `pricing.toml`,
   which holds a `verified` date and a source URL per row. The desktop
   already embeds it (`review/usage/pricing.toml`). Every request records
   its cost from the response's `usage` against that table.
2. **Monthly allowance per entitlement.** Plus carries a soft budget of $1.50
   and a hard stop at $3.00 a month. Past the soft budget, Ask drops to medium
   effort. Past the hard stop, Ask pauses until the next cycle; scheduled
   reviews always run.
3. **Scheduled reviews go through the Batch API.** A weekly review that
   arrives an hour later costs half.
4. **Ask uses a 1-hour prompt cache** over the entry corpus, in a stable order
   (entries oldest first, instructions before entries).
5. **Model routing is configuration.** Each review kind sets its provider,
   model, and effort. Defaults:

   | Review kind | Provider and effort |
   | --- | --- |
   | Weekly | Apple Private Cloud Compute, if it passes the P0 comparison; otherwise Opus 5.5 at high effort |
   | Monthly | Opus 5.5 at high effort |
   | Quarterly and yearly | Opus 5.5 at max effort (GPT-6 Astra as an alternative) |
   | Ask | Opus 5.5 at medium effort |

   Moving weekly reviews to Apple's free models cuts core cost by about 75%
   (see [Apple's models](apple-models.md#where-apples-models-lower-the-cost-of-reviews)).
6. **Repricing trigger.** Each month, compute the p50 and p90 AI cost per paying
   user from the ledger. If the p90 exceeds 40% of net revenue per user for two
   consecutive months, change the allowance, the routing, or the price.
7. **Re-verify prices monthly**, and whenever a provider announces a model.
   Bump the `verified` date even when nothing changed.

## Recomputing

```python
# $/MTok: input, output, cache_read  (verified 2026-10-14)
M = {'Opus 5.5': (4, 20, 0.20), 'Fable 5.1': (10, 50, 0.25), 'Sonnet 5.5': (2, 10, 0.10),
     'GPT-6 Astra': (10, 50, 1.0), 'GPT-6.1 Sol': (2, 10, 0.10), 'Gemini 3.1 Pro': (2, 12, 0.20)}
OVER, OUT = 1500, 1000  # prompt overhead, visible JSON output

def review(model, entries, tok_per_entry, think, batch=False):
    i, o, _ = M[model]
    f = 0.5 if batch else 1
    return f * ((OVER + entries * tok_per_entry) * i + (OUT + think) * o) / 1e6

def ask(model, ctx, n, think=2000):  # 1h cache write 2x would be slightly higher
    i, o, c = M[model]
    return ctx * i * 1.25 / 1e6 + n * (ctx * c + 300 * i + (800 + think) * o) / 1e6
```

When the ledger has real thinking-token counts, replace `think` with the
measured p50 and p90 and regenerate the tables.

### From the usage ledger

`seneca review` records every attempt in the usage ledger (see
[docs/review.md](../review.md#usage-and-cost)), and `seneca usage --json`
summarizes it per month and review kind:

```json
{
  "groups": [
    {"month": "2026-11", "kind": "weekly", "count": 4, "cost_known": 4,
     "total_cost_usd": 0.61, "cost_p50_usd": 0.15, "cost_p90_usd": 0.18,
     "thinking_known": 4, "thinking_p50": 7400, "thinking_p90": 9100}
  ],
  "skipped_lines": 0
}
```

To regenerate the tables above from four weeks or more of real use:

1. Run `seneca usage --json > usage.json` after the period.
2. For each review kind, take `thinking_p50` and `thinking_p90` from the
   `groups` of the months covered (weighting by `thinking_known` when a kind
   spans several months, which is exact only for one month and close enough
   otherwise) and use them as `think` in `review()` above: the p50 for the tables and the p90 for a second,
   upper-bound run. Groups with
   `thinking_known` of 0 come from a harness that does not report thinking
   and are left out.
3. Check `cost_p50_usd` and `cost_p90_usd` against what `review()` now
   predicts for the same model and effort. They are priced from the same
   `pricing.toml`, so a gap means the prompt-size assumptions (`OVER`,
   `tok_per_entry`, `OUT`) are off; correct those from the ledger's token
   counts.
4. Replace the "Monthly cost per user" table with the regenerated numbers,
   and change "estimates" to "measured" with the months they cover.

## Sources

- Anthropic pricing, retrieved 2026-10-14: <https://platform.claude.com/docs/en/about-claude/pricing>
- OpenAI pricing, retrieved 2026-10-14: <https://developers.openai.com/api/docs/pricing>
- Gemini API pricing, page dated 2026-10-14: <https://ai.google.dev/gemini-api/docs/pricing>
- Apple Small Business Program (15% commission): <https://developer.apple.com/app-store/small-business-program/>
