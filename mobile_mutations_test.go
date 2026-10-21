package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/seneca/engine"
	"github.com/sglavoie/seneca/mobile/senecabridge"
)

// Every change the phone can make goes through the bridge the app links, and
// reaches the desktop through an exchange directory, where the real seneca
// program imports it into Markdown and a Git commit. The desktop then answers
// with changes of its own, which the phone imports. The phone here is the
// bridge alone: the same requests the app sends, with no app around them.

// bridgePhone is the phone: a bridge over a journal and a state directory of
// its own, sharing only the exchange with the desktop.
type bridgePhone struct {
	t        *testing.T
	bridge   *senecabridge.Bridge
	dir      string
	exchange string
	next     int
}

type bridgeAnswer struct {
	OK       bool            `json:"ok"`
	Replayed bool            `json:"replayed"`
	Result   json.RawMessage `json:"result"`
	Error    *struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

func newBridgePhone(t *testing.T, exchange string) *bridgePhone {
	t.Helper()
	root := t.TempDir()
	p := &bridgePhone{t: t, bridge: senecabridge.NewBridge(), dir: filepath.Join(root, "journal"), exchange: exchange}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p.must("journal.open", map[string]any{
		"reflections_path":      p.reflections(),
		"state_dir":             filepath.Join(root, "state"),
		"questions":             syncQuestions,
		"anchor_question_index": 3,
		"time_zone":             "UTC",
	}, nil)
	return p
}

func (p *bridgePhone) reflections() string { return filepath.Join(p.dir, "reflections.md") }

// send makes one request. Every request carries an identity of its own; only
// mutations are held to it.
func (p *bridgePhone) send(operation string, params any) bridgeAnswer {
	p.t.Helper()
	p.next++
	raw, err := json.Marshal(map[string]any{
		"version": senecabridge.BridgeVersion(), "operation": operation,
		"request_id": fmt.Sprintf("%s-%d", operation, p.next), "params": params,
	})
	if err != nil {
		p.t.Fatal(err)
	}
	var answer bridgeAnswer
	if err := json.Unmarshal([]byte(p.bridge.Call(string(raw))), &answer); err != nil {
		p.t.Fatalf("phone: %s answered with no response: %v", operation, err)
	}
	return answer
}

func (p *bridgePhone) must(operation string, params any, into any) {
	p.t.Helper()
	answer := p.send(operation, params)
	if !answer.OK {
		p.t.Fatalf("phone: %s refused: %s: %s", operation, answer.Error.Code, answer.Error.Message)
	}
	if into != nil {
		if err := json.Unmarshal(answer.Result, into); err != nil {
			p.t.Fatalf("phone: %s result: %v", operation, err)
		}
	}
}

func (p *bridgePhone) refused(operation string, params any, code string) {
	p.t.Helper()
	if answer := p.send(operation, params); answer.OK || answer.Error.Code != code {
		p.t.Fatalf("phone: %s = %+v, want refusal %s", operation, answer, code)
	}
}

// exchangeNow imports what the desktop published, then publishes what the
// phone recorded, as the app's exchange does.
func (p *bridgePhone) exchangeNow() (imported struct {
	Conflicts []string `json:"conflicts"`
	Written   []string `json:"written"`
}) {
	p.t.Helper()
	at := map[string]any{"exchange_path": p.exchange}
	p.must("sync.import", at, &imported)
	p.must("sync.export", at, nil)
	return imported
}

type phoneDraft = map[string]any

// write saves a day the way the form does: prepared, then saved against the
// draft. It returns the answer, refused or not.
func (p *bridgePhone) write(date string, answers ...string) bridgeAnswer {
	p.t.Helper()
	var prepared struct {
		Draft phoneDraft `json:"draft"`
	}
	p.must("reflection.prepare", map[string]any{"date": date, "blank": true}, &prepared)
	questions, _ := prepared.Draft["questions"].([]any)
	for len(answers) < len(questions) {
		answers = append(answers, "")
	}
	return p.send("reflection.save", map[string]any{"draft": prepared.Draft, "answers": answers})
}

func (p *bridgePhone) mustWrite(date string, answers ...string) {
	p.t.Helper()
	if answer := p.write(date, answers...); !answer.OK {
		p.t.Fatalf("phone: saving %s refused: %s: %s", date, answer.Error.Code, answer.Error.Message)
	}
}

func (p *bridgePhone) entry(date string) string {
	p.t.Helper()
	var entry struct {
		Raw string `json:"raw"`
	}
	p.must("entry.get", map[string]any{"date": date}, &entry)
	return entry.Raw
}

type phoneFocus struct {
	Focus struct {
		Active   bool   `json:"active"`
		Text     string `json:"text"`
		Disputed bool   `json:"disputed"`
	} `json:"focus"`
	Revision string `json:"revision"`
}

func (p *bridgePhone) focus() phoneFocus {
	p.t.Helper()
	var reading phoneFocus
	p.must("focus.get", nil, &reading)
	return reading
}

type phoneSettings struct {
	Initialized         bool     `json:"initialized"`
	Questions           []string `json:"questions"`
	AnchorQuestionIndex int      `json:"anchor_question_index"`
	Revision            string   `json:"revision"`
}

func (p *bridgePhone) settings() phoneSettings {
	p.t.Helper()
	var settings phoneSettings
	p.must("settings.get", nil, &settings)
	return settings
}

type phoneConflicts struct {
	Conflicts []struct {
		ID     string `json:"id"`
		Date   string `json:"date"`
		Ours   struct{ Revision, Content string }
		Theirs []struct{ Revision, Content string }
	} `json:"conflicts"`
	FocusDisputes []struct {
		ID         string `json:"id"`
		Candidates []struct {
			Record string `json:"record"`
			Text   string `json:"text"`
		} `json:"candidates"`
	} `json:"focus_disputes"`
}

func (p *bridgePhone) conflicts() phoneConflicts {
	p.t.Helper()
	var listed phoneConflicts
	p.must("conflicts.list", nil, &listed)
	return listed
}

// show is the desktop's reading of one day: exactly the entry's bytes.
func (d *desk) show(date string) string {
	d.t.Helper()
	return strings.TrimSuffix(d.must("show", "--date", date), "\n")
}

func utcDay(offset int) string {
	return time.Now().UTC().AddDate(0, 0, offset).Format("2006-01-02")
}

func requireContains(t *testing.T, what, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Fatalf("%s does not contain %q:\n%s", what, want, text)
	}
}

