package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	archiveHexRe     = regexp.MustCompile(`\b[0-9a-f]{64}\b`)
	archiveCreatedRe = regexp.MustCompile(`created=\S+`)
)

// normalizeArchiveText masks the two values that vary per run — record IDs and
// creation timestamps — so a golden can assert on the readable Markdown.
func normalizeArchiveText(text string) string {
	text = archiveCreatedRe.ReplaceAllString(text, "created=<ts>")
	return archiveHexRe.ReplaceAllString(text, "<id>")
}

// archiveMutationLog counts what one command did: how many times the archive
// file itself was replaced, and every git commit it asked for.
type archiveMutationLog struct {
	replacements int
	commits      []string
}

// withArchiveMutationLog runs fn with the git seam captured and renames onto
// path counted. Both seams are process-global, so callers must not run in
// parallel.
func withArchiveMutationLog(t *testing.T, path string, fn func()) archiveMutationLog {
	t.Helper()

	var log archiveMutationLog
	previous := commitArchivePaths
	defer func() { commitArchivePaths = previous }()
	commitArchivePaths = func(message string, paths ...string) {
		log.commits = append(log.commits, message+" :: "+strings.Join(paths, ","))
	}

	withJournalFS(func(s *engine.FS) {
		rename := s.Rename
		s.Rename = func(oldpath, newpath string) error {
			if newpath == path {
				log.replacements++
			}
			return rename(oldpath, newpath)
		}
	}, fn)
	return log
}

func TestArchivePathFollowsTheResolvedReflectionsFile(t *testing.T) {
	t.Parallel()

	home := "/home/author"
	cases := []struct {
		name   string
		getenv func(string) string
		want   string
	}{
		{
			name:   "default",
			getenv: func(string) string { return "" },
			want:   filepath.Join(filepath.Dir(defaultReflectionsPath(home)), engine.ArchiveFileName),
		},
		{
			name: "SENECA_FILE override",
			getenv: func(key string) string {
				if key == "SENECA_FILE" {
					return "/elsewhere/journal/reflections.md"
				}
				return ""
			},
			want: "/elsewhere/journal/" + engine.ArchiveFileName,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path, err := resolveFilePathForHome(home, tc.getenv)
			if err != nil {
				t.Fatalf("resolveFilePathForHome returned error: %v", err)
			}
			if got := engine.ArchivePathFor(path); got != tc.want {
				t.Fatalf("expected archive at %s, got %s", tc.want, got)
			}
		})
	}
}

func TestArchiveCreationRendersReadableMarkdown(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)

	tx := mustOpenArchive(t, path)
	review := tx.Archive.NewReviewRecord(
		archiveTime(24, 21, 4),
		engine.ReviewPeriodWeekly,
		"2026-08-17",
		"2026-08-23",
		"",
		"### Recurring struggles\n- Rushing: three late evenings in a row.\n\n### Next focus\nProtect the first hour.",
	)
	if err := tx.AppendRecords(review); err != nil {
		t.Fatalf("appending the review returned error: %v", err)
	}

	second := mustOpenArchive(t, path)
	focus := engine.NewFocusSetRecord(archiveTime(24, 21, 5), engine.FocusOriginReview, review.Review.ID, "Protect the first hour.")
	if err := second.AppendRecords(focus); err != nil {
		t.Fatalf("appending the focus returned error: %v", err)
	}

	want := "# Seneca Review Archive\n" +
		"<!-- seneca:archive v=3 -->\n" +
		"\n" +
		"Append-only. Seneca adds one record per saved review and per focus\n" +
		"change, and never rewrites a record it already wrote. Prose you add\n" +
		"between records is preserved. Seneca refuses to touch this file if the\n" +
		"marker above goes missing.\n" +
		"\n" +
		"## Weekly review — 2026-08-17 to 2026-08-23\n" +
		"<!-- seneca:review v=3 id=<id> created=<ts> period=weekly start=2026-08-17 end=2026-08-23 -->\n" +
		"\n" +
		"### Recurring struggles\n" +
		"- Rushing: three late evenings in a row.\n" +
		"\n" +
		"### Next focus\n" +
		"Protect the first hour.\n" +
		"\n" +
		"---\n" +
		"\n" +
		"## Focus set — 2026-08-24\n" +
		"<!-- seneca:focus v=3 id=<id> created=<ts> event=set effective=2026-08-24 origin=review source=<id> -->\n" +
		"\n" +
		"Protect the first hour.\n" +
		"\n" +
		"---\n"

	if got := normalizeArchiveText(readFileString(t, path)); got != want {
		t.Fatalf("unexpected archive markdown:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestArchiveRoundTripsUnknownProseAndMarkers(t *testing.T) {
	t.Parallel()

	content := "# Seneca Review Archive\n" +
		"<!-- seneca:archive v=2 -->\n" +
		"\n" +
		"Notes I keep at the top of the file.\n" +
		"\n" +
		"## Weekly review — 2026-08-17 to 2026-08-23\n" +
		"<!-- seneca:review v=2 id=" + strings.Repeat("a", 64) + " created=2026-08-24T21:04:05.000000001Z period=weekly start=2026-08-17 end=2026-08-23 weight=heavy -->\n" +
		"\n" +
		"### Recurring struggles\n" +
		"- Rushing.\n" +
		"\n" +
		"> A quote I pasted in later.\n" +
		"\n" +
		"---\n" +
		"\n" +
		"## A heading that is not a record\n" +
		"\n" +
		"Prose between records, kept verbatim.\n" +
		"\n" +
		"## Something a newer Seneca wrote — 2026-08-25\n" +
		"<!-- seneca:daily-note v=3 id=" + strings.Repeat("b", 64) + " -->\n" +
		"\n" +
		"A record kind this binary does not know.\n" +
		"\n" +
		"---\n"

	archive := engine.ParseReviewArchive(content)
	if got := archive.Render(); got != content {
		t.Fatalf("expected a byte-identical round trip, got:\n%s", got)
	}
	if len(archive.Diagnostics) != 0 {
		t.Fatalf("expected unknown prose and markers to be tolerated, got %v", archive.Diagnostics)
	}
	if len(archive.Records) != 2 {
		t.Fatalf("expected the review and the unknown record, got %d records", len(archive.Records))
	}
	if archive.Records[1].Kind != engine.ArchiveRecordUnknown {
		t.Fatalf("expected the future record kind to parse as unknown, got %q", archive.Records[1].Kind)
	}
	if !strings.Contains(archive.Records[0].Raw, "A heading that is not a record") {
		t.Fatal("expected prose after a record to stay inside that record's raw text")
	}
}

func TestArchiveAppendPreservesUnknownProse(t *testing.T) {
	t.Parallel()

	// Seeded at the current schema version so this test stays about the prose:
	// TestArchiveAppendUpgradesAnOlderHeaderVersion owns the marker rewrite.
	path := newArchivePath(t)
	original := "# Seneca Review Archive\n" +
		"<!-- seneca:archive v=" + strconv.Itoa(engine.ArchiveSchemaVersion) + " -->\n" +
		"\n" +
		"My own note at the top.\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("seeding archive: %v", err)
	}

	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Ship the archive layer."))

	got := readFileString(t, path)
	if !strings.HasPrefix(got, original) {
		t.Fatalf("expected the author's header preserved byte for byte, got:\n%s", got)
	}
	if !strings.Contains(got, "Ship the archive layer.") {
		t.Fatalf("expected the appended focus record, got:\n%s", got)
	}
}
