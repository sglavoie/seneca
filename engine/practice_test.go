package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A practice pack is held to two promises: switching to one never changes how
// anything already written reads, and the journal remembers which pack each
// day was written under. A journal that never applies one is not changed at
// all.

// journalOn opens the same journal with the clock on another day of August
// 2026, the way the author comes back to it later.
func journalOn(t *testing.T, journal *Journal, day int) *Journal {
	t.Helper()

	later, err := Open(Options{ReflectionsPath: journal.ReflectionsPath(), Settings: journal.Settings(), Clock: fixedNow(day)})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	return later
}

func mustBuiltin(t *testing.T, id string) PracticePack {
	t.Helper()

	pack, ok := BuiltinPracticePack(id)
	if !ok {
		t.Fatalf("no built-in pack %q", id)
	}
	return pack
}

func mustApply(t *testing.T, journal *Journal, pack PracticePack) SettingsOutcome {
	t.Helper()

	outcome, err := journal.ApplyPracticePack(mustSettings(t, journal).Revision, pack)
	if err != nil {
		t.Fatalf("ApplyPracticePack(%s) returned error: %v", pack.ID, err)
	}
	return outcome
}

func practiceIDs(state SettingsState) string {
	var ids []string
	for _, period := range state.Practice {
		ids = append(ids, period.From+" "+period.Pack.ID)
	}
	return strings.Join(ids, ", ")
}

func TestBuiltinPacksAreValidAndRoundTrip(t *testing.T) {
	t.Parallel()

	want := []string{"stoic", "step-10", "examen", "gratitude", "virtues"}
	packs := BuiltinPracticePacks()
	if len(packs) != len(want) {
		t.Fatalf("expected %d built-in packs, got %d", len(want), len(packs))
	}
	for i, pack := range packs {
		if pack.ID != want[i] {
			t.Errorf("built-in pack %d is %q, want %q", i, pack.ID, want[i])
		}
		encoded, err := EncodePracticePack(pack)
		if err != nil {
			t.Fatalf("%s: EncodePracticePack returned error: %v", pack.ID, err)
		}
		parsed, err := ParsePracticePack(encoded)
		if err != nil {
			t.Fatalf("%s: ParsePracticePack returned error: %v", pack.ID, err)
		}
		again, _ := EncodePracticePack(parsed)
		if string(again) != string(encoded) {
			t.Errorf("%s: the pack does not encode to the same bytes twice", pack.ID)
		}
		if pack.Guidance == "" || pack.ReminderTime == "" || pack.AnchorQuestionIndex != len(pack.Questions) {
			t.Errorf("%s: expected guidance, a reminder time, and the last question as the anchor", pack.ID)
		}
	}

	// The Stoic pack is the default practice exactly, so applying it to a
	// journal that kept the defaults asks nothing new.
	stoic := mustBuiltin(t, "stoic")
	if !equalStrings(stoic.Questions, DefaultReflectionQuestions()) || stoic.AnchorQuestionIndex != defaultAnchorQuestionIndex(stoic.Questions) {
		t.Fatalf("expected the Stoic pack to ask the default questions, got %+v", stoic.Settings())
	}

	// A caller cannot change the built-ins through what it was given.
	packs[0].Questions[0] = "Changed?"
	if mustBuiltin(t, "stoic").Questions[0] == "Changed?" {
		t.Fatal("the built-in packs share their questions with callers")
	}
}

