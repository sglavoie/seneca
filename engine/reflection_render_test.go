package engine

import (
	"strings"
	"testing"
)

func TestPreferredRenderModeKeepsLegacyEntriesLegacy(t *testing.T) {
	t.Parallel()

	if got := NewReflectionEntry(legacyThreeQuestionEntry).preferredRenderMode(); got != renderLegacy {
		t.Fatalf("expected legacy render mode, got %v", got)
	}
	if got := NewReflectionEntry(v2EntryWithUnknownContent).preferredRenderMode(); got != RenderCanonicalV2 {
		t.Fatalf("expected canonical v2 render mode, got %v", got)
	}
}

func TestRenderLegacyMatchesHistoricalEntryFormat(t *testing.T) {
	t.Parallel()

	questions := DefaultReflectionQuestions()
	entry := BuildReflectionEntry("2026-05-26", questions, []string{"Rushed the review.", "Walked at dawn.", "Slow down."}, 3)

	got := entry.render(renderLegacy)

	// Every section is terminated by a blank line, so the fixture's single
	// trailing newline becomes two.
	if want := legacyThreeQuestionEntry + "\n"; got != want {
		t.Fatalf("unexpected legacy render:\n%q\nwant:\n%q", got, want)
	}
	if strings.Contains(got, "seneca:") {
		t.Fatal("legacy render must not emit Seneca markers")
	}
	if got != FormatReflectionEntry("2026-05-26", questions, []string{"Rushed the review.", "Walked at dawn.", "Slow down."}) {
		t.Fatal("expected formatReflectionEntry to route through the document model")
	}
}

func TestRenderCanonicalIsIdempotent(t *testing.T) {
	t.Parallel()

	for name, fixture := range map[string]string{
		"legacy": legacyThreeQuestionEntry,
		"v2":     v2EntryWithUnknownContent,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			once := NewReflectionEntry(fixture).RenderCanonical()
			twice := NewReflectionEntry(once).RenderCanonical()
			if once != twice {
				t.Fatalf("canonical render is not idempotent:\nfirst:\n%q\nsecond:\n%q", once, twice)
			}
		})
	}
}

func TestRenderCanonicalKeepsUnknownContent(t *testing.T) {
	t.Parallel()

	rendered := NewReflectionEntry(v2EntryWithUnknownContent).RenderCanonical()

	for _, want := range []string{
		"A stray note the author typed above the questions.",
		"### Something nobody configured",
		"Kept anyway.",
		"<!-- seneca:role name=anchor -->",
		"<!-- seneca:follow-through source=2026-05-25 -->",
		"> Start with the hard draft.",
		"I did, and it was easier than expected.",
		"id=abc123",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected canonical render to keep %q, got:\n%s", want, rendered)
		}
	}
}

func TestRenderCanonicalPassesUnknownMarkersThrough(t *testing.T) {
	t.Parallel()

	raw := "" +
		"## 2026-05-26\n" +
		"<!-- seneca:entry v=2 future=\"a quoted \\\"value\\\"\" -->\n\n" +
		"<!-- seneca:someday tier=3 -->\n" +
		"### What did I do well?\n" +
		"Kept going.\n"

	entry := NewReflectionEntry(raw)
	rendered := entry.RenderCanonical()

	if !strings.Contains(rendered, "<!-- seneca:someday tier=3 -->") {
		t.Fatalf("expected unknown marker preserved, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `future="a quoted \"value\""`) {
		t.Fatalf("expected unknown entry attribute preserved, got:\n%s", rendered)
	}
	if NewReflectionEntry(rendered).RenderCanonical() != rendered {
		t.Fatal("expected unknown markers to round-trip stably")
	}
}

func TestNewFollowThroughSectionRendersCanonically(t *testing.T) {
	t.Parallel()

	section := newFollowThroughSection(FollowThrough{
		SourceDate:      "2026-05-25",
		SourceIntention: "Start with the hard draft.\n\nAnd stop at 21:00.",
		Response:        "I did.",
	})

	got := section.render(RenderCanonicalV2)
	want := "" +
		"<!-- seneca:follow-through source=2026-05-25 -->\n" +
		"### Follow-through\n" +
		"> Start with the hard draft.\n" +
		">\n" +
		"> And stop at 21:00.\n\n" +
		"I did.\n\n"
	if got != want {
		t.Fatalf("unexpected follow-through render:\n%q\nwant:\n%q", got, want)
	}

	reparsed := NewReflectionEntry("## 2026-05-26\n\n" + got)
	follow, ok := reparsed.FollowThroughSection()
	if !ok {
		t.Fatal("expected rendered follow-through to parse back")
	}
	if follow.SourceIntention != "Start with the hard draft.\n\nAnd stop at 21:00." {
		t.Fatalf("unexpected intention round trip %q", follow.SourceIntention)
	}
	if follow.Response != "I did." {
		t.Fatalf("unexpected response round trip %q", follow.Response)
	}
}
