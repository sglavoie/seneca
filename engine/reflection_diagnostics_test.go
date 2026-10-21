package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParserDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want ReflectionDiagnosticCode
	}{
		{
			name: "malformed date",
			raw:  "## 2026-13-45\n\n### What did I do well?\nFine.\n",
			want: DiagMalformedDate,
		},
		{
			name: "missing date",
			raw:  "## Untitled\n\n### What did I do well?\nFine.\n",
			want: DiagMissingDate,
		},
		{
			name: "structurally invalid entry",
			raw:  "### What did I do well?\nNo heading above me.\n",
			want: diagInvalidEntry,
		},
		{
			name: "duplicate section",
			raw:  "## 2026-05-26\n\n### Same?\nFirst.\n\n### Same?\nSecond.\n",
			want: diagDuplicateSection,
		},
		{
			name: "unterminated marker comment",
			raw:  "## 2026-05-26\n<!-- seneca:entry v=2\n\n### Same?\nFirst.\n",
			want: diagMalformedMetadata,
		},
		{
			name: "entry marker without version",
			raw:  "## 2026-05-26\n<!-- seneca:entry id=abc -->\n\n### Same?\nFirst.\n",
			want: diagMalformedMetadata,
		},
		{
			name: "role marker without name",
			raw:  "## 2026-05-26\n\n<!-- seneca:role -->\n### Same?\nFirst.\n",
			want: diagMalformedMetadata,
		},
		{
			name: "follow-through with malformed source",
			raw:  "## 2026-05-26\n\n<!-- seneca:follow-through source=yesterday -->\n### Follow-through\n> x\n",
			want: diagMalformedMetadata,
		},
		{
			name: "marker with repeated attribute",
			raw:  "## 2026-05-26\n<!-- seneca:entry v=2 v=3 -->\n\n### Same?\nFirst.\n",
			want: diagMalformedMetadata,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entry := NewReflectionEntry(tc.raw)
			found := false
			for _, d := range entry.Diagnostics {
				if d.Code == tc.want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected diagnostic %q, got %v", tc.want, entry.Diagnostics)
			}
			if entry.Raw != strings.TrimSpace(tc.raw) {
				t.Fatal("expected a diagnosed entry to still preserve its raw text")
			}
		})
	}
}

