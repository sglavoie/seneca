package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// secondWriter opens the same journal the way another process would: its own
// Journal, its own lock handle, and little patience.
func secondWriter(t *testing.T, journal *Journal, wait time.Duration) *Journal {
	t.Helper()

	other, err := Open(Options{
		ReflectionsPath: journal.ReflectionsPath(),
		Settings:        journalSettings,
		Clock:           journal.clock,
		LockWait:        wait,
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	return other
}

func requireNoSenecaDir(t *testing.T, dir string) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(dir, JournalMetadataRootName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no %s directory left in %s, got %v", JournalMetadataRootName, dir, err)
	}
}

func TestACommitExcludesEveryOtherWriterUntilItReturns(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20,
		dayEntry("2026-08-19", "Second.", "", ""),
		dayEntry("2026-08-18", "First.", "", ""))
	dir := filepath.Dir(journal.ReflectionsPath())
	other := secondWriter(t, journal, 50*time.Millisecond)

	err := journal.Commit(func(c *Commit) error {
		if _, err := os.Stat(LockPathFor(dir)); err != nil {
			t.Errorf("expected the lock file while the commit runs, got %v", err)
		}

		var busy *JournalBusyError
		if _, err := other.SetFocus("Written by the other process."); !errors.As(err, &busy) {
			t.Errorf("expected the second writer to be told the journal is busy, got %v", err)
		}
		if _, err := other.Sort(); !errors.As(err, &busy) {
			t.Errorf("expected a second reflections writer to be told the journal is busy, got %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	if _, err := os.Stat(journal.ArchivePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the refused writer to have written nothing, got %v", err)
	}
	requireNoSenecaDir(t, dir)

	// The lock went with the commit.
	if _, err := other.SetFocus("Written by the other process."); err != nil {
		t.Fatalf("expected the second writer to succeed once the commit returned, got %v", err)
	}
	requireNoSenecaDir(t, dir)
}

func TestOperationsWithNothingToWriteNeverWaitForTheLock(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20,
		dayEntry("2026-08-18", "First.", "", ""),
		dayEntry("2026-08-19", "Second.", "", ""))
	other := secondWriter(t, journal, 50*time.Millisecond)
	// Settle the file's framing first, so that sorting it again changes nothing.
	if _, err := journal.Sort(); err != nil {
		t.Fatalf("Sort returned error: %v", err)
	}

	err := journal.Commit(func(*Commit) error {
		// Reads, a sort of a journal already in order, and a clear with no
		// focus set all finish while another writer holds the lock.
		if outcome, err := other.Sort(); err != nil || outcome.Changed || outcome.Count != 2 {
			t.Errorf("expected a no-op sort not to need the lock, got %+v, %v", outcome, err)
		}
		if change, err := other.ClearFocus(); err != nil || change.Changed {
			t.Errorf("expected a no-op clear not to need the lock, got %+v, %v", change, err)
		}
		if _, err := other.PrepareReflection("2026-08-20", false); err != nil {
			t.Errorf("expected opening a reflection not to need the lock, got %v", err)
		}
		if _, _, err := other.Focus("2026-08-20"); err != nil {
			t.Errorf("expected reading the focus not to need the lock, got %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
}

func TestTheLockIsReleasedWhenACommitFailsOrIsRefused(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{Rename: func(string, string) error { return injectedFailure }}, 20,
		dayEntry("2026-08-18", "Noticed.", "", ""))
	dir := filepath.Dir(journal.ReflectionsPath())

	if _, err := journal.SetFocus("Never lands."); !errors.Is(err, injectedFailure) {
		t.Fatalf("expected the injected failure, got %v", err)
	}
	if err := journal.Commit(func(*Commit) error { return errors.New("refused") }); err == nil {
		t.Fatal("expected the refusal to surface")
	}
	requireNoSenecaDir(t, dir)

	if _, err := secondWriter(t, journal, 50*time.Millisecond).SetFocus("Lands."); err != nil {
		t.Fatalf("expected the journal to be writable after a failed commit, got %v", err)
	}
}

func TestNoLockIsHeldWhileAReflectionIsBeingWritten(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20, dayEntry("2026-08-18", "Noticed.", "", "Start before nine."))
	dir := filepath.Dir(journal.ReflectionsPath())

	// The author opens today's form and takes their time.
	draft, err := journal.PrepareReflection("2026-08-20", false)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	requireNoSenecaDir(t, dir)

	// Meanwhile a background writer saves another day and changes the focus,
	// without waiting.
	background := secondWriter(t, journal, 50*time.Millisecond)
	other, err := background.PrepareReflection("2026-08-19", false)
	if err != nil {
		t.Fatalf("the background PrepareReflection returned error: %v", err)
	}
	if _, err := background.SaveReflection(other, writtenResult("From the phone.", "", ""), true); err != nil {
		t.Fatalf("expected the background save not to wait on an open form, got %v", err)
	}
	if _, err := background.SetFocus("Set in the background."); err != nil {
		t.Fatalf("expected the background focus change not to wait on an open form, got %v", err)
	}

	// The author saves: their day lands beside the one written meanwhile.
	saved, err := journal.SaveReflection(draft, writtenResult("Today.", "", "Tomorrow."), false)
	if err != nil {
		t.Fatalf("SaveReflection returned error: %v", err)
	}
	var dates []string
	for _, entry := range saved.Entries {
		dates = append(dates, entry.Date)
	}
	if !equalStrings(dates, []string{"2026-08-18", "2026-08-19", "2026-08-20"}) {
		t.Fatalf("expected both writers' days in the journal, got %v", dates)
	}
	if got := readFileString(t, journal.ReflectionsPath()); !strings.Contains(got, "From the phone.") || !strings.Contains(got, "Today.") {
		t.Fatalf("expected neither save to overwrite the other, got:\n%s", got)
	}
	requireNoSenecaDir(t, dir)
}

func TestALockFileLeftByADeadProcessBlocksNobody(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20, dayEntry("2026-08-18", "Noticed.", "", ""))
	dir := filepath.Dir(journal.ReflectionsPath())
	lock := LockPathFor(dir)
	if err := os.MkdirAll(filepath.Dir(lock), 0755); err != nil {
		t.Fatalf("creating the lock directory: %v", err)
	}
	// A process that died held an advisory lock the system has since dropped;
	// only its file is left.
	if err := os.WriteFile(lock, nil, 0600); err != nil {
		t.Fatalf("leaving a lock file behind: %v", err)
	}

	if _, err := secondWriter(t, journal, 50*time.Millisecond).SetFocus("Still writable."); err != nil {
		t.Fatalf("expected a leftover lock file not to block a write, got %v", err)
	}
	requireNoSenecaDir(t, dir)
}

func TestTheLockNeverCreatesTheJournalsDirectory(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "not-yet", "reflections.md")
	journal, err := Open(Options{ReflectionsPath: missing, Settings: journalSettings, Clock: fixedNow(20)})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	if _, err := journal.SetFocus("Nowhere to land."); err == nil {
		t.Fatal("expected a write into a missing directory to fail")
	}
	if _, err := os.Stat(filepath.Dir(missing)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the missing directory to stay missing, got %v", err)
	}
}

func TestJournalsThatShareADirectoryShareItsLock(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20, dayEntry("2026-08-18", "Noticed.", "", ""))
	dir := filepath.Dir(journal.ReflectionsPath())

	// A second journal beside the first, and a journal opened on the archive
	// alone: all three write the same archive.
	work, err := Open(Options{ReflectionsPath: filepath.Join(dir, "work.md"), Settings: journalSettings, Clock: fixedNow(20), LockWait: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	archiveOnly, err := Open(Options{ArchivePath: journal.ArchivePath(), Clock: fixedNow(20), LockWait: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	err = journal.Commit(func(*Commit) error {
		var busy *JournalBusyError
		if _, err := work.SetFocus("From the journal next door."); !errors.As(err, &busy) {
			t.Errorf("expected the neighbouring journal to be excluded, got %v", err)
		}
		if _, err := archiveOnly.SetFocus("From the archive alone."); !errors.As(err, &busy) {
			t.Errorf("expected the archive-only journal to be excluded, got %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
}

func TestCompetingWritersNeverLoseASave(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20, dayEntry("2026-07-31", "Before.", "", ""))
	dir := filepath.Dir(journal.ReflectionsPath())

	const writers = 8
	var wg sync.WaitGroup
	failures := make(chan error, writers*2)
	for i := 1; i <= writers; i++ {
		date := fmt.Sprintf("2026-08-%02d", i)
		focus := fmt.Sprintf("Focus number %d.", i)

		wg.Add(2)
		go func() {
			defer wg.Done()
			writer := secondWriter(t, journal, 30*time.Second)
			draft, err := writer.PrepareReflection(date, true)
			if err != nil {
				failures <- err
				return
			}
			if _, err := writer.SaveReflection(draft, writtenResult("Written on "+date+".", "", ""), true); err != nil {
				failures <- err
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := secondWriter(t, journal, 30*time.Second).SetFocus(focus); err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("a competing writer failed: %v", err)
	}

	entries, err := journal.Entries()
	if err != nil {
		t.Fatalf("Entries returned error: %v", err)
	}
	if len(entries) != writers+1 {
		t.Fatalf("expected every writer's day in the journal, got %d entries", len(entries))
	}
	content := readFileString(t, journal.ReflectionsPath())
	for i := 1; i <= writers; i++ {
		if want := fmt.Sprintf("Written on 2026-08-%02d.", i); strings.Count(content, want) != 1 {
			t.Errorf("expected %q exactly once in the journal", want)
		}
	}

	_, events, err := journal.Focus("2026-08-20")
	if err != nil {
		t.Fatalf("Focus returned error: %v", err)
	}
	if len(events) != writers {
		t.Fatalf("expected every focus change in the archive, got %d", len(events))
	}
	validation, err := journal.Validate()
	if err != nil || !validation.Writable() {
		t.Fatalf("expected a journal that still accepts writes, got %+v, %v", validation, err)
	}
	if leftovers := tempFileNames(t, dir); len(leftovers) != 0 {
		t.Fatalf("expected no temporary files, got %v", leftovers)
	}
	requireNoSenecaDir(t, dir)
}

func TestWritersCompetingForOneDayProduceOneWinner(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20, dayEntry("2026-08-18", "Noticed.", "", ""))

	// Every writer opens the same empty day before any of them saves.
	const writers = 6
	drafts := make([]ReflectionDraft, writers)
	for i := range drafts {
		draft, err := secondWriter(t, journal, 30*time.Second).PrepareReflection("2026-08-20", true)
		if err != nil {
			t.Fatalf("PrepareReflection returned error: %v", err)
		}
		drafts[i] = draft
	}

	var wg sync.WaitGroup
	results := make([]error, writers)
	for i := range drafts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = secondWriter(t, journal, 30*time.Second).
				SaveReflection(drafts[i], writtenResult(fmt.Sprintf("Writer %d.", i), "", ""), false)
		}()
	}
	wg.Wait()

	winner := -1
	for i, err := range results {
		var stale *StaleReflectionError
		switch {
		case err == nil && winner < 0:
			winner = i
		case err == nil:
			t.Fatalf("writers %d and %d both believe they saved 2026-08-20", winner, i)
		case !errors.As(err, &stale):
			t.Fatalf("expected the losing writer to be told its draft is stale, got %v", err)
		case stale.Base != RevisionAbsent || stale.Current == RevisionAbsent || stale.Date != "2026-08-20":
			t.Fatalf("expected the refusal to describe a day written meanwhile, got %+v", stale)
		}
	}
	if winner < 0 {
		t.Fatal("expected exactly one writer to save the day")
	}
	content := readFileString(t, journal.ReflectionsPath())
	if strings.Count(content, "## 2026-08-20") != 1 || !strings.Contains(content, fmt.Sprintf("Writer %d.", winner)) {
		t.Fatalf("expected the winner's entry, once, got:\n%s", content)
	}
}

func TestSavingOverAnEntryChangedMeanwhileIsRefused(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20,
		dayEntry("2026-08-18", "Noticed.", "Handled.", "Start before nine."),
		dayEntry("2026-08-19", "Yesterday.", "", ""))
	dir := filepath.Dir(journal.ReflectionsPath())

	mine, err := journal.PrepareReflection("2026-08-18", true)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	if mine.Base == RevisionAbsent || mine.Base == "" {
		t.Fatalf("expected an edit to carry the revision of the entry it opened, got %q", mine.Base)
	}
	theirs, err := secondWriter(t, journal, time.Second).PrepareReflection("2026-08-18", true)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	if theirs.Base != mine.Base {
		t.Fatal("expected two readers of one entry to carry the same base revision")
	}
	if _, err := secondWriter(t, journal, time.Second).SaveReflection(theirs, writtenResult("Theirs.", "Handled.", "Start before nine."), true); err != nil {
		t.Fatalf("the first save returned error: %v", err)
	}
	afterTheirs := readFileString(t, journal.ReflectionsPath())
	backupAfterTheirs := readFileString(t, journal.ReflectionsPath()+BackupSuffix)

	_, err = journal.SaveReflection(mine, writtenResult("Mine.", "Handled.", "Start before nine."), true)
	var stale *StaleReflectionError
	if !errors.As(err, &stale) {
		t.Fatalf("expected the second save to be refused as stale, got %v", err)
	}
	if WriteCommitted(err) {
		t.Fatal("a stale refusal must not read as a committed write")
	}
	if stale.Base != mine.Base || stale.Current == mine.Base || stale.Path != journal.ReflectionsPath() {
		t.Fatalf("expected the refusal to carry both revisions, got %+v", stale)
	}
	if !strings.Contains(err.Error(), "2026-08-18 changed") || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("expected the refusal to say what happened, got %q", err)
	}
	if got := readFileString(t, journal.ReflectionsPath()); got != afterTheirs {
		t.Fatalf("expected the first save left in place, got:\n%s", got)
	}
	if got := readFileString(t, journal.ReflectionsPath()+BackupSuffix); got != backupAfterTheirs {
		t.Fatal("expected a refused save to leave the backup alone")
	}
	requireNoSenecaDir(t, dir)

	// Opening the day again reads what is there now, and that save goes through.
	again, err := journal.PrepareReflection("2026-08-18", true)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	if again.Initial[0] != "Theirs." {
		t.Fatalf("expected the form to show the entry now in the journal, got %q", again.Initial)
	}
	if _, err := journal.SaveReflection(again, writtenResult("Mine, having read theirs.", "Handled.", "Start before nine."), true); err != nil {
		t.Fatalf("expected a save on the current revision to succeed, got %v", err)
	}
}

func TestAStaleDraftSurvivesChangesToOtherDaysAndFraming(t *testing.T) {
	t.Parallel()

	// Framing with a blank line before each rule: the first rewrite settles it.
	path := filepath.Join(t.TempDir(), "reflections.md")
	seed := dayEntry("2026-08-19", "Second.", "", "") + "\n---\n\n" + dayEntry("2026-08-18", "First.", "", "") + "\n---\n"
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatalf("seeding the journal: %v", err)
	}
	journal, err := Open(Options{ReflectionsPath: path, Settings: journalSettings, Clock: fixedNow(20)})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	draft, err := journal.PrepareReflection("2026-08-18", true)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	// Another process reorders the whole file and rewrites a different day.
	other := secondWriter(t, journal, time.Second)
	if outcome, err := other.Sort(); err != nil || !outcome.Changed {
		t.Fatalf("expected the sort to rewrite the file, got %+v, %v", outcome, err)
	}
	neighbour, err := other.PrepareReflection("2026-08-19", true)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	if _, err := other.SaveReflection(neighbour, writtenResult("Second, edited.", "", ""), true); err != nil {
		t.Fatalf("the neighbouring save returned error: %v", err)
	}

	if _, err := journal.SaveReflection(draft, writtenResult("First, edited.", "", ""), true); err != nil {
		t.Fatalf("expected a save whose own day is untouched to succeed, got %v", err)
	}
	content := readFileString(t, path)
	if !strings.Contains(content, "First, edited.") || !strings.Contains(content, "Second, edited.") {
		t.Fatalf("expected both edits in the journal, got:\n%s", content)
	}
}

func TestSavingADayRemovedMeanwhileIsRefused(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20,
		dayEntry("2026-08-18", "Noticed.", "", ""),
		dayEntry("2026-08-19", "Yesterday.", "", ""))

	draft, err := journal.PrepareReflection("2026-08-19", true)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	// Someone deletes the day by hand.
	if err := os.WriteFile(journal.ReflectionsPath(), []byte(dayEntry("2026-08-18", "Noticed.", "", "")+EntryTerminator), 0644); err != nil {
		t.Fatalf("rewriting the journal: %v", err)
	}

	_, err = journal.SaveReflection(draft, writtenResult("Edited.", "", ""), true)
	var stale *StaleReflectionError
	if !errors.As(err, &stale) || stale.Current != RevisionAbsent {
		t.Fatalf("expected a refusal for a day that is gone, got %v", err)
	}
	if !strings.Contains(err.Error(), "was removed") {
		t.Fatalf("expected the refusal to say the day was removed, got %q", err)
	}
	if strings.Contains(readFileString(t, journal.ReflectionsPath()), "2026-08-19") {
		t.Fatal("expected the removed day not to be written back")
	}
}

func TestAReflectionWithNoBaseRevisionIsRefused(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20, dayEntry("2026-08-18", "Noticed.", "", ""))
	before := readFileString(t, journal.ReflectionsPath())

	draft := ReflectionDraft{Date: "2026-08-18", Questions: continuityQuestions}
	if _, err := journal.SaveReflection(draft, writtenResult("Overwritten.", "", ""), true); err == nil {
		t.Fatal("expected a draft that never read the journal to be refused")
	}
	if got := readFileString(t, journal.ReflectionsPath()); got != before {
		t.Fatal("expected the journal untouched")
	}
}
