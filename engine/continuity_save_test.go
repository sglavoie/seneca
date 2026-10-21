package engine

import (
	"strings"
	"testing"
)

// saveResult runs the modify half of the daily write and returns the rendered
// file, exactly as commitEntries would persist it.
func saveResult(t *testing.T, entries []ReflectionEntry, save ReflectionSave) (string, bool) {
	t.Helper()

	if save.Label == "" {
		save.Label = "reflections file"
	}
	if save.Path == "" {
		save.Path = "/tmp/reflections.md"
	}
	if save.Questions == nil {
		save.Questions = continuityQuestions
	}
	updated, isEdit, err := ApplyReflectionResult(entries, save)
	if err != nil {
		t.Fatalf("applyReflectionResult returned error: %v", err)
	}
	return RenderReflectionEntries(updated), isEdit
}

func TestSaveWritesCanonicalV2WithFollowThroughAndAnchorRole(t *testing.T) {
	t.Parallel()

	entries := journalEntries(t, dayEntry("2026-08-18", "Noticed.", "Handled.", "Finish the migration."))
	ctx := ContinuityContext{
		Focus:             "Protect the first hour.",
		PriorDate:         "2026-08-18",
		PriorText:         "Finish the migration.",
		ShowFollowThrough: true,
	}

	got, isEdit := saveResult(t, entries, ReflectionSave{
		Date:                "2026-08-24",
		AnchorQuestionIndex: 3,
		Context:             ctx,
		Result: ReflectionResult{
			Saved:         true,
			Answers:       []string{"Rushed.", "Paused first.", "Start smaller."},
			FollowThrough: "It took the whole morning.",
		},
	})
	if isEdit {
		t.Fatal("expected a create, not an edit")
	}

	for _, want := range []string{
		"<!-- seneca:entry v=2 -->",
		"<!-- seneca:role name=anchor -->\n### Carry?",
		"<!-- seneca:follow-through source=2026-08-18 -->\n### Follow-through",
		"> Finish the migration.",
		"It took the whole morning.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected saved file to contain %q:\n%s", want, got)
		}
	}
	// Focus is context, never an answer: it is not written into the entry.
	if strings.Contains(got, "Protect the first hour.") {
		t.Fatalf("expected the focus to stay out of the entry:\n%s", got)
	}
	// The untouched historical entry keeps its legacy bytes.
	if strings.Contains(strings.SplitN(got, "\n---\n", 2)[0], "seneca:") {
		t.Fatalf("expected the 2026-08-18 entry to stay legacy:\n%s", got)
	}

	// What was written parses back to the same snapshot and response.
	doc := ParseReflectionDocument(got)
	idx, found := doc.Find("2026-08-24")
	if !found {
		t.Fatalf("expected the new entry to parse back:\n%s", got)
	}
	follow, ok := doc.Entries[idx].FollowThroughSection()
	if !ok {
		t.Fatalf("expected a follow-through section:\n%s", got)
	}
	if follow.SourceDate != "2026-08-18" || follow.SourceIntention != "Finish the migration." || follow.Response != "It took the whole morning." {
		t.Fatalf("unexpected round-tripped follow-through: %+v", follow)
	}
	if len(doc.Diagnostics) != 0 {
		t.Fatalf("expected a clean parse, got %v", doc.Diagnostics)
	}
}

func TestSavePersistsTheFollowThroughSectionEvenWhenTheResponseIsBlank(t *testing.T) {
	t.Parallel()

	ctx := ContinuityContext{
		PriorDate:         "2026-08-18",
		PriorText:         "Finish the migration.",
		ShowFollowThrough: true,
	}

	got, _ := saveResult(t, nil, ReflectionSave{
		Date:                "2026-08-24",
		AnchorQuestionIndex: 3,
		Context:             ctx,
		Result: ReflectionResult{
			Saved:   true,
			Answers: []string{"Rushed.", "", ""},
		},
	})

	if !strings.Contains(got, "<!-- seneca:follow-through source=2026-08-18 -->") {
		t.Fatalf("expected the follow-through section to be recorded anyway:\n%s", got)
	}
	if !strings.Contains(got, "> Finish the migration.") {
		t.Fatalf("expected the source snapshot to be recorded:\n%s", got)
	}
}

