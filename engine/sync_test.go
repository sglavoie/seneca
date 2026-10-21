package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readManifest reads one of a peer's own stored manifests by identity.
func (p *peer) manifest(id string) SyncTransaction {
	p.t.Helper()

	refs, err := listSyncManifests(filepath.Join(SyncDirFor(p.reflections()), syncTransactionsName))
	if err != nil {
		p.t.Fatalf("%s: listing the sync history: %v", p.label, err)
	}
	for _, ref := range refs {
		if ref.ID != id {
			continue
		}
		data, err := os.ReadFile(filepath.Join(SyncDirFor(p.reflections()), syncTransactionsName, ref.Device, syncManifestName(ref.Sequence, ref.ID)))
		if err != nil {
			p.t.Fatalf("%s: reading transaction %s: %v", p.label, id, err)
		}
		tx, err := DecodeSyncTransaction(data)
		if err != nil {
			p.t.Fatalf("%s: transaction %s is not valid: %v", p.label, id, err)
		}
		return tx
	}
	p.t.Fatalf("%s: transaction %s is not in the sync history", p.label, id)
	return SyncTransaction{}
}

// retained reads contents from a peer's object store, or fails the test.
func (p *peer) retained(digest Revision) string {
	p.t.Helper()

	data, err := readSyncObject(filepath.Join(SyncDirFor(p.reflections()), syncObjectsName), digest)
	if err != nil {
		p.t.Fatalf("%s: contents %s are not retained: %v", p.label, digest, err)
	}
	return string(data)
}

func TestEnrollRecordsTheJournalAsGenesisAndWritesNoMarkdown(t *testing.T) {
	t.Parallel()

	a := newPeer(t, "desk", 1)
	a.seedEntries(
		dayEntry("2026-08-10", "Noticed.", "Handled.", "Start before nine."),
		dayEntry("2026-08-11", "Again.", "", "Keep going."),
	)
	if _, err := a.journal.SetFocus("The first focus."); err != nil {
		t.Fatalf("SetFocus returned error: %v", err)
	}
	reflections, archive := readFileString(t, a.reflections()), readFileString(t, a.archive())

	status := a.enroll()

	if readFileString(t, a.reflections()) != reflections || readFileString(t, a.archive()) != archive {
		t.Fatal("enrolling rewrote the journal's Markdown")
	}
	if !syncIdentityPattern.MatchString(status.Journal) || !syncIdentityPattern.MatchString(status.Device) || status.Journal == status.Device {
		t.Fatalf("expected distinct journal and device identities, got %q and %q", status.Journal, status.Device)
	}
	if status.Sequence != 1 || len(status.Heads) != 1 || status.Heads[0] != status.Genesis {
		t.Fatalf("expected the genesis to be the one transaction and the head, got %+v", status)
	}
	state, err := a.journal.EffectiveSettings()
	if err != nil || !state.Initialized {
		t.Fatalf("expected enrolling to give the journal settings of its own, got %+v, %v", state, err)
	}

	genesis := a.manifest(status.Genesis)
	if genesis.Origin != SyncOriginGenesis || len(genesis.Parents) != 0 || genesis.Protocol != SyncProtocolVersion {
		t.Fatalf("unexpected genesis manifest: %+v", genesis)
	}
	artifacts := a.artifacts()
	if len(genesis.Changes) != len(artifacts) || len(artifacts) != 4 {
		t.Fatalf("expected the genesis to name all %d artifacts, got %d changes", len(artifacts), len(genesis.Changes))
	}
	for _, change := range genesis.Changes {
		if change.Base != RevisionAbsent || change.Deleted {
			t.Errorf("genesis change %+v is not a creation", change)
		}
		if a.retained(change.New) != artifacts[change.Artifact] {
			t.Errorf("the genesis does not retain the bytes of %s", change.Artifact)
		}
	}

	before := a.tree()
	if again := a.enroll(); again.Journal != status.Journal || again.Genesis != status.Genesis {
		t.Fatalf("enrolling twice changed the journal's identity: %+v then %+v", status, again)
	}
	if !sameTree(before, a.tree()) {
		t.Fatal("enrolling an enrolled journal wrote something")
	}
}

func TestJoinAdoptsTheJournalIdentityAndTheGenesisSnapshot(t *testing.T) {
	t.Parallel()

	a, b := pair(t)

	desk, phone := a.status(), b.status()
	if phone.Journal != desk.Journal || phone.Genesis != desk.Genesis {
		t.Fatalf("the joined device has another journal: %+v against %+v", phone, desk)
	}
	if phone.Device == desk.Device {
		t.Fatal("both devices have one device identity")
	}
	if phone.Sequence != 0 || len(phone.Heads) != 1 || phone.Heads[0] != desk.Genesis {
		t.Fatalf("expected the joined device to hold the genesis and nothing of its own, got %+v", phone)
	}
	// The reflections file is compared as artifacts: the desk's still has the
	// spacing it was first written with, which is framing and not content.
	for _, name := range []string{ArchiveFileName, ".seneca/journals/reflections.md/settings.json"} {
		if left, right := readFileString(t, filepath.Join(a.dir, name)), readFileString(t, filepath.Join(b.dir, name)); left != right {
			t.Errorf("%s differs after joining:\n%q\n%q", name, left, right)
		}
	}
	focus, _, err := b.journal.Focus("2026-08-20")
	if err != nil || focus.Text != "The first focus." {
		t.Fatalf("expected the joined device to read the focus, got %+v, %v", focus, err)
	}
}

func TestJoiningBeforeAnythingIsPublishedIsRefused(t *testing.T) {
	t.Parallel()

	b := newPeer(t, "phone", 2)
	if _, err := b.journal.JoinSync(b.exchange(), "phone"); err == nil {
		t.Fatal("expected joining an empty exchange to be refused")
	}
	if len(b.tree()) != 0 {
		t.Fatalf("a refused join wrote %v", b.tree())
	}
	if _, err := b.journal.ImportSync(b.exchange()); !errors.Is(err, ErrSyncNotEnrolled) {
		t.Fatalf("expected ErrSyncNotEnrolled, got %v", err)
	}
}

