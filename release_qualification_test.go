package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/seneca/engine"
	"github.com/sglavoie/seneca/mobile/senecabridge"
)

// These tests are the automated half of the release qualification in
// docs/mobile/qualification.md: a synthetic journal several times the size of
// the author's own crossing between the Mac and a phone, and the documented
// rollback carried out step by step. Like the sync tests they run the real
// program against a journal in a Git repository of their own, with the phone
// played by the engine and the bridge; nothing here reaches a real journal.

// A journal kept daily for a few months holds a hundred-odd days and a couple
// of reviews. The synthetic one is about nine times that and spans three years.
const (
	scaleDays    = 1100
	scaleReviews = 150
	scaleStart   = "2022-06-01"
	// stepBudget bounds an everyday step (an exchange, a listing, a backup),
	// each well under a second at this size: generous on a loaded machine, and
	// still far below what a step that grew with the square of the journal
	// would take.
	stepBudget = 10 * time.Second
	// setupBudget bounds enrolling and joining. Each records every day as its
	// own durable object, flushed to disk one by one (about 1,100 files here,
	// 20 ms or more each on a Mac), so they take time in proportion to the
	// journal: half a minute to a minute and a half at this size, a few
	// seconds for a journal of a few months. They happen once per device.
	setupBudget = 5 * time.Minute
)

var scaleWords = strings.Fields("quiet morning walk draft lunch call friend tired patient rushed listened " +
	"kitchen evening plan refused promise slept early late garden letter café décision réunion " +
	"train rain notebook budget window phone deadline breath ten minutes careful honest")

// scaleSentence is a deterministic line of synthetic prose.
func scaleSentence(day, n int) string {
	words := make([]string, 0, 14)
	for i := 0; i < 9+(day+n)%6; i++ {
		words = append(words, scaleWords[(day*7+n*13+i*5)%len(scaleWords)])
	}
	line := strings.Join(words, " ")
	return "Synthetic: " + strings.ToUpper(line[:1]) + line[1:] + "."
}

func scaleAnswer(day, n int) string {
	return scaleSentence(day, n) + " " + scaleSentence(day, n+1) + " " + scaleSentence(day, n+2)
}

// scaleJournal writes the representative journal: three years of days in the
// shapes a long-kept journal accumulates. The first two thirds are legacy
// entries with no markers, as most of the author's are; every thirteenth carries preamble prose, a foreign comment,
// an unknown Seneca marker, a deeper heading, prose ending in three dashes,
// and a section no question names; every eleventh was saved with CRLF line
// breaks. Current entries carry the anchor role and, every third day, a
// follow-through of the day before. It returns the text and the CRLF days.
func scaleJournal(t *testing.T) (string, []string) {
	t.Helper()
	start, err := time.Parse(engine.CalendarDateLayout, scaleStart)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	var crlf []string
	previousAnchor, previousDate := "", ""
	for i := 0; i < scaleDays; i++ {
		date := start.AddDate(0, 0, i).Format(engine.CalendarDateLayout)
		var entry strings.Builder
		fmt.Fprintf(&entry, "## %s\n", date)
		odd := i%13 == 5
		if i < scaleDays*2/3 {
			entry.WriteString("\n")
			if odd {
				entry.WriteString("A line of preamble prose typed above the first question.\n\n<!-- an ordinary HTML comment Seneca does not own -->\n\n")
			}
			fmt.Fprintf(&entry, "### What did I do badly today?\n%s\n\n", scaleAnswer(i, 0))
			fmt.Fprintf(&entry, "### What did I do well?\n%s\n\n", scaleAnswer(i, 3))
			if odd {
				entry.WriteString("<!-- seneca:mood value=steady -->\n\n")
			}
			fmt.Fprintf(&entry, "### What could I do differently tomorrow?\n%s\n", scaleSentence(i, 6))
			if odd {
				entry.WriteString("\n#### A deeper heading inside an answer\nStill part of the answer above ---\n")
			}
			if i%2 == 1 {
				fmt.Fprintf(&entry, "\n### Dream log\n%s\n", scaleSentence(i, 9))
			}
			previousAnchor = ""
		} else {
			entry.WriteString("<!-- seneca:entry v=2 -->\n\n")
			if odd {
				entry.WriteString("A line of preamble prose typed above the first question.\n\n")
			}
			if previousAnchor != "" && i%3 == 0 {
				fmt.Fprintf(&entry, "<!-- seneca:follow-through source=%s -->\n### Follow-through\n> %s\n\n%s\n\n", previousDate, previousAnchor, scaleSentence(i, 1))
			}
			fmt.Fprintf(&entry, "### What did I notice today?\n%s\n\n", scaleAnswer(i, 0))
			if odd {
				entry.WriteString("<!-- seneca:mood value=steady -->\n")
			}
			fmt.Fprintf(&entry, "### What did I handle well?\n%s\n\n", scaleAnswer(i, 3))
			previousAnchor = scaleSentence(i, 6)
			fmt.Fprintf(&entry, "<!-- seneca:role name=anchor -->\n### What will I carry into tomorrow?\n%s\n", previousAnchor)
			if odd {
				fmt.Fprintf(&entry, "\n### Gratitude\n%s ---\n", scaleSentence(i, 8))
			}
		}
		previousDate = date
		text := entry.String() + "---\n\n"
		if i%11 == 7 {
			text = strings.ReplaceAll(text, "\n", "\r\n")
			crlf = append(crlf, date)
		}
		out.WriteString(text)
	}
	return out.String(), crlf
}

