package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func gitCommit(filePath, message string) {
	gitCommitPaths(message, filePath)
}

// gitStage names the step of a commit that failed.
type gitStage string

const (
	gitStageRepo   gitStage = "repo"
	gitStageAdd    gitStage = "add"
	gitStageDiff   gitStage = "diff"
	gitStageCommit gitStage = "commit"
)

// gitError is a commit that did not happen. The files it was for are already
// written: it is never a reason to undo or redo them.
type gitError struct {
	stage  gitStage
	path   string
	detail string
}

func (e *gitError) Error() string {
	if e.stage == gitStageRepo {
		return fmt.Sprintf("%s is not in a git repository", e.path)
	}
	return fmt.Sprintf("git %s failed: %s", e.stage, strings.TrimSpace(e.detail))
}

// warning is the line the commands have always printed for the failure.
func (e *gitError) warning() string {
	switch e.stage {
	case gitStageRepo:
		return fmt.Sprintf("warning: %s is not in a git repository, skipping commit\n", e.path)
	case gitStageDiff:
		return fmt.Sprintf("warning: git diff failed: %s\n", e.detail)
	default:
		return fmt.Sprintf("warning: git %s failed: %s\n", e.stage, e.detail)
	}
}

func gitCommitPaths(message string, filePaths ...string) {
	committed, err := commitPaths(message, filePaths...)
	var failure *gitError
	if errors.As(err, &failure) {
		fmt.Fprint(os.Stderr, failure.warning())
		return
	}
	if committed {
		fmt.Printf("Committed: %s\n", message)
	}
}

// commitPaths stages and commits exactly filePaths, deletions included, and
// reports whether a commit was made. Whatever else is staged stays staged and
// is not part of the commit. Nothing is committed, with no error, when the
// paths hold nothing new.
func commitPaths(message string, filePaths ...string) (bool, error) {
	if len(filePaths) == 0 {
		return false, nil
	}

	dir := filepath.Dir(filePaths[0])

	// Check if in a git repo
	check := exec.Command("git", "-C", dir, "rev-parse", "--git-dir")
	if err := check.Run(); err != nil {
		return false, &gitError{stage: gitStageRepo, path: filePaths[0]}
	}

	// Stage only our paths, including deletions.
	addArgs := []string{"-C", dir, "add", "-A", "--"}
	addArgs = append(addArgs, filePaths...)
	if out, err := exec.Command("git", addArgs...).CombinedOutput(); err != nil {
		return false, &gitError{stage: gitStageAdd, detail: string(out)}
	}

	// Check if there are staged changes for these paths.
	diffArgs := []string{"-C", dir, "diff", "--cached", "--quiet", "--"}
	diffArgs = append(diffArgs, filePaths...)
	diff := exec.Command("git", diffArgs...)
	if err := diff.Run(); err == nil {
		return false, nil
	} else {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return false, &gitError{stage: gitStageDiff, detail: err.Error()}
		}
	}

	// Commit only our paths.
	commitArgs := []string{"-C", dir, "commit", "-m", message, "--"}
	commitArgs = append(commitArgs, filePaths...)
	if out, err := exec.Command("git", commitArgs...).CombinedOutput(); err != nil {
		return false, &gitError{stage: gitStageCommit, detail: string(out)}
	}
	return true, nil
}

// gitActivityMarkers are the entries of a repository's git directory that
// exist while an operation that rewrites the working tree is under way.
var gitActivityMarkers = []struct{ name, activity string }{
	{"MERGE_HEAD", "a merge"},
	{"rebase-merge", "a rebase"},
	{"rebase-apply", "a rebase"},
	{"CHERRY_PICK_HEAD", "a cherry-pick"},
	{"REVERT_HEAD", "a revert"},
	{"BISECT_LOG", "a bisect"},
}

// gitBusy says why the repository holding paths is not safe to update right
// now: an operation is rewriting the working tree, or one of the paths is
// unmerged. It is empty for a quiet repository and outside one. It only reads.
func gitBusy(paths ...string) string {
	if len(paths) == 0 {
		return ""
	}
	dir := filepath.Dir(paths[0])
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return ""
	}
	gitDir := strings.TrimSpace(string(out))
	for _, marker := range gitActivityMarkers {
		if _, err := os.Stat(filepath.Join(gitDir, marker.name)); err == nil {
			return marker.activity + " is in progress in the repository"
		}
	}

	args := append([]string{"-C", dir, "ls-files", "--unmerged", "--"}, paths...)
	unmerged, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	if strings.TrimSpace(string(unmerged)) != "" {
		return "a journal file has an unresolved merge in the repository"
	}
	return ""
}

// gitKnows reports whether a commit naming path can succeed: the file exists
// and is not ignored, or it is gone and Git tracks it. Naming anything else
// makes `git add` fail for every path beside it.
func gitKnows(path string) bool {
	dir := filepath.Dir(path)
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
	if _, err := os.Lstat(path); err == nil {
		return exec.Command("git", "-C", dir, "check-ignore", "--quiet", "--", path).Run() != nil
	}
	return exec.Command("git", "-C", dir, "ls-files", "--error-unmatch", "--", path).Run() == nil
}
