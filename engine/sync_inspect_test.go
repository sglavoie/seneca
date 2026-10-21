package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectingSyncReadsWithoutChangingAnything(t *testing.T) {
	a, b := pair(t)

	// One change the exchange lacks, and one this device lacks.
	a.write("2026-08-12", "Mine.", "", "")
	b.write("2026-08-13", "Theirs.", "", "")
	b.publish()
	carry(t, b, a, nil)

	before := a.tree()
	cloud := readTree(t, a.cloud)

	status, enrolled, err := LoadSyncStatus(a.reflections())
	if err != nil || !enrolled {
		t.Fatalf("LoadSyncStatus returned enrolled=%v error=%v", enrolled, err)
	}
	if want := a.status(); status.Device != want.Device || status.Journal != want.Journal || status.Sequence != want.Sequence {
		t.Fatalf("LoadSyncStatus = %+v, want what the journal reports: %+v", status, want)
	}
	view, err := InspectSyncExchange(a.reflections(), a.exchange())
	if err != nil {
		t.Fatalf("InspectSyncExchange returned error: %v", err)
	}
	if !view.Found || view.Unpublished != 1 || view.Unimported != 1 {
		t.Fatalf("InspectSyncExchange = %+v, want one change each way", view)
	}

	if after := a.tree(); !sameTree(before, after) {
		t.Fatalf("inspecting changed the journal:\nbefore %v\nafter  %v", before, after)
	}
	if after := readTree(t, a.cloud); !sameTree(cloud, after) {
		t.Fatal("inspecting changed the exchange")
	}

	a.publish()
	requireNoDiagnostics(t, a.pull())
	if view, err = InspectSyncExchange(a.reflections(), a.exchange()); err != nil || view.Unpublished != 0 || view.Unimported != 0 {
		t.Fatalf("after an exchange InspectSyncExchange = %+v (%v), want nothing waiting", view, err)
	}
}

func TestInspectingAJournalOutsideSyncCreatesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	reflections := filepath.Join(dir, "reflections.md")

	if _, enrolled, err := LoadSyncStatus(reflections); enrolled || err != nil {
		t.Fatalf("LoadSyncStatus returned enrolled=%v error=%v for a journal outside sync", enrolled, err)
	}
	if _, err := InspectSyncExchange(reflections, NewDirectoryTransport(filepath.Join(dir, "exchange"))); !errors.Is(err, ErrSyncNotEnrolled) {
		t.Fatalf("InspectSyncExchange returned %v, want ErrSyncNotEnrolled", err)
	}
	if PendingCommit(dir) {
		t.Fatal("an empty directory reports an interrupted commit")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("inspecting created %d entries (%v)", len(entries), err)
	}
}
