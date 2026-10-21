package cmd

import (
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The whole arc feature end to end: three weekly reviews are saved the ordinary
// way, then `--quarter` reads them back out of the same file, synthesizes them,
// and appends its verdict beside them. The journal is never opened for the
// quarterly run — its evidence is what Seneca already concluded.
func TestLifecycleQuarterlyReviewSynthesizesTheSavedWeeklyOnes(t *testing.T) {
	l := newLifecycle(t)
	for _, week := range [][2]string{
		{"2026-07-06", "2026-07-12"},
		{"2026-07-13", "2026-07-19"},
		{"2026-07-20", "2026-07-26"},
	} {
		l.seedWeeklyReview(week[0], week[1], week[0], week[1])
	}
	seeded := readFileString(t, l.archive)
	commitsSeeded := len(l.commits)

	// Piped output is a report and nothing else: no decision, no write.
	quarterEnd := reviewDay(t, "2026-09-30").Add(20 * time.Hour)
	if err := l.arcReview(quarterEnd, "2026-Q3", false, reviewPayloadCiting("null", "2026-07-26"), reviewDecision{}); err != nil {
		t.Fatalf("non-TTY quarterly review returned error: %v", err)
	}
	if readFileString(t, l.archive) != seeded {
		t.Fatal("expected a piped quarterly review to leave the archive byte-identical")
	}
	if len(l.commits) != commitsSeeded {
		t.Fatalf("expected no commit for a piped review, got %v", l.commits[commitsSeeded:])
	}
	for _, want := range []string{"Quarterly review", "2026-07-01 to 2026-09-30", "3 source reviews"} {
		if !strings.Contains(l.out.String(), want) {
			t.Fatalf("expected the piped report to contain %q, got:\n%s", want, l.out.String())
		}
	}

	// Declining at the terminal is just as inert.
	if err := l.arcReview(quarterEnd, "2026-Q3", true, reviewPayloadCiting("null", "2026-07-26"), reviewDecision{kind: reviewDiscard}); err != nil {
		t.Fatalf("discarded quarterly review returned error: %v", err)
	}
	if readFileString(t, l.archive) != seeded {
		t.Fatal("expected a discarded quarterly review to leave the archive byte-identical")
	}
	if len(l.commits) != commitsSeeded {
		t.Fatalf("expected no commit for a discarded review, got %v", l.commits[commitsSeeded:])
	}

	// Accepting is the one run that lands: one new review record, one commit.
	if err := l.arcReview(quarterEnd, "2026-Q3", true, reviewPayloadCiting("null", "2026-07-26"),
		reviewDecision{kind: reviewAcceptFocus, focusText: "Protect the first hour of the quarter."}); err != nil {
		t.Fatalf("accepted quarterly review returned error: %v", err)
	}
	if len(l.commits) != commitsSeeded+1 {
		t.Fatalf("expected exactly one commit for the accepted review, got %v", l.commits[commitsSeeded:])
	}
	if !strings.HasPrefix(l.commits[commitsSeeded], "seneca: quarterly review 2026-07-01 to 2026-09-30") {
		t.Fatalf("unexpected commit message %q", l.commits[commitsSeeded])
	}

	// The file re-parses cleanly and the header carries the current schema.
	archive, exists, err := engine.LoadReviewArchive(l.archive)
	if err != nil {
		t.Fatalf("loadReviewArchive returned error: %v", err)
	}
	if !exists {
		t.Fatalf("expected an archive at %s", l.archive)
	}
	if len(archive.Diagnostics) != 0 {
		t.Fatalf("expected a clean re-parse, got %v", archive.Diagnostics)
	}
	if want := fmt.Sprintf("<!-- seneca:%s v=%d -->", engine.MarkerKindArchive, engine.ArchiveSchemaVersion); !strings.Contains(readFileString(t, l.archive), want) {
		t.Fatalf("expected the header %q, got:\n%s", want, readFileString(t, l.archive))
	}

	records := archive.ReviewRecords() // newest first
	if len(records) != 4 {
		t.Fatalf("expected three weekly reviews plus one quarterly, got %d", len(records))
	}
	quarterly := records[0]
	if quarterly.Period != engine.ReviewPeriodQuarterly || quarterly.Start != "2026-07-01" || quarterly.End != "2026-09-30" {
		t.Fatalf("unexpected quarterly record: %s %s to %s", quarterly.Period, quarterly.Start, quarterly.End)
	}
	// It is the first review of this window, so it revises nothing.
	if quarterly.Revises != "" {
		t.Fatalf("expected the first quarterly review to revise nothing, got %q", quarterly.Revises)
	}
	for _, weekly := range records[1:] {
		if weekly.Period != engine.ReviewPeriodWeekly {
			t.Fatalf("expected the seeded reviews to stay weekly, got %q", weekly.Period)
		}
	}
	// The accepted focus rode in the same transaction as the review it came from.
	if state := archive.FocusOn("2026-10-01"); state.Text != "Protect the first hour of the quarter." || state.Source != quarterly.ID {
		t.Fatalf("expected the accepted focus to cite the quarterly review, got %+v", state)
	}

	// The quarter is readable back out, and it is grounded in a source review's
	// window end date rather than a day the author wrote.
	var history strings.Builder
	if err := showReviewHistory(&history, l.archive, 1, false); err != nil {
		t.Fatalf("showReviewHistory returned error: %v", err)
	}
	for _, want := range []string{
		"Quarterly review — 2026-07-01 to 2026-09-30",
		"2026-07-01 to 2026-09-30  ·  3 source reviews",
		"  - `2026-07-26` Opened late again.",
		"### Accepted focus\n\nProtect the first hour of the quarter.",
	} {
		if !strings.Contains(history.String(), want) {
			t.Fatalf("expected the saved quarterly review to contain %q, got:\n%s", want, history.String())
		}
	}
}

func TestLifecycleFocusClearStopsTheDailyContext(t *testing.T) {
	l := newLifecycle(t)
	l.mustSetFocus(archiveTime(20, 9, 0), "Protect the first hour.")
	l.mustReflect(day{date: "2026-08-20", answers: []string{"Noticed 20.", "Handled 20.", "Ship the parser."}})

	l.mustClearFocus(archiveTime(21, 9, 0))
	l.mustReflect(day{date: "2026-08-21", answers: []string{"Noticed 21.", "Handled 21.", "Ship the archive."}, follow: "Shipped it."})
	if l.context.Focus != "" {
		t.Fatalf("expected a cleared focus to leave the form, got %q", l.context.Focus)
	}
	// The prior intention is not focus and survives the clear.
	if l.context.PriorText != "Ship the parser." {
		t.Fatalf("expected the prior intention to survive a clear, got %q", l.context.PriorText)
	}

	// Clearing again is a read-only no-op: no write, no commit.
	before := readFileString(t, l.archive)
	commitsBefore := len(l.commits)
	l.mustClearFocus(archiveTime(22, 9, 0))
	if readFileString(t, l.archive) != before {
		t.Fatal("expected a second clear to leave the archive byte-identical")
	}
	if len(l.commits) != commitsBefore {
		t.Fatalf("expected no commit for a no-op clear, got %v", l.commits[commitsBefore:])
	}
	if !strings.Contains(l.out.String(), "nothing to clear") {
		t.Fatalf("expected the no-op to be reported, got:\n%s", l.out.String())
	}
	requireNoDoctor(t, l.doctor(), doctorError)
}

func TestLifecycleMergeConflictChangesNothingAnywhere(t *testing.T) {
	l := newLifecycle(t)
	l.mustSetFocus(archiveTime(20, 9, 0), "Protect the first hour.")
	l.mustReflect(day{date: "2026-08-20", answers: []string{"Noticed 20.", "Handled 20.", "Ship the parser."}})
	l.mustReflect(day{date: "2026-08-21", answers: []string{"Noticed 21.", "Handled 21.", "Ship the archive."}, follow: "Shipped it."})

	incoming := filepath.Join(l.dir, "incoming.md")
	source := dayEntry("2026-08-21", "Other 21.", "Other 21.", "Other 21.") + "\n---\n\n" +
		dayEntry("2026-08-22", "New 22.", "New 22.", "New 22.") + "\n---\n\n"
	if err := os.WriteFile(incoming, []byte(source), 0644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	before := snapshotDir(t, l.dir)
	t.Setenv("SENECA_FILE", l.path)

	err := runMerge(nil, []string{incoming})
	var conflict *engine.MergeConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a merge conflict, got %v", err)
	}
	if !strings.Contains(err.Error(), "2026-08-21") {
		t.Fatalf("expected the conflicting date to be named, got %v", err)
	}

	// Nothing at all moved: not the journal, not the archive, not the source
	// file, and no new backup.
	if after := snapshotDir(t, l.dir); !equalStrings(after, before) {
		t.Fatalf("expected a refused merge to change nothing:\nbefore %v\nafter  %v", before, after)
	}
	if len(l.commits) != 1 {
		t.Fatalf("expected only the focus commit, got %v", l.commits)
	}
	requireNoDoctor(t, l.doctor(), doctorError)
}

func TestLifecycleDoctorSeesHealthyThenCorruptState(t *testing.T) {
	l := newLifecycle(t)
	l.mustSetFocus(archiveTime(20, 9, 0), "Protect the first hour.")
	l.mustReflect(day{date: "2026-08-20", answers: []string{"Noticed 20.", "Handled 20.", "Ship the parser."}})
	l.mustReflect(day{date: "2026-08-21", answers: []string{"Noticed 21.", "Handled 21.", "Ship the archive."}, follow: "Shipped it."})

	healthy := l.doctor()
	requireNoDoctor(t, healthy, doctorError)
	requireDoctor(t, healthy, doctorOK, "2 entries parsed")
	requireDoctor(t, healthy, doctorOK, "focus active since 2026-08-20")
	requireDoctor(t, healthy, doctorOK, "every record id matches its content")

	// A hand-edit that repeats a date is exactly what blocks every write.
	if err := os.WriteFile(l.path, []byte(l.journal()+dayEntry("2026-08-21", "a", "b", "c")+"\n---\n\n"), 0644); err != nil {
		t.Fatalf("corrupting the journal: %v", err)
	}
	corrupt := l.doctor()
	requireDoctor(t, corrupt, doctorError, "duplicate entry dates: 2026-08-21")
	if err := reportDoctor(&strings.Builder{}, corrupt); err == nil {
		t.Fatal("expected a corrupt journal to fail the doctor")
	}

	// Doctor's report and the write path agree: the same file is refused.
	err := l.reflect(day{date: "2026-08-22", answers: []string{"Noticed 22.", "Handled 22.", "Carry 22."}})
	var duplicate *engine.DuplicateDatesError
	if !errors.As(err, &duplicate) {
		t.Fatalf("expected the same file to be refused for writing, got %v", err)
	}
}
