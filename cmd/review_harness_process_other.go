//go:build !unix && !windows

package cmd

import (
	"errors"
	"os"
	"os/exec"
)

// Neither a Unix process group nor a Windows process tree exists here — this
// build covers targets like js/wasm and plan9, where Seneca cannot run a harness
// subprocess in the first place. The direct child is killed and harnessWaitDelay
// bounds the wait on anything it spawned, which is the most the platform offers.
func configureHarnessProcessGroup(command *exec.Cmd) {}

func terminateHarnessTree(command *exec.Cmd, _ *harnessTreeWatcher) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	return command.Process.Kill()
}

// harnessProcessTable has nothing to read on a target with no process table, so
// the watcher records nothing and the kill above is the whole of containment.
func harnessProcessTable() ([]harnessProcess, error) {
	return nil, errors.ErrUnsupported
}

func harnessProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// FindProcess is a lookup on this platform: an error means no such process.
	_, err := os.FindProcess(pid)
	return err == nil
}
