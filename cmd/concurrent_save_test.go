package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The command line is one writer among several: a background agent may save
// while the form is open or while a harness runs. These tests drive the real
// commands against a second writer and hold them to two rules: neither save is
// silently lost, and writing the journal refused is kept for the author.

// runDaily drives the real `seneca --date` command with the terminal form
// replaced by form, and returns everything the command printed.
func runDaily(t *testing.T, date string, form func(reflectionTUIConfig) engine.ReflectionResult) (string, error) {
	t.Helper()

	previousForm, previousDate, previousBlank := reflectionForm, dateFlag, blankFlag
	defer func() { reflectionForm, dateFlag, blankFlag = previousForm, previousDate, previousBlank }()

	reflectionForm = func(cfg reflectionTUIConfig) (engine.ReflectionResult, error) {
		return form(cfg), nil
	}
	dateFlag, blankFlag = date, false
	return quietly(t, func() error { return runReflection(rootCmd, nil) })
}

// backgroundWriter opens the journal the commands are pointed at the way a
// sync agent would: straight through the engine, with no patience for a lock.
func backgroundWriter(t *testing.T, j compatJournal) *engine.Journal {
	t.Helper()

	settings, err := loadReflectionSettings(j.home)
	if err != nil {
		t.Fatalf("loading the fixture settings: %v", err)
	}
	journal, err := engine.Open(engine.Options{
		ReflectionsPath: j.reflections,
		Settings:        settings,
		Clock:           time.Now,
		LockWait:        100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("engine.Open returned error: %v", err)
	}
	return journal
}

func backgroundSave(t *testing.T, journal *engine.Journal, date string, answers ...string) {
	t.Helper()

	draft, err := journal.PrepareReflection(date, true)
	if err != nil {
		t.Fatalf("the background PrepareReflection returned error: %v", err)
	}
	if _, err := journal.SaveReflection(draft, engine.ReflectionResult{Saved: true, Answers: answers}, true); err != nil {
		t.Fatalf("the background save returned error: %v", err)
	}
}

func requireNoLock(t *testing.T, dir, when string) {
	t.Helper()

	if _, err := os.Stat(engine.LockPathFor(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no journal lock %s, got %v", when, err)
	}
}

func newConcurrentJournal(t *testing.T) compatJournal {
	t.Helper()

	j := newCompatJournal(t)
	writeConfigFile(t, j.home, compatFixture(t, "config.toml"))
	return j
}

var mine = engine.ReflectionResult{
	Saved:         true,
	Answers:       []string{"Mine: noticed.", "Mine: handled.", "Mine: carried."},
	FollowThrough: "Mine: followed through.",
}

// resubmit saves the form exactly as it was shown.
func resubmit(shown *reflectionTUIConfig) func(reflectionTUIConfig) engine.ReflectionResult {
	return func(cfg reflectionTUIConfig) engine.ReflectionResult {
		*shown = cfg
		return engine.ReflectionResult{Saved: true, Answers: cfg.initial, FollowThrough: cfg.context.FollowInitial}
	}
}

func TestDailySaveOverADayWrittenMeanwhileIsRefusedAndTheWritingKept(t *testing.T) {
	// The fixture's last day answers the one before it, so the form carries a
	// follow-through field as well as the questions.
	const day = "2026-03-05"
	j := newConcurrentJournal(t)
	background := backgroundWriter(t, j)
	draftPath := engine.DraftPathFor(j.reflections, day)

	// While the author writes, the background agent saves the same day.
	_, err := runDaily(t, day, func(reflectionTUIConfig) engine.ReflectionResult {
		requireNoLock(t, j.dir, "while the form is open")
		backgroundSave(t, background, day, "Theirs: noticed.", "Theirs: handled.", "Theirs: carried.")
		return mine
	})

	var stale *engine.StaleReflectionError
	if !errors.As(err, &stale) || stale.Date != day {
		t.Fatalf("expected the save to be refused as stale, got %v", err)
	}
	theirs := readFileString(t, j.reflections)
	if !strings.Contains(theirs, "Theirs: noticed.") || strings.Contains(theirs, "Mine:") {
		t.Fatalf("expected the background save left in place, got:\n%s", theirs)
	}
	kept := readFileString(t, draftPath)
	for _, want := range append([]string{mine.FollowThrough}, mine.Answers...) {
		if !strings.Contains(kept, want) {
			t.Fatalf("expected %q in the kept draft, got:\n%s", want, kept)
		}
	}

	// Running the command again puts the author's own words back in the form
	// and says what saving will do to the entry written meanwhile.
	var shown reflectionTUIConfig
	out, err := runDaily(t, day, resubmit(&shown))
	if err != nil {
		t.Fatalf("the second run returned error: %v", err)
	}
	if !equalStrings(shown.initial, mine.Answers) || shown.context.FollowInitial != mine.FollowThrough {
		t.Fatalf("expected the kept writing restored into the form, got %q and %q", shown.initial, shown.context.FollowInitial)
	}
	for _, want := range []string{
		"Restored the unsaved writing for " + day,
		"already holds a different entry for " + day,
		j.reflections + engine.BackupSuffix,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the output, got:\n%s", want, out)
		}
	}

	saved := readFileString(t, j.reflections)
	if !strings.Contains(saved, "Mine: noticed.") || strings.Contains(saved, "Theirs:") {
		t.Fatalf("expected the author's entry after the explicit second save, got:\n%s", saved)
	}
	// The entry it replaced is not gone.
	if got := readFileString(t, j.reflections+engine.BackupSuffix); got != theirs {
		t.Fatal("expected the replaced entry in the backup")
	}
	if _, err := os.Stat(filepath.Join(j.dir, engine.JournalMetadataRootName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the draft and its directories gone once the writing is saved, got %v", err)
	}
}

func TestDailySaveKeepsADayWrittenMeanwhileOnAnotherDate(t *testing.T) {
	j := newConcurrentJournal(t)
	background := backgroundWriter(t, j)

	_, err := runDaily(t, "2026-03-04", func(reflectionTUIConfig) engine.ReflectionResult {
		backgroundSave(t, background, "2026-03-01", "Theirs: an earlier day.", "", "")
		if _, err := background.SetFocus("Set while the form was open."); err != nil {
			t.Errorf("expected a focus change not to wait on the form, got %v", err)
		}
		return mine
	})
	if err != nil {
		t.Fatalf("expected a save that overlaps nothing to succeed, got %v", err)
	}

	content := readFileString(t, j.reflections)
	if !strings.Contains(content, "Theirs: an earlier day.") || !strings.Contains(content, "Mine: noticed.") {
		t.Fatalf("expected both saves in the journal, got:\n%s", content)
	}
	if !strings.Contains(readFileString(t, j.archive), "Set while the form was open.") {
		t.Fatal("expected the focus set meanwhile in the archive")
	}
	if _, err := os.Stat(filepath.Join(j.dir, engine.JournalMetadataRootName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected a clean save to leave no metadata behind, got %v", err)
	}
}

func TestDailySaveThatFailsKeepsTheWritingForTheNextRun(t *testing.T) {
	const day = "2026-03-04"
	j := newConcurrentJournal(t)
	before := readFileString(t, j.reflections)
	draftPath := engine.DraftPathFor(j.reflections, day)

	// The journal cannot be replaced; everything else on the disk still works.
	var out string
	var err error
	withJournalFS(func(s *engine.FS) {
		s.Rename = func(oldpath, newpath string) error {
			if newpath == j.reflections {
				return injectedFailure
			}
			return os.Rename(oldpath, newpath)
		}
	}, func() {
		out, err = runDaily(t, day, func(reflectionTUIConfig) engine.ReflectionResult { return mine })
	})

	if !errors.Is(err, injectedFailure) || engine.WriteCommitted(err) {
		t.Fatalf("expected a write-free failure, got %v", err)
	}
	if got := readFileString(t, j.reflections); got != before {
		t.Fatal("expected the journal untouched by the failed save")
	}
	if !strings.Contains(out, "not saved, but it is kept in "+draftPath) || !strings.Contains(out, "seneca --date "+day) {
		t.Fatalf("expected the author to be told where the writing is, got:\n%s", out)
	}
	if strings.Contains(out, "Reflection saved") {
		t.Fatalf("a failed save must not be announced as saved, got:\n%s", out)
	}

	// Cancelling the restored form keeps the writing, and says where.
	out, err = runDaily(t, day, func(reflectionTUIConfig) engine.ReflectionResult { return engine.ReflectionResult{} })
	if err != nil {
		t.Fatalf("the cancelled run returned error: %v", err)
	}
	if !strings.Contains(out, "still kept in "+draftPath) {
		t.Fatalf("expected the cancelled run to say the writing is still kept, got:\n%s", out)
	}
	if _, err := os.Stat(draftPath); err != nil {
		t.Fatalf("expected the draft to survive a cancelled form, got %v", err)
	}

	// The next run restores it and, with the disk working, saves it.
	var shown reflectionTUIConfig
	out, err = runDaily(t, day, resubmit(&shown))
	if err != nil {
		t.Fatalf("the retry returned error: %v", err)
	}
	if !equalStrings(shown.initial, mine.Answers) {
		t.Fatalf("expected the kept writing restored, got %q", shown.initial)
	}
	if strings.Contains(out, "already holds a different entry") {
		t.Fatalf("nothing else changed the day, so no replacement warning is due, got:\n%s", out)
	}
	if !strings.Contains(readFileString(t, j.reflections), "Mine: noticed.") {
		t.Fatal("expected the kept writing saved by the retry")
	}
	if _, err := os.Stat(draftPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the draft removed once saved, got %v", err)
	}
}

func TestDailySaveThatCannotKeepTheWritingPrintsIt(t *testing.T) {
	j := newConcurrentJournal(t)
	before := readFileString(t, j.reflections)

	var out string
	var err error
	withJournalFS(func(s *engine.FS) {
		s.Rename = func(string, string) error { return injectedFailure }
	}, func() {
		out, err = runDaily(t, "2026-03-04", func(reflectionTUIConfig) engine.ReflectionResult { return mine })
	})

	if !errors.Is(err, injectedFailure) {
		t.Fatalf("expected the injected failure, got %v", err)
	}
	if got := readFileString(t, j.reflections); got != before {
		t.Fatal("expected the journal untouched by the failed save")
	}
	for _, want := range append([]string{"could not be kept", mine.FollowThrough}, mine.Answers...) {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q printed as the last resort, got:\n%s", want, out)
		}
	}
}

func TestADailySaveWhoseFlushFailedIsSavedAndKeepsNoDraft(t *testing.T) {
	const day = "2026-03-04"
	j := newConcurrentJournal(t)

	var out string
	var err error
	// Only the flush after the journal itself is replaced fails: the backup
	// written before it is flushed normally.
	replaced := false
	withJournalFS(func(s *engine.FS) {
		s.Rename = func(oldpath, newpath string) error {
			replaced = replaced || newpath == j.reflections
			return os.Rename(oldpath, newpath)
		}
		s.SyncDir = func(dir *os.File) error {
			if replaced {
				return injectedFailure
			}
			return dir.Sync()
		}
	}, func() {
		out, err = runDaily(t, day, func(reflectionTUIConfig) engine.ReflectionResult { return mine })
	})

	if !engine.WriteCommitted(err) {
		t.Fatalf("expected a committed write with a durability warning, got %v", err)
	}
	if !strings.Contains(readFileString(t, j.reflections), "Mine: noticed.") || !strings.Contains(out, "Reflection saved") {
		t.Fatalf("expected the day saved and announced, got:\n%s", out)
	}
	if _, err := os.Stat(engine.DraftPathFor(j.reflections, day)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a saved day must leave no draft to be restored over it, got %v", err)
	}
}

func TestReviewHarnessRunsWithNoLockAndItsSaveKeepsWhatWasWrittenMeanwhile(t *testing.T) {
	path := archiveWithFocus(t, 18, "Ship the archive layer.")
	dir := filepath.Dir(path)
	background, err := engine.Open(engine.Options{
		ArchivePath: path,
		Clock:       func() time.Time { return archiveTime(24, 19, 0) },
		LockWait:    100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("engine.Open returned error: %v", err)
	}

	fake := &fakeReview{
		tty:      true,
		response: reviewPayload(`{"summary":"You mostly held the focus.","evidence":[]}`),
		decision: reviewDecision{kind: reviewSaveWithoutFocus},
	}
	runtime := fake.runtime(archiveTime(24, 20, 0))
	harness, decide := runtime.harness, runtime.decide
	runtime.harness = func(cfg reviewConfig, prompt string) (reviewReply, error) {
		// The model takes minutes; the background agent does not wait for it.
		requireNoLock(t, dir, "while the harness runs")
		if _, err := background.SetFocus("Set while the model was thinking."); err != nil {
			t.Errorf("expected a focus change not to wait on the harness, got %v", err)
		}
		return harness(cfg, prompt)
	}
	runtime.decide = func(w reviewWindow, r reviewResponse, f reviewFocusContext) (reviewDecision, error) {
		requireNoLock(t, dir, "while the author decides")
		return decide(w, r, f)
	}

	if _, err := quietly(t, func() error {
		return executeReview(runtime, reviewConfig{Harness: harnessClaude}, path, savedWindow(t))
	}); err != nil {
		t.Fatalf("executeReview returned error: %v", err)
	}

	archive := readFileString(t, path)
	for _, want := range []string{"Ship the archive layer.", "Set while the model was thinking.", "Weekly review"} {
		if !strings.Contains(archive, want) {
			t.Errorf("expected %q in the archive, got:\n%s", want, archive)
		}
	}
	if strings.Index(archive, "Set while the model was thinking.") > strings.Index(archive, "## Weekly review") {
		t.Error("expected the review appended after the record written while it was being generated")
	}
	if _, err := os.Stat(filepath.Join(dir, engine.JournalMetadataRootName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected a clean save to leave no metadata behind, got %v", err)
	}
}

func TestAReviewTheArchiveRefusedKeepsItsText(t *testing.T) {
	path := archiveWithFocus(t, 18, "Ship the archive layer.")
	fake := &fakeReview{
		tty:      true,
		response: reviewPayload(`{"summary":"You mostly held the focus.","evidence":[]}`),
		decision: reviewDecision{kind: reviewAcceptFocus, focusText: savedFocusRecommendation},
	}

	var out string
	var err error
	withJournalFS(func(s *engine.FS) {
		s.Backup = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	}, func() {
		out, err = quietly(t, func() error {
			return executeReview(fake.runtime(archiveTime(24, 20, 0)), reviewConfig{Harness: harnessClaude}, path, savedWindow(t))
		})
	})
	if err == nil || engine.WriteCommitted(err) {
		t.Fatalf("expected a write-free failure, got %v", err)
	}

	kept := engine.ReviewDraftPathFor(path, engine.ReviewPeriodWeekly, "2026-08-17", "2026-08-23")
	if !strings.Contains(out, "its text is kept in "+kept) {
		t.Fatalf("expected the author to be told where the review is, got:\n%s", out)
	}
	text := readFileString(t, kept)
	if !strings.Contains(text, "Late starts") || !strings.Contains(text, "Accepted focus: "+savedFocusRecommendation) {
		t.Fatalf("expected the review and its accepted focus kept, got:\n%s", text)
	}
}
