package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failingSyncDirOnCall returns a syncDir override that fails the nth call and
// succeeds on every other one. A commit of an existing file syncs twice: once
// for the `.bak` and once for the primary replacement.
func failingSyncDirOnCall(n int) func(*engine.FS) {
	calls := 0
	return func(s *engine.FS) {
		sync := s.SyncDir
		s.SyncDir = func(dir *os.File) error {
			calls++
			if calls == n {
				return injectedFailure
			}
			return sync(dir)
		}
	}
}

func requireCommittedWrite(t *testing.T, err error, path string) {
	t.Helper()

	var committed *engine.CommittedWriteError
	if !errors.As(err, &committed) {
		t.Fatalf("expected a committed write failure, got %v", err)
	}
	if committed.Path != path {
		t.Fatalf("expected the committed failure to name %s, got %s", path, committed.Path)
	}
	if !errors.Is(err, injectedFailure) {
		t.Fatalf("expected the underlying cause to survive, got %v", err)
	}
	if !engine.WriteCommitted(err) {
		t.Fatal("expected writeCommitted to recognize the failure")
	}
	if !strings.Contains(err.Error(), "do not rerun") {
		t.Fatalf("expected the message to refuse a rerun, got %q", err.Error())
	}
}

// The directory sync is the one stage that runs after the rename, so it cannot
// join the all-stage table above: by the time it fails the destination already
// holds the new bytes. It gets its own test, which asserts the opposite
// guarantee — that the landed write is reported as landed.
func TestAtomicWriteDirectorySyncFailureHasUnambiguousCommitState(t *testing.T) {
	t.Run("new file", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "reflections.md")

		var err error
		withJournalFS(failingSyncDirOnCall(1), func() {
			err = journalFS.AtomicWriteFile(path, []byte(originalContent), engine.DefaultFileMode)
		})

		requireCommittedWrite(t, err, path)
		if got := readFileString(t, path); got != originalContent {
			t.Fatalf("expected the new file to exist with its contents, got %q", got)
		}
		if leftovers := tempFileNames(t, root); len(leftovers) != 0 {
			t.Fatalf("expected temporary files cleaned up, got %v", leftovers)
		}
	})

	t.Run("existing file", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "reflections.md")
		if err := os.WriteFile(path, []byte(originalContent), 0644); err != nil {
			t.Fatalf("seeding file: %v", err)
		}

		var err error
		withJournalFS(failingSyncDirOnCall(1), func() {
			err = journalFS.AtomicWriteFile(path, []byte("replacement"), engine.DefaultFileMode)
		})

		requireCommittedWrite(t, err, path)
		if got := readFileString(t, path); got != "replacement" {
			t.Fatalf("expected the replacement to be in place, got %q", got)
		}
	})

	// The backup is written before the primary, so a sync failure there is
	// write-free for the file being mutated and must not claim otherwise.
	t.Run("backup sync leaves the mutation write-free", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "reflections.md")
		if err := os.WriteFile(path, []byte(originalContent), 0644); err != nil {
			t.Fatalf("seeding file: %v", err)
		}
		// The transaction carries the filesystem it was opened with, so it is
		// opened under the fault.
		var commitErr error
		withJournalFS(failingSyncDirOnCall(1), func() {
			tx, err := journalFS.BeginFileTransaction(path)
			if err != nil {
				t.Fatalf("beginFileTransaction returned error: %v", err)
			}
			commitErr = tx.Commit([]byte("replacement"))
		})

		if commitErr == nil {
			t.Fatal("expected the backup sync failure to surface")
		}
		if engine.WriteCommitted(commitErr) {
			t.Fatalf("expected a write-free failure, got %v", commitErr)
		}
		if !errors.Is(commitErr, injectedFailure) {
			t.Fatalf("expected the underlying cause to survive, got %v", commitErr)
		}
		if got := readFileString(t, path); got != originalContent {
			t.Fatalf("expected the primary file untouched, got %q", got)
		}
		if got := readFileString(t, path+engine.BackupSuffix); got != originalContent {
			t.Fatalf("expected the backup to hold the pre-mutation bytes, got %q", got)
		}
	})

	t.Run("primary sync keeps the backup recoverable", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "reflections.md")
		if err := os.WriteFile(path, []byte(originalContent), 0644); err != nil {
			t.Fatalf("seeding file: %v", err)
		}
		// The transaction carries the filesystem it was opened with, so it is
		// opened under the fault.
		var commitErr error
		withJournalFS(failingSyncDirOnCall(2), func() {
			tx, err := journalFS.BeginFileTransaction(path)
			if err != nil {
				t.Fatalf("beginFileTransaction returned error: %v", err)
			}
			commitErr = tx.Commit([]byte("replacement"))
		})

		requireCommittedWrite(t, commitErr, path)
		if got := readFileString(t, path); got != "replacement" {
			t.Fatalf("expected the replacement to be in place, got %q", got)
		}
		if got := readFileString(t, path+engine.BackupSuffix); got != originalContent {
			t.Fatalf("expected the backup to hold the pre-mutation bytes, got %q", got)
		}
	})

	// An archive append is the case a blind retry would corrupt: a second run
	// would record the same event twice. The landed record must therefore be
	// reported and committed exactly as a success would have been.
	t.Run("archive append reports and commits the landed record", func(t *testing.T) {
		path := newArchivePath(t)
		mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "ship the archive"))
		before := readFileString(t, path)

		var out strings.Builder
		var err error
		log := withArchiveMutationLog(t, path, func() {
			withJournalFS(failingSyncDirOnCall(2), func() {
				err = setFocus(&out, path, archiveTime(25, 9, 0), []string{"finish the repair"})
			})
		})

		requireCommittedWrite(t, err, path)
		after := readFileString(t, path)
		if !strings.Contains(after, "finish the repair") {
			t.Fatalf("expected the focus event to be in the archive, got %q", after)
		}
		if !strings.Contains(after, before) {
			t.Fatal("expected the append to preserve the existing archive")
		}
		if log.replacements != 1 {
			t.Fatalf("expected exactly one archive replacement, got %d", log.replacements)
		}
		if len(log.commits) != 1 || !strings.Contains(log.commits[0], "focus set 2026-08-25") {
			t.Fatalf("expected the landed record to be committed once, got %v", log.commits)
		}
		if !strings.Contains(out.String(), "Focus set for 2026-08-25") {
			t.Fatalf("expected the landed record to be reported, got %q", out.String())
		}
		if got := readFileString(t, path+engine.BackupSuffix); got != before {
			t.Fatalf("expected the backup to hold the pre-append archive, got %q", got)
		}
	})

	t.Run("archive append backup sync writes and commits nothing", func(t *testing.T) {
		path := newArchivePath(t)
		mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "ship the archive"))
		before := readFileString(t, path)

		var out strings.Builder
		var err error
		log := withArchiveMutationLog(t, path, func() {
			withJournalFS(failingSyncDirOnCall(1), func() {
				err = setFocus(&out, path, archiveTime(25, 9, 0), []string{"finish the repair"})
			})
		})

		if err == nil || engine.WriteCommitted(err) {
			t.Fatalf("expected a write-free failure, got %v", err)
		}
		if got := readFileString(t, path); got != before {
			t.Fatalf("expected the archive untouched, got %q", got)
		}
		if log.replacements != 0 || len(log.commits) != 0 {
			t.Fatalf("expected no replacement and no commit, got %d and %v", log.replacements, log.commits)
		}
		if out.String() != "" {
			t.Fatalf("expected nothing reported, got %q", out.String())
		}
	})
}

