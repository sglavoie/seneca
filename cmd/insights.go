package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	insightsDays int
	insightsFrom string
	insightsTo   string
	insightsAll  bool
)

func init() {
	insightsCmd.Flags().IntVar(&insightsDays, "days", engine.InsightsDays, "Report on the last N days, today included")
	insightsCmd.Flags().StringVar(&insightsFrom, "from", "", "Report on intentions written on or after a date (YYYY-MM-DD, today, or yesterday)")
	insightsCmd.Flags().StringVar(&insightsTo, "to", "", "Report on intentions written on or before a date (YYYY-MM-DD, today, or yesterday)")
	insightsCmd.Flags().BoolVar(&insightsAll, "all", false, "Report on the whole journal")
	rootCmd.AddCommand(insightsCmd)
}

var insightsCmd = &cobra.Command{
	Use:   "insights",
	Short: "Report what became of your intentions and how long each focus stood",
	Long: "Count the intentions written in a window and what the days after them did:\n" +
		"answered, carried with a blank response, not carried, or still open. List the\n" +
		"intentions written in the same words more than once, the words that keep\n" +
		"returning, and how long each focus stood and what replaced it.\n\n" +
		"Nothing is inferred and nothing is scored: every line can be checked with\n" +
		"`seneca show --date` and `seneca focus history`. This never contacts an AI\n" +
		"harness and never writes.",
	Args: cobra.NoArgs,
	RunE: runInsights,
}

func runInsights(cmd *cobra.Command, args []string) error {
	now := time.Now()
	rangeSet := insightsFrom != "" || insightsTo != ""
	daysSet := cmd.Flags().Changed("days")
	set := 0
	for _, on := range []bool{daysSet, rangeSet, insightsAll} {
		if on {
			set++
		}
	}
	if set > 1 {
		return fmt.Errorf("--days, --from/--to, and --all are mutually exclusive")
	}
	if insightsDays <= 0 {
		return fmt.Errorf("invalid --days %d: expected a positive number", insightsDays)
	}

	journal, _, err := settingsJournal(now)
	if err != nil {
		return err
	}
	today := journal.Today()
	var from, to, scope string
	switch {
	case insightsAll:
		scope = "the whole journal"
	case rangeSet:
		if insightsFrom != "" {
			if from, err = engine.ParseDayFlag("from", insightsFrom, now); err != nil {
				return err
			}
		}
		if insightsTo != "" {
			if to, err = engine.ParseDayFlag("to", insightsTo, now); err != nil {
				return err
			}
		}
		if from != "" && to != "" && from > to {
			return fmt.Errorf("invalid range: --from %s is after --to %s", from, to)
		}
	default:
		from, to = engine.InsightsWindow(today, insightsDays)
		scope = "the last " + engine.Plural(insightsDays, "day", "days")
	}

	report, err := journal.Insights(from, to)
	if err != nil {
		return err
	}
	printInsights(cmd.OutOrStdout(), report, scope)
	return nil
}

func printInsights(out io.Writer, report engine.Insights, scope string) {
	title := "Follow-through insights"
	switch {
	case report.From == "" && len(report.Intentions) == 0:
		title += ": nothing written yet"
	default:
		title += fmt.Sprintf(", %s to %s", orStart(report.From), report.To)
		if scope != "" {
			title += " (" + scope + ")"
		}
	}
	fmt.Fprintln(out, title)

	fmt.Fprintln(out)
	fmt.Fprintf(out, "Intentions: %d\n", len(report.Intentions))
	if len(report.Intentions) > 0 {
		fmt.Fprintf(out, "  answered     %3d  a later day followed through\n", report.Answered)
		fmt.Fprintf(out, "  unanswered   %3d  carried, with the response left blank\n", report.Unanswered)
		fmt.Fprintf(out, "  not carried  %3d  days were written after it, none carried it\n", report.NotCarried)
		fmt.Fprintf(out, "  open         %3d  nothing written since\n", report.Open)
		if report.Untracked > 0 {
			since := "the journal has never carried one"
			if report.Tracked != "" {
				since = "the journal carried none before " + report.Tracked
			}
			fmt.Fprintf(out, "  untracked    %3d  %s\n", report.Untracked, since)
		}
		for _, intention := range report.Intentions {
			if intention.Status == engine.IntentionAnswered || intention.Status == engine.IntentionUntracked {
				continue
			}
			fmt.Fprintf(out, "  %s  %-11s  %s\n", intention.Date, intention.Status, oneLine(intention.Text))
		}
	}

	if len(report.Repeated) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Written in the same words more than once:")
		for _, repeated := range report.Repeated {
			fmt.Fprintf(out, "  %d×  %s\n       %s\n", len(repeated.Dates), oneLine(repeated.Text), strings.Join(repeated.Dates, ", "))
		}
	}
	if len(report.RecurringWords) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "Words in the intentions of %d days or more:\n", engine.RecurringWordDays)
		for _, word := range report.RecurringWords {
			fmt.Fprintf(out, "  %-16s %-8s  %s\n", word.Word, engine.Plural(len(word.Dates), "day", "days"), latestDates(word.Dates))
		}
	}

	fmt.Fprintln(out)
	switch {
	case report.FocusProblem != "":
		fmt.Fprintf(out, "Focus: not shown. %s\n", report.FocusProblem)
	case len(report.Focus) == 0:
		fmt.Fprintln(out, "Focus: none set in this period.")
	default:
		fmt.Fprintln(out, "Focus:")
		for _, span := range report.Focus {
			fmt.Fprintf(out, "  %s\n", focusSpanLine(span))
		}
	}
}

// focusSpanLine is one span: its dates, how long it stood, what it was, and
// what replaced it.
func focusSpanLine(span engine.FocusSpan) string {
	until := span.Until
	if span.Current() {
		until = "current"
	}
	what := "no focus"
	if span.Event == engine.FocusEventSet {
		what = oneLine(span.Text)
	}
	line := fmt.Sprintf("%s → %-10s  %-8s  %s", span.From, until, engine.Plural(span.Days, "day", "days"), what)
	switch {
	case span.Current():
	case span.ReplacedBy == engine.FocusEventClear:
		line += "  (then cleared)"
	default:
		line += fmt.Sprintf("  (then: %s)", oneLine(span.ReplacedByText))
	}
	return line
}

// latestDates lists the last few of a word's dates, which `seneca show
// --search` finds in full.
func latestDates(dates []string) string {
	const shown = 5
	if len(dates) <= shown {
		return strings.Join(dates, ", ")
	}
	return "…, " + strings.Join(dates[len(dates)-shown:], ", ")
}

func orStart(date string) string {
	if date == "" {
		return "the first entry"
	}
	return date
}

// oneLine keeps a multi-line answer to its first line, marked as cut.
func oneLine(text string) string {
	first, rest, cut := strings.Cut(strings.TrimSpace(text), "\n")
	if cut && strings.TrimSpace(rest) != "" {
		return first + " …"
	}
	return first
}
