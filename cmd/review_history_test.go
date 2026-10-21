package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedReviewHistory appends three weekly reviews oldest first and returns the
// archive path.
func seedReviewHistory(t *testing.T) string {
	t.Helper()

	path := newArchivePath(t)
	windows := []struct {
		day        int
		start, end string
		body       string
	}{
		{day: 9, start: "2026-08-03", end: "2026-08-09", body: "Oldest body."},
		{day: 16, start: "2026-08-10", end: "2026-08-16", body: "Middle body."},
		{day: 23, start: "2026-08-17", end: "2026-08-23", body: "Newest body."},
	}
	for _, window := range windows {
		tx := mustOpenArchive(t, path)
		record := tx.Archive.NewReviewRecord(archiveTime(window.day, 20, 0), engine.ReviewPeriodWeekly, window.start, window.end, "", window.body)
		if err := tx.AppendRecords(record); err != nil {
			t.Fatalf("appending review for %s: %v", window.start, err)
		}
	}
	return path
}

func TestReviewHistoryDefaultsToTheNewestReview(t *testing.T) {
	t.Parallel()

	path := seedReviewHistory(t)
	var out strings.Builder

	// 1 is the flag's declared default; the command layer passes it through.
	if err := showReviewHistory(&out, path, 1, false); err != nil {
		t.Fatalf("showReviewHistory returned error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Newest body.") {
		t.Fatalf("expected the newest review, got:\n%s", got)
	}
	if strings.Contains(got, "Middle body.") || strings.Contains(got, "Oldest body.") {
		t.Fatalf("expected only one review by default, got:\n%s", got)
	}
	if reviewHistoryCmd.Flags().Lookup("last").DefValue != "1" {
		t.Fatalf("expected --last to default to 1, got %s", reviewHistoryCmd.Flags().Lookup("last").DefValue)
	}
}

func TestReviewHistoryRendersNewestFirstAndHonoursBounds(t *testing.T) {
	t.Parallel()

	path := seedReviewHistory(t)

	for _, tty := range []bool{false, true} {
		var out strings.Builder
		if err := showReviewHistory(&out, path, 2, tty); err != nil {
			t.Fatalf("showReviewHistory(tty=%v) returned error: %v", tty, err)
		}
		got := out.String()

		newest := strings.Index(got, "Newest body.")
		middle := strings.Index(got, "Middle body.")
		switch {
		case newest < 0 || middle < 0:
			t.Fatalf("tty=%v: expected two reviews, got:\n%s", tty, got)
		case newest > middle:
			t.Fatalf("tty=%v: expected newest first, got:\n%s", tty, got)
		case strings.Contains(got, "Oldest body."):
			t.Fatalf("tty=%v: expected --last 2 to stop at two reviews, got:\n%s", tty, got)
		}
		if !strings.Contains(got, "2026-08-17 to 2026-08-23") {
			t.Fatalf("tty=%v: expected the window in the heading, got:\n%s", tty, got)
		}
	}

	// Asking for more than exist is not an error; it just shows everything.
	var out strings.Builder
	if err := showReviewHistory(&out, path, 99, false); err != nil {
		t.Fatalf("showReviewHistory returned error: %v", err)
	}
	for _, body := range []string{"Oldest body.", "Middle body.", "Newest body."} {
		if !strings.Contains(out.String(), body) {
			t.Fatalf("expected %q in the full history, got:\n%s", body, out.String())
		}
	}
}

func TestReviewHistoryPlainAndTerminalRenderingDiffer(t *testing.T) {
	t.Parallel()

	path := seedReviewHistory(t)

	var plain, tty strings.Builder
	if err := showReviewHistory(&plain, path, 2, false); err != nil {
		t.Fatalf("plain rendering returned error: %v", err)
	}
	if err := showReviewHistory(&tty, path, 2, true); err != nil {
		t.Fatalf("terminal rendering returned error: %v", err)
	}
	if plain.String() == tty.String() {
		t.Fatal("expected the terminal rendering to differ from the plain one")
	}
	if strings.Contains(plain.String(), "─") {
		t.Fatalf("expected no terminal rule in plain output, got:\n%s", plain.String())
	}
	if !strings.Contains(tty.String(), "─") {
		t.Fatalf("expected a terminal rule between reviews, got:\n%s", tty.String())
	}
}

func TestReviewHistoryShowsRevisionLinks(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)

	first := mustOpenArchive(t, path)
	original := first.Archive.NewReviewRecord(archiveTime(23, 20, 0), engine.ReviewPeriodWeekly, "2026-08-17", "2026-08-23", "", "First pass.")
	if err := first.AppendRecords(original); err != nil {
		t.Fatalf("appending the first review: %v", err)
	}

	second := mustOpenArchive(t, path)
	revision := second.Archive.NewReviewRecord(archiveTime(24, 20, 0), engine.ReviewPeriodWeekly, "2026-08-17", "2026-08-23", "", "Second pass.")
	if err := second.AppendRecords(revision); err != nil {
		t.Fatalf("appending the revision: %v", err)
	}

	var out strings.Builder
	if err := showReviewHistory(&out, path, 1, false); err != nil {
		t.Fatalf("showReviewHistory returned error: %v", err)
	}
	if !strings.Contains(out.String(), "revises "+engine.ShortRecordID(original.Review.ID)) {
		t.Fatalf("expected the revision link shown, got:\n%s", out.String())
	}
}