func TestAPackFileThatCannotBeUsedIsRefused(t *testing.T) {
	t.Parallel()

	valid := func(edit func(p map[string]string)) string {
		fields := map[string]string{
			"seneca_pack":           `1`,
			"id":                    `"custom"`,
			"version":               `1`,
			"name":                  `"Custom"`,
			"questions":             `["A?", "B?"]`,
			"anchor_question_index": `2`,
		}
		edit(fields)
		var parts []string
		for key, value := range fields {
			parts = append(parts, `"`+key+`": `+value)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	if _, err := ParsePracticePack([]byte(valid(func(map[string]string) {}))); err != nil {
		t.Fatalf("expected the minimal pack accepted, got %v", err)
	}

	cases := map[string]string{
		"not JSON":             "id = custom",
		"not a pack":           `{"version": 1, "reflection": {}}`,
		"a newer format":       valid(func(p map[string]string) { p["seneca_pack"] = `2` }),
		"an unknown field":     valid(func(p map[string]string) { p["theme"] = `"dark"` }),
		"an id with capitals":  valid(func(p map[string]string) { p["id"] = `"Custom"` }),
		"an id with a space":   valid(func(p map[string]string) { p["id"] = `"my pack"` }),
		"version zero":         valid(func(p map[string]string) { p["version"] = `0` }),
		"a blank name":         valid(func(p map[string]string) { p["name"] = `"  "` }),
		"a two-line name":      valid(func(p map[string]string) { p["name"] = `"A\nB"` }),
		"no questions":         valid(func(p map[string]string) { p["questions"] = `[]` }),
		"a repeated question":  valid(func(p map[string]string) { p["questions"] = `["A?", "A?"]` }),
		"an anchor too far":    valid(func(p map[string]string) { p["anchor_question_index"] = `3` }),
		"a reminder at 9pm":    valid(func(p map[string]string) { p["reminder_time"] = `"9pm"` }),
		"guidance too long":    valid(func(p map[string]string) { p["guidance"] = `"` + strings.Repeat("a", MaxPackGuidanceRunes+1) + `"` }),
		"a second document":    valid(func(map[string]string) {}) + "{}",
		"a description on two": valid(func(p map[string]string) { p["description"] = `"A\r\nB"` }),
	}
	for name, content := range cases {
		if _, err := ParsePracticePack([]byte(content)); !errors.Is(err, ErrInvalidPracticePack) {
			t.Errorf("%s: expected ErrInvalidPracticePack, got %v", name, err)
		}
	}
	if _, err := ParsePracticePack([]byte(`{"seneca_pack": 1, "guidance": "` + strings.Repeat(" ", maxPackFileBytes) + `"}`)); !errors.Is(err, ErrInvalidPracticePack) {
		t.Errorf("expected an oversized file refused, got %v", err)
	}
}

func TestAJournalThatNeverAppliesAPackWritesVersionOne(t *testing.T) {
	t.Parallel()

	journal := legacyAndMarkedJournal(t)
	state := mustInitialize(t, journal).State
	if _, err := journal.UpdateSettings(state.Revision, SettingsChange{AnchorQuestionIndex: anchorAt(2)}); err != nil {
		t.Fatalf("UpdateSettings returned error: %v", err)
	}
	got := readFileString(t, SettingsPathFor(journal.ReflectionsPath()))
	if !strings.HasPrefix(got, "{\n  \"version\": 1,\n") || strings.Contains(got, "practice") {
		t.Fatalf("expected a version 1 document with no practice, got:\n%s", got)
	}
	if state := mustSettings(t, journal); state.Version != 1 || len(state.Practice) != 0 {
		t.Fatalf("expected version 1 and no practice, got %+v", state)
	}
}

func TestApplyingAPackToAJournalWithoutSettingsKeepsItsLegacyReading(t *testing.T) {
	t.Parallel()

	journal := legacyAndMarkedJournal(t)
	if mustSettings(t, journal).Initialized {
		t.Fatal("expected the journal to start without settings of its own")
	}
	examen := mustBuiltin(t, "examen")
	outcome := mustApply(t, journal, examen)

	state := outcome.State
	if !outcome.Changed || !state.Initialized || state.Version != JournalSettingsVersion {
		t.Fatalf("expected the settings created at version %d, got %+v", JournalSettingsVersion, outcome)
	}
	if !sameSettings(state.Settings, examen.Settings()) || !sameSettings(state.Legacy, journalSettings) || !sameSettings(outcome.Previous, journalSettings) {
		t.Fatalf("expected the pack in force and the opened settings as legacy, got %+v", state)
	}
	if got := practiceIDs(state); got != "2026-08-25 examen" {
		t.Fatalf("expected the examen from today, got %q", got)
	}

	// Another device reads the same practice, whatever it was opened with.
	other := mustSettings(t, otherDevice(t, journal))
	if practiceIDs(other) != "2026-08-25 examen" || !sameSettings(other.Settings, examen.Settings()) {
		t.Fatalf("expected the other device to read the practice, got %+v", other)
	}
}

func TestSwitchingPacksMidJournalKeepsEveryPastEntryReadable(t *testing.T) {
	t.Parallel()

	journal := legacyAndMarkedJournal(t)
	mustInitialize(t, journal)
	written := readFileString(t, journal.ReflectionsPath())
	entriesBefore, err := journal.Entries()
	if err != nil {
		t.Fatalf("Entries returned error: %v", err)
	}
	contextsBefore := map[string]ContinuityContext{}
	for _, date := range []string{"2026-08-18", "2026-08-19", "2026-08-20"} {
		ctx, err := journal.Context(date, false)
		if err != nil {
			t.Fatalf("Context(%s) returned error: %v", date, err)
		}
		contextsBefore[date] = ctx
	}

	for i, id := range []string{"step-10", "gratitude", "virtues"} {
		later := journalOn(t, journal, 25+i)
		mustApply(t, later, mustBuiltin(t, id))

		if got := readFileString(t, journal.ReflectionsPath()); got != written {
			t.Fatalf("%s: expected the reflections file byte for byte as written", id)
		}
		entries, err := later.Entries()
		if err != nil {
			t.Fatalf("%s: Entries returned error: %v", id, err)
		}
		for n, entry := range entries {
			if got, want := entry.Answers(continuityQuestions), entriesBefore[n].Answers(continuityQuestions); !equalStrings(got, want) {
				t.Fatalf("%s: %s answers = %q, want %q", id, entry.Date, got, want)
			}
		}
		for date, before := range contextsBefore {
			ctx, err := later.Context(date, false)
			if err != nil {
				t.Fatalf("%s: Context(%s) returned error: %v", id, date, err)
			}
			ctx.PriorAge, before.PriorAge = "", ""
			if ctx != before {
				t.Fatalf("%s: %s reads differently under the pack:\n got %+v\nwant %+v", id, date, ctx, before)
			}
		}
	}

	// The form for a new day asks the pack's questions.
	draft, err := journalOn(t, journal, 27).PrepareReflection("2026-08-27", false)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	if !equalStrings(draft.Questions, mustBuiltin(t, "virtues").Questions) {
		t.Fatalf("expected the virtue questions, got %q", draft.Questions)
	}
}

func TestThePracticeHistoryKeepsOnePackADay(t *testing.T) {
	t.Parallel()

	journal := journalOn(t, legacyAndMarkedJournal(t), 20)
	stoic, step10, examen := mustBuiltin(t, "stoic"), mustBuiltin(t, "step-10"), mustBuiltin(t, "examen")

	mustApply(t, journal, stoic)
	// A second pack the same day replaces the first.
	state := mustApply(t, journal, step10).State
	if got := practiceIDs(state); got != "2026-08-20 step-10" {
		t.Fatalf("expected one pack on the 20th, got %q", got)
	}

	// Applying the pack in force writes nothing.
	path := SettingsPathFor(journal.ReflectionsPath())
	before := readFileString(t, path)
	if outcome := mustApply(t, journalOn(t, journal, 22), step10); outcome.Changed || readFileString(t, path) != before {
		t.Fatalf("expected applying the pack in force to change nothing, got %+v", outcome)
	}

	// Editing the questions keeps the pack in force; applying it again puts
	// its questions back without starting a new period.
	later := journalOn(t, journal, 23)
	edited, err := later.UpdateSettings(state.Revision, SettingsChange{Questions: append(step10.Questions, "One more?")})
	if err != nil {
		t.Fatalf("UpdateSettings returned error: %v", err)
	}
	if practiceIDs(edited.State) != "2026-08-20 step-10" {
		t.Fatalf("expected editing the questions to keep the practice, got %q", practiceIDs(edited.State))
	}
	restored := mustApply(t, later, step10)
	if !restored.Changed || !sameSettings(restored.State.Settings, step10.Settings()) || practiceIDs(restored.State) != "2026-08-20 step-10" {
		t.Fatalf("expected the pack's questions back under the same period, got %+v", restored.State)
	}

	// A new pack on a later day starts a period of its own.
	if got := practiceIDs(mustApply(t, journalOn(t, journal, 24), examen).State); got != "2026-08-20 step-10, 2026-08-24 examen" {
		t.Fatalf("expected two periods, got %q", got)
	}
	// A device whose clock is behind the newest period replaces it rather than
	// putting the history out of order.
	if got := practiceIDs(mustApply(t, journalOn(t, journal, 21), stoic).State); got != "2026-08-20 step-10, 2026-08-24 stoic" {
		t.Fatalf("expected the newest period replaced, got %q", got)
	}
}

func TestApplyingAPackOnAnOlderRevisionIsRefused(t *testing.T) {
	t.Parallel()

	journal := legacyAndMarkedJournal(t)
	stale := mustInitialize(t, journal).State.Revision
	if _, err := journal.UpdateSettings(stale, SettingsChange{AnchorQuestionIndex: anchorAt(1)}); err != nil {
		t.Fatalf("UpdateSettings returned error: %v", err)
	}
	path := SettingsPathFor(journal.ReflectionsPath())
	before := readFileString(t, path)

	var staleErr *StaleSettingsError
	if _, err := journal.ApplyPracticePack(stale, mustBuiltin(t, "examen")); !errors.As(err, &staleErr) {
		t.Fatalf("expected a StaleSettingsError, got %v", err)
	}
	if _, err := journal.ApplyPracticePack("", mustBuiltin(t, "examen")); err == nil {
		t.Fatal("expected a change without a base refused")
	}
	invalid := mustBuiltin(t, "examen")
	invalid.ID = "Not An ID"
	if _, err := journal.ApplyPracticePack(mustSettings(t, journal).Revision, invalid); !errors.Is(err, ErrInvalidPracticePack) {
		t.Fatalf("expected an invalid pack refused, got %v", err)
	}
	if readFileString(t, path) != before {
		t.Fatal("expected the settings left alone")
	}
}

func TestASettingsDocumentWhoseVersionDisagreesWithItsPracticeIsRefused(t *testing.T) {
	t.Parallel()

	unit := `{"questions": ["A?"], "anchor_question_index": 1}`
	pack, _ := EncodePracticePack(mustBuiltin(t, "gratitude"))
	period := `[{"from": "2026-08-20", "pack": ` + string(pack) + `}]`
	cases := map[string]string{
		"version 1 with a practice":    `{"version": 1, "reflection": ` + unit + `, "legacy": ` + unit + `, "practice": ` + period + `}`,
		"version 2 without a practice": `{"version": 2, "reflection": ` + unit + `, "legacy": ` + unit + `}`,
		"a practice out of order": `{"version": 2, "reflection": ` + unit + `, "legacy": ` + unit + `, "practice": [` +
			`{"from": "2026-08-21", "pack": ` + string(pack) + `}, {"from": "2026-08-20", "pack": ` + string(pack) + `}]}`,
		"a practice with no date": `{"version": 2, "reflection": ` + unit + `, "legacy": ` + unit + `, "practice": [{"from": "soon", "pack": ` + string(pack) + `}]}`,
	}
	for name, content := range cases {
		journal := newTestJournal(t, FS{}, 20)
		path := SettingsPathFor(journal.ReflectionsPath())
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("creating the metadata directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("writing the settings: %v", err)
		}
		var unusable *SettingsDocumentError
		if _, err := journal.EffectiveSettings(); !errors.As(err, &unusable) {
			t.Errorf("%s: expected EffectiveSettings to refuse, got %v", name, err)
		}
	}
}

func TestPracticeDuringDividesAWindowByThePackInForce(t *testing.T) {
	t.Parallel()

	stoic, step10 := mustBuiltin(t, "stoic"), mustBuiltin(t, "step-10")
	practice := []PracticePeriod{{From: "2026-08-15", Pack: stoic}, {From: "2026-08-20", Pack: step10}}
	describe := func(spans []PracticeSpan) string {
		var parts []string
		for _, span := range spans {
			name := "none"
			if span.Pack != nil {
				name = span.Pack.ID
			}
			parts = append(parts, span.From+".."+span.To+" "+name)
		}
		return strings.Join(parts, ", ")
	}

	cases := []struct{ start, end, want string }{
		{"2026-08-01", "2026-08-14", ""},
		{"2026-08-10", "2026-08-16", "2026-08-10..2026-08-14 none, 2026-08-15..2026-08-16 stoic"},
		{"2026-08-17", "2026-08-23", "2026-08-17..2026-08-19 stoic, 2026-08-20..2026-08-23 step-10"},
		{"2026-08-21", "2026-08-27", "2026-08-21..2026-08-27 step-10"},
		{"2026-08-01", "2026-08-31", "2026-08-01..2026-08-14 none, 2026-08-15..2026-08-19 stoic, 2026-08-20..2026-08-31 step-10"},
		{"2026-08-20", "2026-08-20", "2026-08-20..2026-08-20 step-10"},
	}
	for _, tc := range cases {
		if got := describe(PracticeDuring(practice, tc.start, tc.end)); got != tc.want {
			t.Errorf("PracticeDuring(%s, %s) = %q, want %q", tc.start, tc.end, got, tc.want)
		}
	}
	if spans := PracticeDuring(nil, "2026-08-01", "2026-08-31"); spans != nil {
		t.Errorf("expected no spans without a practice, got %+v", spans)
	}
}
