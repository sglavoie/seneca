package engine

import (
	"errors"
	"testing"
)

func TestDeleteReflectionPreservesOtherEntriesAndBackup(t *testing.T) {
	j := newTestJournal(t, FS{}, 20,
		dayEntry("2026-08-10", "Keep this.", "", ""),
		dayEntry("2026-08-11", "Delete this.", "", ""))
	before := readFileString(t, j.ReflectionsPath())
	entries, err := j.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if err := j.DeleteReflection("2026-08-11", entryRevision(entries, "2026-08-11")); err != nil {
		t.Fatal(err)
	}
	after, err := j.Entries()
	if err != nil || len(after) != 1 || after[0].Raw != entries[0].Raw {
		t.Fatalf("other entry changed: %#v, %v", after, err)
	}
	if got := readFileString(t, j.ReflectionsPath()+BackupSuffix); got != before {
		t.Fatal("backup did not preserve deleted entry")
	}
	if err := j.DeleteReflection(after[0].Date, entryRevision(after, after[0].Date)); err != nil {
		t.Fatal(err)
	}
	empty, err := j.Entries()
	if err != nil || len(empty) != 0 {
		t.Fatalf("last entry not deleted: %#v, %v", empty, err)
	}
}

func TestDeleteReflectionRejectsChangedOrMissingBase(t *testing.T) {
	j := newTestJournal(t, FS{}, 20, dayEntry("2026-08-10", "Original.", "", ""))
	draft, err := j.PrepareReflection("2026-08-10", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.SaveReflection(draft, writtenResult("Changed.", "", ""), true); err != nil {
		t.Fatal(err)
	}
	before := readFileString(t, j.ReflectionsPath())
	var stale *StaleReflectionError
	if err := j.DeleteReflection(draft.Date, draft.Base); !errors.As(err, &stale) {
		t.Fatalf("want stale refusal, got %v", err)
	}
	for _, base := range []Revision{"", RevisionAbsent} {
		if err := j.DeleteReflection(draft.Date, base); err == nil {
			t.Fatal("accepted missing base")
		}
	}
	if got := readFileString(t, j.ReflectionsPath()); got != before {
		t.Fatal("refused deletion changed the journal")
	}
}

func TestDeleteReflectionSyncsAndRefusesConflictedDays(t *testing.T) {
	a, b := pair(t)
	entries, err := a.journal.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.journal.DeleteReflection("2026-08-10", entryRevision(entries, "2026-08-10")); err != nil {
		t.Fatal(err)
	}
	settle(t, a, b)
	requireSameArtifacts(t, a, b)
	if _, found := b.artifacts()[entryArtifact("2026-08-10")]; found {
		t.Fatal("deleted day still on other device")
	}
	a, _ = conflicted(t)
	entries, err = a.journal.Entries()
	if err != nil {
		t.Fatal(err)
	}
	before := readFileString(t, a.reflections())
	if err := a.journal.DeleteReflection("2026-08-10", entryRevision(entries, "2026-08-10")); !errors.Is(err, ErrConflictedReflection) {
		t.Fatalf("want conflict refusal, got %v", err)
	}
	if got := readFileString(t, a.reflections()); got != before {
		t.Fatal("conflicted deletion changed the journal")
	}
}

func TestDeleteReflectionWriteFailurePreservesEntry(t *testing.T) {
	j := newTestJournal(t, FS{}, 20, dayEntry("2026-08-10", "Keep on failure.", "", ""))
	entries, err := j.Entries()
	if err != nil {
		t.Fatal(err)
	}
	before := readFileString(t, j.ReflectionsPath())
	j.fs.Rename = func(_, _ string) error { return injectedFailure }
	if err := j.DeleteReflection(entries[0].Date, entryRevision(entries, entries[0].Date)); err == nil {
		t.Fatal("expected write failure")
	}
	if got := readFileString(t, j.ReflectionsPath()); got != before {
		t.Fatal("failed delete changed the journal")
	}
}