func TestSourceSnapshotAloneCannotMakeAnEntrySaveable(t *testing.T) {
	t.Parallel()

	// A result carrying no author text is empty no matter how much context the
	// form displayed; only the follow-through response can flip it.
	blank := ReflectionResult{Saved: true, Answers: []string{"", "", ""}}
	if !blank.Empty() {
		t.Fatal("expected an all-blank result to be empty")
	}
	written := ReflectionResult{Saved: true, Answers: []string{"", "", ""}, FollowThrough: "It took the whole morning."}
	if written.Empty() {
		t.Fatal("expected follow-through text alone to make the entry saveable")
	}
	if (ReflectionResult{Saved: true, Answers: []string{"", "", ""}, FollowThrough: "   \n"}).Empty() != true {
		t.Fatal("expected whitespace-only follow-through to stay empty")
	}
}

func TestEditKeepsUnknownSectionsAndTheStoredSourceSnapshot(t *testing.T) {
	t.Parallel()

	stored := "" +
		"## 2026-08-24\n" +
		"<!-- seneca:entry v=2 -->\n\n" +
		"### Notice?\n" +
		"Noticed.\n\n" +
		"<!-- seneca:follow-through source=2026-08-18 -->\n" +
		"### Follow-through\n" +
		"> Finish the migration.\n\n" +
		"First pass.\n\n" +
		"### Dream log\n" +
		"A long hallway.\n\n" +
		"### Handle?\n" +
		"Handled.\n\n" +
		"### Carry?\n" +
		"Ship the parser.\n\n"

	entries := journalEntries(t, stored)
	ctx := ContinuityContext{
		PriorDate:         "2026-08-18",
		PriorText:         "Finish the migration.",
		FollowInitial:     "First pass.",
		ShowFollowThrough: true,
	}

	got, isEdit := saveResult(t, entries, ReflectionSave{
		Date:                "2026-08-24",
		AnchorQuestionIndex: 3,
		Context:             ctx,
		Result: ReflectionResult{
			Saved:         true,
			Answers:       []string{"Noticed again.", "Handled.", "Ship the parser."},
			FollowThrough: "Second pass, finished it.",
		},
	})
	if !isEdit {
		t.Fatal("expected an edit")
	}

	if !strings.Contains(got, "### Dream log\nA long hallway.") {
		t.Fatalf("expected the unknown section to survive:\n%s", got)
	}
	if !strings.Contains(got, "> Finish the migration.") {
		t.Fatalf("expected the stored snapshot to survive the edit:\n%s", got)
	}
	if !strings.Contains(got, "Second pass, finished it.") || strings.Contains(got, "First pass.") {
		t.Fatalf("expected the response to be replaced:\n%s", got)
	}
	// The follow-through stays where it was, before the unknown section.
	if strings.Index(got, "### Follow-through") > strings.Index(got, "### Dream log") {
		t.Fatalf("expected the follow-through to keep its position:\n%s", got)
	}
}

