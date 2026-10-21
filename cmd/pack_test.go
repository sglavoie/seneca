package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The pack commands change the practice in one step and nothing else: the
// settings document is the only file written and committed, entries are never
// rewritten, and reviews hear the pack's guidance only for the days it covered.

func runPackCLI(t *testing.T, args ...string) (string, []string, error) {
	t.Helper()

	var committed []string
	previous := commitArchivePaths
	commitArchivePaths = func(_ string, paths ...string) { committed = append(committed, paths...) }
	defer func() { commitArchivePaths = previous }()

	out, err := runCompatCLI(t, append([]string{"pack"}, args...)...)
	return out, committed, err
}

func TestPackListNamesTheBuiltinPacksAndWritesNothing(t *testing.T) {
	j, _ := newSettingsJournal(t)
	before := snapshotDir(t, j.dir)

	out, committed, err := runPackCLI(t, "list")
	if err != nil {
		t.Fatalf("seneca pack list returned error: %v\n%s", err, out)
	}
	for _, pack := range engine.BuiltinPracticePacks() {
		if !strings.Contains(out, pack.ID) || !strings.Contains(out, pack.Name) {
			t.Errorf("expected %s listed:\n%s", pack.ID, out)
		}
	}
	if !strings.Contains(out, "Practice: no pack applied.") || strings.Contains(out, "* ") {
		t.Errorf("expected no pack marked in force:\n%s", out)
	}
	if after := snapshotDir(t, j.dir); !equalStrings(before, after) || len(committed) > 0 {
		t.Fatalf("expected a read-only listing, got %q and commits %q", after, committed)
	}
}

