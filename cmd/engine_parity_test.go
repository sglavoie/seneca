package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/seneca/engine"
)

// The parity tests run one operation twice: through the command the author
// types, and through engine.Journal with nothing of the command line around it.
// Each side works on its own copy of the fixture journal, and the two
// directories must end up byte for byte the same — the reflections file, the
// archive, and every `.bak`. That is the property the phone depends on: it
// calls the engine directly and must write what the desktop would have written.

// parityPair is the fixture journal twice over: one copy the commands resolve
// through HOME and SENECA_FILE, one copy only the engine is pointed at.
type parityPair struct {
	cli    compatJournal
	mirror compatJournal
}

func newParityPair(t *testing.T) parityPair {
	t.Helper()

	cli := newCompatJournal(t)
	writeConfigFile(t, cli.home, compatFixture(t, "config.toml"))

	dir := t.TempDir()
	mirror := compatJournal{
		home:        cli.home,
		dir:         dir,
		reflections: filepath.Join(dir, filepath.Base(cli.reflections)),
		archive:     filepath.Join(dir, engine.ArchiveFileName),
	}
	mirror.write(t, mirror.reflections, readFileString(t, cli.reflections))
	mirror.write(t, mirror.archive, readFileString(t, cli.archive))
	return parityPair{cli: cli, mirror: mirror}
}

// seed puts the same content at the same relative name on both sides.
func (p parityPair) seed(t *testing.T, name, content string) {
	t.Helper()
	p.cli.write(t, filepath.Join(p.cli.dir, name), content)
	p.mirror.write(t, filepath.Join(p.mirror.dir, name), content)
}

// engineJournal opens the mirror the way a caller with no command line would:
// every input explicit.
func (p parityPair) engineJournal(t *testing.T, now time.Time) *engine.Journal {
	t.Helper()

	settings, err := loadReflectionSettings(p.cli.home)
	if err != nil {
		t.Fatalf("loading the fixture settings: %v", err)
	}
	journal, err := engine.Open(engine.Options{
		ReflectionsPath: p.mirror.reflections,
		Settings:        settings,
		Clock:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("engine.Open returned error: %v", err)
	}
	return journal
}

func parityTree(t *testing.T, dir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	tree := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("unexpected directory %s in %s", entry.Name(), dir)
		}
		tree[entry.Name()] = readFileString(t, filepath.Join(dir, entry.Name()))
	}
	return tree
}

// requireSameFiles fails when the two directories differ in any file's name or
// bytes, and returns the shared tree so a test can also say what changed.
func (p parityPair) requireSameFiles(t *testing.T) map[string]string {
	t.Helper()

	cli, mirror := parityTree(t, p.cli.dir), parityTree(t, p.mirror.dir)
	for name, want := range cli {
		got, ok := mirror[name]
		if !ok {
			t.Errorf("the command wrote %s and the engine did not", name)
			continue
		}
		if got != want {
			t.Errorf("%s differs between the command and the engine:\ncommand %q\nengine  %q", name, want, got)
		}
	}
	for name := range mirror {
		if _, ok := cli[name]; !ok {
			t.Errorf("the engine wrote %s and the command did not", name)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	return cli
}

// requireSameRefusal compares two errors with each side's own directory
// replaced, since the paths are the only thing allowed to differ.
func (p parityPair) requireSameRefusal(t *testing.T, cliErr, engineErr error) {
	t.Helper()

	if cliErr == nil || engineErr == nil {
		t.Fatalf("expected both sides to refuse, got command %v and engine %v", cliErr, engineErr)
	}
	cli := strings.ReplaceAll(cliErr.Error(), p.cli.dir, "<dir>")
	eng := strings.ReplaceAll(engineErr.Error(), p.mirror.dir, "<dir>")
	if cli != eng {
		t.Fatalf("the refusals differ:\ncommand %s\nengine  %s", cli, eng)
	}
}

// quietly runs a command with its stdout captured, so `go test` output stays
// readable, and with the archive git seam stubbed.
func quietly(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	previousCommit := commitArchivePaths
	commitArchivePaths = func(string, ...string) {}
	defer func() { commitArchivePaths = previousCommit }()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating a pipe: %v", err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = writer, writer
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, reader)
		done <- buf.String()
	}()

	runErr := fn()

	os.Stdout, os.Stderr = stdout, stderr
	_ = writer.Close()
	return <-done, runErr
}

