package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunMergeRejectsSameSourceAndDestinationFile(t *testing.T) {
	root := t.TempDir()
	reflections := filepath.Join(root, "reflections.md")
	original := "## 2026-05-26\n\n### What did I do badly today?\nRushed.\n\n### What did I do well?\nWalked.\n\n### What could I do differently tomorrow?\nSlow down.\n\n---\n\n"
	if err := os.WriteFile(reflections, []byte(original), 0644); err != nil {
		t.Fatalf("writing reflections file: %v", err)
	}
	t.Setenv("SENECA_FILE", reflections)

	err := runMerge(nil, []string{reflections})
	if err == nil || !strings.Contains(err.Error(), "source and destination must differ") {
		t.Fatalf("expected same-file error, got %v", err)
	}

	got, readErr := os.ReadFile(reflections)
	if readErr != nil {
		t.Fatalf("reading reflections file: %v", readErr)
	}
	if string(got) != original {
		t.Fatalf("expected reflections file to remain unchanged, got %q", string(got))
	}
}

func TestRunMergeDeleteCommitsSourceRemoval(t *testing.T) {
	root := t.TempDir()
	reflections := filepath.Join(root, "reflections.md")
	source := filepath.Join(root, "incoming.md")

	dstEntry := "## 2026-05-25\n\n### What did I do badly today?\nRushed.\n\n### What did I do well?\nWalked.\n\n### What could I do differently tomorrow?\nSlow down.\n"
	srcEntry := "## 2026-05-26\n\n### What did I do badly today?\nProcrastinated.\n\n### What did I do well?\nCooked.\n\n### What could I do differently tomorrow?\nStart earlier.\n"

	if err := os.WriteFile(reflections, []byte(dstEntry+"\n---\n\n"), 0644); err != nil {
		t.Fatalf("writing reflections file: %v", err)
	}
	if err := os.WriteFile(source, []byte(srcEntry+"\n---\n\n"), 0644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	runGit(t, root, "init")
	runGit(t, root, "config", "user.name", "Seneca Test")
	runGit(t, root, "config", "user.email", "seneca@example.com")
	runGit(t, root, "add", "reflections.md", "incoming.md")
	runGit(t, root, "commit", "-m", "seed reflections")

	t.Setenv("SENECA_FILE", reflections)
	previousDeleteSource := deleteSource
	deleteSource = true
	defer func() { deleteSource = previousDeleteSource }()

	if err := runMerge(nil, []string{source}); err != nil {
		t.Fatalf("runMerge returned error: %v", err)
	}

	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("expected source file to be deleted, got %v", err)
	}

	status := strings.TrimSpace(runGit(t, root, "status", "--short", "--untracked-files=no"))
	if status != "" {
		t.Fatalf("expected merge to leave no tracked changes behind, got status %q", status)
	}

	head := runGit(t, root, "show", "--name-status", "--format=%s", "HEAD")
	if !strings.Contains(head, "seneca: merge 1 entries") {
		t.Fatalf("expected merge commit message, got %q", head)
	}
	if !strings.Contains(head, "M\treflections.md") {
		t.Fatalf("expected destination file change in commit, got %q", head)
	}
	if !strings.Contains(head, "D\tincoming.md") {
		t.Fatalf("expected source deletion in commit, got %q", head)
	}
}

func TestRunMergeConflictLeavesBothFilesAndGitUntouched(t *testing.T) {
	root := t.TempDir()
	reflections := filepath.Join(root, "reflections.md")
	source := filepath.Join(root, "incoming.md")

	dstContent := "" +
		"## 2026-05-24\n\n### One?\nDestination 24.\n\n---\n\n" +
		"## 2026-05-26\n\n### One?\nDestination 26.\n\n---\n\n" +
		"## 2026-05-22\n\n### One?\nDestination 22.\n\n---\n\n"
	srcContent := "" +
		"## 2026-05-26\n\n### One?\nSource 26.\n\n---\n\n" +
		"## 2026-05-22\n\n### One?\nSource 22.\n\n---\n\n" +
		"## 2026-05-30\n\n### One?\nSource 30.\n\n---\n\n"

	if err := os.WriteFile(reflections, []byte(dstContent), 0644); err != nil {
		t.Fatalf("writing reflections file: %v", err)
	}
	if err := os.WriteFile(source, []byte(srcContent), 0644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	runGit(t, root, "init")
	runGit(t, root, "config", "user.name", "Seneca Test")
	runGit(t, root, "config", "user.email", "seneca@example.com")
	runGit(t, root, "add", "reflections.md", "incoming.md")
	runGit(t, root, "commit", "-m", "seed reflections")
	headBefore := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))

	t.Setenv("SENECA_FILE", reflections)
	previousDeleteSource := deleteSource
	deleteSource = true
	defer func() { deleteSource = previousDeleteSource }()

	err := runMerge(nil, []string{source})
	var conflict *engine.MergeConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a merge conflict error, got %v", err)
	}
	// Every overlapping date is reported, sorted, so the author can resolve
	// them all in one pass.
	if strings.Join(conflict.Dates, ",") != "2026-05-22,2026-05-26" {
		t.Fatalf("expected sorted conflicting dates, got %v", conflict.Dates)
	}

	if got := readFileString(t, reflections); got != dstContent {
		t.Fatalf("expected destination byte-identical, got %q", got)
	}
	if got := readFileString(t, source); got != srcContent {
		t.Fatalf("expected source byte-identical, got %q", got)
	}
	if _, statErr := os.Stat(reflections + engine.BackupSuffix); !os.IsNotExist(statErr) {
		t.Fatalf("expected no backup on a conflicted merge, got %v", statErr)
	}

	if status := strings.TrimSpace(runGit(t, root, "status", "--short")); status != "" {
		t.Fatalf("expected the git index and worktree untouched, got %q", status)
	}
	if headAfter := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD")); headAfter != headBefore {
		t.Fatalf("expected no commit, HEAD moved from %s to %s", headBefore, headAfter)
	}
}

