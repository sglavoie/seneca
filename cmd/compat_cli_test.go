package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The read-only half of the baseline: what the commands that only read print
// for the synthetic journal, and the promise that reading changes nothing.

func TestCompatConfigurationFixtureLoadsEveryTable(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeConfigFile(t, home, compatFixture(t, "config.toml"))

	settings, err := loadReflectionSettings(home)
	if err != nil {
		t.Fatalf("loadReflectionSettings returned error: %v", err)
	}
	if !equalStrings(settings.Questions, compatQuestions) || settings.AnchorQuestionIndex != 3 {
		t.Fatalf("unexpected reflection settings: %+v", settings)
	}

	review, err := loadReviewSettings(home)
	if err != nil {
		t.Fatalf("loadReviewSettings returned error: %v", err)
	}
	if review.Harness != harnessClaude || review.Model != "synthetic-model" {
		t.Fatalf("unexpected review settings: %+v", review)
	}

	editor, err := loadEditorCommand(home)
	if err != nil {
		t.Fatalf("loadEditorCommand returned error: %v", err)
	}
	if want := []string{"synthetic-editor", "--wait"}; !equalStrings(editor, want) {
		t.Fatalf("editor command = %v, want %v", editor, want)
	}

	// The configured path wins over the default and loses to SENECA_FILE, and
	// the archive and the metadata directory follow whichever one wins.
	configured := filepath.Join(home, "journal", "reflections.md")
	none := func(string) string { return "" }
	path, err := resolveFilePathForHome(home, none)
	if err != nil {
		t.Fatalf("resolveFilePathForHome returned error: %v", err)
	}
	if path != configured {
		t.Fatalf("resolved path = %q, want the configured %q", path, configured)
	}

	override := filepath.Join(t.TempDir(), "elsewhere.md")
	path, err = resolveFilePathForHome(home, func(key string) string {
		if key == "SENECA_FILE" {
			return override
		}
		return ""
	})
	if err != nil {
		t.Fatalf("resolveFilePathForHome returned error: %v", err)
	}
	if path != override {
		t.Fatalf("resolved path = %q, want the SENECA_FILE override %q", path, override)
	}
	if got, want := engine.ArchivePathFor(path), filepath.Join(filepath.Dir(override), engine.ArchiveFileName); got != want {
		t.Fatalf("archive path = %q, want %q", got, want)
	}
}

// Two journals may share a directory. Their private metadata never collides,
// and the archive is, as it has always been, one per directory.
func TestCompatJournalMetadataIsScopedToTheReflectionsFilename(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(string(filepath.Separator), "synthetic", "journal")
	personal := filepath.Join(dir, "reflections.md")
	work := filepath.Join(dir, "work.md")

	if got, want := engine.JournalMetadataDirFor(personal), filepath.Join(dir, ".seneca", "journals", "reflections.md"); got != want {
		t.Fatalf("metadata directory = %q, want %q", got, want)
	}
	if got, want := engine.JournalMetadataDirFor(work), filepath.Join(dir, ".seneca", "journals", "work.md"); got != want {
		t.Fatalf("metadata directory = %q, want %q", got, want)
	}

	// Neither directory is the other, an ancestor of the other, or one of the
	// files Seneca already keeps beside a journal.
	a, b := engine.JournalMetadataDirFor(personal), engine.JournalMetadataDirFor(work)
	if a == b || strings.HasPrefix(a+string(filepath.Separator), b+string(filepath.Separator)) ||
		strings.HasPrefix(b+string(filepath.Separator), a+string(filepath.Separator)) {
		t.Fatalf("expected disjoint metadata directories, got %q and %q", a, b)
	}
	for _, journal := range []string{personal, work} {
		meta := engine.JournalMetadataDirFor(journal)
		for _, taken := range []string{journal, journal + engine.BackupSuffix, engine.ArchivePathFor(journal), engine.ArchivePathFor(journal) + engine.BackupSuffix, journalDirForReflectionsPath(journal)} {
			if meta == taken || strings.HasPrefix(taken, meta+string(filepath.Separator)) {
				t.Errorf("metadata directory %q collides with %q", meta, taken)
			}
		}
	}
	// State decided once for the directory sits where no journal filename can
	// reach it, whatever the journals are called.
	shared := engine.DirectoryMetadataDirFor(personal)
	if shared != engine.DirectoryMetadataDirFor(work) || shared != filepath.Join(dir, ".seneca", "directory") {
		t.Fatalf("expected one directory-scoped metadata directory, got %q and %q", shared, engine.DirectoryMetadataDirFor(work))
	}
	for _, name := range []string{"directory", "journals", ".seneca", engine.ArchiveFileName} {
		meta := engine.JournalMetadataDirFor(filepath.Join(dir, name))
		if meta == shared || strings.HasPrefix(shared, meta+string(filepath.Separator)) {
			t.Errorf("a journal named %q would own the directory-scoped metadata: %q", name, meta)
		}
	}
	if engine.ArchivePathFor(personal) != engine.ArchivePathFor(work) {
		t.Fatalf("expected co-located journals to share one archive, got %q and %q", engine.ArchivePathFor(personal), engine.ArchivePathFor(work))
	}

	// The same filename in another directory is another journal.
	elsewhere := filepath.Join(string(filepath.Separator), "synthetic", "other", "reflections.md")
	if engine.JournalMetadataDirFor(elsewhere) == engine.JournalMetadataDirFor(personal) {
		t.Fatal("expected the metadata directory to follow the journal's own directory")
	}
}