func TestWorkspacesExchangeOfflineAndAfterRestart(t *testing.T) {
	t.Parallel()

	a, b := pair(t)

	// Both write with nothing between them.
	a.write("2026-08-12", "Desk day.", "", "Carry it.")
	b.write("2026-08-13", "Phone day.", "", "")
	if _, err := b.journal.SetFocus("The phone's focus."); err != nil {
		t.Fatalf("SetFocus returned error: %v", err)
	}

	// Both processes end before anything is exchanged.
	a.restart(FS{})
	b.restart(FS{})
	settle(t, a, b)

	requireSameArtifacts(t, a, b)
	artifacts := a.artifacts()
	for _, date := range []string{"2026-08-10", "2026-08-11", "2026-08-12", "2026-08-13"} {
		if _, ok := artifacts[entryArtifact(date)]; !ok {
			t.Errorf("the entry for %s did not survive the exchange", date)
		}
	}
	if left, right := readFileString(t, a.reflections()), readFileString(t, b.reflections()); left != right {
		t.Errorf("the reflections files differ:\n%q\n%q", left, right)
	}
	focus, _, err := a.journal.Focus("2026-08-20")
	if err != nil || focus.Text != "The phone's focus." {
		t.Fatalf("expected the desk to read the phone's focus, got %+v, %v", focus, err)
	}

	// Nothing is left to do, across another restart.
	a.restart(FS{})
	b.restart(FS{})
	left, right := a.tree(), b.tree()
	settle(t, a, b)
	if !sameTree(left, a.tree()) || !sameTree(right, b.tree()) {
		t.Fatal("exchanging again after everything was exchanged changed a workspace")
	}
	if pending := append(a.status().Pending, b.status().Pending...); len(pending) > 0 {
		t.Fatalf("expected nothing pending, got %v", pending)
	}
}

func TestDuplicateDeliveryHasNoFurtherEffect(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	a.write("2026-08-12", "Desk day.", "", "")
	a.publish()
	carry(t, a, b, nil)

	first := b.pull()
	if len(first.Applied) != 1 || len(first.Written) != 1 {
		t.Fatalf("expected one transaction applied to one file, got %+v", first)
	}
	after := b.tree()

	for round := 0; round < 2; round++ {
		carry(t, a, b, nil)
		again := b.pull()
		if len(again.Applied) != 0 || len(again.Written) != 0 || len(again.Recorded) != 0 {
			t.Fatalf("a transaction delivered again did something: %+v", again)
		}
		if !sameTree(after, b.tree()) {
			t.Fatal("a transaction delivered again changed the workspace")
		}
	}
}

func TestDelayedAndReorderedDeliveryAppliesInCausalOrder(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	a.write("2026-08-12", "First draft.", "", "")
	a.write("2026-08-12", "Second draft.", "", "")
	a.write("2026-08-14", "Later.", "", "")
	a.publish()

	sent := manifests(t, a.cloud, a.status().Device)
	if len(sent) != 4 {
		t.Fatalf("expected the genesis and three transactions, got %v", sent)
	}
	only := func(manifest string) func(string) bool {
		return func(name string) bool {
			return name == manifest || strings.HasPrefix(name, syncObjectsName+"/")
		}
	}
	before := readFileString(t, b.reflections())

	// The newest arrives first, then the middle one.
	for i, manifest := range []string{sent[3], sent[2]} {
		carry(t, a, b, only(manifest))
		imported := b.pull()
		if len(imported.Applied) != 0 || len(imported.Pending) != i+1 {
			t.Fatalf("expected %d waiting and nothing applied, got %+v", i+1, imported)
		}
		for _, pending := range imported.Pending {
			if pending.Reason != SyncPendingWaiting {
				t.Fatalf("expected a transaction ahead of its parents to wait, got %+v", pending)
			}
		}
		if readFileString(t, b.reflections()) != before {
			t.Fatal("a transaction was applied before the one it was made on top of")
		}
	}

	// The process ends while they wait.
	b.restart(FS{})

	carry(t, a, b, only(sent[1]))
	imported := b.pull()
	if len(imported.Applied) != 3 || len(imported.Pending) != 0 {
		t.Fatalf("expected all three to apply once the first arrived, got %+v", imported)
	}
	requireSameArtifacts(t, a, b)
	if got := b.artifacts()[entryArtifact("2026-08-12")]; !strings.Contains(got, "Second draft.") {
		t.Fatalf("expected the later edit to be the one in force, got %q", got)
	}
}

func TestIncompleteDeliveryIsNotImported(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	a.write("2026-08-12", "Desk day.", "", "")
	a.publish()
	before := b.tree()

	requireHeld := func(stage string) {
		t.Helper()
		imported := b.pull()
		if !hasDiagnostic(imported, SyncDiagIncomplete) || len(imported.Applied) != 0 || len(imported.Pending) != 0 {
			t.Fatalf("%s: expected an incomplete transaction to be reported and not stored, got %+v", stage, imported)
		}
		if !sameTree(before, b.tree()) {
			t.Fatalf("%s: an incomplete transaction changed the workspace", stage)
		}
	}

	// The manifest arrives without the contents it names.
	carry(t, a, b, func(name string) bool { return !strings.HasPrefix(name, syncObjectsName+"/") })
	requireHeld("manifest alone")

	// The contents arrive cut short.
	for _, name := range exchanged(t, a.cloud) {
		target := filepath.Join(b.cloud, filepath.FromSlash(name))
		if _, err := os.Stat(target); err == nil {
			continue
		}
		data := readFileString(t, filepath.Join(a.cloud, filepath.FromSlash(name)))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatalf("preparing %s: %v", target, err)
		}
		if err := os.WriteFile(target, []byte(data[:len(data)/2]), 0644); err != nil {
			t.Fatalf("writing %s: %v", target, err)
		}
	}
	requireHeld("contents cut short")

	// The manifest itself is cut short.
	manifest := manifests(t, a.cloud, a.status().Device)[1]
	whole := readFileString(t, filepath.Join(a.cloud, filepath.FromSlash(manifest)))
	if err := os.WriteFile(filepath.Join(b.cloud, filepath.FromSlash(manifest)), []byte(whole[:len(whole)/2]), 0644); err != nil {
		t.Fatalf("cutting the manifest short: %v", err)
	}
	requireHeld("manifest cut short")

	carry(t, a, b, nil)
	requireNoDiagnostics(t, b.pull())
	requireSameArtifacts(t, a, b)
}

