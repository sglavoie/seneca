package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"os"
	"strings"
	"testing"
)

func TestFocusSetShowClearLifecycle(t *testing.T) {
	path := newArchivePath(t)
	var out strings.Builder

	log := withArchiveMutationLog(t, path, func() {
		if err := setFocus(&out, path, archiveTime(24, 9, 0), []string{"Protect", "the", "first", "hour."}); err != nil {
			t.Fatalf("setFocus returned error: %v", err)
		}
	})
	if log.replacements != 1 {
		t.Fatalf("expected exactly one archive write, got %d", log.replacements)
	}
	if len(log.commits) != 1 || !strings.Contains(log.commits[0], "seneca: focus set 2026-08-24 :: "+path) {
		t.Fatalf("expected one commit of the exact archive path, got %v", log.commits)
	}
	if !strings.Contains(out.String(), "Protect the first hour.") {
		t.Fatalf("expected the joined focus text echoed, got %q", out.String())
	}

	out.Reset()
	if err := showFocus(&out, path, "2026-08-24", "2026-08-30"); err != nil {
		t.Fatalf("showFocus returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Protect the first hour.") {
		t.Fatalf("expected the focus shown, got %q", out.String())
	}
	if !strings.Contains(out.String(), "Focus (set 2026-08-24, 6 days ago, manual)") {
		t.Fatalf("expected the focus dated relative to today, got %q", out.String())
	}

	// Replacing the focus is another set event, not an edit of the first.
	out.Reset()
	if err := setFocus(&out, path, archiveTime(26, 9, 0), []string{"Write the handoff."}); err != nil {
		t.Fatalf("setFocus returned error: %v", err)
	}

	archive, _, err := engine.LoadReviewArchive(path)
	if err != nil {
		t.Fatalf("loadReviewArchive returned error: %v", err)
	}
	if got := archive.FocusOn("2026-08-26"); got.Text != "Write the handoff." {
		t.Fatalf("expected the replacement focus, got %q", got.Text)
	}
	// The first focus is still the answer for the days it covered.
	if got := archive.FocusOn("2026-08-25"); got.Text != "Protect the first hour." {
		t.Fatalf("expected the historical focus, got %q", got.Text)
	}
	if len(archive.FocusEvents()) != 2 {
		t.Fatalf("expected an append-only timeline of two events, got %d", len(archive.FocusEvents()))
	}

	out.Reset()
	log = withArchiveMutationLog(t, path, func() {
		if err := clearFocus(&out, path, archiveTime(27, 9, 0)); err != nil {
			t.Fatalf("clearFocus returned error: %v", err)
		}
	})
	if log.replacements != 1 {
		t.Fatalf("expected exactly one archive write for the clear, got %d", log.replacements)
	}
	if len(log.commits) != 1 || !strings.Contains(log.commits[0], "seneca: focus clear 2026-08-27") {
		t.Fatalf("expected one clear commit, got %v", log.commits)
	}

	out.Reset()
	if err := showFocus(&out, path, "2026-08-27", "2026-08-27"); err != nil {
		t.Fatalf("showFocus returned error: %v", err)
	}
	if !strings.Contains(out.String(), "No focus in effect on 2026-08-27") {
		t.Fatalf("expected no active focus after a clear, got %q", out.String())
	}

	// A clear ends the timeline going forward but rewrites no history.
	archive, _, err = engine.LoadReviewArchive(path)
	if err != nil {
		t.Fatalf("loadReviewArchive returned error: %v", err)
	}
	if got := archive.FocusOn("2026-08-26"); got.Text != "Write the handoff." {
		t.Fatalf("expected the day before the clear to keep its focus, got %q", got.Text)
	}
}

func TestFocusSetRejectsBlankText(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	var out strings.Builder

	err := setFocus(&out, path, archiveTime(24, 9, 0), []string{"   ", "\t"})
	if err == nil || !strings.Contains(err.Error(), "blank") {
		t.Fatalf("expected a blank-text refusal, got %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("expected no archive created for a rejected focus, got %v", statErr)
	}
}

func TestFocusSameDayEventsResolveInCreationOrder(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	var out strings.Builder

	for _, text := range []struct {
		hour int
		text string
	}{
		{9, "Morning intention."},
		{13, "Afternoon correction."},
		{18, "Evening final answer."},
	} {
		if err := setFocus(&out, path, archiveTime(24, text.hour, 0), []string{text.text}); err != nil {
			t.Fatalf("setFocus returned error: %v", err)
		}
	}

	archive, _, err := engine.LoadReviewArchive(path)
	if err != nil {
		t.Fatalf("loadReviewArchive returned error: %v", err)
	}
	if got := archive.FocusOn("2026-08-24"); got.Text != "Evening final answer." {
		t.Fatalf("expected the last event of the day to win, got %q", got.Text)
	}
	if got := archive.FocusOn("2026-08-23"); got.Active {
		t.Fatalf("expected no focus before the first event, got %q", got.Text)
	}
}

func TestFocusShowReportsMissingArchiveWithoutCreatingIt(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	var out strings.Builder

	if err := showFocus(&out, path, "2026-08-24", "2026-08-24"); err != nil {
		t.Fatalf("showFocus returned error: %v", err)
	}
	if !strings.Contains(out.String(), "No focus recorded yet") {
		t.Fatalf("expected a missing-archive report, got %q", out.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected a read not to create the archive, got %v", err)
	}
}

func TestFocusClearIsAReadOnlyNoOpWhenNothingIsActive(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{name: "no archive", setup: func(*testing.T, string) {}},
		{
			name: "already cleared",
			setup: func(t *testing.T, path string) {
				t.Helper()
				var discard strings.Builder
				if err := setFocus(&discard, path, archiveTime(20, 9, 0), []string{"Old focus."}); err != nil {
					t.Fatalf("seeding focus: %v", err)
				}
				if err := clearFocus(&discard, path, archiveTime(21, 9, 0)); err != nil {
					t.Fatalf("seeding clear: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := newArchivePath(t)
			tc.setup(t, path)

			before, existed := "", false
			if data, err := os.ReadFile(path); err == nil {
				before, existed = string(data), true
			}

			var out strings.Builder
			log := withArchiveMutationLog(t, path, func() {
				if err := clearFocus(&out, path, archiveTime(24, 9, 0)); err != nil {
					t.Fatalf("clearFocus returned error: %v", err)
				}
			})

			if !strings.Contains(out.String(), "nothing to clear") {
				t.Fatalf("expected a no-op report, got %q", out.String())
			}
			if log.replacements != 0 {
				t.Fatalf("expected no write for a no-op clear, got %d", log.replacements)
			}
			if len(log.commits) != 0 {
				t.Fatalf("expected no commit for a no-op clear, got %v", log.commits)
			}

			data, err := os.ReadFile(path)
			switch {
			case existed && (err != nil || string(data) != before):
				t.Fatalf("expected the archive untouched, got %q (%v)", string(data), err)
			case !existed && !os.IsNotExist(err):
				t.Fatalf("expected no archive created, got %v", err)
			}
		})
	}
}

func TestWeeklyAndMonthlyReviewsShareOneFocusTimeline(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)

	weekly := mustOpenArchive(t, path)
	weeklyReview := weekly.Archive.NewReviewRecord(archiveTime(23, 20, 0), engine.ReviewPeriodWeekly, "2026-08-17", "2026-08-23", "", "Weekly body.")
	if err := weekly.AppendRecords(
		weeklyReview,
		engine.NewFocusRecord(archiveTime(23, 20, 1), engine.FocusEventSet, "2026-08-23", engine.FocusOriginReview, weeklyReview.Review.ID, "Weekly focus."),
	); err != nil {
		t.Fatalf("appending the weekly review: %v", err)
	}

	monthly := mustOpenArchive(t, path)
	monthlyReview := monthly.Archive.NewReviewRecord(archiveTime(31, 20, 0), engine.ReviewPeriodMonthly, "2026-08-01", "2026-08-31", "", "Monthly body.")
	if err := monthly.AppendRecords(
		monthlyReview,
		engine.NewFocusRecord(archiveTime(31, 20, 1), engine.FocusEventSet, "2026-08-31", engine.FocusOriginReview, monthlyReview.Review.ID, "Monthly focus."),
	); err != nil {
		t.Fatalf("appending the monthly review: %v", err)
	}

	// A manual change lands in the same stream as the two review-sourced ones.
	var discard strings.Builder
	if err := setFocus(&discard, path, archiveTime(25, 9, 0), []string{"Manual focus."}); err != nil {
		t.Fatalf("setFocus returned error: %v", err)
	}

	archive, _, err := engine.LoadReviewArchive(path)
	if err != nil {
		t.Fatalf("loadReviewArchive returned error: %v", err)
	}
	if len(archive.FocusEvents()) != 3 {
		t.Fatalf("expected one shared timeline of three events, got %d", len(archive.FocusEvents()))
	}

	cases := []struct {
		on     string
		text   string
		origin engine.FocusOrigin
		source string
	}{
		{on: "2026-08-23", text: "Weekly focus.", origin: engine.FocusOriginReview, source: weeklyReview.Review.ID},
		{on: "2026-08-25", text: "Manual focus.", origin: engine.FocusOriginManual},
		{on: "2026-08-30", text: "Manual focus.", origin: engine.FocusOriginManual},
		{on: "2026-08-31", text: "Monthly focus.", origin: engine.FocusOriginReview, source: monthlyReview.Review.ID},
	}
	for _, tc := range cases {
		got := archive.FocusOn(tc.on)
		if got.Text != tc.text || got.Origin != tc.origin || got.Source != tc.source {
			t.Fatalf("on %s: expected %q from %s (source %s), got %q from %s (source %s)",
				tc.on, tc.text, tc.origin, tc.source, got.Text, got.Origin, got.Source)
		}
	}
}

func TestFocusRefusesToChangeAForeignArchive(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	foreign := "# My own review notes\n\nNot Seneca's.\n"
	if err := os.WriteFile(path, []byte(foreign), 0644); err != nil {
		t.Fatalf("seeding foreign file: %v", err)
	}

	var out strings.Builder
	if err := setFocus(&out, path, archiveTime(24, 9, 0), []string{"Ship it."}); err == nil {
		t.Fatal("expected setFocus to refuse a foreign archive")
	}
	if got := readFileString(t, path); got != foreign {
		t.Fatalf("expected the foreign file untouched, got:\n%s", got)
	}
}
