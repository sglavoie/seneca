package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/seneca/cmd"
)

func TestRunReturnsZeroWhenExecuteSucceeds(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := run(func() error { return nil }, &stderr)

	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0", exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("run() wrote unexpected stderr output: %q", stderr.String())
	}
}

func TestRunPrintsErrorAndReturnsOne(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := run(func() error { return errors.New("boom") }, &stderr)

	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
	if got := stderr.String(); !strings.Contains(got, "boom\n") {
		t.Fatalf("run() stderr = %q, want message with newline", got)
	}
}

// captureStdout swaps the process's own stdout for a pipe while fn runs. The
// commands resolve their output writer at call time, so this is what makes the
// tests below a check on the real stream rather than on a buffer a test handed
// cobra in place of it.
func captureStdout(t *testing.T, fn func() int) (string, int) {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stdout pipe: %v", err)
	}
	original := os.Stdout
	os.Stdout = writer

	// Drained concurrently: a packet larger than the pipe buffer would
	// otherwise block the command that is writing it.
	captured := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, reader)
		captured <- buf.String()
	}()

	code := fn()

	os.Stdout = original
	if err := writer.Close(); err != nil {
		t.Fatalf("closing the stdout pipe: %v", err)
	}
	out := <-captured
	if err := reader.Close(); err != nil {
		t.Fatalf("closing the stdout reader: %v", err)
	}
	return out, code
}

// senecaArgs points the command tree at a real argument vector for one test.
// Without it cobra would fall back to `go test`'s own flags.
func senecaArgs(t *testing.T, args ...string) {
	t.Helper()

	original := os.Args
	os.Args = append([]string{"seneca"}, args...)
	t.Cleanup(func() { os.Args = original })
}

// seedExportJournal writes a journal in a temporary directory and points
// SENECA_FILE at it, so nothing here can reach the author's own reflections, an
// installed seneca, or another checkout.
func seedExportJournal(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "reflections.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing reflections file: %v", err)
	}
	t.Setenv("SENECA_FILE", path)
	return path
}

const exportTestEntry = "## 2026-03-01\n\n### One?\nFirst.\n---\n\n"

// The export packet is the one thing seneca prints for another program to read,
// so which stream carries it — and what the process exits with — is part of its
// contract, not an implementation detail.
func TestExportPacketGoesToStdoutWithASuccessfulStatus(t *testing.T) {
	path := seedExportJournal(t, exportTestEntry)
	senecaArgs(t, "export", "--from", "2026-03-01", "--to", "2026-03-01")

	var stderr bytes.Buffer
	stdout, code := captureStdout(t, func() int { return run(cmd.Execute, &stderr) })

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected a silent stderr, got %q", stderr.String())
	}
	if !strings.HasPrefix(stdout, "# Seneca journal export\n") {
		t.Fatalf("stdout does not start with the packet header:\n%q", stdout)
	}
	for _, want := range []string{"- Source: " + path + "\n", "### One?\nFirst."} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
}

// A refusal must not be mistaken for a packet by anything reading stdout, and
// must not be printed twice — once by cobra and once by main.
func TestExportRefusalGoesToStderrWithAnUnsuccessfulStatus(t *testing.T) {
	seedExportJournal(t, exportTestEntry)
	senecaArgs(t, "export", "--from", "2026-06-01", "--to", "2026-06-30")

	var stderr bytes.Buffer
	stdout, code := captureStdout(t, func() int { return run(cmd.Execute, &stderr) })

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("expected an empty stdout on refusal, got %q", stdout)
	}
	message := stderr.String()
	if !strings.Contains(message, "no reflection entries dated from 2026-06-01 through 2026-06-30") {
		t.Fatalf("unexpected stderr: %q", message)
	}
	if strings.Count(message, "no reflection entries") != 1 {
		t.Fatalf("expected the refusal printed exactly once, got %q", message)
	}
	if strings.Contains(message, "Usage:") {
		t.Fatalf("expected no usage dump for a data refusal, got %q", message)
	}
}
