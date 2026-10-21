package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"strings"
	"testing"
	"time"
)

// Late enough in the year that no quarter under test is clamped to today.
func arcPromptNow() time.Time {
	return time.Date(2026, time.December, 31, 9, 0, 0, 0, time.Local)
}

// arcTime turns a YYYY-MM-DD into the mid-morning instant a focus record is
// written at, so a focus event's effective date is exactly that date.
func arcTime(t *testing.T, date string) time.Time {
	t.Helper()
	return reviewDay(t, date).Add(9 * time.Hour)
}

func arcSourceRecord(period engine.ReviewPeriod, start, end, title, body string) engine.ReviewRecord {
	return engine.ReviewRecord{
		ID:     string(period) + "-" + end,
		Period: period,
		Start:  start,
		End:    end,
		Title:  title,
		Body:   body,
	}
}

// arcPromptWindow builds a real 2026-Q3 window through selectArcWindow, so the
// prompt tests read the same source list the command will.
func arcPromptWindow(t *testing.T, records ...engine.ReviewRecord) reviewWindow {
	t.Helper()

	window, err := selectArcWindow(records, arcPromptNow(), "2026-Q3", true, "", false)
	if err != nil {
		t.Fatalf("selectArcWindow returned error: %v", err)
	}
	return window
}

// q3Sources are three weekly reviews inside 2026-Q3, newest first, the order
// reviewRecords returns them in.
func q3Sources() []engine.ReviewRecord {
	return []engine.ReviewRecord{
		arcSourceRecord(engine.ReviewPeriodWeekly, "2026-08-17", "2026-08-23", "Weekly review", "Third week: the mornings held."),
		arcSourceRecord(engine.ReviewPeriodWeekly, "2026-08-10", "2026-08-16", "Weekly review", "Second week: drifted after lunch."),
		arcSourceRecord(engine.ReviewPeriodWeekly, "2026-08-03", "2026-08-09", "Weekly review", "First week: started late every day."),
	}
}

func assertPromptContains(t *testing.T, prompt string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

// An arc review synthesizes saved reviews. Every source has to arrive whole
// enough to be cited — title, period, both bounds, body — and nothing from the
// reflection document may leak in beside them.
func TestBuildArcReviewPromptRendersEverySourceReview(t *testing.T) {
	t.Parallel()

	window := arcPromptWindow(t, q3Sources()...)
	// An arc window never carries entries; planting one proves the arc prompt
	// reads Reviews and not Entries even if a caller hands it both.
	window.Entries = []engine.ReflectionEntry{engine.NewReflectionEntry(
		dayEntry("2026-08-20", "Reflection prose that must not reach an arc prompt.", "", ""))}

	prompt := buildArcReviewPrompt(window, reviewFocusContext{})

	assertPromptContains(t, prompt,
		"You are generating a structured quarterly review",
		"The material below is prior reviews Seneca already saved, not raw reflections.",
		"Each theme must be one that recurs across these reviews, never one restated from a single review.",
		"Cite a source review by its window end date",
		"Coverage context: 3 source reviews across 2026-07-01 to 2026-09-30.",
		"Source reviews:",
		"Title: Weekly review\nPeriod: weekly\nStart: 2026-08-03\nEnd: 2026-08-09\nBody: First week: started late every day.",
		"Start: 2026-08-10\nEnd: 2026-08-16\nBody: Second week: drifted after lunch.",
		"Start: 2026-08-17\nEnd: 2026-08-23\nBody: Third week: the mornings held.",
	)

	for _, unwanted := range []string{"Reflections:", "Reflection prose that must not reach an arc prompt.", "Coverage context: 0 of"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("expected prompt to omit %q, got:\n%s", unwanted, prompt)
		}
	}
}

