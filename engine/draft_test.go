package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestARetainedDraftRoundTripsAndLeavesNothingWhenDiscarded(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20, dayEntry("2026-08-18", "Noticed.", "", "Start before nine."))
	dir := filepath.Dir(journal.ReflectionsPath())

	if _, found, err := journal.RetainedDraft("2026-08-20"); err != nil || found {
		t.Fatalf("expected no draft yet, got %v, %v", found, err)
	}
	requireNoSenecaDir(t, dir)

	draft, err := journal.PrepareReflection("2026-08-20", false)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	result := writtenResult("Today.", "", "Tomorrow.")
	result.FollowThrough = "Started at ten."

	path, err := journal.RetainDraft(draft, result, "the save failed")
	if err != nil {
		t.Fatalf("RetainDraft returned error: %v", err)
	}
	if path != DraftPathFor(journal.ReflectionsPath(), "2026-08-20") {
		t.Fatalf("unexpected draft path %s", path)
	}
	if !strings.HasPrefix(path, JournalMetadataDirFor(journal.ReflectionsPath())) {
		t.Fatalf("expected the draft under the journal's own metadata, got %s", path)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("expected a private draft file, got %v, %v", info, err)
	}

	kept, found, err := journal.RetainedDraft("2026-08-20")
	if err != nil || !found {
		t.Fatalf("expected the draft back, got %v, %v", found, err)
	}
	if kept.Date != "2026-08-20" || kept.Reason != "the save failed" || kept.Base != RevisionAbsent ||
		kept.FollowThrough != "Started at ten." || kept.RetainedAt != journal.Now().Format("2006-01-02T15:04:05Z07:00") {
		t.Fatalf("unexpected draft %+v", kept)
	}
	if !equalStrings(kept.AnswersFor(continuityQuestions), result.Answers) {
		t.Fatalf("expected the answers back, got %q", kept.AnswersFor(continuityQuestions))
	}
	// The questions were reordered and one was added since.
	reordered := []string{"Carry?", "New?", "Notice?"}
	if got := kept.AnswersFor(reordered); !equalStrings(got, []string{"Tomorrow.", "", "Today."}) {
		t.Fatalf("expected answers to follow their questions, got %q", got)
	}

	// A draft is not the journal: nothing reads it as an entry.
	if entries, err := journal.Entries(); err != nil || len(entries) != 1 {
		t.Fatalf("expected the journal unchanged by a draft, got %d entries, %v", len(entries), err)
	}

	if err := journal.DiscardDraft("2026-08-20"); err != nil {
		t.Fatalf("DiscardDraft returned error: %v", err)
	}
	if err := journal.DiscardDraft("2026-08-20"); err != nil {
		t.Fatalf("discarding a missing draft returned error: %v", err)
	}
	requireNoSenecaDir(t, dir)
}

func TestADraftThatCannotBeReadIsReportedNotGuessed(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20)
	path := DraftPathFor(journal.ReflectionsPath(), "2026-08-20")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("creating the drafts directory: %v", err)
	}

	for name, content := range map[string]string{
		"not JSON":        "half a dra",
		"a newer version": `{"version":2,"date":"2026-08-20"}`,
		"another day's":   `{"version":1,"date":"2026-08-19"}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatalf("writing the draft: %v", err)
		}
		if _, found, err := journal.RetainedDraft("2026-08-20"); err == nil || found {
			t.Errorf("%s: expected an error and no draft, got %v, %v", name, found, err)
		}
		if got := readFileString(t, path); got != content {
			t.Errorf("%s: expected the unreadable draft left as it is", name)
		}
	}
}

func TestADraftIsKeptEvenWhenTheJournalIsLocked(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20, dayEntry("2026-08-18", "Noticed.", "", ""))
	draft, err := journal.PrepareReflection("2026-08-20", true)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}

	err = journal.Commit(func(*Commit) error {
		// The save that lost the race for the lock still keeps its writing.
		if _, err := journal.RetainDraft(draft, writtenResult("Kept.", "", ""), "busy"); err != nil {
			t.Errorf("expected a draft to be kept without the lock, got %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	if _, found, err := journal.RetainedDraft("2026-08-20"); err != nil || !found {
		t.Fatalf("expected the draft to outlive the commit, got %v, %v", found, err)
	}
}

func TestAFailedDraftWriteLeavesNoEmptyDirectories(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{Rename: func(string, string) error { return injectedFailure }}, 20,
		dayEntry("2026-08-18", "Noticed.", "", ""))
	draft, err := journal.PrepareReflection("2026-08-20", true)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	if _, err := journal.RetainDraft(draft, writtenResult("Lost.", "", ""), "failed"); !errors.Is(err, injectedFailure) {
		t.Fatalf("expected the injected failure, got %v", err)
	}
	requireNoSenecaDir(t, filepath.Dir(journal.ReflectionsPath()))
}

func TestAReviewDraftKeepsTheReviewText(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20)
	path, err := journal.RetainReviewDraft(ReviewSave{
		Period: ReviewPeriodWeekly, Start: "2026-08-10", End: "2026-08-16",
		Title: "Weekly review", Body: "What recurred.", AcceptedFocus: "Start before nine.",
	})
	if err != nil {
		t.Fatalf("RetainReviewDraft returned error: %v", err)
	}
	if path != ReviewDraftPathFor(journal.ArchivePath(), ReviewPeriodWeekly, "2026-08-10", "2026-08-16") {
		t.Fatalf("unexpected review draft path %s", path)
	}
	got := readFileString(t, path)
	for _, want := range []string{"# Weekly review", "What recurred.", "Accepted focus: Start before nine."} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in the kept review, got:\n%s", want, got)
		}
	}
	if _, err := os.Stat(journal.ArchivePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected a kept review not to touch the archive, got %v", err)
	}
}
