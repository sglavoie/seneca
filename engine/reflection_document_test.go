package engine

import (
	"strings"
	"testing"
)

const legacyThreeQuestionEntry = "" +
	"## 2026-05-26\n\n" +
	"### What did I do badly today?\n" +
	"Rushed the review.\n\n" +
	"### What did I do well?\n" +
	"Walked at dawn.\n\n" +
	"### What could I do differently tomorrow?\n" +
	"Slow down.\n"

const v2EntryWithUnknownContent = "" +
	"## 2026-05-26\n" +
	"<!-- seneca:entry v=2 id=abc123 created=2026-05-26T21:04:05.123456789Z -->\n\n" +
	"A stray note the author typed above the questions.\n\n" +
	"### What did I do badly today?\n" +
	"Rushed the review.\n\n" +
	"<!-- seneca:role name=anchor -->\n" +
	"### What could I do differently tomorrow?\n" +
	"Slow down.\n\n" +
	"### Something nobody configured\n" +
	"Kept anyway.\n\n" +
	"<!-- seneca:follow-through source=2026-05-25 -->\n" +
	"### Follow-through\n" +
	"> Start with the hard draft.\n\n" +
	"I did, and it was easier than expected.\n"

func TestParseReflectionDocumentSplitsAndDatesLegacyEntries(t *testing.T) {
	t.Parallel()

	content := legacyThreeQuestionEntry + "\n---\n\n" +
		strings.Replace(legacyThreeQuestionEntry, "2026-05-26", "2026-05-27", 1) + "\n---\n\n"

	doc := ParseReflectionDocument(content)

	if len(doc.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(doc.Entries))
	}
	if doc.Entries[0].Date != "2026-05-26" || doc.Entries[1].Date != "2026-05-27" {
		t.Fatalf("unexpected dates %q and %q", doc.Entries[0].Date, doc.Entries[1].Date)
	}
	if len(doc.Diagnostics) != 0 {
		t.Fatalf("expected clean legacy parse, got %v", doc.Diagnostics)
	}
	if got := doc.Entries[0].Answers(DefaultReflectionQuestions()); got[0] != "Rushed the review." || got[2] != "Slow down." {
		t.Fatalf("unexpected legacy answers %q", got)
	}
}

func TestParseReflectionDocumentPreservesRawBytes(t *testing.T) {
	t.Parallel()

	// Entries keep their exact bytes; only the already-established separator
	// normalization (trim + `\n---\n\n`) applies.
	content := strings.TrimSpace(legacyThreeQuestionEntry) + EntryTerminator +
		strings.TrimSpace(v2EntryWithUnknownContent) + EntryTerminator

	doc := ParseReflectionDocument(content)

	if got := doc.Render(); got != content {
		t.Fatalf("expected byte-equivalent raw round trip, got:\n%q", got)
	}
}

func TestReflectionEntryAnswersHandlesAwkwardBodies(t *testing.T) {
	t.Parallel()

	questions := []string{"Multiline?", "Blank?", "Unicode?"}
	raw := "" +
		"## 2026-05-26\n\n" +
		"### Multiline?\n" +
		"First paragraph.\n\n" +
		"Second paragraph.\n\n" +
		"### Blank?\n\n" +
		"### Unicode?\n" +
		"Ich übte Gelassenheit — 平静 — ✅\n\n" +
		"### An unconfigured heading\n" +
		"Should not leak into an answer.\n"

	got := NewReflectionEntry(raw).Answers(questions)

	if got[0] != "First paragraph.\n\nSecond paragraph." {
		t.Fatalf("expected multiline answer preserved, got %q", got[0])
	}
	if got[1] != "" {
		t.Fatalf("expected blank answer, got %q", got[1])
	}
	if got[2] != "Ich übte Gelassenheit — 平静 — ✅" {
		t.Fatalf("expected unicode answer preserved, got %q", got[2])
	}
}

func TestReflectionEntryAnswersNormalizesCRLF(t *testing.T) {
	t.Parallel()

	questions := DefaultReflectionQuestions()
	raw := strings.ReplaceAll(legacyThreeQuestionEntry, "\n", "\r\n")

	entry := NewReflectionEntry(raw)
	got := entry.Answers(questions)

	if got[0] != "Rushed the review." || got[1] != "Walked at dawn." || got[2] != "Slow down." {
		t.Fatalf("expected CRLF entry to parse identically, got %q", got)
	}
	if entry.Raw != strings.TrimSpace(raw) {
		t.Fatal("expected CRLF raw text to stay byte-preserved")
	}
}

func TestParseReflectionEntryReadsV2Metadata(t *testing.T) {
	t.Parallel()

	entry := NewReflectionEntry(v2EntryWithUnknownContent)

	if !entry.Metadata.Present || entry.Metadata.Version != 2 {
		t.Fatalf("expected v2 metadata, got %+v", entry.Metadata)
	}
	if entry.Metadata.id != "abc123" {
		t.Fatalf("unexpected id %q", entry.Metadata.id)
	}
	if entry.Metadata.createdAt != "2026-05-26T21:04:05.123456789Z" {
		t.Fatalf("unexpected created timestamp %q", entry.Metadata.createdAt)
	}
	if len(entry.Diagnostics) != 0 {
		t.Fatalf("expected clean v2 parse, got %v", entry.Diagnostics)
	}

	follow, ok := entry.FollowThroughSection()
	if !ok {
		t.Fatal("expected a follow-through section")
	}
	if follow.SourceDate != "2026-05-25" {
		t.Fatalf("unexpected follow-through source %q", follow.SourceDate)
	}
	if follow.SourceIntention != "Start with the hard draft." {
		t.Fatalf("unexpected intention snapshot %q", follow.SourceIntention)
	}
	if follow.Response != "I did, and it was easier than expected." {
		t.Fatalf("unexpected follow-through response %q", follow.Response)
	}
}