// The day-coverage line is meaningless for a quarter, so it is replaced by the
// source count and the span the sources were drawn from.
func TestBuildArcReviewPromptReplacesTheDayCoverageLine(t *testing.T) {
	t.Parallel()

	single := arcPromptWindow(t, q3Sources()[0])
	assertPromptContains(t, buildArcReviewPrompt(single, reviewFocusContext{}),
		"Coverage context: 1 source review across 2026-07-01 to 2026-09-30.")

	empty := arcPromptWindow(t)
	assertPromptContains(t, buildArcReviewPrompt(empty, reviewFocusContext{}),
		"Coverage context: 0 source reviews across 2026-07-01 to 2026-09-30.")
}

// A quarter can legitimately hold a single saved review, and that path is
// accepted rather than refused — so the prompt must not order the model to find
// recurrence across reviews it was never given. It asks for what the one source
// supports and forbids only the cross-review claim, while a multi-source arc
// keeps the recurrence requirement verbatim.
func TestArcPromptAllowsSynthesisFromSingleSource(t *testing.T) {
	t.Parallel()

	single := buildArcReviewPrompt(arcPromptWindow(t, q3Sources()[0]), reviewFocusContext{})
	assertPromptContains(t, single,
		"Only one review was saved for this period, so no theme can recur across reviews.",
		"Each theme must be a pattern that single source review itself supports, "+
			"stated without claiming it recurred across reviews.",
		"Each summary should synthesize what that one review shows about the period, not restate its wording.",
		"If a section has no clear support in that review, return an empty array for that section.",
	)
	for _, unwanted := range []string{
		"Each theme must be one that recurs across these reviews",
		"Each summary should synthesize a pattern running through the period",
		"If a section has no clear recurring evidence",
	} {
		if strings.Contains(single, unwanted) {
			t.Fatalf("expected the one-source prompt to omit %q, got:\n%s", unwanted, single)
		}
	}

	multi := buildArcReviewPrompt(arcPromptWindow(t, q3Sources()...), reviewFocusContext{})
	assertPromptContains(t, multi,
		"Each theme must be one that recurs across these reviews, never one restated from a single review.",
		"Each summary should synthesize a pattern running through the period, not restate one review.",
		"If a section has no clear recurring evidence, return an empty array for that section.",
	)
	if strings.Contains(multi, "Only one review was saved for this period") {
		t.Fatalf("expected the multi-source prompt to keep the recurrence requirement alone, got:\n%s", multi)
	}
}

// Grounding is the whole point: the model may cite a source review's window end
// date and nothing else. The rejection path is the untouched entry-review one.
func TestArcGroundingAcceptsSourceEndDatesAndRejectsEverythingElse(t *testing.T) {
	t.Parallel()

	window := arcPromptWindow(t, q3Sources()...)
	grounding := newReviewGrounding(window, reviewFocusContext{})

	if got := strings.Join(grounding.entryDates, ","); got != "2026-08-09,2026-08-16,2026-08-23" {
		t.Fatalf("expected the three source end dates, got %q", got)
	}

	cited := func(date string) []byte {
		return []byte(`{"focus_assessment":null,"struggles":[{"theme":"Late starts",` +
			`"summary":"Mornings kept slipping across the quarter.",` +
			`"evidence":[{"date":"` + date + `","snippet":"Started late every day."}]}],` +
			`"wins":[],"carry_forward":[],"focus_recommendation":"Protect the first hour."}`)
	}

	if _, err := parseReviewResponse(cited("2026-08-16"), grounding); err != nil {
		t.Fatalf("expected a source review end date to ground, got %v", err)
	}

	// 2026-08-20 is a day inside the quarter, but it is a reflection date the
	// arc prompt never sent — only whole reviews were supplied.
	_, err := parseReviewResponse(cited("2026-08-20"), grounding)
	if err == nil || !strings.Contains(err.Error(), "cites 2026-08-20") {
		t.Fatalf("expected an entry date that was never sent to be refused, got %v", err)
	}
}

