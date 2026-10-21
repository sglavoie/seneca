package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Entry boundaries decide which day a section belongs to, so a file written
// with CRLF or bare-CR breaks must split into exactly the same entries as the
// LF file it is otherwise byte-for-byte equivalent to. When it does not, the
// failure is silent and total: the whole document parses as one entry, every
// date but the first stops existing for find, sort, merge and continuity, and
// duplicate-date validation has nothing left to refuse.

const boundaryEntry24 = "## 2026-05-24\n\n### One?\nEarliest.\n"
const boundaryEntry26 = "## 2026-05-26\n\n### One?\nMiddle.\n"
const boundaryEntry28 = "## 2026-05-28\n\n### One?\nLatest.\n"

// asCRLF and asCR rewrite an LF fixture into the other two break styles. The
// text is otherwise identical, which is the whole point: only the bytes that
// end a line change.
func asCRLF(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
func asCR(s string) string   { return strings.ReplaceAll(s, "\n", "\r") }

// journalWith joins entries with the canonical separator framing, written in
// the same break style as the entries themselves.
func journalWith(convert func(string) string, entries ...string) string {
	var b strings.Builder
	for _, entry := range entries {
		b.WriteString(convert(entry))
		b.WriteString(convert("\n---\n\n"))
	}
	return b.String()
}

func TestParseReflectionDocumentSplitsEveryLineEndingStyle(t *testing.T) {
	t.Parallel()

	// A mixed-ending file is not a curiosity: it is what an editor leaves behind
	// after one entry is appended to a journal that came from another machine.
	mixedEntry24 := boundaryEntry24
	mixedEntry26 := asCRLF(boundaryEntry26)
	mixedEntry28 := asCR(boundaryEntry28)
	mixed := mixedEntry24 + "\n---\n\n" +
		mixedEntry26 + asCRLF("\n---\n\n") +
		mixedEntry28 + asCR("\n---\n\n")

	tests := []struct {
		name     string
		content  string
		wantRaws []string
	}{
		{
			name:    "lf",
			content: journalWith(func(s string) string { return s }, boundaryEntry24, boundaryEntry26, boundaryEntry28),
			wantRaws: []string{
				strings.TrimSuffix(boundaryEntry24, "\n"),
				strings.TrimSuffix(boundaryEntry26, "\n"),
				strings.TrimSuffix(boundaryEntry28, "\n"),
			},
		},
		{
			name:    "crlf",
			content: journalWith(asCRLF, boundaryEntry24, boundaryEntry26, boundaryEntry28),
			wantRaws: []string{
				asCRLF(strings.TrimSuffix(boundaryEntry24, "\n")),
				asCRLF(strings.TrimSuffix(boundaryEntry26, "\n")),
				asCRLF(strings.TrimSuffix(boundaryEntry28, "\n")),
			},
		},
		{
			name:    "bare cr",
			content: journalWith(asCR, boundaryEntry24, boundaryEntry26, boundaryEntry28),
			wantRaws: []string{
				asCR(strings.TrimSuffix(boundaryEntry24, "\n")),
				asCR(strings.TrimSuffix(boundaryEntry26, "\n")),
				asCR(strings.TrimSuffix(boundaryEntry28, "\n")),
			},
		},
		{
			name:    "mixed",
			content: mixed,
			wantRaws: []string{
				strings.TrimSuffix(mixedEntry24, "\n"),
				asCRLF(strings.TrimSuffix(boundaryEntry26, "\n")),
				asCR(strings.TrimSuffix(boundaryEntry28, "\n")),
			},
		},
	}

	wantDates := []string{"2026-05-24", "2026-05-26", "2026-05-28"}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc := engine.ParseReflectionDocument(tc.content)

			if len(doc.Entries) != len(wantDates) {
				t.Fatalf("expected %d entries, got %d", len(wantDates), len(doc.Entries))
			}
			if len(doc.Diagnostics) != 0 {
				t.Fatalf("expected a clean parse, got %v", doc.Diagnostics)
			}
			for i, want := range wantDates {
				if doc.Entries[i].Date != want {
					t.Fatalf("entry %d: expected date %q, got %q", i, want, doc.Entries[i].Date)
				}
				if _, ok := doc.Find(want); !ok {
					t.Fatalf("expected %s to be selectable by date", want)
				}
				// Raw bytes, line endings included, are the author's; only the
				// separator framing between entries is ours to normalize.
				if got := doc.Entries[i].Raw; got != tc.wantRaws[i] {
					t.Fatalf("entry %d: expected raw %q, got %q", i, tc.wantRaws[i], got)
				}
			}
			if got := doc.Entries[1].Answers([]string{"One?"}); got[0] != "Middle." {
				t.Fatalf("expected the middle answer readable, got %q", got[0])
			}
		})
	}
}

