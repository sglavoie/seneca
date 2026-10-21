package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewReviewRecordRevisesTheLatestMatchingWindow(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)

	first := mustOpenArchive(t, path)
	original := first.Archive.NewReviewRecord(archiveTime(24, 21, 0), engine.ReviewPeriodWeekly, "2026-08-17", "2026-08-23", "", "First pass.")
	if original.Review.Revises != "" {
		t.Fatalf("expected a first save to revise nothing, got %s", original.Review.Revises)
	}
	if err := first.AppendRecords(original); err != nil {
		t.Fatalf("appending the first review: %v", err)
	}

	// A different window must not be treated as a revision of this one.
	second := mustOpenArchive(t, path)
	other := second.Archive.NewReviewRecord(archiveTime(24, 21, 1), engine.ReviewPeriodMonthly, "2026-08-01", "2026-08-31", "", "Monthly pass.")
	if other.Review.Revises != "" {
		t.Fatalf("expected a different window to revise nothing, got %s", other.Review.Revises)
	}
	if err := second.AppendRecords(other); err != nil {
		t.Fatalf("appending the monthly review: %v", err)
	}

	third := mustOpenArchive(t, path)
	revision := third.Archive.NewReviewRecord(archiveTime(25, 21, 0), engine.ReviewPeriodWeekly, "2026-08-17", "2026-08-23", "", "Second pass.")
	if revision.Review.Revises != original.Review.ID {
		t.Fatalf("expected the revision to link to %s, got %s", original.Review.ID, revision.Review.Revises)
	}
	if err := third.AppendRecords(revision); err != nil {
		t.Fatalf("appending the revision: %v", err)
	}

	// A revision written by Seneca itself must satisfy Seneca's own validation.
	if err := mustOpenArchive(t, path).Validate(); err != nil {
		t.Fatalf("expected the archive Seneca wrote to validate, got %v", err)
	}
}

func TestArchiveRecordIDsAreStableAndContentSensitive(t *testing.T) {
	t.Parallel()

	now := archiveTime(24, 21, 0)
	base := engine.NewFocusSetRecord(now, engine.FocusOriginManual, "", "Protect the first hour.")
	same := engine.NewFocusSetRecord(now, engine.FocusOriginManual, "", "Protect the first hour.")
	if base.Focus.ID != same.Focus.ID {
		t.Fatal("expected identical inputs to produce the same record id")
	}
	if len(base.Focus.ID) != 64 {
		t.Fatalf("expected a SHA-256 hex id, got %q", base.Focus.ID)
	}

	different := []engine.ArchiveRecord{
		engine.NewFocusSetRecord(now, engine.FocusOriginManual, "", "Protect the second hour."),
		engine.NewFocusSetRecord(archiveTime(24, 21, 1), engine.FocusOriginManual, "", "Protect the first hour."),
		engine.NewFocusSetRecord(now, engine.FocusOriginReview, strings.Repeat("a", 64), "Protect the first hour."),
	}
	for i, record := range different {
		if record.Focus.ID == base.Focus.ID {
			t.Fatalf("expected variant %d to produce a different record id", i)
		}
	}
}

func TestArchiveTransactionInheritsPersistenceSafety(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "real.md")
	link := filepath.Join(root, engine.ArchiveFileName)
	if err := os.WriteFile(target, []byte("# Seneca Review Archive\n<!-- seneca:archive v=2 -->\n"), 0644); err != nil {
		t.Fatalf("seeding target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	_, err := journalFS.OpenReviewArchive(link)
	var symlinkErr *engine.SymlinkDestinationError
	if !errors.As(err, &symlinkErr) {
		t.Fatalf("expected symlink refusal, got %v", err)
	}
}

func TestArchiveAppendBacksUpThePreviousContent(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "First."))
	firstContent := readFileString(t, path)

	if _, err := os.Stat(path + engine.BackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected no backup when the archive is created, got %v", err)
	}

	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(25, 9, 0), engine.FocusOriginManual, "", "Second."))
	if got := readFileString(t, path+engine.BackupSuffix); got != firstContent {
		t.Fatalf("expected the backup to hold the pre-append bytes, got:\n%s", got)
	}
}

