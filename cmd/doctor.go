package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"github.com/sglavoie/seneca/review/provider"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Doctor is Seneca's read-only diagnosis. It answers one question — "would the
// commands that write work right now, and is what they already wrote still
// coherent?" — and it answers it without touching anything.
//
// # Read-only contract
//
// Doctor never creates a directory or a file, never writes a `.bak`, never
// stages or commits, never runs an AI harness, and never repairs anything it
// finds. Every filesystem call below is a stat or a read. `ensureDir`, the
// transaction types in persistence.go, and `commitArchivePaths` are all off
// limits here; the git probe is `rev-parse --git-dir`, which only reads.
//
// # Severity
//
//   - error: corrupt or ambiguous data, and invalid configuration. These are
//     the states a writing command already refuses, so doctor names them before
//     the author discovers them mid-reflection.
//   - warn: an optional integration is unavailable (git, the configured
//     harness) or the filesystem looks like it will refuse a write.
//   - info: a healthy fact worth stating — legacy entries, a missing optional
//     archive, no active focus.
//   - ok: the check passed.
//
// The process exits nonzero only when at least one error is reported; a report
// of warnings and information is a success.

const (
	doctorCheckConfig      = "config"
	doctorCheckPaths       = "paths"
	doctorCheckReflections = "reflections"
	doctorCheckArchive     = "archive"
	doctorCheckGit         = "git"
	doctorCheckHarness     = "harness"
	doctorCheckUsage       = "usage"
)

var doctorDiagnostics bool

func init() {
	doctorCmd.Flags().BoolVar(&doctorDiagnostics, "diagnostics", false,
		"Print a content-free report to share: versions, counts, and codes, nothing written in the journal")
	rootCmd.AddCommand(doctorCmd)
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check configuration, reflections, and the review archive",
	Long: "Report on everything Seneca depends on: configuration, the resolved\n" +
		"reflections file, the review archive, directory permissions, git, the\n" +
		"configured AI harness, where sync stands, and the usage ledger and pricing.\n\n" +
		"Doctor is strictly read-only. It creates nothing, writes nothing, commits\n" +
		"nothing, calls no AI harness, and repairs nothing. It exits nonzero only\n" +
		"when it reports an error.\n\n" +
		"--diagnostics prints a report to share instead: versions, counts, and stable\n" +
		"codes, with no path, question, date, or anything written in the journal.",
	Args: cobra.NoArgs,
	RunE: runDoctor,
	// A nonzero exit here means the author's data has a problem, not that they
	// typed the command wrong. Printing usage and a second copy of the error
	// under a report that already says everything would bury it.
	SilenceUsage:  true,
	SilenceErrors: true,
}

type doctorSeverity int

const (
	doctorOK doctorSeverity = iota
	doctorInfo
	doctorWarn
	doctorError
)

func (s doctorSeverity) label() string {
	switch s {
	case doctorInfo:
		return "info"
	case doctorWarn:
		return "warn"
	case doctorError:
		return "error"
	default:
		return "ok"
	}
}

type doctorResult struct {
	check    string
	severity doctorSeverity
	detail   string
}

type doctorReport struct {
	results []doctorResult
}

func (r *doctorReport) add(check string, severity doctorSeverity, format string, args ...any) {
	r.results = append(r.results, doctorResult{
		check:    check,
		severity: severity,
		detail:   fmt.Sprintf(format, args...),
	})
}

func (r doctorReport) count(severity doctorSeverity) int {
	n := 0
	for _, result := range r.results {
		if result.severity == severity {
			n++
		}
	}
	return n
}

// render lays the report out as one aligned line per result, in the order the
// checks ran, so the output reads top-down like the startup path it mirrors.
func (r doctorReport) render() string {
	var b strings.Builder
	b.WriteString("Seneca doctor\n\n")

	width := 0
	for _, result := range r.results {
		if len(result.check) > width {
			width = len(result.check)
		}
	}
	for _, result := range r.results {
		fmt.Fprintf(&b, "  %-5s %-*s  %s\n", result.severity.label(), width, result.check, result.detail)
	}

	errorCount, warnCount := r.count(doctorError), r.count(doctorWarn)
	b.WriteByte('\n')
	switch {
	case errorCount > 0:
		fmt.Fprintf(&b, "%s, %s\n", engine.Plural(errorCount, "error", "errors"), engine.Plural(warnCount, "warning", "warnings"))
	case warnCount > 0:
		fmt.Fprintf(&b, "No errors, %s\n", engine.Plural(warnCount, "warning", "warnings"))
	default:
		b.WriteString("No errors.\n")
	}
	return b.String()
}

// doctorEnv is everything outside the checks themselves. Tests supply a
// temporary home, a fake environment, a fake PATH lookup, and a fake git probe,
// so no check ever reads the author's real state.
type doctorEnv struct {
	home     string
	getenv   func(string) string
	lookPath func(string) (string, error)
	gitRepo  func(dir string) error
	// keychain reads a provider's API key; nil means no Keychain to read.
	keychain provider.KeychainReader
	// today anchors every "N days ago"; empty means the real today.
	today string
}

func (e doctorEnv) todayDate() string {
	if e.today != "" {
		return e.today
	}
	return time.Now().Format(engine.CalendarDateLayout)
}

func defaultDoctorEnv() (doctorEnv, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return doctorEnv{}, fmt.Errorf("getting home directory: %w", err)
	}
	return doctorEnv{
		home:     home,
		getenv:   os.Getenv,
		lookPath: exec.LookPath,
		gitRepo:  gitRepoProbe,
		keychain: provider.SecurityKeychain,
	}, nil
}

// gitRepoProbe asks git whether a directory is inside a repository. It is the
// read-only half of what gitCommitPaths does before it stages anything.
func gitRepoProbe(dir string) error {
	return exec.Command("git", "-C", dir, "rev-parse", "--git-dir").Run()
}

func runDoctor(cmd *cobra.Command, args []string) error {
	env, err := defaultDoctorEnv()
	if err != nil {
		return err
	}
	if doctorDiagnostics {
		return writeSharedDiagnostics(cmd.OutOrStdout(), env)
	}
	return reportDoctor(cmd.OutOrStdout(), diagnose(env))
}

// reportDoctor prints the report and turns errors — and only errors — into a
// nonzero process status.
func reportDoctor(out io.Writer, report doctorReport) error {
	fmt.Fprint(out, report.render())
	if n := report.count(doctorError); n > 0 {
		return fmt.Errorf("doctor found %s", engine.Plural(n, "error", "errors"))
	}
	return nil
}

// diagnose runs every check. Nothing here mutates: the whole function is stats,
// reads, and one `git rev-parse`.
func diagnose(env doctorEnv) doctorReport {
	var report doctorReport

	path, ok := checkDoctorConfig(env, &report)
	if !ok {
		// Without a resolved reflections path there is nothing left to inspect;
		// every remaining check is defined relative to it.
		return report
	}

	checkDoctorJournalSettings(env, path, &report)
	checkDoctorPaths(path, &report)
	today := env.todayDate()
	entries := checkDoctorReflections(path, today, &report)
	if archive, ok := checkDoctorArchive(engine.ArchivePathFor(path), today, &report); ok {
		if note, due := engine.ReviewDueNote(entries, archive, today); due {
			report.add(doctorCheckArchive, doctorInfo, "%s", note)
		}
	}
	checkDoctorSync(env, path, &report)
	checkDoctorGit(env, path, &report)
	harness, settings := checkDoctorHarness(env, &report)
	checkDoctorUsage(env, path, harness, settings, &report)
	return report
}
