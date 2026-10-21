package cmd

import (
	"encoding/json"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// syncedCompatJournal enrolls the compatibility journal in sync through the
// engine and publishes it to an exchange of the test's own, and joins a second
// device to it that has one change of its own waiting in the exchange.
func syncedCompatJournal(t *testing.T) (compatJournal, string) {
	t.Helper()

	j := newCompatJournal(t)
	exchange := t.TempDir()
	now := time.Date(2026, time.April, 2, 21, 0, 0, 0, time.UTC)
	open := func(path string) *engine.Journal {
		journal, err := engine.Open(engine.Options{ReflectionsPath: path, Settings: defaultCompatSettings(t), Clock: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		return journal
	}
	desk := open(j.reflections)
	if _, err := desk.EnrollSync("desk"); err != nil {
		t.Fatal(err)
	}
	if _, err := desk.PublishSync(engine.NewDirectoryTransport(exchange)); err != nil {
		t.Fatal(err)
	}
	phone := open(filepath.Join(t.TempDir(), "reflections.md"))
	if _, err := phone.JoinSync(engine.NewDirectoryTransport(exchange), "phone"); err != nil {
		t.Fatal(err)
	}
	if _, err := phone.ImportSync(engine.NewDirectoryTransport(exchange)); err != nil {
		t.Fatal(err)
	}
	draft, err := phone.PrepareReflection("2026-04-02", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := phone.SaveReflection(draft, engine.ReflectionResult{Saved: true, Answers: []string{"Pelican words from the phone."}}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := phone.PublishSync(engine.NewDirectoryTransport(exchange)); err != nil {
		t.Fatal(err)
	}
	return j, exchange
}

func TestDoctorSaysWhatWaitsInSyncAndHowToSettleIt(t *testing.T) {
	j, exchange := syncedCompatJournal(t)
	// A change from a device running a newer seneca.
	newer := `{"protocol": 9}`
	dir := filepath.Join(exchange, "transactions", strings.Repeat("e", 32))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "00000001-"+engine.SyncTransactionID([]byte(newer))+".json"), []byte(newer), 0644); err != nil {
		t.Fatal(err)
	}
	env := j.doctorEnv()
	env.getenv = func(key string) string {
		switch key {
		case "SENECA_FILE":
			return j.reflections
		case syncExchangeEnv:
			return exchange
		}
		return ""
	}
	before := treeOf(t, j.dir)
	cloud := treeOf(t, exchange)

	report := diagnose(env)

	requireDoctor(t, report, doctorOK, `this device is "desk"`)
	requireDoctor(t, report, doctorInfo, "1 change from other devices not imported yet; `seneca sync once` brings them in")
	requireDoctor(t, report, doctorWarn, "1 change from another device use a newer sync protocol and wait; update seneca on this Mac")
	requireNoDoctor(t, report, doctorError)
	if after := treeOf(t, j.dir); !equalStrings(before, after) {
		t.Fatal("doctor changed the journal's directory")
	}
	if after := treeOf(t, exchange); !equalStrings(cloud, after) {
		t.Fatal("doctor changed the exchange")
	}

	// A sync state from a newer seneca is an error with the way out.
	state := engine.SyncStatePathFor(j.reflections)
	if err := os.WriteFile(state, []byte(`{"version": 7}`), 0644); err != nil {
		t.Fatal(err)
	}
	requireDoctor(t, diagnose(env), doctorError, "written by a newer seneca; nothing is exchanged until seneca is updated on this Mac")
}

func TestSharedDiagnosticsCarryNothingWritten(t *testing.T) {
	j, exchange := syncedCompatJournal(t)
	env := j.doctorEnv()
	env.getenv = func(key string) string {
		switch key {
		case "SENECA_FILE":
			return j.reflections
		case syncExchangeEnv:
			return exchange
		}
		return ""
	}
	var out strings.Builder
	if err := writeSharedDiagnostics(&out, env); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out.String()), &decoded); err != nil || decoded["format"] != engine.DiagnosticsFormat {
		t.Fatalf("expected a diagnostics document, got %v\n%s", err, out.String())
	}
	status, _, _ := engine.LoadSyncStatus(j.reflections)
	journalText := readFileString(t, j.reflections) + readFileString(t, j.archive)
	for _, secret := range []string{
		j.dir, j.home, filepath.Base(j.dir), exchange, "desk", "phone", "Pelican", "2026-",
		"Finish before starting.", "What could I do differently tomorrow?", status.Device, status.Journal,
	} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("the shared diagnostics carry %q:\n%s", secret, out.String())
		}
	}
	// Nothing a line of the journal says, either.
	for _, line := range strings.Split(journalText, "\n") {
		line = strings.TrimSpace(line)
		if len(line) > 12 && !strings.HasPrefix(line, "<!--") && strings.Contains(out.String(), line) {
			t.Fatalf("the shared diagnostics quote the journal: %q", line)
		}
	}
	if !strings.Contains(out.String(), `"changes-unimported"`) || !strings.Contains(out.String(), `"enrolled": true`) {
		t.Fatalf("expected the findings and counts, got\n%s", out.String())
	}
}

func TestBackupAndLeavingSyncKeepEveryFile(t *testing.T) {
	j, _ := syncedCompatJournal(t)
	reflections := readFileString(t, j.reflections)
	archive := readFileString(t, j.archive)

	destination := filepath.Join(t.TempDir(), "backup")
	out, err := runCompatCLI(t, "backup", destination)
	if err != nil {
		t.Fatalf("backup failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Backed up to "+destination) || !strings.Contains(out, "keep it private") {
		t.Fatalf("unexpected backup output:\n%s", out)
	}
	if readFileString(t, filepath.Join(destination, "reflections.md")) != reflections ||
		readFileString(t, filepath.Join(destination, engine.ArchiveFileName)) != archive {
		t.Fatal("expected the backup to hold the journal byte for byte")
	}
	if _, err := runCompatCLI(t, "backup", destination); err == nil {
		t.Fatal("expected a second backup into the same directory to be refused")
	}

	status, _, _ := engine.LoadSyncStatus(j.reflections)
	out, err = runCompatCLI(t, "sync", "leave")
	if err != nil {
		t.Fatalf("sync leave failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "device "+engine.ShortRecordID(status.Device)+" is retired") || !strings.Contains(out, "seneca sync enroll --join") {
		t.Fatalf("unexpected leave output:\n%s", out)
	}
	if _, enrolled, err := engine.LoadSyncStatus(j.reflections); enrolled || err != nil {
		t.Fatalf("expected the journal out of sync, got enrolled=%v %v", enrolled, err)
	}
	if readFileString(t, j.reflections) != reflections || readFileString(t, j.archive) != archive {
		t.Fatal("leaving sync changed a journal file")
	}
}

func defaultCompatSettings(t *testing.T) engine.ReflectionSettings {
	t.Helper()
	settings, err := engine.ResolveReflectionSettings(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

// treeOf lists every file under dir with its content, recursively.
func treeOf(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		found = append(found, path+"="+string(data))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}
