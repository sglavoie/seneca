package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"strings"
	"testing"
)

// The settings commands move the questions and the anchor from the machine to
// the journal. These tests hold them to the promises that makes safe: nothing
// changes until the author says so, config.toml and the Markdown are never
// rewritten, and what was superseded is said out loud.

var compatConfigQuestions = []string{
	"What did I notice today?",
	"What did I handle well?",
	"What will I carry into tomorrow?",
}

// newSettingsJournal is the fixture journal with the fixture configuration.
func newSettingsJournal(t *testing.T) (compatJournal, string) {
	t.Helper()

	j := newCompatJournal(t)
	writeConfigFile(t, j.home, compatFixture(t, "config.toml"))
	return j, readFileString(t, configFilePath(j.home))
}

// runSettingsCLI drives a settings command with the git seam replaced, and
// returns its output and the paths it asked git to commit.
func runSettingsCLI(t *testing.T, args ...string) (string, []string, error) {
	t.Helper()

	var committed []string
	previous := commitArchivePaths
	commitArchivePaths = func(_ string, paths ...string) { committed = append(committed, paths...) }
	defer func() { commitArchivePaths = previous }()

	out, err := runCompatCLI(t, append([]string{"settings"}, args...)...)
	return out, committed, err
}

func TestSettingsShowReportsAnUninitializedJournalAndWritesNothing(t *testing.T) {
	j, _ := newSettingsJournal(t)
	before := snapshotDir(t, j.dir)

	out, committed, err := runSettingsCLI(t)
	if err != nil {
		t.Fatalf("seneca settings returned error: %v\n%s", err, out)
	}
	for _, want := range append([]string{
		"not initialized",
		engine.SettingsPathFor(j.reflections),
		configFilePath(j.home),
		"Anchor: question 3",
		"seneca settings init",
	}, compatConfigQuestions...) {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Superseded") {
		t.Errorf("nothing is superseded before initialization:\n%s", out)
	}
	if after := snapshotDir(t, j.dir); !equalStrings(before, after) || len(committed) > 0 {
		t.Fatalf("expected a read-only report, got %q and commits %q", after, committed)
	}

	// Changing settings the journal does not have yet is refused, write-free.
	out, _, err = runSettingsCLI(t, "set", "--anchor", "1")
	if !errors.Is(err, engine.ErrSettingsNotInitialized) || !strings.Contains(err.Error(), "seneca settings init") {
		t.Fatalf("expected the update to point at init, got %v\n%s", err, out)
	}
	if after := snapshotDir(t, j.dir); !equalStrings(before, after) {
		t.Fatalf("expected nothing written, got %q", after)
	}
}

func TestSettingsInitSeedsFromTheConfigurationAndLeavesItAndTheMarkdownAlone(t *testing.T) {
	j, config := newSettingsJournal(t)
	reflections, archive := readFileString(t, j.reflections), readFileString(t, j.archive)
	settingsPath := engine.SettingsPathFor(j.reflections)

	out, committed, err := runSettingsCLI(t, "init")
	if err != nil {
		t.Fatalf("seneca settings init returned error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "initialized in "+settingsPath) || !strings.Contains(out, configFilePath(j.home)) {
		t.Errorf("expected the report to name the document and its source:\n%s", out)
	}
	for _, key := range []string{"seneca.questions", "seneca.anchor_question_index"} {
		if !strings.Contains(out, "Superseded: "+key) {
			t.Errorf("expected %s reported as superseded:\n%s", key, out)
		}
	}
	if len(committed) != 1 || committed[0] != settingsPath {
		t.Fatalf("expected exactly the settings document committed, got %q", committed)
	}

	state, initialized, err := engine.LoadJournalSettings(j.reflections)
	if err != nil || !initialized {
		t.Fatalf("expected initialized settings, got %v, %v", initialized, err)
	}
	if !equalStrings(state.Settings.Questions, compatConfigQuestions) || state.Settings.AnchorQuestionIndex != 3 {
		t.Fatalf("expected the configured questions and anchor, got %+v", state.Settings)
	}

	if got := readFileString(t, configFilePath(j.home)); got != config {
		t.Fatal("expected config.toml byte for byte as it was")
	}
	if readFileString(t, j.reflections) != reflections || readFileString(t, j.archive) != archive {
		t.Fatal("expected the reflections file and the archive byte for byte as they were")
	}
	for _, path := range []string{j.reflections + engine.BackupSuffix, j.archive + engine.BackupSuffix} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected no backup %s, got %v", path, err)
		}
	}

	// A second init is a report, not a reseed, even after config.toml moves on.
	document := readFileString(t, settingsPath)
	writeConfigFile(t, j.home, "[seneca]\nquestions = [\"Only one?\"]\n")
	out, committed, err = runSettingsCLI(t, "init")
	if err != nil || len(committed) > 0 || !strings.Contains(out, "nothing was changed") {
		t.Fatalf("expected a no-op report, got %v, commits %q:\n%s", err, committed, out)
	}
	if readFileString(t, settingsPath) != document {
		t.Fatal("expected the settings document untouched by the second init")
	}
}

