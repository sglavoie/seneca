package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeHarness installs an executable named after the harness ahead of the real
// one on PATH and points the temp-file helpers at a directory the test owns, so
// a run can be judged by what it left behind as well as by what it returned. It
// returns that directory.
//
// The deadline is the one thing in this file that cannot be faked: only a real
// child process proves CommandContext actually kills one.
func fakeHarness(t *testing.T, harness harnessName, body string) string {
	t.Helper()

	temp := t.TempDir()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, string(harness)), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("writing fake harness: %v", err)
	}
	// Prepended, not replaced: the fake shadows any real harness while the
	// script still finds the shell utilities it uses.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMPDIR", temp)
	return temp
}

func TestRunReviewHarnessStopsAHungHarnessAtTheDeadline(t *testing.T) {
	// `exec` replaces the shell, so the process CommandContext kills is the one
	// actually sleeping.
	fakeHarness(t, harnessClaude, "exec sleep 60\n")

	start := time.Now()
	_, err := runReviewHarness(reviewConfig{Harness: harnessClaude, Timeout: 200 * time.Millisecond}, "the prompt")
	elapsed := time.Since(start)

	var timeout *harnessTimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("expected a harnessTimeoutError, got %T: %v", err, err)
	}
	if elapsed > 30*time.Second {
		t.Fatalf("expected the child to be killed at the deadline, waited %s", elapsed)
	}

	// The message has to carry the three things the author needs: what was
	// stopped, what limit stopped it, and how to raise the limit.
	for _, want := range []string{"claude", "200ms", "--timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected the timeout error to name %q, got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "signal: killed") {
		t.Fatalf("expected the deadline to be explained, not reported as a bare signal: %v", err)
	}
}

// recordingHarness is a fake harness that backgrounds a long sleep and writes
// that child's pid where the test can read it, so an assertion can name the
// exact process it expects to have been stopped rather than guessing from
// elapsed time. `prelude` is shell run before the child is started.
func recordingHarness(t *testing.T, prelude string) string {
	t.Helper()

	pidPath := filepath.Join(t.TempDir(), "descendant.pid")
	fakeHarness(t, harnessClaude, prelude+"sleep 60 &\necho $! > "+pidPath+"\nwait\n")
	return pidPath
}

// recordedPID reads the pid the fake harness wrote. It registers no cleanup on
// purpose: a test that needs one is a test whose descendant Seneca did not stop,
// so the kill belongs where that is the point rather than hidden in a helper
// every assertion silently leans on.
func recordedPID(t *testing.T, pidPath string) int {
	t.Helper()

	raw, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("the fake harness never recorded its child: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		t.Fatalf("expected a pid in %s, got %q", pidPath, raw)
	}
	return pid
}

// waitForProcessExit gives a killed process time to be reaped: SIGKILL is
// delivered before the run returns, but the pid keeps answering until its
// parent — here init, since the harness died first — collects it.
func waitForProcessExit(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if !harnessProcessAlive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Killing the harness is only half the job: `claude` and `codex` both spawn
// their own children, and a deadline that stops the named process while its
// children keep working has not stopped anything the author cares about. The
// harness runs in its own process group so one signal reaches the whole tree.
func TestRunReviewHarnessStopsDescendantsAtDeadline(t *testing.T) {
	// No `set -m`: the shell leaves the backgrounded sleep in the harness's own
	// process group, which is the shape a real harness's children have.
	pidPath := recordingHarness(t, "")

	_, err := runReviewHarness(reviewConfig{Harness: harnessClaude, Timeout: time.Second}, "the prompt")

	var timeout *harnessTimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("expected a harnessTimeoutError, got %T: %v", err, err)
	}

	descendant := recordedPID(t, pidPath)
	if !waitForProcessExit(descendant, 10*time.Second) {
		t.Fatalf("expected the deadline to stop descendant %d, it is still running", descendant)
	}
}

