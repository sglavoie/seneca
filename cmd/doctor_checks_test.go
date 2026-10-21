package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorReportsSymlinkAndPermissionRefusals(t *testing.T) {
	h := newDoctorHome(t)
	target := filepath.Join(t.TempDir(), "elsewhere.md")
	h.write(t, target, doctorHealthyJournal)
	if err := os.Symlink(target, h.reflections); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	report := diagnose(h.env())
	requireDoctor(t, report, doctorError, "is a symlink; seneca refuses to write through one")

	readOnly := newDoctorHome(t)
	readOnly.write(t, readOnly.reflections, doctorHealthyJournal)
	if err := os.Chmod(readOnly.reflections, 0444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	requireDoctor(t, diagnose(readOnly.env()), doctorWarn, "has mode 0444; writes may be refused")
}

func TestDoctorReportsInvalidConfigurationAsAnError(t *testing.T) {
	h := newDoctorHome(t)
	writeConfigFile(t, h.home, "[seneca]\nquestions = [\"One?\", \"One?\"]\n")

	report := diagnose(h.env())

	requireDoctor(t, report, doctorError, "duplicate question")
}

func TestDoctorNamesTheReflectionsPathSource(t *testing.T) {
	h := newDoctorHome(t)
	report := diagnose(h.env())
	requireDoctor(t, report, doctorOK, "(from SENECA_FILE)")

	configured := newDoctorHome(t)
	writeConfigFile(t, configured.home, "[seneca]\nreflections_path = "+quoteTOML(configured.reflections)+"\n")
	env := configured.env()
	env.getenv = func(string) string { return "" }
	requireDoctor(t, diagnose(env), doctorOK, "(from seneca.reflections_path)")

	fallback := newDoctorHome(t)
	fallbackEnv := fallback.env()
	fallbackEnv.getenv = func(string) string { return "" }
	requireDoctor(t, diagnose(fallbackEnv), doctorOK, "(from the built-in default)")
}

func TestDoctorReportsGitAvailabilityAgainstARealRepository(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections, doctorHealthyJournal)

	env := h.env()
	env.lookPath = exec.LookPath
	env.gitRepo = gitRepoProbe

	// Outside a repository the commit is skipped, never fatal.
	requireDoctor(t, diagnose(env), doctorWarn, "is not in a repository; commits are skipped")

	runGit(t, h.dir, "init")
	runGit(t, h.dir, "config", "user.name", "Seneca Test")
	runGit(t, h.dir, "config", "user.email", "seneca@example.com")

	report := diagnose(env)
	requireDoctor(t, report, doctorOK, "is in a repository")
	requireNoDoctor(t, report, doctorError)
}

func TestDoctorReportsTheConfiguredHarnessWithoutRunningIt(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections, doctorHealthyJournal)

	// A fake harness on a temporary PATH. If doctor ever executed it, the
	// sentinel file would appear.
	binDir := t.TempDir()
	sentinel := filepath.Join(binDir, "was-run")
	script := "#!/bin/sh\ntouch " + sentinel + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0755); err != nil {
		t.Fatalf("writing fake harness: %v", err)
	}
	t.Setenv("PATH", binDir)

	env := h.env()
	env.lookPath = exec.LookPath
	env.getenv = func(key string) string {
		switch key {
		case "SENECA_FILE":
			return h.reflections
		case "SENECA_HARNESS":
			return "claude"
		}
		return ""
	}

	report := diagnose(env)
	requireDoctor(t, report, doctorOK, "harness claude (from SENECA_HARNESS) is at")
	requireDoctor(t, report, doctorOK, "(not run)")
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expected doctor never to execute the harness")
	}

	// Configured but absent is a warning: only `seneca review` needs it.
	missing := h.env()
	missing.getenv = func(key string) string {
		if key == "SENECA_HARNESS" {
			return "codex"
		}
		return ""
	}
	requireDoctor(t, diagnose(missing), doctorWarn, "harness codex (from SENECA_HARNESS) is configured but is not on PATH")

	// A harness name Seneca does not support is invalid configuration.
	invalid := h.env()
	invalid.getenv = func(key string) string {
		if key == "SENECA_HARNESS" {
			return "gemini"
		}
		return ""
	}
	requireDoctor(t, diagnose(invalid), doctorError, "invalid harness \"gemini\"")
}