// TestArchiveRefusesRecordIDContentMismatch pins the one guarantee doctor and
// the writing commands have to agree on: a record whose id no longer describes
// its content was edited outside seneca, and no append may build on it. The
// refusal has to land before the backup, the replacement, and the commit, so a
// hand-edited archive is byte-identical afterwards.
func TestArchiveRefusesRecordIDContentMismatch(t *testing.T) {
	// withArchiveMutationLog installs process-global seams: no t.Parallel here.
	cases := []struct {
		name string
		seed func(t *testing.T, path string)
		edit func(string) string
		want string
	}{
		{
			name: "an edited review body",
			seed: func(t *testing.T, path string) {
				t.Helper()

				tx := mustOpenArchive(t, path)
				review := tx.Archive.NewReviewRecord(archiveTime(24, 21, 4), engine.ReviewPeriodWeekly,
					"2026-08-17", "2026-08-23", "Weekly review",
					"### Recurring struggles\n- Rushing.\n\n### Next focus\nProtect the first hour.")
				if err := tx.AppendRecords(review); err != nil {
					t.Fatalf("seeding the review returned error: %v", err)
				}
			},
			edit: func(content string) string {
				return strings.Replace(content, "- Rushing.", "- Rushing, but only on Tuesdays.", 1)
			},
			want: "does not match its own content",
		},
		{
			name: "an edited focus text",
			seed: func(t *testing.T, path string) {
				t.Helper()
				mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Ship the archive layer."))
			},
			edit: func(content string) string {
				return strings.Replace(content, "Ship the archive layer.", "Ship the whole product.", 1)
			},
			want: "does not match its own content",
		},
		{
			name: "an id seneca could not have derived",
			seed: func(t *testing.T, path string) {
				t.Helper()
				mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Ship the archive layer."))
			},
			edit: func(content string) string {
				return archiveHexRe.ReplaceAllString(content, "handwritten")
			},
			want: "is not a sha-256 digest",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := newArchivePath(t)
			tc.seed(t, path)

			edited := tc.edit(readFileString(t, path))
			if edited == readFileString(t, path) {
				t.Fatal("the fixture edit changed nothing; the test would prove nothing")
			}
			if err := os.WriteFile(path, []byte(edited), 0644); err != nil {
				t.Fatalf("writing the hand-edited archive: %v", err)
			}
			if _, err := os.Stat(path + engine.BackupSuffix); !os.IsNotExist(err) {
				t.Fatalf("expected the fixture to leave no backup behind, got %v", err)
			}

			// Reading stays lossless and tolerant: an id is a claim about how the
			// record was written, not about whether the file can be parsed.
			archive := engine.ParseReviewArchive(edited)
			if len(archive.Diagnostics) != 0 {
				t.Fatalf("expected the hand edit to leave the parse clean, got %v", archive.Diagnostics)
			}
			if got := archive.Render(); got != edited {
				t.Fatalf("expected a lossless round trip of a hand-edited archive, got:\n%s", got)
			}
			if !hasArchiveDiagnostic(archive.RecordIntegrityDiagnostics(), engine.DiagArchiveRecordIDBroken) {
				t.Fatal("expected the record-integrity check to reject the hand-edited record")
			}

			// Doctor calls it corrupt through the same check the mutation uses.
			var report doctorReport
			checkDoctorRecordIDs(archive, &report)
			requireDoctor(t, report, doctorError, tc.want)

			var err error
			log := withArchiveMutationLog(t, path, func() {
				err = setFocus(io.Discard, path, archiveTime(25, 9, 0), []string{"Something new."})
			})

			var invalidErr *engine.InvalidArchiveError
			if !errors.As(err, &invalidErr) {
				t.Fatalf("expected `focus set` to refuse the archive, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected the refusal to name the damage %q, got %v", tc.want, err)
			}
			if log.replacements != 0 || len(log.commits) != 0 {
				t.Fatalf("expected no replacement and no commit, got %d replacements and commits %v",
					log.replacements, log.commits)
			}
			if got := readFileString(t, path); got != edited {
				t.Fatalf("expected the archive untouched, got:\n%s", got)
			}
			if _, err := os.Stat(path + engine.BackupSuffix); !os.IsNotExist(err) {
				t.Fatalf("expected no backup of an archive seneca refused, got %v", err)
			}
		})
	}
}
