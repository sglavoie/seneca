package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests never call a model: the harness is an injected function, and the
// one test that does execute a binary runs a fake `claude` script from a
// temporary PATH.

const savedFocusRecommendation = "Start the hardest task before checking messages."

// reviewPayload is a complete, schema-valid harness reply. assessment is the
// raw JSON for focus_assessment: `null`, or an object.
func reviewPayload(assessment string) string {
	return `{"focus_assessment":` + assessment + `,` +
		`"struggles":[{"theme":"Late starts","summary":"Deep work slipped past noon.",` +
		`"evidence":[{"date":"2026-08-20","snippet":"Started after lunch."}]}],` +
		`"wins":[{"theme":"Training","summary":"You protected movement.",` +
		`"evidence":[{"date":"2026-08-22","snippet":"Ran before dinner."}]}],` +
		`"carry_forward":[],"focus_recommendation":"` + savedFocusRecommendation + `"}`
}

// reviewPayloadCiting is the same complete, schema-valid reply with its one
// evidence date chosen by the caller: a reflection date for an entry review, a
// source review's window end date for an arc one.
func reviewPayloadCiting(assessment, cited string) string {
	return `{"focus_assessment":` + assessment + `,` +
		`"struggles":[{"theme":"Late starts","summary":"Mornings kept slipping past the plan.",` +
		`"evidence":[{"date":"` + cited + `","snippet":"Opened late again."}]}],` +
		`"wins":[],"carry_forward":[],"focus_recommendation":"` + savedFocusRecommendation + `"}`
}

// fakeReview stands in for the terminal, the clock, and the model.
type fakeReview struct {
	prompts    []string
	response   string
	harnessErr error
	decision   reviewDecision
	decideErr  error
	decisions  int
	tty        bool
	out        strings.Builder
}

func (f *fakeReview) runtime(now time.Time) reviewRuntime {
	return reviewRuntime{
		now:   func() time.Time { return now },
		isTTY: func() bool { return f.tty },
		harness: func(cfg reviewConfig, prompt string) (reviewReply, error) {
			f.prompts = append(f.prompts, prompt)
			if f.harnessErr != nil {
				return reviewReply{}, f.harnessErr
			}
			return reviewReply{raw: []byte(f.response)}, nil
		},
		decide: func(reviewWindow, reviewResponse, reviewFocusContext) (reviewDecision, error) {
			f.decisions++
			return f.decision, f.decideErr
		},
		out: &f.out,
	}
}

func savedWindow(t *testing.T) reviewWindow {
	t.Helper()
	return testReviewWindow(t, "2026-08-17", "2026-08-23", "2026-08-18", "2026-08-20", "2026-08-22")
}

func archiveFocusOn(t *testing.T, path, date string) engine.FocusState {
	t.Helper()

	archive, exists, err := engine.LoadReviewArchive(path)
	if err != nil {
		t.Fatalf("loadReviewArchive returned error: %v", err)
	}
	if !exists {
		t.Fatalf("expected an archive at %s", path)
	}
	return archive.FocusOn(date)
}

