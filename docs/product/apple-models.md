[← Product scope](scope.md)

# Apple's models: what they are good enough for

Measured 2026-10-14 on an Apple M2 Ultra running macOS 27.0.1, with Xcode 27.
The phone runs the same system model, but slower, so the timings below are a
lower bound; [Measured on the iPhone](#measured-on-the-iphone) holds what the
author's iPhone gives. The rig is
[`scripts/apple-models/`](../../scripts/apple-models/run.sh). It runs
Seneca's real review path (prompt, schema, and grounding validation)
against Apple's models through a stand-in `claude` harness, on a copy of the
journal, and never writes.

## What is available

| Model | Context | Cost to Seneca | Requirements |
| --- | --- | --- | --- |
| On-device `SystemLanguageModel` | **4,096 tokens**, input and output together | Free, offline | iOS 26+, an Apple Intelligence device, Apple Intelligence enabled |
| `PrivateCloudComputeLanguageModel` | **32,768 tokens** (measured here) | Free to the developer. Users have a daily quota, raised by iCloud+ | iOS 27+. The managed entitlement `com.apple.developer.private-cloud-compute`, granted on application to apps under 2M downloads (secondhand reports; confirm with Apple). Reasoning levels light, moderate and deep. Apple says it never stores request data |
| `SpeechTranscriber` / `SpeechAnalyzer` | Whole files | Free, offline after a one-time download per language | iOS 26+. No speech-recognition permission prompt needed |

PCC reported itself available here, but every request failed with
`ModelManagerError 1046`. That matches reports that it needs the entitlement,
which a command-line build cannot carry. **PCC quality is therefore
untested**, and testing it from a signed build is a P0 task.

## Results

### 1. Full reviews on the on-device model

Seneca's weekly-review prompt with schema, run twice per window on 14 real
weeks from one journal of 5–7 entries, and on the 7 calendar months.

| Window | Prompt size (on-device tokens) | Fits in 4K | Passes Seneca's validation |
| --- | --- | --- | --- |
| Calendar month, 13–25 entries | 3,940–5,157 | **No** (input plus output exceeds 4,096) | — |
| Calendar month, 9–12 entries | 2,268–2,629 | Usually | **0 of 4** runs without constraints. One run overflowed while generating. The others invented the date `2022-08-30` and filled in a focus assessment when none applied |
| Week, 5–7 entries | 1,225–1,991 | Yes | **36 of 42 runs** (3 passes over 14 weeks) with constrained decoding (below) |

- **Constrained decoding is required.** The guided-generation schema lists the
  only dates the model may cite, and forces `focus_assessment` to `null` when
  no focus applies. With that, all grounding failures disappeared. Seneca's JSON
  Schema has to be converted to Apple's `DynamicGenerationSchema`
  (see `review_harness.swift`). Decoding it directly fails on nullable types and
  on the missing `x-order` key.
- **Latency:** 10–42 s per weekly review, median about 19 s, on this Mac.
- **Every failure was Apple's guardrail** ("may contain sensitive or unsafe
  content"), on three of the weeks. The guardrail is
  not deterministic: one of those weeks passed twice, then was refused on the
  third pass.
  - Guided generation does not honor `permissiveContentTransformations`.
    Unguided output on the same week passed the guardrail but then broke the
    schema.
  - **The on-device model refuses exactly the weeks where a review matters
    most.** Every on-device feature therefore needs a fallback path, and must
    say plainly when it declines.
- **Quality:** read side by side with the entries.
  - Accurate and grounded: every citation was real and its snippet verbatim.
  - It finds the obvious recurring pattern.
  - It is mostly **extractive**. One run put all seven days under a single
    theme.
  - It occasionally makes a claim the entries don't support, such as wins
    credited to "Stoic priorities".
  - Its **focus recommendations are platitudes** ("Apply Stoic principles
    daily…").
  - Two runs on the same week differ noticeably.
  - **Verdict:** good enough for a free weekly *snapshot* (what happened, with
    the evidence), not for the insight and focus recommendation Plus sells.

### 2. Transcription

Synthetic speech (macOS `say`) was transcribed on-device with
`SpeechTranscriber(preset: .transcription)`, the path Seneca ships.

| Sample | Audio | Time (first run / warm) | Accuracy |
| --- | --- | --- | --- |
| English monologue with fillers | 40.4 s | 7.8 s / 2.8 s | Near verbatim. One misplaced sentence break |
| Québec French | 21.4 s | 5.2 s / 1.1 s | Near verbatim. One wrong gender ending (`pressée`), one misplaced sentence break |

Synthetic speech is a best case. Real voices, a noisy kitchen and code
switching between French and English still need testing on the phone.

### 3. Splitting a transcript into the journal's questions

The on-device model was given a transcript and the configured questions. It
returned one answer per question, plus "unassigned", in the speaker's words
with fillers removed (`split.swift`).

- **Questions that match what was said:** correct every time, in English and
  French, in 2–8 s.
  - The speaker's wording was kept, and fillers and false starts removed.
  - A remark that answered no question was set aside once, and filed under
    "did well" another time. Both are defensible.
- **Questions that don't match** (examen questions over a Stoic-style
  monologue):
  - Content was mis-filed.
  - One sentence was **garbled**: "before work before it never happens".
- **Verdict:** good enough to *propose* answers. The app must:
  - show the split for confirmation before saving;
  - keep the raw transcript with the entry;
  - never save model-altered text silently.

## Where Apple's models lower the cost of reviews

AI cost is dominated by output and thinking tokens (see
[AI costs](ai-costs.md)). Shrinking the *input* with on-device digests saves
almost nothing: cutting a monthly prompt from 7.5K to 3K tokens saves about
$0.01 on Opus 5.5. What saves money is **not making the cloud call at all**:

| Plus core reviews per month | All on Opus 5.5 (high, batched) | Weekly on device or PCC; monthly and arc on Opus |
| --- | --- | --- |
| Typical writer | $0.56 | **$0.14** (−75%) |

Recommended routing:

| Job | Model | Why |
| --- | --- | --- |
| Splitting and cleaning up a transcript | On-device | Fast, free, offline, good enough with confirmation |
| Condensing a long spoken answer to fit the prompt's 500-rune limit | On-device | Replaces blunt truncation (`maxPromptAnswerRunes`), which would cut most spoken answers |
| Free weekly snapshot | On-device, constrained | Grounded and free. Labeled as a snapshot, not insight |
| Plus weekly review | PCC (moderate reasoning), falling back to Opus 5.5 | Free to Seneca if PCC quality holds. **Gated on the P0 PCC evaluation** |
| Monthly, quarterly and yearly reviews, Ask, pattern threads | Opus 5.5 | Needs more than 4K and 32K contexts can hold, and real insight. A year of entries is about 73K tokens |
| Guardrail refusal on device or PCC | Opus 5.5 for Plus; a plain message for Free | Hard weeks must not end in a silent failure |
| Entry tagging for local search (`useCase: .contentTagging`) | On-device | Free, offline. Untested; part of P0 |

## Running it on the iPhone

The same prompts the Mac's rig runs, run on the phone by a debug-only screen,
Settings › Model Evaluation (`ios/SenecaMobile/Debug/`). Debug builds only: a
release build has no such screen. It never opens the journal; it reads and
writes `Application Support/Seneca/evaluation/` beside the app's other
device-local state, and nothing else.

```bash
scripts/apple-models/run.sh export REFLECTIONS.md [seneca-reviews.md]   # one prompt per qualifying week
just voice-qualify install                                              # a signed Debug build of these sources
scripts/apple-models/run.sh push                                        # prompts.json onto the iPhone
# on an Apple Intelligence iPhone: Settings › Model Evaluation › Run Evaluation, unlocked, until it says the responses are written
scripts/apple-models/run.sh pull                                        # responses.json back
scripts/apple-models/run.sh replay                                      # optional: see the verdicts first
just voice-qualify evaluation                                           # replay with seneca from these sources and record it
```

- **An iPhone that cannot run the model** (Apple Intelligence needs an A17
  Pro or later with 8 GB of memory) is not offered the screen at all, nor the
  model's sorting of a spoken take, which becomes Copy Into Your Questions.
  `push` relaunches the app, and on such an iPhone the app records the
  device's answer by itself (`deviceNotEligible`, no model called) for
  `pull` to fetch. An iPhone where Apple Intelligence is only turned off or
  not ready yet still shows both, and says why when they cannot run.
- **export** splits the journal into ISO weeks of five or more entries in one
  month, as `weekly` does, and runs `seneca review` on a copy of each with a
  stand-in `claude` that keeps the prompt and the schema it is handed and
  answers nothing, so no review is made and nothing is written. The export
  lives under `.cache/apple-models/export/` (ignored by Git): it holds journal
  text, and never leaves this Mac except as the prompts copied to the iPhone.
- **The screen** checks each prompt against its digest, then runs every
  prompt on the on-device model, and on Private Cloud Compute at the light,
  moderate, and deep reasoning levels when the build's provisioning profile
  grants `com.apple.developer.private-cloud-compute`. The schema is the
  review's own, converted as `review_harness.swift` does: the dates the prompt
  names are the only ones offered, and the focus assessment is forced to null
  when none applies. It writes each response verbatim with its time, the
  on-device token count of the prompt, why there was no response (refused,
  too long, unavailable, quota reached), whether each model was available and
  why not, and the battery, heat, and PCC quota at the start and end. A model
  that is not available is said, in Apple's own words, and not called.
- **replay** runs `seneca review` over each week again with a stand-in that
  answers with the phone's response, after checking the prompt Seneca builds
  now is the one the phone ran. Seneca's own parser and grounding checks
  decide what is valid. `replay.json` keeps the verdicts, the reasons, and
  the timings, and not the responses; it is what device qualification records.
- `run.sh` without an iPhone: `python3 -I scripts/apple-models/evaluation.py
  selftest` exports the synthetic week in `scripts/apple-models/testdata/`
  and replays a valid, an invalid, a refused, and a stale response through it.

Private Cloud Compute needs Apple to grant the managed entitlement to the
team, then `CODE_SIGN_ENTITLEMENTS` naming it in `ios/project.yml` for the
debug build. Until then the screen records `not-entitled` and calls nothing.

## Measured on the iPhone

Measured on 2026-10-16 on the author's iPhone 13 Pro (`iPhone14,2`, iOS
27.0.1), Debug build 1792197341 of these sources. The records are
[`docs/mobile/evidence/session-18-*.json`](../mobile/evidence/);
[Voice on the iPhone](../mobile/qualification.md#voice-on-the-iphone) is the
procedure, and `just mobile-verify 18` checks this section against them.

**The author's iPhone is not eligible for Apple Intelligence**, which needs an
iPhone 15 Pro or later. On it:

- the app hides the Model Evaluation screen and records the on-device model
  as `deviceNotEligible` by itself, calling nothing, so there are 0
  responses. That is the device's answer, recorded as such, not a failure of
  the model or of the app;
- Private Cloud Compute is not reached either: its entitlement has not been
  granted to Seneca, so the screen never calls it;
- a spoken take's Sort Into Your Questions becomes Copy Into Your Questions:
  the transcript is copied into each question by hand, and the model is never
  asked;
- recording and transcription do not depend on Apple Intelligence
  (`SpeechTranscriber`, with `DictationTranscriber` as its fallback).

Most voice checks were done by the author by hand rather than measured: the
takes were not kept on the phone, so there was nothing for the checker to
read. The voice record keeps each one as **waived by the author**, with what
the author confirmed, and `just voice-qualify status` lists them as waived,
not measured.

| Measurement | iPhone | Result |
| --- | --- | --- |
| Recording through force-quit, lock, a call, a headset change | iPhone 13 Pro | works, as the author confirmed; waived, not measured (seconds lost not recorded) |
| Transcription, English in a quiet room | iPhone 13 Pro | measured: a 2 s take transcribed as en-US by `SpeechTranscriber` |
| Transcription, English noisy, Québec French and mixed, quiet and noisy | iPhone 13 Pro | right, as the author confirmed; waived, not measured |
| Three-minute take to proposed answers (target under 15 s) | iPhone 13 Pro | fine, as the author confirmed; waived, no timing recorded |
| Battery, ten takes | — | waived by the author, not measured |
| Weekly reviews on the on-device model: valid, refused, median time | iPhone 13 Pro | not run: `deviceNotEligible`; needs an iPhone 15 Pro or later |
| Weekly reviews on PCC, light / moderate / deep | — | not run: the PCC entitlement has not been granted, and the iPhone 13 Pro is not eligible |

The on-phone model numbers therefore still need an eligible iPhone (15 Pro or
later), and PCC needs its entitlement; until then the Mac's numbers above are
the only measurements of Apple's language models.

## Comparing blind against Opus

Whether PCC can carry the Plus weekly review is the author's judgement, made
blind, on the weeks the phone ran:

1. **Opus on the same weeks.** For each week in `.cache/apple-models/export/windows/`,
   run the real review on a copy with the direct provider:
   `SENECA_FILE=<copy>/reflections.md seneca review --month=<month> --harness anthropic | tee <week>.opus.json`.
   Piping makes it a `piped` review: nothing is saved, and the
   [usage ledger](../review.md) records its tokens, thinking, cost, and time.
   `seneca usage` then gives the Opus side's cost per week, to set against the
   phone's, which costs nothing.
2. **The phone's reviews.** The valid on-device and PCC responses from
   `responses.json` (those `replay.json` calls valid), one file per week and
   model.
3. **Blind.** For each week, copy the three or four reviews to files named by
   a random letter, keep the key in a file you do not open, and read them
   beside the week's entries. Score each 1–5 on grounding (every citation real
   and fair), insight (a pattern you had not named), and the focus
   recommendation (specific and doable), and note any claim the entries do not
   support.
4. **Unblind** and tabulate per model: mean scores, guardrail refusals, time,
   and cost. PCC carries the Plus weekly review if it is within half a point
   of Opus on insight and focus, never cites what is not there, and is refused
   on no more weeks than Opus is.

The scores and the verdict go in this section; the reviews themselves stay on
the Mac with the export.

## Open measurements (P0)

1. The same suites on an iPhone: latency, battery, and thermal behavior while a
   review runs.
2. PCC from a signed build with the entitlement: pass rate, quality against
   Opus on the same weeks, daily quota in practice, and guardrail behavior on
   the heavy weeks.
3. Real recorded audio, in English, French and mixed, including a noisy room.
4. A blind comparison of on-device, PCC and Opus weekly reviews by the author,
   which decides whether PCC can carry the Plus weekly review.