// keepScaleJournal copies the synthetic journal into a new directory.
func keepScaleJournal(t *testing.T, dir string, files ...string) {
	t.Helper()
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("SENECA_SCALE_JOURNAL: %s already exists; name a new directory", dir)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(file)), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("kept the synthetic journal in %s", dir)
}

// entriesByDate is every day of a reflections file with its bytes as written.
func entriesByDate(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	days := map[string]string{}
	for _, entry := range engine.ParseReflectionDocument(string(data)).Entries {
		if _, twice := days[entry.Date]; twice {
			t.Fatalf("%s holds %s twice", path, entry.Date)
		}
		days[entry.Date] = entry.Raw
	}
	return days
}

// timed runs a step and fails the test when it takes longer than budget.
func timed(t *testing.T, what string, budget time.Duration, step func()) {
	t.Helper()
	began := time.Now()
	step()
	took := time.Since(began)
	t.Logf("%s: %v", what, took.Round(time.Millisecond))
	if took > budget {
		t.Fatalf("%s took %v at representative size; the budget is %v", what, took, budget)
	}
}

func requireSameBytesPerDay(t *testing.T, what string, want, got map[string]string, except ...string) {
	t.Helper()
	skip := map[string]bool{}
	for _, date := range except {
		skip[date] = true
	}
	for date, raw := range want {
		if skip[date] {
			continue
		}
		if other, ok := got[date]; !ok {
			t.Fatalf("%s: %s is missing", what, date)
		} else if other != raw {
			t.Fatalf("%s: %s is not byte for byte the same\nwant %q\ngot  %q", what, date, raw, other)
		}
	}
}