func TestAnchorMarkerSurvivesQuestionReordering(t *testing.T) {
	t.Parallel()

	entry := NewReflectionEntry(v2EntryWithUnknownContent)

	// The anchor role rides on the section, so reordering the configured
	// questions must not move the anchor to a different answer.
	reordered := []string{
		"What could I do differently tomorrow?",
		"What did I do badly today?",
		"Something nobody configured",
	}
	if got := entry.AnchorAnswer(reordered, 3); got != "Slow down." {
		t.Fatalf("expected marker-driven anchor after reordering, got %q", got)
	}

	// Rewording the anchor question must not lose it either.
	reworded := []string{"What did I do badly today?", "A brand new question?"}
	if got := entry.AnchorAnswer(reworded, 2); got != "Slow down." {
		t.Fatalf("expected marker-driven anchor after rewording, got %q", got)
	}
}

func TestAnchorAnswerFallsBackToConfiguredPositionForLegacyEntries(t *testing.T) {
	t.Parallel()

	entry := NewReflectionEntry(legacyThreeQuestionEntry)
	questions := DefaultReflectionQuestions()

	if got := entry.AnchorAnswer(questions, 2); got != "Walked at dawn." {
		t.Fatalf("expected positional anchor, got %q", got)
	}
	if got := entry.AnchorAnswer(questions, 0); got != "" {
		t.Fatalf("expected disabled anchor to be empty, got %q", got)
	}
	if got := entry.AnchorAnswer(questions, 9); got != "" {
		t.Fatalf("expected out-of-range anchor to be empty, got %q", got)
	}
}

func TestApplyAnswersPreservesUnknownSectionOrder(t *testing.T) {
	t.Parallel()

	raw := "" +
		"## 2026-05-26\n\n" +
		"### What did I do badly today?\n" +
		"Old bad.\n\n" +
		"### A historical question nobody asks anymore\n" +
		"Keep me.\n\n" +
		"### What did I do well?\n" +
		"Old good.\n"

	entry := NewReflectionEntry(raw)
	questions := []string{"What did I do badly today?", "What did I do well?", "Newly configured question?"}
	entry.ApplyAnswers(questions, []string{"New bad.", "New good.", "New answer."}, 0)

	var headings []string
	for _, section := range entry.Sections {
		headings = append(headings, section.Heading)
	}
	want := []string{
		"What did I do badly today?",
		"A historical question nobody asks anymore",
		"What did I do well?",
		"Newly configured question?",
	}
	if !equalStrings(headings, want) {
		t.Fatalf("expected preserved order %q, got %q", want, headings)
	}
	if entry.Sections[1].Body() != "Keep me." {
		t.Fatalf("expected historical section untouched, got %q", entry.Sections[1].Body())
	}
	if got := entry.Answers(questions); !equalStrings(got, []string{"New bad.", "New good.", "New answer."}) {
		t.Fatalf("unexpected answers after edit: %q", got)
	}
}

func TestUpgradeToV2TouchesOnlyTheEditedEntry(t *testing.T) {
	t.Parallel()

	first := legacyThreeQuestionEntry
	second := strings.Replace(legacyThreeQuestionEntry, "2026-05-26", "2026-05-27", 1)
	third := strings.Replace(legacyThreeQuestionEntry, "2026-05-26", "2026-05-28", 1)
	content := first + "\n---\n\n" + second + "\n---\n\n" + third + "\n---\n\n"

	doc := ParseReflectionDocument(content)
	questions := DefaultReflectionQuestions()

	edited := doc.Entries[1]
	edited.ApplyAnswers(questions, []string{"Edited bad.", "Edited good.", "Edited carry."}, 3)
	edited.upgradeToV2()
	doc.Entries[1] = edited

	if doc.Entries[0].Raw != strings.TrimSpace(first) {
		t.Fatalf("expected first entry byte-identical, got %q", doc.Entries[0].Raw)
	}
	if doc.Entries[2].Raw != strings.TrimSpace(third) {
		t.Fatalf("expected third entry byte-identical, got %q", doc.Entries[2].Raw)
	}

	upgraded := doc.Entries[1].Raw
	if !strings.Contains(upgraded, "<!-- seneca:entry v=2 -->") {
		t.Fatalf("expected v2 entry marker, got:\n%s", upgraded)
	}
	if !strings.Contains(upgraded, "<!-- seneca:role name=anchor -->\n### What could I do differently tomorrow?") {
		t.Fatalf("expected anchor role on the configured anchor question, got:\n%s", upgraded)
	}
	if !strings.Contains(upgraded, "Edited carry.") {
		t.Fatalf("expected edited answers, got:\n%s", upgraded)
	}
	if strings.Contains(doc.Entries[0].Raw, "seneca:") || strings.Contains(doc.Entries[2].Raw, "seneca:") {
		t.Fatal("expected neighbouring legacy entries to stay unmarked")
	}
}
