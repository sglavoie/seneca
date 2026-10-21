package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"strings"
	"testing"
	"time"
)

// reviewDay parses a YYYY-MM-DD into the local zone, matching how
// selectReviewWindow builds a window's boundaries.
func reviewDay(t *testing.T, date string) time.Time {
	t.Helper()

	parsed, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		t.Fatalf("parsing %q: %v", date, err)
	}
	return parsed
}

// testReviewWindow builds a weekly window whose entries are real parsed
// reflections, one per supplied date.
func testReviewWindow(t *testing.T, start, end string, dates ...string) reviewWindow {
	t.Helper()

	window := reviewWindow{
		Title:        "Weekly review",
		Period:       engine.ReviewPeriodWeekly,
		PeriodLabel:  start + " to " + end,
		PromptLabel:  "weekly",
		Start:        reviewDay(t, start),
		End:          reviewDay(t, end),
		ExpectedDays: 7,
		CoveredDays:  len(dates),
	}
	for _, date := range dates {
		window.Entries = append(window.Entries, engine.NewReflectionEntry(
			dayEntry(date, "Noticed something on "+date, "Handled something on "+date, "Carry something from "+date)))
	}
	return window
}

func groundingForDates(dates ...string) reviewGrounding {
	return reviewGrounding{entryDates: dates}
}

// focusContext builds the grounding for a focus that took effect on a date,
// without going through an archive file.
func focusContext(text, effective string, eligible ...string) reviewFocusContext {
	return reviewFocusContext{
		state: engine.FocusState{
			Active:    true,
			Text:      text,
			Effective: effective,
			Origin:    engine.FocusOriginReview,
			Source:    "review-record",
		},
		eligibleDates: eligible,
	}
}

func assessmentPayload(summary string, evidenceDate string) string {
	evidence := "[]"
	if evidenceDate != "" {
		evidence = `[{"date":"` + evidenceDate + `","snippet":"Worked on it."}]`
	}
	return `{"focus_assessment":{"summary":"` + summary + `","evidence":` + evidence + `},` +
		`"struggles":[],"wins":[],"carry_forward":[],"focus_recommendation":"Protect the first hour."}`
}

func TestParseReviewResponseAcceptsAFocusAssessment(t *testing.T) {
	t.Parallel()

	focus := focusContext("Ship the archive layer.", "2026-08-18", "2026-08-19", "2026-08-20")
	grounding := reviewGrounding{entryDates: []string{"2026-08-17", "2026-08-19", "2026-08-20"}, focus: focus}

	response, err := parseReviewResponse([]byte(assessmentPayload("You held the focus on most days.", "2026-08-19")), grounding)
	if err != nil {
		t.Fatalf("parseReviewResponse returned error: %v", err)
	}
	if response.FocusAssessment == nil {
		t.Fatal("expected a focus assessment")
	}
	if got := response.FocusAssessment.Summary; got != "You held the focus on most days." {
		t.Fatalf("unexpected assessment summary %q", got)
	}
	if len(response.FocusAssessment.Evidence) != 1 {
		t.Fatalf("expected one evidence item, got %d", len(response.FocusAssessment.Evidence))
	}
}

func TestParseReviewResponseRejectsAMissingFocusAssessment(t *testing.T) {
	t.Parallel()

	grounding := reviewGrounding{focus: focusContext("Ship it.", "2026-08-18", "2026-08-19")}

	_, err := parseReviewResponse([]byte(
		`{"focus_assessment":null,"struggles":[],"wins":[],"carry_forward":[],"focus_recommendation":"Keep going."}`), grounding)
	if err == nil || !strings.Contains(err.Error(), "missing focus_assessment") {
		t.Fatalf("expected a missing-assessment error, got %v", err)
	}
}

func TestParseReviewResponseRejectsAnAssessmentWithoutAFocus(t *testing.T) {
	t.Parallel()

	_, err := parseReviewResponse([]byte(assessmentPayload("You did well.", "")), reviewGrounding{})
	if err == nil || !strings.Contains(err.Error(), "must be null") {
		t.Fatalf("expected a null-assessment error, got %v", err)
	}
}

func TestParseReviewResponseRejectsABlankAssessmentSummary(t *testing.T) {
	t.Parallel()

	grounding := reviewGrounding{focus: focusContext("Ship it.", "2026-08-18", "2026-08-19")}

	_, err := parseReviewResponse([]byte(assessmentPayload("   ", "")), grounding)
	if err == nil || !strings.Contains(err.Error(), "missing summary") {
		t.Fatalf("expected a blank-summary error, got %v", err)
	}
}

func TestParseReviewResponseRejectsAssessmentEvidenceFromAnIneligibleDay(t *testing.T) {
	t.Parallel()

	// 2026-08-17 is inside the review window but predates the focus, so it
	// cannot say anything about how that focus went.
	focus := focusContext("Ship it.", "2026-08-18", "2026-08-19")
	grounding := reviewGrounding{entryDates: []string{"2026-08-17", "2026-08-19"}, focus: focus}

	_, err := parseReviewResponse([]byte(assessmentPayload("You held it.", "2026-08-17")), grounding)
	if err == nil || !strings.Contains(err.Error(), "on or after the focus took effect on 2026-08-18") {
		t.Fatalf("expected an ineligible-evidence error, got %v", err)
	}
}

