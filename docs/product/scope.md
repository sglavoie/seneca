[← Seneca](../../README.md)

# Product scope

Seneca becomes one application serving three audiences:

- **The author's own daily use.** It never gets worse.
- **A free app for anyone, with an optional Premium tier.** Writing and keeping
  a journal is free. Hosted, high-quality AI is what Premium sells.
- **Practitioners and the people they guide (B2B2C).** Coaches, sponsors and
  spiritual directors, and therapists through their clients. This audience is
  unproven, so it is built last and only to the depth demand justifies.

This document is the contract for implementation. It records what is in scope,
what is not, and the order of work. Two companion documents feed it:

- [AI costs](ai-costs.md): model prices and the per-user cost model.
- [Apple's models](apple-models.md): what Apple's on-device and Private Cloud
  Compute models measured as good enough for, and how routing between them and
  Opus lowers cost.

## Why this shape

From the market research of 2026-10-14 (sources are listed at the end):

- **People pay for journaling, but for sync, AI and polish, not for capture.**
  Prices cluster at $50–130 a year. Day One charges about $25 a year more for
  AI. Paywalling capture is the category's top complaint.
- **AI that finds patterns over time is the most-praised paid feature**
  ("worth every penny"). No competitor chains its reviews, recommends one
  focus, or checks yesterday's intention against today's follow-through. That
  is Seneca's distinctive core.
- **"Stoic" alone does not sell.** One solo developer's Stoic journal makes $24
  a month. Stoic. grew by broadening. Seneca keeps Stoicism as the default
  practice, not the only one.
- **Nightly-practice communities already use structured apps.** Recovery's
  step-10 inventory, which sponsors recommend and members share with them. The
  Ignatian examen, where users write their own questions. These are Seneca's
  first entry points beyond the author.
- **Many people decline cloud AI over their journal,** and the mainstream
  doesn't care. Seneca serves both groups by keeping data local by default and
  making every AI send explicit.
- **Realistic expectations.** The median subscription app makes about $72 a
  month a year after launch. 17% of apps reach $1K a month within two years.
  Treat this as a side business with validation gates, not a venture.

## Principles

These carry over from what Seneca already is. They bind every feature below.

1. **The journal is a plain Markdown file the user owns.** No feature makes a
   server the source of truth for writing.
2. **Capture is never paywalled.** Writing, editing, history, search, export,
   backup, sync and reminders stay free forever.
3. **Fail closed, never lose writing.** The existing recovery guarantees (see
   the README's Recovery section) apply to every new write path, including
   anything Premium writes back.
4. **AI reads only what the user sent, and says so.** Every hosted call is
   initiated or scheduled by the user. They can see exactly what was sent.
   Nothing is retained server-side.
5. **No streaks, scores or engagement tricks.** Structure, not gamification,
   as the focus history already does.
6. **The author is user zero.** Every phase ships to the author's own journal
   first.

## Audiences and editions

| | **Free** | **Plus** (Premium) | **Practitioner** (B2B2C) |
| --- | --- | --- | --- |
| Who | Anyone; developers and privacy-first users | People who want great reviews without setup | Coach, sponsor, director, or therapist with clients |
| Price | $0 | $7.99/mo or $69.99/yr, 14-day trial | $29/mo per practitioner, billed on the web; includes Plus for up to 10 clients |
| Sold through | App Store | App Store (StoreKit 2) | Web (Stripe); clients redeem a code in the app |

A **Supporter lifetime** at $199 unlocks every non-AI Plus feature, plus
bring-your-own-key AI, forever. Hosted AI always needs an active subscription,
because its cost recurs (see [AI costs](ai-costs.md#what-a-subscription-can-carry)).

### Entitlements

| Capability | Free | Plus | Practitioner's client |
| --- | --- | --- | --- |
| Capture, edit, history, search, on-this-day | ✓ | ✓ | ✓ |
| Voice capture, on-device transcription, answers proposed from a transcript | ✓ | ✓ | ✓ |
| Export, backup, doctor, sync (iCloud) | ✓ | ✓ | ✓ |
| Reminders, intention and follow-through, focus | ✓ | ✓ | ✓ |
| Practice packs (built-in and imported) | ✓ | ✓ | ✓ |
| Follow-through insights (no AI) | Last 30 days | Full history | Full history |
| Reviews through the user's own harness or API key (CLI and iPhone) | ✓ | ✓ | ✓ |
| On-device weekly snapshot (Apple Foundation Models, where supported) | ✓ | ✓ | ✓ |
| **Hosted reviews**: weekly on Private Cloud Compute or Opus 5.5; monthly on Opus 5.5 | — | ✓ | ✓ |
| **Hosted arc reviews**: quarterly and yearly at the top setting | — | ✓ | ✓ |
| **Ask your journal**: questions over a year of entries | — | Within allowance | Within allowance |
| **Pattern threads**: themes followed across months | — | ✓ | ✓ |
| Share packets to a person (share sheet) | ✓ | ✓ | ✓ |
| Share packets to a practitioner's inbox (end-to-end encrypted) | — | — | ✓ |

The free edition is complete on its own: someone who never pays has a working
reflective practice with weekly reviews. Plus sells **quality and effort
saved**: frontier models, no setup, longer arcs. It never sells access to the
user's own writing.

## Features

Each feature lists its scope and acceptance criteria. "Engine" means the
shared Go engine behind the CLI and the iPhone app.

### F1 — Direct model providers and a usage ledger
*All editions. This is the foundation for everything else.*

- Add `anthropic` and `openai` review providers that call the APIs directly
  with the user's key. They sit alongside the existing `claude`, `codex` and
  `copilot` CLI harnesses and are configured through `[review]` (`provider`,
  `model`, `effort`).
- Requests use structured output with the existing `reviewSchema`, and the
  same validation and citation checks the harness path uses.
- Every review appends a **usage record** to the journal's `.seneca/` folder:
  provider, model, effort, input, cached, output and thinking tokens, cost
  computed from a dated `pricing.toml`, and latency. It holds no content.
- `seneca usage` reports per-kind and per-month cost, with p50 and p90 per
  review.
- **Acceptance:**
  - A weekly and a monthly review on Opus 5.5 produce archive records
    byte-compatible with today's.
  - The ledger's cost matches the provider console within 2%.
  - Keys come from the macOS Keychain or the environment, never from
    `config.toml`.

### F2 — The iPhone app as a shippable product
*Free, so it is the funnel.*

Built: onboarding, the practice pack screens, opening a pack file, the privacy
manifest, and `just ios-testflight` (see the [iPhone README](../../ios/README.md#distribution)).
Running reviews on the phone is deferred to go with [F5](#f5--hosted-reviews-plus):
the engine will build the prompt and the app will send it, with the
author's key or through the relay. Until then, reviews are run on the desktop
and read on the phone, so the acceptance below waits on it.

- **Onboarding** that needs no Xcode and no git. It sets up a local journal,
  helps choose a practice pack, asks for notification permission, and offers
  optional iCloud sync.
- **Settings on the phone:** questions, anchor, reminders, practice pack, and
  reviews (provider, key, or Plus).
- App Store distribution: TestFlight first, then release. Privacy nutrition
  label: no data collected by the free edition.
- **Acceptance:** a non-developer installs from TestFlight, writes for 7 days,
  and runs a weekly review without touching a computer.

### F3 — Practice packs
*All editions. This is how Seneca reaches beyond Stoicism.*

The format, the built-in packs, and how a pack reaches reviews are in
[Practice packs](practice-packs.md).

- A pack bundles a question set, an anchor question, a reminder time, review
  guidance (a short prompt addendum: tone, vocabulary, what counts as progress)
  and a description.
- Built-in packs:
  - **Stoic evening review** (the current default)
  - **Step 10 nightly inventory**
  - **Ignatian examen**
  - **Gratitude and intention**
  - **Virtue tracking** (Franklin)
- A pack is a file (`*.seneca-pack.json`) that can be imported from a link,
  AirDrop, or a practitioner. Applying a pack goes through the existing
  journal-settings path, so entries are never rewritten.
- **Acceptance:**
  - Switching packs mid-journal keeps every past entry readable under its
    original questions.
  - The review prompt includes the guidance of the pack in force for each
    window.

### F4 — Follow-through insights
*No AI. Free covers the last 30 days; Plus covers full history.*

Built: `seneca insights` and the iPhone's Reviews, Follow-Through, over the
bridge's `insights.get`. The rules are in [Follow-through insights](../insights.md).

Structural reporting over the intention, follow-through and focus data
Seneca already stores:

- Intentions answered vs. left unanswered.
- Intentions that recur. Detection is literal and lexical, not AI.
- How long each focus stood, and what replaced it.

No scores and no streaks, consistent with focus history.

- **Acceptance:**
  - Reproduces counts that `seneca show` and `seneca focus history` can verify
    by hand.
  - Works offline.

### F5 — Hosted reviews (Plus)

- A **stateless relay** receives the prompt the engine already builds, calls
  the configured model, validates the response against the schema, and returns
  it. It stores no content and logs only metadata: entitlement id, model,
  tokens, cost, latency.
- The prompt is built on the device. The relay never sees the journal file,
  only the window being reviewed.
- Scheduled weekly and monthly reviews are submitted through the provider's
  Batch API and ready by morning. On-demand reviews go through the standard
  API.
- Routing, effort and allowances follow
  [AI costs § guardrails](ai-costs.md#guardrails-requirements-for-the-hosted-service).
- **Acceptance:**
  - An offline phone queues the request and sends it later, and nothing is
    written to the archive until a valid response arrives.
  - A relay outage never blocks writing.
  - The cost per paying user stays under the budget for the first 50 users.

### F6 — Ask your journal (Plus)

- Questions in plain language over up to a year of entries and saved reviews,
  for example "When did patience in meetings get easier?".
- Answers cite entry dates. A citation to a date that wasn't sent is refused,
  matching the existing arc-review rule.
- The corpus is cached with a 1-hour TTL. Answers are optionally saved to the
  archive as a new record kind, which needs an archive version bump and doctor
  support.
- **Acceptance:**
  - Every claim in an answer carries a date that opens to the cited entry.
  - Asking is refused cleanly when the allowance is exhausted.

### F7 — Pattern threads (Plus)

- A theme followed across months, built from saved reviews rather than raw
  entries, the same discipline as arc reviews.
- Shown as a timeline: when the theme appeared, when it waned, and the focus
  events around it.
- **Acceptance:**
  - Generated at most once a month per user, through the batch API.
  - Each thread point links to the review that produced it.

### F8 — Apple's models: weekly snapshot (Free) and routing (Plus)

These measured good enough in [Apple's models](apple-models.md): 36 of 42
weekly reviews passed Seneca's validation, but the output was extractive and
the focus recommendations were platitudes.

- **Free weekly snapshot.**
  - The on-device model produces the weekly review shape with constrained
    decoding:
    - the guided schema lists the only citable dates;
    - `focus_assessment` is forced to null when no focus applies.
  - It is labeled a *snapshot*: what happened, with the evidence. The insight
    and the recommended focus are presented as Plus.
- **Plus weekly review** runs on Private Cloud Compute (moderate reasoning)
  when it passes the P0 quality comparison against Opus 5.5. Otherwise, and on
  quota exhaustion or refusal, it runs on Opus 5.5.
  - Monthly and arc reviews, Ask, and pattern threads always run on Opus.
  - This routing cuts Plus core-review cost by about 75%
    (see [Apple's models § cost](apple-models.md#where-apples-models-lower-the-cost-of-reviews)).
- **Refusals are part of the design.** Apple's guardrail declined guided output
  on emotionally heavy weeks. Free says so plainly and offers the
  bring-your-own-key route. Plus falls back to Opus. Either way, it never fails
  silently.
- The engine gets a review-provider interface that the iPhone app implements
  with Foundation Models. The prompt, the schema, and the validation all stay
  in Go (`parseReviewResponse`), and the schema conversion to Apple's
  `DynamicGenerationSchema` lives in Swift.
- **Requirements:**
  - Raise the app's deployment target to iOS 26. Foundation Models and
    `SpeechTranscriber` both need it.
  - Gate Private Cloud Compute on iOS 27 and the managed entitlement.
- **Acceptance:**
  - Unsupported devices hide the feature rather than breaking.
  - Every Apple-model output passes the same validation as the cloud path.
  - A guardrail refusal shows its message and the fallback, and leaves nothing
    written.

### F9 — Share packets (Free) and practitioner inbox (Practitioner)

Built: the free share packets, from a review or a day on the iPhone, through
the bridge's `share.packet` ([Share packets](../export.md#share-packets)). The
practitioner inbox belongs to P3.

- **Packet:** a user-chosen bundle, usually a review summary, optionally with
  selected entries. It reuses `seneca export`. The default share is
  summary-only, and the user previews exactly what leaves the device.
- **Free:** sent through the iOS share sheet as PDF or Markdown, for example to
  a sponsor by message.
- **Practitioner:**
  - The practitioner has a web inbox with a keypair generated in their browser.
  - A client links to them by scanning a code, which also delivers the
    practitioner's practice pack.
  - Packets are encrypted on the client's device to the practitioner's public
    key. The server stores only ciphertext and timestamps.
  - Sharing is per packet or scheduled, for example "send my weekly review
    every Sunday".
  - Consent can be revoked at any time.
- **Acceptance:**
  - The server cannot read a packet.
  - Revoking consent stops future packets immediately.
  - The practitioner's view shows only what was sent.

### F11 — Voice capture and transcription
*All editions. Capture is free, so this is too.*

Speak the evening review instead of typing it. The transcript becomes the
entry's answers, and the reviews read the transcript like any other entry.

**Recording**

- Reuse the capture packages of an earlier app by the same author
  (`CaptureApple` and `CaptureCore`). They have no dependency on that app:
  - `AVAudioEngine` writes a **fragmented MP4** (AAC, 48 kHz, mono, 1-second
    fragments), so a killed app keeps everything up to the last second.
  - Interruptions end the take and keep it. Route changes, such as a headset,
    re-tap the input without a gap.
  - Recording continues while the phone is locked.
- Vendor only the recording and transcription parts, with a note recording
  where they came from (`ios/Packages/Capture/PROVENANCE.md`).

**Transcription**

- On-device `SpeechTranscriber`, falling back to `DictationTranscriber`. Free
  and offline; measured at 14× real time warm on a Mac.
- Language follows the journal's settings: English and Québec French at first.
- The speech model downloads ahead of time, during onboarding. A transcription
  never waits on a download that hasn't started, a lesson learned the hard way
  in that earlier app.

**Answers**

- The on-device model *proposes* how the transcript fills each configured
  question, plus anything that answers none of them. This tested well in
  English and French, and it can mis-file content when the questions don't
  match what was said.
- The proposal is always shown for editing before it is saved. Nothing a model
  altered is ever saved silently.
- A long spoken answer is condensed on device for the review prompt, instead
  of being cut at `maxPromptAnswerRunes`. The entry itself keeps the full
  answer.

**Storage**

- The answers are written to the Markdown entry exactly like typed ones, so
  the CLI, search, and reviews need no change.
- The audio and the raw transcript stay in a sidecar in the app's own storage,
  `Application Support/Seneca/audio/<date>/<id>.{m4a,json}`, next to its drafts
  and reminder preferences. Every phone write *into* the journal goes through
  the engine (see `docs/mobile/write-path-inventory.md`), so the sidecar stays
  outside it. The JSON holds the transcript, its locale, the provider, the
  duration, and the audio's sha256.
- **Decided 2026-10-14: the Markdown entry carries nothing.** A spoken entry is
  byte-identical to a typed one: no marker and no link. The format the
  CLI-compatibility contract freezes is unchanged, and a marker can still be
  added later under a schema version.
- The sidecar is a device-local app write, like drafts and `reminders.json`,
  listed in the app-side part of the write-path inventory. A recovery kit
  carries it as a separate folder when the author asks.
- Audio is kept on the device by default.
  - Sync is a later option. Audio files are immutable and content-addressed, so
    they cannot conflict, but the sync format would need a blob kind and a
    protocol version bump.
  - "Delete audio after the answers are saved" is offered for people who want
    text only.

**Privacy**

- Audio never leaves the device except through the user's own iCloud, if they
  opt in.
- AI reviews only ever see transcript text, inside the same windows as typed
  entries.

**Not in this feature**

- Cloud transcription. Add WhisperKit only if both Apple transcribers fail on
  target phones.
- Recording from the Mac CLI. `seneca show` reports which entries have audio.

**Acceptance**

- Recording survives a force-quit, a lock, a phone call and a headset change
  with no lost audio past the last second.
- A 3-minute English or French take becomes proposed answers in under
  15 seconds on a supported iPhone.
- The saved entry is byte-identical to a typed entry with the same answers.
- Deleting audio removes the sidecar files and nothing else.

### F10 — Entitlements and billing

- **Plus:**
  - StoreKit 2 subscriptions and a non-consumable Supporter purchase.
  - The relay verifies Apple-signed transactions.
  - App Attest guards the relay from abuse.
- **Practitioner:**
  - Stripe on the web.
  - Each seat issues client codes. A redeemed code grants Plus while the
    practitioner subscription is active, with a 30-day grace period.
- **Acceptance:**
  - A refunded or expired subscription stops hosted calls within one hour.
  - Local data and every free feature are untouched by entitlement state.

## Privacy and safety

- **Data minimization.**
  - Free collects nothing.
  - Plus sends only the review window or Ask corpus, per request.
  - No content is stored by the relay. Logs hold metadata only.
- **Apple's models first where they are good enough.**
  - Transcription, answer splitting, condensing and the free weekly snapshot
    run on the device.
  - The Plus weekly review uses Private Cloud Compute, which stores no request
    data, before any third-party model.
  - Audio stays on the device, or in the user's own iCloud.
- **Zero data retention.**
  - Run the relay under zero-data-retention arrangements with each provider
    where they are granted. Until then, state each provider's retention
    plainly in the app.
  - Claude Fable 5.1 is excluded because it requires 30-day retention unless
    Anthropic expressly authorizes zero retention.
  - Never use providers' free tiers that train on data.
- **Opt-in per kind.** Hosted reviews, Ask and pattern threads each have their
  own toggle. Turning one off removes its scheduled jobs.
- **Not a clinical product.**
  - No diagnosis, no treatment claims, no "mental health" positioning in the
    store listing.
  - Practitioner features are client-initiated sharing. Before marketing to
    therapists, get legal review of HIPAA and other health-privacy obligations;
    until then, market to coaches, sponsors and spiritual directors.
- **Crisis-aware output.** When reviewed text expresses self-harm or crisis,
  the review must not analyze or minimize it. It surfaces crisis resources
  instead. This goes into every review and Ask prompt, with evaluation cases.
- **Practitioner E2E is a promise, so it is tested.** A test asserts that the
  server's storage holds only ciphertext.

## Architecture

```
iPhone app (SwiftUI) ──bridge──▶ Go engine ◀── CLI
     │  capture (shared package: recorder, SpeechTranscriber)
     │  Apple models: on-device + Private Cloud Compute (provider behind the bridge)
     │                              │
     │ prompt built on device       │ journal.md, archive, .seneca/ (ledger, drafts, sync, audio)
     ▼                              ▼
Relay (Go, stateless) ──▶ Anthropic / OpenAI (standard + batch)
     │  StoreKit/App Attest verify, allowance, pricing.toml, metadata log
     ▼
Practitioner service (Go + web inbox): accounts, Stripe, client codes,
ciphertext packet store
```

- The provider client (F1) lives in the engine. The relay reuses it, so a
  personal API key and Plus run identical prompt and validation code.
- The relay and the practitioner service are separate deployables. The relay
  holds no user data and can be scaled or replaced without migrating anything.
- Hosting is open. A small managed container platform is enough at this scale.
  Pick one at P2.

## Phases and gates

Each phase ends with a gate. Failing a gate means stopping or changing
course, not pushing on.

### P0 — Measure (personal)
F1. Run Opus 5.5 reviews on the author's own journal for 4 weeks.

Alongside that, finish the [open measurements of Apple's models](apple-models.md#open-measurements-p0):

- Apply for the Private Cloud Compute entitlement.
- Rerun `scripts/apple-models` from a signed iPhone build, and against PCC.
- Record real English and French audio.
- Do a blind comparison of on-device, PCC and Opus weekly reviews.

- **Gate:**
  - Real thinking-token p50 and p90 are recorded, and the
    [cost tables](ai-costs.md#monthly-cost-per-user-estimates) are regenerated.
  - The author judges Opus reviews clearly better than the current harness
    output on at least 3 of 4 weeks.
  - PCC either earns the Plus weekly review (preferred over Opus in at least
    half the blind comparisons), or the routing falls back to Opus.

### P1 — Free product and validation
F2, F3, F4 (free scope), F8 (free snapshot), F11 (voice capture), and F9
(share sheet only). Ship voice capture to the author's phone first: it is the
feature the author most wants for daily use. TestFlight beta.
Landing page with prices. A paywall that measures demand before hosted AI is
built: tapping "Subscribe" records intent and explains that Plus is coming.

- **Recruit** from r/Stoicism, r/Journaling, and step-10 and examen
  communities.
- **Gate:**
  - At least 50 beta users.
  - At least 20% still writing at day 30.
  - At least 2% of active users tap the paywall.
  - At least 3 practitioners ask for the inbox unprompted, or say yes when
    shown it.

### P2 — Plus
F5, F6, F7 and F10 (Plus), plus the cost guardrails. App Store release.

- **Gate after 90 days:**
  - The trial-to-paid rate is at least 25% (category median is about 38%).
  - The p90 AI cost per payer stays inside budget.
  - Refunds or complaints about AI quality are under 5%.

### P3 — Practitioner (B2B2C)
F9 (inbox) and F10 (practitioner billing), offered to the practitioners from
the P1 gate. Ten design-partner practitioners, free for 3 months.

- **Gate:**
  - At least 5 convert to paid.
  - Their clients' 60-day retention beats unaffiliated Plus users.

## Out of scope

- Android and a web journal editor. Revisit only after the P2 gate.
- Server-side storage of journals, and cloud sync through Seneca's own
  servers. iCloud stays the sync transport.
- Social features, sharing to the public, and community feeds.
- Streaks, badges, scores and mood charts.
- Photos and video. Voice is in scope (F11) because it ends up as text the
  reviews can read.
- Cloud transcription, and recording from the Mac CLI.
- Clinical features: assessments, diagnosis, treatment plans.
- Employer and enterprise wellbeing sales.

## Open questions

1. **Name and positioning.** Should the product keep "Seneca" and the Stoic
   framing, or present Seneca as the brand with practice packs as the
   positioning ("the journal that remembers what you promised yourself")?
   Decide before the P1 landing page.
2. **Default hosted model.** Opus 5.5 at high effort is the working assumption.
   Revisit with P0 data, and with blind comparisons against GPT-6 Astra on arc
   reviews.
3. **Zero data retention.** Is it granted for the relay's organization with
   each provider? This determines the privacy copy at P2.
4. **App Store rules for practitioner-issued client codes.** Confirm against
   the current App Review Guidelines (3.1.1 and 3.1.3) before P3.
5. **Private Cloud Compute access.** Is Seneca eligible? Does the daily quota
   cover a weekly review for typical users? What does PCC's guardrail do on
   heavy weeks? The P0 measurements answer these.
6. **Price testing.** Is $69.99 a year or $59.99 a year the better default?
   Test at P2 with the paywall analytics.

## Market sources

- Competitor pricing and traction:
  - Day One plans: <https://dayoneapp.com/guides/premium-subscription/day-one-pricing-features-guide/>
  - Rosebud's free plan change: <https://help.rosebud.app/account/changes-to-the-free-plan>
  - Reflection.app pricing: <https://www.reflection.app/pricing>
  - Stoic.: <https://www.ycombinator.com/companies/stoic>
  - Stoxia revenue: <https://trustmrr.com/startup/stoxia-ai-stoic-journal>
- Subscription benchmarks: <https://www.revenuecat.com/state-of-subscription-apps>
- User voice:
  - Rosebud App Store reviews: <https://apps.apple.com/us/app/rosebud-ai-journal-diary/id6451135127?see-all=reviews>
  - Hacker News on local, private journaling: <https://news.ycombinator.com/item?id=37045568>
  - Step-10 inventory app: <https://apps.apple.com/app/id899778010>
  - Ignatian examen app: <https://apps.apple.com/app/id1065042173>
- Coach platforms with client journaling: <https://www.capterra.com/p/218512/Quenza/pricing/> (prices vary by source)
- Model prices: see [AI costs § sources](ai-costs.md#sources).

Not covered: Reddit could not be searched directly. P1 recruiting doubles as
that validation.