func TestAtomicWriteFileFailureAtEveryStageLeavesOriginalIntact(t *testing.T) {
	stages := []struct {
		name   string
		mutate func(*engine.FS)
	}{
		{"write", func(s *engine.FS) {
			s.WriteFile = func(*os.File, []byte) (int, error) { return 0, injectedFailure }
		}},
		{"short-write", func(s *engine.FS) {
			s.WriteFile = func(f *os.File, data []byte) (int, error) { return f.Write(data[:len(data)/2]) }
		}},
		{"sync", func(s *engine.FS) {
			s.SyncFile = func(*os.File) error { return injectedFailure }
		}},
		{"close", func(s *engine.FS) {
			s.CloseFile = func(f *os.File) error {
				_ = f.Close()
				return injectedFailure
			}
		}},
		{"rename", func(s *engine.FS) {
			s.Rename = func(string, string) error { return injectedFailure }
		}},
	}

	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "reflections.md")
			if err := os.WriteFile(path, []byte(originalContent), 0644); err != nil {
				t.Fatalf("seeding file: %v", err)
			}

			var err error
			withJournalFS(stage.mutate, func() {
				err = journalFS.AtomicWriteFile(path, []byte("replacement"), engine.DefaultFileMode)
			})
			if err == nil {
				t.Fatalf("expected %s failure to surface", stage.name)
			}

			if got := readFileString(t, path); got != originalContent {
				t.Fatalf("expected original bytes after %s failure, got %q", stage.name, got)
			}
			if leftovers := tempFileNames(t, root); len(leftovers) != 0 {
				t.Fatalf("expected temporary files cleaned up after %s failure, got %v", stage.name, leftovers)
			}
		})
	}
}

