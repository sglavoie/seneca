package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/seneca/engine"
	"github.com/sglavoie/seneca/mobile/senecabridge"
)

// The round trip is the one scenario every platform runs: open the synthetic
// journal, save a reflection, reopen it. mobile/testdata/roundtrip holds the
// journal, the scenario, and what the desktop makes of it. The tests here
// hold that expectation to the desktop engine and to the seneca program, and
// hold the bridge to it; the iPhone app's tests carry the same directory in
// their bundle and are held to the same file on a simulator and on a phone.

const roundTripDir = "mobile/testdata/roundtrip"

type roundTripScenario struct {
	Now                 string   `json:"now"`
	TimeZone            string   `json:"time_zone"`
	Questions           []string `json:"questions"`
	AnchorQuestionIndex int      `json:"anchor_question_index"`
	Date                string   `json:"date"`
	Answers             []string `json:"answers"`
	FollowThrough       string   `json:"follow_through"`
	RequestID           string   `json:"request_id"`
}

// roundTripExpected is what the journal holds after the scenario's save, as
// the desktop engine writes it.
type roundTripExpected struct {
	Today             string `json:"today"`
	EntryCount        int    `json:"entry_count"`
	ReflectionsSHA256 string `json:"reflections_sha256"`
	ArchiveSHA256     string `json:"archive_sha256"`
	EntryRevision     string `json:"entry_revision"`
	EntryRaw          string `json:"entry_raw"`
	IntentionLabel    string `json:"intention_label"`
	Focus             string `json:"focus"`
}

