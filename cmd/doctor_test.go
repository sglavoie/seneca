package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Doctor tests never read the author's real home, journal, archive, git index,
// or PATH: every environment dependency is injected through doctorEnv, and the
// one test that resolves a real binary builds a fake one in a temporary PATH.

// doctorHome is a self-contained world: a temporary home for the config file
// and a temporary directory holding the reflections file and its archive.
type doctorHome struct {
	home        string
	dir         string
	reflections string
	archive     string
}

func newDoctorHome(t *testing.T) doctorHome {
	t.Helper()

	dir := t.TempDir()
	return doctorHome{
		home:        t.TempDir(),
		dir:         dir,
		reflections: filepath.Join(dir, "reflections.md"),
		archive:     filepath.Join(dir, engine.ArchiveFileName),
	}
}

// env wires the world to this home with nothing else available: no git, no
// harness, and SENECA_FILE pointing at the temporary journal.
func (h doctorHome) env() doctorEnv {
	return doctorEnv{
		home: h.home,
		getenv: func(key string) string {
			if key == "SENECA_FILE" {
				return h.reflections
			}
			return ""
		},
		lookPath: func(string) (string, error) { return "", exec.ErrNotFound },
		gitRepo:  func(string) error { return errors.New("not a git repository") },
	}
}

func (h doctorHome) write(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// details returns every reported detail at one severity, so an assertion can
// name the state it expects instead of an index.
func (r doctorReport) details(severity doctorSeverity) []string {
	var found []string
	for _, result := range r.results {
		if result.severity == severity {
			found = append(found, result.check+": "+result.detail)
		}
	}
	return found
}

func requireDoctor(t *testing.T, report doctorReport, severity doctorSeverity, want string) {
	t.Helper()

	for _, detail := range report.details(severity) {
		if strings.Contains(detail, want) {
			return
		}
	}
	t.Fatalf("expected a %s containing %q, got:\n%s", severity.label(), want, report.render())
}

func requireNoDoctor(t *testing.T, report doctorReport, severity doctorSeverity) {
	t.Helper()

	if found := report.details(severity); len(found) > 0 {
		t.Fatalf("expected no %s results, got %q\n%s", severity.label(), found, report.render())
	}
}

const doctorHealthyJournal = "" +
	"## 2026-08-23\n" +
	"<!-- seneca:entry v=2 -->\n\n" +
	"### Notice?\nNoticed.\n\n" +
	"<!-- seneca:role name=anchor -->\n" +
	"### Carry?\nShip the archive layer.\n\n" +
	"---\n\n"

func TestDoctorReportsAHealthySetupWithoutErrors(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections, doctorHealthyJournal)
	mustAppend(t, h.archive, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Protect the first hour."))

	report := diagnose(h.env())

	requireNoDoctor(t, report, doctorError)
	requireDoctor(t, report, doctorOK, "1 entry parsed")
	requireDoctor(t, report, doctorOK, "every record id matches its content")
	requireDoctor(t, report, doctorOK, "focus active since 2026-08-24")
	if err := reportDoctor(&strings.Builder{}, report); err != nil {
		t.Fatalf("expected a warning-only report to succeed, got %v", err)
	}
}

func TestDoctorReportsAnEmptyWorldAsInformationOnly(t *testing.T) {
	h := newDoctorHome(t)

	report := diagnose(h.env())

	requireNoDoctor(t, report, doctorError)
	requireDoctor(t, report, doctorInfo, "no config file at")
	requireDoctor(t, report, doctorOK, "3 questions from the built-in defaults")
	requireDoctor(t, report, doctorInfo, "no reflections file yet")
	requireDoctor(t, report, doctorInfo, "no review archive yet")
	requireDoctor(t, report, doctorInfo, "no harness configured")
	requireDoctor(t, report, doctorWarn, "git is not on PATH")
}

func TestDoctorNamesTheConfigFileOnlyWhenItSuppliesTheQuestions(t *testing.T) {
	h := newDoctorHome(t)
	cfgPath := configFilePath(h.home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		t.Fatal(err)
	}

	h.write(t, cfgPath, "[review]\nharness = \"claude\"\n")
	requireDoctor(t, diagnose(h.env()), doctorOK, "3 questions from the built-in defaults")

	h.write(t, cfgPath, "[seneca]\nquestions = [\"One?\", \"Two?\"]\n")
	requireDoctor(t, diagnose(h.env()), doctorOK, "2 questions configured in "+cfgPath)
}

func TestDoctorNeverCreatesOrChangesAnything(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections, doctorHealthyJournal)
	mustAppend(t, h.archive, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Protect the first hour."))

	before := snapshotDir(t, h.dir)
	_ = diagnose(h.env())
	if after := snapshotDir(t, h.dir); !equalStrings(before, after) {
		t.Fatalf("expected doctor to leave the directory untouched:\nbefore %q\nafter  %q", before, after)
	}

	// The same must hold when the files do not exist: a diagnosis must never be
	// the thing that creates a journal or an archive.
	empty := newDoctorHome(t)
	_ = diagnose(empty.env())
	if entries := snapshotDir(t, empty.dir); len(entries) > 0 {
		t.Fatalf("expected doctor to create nothing, found %q", entries)
	}
	if _, err := os.Stat(configFilePath(empty.home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected doctor to leave the config uncreated, got %v", err)
	}
}

// snapshotDir lists a directory as name+content pairs, which is enough to catch
// a created file, a rewritten file, or a stray `.bak`.
func snapshotDir(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	snapshot := make([]string, 0, len(entries))
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		snapshot = append(snapshot, entry.Name()+"="+string(data))
	}
	return snapshot
}

func TestDoctorFailureIsAReportNotAUsageError(t *testing.T) {
	var out strings.Builder
	doctorCmd.SetOut(&out)
	doctorCmd.SetErr(&out)
	t.Cleanup(func() {
		doctorCmd.SetOut(nil)
		doctorCmd.SetErr(nil)
	})

	report := doctorReport{}
	report.add(doctorCheckReflections, doctorError, "duplicate entry dates: 2026-03-17")
	if err := reportDoctor(&out, report); err == nil {
		t.Fatal("expected an error report to fail")
	}
	if strings.Contains(out.String(), "Usage:") {
		t.Fatalf("expected no usage dump in a doctor report:\n%s", out.String())
	}
	if !doctorCmd.SilenceUsage || !doctorCmd.SilenceErrors {
		t.Fatal("expected doctor to silence cobra's usage and duplicate error printing")
	}
}

func TestDoctorPlacesDatesAgainstTodayAndNotesUnreviewedEntries(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections, doctorHealthyJournal+
		"## 2026-09-05\n<!-- seneca:entry v=2 -->\n\n### Notice?\nLater.\n\n---\n\n"+
		"## 2026-09-20\n<!-- seneca:entry v=2 -->\n\n### Notice?\nLatest.\n\n---\n\n")
	tx := mustOpenArchive(t, h.archive)
	review := tx.Archive.NewReviewRecord(archiveTime(24, 21, 0), engine.ReviewPeriodWeekly, "2026-08-17", "2026-08-23", "", "First pass.")
	if err := tx.AppendRecords(review, engine.NewFocusSetRecord(archiveTime(24, 21, 1), engine.FocusOriginManual, "", "Protect the first hour.")); err != nil {
		t.Fatalf("appending records: %v", err)
	}

	env := h.env()
	env.today = "2026-10-04"
	report := diagnose(env)

	requireNoDoctor(t, report, doctorError)
	requireDoctor(t, report, doctorInfo, "latest entry 2026-09-20 (14 days ago)")
	requireDoctor(t, report, doctorInfo, "latest saved review: weekly review, 2026-08-17 to 2026-08-23 (42 days ago)")
	requireDoctor(t, report, doctorOK, "focus active since 2026-08-24 (41 days ago, manual)")
	requireDoctor(t, report, doctorInfo, "2 entries written since the latest review ended 2026-08-23 (42 days ago)")

	// Within a month of the newest review there is nothing to mention.
	env.today = "2026-09-23"
	for _, detail := range diagnose(env).details(doctorInfo) {
		if strings.Contains(detail, "written since the latest review") {
			t.Fatalf("expected no review note a month after the review, got %q", detail)
		}
	}
}

