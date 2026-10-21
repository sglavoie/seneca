package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These are the end-to-end tests: one temporary directory holding a real
// reflections file and a real review archive, driven through the same functions
// the commands call. Only the terminal, the clock, the AI model, and the git
// commit are faked; every parse, validation, and write is the production path.
//
// The git seam is process-global, so nothing here calls t.Parallel.

// lifecycle is one author's world for the duration of a test.
type lifecycle struct {
	t       *testing.T
	dir     string
	path    string
	archive string
	commits []string
	// context is the continuity the last reflection was offered, which is what
	// the daily form would have shown above the questions.
	context engine.ContinuityContext
	out     strings.Builder
}

func newLifecycle(t *testing.T) *lifecycle {
	t.Helper()

	dir := t.TempDir()
	l := &lifecycle{
		t:       t,
		dir:     dir,
		path:    filepath.Join(dir, "reflections.md"),
		archive: filepath.Join(dir, engine.ArchiveFileName),
	}

	previous := commitArchivePaths
	commitArchivePaths = func(message string, paths ...string) {
		l.commits = append(l.commits, message)
	}
	t.Cleanup(func() { commitArchivePaths = previous })
	return l
}

// day is one evening at the keyboard: what the author typed, and whether they
// asked for a context-free form.
type day struct {
	date     string
	answers  []string
	follow   string
	blank    bool
	backfill bool
}

// reflect replays runReflection with the TUI removed: the same fail-closed
// validation, the same continuity context, and the same read-modify-write.
func (l *lifecycle) reflect(in day) error {
	seed, err := journalFS.OpenReflectionFile(l.path)
	if err != nil {
		return err
	}
	if err := seed.Validate(); err != nil {
		return err
	}

	context, err := engine.ContinuityContextFor(seed.Entries(), l.archive, in.date, continuityQuestions, 3, in.blank)
	if err != nil {
		return err
	}
	l.context = context

	tx, err := journalFS.OpenReflectionFile(l.path)
	if err != nil {
		return err
	}
	if err := tx.Validate(); err != nil {
		return err
	}

	entries, _, err := engine.ApplyReflectionResult(tx.Entries(), engine.ReflectionSave{
		Label:               tx.Label,
		Path:                l.path,
		Date:                in.date,
		Questions:           continuityQuestions,
		AnchorQuestionIndex: 3,
		Context:             context,
		Result:              engine.ReflectionResult{Saved: true, Answers: in.answers, FollowThrough: in.follow},
		Backfill:            in.backfill,
	})
	if err != nil {
		return err
	}
	return tx.CommitEntries(entries)
}

func (l *lifecycle) mustReflect(in day) {
	l.t.Helper()

	if err := l.reflect(in); err != nil {
		l.t.Fatalf("reflection on %s returned error: %v", in.date, err)
	}
}

func (l *lifecycle) mustSetFocus(now time.Time, text string) {
	l.t.Helper()

	if err := setFocus(&l.out, l.archive, now, []string{text}); err != nil {
		l.t.Fatalf("setFocus returned error: %v", err)
	}
}

func (l *lifecycle) mustClearFocus(now time.Time) {
	l.t.Helper()

	if err := clearFocus(&l.out, l.archive, now); err != nil {
		l.t.Fatalf("clearFocus returned error: %v", err)
	}
}

// review runs a whole review against the journal on disk: the window is derived
// from the real entries, and only the harness and the decision are faked.
func (l *lifecycle) review(now time.Time, response string, decision reviewDecision) error {
	l.t.Helper()

	data, err := os.ReadFile(l.path)
	if err != nil {
		l.t.Fatalf("reading %s: %v", l.path, err)
	}
	window, err := selectReviewWindow(engine.ParseReflectionDocument(string(data)).Entries, now, true, "", false)
	if err != nil {
		l.t.Fatalf("selectReviewWindow returned error: %v", err)
	}

	fake := &fakeReview{tty: true, response: response, decision: decision}
	err = executeReview(fake.runtime(now), reviewConfig{Harness: harnessClaude}, l.archive, window)
	l.out.WriteString(fake.out.String())
	return err
}

// seedWeeklyReview saves one weekly review through the production path, so an
// arc review later reads an archive `seneca review` actually wrote rather than
// a fixture shaped like one.
func (l *lifecycle) seedWeeklyReview(start, end string, dates ...string) {
	l.t.Helper()

	fake := &fakeReview{
		tty:      true,
		response: reviewPayloadCiting("null", dates[0]),
		decision: reviewDecision{kind: reviewSaveWithoutFocus},
	}
	now := reviewDay(l.t, end).Add(20 * time.Hour)
	if err := executeReview(fake.runtime(now), reviewConfig{Harness: harnessClaude}, l.archive, testReviewWindow(l.t, start, end, dates...)); err != nil {
		l.t.Fatalf("seeding the weekly review %s to %s: %v", start, end, err)
	}
	l.out.WriteString(fake.out.String())
}