func TestBlankEditHidesButPreservesTheStoredFollowThrough(t *testing.T) {
	t.Parallel()

	stored := "" +
		"## 2026-08-24\n" +
		"<!-- seneca:entry v=2 -->\n\n" +
		"<!-- seneca:follow-through source=2026-08-18 -->\n" +
		"### Follow-through\n" +
		"> Finish the migration.\n\n" +
		"It took the whole morning.\n\n" +
		"### Notice?\n" +
		"Noticed.\n\n" +
		"### Handle?\n" +
		"Handled.\n\n" +
		"### Carry?\n" +
		"Ship the parser.\n\n"

	entries := journalEntries(t, stored)

	// --blank produced an empty context, so the form never showed the section.
	got, isEdit := saveResult(t, entries, ReflectionSave{
		Date:                "2026-08-24",
		AnchorQuestionIndex: 3,
		Context:             ContinuityContext{},
		Result: ReflectionResult{
			Saved:   true,
			Answers: []string{"Noticed differently.", "Handled.", "Ship the parser."},
		},
	})
	if !isEdit {
		t.Fatal("expected an edit")
	}

	if !strings.Contains(got, "<!-- seneca:follow-through source=2026-08-18 -->") {
		t.Fatalf("expected --blank to preserve the follow-through marker:\n%s", got)
	}
	if !strings.Contains(got, "> Finish the migration.\n\nIt took the whole morning.") {
		t.Fatalf("expected --blank to preserve the stored follow-through body:\n%s", got)
	}
	if !strings.Contains(got, "Noticed differently.") {
		t.Fatalf("expected the edited answer to land:\n%s", got)
	}
}

func TestSaveHandlesBackfillsAndChangedQuestionConfigurations(t *testing.T) {
	t.Parallel()

	entries := journalEntries(t,
		dayEntry("2026-08-18", "Noticed.", "Handled.", "Finish the migration."),
		dayEntry("2026-08-25", "Noticed.", "Handled.", "Ship the parser."),
	)

	got, isEdit := saveResult(t, entries, ReflectionSave{
		Date:                "2026-08-20",
		AnchorQuestionIndex: 3,
		Backfill:            true,
		Context: ContinuityContext{
			PriorDate:         "2026-08-18",
			PriorText:         "Finish the migration.",
			ShowFollowThrough: true,
		},
		Result: ReflectionResult{
			Saved:         true,
			Answers:       []string{"Backfilled.", "", ""},
			FollowThrough: "Half done.",
		},
	})
	if isEdit {
		t.Fatal("expected a create")
	}

	order := []string{"## 2026-08-18", "## 2026-08-20", "## 2026-08-25"}
	at := -1
	for _, heading := range order {
		i := strings.Index(got, heading)
		if i <= at {
			t.Fatalf("expected entries in date order, %q landed wrong:\n%s", heading, got)
		}
		at = i
	}

	// The author then reworded the questions and re-edited the backfilled day.
	reworded := []string{"Notice?", "Handle?", "What will I carry?"}
	edited, isEdit := saveResult(t, ParseReflectionDocument(got).Entries, ReflectionSave{
		Date:                "2026-08-20",
		Questions:           reworded,
		AnchorQuestionIndex: 3,
		Context: ContinuityContext{
			PriorDate:         "2026-08-18",
			PriorText:         "Finish the migration.",
			FollowInitial:     "Half done.",
			ShowFollowThrough: true,
		},
		Result: ReflectionResult{
			Saved:         true,
			Answers:       []string{"Backfilled.", "", "Rest earlier."},
			FollowThrough: "Half done.",
		},
	})
	if !isEdit {
		t.Fatal("expected the second save to edit")
	}
	if !strings.Contains(edited, "### What will I carry?\nRest earlier.") {
		t.Fatalf("expected the newly configured question to be appended:\n%s", edited)
	}
	if !strings.Contains(edited, "### Carry?") {
		t.Fatalf("expected the retired question's section to be kept:\n%s", edited)
	}
	// The anchor role follows the configured question, so tomorrow's context
	// reads the new one.
	if !strings.Contains(edited, "<!-- seneca:role name=anchor -->\n### What will I carry?") {
		t.Fatalf("expected the anchor role to move to the configured question:\n%s", edited)
	}
	prior := MostRecentAnchor(ParseReflectionDocument(edited).Entries, "2026-08-21", reworded, 3)
	if prior.Text != "Rest earlier." || prior.Date != "2026-08-20" {
		t.Fatalf("expected the marked anchor to drive tomorrow's context, got %+v", prior)
	}
}
