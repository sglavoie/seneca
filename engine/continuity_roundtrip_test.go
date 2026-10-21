package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDailyWriteRoundTripsThroughRealPersistence walks two consecutive days
// through the same read-modify-write the command performs, against a real file.
func TestDailyWriteRoundTripsThroughRealPersistence(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "reflections.md")
	legacy := dayEntry("2026-08-18", "Noticed.", "Handled.", "Finish the migration.") +
		"\n### Dream log\nA long staircase.\n"
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatalf("seeding journal: %v", err)
	}

	archivePath := archiveWithFocus(t, 17, "Protect the first hour.")

	save := func(date string, answers []string, response string) {
		t.Helper()

		tx, err := FS{}.OpenReflectionFile(path)
		if err != nil {
			t.Fatalf("openReflectionFile returned error: %v", err)
		}
		if err := tx.Validate(); err != nil {
			t.Fatalf("validate returned error: %v", err)
		}
		ctx, err := ContinuityContextFor(tx.Entries(), archivePath, date, continuityQuestions, 3, false)
		if err != nil {
			t.Fatalf("continuityContextFor returned error: %v", err)
		}
		entries, _, err := ApplyReflectionResult(tx.Entries(), ReflectionSave{
			Label:               tx.Label,
			Path:                path,
			Date:                date,
			Questions:           continuityQuestions,
			AnchorQuestionIndex: 3,
			Context:             ctx,
			Result:              ReflectionResult{Saved: true, Answers: answers, FollowThrough: response},
		})
		if err != nil {
			t.Fatalf("applyReflectionResult returned error: %v", err)
		}
		if err := tx.CommitEntries(entries); err != nil {
			t.Fatalf("commitEntries returned error: %v", err)
		}
	}

	save("2026-08-19", []string{"Rushed.", "Paused.", "Start smaller."}, "It took the whole morning.")
	save("2026-08-20", []string{"Steadier.", "Said no once.", "Rest earlier."}, "Smaller worked.")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading journal: %v", err)
	}
	got := string(data)

	// Everything that was there before is still there, in place.
	if !strings.Contains(got, "### Dream log\nA long staircase.") {
		t.Fatalf("expected the unknown section preserved:\n%s", got)
	}
	if !strings.Contains(got, "### Carry?\nFinish the migration.") {
		t.Fatalf("expected the seeded entry preserved:\n%s", got)
	}
	// Each day answers the day before it, snapshot and all.
	if !strings.Contains(got, "<!-- seneca:follow-through source=2026-08-18 -->") ||
		!strings.Contains(got, "<!-- seneca:follow-through source=2026-08-19 -->") {
		t.Fatalf("expected a follow-through chain across both days:\n%s", got)
	}
	if !strings.Contains(got, "> Start smaller.") {
		t.Fatalf("expected the 2026-08-19 intention snapshotted into 2026-08-20:\n%s", got)
	}

	doc := ParseReflectionDocument(got)
	if len(doc.Diagnostics) != 0 {
		t.Fatalf("expected the written journal to parse clean, got %v", doc.Diagnostics)
	}
	if err := ValidateReflectionDocument("reflections file", path, doc); err != nil {
		t.Fatalf("expected the written journal to stay mutable: %v", err)
	}

	// The next evening's context comes from the marked anchor of the last day.
	ctx, err := ContinuityContextFor(doc.Entries, archivePath, "2026-08-21", continuityQuestions, 3, false)
	if err != nil {
		t.Fatalf("continuityContextFor returned error: %v", err)
	}
	if ctx.Focus != "Protect the first hour." {
		t.Fatalf("expected the active focus, got %q", ctx.Focus)
	}
	if ctx.PriorDate != "2026-08-20" || ctx.PriorText != "Rest earlier." {
		t.Fatalf("expected tomorrow to answer 2026-08-20, got %+v", ctx)
	}
}

func TestShowHidesMarkersButKeepsEveryWrittenLine(t *testing.T) {
	t.Parallel()

	raw := "" +
		"## 2026-08-24\n" +
		"<!-- seneca:entry v=2 -->\n\n" +
		"<!-- seneca:follow-through source=2026-08-18 -->\n" +
		"### Follow-through\n" +
		"> Finish the migration.\n\n" +
		"It took the whole morning.\n\n" +
		"<!-- seneca:role name=anchor -->\n" +
		"### Carry?\n" +
		"Ship the parser."

	got := strings.Join(VisibleEntryLines(raw), "\n")

	if strings.Contains(got, "seneca:") {
		t.Fatalf("expected markers hidden from the terminal:\n%s", got)
	}
	for _, want := range []string{"## 2026-08-24", "### Follow-through", "> Finish the migration.", "It took the whole morning.", "### Carry?", "Ship the parser."} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q to survive:\n%s", want, got)
		}
	}
}

func TestSaveWithoutContextLeavesALegacyEntryLegacy(t *testing.T) {
	t.Parallel()

	entries := journalEntries(t, dayEntry("2026-08-24", "Noticed.", "Handled.", "Ship the parser."))

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
	if strings.Contains(got, "seneca:") {
		t.Fatalf("expected an untouched legacy entry to stay legacy:\n%s", got)
	}
}