func TestParseReviewResponseRequiresAnEmptyEvidenceArrayWhenNothingIsEligible(t *testing.T) {
	t.Parallel()

	// The focus took effect after every supplied reflection: there is nothing
	// honest to cite, so only a stated insufficiency is acceptable.
	focus := focusContext("Ship it.", "2026-08-24")
	grounding := reviewGrounding{entryDates: []string{"2026-08-19", "2026-08-20"}, focus: focus}

	if _, err := parseReviewResponse([]byte(assessmentPayload("Not enough evidence to judge this focus yet.", "")), grounding); err != nil {
		t.Fatalf("expected an insufficient-evidence assessment to parse, got %v", err)
	}

	_, err := parseReviewResponse([]byte(assessmentPayload("You held it.", "2026-08-20")), grounding)
	if err == nil || !strings.Contains(err.Error(), "focus_assessment.evidence[0]") {
		t.Fatalf("expected fabricated evidence to be refused, got %v", err)
	}
}

func TestParseReviewResponseRejectsEvidenceDatesTheHarnessNeverSaw(t *testing.T) {
	t.Parallel()

	payload := `{"focus_assessment":null,"struggles":[{"theme":"Late starts","summary":"You delayed deep work.",` +
		`"evidence":[{"date":"2026-05-19","snippet":"Started after lunch."}]}],` +
		`"wins":[],"carry_forward":[],"focus_recommendation":"Start earlier."}`

	_, err := parseReviewResponse([]byte(payload), groundingForDates("2026-05-20", "2026-05-21"))
	if err == nil || !strings.Contains(err.Error(), "not one of the supplied reflection dates") {
		t.Fatalf("expected an unsupported-date error, got %v", err)
	}
}

func TestParseReviewResponseRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	_, err := parseReviewResponse([]byte(`{"focus_assessment":"not an object"`), reviewGrounding{})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected an invalid-JSON error, got %v", err)
	}
}

// A review of a past window must be judged against the focus that was actually
// in force then, not whatever is active today.
func TestFocusContextUsesThePeriodEndFocusNotTodays(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	mustAppend(t, path,
		engine.NewFocusSetRecord(archiveTime(10, 9, 0), engine.FocusOriginManual, "", "Old focus: finish the parser."),
		engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Today's focus: ship the review."),
	)

	window := testReviewWindow(t, "2026-08-11", "2026-08-17", "2026-08-12", "2026-08-15")
	focus, err := focusContextForWindow(path, window)
	if err != nil {
		t.Fatalf("focusContextForWindow returned error: %v", err)
	}

	if !focus.applies() {
		t.Fatal("expected the period-end focus to apply")
	}
	if focus.state.Text != "Old focus: finish the parser." {
		t.Fatalf("expected the focus in force at the window end, got %q", focus.state.Text)
	}
	if len(focus.eligibleDates) != 2 {
		t.Fatalf("expected both window entries to be eligible, got %v", focus.eligibleDates)
	}
}

func TestFocusContextHandlesFocusBeforeDuringAndAfterAWindow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		focusDay      int
		wantApplies   bool
		wantEligible  []string
		wantEffective string
	}{
		{
			name:          "before the window",
			focusDay:      10,
			wantApplies:   true,
			wantEligible:  []string{"2026-08-18", "2026-08-20", "2026-08-22"},
			wantEffective: "2026-08-10",
		},
		{
			name:          "during the window",
			focusDay:      20,
			wantApplies:   true,
			wantEligible:  []string{"2026-08-20", "2026-08-22"},
			wantEffective: "2026-08-20",
		},
		{
			// Set after the window closed: it was never in force for these
			// days, so the review has no prior focus to assess at all.
			name:        "after the window",
			focusDay:    26,
			wantApplies: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := archiveWithFocus(t, tc.focusDay, "Protect the first hour.")
			window := testReviewWindow(t, "2026-08-17", "2026-08-23", "2026-08-18", "2026-08-20", "2026-08-22")

			focus, err := focusContextForWindow(path, window)
			if err != nil {
				t.Fatalf("focusContextForWindow returned error: %v", err)
			}
			if focus.applies() != tc.wantApplies {
				t.Fatalf("expected applies=%v, got %v", tc.wantApplies, focus.applies())
			}
			if !tc.wantApplies {
				return
			}
			if focus.state.Effective != tc.wantEffective {
				t.Fatalf("expected effective %q, got %q", tc.wantEffective, focus.state.Effective)
			}
			if strings.Join(focus.eligibleDates, ",") != strings.Join(tc.wantEligible, ",") {
				t.Fatalf("expected eligible dates %v, got %v", tc.wantEligible, focus.eligibleDates)
			}
		})
	}
}