// plant writes a manifest into a peer's copy of the exchange under the name its
// bytes earn, with the contents it names.
func plant(t *testing.T, cloud string, tx SyncTransaction, contents ...string) string {
	t.Helper()

	data, err := encodeSyncJSON(tx)
	if err != nil {
		t.Fatalf("encoding a manifest: %v", err)
	}
	transport := NewDirectoryTransport(cloud)
	for _, content := range contents {
		if err := transport.PutObject(RevisionOf([]byte(content)), []byte(content)); err != nil {
			t.Fatalf("planting contents: %v", err)
		}
	}
	return plantBytes(t, cloud, tx.Device, tx.Sequence, data)
}

func plantBytes(t *testing.T, cloud, device string, sequence int, data []byte) string {
	t.Helper()

	id := SyncTransactionID(data)
	ref := SyncTransactionRef{Device: device, Sequence: sequence, ID: id}
	if err := NewDirectoryTransport(cloud).PutTransaction(ref, data); err != nil {
		t.Fatalf("planting a manifest: %v", err)
	}
	return id
}

func TestMalformedAndIncompatibleTransactionsAreRefused(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	desk := a.status()
	valid := func(sequence int, changes ...SyncChange) SyncTransaction {
		return SyncTransaction{
			Protocol: SyncProtocolVersion,
			Journal:  desk.Journal,
			Device:   desk.Device,
			Sequence: sequence,
			Parents:  []string{desk.Genesis},
			Created:  "2026-08-20T21:00:00Z",
			Origin:   SyncOriginLocal,
			Changes:  changes,
		}
	}
	entry := NewReflectionEntry(dayEntry("2026-08-15", "Planted.", "", "")).Raw
	create := func(artifact, content string) SyncChange {
		return SyncChange{Artifact: artifact, Base: RevisionAbsent, New: RevisionOf([]byte(content))}
	}

	newer := valid(2, create(entryArtifact("2026-08-15"), entry))
	newer.Protocol = SyncProtocolVersion + 1
	misdated := NewReflectionEntry(dayEntry("2026-08-16", "Not the day it claims.", "", "")).Raw
	two := entry + EntryTerminator + dayEntry("2026-08-17", "A second entry.", "", "")
	forged := NewFocusSetRecord(archiveTime(19, 9, 0), FocusOriginManual, "", "A focus.")
	forgedRaw := strings.Replace(normalizeRecordRaw(forged.Raw), "A focus.", "Another focus entirely.", 1)
	brokenSettings := `{"version":1,"reflection":{"questions":[],"anchor_question_index":0},"legacy":{"questions":[],"anchor_question_index":0}}` + "\n"
	deletesNothing := valid(8)
	deletesNothing.Changes = []SyncChange{{Artifact: entryArtifact("2026-08-10"), Base: RevisionAbsent, Deleted: true}}

	cases := []struct {
		name string
		id   string
		code SyncDiagnosticCode
	}{
		{"not a manifest", plantBytes(t, b.cloud, desk.Device, 2, []byte("this is not a manifest\n")), SyncDiagMalformed},
		{"an unknown field", plantBytes(t, b.cloud, desk.Device, 3,
			[]byte(`{"protocol":1,"journal":"`+desk.Journal+`","device":"`+desk.Device+`","sequence":3,"parents":[],"created":"2026-08-20T21:00:00Z","origin":"local","changes":[],"winner":"me"}`)), SyncDiagMalformed},
		{"a newer protocol", plant(t, b.cloud, newer, entry), SyncDiagNewerProtocol},
		{"an entry for another day", plant(t, b.cloud, valid(4, create(entryArtifact("2026-08-15"), misdated)), misdated), SyncDiagInvalidContent},
		{"two entries as one", plant(t, b.cloud, valid(5, create(entryArtifact("2026-08-15"), two)), two), SyncDiagInvalidContent},
		{"a record that is not its content", plant(t, b.cloud, valid(6, create(recordArtifact(forged.RecordID()), forgedRaw)), forgedRaw), SyncDiagInvalidContent},
		{"unusable settings", plant(t, b.cloud, valid(7, SyncChange{Artifact: settingsArtifact,
			Base: RevisionOf([]byte(b.artifacts()[settingsArtifact])), New: RevisionOf([]byte(brokenSettings))}), brokenSettings), SyncDiagInvalidContent},
		{"a deletion of nothing", plant(t, b.cloud, deletesNothing), SyncDiagMalformed},
		{"a reused sequence number", plant(t, b.cloud, valid(1, create(entryArtifact("2026-08-15"), entry)), entry), SyncDiagSequenceReuse},
	}
	// A manifest under a name its bytes did not earn.
	misnamed, err := encodeSyncJSON(valid(9, create(entryArtifact("2026-08-15"), entry)))
	if err != nil {
		t.Fatalf("encoding a manifest: %v", err)
	}
	wrongID := strings.Repeat("ab", 32)
	if err := NewDirectoryTransport(b.cloud).PutTransaction(SyncTransactionRef{Device: desk.Device, Sequence: 9, ID: wrongID}, misnamed); err != nil {
		t.Fatalf("planting a manifest: %v", err)
	}
	cases = append(cases, struct {
		name string
		id   string
		code SyncDiagnosticCode
	}{"a manifest under another's name", wrongID, SyncDiagIncomplete})

	before := b.tree()
	imported := b.pull()

	if len(imported.Applied) != 0 || len(imported.Pending) != 0 || len(imported.Written) != 0 {
		t.Fatalf("a refused transaction was stored or applied: %+v", imported)
	}
	for _, tc := range cases {
		found := false
		for _, diagnostic := range imported.Diagnostics {
			if diagnostic.Transaction == tc.id {
				found = true
				if diagnostic.Code != tc.code {
					t.Errorf("%s: expected %s, got %s", tc.name, tc.code, diagnostic)
				}
			}
		}
		if !found {
			t.Errorf("%s: transaction %s was not reported", tc.name, ShortRecordID(tc.id))
		}
	}
	if !sameTree(before, b.tree()) {
		t.Fatal("refused transactions changed the workspace")
	}

	// The exchange still works for what is valid.
	a.write("2026-08-12", "Desk day.", "", "")
	a.publish()
	carry(t, a, b, nil)
	if imported := b.pull(); len(imported.Applied) != 1 {
		t.Fatalf("expected the valid transaction to apply beside the refused ones, got %+v", imported)
	}
	requireSameArtifacts(t, a, b)
}

