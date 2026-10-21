package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportMonthSelectsTheWholeMonth(t *testing.T) {
	feb28 := journalEntry("## 2026-02-28\n\n### One?\nBefore.")
	mar31 := journalEntry("## 2026-03-31\n\n### One?\nLast day.")
	apr01 := journalEntry("## 2026-04-01\n\n### One?\nAfter.")
	seedExportJournal(t, journalOf(feb28, day01, day05, mar31, apr01))

	byMonth, _, err := runExportCLI(t, "--month=2026-03")
	if err != nil {
		t.Fatalf("export --month failed: %v", err)
	}
	byRange, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-31")
	if err != nil {
		t.Fatalf("export --from/--to failed: %v", err)
	}
	if byMonth != byRange {
		t.Fatalf("--month=2026-03 should equal --from 2026-03-01 --to 2026-03-31\nmonth:\n%s\nrange:\n%s", byMonth, byRange)
	}
	if !strings.Contains(byMonth, "- Range: 2026-03-01 to 2026-03-31 (inclusive)") {
		t.Fatalf("packet does not state the expanded month range:\n%s", byMonth)
	}
	for _, outside := range []string{"Before.", "After."} {
		if strings.Contains(byMonth, outside) {
			t.Fatalf("packet includes %q from outside the month:\n%s", outside, byMonth)
		}
	}
}

func TestExportMonthEndsOnTheMonthsLastDay(t *testing.T) {
	cases := map[string]string{"2026-02": "2026-02-28", "2028-02": "2028-02-29", "2026-04": "2026-04-30", "2026-12": "2026-12-31"}
	for month, last := range cases {
		bounds, err := parseExportMonth(month)
		if err != nil {
			t.Fatalf("parseExportMonth(%q): %v", month, err)
		}
		if bounds.from != month+"-01" || bounds.to != last {
			t.Fatalf("parseExportMonth(%q) = %s..%s, want %s-01..%s", month, bounds.from, bounds.to, month, last)
		}
	}
}

func TestExportMonthRefusals(t *testing.T) {
	seedExportJournal(t, journalOf(day01))

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"non-canonical month", []string{"--month=2026-3"}, `invalid --month "2026-3"`},
		{"not a month", []string{"--month=2026-13"}, `invalid --month "2026-13"`},
		{"word instead of month", []string{"--month=current"}, `invalid --month "current"`},
		{"bare flag", []string{"--month"}, "flag needs an argument"},
		{"with --from", []string{"--month=2026-03", "--from", "2026-03-01"}, "cannot be combined"},
		{"with --to", []string{"--month=2026-03", "--to", "2026-03-31"}, "cannot be combined"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, _, err := runExportCLI(t, tc.args...)
			if err == nil {
				t.Fatalf("expected %v to be refused, got:\n%s", tc.args, stdout)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not report %q", err, tc.want)
			}
			if stdout != "" {
				t.Fatalf("expected no output, got %q", stdout)
			}
		})
	}
}

// The whole document is validated, not the slice inside the range. An entry
// seneca cannot date might belong to any day, including one the range asks for,
// so a packet built around it could be quietly missing a session's evidence.
func TestExportRefusesAnUndatableEntryOutsideTheRange(t *testing.T) {
	undatable := journalEntry("## Sometime last week\n\n### One?\nUndated.")
	path := seedExportJournal(t, journalOf(day01, day02, undatable))

	stdout, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-02")
	var invalid *engine.InvalidDocumentError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected an invalid-document refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("refusal %q does not name the journal %s", err, path)
	}
	if stdout != "" {
		t.Fatalf("expected no packet when the journal is refused, got %q", stdout)
	}
}

// A heading that starts with a date but is not exactly `## YYYY-MM-DD` is the
// quieter half of the same rule: it looks like a day and answers to none.
func TestExportRefusesANoncanonicalDateHeadingOutsideTheRange(t *testing.T) {
	noncanonical := journalEntry("## 2026-03-09 (draft)\n\n### One?\nAlmost dated.")
	seedExportJournal(t, journalOf(day01, day02, noncanonical))

	stdout, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-02")
	var invalid *engine.InvalidDocumentError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected an invalid-document refusal, got %v", err)
	}
	if stdout != "" {
		t.Fatalf("expected no packet when the journal is refused, got %q", stdout)
	}
}

