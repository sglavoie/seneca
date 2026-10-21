package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var journalSettings = ReflectionSettings{Questions: continuityQuestions, AnchorQuestionIndex: 3}

func fixedNow(day int) func() time.Time {
	return func() time.Time { return archiveTime(day, 21, 0) }
}

// newTestJournal opens a journal over a fresh directory holding the given
// entries. Nothing here is process-wide, so every caller may run in parallel.
func newTestJournal(t *testing.T, fs FS, day int, entries ...string) *Journal {
	t.Helper()

	path := filepath.Join(t.TempDir(), "reflections.md")
	if len(entries) > 0 {
		var content strings.Builder
		for _, entry := range entries {
			content.WriteString(entry + EntryTerminator)
		}
		if err := os.WriteFile(path, []byte(content.String()), 0644); err != nil {
			t.Fatalf("seeding the journal: %v", err)
		}
	}
	journal, err := Open(Options{ReflectionsPath: path, Settings: journalSettings, Clock: fixedNow(day), FS: fs})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	return journal
}

func writtenResult(answers ...string) ReflectionResult {
	return ReflectionResult{Saved: true, Answers: answers}
}

func TestOpenRequiresExplicitInputs(t *testing.T) {
	t.Parallel()

	if _, err := Open(Options{Clock: time.Now}); err == nil {
		t.Fatal("expected a journal with no location to be refused")
	}
	if _, err := Open(Options{ReflectionsPath: "/journal/reflections.md"}); err == nil {
		t.Fatal("expected a journal with no clock to be refused")
	}

	journal, err := Open(Options{ReflectionsPath: "/journal/reflections.md", Clock: fixedNow(20)})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if want := filepath.Join("/journal", ArchiveFileName); journal.ArchivePath() != want {
		t.Fatalf("expected the archive beside the reflections file at %s, got %s", want, journal.ArchivePath())
	}
	if journal.Today() != "2026-08-20" {
		t.Fatalf("expected today from the injected clock, got %s", journal.Today())
	}
}

func TestOpenTouchesNoFile(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20)
	dir := filepath.Dir(journal.ReflectionsPath())

	if _, err := journal.Entries(); err != nil {
		t.Fatalf("Entries returned error: %v", err)
	}
	if _, _, err := journal.Focus("2026-08-20"); err != nil {
		t.Fatalf("Focus returned error: %v", err)
	}
	if _, err := journal.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if change, err := journal.ClearFocus(); err != nil || change.Changed {
		t.Fatalf("expected a clear with no focus to be a no-op, got %+v, %v", change, err)
	}
	if _, err := journal.PrepareReflection("2026-08-20", false); err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}

	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(left) != 0 {
		t.Fatalf("expected reads to create nothing, found %d files", len(left))
	}
}

func TestJournalReflectionRoundTrip(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20, dayEntry("2026-08-18", "Noticed.", "Handled.", "Start before nine."))

	draft, err := journal.PrepareReflection("2026-08-20", false)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	if draft.IsEdit || draft.Initial != nil {
		t.Fatalf("expected a new day, got %+v", draft)
	}
	if draft.Context.PriorDate != "2026-08-18" || draft.Context.PriorText != "Start before nine." {
		t.Fatalf("expected the prior intention to be carried, got %+v", draft.Context)
	}
	if draft.Context.PriorAge != "2 days ago" {
		t.Fatalf("expected the age measured from the injected clock, got %q", draft.Context.PriorAge)
	}

	result := writtenResult("Today.", "Well.", "Tomorrow.")
	result.FollowThrough = "Started at ten."
	saved, err := journal.SaveReflection(draft, result, false)
	if err != nil {
		t.Fatalf("SaveReflection returned error: %v", err)
	}
	if saved.IsEdit || len(saved.Entries) != 2 || saved.Path != journal.ReflectionsPath() {
		t.Fatalf("unexpected save report: %+v", saved)
	}

	again, err := journal.PrepareReflection("2026-08-20", false)
	if err != nil {
		t.Fatalf("the second PrepareReflection returned error: %v", err)
	}
	if !again.IsEdit || !equalStrings(again.Initial, result.Answers) {
		t.Fatalf("expected the saved answers back, got %+v", again)
	}
	if again.Context.FollowInitial != "Started at ten." {
		t.Fatalf("expected the stored follow-through back, got %+v", again.Context)
	}

	entries, err := journal.Entries()
	if err != nil {
		t.Fatalf("Entries returned error: %v", err)
	}
	if len(entries) != 2 || entries[1].Date != "2026-08-20" {
		t.Fatalf("expected the new day last, got %d entries", len(entries))
	}
}