// A desktop in UTC, so its day is the phone's.
func utcDesk(t *testing.T) *desk {
	t.Helper()
	d := newDesk(t)
	d.env = append(d.env, "TZ=UTC")
	return d
}

func TestEveryPhoneMutationRoundTripsThroughTheDesktop(t *testing.T) {
	d := utcDesk(t)
	d.must("sync", "enroll", "--name", "desk")
	p := newBridgePhone(t, d.exchange)
	p.must("sync.join", map[string]any{"exchange_path": d.exchange, "name": "phone"}, nil)
	p.exchangeNow()
	requireContains(t, "the phone's copy of 2026-01-02", p.entry("2026-01-02"), "Apologized.")
	today, yesterday := utcDay(0), utcDay(-1)
	imports := d.count("seneca: sync import")

	// reflection.save: the phone's day reaches the desktop's Markdown and Git.
	p.mustWrite(today, "Phone: rushed the morning.", "Phone: kept the walk.", "Phone: leave earlier.")
	p.exchangeNow()
	d.must("sync", "once")
	if d.show(today) != p.entry(today) {
		t.Fatalf("the desktop shows today as\n%s\nthe phone holds\n%s", d.show(today), p.entry(today))
	}
	if d.count("seneca: sync import") != imports+1 {
		t.Fatalf("the phone's day was not committed on its own: %v", d.subjects())
	}
	// ...and a day the author writes on the desktop comes back.
	d.write(d.reflections, d.read(d.reflections)+seedEntry(yesterday, "Desk: late.", "Desk: tidy.", "Desk: earlier."))
	d.must("sync", "once")
	p.exchangeNow()
	if p.entry(yesterday) != d.show(yesterday) {
		t.Fatalf("the desktop's day reached the phone as\n%s", p.entry(yesterday))
	}

	// settings.update: new questions and a new anchor, as one unit.
	settings := p.settings()
	if !settings.Initialized {
		t.Fatalf("the phone's settings after joining = %+v", settings)
	}
	questions := append(append([]string{}, settings.Questions...), "What surprised me?")
	p.must("settings.update", map[string]any{"base": settings.Revision, "questions": questions, "anchor_question_index": 4}, nil)
	p.exchangeNow()
	d.must("sync", "once")
	desktopSettings := d.must("settings")
	requireContains(t, "the desktop's settings", desktopSettings, "4. What surprised me?")
	requireContains(t, "the desktop's settings", desktopSettings, "Anchor: question 4")
	d.must("settings", "set", "--anchor", "2")
	p.exchangeNow()
	if settings = p.settings(); settings.AnchorQuestionIndex != 2 || len(settings.Questions) != 4 {
		t.Fatalf("the desktop's anchor reached the phone as %+v", settings)
	}

	// focus.set and focus.clear.
	p.must("focus.set", map[string]any{"base": p.focus().Revision, "text": "Phone: one thing at a time."}, nil)
	p.exchangeNow()
	d.must("sync", "once")
	requireContains(t, "the desktop's focus", d.must("focus"), "Phone: one thing at a time.")
	d.must("focus", "set", "Desk: finish the draft.")
	p.exchangeNow()
	if focus := p.focus(); focus.Focus.Text != "Desk: finish the draft." {
		t.Fatalf("the desktop's focus reached the phone as %+v", focus)
	}
	p.must("focus.clear", map[string]any{"base": p.focus().Revision}, nil)
	p.exchangeNow()
	d.must("sync", "once")
	requireContains(t, "the desktop's focus", d.must("focus"), "No focus")

	// focus_dispute.resolve: both set a focus before hearing from the other.
	p.must("focus.set", map[string]any{"base": p.focus().Revision, "text": "Phone: walk first."}, nil)
	d.must("focus", "set", "Desk: write first.")
	p.exchangeNow()
	d.must("sync", "once")
	requireContains(t, "the desktop's focus", d.must("focus"), "disputed")
	disputes := p.conflicts().FocusDisputes
	if len(disputes) != 1 || len(disputes[0].Candidates) != 2 {
		t.Fatalf("the phone's disputes = %+v", disputes)
	}
	p.must("focus_dispute.resolve", map[string]any{"id": disputes[0].ID, "text": "Phone and desk: walk, then write."}, nil)
	p.exchangeNow()
	d.must("sync", "once")
	requireContains(t, "the desktop's focus", d.must("focus"), "Phone and desk: walk, then write.")
	if out := d.must("sync", "conflicts"); !strings.Contains(out, "No conflicts.") {
		t.Fatalf("the desktop still lists\n%s", out)
	}

	// conflict.resolve, keeping a version: both edit today.
	p.mustWrite(today, "Phone: second thoughts.", "Phone: kept the walk.", "Phone: leave earlier.")
	d.write(d.reflections, strings.Replace(d.read(d.reflections), "Phone: rushed the morning.", "Desk: rushed, and said so.", 1))
	d.must("sync", "once")
	if imported := p.exchangeNow(); len(imported.Conflicts) != 1 {
		t.Fatalf("the phone's import = %+v, want one conflict", imported)
	}
	d.must("sync", "once")
	requireContains(t, "the desktop's conflicts", d.must("sync", "conflicts"), today)
	conflict := p.conflicts().Conflicts[0]
	p.must("conflict.resolve", map[string]any{"id": conflict.ID, "keep": conflict.Theirs[0].Revision}, nil)
	p.exchangeNow()
	d.must("sync", "once")
	if out := d.must("sync", "conflicts"); !strings.Contains(out, "No conflicts.") {
		t.Fatalf("the desktop still lists\n%s", out)
	}
	requireContains(t, "the desktop's today", d.show(today), "Desk: rushed, and said so.")
	if d.show(today) != p.entry(today) {
		t.Fatalf("after keeping the desktop's version the two disagree:\n%s\n%s", d.show(today), p.entry(today))
	}
	requireSameDays(t, d, p)
}

