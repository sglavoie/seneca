package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"github.com/sglavoie/seneca/review/usage"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

func init() {
	reviewCmd.Flags().BoolVar(&reviewWeek, "week", false, "Review the last 7 days")
	reviewCmd.Flags().StringVar(&reviewMonth, "month", "", "Review a month (YYYY-MM; --month alone for current month)")
	reviewCmd.Flags().Lookup("month").NoOptDefVal = "current"
	reviewCmd.Flags().StringVar(&reviewQuarter, "quarter", "", "Review saved reviews across a quarter (YYYY-QN; --quarter alone for current quarter)")
	reviewCmd.Flags().Lookup("quarter").NoOptDefVal = "current"
	reviewCmd.Flags().StringVar(&reviewYear, "year", "", "Review saved reviews across a year (YYYY; --year alone for current year)")
	reviewCmd.Flags().Lookup("year").NoOptDefVal = "current"
	reviewCmd.Flags().StringVar(&reviewHarness, "harness", "", "AI harness to run (claude, copilot, codex, anthropic, openai)")
	reviewCmd.Flags().StringVar(&reviewModel, "model", "", "Model name for the selected harness")
	reviewCmd.Flags().StringVar(&reviewEffort, "effort", "", "Effort level for a provider harness (low, medium, high, xhigh, max)")
	reviewCmd.Flags().Var(reviewTimeoutFlag{&reviewTimeout}, "timeout", "Maximum time to wait for the harness (Go duration, e.g. 15m)")
	if err := reviewCmd.RegisterFlagCompletionFunc("harness", completeHarnessNames); err != nil {
		// Only reachable by naming a flag that was never registered above.
		panic(fmt.Sprintf("registering --harness completion: %v", err))
	}
	if err := reviewCmd.RegisterFlagCompletionFunc("effort", completeReviewEfforts); err != nil {
		panic(fmt.Sprintf("registering --effort completion: %v", err))
	}
	rootCmd.AddCommand(reviewCmd)
}

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Synthesize a weekly, monthly, quarterly, or yearly reflection review",
	Args:  cobra.NoArgs,
	RunE:  runReview,
}

// reviewRuntime holds every dependency a review has on the world outside its
// own logic. Collecting them here is what lets an orchestration test drive a
// whole review — window, harness, decision, and archive write — with a fake
// clock, a fake terminal, and a fake model, and never touch a real one. harness
// is the one seam that yields a raw reply, whether a CLI or a provider wrote it.
// recordUsage receives one record per attempt; nil records nothing.
type reviewRuntime struct {
	now         func() time.Time
	isTTY       func() bool
	harness     func(cfg reviewConfig, prompt string) (reviewReply, error)
	decide      reviewDecider
	out         io.Writer
	recordUsage func(usage.Record)
}

func defaultReviewRuntime(out io.Writer) reviewRuntime {
	return reviewRuntime{
		now:     time.Now,
		isTTY:   func() bool { return term.IsTerminal(os.Stdout.Fd()) },
		harness: obtainReviewResponse,
		decide:  promptReviewDecision,
		out:     out,
	}
}

// reviewPeriodFlags are the four ways to name a window, shortest span first.
var reviewPeriodFlags = []string{"week", "month", "quarter", "year"}

// setReviewPeriodFlags names every period flag the author actually asked for.
// Four flags would be six pairwise comparisons and six one-pair messages, so
// the set is counted instead and reported whole: an author who typed three of
// them learns that in one run rather than three.
func setReviewPeriodFlags(cmd *cobra.Command) []string {
	var set []string
	for _, name := range reviewPeriodFlags {
		// --week is the one bool, so an explicit --week=false is not a request
		// for a weekly window and must not count against another flag.
		if name == "week" {
			if reviewWeek {
				set = append(set, "--week")
			}
			continue
		}
		if cmd.Flags().Changed(name) {
			set = append(set, "--"+name)
		}
	}
	return set
}

func runReview(cmd *cobra.Command, args []string) error {
	if set := setReviewPeriodFlags(cmd); len(set) > 1 {
		return fmt.Errorf("%s are mutually exclusive: choose one period", strings.Join(set, ", "))
	}

	path, err := resolveFilePath()
	if err != nil {
		return err
	}
	archivePath := engine.ArchivePathFor(path)

	// A review reads the journal to decide what it covers, so what other
	// devices delivered comes in first.
	syncBefore(path)
	defer syncAfter(path)

	runtime := defaultReviewRuntime(cmd.OutOrStdout())
	// Without a home there is no pricing override to read; the built-in
	// table still prices the attempt.
	home, _ := os.UserHomeDir()
	runtime.recordUsage = usageLedgerRecorder(path, home, os.Stderr)

	var window reviewWindow
	quarterSet, yearSet := cmd.Flags().Changed("quarter"), cmd.Flags().Changed("year")
	if quarterSet || yearSet {
		window, err = selectArcWindowFrom(archivePath, runtime.now(), quarterSet, yearSet)
	} else {
		window, err = selectEntryWindowFrom(path, runtime.now(), cmd.Flags().Changed("month"))
	}
	if err != nil {
		return err
	}
	if window.Practice, err = practiceForWindow(path, window); err != nil {
		return err
	}

	// The window comes first because "there is nothing to review" is an answer
	// about the author's own material, and a harness is only needed to say
	// something about material that exists. Resolving the harness above this
	// point turned an author who ran --quarter early into an author told to
	// configure claude, which answers a question they did not ask.
	if reportEmptySource(runtime.out, window) {
		return nil
	}

	file, err := configuredReviewSettings()
	if err != nil {
		return err
	}

	cfg, err := resolveReviewConfig(
		reviewHarness,
		cmd.Flags().Changed("harness"),
		reviewModel,
		cmd.Flags().Changed("model"),
		reviewEffort,
		cmd.Flags().Changed("effort"),
		reviewTimeout,
		os.Getenv,
		file,
	)
	if err != nil {
		return err
	}

	return executeReview(runtime, cfg, archivePath, window)
}