// compatReadOnlyCommands are the invocations whose output is frozen. Each one
// has a fixed answer for the fixture: nothing here depends on today's date or
// on chance.
var compatReadOnlyCommands = []struct {
	golden string
	args   []string
}{
	{"show.txt", []string{"show"}},
	{"show-last-2.txt", []string{"show", "--last", "2"}},
	{"show-date.txt", []string{"show", "--date", "2026-03-05"}},
	{"show-month.txt", []string{"show", "--month=2026-03"}},
	{"show-range.txt", []string{"show", "--from", "2026-03-03", "--to", "2026-03-04"}},
	{"show-search.txt", []string{"show", "--search", "walk"}},
	{"export-month.md", []string{"export", "--month", "2026-03"}},
	{"export-range.md", []string{"export", "--from", "2026-03-04", "--to", "2026-03-05"}},
	{"focus-history.txt", []string{"focus", "history", "--last", "10"}},
	{"review-history.txt", []string{"review", "history", "--last", "3"}},
	{"review-history-monthly.txt", []string{"review", "history", "--period", "monthly"}},
}

func TestCompatReadOnlyCommandsPrintTheirBaselineAndWriteNothing(t *testing.T) {
	for _, tc := range compatReadOnlyCommands {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			j := newCompatJournal(t)
			before := snapshotDir(t, j.dir)

			out, err := runCompatCLI(t, tc.args...)
			if err != nil {
				t.Fatalf("seneca %s returned error: %v", strings.Join(tc.args, " "), err)
			}
			requireCompatGolden(t, j, tc.golden, out)
			j.requireUntouched(t, before)
		})
	}
}

// `seneca focus` and `seneca doctor` phrase part of their answer relative to
// today, so they are frozen through the functions the commands call, with the
// day held still.
func TestCompatFocusPrintsItsBaselineAndWritesNothing(t *testing.T) {
	j := newCompatJournal(t)
	before := snapshotDir(t, j.dir)

	var out strings.Builder
	for _, on := range []string{"2026-02-28", "2026-03-10", "2026-03-15", compatToday} {
		fmt.Fprintf(&out, "$ seneca focus --date %s\n", on)
		if err := showFocus(&out, j.archive, on, compatToday); err != nil {
			t.Fatalf("showFocus(%s) returned error: %v", on, err)
		}
	}
	requireCompatGolden(t, j, "focus.txt", out.String())
	j.requireUntouched(t, before)
}

func TestCompatDoctorPrintsItsBaselineAndWritesNothing(t *testing.T) {
	j := newCompatJournal(t)
	before := snapshotDir(t, j.dir)

	var out strings.Builder
	if err := reportDoctor(&out, diagnose(j.doctorEnv())); err != nil {
		t.Fatalf("expected the fixture journal to pass doctor: %v\n%s", err, out.String())
	}
	requireCompatGolden(t, j, "doctor.txt", out.String())
	j.requireUntouched(t, before)
}

// A read must never be the thing that creates a journal, an archive, a backup,
// a configuration file, or the metadata directory.
func TestCompatReadOnlyCommandsCreateNothingWhenNothingExists(t *testing.T) {
	invocations := [][]string{
		{"show"},
		{"export", "--month", "2026-03"},
		{"focus"},
		{"focus", "clear"},
		{"focus", "history"},
		{"review", "history"},
		{"sort"},
		{"usage"},
		{"usage", "--json"},
	}
	for _, args := range invocations {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			j := newEmptyCompatJournal(t)

			// Some of these report and succeed, some refuse; none may write.
			_, _ = runCompatCLI(t, args...)
			j.requireUntouched(t, nil)
		})
	}

	t.Run("doctor", func(t *testing.T) {
		j := newEmptyCompatJournal(t)
		_ = diagnose(j.doctorEnv())
		j.requireUntouched(t, nil)
	})
}

// The fixture journal directory is the whole layout a journal has today. A new
// sibling file is a layout change, and belongs in docs/mobile/journal-layout.md
// before it belongs on disk.
func TestCompatJournalFixtureIsTheWholeLayout(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(filepath.Join(compatFixtureDir, "journal"))
	if err != nil {
		t.Fatalf("listing the fixture journal: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{"incoming.md", "reflections.md", engine.ArchiveFileName}; !equalStrings(names, want) {
		t.Fatalf("fixture journal holds %v, want %v", names, want)
	}
}