func TestReviewDecisionsDriveWhatIsPersisted(t *testing.T) {
	cases := []struct {
		name          string
		decision      reviewDecision
		wantWrites    int
		wantFocusText string
		wantOutput    []string
	}{
		{
			name:          "accept adopts the recommendation verbatim",
			decision:      reviewDecision{kind: reviewAcceptFocus, focusText: savedFocusRecommendation},
			wantWrites:    1,
			wantFocusText: savedFocusRecommendation,
			wantOutput:    []string{"Saved Weekly review — 2026-08-17 to 2026-08-23", "Focus set for 2026-08-24"},
		},
		{
			name:          "edit adopts the author's rewrite",
			decision:      reviewDecision{kind: reviewEditFocus, focusText: "  Open the editor before the inbox.  "},
			wantWrites:    1,
			wantFocusText: "Open the editor before the inbox.",
			wantOutput:    []string{"Open the editor before the inbox."},
		},
		{
			name:       "save only leaves the focus timeline alone",
			decision:   reviewDecision{kind: reviewSaveWithoutFocus},
			wantWrites: 1,
			// The focus set before the window is still the one in effect.
			wantFocusText: "Ship the archive layer.",
			wantOutput:    []string{"Focus unchanged (set 2026-08-18)"},
		},
		{
			name:          "discard writes nothing at all",
			decision:      reviewDecision{kind: reviewDiscard},
			wantWrites:    0,
			wantFocusText: "Ship the archive layer.",
			wantOutput:    []string{"Discarded: nothing was written."},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := archiveWithFocus(t, 18, "Ship the archive layer.")
			before := readFileString(t, path)

			fake := &fakeReview{
				tty:      true,
				response: reviewPayload(`{"summary":"You mostly held the focus.","evidence":[{"date":"2026-08-20","snippet":"Worked on the archive."}]}`),
				decision: tc.decision,
			}
			window := savedWindow(t)

			log := withArchiveMutationLog(t, path, func() {
				if err := executeReview(fake.runtime(archiveTime(24, 20, 0)), reviewConfig{Harness: harnessClaude}, path, window); err != nil {
					t.Fatalf("executeReview returned error: %v", err)
				}
			})

			if fake.decisions != 1 {
				t.Fatalf("expected exactly one decision prompt, got %d", fake.decisions)
			}
			if log.replacements != tc.wantWrites {
				t.Fatalf("expected %d archive writes, got %d", tc.wantWrites, log.replacements)
			}
			if len(log.commits) != tc.wantWrites {
				t.Fatalf("expected %d commits, got %v", tc.wantWrites, log.commits)
			}
			if tc.wantWrites == 0 && readFileString(t, path) != before {
				t.Fatal("expected a discarded review to leave the archive byte-identical")
			}

			if got := archiveFocusOn(t, path, "2026-08-24"); got.Text != tc.wantFocusText {
				t.Fatalf("expected focus %q after the decision, got %q", tc.wantFocusText, got.Text)
			}
			for _, want := range tc.wantOutput {
				if !strings.Contains(fake.out.String(), want) {
					t.Fatalf("expected output to contain %q, got:\n%s", want, fake.out.String())
				}
			}
		})
	}
}

func TestSavedReviewKeepsBothTheRecommendationAndTheEditedFocus(t *testing.T) {
	path := archiveWithFocus(t, 18, "Ship the archive layer.")
	fake := &fakeReview{
		tty:      true,
		response: reviewPayload(`{"summary":"You held it on two of three days.","evidence":[{"date":"2026-08-20","snippet":"Worked on the archive."}]}`),
		decision: reviewDecision{kind: reviewEditFocus, focusText: "Open the editor before the inbox."},
	}

	withArchiveMutationLog(t, path, func() {
		if err := executeReview(fake.runtime(archiveTime(24, 20, 0)), reviewConfig{Harness: harnessClaude}, path, savedWindow(t)); err != nil {
			t.Fatalf("executeReview returned error: %v", err)
		}
	})

	content := readFileString(t, path)
	for _, want := range []string{
		"### Previous focus",
		"> Ship the archive layer.",
		"Set 2026-08-18 (manual).",
		"You held it on two of three days.",
		"  - `2026-08-20` Worked on the archive.",
		"### Recurring struggles",
		"- **Late starts** — Deep work slipped past noon.",
		"### Next focus\n\n" + savedFocusRecommendation,
		"### Accepted focus\n\nOpen the editor before the inbox.",
		"origin=review",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected the archive to contain %q, got:\n%s", want, content)
		}
	}

	// The previous-focus block comes before the recurring themes.
	if strings.Index(content, "### Previous focus") > strings.Index(content, "### Recurring struggles") {
		t.Fatal("expected the previous-focus assessment to render before recurring struggles")
	}

	// The focus event points back at the review that justified it.
	archive, _, err := engine.LoadReviewArchive(path)
	if err != nil {
		t.Fatalf("loadReviewArchive returned error: %v", err)
	}
	reviews := archive.ReviewRecords()
	if len(reviews) != 1 {
		t.Fatalf("expected one saved review, got %d", len(reviews))
	}
	state := archive.FocusOn("2026-08-24")
	if state.Source != reviews[0].ID {
		t.Fatalf("expected the focus event to cite review %s, got source %q", engine.ShortRecordID(reviews[0].ID), state.Source)
	}
}