func TestAnExchangeOfAnotherJournalIsRefused(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	other := newPeer(t, "stranger", 3)
	other.seedEntries(dayEntry("2026-08-01", "Someone else's day.", "", ""))
	other.enroll()
	other.publish()

	before, exchange := b.tree(), readTree(t, other.cloud)
	var mismatch *SyncIdentityError
	if _, err := b.journal.ImportSync(other.exchange()); !errors.As(err, &mismatch) {
		t.Fatalf("expected importing another journal to be refused, got %v", err)
	}
	if mismatch.Local != b.status().Journal || mismatch.Remote != other.status().Journal {
		t.Fatalf("the refusal names the wrong journals: %+v", mismatch)
	}
	if _, err := b.journal.PublishSync(other.exchange()); !errors.As(err, &mismatch) {
		t.Fatalf("expected publishing into another journal to be refused, got %v", err)
	}
	if _, err := b.journal.JoinSync(other.exchange(), "phone"); !errors.As(err, &mismatch) {
		t.Fatalf("expected joining a second journal to be refused, got %v", err)
	}
	if !sameTree(before, b.tree()) || !sameTree(exchange, readTree(t, other.cloud)) {
		t.Fatal("a refused exchange wrote something")
	}

	// One of the other journal's manifests strays into this exchange.
	stray := manifests(t, other.cloud, other.status().Device)[0]
	data := readFileString(t, filepath.Join(other.cloud, filepath.FromSlash(stray)))
	id := plantBytes(t, b.cloud, other.status().Device, 1, []byte(data))
	carry(t, other, b, func(name string) bool { return strings.HasPrefix(name, syncObjectsName+"/") })
	imported := b.pull()
	if !hasDiagnostic(imported, SyncDiagForeignJournal) || imported.Diagnostics[0].Transaction != id || len(imported.Pending) != 0 {
		t.Fatalf("expected the stray manifest to be reported and not stored, got %+v", imported)
	}
	requireSameArtifacts(t, a, b)

	// The same journal identity over a different history is not the same journal.
	forged, err := encodeSyncJSON(syncDescriptor{Protocol: SyncProtocolVersion, Journal: b.status().Journal, Genesis: strings.Repeat("cd", 32)})
	if err != nil {
		t.Fatalf("encoding a descriptor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(b.cloud, syncDescriptorFileName), forged, 0644); err != nil {
		t.Fatalf("forging the descriptor: %v", err)
	}
	if _, err := b.journal.ImportSync(b.exchange()); !errors.As(err, &mismatch) {
		t.Fatalf("expected a different genesis to be refused, got %v", err)
	}
}

func TestExternalEditsAreRecordedBeforeIncomingWorkIsApplied(t *testing.T) {
	t.Parallel()

	a, b := pair(t)

	// On the phone's side an editor rewrites one day and adds another.
	edited := NewReflectionEntry(dayEntry("2026-08-11", "Rewritten by hand.", "", "Keep going.")).Raw
	b.seedEntries(
		dayEntry("2026-08-10", "Noticed.", "Handled.", "Start before nine."),
		edited,
		dayEntry("2026-08-15", "Typed in an editor.", "", ""),
	)
	a.write("2026-08-12", "Desk day.", "", "")
	a.publish()
	carry(t, a, b, nil)

	imported := b.pull()
	if len(imported.Recorded) != 1 || len(imported.Applied) != 1 {
		t.Fatalf("expected the hand edit to be recorded and the incoming day applied, got %+v", imported)
	}
	requireNoDiagnostics(t, imported)
	recorded := b.manifest(imported.Recorded[0])
	if recorded.Origin != SyncOriginExternal || len(recorded.Changes) != 2 {
		t.Fatalf("expected one external transaction for the two edited days, got %+v", recorded)
	}
	for _, change := range recorded.Changes {
		if change.Artifact == entryArtifact("2026-08-11") && change.Base != RevisionOf([]byte(a.artifacts()[change.Artifact])) {
			t.Errorf("the hand edit does not name the revision it replaced: %+v", change)
		}
	}
	got := b.artifacts()
	if got[entryArtifact("2026-08-11")] != edited || got[entryArtifact("2026-08-15")] == "" || got[entryArtifact("2026-08-12")] == "" {
		t.Fatalf("the import lost the hand edit or the incoming day: %q", got)
	}

	settle(t, a, b)
	requireSameArtifacts(t, a, b)
	if a.artifacts()[entryArtifact("2026-08-11")] != edited {
		t.Fatal("the hand edit did not reach the other device")
	}
}

func TestAnExternalEditIsRecordedAheadOfTheSaveThatFindsIt(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	b.seedEntries(
		dayEntry("2026-08-10", "Noticed.", "Handled.", "Start before nine."),
		dayEntry("2026-08-11", "Rewritten by hand.", "", "Keep going."),
	)
	before := b.status()

	b.write("2026-08-16", "Saved through the engine.", "", "")

	after := b.status()
	if after.Sequence != before.Sequence+2 || len(after.Heads) != 1 {
		t.Fatalf("expected the hand edit and the save as two transactions, got sequence %d then %d", before.Sequence, after.Sequence)
	}
	save := b.manifest(after.Heads[0])
	if save.Origin != SyncOriginLocal || len(save.Changes) != 1 || save.Changes[0].Artifact != entryArtifact("2026-08-16") {
		t.Fatalf("expected the save to name only its own day, got %+v", save)
	}
	if len(save.Parents) != 1 {
		t.Fatalf("expected the save to follow the hand edit, got parents %v", save.Parents)
	}
	if edit := b.manifest(save.Parents[0]); edit.Origin != SyncOriginExternal || edit.Changes[0].Artifact != entryArtifact("2026-08-11") {
		t.Fatalf("expected the hand edit recorded first, got %+v", edit)
	}

	settle(t, a, b)
	requireSameArtifacts(t, a, b)
}

func TestInvalidExternalContentStaysUntouchedAndIsReported(t *testing.T) {
	t.Parallel()

	a, b := pair(t)

	// An editor leaves two entries under one date.
	broken := dayEntry("2026-08-10", "Noticed.", "Handled.", "Start before nine.") + EntryTerminator +
		dayEntry("2026-08-11", "Again.", "", "Keep going.") + EntryTerminator +
		dayEntry("2026-08-11", "A second entry for the same day.", "", "") + EntryTerminator
	b.overwrite(broken)
	sequence := b.status().Sequence

	a.write("2026-08-12", "Desk day.", "", "")
	a.publish()
	carry(t, a, b, nil)
	imported := b.pull()

	if readFileString(t, b.reflections()) != broken {
		t.Fatal("the import rewrote writing it could not read")
	}
	if !hasDiagnostic(imported, SyncDiagLocalInvalid) || len(imported.Applied) != 0 || len(imported.Recorded) != 0 {
		t.Fatalf("expected the invalid file to be reported and nothing applied or recorded, got %+v", imported)
	}
	if len(imported.Pending) != 1 || imported.Pending[0].Reason != SyncPendingLocalFile {
		t.Fatalf("expected the incoming day to be kept for later, got %+v", imported.Pending)
	}
	if b.status().Sequence != sequence || len(b.publish().Transactions) != 0 {
		t.Fatal("invalid writing was recorded or published")
	}

	// The author repairs the file; nothing was lost on either side.
	repaired := strings.Replace(broken, "## 2026-08-11\n\n### Notice?\nA second", "## 2026-08-14\n\n### Notice?\nA second", 1)
	if repaired == broken {
		t.Fatal("the test did not repair its fixture")
	}
	b.overwrite(repaired)
	imported = b.pull()
	if len(imported.Recorded) != 1 || len(imported.Applied) != 1 {
		t.Fatalf("expected the repair to be recorded and the kept day applied, got %+v", imported)
	}
	requireNoDiagnostics(t, imported)
	settle(t, a, b)
	requireSameArtifacts(t, a, b)
	if got := a.artifacts()[entryArtifact("2026-08-14")]; !strings.Contains(got, "A second entry for the same day.") {
		t.Fatalf("the repaired day did not reach the other device: %q", got)
	}
}

func TestMissingFilesNeverImplyDeletion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		lose  func(t *testing.T, path string)
		empty bool
	}{
		{"removed", func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatalf("removing %s: %v", path, err)
			}
		}, false},
		{"emptied", func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0644); err != nil {
				t.Fatalf("emptying %s: %v", path, err)
			}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, b := pair(t)
			kept := readFileString(t, b.reflections())
			keptArchive := readFileString(t, b.archive())
			tc.lose(t, b.reflections())
			tc.lose(t, b.archive())

			imported := b.pull()
			if !hasDiagnostic(imported, SyncDiagLocalMissing) || len(imported.Recorded) != 0 {
				t.Fatalf("expected the missing files to be reported and nothing recorded, got %+v", imported)
			}
			if suspended := b.status().Suspended; suspended["reflections"] == "" || suspended["archive"] == "" {
				t.Fatalf("expected both files to be suspended, got %v", suspended)
			}
			if len(b.publish().Transactions) != 0 {
				t.Fatal("a missing file was published as a change")
			}

			// The other device keeps writing, and loses nothing.
			a.write("2026-08-12", "Desk day.", "", "")
			settle(t, a, b)
			if got := a.artifacts(); len(got) != 5 {
				t.Fatalf("expected the desk to keep its four artifacts and gain one, got %q", got)
			}
			if _, err := os.Stat(b.reflections()); tc.empty != (err == nil) {
				t.Fatalf("the import recreated or removed the missing file: %v", err)
			}
			if pending := b.status().Pending; len(pending) != 1 || pending[0].Reason != SyncPendingLocalFile {
				t.Fatalf("expected the incoming day to be kept for later, got %+v", pending)
			}

			// A save into the missing file records that day and deletes nothing.
			b.write("2026-08-13", "Written into the gap.", "", "")
			settle(t, a, b)
			if got := a.artifacts(); got[entryArtifact("2026-08-13")] == "" || got[entryArtifact("2026-08-10")] == "" || len(got) != 6 {
				t.Fatalf("expected the new day to arrive and nothing to be deleted, got %q", got)
			}

			// The author restores the files from a copy.
			b.overwrite(kept + dayEntry("2026-08-13", "Written into the gap.", "", "") + EntryTerminator)
			if err := os.WriteFile(b.archive(), []byte(keptArchive), 0644); err != nil {
				t.Fatalf("restoring the archive: %v", err)
			}
			settle(t, a, b)
			requireSameArtifacts(t, a, b)
			if status := b.status(); len(status.Suspended) != 0 || len(status.Pending) != 0 {
				t.Fatalf("expected the restored files to be read again, got %+v", status)
			}
		})
	}
}