// TestParseReflectionDocumentKeepsProseEndingInDashes pins the other half of the
// grammar: only a line that is exactly `---` separates. Three dashes closing a
// sentence are the author's words, and a trim that stripped them from the end of
// a chunk would delete them from a place nobody thinks to check.
func TestParseReflectionDocumentKeepsProseEndingInDashes(t *testing.T) {
	t.Parallel()

	first := "" +
		"## 2026-05-24\n\n" +
		"### One?\n" +
		"An em dash typed the long way ---\n" +
		"----\n" +
		" ---\n" +
		"--- \n" +
		"and a thought that simply trails off---"
	second := "" +
		"## 2026-05-26\n\n" +
		"### One?\n" +
		"Ends in dashes too---"
	content := first + "\n\n---\n\n" + second + "\n\n---\n\n"

	doc := engine.ParseReflectionDocument(content)

	if len(doc.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(doc.Entries))
	}
	if doc.Entries[0].Raw != first {
		t.Fatalf("expected the first entry byte-identical, got %q", doc.Entries[0].Raw)
	}
	if doc.Entries[1].Raw != second {
		t.Fatalf("expected the second entry byte-identical, got %q", doc.Entries[1].Raw)
	}
	if got := doc.Entries[1].Answers([]string{"One?"}); got[0] != "Ends in dashes too---" {
		t.Fatalf("expected the trailing dashes kept in the answer, got %q", got[0])
	}
	if got := doc.Render(); got != first+engine.EntryTerminator+second+engine.EntryTerminator {
		t.Fatalf("expected a byte-stable render, got %q", got)
	}
}

func TestParseReflectionDocumentSeparatorFramingVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "canonical terminator",
			content: boundaryEntry24 + "\n---\n\n" + boundaryEntry26 + "\n---\n\n",
		},
		{
			name:    "final separator without a trailing newline",
			content: boundaryEntry24 + "\n---\n\n" + boundaryEntry26 + "\n---",
		},
		{
			name:    "no final separator at all",
			content: boundaryEntry24 + "\n---\n\n" + boundaryEntry26,
		},
		{
			name:    "no final separator and no final newline",
			content: boundaryEntry24 + "\n---\n\n" + strings.TrimSuffix(boundaryEntry26, "\n"),
		},
		{
			name:    "doubled separators",
			content: boundaryEntry24 + "\n---\n---\n\n" + boundaryEntry26 + "\n---\n\n",
		},
		{
			name:    "leading separator",
			content: "---\n\n" + boundaryEntry24 + "\n---\n\n" + boundaryEntry26 + "\n---\n\n",
		},
		{
			name:    "crlf terminator without a trailing newline",
			content: asCRLF(boundaryEntry24+"\n---\n\n"+boundaryEntry26) + "\r\n---",
		},
		{
			name:    "generous blank framing",
			content: "\n\n" + boundaryEntry24 + "\n\n\n---\n\n\n" + boundaryEntry26 + "\n---\n\n\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc := engine.ParseReflectionDocument(tc.content)

			if len(doc.Entries) != 2 {
				t.Fatalf("expected 2 entries, got %d: %#v", len(doc.Entries), doc.Entries)
			}
			if doc.Entries[0].Date != "2026-05-24" || doc.Entries[1].Date != "2026-05-26" {
				t.Fatalf("unexpected dates %q and %q", doc.Entries[0].Date, doc.Entries[1].Date)
			}
			if len(doc.Diagnostics) != 0 {
				t.Fatalf("expected a clean parse, got %v", doc.Diagnostics)
			}
		})
	}
}