// A refusal has to describe the material the arc prompt actually supplied. Both
// gates — section evidence and focus_assessment evidence — send an arc author
// back to source review end dates, while an entry review keeps the reflection
// wording it has always used.
func TestArcGroundingErrorsNameSourceReviews(t *testing.T) {
	t.Parallel()

	window := arcPromptWindow(t, q3Sources()...)
	// The focus took effect after the first source closed, so 2026-08-09 is a
	// grounded date that still cannot support a verdict about the focus.
	focus := focusContext("Protect the first hour.", "2026-08-14", "2026-08-16", "2026-08-23")
	grounding := newReviewGrounding(window, focus)

	// 2026-08-20 is a day inside the quarter, but no source review ended on it.
	sectionPayload := []byte(`{"focus_assessment":{"summary":"The mornings improved.","evidence":[]},` +
		`"struggles":[{"theme":"Late starts","summary":"Mornings kept slipping.",` +
		`"evidence":[{"date":"2026-08-20","snippet":"Started late."}]}],` +
		`"wins":[],"carry_forward":[],"focus_recommendation":"Protect the first hour."}`)

	_, err := parseReviewResponse(sectionPayload, grounding)
	if err == nil || err.Error() != "struggles[0].evidence[0] cites 2026-08-20, "+
		"which is not one of the supplied source review end dates" {
		t.Fatalf("expected the section refusal to name source review end dates, got %v", err)
	}

	focusPayload := []byte(`{"focus_assessment":{"summary":"The mornings improved.",` +
		`"evidence":[{"date":"2026-08-09","snippet":"Started late every day."}]},` +
		`"struggles":[],"wins":[],"carry_forward":[],"focus_recommendation":"Protect the first hour."}`)

	_, err = parseReviewResponse(focusPayload, grounding)
	if err == nil || err.Error() != "focus_assessment.evidence[0] cites 2026-08-09, "+
		"which is not a supplied source review ending on or after the focus took effect on 2026-08-14" {
		t.Fatalf("expected the focus refusal to name source reviews, got %v", err)
	}

	// The same two payloads against an entry window keep the original nouns.
	entry := newReviewGrounding(testReviewWindow(t, "2026-08-17", "2026-08-23", "2026-08-19"), focus)

	_, err = parseReviewResponse(sectionPayload, entry)
	if err == nil || err.Error() != "struggles[0].evidence[0] cites 2026-08-20, "+
		"which is not one of the supplied reflection dates" {
		t.Fatalf("expected the entry section refusal to keep the reflection wording, got %v", err)
	}

	_, err = parseReviewResponse(focusPayload, entry)
	if err == nil || err.Error() != "focus_assessment.evidence[0] cites 2026-08-09, "+
		"which is not a supplied reflection dated on or after the focus took effect on 2026-08-14" {
		t.Fatalf("expected the entry focus refusal to keep the reflection wording, got %v", err)
	}
}

// A weekly and a monthly review can close on the same day. Both are evidence,
// so both must appear in the prompt distinguishable by period and start — while
// the shared date grounds exactly once.
func TestArcPromptDisambiguatesSourcesSharingAnEndDate(t *testing.T) {
	t.Parallel()

	window := arcPromptWindow(t,
		arcSourceRecord(engine.ReviewPeriodMonthly, "2026-08-01", "2026-08-31", "Monthly review", "August as a whole."),
		arcSourceRecord(engine.ReviewPeriodWeekly, "2026-08-25", "2026-08-31", "Weekly review", "The last week of August."),
	)

	prompt := buildArcReviewPrompt(window, reviewFocusContext{})
	assertPromptContains(t, prompt,
		"Two source reviews can share an End date; their Period and Start tell them apart.",
		"Period: monthly\nStart: 2026-08-01\nEnd: 2026-08-31\nBody: August as a whole.",
		"Period: weekly\nStart: 2026-08-25\nEnd: 2026-08-31\nBody: The last week of August.",
	)

	grounding := newReviewGrounding(window, reviewFocusContext{})
	if got := strings.Join(grounding.entryDates, ","); got != "2026-08-31" {
		t.Fatalf("expected the shared end date to ground once, got %q", got)
	}
}