func TestInitializedSettingsOutrankConfigTomlEverywhere(t *testing.T) {
	j, _ := newSettingsJournal(t)
	if out, _, err := runSettingsCLI(t, "init"); err != nil {
		t.Fatalf("seneca settings init returned error: %v\n%s", err, out)
	}
	// The machine's configuration now disagrees with the journal.
	writeConfigFile(t, j.home, "[seneca]\nquestions = [\"Only this machine asks this?\"]\nanchor_question_index = 1\n")
	config := readFileString(t, configFilePath(j.home))

	out, _, err := runSettingsCLI(t)
	if err != nil {
		t.Fatalf("seneca settings returned error: %v\n%s", err, out)
	}
	if !strings.Contains(out, compatConfigQuestions[0]) || strings.Contains(out, "Only this machine") {
		t.Errorf("expected the journal's questions shown:\n%s", out)
	}
	if !strings.Contains(out, "Superseded: seneca.questions") || !strings.Contains(out, "Superseded: seneca.anchor_question_index") || !strings.Contains(out, "Revision: sha256:") {
		t.Errorf("expected the superseded keys and the revision reported:\n%s", out)
	}

	// The daily form asks the journal's questions.
	shown, err := runDailyCLI(t, compatToday, false, engine.ReflectionResult{
		Saved:   true,
		Answers: []string{"Noticed.", "Handled.", "Carried."},
	})
	if err != nil {
		t.Fatalf("the daily command returned error: %v", err)
	}
	if !equalStrings(shown.questions, compatConfigQuestions) {
		t.Fatalf("expected the form to ask the journal's questions, got %q", shown.questions)
	}
	if saved := readFileString(t, j.reflections); !strings.Contains(saved, "### "+compatConfigQuestions[2]+"\nCarried.") {
		t.Fatalf("expected the day saved under the journal's questions, got:\n%s", saved)
	}

	// Doctor says which settings are in force and what they replaced.
	report := diagnose(j.doctorEnv())
	requireDoctor(t, report, doctorOK, "journal settings "+engine.SettingsPathFor(j.reflections)+" are in force: 3 questions")
	requireDoctor(t, report, doctorInfo, "seneca.questions in "+configFilePath(j.home)+" is superseded")
	requireDoctor(t, report, doctorInfo, "seneca.anchor_question_index in "+configFilePath(j.home)+" is superseded")

	if got := readFileString(t, configFilePath(j.home)); got != config {
		t.Fatal("expected config.toml byte for byte as it was")
	}
}

