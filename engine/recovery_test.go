package engine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestARecoveryExportHoldsAllWritingAndNoIdentity(t *testing.T) {
	t.Parallel()

	_, b := conflicted(t)
	device := t.TempDir()
	if err := os.WriteFile(filepath.Join(device, "2026-08-14.json"), []byte(`{"answers":["Unsaved phone words."]}`), 0600); err != nil {
		t.Fatal(err)
	}
	before := b.tree()

	destination := filepath.Join(t.TempDir(), "Seneca Export")
	export, err := b.journal.ExportRecovery(destination, device)
	if err != nil {
		t.Fatalf("ExportRecovery returned error: %v", err)
	}
	if after := b.tree(); !sameTree(before, after) {
		t.Fatal("exporting changed the journal")
	}
	exported := readTree(t, destination)
	reflections, _ := os.ReadFile(b.reflections())
	archive, _ := os.ReadFile(b.archive())
	if exported["reflections.md"] != string(reflections) || exported[ArchiveFileName] != string(archive) {
		t.Fatal("expected the reflections file and the archive byte for byte")
	}
	if !strings.Contains(exported["settings/settings.json"], "Notice?") {
		t.Fatalf("expected the journal's settings, got %q", exported["settings/settings.json"])
	}
	if exported["drafts/device/2026-08-14.json"] == "" {
		t.Fatal("expected the device's unsaved writing")
	}
	versions := ""
	for name, content := range exported {
		if strings.HasPrefix(name, "conflicts/") {
			versions += content
		}
	}
	if !strings.Contains(versions, "The desk's version.") || !strings.Contains(versions, "The phone's version.") {
		t.Fatalf("expected every version of the open conflict, got %q", versions)
	}
	if export.Count("conflict") < 2 || export.Count("reflections") != 1 {
		t.Fatalf("unexpected export %+v", export)
	}

	// No identity travels: nothing names this device, and no sync state or
	// history is in it.
	status := b.status()
	for name, content := range exported {
		if strings.Contains(name, ".seneca") || strings.Contains(name, "state.json") || strings.Contains(content, status.Device) {
			t.Fatalf("the export carries this device's identity in %s", name)
		}
	}
	var manifest RecoveryExport
	if err := json.Unmarshal([]byte(exported["MANIFEST.json"]), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Format != RecoveryExportFormat || len(manifest.Files) != len(export.Files) {
		t.Fatalf("unexpected manifest %+v", manifest)
	}
	for _, file := range manifest.Files {
		if RevisionOf([]byte(exported[file.Path])) != file.Digest {
			t.Fatalf("the manifest's digest of %s does not match", file.Path)
		}
	}

	// An export never lands over another.
	if _, err := b.journal.ExportRecovery(destination, ""); !errors.Is(err, ErrRecoveryDestinationExists) {
		t.Fatalf("expected an existing destination to be refused, got %v", err)
	}
}

func TestAnInterruptedRecoveryExportLeavesNothingAndIsDoneAgain(t *testing.T) {
	t.Parallel()

	a, _ := pair(t)
	before := a.tree()
	parent := t.TempDir()
	destination := filepath.Join(parent, "export")

	// A write fails part-way: no destination, nothing half-written beside it.
	writes := 0
	failing := FS{WriteFile: func(f *os.File, data []byte) (int, error) {
		writes++
		if writes == 2 {
			return 0, errors.New("disk full")
		}
		return f.Write(data)
	}}
	broken, err := Open(Options{ReflectionsPath: a.reflections(), Settings: journalSettings, Clock: a.clock, FS: failing})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broken.ExportRecovery(destination, ""); err == nil {
		t.Fatal("expected the failed write to fail the export")
	}
	if names, _ := os.ReadDir(parent); len(names) != 0 {
		t.Fatalf("expected a failed export to leave nothing, found %d entries", len(names))
	}

	// A process stopped part-way leaves only its hidden directory, which the
	// next export removes.
	abandoned := filepath.Join(parent, ".export"+recoveryPartialPrefix+"0123456789ab")
	if err := os.MkdirAll(abandoned, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(abandoned, "reflections.md"), []byte("half"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an interrupted export must not look finished")
	}
	export, err := a.journal.ExportRecovery(destination, "")
	if err != nil {
		t.Fatalf("exporting again returned error: %v", err)
	}
	if _, err := os.Stat(abandoned); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expected the abandoned export to be removed")
	}
	reflections, _ := os.ReadFile(a.reflections())
	if got, _ := os.ReadFile(filepath.Join(export.Directory, "reflections.md")); string(got) != string(reflections) {
		t.Fatal("expected the second export to hold the journal whole")
	}
	if after := a.tree(); !sameTree(before, after) {
		t.Fatal("exporting changed the journal")
	}
}

func TestLeavingSyncKeepsTheWritingAndRetiresTheIdentity(t *testing.T) {
	t.Parallel()

	_, b := conflicted(t)
	old := b.status()
	reflections, _ := os.ReadFile(b.reflections())
	archive, _ := os.ReadFile(b.archive())

	left, err := b.journal.LeaveSync()
	if err != nil {
		t.Fatalf("LeaveSync returned error: %v", err)
	}
	if left.Device != old.Device || left.Conflicts != 1 {
		t.Fatalf("unexpected %+v", left)
	}
	if _, enrolled, err := b.journal.SyncStatus(); enrolled || err != nil {
		t.Fatalf("expected the journal out of sync, got enrolled=%v %v", enrolled, err)
	}
	if got, _ := os.ReadFile(b.reflections()); string(got) != string(reflections) {
		t.Fatal("leaving changed the reflections file")
	}
	if got, _ := os.ReadFile(b.archive()); string(got) != string(archive) {
		t.Fatal("leaving changed the archive")
	}
	// The history, with every version of the open conflict, is kept aside.
	kept := readTree(t, left.Kept)
	if kept["state.json"] == "" {
		t.Fatalf("expected the history kept at %s", left.Kept)
	}
	if _, err := b.journal.LeaveSync(); !errors.Is(err, ErrSyncNotEnrolled) {
		t.Fatalf("expected leaving twice to be refused, got %v", err)
	}

	// Joining again is as a new device.
	rejoined := b.join()
	if rejoined.Device == old.Device || rejoined.Journal != old.Journal {
		t.Fatalf("expected a new device of the same journal, got %+v (was %+v)", rejoined, old)
	}
}

func TestADeviceReplacedOrRestoredFromACopyNeverSpeaksAsAnother(t *testing.T) {
	t.Parallel()

	desk, phone := pair(t)
	phone.write("2026-08-12", "Written on the old phone.", "", "")
	phone.publish()
	carry(t, phone, desk, nil)
	desk.pull()

	// A new phone restored from a copy of the old one's container: the same
	// files, the same sync state, the same identity.
	restored := newPeer(t, "restored", 3)
	for name, content := range phone.tree() {
		path := filepath.Join(restored.dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	restored.restart(FS{})
	if restored.status().Device != phone.status().Device {
		t.Fatal("expected the copy to carry the old phone's identity")
	}

	// The old phone writes once more, and the copy sees its own identity in
	// the exchange on something it never recorded.
	phone.write("2026-08-13", "The old phone again.", "", "")
	phone.publish()
	carry(t, phone, restored, nil)
	diagnostics := CollectDiagnostics(restored.reflections(), restored.exchange())
	if finding, ok := diagnostics.Finding(FindingIdentityReused); !ok || finding.Severity != DiagnosticError {
		t.Fatalf("expected the copy to report a reused identity, got %+v", diagnostics.Findings)
	}

	// The recovery path: leave, and join as a device of its own. Everything
	// the copy holds is kept, and everything the others wrote arrives.
	if _, err := restored.journal.LeaveSync(); err != nil {
		t.Fatal(err)
	}
	carry(t, desk, restored, nil)
	joined := restored.join()
	if joined.Device == phone.status().Device || joined.Device == desk.status().Device {
		t.Fatal("expected the restored phone to join as a new device")
	}
	requireNoDiagnostics(t, restored.pull())
	settle(t, restored, desk)
	settle(t, phone, desk)
	settle(t, restored, desk)
	requireSameArtifacts(t, restored, desk)
	requireSameArtifacts(t, phone, desk)
	if !strings.Contains(restored.artifacts()[entryArtifact("2026-08-13")], "The old phone again.") {
		t.Fatal("expected the old phone's last writing on the replacement")
	}
	if CollectDiagnostics(restored.reflections(), restored.exchange()).Has(FindingIdentityReused) {
		t.Fatal("expected the replacement to speak only as itself")
	}
}

func TestDiagnosticsSayNothingWrittenInTheJournal(t *testing.T) {
	t.Parallel()

	_, b := conflicted(t)
	b.write("2026-08-14", "Zebra-secret answer.", "Quokka handled.", "Ibis tomorrow.")
	if _, err := b.journal.SetFocus("Narwhal focus text."); err != nil {
		t.Fatal(err)
	}
	status := b.status()

	d := CollectDiagnostics(b.reflections(), b.exchange())
	encoded, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{
		"Zebra", "Quokka", "Ibis", "Narwhal", "secret", "version.", "desk", "Notice?", "Handle?", "Carry?",
		"2026-", b.dir, filepath.Base(b.dir), status.Device, status.Journal, status.Name, "phone",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("the diagnostics carry %q:\n%s", secret, text)
		}
	}
	if !d.Sync.Enrolled || d.Journal.Entries < 3 || d.Sync.Conflicts != 1 || !d.Has(FindingConflictsOpen) || !d.Has(FindingChangesUnpublished) {
		t.Fatalf("expected counts that describe the journal, got %s", text)
	}
}

func TestDiagnosticsNameIncompatibleVersionsAndPendingTransfers(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	a.write("2026-08-12", "New on the desk.", "", "")
	a.publish()
	carry(t, a, b, nil)
	// A manifest from a newer seneca, under another device.
	newer := `{"protocol": 2, "journal": "` + a.status().Journal + `"}`
	other := strings.Repeat("c", 32)
	dir := filepath.Join(b.cloud, syncTransactionsName, other)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, syncManifestName(1, SyncTransactionID([]byte(newer)))), []byte(newer), 0644); err != nil {
		t.Fatal(err)
	}

	d := CollectDiagnostics(b.reflections(), b.exchange())
	if f, ok := d.Finding(FindingChangesIncompatible); !ok || f.Count != 1 {
		t.Fatalf("expected one incompatible change, got %+v", d.Findings)
	}
	if f, ok := d.Finding(FindingChangesUnimported); !ok || f.Count != 1 {
		t.Fatalf("expected one change waiting to be imported, got %+v", d.Findings)
	}
	if d.Sync.Exchange.Unimported != 2 {
		t.Fatalf("expected both changes counted as not imported, got %+v", d.Sync.Exchange)
	}

	// A sync state written by a newer seneca stops everything until updated.
	state := SyncStatePathFor(b.reflections())
	if err := os.WriteFile(state, []byte(`{"version": 9}`), 0644); err != nil {
		t.Fatal(err)
	}
	if !CollectDiagnostics(b.reflections(), b.exchange()).Has(FindingSyncStateNewer) {
		t.Fatal("expected a newer sync state to be named")
	}
	// An exchange that cannot be reached is a warning, never an error.
	if !CollectDiagnostics(a.reflections(), NewDirectoryTransport(filepath.Join(t.TempDir(), "gone", "journal.json"))).Has(FindingExchangeEmpty) {
		t.Fatal("expected a folder holding nothing to be said so")
	}
}
