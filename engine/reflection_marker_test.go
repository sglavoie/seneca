package engine

import (
	"strings"
	"testing"
)

func TestMarkerAdjacencyRules(t *testing.T) {
	t.Parallel()

	// A blank line between a marker and a heading demotes the marker to body
	// text; it must not silently claim the following section.
	raw := "" +
		"## 2026-05-26\n\n" +
		"### First?\n" +
		"One.\n" +
		"<!-- seneca:role name=anchor -->\n\n" +
		"### Second?\n" +
		"Two.\n"

	entry := NewReflectionEntry(raw)
	for _, section := range entry.Sections {
		if section.role != "" {
			t.Fatalf("expected no bound role, got %q on %q", section.role, section.Heading)
		}
	}
	if got := entry.Sections[0].Body(); got != "One.\n<!-- seneca:role name=anchor -->" {
		t.Fatalf("expected demoted marker to stay in the body, got %q", got)
	}
}

func TestParseMarkerAttributesQuoting(t *testing.T) {
	t.Parallel()

	marker, ok := parseReflectionMarker(`<!--  seneca:entry   v=2 note="two words"  esc="a \"b\" \\c"  -->`)
	if !ok || marker.malformed {
		t.Fatalf("expected a well-formed marker, got %+v", marker)
	}
	if marker.kind != markerKindEntry {
		t.Fatalf("unexpected kind %q", marker.kind)
	}
	if marker.attr("note") != "two words" {
		t.Fatalf("unexpected note %q", marker.attr("note"))
	}
	if marker.attr("esc") != `a "b" \c` {
		t.Fatalf("unexpected escaped value %q", marker.attr("esc"))
	}
	if !equalStrings(marker.order, []string{"v", "note", "esc"}) {
		t.Fatalf("expected preserved attribute order, got %q", marker.order)
	}

	if _, ok := parseReflectionMarker("<!-- not a seneca marker -->"); ok {
		t.Fatal("expected non-Seneca comments to be ignored")
	}
	if marker, ok := parseReflectionMarker(`<!-- seneca:entry v="unterminated -->`); !ok || !marker.malformed {
		t.Fatalf("expected malformed marker, got %+v", marker)
	}
}

func TestFormatMarkerValueQuotesOnlyWhenNeeded(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"abc123":     "abc123",
		"2026-05-25": "2026-05-25",
		"":           `""`,
		"two words":  `"two words"`,
		`say "hi"`:   `"say \"hi\""`,
		`back\slash`: `"back\\slash"`,
		"a-->b":      `"a-->b"`,
	}
	for input, want := range cases {
		if got := formatMarkerValue(input); got != want {
			t.Fatalf("formatMarkerValue(%q) = %q, want %q", input, got, want)
		}
	}
}

func FuzzParseReflectionDocument(f *testing.F) {
	f.Add(legacyThreeQuestionEntry)
	f.Add(v2EntryWithUnknownContent)
	f.Add(legacyThreeQuestionEntry + "\n---\n\n" + v2EntryWithUnknownContent + "\n---\n\n")
	f.Add("")
	f.Add("---\n---\n---")
	f.Add("## 2026-13-45\n<!-- seneca:entry -->\n### a\n### a\n")
	f.Add("<!-- seneca:role name=\"\\\" -->\n### x\n")
	f.Add("## 2026-05-26\r\n\r\n### Q\r\nA\r\n")

	f.Fuzz(func(t *testing.T, content string) {
		doc := ParseReflectionDocument(content)

		// Raw-preserving rendering must never lose or mangle entry text.
		rendered := doc.Render()
		for _, entry := range doc.Entries {
			if entry.Raw != strings.TrimSpace(entry.Raw) {
				t.Fatalf("entry raw text is not trimmed: %q", entry.Raw)
			}
			if !strings.Contains(rendered, entry.Raw) {
				t.Fatalf("raw render dropped entry %q", entry.Raw)
			}
		}

		questions := DefaultReflectionQuestions()
		for _, entry := range doc.Entries {
			if got := entry.Answers(questions); len(got) != len(questions) {
				t.Fatalf("answers returned %d values for %d questions", len(got), len(questions))
			}
			entry.AnchorAnswer(questions, 3)
			entry.FollowThroughSection()
		}
		for _, d := range doc.Diagnostics {
			_ = d.String()
		}
	})
}

func FuzzReflectionEntryRenderParseStability(f *testing.F) {
	f.Add(legacyThreeQuestionEntry)
	f.Add(v2EntryWithUnknownContent)
	f.Add("## 2026-05-26\n<!-- seneca:entry v=2 -->\n\n### Q\nA\n")
	f.Add("no heading at all")
	f.Add("## 2026-05-26\n<!-- seneca:x -->\n<!-- seneca:entry v=2 -->\n\n### Q\nA\n")
	f.Add("## 2026-05-26\n\n<!-- seneca:follow-through source=2026-05-25 -->\n### Follow-through\n> a\n\nb\n")
	f.Add("### orphan\n---\n")

	f.Fuzz(func(t *testing.T, raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}

		for _, mode := range []RenderMode{renderLegacy, RenderCanonicalV2} {
			once := NewReflectionEntry(raw).render(mode)
			twice := NewReflectionEntry(once).render(mode)
			if once != twice {
				t.Fatalf("render/parse is not stable for mode %v:\nfirst:\n%q\nsecond:\n%q", mode, once, twice)
			}
		}
	})
}
