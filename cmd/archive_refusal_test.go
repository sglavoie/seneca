package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestArchiveRefusesForeignFile(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	foreign := "# Somebody else's reviews\n\nNothing here belongs to Seneca.\n"
	if err := os.WriteFile(path, []byte(foreign), 0644); err != nil {
		t.Fatalf("seeding foreign file: %v", err)
	}

	tx := mustOpenArchive(t, path)
	err := tx.AppendRecords(engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Ship it."))

	var foreignErr *engine.ForeignArchiveError
	if !errors.As(err, &foreignErr) {
		t.Fatalf("expected a foreign-archive refusal, got %v", err)
	}
	if got := readFileString(t, path); got != foreign {
		t.Fatalf("expected the foreign file untouched, got:\n%s", got)
	}
	if _, err := os.Stat(path + engine.BackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected no backup of a file Seneca refused, got %v", err)
	}
}

func TestArchiveRefusesUnsupportedSchemaVersion(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	content := "# Seneca Review Archive\n<!-- seneca:archive v=99 -->\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("seeding archive: %v", err)
	}

	tx := mustOpenArchive(t, path)
	err := tx.AppendRecords(engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Ship it."))

	var versionErr *engine.UnsupportedArchiveVersionError
	if !errors.As(err, &versionErr) {
		t.Fatalf("expected an unsupported-version refusal, got %v", err)
	}
	if got := readFileString(t, path); got != content {
		t.Fatalf("expected the archive untouched, got:\n%s", got)
	}
}

// TestArchiveRefusesMalformedHeaderVersion pins the rule that a nonempty
// archive must state a schema version Seneca can read. Substituting the current
// version for a missing or unreadable one would let a mutation proceed on a
// promise about the file's shape that nobody made.
func TestArchiveRefusesMalformedHeaderVersion(t *testing.T) {
	cases := []struct {
		name   string
		marker string
		want   string
	}{
		{
			name:   "no version attribute",
			marker: "<!-- seneca:archive -->",
			want:   "declares no archive schema version",
		},
		{
			name:   "empty version attribute",
			marker: "<!-- seneca:archive v=\"\" -->",
			want:   "declares no archive schema version",
		},
		{
			name:   "nonnumeric version",
			marker: "<!-- seneca:archive v=two -->",
			want:   `declares "two", which is not a usable archive schema version`,
		},
		{
			name:   "version zero",
			marker: "<!-- seneca:archive v=0 -->",
			want:   `declares "0", which is not a usable archive schema version`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Seneca writes the archive itself, so only the header marker is
			// damaged: every record below it is genuine and would be accepted.
			path := newArchivePath(t)
			mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Ship it."))

			healthy := "<!-- seneca:archive v=" + strconv.Itoa(engine.ArchiveSchemaVersion) + " -->"
			content := strings.Replace(readFileString(t, path), healthy, tc.marker, 1)
			if strings.Contains(content, healthy) {
				t.Fatalf("expected the seeded header marker to be replaced, got:\n%s", content)
			}
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatalf("seeding archive: %v", err)
			}

			var err error
			log := withArchiveMutationLog(t, path, func() {
				tx := mustOpenArchive(t, path)
				err = tx.AppendRecords(engine.NewFocusSetRecord(archiveTime(25, 9, 0), engine.FocusOriginManual, "", "Ship something else."))
			})

			var versionErr *engine.UnreadableArchiveVersionError
			if !errors.As(err, &versionErr) {
				t.Fatalf("expected an unreadable-version refusal, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected the refusal to say %q, got %q", tc.want, err.Error())
			}
			if got := readFileString(t, path); got != content {
				t.Fatalf("expected the archive untouched, got:\n%s", got)
			}
			if _, statErr := os.Stat(path + engine.BackupSuffix); !os.IsNotExist(statErr) {
				t.Fatalf("expected no backup of a refused archive, got %v", statErr)
			}
			if log.replacements != 0 || len(log.commits) != 0 {
				t.Fatalf("expected no replacement and no commit, got %d replacements and %v", log.replacements, log.commits)
			}

			// Doctor must name the same defect from the same code, so a report
			// that stays quiet can never precede a refused write.
			h := newDoctorHome(t)
			h.write(t, h.reflections, doctorHealthyJournal)
			h.write(t, h.archive, content)
			requireDoctor(t, diagnose(h.env()), doctorError, tc.want)
		})
	}
}