func TestAtomicWriteFileCreatesNewFileWithDefaultMode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "reflections.md")

	if err := journalFS.AtomicWriteFile(path, []byte(originalContent), engine.DefaultFileMode); err != nil {
		t.Fatalf("atomicWriteFile returned error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat new file: %v", err)
	}
	if info.Mode().Perm() != engine.DefaultFileMode {
		t.Fatalf("expected new file mode %v, got %v", engine.DefaultFileMode, info.Mode().Perm())
	}
	if got := readFileString(t, path); got != originalContent {
		t.Fatalf("unexpected new file content %q", got)
	}
	if leftovers := tempFileNames(t, root); len(leftovers) != 0 {
		t.Fatalf("expected no temporary files, got %v", leftovers)
	}
}

func TestAtomicWriteFilePreservesExistingPermissions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "reflections.md")
	if err := os.WriteFile(path, []byte(originalContent), 0600); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	// A caller asking for 0644 must not widen a file the author locked down.
	if err := journalFS.AtomicWriteFile(path, []byte("replacement"), engine.DefaultFileMode); err != nil {
		t.Fatalf("atomicWriteFile returned error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat replaced file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected preserved mode 0600, got %v", info.Mode().Perm())
	}
	if got := readFileString(t, path); got != "replacement" {
		t.Fatalf("unexpected replaced content %q", got)
	}
}

func TestAtomicWriteFileRefusesSymlinkDestination(t *testing.T) {
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

	err := journalFS.AtomicWriteFile(link, []byte("replacement"), engine.DefaultFileMode)
	var symlinkErr *engine.SymlinkDestinationError
	if !errors.As(err, &symlinkErr) {
		t.Fatalf("expected symlink refusal, got %v", err)
	}

	if got := readFileString(t, target); got != originalContent {
		t.Fatalf("expected symlink target untouched, got %q", got)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected the symlink itself to survive")
	}
}

// withJournalFS installs a mutated filesystem for the commands run inside fn
// and restores the previous one afterwards, even when fn panics. The engine
// itself holds no global: this swaps only what the commands hand to it.
func withJournalFS(mutate func(*engine.FS), fn func()) {
	previous := journalFS
	defer func() { journalFS = previous }()
	mutate(&journalFS)
	fn()
}