func TestAConflictSettledOfflineConvergesOnceTheExchangeResumes(t *testing.T) {
	d := utcDesk(t)
	d.must("sync", "enroll", "--name", "desk")
	p := newBridgePhone(t, d.exchange)
	p.must("sync.join", map[string]any{"exchange_path": d.exchange, "name": "phone"}, nil)
	p.exchangeNow()
	today, yesterday := utcDay(0), utcDay(-1)

	p.mustWrite(today, "Phone: the first version.")
	p.exchangeNow()
	d.must("sync", "once")
	// Both edit the day before either hears from the other.
	p.mustWrite(today, "Phone: the second version.")
	d.write(d.reflections, strings.Replace(d.read(d.reflections), "Phone: the first version.", "Desk: another version.", 1))
	d.must("sync", "once")
	if imported := p.exchangeNow(); len(imported.Conflicts) != 1 {
		t.Fatalf("the phone's import = %+v, want one conflict", imported)
	}
	d.must("sync", "once")
	requireContains(t, "the desktop's conflicts", d.must("sync", "conflicts"), today)

	// Offline from here: nothing is exchanged. Writing for the day waits for
	// the author's choice, and the phone's version stays readable meanwhile.
	journal := readFile(t, p.reflections())
	if answer := p.write(today, "Phone: typed while the day was conflicted."); answer.OK || answer.Error.Code != "conflicted" {
		t.Fatalf("saving a conflicted day = %+v, want refusal conflicted", answer)
	}
	if readFile(t, p.reflections()) != journal {
		t.Fatal("a refused save changed the phone's journal")
	}
	requireContains(t, "the phone's conflicted day", p.entry(today), "Phone: the second version.")
	conflict := p.conflicts().Conflicts[0]
	edited := strings.Replace(conflict.Ours.Content, "Phone: the second version.", "Phone and desk: both versions, in one.", 1)
	p.must("conflict.resolve", map[string]any{"id": conflict.ID, "content": edited}, nil)
	// The kept writing is saved once the day is settled, still offline.
	p.mustWrite(today, "Phone and desk: both versions, in one.", "Phone: typed while the day was conflicted.")
	// The desktop goes on meanwhile.
	d.write(d.reflections, d.read(d.reflections)+seedEntry(yesterday, "Desk: offline day.", "", ""))
	d.must("sync", "once")

	// The exchange resumes, in whatever order the devices come back.
	p.exchangeNow()
	d.must("sync", "once")
	p.exchangeNow()
	d.must("sync", "once")

	if out := d.must("sync", "conflicts"); !strings.Contains(out, "No conflicts.") {
		t.Fatalf("the desktop still lists\n%s", out)
	}
	if listed := p.conflicts(); len(listed.Conflicts) != 0 || len(listed.FocusDisputes) != 0 {
		t.Fatalf("the phone still lists %+v", listed)
	}
	for _, day := range []string{today, yesterday} {
		if d.show(day) != p.entry(day) {
			t.Fatalf("%s differs:\ndesktop\n%s\nphone\n%s", day, d.show(day), p.entry(day))
		}
	}
	requireContains(t, "today on the desktop", d.show(today), "Phone: typed while the day was conflicted.")
	requireSameDays(t, d, p)
	if dirty := d.git("status", "--porcelain", "--", d.reflections); dirty != "" {
		t.Fatalf("the desktop left its journal uncommitted:\n%s", dirty)
	}
}

