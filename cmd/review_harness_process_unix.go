//go:build unix

package cmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// harnessFreezeRounds bounds the stop-then-look loop below. Each round can only
// discover children forked during the previous one, so the tree settles in two
// rounds in practice; the extra rounds are for a harness that is still starting
// its workers as the deadline lands.
const harnessFreezeRounds = 4

// Killing the harness process alone is not enough: `claude` and `codex` both
// spawn their own children, and those children survive their parent, keep
// running paid work, and hold the output pipe open. Giving the harness its own
// process group means one signal can reach the whole tree, so the deadline
// stops everything Seneca started rather than just the process it named.
func configureHarnessProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateHarnessTree is the harness command's Cancel hook, and it ends the
// whole tree rather than a group: a descendant that calls setsid, or a shell job
// started with job control on, leaves the harness's process group and would
// survive a group kill while its work keeps costing money. Worse, a descendant
// whose parent already exited has no link back to the harness at all — which is
// why the watcher has been recording the tree since the run began, and why its
// record, not the live links, is the authority here.
//
// SIGKILL rather than SIGTERM, because the deadline has already passed and a
// harness that ignored a terminate would put Seneca back where it started.
func terminateHarnessTree(command *exec.Cmd, watcher *harnessTreeWatcher) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	pid := command.Process.Pid

	// Enumerate before killing anything for good: the moment the harness dies,
	// its children are reparented to init and the links that name them as
	// Seneca's work are gone.
	tree := freezeHarnessTree(pid, watcher)

	groupErr := syscall.Kill(-pid, syscall.SIGKILL)
	for _, descendant := range tree {
		// Already stopped, so nothing here can fork ahead of the signal.
		_ = syscall.Kill(descendant, syscall.SIGKILL)
	}

	if groupErr == nil {
		return nil
	}
	if errors.Is(groupErr, syscall.ESRCH) {
		// The whole group is already gone; os/exec reads this as "nothing left
		// to cancel" rather than as a cancellation failure.
		return os.ErrProcessDone
	}
	return command.Process.Kill()
}

// freezeHarnessTree stops every process it can reach from the harness and
// returns them, repeating until a round finds nothing new. Stopping first is
// what makes the list worth having: a running descendant can fork between the
// snapshot and the kill and the child would outlive both, while a stopped one
// cannot. SIGKILL is delivered to stopped processes just the same.
//
// Reach means three things at once: still in the harness's process group, still
// linked to it by parent, or recorded by the watcher at any point during the
// run. The third is what covers a process that has since escaped both of the
// first two, and it is checked against the live table so a reused pid is left
// alone.
//
// A tree it cannot read at all — no `ps` — comes back short rather than wrong,
// and the group kill and harnessWaitDelay carry what is left.
func freezeHarnessTree(pid int, watcher *harnessTreeWatcher) []int {
	seen := make(map[int]bool)
	frozen := make([]int, 0, 8)

	for round := 0; round < harnessFreezeRounds; round++ {
		_ = syscall.Kill(-pid, syscall.SIGSTOP)

		table, err := harnessProcessTable()
		if err != nil {
			break
		}
		// Recorded as well as read: a child forked between the last sample and
		// this freeze is still linked now, and adding it here means later rounds
		// keep it even if its parent dies mid-kill.
		reached := harnessTreeReach(table, pid, watcher.survivors(table))
		watcher.record(table, reached)

		discovered := false
		for _, candidate := range reached {
			if seen[candidate] || candidate == os.Getpid() {
				continue
			}
			seen[candidate] = true
			frozen = append(frozen, candidate)
			discovered = true
			_ = syscall.Kill(candidate, syscall.SIGSTOP)
		}
		if !discovered {
			break
		}
	}

	return frozen
}

// harnessProcessTable reads the process table through `ps`. The standard library
// has no portable way to enumerate processes, and
// `ps -A -o pid=,ppid=,pgid=,lstart=` is the one spelling BSD and Linux answer
// identically; the alternative is a /proc reader plus a sysctl reader plus the
// build tags to choose between them. `lstart` is there to make a pid an
// identity: it is what tells a recorded descendant apart from an unrelated
// process that inherited its number after it died.
//
// A row that does not parse is skipped rather than fatal — the caller degrades
// to the group kill, and this is a kill path, not a report.
func harnessProcessTable() ([]harnessProcess, error) {
	ctx, cancel := context.WithTimeout(context.Background(), harnessTreeCommandTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,pgid=,lstart=").Output()
	if err != nil {
		return nil, err
	}

	table := make([]harnessProcess, 0, 256)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		// `lstart` is a date spread over several fields, so the tail is taken
		// whole rather than counted.
		if len(fields) < 4 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		pgid, pgidErr := strconv.Atoi(fields[2])
		if pidErr != nil || ppidErr != nil || pgidErr != nil {
			continue
		}
		table = append(table, harnessProcess{
			pid:   pid,
			ppid:  ppid,
			pgid:  pgid,
			start: strings.Join(fields[3:], " "),
		})
	}

	return table, nil
}

// harnessProcessAlive reports whether a pid still names a live process. Signal
// 0 performs the permission and existence checks without delivering anything;
// only ESRCH means the process is really gone, since a process owned by someone
// else answers with EPERM.
func harnessProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
