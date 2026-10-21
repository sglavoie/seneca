package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedSortRepo(t *testing.T, content string) (root, path string) {
	t.Helper()

	root = t.TempDir()
	path = filepath.Join(root, "reflections.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing reflections file: %v", err)
	}

	runGit(t, root, "init")
	runGit(t, root, "config", "user.name", "Seneca Test")
	runGit(t, root, "config", "user.email", "seneca@example.com")
	runGit(t, root, "add", "reflections.md")
	runGit(t, root, "commit", "-m", "seed reflections")

	t.Setenv("SENECA_FILE", path)
	return root, path
}

func TestRunSortRewritesOutOfOrderEntries(t *testing.T) {
	unsorted := "" +
		"## 2026-05-26\n\n### One?\nLater.\n\n---\n\n" +
		"## 2026-05-24\n\n### One?\nEarlier.\n\n---\n\n"
	sorted := "" +
		"## 2026-05-24\n\n### One?\nEarlier.\n---\n\n" +
		"## 2026-05-26\n\n### One?\nLater.\n---\n\n"

	root, path := seedSortRepo(t, unsorted)

	if err := runSort(nil, nil); err != nil {
		t.Fatalf("runSort returned error: %v", err)
	}

	if got := readFileString(t, path); got != sorted {
		t.Fatalf("unexpected sorted content %q", got)
	}
	if got := readFileString(t, path+engine.BackupSuffix); got != unsorted {
		t.Fatalf("expected the backup to hold the pre-sort bytes, got %q", got)
	}
	head := runGit(t, root, "show", "--name-status", "--format=%s", "HEAD")
	if !strings.Contains(head, "seneca: sort 2 entries") {
		t.Fatalf("expected a sort commit, got %q", head)
	}
}

func TestRunSortWithNoChangeSkipsBackupWriteAndCommit(t *testing.T) {
	alreadySorted := "" +
		"## 2026-05-24\n\n### One?\nEarlier.\n---\n\n" +
		"## 2026-05-26\n\n### One?\nLater.\n---\n\n"

	root, path := seedSortRepo(t, alreadySorted)
	headBefore := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))

	if err := runSort(nil, nil); err != nil {
		t.Fatalf("runSort returned error: %v", err)
	}

	if got := readFileString(t, path); got != alreadySorted {
		t.Fatalf("expected file untouched, got %q", got)
	}
	if _, err := os.Stat(path + engine.BackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected no backup for a no-op sort, got %v", err)
	}
	if headAfter := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD")); headAfter != headBefore {
		t.Fatalf("expected no commit, HEAD moved from %s to %s", headBefore, headAfter)
	}
}

func TestRunSortRefusesDuplicateDates(t *testing.T) {
	duplicated := "" +
		"## 2026-05-26\n\n### One?\nFirst.\n\n---\n\n" +
		"## 2026-05-26\n\n### One?\nSecond.\n\n---\n\n"

	_, path := seedSortRepo(t, duplicated)

	err := runSort(nil, nil)
	var dupErr *engine.DuplicateDatesError
	if !errors.As(err, &dupErr) {
		t.Fatalf("expected duplicate-date refusal, got %v", err)
	}
	if got := readFileString(t, path); got != duplicated {
		t.Fatalf("expected file untouched, got %q", got)
	}
	if _, statErr := os.Stat(path + engine.BackupSuffix); !os.IsNotExist(statErr) {
		t.Fatalf("expected no backup, got %v", statErr)
	}
}

func TestRunSortLeavesFileIntactWhenRenameFails(t *testing.T) {
	unsorted := "" +
		"## 2026-05-26\n\n### One?\nLater.\n\n---\n\n" +
		"## 2026-05-24\n\n### One?\nEarlier.\n\n---\n\n"

	root, path := seedSortRepo(t, unsorted)
	headBefore := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))

	var err error
	withJournalFS(func(s *engine.FS) {
		s.Rename = func(oldpath, newpath string) error {
			if newpath == path {
				return injectedFailure
			}
			return os.Rename(oldpath, newpath)
		}
	}, func() {
		err = runSort(nil, nil)
	})

	if !errors.Is(err, injectedFailure) {
		t.Fatalf("expected the rename failure to surface, got %v", err)
	}
	if got := readFileString(t, path); got != unsorted {
		t.Fatalf("expected file untouched, got %q", got)
	}
	if headAfter := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD")); headAfter != headBefore {
		t.Fatalf("expected no commit, HEAD moved from %s to %s", headBefore, headAfter)
	}
	if leftovers := tempFileNames(t, root); len(leftovers) != 0 {
		t.Fatalf("expected temporary files cleaned up, got %v", leftovers)
	}
}
