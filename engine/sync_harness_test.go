package engine

import (
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The sync tests run two workspaces that share nothing but an exchange: each
// has its own directory, its own Journal, and its own identity. Neither has a
// Git repository, and nothing leaves the test's temporary directories.
//
// Each peer sees the exchange through a folder of its own, the way two devices
// each see their own copy of a synced folder. A courier carries files from one
// folder to the other, so a test decides what has arrived, in what order, and
// how much of it.

type peer struct {
	t     *testing.T
	label string
	dir   string
	// cloud is this peer's copy of the exchange folder.
	cloud string
	seed  int64
	// clock is this peer's time; restart opens the journal with it.
	clock   func() time.Time
	journal *Journal
}

func newPeer(t *testing.T, label string, seed int64) *peer {
	t.Helper()

	p := &peer{t: t, label: label, dir: t.TempDir(), cloud: t.TempDir(), seed: seed, clock: fixedNow(20)}
	p.restart(FS{})
	return p
}

// restart drops the Journal and opens the workspace again, as a process that
// was closed and started later does. Nothing is carried over in memory.
func (p *peer) restart(seams FS) {
	p.t.Helper()

	journal, err := Open(Options{
		ReflectionsPath: p.reflections(),
		Settings:        journalSettings,
		Clock:           p.clock,
		FS:              unflushed(seams),
		LockWait:        5 * time.Second,
		Random:          rand.New(rand.NewSource(p.seed)),
	})
	if err != nil {
		p.t.Fatalf("%s: Open returned error: %v", p.label, err)
	}
	p.journal = journal
}

// unflushed skips the flushes to disk, which decide nothing these tests look at
// and are most of what an exchange costs.
func unflushed(seams FS) FS {
	seams.SyncFile = func(*os.File) error { return nil }
	seams.SyncDir = func(*os.File) error { return nil }
	return seams
}

func (p *peer) reflections() string { return filepath.Join(p.dir, "reflections.md") }

func (p *peer) archive() string { return filepath.Join(p.dir, ArchiveFileName) }

func (p *peer) exchange() *DirectoryTransport {
	return &DirectoryTransport{root: p.cloud, fs: unflushed(FS{})}
}

// seed writes entries straight into the reflections file, as a journal that
// existed before it was enrolled.
func (p *peer) seedEntries(entries ...string) {
	p.t.Helper()

	var content strings.Builder
	for _, entry := range entries {
		content.WriteString(entry + EntryTerminator)
	}
	p.overwrite(content.String())
}

// overwrite replaces the reflections file the way an editor does: without the
// engine, the lock, or the sync history.
func (p *peer) overwrite(content string) {
	p.t.Helper()

	if err := os.WriteFile(p.reflections(), []byte(content), 0644); err != nil {
		p.t.Fatalf("%s: writing the reflections file: %v", p.label, err)
	}
}

// write saves a reflection through the engine, as the daily form does.
func (p *peer) write(date string, answers ...string) {
	p.t.Helper()

	draft, err := p.journal.PrepareReflection(date, true)
	if err != nil {
		p.t.Fatalf("%s: PrepareReflection(%s) returned error: %v", p.label, date, err)
	}
	if _, err := p.journal.SaveReflection(draft, writtenResult(answers...), true); err != nil {
		p.t.Fatalf("%s: SaveReflection(%s) returned error: %v", p.label, date, err)
	}
}

func (p *peer) enroll() SyncStatus {
	p.t.Helper()

	status, err := p.journal.EnrollSync(p.label)
	if err != nil {
		p.t.Fatalf("%s: EnrollSync returned error: %v", p.label, err)
	}
	return status
}

func (p *peer) join() SyncStatus {
	p.t.Helper()

	status, err := p.journal.JoinSync(p.exchange(), p.label)
	if err != nil {
		p.t.Fatalf("%s: JoinSync returned error: %v", p.label, err)
	}
	return status
}

func (p *peer) publish() SyncPublished {
	p.t.Helper()

	published, err := p.journal.PublishSync(p.exchange())
	if err != nil {
		p.t.Fatalf("%s: PublishSync returned error: %v", p.label, err)
	}
	return published
}

func (p *peer) pull() SyncImported {
	p.t.Helper()

	imported, err := p.journal.ImportSync(p.exchange())
	if err != nil {
		p.t.Fatalf("%s: ImportSync returned error: %v", p.label, err)
	}
	return imported
}

func (p *peer) conflicts() []SyncConflict {
	p.t.Helper()

	conflicts, err := p.journal.SyncConflicts()
	if err != nil {
		p.t.Fatalf("%s: SyncConflicts returned error: %v", p.label, err)
	}
	return conflicts
}

func (p *peer) status() SyncStatus {
	p.t.Helper()

	status, enrolled, err := p.journal.SyncStatus()
	if err != nil || !enrolled {
		p.t.Fatalf("%s: SyncStatus returned enrolled=%v error=%v", p.label, enrolled, err)
	}
	return status
}

// artifacts reads the peer's three files as artifacts. It fails the test when
// one of them is not a file a local command would accept.
func (p *peer) artifacts() map[string]string {
	p.t.Helper()

	found := map[string]string{}
	for _, source := range syncSources {
		path := map[syncSource]string{
			syncSourceReflections: p.reflections(),
			syncSourceArchive:     p.archive(),
			syncSourceSettings:    SettingsPathFor(p.reflections()),
		}[source]
		data, err := os.ReadFile(path)
		view := viewSource(source, path, data, err == nil)
		if view.problem != "" {
			p.t.Fatalf("%s: the %s file is not valid: %s", p.label, source, view.problem)
		}
		for _, unit := range view.units {
			found[unit.id] = string(unit.data)
		}
	}
	return found
}

// tree reads every file in the workspace, keyed by its path relative to it.
func (p *peer) tree() map[string]string {
	p.t.Helper()
	return readTree(p.t, p.dir)
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()

	found := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		found[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatalf("reading %s: %v", root, err)
	}
	return found
}

// exchanged lists the files in a peer's copy of the exchange, sorted.
func exchanged(t *testing.T, cloud string) []string {
	t.Helper()

	var names []string
	for name := range readTree(t, cloud) {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// carry copies files from one peer's copy of the exchange to another's, as the
// service between them eventually does. pick chooses which; nil carries all.
func carry(t *testing.T, from, to *peer, pick func(name string) bool) {
	t.Helper()

	for _, name := range exchanged(t, from.cloud) {
		if pick != nil && !pick(name) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(from.cloud, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("carrying %s: %v", name, err)
		}
		target := filepath.Join(to.cloud, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatalf("carrying %s: %v", name, err)
		}
		if err := os.WriteFile(target, data, 0644); err != nil {
			t.Fatalf("carrying %s: %v", name, err)
		}
	}
}

// settle exchanges in both directions until neither peer has anything left to
// send or apply.
func settle(t *testing.T, a, b *peer) {
	t.Helper()

	for round := 0; round < 4; round++ {
		a.publish()
		carry(t, a, b, nil)
		b.pull()
		b.publish()
		carry(t, b, a, nil)
		a.pull()
	}
}

func requireSameArtifacts(t *testing.T, a, b *peer) {
	t.Helper()

	left, right := a.artifacts(), b.artifacts()
	if !sameTree(left, right) {
		t.Fatalf("%s and %s hold different journals:\n%s: %q\n%s: %q", a.label, b.label, a.label, left, b.label, right)
	}
}

func requireNoDiagnostics(t *testing.T, imported SyncImported) {
	t.Helper()

	if len(imported.Diagnostics) > 0 || len(imported.Pending) > 0 {
		t.Fatalf("expected a clean import, got diagnostics %v and pending %v", imported.Diagnostics, imported.Pending)
	}
}

func hasDiagnostic(imported SyncImported, code SyncDiagnosticCode) bool {
	for _, diagnostic := range imported.Diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

// pair is an enrolled peer with a week of writing, a focus, and a second peer
// that joined it and holds the same journal.
func pair(t *testing.T) (a, b *peer) {
	t.Helper()

	a = newPeer(t, "desk", 1)
	a.seedEntries(
		dayEntry("2026-08-10", "Noticed.", "Handled.", "Start before nine."),
		dayEntry("2026-08-11", "Again.", "", "Keep going."),
	)
	if _, err := a.journal.SetFocus("The first focus."); err != nil {
		t.Fatalf("SetFocus returned error: %v", err)
	}
	a.enroll()
	a.publish()

	b = newPeer(t, "phone", 2)
	carry(t, a, b, nil)
	b.join()
	requireNoDiagnostics(t, b.pull())
	requireSameArtifacts(t, a, b)
	return a, b
}

// manifests lists the manifests in a peer's copy of the exchange written by
// device, oldest first.
func manifests(t *testing.T, cloud, device string) []string {
	t.Helper()

	var names []string
	for _, name := range exchanged(t, cloud) {
		if strings.HasPrefix(name, syncTransactionsName+"/"+device+"/") {
			names = append(names, name)
		}
	}
	return names
}