func TestRepeatedReviewWindowsAreLinkedAsRevisions(t *testing.T) {
	path := archiveWithFocus(t, 18, "Ship the archive layer.")
	window := savedWindow(t)
	assessment := `{"summary":"You mostly held the focus.","evidence":[]}`

	var second *fakeReview
	withArchiveMutationLog(t, path, func() {
		for i, decision := range []reviewDecision{
			{kind: reviewAcceptFocus, focusText: savedFocusRecommendation},
			{kind: reviewSaveWithoutFocus},
		} {
			fake := &fakeReview{tty: true, response: reviewPayload(assessment), decision: decision}
			if err := executeReview(fake.runtime(archiveTime(24, 20+i, 0)), reviewConfig{Harness: harnessClaude}, path, window); err != nil {
				t.Fatalf("executeReview %d returned error: %v", i, err)
			}
			second = fake
		}
	})

	archive, _, err := engine.LoadReviewArchive(path)
	if err != nil {
		t.Fatalf("loadReviewArchive returned error: %v", err)
	}
	if len(archive.Diagnostics) != 0 {
		t.Fatalf("expected a clean archive, got %v", archive.Diagnostics)
	}

	records := archive.ReviewRecords() // newest first
	if len(records) != 2 {
		t.Fatalf("expected two saved reviews, got %d", len(records))
	}
	if records[0].Revises != records[1].ID {
		t.Fatalf("expected the newer review to revise %s, got %q", engine.ShortRecordID(records[1].ID), records[0].Revises)
	}
	if !strings.Contains(second.out.String(), "Recorded as a revision of "+engine.ShortRecordID(records[1].ID)) {
		t.Fatalf("expected the revision to be reported, got:\n%s", second.out.String())
	}
}

// The save path needed no special case for arc periods, and this is the proof
// rather than the assumption: newReviewRecord already takes the window's own
// period, and latestReviewForPeriod already keys on (period, start, end), so a
// second quarterly review of the same quarter supersedes the first exactly as a
// second weekly review does — same revision link, same commit message shape.
func TestArcReviewsSaveAndSupersedeThroughTheUnchangedSavePath(t *testing.T) {
	path := newArchivePath(t)
	window := arcPromptWindow(t, q3Sources()...)

	var second *fakeReview
	log := withArchiveMutationLog(t, path, func() {
		responses := []string{
			// The first run meets an empty archive, so no focus applied.
			reviewPayloadCiting("null", "2026-08-23"),
			// The first run then accepted one, effective the day it was accepted
			// — after every source review closed, so there is nothing to judge it
			// on and the evidence array must be empty.
			reviewPayloadCiting(`{"summary":"Too early to tell: no source review closed after the focus was set.","evidence":[]}`, "2026-08-23"),
		}
		for i, decision := range []reviewDecision{
			{kind: reviewAcceptFocus, focusText: savedFocusRecommendation},
			{kind: reviewSaveWithoutFocus},
		} {
			fake := &fakeReview{tty: true, response: responses[i], decision: decision}
			if err := executeReview(fake.runtime(archiveTime(24, 20+i, 0)), reviewConfig{Harness: harnessClaude}, path, window); err != nil {
				t.Fatalf("executeReview %d returned error: %v", i, err)
			}
			second = fake
		}
	})

	if log.replacements != 2 || len(log.commits) != 2 {
		t.Fatalf("expected two writes and two commits, got %d writes and %v", log.replacements, log.commits)
	}
	for _, commit := range log.commits {
		if !strings.HasPrefix(commit, "seneca: quarterly review 2026-07-01 to 2026-09-30") {
			t.Fatalf("unexpected commit message %q", commit)
		}
	}

	archive, _, err := engine.LoadReviewArchive(path)
	if err != nil {
		t.Fatalf("loadReviewArchive returned error: %v", err)
	}
	if len(archive.Diagnostics) != 0 {
		t.Fatalf("expected a clean archive, got %v", archive.Diagnostics)
	}

	records := archive.ReviewRecords() // newest first
	if len(records) != 2 {
		t.Fatalf("expected two saved quarterly reviews, got %d", len(records))
	}
	for _, record := range records {
		if record.Period != engine.ReviewPeriodQuarterly {
			t.Fatalf("expected a quarterly period, got %q", record.Period)
		}
		if record.Start != "2026-07-01" || record.End != "2026-09-30" {
			t.Fatalf("unexpected window %s to %s", record.Start, record.End)
		}
	}
	if records[0].Revises != records[1].ID {
		t.Fatalf("expected the newer quarterly review to revise %s, got %q", engine.ShortRecordID(records[1].ID), records[0].Revises)
	}
	for _, want := range []string{
		"Saved Quarterly review — 2026-07-01 to 2026-09-30",
		"Recorded as a revision of " + engine.ShortRecordID(records[1].ID),
	} {
		if !strings.Contains(second.out.String(), want) {
			t.Fatalf("expected the save report to contain %q, got:\n%s", want, second.out.String())
		}
	}
}