// runDailyCLI drives the real `seneca --date` command with the terminal form
// replaced by a fixed answer, and returns what the form was shown.
func runDailyCLI(t *testing.T, date string, blank bool, result engine.ReflectionResult) (reflectionTUIConfig, error) {
	t.Helper()

	previousForm, previousDate, previousBlank := reflectionForm, dateFlag, blankFlag
	defer func() { reflectionForm, dateFlag, blankFlag = previousForm, previousDate, previousBlank }()

	var shown reflectionTUIConfig
	reflectionForm = func(cfg reflectionTUIConfig) (engine.ReflectionResult, error) {
		shown = cfg
		return result, nil
	}
	dateFlag, blankFlag = date, blank

	_, err := quietly(t, func() error { return runReflection(rootCmd, nil) })
	return shown, err
}

func TestParityDailyReflectionWritesTheSameJournal(t *testing.T) {
	cases := []struct {
		name   string
		date   string
		blank  bool
		result engine.ReflectionResult
		isEdit bool
	}{
		{
			name: "a new day after every entry",
			date: compatToday,
			result: engine.ReflectionResult{
				Saved:         true,
				Answers:       []string{"Noticed the parity.", "Kept both sides equal.", "Carry the engine forward."},
				FollowThrough: "Followed it through.",
			},
		},
		{
			name: "a backfill between existing entries",
			date: "2026-02-27",
			result: engine.ReflectionResult{
				Saved:   true,
				Answers: []string{"An earlier day.", "", "Written late."},
			},
		},
		{
			name:   "an edit of an existing day",
			date:   "2026-03-04",
			isEdit: true,
			result: engine.ReflectionResult{
				Saved:         true,
				Answers:       []string{"Rewritten.", "Rewritten too.", "And the anchor."},
				FollowThrough: "A new response.",
			},
		},
		{
			name:  "a blank day carries no context",
			date:  compatToday,
			blank: true,
			result: engine.ReflectionResult{
				Saved:   true,
				Answers: []string{"Only this.", "", ""},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pair := newParityPair(t)
			before := readFileString(t, pair.cli.reflections)

			shown, err := runDailyCLI(t, tc.date, tc.blank, tc.result)
			if err != nil {
				t.Fatalf("the command returned error: %v", err)
			}

			journal := pair.engineJournal(t, time.Now())
			draft, err := journal.PrepareReflection(tc.date, tc.blank)
			if err != nil {
				t.Fatalf("PrepareReflection returned error: %v", err)
			}
			// The form is shown exactly what the engine prepared.
			if !equalStrings(shown.initial, draft.Initial) {
				t.Fatalf("the form opened with %q, the engine prepared %q", shown.initial, draft.Initial)
			}
			if shown.context != draft.Context {
				t.Fatalf("the form showed context %+v, the engine prepared %+v", shown.context, draft.Context)
			}
			if !equalStrings(shown.questions, draft.Questions) || shown.date != draft.Date {
				t.Fatalf("the form and the engine disagree on the questions or the date")
			}
			if draft.IsEdit != tc.isEdit {
				t.Fatalf("expected IsEdit %v, got %v", tc.isEdit, draft.IsEdit)
			}

			saved, err := journal.SaveReflection(draft, tc.result, true)
			if err != nil {
				t.Fatalf("SaveReflection returned error: %v", err)
			}
			if saved.IsEdit != tc.isEdit {
				t.Fatalf("expected the save to report IsEdit %v, got %v", tc.isEdit, saved.IsEdit)
			}

			tree := pair.requireSameFiles(t)
			if tree[filepath.Base(pair.cli.reflections)] == before {
				t.Fatal("expected the reflection to change the journal")
			}
			if tree[filepath.Base(pair.cli.reflections)+engine.BackupSuffix] != before {
				t.Fatal("expected the backup to hold the journal as it was")
			}
		})
	}
}