// A descendant that leaves the harness's process group is still stopped at the
// deadline, because the tree kill enumerates by parent link before it kills
// anything. Nothing Seneca started keeps running, and no cleanup in this test
// is what makes that true.
func TestRunReviewHarnessStopsEscapedDescendantsAtDeadline(t *testing.T) {
	// `set -m` is what makes this an escapee: with job control on, the shell puts
	// the background job in a process group of its own, out of reach of the
	// negative-pid signal that ends the group.
	pidPath := recordingHarness(t, "set -m\n")

	_, err := runReviewHarness(reviewConfig{Harness: harnessClaude, Timeout: time.Second}, "the prompt")

	var timeout *harnessTimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("expected a harnessTimeoutError, got %T: %v", err, err)
	}
	// The remedy has to survive the repair: the run is over, and the author still
	// needs to know what was stopped, by what limit, and how to raise it.
	for _, want := range []string{"claude", "1s", "--timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected the timeout error to name %q, got: %v", want, err)
		}
	}

	escapee := recordedPID(t, pidPath)
	if !waitForProcessExit(escapee, 10*time.Second) {
		t.Fatalf("expected the deadline to stop escaped descendant %d, it is still running", escapee)
	}
}

// The case no snapshot taken at the deadline can cover: a descendant that both
// left the harness's process group and outlived the parent that linked it to the
// run. Nothing in the live process table names it as Seneca's by the time the
// limit lands, so the deadline can only stop it by having watched the run all
// along. This test registers no cleanup for the detached process, because a
// cleanup would be the leak it exists to deny.
func TestRunReviewHarnessStopsADetachedOrphanAtDeadline(t *testing.T) {
	// Sampled fast enough that the intermediate parent's short life is enough to
	// be seen, and no faster than the assertions need.
	restoreInterval := harnessTreeSampleInterval
	harnessTreeSampleInterval = 20 * time.Millisecond
	t.Cleanup(func() { harnessTreeSampleInterval = restoreInterval })

	pidPath := filepath.Join(t.TempDir(), "orphan.pid")
	// `set -m` puts the intermediate shell in a process group of its own, so the
	// sleep it starts is outside the harness's group. That shell then exits well
	// before the deadline, leaving the sleep parented to init: neither the group
	// nor the parent links lead back to the harness afterwards. `exec` keeps the
	// harness itself alive as the sleeping process the deadline stops.
	fakeHarness(t, harnessClaude, "set -m\n"+
		"sh -c 'sleep 60 & echo $! > "+pidPath+"; sleep 0.5' &\n"+
		"exec sleep 60\n")

	_, err := runReviewHarness(reviewConfig{Harness: harnessClaude, Timeout: 2 * time.Second}, "the prompt")

	var timeout *harnessTimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("expected a harnessTimeoutError, got %T: %v", err, err)
	}
	// The remedy survives this repair too: the author still needs to know what
	// was stopped, by what limit, and how to raise it.
	for _, want := range []string{"claude", "2s", "--timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected the timeout error to name %q, got: %v", want, err)
		}
	}

	orphan := recordedPID(t, pidPath)
	if !waitForProcessExit(orphan, 10*time.Second) {
		t.Fatalf("expected the deadline to stop detached orphan %d, it is still running", orphan)
	}
}

