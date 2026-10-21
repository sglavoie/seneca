package cmd

import (
	"bytes"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// Everything here runs against a journal this test wrote in its own temporary
// directory, reached through SENECA_FILE. Nothing reads the author's journal,
// an installed seneca, a BrainNotes vault, another checkout, or an AI harness.

// journalEntry values are written without a trailing newline, which is exactly
// what the parser hands back as an entry's raw text. A test can therefore state
// one string and use it both as the file's content and as the bytes it expects
// to find in the packet.
type journalEntry = string

// journalOf frames entries the way seneca writes them: each entry followed by a
// standalone `---` and a blank line.
func journalOf(entries ...journalEntry) string {
	var b strings.Builder
	for _, entry := range entries {
		b.WriteString(entry)
		b.WriteString("\n---\n\n")
	}
	return b.String()
}

// wantPacket spells the packet format out in literals rather than reusing the
// command's own constants, so a change to the format has to be made twice — once
// in the code and once here, deliberately.
func wantPacket(path, from, to string, entries ...journalEntry) string {
	dates := make([]string, 0, len(entries))
	for _, entry := range entries {
		// The heading line may end in CR when the entry was written with CRLF;
		// the date itself is the same either way.
		heading := strings.TrimSuffix(strings.SplitN(entry, "\n", 2)[0], "\r")
		dates = append(dates, strings.TrimPrefix(heading, "## "))
	}

	var b strings.Builder
	b.WriteString("# Seneca journal export\n\n")
	fmt.Fprintf(&b, "- Source: %s\n", path)
	fmt.Fprintf(&b, "- Range: %s to %s (inclusive)\n", from, to)
	fmt.Fprintf(&b, "- Entries: %d\n", len(entries))
	fmt.Fprintf(&b, "- Dates: %s", strings.Join(dates, ", "))
	b.WriteString("\n---\n\n")
	for _, entry := range entries {
		b.WriteString(entry)
		b.WriteString("\n---\n\n")
	}
	return b.String()
}

func seedExportJournal(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "reflections.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing reflections file: %v", err)
	}
	t.Setenv("SENECA_FILE", path)
	return path
}

// resetExportFlags clears both the bound variables and pflag's `Changed` bits.
// Cobra decides a required flag was supplied by looking at `Changed`, and that
// bit survives one Execute into the next inside a single test binary, so without
// this a test that asserts a missing flag would pass or fail depending on which
// tests ran before it.
func resetExportFlags() {
	exportFrom, exportTo, exportMonth = "", "", ""
	exportCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
}

// runExportCLI drives the real command through cobra with its own stdout and
// stderr, so what a test reads is what the streams would carry.
func runExportCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out, errOut bytes.Buffer
	resetExportFlags()
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetArgs(append([]string{"export"}, args...))
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		resetExportFlags()
	})

	err = rootCmd.Execute()
	return out.String(), errOut.String(), err
}

func mustExportRange(t *testing.T, from, to string) exportRange {
	t.Helper()

	bounds, err := parseExportRange(from, to)
	if err != nil {
		t.Fatalf("parseExportRange(%q, %q) returned error: %v", from, to, err)
	}
	return bounds
}

const (
	day01 journalEntry = "## 2026-03-01\n\n### One?\nFirst."
	day02 journalEntry = "## 2026-03-02\n\n### One?\nSecond."
	day04 journalEntry = "## 2026-03-04\n\n### One?\nFourth."
	day05 journalEntry = "## 2026-03-05\n\n### One?\nFifth."
)

// Both bounds are inclusive, and the day on either side of them is not. A range
// that quietly dropped its own endpoints would lose the two days a therapy
// packet is most likely to be built around: the first and the last.
func TestExportIncludesBothEndpointsAndNothingOutside(t *testing.T) {
	path := seedExportJournal(t, journalOf(day01, day02, day04, day05))

	stdout, stderr, err := runExportCLI(t, "--from", "2026-03-02", "--to", "2026-03-04")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	if stderr != "" {
		t.Fatalf("expected a silent stderr on success, got %q", stderr)
	}

	want := wantPacket(path, "2026-03-02", "2026-03-04", day02, day04)
	if stdout != want {
		t.Fatalf("unexpected packet:\n%q\nwant:\n%q", stdout, want)
	}
}

func TestExportAcceptsASingleDayRange(t *testing.T) {
	path := seedExportJournal(t, journalOf(day01, day02, day04))

	stdout, _, err := runExportCLI(t, "--from", "2026-03-02", "--to", "2026-03-02")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}

	if want := wantPacket(path, "2026-03-02", "2026-03-02", day02); stdout != want {
		t.Fatalf("unexpected packet:\n%q\nwant:\n%q", stdout, want)
	}
}

// A gap inside the range is a day the author did not write. The packet says so
// by not containing it; it never fabricates an empty entry to fill the span.
func TestExportLeavesGapsInTheRangeEmptyRatherThanInventingDays(t *testing.T) {
	path := seedExportJournal(t, journalOf(day01, day05))

	stdout, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-05")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}

	if want := wantPacket(path, "2026-03-01", "2026-03-05", day01, day05); stdout != want {
		t.Fatalf("unexpected packet:\n%q\nwant:\n%q", stdout, want)
	}
	for _, absent := range []string{"2026-03-02", "2026-03-03", "2026-03-04"} {
		if strings.Contains(stdout, absent) {
			t.Fatalf("packet mentions %s, which the journal does not hold:\n%s", absent, stdout)
		}
	}
}