func TestParityDailyReflectionRefusesTheSameFile(t *testing.T) {
	pair := newParityPair(t)
	// The same date twice: neither side may write, and both must say why in
	// the same words.
	duplicated := readFileString(t, pair.cli.reflections) + "## 2026-03-02\n\n### What did I notice today?\nAgain.\n"
	pair.seed(t, filepath.Base(pair.cli.reflections), duplicated)

	result := engine.ReflectionResult{Saved: true, Answers: []string{"Never written.", "", ""}}
	_, cliErr := runDailyCLI(t, compatToday, false, result)
	_, engineErr := pair.engineJournal(t, time.Now()).PrepareReflection(compatToday, false)

	pair.requireSameRefusal(t, cliErr, engineErr)
	if tree := pair.requireSameFiles(t); tree[filepath.Base(pair.cli.reflections)] != duplicated {
		t.Fatal("expected the refused journal to be untouched")
	}
}

func TestParitySortWritesTheSameJournal(t *testing.T) {
	pair := newParityPair(t)
	// Out of order, and with the legacy framing a rewrite normalizes.
	shuffled := compatFixture(t, "journal/incoming.md") + compatFixture(t, "legacy-framing.md")
	pair.seed(t, filepath.Base(pair.cli.reflections), shuffled)

	if _, err := quietly(t, func() error { return runSort(sortCmd, nil) }); err != nil {
		t.Fatalf("the command returned error: %v", err)
	}
	outcome, err := pair.engineJournal(t, time.Now()).Sort()
	if err != nil {
		t.Fatalf("Sort returned error: %v", err)
	}
	if !outcome.Changed || outcome.Count == 0 {
		t.Fatalf("expected the engine to report a reordering, got %+v", outcome)
	}
	settled := pair.requireSameFiles(t)

	// A second sort is a no-op on both sides: same report, nothing rewritten.
	output, err := quietly(t, func() error { return runSort(sortCmd, nil) })
	if err != nil {
		t.Fatalf("the second command returned error: %v", err)
	}
	if !strings.HasPrefix(output, "Already sorted:") {
		t.Fatalf("expected the command to report a no-op, got %q", output)
	}
	again, err := pair.engineJournal(t, time.Now()).Sort()
	if err != nil {
		t.Fatalf("the second Sort returned error: %v", err)
	}
	if again.Changed || again.Count != outcome.Count {
		t.Fatalf("expected the engine to report a no-op over %d entries, got %+v", outcome.Count, again)
	}
	after := pair.requireSameFiles(t)
	for name, content := range settled {
		if after[name] != content {
			t.Fatalf("expected a no-op sort to leave %s alone", name)
		}
	}
}

func TestParityMergeWritesTheSameJournalAndRefusesTheSameOverlap(t *testing.T) {
	pair := newParityPair(t)
	pair.seed(t, "incoming.md", compatFixture(t, "journal/incoming.md"))
	cliSource := filepath.Join(pair.cli.dir, "incoming.md")
	mirrorSource := filepath.Join(pair.mirror.dir, "incoming.md")

	previousDelete := deleteSource
	deleteSource = false
	defer func() { deleteSource = previousDelete }()

	if _, err := quietly(t, func() error { return runMerge(mergeCmd, []string{cliSource}) }); err != nil {
		t.Fatalf("the command returned error: %v", err)
	}
	outcome, err := pair.engineJournal(t, time.Now()).Merge(mirrorSource)
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	if outcome.Merged != 2 {
		t.Fatalf("expected the engine to report 2 merged entries, got %d", outcome.Merged)
	}
	merged := pair.requireSameFiles(t)

	// The source is now wholly inside the destination: a second merge overlaps
	// on every date, and neither side writes.
	_, cliErr := quietly(t, func() error { return runMerge(mergeCmd, []string{cliSource}) })
	_, engineErr := pair.engineJournal(t, time.Now()).Merge(mirrorSource)
	pair.requireSameRefusal(t, cliErr, engineErr)

	after := pair.requireSameFiles(t)
	for name, content := range merged {
		if after[name] != content {
			t.Fatalf("expected a refused merge to leave %s alone", name)
		}
	}
}

