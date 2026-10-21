package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// harnessTimeoutError reports an AI harness that ran past its deadline. The
// bare error for a child killed by CommandContext is "signal: killed", which
// names neither who killed it nor why; this says both, and names the flag that
// buys more time. It carries a remedy for the same reason UnreadableContextError
// does: the author is at a terminal and needs the next move, not a diagnosis.
type harnessTimeoutError struct {
	harness harnessName
	limit   time.Duration
}

// harnessWaitDelay is how long a killed harness has to release the output pipe
// before Seneca stops waiting on it. A variable so the test that exercises a
// surviving grandchild does not have to sit through the real one.
var harnessWaitDelay = 5 * time.Second

// harnessTreeCommandTimeout bounds the helper process the tree kill runs to see
// or end the tree (`ps` on Unix, `taskkill` on Windows). The deadline exists to
// give the terminal back, so the machinery that enforces it may not block
// either; if the helper overruns, the group kill and harnessWaitDelay still
// stand behind it.
const harnessTreeCommandTimeout = 2 * time.Second

// terminateHarness is the deadline's kill hook. A variable only so the test
// that proves harnessWaitDelay is still a fallback can neuter tree termination
// and leave a pipe holder standing on purpose; nothing in production reassigns
// it.
var terminateHarness func(*exec.Cmd, *harnessTreeWatcher) error = terminateHarnessTree

func (e *harnessTimeoutError) Error() string {
	return fmt.Sprintf("%s harness exceeded its %s time limit and was stopped: rerun with --timeout to allow longer (nothing was written)",
		e.harness, e.limit)
}

func runReviewHarness(cfg reviewConfig, prompt string) ([]byte, error) {
	schemaPath, err := writeReviewSchema()
	if err != nil {
		return nil, err
	}
	defer os.Remove(schemaPath)

	outputPath := ""
	if cfg.Harness == harnessCodex {
		outputPath, err = prepareReviewOutputPath()
		if err != nil {
			return nil, err
		}
		defer os.Remove(outputPath)
	}

	name, args, err := buildReviewCommand(cfg, prompt, schemaPath, outputPath)
	if err != nil {
		return nil, err
	}

	// The deadline is the whole harness run. Cancelling it kills the child, so
	// a hung `claude` gives the terminal back instead of holding it forever, and
	// the deferred removals above still run: the deadline returns through this
	// normal error path, not through a panic or an exit.
	limit := cfg.harnessTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()

	command := exec.CommandContext(ctx, name, args...)
	// Killing the harness is only half the job: a harness that spawned its own
	// children leaves them running and holding the output pipe. The harness gets
	// its own process group so the deadline can signal the whole tree, and
	// terminateHarnessTree replaces the default Cancel to end every descendant —
	// including one that left the group, and one whose parent died and took the
	// last link to it with him — before this function returns.
	configureHarnessProcessGroup(command)
	watcher := newHarnessTreeWatcher()
	command.Cancel = func() error { return terminateHarness(command, watcher) }
	// The fallback for whatever the tree kill cannot reach — a descendant started
	// and detached inside a single sampling interval was never seen, and it keeps
	// the output pipe open. WaitDelay bounds that second wait, so the deadline is
	// a deadline rather than a suggestion.
	command.WaitDelay = harnessWaitDelay

	// CombinedOutput inlined: the watcher has to start the instant the harness
	// has a pid, and Start is the only place that pid exists before the run is
	// already underway.
	var combined bytes.Buffer
	command.Stdout = &combined
	command.Stderr = &combined
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("running %s harness: %w", cfg.Harness, err)
	}
	watcher.watch(command.Process.Pid)
	err = command.Wait()
	watcher.stop()

	out := combined.Bytes()
	if err != nil {
		// Checked before the output branch: a killed child may have printed
		// something first, and that partial noise must not be reported as the
		// harness's own failure message.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &harnessTimeoutError{harness: cfg.Harness, limit: limit}
		}
		// The output comes back with the error because a CLI that failed may
		// still have reported what the attempt was billed for. It is never a
		// review: the caller reads only that report from it.
		message := strings.TrimSpace(string(out))
		if message == "" {
			return out, fmt.Errorf("running %s harness: %w", cfg.Harness, err)
		}
		return out, fmt.Errorf("running %s harness: %w\n%s", cfg.Harness, err, message)
	}

	if outputPath != "" {
		captured, err := os.ReadFile(outputPath)
		if err != nil {
			return nil, fmt.Errorf("reading %s output: %w", cfg.Harness, err)
		}
		if trimmed := strings.TrimSpace(string(captured)); trimmed != "" {
			return []byte(trimmed), nil
		}
	}

	return out, nil
}

func writeReviewSchema() (string, error) {
	file, err := os.CreateTemp("", "seneca-review-schema-*.json")
	if err != nil {
		return "", fmt.Errorf("creating schema file: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(reviewSchema); err != nil {
		return "", fmt.Errorf("writing schema file: %w", err)
	}

	return file.Name(), nil
}

func prepareReviewOutputPath() (string, error) {
	file, err := os.CreateTemp("", "seneca-review-output-*.json")
	if err != nil {
		return "", fmt.Errorf("creating output file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("closing output file: %w", err)
	}
	return file.Name(), nil
}

func buildReviewCommand(cfg reviewConfig, prompt string, schemaPath string, outputPath string) (string, []string, error) {
	switch cfg.Harness {
	case harnessClaude:
		args := []string{"--print", "--output-format", "json", "--json-schema", reviewSchema}
		if cfg.Model != "" {
			args = append(args, "--model", cfg.Model)
		}
		args = append(args, prompt)
		return string(harnessClaude), args, nil
	case harnessCopilot:
		args := []string{"--allow-all-tools", "--silent"}
		if cfg.Model != "" {
			args = append(args, "--model", cfg.Model)
		}
		args = append(args, "--prompt", prompt)
		return string(harnessCopilot), args, nil
	case harnessCodex:
		args := []string{"exec", "--skip-git-repo-check", "--output-schema", schemaPath}
		if outputPath != "" {
			args = append(args, "-o", outputPath)
		}
		if cfg.Model != "" {
			args = append(args, "--model", cfg.Model)
		}
		args = append(args, prompt)
		return string(harnessCodex), args, nil
	default:
		return "", nil, fmt.Errorf("unsupported harness %q", cfg.Harness)
	}
}