// TestParseReflectionDocumentPreservesCRLFBoundaryWhitespace is the CRLF twin of
// the LF boundary case: a suffixed date heading is the entire last entry, so its
// trailing space is both the first and the last byte-bearing content of the
// chunk. It must stay undated, keep its bytes, and keep its diagnostic index.
func TestParseReflectionDocumentPreservesCRLFBoundaryWhitespace(t *testing.T) {
	t.Parallel()

	const suffixed = "## 2026-05-26 "
	content := asCRLF("## 2026-05-25\n\n### One?\nFine.\n\n---\n\n") + suffixed + "\r\n\r\n---\r\n\r\n"

	doc := engine.ParseReflectionDocument(content)

	if len(doc.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(doc.Entries))
	}
	boundary := doc.Entries[1]
	if boundary.Raw != suffixed {
		t.Fatalf("expected the suffix preserved verbatim, got %q", boundary.Raw)
	}
	if boundary.HeadingLine != suffixed {
		t.Fatalf("expected the heading preserved verbatim, got %q", boundary.HeadingLine)
	}
	if boundary.Date != "" {
		t.Fatalf("expected the boundary entry undated, got %q", boundary.Date)
	}
	if _, ok := doc.Find("2026-05-26"); ok {
		t.Fatal("expected the suffixed heading to be unselectable by its date prefix")
	}
	if !hasDateDiagnostic(boundary.Diagnostics) {
		t.Fatalf("expected a fatal date diagnostic, got %v", boundary.Diagnostics)
	}
	// Diagnostic indexes are document positions, so they must count the entries
	// the split actually produced.
	for _, d := range doc.Diagnostics {
		if d.EntryIndex != 1 {
			t.Fatalf("expected the diagnostic on entry 1, got %+v", d)
		}
	}

	var invalidErr *engine.InvalidDocumentError
	if err := engine.ValidateReflectionDocument("reflections file", "reflections.md", doc); !errors.As(err, &invalidErr) {
		t.Fatalf("expected the document refused for writing, got %v", err)
	}
}

func TestRunSortOrdersACRLFJournalAndNormalizesOnlyItsFraming(t *testing.T) {
	unsorted := asCRLF("## 2026-05-26\n\n### One?\nLater.\n\n---\n\n") +
		asCRLF("## 2026-05-24\n\n### One?\nEarlier.\n\n---\n\n")
	// Entry bytes survive with their CRLF breaks; the separator around them is
	// rewritten with LF, exactly as the renderer has always emitted it.
	sorted := asCRLF("## 2026-05-24\n\n### One?\nEarlier.") + engine.EntryTerminator +
		asCRLF("## 2026-05-26\n\n### One?\nLater.") + engine.EntryTerminator

	root, path := seedSortRepo(t, unsorted)

	if err := runSort(nil, nil); err != nil {
		t.Fatalf("runSort returned error: %v", err)
	}

	if got := readFileString(t, path); got != sorted {
		t.Fatalf("unexpected sorted content %q", got)
	}
	if got := readFileString(t, path+engine.BackupSuffix); got != unsorted {
		t.Fatalf("expected the backup to hold the pre-sort bytes, got %q", got)
	}

	// Once written, the file is canonical: sorting again touches nothing.
	headAfterSort := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	if err := runSort(nil, nil); err != nil {
		t.Fatalf("second runSort returned error: %v", err)
	}
	if got := readFileString(t, path); got != sorted {
		t.Fatalf("expected the second sort to be a no-op, got %q", got)
	}
	if head := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD")); head != headAfterSort {
		t.Fatalf("expected no second commit, HEAD moved from %s to %s", headAfterSort, head)
	}
}