func TestParityFocusWritesTheSameArchive(t *testing.T) {
	pair := newParityPair(t)
	now := time.Date(2026, time.April, 2, 21, 30, 0, 0, time.Local)
	journal := pair.engineJournal(t, now)

	// set
	cliOut, err := quietly(t, func() error {
		return setFocus(os.Stdout, pair.cli.archive, now, []string{"  Finish", "the engine.  "})
	})
	if err != nil {
		t.Fatalf("focus set returned error: %v", err)
	}
	change, err := journal.SetFocus("  Finish the engine.  ")
	if err != nil {
		t.Fatalf("SetFocus returned error: %v", err)
	}
	if want := "Focus set for " + change.Effective + ":\n" + change.Text + "\n"; cliOut != want {
		t.Fatalf("the command printed %q, the engine reported %q", cliOut, want)
	}
	pair.requireSameFiles(t)

	// show
	var shown bytes.Buffer
	if err := showFocus(&shown, pair.cli.archive, compatToday, compatToday); err != nil {
		t.Fatalf("focus returned error: %v", err)
	}
	state, _, err := journal.Focus(compatToday)
	if err != nil {
		t.Fatalf("Focus returned error: %v", err)
	}
	if !state.Active || !strings.HasSuffix(shown.String(), ":\n"+state.Text+"\n") {
		t.Fatalf("the command showed %q, the engine resolved %+v", shown.String(), state)
	}

	// clear, a minute later so the two records order by creation time
	later := now.Add(time.Minute)
	journal = pair.engineJournal(t, later)
	cliOut, err = quietly(t, func() error { return clearFocus(os.Stdout, pair.cli.archive, later) })
	if err != nil {
		t.Fatalf("focus clear returned error: %v", err)
	}
	cleared, err := journal.ClearFocus()
	if err != nil {
		t.Fatalf("ClearFocus returned error: %v", err)
	}
	if want := "Focus cleared for " + cleared.Effective + " (was: " + cleared.Previous.Text + ")\n"; cliOut != want {
		t.Fatalf("the command printed %q, the engine reported %q", cliOut, want)
	}
	settled := pair.requireSameFiles(t)

	// clear again: a read-only no-op on both sides
	if _, err := quietly(t, func() error { return clearFocus(os.Stdout, pair.cli.archive, later) }); err != nil {
		t.Fatalf("the second focus clear returned error: %v", err)
	}
	again, err := journal.ClearFocus()
	if err != nil {
		t.Fatalf("the second ClearFocus returned error: %v", err)
	}
	if again.Changed {
		t.Fatal("expected the engine to report nothing to clear")
	}
	after := pair.requireSameFiles(t)
	for name, content := range settled {
		if after[name] != content {
			t.Fatalf("expected a no-op clear to leave %s alone", name)
		}
	}
}

func TestParityFocusRefusesTheSameArchive(t *testing.T) {
	pair := newParityPair(t)
	// A record whose content no longer matches its ID: appending would bury the
	// damage, so both sides refuse.
	original := readFileString(t, pair.cli.archive)
	damaged := strings.Replace(original, "Protect the first hour.", "Protect the first hour, edited afterwards.", 1)
	if damaged == original {
		t.Fatal("the fixture no longer holds the record this test damages")
	}
	pair.seed(t, engine.ArchiveFileName, damaged)
	now := time.Date(2026, time.April, 2, 21, 30, 0, 0, time.Local)

	_, cliErr := quietly(t, func() error { return setFocus(os.Stdout, pair.cli.archive, now, []string{"Never written."}) })
	_, engineErr := pair.engineJournal(t, now).SetFocus("Never written.")

	pair.requireSameRefusal(t, cliErr, engineErr)
	if tree := pair.requireSameFiles(t); tree[engine.ArchiveFileName] != damaged {
		t.Fatal("expected the refused archive to be untouched")
	}
}

