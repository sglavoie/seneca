package senecabridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/seneca/engine"
)

func TestRemindersArePlannedFromWhatThisDeviceHolds(t *testing.T) {
	d := newDevice(t, true)
	before := d.file()

	var plan reminderPlan
	d.must("reminders.plan", nil, &plan)
	if plan.Today != fixtureToday || plan.TimeZone != "UTC" || strings.Join(plan.Times, ",") != "20:00,21:00,21:30,22:00" ||
		strings.Join(plan.Defaults, ",") != strings.Join(engine.DefaultReminderTimes, ",") {
		t.Fatalf("expected the desktop's evening schedule from today, got %+v", plan)
	}
	if len(plan.Reminders) != 28 || plan.Reminders[0].At != "2026-04-02T20:00:00Z" || len(plan.Complete) != 0 {
		t.Fatalf("expected a week of evenings, got %d reminders from %+v", len(plan.Reminders), plan.Reminders[0])
	}
	if d.file() != before {
		t.Fatal("planning changed the journal")
	}

	// Saving the day completes it: its evening is not reminded.
	d.must("reflection.save", saveParams(d.prepare(fixtureToday).Draft, "", "Noticed.", "", ""), nil)
	d.must("reminders.plan", map[string]any{"times": []string{"21:30", "07:05"}, "days": 2}, &plan)
	if strings.Join(plan.Complete, ",") != fixtureToday || len(plan.Reminders) != 2 ||
		plan.Reminders[0].At != "2026-04-03T07:05:00Z" || plan.Reminders[1].Date != "2026-04-03" {
		t.Fatalf("expected only tomorrow's reminders, got %+v", plan)
	}

	// The same instant somewhere else is another evening.
	params := d.openParams(fixtureNow)
	params["time_zone"] = "Asia/Tokyo"
	params["now"] = "2026-04-02T19:00:00Z"
	d.must("journal.open", params, nil)
	d.must("reminders.plan", map[string]any{"days": 1}, &plan)
	if plan.Today != "2026-04-03" || plan.TimeZone != "Asia/Tokyo" || plan.Reminders[0].At != "2026-04-03T20:00:00+09:00" {
		t.Fatalf("expected the plan to follow the journal's zone, got %+v", plan)
	}

	d.refused("reminders.plan", "", map[string]any{"times": []string{"9pm"}}, "invalid-reminders")
	d.refused("reminders.plan", "", map[string]any{"days": 40}, "invalid-reminders")
}

func TestDiagnosticsRecoveryAndLeavingThroughTheBridge(t *testing.T) {
	exchange := filepath.Join(t.TempDir(), "exchange")
	at := map[string]any{"exchange_path": exchange}
	mac := newDevice(t, true)
	phone := newDevice(t, false)
	var status syncStatusResult
	mac.must("sync.enroll", map[string]any{"name": "mac"}, &status)
	mac.must("sync.export", at, nil)
	phone.must("sync.join", map[string]any{"exchange_path": exchange, "name": "phone"}, nil)
	phone.must("sync.import", at, nil)
	mac.must("reflection.save", saveParams(mac.prepare(fixtureToday).Draft, "", "Walrus from the Mac.", "", ""), nil)
	phone.must("reflection.save", saveParams(phone.prepare(fixtureToday).Draft, "", "Toucan from the phone.", "", ""), nil)
	mac.must("sync.export", at, nil)
	phone.must("sync.import", at, nil)

	// Diagnostics: counts and codes, and nothing written.
	var diagnostics engine.Diagnostics
	r := phone.must("diagnostics.collect", at, &diagnostics)
	if !diagnostics.Sync.Enrolled || !diagnostics.Has(engine.FindingConflictsOpen) || !diagnostics.Has(engine.FindingChangesUnpublished) {
		t.Fatalf("expected an open conflict and unsent changes, got %s", r.Result)
	}
	var phoneStatus syncStatusResult
	phone.must("sync.status", nil, &phoneStatus)
	for _, secret := range []string{"Walrus", "Toucan", "What did I", "2026-", phone.dir, "phone", "mac", phoneStatus.Device, phoneStatus.Journal} {
		if strings.Contains(string(r.Result), secret) {
			t.Fatalf("the diagnostics carry %q: %s", secret, r.Result)
		}
	}
	var unsynced engine.Diagnostics
	newDevice(t, true).must("diagnostics.collect", nil, &unsynced)
	if unsynced.Sync.Enrolled || unsynced.Journal.Entries != 4 || len(unsynced.Findings) != 0 {
		t.Fatalf("expected a quiet report on a journal outside sync, got %+v", unsynced)
	}

	// A recovery export holds both versions of the day and the app's drafts.
	drafts := t.TempDir()
	if err := os.WriteFile(filepath.Join(drafts, "2026-04-03.json"), []byte(`{"answers":["Unsaved."]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "Seneca Export")
	var exported recoveryResult
	phone.must("recovery.export", map[string]any{"destination": destination, "drafts_dir": drafts}, &exported)
	if exported.Directory != destination || exported.ConflictVersions < 2 || exported.Drafts != 1 {
		t.Fatalf("unexpected export %+v", exported)
	}
	all := ""
	for _, file := range exported.Files {
		data, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatal(err)
		}
		all += string(data)
	}
	if !strings.Contains(all, "Walrus from the Mac.") || !strings.Contains(all, "Toucan from the phone.") || strings.Contains(all, phoneStatus.Device) {
		t.Fatal("expected every version of the conflict and no device identity in the export")
	}
	phone.refused("recovery.export", "", map[string]any{"destination": destination}, "destination-exists")
	phone.refused("recovery.export", "", map[string]any{"destination": "relative"}, "invalid-params")

	// Leaving: the files stay, the identity retires, and joining again is as
	// a new device that keeps its writing.
	before := phone.file()
	var left syncLeft
	phone.must("sync.leave", nil, &left)
	if left.Device != phoneStatus.Device || left.Conflicts != 1 || phone.file() != before {
		t.Fatalf("unexpected leave %+v", left)
	}
	phone.must("sync.status", nil, &status)
	if status.Enrolled {
		t.Fatal("expected the journal out of sync")
	}
	phone.refused("sync.leave", phone.id(), nil, "sync-not-enrolled")
	var rejoined syncStatusResult
	phone.must("sync.join", map[string]any{"exchange_path": exchange, "name": "phone"}, &rejoined)
	if rejoined.Device == phoneStatus.Device || rejoined.Journal != phoneStatus.Journal {
		t.Fatalf("expected a new device of the same journal, got %+v", rejoined)
	}

	// A sync state from a newer seneca is named, and leaving clears it.
	state := engine.SyncStatePathFor(phone.reflections())
	if err := os.WriteFile(state, []byte(`{"version": 5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	phone.refused("sync.status", "", nil, "sync-state-version")
	phone.must("diagnostics.collect", nil, &diagnostics)
	if !diagnostics.Has(engine.FindingSyncStateNewer) {
		t.Fatal("expected the newer sync state to be named")
	}
	phone.must("sync.leave", nil, &left)
	if left.Device != "" {
		t.Fatalf("a state that cannot be read names no device, got %+v", left)
	}
	if phone.file() != before {
		t.Fatal("leaving changed the journal")
	}
}
