package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"strings"
)

// buildPromptForWindow picks the prompt the window's evidence calls for. The
// window itself names its source kind, so the dispatch is one decision made in
// one place rather than a flag threaded down from the command layer.
func buildPromptForWindow(window reviewWindow, focus reviewFocusContext) string {
	if window.SourceKind == sourceReviews {
		return buildArcReviewPrompt(window, focus)
	}
	return buildReviewPrompt(window, focus)
}

// buildReviewPrompt renders every parsed section of every window entry, not
// just the currently configured questions. A reworded question, a section the
// author added by hand, and the follow-through narrative are all reflection
// material; dropping them because they no longer match today's config would
// quietly starve the review of exactly the continuity Seneca just learned to
// keep.
func buildReviewPrompt(window reviewWindow, focus reviewFocusContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are generating a structured %s review for %s.\n", window.PromptLabel, promptSubject(window))
	b.WriteString("Return only valid JSON matching the schema below.\n")
	b.WriteString("Use only the supplied reflections.\n")
	b.WriteString("Keep struggles, wins, and carry_forward to at most 3 items each.\n")
	b.WriteString("Each theme should be short and specific.\n")
	b.WriteString("Each summary should synthesize a recurring pattern, not restate one entry.\n")
	b.WriteString("Each evidence item must include the exact source date and a short snippet grounded in the entry.\n")
	b.WriteString("Every evidence date must be one of the reflection dates listed below, copied exactly.\n")
	b.WriteString("If a section has no clear recurring evidence, return an empty array for that section.\n")
	b.WriteString("focus_recommendation must be one sentence with 18 words or fewer.\n")
	writeFocusAssessmentInstructions(&b, focus, window.SourceKind)
	fmt.Fprintf(&b, "Coverage context: %d of %d days across %s.\n", window.CoveredDays, window.ExpectedDays, window.PeriodLabel)
	writePracticeGuidance(&b, window)
	fmt.Fprintf(&b, "JSON schema: %s\n\n", reviewSchema)
	b.WriteString("Reflections:\n")

	for _, reflection := range window.Entries {
		fmt.Fprintf(&b, "Date: %s\n", reflection.Date)
		writePromptSections(&b, reflection)
		b.WriteByte('\n')
	}

	return b.String()
}

// buildArcReviewPrompt feeds a quarter or a year what Seneca already concluded
// rather than what the author wrote: the reviews saved inside the window, never
// the entries beneath them. The response schema is byte-identical to the entry
// prompt's — an arc review is a second reading of the same shape — with one
// substitution the model has to be told about, that an evidence date names a
// source review by the day its window ended.
func buildArcReviewPrompt(window reviewWindow, focus reviewFocusContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are generating a structured %s review for %s.\n", window.PromptLabel, promptSubject(window))
	b.WriteString("Return only valid JSON matching the schema below.\n")
	b.WriteString("The material below is prior reviews Seneca already saved, not raw reflections.\n")
	b.WriteString("Use only the supplied source reviews.\n")
	b.WriteString("Keep struggles, wins, and carry_forward to at most 3 items each.\n")
	b.WriteString("Each theme should be short and specific.\n")
	writeArcSynthesisInstructions(&b, window.sourceCount())
	b.WriteString("Each evidence item must include the exact source date and a short snippet grounded in the source review.\n")
	b.WriteString("Cite a source review by its window end date: the End line below, copied exactly.\n")
	b.WriteString("Two source reviews can share an End date; their Period and Start tell them apart.\n")
	b.WriteString("focus_recommendation must be one sentence with 18 words or fewer.\n")
	writeFocusAssessmentInstructions(&b, focus, window.SourceKind)
	writeFocusTimeline(&b, focus)
	fmt.Fprintf(&b, "Coverage context: %s across %s.\n", window.coverageLine(), window.PeriodLabel)
	writePracticeGuidance(&b, window)
	fmt.Fprintf(&b, "JSON schema: %s\n\n", reviewSchema)
	b.WriteString("Source reviews:\n")

	for _, record := range window.Reviews {
		fmt.Fprintf(&b, "Title: %s\n", record.Title)
		fmt.Fprintf(&b, "Period: %s\n", record.Period)
		fmt.Fprintf(&b, "Start: %s\n", record.Start)
		fmt.Fprintf(&b, "End: %s\n", record.End)
		fmt.Fprintf(&b, "Body: %s\n\n", promptReviewBodyOrBlank(record.Body))
	}

	return b.String()
}