// TestRunSortRefusesDuplicateDatesInACRLFJournal guards the refusal that a
// broken split disarms: with the whole file collapsed into one entry there is no
// second 2026-05-26 to find, and the sort would rewrite the journal instead of
// stopping.
func TestRunSortRefusesDuplicateDatesInACRLFJournal(t *testing.T) {
	duplicated := asCRLF("## 2026-05-26\n\n### One?\nFirst.\n\n---\n\n") +
		asCRLF("## 2026-05-26\n\n### One?\nSecond.\n\n---\n\n")

	_, path := seedSortRepo(t, duplicated)

	err := runSort(nil, nil)
	var dupErr *engine.DuplicateDatesError
	if !errors.As(err, &dupErr) {
		t.Fatalf("expected duplicate-date refusal, got %v", err)
	}
	if got := readFileString(t, path); got != duplicated {
		t.Fatalf("expected file untouched, got %q", got)
	}
	if _, statErr := os.Stat(path + engine.BackupSuffix); !os.IsNotExist(statErr) {
		t.Fatalf("expected no backup, got %v", statErr)
	}
}

func TestRunMergeCombinesJournalsWrittenWithDifferentLineEndings(t *testing.T) {
	root := t.TempDir()
	reflections := filepath.Join(root, "reflections.md")
	source := filepath.Join(root, "incoming.md")

	dstEntry := "## 2026-05-25\n\n### One?\nDestination.\n"
	srcEntry24 := asCRLF("## 2026-05-24\n\n### One?\nSource earliest.\n")
	srcEntry26 := asCRLF("## 2026-05-26\n\n### One?\nSource latest.\n")

	dstContent := dstEntry + "\n---\n\n"
	srcContent := srcEntry24 + asCRLF("\n---\n\n") + srcEntry26 + asCRLF("\n---\n\n")

	if err := os.WriteFile(reflections, []byte(dstContent), 0644); err != nil {
		t.Fatalf("writing reflections file: %v", err)
	}
	if err := os.WriteFile(source, []byte(srcContent), 0644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}
	t.Setenv("SENECA_FILE", reflections)

	if err := runMerge(nil, []string{source}); err != nil {
		t.Fatalf("runMerge returned error: %v", err)
	}

	want := strings.TrimSuffix(srcEntry24, "\r\n") + engine.EntryTerminator +
		strings.TrimSuffix(dstEntry, "\n") + engine.EntryTerminator +
		strings.TrimSuffix(srcEntry26, "\r\n") + engine.EntryTerminator
	if got := readFileString(t, reflections); got != want {
		t.Fatalf("expected the CRLF source merged as two dated entries, got %q", got)
	}
	if got := readFileString(t, source); got != srcContent {
		t.Fatalf("expected the source byte-identical, got %q", got)
	}
}

// TestContinuityContextReadsThePriorEntryOfACRLFJournal covers the consumer the
// boundary bug hurt most quietly: continuity asks for the newest dated entry
// before today, and a collapsed document answers with the oldest heading in the
// file while carrying the newest text.
func TestContinuityContextReadsThePriorEntryOfACRLFJournal(t *testing.T) {
	t.Parallel()

	content := asCRLF(""+
		"## 2026-08-18\n\n### Notice?\nNoticed.\n\n### Handle?\nHandled.\n\n### Carry?\nFinish the migration.\n\n---\n\n") +
		asCRLF("## 2026-08-23\n\n### Notice?\nNoticed again.\n\n### Handle?\nHandled again.\n\n### Carry?\nShip the parser.\n\n---\n\n")

	doc := engine.ParseReflectionDocument(content)
	if len(doc.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(doc.Entries))
	}

	ctx := mustContext(t, doc.Entries, filepath.Join(t.TempDir(), engine.ArchiveFileName), "2026-08-24", 3, false)

	if ctx.PriorDate != "2026-08-23" {
		t.Fatalf("expected the newest prior entry, got %q", ctx.PriorDate)
	}
	if ctx.PriorText != "Ship the parser." {
		t.Fatalf("expected the newest intention, got %q", ctx.PriorText)
	}
}