func TestARepresentativeJournalCrossesBetweenTheMacAndThePhoneIntact(t *testing.T) {
	d := newDesk(t)
	text, crlf := scaleJournal(t)
	if len(text) < 800_000 || len(crlf) < 90 {
		t.Fatalf("the synthetic journal is %d bytes with %d CRLF days; it no longer represents a long-kept journal", len(text), len(crlf))
	}
	d.write(d.reflections, text)
	journal, err := engine.Open(engine.Options{
		ReflectionsPath: d.reflections,
		Settings:        engine.ReflectionSettings{Questions: syncQuestions, AnchorQuestionIndex: 3},
		Clock:           func() time.Time { return time.Date(2025, 6, 10, 21, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := time.Parse(engine.CalendarDateLayout, scaleStart)
	for i := 0; i < scaleReviews; i++ {
		begin := first.AddDate(0, 0, 7*i)
		save := engine.ReviewSave{
			Period: engine.ReviewPeriodWeekly,
			Start:  begin.Format(engine.CalendarDateLayout),
			End:    begin.AddDate(0, 0, 6).Format(engine.CalendarDateLayout),
			Body:   "## Patterns\n\n" + scaleAnswer(i, 20) + "\n\n## Next\n\n" + scaleAnswer(i, 30) + "\n",
		}
		if _, err := journal.SaveReview(save); err != nil {
			t.Fatalf("archiving review %d: %v", i, err)
		}
	}
	// SENECA_SCALE_JOURNAL=<dir> keeps a copy for the multi-day trial on a
	// real iPhone (docs/mobile/qualification.md); the test runs as always.
	if dir := os.Getenv("SENECA_SCALE_JOURNAL"); dir != "" {
		keepScaleJournal(t, dir, d.reflections, d.archive)
	}
	d.git("add", "journal/reflections.md", "journal/"+engine.ArchiveFileName)
	d.git("commit", "--quiet", "-m", "a representative journal")
	before := entriesByDate(t, d.reflections)
	if len(before) != scaleDays {
		t.Fatalf("the desk reads %d days, want %d", len(before), scaleDays)
	}
	archive := d.read(d.archive)

	timed(t, "enrolling on the Mac", setupBudget, func() { d.must("sync", "enroll", "--name", "desk") })
	if got := d.read(d.reflections); got != text {
		t.Fatal("enrolling changed the reflections file")
	}
	var p *phone
	timed(t, "joining on the phone", setupBudget, func() { p = newPhone(t, d.exchange) })

	// The phone holds every day as the Mac wrote it, CRLF and legacy days
	// included, and the same archive.
	onPhone := entriesByDate(t, p.reflections())
	if len(onPhone) != scaleDays {
		t.Fatalf("the phone holds %d days, want %d", len(onPhone), scaleDays)
	}
	requireSameBytesPerDay(t, "the phone after joining", before, onPhone)
	if got, err := os.ReadFile(filepath.Join(p.dir, engine.ArchiveFileName)); err != nil || string(got) != archive {
		t.Fatalf("the phone's archive is not the Mac's (%v)", err)
	}

	// The phone browses and searches the whole journal through the bridge.
	bridge := senecabridge.NewBridge()
	bridgeCall(t, bridge, "journal.open", "", map[string]any{
		"reflections_path":      p.reflections(),
		"state_dir":             t.TempDir(),
		"questions":             syncQuestions,
		"anchor_question_index": 3,
		"time_zone":             "America/Toronto",
		"now":                   "2025-07-01T21:00:00-04:00",
	}, nil)
	var listed struct {
		Total int `json:"total"`
	}
	timed(t, "listing every day through the bridge", stepBudget, func() {
		bridgeCall(t, bridge, "entries.list", "", map[string]any{}, &listed)
	})
	if listed.Total != scaleDays {
		t.Fatalf("entries.list counts %d days, want %d", listed.Total, scaleDays)
	}
	var found struct {
		Hits []json.RawMessage `json:"hits"`
	}
	timed(t, "searching every day through the bridge", stepBudget, func() {
		bridgeCall(t, bridge, "entries.search", "", map[string]any{"phrase": "décision"}, &found)
	})
	if len(found.Hits) == 0 {
		t.Fatal("a search of the whole journal found nothing")
	}

	// A day written on the phone reaches the Mac, and nothing else moves.
	p.reflect("2025-07-01", "Hurried the last call.", "Wrote it down anyway.", "Close the laptop at nine.")
	timed(t, "importing on the Mac", stepBudget, func() { d.must("sync", "once") })
	afterImport := entriesByDate(t, d.reflections)
	if !strings.Contains(afterImport["2025-07-01"], "Wrote it down anyway.") {
		t.Fatal("the phone's day did not reach the Mac")
	}
	requireSameBytesPerDay(t, "the Mac after importing", before, afterImport)
	if d.subjects()[0] != "seneca: sync import" {
		t.Fatalf("the import was not committed: %v", d.subjects()[:3])
	}
	if changed := strings.Fields(d.git("show", "--name-only", "--format=", "HEAD")); len(changed) != 1 || changed[0] != "journal/reflections.md" {
		t.Fatalf("the import commit holds %v, want only the reflections file", changed)
	}

	// A CRLF day edited by hand on the Mac reaches the phone as edited, and
	// every other day stays as it was there.
	edited := crlf[len(crlf)/2]
	marked := strings.Replace(afterImport[edited], "Synthetic: ", "Synthetic (edited at the desk): ", 1)
	d.write(d.reflections, strings.Replace(d.read(d.reflections), afterImport[edited], marked, 1))
	timed(t, "publishing an external edit", stepBudget, func() { d.must("sync", "once") })
	timed(t, "importing on the phone", stepBudget, func() { p.pull() })
	onPhone = entriesByDate(t, p.reflections())
	if onPhone[edited] != marked {
		t.Fatalf("the edited CRLF day reached the phone as %q, want %q", onPhone[edited], marked)
	}
	requireSameBytesPerDay(t, "the phone after the edit", afterImport, onPhone, edited)
	requireSameBytesPerDay(t, "the Mac and the phone", entriesByDate(t, d.reflections), onPhone)

	// Status, doctor, and a backup at this size: the first two change nothing,
	// and the backup holds the files byte for byte.
	snapshot := d.tree()
	timed(t, "sync status", stepBudget, func() { d.must("sync", "status") })
	timed(t, "doctor", stepBudget, func() { _, _, _ = d.run("doctor") })
	requireSameTree(t, "status and doctor", snapshot, d.tree())
	backup := filepath.Join(t.TempDir(), "backup")
	timed(t, "backing up", stepBudget, func() { d.must("backup", backup) })
	requireManifestHolds(t, backup, map[string]string{"reflections": d.reflections, "archive": d.archive})
}

// requireManifestHolds checks a recovery export's manifest against the files
// it was made from and against the copies it holds.
func requireManifestHolds(t *testing.T, export string, sources map[string]string) engine.RecoveryExport {
	t.Helper()
	var manifest engine.RecoveryExport
	readJSON(t, filepath.Join(export, "MANIFEST.json"), &manifest)
	seen := map[string]bool{}
	for _, file := range manifest.Files {
		copied, err := os.ReadFile(filepath.Join(export, filepath.FromSlash(file.Path)))
		if err != nil || engine.RevisionOf(copied) != file.Digest {
			t.Fatalf("the export's %s does not match its manifest (%v)", file.Path, err)
		}
		if source, ok := sources[file.Kind]; ok {
			data, err := os.ReadFile(source)
			if err != nil || engine.RevisionOf(data) != file.Digest {
				t.Fatalf("the export's %s is not %s as it is (%v)", file.Kind, source, err)
			}
			seen[file.Kind] = true
		}
	}
	for kind := range sources {
		if !seen[kind] {
			t.Fatalf("the export holds no %s", kind)
		}
	}
	return manifest
}

func withoutEnv(env []string, name string) []string {
	var kept []string
	for _, value := range env {
		if !strings.HasPrefix(value, name+"=") {
			kept = append(kept, value)
		}
	}
	return kept
}

// TestTheDocumentedRollbackKeepsEveryWordAndALocalWorkflow carries out the
// rollback in docs/mobile/qualification.md on the Mac's side, in its order:
// stop background sync, export what was not sent and what is in conflict,
// keep the backups, leave sync, and go back to the local command line.
func TestTheDocumentedRollbackKeepsEveryWordAndALocalWorkflow(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the background service is a macOS LaunchAgent")
	}
	d, p := enrolled(t)

	// The backup taken before enrolling stands in for the rollout's first one.
	retained := filepath.Join(t.TempDir(), "before-sync")
	d.must("backup", retained)
	requireManifestHolds(t, retained, map[string]string{"reflections": d.reflections})
	d.must("sync", "service", "install")
	plistPath := filepath.Join(d.home, "Library", "LaunchAgents", "io.sglavoie.seneca.sync.plist")

	// Work in flight: a conflict on 2026-01-02, a day the phone never sent,
	// and a hand edit on the Mac not exchanged yet.
	if err := os.WriteFile(p.reflections(), []byte(strings.Replace(p.text(), "Snapped.", "Snapped at the phone.", 1)), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := p.journal().RecordExternalEdits(); err != nil {
		t.Fatal(err)
	}
	p.publish()
	d.write(d.reflections, strings.Replace(d.read(d.reflections), "Snapped.", "Snapped at the desk.", 1))
	if _, stderr, err := d.run("sync", "once"); err != nil || !strings.Contains(stderr, "1 conflict waits for you") {
		t.Fatalf("expected a conflict, got err=%v stderr:\n%s", err, stderr)
	}
	journal := p.journal()
	draft, err := journal.PrepareReflection("2026-01-03", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.SaveReflection(draft, engine.ReflectionResult{Saved: true, Answers: []string{"Never sent.", "Kept anyway.", "Export first."}}, true); err != nil {
		t.Fatal(err)
	}
	d.write(d.reflections, strings.Replace(d.read(d.reflections), "Apologized.", "Apologized, and meant it.", 1))

	// 1. Stop background sync.
	if out := d.must("sync", "service", "remove"); !strings.Contains(out, "Background sync removed") {
		t.Fatalf("unexpected remove output:\n%s", out)
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("the LaunchAgent is still there: %v", err)
	}

	// 2. Export unsent and conflicted work from both devices.
	macExport := filepath.Join(t.TempDir(), "mac-export")
	d.must("backup", macExport)
	manifest := requireManifestHolds(t, macExport, map[string]string{"reflections": d.reflections})
	if manifest.Count("conflict") < 2 {
		t.Fatalf("the Mac's export holds %d conflict versions, want both", manifest.Count("conflict"))
	}
	conflicts := ""
	for _, file := range manifest.Files {
		if file.Kind == "conflict" {
			conflicts += d.read(filepath.Join(macExport, filepath.FromSlash(file.Path)))
		}
	}
	if !strings.Contains(conflicts, "Snapped at the phone.") || !strings.Contains(conflicts, "Snapped at the desk.") {
		t.Fatalf("the export does not hold both versions of the conflict:\n%s", conflicts)
	}
	if !strings.Contains(d.read(filepath.Join(macExport, "reflections.md")), "Apologized, and meant it.") {
		t.Fatal("the Mac's export lacks the edit not exchanged yet")
	}
	phoneExport := filepath.Join(t.TempDir(), "phone-export")
	if _, err := p.journal().ExportRecovery(phoneExport, ""); err != nil {
		t.Fatalf("phone: exporting: %v", err)
	}
	if text, err := os.ReadFile(filepath.Join(phoneExport, "reflections.md")); err != nil || !strings.Contains(string(text), "Never sent.") {
		t.Fatalf("the phone's export lacks the day it never sent (%v)", err)
	}

	// 3. Leave sync on the Mac; the history moves aside and nothing is lost.
	if out := d.must("sync", "leave"); !strings.Contains(strings.ToLower(out), "left") {
		t.Fatalf("unexpected leave output:\n%s", out)
	}
	left, _ := filepath.Glob(filepath.Join(engine.JournalMetadataDirFor(d.reflections), "sync-left-*"))
	if len(left) != 1 {
		t.Fatalf("the sync history was not moved aside: %v", left)
	}

	// 4. The local command line, with no exchange configured, as before sync.
	d.env = withoutEnv(d.env, "SENECA_SYNC_EXCHANGE")
	if out := d.must("sync", "status"); !strings.Contains(out, "does not take part in sync") {
		t.Fatalf("the journal still takes part in sync:\n%s", out)
	}
	if out := d.must("show"); !strings.Contains(out, "Apologized, and meant it.") {
		t.Fatalf("show does not read the journal:\n%s", out)
	}
	stdout, stderr, err := d.run("focus", "set", "Export before leaving.")
	if err != nil || strings.Contains(strings.ToLower(stderr), "sync") {
		t.Fatalf("a local change after leaving failed or mentions sync: err=%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if out := d.must("focus"); !strings.Contains(out, "Export before leaving.") {
		t.Fatalf("the focus set after leaving is not there:\n%s", out)
	}

	// 5. The backups are still there and still hold.
	requireManifestHolds(t, retained, map[string]string{})
	requireManifestHolds(t, macExport, map[string]string{})
}
