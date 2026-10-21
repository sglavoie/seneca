package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureDirCreatesMissingDirectoryWhenConfirmed(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "nested", "reflections.md")

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stdin pipe: %v", err)
	}
	if _, err := writer.WriteString("y\n"); err != nil {
		t.Fatalf("writing confirmation: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing writer: %v", err)
	}
	defer reader.Close()

	originalStdin := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = originalStdin }()

	if err := ensureDir(target); err != nil {
		t.Fatalf("ensureDir returned error: %v", err)
	}

	info, err := os.Stat(filepath.Dir(target))
	if err != nil {
		t.Fatalf("stat created directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected created path to be a directory")
	}
}

func TestEnsureDirRejectsFilePathDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	notDir := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(notDir, []byte("x"), 0644); err != nil {
		t.Fatalf("writing sentinel file: %v", err)
	}

	err := ensureDir(filepath.Join(notDir, "reflections.md"))
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("expected not-a-directory error, got %v", err)
	}
}

func TestEnsureDirSurfacesUnexpectedStatErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	loop := filepath.Join(root, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatalf("creating symlink loop: %v", err)
	}

	err := ensureDir(filepath.Join(loop, "reflections.md"))
	if err == nil || !strings.Contains(err.Error(), "checking directory:") {
		t.Fatalf("expected wrapped stat error, got %v", err)
	}
}

func TestResolveFilePathPrefersEnvOverConfig(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	envPath := filepath.Join(home, "env", "reflections.md")
	writeConfigFile(t, home, "[seneca]\nreflections_path = \"reflections.md\"\n")

	got, err := resolveFilePathForHome(home, func(key string) string {
		if key == "SENECA_FILE" {
			return envPath
		}
		return ""
	})
	if err != nil {
		t.Fatalf("resolveFilePathForHome returned error: %v", err)
	}

	if got != envPath {
		t.Fatalf("expected env path %q, got %q", envPath, got)
	}
}

func TestResolveFilePathUsesConfigBeforeDefault(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	configPath := filepath.Join(home, "config", "reflections.md")
	writeConfigFile(t, home, "[seneca]\nreflections_path = "+quoteTOML(configPath)+"\n")

	got, err := resolveFilePathForHome(home, func(string) string {
		return ""
	})
	if err != nil {
		t.Fatalf("resolveFilePathForHome returned error: %v", err)
	}

	if got != configPath {
		t.Fatalf("expected config path %q, got %q", configPath, got)
	}
}

func TestResolveFilePathFallsBackToDefault(t *testing.T) {
	t.Parallel()

	home := t.TempDir()

	got, err := resolveFilePathForHome(home, func(string) string {
		return ""
	})
	if err != nil {
		t.Fatalf("resolveFilePathForHome returned error: %v", err)
	}
	want := filepath.Join(home, "Documents", "Seneca", "reflections.md")

	if got != want {
		t.Fatalf("expected default path %q, got %q", want, got)
	}
}