func TestSettingsSetChangesQuestionsAndAnchorTogether(t *testing.T) {
	j, _ := newSettingsJournal(t)
	if out, _, err := runSettingsCLI(t, "init"); err != nil {
		t.Fatalf("seneca settings init returned error: %v\n%s", err, out)
	}
	settingsPath := engine.SettingsPathFor(j.reflections)
	reflections := readFileString(t, j.reflections)
	document := readFileString(t, settingsPath)

	refused := [][]string{
		{"set"},
		{"set", "--anchor", "4"},
		{"set", "--anchor", "-1"},
		{"set", "--question", "One?", "--question", "One?", "--anchor", "1"},
		{"set", "--question", "One?", "--question", " ", "--anchor", "1"},
		// The anchor question is gone and no anchor is stated.
		{"set", "--question", "One?", "--question", "Two?", "--question", "Three?"},
	}
	for _, args := range refused {
		out, committed, err := runSettingsCLI(t, args...)
		if err == nil {
			t.Errorf("seneca settings %s: expected a refusal:\n%s", strings.Join(args, " "), out)
		}
		if readFileString(t, settingsPath) != document || len(committed) > 0 {
			t.Fatalf("seneca settings %s: expected nothing written or committed", strings.Join(args, " "))
		}
	}

	out, committed, err := runSettingsCLI(t, "set",
		"--question", compatConfigQuestions[2], "--question", "What did I avoid?", "--anchor", "1")
	if err != nil {
		t.Fatalf("seneca settings set returned error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "updated in "+settingsPath) || !strings.Contains(out, "2. What did I avoid?") || !strings.Contains(out, "Anchor: question 1") {
		t.Errorf("unexpected report:\n%s", out)
	}
	if len(committed) != 1 || committed[0] != settingsPath {
		t.Fatalf("expected exactly the settings document committed, got %q", committed)
	}

	// The anchor alone, then the same again as a no-op.
	if out, _, err := runSettingsCLI(t, "set", "--anchor", "0"); err != nil || !strings.Contains(out, "Anchor: disabled") {
		t.Fatalf("expected the anchor disabled, got %v:\n%s", err, out)
	}
	out, committed, err = runSettingsCLI(t, "set", "--anchor", "0")
	if err != nil || len(committed) > 0 || !strings.Contains(out, "nothing was changed") {
		t.Fatalf("expected a no-op, got %v, commits %q:\n%s", err, committed, out)
	}

	state, _, err := engine.LoadJournalSettings(j.reflections)
	if err != nil {
		t.Fatalf("LoadJournalSettings returned error: %v", err)
	}
	if !equalStrings(state.Settings.Questions, []string{compatConfigQuestions[2], "What did I avoid?"}) || state.Settings.AnchorQuestionIndex != 0 {
		t.Fatalf("unexpected settings: %+v", state.Settings)
	}
	if readFileString(t, j.reflections) != reflections {
		t.Fatal("expected the reflections file byte for byte as it was")
	}
}

func TestDailySaveAfterTheQuestionsChangedIsRefusedKeptAndReviewed(t *testing.T) {
	const day = "2026-03-05"
	j, _ := newSettingsJournal(t)
	if out, _, err := runSettingsCLI(t, "init"); err != nil {
		t.Fatalf("seneca settings init returned error: %v\n%s", err, out)
	}
	background := backgroundWriter(t, j)
	state, err := background.EffectiveSettings()
	if err != nil {
		t.Fatalf("EffectiveSettings returned error: %v", err)
	}
	before := readFileString(t, j.reflections)
	reworded := []string{compatConfigQuestions[0], compatConfigQuestions[1], "What do I intend for tomorrow?"}

	// While the author writes, another device rewords the last question.
	_, err = runDaily(t, day, func(reflectionTUIConfig) engine.ReflectionResult {
		anchor := 3
		if _, err := background.UpdateSettings(state.Revision, engine.SettingsChange{Questions: reworded, AnchorQuestionIndex: &anchor}); err != nil {
			t.Fatalf("the background UpdateSettings returned error: %v", err)
		}
		return mine
	})
	var stale *engine.StaleSettingsError
	if !errors.As(err, &stale) || stale.Date != day {
		t.Fatalf("expected the save to be refused over the settings, got %v", err)
	}
	if got := readFileString(t, j.reflections); got != before {
		t.Fatal("expected the journal untouched")
	}
	kept := readFileString(t, engine.DraftPathFor(j.reflections, day))
	for _, want := range mine.Answers {
		if !strings.Contains(kept, want) {
			t.Fatalf("expected the kept draft to hold %q, got:\n%s", want, kept)
		}
	}

	// The next run shows the new questions, the answers that still have one,
	// and says what does not.
	var shown reflectionTUIConfig
	out, err := runDaily(t, day, resubmit(&shown))
	if err != nil {
		t.Fatalf("the reviewed save returned error: %v\n%s", err, out)
	}
	if !equalStrings(shown.questions, reworded) || !equalStrings(shown.initial, []string{mine.Answers[0], mine.Answers[1], ""}) {
		t.Fatalf("expected the kept answers lined up with the new questions, got %q / %q", shown.questions, shown.initial)
	}
	for _, want := range []string{"questions changed", "No longer asked", compatConfigQuestions[2], mine.Answers[2]} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
	if _, err := os.Stat(engine.DraftPathFor(j.reflections, day)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the draft discarded once the day was saved, got %v", err)
	}
}

// A journal nobody initialized behaves exactly as it did: config.toml decides,
// and no command creates settings as a side effect.
func TestAnUninitializedJournalStillFollowsConfigToml(t *testing.T) {
	j, _ := newSettingsJournal(t)

	shown, err := runDailyCLI(t, compatToday, false, engine.ReflectionResult{Saved: true, Answers: []string{"Noticed.", "", ""}})
	if err != nil {
		t.Fatalf("the daily command returned error: %v", err)
	}
	if !equalStrings(shown.questions, compatConfigQuestions) {
		t.Fatalf("expected the configured questions, got %q", shown.questions)
	}
	writeConfigFile(t, j.home, "[seneca]\nquestions = [\"Only one?\"]\n")
	shown, err = runDailyCLI(t, compatToday, false, engine.ReflectionResult{Saved: true, Answers: []string{"One."}})
	if err != nil {
		t.Fatalf("the daily command returned error: %v", err)
	}
	if !equalStrings(shown.questions, []string{"Only one?"}) {
		t.Fatalf("expected an edit to config.toml to take effect, got %q", shown.questions)
	}
	if _, err := os.Stat(engine.SettingsPathFor(j.reflections)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no settings document, got %v", err)
	}
	for _, result := range diagnose(j.doctorEnv()).results {
		if strings.Contains(result.detail, "journal settings") || strings.Contains(result.detail, "superseded") {
			t.Errorf("doctor mentioned journal settings for an uninitialized journal: %s", result.detail)
		}
	}
}