func TestExportRefusesADuplicateDateOutsideTheRange(t *testing.T) {
	other := journalEntry("## 2026-03-05\n\n### One?\nAlso the fifth.")
	seedExportJournal(t, journalOf(day01, day05, other))

	stdout, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-01")
	var duplicate *engine.DuplicateDatesError
	if !errors.As(err, &duplicate) {
		t.Fatalf("expected a duplicate-date refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "2026-03-05") {
		t.Fatalf("refusal %q does not name the repeated date", err)
	}
	if stdout != "" {
		t.Fatalf("expected no packet when the journal is refused, got %q", stdout)
	}
}

// malformed-metadata and duplicate-section are tolerated by the raw-preserving
// validation a rewrite uses, and export must tolerate exactly the same set: it
// replays bytes, so there is nothing for it to get wrong about them.
func TestExportPreservesContentTheSharedValidationTolerates(t *testing.T) {
	odd := journalEntry("## 2026-03-02\n" +
		"<!-- seneca:entry -->\n\n" +
		"### One?\nFirst answer.\n\n" +
		"### One?\nSecond answer under the same heading.\n\n" +
		"<!-- seneca:role -->\n" +
		"### Two?\nStill here.")
	path := seedExportJournal(t, journalOf(day01, odd))

	stdout, _, err := runExportCLI(t, "--from", "2026-03-02", "--to", "2026-03-02")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	if want := wantPacket(path, "2026-03-02", "2026-03-02", odd); stdout != want {
		t.Fatalf("unexpected packet:\n%q\nwant:\n%q", stdout, want)
	}
}

// Nothing between the headings is Seneca's to edit: hand-written sections,
// markers, blockquotes, non-ASCII text, and an answer far longer than any
// summary would keep all come back byte for byte.
func TestExportReproducesCompleteEntryContent(t *testing.T) {
	long := strings.Repeat("Una frase larga que no debe truncarse nunca. ", 200)
	rich := journalEntry("## 2026-03-02\n" +
		"<!-- seneca:entry v=2 id=b0a1 created=2026-03-02T21:04:05.000000001Z -->\n\n" +
		"### ¿Qué noté hoy?\n" +
		"Café, naïve, 日本語, emoji 🌱 — and an em dash…\n\n" +
		long + "\n\n" +
		"<!-- seneca:role name=anchor -->\n" +
		"### ¿Qué llevo a mañana?\n" +
		"Terminar el borrador difícil.\n\n" +
		"<!-- seneca:follow-through source=2026-03-01 -->\n" +
		"### Follow-through\n" +
		"> Empezar por lo difícil.\n\n" +
		"Lo hice a medias.\n\n" +
		"### Notes I keep by hand\n" +
		"- a bullet\n" +
		"-   an indented-looking one\n" +
		"\ta tab-indented line\n" +
		"```\ncode --with --flags\n```")
	path := seedExportJournal(t, journalOf(day01, rich))

	stdout, _, err := runExportCLI(t, "--from", "2026-03-02", "--to", "2026-03-02")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	if want := wantPacket(path, "2026-03-02", "2026-03-02", rich); stdout != want {
		t.Fatalf("packet did not reproduce the entry verbatim:\n%q\nwant:\n%q", stdout, want)
	}
	if !strings.Contains(stdout, long) {
		t.Fatal("the long answer was truncated")
	}
}

// The corrected boundary parser makes a CRLF journal split into its real days.
// Export replays each entry's own bytes, so a CRLF entry stays CRLF; only the
// framing between entries is LF, which is the one normalization the shared
// renderer has always applied.
func TestExportPreservesEachEntrysOwnLineEndings(t *testing.T) {
	crlf01 := asCRLF(day01)
	crlf02 := asCRLF(day02)
	content := crlf01 + asCRLF("\n---\n\n") + crlf02 + asCRLF("\n---\n\n")
	path := seedExportJournal(t, content)

	stdout, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-02")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}

	want := wantPacket(path, "2026-03-01", "2026-03-02", crlf01, crlf02)
	if stdout != want {
		t.Fatalf("unexpected packet:\n%q\nwant:\n%q", stdout, want)
	}
	if !strings.Contains(stdout, "### One?\r\nFirst.") {
		t.Fatalf("expected the entry's own CRLF bytes, got %q", stdout)
	}
	if strings.Contains(stdout, "\r\n---\r\n") {
		t.Fatalf("expected LF framing between entries, got %q", stdout)
	}
}

// A mixed-ending file is the realistic version of the same thing: one day typed
// in an editor that writes CRLF, the rest written by seneca.
func TestExportHandlesAMixedEndingJournal(t *testing.T) {
	crlf02 := asCRLF(day02)
	content := day01 + "\n---\n\n" + crlf02 + asCRLF("\n---\n\n") + day04 + "\n---\n\n"
	path := seedExportJournal(t, content)

	stdout, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-04")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	if want := wantPacket(path, "2026-03-01", "2026-03-04", day01, crlf02, day04); stdout != want {
		t.Fatalf("unexpected packet:\n%q\nwant:\n%q", stdout, want)
	}
}

func TestExportRefusesAnEmptySelection(t *testing.T) {
	path := seedExportJournal(t, journalOf(day01, day02))

	stdout, _, err := runExportCLI(t, "--from", "2026-06-01", "--to", "2026-06-30")
	var empty *emptyExportError
	if !errors.As(err, &empty) {
		t.Fatalf("expected an empty-selection refusal, got %v", err)
	}
	for _, want := range []string{"2026-06-01", "2026-06-30", path, "2 entries"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not mention %q", err, want)
		}
	}
	if stdout != "" {
		t.Fatalf("expected no packet for an empty selection, got %q", stdout)
	}
}

func TestExportRefusesAnEmptyFile(t *testing.T) {
	path := seedExportJournal(t, "")

	stdout, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-05")
	var empty *emptyExportError
	if !errors.As(err, &empty) {
		t.Fatalf("expected an empty-selection refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "holds 0 entries") {
		t.Fatalf("refusal %q does not say the journal is empty", err)
	}
	if got := readFileString(t, path); got != "" {
		t.Fatalf("expected the empty file untouched, got %q", got)
	}
	if stdout != "" {
		t.Fatalf("expected no packet, got %q", stdout)
	}
}

func TestExportRefusesAMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nowhere", "reflections.md")
	t.Setenv("SENECA_FILE", missing)

	stdout, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-05")
	if err == nil {
		t.Fatalf("expected a missing journal to be refused, got:\n%s", stdout)
	}
	if !strings.Contains(err.Error(), "reading file:") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the underlying not-exist error to survive wrapping, got %v", err)
	}
	if stdout != "" {
		t.Fatalf("expected no packet, got %q", stdout)
	}
	// A read must never be the thing that creates the file, or its directory.
	if _, statErr := os.Stat(filepath.Dir(missing)); !os.IsNotExist(statErr) {
		t.Fatalf("export created the missing directory: %v", statErr)
	}
}