func TestOnlyAnExplicitDeletionRemovesAnEntry(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	removed := a.artifacts()[entryArtifact("2026-08-10")]

	// The author deletes one day in an editor; the rest of the file stays.
	b.seedEntries(dayEntry("2026-08-11", "Again.", "", "Keep going."))
	imported := b.pull()
	if len(imported.Recorded) != 1 {
		t.Fatalf("expected the deletion to be recorded, got %+v", imported)
	}
	deletion := b.manifest(imported.Recorded[0])
	if len(deletion.Changes) != 1 || !deletion.Changes[0].Deleted || deletion.Changes[0].Artifact != entryArtifact("2026-08-10") {
		t.Fatalf("expected one explicit deletion, got %+v", deletion)
	}

	settle(t, a, b)
	requireSameArtifacts(t, a, b)
	if _, still := a.artifacts()[entryArtifact("2026-08-10")]; still {
		t.Fatal("the deletion did not reach the other device")
	}
	// The bytes that were deleted are still held for whoever must reconcile.
	if a.retained(deletion.Changes[0].Base) != removed || b.retained(deletion.Changes[0].Base) != removed {
		t.Fatal("the deleted entry's bytes are no longer retained")
	}
}

func TestIndependentEditsOfOneDayAreBothKeptAndNeitherChosen(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	a.write("2026-08-11", "The desk's version.", "", "")
	b.write("2026-08-11", "The phone's version.", "", "")
	settle(t, a, b)

	for _, p := range []*peer{a, b} {
		mine, theirs := "The desk's version.", "The phone's version."
		if p == b {
			mine, theirs = theirs, mine
		}
		if got := p.artifacts()[entryArtifact("2026-08-11")]; !strings.Contains(got, mine) {
			t.Fatalf("%s: its own writing was replaced: %q", p.label, got)
		}
		// Nothing waits: the other device's transaction is applied, and only
		// the day both changed is held for the author.
		if pending := p.status().Pending; len(pending) != 0 {
			t.Fatalf("%s: expected nothing to wait on the conflict, got %+v", p.label, pending)
		}
		conflicts := p.conflicts()
		if len(conflicts) != 1 || conflicts[0].Artifact != entryArtifact("2026-08-11") || len(conflicts[0].Theirs) != 1 {
			t.Fatalf("%s: expected the other version to be held as a conflict, got %+v", p.label, conflicts)
		}
		if got := conflicts[0].Theirs[0].Content; !strings.Contains(got, theirs) {
			t.Fatalf("%s: the other version's bytes are not held: %q", p.label, got)
		}
		// The bytes both were made on are held too.
		if got := conflicts[0].Base.Content; !strings.Contains(got, "Again.") {
			t.Fatalf("%s: the common base is not held: %q", p.label, got)
		}
	}
}