func TestPrepareReflectionRefusesBeforeAnythingIsWritten(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20, dayEntry("2026-08-18", "a", "b", "c"))

	var future *FutureDateError
	if _, err := journal.PrepareReflection("2026-08-21", false); !errors.As(err, &future) {
		t.Fatalf("expected a future date to be refused, got %v", err)
	}
	if _, err := journal.PrepareReflection("tomorrow", false); err == nil {
		t.Fatal("expected a date that is not a calendar date to be refused")
	}

	draft, err := journal.PrepareReflection("2026-08-20", false)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	before := readFileString(t, journal.ReflectionsPath())
	if _, err := journal.SaveReflection(draft, ReflectionResult{Saved: true, Answers: []string{"", "", ""}}, false); !errors.Is(err, ErrEmptyReflection) {
		t.Fatalf("expected an empty reflection to be refused, got %v", err)
	}
	if got := readFileString(t, journal.ReflectionsPath()); got != before {
		t.Fatal("expected a refused save to leave the journal untouched")
	}

	noQuestions, err := Open(Options{ReflectionsPath: journal.ReflectionsPath(), Clock: fixedNow(20)})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if _, err := noQuestions.PrepareReflection("2026-08-20", false); err == nil {
		t.Fatal("expected a journal opened without questions to refuse a reflection")
	}
}

// Each subtest injects its own failure and runs beside the others. With a
// process-wide seam this would race; with one FS per journal it cannot.
func TestJournalFaultsAreLocalToOneJournal(t *testing.T) {
	t.Parallel()

	stages := map[string]FS{
		"write":  {WriteFile: func(*os.File, []byte) (int, error) { return 0, injectedFailure }},
		"sync":   {SyncFile: func(*os.File) error { return injectedFailure }},
		"rename": {Rename: func(string, string) error { return injectedFailure }},
		"backup": {Backup: func(string, []byte, os.FileMode) error { return injectedFailure }},
	}
	for name, fs := range stages {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			broken := newTestJournal(t, fs, 20, dayEntry("2026-08-18", "a", "b", "c"))
			healthy := newTestJournal(t, FS{}, 20, dayEntry("2026-08-18", "a", "b", "c"))
			before := readFileString(t, broken.ReflectionsPath())

			for _, journal := range []*Journal{broken, healthy} {
				draft, err := journal.PrepareReflection("2026-08-20", false)
				if err != nil {
					t.Fatalf("PrepareReflection returned error: %v", err)
				}
				saved, err := journal.SaveReflection(draft, writtenResult("x", "y", "z"), false)
				if journal == healthy {
					if err != nil {
						t.Fatalf("expected the healthy journal to save, got %v", err)
					}
					continue
				}
				if !errors.Is(err, injectedFailure) || WriteCommitted(err) {
					t.Fatalf("expected a write-free %s failure, got %v", name, err)
				}
				if saved.Path != "" || saved.Entries != nil {
					t.Fatalf("expected no save report for a write that never landed, got %+v", saved)
				}
			}

			if got := readFileString(t, broken.ReflectionsPath()); got != before {
				t.Fatalf("expected the journal untouched after a %s failure, got %q", name, got)
			}
			if leftovers := tempFileNames(t, filepath.Dir(broken.ReflectionsPath())); len(leftovers) != 0 {
				t.Fatalf("expected no temporary files, got %v", leftovers)
			}
			if got := readFileString(t, healthy.ReflectionsPath()); !strings.Contains(got, "## 2026-08-20") {
				t.Fatal("expected the journal beside it to have saved normally")
			}
		})
	}
}