func TestReviewDueNoteStaysSilentWithoutSomethingToReview(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	entries := []engine.ReflectionEntry{{Date: "2026-08-20"}, {Date: "2026-08-23"}}

	empty, _, err := engine.LoadReviewArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if note, due := engine.ReviewDueNote(entries, empty, "2026-12-01"); due {
		t.Fatalf("expected no note without any saved review, got %q", note)
	}

	tx := mustOpenArchive(t, path)
	if err := tx.AppendRecords(tx.Archive.NewReviewRecord(archiveTime(24, 21, 0), engine.ReviewPeriodWeekly, "2026-08-17", "2026-08-23", "", "Pass.")); err != nil {
		t.Fatal(err)
	}
	archive, _, err := engine.LoadReviewArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if note, due := engine.ReviewDueNote(entries, archive, "2026-12-01"); due {
		t.Fatalf("expected no note when every entry is already reviewed, got %q", note)
	}
	entries = append(entries, engine.ReflectionEntry{Date: "2026-11-30"})
	note, due := engine.ReviewDueNote(entries, archive, "2026-12-01")
	if !due || note != "1 entry written since the latest review ended 2026-08-23 (100 days ago); `seneca review --month=2026-11` takes stock of the latest" {
		t.Fatalf("unexpected note %q (due %v)", note, due)
	}

	var out strings.Builder
	printReviewDue(&out, entries, path, "2026-12-01")
	if !strings.HasSuffix(out.String(), "takes stock of the latest.\n") {
		t.Fatalf("expected the note printed after a save, got %q", out.String())
	}

	out.Reset()
	printReviewDue(&out, entries, filepath.Join(t.TempDir(), engine.ArchiveFileName), "2026-12-01")
	if out.Len() != 0 {
		t.Fatalf("expected nothing printed without an archive, got %q", out.String())
	}
}