func loadRoundTripScenario(t *testing.T) roundTripScenario {
	t.Helper()
	var scenario roundTripScenario
	readJSON(t, filepath.Join(roundTripDir, "scenario.json"), &scenario)
	return scenario
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// roundTripJournal copies the synthetic journal into a directory of the
// test's own and returns its reflections file.
func roundTripJournal(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"reflections.md", engine.ArchiveFileName} {
		data, err := os.ReadFile(filepath.Join(roundTripDir, "journal", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "reflections.md")
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// desktopRoundTrip runs the scenario the way the daily command does: the
// same two engine calls, with the scenario's clock.
func desktopRoundTrip(t *testing.T, scenario roundTripScenario) (string, roundTripExpected) {
	t.Helper()
	reflections := roundTripJournal(t)
	location, err := time.LoadLocation(scenario.TimeZone)
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, scenario.Now)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := engine.Open(engine.Options{
		ReflectionsPath: reflections,
		Settings:        engine.ReflectionSettings{Questions: scenario.Questions, AnchorQuestionIndex: scenario.AnchorQuestionIndex},
		Clock:           func() time.Time { return now.In(location) },
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := journal.PrepareReflection(journal.Today(), false)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := journal.SaveReflection(draft, engine.ReflectionResult{
		Saved: true, Answers: scenario.Answers, FollowThrough: scenario.FollowThrough,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	index, found := engine.FindEntry(saved.Entries, saved.Date)
	if !found {
		t.Fatalf("the desktop save left no entry for %s", saved.Date)
	}
	raw := saved.Entries[index].Raw
	return reflections, roundTripExpected{
		Today:             journal.Today(),
		EntryCount:        len(saved.Entries),
		ReflectionsSHA256: fileSHA256(t, reflections),
		ArchiveSHA256:     fileSHA256(t, journal.ArchivePath()),
		EntryRevision:     string(engine.RevisionOf([]byte(raw))),
		EntryRaw:          raw,
		IntentionLabel:    draft.Context.IntentionLabel(),
		Focus:             draft.Context.Focus,
	}
}

func TestRoundTripExpectationIsWhatTheDesktopWrites(t *testing.T) {
	scenario := loadRoundTripScenario(t)
	_, got := desktopRoundTrip(t, scenario)
	if got.Today != scenario.Date {
		t.Fatalf("the scenario's clock gives %s as today, and it saves %s", got.Today, scenario.Date)
	}

	path := filepath.Join(roundTripDir, "expected.json")
	if os.Getenv("SENECA_UPDATE_ROUNDTRIP") == "1" {
		data, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var want roundTripExpected
	readJSON(t, path, &want)
	if got != want {
		t.Fatalf("the desktop engine now writes\n%+v\nand %s expects\n%+v\nrerun with SENECA_UPDATE_ROUNDTRIP=1 if the change is intended", got, path, want)
	}
}

// bridgeCall sends one request and decodes its result, failing the test on a
// refusal.
func bridgeCall(t *testing.T, bridge *senecabridge.Bridge, operation, requestID string, params, into any) (replayed bool) {
	t.Helper()
	request := map[string]any{"version": senecabridge.BridgeVersion(), "operation": operation, "params": params}
	if requestID != "" {
		request["request_id"] = requestID
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		OK       bool            `json:"ok"`
		Replayed bool            `json:"replayed"`
		Result   json.RawMessage `json:"result"`
		Error    *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(bridge.Call(string(raw))), &response); err != nil {
		t.Fatalf("%s: %v", operation, err)
	}
	if !response.OK {
		t.Fatalf("%s refused: %s: %s", operation, response.Error.Code, response.Error.Message)
	}
	if into != nil {
		if err := json.Unmarshal(response.Result, into); err != nil {
			t.Fatalf("%s result: %v", operation, err)
		}
	}
	return response.Replayed
}

func TestRoundTripThroughTheBridgeMatchesTheDesktop(t *testing.T) {
	scenario := loadRoundTripScenario(t)
	var want roundTripExpected
	readJSON(t, filepath.Join(roundTripDir, "expected.json"), &want)

	reflections := roundTripJournal(t)
	stateDir := t.TempDir()
	open := map[string]any{
		"reflections_path":      reflections,
		"state_dir":             stateDir,
		"questions":             scenario.Questions,
		"anchor_question_index": scenario.AnchorQuestionIndex,
		"time_zone":             scenario.TimeZone,
		"now":                   scenario.Now,
	}

	// Open the synthetic journal and save a reflection through the bridge.
	bridge := senecabridge.NewBridge()
	var status struct {
		Today string `json:"today"`
	}
	bridgeCall(t, bridge, "journal.open", "", open, &status)
	if status.Today != want.Today {
		t.Fatalf("today = %s, want %s", status.Today, want.Today)
	}
	var prepared struct {
		Draft          json.RawMessage `json:"draft"`
		IntentionLabel string          `json:"intention_label"`
	}
	bridgeCall(t, bridge, "reflection.prepare", "", map[string]any{}, &prepared)
	if prepared.IntentionLabel != want.IntentionLabel {
		t.Fatalf("intention label = %q, want %q", prepared.IntentionLabel, want.IntentionLabel)
	}
	save := map[string]any{"draft": prepared.Draft, "answers": scenario.Answers, "follow_through": scenario.FollowThrough}
	type entry struct {
		Revision string `json:"revision"`
		Raw      string `json:"raw"`
	}
	var saved struct {
		Entry entry `json:"entry"`
	}
	if bridgeCall(t, bridge, "reflection.save", scenario.RequestID, save, &saved) {
		t.Fatal("the first save was answered as a repeat")
	}
	if !bridgeCall(t, bridge, "reflection.save", scenario.RequestID, save, nil) {
		t.Fatal("the repeated save was carried out again")
	}

	// Reopen it: a new bridge, as a relaunched app has.
	reopened := senecabridge.NewBridge()
	bridgeCall(t, reopened, "journal.open", "", open, nil)
	var read entry
	bridgeCall(t, reopened, "entry.get", "", map[string]any{"date": scenario.Date}, &read)
	if read != saved.Entry {
		t.Fatalf("reopened entry = %+v, saved %+v", read, saved.Entry)
	}
	var listed struct {
		Entries []json.RawMessage `json:"entries"`
	}
	bridgeCall(t, reopened, "entries.list", "", map[string]any{}, &listed)

	got := want
	got.EntryCount = len(listed.Entries)
	got.ReflectionsSHA256 = fileSHA256(t, reflections)
	got.ArchiveSHA256 = fileSHA256(t, engine.ArchivePathFor(reflections))
	got.EntryRevision = read.Revision
	got.EntryRaw = read.Raw
	if got != want {
		t.Fatalf("through the bridge the journal holds\n%+v\nand the desktop writes\n%+v", got, want)
	}

	// The seneca program reads the day the bridge saved exactly as it reads
	// one it saved itself.
	desktopReflections, _ := desktopRoundTrip(t, scenario)
	show := func(reflections string) string {
		command := exec.Command(builtSeneca(t), "show", "--date", scenario.Date)
		command.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH"), "SENECA_FILE=" + reflections}
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("seneca show --date %s: %v\n%s", scenario.Date, err, output)
		}
		return string(output)
	}
	fromBridge, fromDesktop := show(reflections), show(desktopReflections)
	if fromBridge != fromDesktop {
		t.Fatalf("seneca show prints the bridge's save as\n%s\nand the desktop's as\n%s", fromBridge, fromDesktop)
	}
	if strings.TrimSuffix(fromBridge, "\n") != read.Raw {
		t.Fatalf("seneca show prints\n%s\nand the bridge returned\n%s", fromBridge, read.Raw)
	}
}