// window.Entries is empty for an arc window, so focus eligibility has to be
// decided over the source reviews: a source counts only if its window closed on
// or after the focus took effect.
func TestArcFocusEligibilityIsDecidedOverSourceReviews(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	mustAppend(t, path, engine.NewFocusSetRecord(arcTime(t, "2026-08-14"), engine.FocusOriginManual, "", "Protect the first hour."))

	window := arcPromptWindow(t, q3Sources()...)
	focus, err := focusContextForWindow(path, window)
	if err != nil {
		t.Fatalf("focusContextForWindow returned error: %v", err)
	}
	if !focus.applies() {
		t.Fatal("expected the focus in force at the quarter end to apply")
	}
	// 2026-08-09 closed before the focus existed; the other two did not.
	if got := strings.Join(focus.eligibleDates, ","); got != "2026-08-16,2026-08-23" {
		t.Fatalf("expected only sources ending after the focus took effect, got %q", got)
	}

	assertPromptContains(t, buildArcReviewPrompt(window, focus),
		"A focus was in effect at the end of this period, set on 2026-08-14 (manual):",
		"Only these source review end dates may appear in focus_assessment.evidence: 2026-08-16, 2026-08-23.",
		"If those source reviews do not actually speak to the focus,",
	)
}

// A focus set after every source review has nothing to be judged on, and the
// prompt must say so in the arc's own terms rather than name reflections that
// were never supplied.
func TestArcFocusWithNoEligibleSourceAsksForAnEmptyEvidenceArray(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	mustAppend(t, path, engine.NewFocusSetRecord(arcTime(t, "2026-09-15"), engine.FocusOriginManual, "", "Write every morning."))

	window := arcPromptWindow(t, q3Sources()...)
	focus, err := focusContextForWindow(path, window)
	if err != nil {
		t.Fatalf("focusContextForWindow returned error: %v", err)
	}
	if !focus.applies() {
		t.Fatal("expected a focus set inside the quarter to apply at its end")
	}
	if len(focus.eligibleDates) != 0 {
		t.Fatalf("expected no eligible source, got %v", focus.eligibleDates)
	}

	prompt := buildArcReviewPrompt(window, focus)
	assertPromptContains(t, prompt,
		"No supplied source review ended on or after the focus took effect, "+
			"so focus_assessment.evidence must be an empty array and the summary must say plainly "+
			"that there is not enough evidence to judge the focus.",
	)
	if strings.Contains(prompt, "No supplied reflection was written") {
		t.Fatalf("expected the arc wording rather than the reflection wording, got:\n%s", prompt)
	}
}

// The entry prompt keeps its own vocabulary exactly: the shared helper changed
// nouns, never the instruction the weekly review has always emitted.
func TestEntryFocusInstructionsKeepReflectionWording(t *testing.T) {
	t.Parallel()

	window := testReviewWindow(t, "2026-08-17", "2026-08-23", "2026-08-19", "2026-08-20")

	assertPromptContains(t, buildReviewPrompt(window, focusContext("Ship it.", "2026-08-18", "2026-08-19", "2026-08-20")),
		"Only these reflection dates may appear in focus_assessment.evidence: 2026-08-19, 2026-08-20.",
		"If those reflections do not actually speak to the focus,",
	)
	assertPromptContains(t, buildReviewPrompt(window, focusContext("Ship it.", "2026-08-24")),
		"No supplied reflection was written on or after the focus took effect,",
	)
}