// failSyncDirOn fails the nth directory flush. A commit of an existing file
// flushes twice: once for the `.bak`, once for the primary replacement.
func failSyncDirOn(n int) FS {
	calls := 0
	return FS{SyncDir: func(dir *os.File) error {
		calls++
		if calls == n {
			return injectedFailure
		}
		return dir.Sync()
	}}
}

func TestJournalReportsACommittedWriteWithItsOutcome(t *testing.T) {
	t.Parallel()

	t.Run("reflection", func(t *testing.T) {
		t.Parallel()

		journal := newTestJournal(t, failSyncDirOn(2), 20, dayEntry("2026-08-18", "a", "b", "c"))
		draft, err := journal.PrepareReflection("2026-08-20", false)
		if err != nil {
			t.Fatalf("PrepareReflection returned error: %v", err)
		}
		saved, err := journal.SaveReflection(draft, writtenResult("x", "y", "z"), false)
		if !WriteCommitted(err) {
			t.Fatalf("expected a committed write failure, got %v", err)
		}
		if saved.Date != "2026-08-20" || len(saved.Entries) != 2 {
			t.Fatalf("expected the outcome of the write that landed, got %+v", saved)
		}
		if got := readFileString(t, journal.ReflectionsPath()); !strings.Contains(got, "## 2026-08-20") {
			t.Fatal("expected the entry to be on disk")
		}
	})

	t.Run("focus", func(t *testing.T) {
		t.Parallel()

		// A new archive has no backup, so its only flush is the first.
		journal := newTestJournal(t, failSyncDirOn(1), 20)
		change, err := journal.SetFocus("Protect the morning.")
		if !WriteCommitted(err) {
			t.Fatalf("expected a committed write failure, got %v", err)
		}
		if !change.Changed || change.Effective != "2026-08-20" || change.Text != "Protect the morning." {
			t.Fatalf("expected the outcome of the write that landed, got %+v", change)
		}
		state, _, err := journal.Focus("2026-08-20")
		if err != nil || state.Text != "Protect the morning." {
			t.Fatalf("expected the focus to be in the archive, got %+v, %v", state, err)
		}
	})
}

func TestJournalFocusAndReviewsShareOneArchive(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20)

	if _, err := journal.SetFocus("   "); !errors.Is(err, ErrBlankFocus) {
		t.Fatalf("expected a blank focus to be refused, got %v", err)
	}
	if _, err := os.Stat(journal.ArchivePath()); !os.IsNotExist(err) {
		t.Fatalf("expected a refused focus to create no archive, got %v", err)
	}

	if _, err := journal.SetFocus("  Protect the morning.  "); err != nil {
		t.Fatalf("SetFocus returned error: %v", err)
	}
	saved, err := journal.SaveReview(ReviewSave{
		Period:        ReviewPeriodWeekly,
		Start:         "2026-08-10",
		End:           "2026-08-16",
		Title:         "Weekly review",
		Body:          "### Recurring wins\n\nYou kept the mornings.",
		AcceptedFocus: "Finish before dinner.",
	})
	if err != nil {
		t.Fatalf("SaveReview returned error: %v", err)
	}
	if saved.FocusEffective != "2026-08-20" {
		t.Fatalf("expected the accepted focus effective on the journal's today, got %q", saved.FocusEffective)
	}

	state, timeline, err := journal.Focus("2026-08-20")
	if err != nil {
		t.Fatalf("Focus returned error: %v", err)
	}
	if state.Text != "Finish before dinner." || state.Origin != FocusOriginReview || state.Source != saved.Review.Review.ID {
		t.Fatalf("expected the review's focus in effect, got %+v", state)
	}
	if len(timeline) != 2 {
		t.Fatalf("expected both focus events in the timeline, got %d", len(timeline))
	}

	reviews, err := journal.Reviews()
	if err != nil {
		t.Fatalf("Reviews returned error: %v", err)
	}
	if len(reviews) != 1 || reviews[0].ID != saved.Review.Review.ID || reviews[0].Title != "Weekly review" {
		t.Fatalf("expected the saved review back, got %+v", reviews)
	}

	// A second save of the same window is a revision of the first.
	revised, err := journal.SaveReview(ReviewSave{Period: ReviewPeriodWeekly, Start: "2026-08-10", End: "2026-08-16", Title: "Weekly review", Body: "Rewritten."})
	if err != nil {
		t.Fatalf("the second SaveReview returned error: %v", err)
	}
	if revised.Review.Review.Revises != saved.Review.Review.ID || revised.AcceptedFocus != "" {
		t.Fatalf("expected a revision with no focus change, got %+v", revised.Review.Review)
	}

	cleared, err := journal.ClearFocus()
	if err != nil {
		t.Fatalf("ClearFocus returned error: %v", err)
	}
	if !cleared.Changed || cleared.Previous.Text != "Finish before dinner." {
		t.Fatalf("expected the clear to report what it replaced, got %+v", cleared)
	}
}