// arcReview runs a whole quarterly review against the archive on disk: the
// window and its sources are derived from the reviews actually saved there, and
// only the terminal, the clock, the model, and the commit are faked.
func (l *lifecycle) arcReview(now time.Time, quarter string, tty bool, response string, decision reviewDecision) error {
	l.t.Helper()

	archive, _, err := engine.LoadReviewArchive(l.archive)
	if err != nil {
		l.t.Fatalf("loadReviewArchive returned error: %v", err)
	}
	// reviewRecords is newest first, which is the order selectArcWindow needs.
	window, err := selectArcWindow(archive.ReviewRecords(), now, quarter, true, "", false)
	if err != nil {
		l.t.Fatalf("selectArcWindow returned error: %v", err)
	}

	fake := &fakeReview{tty: tty, response: response, decision: decision}
	err = executeReview(fake.runtime(now), reviewConfig{Harness: harnessClaude}, l.archive, window)
	l.out.WriteString(fake.out.String())
	return err
}

func (l *lifecycle) journal() string {
	l.t.Helper()

	data, err := os.ReadFile(l.path)
	if err != nil {
		l.t.Fatalf("reading %s: %v", l.path, err)
	}
	return string(data)
}

// doctor diagnoses this world with nothing else available, so a report is about
// the author's data and never about the machine running the test.
func (l *lifecycle) doctor() doctorReport {
	l.t.Helper()

	env := doctorEnv{
		home: l.t.TempDir(),
		getenv: func(key string) string {
			if key == "SENECA_FILE" {
				return l.path
			}
			return ""
		},
		lookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
		gitRepo:  func(string) error { return errors.New("not a git repository") },
	}
	return diagnose(env)
}

// lifecycleReview is a complete, schema-valid model reply grounded in the days
// this journal actually contains.
func lifecycleReview(assessment string) string {
	return `{"focus_assessment":` + assessment + `,` +
		`"struggles":[{"theme":"Late starts","summary":"Deep work slipped past noon.",` +
		`"evidence":[{"date":"2026-08-21","snippet":"Handled 2026-08-21."}]}],` +
		`"wins":[{"theme":"Follow-through","summary":"You answered your own intentions.",` +
		`"evidence":[{"date":"2026-08-22","snippet":"Handled 2026-08-22."}]}],` +
		`"carry_forward":[],"focus_recommendation":"Open the editor before the inbox."}`
}

const lifecycleAssessment = `{"summary":"You mostly held the focus.","evidence":[{"date":"2026-08-21","snippet":"Handled 2026-08-21."}]}`

func TestLifecycleLegacyEntryGainsV2OnlyWhenItAnswersADay(t *testing.T) {
	l := newLifecycle(t)

	// A journal written by the pre-v2 binary: no markers anywhere.
	legacy := dayEntry("2026-08-20", "Noticed 20.", "Handled 20.", "Ship the parser.") + "\n---\n\n" +
		dayEntry("2026-08-21", "Noticed 21.", "Handled 21.", "Ship the archive.") + "\n---\n\n"
	if err := os.WriteFile(l.path, []byte(legacy), 0644); err != nil {
		t.Fatalf("seeding legacy journal: %v", err)
	}

	// Editing 2026-08-20 in place: it has no prior day to answer, so there is
	// no follow-through and the entry stays exactly as legacy as it was.
	l.mustReflect(day{date: "2026-08-20", answers: []string{"Noticed 20, revised.", "Handled 20.", "Ship the parser."}})
	if strings.Contains(l.journal(), "seneca:") {
		t.Fatalf("expected an edit without continuity to stay legacy:\n%s", l.journal())
	}

	// Editing 2026-08-21 does answer 2026-08-20's intention, and a
	// follow-through can only be expressed with markers.
	l.mustReflect(day{
		date:    "2026-08-21",
		answers: []string{"Noticed 21.", "Handled 21.", "Ship the archive."},
		follow:  "The parser shipped before lunch.",
	})

	journal := l.journal()
	if l.context.PriorDate != "2026-08-20" || l.context.PriorText != "Ship the parser." {
		t.Fatalf("expected the 2026-08-20 intention to be offered, got %+v", l.context)
	}
	for _, want := range []string{
		"<!-- seneca:follow-through source=2026-08-20 -->",
		"### Follow-through",
		"> Ship the parser.",
		"The parser shipped before lunch.",
	} {
		if !strings.Contains(journal, want) {
			t.Fatalf("expected the edited entry to contain %q:\n%s", want, journal)
		}
	}
	// The untouched day keeps its legacy bytes.
	if !strings.Contains(journal, "## 2026-08-20\n\n### Notice?") {
		t.Fatalf("expected 2026-08-20 to stay legacy:\n%s", journal)
	}
	requireNoDoctor(t, l.doctor(), doctorError)
}

