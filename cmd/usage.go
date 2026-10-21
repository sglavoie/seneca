package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"github.com/sglavoie/seneca/review/usage"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var usageJSON bool

func init() {
	usageCmd.Flags().BoolVar(&usageJSON, "json", false, "Print the summary as JSON")
	rootCmd.AddCommand(usageCmd)
}

var usageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Summarize what reviews have cost, from the usage ledger",
	Long: "Summarize the usage ledger `seneca review` keeps: per month and per review\n" +
		"kind, how many attempts were made, their total cost, and the p50 and p90 of\n" +
		"cost and of thinking tokens. Percentiles cover the attempts that reported\n" +
		"the value.\n\n" +
		"This only reads the ledger. It never creates a file and never calls a model.",
	Args: cobra.NoArgs,
	RunE: runUsage,
}

// usageSummary is what `seneca usage --json` prints.
type usageSummary struct {
	Groups       []usage.Group `json:"groups"`
	SkippedLines int           `json:"skipped_lines"`
}

func runUsage(cmd *cobra.Command, args []string) error {
	path, err := resolveFilePath()
	if err != nil {
		return err
	}
	summary, err := summarizeUsageLedger(usageLedgerPathFor(path))
	if err != nil {
		return err
	}
	if usageJSON {
		return writeUsageJSON(cmd.OutOrStdout(), summary)
	}
	fmt.Fprint(cmd.OutOrStdout(), renderUsage(summary))
	return nil
}

// summarizeUsageLedger reads the ledger at path. A missing ledger is an empty
// summary, never a reason to create one.
func summarizeUsageLedger(path string) (usageSummary, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return usageSummary{Groups: []usage.Group{}}, nil
	}
	if err != nil {
		return usageSummary{}, fmt.Errorf("reading the usage ledger: %w", err)
	}
	defer file.Close()
	records, skipped, err := usage.ReadLedger(file)
	if err != nil {
		return usageSummary{}, err
	}
	return usageSummary{Groups: usage.Summarize(records), SkippedLines: skipped}, nil
}

func writeUsageJSON(out io.Writer, summary usageSummary) error {
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", data)
	return err
}

// renderUsage lays the summary out as one row per month and kind.
func renderUsage(summary usageSummary) string {
	var b strings.Builder
	if len(summary.Groups) == 0 {
		b.WriteString("No review attempts recorded yet: `seneca review` adds one line to the usage ledger for each.\n")
	} else {
		fmt.Fprintf(&b, "%-7s  %-9s  %8s  %10s  %9s  %9s  %12s  %12s\n",
			"Month", "Kind", "Attempts", "Total cost", "Cost p50", "Cost p90", "Thinking p50", "Thinking p90")
		unpriced := 0
		for _, g := range summary.Groups {
			total := "—"
			if g.CostKnown > 0 {
				total = formatUSD(g.TotalCostUSD)
			}
			fmt.Fprintf(&b, "%-7s  %-9s  %8d  %10s  %9s  %9s  %12s  %12s\n",
				g.Month, g.Kind, g.Count, total,
				optionalUSD(g.CostP50USD), optionalUSD(g.CostP90USD),
				optionalTokens(g.ThinkingP50), optionalTokens(g.ThinkingP90))
			unpriced += g.Count - g.CostKnown
		}
		if unpriced > 0 {
			fmt.Fprintf(&b, "\nCounted but not priced: %s with no known cost.\n",
				engine.Plural(unpriced, "attempt", "attempts"))
		}
	}
	if summary.SkippedLines > 0 {
		fmt.Fprintf(&b, "\nSkipped: %s of the ledger that could not be read.\n",
			engine.Plural(summary.SkippedLines, "line", "lines"))
	}
	return b.String()
}

func formatUSD(v float64) string { return fmt.Sprintf("$%.4f", v) }

func optionalUSD(v *float64) string {
	if v == nil {
		return "—"
	}
	return formatUSD(*v)
}

func optionalTokens(v *int64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *v)
}
