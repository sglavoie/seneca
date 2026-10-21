package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestFileTransactionCommitWritesBackupBeforeReplacing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "reflections.md")
	if err := os.WriteFile(path, []byte(originalContent), 0640); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	tx, err := FS{}.BeginFileTransaction(path)
	if err != nil {
		t.Fatalf("beginFileTransaction returned error: %v", err)
	}
	if err := tx.Commit([]byte("replacement")); err != nil {
		t.Fatalf("commit returned error: %v", err)
	}

	if got := readFileString(t, path); got != "replacement" {
		t.Fatalf("unexpected primary content %q", got)
	}
	if got := readFileString(t, path+BackupSuffix); got != originalContent {
		t.Fatalf("expected backup to hold the pre-mutation bytes, got %q", got)
	}
	info, err := os.Stat(path + BackupSuffix)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("expected backup to inherit source mode 0640, got %v", info.Mode().Perm())
	}
}

func TestFileTransactionCommitBackupFailureLeavesPrimaryIntact(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "reflections.md")
	if err := os.WriteFile(path, []byte(originalContent), 0644); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	fs := FS{Backup: func(string, []byte, os.FileMode) error { return injectedFailure }}
	tx, err := fs.BeginFileTransaction(path)
	if err != nil {
		t.Fatalf("beginFileTransaction returned error: %v", err)
	}

	commitErr := tx.Commit([]byte("replacement"))

	if !errors.Is(commitErr, injectedFailure) {
		t.Fatalf("expected the backup failure to surface, got %v", commitErr)
	}
	if got := readFileString(t, path); got != originalContent {
		t.Fatalf("expected primary file untouched, got %q", got)
	}
	if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected no backup file, got %v", err)
	}
	if leftovers := tempFileNames(t, root); len(leftovers) != 0 {
		t.Fatalf("expected no temporary files, got %v", leftovers)
	}
}

func TestFileTransactionCommitRenameFailureKeepsBackupAndOriginal(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "reflections.md")
	if err := os.WriteFile(path, []byte(originalContent), 0644); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	// Only the primary replacement fails; the backup must still land.
	fs := FS{Rename: func(oldpath, newpath string) error {
		if newpath == path {
			return injectedFailure
		}
		return os.Rename(oldpath, newpath)
	}}
	tx, err := fs.BeginFileTransaction(path)
	if err != nil {
		t.Fatalf("beginFileTransaction returned error: %v", err)
	}

	commitErr := tx.Commit([]byte("replacement"))

	if !errors.Is(commitErr, injectedFailure) {
		t.Fatalf("expected the rename failure to surface, got %v", commitErr)
	}
	if got := readFileString(t, path); got != originalContent {
		t.Fatalf("expected primary file untouched, got %q", got)
	}
	if got := readFileString(t, path+BackupSuffix); got != originalContent {
		t.Fatalf("expected a recoverable backup, got %q", got)
	}
	if leftovers := tempFileNames(t, root); len(leftovers) != 0 {
		t.Fatalf("expected temporary files cleaned up, got %v", leftovers)
	}
}

func TestFileTransactionCommitSkipsBackupForNewFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "reflections.md")

	tx, err := FS{}.BeginFileTransaction(path)
	if err != nil {
		t.Fatalf("beginFileTransaction returned error: %v", err)
	}
	if tx.exists {
		t.Fatal("expected new-file mode")
	}
	if err := tx.Commit([]byte(originalContent)); err != nil {
		t.Fatalf("commit returned error: %v", err)
	}

	if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected no backup for a newly created file, got %v", err)
	}
}

func TestBeginFileTransactionRefusesSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "real.md")
	link := filepath.Join(root, "link.md")
	if err := os.WriteFile(target, []byte(originalContent), 0644); err != nil {
		t.Fatalf("seeding target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	_, err := FS{}.BeginFileTransaction(link)
	var symlinkErr *SymlinkDestinationError
	if !errors.As(err, &symlinkErr) {
		t.Fatalf("expected symlink refusal, got %v", err)
	}
}

func TestOpenReflectionFileValidateRejectsDuplicateDates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "reflections.md")
	content := "" +
		"## 2026-05-26\n\n### One?\nA.\n\n---\n\n" +
		"## 2026-05-25\n\n### One?\nB.\n\n---\n\n" +
		"## 2026-05-26\n\n### One?\nC.\n\n---\n\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	tx, err := FS{}.OpenReflectionFile(path)
	if err != nil {
		t.Fatalf("openReflectionFile returned error: %v", err)
	}

	var dupErr *DuplicateDatesError
	if err := tx.Validate(); !errors.As(err, &dupErr) {
		t.Fatalf("expected duplicate-date refusal, got %v", err)
	}
	if len(dupErr.dates) != 1 || dupErr.dates[0] != "2026-05-26" {
		t.Fatalf("expected the repeated date reported, got %v", dupErr.dates)
	}
}

func TestOpenReflectionFileValidateRejectsUndatableEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "reflections.md")
	content := "" +
		"## 2026-05-26\n\n### One?\nA.\n\n---\n\n" +
		"## not a date\n\n### One?\nB.\n\n---\n\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	tx, err := FS{}.OpenReflectionFile(path)
	if err != nil {
		t.Fatalf("openReflectionFile returned error: %v", err)
	}

	var invalidErr *InvalidDocumentError
	if err := tx.Validate(); !errors.As(err, &invalidErr) {
		t.Fatalf("expected undatable-entry refusal, got %v", err)
	}
}

func TestCommitEntriesRejectsBeforeTouchingDisk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "reflections.md")
	if err := os.WriteFile(path, []byte(originalContent+"\n---\n\n"), 0644); err != nil {
		t.Fatalf("seeding file: %v", err)
	}
	before := readFileString(t, path)

	tx, err := FS{}.OpenReflectionFile(path)
	if err != nil {
		t.Fatalf("openReflectionFile returned error: %v", err)
	}

	// Two entries for the same day: validation must stop the write.
	entries := append(tx.Entries(), tx.Entries()[0])
	var dupErr *DuplicateDatesError
	if err := tx.CommitEntries(entries); !errors.As(err, &dupErr) {
		t.Fatalf("expected duplicate-date refusal, got %v", err)
	}

	if got := readFileString(t, path); got != before {
		t.Fatalf("expected file untouched after validation failure, got %q", got)
	}
	if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected no backup after validation failure, got %v", err)
	}
}

func TestConflictingEntryDatesReportsSortedIntersection(t *testing.T) {
	t.Parallel()

	left := []ReflectionEntry{
		NewReflectionEntry("## 2026-05-24\n\n### One?\nA.\n"),
		NewReflectionEntry("## 2026-05-26\n\n### One?\nB.\n"),
		NewReflectionEntry("## 2026-05-22\n\n### One?\nC.\n"),
	}
	right := []ReflectionEntry{
		NewReflectionEntry("## 2026-05-26\n\n### One?\nD.\n"),
		NewReflectionEntry("## 2026-05-22\n\n### One?\nE.\n"),
		NewReflectionEntry("## 2026-05-30\n\n### One?\nF.\n"),
	}

	got := ConflictingEntryDates(left, right)
	want := []string{"2026-05-22", "2026-05-26"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("expected sorted conflicts %v, got %v", want, got)
	}
}