func TestJournalValidateExplainsWithoutWriting(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20,
		dayEntry("2026-08-18", "a", "b", "c"),
		dayEntry("2026-08-18", "d", "e", "f"),
	)
	before := readFileString(t, journal.ReflectionsPath())

	validation, err := journal.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	var duplicates *DuplicateDatesError
	if !errors.As(validation.ReflectionsReason, &duplicates) {
		t.Fatalf("expected the repeated date to be the reason, got %v", validation.ReflectionsReason)
	}
	if validation.Writable() || validation.ArchiveExists || validation.ArchiveReason != nil {
		t.Fatalf("unexpected validation: %+v", validation)
	}
	if len(validation.Reflections.Entries) != 2 {
		t.Fatalf("expected the document to be returned whatever its state, got %d entries", len(validation.Reflections.Entries))
	}

	// Every operation that would write refuses with that same reason.
	if _, err := journal.Entries(); err == nil || err.Error() != validation.ReflectionsReason.Error() {
		t.Fatalf("expected Entries to refuse with the validation reason, got %v", err)
	}
	if _, err := journal.Sort(); err == nil || err.Error() != validation.ReflectionsReason.Error() {
		t.Fatalf("expected Sort to refuse with the validation reason, got %v", err)
	}
	if got := readFileString(t, journal.ReflectionsPath()); got != before {
		t.Fatal("expected validation and refusals to leave the journal untouched")
	}
}

func TestJournalSortAndMerge(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{},
		20,
		dayEntry("2026-08-19", "a", "b", "c"),
		dayEntry("2026-08-17", "d", "e", "f"),
	)

	outcome, err := journal.Sort()
	if err != nil || !outcome.Changed || outcome.Count != 2 {
		t.Fatalf("expected a reordering of 2 entries, got %+v, %v", outcome, err)
	}
	if outcome, err = journal.Sort(); err != nil || outcome.Changed || outcome.Count != 2 {
		t.Fatalf("expected a no-op, got %+v, %v", outcome, err)
	}

	if _, err := journal.Merge(journal.ReflectionsPath()); !errors.Is(err, ErrMergeSameFile) {
		t.Fatalf("expected a merge into itself to be refused, got %v", err)
	}

	source := filepath.Join(t.TempDir(), "incoming.md")
	incoming := dayEntry("2026-08-18", "g", "h", "i") + EntryTerminator
	if err := os.WriteFile(source, []byte(incoming), 0644); err != nil {
		t.Fatalf("seeding the source: %v", err)
	}
	merged, err := journal.Merge(source)
	if err != nil || merged.Merged != 1 {
		t.Fatalf("expected 1 merged entry, got %+v, %v", merged, err)
	}
	if got := readFileString(t, source); got != incoming {
		t.Fatal("expected the source to be read, never changed")
	}
	entries, err := journal.Entries()
	if err != nil {
		t.Fatalf("Entries returned error: %v", err)
	}
	var dates []string
	for _, entry := range entries {
		dates = append(dates, entry.Date)
	}
	if want := []string{"2026-08-17", "2026-08-18", "2026-08-19"}; !equalStrings(dates, want) {
		t.Fatalf("expected %v, got %v", want, dates)
	}

	var conflict *MergeConflictError
	if _, err := journal.Merge(source); !errors.As(err, &conflict) {
		t.Fatalf("expected an overlapping date to be refused, got %v", err)
	}
}
