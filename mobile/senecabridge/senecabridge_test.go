package senecabridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/seneca/engine"
)

// The fixture is the synthetic journal the command line's compatibility
// baseline runs on: what the bridge returns for it is held to what the
// commands print for it.
const (
	fixtureDir    = "../testdata/roundtrip/journal"
	compatDir     = "../../cmd/testdata/compat"
	fixtureNow    = "2026-04-02T09:00:00Z"
	fixtureToday  = "2026-04-02"
	fixtureAnchor = 3
)

var fixtureQuestions = []string{
	"What did I notice today?",
	"What did I handle well?",
	"What will I carry into tomorrow?",
}

// device is one installation: a bridge, a journal directory, and the app's
// state directory.
type device struct {
	t        *testing.T
	bridge   *Bridge
	dir      string
	stateDir string
	nextID   int
}

type reply struct {
	Version   int             `json:"version"`
	Operation string          `json:"operation"`
	RequestID string          `json:"request_id"`
	OK        bool            `json:"ok"`
	Replayed  bool            `json:"replayed"`
	Result    json.RawMessage `json:"result"`
	Warning   *problem        `json:"warning"`
	Error     *problem        `json:"error"`
}

func newDevice(t *testing.T, seeded bool) *device {
	t.Helper()
	root := t.TempDir()
	d := &device{t: t, bridge: NewBridge(), dir: filepath.Join(root, "journal"), stateDir: filepath.Join(root, "state")}
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if seeded {
		for _, name := range []string{"reflections.md", engine.ArchiveFileName} {
			data, err := os.ReadFile(filepath.Join(fixtureDir, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d.dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	d.open(fixtureNow)
	return d
}

func (d *device) reflections() string { return filepath.Join(d.dir, "reflections.md") }

func (d *device) openParams(now string) map[string]any {
	return map[string]any{
		"reflections_path":      d.reflections(),
		"state_dir":             d.stateDir,
		"questions":             fixtureQuestions,
		"anchor_question_index": fixtureAnchor,
		"time_zone":             "UTC",
		"now":                   now,
	}
}

func (d *device) open(now string) {
	d.t.Helper()
	d.must("journal.open", d.openParams(now), nil)
}

func (d *device) file() string {
	d.t.Helper()
	data, err := os.ReadFile(d.reflections())
	if err != nil {
		d.t.Fatal(err)
	}
	return string(data)
}

// send answers one request as the app would send it.
func (d *device) send(operation, requestID string, params any) reply {
	d.t.Helper()
	req := map[string]any{"version": Version, "operation": operation}
	if requestID != "" {
		req["request_id"] = requestID
	}
	if params != nil {
		req["params"] = params
	}
	raw, err := json.Marshal(req)
	if err != nil {
		d.t.Fatal(err)
	}
	return decodeReply(d.t, d.bridge.call(string(raw)))
}

func decodeReply(t *testing.T, raw string) reply {
	t.Helper()
	var r reply
	if err := decodeStrict([]byte(raw), &r); err != nil {
		t.Fatalf("the bridge answered with something that is not a response: %v\n%s", err, raw)
	}
	if r.Version != Version {
		t.Fatalf("response version = %d, want %d\n%s", r.Version, Version, raw)
	}
	if r.OK == (r.Error != nil) {
		t.Fatalf("a response is ok or carries an error, never both or neither\n%s", raw)
	}
	return r
}

func (d *device) id() string {
	d.nextID++
	return d.t.Name() + "-" + string(rune('a'+d.nextID))
}

// must sends a request that has to succeed and decodes its result. A
// mutation gets a fresh request identity.
func (d *device) must(operation string, params any, into any) reply {
	d.t.Helper()
	requestID := ""
	if operations[operation].mutates {
		requestID = d.id()
	}
	r := d.send(operation, requestID, params)
	if !r.OK {
		d.t.Fatalf("%s failed: %s: %s", operation, r.Error.Code, r.Error.Message)
	}
	if into != nil {
		if err := decodeStrict(r.Result, into); err != nil {
			d.t.Fatalf("%s result: %v\n%s", operation, err, r.Result)
		}
	}
	return r
}

// refused sends a request that has to be refused with code.
func (d *device) refused(operation, requestID string, params any, code string) *problem {
	d.t.Helper()
	r := d.send(operation, requestID, params)
	if r.OK {
		d.t.Fatalf("%s succeeded, want refusal %s\n%s", operation, code, r.Result)
	}
	if r.Error.Code != code {
		d.t.Fatalf("%s refused with %s (%s), want %s", operation, r.Error.Code, r.Error.Message, code)
	}
	if r.Error.Message == "" {
		d.t.Fatalf("%s refused with no message", operation)
	}
	return r.Error
}

func (d *device) prepare(date string) prepareResult {
	d.t.Helper()
	var prepared prepareResult
	d.must("reflection.prepare", map[string]any{"date": date}, &prepared)
	return prepared
}

func saveParams(draft reflectionDraft, followThrough string, answers ...string) map[string]any {
	return map[string]any{"draft": draft, "answers": answers, "follow_through": followThrough}
}

func TestEveryRequestIsHeldToTheEnvelope(t *testing.T) {
	d := newDevice(t, true)
	before := d.file()

	cases := []struct {
		name string
		raw  string
		code string
	}{
		{"not JSON", `not json`, "malformed-request"},
		{"an unknown envelope field", `{"version":8,"operation":"journal.status","extra":true}`, "malformed-request"},
		{"data after the request", `{"version":8,"operation":"journal.status"} {}`, "malformed-request"},
		{"a newer version", `{"version":9,"operation":"journal.status"}`, "unsupported-version"},
		{"an older version", `{"version":5,"operation":"journal.status"}`, "unsupported-version"},
		{"no version", `{"operation":"journal.status"}`, "unsupported-version"},
		{"an unknown operation", `{"version":8,"operation":"journal.erase"}`, "unknown-operation"},
		{"an unknown parameter", `{"version":8,"operation":"entry.get","params":{"date":"2026-03-05","raw":true}}`, "invalid-params"},
		{"a mutation without an identity", `{"version":8,"operation":"focus.set","params":{"base":"absent","text":"x"}}`, "missing-request-id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := decodeReply(t, d.bridge.call(tc.raw))
			if r.OK || r.Error.Code != tc.code {
				t.Fatalf("answer = %+v, want refusal %s", r, tc.code)
			}
			if r.Error.Retryable {
				t.Fatalf("%s is not something sending again can help", tc.code)
			}
		})
	}
	if d.file() != before {
		t.Fatal("a refused request changed the journal")
	}
}

func TestOperationsNeedAnOpenJournal(t *testing.T) {
	b := NewBridge()
	r := decodeReply(t, b.call(`{"version":8,"operation":"entries.list"}`))
	if r.OK || r.Error.Code != "journal-not-open" {
		t.Fatalf("answer = %+v, want journal-not-open", r)
	}
	// What needs no journal still answers.
	r = decodeReply(t, b.call(`{"version":8,"operation":"bridge.describe"}`))
	if !r.OK {
		t.Fatalf("bridge.describe failed: %+v", r.Error)
	}
}

func TestTheBridgeVersionIsAClosedSet(t *testing.T) {
	d := newDevice(t, false)
	var described bridgeDescription
	d.must("bridge.describe", nil, &described)

	// Adding to this list makes a new bridge version: see docs/mobile/bridge.md.
	want := []string{
		"bridge.describe",
		"conflict.resolve", "conflicts.list",
		"diagnostics.collect",
		"draft.discard", "draft.get", "draft.retain",
		"entries.list", "entries.search", "entry.delete", "entry.get",
		"focus.clear", "focus.get", "focus.set", "focus_dispute.resolve",
		"insights.get",
		"journal.close", "journal.open", "journal.status",
		"packs.list", "packs.read",
		"recovery.export",
		"reflection.prepare", "reflection.save",
		"reminders.plan",
		"review.get", "reviews.list",
		"settings.apply_pack", "settings.get", "settings.initialize", "settings.update",
		"share.packet",
		"sync.delivery", "sync.enroll", "sync.export", "sync.import", "sync.join", "sync.leave", "sync.recover", "sync.status",
	}
	if !reflect.DeepEqual(described.Operations, want) {
		t.Fatalf("operations = %q\nwant %q", described.Operations, want)
	}
	if described.BridgeVersion != BridgeVersion() || described.SyncProtocol != engine.SyncProtocolVersion ||
		described.ReflectionSchema != engine.ReflectionSchemaVersion || described.ArchiveSchema != engine.ArchiveSchemaVersion {
		t.Fatalf("described versions = %+v", described)
	}

	// Every operation that changes the journal carries an identity; what only
	// reads, or only touches the device's own drafts and the exchange, does not.
	mutating := map[string]bool{
		"entry.delete":     true,
		"conflict.resolve": true, "focus.clear": true, "focus.set": true, "focus_dispute.resolve": true,
		"reflection.save": true, "settings.apply_pack": true, "settings.initialize": true, "settings.update": true,
		"sync.enroll": true, "sync.import": true, "sync.join": true, "sync.leave": true, "sync.recover": true,
	}
	for name, op := range operations {
		if op.mutates != mutating[name] {
			t.Errorf("%s mutates = %v, want %v", name, op.mutates, mutating[name])
		}
	}
}

func TestOpenReportsTheJournalWithoutWritingIt(t *testing.T) {
	d := newDevice(t, true)
	before := d.file()

	var status journalStatusResult
	d.must("journal.status", nil, &status)
	if status.Today != fixtureToday || !status.ReflectionsExist || !status.ArchiveExists || !status.Writable {
		t.Fatalf("status = %+v", status)
	}
	if status.EntryCount != 4 || status.ReviewCount != 3 {
		t.Fatalf("entries = %d, reviews = %d, want 4 and 3", status.EntryCount, status.ReviewCount)
	}
	if status.Settings.Initialized || !reflect.DeepEqual(status.Settings.Questions, fixtureQuestions) || status.Settings.AnchorQuestionIndex != fixtureAnchor {
		t.Fatalf("settings = %+v, want the device's own", status.Settings)
	}
	if status.SyncEnrolled {
		t.Fatal("the fixture journal is not enrolled in sync")
	}
	if d.file() != before {
		t.Fatal("opening changed the reflections file")
	}
	if _, err := os.Stat(filepath.Join(d.dir, engine.JournalMetadataRootName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("opening created %s: %v", engine.JournalMetadataRootName, err)
	}

	// A journal that does not exist yet is an ordinary journal.
	empty := newDevice(t, false)
	empty.must("journal.status", nil, &status)
	if status.ReflectionsExist || status.EntryCount != 0 || !status.Writable {
		t.Fatalf("empty journal status = %+v", status)
	}
}

func TestOpenRefusesWhatItCannotResolve(t *testing.T) {
	d := newDevice(t, true)
	for name, change := range map[string]func(map[string]any){
		"a relative journal":  func(p map[string]any) { p["reflections_path"] = "reflections.md" },
		"a relative state":    func(p map[string]any) { p["state_dir"] = "state" },
		"an unknown zone":     func(p map[string]any) { p["time_zone"] = "Mars/Olympus" },
		"an unreadable clock": func(p map[string]any) { p["now"] = "yesterday" },
	} {
		p := d.openParams(fixtureNow)
		change(p)
		if r := d.send("journal.open", "", p); r.OK || r.Error.Code != "invalid-params" {
			t.Errorf("%s: answer = %+v, want invalid-params", name, r)
		}
	}
	p := d.openParams(fixtureNow)
	p["anchor_question_index"] = 9
	d.refused("journal.open", "", p, "invalid-settings")

	// A refused open leaves the journal that was open, open.
	d.must("journal.status", nil, nil)
}

func TestTheJournalsDayFollowsItsTimeZone(t *testing.T) {
	d := newDevice(t, true)
	p := d.openParams("2026-04-03T01:30:00Z")
	p["time_zone"] = "America/Toronto"
	var status journalStatusResult
	d.must("journal.open", p, &status)
	if status.Today != "2026-04-02" {
		t.Fatalf("today = %s, want 2026-04-02: half past one UTC is still the evening before in Toronto", status.Today)
	}
}

func TestFixtureReadsAgreeWithTheCommandLineBaseline(t *testing.T) {
	d := newDevice(t, true)
	golden := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(compatDir, "golden", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	// `seneca show --date 2026-03-05`, piped, prints the entry as stored.
	var entry entryResult
	d.must("entry.get", map[string]any{"date": "2026-03-05"}, &entry)
	if got, want := entry.Raw+"\n", golden("show-date.txt"); got != want {
		t.Fatalf("entry.get raw =\n%s\nwant what `seneca show --date` prints:\n%s", got, want)
	}
	for _, line := range entry.Lines {
		if strings.Contains(line, "seneca:") {
			t.Fatalf("displayed lines include a marker: %q", line)
		}
	}
	if entry.Relative != "28 days ago" {
		t.Fatalf("relative = %q", entry.Relative)
	}
	var follow *followThrough
	for _, section := range entry.Sections {
		if section.FollowThrough != nil {
			follow = section.FollowThrough
		}
	}
	if follow == nil || follow.SourceDate != "2026-03-04" || follow.SourceIntention != "Finish the draft before lunch." ||
		follow.Response != "Synthetic: the draft was done by eleven." {
		t.Fatalf("follow-through = %+v", follow)
	}

	// `seneca show --month=2026-03` prints the same days in the same order.
	var listed entriesResult
	d.must("entries.list", nil, &listed)
	var dates []string
	for _, summary := range listed.Entries {
		dates = append(dates, summary.Date)
		if !strings.Contains(golden("show-month.txt"), "## "+summary.Date) {
			t.Errorf("entries.list has %s, which `seneca show --month` does not print", summary.Date)
		}
	}
	if want := []string{"2026-03-02", "2026-03-03", "2026-03-04", "2026-03-05"}; !reflect.DeepEqual(dates, want) {
		t.Fatalf("dates = %q, want %q", dates, want)
	}

	// `seneca show --search walk` finds the same lines.
	var found searchResult
	d.must("entries.search", map[string]any{"phrase": "walk"}, &found)
	if len(found.Hits) != 1 || found.Hits[0].Date != "2026-03-05" {
		t.Fatalf("search hits = %+v", found.Hits)
	}
	for _, snippet := range found.Hits[0].Snippets {
		if !strings.Contains(golden("show-search.txt"), "  "+snippet+"\n") {
			t.Errorf("search snippet %q is not in what `seneca show --search` prints", snippet)
		}
	}
	if len(found.Hits[0].Snippets) != 2 {
		t.Fatalf("snippets = %q, want the 2 lines the command reports", found.Hits[0].Snippets)
	}

	// `seneca focus --date` for each day the baseline asks about.
	focusGolden := golden("focus.txt")
	for date, want := range map[string]string{"2026-02-28": "", "2026-03-10": "Walk before the dip.", "2026-03-15": "", "2026-04-02": "Finish before starting."} {
		var focus focusResult
		d.must("focus.get", map[string]any{"date": date}, &focus)
		if focus.Focus.Text != want || focus.Focus.Active != (want != "") {
			t.Errorf("focus on %s = %+v, want %q", date, focus.Focus, want)
		}
		if want == "" && !strings.Contains(focusGolden, "No focus in effect on "+date+".") {
			t.Errorf("the baseline does not say there is no focus on %s", date)
		}
		if want != "" && !strings.Contains(focusGolden, want) {
			t.Errorf("the baseline does not print focus %q", want)
		}
	}

	// `seneca review history`: the same saved reviews, newest first.
	var reviews reviewsResult
	d.must("reviews.list", nil, &reviews)
	if len(reviews.Reviews) != 3 || reviews.Reviews[0].Period != "monthly" || reviews.Reviews[1].Revises != reviews.Reviews[2].ID ||
		reviews.Reviews[1].Superseded || !reviews.Reviews[2].Superseded {
		t.Fatalf("reviews = %+v", reviews.Reviews)
	}
	history := golden("review-history.txt")
	for _, review := range reviews.Reviews {
		if !strings.Contains(history, review.Title+" — "+review.Start+" to "+review.End) || !strings.Contains(history, "Saved "+review.CreatedAt) {
			t.Errorf("review %s is not titled and dated as `seneca review history` prints it", review.ShortID)
		}
		if !strings.Contains(history, review.ShortID) {
			t.Errorf("review %s is not in what `seneca review history` prints", review.ShortID)
		}
	}
	var review reviewResult
	d.must("review.get", map[string]any{"id": reviews.Reviews[1].ID}, &review)
	if !strings.HasPrefix(review.Body, "Synthetic weekly review, revised the next morning.") {
		t.Fatalf("review body = %q", review.Body)
	}
	d.refused("review.get", "", map[string]any{"id": "nope"}, "not-found")
}

func TestASavedReflectionIsWhatTheDesktopWouldHaveWritten(t *testing.T) {
	d := newDevice(t, true)
	prepared := d.prepare(fixtureToday)
	draft := prepared.Draft
	if draft.IsEdit || draft.Base != string(engine.RevisionAbsent) || !reflect.DeepEqual(draft.Questions, fixtureQuestions) {
		t.Fatalf("draft = %+v", draft)
	}
	if draft.Context.Focus != "Finish before starting." || draft.Context.PriorDate != "2026-03-05" ||
		draft.Context.PriorText != "Walk before the dip, not after." || !draft.Context.ShowFollowThrough {
		t.Fatalf("context = %+v", draft.Context)
	}
	if prepared.IntentionLabel != "Intention 2026-03-05 (28 days ago)" {
		t.Fatalf("intention label = %q", prepared.IntentionLabel)
	}

	var saved savedReflection
	d.must("reflection.save", saveParams(draft, "Walked first.", "Noticed.", "Handled.", "Carry."), &saved)
	if saved.Date != fixtureToday || saved.IsEdit {
		t.Fatalf("saved = %+v", saved)
	}

	// The desktop path: the same engine calls the daily command makes, on a
	// second copy of the same journal.
	desktop := newDevice(t, true)
	journal, err := engine.Open(engine.Options{
		ReflectionsPath: desktop.reflections(),
		Settings:        engine.ReflectionSettings{Questions: fixtureQuestions, AnchorQuestionIndex: fixtureAnchor},
		Clock:           func() time.Time { return time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	desktopDraft, err := journal.PrepareReflection(fixtureToday, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.SaveReflection(desktopDraft, engine.ReflectionResult{
		Saved: true, Answers: []string{"Noticed.", "Handled.", "Carry."}, FollowThrough: "Walked first.",
	}, false); err != nil {
		t.Fatal(err)
	}
	if d.file() != desktop.file() {
		t.Fatalf("the bridge wrote\n%s\nand the desktop engine wrote\n%s", d.file(), desktop.file())
	}

	// Reopened, the day reads back as it was saved.
	d.must("journal.close", nil, nil)
	d.open(fixtureNow)
	var entry entryResult
	d.must("entry.get", map[string]any{"date": fixtureToday}, &entry)
	if !reflect.DeepEqual(entry, saved.Entry) {
		t.Fatalf("reopened entry = %+v\nsaved %+v", entry, saved.Entry)
	}
	again := d.prepare(fixtureToday)
	if !again.Draft.IsEdit || again.Draft.Base != entry.Revision ||
		!reflect.DeepEqual(again.Draft.Initial, []string{"Noticed.", "Handled.", "Carry."}) ||
		again.Draft.Context.FollowInitial != "Walked first." {
		t.Fatalf("reopened draft = %+v", again.Draft)
	}
}

func TestARepeatedSaveIsAnsweredNotRepeated(t *testing.T) {
	d := newDevice(t, true)
	draft := d.prepare(fixtureToday).Draft
	params := saveParams(draft, "", "Noticed.", "", "")

	first := d.send("reflection.save", "save-1", params)
	if !first.OK || first.Replayed {
		t.Fatalf("first save = %+v", first)
	}
	written := d.file()
	backup, err := os.ReadFile(d.reflections() + engine.BackupSuffix)
	if err != nil {
		t.Fatal(err)
	}

	// The same request again: the answer it was given, and no second write.
	second := d.send("reflection.save", "save-1", params)
	if !second.OK || !second.Replayed || string(second.Result) != string(first.Result) {
		t.Fatalf("repeated save = %+v, want the first answer replayed", second)
	}
	// A new process, the same installation.
	d.bridge = NewBridge()
	d.open(fixtureNow)
	third := d.send("reflection.save", "save-1", params)
	if !third.OK || !third.Replayed || string(third.Result) != string(first.Result) {
		t.Fatalf("save repeated after a restart = %+v, want the first answer replayed", third)
	}
	if d.file() != written {
		t.Fatal("a repeated save changed the journal")
	}
	if after, _ := os.ReadFile(d.reflections() + engine.BackupSuffix); string(after) != string(backup) {
		t.Fatal("a repeated save rewrote the backup: it wrote the journal again")
	}

	// An identity names one request.
	other := saveParams(draft, "", "Something else.", "", "")
	d.refused("reflection.save", "save-1", other, "request-id-reused")

	// The same writing under a new identity is a new request, and the draft
	// it was made on is no longer what the journal holds.
	stale := d.refused("reflection.save", "save-2", params, "stale-reflection")
	if stale.Details["date"] != fixtureToday || stale.Details["base"] != string(engine.RevisionAbsent) || stale.Details["current"] == "" {
		t.Fatalf("stale details = %+v", stale.Details)
	}
	if d.file() != written {
		t.Fatal("a refused save changed the journal")
	}

	// With the ledger lost, the request is carried out again and refused by
	// its own precondition: still no second save.
	if err := os.Remove(filepath.Join(d.stateDir, ledgerFileName)); err != nil {
		t.Fatal(err)
	}
	d.refused("reflection.save", "save-1", params, "stale-reflection")
	if d.file() != written {
		t.Fatal("a save repeated without its ledger changed the journal")
	}
}

func TestARepeatedSaveIsAnsweredAfterTheContainerMoved(t *testing.T) {
	d := newDevice(t, true)
	params := saveParams(d.prepare(fixtureToday).Draft, "", "Noticed.", "", "")
	first := d.send("reflection.save", "save-1", params)
	if !first.OK {
		t.Fatalf("save failed: %+v", first.Error)
	}
	written := d.file()

	// Installing a new build gives the app's container a new path: the same
	// journal and the same ledger, somewhere else.
	root := filepath.Dir(d.dir)
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	d.dir, d.stateDir = filepath.Join(moved, "journal"), filepath.Join(moved, "state")
	d.bridge = NewBridge()
	d.open(fixtureNow)

	again := d.send("reflection.save", "save-1", params)
	if !again.OK || !again.Replayed || string(again.Result) != string(first.Result) {
		t.Fatalf("save repeated from the moved container = %+v (%+v), want the first answer replayed", again, again.Error)
	}
	if d.file() != written {
		t.Fatal("the repeated save changed the journal")
	}
}

func TestARefusedRequestIsNotRemembered(t *testing.T) {
	d := newDevice(t, true)
	draft := d.prepare(fixtureToday).Draft

	// Nothing written: refused, and free to be sent again once there is
	// something to save.
	d.refused("reflection.save", "save-1", saveParams(draft, "", "", "", ""), "empty-reflection")
	d.refused("reflection.save", "save-1", saveParams(draft, "", "", "", ""), "empty-reflection")
	if _, err := os.Stat(filepath.Join(d.stateDir, ledgerFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refusal was written to the ledger: %v", err)
	}
}

func TestReflectionRefusalsCarryStableCodes(t *testing.T) {
	d := newDevice(t, true)
	before := d.file()

	future := d.refused("reflection.prepare", "", map[string]any{"date": "2026-04-03"}, "future-date")
	if future.Details["today"] != fixtureToday {
		t.Fatalf("future-date details = %+v", future.Details)
	}
	d.refused("reflection.prepare", "", map[string]any{"date": "April 2"}, "invalid-date")
	d.refused("entry.get", "", map[string]any{"date": "2026-01-01"}, "not-found")
	d.refused("entries.search", "", map[string]any{"phrase": "  "}, "invalid-params")

	draft := d.prepare(fixtureToday).Draft
	d.refused("reflection.save", d.id(), map[string]any{"draft": draft, "answers": []string{"one"}}, "invalid-params")

	// A draft made under other questions.
	changed := draft
	changed.SettingsRevision = string(engine.SettingsRevisionOf(engine.ReflectionSettings{Questions: []string{"Only one?"}, AnchorQuestionIndex: 1}))
	stale := d.refused("reflection.save", d.id(), saveParams(changed, "", "a", "b", "c"), "stale-settings")
	if stale.Details["date"] != fixtureToday {
		t.Fatalf("stale-settings details = %+v", stale.Details)
	}

	// A journal the desktop would refuse to write, the phone refuses to write.
	if err := os.WriteFile(d.reflections(), []byte(before+"## 2026-03-05\n\n### Again\nA second entry for one day.\n---\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := d.file()
	refusal := d.refused("reflection.prepare", "", map[string]any{"date": fixtureToday}, "invalid-reflections")
	if !strings.Contains(refusal.Message, "duplicate entry dates") {
		t.Fatalf("message = %q, want the command line's wording", refusal.Message)
	}
	var status journalStatusResult
	d.must("journal.status", nil, &status)
	if status.Writable || !strings.Contains(status.ReflectionsReason, "duplicate entry dates") {
		t.Fatalf("status = %+v", status)
	}
	if d.file() != broken {
		t.Fatal("a refusal changed the journal")
	}
}

func TestAWriteThatLandedIsAnsweredAsOne(t *testing.T) {
	d := newDevice(t, true)
	draft := d.prepare(fixtureToday).Draft

	// The journal is replaced, and only the directory flush after it fails.
	d.bridge.fs = engine.DefaultFS()
	real := d.bridge.fs.SyncDir
	renamed := false
	realRename := d.bridge.fs.Rename
	d.bridge.fs.Rename = func(oldpath, newpath string) error {
		if newpath == d.reflections() {
			renamed = true
		}
		return realRename(oldpath, newpath)
	}
	d.bridge.fs.SyncDir = func(dir *os.File) error {
		if renamed {
			return errors.New("synthetic flush failure")
		}
		return real(dir)
	}

	params := saveParams(draft, "", "Noticed.", "", "")
	r := d.send("reflection.save", "save-1", params)
	if !r.OK || r.Warning == nil || r.Warning.Code != "write-not-flushed" || r.Warning.Retryable {
		t.Fatalf("answer = %+v (warning %+v), want the result with a write-not-flushed warning", r, r.Warning)
	}
	var saved savedReflection
	if err := decodeStrict(r.Result, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Date != fixtureToday || !strings.Contains(saved.Entry.Raw, "Noticed.") || !strings.Contains(d.file(), "Noticed.") {
		t.Fatalf("saved = %+v", saved)
	}

	// It is remembered like any carried-out request, warning and all.
	d.bridge.fs = engine.FS{}
	written := d.file()
	again := d.send("reflection.save", "save-1", params)
	if !again.OK || !again.Replayed || again.Warning == nil || again.Warning.Code != "write-not-flushed" {
		t.Fatalf("repeated = %+v", again)
	}
	if d.file() != written {
		t.Fatal("the repeated request wrote again")
	}
}

func TestAFailureInsideTheEngineIsAnAnswer(t *testing.T) {
	d := newDevice(t, true)
	operations["test.panic"] = operation{run: func(*call) (any, error) { panic("synthetic") }}
	t.Cleanup(func() { delete(operations, "test.panic") })

	r := d.send("test.panic", "", nil)
	if r.OK || r.Error.Code != "internal" {
		t.Fatalf("answer = %+v, want an internal error", r)
	}
	// The bridge still answers afterwards.
	d.must("journal.status", nil, nil)
}

func TestFocusChangesAreConditionalAndNotRepeated(t *testing.T) {
	d := newDevice(t, true)
	archive := filepath.Join(d.dir, engine.ArchiveFileName)
	read := func() string {
		data, err := os.ReadFile(archive)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	var focus focusResult
	d.must("focus.get", nil, &focus)
	if focus.Focus.Text != "Finish before starting." || focus.Focus.Origin != "manual" || len(focus.Timeline) != 4 {
		t.Fatalf("focus = %+v", focus)
	}
	before := read()

	d.refused("focus.set", d.id(), map[string]any{"text": "No base."}, "missing-base")
	d.refused("focus.set", d.id(), map[string]any{"base": focus.Revision, "text": "   "}, "blank-focus")
	if read() != before {
		t.Fatal("a refused focus change wrote the archive")
	}

	params := map[string]any{"base": focus.Revision, "text": "One thing at a time."}
	first := d.send("focus.set", "focus-1", params)
	if !first.OK {
		t.Fatalf("focus.set failed: %+v", first.Error)
	}
	var change focusChange
	if err := decodeStrict(first.Result, &change); err != nil {
		t.Fatal(err)
	}
	if !change.Changed || change.Text != "One thing at a time." || change.Effective != fixtureToday || change.Revision == focus.Revision {
		t.Fatalf("change = %+v", change)
	}
	written := read()
	if strings.Count(written, "One thing at a time.") != 1 {
		t.Fatalf("the archive holds the new focus %d times", strings.Count(written, "One thing at a time."))
	}

	// The same request at a later moment would be a second record with a new
	// identity if it were carried out again.
	d.open("2026-04-02T09:05:00Z")
	if again := d.send("focus.set", "focus-1", params); !again.OK || !again.Replayed {
		t.Fatalf("repeated focus.set = %+v", again)
	}
	// And one decided on the old timeline is refused even without the ledger.
	stale := d.refused("focus.set", "focus-2", params, "stale-focus")
	if stale.Details["current"] != change.Revision {
		t.Fatalf("stale-focus details = %+v", stale.Details)
	}
	d.refused("focus.clear", "focus-3", map[string]any{"base": focus.Revision}, "stale-focus")
	if read() != written {
		t.Fatal("a repeated or stale focus change wrote the archive")
	}

	var cleared focusChange
	d.must("focus.clear", map[string]any{"base": change.Revision}, &cleared)
	if !cleared.Changed || cleared.Previous != "One thing at a time." || cleared.Text != "" {
		t.Fatalf("cleared = %+v", cleared)
	}
	d.must("focus.get", nil, &focus)
	if focus.Focus.Active || focus.Revision != cleared.Revision {
		t.Fatalf("focus after clear = %+v", focus)
	}
}

func TestAPackIsReadAndAppliedThroughTheSettings(t *testing.T) {
	d := newDevice(t, true)
	before := d.file()

	var list packList
	d.must("packs.list", nil, &list)
	if len(list.Packs) != len(engine.BuiltinPracticePacks()) || list.Packs[0].ID != "stoic" {
		t.Fatalf("packs.list = %+v", list)
	}

	// A file handed to the app is checked before anything is offered.
	examen := list.Packs[2]
	encoded, err := engine.EncodePracticePack(examen)
	if err != nil {
		t.Fatalf("EncodePracticePack: %v", err)
	}
	var read readPackResult
	d.must("packs.read", map[string]any{"data": string(encoded)}, &read)
	if !reflect.DeepEqual(read.Pack, examen) {
		t.Fatalf("packs.read = %+v, want %+v", read.Pack, examen)
	}
	d.refused("packs.read", "", map[string]any{"data": `{"seneca_pack": 1, "id": "x"}`}, "invalid-pack")

	var state settingsState
	d.must("settings.get", nil, &state)
	if state.Practice != nil {
		t.Fatalf("expected no practice before a pack, got %+v", state.Practice)
	}
	d.refused("settings.apply_pack", d.id(), map[string]any{"pack": examen}, "missing-base")
	d.refused("settings.apply_pack", d.id(), map[string]any{"base": "sha256:0", "pack": examen}, "stale-settings")
	broken := examen
	broken.ID = "Not An ID"
	d.refused("settings.apply_pack", d.id(), map[string]any{"base": state.Revision, "pack": broken}, "invalid-pack")

	var outcome settingsOutcome
	d.must("settings.apply_pack", map[string]any{"base": state.Revision, "pack": examen}, &outcome)
	practice := outcome.Settings.Practice
	if !outcome.Changed || !outcome.Settings.Initialized || practice == nil || practice.Pack.ID != "examen" ||
		practice.From != "2026-04-02" || practice.QuestionsChanged || !reflect.DeepEqual(outcome.Settings.Questions, examen.Questions) {
		t.Fatalf("settings.apply_pack = %+v", outcome)
	}
	if d.file() != before {
		t.Fatal("applying a pack rewrote the reflections file")
	}

	// An edit afterwards keeps the practice and says so.
	d.must("settings.update", map[string]any{"base": outcome.Settings.Revision, "anchor_question_index": 1}, &outcome)
	if outcome.Settings.Practice == nil || !outcome.Settings.Practice.QuestionsChanged {
		t.Fatalf("settings after an edit = %+v", outcome.Settings)
	}
}

func TestSettingsChangeAsOneConditionalUnit(t *testing.T) {
	d := newDevice(t, true)
	before := d.file()
	var state settingsState
	d.must("settings.get", nil, &state)
	if state.Initialized {
		t.Fatalf("settings = %+v", state)
	}
	d.refused("settings.update", d.id(), map[string]any{"base": state.Revision, "anchor_question_index": 1}, "settings-not-initialized")

	var outcome settingsOutcome
	d.must("settings.initialize", nil, &outcome)
	if !outcome.Changed || !outcome.Settings.Initialized || outcome.Settings.Revision != state.Revision {
		t.Fatalf("initialize = %+v", outcome)
	}
	d.must("settings.initialize", nil, &outcome)
	if outcome.Changed {
		t.Fatal("initializing twice changed the settings")
	}

	questions := []string{"What did I notice today?", "What will I carry into tomorrow?"}
	d.refused("settings.update", d.id(), map[string]any{"questions": questions}, "missing-base")
	update := map[string]any{"base": state.Revision, "questions": questions}
	first := d.send("settings.update", "settings-1", update)
	if !first.OK {
		t.Fatalf("settings.update failed: %+v", first.Error)
	}
	if err := decodeStrict(first.Result, &outcome); err != nil {
		t.Fatal(err)
	}
	// The anchor stays on the same question, wherever it moved.
	if !outcome.Changed || outcome.Settings.AnchorQuestionIndex != 2 || !reflect.DeepEqual(outcome.Settings.Questions, questions) {
		t.Fatalf("update = %+v", outcome)
	}
	if again := d.send("settings.update", "settings-1", update); !again.Replayed {
		t.Fatalf("repeated settings.update = %+v", again)
	}
	d.refused("settings.update", "settings-2", update, "stale-settings")
	d.refused("settings.update", d.id(), map[string]any{"base": outcome.Settings.Revision, "questions": []string{"What did I notice today?"}}, "anchor-moved")

	// The form follows the journal's settings now, not the device's.
	if draft := d.prepare(fixtureToday).Draft; !reflect.DeepEqual(draft.Questions, questions) || draft.SettingsRevision != outcome.Settings.Revision {
		t.Fatalf("draft = %+v", draft)
	}
	if d.file() != before {
		t.Fatal("a settings operation changed the reflections file")
	}
}

func TestWritingASaveCouldNotStoreIsKept(t *testing.T) {
	d := newDevice(t, true)
	draft := d.prepare(fixtureToday).Draft
	var found draftResult
	d.must("draft.get", map[string]any{"date": fixtureToday}, &found)
	if found.Found {
		t.Fatalf("draft = %+v, want none", found)
	}

	d.must("draft.retain", map[string]any{
		"draft": draft, "answers": []string{"Kept.", "", "Carry."}, "follow_through": "Walked.", "reason": "the app was closing",
	}, nil)
	prepared := d.prepare(fixtureToday)
	if prepared.Retained == nil || !reflect.DeepEqual(prepared.Retained.Answers, []string{"Kept.", "", "Carry."}) ||
		prepared.Retained.FollowThrough != "Walked." || prepared.Retained.Reason != "the app was closing" || len(prepared.Retained.Unasked) != 0 {
		t.Fatalf("retained = %+v", prepared.Retained)
	}

	// Saving the day drops what was kept for it.
	d.must("reflection.save", saveParams(draft, "Walked.", "Kept.", "", "Carry."), nil)
	d.must("draft.get", map[string]any{"date": fixtureToday}, &found)
	if found.Found {
		t.Fatal("the draft outlived the save")
	}

	d.must("draft.retain", map[string]any{"draft": draft, "answers": []string{"a", "b", "c"}, "reason": "again"}, nil)
	d.must("draft.discard", map[string]any{"date": fixtureToday}, nil)
	d.must("draft.get", map[string]any{"date": fixtureToday}, &found)
	if found.Found {
		t.Fatal("the draft outlived its discard")
	}
}

func TestTwoDevicesExchangeAndSettleAConflict(t *testing.T) {
	exchange := filepath.Join(t.TempDir(), "exchange")
	at := map[string]any{"exchange_path": exchange}

	mac := newDevice(t, true)
	phone := newDevice(t, false)

	var status syncStatusResult
	mac.must("sync.status", nil, &status)
	if status.Enrolled {
		t.Fatalf("status before enrolling = %+v", status)
	}
	mac.refused("sync.export", "", at, "sync-not-enrolled")
	var none conflictsResult
	mac.must("conflicts.list", nil, &none)
	if len(none.Conflicts) != 0 || len(none.FocusDisputes) != 0 {
		t.Fatalf("conflicts before enrolling = %+v", none)
	}

	mac.must("sync.enroll", map[string]any{"name": "mac"}, &status)
	if !status.Enrolled || status.Name != "mac" || status.Sequence != 1 || status.Journal == "" {
		t.Fatalf("enrolled = %+v", status)
	}
	var published syncPublished
	mac.must("sync.export", at, &published)
	if len(published.Transactions) != 1 {
		t.Fatalf("published = %+v", published)
	}

	phone.refused("sync.join", phone.id(), map[string]any{"exchange_path": "exchange", "name": "phone"}, "invalid-params")
	var joined syncStatusResult
	phone.must("sync.join", map[string]any{"exchange_path": exchange, "name": "phone"}, &joined)
	if joined.Journal != status.Journal || joined.Device == status.Device {
		t.Fatalf("joined = %+v, mac = %+v", joined, status)
	}
	var imported syncImported
	phone.must("sync.import", at, &imported)
	if len(imported.Applied) != 1 || !reflect.DeepEqual(imported.Written, []string{"reflections", "archive", "settings"}) {
		t.Fatalf("imported = %+v", imported)
	}
	var entries entriesResult
	phone.must("entries.list", nil, &entries)
	if len(entries.Entries) != 4 {
		t.Fatalf("the phone holds %d entries after its first import, want 4", len(entries.Entries))
	}

	// Both write the same day before either hears from the other.
	mac.must("reflection.save", saveParams(mac.prepare(fixtureToday).Draft, "", "From the Mac.", "", ""), nil)
	phone.must("reflection.save", saveParams(phone.prepare(fixtureToday).Draft, "", "From the phone.", "", ""), nil)
	mac.must("sync.export", at, nil)
	phone.must("sync.export", at, nil)
	mac.must("sync.import", at, &imported)
	if !reflect.DeepEqual(imported.Conflicts, []string{"entry:" + fixtureToday}) || len(imported.Written) != 0 {
		t.Fatalf("mac import = %+v, want one conflict and its own writing kept", imported)
	}
	phone.must("sync.import", at, &imported)

	var onMac, onPhone conflictsResult
	mac.must("conflicts.list", nil, &onMac)
	phone.must("conflicts.list", nil, &onPhone)
	if len(onMac.Conflicts) != 1 || len(onPhone.Conflicts) != 1 || onMac.Conflicts[0].ID != onPhone.Conflicts[0].ID {
		t.Fatalf("conflicts: mac %+v, phone %+v", onMac.Conflicts, onPhone.Conflicts)
	}
	conflict := onPhone.Conflicts[0]
	if conflict.Date != fixtureToday || !strings.Contains(conflict.Ours.Content, "From the phone.") ||
		len(conflict.Theirs) != 1 || !strings.Contains(conflict.Theirs[0].Content, "From the Mac.") || !conflict.Base.Deleted {
		t.Fatalf("conflict = %+v", conflict)
	}
	mac.must("sync.status", nil, &status)
	if !reflect.DeepEqual(status.Conflicts, []string{"entry:" + fixtureToday}) {
		t.Fatalf("status conflicts = %q", status.Conflicts)
	}

	// The phone answers it; an answer to a conflict that is not there is refused.
	phone.refused("conflict.resolve", phone.id(), map[string]any{"id": conflict.ID}, "invalid-params")
	phone.refused("conflict.resolve", phone.id(), map[string]any{"id": "0000", "keep": conflict.Ours.Revision}, "stale-conflict")
	answer := map[string]any{"id": conflict.ID, "keep": conflict.Theirs[0].Revision}
	first := phone.send("conflict.resolve", "resolve-1", answer)
	if !first.OK {
		t.Fatalf("conflict.resolve failed: %+v", first.Error)
	}
	var resolved syncResolved
	if err := decodeStrict(first.Result, &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Artifact != "entry:"+fixtureToday || resolved.Revision != conflict.Theirs[0].Revision ||
		!reflect.DeepEqual(resolved.Written, []string{"reflections"}) {
		t.Fatalf("resolved = %+v", resolved)
	}
	if again := phone.send("conflict.resolve", "resolve-1", answer); !again.Replayed {
		t.Fatalf("repeated conflict.resolve = %+v", again)
	}
	phone.refused("conflict.resolve", "resolve-2", answer, "stale-conflict")

	phone.must("sync.export", at, nil)
	mac.must("sync.import", at, &imported)
	mac.must("conflicts.list", nil, &onMac)
	if len(onMac.Conflicts) != 0 {
		t.Fatalf("the Mac still holds %+v after the phone's answer arrived", onMac.Conflicts)
	}
	var macEntry, phoneEntry entryResult
	mac.must("entry.get", map[string]any{"date": fixtureToday}, &macEntry)
	phone.must("entry.get", map[string]any{"date": fixtureToday}, &phoneEntry)
	if macEntry.Raw != phoneEntry.Raw || !strings.Contains(macEntry.Raw, "From the Mac.") {
		t.Fatalf("the devices disagree after settling:\nmac %q\nphone %q", macEntry.Raw, phoneEntry.Raw)
	}

	phone.refused("sync.recover", phone.id(), map[string]any{"file": "reflections", "action": "erase"}, "invalid-params")
	phone.refused("sync.recover", phone.id(), map[string]any{"file": "reflections", "action": "restore"}, "nothing-to-recover")
}

// The contract page is what the app is written against, so it lists exactly
// the operations and the error codes this package has.
func TestTheContractPageListsEveryOperationAndCode(t *testing.T) {
	data, err := os.ReadFile("../../docs/mobile/bridge.md")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	section := func(heading string) string {
		t.Helper()
		_, after, found := strings.Cut(page, "\n## "+heading+"\n")
		if !found {
			t.Fatalf("docs/mobile/bridge.md has no section %q", heading)
		}
		body, _, _ := strings.Cut(after, "\n## ")
		return body
	}
	rows := func(body string) map[string]bool {
		named := map[string]bool{}
		for _, match := range regexp.MustCompile("(?m)^\\| `([a-z_.-]+)` \\|").FindAllStringSubmatch(body, -1) {
			named[match[1]] = true
		}
		return named
	}

	documented := rows(section("Operations"))
	for _, name := range operationNames() {
		if !documented[name] {
			t.Errorf("the contract page does not list operation %s", name)
		}
		delete(documented, name)
	}
	for name := range documented {
		t.Errorf("the contract page lists operation %s, which this bridge does not have", name)
	}
	if !strings.Contains(page, fmt.Sprintf("this page is version %d of the contract", Version)) {
		t.Errorf("the contract page does not say it describes version %d", Version)
	}

	// Every code this package can answer with, read from its source.
	codes := map[string]bool{}
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`refuse\("([a-z-]+)"`),
		regexp.MustCompile(`Code:\s+"([a-z-]+)"`),
		regexp.MustCompile(`return "([a-z-]+)"`),
	}
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		text, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, pattern := range patterns {
			for _, match := range pattern.FindAllStringSubmatch(string(text), -1) {
				codes[match[1]] = true
			}
		}
	}
	if len(codes) < 30 {
		t.Fatalf("found only %d error codes in the source: %v", len(codes), codes)
	}
	documented = rows(section("Error codes"))
	for code := range codes {
		if !documented[code] {
			t.Errorf("the contract page does not list error code %s", code)
		}
		delete(documented, code)
	}
	for code := range documented {
		t.Errorf("the contract page lists error code %s, which this bridge never answers with", code)
	}
}

func TestTheBridgeLinksOnlyTheEngine(t *testing.T) {
	output, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Fatalf("listing the bridge's dependencies: %v", err)
	}
	for _, path := range strings.Fields(string(output)) {
		if path != "github.com/sglavoie/seneca/engine" && path != "github.com/sglavoie/seneca/mobile/senecabridge" {
			t.Errorf("the bridge depends on %s; only the engine and the standard library are bound for the phone", path)
		}
	}
}

func TestTheRoundTripFixtureIsTheCompatibilityJournal(t *testing.T) {
	for _, name := range []string{"reflections.md", engine.ArchiveFileName} {
		fixture, err := os.ReadFile(filepath.Join(fixtureDir, name))
		if err != nil {
			t.Fatal(err)
		}
		compat, err := os.ReadFile(filepath.Join(compatDir, "journal", name))
		if err != nil {
			t.Fatal(err)
		}
		if string(fixture) != string(compat) {
			t.Errorf("mobile/testdata/roundtrip/journal/%s differs from the compatibility baseline's; copy it again", name)
		}
	}
}

// The phone's way around an archive it cannot read: the context is refused
// with a stable code, and the same day prepared blank is written without the
// archive being read or touched.
func TestAnUnreadableArchiveStillPermitsABlankReflection(t *testing.T) {
	d := newDevice(t, true)
	archivePath := filepath.Join(d.dir, engine.ArchiveFileName)
	whole, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	cut := strings.Index(string(whole), "<!-- seneca:focus v=3 id=")
	if cut < 0 {
		t.Fatal("the fixture archive has no focus record")
	}
	// What an interrupted copy leaves: cut off inside a marker.
	torn := append([]byte(string(whole[:cut])+"<!-- seneca:focus v=3 id=93e"), 0x00, 0xff, 0xfe)
	if err := os.WriteFile(archivePath, torn, 0o644); err != nil {
		t.Fatal(err)
	}

	// The journal still opens and its reflections are still read.
	d.open(fixtureNow)
	var entries entriesResult
	d.must("entries.list", nil, &entries)
	if len(entries.Entries) == 0 {
		t.Fatal("the reflections are not listed beside an unreadable archive")
	}
	d.refused("reflection.prepare", "", map[string]any{"date": fixtureToday}, "unreadable-context")
	d.refused("focus.get", "", map[string]any{}, "unreadable-context")

	var prepared prepareResult
	d.must("reflection.prepare", map[string]any{"date": fixtureToday, "blank": true}, &prepared)
	context := prepared.Draft.Context
	if context.Focus != "" || context.ShowFollowThrough || context.PriorText != "" || prepared.IntentionLabel != "" {
		t.Fatalf("a blank draft carries context: %+v", prepared)
	}
	var saved savedReflection
	d.must("reflection.save", saveParams(prepared.Draft, "", "Noticed.", "Handled.", "Carry."), &saved)
	if saved.Date != fixtureToday || saved.IsEdit || !strings.Contains(d.file(), "## "+fixtureToday) {
		t.Fatalf("saved = %+v", saved)
	}
	after, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(torn) {
		t.Fatal("a blank save touched the unreadable archive")
	}
}

// An edit sent from the phone's form keeps what the form never showed: the
// section no question names, and the follow-through as it was stored.
func TestAnEditKeepsWhatNoQuestionNames(t *testing.T) {
	d := newDevice(t, true)
	var before entryResult
	d.must("entry.get", map[string]any{"date": "2026-03-05"}, &before)

	for _, blank := range []bool{false, true} {
		var prepared prepareResult
		d.must("reflection.prepare", map[string]any{"date": "2026-03-05", "blank": blank}, &prepared)
		draft := prepared.Draft
		if !draft.IsEdit || len(draft.Initial) != len(fixtureQuestions) {
			t.Fatalf("blank=%v: draft = %+v", blank, draft)
		}
		for _, text := range append(append([]string{}, draft.Initial...), draft.Context.FollowInitial) {
			if strings.Contains(text, "seneca:") || strings.Contains(text, "###") {
				t.Fatalf("blank=%v: the form is handed the file's structure: %q", blank, text)
			}
		}
		answers := append([]string{}, draft.Initial...)
		answers[0] += " Edited."
		var saved savedReflection
		d.must("reflection.save", saveParams(draft, draft.Context.FollowInitial, answers...), &saved)
		if !saved.IsEdit {
			t.Fatalf("blank=%v: saved = %+v", blank, saved)
		}
		raw := saved.Entry.Raw
		for _, kept := range []string{
			"### Gratitude\nSynthetic: a section no configured question names.",
			"<!-- seneca:follow-through source=2026-03-04 -->\n### Follow-through\n> Finish the draft before lunch.\n\nSynthetic: the draft was done by eleven.",
			answers[0],
		} {
			if !strings.Contains(raw, kept) {
				t.Fatalf("blank=%v: the edit lost %q:\n%s", blank, kept, raw)
			}
		}
		if len(saved.Entry.Sections) != len(before.Sections) {
			t.Fatalf("blank=%v: sections = %d, want %d", blank, len(saved.Entry.Sections), len(before.Sections))
		}
	}
}

// --- the exchange as a cloud folder carries it --------------------------------

// carryExchange copies files between two copies of an exchange directory, as
// the service between two devices eventually does. pick chooses which by
// their slash-separated name and may cut one short; nil carries all, whole.
func carryExchange(t *testing.T, from, to string, pick func(name string, data []byte) ([]byte, bool)) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if pick != nil {
			var carried bool
			if data, carried = pick(filepath.ToSlash(relative), data); !carried {
				return nil
			}
		}
		target := filepath.Join(to, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// syncedPair is a Mac that enrolled the fixture journal and a phone that
// joined it, each with its own copy of the exchange folder.
func syncedPair(t *testing.T) (mac, phone *device, macCloud, phoneCloud map[string]any) {
	t.Helper()
	root := t.TempDir()
	macCloud = map[string]any{"exchange_path": filepath.Join(root, "mac-cloud")}
	phoneCloud = map[string]any{"exchange_path": filepath.Join(root, "phone-cloud")}
	mac, phone = newDevice(t, true), newDevice(t, false)

	mac.must("sync.enroll", map[string]any{"name": "mac"}, nil)
	mac.must("sync.export", macCloud, nil)
	carryExchange(t, macCloud["exchange_path"].(string), phoneCloud["exchange_path"].(string), nil)
	phone.must("sync.join", map[string]any{"exchange_path": phoneCloud["exchange_path"], "name": "phone"}, nil)
	var imported syncImported
	phone.must("sync.import", phoneCloud, &imported)
	if len(imported.Applied) != 1 || len(imported.Diagnostics) != 0 {
		t.Fatalf("the phone's first import = %+v", imported)
	}
	return mac, phone, macCloud, phoneCloud
}

// The phone's status tells a change that is saved on it from one that is in
// the exchange and from one the Mac has in its own journal, and says each
// only once it is true.
func TestDeliveryTellsSavedFromPublishedFromApplied(t *testing.T) {
	mac, phone, macCloud, phoneCloud := syncedPair(t)
	macDir, phoneDir := macCloud["exchange_path"].(string), phoneCloud["exchange_path"].(string)

	var before, delivery syncDeliveryResult
	phone.must("sync.delivery", phoneCloud, &before)
	phone.must("reflection.save", saveParams(phone.prepare(fixtureToday).Draft, "", "Written offline on the phone.", "", ""), nil)

	phone.must("sync.delivery", phoneCloud, &delivery)
	if delivery.Recorded != before.Recorded+1 || delivery.Published != before.Published {
		t.Fatalf("after a save delivery = %+v, was %+v: want one more recorded and no more published", delivery, before)
	}

	var published syncPublished
	phone.must("sync.export", phoneCloud, &published)
	if len(published.Transactions) != 1 || published.Receipt == "" {
		t.Fatalf("published = %+v", published)
	}
	phone.must("sync.delivery", phoneCloud, &delivery)
	if delivery.Published != delivery.Recorded {
		t.Fatalf("after publishing delivery = %+v", delivery)
	}
	for _, peer := range delivery.Peers {
		if peer.Applied != 0 {
			t.Fatalf("a peer is reported as having applied %d before it said so: %+v", peer.Applied, delivery)
		}
	}

	// The files reaching the Mac's folder change nothing in the report.
	carryExchange(t, phoneDir, macDir, nil)
	carryExchange(t, macDir, phoneDir, nil)
	phone.must("sync.delivery", phoneCloud, &delivery)
	for _, peer := range delivery.Peers {
		if peer.Applied != 0 {
			t.Fatalf("delivery of a manifest was reported as application: %+v", delivery)
		}
	}

	var imported syncImported
	mac.must("sync.import", macCloud, &imported)
	if len(imported.Applied) != 1 || !reflect.DeepEqual(imported.Written, []string{"reflections"}) {
		t.Fatalf("mac import = %+v", imported)
	}
	mac.must("sync.export", macCloud, &published)
	if !published.ReceiptWritten {
		t.Fatalf("the Mac published no receipt after applying: %+v", published)
	}
	carryExchange(t, macDir, phoneDir, nil)
	phone.must("sync.delivery", phoneCloud, &delivery)
	if len(delivery.Peers) != 1 || delivery.Peers[0].Name != "mac" || delivery.Peers[0].Applied != delivery.Recorded {
		t.Fatalf("after the Mac's receipt delivery = %+v, want everything applied on mac", delivery)
	}
	if !strings.Contains(mac.file(), "Written offline on the phone.") {
		t.Fatal("the receipt says applied, and the Mac's journal lacks the reflection")
	}

	mac.must("sync.export", macCloud, &published)
	if published.ReceiptWritten {
		t.Fatalf("an unchanged device published another receipt: %+v", published)
	}
	phone.refused("sync.delivery", "", map[string]any{"exchange_path": "relative"}, "invalid-params")
}

// A cloud folder delivers files in any order and sometimes in part. Nothing
// is applied until a transaction is whole, nothing partial is kept, and the
// journal is untouched in between.
func TestAnExchangeArrivingOutOfOrderOrCutShortIsAppliedOnlyWhole(t *testing.T) {
	mac, phone, macCloud, phoneCloud := syncedPair(t)
	macDir, phoneDir := macCloud["exchange_path"].(string), phoneCloud["exchange_path"].(string)

	mac.must("reflection.save", saveParams(mac.prepare(fixtureToday).Draft, "", "From the Mac.", "", ""), nil)
	mac.must("sync.export", macCloud, nil)
	before := phone.file()
	incomplete := func(imported syncImported) bool {
		for _, diagnostic := range imported.Diagnostics {
			if diagnostic.Code == string(engine.SyncDiagIncomplete) {
				return true
			}
		}
		return false
	}

	// The manifest first, its contents not yet.
	carryExchange(t, macDir, phoneDir, func(name string, data []byte) ([]byte, bool) {
		return data, strings.HasPrefix(name, "transactions/")
	})
	var imported syncImported
	phone.must("sync.import", phoneCloud, &imported)
	if len(imported.Applied) != 0 || len(imported.Written) != 0 || !incomplete(imported) || phone.file() != before {
		t.Fatalf("a manifest without its contents: import = %+v", imported)
	}

	// The contents, cut short on the way.
	carryExchange(t, macDir, phoneDir, func(name string, data []byte) ([]byte, bool) {
		return data[:len(data)/2], strings.HasPrefix(name, "objects/")
	})
	phone.must("sync.import", phoneCloud, &imported)
	if len(imported.Applied) != 0 || !incomplete(imported) || phone.file() != before {
		t.Fatalf("contents cut short: import = %+v", imported)
	}
	var status syncStatusResult
	phone.must("sync.status", nil, &status)
	if len(status.Pending) != 0 {
		t.Fatalf("a transaction that has not arrived whole was stored: %+v", status.Pending)
	}

	// Everything, whole.
	carryExchange(t, macDir, phoneDir, nil)
	phone.must("sync.import", phoneCloud, &imported)
	if len(imported.Applied) != 1 || len(imported.Diagnostics) != 0 || !strings.Contains(phone.file(), "From the Mac.") {
		t.Fatalf("the whole transaction: import = %+v", imported)
	}
	// Delivered again, it is not applied again.
	carryExchange(t, macDir, phoneDir, nil)
	after := phone.file()
	phone.must("sync.import", phoneCloud, &imported)
	if len(imported.Applied) != 0 || len(imported.Written) != 0 || phone.file() != after {
		t.Fatalf("a second delivery: import = %+v", imported)
	}
}

// A publication stopped after its contents and before its manifests leaves
// nothing another device can see, and publishing again finishes it.
func TestAnInterruptedPublicationIsInvisibleUntilItIsFinished(t *testing.T) {
	mac, phone, macCloud, phoneCloud := syncedPair(t)
	macDir, phoneDir := macCloud["exchange_path"].(string), phoneCloud["exchange_path"].(string)

	phone.must("reflection.save", saveParams(phone.prepare(fixtureToday).Draft, "", "Sent in two halves.", "", ""), nil)
	phone.must("sync.export", phoneCloud, nil)
	before := mac.file()

	// Contents are published before the manifest that names them, so an
	// upload that stops in between carries contents alone.
	carryExchange(t, phoneDir, macDir, func(name string, data []byte) ([]byte, bool) {
		return data, strings.HasPrefix(name, "objects/")
	})
	var imported syncImported
	mac.must("sync.import", macCloud, &imported)
	if len(imported.Applied) != 0 || len(imported.Diagnostics) != 0 || mac.file() != before {
		t.Fatalf("contents without a manifest: import = %+v", imported)
	}

	// The phone publishes again: only what is missing is sent, and it is the
	// same files.
	var again syncPublished
	phone.must("sync.export", phoneCloud, &again)
	if len(again.Transactions) != 0 {
		t.Fatalf("publishing again sent %+v", again)
	}
	carryExchange(t, phoneDir, macDir, nil)
	mac.must("sync.import", macCloud, &imported)
	if len(imported.Applied) != 1 || !strings.Contains(mac.file(), "Sent in two halves.") {
		t.Fatalf("the finished publication: import = %+v", imported)
	}
}