func TestIndependentIdenticalWritingConverges(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	a.write("2026-08-14", "The same words.", "", "")
	b.write("2026-08-14", "The same words.", "", "")
	a.write("2026-08-15", "Only on the desk.", "", "")
	settle(t, a, b)

	requireSameArtifacts(t, a, b)
	if pending := append(a.status().Pending, b.status().Pending...); len(pending) > 0 {
		t.Fatalf("expected identical writing to need nothing, got %v", pending)
	}
	if len(a.status().Heads) != len(b.status().Heads) {
		t.Fatalf("the devices disagree about the history's heads: %v against %v", a.status().Heads, b.status().Heads)
	}
}

func TestJoiningWithWritingOfItsOwnMergesWhatDoesNotOverlap(t *testing.T) {
	t.Parallel()

	a := newPeer(t, "desk", 1)
	a.seedEntries(
		dayEntry("2026-08-10", "Noticed.", "Handled.", "Start before nine."),
		dayEntry("2026-08-11", "The desk's version.", "", ""),
	)
	a.enroll()
	a.publish()

	b := newPeer(t, "phone", 2)
	b.seedEntries(
		dayEntry("2026-08-09", "Only on the phone.", "", ""),
		dayEntry("2026-08-10", "Noticed.", "Handled.", "Start before nine."),
	)
	carry(t, a, b, nil)
	b.join()
	settle(t, a, b)

	requireSameArtifacts(t, a, b)
	got := a.artifacts()
	for _, date := range []string{"2026-08-09", "2026-08-10", "2026-08-11"} {
		if got[entryArtifact(date)] == "" {
			t.Errorf("the entry for %s is missing after the join", date)
		}
	}
	if pending := append(a.status().Pending, b.status().Pending...); len(pending) > 0 {
		t.Fatalf("expected nothing pending, got %v", pending)
	}
}

func TestASaveAndItsOutgoingTransactionAreOneRecoverableUnit(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		stage CommitStage
		lands bool
	}{
		{CommitStaged, false},
		{CommitBackedUp, false},
		{CommitRecorded, true},
		{CommitApplied, true},
	} {
		t.Run(string(tc.stage), func(t *testing.T) {
			t.Parallel()

			a, b := pair(t)
			before := b.status()
			b.restart(FS{CommitStage: func(stage CommitStage, applied int) error {
				if stage == tc.stage {
					return injectedFailure
				}
				return nil
			}})
			draft, err := b.journal.PrepareReflection("2026-08-13", true)
			if err != nil {
				t.Fatalf("PrepareReflection returned error: %v", err)
			}
			if _, err := b.journal.SaveReflection(draft, writtenResult("Interrupted.", "", ""), true); !errors.Is(err, injectedFailure) {
				t.Fatalf("expected the save to die at %s, got %v", tc.stage, err)
			}

			// The process is gone; the next one settles what it left.
			b.restart(FS{})
			entries, err := b.journal.Entries()
			if err != nil {
				t.Fatalf("Entries returned error: %v", err)
			}
			_, saved := FindEntry(entries, "2026-08-13")
			after := b.status()
			recorded := after.Sequence == before.Sequence+1
			if saved != tc.lands || recorded != tc.lands {
				t.Fatalf("expected the day and its transaction to land together (%v), got saved=%v recorded=%v", tc.lands, saved, recorded)
			}
			if tc.lands {
				if tx := b.manifest(after.Heads[0]); len(tx.Changes) != 1 || tx.Changes[0].Artifact != entryArtifact("2026-08-13") {
					t.Fatalf("unexpected outgoing transaction: %+v", tx)
				}
			}

			// Either way the history is whole: nothing is recorded twice or lost.
			imported := b.pull()
			if len(imported.Recorded) != 0 {
				t.Fatalf("the recovered save was recorded again: %+v", imported)
			}
			settle(t, a, b)
			requireSameArtifacts(t, a, b)
			if _, arrived := a.artifacts()[entryArtifact("2026-08-13")]; arrived != tc.lands {
				t.Fatalf("expected the day to reach the other device only when it landed (%v)", tc.lands)
			}
		})
	}
}