func TestPackApplyChangesOnlyTheSettingsAndCommitsThem(t *testing.T) {
	j, config := newSettingsJournal(t)
	reflections, archive := readFileString(t, j.reflections), readFileString(t, j.archive)
	settingsPath := engine.SettingsPathFor(j.reflections)
	step10, _ := engine.BuiltinPracticePack("step-10")

	out, committed, err := runPackCLI(t, "apply", "step-10")
	if err != nil {
		t.Fatalf("seneca pack apply returned error: %v\n%s", err, out)
	}
	for _, want := range append([]string{
		"Practice: Step 10 nightly inventory, from " + time.Now().Format("2006-01-02"),
		"Anchor: question 4",
		"Suggested reminder: 21:30",
	}, step10.Questions...) {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
	if len(committed) != 1 || committed[0] != settingsPath {
		t.Fatalf("expected exactly the settings document committed, got %q", committed)
	}
	if readFileString(t, j.reflections) != reflections || readFileString(t, j.archive) != archive || readFileString(t, configFilePath(j.home)) != config {
		t.Fatal("expected the reflections file, the archive, and config.toml byte for byte as they were")
	}

	// The journal recorded the configured questions as its legacy reading.
	state, initialized, err := engine.LoadJournalSettings(j.reflections)
	if err != nil || !initialized {
		t.Fatalf("expected initialized settings, got %v, %v", initialized, err)
	}
	if !equalStrings(state.Legacy.Questions, compatConfigQuestions) || !equalStrings(state.Settings.Questions, step10.Questions) {
		t.Fatalf("expected the configuration as legacy and the pack in force, got %+v", state)
	}

	// The listing and the settings both name it; applying it again is a no-op.
	if out, _, _ := runPackCLI(t); !strings.Contains(out, "* step-10") || !strings.Contains(out, "Practice: Step 10 nightly inventory (step-10), since") {
		t.Errorf("expected step-10 marked in force:\n%s", out)
	}
	if out, _, _ := runSettingsCLI(t); !strings.Contains(out, "(version 2)") || !strings.Contains(out, "Practice: Step 10 nightly inventory") {
		t.Errorf("expected the settings to name the practice:\n%s", out)
	}
	out, committed, err = runPackCLI(t, "apply", "step-10")
	if err != nil || len(committed) > 0 || !strings.Contains(out, "already the journal's practice") {
		t.Fatalf("expected the second apply to change nothing, got %v, %q:\n%s", err, committed, out)
	}

	// Editing the questions afterwards is said out loud.
	if _, _, err := runSettingsCLI(t, "set", "--anchor", "1"); err != nil {
		t.Fatalf("seneca settings set returned error: %v", err)
	}
	if out, _, _ := runSettingsCLI(t); !strings.Contains(out, "the questions were changed after it was applied") {
		t.Errorf("expected the edit noted:\n%s", out)
	}
}

func TestPackApplyReadsACustomPackFromAFile(t *testing.T) {
	j, _ := newSettingsJournal(t)

	exported, _, err := runPackCLI(t, "export", "gratitude")
	if err != nil {
		t.Fatalf("seneca pack export returned error: %v", err)
	}
	pack, err := engine.ParsePracticePack([]byte(exported))
	if err != nil {
		t.Fatalf("expected an exported pack that parses, got %v:\n%s", err, exported)
	}
	pack.ID, pack.Name, pack.Questions = "evening-three", "Evening three", []string{"What happened?", "What mattered?", "What next?"}
	encoded, err := engine.EncodePracticePack(pack)
	if err != nil {
		t.Fatalf("EncodePracticePack returned error: %v", err)
	}
	file := filepath.Join(t.TempDir(), "evening"+engine.PracticePackFileSuffix)
	if err := os.WriteFile(file, encoded, 0644); err != nil {
		t.Fatalf("writing the pack: %v", err)
	}

	if out, _, err := runPackCLI(t, "show", file); err != nil || !strings.Contains(out, "Evening three (evening-three, version 1)") || !strings.Contains(out, "What mattered?") {
		t.Fatalf("expected the file shown, got %v:\n%s", err, out)
	}
	if out, _, err := runPackCLI(t, "apply", file); err != nil {
		t.Fatalf("seneca pack apply returned error: %v\n%s", err, out)
	}
	state, _, _ := engine.LoadJournalSettings(j.reflections)
	if current, ok := state.CurrentPractice(); !ok || current.Pack.ID != "evening-three" || !equalStrings(state.Settings.Questions, pack.Questions) {
		t.Fatalf("expected the custom pack in force, got %+v", state)
	}
}

func TestPackApplyRefusesWhatItCannotReadAndWritesNothing(t *testing.T) {
	j, _ := newSettingsJournal(t)
	broken := filepath.Join(t.TempDir(), "broken"+engine.PracticePackFileSuffix)
	if err := os.WriteFile(broken, []byte(`{"seneca_pack": 1, "id": "broken"}`), 0644); err != nil {
		t.Fatalf("writing the pack: %v", err)
	}
	before := snapshotDir(t, j.dir)

	for _, arg := range []string{"no-such-pack", broken, filepath.Join(t.TempDir(), "missing.json")} {
		out, committed, err := runPackCLI(t, "apply", arg)
		if err == nil {
			t.Errorf("%s: expected a refusal:\n%s", arg, out)
		}
		if len(committed) > 0 {
			t.Errorf("%s: expected nothing committed, got %q", arg, committed)
		}
	}
	if after := snapshotDir(t, j.dir); !equalStrings(before, after) {
		t.Fatalf("expected nothing written, got %q", after)
	}
}

func TestTheReviewPromptCarriesTheGuidanceOfThePackInForce(t *testing.T) {
	t.Parallel()

	window := testReviewWindow(t, "2026-08-17", "2026-08-23", "2026-08-18", "2026-08-21")
	plain := buildReviewPrompt(window, reviewFocusContext{})
	if !strings.Contains(plain, "for a Stoic reflection CLI.") || strings.Contains(plain, "Practice") {
		t.Fatalf("expected a window no pack covered to keep its prompt:\n%s", plain)
	}

	stoic, _ := engine.BuiltinPracticePack("stoic")
	step10, _ := engine.BuiltinPracticePack("step-10")
	practice := []engine.PracticePeriod{{From: "2026-08-01", Pack: stoic}, {From: "2026-08-20", Pack: step10}}
	window.Practice = engine.PracticeDuring(practice, window.startDate(), window.endDate())
	prompt := buildReviewPrompt(window, reviewFocusContext{})
	assertPromptContains(t, prompt,
		"for a reflection journal.",
		"Practice in force during this period:\n",
		"- 2026-08-17 to 2026-08-19: Stoic evening review. Guidance: "+stoic.Guidance+"\n",
		"- 2026-08-20 to 2026-08-23: Step 10 nightly inventory. Guidance: "+step10.Guidance+"\n",
		"Read each entry under the guidance of the practice in force on its date.\n",
	)
	// Nothing else about the prompt moves: it is the plain prompt with the
	// subject renamed and the practice block added.
	block := prompt[strings.Index(prompt, "Practice in force"):strings.Index(prompt, "JSON schema:")]
	if got := strings.Replace(strings.Replace(prompt, block, "", 1), "a reflection journal", "a Stoic reflection CLI", 1); got != plain {
		t.Fatalf("expected only the subject and the practice block to differ:\n%s", prompt)
	}

	// Days before the first pack are said to have none, and an arc review is
	// told to read its sources by their windows.
	arc := testReviewWindow(t, "2026-07-01", "2026-09-30")
	arc.SourceKind = sourceReviews
	arc.Practice = engine.PracticeDuring(practice, arc.startDate(), arc.endDate())
	assertPromptContains(t, buildArcReviewPrompt(arc, reviewFocusContext{}),
		"- 2026-07-01 to 2026-07-31: no practice pack.\n",
		"- 2026-08-20 to 2026-09-30: Step 10 nightly inventory.",
		"Read each source review under the guidance of the practice in force during its window.\n",
	)
}

func TestAReviewWindowReadsThePracticeTheJournalRecorded(t *testing.T) {
	j, _ := newSettingsJournal(t)
	window := testReviewWindow(t, "2026-08-17", "2026-08-23")

	if spans, err := practiceForWindow(j.reflections, window); err != nil || spans != nil {
		t.Fatalf("expected no practice for a journal without settings, got %+v, %v", spans, err)
	}
	if _, _, err := runPackCLI(t, "apply", "examen"); err != nil {
		t.Fatalf("seneca pack apply returned error: %v", err)
	}
	today := time.Now()
	window = testReviewWindow(t, today.AddDate(0, 0, -6).Format("2006-01-02"), today.Format("2006-01-02"))
	spans, err := practiceForWindow(j.reflections, window)
	if err != nil || len(spans) != 2 || spans[0].Pack != nil || spans[1].Pack == nil || spans[1].Pack.ID != "examen" {
		t.Fatalf("expected the days before today without a pack and today under the examen, got %+v, %v", spans, err)
	}
}