// A quarter can hold several focuses. The response schema stays one assessment,
// so the churn reaches the model as context instead.
func TestArcPromptRendersTheInWindowFocusTimeline(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	mustAppend(t, path,
		// Before the quarter opened: in force during it, but not a change in it.
		engine.NewFocusSetRecord(arcTime(t, "2026-06-20"), engine.FocusOriginManual, "", "Old focus from Q2."),
		engine.NewFocusSetRecord(arcTime(t, "2026-07-14"), engine.FocusOriginManual, "", "Protect the first hour."),
		engine.NewFocusClearRecord(arcTime(t, "2026-08-05")),
		engine.NewFocusSetRecord(arcTime(t, "2026-08-14"), engine.FocusOriginManual, "", "Write every morning."),
	)

	window := arcPromptWindow(t, q3Sources()...)
	focus, err := focusContextForWindow(path, window)
	if err != nil {
		t.Fatalf("focusContextForWindow returned error: %v", err)
	}

	prompt := buildArcReviewPrompt(window, focus)
	assertPromptContains(t, prompt,
		"Focus timeline within this period, oldest first",
		"- 2026-07-14: focus set (manual): Protect the first hour.\n"+
			"- 2026-08-05: focus cleared (manual)\n"+
			"- 2026-08-14: focus set (manual): Write every morning.\n",
	)
	if strings.Contains(prompt, "2026-06-20") {
		t.Fatalf("expected focus events outside the window to stay out of the prompt, got:\n%s", prompt)
	}

	// No focus change inside the window is a fact worth stating, not a silence.
	quiet := arcPromptWindow(t, q3Sources()...)
	assertPromptContains(t, buildArcReviewPrompt(quiet, reviewFocusContext{}),
		"The focus was neither set nor cleared during this period.")
}

// Source review bodies are already summaries, so they get their own generous
// limit — and the answer limit the entry prompt has always used must not move
// with it.
func TestCompactPromptTextTruncatesAtTheLimitBoundary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		length    int
		wantRunes int
		truncated bool
	}{
		{name: "one under the limit", length: maxPromptReviewBodyRunes - 1, wantRunes: maxPromptReviewBodyRunes - 1},
		{name: "exactly at the limit", length: maxPromptReviewBodyRunes, wantRunes: maxPromptReviewBodyRunes},
		{name: "one over the limit", length: maxPromptReviewBodyRunes + 1, wantRunes: maxPromptReviewBodyRunes, truncated: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := compactPromptText(strings.Repeat("é", tc.length), maxPromptReviewBodyRunes)
			if runes := []rune(got); len(runes) != tc.wantRunes {
				t.Fatalf("expected %d runes, got %d", tc.wantRunes, len(runes))
			}
			if strings.HasSuffix(got, "…") != tc.truncated {
				t.Fatalf("unexpected truncation marker on %q…", string([]rune(got)[:8]))
			}
		})
	}

	// The entry path still caps at 500 runes, unchanged by the new limit.
	answer := compactPromptAnswer(strings.Repeat("a", maxPromptAnswerRunes+1))
	if runes := []rune(answer); len(runes) != maxPromptAnswerRunes || !strings.HasSuffix(answer, "…") {
		t.Fatalf("expected the answer limit to stay at %d runes with a marker, got %d", maxPromptAnswerRunes, len(runes))
	}
	if maxPromptAnswerRunes >= maxPromptReviewBodyRunes {
		t.Fatalf("expected a review body to get more room than a single answer")
	}
}

// The prompt is where truncation actually has to bite: a runaway review body
// cannot be allowed to crowd out the sources after it.
func TestBuildArcReviewPromptTruncatesLongSourceBodies(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("b", maxPromptReviewBodyRunes+50)
	window := arcPromptWindow(t,
		arcSourceRecord(engine.ReviewPeriodWeekly, "2026-08-17", "2026-08-23", "Weekly review", long),
		arcSourceRecord(engine.ReviewPeriodWeekly, "2026-08-10", "2026-08-16", "Weekly review", "   "),
	)

	prompt := buildArcReviewPrompt(window, reviewFocusContext{})

	body := ""
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "Body: b") {
			body = strings.TrimPrefix(line, "Body: ")
		}
	}
	if runes := []rune(body); len(runes) != maxPromptReviewBodyRunes {
		t.Fatalf("expected the body capped at %d runes, got %d", maxPromptReviewBodyRunes, len(runes))
	}
	if !strings.HasSuffix(body, "…") {
		t.Fatalf("expected a truncation marker on the capped body")
	}
	assertPromptContains(t, prompt, "Body: (blank)")
}
