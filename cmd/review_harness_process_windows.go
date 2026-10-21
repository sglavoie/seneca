//go:build windows

package cmd

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Windows has no process group a signal can address, so there is nothing to
// configure at start: the tree is addressed at kill time instead.
func configureHarnessProcessGroup(command *exec.Cmd) {}

// terminateHarnessTree ends the harness and everything under it with
// `taskkill /T /F`, which walks the parent links the kernel already records, and
// then kills whatever the watcher recorded that those links no longer reach — a
// detached descendant whose parent exited first has no tree left to be found in.
// The robust alternative is a job object, and the standard library does not
// expose one — golang.org/x/sys/windows does, and this plan takes no new
// dependencies. /F rather than a polite close request, because the deadline has
// already passed. If taskkill is unavailable or refuses, killing the named
// process at least ends the harness and harnessWaitDelay bounds the rest.
func terminateHarnessTree(command *exec.Cmd, watcher *harnessTreeWatcher) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}

	// Enumerated before the kill: once taskkill has run, the parent links that
	// name the rest of the run are gone.
	var survivors []int
	if table, err := harnessProcessTable(); err == nil {
		survivors = harnessTreeReach(table, command.Process.Pid, watcher.survivors(table))
		watcher.record(table, survivors)
	}

	treeErr := taskkill(strconv.Itoa(command.Process.Pid), "/T")
	for _, descendant := range survivors {
		// No suspend step to match the Unix freeze: without one, a descendant
		// can fork between the snapshot and this signal. The bounded wait is
		// what stands behind that on this platform.
		_ = taskkill(strconv.Itoa(descendant))
	}

	if treeErr == nil {
		return nil
	}
	return command.Process.Kill()
}

func taskkill(pid string, extra ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), harnessTreeCommandTimeout)
	defer cancel()

	args := append(append([]string{}, extra...), "/F", "/PID", pid)
	return exec.CommandContext(ctx, "taskkill", args...).Run()
}

// harnessProcessTable reads the process table through PowerShell, the one
// process enumerator present on every supported Windows without adding a
// dependency. The rows are printed in the same pid/ppid/group/start shape the
// Unix reader produces so the tree walk is shared: there is no process group
// here, so that column is a constant nothing can match, and `CreationDate.Ticks`
// is what makes a pid an identity rather than a number that will be reused.
func harnessProcessTable() ([]harnessProcess, error) {
	ctx, cancel := context.WithTimeout(context.Background(), harnessTreeCommandTimeout)
	defer cancel()

	const script = `Get-CimInstance Win32_Process | ForEach-Object { '{0} {1} 0 {2}' -f $_.ProcessId, $_.ParentProcessId, $_.CreationDate.Ticks }`
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return nil, err
	}

	table := make([]harnessProcess, 0, 256)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		if pidErr != nil || ppidErr != nil {
			continue
		}
		table = append(table, harnessProcess{
			pid:   pid,
			ppid:  ppid,
			pgid:  0,
			start: fields[3],
		})
	}

	return table, nil
}

func harnessProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// FindProcess opens the process on this platform: an error means no such
	// process.
	_, err := os.FindProcess(pid)
	return err == nil
}