func TestFocusContextWithoutAnArchiveAppliesNoFocus(t *testing.T) {
	t.Parallel()

	window := testReviewWindow(t, "2026-08-17", "2026-08-23", "2026-08-18")

	focus, err := focusContextForWindow(newArchivePath(t), window)
	if err != nil {
		t.Fatalf("focusContextForWindow returned error: %v", err)
	}
	if focus.applies() {
		t.Fatalf("expected no focus without an archive, got %+v", focus.state)
	}
}

// --blank is a daily flag; a review that suggested it would send the author
// after a flag `seneca review` does not accept.
func TestUnreadableArchiveTellsAReviewerHowToRepairIt(t *testing.T) {
	t.Parallel()

	malformed := newArchivePath(t)
	if err := os.WriteFile(malformed, []byte("<!-- seneca:archive v=2 -->\n\n## Focus\n<!-- seneca:focus v=2 -->\n\nbroken\n"), 0o644); err != nil {
		t.Fatalf("writing malformed archive: %v", err)
	}

	_, err := focusContextForWindow(malformed, testReviewWindow(t, "2026-08-17", "2026-08-23", "2026-08-18"))
	var unreadable *engine.UnreadableContextError
	if !errors.As(err, &unreadable) {
		t.Fatalf("expected an unreadableContextError, got %v", err)
	}
	if !strings.Contains(err.Error(), "seneca doctor") {
		t.Fatalf("expected the message to point at doctor, got %q", err.Error())
	}
	if strings.Contains(err.Error(), "--blank") {
		t.Fatalf("expected no --blank suggestion from review, got %q", err.Error())
	}
}

func TestBuildReviewPromptStatesTheFocusAndItsEligibleDates(t *testing.T) {
	t.Parallel()

	window := testReviewWindow(t, "2026-08-17", "2026-08-23", "2026-08-18", "2026-08-20")
	focus := focusContext("Ship the archive layer.", "2026-08-19", "2026-08-20")

	prompt := buildReviewPrompt(window, focus)

	for _, want := range []string{
		"A focus was in effect at the end of this period, set on 2026-08-19 (review):",
		"Ship the archive layer.",
		"Only these reflection dates may appear in focus_assessment.evidence: 2026-08-20.",
		"Never invent support.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

func TestBuildReviewPromptDemandsAnInsufficientEvidenceSummary(t *testing.T) {
	t.Parallel()

	window := testReviewWindow(t, "2026-08-17", "2026-08-23", "2026-08-18")
	focus := focusContext("Ship the archive layer.", "2026-08-23")

	prompt := buildReviewPrompt(window, focus)

	if !strings.Contains(prompt, "focus_assessment.evidence must be an empty array") {
		t.Fatalf("expected an empty-evidence instruction, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "not enough evidence to judge the focus") {
		t.Fatalf("expected an insufficient-evidence instruction, got:\n%s", prompt)
	}
}

// The prompt is built from parsed sections, so continuity material and sections
// no longer matching a configured question still reach the model.
func TestBuildReviewPromptIncludesFollowThroughAndUnconfiguredSections(t *testing.T) {
	t.Parallel()

	entry := engine.BuildReflectionEntry("2026-08-20", continuityQuestions,
		[]string{"Drifted early.", "Held the training block.", "Write the first task tonight."}, 3)
	entry.SetFollowThrough(engine.FollowThrough{
		SourceDate:      "2026-08-19",
		SourceIntention: "Finish the migration.",
		Response:        "I finished it just before dinner.",
	})

	window := reviewWindow{
		PromptLabel:  "weekly",
		PeriodLabel:  "2026-08-17 to 2026-08-23",
		ExpectedDays: 7,
		CoveredDays:  1,
		Entries:      []engine.ReflectionEntry{engine.NewReflectionEntry(entry.RenderCanonical())},
	}

	prompt := buildReviewPrompt(window, reviewFocusContext{})

	for _, want := range []string{
		"Follow-through — intention from 2026-08-19: Finish the migration.",
		"Follow-through — what happened: I finished it just before dinner.",
		"Notice?: Drifted early.",
		"Carry?: Write the first task tonight.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

func TestBuildReviewPromptCompactsLongAnswers(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("word ", maxPromptAnswerRunes)
	window := reviewWindow{
		PromptLabel: "weekly",
		Entries:     []engine.ReflectionEntry{engine.NewReflectionEntry(dayEntry("2026-08-20", long, "", ""))},
	}

	prompt := buildReviewPrompt(window, reviewFocusContext{})

	line := ""
	for _, candidate := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(candidate, "Notice?: ") {
			line = strings.TrimPrefix(candidate, "Notice?: ")
		}
	}
	if line == "" {
		t.Fatalf("expected a compacted answer line, got:\n%s", prompt)
	}
	if runes := []rune(line); len(runes) != maxPromptAnswerRunes {
		t.Fatalf("expected the answer capped at %d runes, got %d", maxPromptAnswerRunes, len(runes))
	}
	if !strings.HasSuffix(line, "…") {
		t.Fatalf("expected a truncation marker, got %q", line)
	}
	if !strings.Contains(prompt, "Handle?: (blank)") {
		t.Fatalf("expected blank answers to be marked, got:\n%s", prompt)
	}
}