func TestAnInterruptedImportIsFinishedOnce(t *testing.T) {
	t.Parallel()

	for _, stage := range []CommitStage{CommitStaged, CommitRecorded, CommitApplied} {
		t.Run(string(stage), func(t *testing.T) {
			t.Parallel()

			a, b := pair(t)
			a.write("2026-08-12", "Desk day.", "", "")
			if _, err := a.journal.SetFocus("The desk's focus."); err != nil {
				t.Fatalf("SetFocus returned error: %v", err)
			}
			a.publish()
			carry(t, a, b, nil)

			b.restart(FS{CommitStage: func(s CommitStage, applied int) error {
				if s == stage {
					return injectedFailure
				}
				return nil
			}})
			if _, err := b.journal.ImportSync(b.exchange()); !errors.Is(err, injectedFailure) {
				t.Fatalf("expected the import to die at %s, got %v", stage, err)
			}

			b.restart(FS{})
			b.pull()
			requireSameArtifacts(t, a, b)
			if imported := b.pull(); len(imported.Applied) != 0 || len(imported.Written) != 0 {
				t.Fatalf("a finished import was applied again: %+v", imported)
			}
			if got := strings.Count(readFileString(t, b.archive()), "The desk's focus."); got != 1 {
				t.Fatalf("expected the focus record once, found it %d times", got)
			}
		})
	}
}

func TestOnlyOneJournalInADirectoryTakesPartInSync(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	work, err := Open(Options{ReflectionsPath: filepath.Join(a.dir, "work.md"), Settings: journalSettings, Clock: fixedNow(20)})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	before := a.tree()
	var claimed *SyncClaimError
	if _, err := work.EnrollSync("work"); !errors.As(err, &claimed) || claimed.Journal != "reflections.md" {
		t.Fatalf("expected the second enrollment to be refused, got %v", err)
	}
	if !sameTree(before, a.tree()) {
		t.Fatal("a refused enrollment wrote something")
	}
	if _, enrolled, err := work.SyncStatus(); err != nil || enrolled {
		t.Fatalf("expected the second journal to stay out of sync, got enrolled=%v error=%v", enrolled, err)
	}

	// The archive is shared, so a focus set from the other journal is still a
	// change to the enrolled one, and is exchanged.
	sequence := a.status().Sequence
	if _, err := work.SetFocus("Set while writing in the work journal."); err != nil {
		t.Fatalf("SetFocus returned error: %v", err)
	}
	if a.status().Sequence != sequence+1 {
		t.Fatal("a change to the shared archive was not recorded")
	}
	settle(t, a, b)
	focus, _, err := b.journal.Focus("2026-08-20")
	if err != nil || focus.Text != "Set while writing in the work journal." {
		t.Fatalf("expected the focus to reach the other device, got %+v, %v", focus, err)
	}
}

func TestSettingsChangesAreExchangedAsOneUnit(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	state, err := a.journal.EffectiveSettings()
	if err != nil {
		t.Fatalf("EffectiveSettings returned error: %v", err)
	}
	anchor := 1
	questions := []string{"Carry?", "Notice?", "Handle?", "Thank?"}
	if _, err := a.journal.UpdateSettings(state.Revision, SettingsChange{Questions: questions, AnchorQuestionIndex: &anchor}); err != nil {
		t.Fatalf("UpdateSettings returned error: %v", err)
	}
	settle(t, a, b)

	got, err := b.journal.EffectiveSettings()
	if err != nil {
		t.Fatalf("EffectiveSettings returned error: %v", err)
	}
	if !equalStrings(got.Settings.Questions, questions) || got.Settings.AnchorQuestionIndex != 1 {
		t.Fatalf("the settings did not arrive whole: %+v", got.Settings)
	}
	if !equalStrings(got.Legacy.Questions, continuityQuestions) || got.Legacy.AnchorQuestionIndex != 3 {
		t.Fatalf("the legacy reading changed in transit: %+v", got.Legacy)
	}
	requireSameArtifacts(t, a, b)
}

func TestAJournalThatIsNotEnrolledKeepsNoSyncHistory(t *testing.T) {
	t.Parallel()

	a := newPeer(t, "desk", 1)
	a.write("2026-08-12", "An ordinary day.", "", "")
	if _, err := a.journal.SetFocus("An ordinary focus."); err != nil {
		t.Fatalf("SetFocus returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.dir, JournalMetadataRootName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no metadata directory beside an unenrolled journal, got %v", err)
	}
	if _, enrolled, err := a.journal.SyncStatus(); err != nil || enrolled {
		t.Fatalf("expected enrolled=false, got %v, %v", enrolled, err)
	}
	for name, run := range map[string]func() error{
		"import":  func() error { _, err := a.journal.ImportSync(a.exchange()); return err },
		"publish": func() error { _, err := a.journal.PublishSync(a.exchange()); return err },
		"record":  func() error { _, err := a.journal.RecordExternalEdits(); return err },
	} {
		if err := run(); !errors.Is(err, ErrSyncNotEnrolled) {
			t.Errorf("%s: expected ErrSyncNotEnrolled, got %v", name, err)
		}
	}
	if len(exchanged(t, a.cloud)) != 0 {
		t.Fatal("an unenrolled journal wrote to the exchange")
	}
}

func TestAnUnreadableSyncStateNeverStopsASave(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	statePath := SyncStatePathFor(b.reflections())
	good := readFileString(t, statePath)
	if err := os.WriteFile(statePath, []byte("{ not the state"), 0644); err != nil {
		t.Fatalf("damaging the state: %v", err)
	}

	b.write("2026-08-13", "Written while sync was broken.", "", "")
	if got := b.journal; got == nil || !strings.Contains(readFileString(t, b.reflections()), "Written while sync was broken.") {
		t.Fatal("the save did not reach the file")
	}

	// Once the state is readable again the day is found in the file and recorded.
	if err := os.WriteFile(statePath, []byte(good), 0644); err != nil {
		t.Fatalf("restoring the state: %v", err)
	}
	settle(t, a, b)
	requireSameArtifacts(t, a, b)
	if a.artifacts()[entryArtifact("2026-08-13")] == "" {
		t.Fatal("the day written while sync was broken never reached the other device")
	}
}

