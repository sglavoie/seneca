package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedFocusHistory appends four focus events oldest first — a manual set, a
// review-origin set, a clear, and a second manual set — and returns the archive
// path together with the review record ID the review-origin event cites.
func seedFocusHistory(t *testing.T) (string, string) {
	t.Helper()

	path := newArchivePath(t)

	tx := mustOpenArchive(t, path)
	review := tx.Archive.NewReviewRecord(archiveTime(9, 20, 0), engine.ReviewPeriodWeekly, "2026-08-03", "2026-08-09", "", "Weekly body.")
	if err := tx.AppendRecords(review); err != nil {
		t.Fatalf("appending the source review: %v", err)
	}
	sourceID := review.Review.ID

	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(4, 9, 0), engine.FocusOriginManual, "", "Oldest focus."))
	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(10, 9, 0), engine.FocusOriginReview, sourceID, "Review focus."))
	mustAppend(t, path, engine.NewFocusClearRecord(archiveTime(18, 9, 0)))
	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Newest focus."))

	return path, sourceID
}

func TestFocusHistoryRendersNewestFirstWithSpans(t *testing.T) {
	t.Parallel()

	path, _ := seedFocusHistory(t)

	for _, tty := range []bool{false, true} {
		var out strings.Builder
		if err := showFocusHistory(&out, path, 10, tty); err != nil {
			t.Fatalf("showFocusHistory(tty=%v) returned error: %v", tty, err)
		}
		got := out.String()

		newest := strings.Index(got, "Newest focus.")
		review := strings.Index(got, "Review focus.")
		oldest := strings.Index(got, "Oldest focus.")
		switch {
		case newest < 0 || review < 0 || oldest < 0:
			t.Fatalf("tty=%v: expected all three focus texts, got:\n%s", tty, got)
		case newest > review || review > oldest:
			t.Fatalf("tty=%v: expected newest first, got:\n%s", tty, got)
		}

		// The newest event is still in effect; every older one ends at the
		// effective date of the event that replaced it.
		for _, want := range []string{
			"span 2026-08-24 → current",
			"span 2026-08-18 → 2026-08-24",
			"span 2026-08-10 → 2026-08-18",
			"span 2026-08-04 → 2026-08-10",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("tty=%v: expected %q in the timeline, got:\n%s", tty, want, got)
			}
		}
	}
}

func TestFocusHistoryCarriesNoScoring(t *testing.T) {
	t.Parallel()

	path, _ := seedFocusHistory(t)

	var out strings.Builder
	if err := showFocusHistory(&out, path, 10, false); err != nil {
		t.Fatalf("showFocusHistory returned error: %v", err)
	}
	// The timeline is structural. Counts, percentages and streaks were declined
	// deliberately; nothing here may quietly reintroduce them.
	for _, banned := range []string{"%", "streak", "Streak", "coverage", "Coverage", "days held", "total"} {
		if strings.Contains(out.String(), banned) {
			t.Fatalf("expected no scoring vocabulary, found %q in:\n%s", banned, out.String())
		}
	}
}

func TestFocusHistoryHonoursLastAndRejectsNonPositiveBounds(t *testing.T) {
	t.Parallel()

	path, _ := seedFocusHistory(t)

	var out strings.Builder
	if err := showFocusHistory(&out, path, 2, false); err != nil {
		t.Fatalf("showFocusHistory returned error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Newest focus.") {
		t.Fatalf("expected the newest event, got:\n%s", got)
	}
	if strings.Contains(got, "Review focus.") || strings.Contains(got, "Oldest focus.") {
		t.Fatalf("expected --last 2 to stop at two events, got:\n%s", got)
	}

	// Asking for more than exist is not an error; it just shows everything.
	var all strings.Builder
	if err := showFocusHistory(&all, path, 99, false); err != nil {
		t.Fatalf("showFocusHistory returned error: %v", err)
	}
	for _, text := range []string{"Newest focus.", "Review focus.", "Oldest focus."} {
		if !strings.Contains(all.String(), text) {
			t.Fatalf("expected %q in the full timeline, got:\n%s", text, all.String())
		}
	}

	for _, last := range []int{0, -1} {
		var rejected strings.Builder
		err := showFocusHistory(&rejected, path, last, false)
		if err == nil || !strings.Contains(err.Error(), "expected a positive number") {
			t.Fatalf("expected --last %d to be rejected, got %v", last, err)
		}
		if rejected.String() != "" {
			t.Fatalf("expected no output for a rejected bound, got %q", rejected.String())
		}
	}

	if focusHistoryCmd.Flags().Lookup("last").DefValue != "10" {
		t.Fatalf("expected --last to default to 10, got %s", focusHistoryCmd.Flags().Lookup("last").DefValue)
	}
}

func TestFocusHistoryReportsMissingAndEmptyArchives(t *testing.T) {
	t.Parallel()

	t.Run("missing archive", func(t *testing.T) {
		t.Parallel()

		path := newArchivePath(t)
		var out strings.Builder
		if err := showFocusHistory(&out, path, 10, false); err != nil {
			t.Fatalf("showFocusHistory returned error: %v", err)
		}
		if !strings.Contains(out.String(), "no review archive at") {
			t.Fatalf("expected a missing-archive report, got %q", out.String())
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected a read not to create the archive, got %v", err)
		}
	})

	t.Run("reviews only", func(t *testing.T) {
		t.Parallel()

		path := newArchivePath(t)
		tx := mustOpenArchive(t, path)
		record := tx.Archive.NewReviewRecord(archiveTime(23, 20, 0), engine.ReviewPeriodWeekly, "2026-08-17", "2026-08-23", "", "A review with no focus.")
		if err := tx.AppendRecords(record); err != nil {
			t.Fatalf("appending the review: %v", err)
		}
		before := readFileString(t, path)

		var out strings.Builder
		if err := showFocusHistory(&out, path, 10, false); err != nil {
			t.Fatalf("showFocusHistory returned error: %v", err)
		}
		// An archive that exists but holds no focus events reads differently
		// from one that is not there at all.
		if !strings.Contains(out.String(), "No focus events yet in") {
			t.Fatalf("expected an empty-timeline report, got %q", out.String())
		}
		if strings.Contains(out.String(), "no review archive at") {
			t.Fatalf("expected an empty archive to read differently from a missing one, got %q", out.String())
		}
		if got := readFileString(t, path); got != before {
			t.Fatalf("expected a read to leave the archive untouched, got:\n%s", got)
		}
	})
}

func TestFocusHistoryAgreesWithFocusOnASharedEffectiveDate(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	// Two sets on the same day: the archive's tie-break is file position, so the
	// later one is what `seneca focus` reports.
	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Superseded same day."))
	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 17, 0), engine.FocusOriginManual, "", "Winner same day."))

	archive, exists, err := engine.LoadReviewArchive(path)
	if err != nil || !exists {
		t.Fatalf("loadReviewArchive(%s) = exists %v, err %v", path, exists, err)
	}
	state := archive.FocusOn("2026-08-24")
	if state.Text != "Winner same day." {
		t.Fatalf("expected focusOn to pick the later event, got %q", state.Text)
	}

	var out strings.Builder
	if err := showFocusHistory(&out, path, 10, false); err != nil {
		t.Fatalf("showFocusHistory returned error: %v", err)
	}
	got := out.String()

	winner := strings.Index(got, state.Text)
	loser := strings.Index(got, "Superseded same day.")
	if winner < 0 || loser < 0 {
		t.Fatalf("expected both events shown, got:\n%s", got)
	}
	if winner > loser {
		t.Fatalf("expected history to show focusOn's winner first, got:\n%s", got)
	}
	if !strings.Contains(got, "span 2026-08-24 → current") {
		t.Fatalf("expected the winner to read as current, got:\n%s", got)
	}
	if !strings.Contains(got, "span 2026-08-24 → 2026-08-24 (superseded the same day)") {
		t.Fatalf("expected the loser to read as superseded the same day, got:\n%s", got)
	}
}

