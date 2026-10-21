package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fixtures shared by the engine tests and the command tests. The same helpers
// live in both packages, differing only in how they name engine identifiers: a
// _test file cannot be imported across packages.

// Fixed clock: local time keeps the derived YYYY-MM-DD effective dates stable
// no matter which zone the test machine runs in.
func archiveTime(day, hour, minute int) time.Time {
	return time.Date(2026, time.August, day, hour, minute, 0, 1, time.Local)
}

func mustAppend(t *testing.T, path string, records ...engine.ArchiveRecord) {
	t.Helper()

	tx := mustOpenArchive(t, path)
	if err := tx.AppendRecords(records...); err != nil {
		t.Fatalf("appendRecords returned error: %v", err)
	}
}

func newArchivePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), engine.ArchiveFileName)
}

func mustOpenArchive(t *testing.T, path string) *engine.ArchiveTransaction {
	t.Helper()

	tx, err := journalFS.OpenReviewArchive(path)
	if err != nil {
		t.Fatalf("openReviewArchive(%s) returned error: %v", path, err)
	}
	return tx
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func readFileString(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// injectedFailure is the sentinel a seam override returns.
var injectedFailure = errors.New("injected failure")

const originalContent = "## 2026-05-26\n\n### What did I do badly today?\nRushed.\n"

// tempFileNames lists the persistence layer's leftovers in dir, so a test can
// prove a failed write cleaned up after itself.
func tempFileNames(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading directory %s: %v", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".seneca-") {
			names = append(names, entry.Name())
		}
	}
	return names
}

func dayEntry(date string, answers ...string) string {
	return engine.FormatReflectionEntry(date, continuityQuestions, answers)
}

var continuityQuestions = []string{"Notice?", "Handle?", "Carry?"}

func journalEntries(t *testing.T, raws ...string) []engine.ReflectionEntry {
	t.Helper()

	entries := make([]engine.ReflectionEntry, 0, len(raws))
	for _, raw := range raws {
		entries = append(entries, engine.NewReflectionEntry(raw))
	}
	return entries
}

// archiveWithFocus writes a real archive holding one focus set event effective
// on the given day of August 2026.
func archiveWithFocus(t *testing.T, day int, text string) string {
	t.Helper()

	path := newArchivePath(t)
	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(day, 9, 0), engine.FocusOriginManual, "", text))
	return path
}

func mustContext(t *testing.T, entries []engine.ReflectionEntry, archivePath, date string, anchorIndex int, blank bool) engine.ContinuityContext {
	t.Helper()

	ctx, err := engine.ContinuityContextFor(entries, archivePath, date, continuityQuestions, anchorIndex, blank)
	if err != nil {
		t.Fatalf("continuityContextFor returned error: %v", err)
	}
	return ctx
}

func hasDateDiagnostic(diagnostics []engine.ReflectionDiagnostic) bool {
	for _, d := range diagnostics {
		if d.Code == engine.DiagMalformedDate || d.Code == engine.DiagMissingDate {
			return true
		}
	}
	return false
}
