package cmd

import (
	"bytes"
	"errors"
	"github.com/sglavoie/seneca/engine"
	"github.com/spf13/pflag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The compatibility baseline is what the iPhone work must not move. Everything
// under testdata/compat is synthetic and frozen: a journal holding legacy and
// current entries with Markdown Seneca does not own, the review archive beside
// it, a configuration file naming every table Seneca reads, and the exact text
// the read-only commands print for them. docs/mobile/README.md says what each
// file covers.
//
// No test here reaches the author's own journal. Every one runs against a copy
// in a temporary directory, with HOME and SENECA_FILE both pointing into
// temporary directories.

const (
	compatFixtureDir = "testdata/compat"
	// compatToday is the day the baseline is read on: after every fixture entry
	// and archive record, so each relative date in the output is fixed.
	compatToday = "2026-04-02"
	// compatUpdateEnv rewrites the golden files from what the commands print
	// now. It exists for a deliberate output change, reviewed as a diff.
	compatUpdateEnv = "SENECA_UPDATE_COMPAT_GOLDEN"
)

// compatQuestions are the questions the fixture configuration declares. The
// two legacy fixture entries answer the built-in defaults instead, the way a
// journal looks after its author rewords the questions.
var compatQuestions = []string{
	"What did I notice today?",
	"What did I handle well?",
	"What will I carry into tomorrow?",
}

func compatFixture(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(compatFixtureDir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("reading compatibility fixture %s: %v", name, err)
	}
	return string(data)
}

// compatJournal is a private copy of the fixture journal directory.
type compatJournal struct {
	home        string
	dir         string
	reflections string
	archive     string
}

// newCompatJournal copies the fixture journal into a temporary directory and
// points HOME and SENECA_FILE at temporary locations. It uses t.Setenv, so a
// test that calls it cannot run in parallel.
func newCompatJournal(t *testing.T) compatJournal {
	t.Helper()

	j := newEmptyCompatJournal(t)
	j.write(t, j.reflections, compatFixture(t, "journal/reflections.md"))
	j.write(t, j.archive, compatFixture(t, "journal/seneca-reviews.md"))
	return j
}

// newEmptyCompatJournal is the same isolation with nothing on disk yet.
func newEmptyCompatJournal(t *testing.T) compatJournal {
	t.Helper()

	dir := t.TempDir()
	j := compatJournal{
		home:        t.TempDir(),
		dir:         dir,
		reflections: filepath.Join(dir, "reflections.md"),
		archive:     filepath.Join(dir, engine.ArchiveFileName),
	}
	// Doctor reports the directory's mode; pin it so the report does not vary
	// with the umask of whoever runs the tests.
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatalf("setting the journal directory mode: %v", err)
	}
	t.Setenv("HOME", j.home)
	t.Setenv("SENECA_FILE", j.reflections)

	// The guard the whole baseline rests on: whatever a command resolves, it
	// resolves inside this test's own directory.
	resolved, err := resolveFilePath()
	if err != nil {
		t.Fatalf("resolving the fixture journal: %v", err)
	}
	if resolved != j.reflections {
		t.Fatalf("the baseline would read %s instead of its fixture copy %s", resolved, j.reflections)
	}
	return j
}

func (j compatJournal) write(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// requireUntouched fails when anything in the journal directory or the home
// directory was created, rewritten, or removed since before was taken.
func (j compatJournal) requireUntouched(t *testing.T, before []string) {
	t.Helper()

	if after := snapshotDir(t, j.dir); !equalStrings(before, after) {
		t.Fatalf("expected the journal directory untouched:\nbefore %q\nafter  %q", before, after)
	}
	entries, err := os.ReadDir(j.home)
	if err != nil {
		t.Fatalf("reading the temporary home: %v", err)
	}
	if len(entries) > 0 {
		t.Fatalf("expected nothing created under HOME, found %d entries, first %q", len(entries), entries[0].Name())
	}
}

// doctorEnv is the doctor's world for this journal: no git, no harness, and a
// fixed today.
func (j compatJournal) doctorEnv() doctorEnv {
	return doctorEnv{
		home: j.home,
		getenv: func(key string) string {
			if key == "SENECA_FILE" {
				return j.reflections
			}
			return ""
		},
		lookPath: func(string) (string, error) { return "", exec.ErrNotFound },
		gitRepo:  func(string) error { return errors.New("not a git repository") },
		today:    compatToday,
	}
}

// resetCompatFlags clears every flag the baseline commands bind. Cobra keeps
// both the values and the Changed bits between Execute calls in one binary.
func resetCompatFlags() {
	resetShowFlags()
	resetExportFlags()
	focusDateFlag = ""
	focusHistoryLast = 10
	reviewHistoryLast, reviewHistoryPeriod = 1, ""
	usageJSON = false
	insightsCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	insightsDays, insightsFrom, insightsTo, insightsAll = engine.InsightsDays, "", "", false
	settingsQuestionsFlag, settingsAnchorFlag = nil, 0
	settingsSetCmd.Flags().VisitAll(func(f *pflag.Flag) {
		f.Changed = false
		// A repeated flag appends until it is told it is starting over.
		if list, ok := f.Value.(pflag.SliceValue); ok {
			_ = list.Replace(nil)
		}
	})
}

// runCompatCLI drives the real command tree and returns what it wrote to
// stdout. Output is not a terminal, so this is the plain form a pipe carries.
func runCompatCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()

	// PersistentPreRun silences usage on whichever command runs; put each one
	// back the way it was declared so a later test sees an untouched tree.
	commands := walkCommands(rootCmd)
	silenced := make(map[string]bool, len(commands))
	for path, c := range commands {
		silenced[path] = c.SilenceUsage
	}

	var out bytes.Buffer
	resetCompatFlags()
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		resetCompatFlags()
		for path, c := range commands {
			c.SilenceUsage = silenced[path]
		}
	})

	err := rootCmd.Execute()
	return out.String(), err
}

// requireCompatGolden compares output against a frozen file. The journal's
// temporary directory and the temporary home are the only values that vary per
// run, so both are replaced by placeholders on the way in.
func requireCompatGolden(t *testing.T, j compatJournal, name, got string) {
	t.Helper()

	got = strings.ReplaceAll(got, j.dir, "{{JOURNAL_DIR}}")
	got = strings.ReplaceAll(got, j.home, "{{HOME}}")
	path := filepath.Join(compatFixtureDir, "golden", name)
	if os.Getenv(compatUpdateEnv) != "" {
		if err := os.WriteFile(path, []byte(got), 0644); err != nil {
			t.Fatalf("updating %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v", name, err)
	}
	if got != string(want) {
		t.Fatalf("%s changed; this output is a compatibility contract (rerun with %s=1 only for a deliberate change)\n--- got ---\n%s\n--- want ---\n%s",
			name, compatUpdateEnv, got, want)
	}
}