func TestReviewHistoryRejectsNonPositiveBounds(t *testing.T) {
	t.Parallel()

	path := seedReviewHistory(t)

	for _, last := range []int{0, -1} {
		var out strings.Builder
		err := showReviewHistory(&out, path, last, false)
		if err == nil || !strings.Contains(err.Error(), "expected a positive number") {
			t.Fatalf("expected --last %d to be rejected, got %v", last, err)
		}
		if out.String() != "" {
			t.Fatalf("expected no output for a rejected bound, got %q", out.String())
		}
	}
}

func TestReviewHistoryReportsMissingAndEmptyArchives(t *testing.T) {
	t.Parallel()

	t.Run("missing archive", func(t *testing.T) {
		t.Parallel()

		path := newArchivePath(t)
		var out strings.Builder
		if err := showReviewHistory(&out, path, 1, false); err != nil {
			t.Fatalf("showReviewHistory returned error: %v", err)
		}
		if !strings.Contains(out.String(), "no review archive at") {
			t.Fatalf("expected a missing-archive report, got %q", out.String())
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected a read not to create the archive, got %v", err)
		}
	})

	t.Run("focus events only", func(t *testing.T) {
		t.Parallel()

		path := newArchivePath(t)
		mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Ship it."))
		before := readFileString(t, path)

		var out strings.Builder
		if err := showReviewHistory(&out, path, 1, false); err != nil {
			t.Fatalf("showReviewHistory returned error: %v", err)
		}
		if !strings.Contains(out.String(), "No saved reviews yet in") {
			t.Fatalf("expected an empty-history report, got %q", out.String())
		}
		if got := readFileString(t, path); got != before {
			t.Fatalf("expected a read to leave the archive untouched, got:\n%s", got)
		}
	})
}

func TestReviewHistoryNeverNeedsAHarness(t *testing.T) {
	path := seedReviewHistory(t)

	// No harness configured and nothing executable on PATH: any attempt to run
	// one would fail loudly instead of quietly succeeding.
	t.Setenv("SENECA_HARNESS", "")
	t.Setenv("SENECA_MODEL", "")
	t.Setenv("PATH", "")
	t.Setenv("SENECA_FILE", filepath.Join(filepath.Dir(path), "reflections.md"))

	previousLast := reviewHistoryLast
	defer func() { reviewHistoryLast = previousLast }()
	reviewHistoryLast = 3

	var out strings.Builder
	reviewHistoryCmd.SetOut(&out)
	defer reviewHistoryCmd.SetOut(nil)

	if err := runReviewHistory(reviewHistoryCmd, nil); err != nil {
		t.Fatalf("runReviewHistory returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Newest body.") {
		t.Fatalf("expected the history rendered without a harness, got:\n%s", out.String())
	}
}

func TestReviewHistoryPeriodKeepsOnlyThatPeriodBeforeCounting(t *testing.T) {
	t.Parallel()

	path := seedReviewHistory(t)
	tx := mustOpenArchive(t, path)
	monthly := tx.Archive.NewReviewRecord(archiveTime(31, 21, 0), engine.ReviewPeriodMonthly, "2026-08-01", "2026-08-31", "", "Monthly body.")
	if err := tx.AppendRecords(monthly); err != nil {
		t.Fatalf("appending monthly review: %v", err)
	}

	var out strings.Builder
	if err := showReviewHistoryFor(&out, path, 2, "weekly", false); err != nil {
		t.Fatalf("showReviewHistoryFor returned error: %v", err)
	}
	got := out.String()
	// The monthly review is the newest of any kind; filtering first still
	// yields two weekly reviews rather than one.
	if strings.Contains(got, "Monthly body.") || !strings.Contains(got, "Newest body.") || !strings.Contains(got, "Middle body.") {
		t.Fatalf("expected the two newest weekly reviews, got:\n%s", got)
	}

	out.Reset()
	if err := showReviewHistoryFor(&out, path, 5, "quarterly", false); err != nil {
		t.Fatalf("showReviewHistoryFor returned error: %v", err)
	}
	if !strings.HasPrefix(out.String(), "No saved quarterly reviews yet in ") {
		t.Fatalf("expected an empty-period report, got %q", out.String())
	}

	err := showReviewHistoryFor(&strings.Builder{}, path, 1, "week", false)
	if err == nil || err.Error() != `invalid --period "week": expected one of weekly, monthly, quarterly, yearly` {
		t.Fatalf("expected an unknown period refused, got %v", err)
	}

	names, _ := completeReviewPeriods(nil, nil, "")
	if strings.Join(names, " ") != "weekly monthly quarterly yearly" {
		t.Fatalf("unexpected --period completions %v", names)
	}
}