// TestReflectionDocumentRejectsNonCanonicalDateHeadings pins the rule that only
// an exact `## YYYY-MM-DD` heading names a day. A heading that merely starts
// with a date is ambiguous — it is diagnosed, it never answers to that date for
// a date-targeted edit, and it still round-trips byte for byte.
func TestReflectionDocumentRejectsNonCanonicalDateHeadings(t *testing.T) {
	t.Parallel()

	t.Run("only exact headings carry a date", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			heading  string
			wantDate string
		}{
			{name: "canonical", heading: "## 2026-05-26", wantDate: "2026-05-26"},
			{name: "trailing space", heading: "## 2026-05-26 "},
			{name: "trailing tab", heading: "## 2026-05-26\t"},
			{name: "trailing spaces and tab", heading: "## 2026-05-26  \t"},
			{name: "trailing title", heading: "## 2026-05-26 travel notes"},
			{name: "trailing punctuation", heading: "## 2026-05-26."},
			{name: "longer digit run", heading: "## 2026-05-260"},
			{name: "time suffix", heading: "## 2026-05-26T09:00"},
			{name: "date range", heading: "## 2026-05-26 - 2026-05-27"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				raw := tc.heading + "\n\n### What did I do well?\nFine.\n"
				entry := NewReflectionEntry(raw)
				if entry.Date != tc.wantDate {
					t.Fatalf("expected date %q for heading %q, got %q", tc.wantDate, tc.heading, entry.Date)
				}

				var dated bool
				for _, d := range entry.Diagnostics {
					if d.Code == DiagMalformedDate || d.Code == DiagMissingDate {
						dated = true
					}
				}
				if tc.wantDate != "" && dated {
					t.Fatalf("expected no date diagnostic for %q, got %v", tc.heading, entry.Diagnostics)
				}
				if tc.wantDate == "" && !dated {
					t.Fatalf("expected a fatal date diagnostic for %q, got %v", tc.heading, entry.Diagnostics)
				}
				if entry.Raw != strings.TrimSpace(raw) {
					t.Fatalf("expected raw preserved, got %q", entry.Raw)
				}
			})
		}
	})

	// A whitespace suffix is invisible in a rendered document, so it gets its own
	// on-disk case beside the visible one: the refusal must be byte-neutral for
	// both, and neither may be tidied into a canonical heading on the way out.
	rejected := []struct {
		name    string
		heading string
	}{
		{name: "visible suffix", heading: "## 2026-05-26 travel notes"},
		{name: "trailing space", heading: "## 2026-05-26 "},
		{name: "trailing tab", heading: "## 2026-05-26\t"},
	}

	for _, tc := range rejected {
		t.Run("a rejected heading cannot be edited by date: "+tc.name, func(t *testing.T) {
			t.Parallel()

			content := "" +
				"## 2026-05-25\n\n### What did I do well?\nFine.\n\n---\n\n" +
				tc.heading + "\n\n### What did I do well?\nAlso fine.\n\n---\n\n"

			doc := ParseReflectionDocument(content)
			if len(doc.Entries) != 2 {
				t.Fatalf("expected 2 entries, got %d", len(doc.Entries))
			}
			if _, ok := doc.Find("2026-05-26"); ok {
				t.Fatal("expected the suffixed heading to be unselectable by its date prefix")
			}
			if doc.Entries[1].Date != "" {
				t.Fatalf("expected the suffixed entry to stay undated, got %q", doc.Entries[1].Date)
			}

			var invalidErr *InvalidDocumentError
			if err := ValidateReflectionDocument("reflections file", "reflections.md", doc); !errors.As(err, &invalidErr) {
				t.Fatalf("expected the document refused for writing, got %v", err)
			}

			// Raw preservation: the rejected heading survives verbatim and the
			// document still renders stably through a second parse.
			rendered := doc.Render()
			if !strings.Contains(rendered, tc.heading+"\n") {
				t.Fatalf("expected the rejected heading preserved verbatim, got %q", rendered)
			}
			reparsed := ParseReflectionDocument(rendered)
			if again := reparsed.Render(); again != rendered {
				t.Fatalf("expected a stable round trip, got %q", again)
			}
			if _, ok := reparsed.Find("2026-05-26"); ok {
				t.Fatal("expected the rejected heading to stay undated across a round trip")
			}

			// The same document on disk: every write is refused, byte-neutrally.
			path := filepath.Join(t.TempDir(), "reflections.md")
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatalf("seeding file: %v", err)
			}
			tx, err := FS{}.OpenReflectionFile(path)
			if err != nil {
				t.Fatalf("openReflectionFile returned error: %v", err)
			}
			if err := tx.Validate(); !errors.As(err, &invalidErr) {
				t.Fatalf("expected the stored file refused, got %v", err)
			}
			if err := tx.CommitEntries(tx.Entries()); !errors.As(err, &invalidErr) {
				t.Fatalf("expected the rewrite refused, got %v", err)
			}
			if got := readFileString(t, path); got != content {
				t.Fatalf("expected the file untouched after a refusal, got %q", got)
			}
			if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
				t.Fatalf("expected no backup after a refusal, got %v", err)
			}
		})
	}
}