func TestSavedReviewsAreReadableThroughReviewHistory(t *testing.T) {
	path := archiveWithFocus(t, 18, "Ship the archive layer.")

	// One review with an assessment and an accepted focus, one without either.
	withArchiveMutationLog(t, path, func() {
		accepted := &fakeReview{
			tty:      true,
			response: reviewPayload(`{"summary":"You held it on two of three days.","evidence":[{"date":"2026-08-20","snippet":"Worked on the archive."}]}`),
			decision: reviewDecision{kind: reviewEditFocus, focusText: "Open the editor before the inbox."},
		}
		if err := executeReview(accepted.runtime(archiveTime(24, 20, 0)), reviewConfig{Harness: harnessClaude}, path, savedWindow(t)); err != nil {
			t.Fatalf("executeReview returned error: %v", err)
		}

		// A monthly window that predates every focus event: no prior focus.
		monthly := testReviewWindow(t, "2026-07-01", "2026-07-31", "2026-08-20", "2026-08-22")
		monthly.Title = "Monthly review"
		monthly.Period = engine.ReviewPeriodMonthly
		plain := &fakeReview{
			tty:      true,
			response: reviewPayload("null"),
			decision: reviewDecision{kind: reviewSaveWithoutFocus},
		}
		if err := executeReview(plain.runtime(archiveTime(24, 21, 0)), reviewConfig{Harness: harnessClaude}, path, monthly); err != nil {
			t.Fatalf("executeReview returned error: %v", err)
		}
	})

	var out strings.Builder
	if err := showReviewHistory(&out, path, 5, false); err != nil {
		t.Fatalf("showReviewHistory returned error: %v", err)
	}

	rendered := out.String()
	for _, want := range []string{
		"Monthly review — 2026-07-01 to 2026-07-31",
		"No focus was in effect for this period.",
		"Weekly review — 2026-08-17 to 2026-08-23",
		"### Accepted focus",
		"Open the editor before the inbox.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected review history to render %q, got:\n%s", want, rendered)
		}
	}
}

// The harness argv is exercised end to end against a fake executable, so the
// real command construction is covered without contacting a model.
func TestRunReviewHarnessExecutesTheConfiguredCommand(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\ncat <<'JSON'\n%s\nJSON\n", capture, reviewPayload("null"))
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake harness: %v", err)
	}
	// Prepended, not replaced: the fake shadows any real `claude` while the
	// script still finds the shell utilities it uses.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, err := runReviewHarness(reviewConfig{Harness: harnessClaude, Model: "sonnet"}, "the prompt")
	if err != nil {
		t.Fatalf("runReviewHarness returned error: %v", err)
	}

	response, err := parseReviewResponse(out, groundingForDates("2026-08-20", "2026-08-22"))
	if err != nil {
		t.Fatalf("parseReviewResponse returned error: %v", err)
	}
	if response.FocusRecommendation != savedFocusRecommendation {
		t.Fatalf("unexpected recommendation %q", response.FocusRecommendation)
	}

	args := readFileString(t, capture)
	for _, want := range []string{"--json-schema", "--model", "sonnet", "the prompt"} {
		if !strings.Contains(args, want) {
			t.Fatalf("expected the fake harness to receive %q, got:\n%s", want, args)
		}
	}
}

var _ io.Writer = (*strings.Builder)(nil)