func TestAJournalStartedOnThePhoneReachesTheDesktop(t *testing.T) {
	d := utcDesk(t)
	// The desktop's journal is empty: the phone's is the one.
	d.write(d.reflections, "")
	d.git("commit", "--quiet", "-am", "an empty journal")

	p := newBridgePhone(t, d.exchange)
	p.mustWrite(utcDay(-2), "Phone: before sync.")
	var initialized struct {
		Changed bool `json:"changed"`
	}
	p.must("settings.initialize", nil, &initialized)
	if !initialized.Changed {
		t.Fatal("settings.initialize changed nothing on a journal without settings")
	}
	p.must("sync.enroll", map[string]any{"name": "phone"}, nil)
	p.exchangeNow()

	d.must("sync", "enroll", "--join", "--name", "desk")
	requireContains(t, "the desktop's journal", d.show(utcDay(-2)), "Phone: before sync.")
	desktopSettings := d.must("settings")
	for _, question := range syncQuestions {
		requireContains(t, "the desktop's settings", desktopSettings, question)
	}

	// sync.recover: the phone's file goes missing and is restored from history.
	if err := os.Remove(p.reflections()); err != nil {
		t.Fatal(err)
	}
	p.exchangeNow()
	var status struct {
		Suspended []struct{ File string } `json:"suspended"`
	}
	p.must("sync.status", nil, &status)
	if len(status.Suspended) != 1 || status.Suspended[0].File != "reflections" {
		t.Fatalf("the phone's status after losing its file = %+v", status)
	}
	p.must("sync.recover", map[string]any{"file": "reflections", "action": "restore"}, nil)
	requireContains(t, "the phone's restored day", p.entry(utcDay(-2)), "Phone: before sync.")
	p.mustWrite(utcDay(-1), "Phone: after the restore.")
	p.exchangeNow()
	d.must("sync", "once")
	requireContains(t, "the desktop's journal", d.show(utcDay(-1)), "Phone: after the restore.")
	requireSameDays(t, d, p)
}

// requireSameDays holds the two devices to the same days with the same bytes.
// Their files may still differ in where a day sits and in legacy framing
// until each has rewritten it: devices agree on entries, not on files.
func requireSameDays(t *testing.T, d *desk, p *bridgePhone) {
	t.Helper()
	var listed struct {
		Entries []struct{ Date string } `json:"entries"`
	}
	p.must("entries.list", nil, &listed)
	desktop := engine.ParseReflectionDocument(d.read(d.reflections)).Entries
	if len(desktop) != len(listed.Entries) {
		t.Fatalf("the desktop holds %d days and the phone %d", len(desktop), len(listed.Entries))
	}
	for _, entry := range desktop {
		if phone := p.entry(entry.Date); phone != entry.Raw {
			t.Fatalf("%s differs:\ndesktop\n%s\nphone\n%s", entry.Date, entry.Raw, phone)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