func quoteTOML(value string) string {
	return `"` + strings.ReplaceAll(value, `\`, `\\`) + `"`
}

func TestJournalDirForReflectionsPath(t *testing.T) {
	t.Parallel()

	reflectionsPath := filepath.Join(string(filepath.Separator), "Users", "you", "Documents", "Seneca", "reflections.md")
	want := filepath.Join(string(filepath.Separator), "Users", "you", "Documents", "Seneca")

	if got := journalDirForReflectionsPath(reflectionsPath); got != want {
		t.Fatalf("expected journal dir %q, got %q", want, got)
	}
}

func TestMostRecentAnchorUsesConfiguredQuestionIndex(t *testing.T) {
	t.Parallel()

	questions := []string{"Notice?", "Handle?", "Carry?"}
	entries := []engine.ReflectionEntry{
		engine.NewReflectionEntry(engine.FormatReflectionEntry("2026-05-24", questions, []string{"Old notice.", "Old handle.", "Old carry."})),
		engine.NewReflectionEntry(engine.FormatReflectionEntry("2026-05-25", questions, []string{"Recent notice.", "Recent handle.", "Recent carry."})),
		engine.NewReflectionEntry(engine.FormatReflectionEntry("2026-05-26", questions, []string{"Today notice.", "Today handle.", "Today carry."})),
	}

	got := engine.MostRecentAnchor(entries, "2026-05-26", questions, 2)
	if got.Text != "Recent handle." {
		t.Fatalf("expected configured anchor answer, got %q", got.Text)
	}
	if got.Date != "2026-05-25" {
		t.Fatalf("expected source date 2026-05-25, got %q", got.Date)
	}

	if disabled := engine.MostRecentAnchor(entries, "2026-05-26", questions, 0); disabled.Present() {
		t.Fatalf("expected disabled anchor to return nothing, got %+v", disabled)
	}
}

func TestMostRecentAnchorReachesBackAcrossGapsAndBlanks(t *testing.T) {
	t.Parallel()

	questions := []string{"Notice?", "Handle?", "Carry?"}
	entries := []engine.ReflectionEntry{
		engine.NewReflectionEntry(engine.FormatReflectionEntry("2026-05-10", questions, []string{"Old notice.", "Old handle.", "Reach me."})),
		// A week of skipped days, then a day whose anchor was left blank.
		engine.NewReflectionEntry(engine.FormatReflectionEntry("2026-05-18", questions, []string{"Late notice.", "Late handle.", ""})),
	}

	got := engine.MostRecentAnchor(entries, "2026-05-20", questions, 3)
	if got.Text != "Reach me." || got.Date != "2026-05-10" {
		t.Fatalf("expected the 2026-05-10 intention, got %+v", got)
	}
}

func TestMostRecentAnchorIgnoresLaterAndSameDayEntries(t *testing.T) {
	t.Parallel()

	questions := []string{"Notice?", "Handle?", "Carry?"}
	// Stored out of date order: the scan must pick by date, not by position.
	entries := []engine.ReflectionEntry{
		engine.NewReflectionEntry(engine.FormatReflectionEntry("2026-05-26", questions, []string{"a", "b", "Today's own intention."})),
		engine.NewReflectionEntry(engine.FormatReflectionEntry("2026-05-27", questions, []string{"a", "b", "Tomorrow's intention."})),
		engine.NewReflectionEntry(engine.FormatReflectionEntry("2026-05-25", questions, []string{"a", "b", "Yesterday's intention."})),
		engine.NewReflectionEntry(engine.FormatReflectionEntry("2026-05-24", questions, []string{"a", "b", "Older intention."})),
	}

	got := engine.MostRecentAnchor(entries, "2026-05-26", questions, 3)
	if got.Text != "Yesterday's intention." || got.Date != "2026-05-25" {
		t.Fatalf("expected the 2026-05-25 intention, got %+v", got)
	}
}

func TestMostRecentAnchorPrefersV2RoleMarkerOverConfiguredIndex(t *testing.T) {
	t.Parallel()

	// The entry was written when "Carry?" was the third question; the config
	// has since been reordered so that position holds a different question.
	marked := "" +
		"## 2026-05-25\n" +
		"<!-- seneca:entry v=2 -->\n\n" +
		"### Notice?\n" +
		"Noticed.\n\n" +
		"<!-- seneca:role name=anchor -->\n" +
		"### Carry?\n" +
		"Marked intention.\n\n" +
		"### Handle?\n" +
		"Handled.\n\n"

	reordered := []string{"Notice?", "Carry?", "Handle?"}
	got := engine.MostRecentAnchor([]engine.ReflectionEntry{engine.NewReflectionEntry(marked)}, "2026-05-26", reordered, 3)
	if got.Text != "Marked intention." || got.Date != "2026-05-25" {
		t.Fatalf("expected the role-marked section to win, got %+v", got)
	}

	// A legacy entry has no marker, so the configured index is the fallback.
	legacy := engine.NewReflectionEntry(engine.FormatReflectionEntry("2026-05-25", reordered, []string{"Noticed.", "Legacy intention.", "Handled."}))
	if fallback := engine.MostRecentAnchor([]engine.ReflectionEntry{legacy}, "2026-05-26", reordered, 2); fallback.Text != "Legacy intention." {
		t.Fatalf("expected the configured-index fallback, got %+v", fallback)
	}
}

func TestFormatReflectionEntryUsesConfiguredQuestions(t *testing.T) {
	t.Parallel()

	questions := []string{"What did I notice today?", "What should I carry into tomorrow?"}
	answers := []string{"A tight feedback loop.", "Start with the hard draft."}

	got := engine.FormatReflectionEntry("2026-05-26", questions, answers)

	want := "" +
		"## 2026-05-26\n\n" +
		"### What did I notice today?\n" +
		"A tight feedback loop.\n\n" +
		"### What should I carry into tomorrow?\n" +
		"Start with the hard draft.\n\n"
	if got != want {
		t.Fatalf("unexpected formatted entry:\n%s", got)
	}
}

func TestReflectionRefusesAFutureDateBeforeTouchingAnything(t *testing.T) {
	old := dateFlag
	t.Cleanup(func() { dateFlag = old })
	dateFlag = time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	err := runReflection(rootCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "is in the future") {
		t.Fatalf("expected a future date to be refused, got %v", err)
	}
}
