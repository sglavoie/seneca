package cmd

import (
	"bytes"
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SENECA_FILE wins over the configured path, the same precedence every other
// command follows.
func TestExportUsesTheSharedPathPrecedence(t *testing.T) {
	home := t.TempDir()
	configured := filepath.Join(home, "configured.md")
	if err := os.WriteFile(configured, []byte(journalOf(day05)), 0644); err != nil {
		t.Fatalf("writing the configured journal: %v", err)
	}
	writeConfigFile(t, home, "[seneca]\nreflections_path = "+quoteTOML(configured)+"\n")

	override := filepath.Join(home, "override.md")
	if err := os.WriteFile(override, []byte(journalOf(day01)), 0644); err != nil {
		t.Fatalf("writing the override journal: %v", err)
	}

	got, err := resolveFilePathForHome(home, func(key string) string {
		if key == "SENECA_FILE" {
			return override
		}
		return ""
	})
	if err != nil {
		t.Fatalf("resolveFilePathForHome returned error: %v", err)
	}
	if got != override {
		t.Fatalf("expected the SENECA_FILE override %q, got %q", override, got)
	}

	var out bytes.Buffer
	if err := exportJournal(&out, got, mustExportRange(t, "2026-03-01", "2026-03-05")); err != nil {
		t.Fatalf("exportJournal returned error: %v", err)
	}
	if want := wantPacket(override, "2026-03-01", "2026-03-05", day01); out.String() != want {
		t.Fatalf("expected the override journal exported:\n%q", out.String())
	}
}

// SENECA_FILE may hold a relative path. Provenance is only useful if it names a
// file the reader can open from wherever they are standing, so the packet always
// states the absolute one.
func TestExportReportsAnAbsoluteSourcePathForARelativeOverride(t *testing.T) {
	path := seedExportJournal(t, journalOf(day01))

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting the working directory: %v", err)
	}
	relative, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Fatalf("relating %s to %s: %v", path, cwd, err)
	}
	if filepath.IsAbs(relative) {
		t.Fatalf("expected a relative override, got %q", relative)
	}
	t.Setenv("SENECA_FILE", relative)

	stdout, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-01")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	if want := wantPacket(path, "2026-03-01", "2026-03-01", day01); stdout != want {
		t.Fatalf("expected the absolute source path in the packet:\n%q\nwant:\n%q", stdout, want)
	}
	if strings.Contains(stdout, "- Source: "+relative+"\n") {
		t.Fatalf("packet reported the relative override verbatim:\n%s", stdout)
	}
}

// Export is read-only. Nothing is created, rewritten, or backed up — not even
// the `.bak` every mutating command leaves behind.
func TestExportLeavesTheJournalAndItsDirectoryUntouched(t *testing.T) {
	content := journalOf(day01, day02, day04)
	path := seedExportJournal(t, content)
	dir := filepath.Dir(path)

	before := snapshotDir(t, dir)

	if _, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-04"); err != nil {
		t.Fatalf("export returned error: %v", err)
	}

	if got := readFileString(t, path); got != content {
		t.Fatalf("journal bytes changed:\n%q\nwant:\n%q", got, content)
	}
	if after := snapshotDir(t, dir); !equalStrings(before, after) {
		t.Fatalf("the journal directory changed: %v became %v", before, after)
	}
	if _, err := os.Stat(path + engine.BackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected no backup from a read-only command, got %v", err)
	}
}

func TestExportPropagatesAnOutputWriterFailure(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "reflections.md")
	if err := os.WriteFile(path, []byte(journalOf(day01)), 0644); err != nil {
		t.Fatalf("writing reflections file: %v", err)
	}

	err := exportJournal(failingWriter{}, path, mustExportRange(t, "2026-03-01", "2026-03-01"))
	if !errors.Is(err, injectedFailure) {
		t.Fatalf("expected the writer failure to surface, got %v", err)
	}
	if !strings.Contains(err.Error(), "writing export:") {
		t.Fatalf("expected the failure to say what was being written, got %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, injectedFailure }

// Export answers from the journal alone. A review archive damaged past reading
// blocks the daily form on purpose; it must not stand between the author and
// their own writing, which is the one thing export is for.
func TestExportIgnoresTheReviewArchive(t *testing.T) {
	path := seedExportJournal(t, journalOf(day01))
	archive := engine.ArchivePathFor(path)
	damaged := "# Seneca Review Archive\n<!-- seneca:archive v=2 -->\n\n" +
		"## Focus set — 2026-08-24\n<!-- seneca:focus v=2 id=\"unterminated -->\n\nShip it.\n\n---\n"
	if err := os.WriteFile(archive, []byte(damaged), 0644); err != nil {
		t.Fatalf("writing the damaged archive: %v", err)
	}

	// The archive really is unreadable: the daily form refuses on it.
	if _, err := engine.FocusOnDate(archive, "2026-03-01"); err == nil {
		t.Fatal("expected the damaged archive to be unreadable; the test proves nothing otherwise")
	}

	stdout, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-01")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	if want := wantPacket(path, "2026-03-01", "2026-03-01", day01); stdout != want {
		t.Fatalf("unexpected packet:\n%q\nwant:\n%q", stdout, want)
	}
}

// The packet is evidence, so it has to be quotable: the same journal and the
// same range produce the same bytes, with nothing dated, timed, or randomized
// woven into them.
func TestExportIsDeterministicAcrossRuns(t *testing.T) {
	seedExportJournal(t, journalOf(day01, day02, day04))

	first, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-04")
	if err != nil {
		t.Fatalf("first export returned error: %v", err)
	}
	second, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-04")
	if err != nil {
		t.Fatalf("second export returned error: %v", err)
	}

	if first != second {
		t.Fatalf("two runs disagreed:\n%q\nand:\n%q", first, second)
	}
	if now := time.Now(); strings.Contains(first, now.Format("2006-01-02")) && !strings.Contains(journalOf(day01, day02, day04), now.Format("2006-01-02")) {
		t.Fatalf("packet contains today's date, which no entry holds:\n%s", first)
	}
}

// Cobra must not print the error a second time — main already writes it to
// stderr — and must not dump usage over the stream the packet is built on.
func TestExportSilencesCobrasErrorAndUsageOutput(t *testing.T) {
	seedExportJournal(t, journalOf(day01))

	stdout, stderr, err := runExportCLI(t, "--from", "2026-06-01", "--to", "2026-06-30")
	if err == nil {
		t.Fatal("expected an empty selection to be refused")
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("expected cobra to stay quiet, got stdout %q and stderr %q", stdout, stderr)
	}
	if !exportCmd.SilenceUsage || !exportCmd.SilenceErrors {
		t.Fatal("expected export to silence cobra's usage and duplicate error printing")
	}
}
