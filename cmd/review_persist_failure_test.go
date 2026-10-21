package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNonInteractiveReviewOnlyPrints(t *testing.T) {
	path := filepath.Join(t.TempDir(), engine.ArchiveFileName)
	fake := &fakeReview{tty: false, response: reviewPayload("null")}

	log := withArchiveMutationLog(t, path, func() {
		if err := executeReview(fake.runtime(archiveTime(24, 20, 0)), reviewConfig{Harness: harnessClaude}, path, savedWindow(t)); err != nil {
			t.Fatalf("executeReview returned error: %v", err)
		}
	})

	if fake.decisions != 0 {
		t.Fatal("expected no decision prompt without a terminal")
	}
	if log.replacements != 0 || len(log.commits) != 0 {
		t.Fatalf("expected no writes and no commits, got %d writes and %v", log.replacements, log.commits)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no archive to be created at %s, got %v", path, err)
	}

	rendered := fake.out.String()
	if !strings.Contains(rendered, "Previous focus\n- No focus was in effect for this period.") {
		t.Fatalf("expected the plain render to open with the focus block, got:\n%s", rendered)
	}
	if strings.Index(rendered, "Previous focus") > strings.Index(rendered, "Recurring struggles") {
		t.Fatal("expected the previous focus to render before recurring struggles")
	}
	if !strings.Contains(rendered, savedFocusRecommendation) {
		t.Fatalf("expected the recommendation in the plain render, got:\n%s", rendered)
	}
}

func TestFailedReviewPathsWriteNothing(t *testing.T) {
	assessment := `{"summary":"You mostly held the focus.","evidence":[]}`

	cases := []struct {
		name    string
		mutate  func(*fakeReview)
		wantErr string
	}{
		{
			name:    "harness failure",
			mutate:  func(f *fakeReview) { f.harnessErr = errors.New("claude exploded") },
			wantErr: "claude exploded",
		},
		{
			name:    "unparseable response",
			mutate:  func(f *fakeReview) { f.response = "not json at all" },
			wantErr: "parsing claude review response",
		},
		{
			name: "evidence the harness never saw",
			mutate: func(f *fakeReview) {
				f.response = strings.ReplaceAll(reviewPayload(assessment), "2026-08-20", "2026-01-01")
			},
			wantErr: "not one of the supplied reflection dates",
		},
		{
			name:    "cancelled decision",
			mutate:  func(f *fakeReview) { f.decideErr = errors.New("terminal exploded") },
			wantErr: "terminal exploded",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := archiveWithFocus(t, 18, "Ship the archive layer.")
			before := readFileString(t, path)

			fake := &fakeReview{tty: true, response: reviewPayload(assessment), decision: reviewDecision{kind: reviewAcceptFocus, focusText: "x"}}
			tc.mutate(fake)

			var err error
			log := withArchiveMutationLog(t, path, func() {
				err = executeReview(fake.runtime(archiveTime(24, 20, 0)), reviewConfig{Harness: harnessClaude}, path, savedWindow(t))
			})

			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected an error containing %q, got %v", tc.wantErr, err)
			}
			if log.replacements != 0 || len(log.commits) != 0 {
				t.Fatalf("expected no writes and no commits, got %d writes and %v", log.replacements, log.commits)
			}
			if readFileString(t, path) != before {
				t.Fatal("expected the archive to be byte-identical after a failure")
			}
		})
	}
}

func TestAFailedArchiveWriteLeavesTheArchiveUntouchedAndUncommitted(t *testing.T) {
	path := archiveWithFocus(t, 18, "Ship the archive layer.")
	before := readFileString(t, path)

	fake := &fakeReview{
		tty:      true,
		response: reviewPayload(`{"summary":"You mostly held the focus.","evidence":[]}`),
		decision: reviewDecision{kind: reviewAcceptFocus, focusText: savedFocusRecommendation},
	}

	var err error
	var commits []string
	previous := commitArchivePaths
	defer func() { commitArchivePaths = previous }()
	commitArchivePaths = func(message string, paths ...string) { commits = append(commits, message) }

	withJournalFS(func(s *engine.FS) {
		s.Backup = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	}, func() {
		err = executeReview(fake.runtime(archiveTime(24, 20, 0)), reviewConfig{Harness: harnessClaude}, path, savedWindow(t))
	})

	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected the write failure to surface, got %v", err)
	}
	if len(commits) != 0 {
		t.Fatalf("expected no commit after a failed write, got %v", commits)
	}
	if readFileString(t, path) != before {
		t.Fatal("expected the archive to be byte-identical after a failed write")
	}
	if _, err := os.Stat(path + engine.BackupSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no backup after a failed write, got %v", err)
	}
}

func TestReviewRefusesToAppendToAForeignArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), engine.ArchiveFileName)
	if err := os.WriteFile(path, []byte("# Someone else's notes\n"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	before := readFileString(t, path)

	fake := &fakeReview{
		tty:      true,
		response: reviewPayload("null"),
		decision: reviewDecision{kind: reviewAcceptFocus, focusText: savedFocusRecommendation},
	}

	var err error
	log := withArchiveMutationLog(t, path, func() {
		err = executeReview(fake.runtime(archiveTime(24, 20, 0)), reviewConfig{Harness: harnessClaude}, path, savedWindow(t))
	})

	var foreign *engine.ForeignArchiveError
	if !errors.As(err, &foreign) {
		t.Fatalf("expected a foreign-archive refusal, got %v", err)
	}
	if log.replacements != 0 || len(log.commits) != 0 {
		t.Fatalf("expected no writes and no commits, got %d writes and %v", log.replacements, log.commits)
	}
	if readFileString(t, path) != before {
		t.Fatal("expected the foreign file to be byte-identical")
	}
}

func TestReviewWithoutEntriesNeverReachesTheHarness(t *testing.T) {
	path := filepath.Join(t.TempDir(), engine.ArchiveFileName)
	fake := &fakeReview{tty: true, response: reviewPayload("null")}
	window := testReviewWindow(t, "2026-08-17", "2026-08-23")

	if err := executeReview(fake.runtime(archiveTime(24, 20, 0)), reviewConfig{Harness: harnessClaude}, path, window); err != nil {
		t.Fatalf("executeReview returned error: %v", err)
	}
	if len(fake.prompts) != 0 || fake.decisions != 0 {
		t.Fatal("expected an empty window to short-circuit before the harness and the prompt")
	}
	if !strings.Contains(fake.out.String(), "No entries found for weekly review (2026-08-17 to 2026-08-23).") {
		t.Fatalf("unexpected output: %q", fake.out.String())
	}
}