func TestParityReviewSaveWritesTheSameArchive(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decision reviewDecision
	}{
		{"with an accepted focus", reviewDecision{kind: reviewAcceptFocus, focusText: savedFocusRecommendation}},
		{"without a focus change", reviewDecision{kind: reviewSaveWithoutFocus}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pair := newParityPair(t)
			now := time.Date(2026, time.August, 24, 9, 0, 0, 0, time.Local)
			window := savedWindow(t)

			focus, err := focusContextForWindow(pair.cli.archive, window)
			if err != nil {
				t.Fatalf("focusContextForWindow returned error: %v", err)
			}
			review, err := parseReviewResponse([]byte(reviewPayload("null")), newReviewGrounding(window, reviewFocusContext{}))
			if err != nil {
				t.Fatalf("parseReviewResponse returned error: %v", err)
			}

			if _, err := quietly(t, func() error {
				return saveReview(os.Stdout, pair.cli.archive, now, window, review, focus, tc.decision)
			}); err != nil {
				t.Fatalf("the review save returned error: %v", err)
			}

			saved, err := pair.engineJournal(t, now).SaveReview(engine.ReviewSave{
				Period:        window.Period,
				Start:         window.startDate(),
				End:           window.endDate(),
				Title:         window.Title,
				Body:          renderReviewMarkdown(window, review, focus, tc.decision),
				AcceptedFocus: tc.decision.acceptedFocus(),
			})
			if err != nil {
				t.Fatalf("SaveReview returned error: %v", err)
			}
			if saved.AcceptedFocus != tc.decision.acceptedFocus() {
				t.Fatalf("expected the engine to report focus %q, got %q", tc.decision.acceptedFocus(), saved.AcceptedFocus)
			}

			tree := pair.requireSameFiles(t)
			if !strings.Contains(tree[engine.ArchiveFileName], saved.Review.Review.ID) {
				t.Fatal("expected the archive to hold the record the engine reported")
			}
		})
	}
}

// What the read-only commands print is what the engine reads.
func TestParityReadsAgreeWithTheBaseline(t *testing.T) {
	pair := newParityPair(t)
	journal := pair.engineJournal(t, time.Now())

	entries, err := journal.Entries()
	if err != nil {
		t.Fatalf("Entries returned error: %v", err)
	}
	shown, err := runCompatCLI(t, "show", "--last", "1")
	if err != nil {
		t.Fatalf("show returned error: %v", err)
	}
	last := entries[len(entries)-1]
	if !strings.Contains(shown, strings.TrimSpace(last.Raw)) {
		t.Fatalf("show --last 1 printed %q, the engine's last entry is %q", shown, last.Raw)
	}

	validation, err := journal.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if !validation.ReflectionsExist || !validation.ArchiveExists || len(validation.Reflections.Entries) != len(entries) {
		t.Fatalf("expected Validate to read both documents, got %+v", validation)
	}

	reviews, reviewsErr := journal.Reviews()
	history, err := runCompatCLI(t, "review", "history", "--last", "100")
	if err != nil {
		t.Fatalf("review history returned error: %v", err)
	}
	if reviewsErr == nil {
		for _, record := range reviews {
			if !strings.Contains(history, engine.ShortRecordID(record.ID)) {
				t.Fatalf("review history does not list %s, which the engine returned", record.ID)
			}
		}
	} else if validation.ArchiveReason == nil {
		t.Fatalf("Reviews refused an archive Validate accepts: %v", reviewsErr)
	}
}