func TestArchiveRefusesMalformedMetadata(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		record string
		code   engine.ArchiveDiagnosticCode
	}{
		{
			name:   "unparseable marker",
			record: "## Focus set — 2026-08-24\n<!-- seneca:focus v=2 id=\"unterminated -->\n\nShip it.\n\n---\n",
			code:   engine.DiagArchiveMalformedMarker,
		},
		{
			name:   "missing attribute",
			record: "## Focus set — 2026-08-24\n<!-- seneca:focus v=2 created=2026-08-24T09:00:00Z event=set effective=2026-08-24 origin=manual -->\n\nShip it.\n\n---\n",
			code:   engine.DiagArchiveMissingAttr,
		},
		{
			name:   "invalid enum",
			record: "## Focus set — 2026-08-24\n<!-- seneca:focus v=2 id=" + strings.Repeat("c", 64) + " created=2026-08-24T09:00:00Z event=toggle effective=2026-08-24 origin=manual -->\n\nShip it.\n\n---\n",
			code:   engine.DiagArchiveInvalidAttr,
		},
		{
			name:   "invalid date",
			record: "## Focus set — 2026-08-24\n<!-- seneca:focus v=2 id=" + strings.Repeat("d", 64) + " created=2026-08-24T09:00:00Z event=set effective=2026-13-45 origin=manual -->\n\nShip it.\n\n---\n",
			code:   engine.DiagArchiveInvalidAttr,
		},
		{
			name:   "invalid timestamp",
			record: "## Focus set — 2026-08-24\n<!-- seneca:focus v=2 id=" + strings.Repeat("e", 64) + " created=yesterday event=set effective=2026-08-24 origin=manual -->\n\nShip it.\n\n---\n",
			code:   engine.DiagArchiveInvalidAttr,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := newArchivePath(t)
			content := "# Seneca Review Archive\n<!-- seneca:archive v=2 -->\n\n" + tc.record
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatalf("seeding archive: %v", err)
			}

			archive := engine.ParseReviewArchive(content)
			if !hasArchiveDiagnostic(archive.Diagnostics, tc.code) {
				t.Fatalf("expected a %s diagnostic, got %v", tc.code, archive.Diagnostics)
			}
			// Reads tolerate the damage; the file still round-trips.
			if got := archive.Render(); got != content {
				t.Fatalf("expected a lossless round trip of a malformed record, got:\n%s", got)
			}

			tx := mustOpenArchive(t, path)
			err := tx.AppendRecords(engine.NewFocusSetRecord(archiveTime(25, 9, 0), engine.FocusOriginManual, "", "Ship it."))
			var invalidErr *engine.InvalidArchiveError
			if !errors.As(err, &invalidErr) {
				t.Fatalf("expected a state-changing command to refuse, got %v", err)
			}
			if got := readFileString(t, path); got != content {
				t.Fatalf("expected the archive untouched, got:\n%s", got)
			}
		})
	}
}

func TestArchiveRefusesDuplicateRecordIDs(t *testing.T) {
	t.Parallel()

	id := strings.Repeat("f", 64)
	record := func(day string) string {
		return "## Focus set — " + day + "\n" +
			"<!-- seneca:focus v=2 id=" + id + " created=2026-08-" + strings.TrimPrefix(day, "2026-08-") + "T09:00:00Z event=set effective=" + day + " origin=manual -->\n" +
			"\nShip it.\n\n---\n"
	}
	content := "# Seneca Review Archive\n<!-- seneca:archive v=2 -->\n\n" + record("2026-08-24") + "\n" + record("2026-08-25")

	archive := engine.ParseReviewArchive(content)
	if !hasArchiveDiagnostic(archive.Diagnostics, engine.DiagArchiveDuplicateID) {
		t.Fatalf("expected a duplicate-id diagnostic, got %v", archive.Diagnostics)
	}

	path := newArchivePath(t)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("seeding archive: %v", err)
	}
	tx := mustOpenArchive(t, path)
	var invalidErr *engine.InvalidArchiveError
	if err := tx.AppendRecords(engine.NewFocusClearRecord(archiveTime(26, 9, 0))); !errors.As(err, &invalidErr) {
		t.Fatalf("expected a refusal on duplicate ids, got %v", err)
	}
	if got := readFileString(t, path); got != content {
		t.Fatalf("expected the archive untouched, got:\n%s", got)
	}
}

func TestArchiveRefusesInvalidRevisionReferences(t *testing.T) {
	t.Parallel()

	reviewAt := func(id, revises string) string {
		marker := "<!-- seneca:review v=2 id=" + id + " created=2026-08-24T21:04:05Z period=weekly start=2026-08-17 end=2026-08-23"
		if revises != "" {
			marker += " revises=" + revises
		}
		marker += " -->"
		return "## Weekly review — 2026-08-17 to 2026-08-23\n" + marker + "\n\nBody.\n\n---\n"
	}
	first := strings.Repeat("1", 64)
	second := strings.Repeat("2", 64)
	missing := strings.Repeat("9", 64)

	cases := []struct {
		name    string
		content string
	}{
		{
			name:    "dangling reference",
			content: reviewAt(first, missing),
		},
		{
			name: "forward reference",
			// The first record claims to revise one written after it, which an
			// append-only archive can never produce.
			content: reviewAt(first, second) + "\n" + reviewAt(second, ""),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			content := "# Seneca Review Archive\n<!-- seneca:archive v=2 -->\n\n" + tc.content
			archive := engine.ParseReviewArchive(content)
			if !hasArchiveDiagnostic(archive.Diagnostics, engine.DiagArchiveInvalidRevision) {
				t.Fatalf("expected an invalid-revision diagnostic, got %v", archive.Diagnostics)
			}

			path := newArchivePath(t)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatalf("seeding archive: %v", err)
			}
			tx := mustOpenArchive(t, path)
			var invalidErr *engine.InvalidArchiveError
			if err := tx.AppendRecords(engine.NewFocusClearRecord(archiveTime(26, 9, 0))); !errors.As(err, &invalidErr) {
				t.Fatalf("expected a refusal on an invalid revision, got %v", err)
			}
		})
	}
}

func hasArchiveDiagnostic(diagnostics []engine.ArchiveDiagnostic, code engine.ArchiveDiagnosticCode) bool {
	for _, d := range diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}