// promptSubject names what is being reviewed. A journal no practice pack ever
// covered is the Stoic journal Seneca has always been, and its prompt says so
// exactly as it did; under a pack the practice is the pack's, which the
// guidance names.
func promptSubject(window reviewWindow) string {
	if len(window.Practice) == 0 {
		return "a Stoic reflection CLI"
	}
	return "a reflection journal"
}

// writePracticeGuidance names each practice pack in force during the window,
// with the days it covered there and its guidance, so the model reads every day
// under the practice it was written in. A window no pack covered gets nothing.
func writePracticeGuidance(b *strings.Builder, window reviewWindow) {
	if len(window.Practice) == 0 {
		return
	}
	b.WriteString("Practice in force during this period:\n")
	for _, span := range window.Practice {
		fmt.Fprintf(b, "- %s to %s: ", span.From, span.To)
		switch {
		case span.Pack == nil:
			b.WriteString("no practice pack.\n")
		case span.Pack.Guidance == "":
			fmt.Fprintf(b, "%s.\n", span.Pack.Name)
		default:
			fmt.Fprintf(b, "%s. Guidance: %s\n", span.Pack.Name, compactPromptText(span.Pack.Guidance, engine.MaxPackGuidanceRunes))
		}
	}
	if window.SourceKind == sourceReviews {
		b.WriteString("Read each source review under the guidance of the practice in force during its window.\n")
		return
	}
	b.WriteString("Read each entry under the guidance of the practice in force on its date.\n")
}

// practiceForWindow divides a window by the practice packs the journal at
// reflectionsPath recorded. A journal without settings of its own never had a
// pack. Settings that cannot be read stop the review, as they stop every
// other command: the practice is not guessed at.
func practiceForWindow(reflectionsPath string, window reviewWindow) ([]engine.PracticeSpan, error) {
	state, initialized, err := engine.LoadJournalSettings(reflectionsPath)
	if err != nil || !initialized {
		return nil, err
	}
	return engine.PracticeDuring(state.Practice, window.startDate(), window.endDate()), nil
}

// writeArcSynthesisInstructions states what a theme is allowed to be, which
// depends on how many reviews the period actually saved. Recurrence across
// reviews is the rule that makes an arc review more than a re-reading — but a
// quarter holding a single review cannot satisfy it, and a model obeying it
// there would have to return every section empty. So the one-source arc asks
// for the synthesis that source can support instead, and forbids only the claim
// it cannot back: that the pattern recurred across reviews.
func writeArcSynthesisInstructions(b *strings.Builder, sources int) {
	if sources == 1 {
		b.WriteString("Only one review was saved for this period, so no theme can recur across reviews.\n")
		b.WriteString("Each theme must be a pattern that single source review itself supports, " +
			"stated without claiming it recurred across reviews.\n")
		b.WriteString("Each summary should synthesize what that one review shows about the period, " +
			"not restate its wording.\n")
		b.WriteString("If a section has no clear support in that review, return an empty array for that section.\n")
		return
	}

	b.WriteString("Each theme must be one that recurs across these reviews, never one restated from a single review.\n")
	b.WriteString("Each summary should synthesize a pattern running through the period, not restate one review.\n")
	b.WriteString("If a section has no clear recurring evidence, return an empty array for that section.\n")
}

