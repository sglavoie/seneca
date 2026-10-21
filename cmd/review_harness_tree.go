package cmd

import (
	"sync"
	"sync/atomic"
	"time"
)

// harnessTreeSampleInterval is how often the run is sampled for new
// descendants. It is a variable so the test that proves an orphan is still
// stopped does not have to keep its intermediate parent alive for a quarter of
// a second.
//
// The interval is the width of the only remaining gap: a process that is
// started, detached, and orphaned entirely between two samples was never
// visible to Seneca. Real harness children take orders of magnitude longer than
// this to come up, and shortening it further buys less than the `ps` it costs.
var harnessTreeSampleInterval = 250 * time.Millisecond

// harnessProcess is one row of the process table: enough to walk the tree by
// parent link, to recognize what is still in the harness's process group, and
// to tell a recorded process apart from an unrelated one that later inherited
// its pid.
type harnessProcess struct {
	pid   int
	ppid  int
	pgid  int
	start string
}

// harnessTreeWatcher is what gives the deadline authority over a process that
// has left the family. Enumerating the tree only at the deadline can never find
// a descendant that already orphaned itself and left the harness's process
// group, because by then no link names it. So the run is sampled from the
// moment the harness starts, and every process seen below it is remembered:
// once Seneca has seen a process, reparenting cannot hide it again.
//
// A pid alone is not an identity — pids are reused, and killing a recycled one
// would kill a stranger's process. Each pid is recorded with its start time and
// is only ever signalled while the live process table still shows that same
// pair.
type harnessTreeWatcher struct {
	mu   sync.Mutex
	seen map[int]string

	started  atomic.Bool
	stopOnce sync.Once
	done     chan struct{}
	finished chan struct{}
}

func newHarnessTreeWatcher() *harnessTreeWatcher {
	return &harnessTreeWatcher{
		seen:     make(map[int]string, 8),
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}
}

// watch samples the tree under root until stop. The first sample is taken
// before the ticker starts so a harness that spawns and detaches immediately is
// still recorded.
func (w *harnessTreeWatcher) watch(root int) {
	w.started.Store(true)
	go func() {
		defer close(w.finished)

		ticker := time.NewTicker(harnessTreeSampleInterval)
		defer ticker.Stop()

		for {
			w.sample(root)
			select {
			case <-w.done:
				return
			case <-ticker.C:
			}
		}
	}()
}

// stop ends the sampling and waits for it to finish, so no `ps` outlives the
// run that asked for it. A watcher that never started — the harness failed to
// launch, or a caller took an early return — has nothing to wait for, and
// waiting anyway would hang the run on a goroutine that does not exist.
func (w *harnessTreeWatcher) stop() {
	w.stopOnce.Do(func() { close(w.done) })
	if w.started.Load() {
		<-w.finished
	}
}

// sample records everything currently reachable from root. A table it cannot
// read costs this round only: the recorded set is never emptied, so a transient
// `ps` failure narrows the next kill rather than forgetting the last one.
func (w *harnessTreeWatcher) sample(root int) {
	table, err := harnessProcessTable()
	if err != nil {
		return
	}
	// Seeded from survivors rather than from everything ever recorded: a pid that
	// died and was handed to a stranger must not pull that stranger's children
	// into the run's tree.
	w.record(table, harnessTreeReach(table, root, w.survivors(table)))
}

func (w *harnessTreeWatcher) record(table []harnessProcess, pids []int) {
	starts := make(map[int]string, len(table))
	for _, entry := range table {
		starts[entry.pid] = entry.start
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	for _, pid := range pids {
		if start, ok := starts[pid]; ok {
			// Recorded once, under the identity it had when it was still
			// provably ours. A later row for the same pid with a different
			// start time is a different process and must not overwrite this.
			if _, already := w.seen[pid]; !already {
				w.seen[pid] = start
			}
		}
	}
}

// survivors returns the recorded processes that the given table still shows
// alive under the identity they were recorded with. A pid whose start time no
// longer matches has died and been reused by something that is not Seneca's to
// kill, so it is dropped rather than signalled.
func (w *harnessTreeWatcher) survivors(table []harnessProcess) []int {
	w.mu.Lock()
	defer w.mu.Unlock()

	alive := make([]int, 0, len(w.seen))
	for _, entry := range table {
		if start, ok := w.seen[entry.pid]; ok && start == entry.start {
			alive = append(alive, entry.pid)
		}
	}
	return alive
}

// harnessTreeReach returns every process below root: by parent link, by process
// group, and by parent link from each seed. The group half is not redundant —
// the harness is its own group leader, so a process still in its group belongs
// to the run even after a death broke the parent chain above it. The seeds are
// what the watcher already recorded, and they are how a child of an escaped
// process is still found once its own links lead nowhere.
func harnessTreeReach(table []harnessProcess, root int, seeds []int) []int {
	children := make(map[int][]int, len(table))
	present := make(map[int]bool, len(table))
	for _, entry := range table {
		children[entry.ppid] = append(children[entry.ppid], entry.pid)
		present[entry.pid] = true
	}

	queued := make(map[int]bool, len(table))
	// The queue is also the answer: a pid is appended exactly when it is first
	// admitted, so walking it to the end both expands and enumerates the tree.
	found := make([]int, 0, len(seeds)+8)
	admit := func(pid int) {
		// A seed that has already died is not in the table, and init is nobody's
		// descendant however the links happen to read.
		if pid <= 1 || pid == root || queued[pid] || !present[pid] {
			return
		}
		queued[pid] = true
		found = append(found, pid)
	}

	for _, child := range children[root] {
		admit(child)
	}
	for _, entry := range table {
		if entry.pgid == root {
			admit(entry.pid)
		}
	}
	for _, seed := range seeds {
		admit(seed)
	}

	for cursor := 0; cursor < len(found); cursor++ {
		for _, child := range children[found[cursor]] {
			admit(child)
		}
	}

	return found
}
