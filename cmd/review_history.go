package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

var (
	reviewHistoryLast   int
	reviewHistoryPeriod string
)

func init() {
	reviewHistoryCmd.Flags().IntVar(&reviewHistoryLast, "last", 1, "How many saved reviews to show, newest first")
	reviewHistoryCmd.Flags().StringVar(&reviewHistoryPeriod, "period", "", "Only show reviews of one period (weekly, monthly, quarterly, yearly)")
	if err := reviewHistoryCmd.RegisterFlagCompletionFunc("period", completeReviewPeriods); err != nil {
		// Only reachable by naming a flag that was never registered above.
		panic(fmt.Sprintf("registering --period completion: %v", err))
	}
	reviewCmd.AddCommand(reviewHistoryCmd)
}

func completeReviewPeriods(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return engine.ArchiveReviewPeriodNames(), cobra.ShellCompDirectiveNoFileComp
}

var reviewHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Show saved reviews from the archive",
	Long: "Read saved reviews back out of the review archive, newest first.\n" +
		"This never contacts an AI harness and never creates a file.",
	Args: cobra.NoArgs,
	RunE: runReviewHistory,
}

func runReviewHistory(cmd *cobra.Command, args []string) error {
	path, err := resolveArchivePath()
	if err != nil {
		return err
	}
	return showReviewHistoryFor(cmd.OutOrStdout(), path, reviewHistoryLast, reviewHistoryPeriod, term.IsTerminal(os.Stdout.Fd()))
}

// showReviewHistory prints the newest `last` saved reviews of every period.
func showReviewHistory(out io.Writer, path string, last int, tty bool) error {
	return showReviewHistoryFor(out, path, last, "", tty)
}

// showReviewHistoryFor prints the newest `last` saved reviews, keeping only one
// period when one is named — so `--period monthly --last 3` is the last three
// months, not whatever monthly reviews hide among the last three of any kind.
// A missing archive or an archive holding only focus events is reported, never
// created or filled.
func showReviewHistoryFor(out io.Writer, path string, last int, period string, tty bool) error {
	if last <= 0 {
		return fmt.Errorf("invalid --last %d: expected a positive number", last)
	}
	if period != "" && !isArchiveReviewPeriod(period) {
		return fmt.Errorf("invalid --period %q: expected one of %s", period, strings.Join(engine.ArchiveReviewPeriodNames(), ", "))
	}

	archive, exists, err := engine.LoadReviewArchive(path)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(out, "No saved reviews yet: no review archive at %s\n", path)
		return nil
	}
	warnArchiveDiagnostics(archive)

	records := archive.ReviewRecords()
	if period != "" {
		kept := engine.ReviewRecordsOfPeriod(records, period)
		if len(kept) == 0 {
			fmt.Fprintf(out, "No saved %s reviews yet in %s\n", period, path)
			return nil
		}
		records = kept
	}
	if len(records) == 0 {
		fmt.Fprintf(out, "No saved reviews yet in %s\n", path)
		return nil
	}
	if len(records) > last {
		records = records[:last]
	}

	if tty {
		fmt.Fprintln(out, renderReviewHistoryTTY(records))
	} else {
		fmt.Fprintln(out, renderReviewHistoryPlain(records))
	}
	return nil
}

func renderReviewHistoryTTY(records []engine.ReviewRecord) string {
	isDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	ld := lipgloss.LightDark(isDark)
	bold := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Foreground(ld(lipgloss.Color("244"), lipgloss.Color("241")))

	ruleWidth := 72
	if width, _, err := term.GetSize(os.Stdout.Fd()); err == nil && width > 0 && width < ruleWidth {
		ruleWidth = width
	}
	rule := dim.Render(strings.Repeat("─", ruleWidth))

	parts := make([]string, 0, len(records))
	for _, record := range records {
		var b strings.Builder
		b.WriteString(bold.Render(reviewHistoryTitle(record)))
		b.WriteByte('\n')
		b.WriteString(dim.Render(reviewHistoryMeta(record)))
		if record.Body != "" {
			b.WriteString("\n\n")
			b.WriteString(record.Body)
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, "\n\n"+rule+"\n\n")
}

func renderReviewHistoryPlain(records []engine.ReviewRecord) string {
	var b strings.Builder
	for i, record := range records {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "%s\n", reviewHistoryTitle(record))
		fmt.Fprintf(&b, "%s\n", reviewHistoryMeta(record))
		if record.Body != "" {
			b.WriteByte('\n')
			b.WriteString(record.Body)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func isArchiveReviewPeriod(name string) bool {
	return engine.IsArchiveReviewPeriod(name)
}

func reviewHistoryTitle(record engine.ReviewRecord) string {
	return fmt.Sprintf("%s — %s to %s", record.Title, record.Start, record.End)
}

// reviewHistoryMeta is the one dim line under a title: when it was saved, its
// id, and what it revised.
func reviewHistoryMeta(record engine.ReviewRecord) string {
	meta := fmt.Sprintf("Saved %s  id %s", record.CreatedAt, engine.ShortRecordID(record.ID))
	if record.Revises != "" {
		meta += fmt.Sprintf("  revises %s", engine.ShortRecordID(record.Revises))
	}
	return meta
}