// reportEmptySource writes the window's own no-material message and reports
// that the run is over. Both the command layer's pre-harness check and
// executeReview's guard go through it, so the two places a review can stop for
// want of material can never disagree about what they say.
func reportEmptySource(out io.Writer, window reviewWindow) bool {
	if window.sourceCount() != 0 {
		return false
	}
	fmt.Fprintln(out, window.emptySourceMessage())
	return true
}

// configuredReviewSettings reads the validated `[review]` table for the real
// home, mirroring resolveFilePath's shape so the command layer keeps doing the
// environment lookups and resolveReviewConfig keeps taking everything as
// arguments.
func configuredReviewSettings() (reviewSettings, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return reviewSettings{}, fmt.Errorf("getting home directory: %w", err)
	}
	return loadReviewSettings(home)
}

// selectArcWindowFrom builds a quarterly or yearly window out of the review
// archive. The reflection document is never opened for one: an arc review
// synthesizes what Seneca already concluded, not what the author wrote. A
// missing archive is not an error either — it simply means no source reviews,
// which executeReview reports without creating anything.
func selectArcWindowFrom(archivePath string, now time.Time, quarterSet, yearSet bool) (reviewWindow, error) {
	archive, exists, err := engine.LoadReviewArchive(archivePath)
	if err != nil {
		return reviewWindow{}, err
	}
	if exists {
		warnArchiveDiagnostics(archive)
	}
	// reviewRecords returns newest first, which is the order selectArcWindow's
	// supersede rule needs: reversing it would keep the revision that was
	// superseded instead of the one that replaced it.
	return selectArcWindow(archive.ReviewRecords(), now, reviewQuarter, quarterSet, reviewYear, yearSet)
}

// selectEntryWindowFrom builds a weekly or monthly window out of the reflection
// document, which is the only path that reads it.
func selectEntryWindowFrom(path string, now time.Time, monthSet bool) (reviewWindow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return reviewWindow{}, fmt.Errorf("reading file: %w", err)
	}
	entries := engine.ParseReflectionDocument(string(data)).Entries
	window, err := selectReviewWindow(entries, now, reviewWeek, reviewMonth, monthSet)
	if err != nil {
		return window, err
	}
	start := window.startDate()
	for _, e := range entries {
		if e.Date != "" && e.Date < start && e.Date > window.LatestBefore {
			window.LatestBefore = e.Date
		}
	}
	return window, nil
}

// executeReview is the whole review, minus flag and file resolution. Every
// failure before the decision — harness, parse, validation, cancellation — ends
// the run without creating a file, staging a path, or making a commit, and a
// non-TTY run never even reaches the decision.
func executeReview(runtime reviewRuntime, cfg reviewConfig, archivePath string, window reviewWindow) error {
	// The guard is over the window's own source list, so an arc window with no
	// saved reviews stops here exactly as an empty week does: no harness call,
	// no file, no commit. runReview already answered this case before it
	// resolved a harness; this is the floor for every other caller.
	if reportEmptySource(runtime.out, window) {
		return nil
	}

	focus, err := focusContextForWindow(archivePath, window)
	if err != nil {
		return err
	}

	started := time.Now()
	reply, err := runtime.harness(cfg, buildPromptForWindow(window, focus))
	latency := time.Since(started)
	// Every attempt that reached the harness is recorded exactly once, however
	// it ended. The ledger is told after the outcome is known and can never
	// change it.
	record := func(outcome usage.Outcome) {
		if runtime.recordUsage != nil {
			runtime.recordUsage(newUsageRecord(runtime.now(), cfg, window, reply, latency, outcome))
		}
	}
	if err != nil {
		record(harnessOutcome(err))
		return err
	}

	review, err := parseReviewResponse(reply.raw, newReviewGrounding(window, focus))
	if err != nil {
		record(usage.OutcomeInvalidResponse)
		return fmt.Errorf("parsing %s review response: %w", cfg.Harness, err)
	}

	if !runtime.isTTY() {
		// Piped output stays exactly what it has always been: a report. No
		// prompt, no archive, no commit.
		fmt.Fprintln(runtime.out, renderReviewPlain(window, review, focus))
		record(usage.OutcomePiped)
		return nil
	}

	decision, err := runtime.decide(window, review, focus)
	if err != nil {
		if isDecisionAbort(err) {
			record(usage.OutcomeDiscarded)
		} else {
			record(usage.OutcomeFailed)
		}
		return err
	}
	if !decision.saves() {
		fmt.Fprintln(runtime.out, "Discarded: nothing was written.")
		record(usage.OutcomeDiscarded)
		return nil
	}

	if err := saveReview(runtime.out, archivePath, runtime.now(), window, review, focus, decision); err != nil {
		record(usage.OutcomeSaveFailed)
		return err
	}
	record(usage.OutcomeSaved)
	return nil
}