func TestLifecycleManualFocusThenDailyFollowThrough(t *testing.T) {
	l := newLifecycle(t)
	l.mustSetFocus(archiveTime(20, 9, 0), "Protect the first hour.")

	l.mustReflect(day{date: "2026-08-20", answers: []string{"Noticed 20.", "Handled 20.", "Ship the parser."}})
	if l.context.Focus != "Protect the first hour." {
		t.Fatalf("expected the manual focus in the form, got %q", l.context.Focus)
	}
	if l.context.ShowFollowThrough {
		t.Fatal("expected no follow-through on the first day of the journal")
	}

	// The next evening answers the intention written the night before.
	l.mustReflect(day{
		date:    "2026-08-21",
		answers: []string{"Noticed 21.", "Handled 21.", "Ship the archive."},
		follow:  "Shipped it.",
	})
	if l.context.PriorDate != "2026-08-20" || l.context.Focus != "Protect the first hour." {
		t.Fatalf("expected both focus and prior intention, got %+v", l.context)
	}

	// A day later the stored follow-through does not become the prior
	// intention: the anchor answer does.
	l.mustReflect(day{
		date:    "2026-08-22",
		answers: []string{"Noticed 22.", "Handled 22.", "Write the handoff."},
		follow:  "Archive landed.",
	})
	if l.context.PriorText != "Ship the archive." {
		t.Fatalf("expected the 2026-08-21 anchor answer, got %q", l.context.PriorText)
	}

	// --blank hides everything and still preserves the stored section.
	l.mustReflect(day{date: "2026-08-22", answers: []string{"Noticed 22, revised.", "Handled 22.", "Write the handoff."}, blank: true})
	if l.context.HasContext() || l.context.ShowFollowThrough {
		t.Fatalf("expected --blank to hide all context, got %+v", l.context)
	}
	if !strings.Contains(l.journal(), "Archive landed.") {
		t.Fatalf("expected --blank to preserve the stored follow-through:\n%s", l.journal())
	}
	requireNoDoctor(t, l.doctor(), doctorError)
}

func TestLifecycleReviewAcceptedFocusReachesTheNextReflection(t *testing.T) {
	l := newLifecycle(t)
	l.mustSetFocus(archiveTime(20, 9, 0), "Ship the archive layer.")
	for _, date := range []string{"2026-08-20", "2026-08-21", "2026-08-22", "2026-08-23"} {
		l.mustReflect(day{
			date:    date,
			answers: []string{"Noticed " + date, "Handled " + date, "Carry from " + date},
			follow:  "Answered " + date,
		})
	}

	now := archiveTime(24, 20, 0)
	if err := l.review(now, lifecycleReview(lifecycleAssessment), reviewDecision{
		kind:      reviewAcceptFocus,
		focusText: "Open the editor before the inbox.",
	}); err != nil {
		t.Fatalf("review returned error: %v", err)
	}

	// The accepted focus takes effect the day it was accepted, and the next
	// reflection opens with it.
	l.mustReflect(day{date: "2026-08-24", answers: []string{"Noticed 24.", "Handled 24.", "Carry from 24."}, follow: "Answered 24."})
	if l.context.Focus != "Open the editor before the inbox." {
		t.Fatalf("expected the accepted focus in the next form, got %q", l.context.Focus)
	}

	// The review is readable back out of the archive, with the focus it set.
	var history strings.Builder
	if err := showReviewHistory(&history, l.archive, 1, false); err != nil {
		t.Fatalf("showReviewHistory returned error: %v", err)
	}
	for _, want := range []string{
		"Weekly review — 2026-08-18 to 2026-08-24",
		"### Accepted focus\n\nOpen the editor before the inbox.",
		"You mostly held the focus.",
	} {
		if !strings.Contains(history.String(), want) {
			t.Fatalf("expected the saved review to contain %q, got:\n%s", want, history.String())
		}
	}

	// A second review of the same window is a revision, never an overwrite. It
	// judges the focus accepted an hour ago, so its evidence can only be today.
	const acceptedAssessment = `{"summary":"Too early to tell.","evidence":[{"date":"2026-08-24","snippet":"Handled 2026-08-24"}]}`
	if err := l.review(archiveTime(24, 21, 0), lifecycleReview(acceptedAssessment), reviewDecision{kind: reviewSaveWithoutFocus}); err != nil {
		t.Fatalf("second review returned error: %v", err)
	}
	archive, _, err := engine.LoadReviewArchive(l.archive)
	if err != nil {
		t.Fatalf("loadReviewArchive returned error: %v", err)
	}
	records := archive.ReviewRecords()
	if len(records) != 2 {
		t.Fatalf("expected two saved reviews, got %d", len(records))
	}
	if records[0].Revises != records[1].ID {
		t.Fatalf("expected the newer review to revise the older, got %q", records[0].Revises)
	}
	// Saving without a focus leaves the accepted one in effect.
	if state := archive.FocusOn("2026-08-25"); state.Text != "Open the editor before the inbox." {
		t.Fatalf("expected the accepted focus to survive a save-only review, got %q", state.Text)
	}
	requireNoDoctor(t, l.doctor(), doctorError)
}