// The intermediate parent really does die first, and the orphan really does end
// up outside the harness's process group. Asserted separately from the kill so a
// future shell or platform that quietly stops producing that shape fails here,
// naming the cause, rather than making the test above pass for the wrong reason.
func TestDetachedOrphanLeavesBothTheGroupAndTheParentLink(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "orphan.pid")
	script := "set -m\nsh -c 'sleep 30 & echo $! > " + pidPath + "; sleep 0.2' &\nsleep 1.5\n"

	harness := exec.Command("sh", "-c", script)
	configureHarnessProcessGroup(harness)
	if err := harness.Start(); err != nil {
		t.Fatalf("starting the shape probe: %v", err)
	}
	t.Cleanup(func() {
		_ = harness.Process.Kill()
		_ = harness.Wait()
	})

	var orphan int
	deadline := time.Now().Add(5 * time.Second)
	for {
		if raw, err := os.ReadFile(pidPath); err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil && pid > 0 {
				orphan = pid
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the shape probe never recorded its detached process")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// This one is the test's own, and production is not running: it has to be
	// collected here or it outlives the suite.
	t.Cleanup(func() {
		if process, err := os.FindProcess(orphan); err == nil {
			_ = process.Kill()
		}
	})

	// Long enough for the intermediate shell to exit and the reparenting to land.
	time.Sleep(time.Second)

	table, err := harnessProcessTable()
	if err != nil {
		t.Skipf("no readable process table on this platform: %v", err)
	}
	var row *harnessProcess
	for index := range table {
		if table[index].pid == orphan {
			row = &table[index]
			break
		}
	}
	if row == nil {
		t.Fatalf("the detached process %d was gone before the shape could be checked", orphan)
	}
	if row.pgid == harness.Process.Pid {
		t.Fatalf("expected %d to have left the harness process group %d", orphan, harness.Process.Pid)
	}
	if row.ppid == harness.Process.Pid {
		t.Fatalf("expected %d to have been orphaned, it is still a child of the harness", orphan)
	}

	// And with the shape confirmed, the walk that only reads live links is blind
	// to it — which is what makes the watcher, not the snapshot, the authority.
	reached := harnessTreeReach(table, harness.Process.Pid, nil)
	for _, pid := range reached {
		if pid == orphan {
			t.Fatalf("expected the live-link walk to miss %d; if it can see it, this test no longer covers the gap", orphan)
		}
	}
}

// A descendant the tree walk cannot name — one orphaned before the deadline, so
// no parent link leads to it — keeps the output pipe open after its parent is
// gone. Without a bounded second wait the deadline would not end the run, so the
// fallback is still load-bearing and is still tested.
func TestRunReviewHarnessDoesNotWaitForwardOnASurvivingGrandchild(t *testing.T) {
	pidPath := recordingHarness(t, "set -m\n")

	// Neutered deliberately: with the tree kill in place the escapee dies and the
	// pipe closes on its own, which would let this test pass without ever
	// reaching the bounded wait. Killing only the named process is exactly the
	// case harnessWaitDelay exists to bound.
	restoreTerminate := terminateHarness
	terminateHarness = func(command *exec.Cmd, _ *harnessTreeWatcher) error { return command.Process.Kill() }
	t.Cleanup(func() { terminateHarness = restoreTerminate })

	restore := harnessWaitDelay
	harnessWaitDelay = 200 * time.Millisecond
	t.Cleanup(func() { harnessWaitDelay = restore })

	done := make(chan error, 1)
	go func() {
		_, err := runReviewHarness(reviewConfig{Harness: harnessClaude, Timeout: time.Second}, "the prompt")
		done <- err
	}()

	select {
	case err := <-done:
		var timeout *harnessTimeoutError
		if !errors.As(err, &timeout) {
			t.Fatalf("expected a harnessTimeoutError, got %T: %v", err, err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the harness run outlived its deadline waiting on a grandchild")
	}

	// This survivor is the test's own litter — production would have killed it —
	// so the test collects it, registering that before the assertion so a failure
	// leaves nothing behind either.
	survivor := recordedPID(t, pidPath)
	t.Cleanup(func() {
		if process, err := os.FindProcess(survivor); err == nil {
			_ = process.Kill()
		}
	})
	if !harnessProcessAlive(survivor) {
		t.Fatalf("expected survivor %d to outlive the neutered kill, so that the bounded wait is what ended the run", survivor)
	}
}

// The temporary schema and Codex output files are removed on the way out of a
// timed-out run exactly as they are on a successful one: the deadline returns
// through the normal error path, so the deferred removals still fire.
func TestRunReviewHarnessRemovesItsTempFilesAfterATimeout(t *testing.T) {
	// Codex is the harness that creates both temp files.
	temp := fakeHarness(t, harnessCodex, "exec sleep 60\n")

	if _, err := runReviewHarness(reviewConfig{Harness: harnessCodex, Timeout: 200 * time.Millisecond}, "the prompt"); err == nil {
		t.Fatal("expected the deadline to end the run")
	}

	left, err := os.ReadDir(temp)
	if err != nil {
		t.Fatalf("reading the temp directory: %v", err)
	}
	if len(left) != 0 {
		names := make([]string, 0, len(left))
		for _, entry := range left {
			names = append(names, entry.Name())
		}
		t.Fatalf("expected no temporary files after a timeout, found %v", names)
	}
}

// The regression guard on the failure path the timeout branch now precedes: a
// harness that fails fast still reports what it printed.
func TestRunReviewHarnessStillReportsAFastFailureOutput(t *testing.T) {
	fakeHarness(t, harnessClaude, "echo 'model refused: quota exhausted'\nexit 1\n")

	_, err := runReviewHarness(reviewConfig{Harness: harnessClaude, Timeout: time.Minute}, "the prompt")
	if err == nil {
		t.Fatal("expected the failing harness to return an error")
	}
	var timeout *harnessTimeoutError
	if errors.As(err, &timeout) {
		t.Fatalf("a fast failure is not a timeout: %v", err)
	}
	for _, want := range []string{"running claude harness", "model refused: quota exhausted"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected the failure to report %q, got: %v", want, err)
		}
	}
}

func TestReviewTimeoutFlagRejectsANonPositiveDuration(t *testing.T) {
	for _, arg := range []string{"--timeout=0", "--timeout=0s", "--timeout=-5m"} {
		t.Run(arg, func(t *testing.T) {
			err := parseReviewFlags(t, arg)
			if err == nil {
				t.Fatalf("expected %s to be refused at parse", arg)
			}
			if !strings.Contains(err.Error(), "positive duration") {
				t.Fatalf("expected the refusal to ask for a positive duration, got: %v", err)
			}
			if reviewTimeout != defaultReviewTimeout {
				t.Fatalf("expected the refused value not to be applied, got %s", reviewTimeout)
			}
		})
	}
}

func TestReviewTimeoutFlagRejectsSomethingThatIsNotADuration(t *testing.T) {
	err := parseReviewFlags(t, "--timeout=soon")
	if err == nil || !strings.Contains(err.Error(), "Go duration") {
		t.Fatalf("expected a Go duration to be asked for, got: %v", err)
	}
}

func TestReviewTimeoutDefaultsToTenMinutes(t *testing.T) {
	if got := reviewCmd.Flags().Lookup("timeout").DefValue; got != "10m0s" {
		t.Fatalf("expected the documented default to be 10m0s, got %q", got)
	}
	if err := parseReviewFlags(t); err != nil {
		t.Fatalf("parsing no flags: %v", err)
	}
	if reviewTimeout != 10*time.Minute {
		t.Fatalf("expected 10m with the flag absent, got %s", reviewTimeout)
	}
	// A config built without the flag — every fake harness in the suite, and any
	// future caller — gets the same limit rather than an expired one.
	if got := (reviewConfig{Harness: harnessClaude}).harnessTimeout(); got != 10*time.Minute {
		t.Fatalf("expected a zero Timeout to fall back to 10m, got %s", got)
	}
}

func TestReviewTimeoutFlagAcceptsAGoDuration(t *testing.T) {
	if err := parseReviewFlags(t, "--timeout=15m"); err != nil {
		t.Fatalf("parsing --timeout=15m: %v", err)
	}
	if reviewTimeout != 15*time.Minute {
		t.Fatalf("expected 15m, got %s", reviewTimeout)
	}
	if got := (reviewConfig{Timeout: reviewTimeout}).harnessTimeout(); got != 15*time.Minute {
		t.Fatalf("expected the chosen limit to reach the harness, got %s", got)
	}
}
