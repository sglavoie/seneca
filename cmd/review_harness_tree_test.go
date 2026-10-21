package cmd

import (
	"sort"
	"testing"
	"time"
)

// The tree the deadline actually faces: a harness, a child still in its group,
// and a descendant that both left the group and outlived the parent that linked
// it back. The escapee is reachable only as a seed, which is the whole reason
// the watcher records one.
func escapedTree() []harnessProcess {
	return []harnessProcess{
		{pid: 1, ppid: 0, pgid: 1, start: "boot"},
		{pid: 100, ppid: 1, pgid: 100, start: "t0"},
		{pid: 200, ppid: 100, pgid: 100, start: "t1"},
		{pid: 300, ppid: 1, pgid: 300, start: "t2"},
		{pid: 400, ppid: 300, pgid: 300, start: "t3"},
		{pid: 900, ppid: 1, pgid: 900, start: "t4"},
	}
}

func sortedReach(table []harnessProcess, root int, seeds []int) []int {
	found := harnessTreeReach(table, root, seeds)
	sort.Ints(found)
	return found
}

func equalPIDs(got []int, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestHarnessTreeReachMissesAnEscapeeWithoutASeed(t *testing.T) {
	// Stated as an expectation rather than left implied: this is the gap the
	// watcher exists to close, and a walk that quietly started finding 300 on its
	// own would make every seeding test below pass for the wrong reason.
	if got := sortedReach(escapedTree(), 100, nil); !equalPIDs(got, []int{200}) {
		t.Fatalf("expected the live-link walk to find only 200, got %v", got)
	}
}

func TestHarnessTreeReachFollowsASeedAndItsChildren(t *testing.T) {
	got := sortedReach(escapedTree(), 100, []int{300})
	if !equalPIDs(got, []int{200, 300, 400}) {
		t.Fatalf("expected the seed to bring 300 and its child 400, got %v", got)
	}
}

func TestHarnessTreeReachIgnoresADeadSeedAndInit(t *testing.T) {
	// 500 is not in the table at all, and 1 is nobody's descendant however the
	// parent links happen to read. Killing either would mean killing a number
	// rather than a process.
	got := sortedReach(escapedTree(), 100, []int{500, 1})
	if !equalPIDs(got, []int{200}) {
		t.Fatalf("expected dead and init seeds to be dropped, got %v", got)
	}
	if got := sortedReach(escapedTree(), 100, []int{900}); !equalPIDs(got, []int{200, 900}) {
		t.Fatalf("a live seed is still followed; got %v", got)
	}
}

// The recording is what survives reparenting, so it has to be the thing that
// cannot be tricked: a pid that died and was handed to an unrelated process must
// stop being Seneca's to kill the moment its start time stops matching.
func TestHarnessTreeWatcherDropsARecycledPID(t *testing.T) {
	watcher := newHarnessTreeWatcher()
	table := escapedTree()
	watcher.record(table, []int{200, 300})

	if got := watcher.survivors(table); !equalPIDs(got, []int{200, 300}) {
		t.Fatalf("expected both recorded processes to survive an unchanged table, got %v", got)
	}

	recycled := escapedTree()
	for index := range recycled {
		if recycled[index].pid == 300 {
			// Same number, different process: 300 exited and a stranger booted
			// into its pid.
			recycled[index].start = "much later"
			recycled[index].ppid = 900
			recycled[index].pgid = 900
		}
	}
	if got := watcher.survivors(recycled); !equalPIDs(got, []int{200}) {
		t.Fatalf("expected the recycled pid 300 to be disowned, got %v", got)
	}

	// And a second sighting must not quietly re-adopt it under the new identity.
	watcher.record(recycled, []int{300})
	if got := watcher.survivors(recycled); !equalPIDs(got, []int{200}) {
		t.Fatalf("expected 300 to stay disowned after being seen again, got %v", got)
	}
}

// A watcher whose harness never started is stopped on the way out of the run
// like any other. Waiting on a goroutine that was never launched would hang the
// process the deadline exists to give back.
func TestHarnessTreeWatcherStopIsSafeBeforeItWatches(t *testing.T) {
	done := make(chan struct{})
	go func() {
		newHarnessTreeWatcher().stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stopping an unstarted watcher blocked")
	}
}