func TestSyncTransactionValidation(t *testing.T) {
	t.Parallel()

	digest := RevisionOf([]byte("contents"))
	valid := SyncTransaction{
		Protocol: SyncProtocolVersion,
		Journal:  strings.Repeat("a", 32),
		Device:   strings.Repeat("b", 32),
		Sequence: 2,
		Parents:  []string{strings.Repeat("1", 64), strings.Repeat("2", 64)},
		Created:  "2026-08-20T21:00:00Z",
		Origin:   SyncOriginLocal,
		Changes: []SyncChange{
			{Artifact: entryArtifact("2026-08-12"), Base: RevisionAbsent, New: digest},
			{Artifact: recordArtifact(strings.Repeat("c", 64)), Base: digest, Deleted: true},
		},
	}
	data, err := encodeSyncTransaction(valid)
	if err != nil {
		t.Fatalf("encoding returned error: %v", err)
	}
	if _, err := DecodeSyncTransaction(data); err != nil {
		t.Fatalf("a valid manifest was refused: %v", err)
	}
	again, _ := encodeSyncTransaction(valid)
	if SyncTransactionID(data) != SyncTransactionID(again) || !recordIDPattern.MatchString(SyncTransactionID(data)) {
		t.Fatal("a manifest's identity is not a stable digest of its bytes")
	}

	for name, mutate := range map[string]func(tx *SyncTransaction){
		"no journal":        func(tx *SyncTransaction) { tx.Journal = "" },
		"a device name":     func(tx *SyncTransaction) { tx.Device = "my phone" },
		"sequence zero":     func(tx *SyncTransaction) { tx.Sequence = 0 },
		"unsorted parents":  func(tx *SyncTransaction) { tx.Parents[0], tx.Parents[1] = tx.Parents[1], tx.Parents[0] },
		"a repeated parent": func(tx *SyncTransaction) { tx.Parents[1] = tx.Parents[0] },
		"a parent by name":  func(tx *SyncTransaction) { tx.Parents = []string{"the one before"} },
		"no timestamp":      func(tx *SyncTransaction) { tx.Created = "yesterday" },
		"an unknown origin": func(tx *SyncTransaction) { tx.Origin = "rebase" },
		"a resolved base":   func(tx *SyncTransaction) { tx.Changes[1].Resolves = []Revision{digest} },
		"resolves by name":  func(tx *SyncTransaction) { tx.Changes[0].Resolves = []Revision{"the other one"} },
		"resolves out of order": func(tx *SyncTransaction) {
			tx.Changes[0].Resolves = []Revision{RevisionOf([]byte("b")), RevisionOf([]byte("b"))}
		},
		"a genesis with roots": func(tx *SyncTransaction) { tx.Origin = SyncOriginGenesis },
		"an unknown artifact":  func(tx *SyncTransaction) { tx.Changes[0].Artifact = "file:reflections.md" },
		"an impossible date":   func(tx *SyncTransaction) { tx.Changes[0].Artifact = entryArtifact("2026-02-30") },
		"a repeated artifact":  func(tx *SyncTransaction) { tx.Changes[1] = tx.Changes[0] },
		"a base that is a name": func(tx *SyncTransaction) {
			tx.Changes[0].Base = "the old one"
		},
		"no new revision":      func(tx *SyncTransaction) { tx.Changes[0].New = "" },
		"deleted and written":  func(tx *SyncTransaction) { tx.Changes[1].New = digest },
		"a deletion of absent": func(tx *SyncTransaction) { tx.Changes[1].Base = RevisionAbsent },
		"a change to itself":   func(tx *SyncTransaction) { tx.Changes[0].Base = digest },
		"deleted settings": func(tx *SyncTransaction) {
			tx.Changes[1] = SyncChange{Artifact: settingsArtifact, Base: digest, Deleted: true}
		},
	} {
		tx := valid
		tx.Parents = append([]string{}, valid.Parents...)
		tx.Changes = append([]SyncChange{}, valid.Changes...)
		mutate(&tx)
		encoded, err := encodeSyncJSON(tx)
		if err != nil {
			t.Fatalf("%s: encoding returned error: %v", name, err)
		}
		if _, err := DecodeSyncTransaction(encoded); err == nil {
			t.Errorf("%s: expected the manifest to be refused", name)
		}
	}

	newer := valid
	newer.Protocol = SyncProtocolVersion + 1
	encoded, _ := encodeSyncJSON(newer)
	var protocol *SyncProtocolError
	if _, err := DecodeSyncTransaction(encoded); !errors.As(err, &protocol) || protocol.Protocol != SyncProtocolVersion+1 {
		t.Fatalf("expected a newer protocol to be named as one, got %v", err)
	}
}

func TestSyncManifestNamesRoundTrip(t *testing.T) {
	t.Parallel()

	device, id := strings.Repeat("b", 32), strings.Repeat("3", 64)
	ref, ok := parseSyncManifestName(device, syncManifestName(12, id))
	if !ok || ref != (SyncTransactionRef{Device: device, Sequence: 12, ID: id}) {
		t.Fatalf("a manifest name did not round-trip: %+v, %v", ref, ok)
	}
	for _, name := range []string{
		".seneca-123.tmp",
		"00000012-" + id,
		"12-" + id + ".json",
		"00000000-" + id + ".json",
		"00000012-short.json",
		"." + syncManifestName(12, id) + ".icloud",
	} {
		if _, ok := parseSyncManifestName(device, name); ok {
			t.Errorf("%q was read as a manifest name", name)
		}
	}
	if _, ok := parseSyncManifestName("not-a-device", syncManifestName(12, id)); ok {
		t.Error("a manifest under a directory that is no device was accepted")
	}
}