func TestRunMergeRejectsDuplicateDatesWithinOneFile(t *testing.T) {
	duplicated := "" +
		"## 2026-05-26\n\n### One?\nFirst.\n\n---\n\n" +
		"## 2026-05-26\n\n### One?\nSecond.\n\n---\n\n"
	clean := "## 2026-05-24\n\n### One?\nClean.\n\n---\n\n"

	for _, tc := range []struct {
		name          string
		destination   string
		sourceContent string
	}{
		{"source", clean, duplicated},
		{"destination", duplicated, clean},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			reflections := filepath.Join(root, "reflections.md")
			source := filepath.Join(root, "incoming.md")
			if err := os.WriteFile(reflections, []byte(tc.destination), 0644); err != nil {
				t.Fatalf("writing reflections file: %v", err)
			}
			if err := os.WriteFile(source, []byte(tc.sourceContent), 0644); err != nil {
				t.Fatalf("writing source file: %v", err)
			}
			t.Setenv("SENECA_FILE", reflections)

			err := runMerge(nil, []string{source})
			var dupErr *engine.DuplicateDatesError
			if !errors.As(err, &dupErr) {
				t.Fatalf("expected duplicate-date refusal, got %v", err)
			}

			if got := readFileString(t, reflections); got != tc.destination {
				t.Fatalf("expected destination byte-identical, got %q", got)
			}
			if got := readFileString(t, source); got != tc.sourceContent {
				t.Fatalf("expected source byte-identical, got %q", got)
			}
			if _, statErr := os.Stat(reflections + engine.BackupSuffix); !os.IsNotExist(statErr) {
				t.Fatalf("expected no backup, got %v", statErr)
			}
		})
	}
}

func TestRunMergeKeepsSourceWhenDestinationWriteFails(t *testing.T) {
	root := t.TempDir()
	reflections := filepath.Join(root, "reflections.md")
	source := filepath.Join(root, "incoming.md")

	dstContent := "## 2026-05-25\n\n### One?\nDestination.\n\n---\n\n"
	srcContent := "## 2026-05-26\n\n### One?\nSource.\n\n---\n\n"
	if err := os.WriteFile(reflections, []byte(dstContent), 0644); err != nil {
		t.Fatalf("writing reflections file: %v", err)
	}
	if err := os.WriteFile(source, []byte(srcContent), 0644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}

	t.Setenv("SENECA_FILE", reflections)
	previousDeleteSource := deleteSource
	deleteSource = true
	defer func() { deleteSource = previousDeleteSource }()

	var err error
	withJournalFS(func(s *engine.FS) {
		s.Rename = func(oldpath, newpath string) error {
			if newpath == reflections {
				return injectedFailure
			}
			return os.Rename(oldpath, newpath)
		}
	}, func() {
		err = runMerge(nil, []string{source})
	})

	if !errors.Is(err, injectedFailure) {
		t.Fatalf("expected the write failure to surface, got %v", err)
	}
	if got := readFileString(t, reflections); got != dstContent {
		t.Fatalf("expected destination untouched, got %q", got)
	}
	// --delete must never outrun a successful destination replacement.
	if got := readFileString(t, source); got != srcContent {
		t.Fatalf("expected source to survive a failed merge, got %q", got)
	}
}

func TestParseAnswersMatchesExactQuestionHeadings(t *testing.T) {
	t.Parallel()

	questions := []string{"First custom question?", "Second custom question?"}
	raw := "" +
		"## 2026-05-26\n\n" +
		"### Second custom question?\n" +
		"Second answer.\n\n" +
		"### First custom question?\n" +
		"First answer.\n\n" +
		"### First custom question? extra\n" +
		"Ignored.\n"

	got := engine.NewReflectionEntry(raw).Answers(questions)

	if got[0] != "First answer." {
		t.Fatalf("expected first answer by exact heading, got %q", got[0])
	}
	if got[1] != "Second answer." {
		t.Fatalf("expected second answer by exact heading, got %q", got[1])
	}
}

func TestParseAnswersRequiresExactHeading(t *testing.T) {
	t.Parallel()

	questions := []string{"One?"}
	raw := "" +
		"## 2026-05-26\n\n" +
		"### One? extra\n" +
		"Wrong.\n\n" +
		"### One?\n" +
		"Right.\n"

	got := engine.NewReflectionEntry(raw).Answers(questions)

	if got[0] != "Right." {
		t.Fatalf("expected exact heading match only, got %q", got[0])
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