// writeFocusTimeline renders how the focus itself moved inside the window. An
// arc spans long enough to hold several focuses, and a single end-of-period
// verdict would hide that churn — so the churn goes in as context rather than
// growing the response schema a second assessment shape.
func writeFocusTimeline(b *strings.Builder, focus reviewFocusContext) {
	if len(focus.timeline) == 0 {
		b.WriteString("The focus was neither set nor cleared during this period.\n")
		return
	}

	b.WriteString("Focus timeline within this period, oldest first (context only; do not cite these dates):\n")
	for _, event := range focus.timeline {
		if event.Event == engine.FocusEventClear {
			fmt.Fprintf(b, "- %s: focus cleared (%s)\n", event.Effective, event.Origin)
			continue
		}
		fmt.Fprintf(b, "- %s: focus set (%s): %s\n", event.Effective, event.Origin, compactPromptAnswer(event.Text))
	}
}

// writeFocusAssessmentInstructions states which focus is under review and which
// dates may support a verdict about it. Naming the eligible dates is what makes
// "insufficient evidence" an instruction the model can follow rather than a
// rule it has to guess. Only the nouns differ between the two window kinds, so
// the source kind picks the vocabulary and the logic stays in one place.
func writeFocusAssessmentInstructions(b *strings.Builder, focus reviewFocusContext, source reviewSourceKind) {
	if !focus.applies() {
		b.WriteString("No focus was in effect at the end of this period, so focus_assessment must be null.\n")
		return
	}

	fmt.Fprintf(b, "A focus was in effect at the end of this period, set on %s (%s):\n",
		focus.state.Effective, focus.state.Origin)
	fmt.Fprintf(b, "%s\n", compactPromptAnswer(focus.state.Text))
	b.WriteString("focus_assessment must be an object judging how that focus actually went, in 3 sentences or fewer.\n")
	vocabulary := evidenceVocabularyFor(source)
	if len(focus.eligibleDates) == 0 {
		fmt.Fprintf(b, "%s, so focus_assessment.evidence must be an empty array and the summary must say plainly "+
			"that there is not enough evidence to judge the focus.\n", vocabulary.missing)
		return
	}
	fmt.Fprintf(b, "Only these %s may appear in focus_assessment.evidence: %s.\n",
		vocabulary.dates, strings.Join(focus.eligibleDates, ", "))
	fmt.Fprintf(b, "If %s do not actually speak to the focus, return an empty evidence "+
		"array and say plainly in the summary that there is not enough evidence to judge it. "+
		"Never invent support.\n", vocabulary.material)
}

// writePromptSections emits one line per section. The follow-through is split
// into the intention it answers and what actually happened, so the model can
// tell a plan apart from its outcome.
func writePromptSections(b *strings.Builder, reflection engine.ReflectionEntry) {
	for _, section := range reflection.Sections {
		if section.Follow != nil {
			follow := *section.Follow
			if intention := compactPromptAnswer(follow.SourceIntention); intention != "" {
				fmt.Fprintf(b, "%s — intention from %s: %s\n", section.Heading, follow.SourceDate, intention)
			}
			fmt.Fprintf(b, "%s — what happened: %s\n", section.Heading, promptAnswerOrBlank(follow.Response))
			continue
		}
		fmt.Fprintf(b, "%s: %s\n", section.Heading, promptAnswerOrBlank(section.Body()))
	}
}

func promptAnswerOrBlank(value string) string {
	if answer := compactPromptAnswer(value); answer != "" {
		return answer
	}
	return "(blank)"
}

func promptReviewBodyOrBlank(value string) string {
	if body := compactPromptText(value, maxPromptReviewBodyRunes); body != "" {
		return body
	}
	return "(blank)"
}

func compactPromptAnswer(value string) string {
	return compactPromptText(value, maxPromptAnswerRunes)
}

// compactPromptText flattens a value onto one line and caps it at limit runes,
// counting the ellipsis it adds. The limit is a parameter because a reflection
// answer and an already-summarized review body deserve very different room, and
// two truncators would drift apart.
func compactPromptText(value string, limit int) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if normalized == "" {
		return ""
	}
	runes := []rune(normalized)
	if len(runes) <= limit {
		return normalized
	}
	return string(runes[:limit-1]) + "…"
}