// Doctor names which standing source supplies the harness, the way it already
// names the source of the reflections path. It cannot see `--harness`: that
// belongs to one `seneca review` invocation and doctor has no such flag.
func TestDoctorNamesTheHarnessSource(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatalf("writing fake harness: %v", err)
	}
	t.Setenv("PATH", binDir)

	configured := newDoctorHome(t)
	configured.write(t, configured.reflections, doctorHealthyJournal)
	writeConfigFile(t, configured.home, "[review]\nharness = \"claude\"\nmodel = \"opus\"\n")
	fromFile := configured.env()
	fromFile.lookPath = exec.LookPath
	requireDoctor(t, diagnose(fromFile), doctorOK, "harness claude (from review.harness) is at")

	// The environment outranks the file, and the report says so.
	fromEnv := configured.env()
	fromEnv.lookPath = exec.LookPath
	fromEnv.getenv = func(key string) string {
		switch key {
		case "SENECA_FILE":
			return configured.reflections
		case "SENECA_HARNESS":
			return "claude"
		}
		return ""
	}
	requireDoctor(t, diagnose(fromEnv), doctorOK, "harness claude (from SENECA_HARNESS) is at")

	// Neither source set: the remedy names all three, flag included.
	none := newDoctorHome(t)
	none.write(t, none.reflections, doctorHealthyJournal)
	report := diagnose(none.env())
	requireDoctor(t, report, doctorInfo, "no harness configured")
	requireDoctor(t, report, doctorInfo, "--harness, SENECA_HARNESS, or review.harness")
}

// An invalid [review].harness is invalid configuration, so doctor reports it as
// an error the way it reports a duplicate question — and still names the
// environment's harness when one is set, because that is what a review would
// actually use.
func TestDoctorReportsAnInvalidConfiguredHarness(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections, doctorHealthyJournal)
	writeConfigFile(t, h.home, "[review]\nharness = \"gemini\"\n")

	report := diagnose(h.env())
	requireDoctor(t, report, doctorError, "review.harness: invalid harness \"gemini\"")

	withEnv := h.env()
	withEnv.getenv = func(key string) string {
		switch key {
		case "SENECA_FILE":
			return h.reflections
		case "SENECA_HARNESS":
			return "codex"
		}
		return ""
	}
	overridden := diagnose(withEnv)
	requireDoctor(t, overridden, doctorError, "review.harness: invalid harness \"gemini\"")
	requireDoctor(t, overridden, doctorWarn, "harness codex (from SENECA_HARNESS)")
}

func TestReportDoctorExitStatusFollowsErrorsOnly(t *testing.T) {
	t.Parallel()

	var report doctorReport
	report.add(doctorCheckGit, doctorWarn, "git is not on PATH")
	report.add(doctorCheckArchive, doctorInfo, "no review archive yet")

	var out strings.Builder
	if err := reportDoctor(&out, report); err != nil {
		t.Fatalf("expected warnings and information to succeed, got %v", err)
	}
	if !strings.Contains(out.String(), "No errors, 1 warning") {
		t.Fatalf("expected a warning summary, got %q", out.String())
	}

	report.add(doctorCheckReflections, doctorError, "duplicate entry dates")
	out.Reset()
	err := reportDoctor(&out, report)
	if err == nil || !strings.Contains(err.Error(), "doctor found 1 error") {
		t.Fatalf("expected a nonzero result naming the error count, got %v", err)
	}
	if !strings.Contains(out.String(), "1 error, 1 warning") {
		t.Fatalf("expected the report to still print, got %q", out.String())
	}
}