// TestReflectionDocumentRejectsTrailingWhitespaceAtEntryBoundary covers the case
// the heading table above cannot reach: a suffixed date heading with no section
// content after it, so the suffix sits at the very edge of the chunk the parser
// is handed. Document and entry framing is trimmed away there, and a trim that
// reaches into the last content line would canonicalize the heading before
// readDateHeading ever sees it — the entry would take the date, stay selectable,
// raise no diagnostic, and lose the bytes the author actually wrote.
func TestReflectionDocumentRejectsTrailingWhitespaceAtEntryBoundary(t *testing.T) {
	t.Parallel()

	const firstEntry = "## 2026-05-25\n\n### What did I do well?\nFine.\n\n---\n\n"

	suffixed := []struct {
		name    string
		heading string
	}{
		{name: "trailing space", heading: "## 2026-05-26 "},
		{name: "trailing tab", heading: "## 2026-05-26\t"},
	}

	boundaries := []struct {
		name  string
		build func(heading string) string
	}{
		{
			name:  "at end of file",
			build: func(heading string) string { return firstEntry + heading },
		},
		{
			name:  "at end of file with a final newline",
			build: func(heading string) string { return firstEntry + heading + "\n" },
		},
		{
			name:  "immediately before the final entry terminator",
			build: func(heading string) string { return firstEntry + heading + "\n\n---\n\n" },
		},
	}

	for _, sc := range suffixed {
		for _, bc := range boundaries {
			t.Run(sc.name+" "+bc.name, func(t *testing.T) {
				t.Parallel()

				// Direct: the bare heading is the whole entry chunk, so the
				// suffix is both the first and the last byte-bearing content.
				entry := NewReflectionEntry(bc.build(sc.heading)[len(firstEntry):])
				if entry.Date != "" {
					t.Fatalf("expected the suffixed heading to stay undated, got %q", entry.Date)
				}
				if entry.HeadingLine != sc.heading {
					t.Fatalf("expected the heading preserved verbatim, got %q", entry.HeadingLine)
				}
				if !hasDateDiagnostic(entry.Diagnostics) {
					t.Fatalf("expected a fatal date diagnostic, got %v", entry.Diagnostics)
				}

				content := bc.build(sc.heading)
				doc := ParseReflectionDocument(content)
				if len(doc.Entries) != 2 {
					t.Fatalf("expected 2 entries, got %d", len(doc.Entries))
				}
				boundary := doc.Entries[1]
				if boundary.Date != "" {
					t.Fatalf("expected the boundary entry undated, got %q", boundary.Date)
				}
				if boundary.HeadingLine != sc.heading {
					t.Fatalf("expected the boundary heading preserved verbatim, got %q", boundary.HeadingLine)
				}
				if !hasDateDiagnostic(boundary.Diagnostics) {
					t.Fatalf("expected a fatal date diagnostic, got %v", boundary.Diagnostics)
				}
				if _, ok := doc.Find("2026-05-26"); ok {
					t.Fatal("expected the boundary heading to be unselectable by its date prefix")
				}

				var invalidErr *InvalidDocumentError
				if err := ValidateReflectionDocument("reflections file", "reflections.md", doc); !errors.As(err, &invalidErr) {
					t.Fatalf("expected the document refused for writing, got %v", err)
				}

				// The suffix survives rendering and a second parse, so it can
				// never be tidied into a canonical heading on the way out.
				rendered := doc.Render()
				if !strings.Contains(rendered, sc.heading+"\n") {
					t.Fatalf("expected the suffixed heading preserved verbatim, got %q", rendered)
				}
				reparsed := ParseReflectionDocument(rendered)
				if again := reparsed.Render(); again != rendered {
					t.Fatalf("expected a stable round trip, got %q", again)
				}
				if _, ok := reparsed.Find("2026-05-26"); ok {
					t.Fatal("expected the boundary heading to stay undated across a round trip")
				}

				// On disk: every write is refused and nothing is touched.
				path := filepath.Join(t.TempDir(), "reflections.md")
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					t.Fatalf("seeding file: %v", err)
				}
				tx, err := FS{}.OpenReflectionFile(path)
				if err != nil {
					t.Fatalf("openReflectionFile returned error: %v", err)
				}
				if err := tx.Validate(); !errors.As(err, &invalidErr) {
					t.Fatalf("expected the stored file refused, got %v", err)
				}
				if err := tx.CommitEntries(tx.Entries()); !errors.As(err, &invalidErr) {
					t.Fatalf("expected the rewrite refused, got %v", err)
				}
				if got := readFileString(t, path); got != content {
					t.Fatalf("expected the file untouched after a refusal, got %q", got)
				}
				if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
					t.Fatalf("expected no backup after a refusal, got %v", err)
				}
			})
		}
	}
}
