package cmd

import (
	"bytes"
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

func TestParseReviewResponseAcceptsJSONAndCodeFences(t *testing.T) {
	t.Parallel()

	payload := "```json\n" + `{"focus_assessment":null,"struggles":[{"theme":"Late starts","summary":"You repeatedly delayed deep work.","evidence":[{"date":"2026-05-20","snippet":"Started important work after lunch."}]}],"wins":[{"theme":"Exercise","summary":"You kept movement in the week.","evidence":[{"date":"2026-05-21","snippet":"Went for a run before dinner."}]}],"carry_forward":[{"theme":"Plan tomorrow tonight","summary":"You often promised clearer next-day planning.","evidence":[{"date":"2026-05-22","snippet":"Set out the top three tasks for tomorrow."}]}],"focus_recommendation":"Start the hardest task before checking messages."}` + "\n```"

	response, err := parseReviewResponse([]byte(payload), groundingForDates("2026-05-20", "2026-05-21", "2026-05-22"))
	if err != nil {
		t.Fatalf("parseReviewResponse returned error: %v", err)
	}
	if response.FocusRecommendation == "" {
		t.Fatal("expected focus recommendation to be populated")
	}
	if len(response.Struggles) != 1 || len(response.Wins) != 1 || len(response.CarryForward) != 1 {
		t.Fatalf("unexpected parsed response: %+v", response)
	}
}

func TestParseReviewResponseAcceptsClaudeStructuredOutputEnvelope(t *testing.T) {
	t.Parallel()

	payload := `[{"type":"system"},{"type":"result","structured_output":{"focus_assessment":null,"struggles":[],"wins":[],"carry_forward":[],"focus_recommendation":"Protect the first hour."}}]`

	response, err := parseReviewResponse([]byte(payload), reviewGrounding{})
	if err != nil {
		t.Fatalf("parseReviewResponse returned error: %v", err)
	}
	if response.FocusRecommendation != "Protect the first hour." {
		t.Fatalf("unexpected focus recommendation %q", response.FocusRecommendation)
	}
}

func TestParseReviewResponseRequiresFocus(t *testing.T) {
	t.Parallel()

	// focus_assessment is present and null: the schema is satisfied, so the
	// blank recommendation is what this case is left testing.
	_, err := parseReviewResponse([]byte(`{"focus_assessment":null,"struggles":[],"wins":[],"carry_forward":[],"focus_recommendation":""}`), reviewGrounding{})
	if err == nil || !strings.Contains(err.Error(), "missing focus_recommendation") {
		t.Fatalf("expected focus validation error, got %v", err)
	}
}

// parseReviewFlags parses args onto the real review command and restores every
// flag — value and Changed alike — when the test ends, handing back the parse
// error instead of failing on it. The flag variables are package globals, so
// these tests exercise the command layer's own state rather than a copy of it,
// and none of them may run in parallel.
func parseReviewFlags(t *testing.T, args ...string) error {
	t.Helper()

	reviewCmd.Flags().VisitAll(func(f *pflag.Flag) {
		value, changed := f.Value.String(), f.Changed
		t.Cleanup(func() {
			if err := f.Value.Set(value); err != nil {
				t.Fatalf("restoring --%s: %v", f.Name, err)
			}
			f.Changed = changed
		})
	})
	return reviewCmd.Flags().Parse(args)
}

// withReviewPeriodFlags is parseReviewFlags for the tests whose args are
// expected to be valid.
func withReviewPeriodFlags(t *testing.T, args ...string) {
	t.Helper()

	if err := parseReviewFlags(t, args...); err != nil {
		t.Fatalf("parsing %v: %v", args, err)
	}
}

// The set is what the author actually asked for, which is not the same as what
// cobra saw: an explicit --week=false is a value, not a request for a window,
// and --harness is not a period at all.
func TestSetReviewPeriodFlagsNamesOnlyTheRequestedPeriods(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{name: "nothing set is the default weekly window", args: nil, want: nil},
		{name: "week", args: []string{"--week"}, want: []string{"--week"}},
		{name: "month with a value", args: []string{"--month", "2026-08"}, want: []string{"--month"}},
		{name: "bare quarter", args: []string{"--quarter"}, want: []string{"--quarter"}},
		{name: "year with a value", args: []string{"--year", "2026"}, want: []string{"--year"}},
		{name: "an explicit --week=false is not a request", args: []string{"--week=false", "--quarter"}, want: []string{"--quarter"}},
		{name: "harness and model are not periods", args: []string{"--harness", "claude", "--model", "sonnet"}, want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withReviewPeriodFlags(t, tc.args...)

			if got := setReviewPeriodFlags(reviewCmd); !equalStrings(got, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

// Every pair of the four period flags is refused, and the refusal names both.
// The check counts rather than compares pairwise, so an author who typed three
// of them learns all three at once instead of one rerun at a time.
func TestReviewPeriodFlagsAreMutuallyExclusive(t *testing.T) {
	flags := []string{"--week", "--month", "--quarter", "--year"}

	for i := range flags {
		for _, other := range flags[i+1:] {
			pair := []string{flags[i], other}
			t.Run(strings.Join(pair, " "), func(t *testing.T) {
				withReviewPeriodFlags(t, pair...)

				err := runReview(reviewCmd, nil)
				if err == nil {
					t.Fatalf("expected %v to be refused", pair)
				}
				if !strings.Contains(err.Error(), "mutually exclusive") {
					t.Fatalf("expected a mutual-exclusion error, got %v", err)
				}
				for _, flag := range pair {
					if !strings.Contains(err.Error(), flag) {
						t.Fatalf("expected %q to be named in %v", flag, err)
					}
				}
			})
		}
	}

	t.Run("all four are reported in one message", func(t *testing.T) {
		withReviewPeriodFlags(t, flags...)

		err := runReview(reviewCmd, nil)
		if err == nil {
			t.Fatal("expected all four period flags to be refused")
		}
		if got := err.Error(); got != "--week, --month, --quarter, --year are mutually exclusive: choose one period" {
			t.Fatalf("unexpected message %q", got)
		}
	})
}

// The empty-source answer belongs to the command, not only to executeReview:
// a --quarter run with no archive reports that nothing was saved before it
// asks whether a harness is configured. Every environment source of a harness
// is cleared and HOME points at an empty directory, so the only way this test
// can pass is if runReview never reaches resolveReviewConfig.
func TestRunReviewChecksSourcesBeforeRequiringHarness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SENECA_HARNESS", "")
	t.Setenv("SENECA_MODEL", "")

	path := filepath.Join(home, "reflections.md")
	t.Setenv("SENECA_FILE", path)
	archivePath := engine.ArchivePathFor(path)

	withReviewPeriodFlags(t, "--quarter")

	var out bytes.Buffer
	reviewCmd.SetOut(&out)
	t.Cleanup(func() { reviewCmd.SetOut(nil) })

	if err := runReview(reviewCmd, nil); err != nil {
		t.Fatalf("runReview returned error: %v", err)
	}

	// The window follows the real clock here, so only its stable prefix is pinned.
	if got := out.String(); !strings.HasPrefix(got, "No saved reviews found for quarterly review (") {
		t.Fatalf("unexpected output %q", got)
	}
	// Nothing on the empty-source path may create anything: no archive to hold
	// a review, and not even the reflection document the arc path never reads.
	if _, err := os.Stat(archivePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no archive at %s, got %v", archivePath, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no reflection document at %s, got %v", path, err)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("expected %s to stay empty, got %v (%v)", home, entries, err)
	}
}

// An arc period with nothing saved under it stops before the harness: the
// author is told about saved reviews rather than about entries the arc path
// never reads, and no file is created on the way out.
func TestExecuteReviewStopsOnAnArcWindowWithNoSourceReviews(t *testing.T) {
	path := newArchivePath(t)
	fake := &fakeReview{
		tty:      true,
		response: reviewPayload("null"),
		decision: reviewDecision{kind: reviewAcceptFocus, focusText: savedFocusRecommendation},
	}

	if err := executeReview(fake.runtime(archiveTime(24, 20, 0)), reviewConfig{Harness: harnessClaude}, path, arcPromptWindow(t)); err != nil {
		t.Fatalf("executeReview returned error: %v", err)
	}

	if got := fake.out.String(); got != "No saved reviews found for quarterly review (2026-07-01 to 2026-09-30).\n" {
		t.Fatalf("unexpected output %q", got)
	}
	if len(fake.prompts) != 0 {
		t.Fatalf("expected no harness call, got %v", fake.prompts)
	}
	if fake.decisions != 0 {
		t.Fatalf("expected no decision prompt, got %d", fake.decisions)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no archive at %s, got %v", path, err)
	}
}

// The prompt builder is chosen by the window's own source kind, so neither kind
// can be handed the other's material by a caller that forgot a flag.
func TestExecuteReviewDispatchesThePromptOnTheSourceKind(t *testing.T) {
	cases := []struct {
		name     string
		window   func(*testing.T) reviewWindow
		response string
		wants    []string
		absent   string
	}{
		{
			name:     "an arc window reaches the arc prompt",
			window:   func(t *testing.T) reviewWindow { return arcPromptWindow(t, q3Sources()...) },
			response: reviewPayloadCiting("null", "2026-08-23"),
			wants: []string{
				"You are generating a structured quarterly review",
				"The material below is prior reviews Seneca already saved, not raw reflections.",
				"Source reviews:",
			},
			absent: "Reflections:",
		},
		{
			name:     "an entry window keeps the prompt it always had",
			window:   savedWindow,
			response: reviewPayload("null"),
			wants: []string{
				"You are generating a structured weekly review",
				"Use only the supplied reflections.",
				"Reflections:\nDate: 2026-08-18",
			},
			absent: "Source reviews:",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Non-TTY: the report is printed and nothing is ever saved, so the
			// prompt is the only thing under test.
			fake := &fakeReview{response: tc.response}
			if err := executeReview(fake.runtime(archiveTime(24, 20, 0)), reviewConfig{Harness: harnessClaude}, newArchivePath(t), tc.window(t)); err != nil {
				t.Fatalf("executeReview returned error: %v", err)
			}

			if len(fake.prompts) != 1 {
				t.Fatalf("expected exactly one harness call, got %d", len(fake.prompts))
			}
			prompt := fake.prompts[0]
			for _, want := range tc.wants {
				if !strings.Contains(prompt, want) {
					t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
				}
			}
			if strings.Contains(prompt, tc.absent) {
				t.Fatalf("expected prompt to omit %q, got:\n%s", tc.absent, prompt)
			}
		})
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func TestEmptyEntryWindowPointsAtTheLatestEarlierEntry(t *testing.T) {
	window := reviewWindow{
		Title:        "Weekly review",
		Start:        time.Date(2026, time.September, 28, 0, 0, 0, 0, time.UTC),
		End:          time.Date(2026, time.October, 4, 0, 0, 0, 0, time.UTC),
		LatestBefore: "2026-09-10",
	}
	want := "No entries found for weekly review (2026-09-28 to 2026-10-04). Latest entry 2026-09-10; try --month=2026-09."
	if got := window.emptySourceMessage(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