// A bound after today is allowed. It is how a therapy range that runs to "today"
// stays correct when the day's entry has not been written yet.
func TestExportAcceptsFutureBoundsWithoutInventingEntries(t *testing.T) {
	path := seedExportJournal(t, journalOf(day01, day02))

	stdout, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2999-12-31")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}

	if want := wantPacket(path, "2026-03-01", "2999-12-31", day01, day02); stdout != want {
		t.Fatalf("unexpected packet:\n%q\nwant:\n%q", stdout, want)
	}
}

// The packet is ordered by date; the parsed document is not reordered to get
// there. Export does not rewrite the file, so a sort that reached back into the
// parse result would only make the in-memory view disagree with the disk.
func TestExportSortsThePacketWithoutReorderingTheParsedDocument(t *testing.T) {
	path := seedExportJournal(t, journalOf(day05, day01, day04, day02))

	doc := engine.ParseReflectionDocument(readFileString(t, path))
	before := make([]string, 0, len(doc.Entries))
	for _, e := range doc.Entries {
		before = append(before, e.Date)
	}

	selected := selectExportEntries(doc.Entries, mustExportRange(t, "2026-03-01", "2026-03-05"))

	after := make([]string, 0, len(doc.Entries))
	for _, e := range doc.Entries {
		after = append(after, e.Date)
	}
	if !equalStrings(before, after) {
		t.Fatalf("selection reordered the parsed document: %v became %v", before, after)
	}

	got := make([]string, 0, len(selected))
	for _, e := range selected {
		got = append(got, e.Date)
	}
	want := []string{"2026-03-01", "2026-03-02", "2026-03-04", "2026-03-05"}
	if !equalStrings(got, want) {
		t.Fatalf("selected dates = %v, want %v", got, want)
	}

	stdout, _, err := runExportCLI(t, "--from", "2026-03-01", "--to", "2026-03-05")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	if wantPkt := wantPacket(path, "2026-03-01", "2026-03-05", day01, day02, day04, day05); stdout != wantPkt {
		t.Fatalf("unexpected packet:\n%q\nwant:\n%q", stdout, wantPkt)
	}
}

func TestExportAcceptsALeapDayThatExists(t *testing.T) {
	leap := journalEntry("## 2028-02-29\n\n### One?\nLeap day.")
	path := seedExportJournal(t, journalOf(leap))

	stdout, _, err := runExportCLI(t, "--from", "2028-02-29", "--to", "2028-02-29")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	if want := wantPacket(path, "2028-02-29", "2028-02-29", leap); stdout != want {
		t.Fatalf("unexpected packet:\n%q\nwant:\n%q", stdout, want)
	}
}

func TestExportRefusesBoundsThatAreNotRealCanonicalDates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		from, to string
		want     string
	}{
		{"unpadded month and day", "2026-3-1", "2026-03-05", `invalid --from "2026-3-1"`},
		{"slashes", "2026/03/01", "2026-03-05", `invalid --from "2026/03/01"`},
		{"a day that does not exist", "2026-03-01", "2026-02-29", `invalid --to "2026-02-29"`},
		{"month thirteen", "2026-13-01", "2026-13-05", `invalid --from "2026-13-01"`},
		{"a timestamp", "2026-03-01T00:00:00Z", "2026-03-05", `invalid --from "2026-03-01T00:00:00Z"`},
		{"trailing space", "2026-03-01 ", "2026-03-05", `invalid --from "2026-03-01 "`},
		{"a two-digit year", "26-03-01", "2026-03-05", `invalid --from "26-03-01"`},
		{"empty", "", "2026-03-05", `invalid --from ""`},
		{"reversed", "2026-03-05", "2026-03-01", "--from 2026-03-05 is after --to 2026-03-01"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseExportRange(tc.from, tc.to)
			if err == nil {
				t.Fatalf("expected %q..%q to be refused", tc.from, tc.to)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not report %q", err, tc.want)
			}
		})
	}
}

// A reversed range reaches the command, not just the parser, and it fails before
// the journal is even opened — so a caller who swapped the flags gets told so
// rather than getting an empty-looking packet.
func TestExportRefusesAReversedRangeBeforeReadingAnything(t *testing.T) {
	seedExportJournal(t, journalOf(day01, day02))

	stdout, _, err := runExportCLI(t, "--from", "2026-03-05", "--to", "2026-03-01")
	if err == nil {
		t.Fatalf("expected a reversed range to be refused, got:\n%s", stdout)
	}
	if !strings.Contains(err.Error(), "must run forward in time") {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "" {
		t.Fatalf("expected no output for a refused range, got %q", stdout)
	}
}

func TestExportRequiresBothBounds(t *testing.T) {
	seedExportJournal(t, journalOf(day01))

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"neither", nil, "export needs a range"},
		{"only --from", []string{"--from", "2026-03-01"}, "--from needs --to"},
		{"only --to", []string{"--to", "2026-03-01"}, "--to needs --from"},
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