func TestFocusHistoryDistinguishesClearsAndOrigins(t *testing.T) {
	t.Parallel()

	path, sourceID := seedFocusHistory(t)

	var out strings.Builder
	if err := showFocusHistory(&out, path, 10, false); err != nil {
		t.Fatalf("showFocusHistory returned error: %v", err)
	}
	got := out.String()

	// A clear is rendered as a clear and carries no text of its own.
	if !strings.Contains(got, "2026-08-18 — clear") {
		t.Fatalf("expected the clear event titled as a clear, got:\n%s", got)
	}
	if strings.Contains(got, "Focus cleared.") {
		t.Fatalf("expected a clear to carry no text, got:\n%s", got)
	}
	if !strings.Contains(got, "2026-08-24 — set") {
		t.Fatalf("expected a set event titled as a set, got:\n%s", got)
	}

	// Origin is always shown; the source ID only for review-origin events.
	if !strings.Contains(got, "origin review  source "+engine.ShortRecordID(sourceID)) {
		t.Fatalf("expected the review-origin source ID, got:\n%s", got)
	}
	if strings.Contains(got, "origin manual  source") {
		t.Fatalf("expected manual events to carry no source, got:\n%s", got)
	}
	if strings.Contains(got, sourceID) {
		t.Fatalf("expected the source ID abbreviated, not written in full, got:\n%s", got)
	}
}

func TestFocusHistoryPlainAndTerminalRenderingDiffer(t *testing.T) {
	t.Parallel()

	path, _ := seedFocusHistory(t)

	var plain, tty strings.Builder
	if err := showFocusHistory(&plain, path, 10, false); err != nil {
		t.Fatalf("plain rendering returned error: %v", err)
	}
	if err := showFocusHistory(&tty, path, 10, true); err != nil {
		t.Fatalf("terminal rendering returned error: %v", err)
	}
	if plain.String() == tty.String() {
		t.Fatal("expected the terminal rendering to differ from the plain one")
	}
	if strings.Contains(plain.String(), "─") {
		t.Fatalf("expected no terminal rule in plain output, got:\n%s", plain.String())
	}
	if !strings.Contains(tty.String(), "─") {
		t.Fatalf("expected a terminal rule between events, got:\n%s", tty.String())
	}
}

func TestFocusHistoryNeverNeedsAHarness(t *testing.T) {
	path, _ := seedFocusHistory(t)

	// No harness configured and nothing executable on PATH: any attempt to run
	// one would fail loudly instead of quietly succeeding.
	t.Setenv("SENECA_HARNESS", "")
	t.Setenv("SENECA_MODEL", "")
	t.Setenv("PATH", "")
	t.Setenv("SENECA_FILE", filepath.Join(filepath.Dir(path), "reflections.md"))

	previousLast := focusHistoryLast
	defer func() { focusHistoryLast = previousLast }()
	focusHistoryLast = 10

	var out strings.Builder
	focusHistoryCmd.SetOut(&out)
	defer focusHistoryCmd.SetOut(nil)

	if err := runFocusHistory(focusHistoryCmd, nil); err != nil {
		t.Fatalf("runFocusHistory returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Newest focus.") {
		t.Fatalf("expected the timeline rendered without a harness, got:\n%s", out.String())
	}
}
