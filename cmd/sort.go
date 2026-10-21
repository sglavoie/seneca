package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(sortCmd)
}

var sortCmd = &cobra.Command{
	Use:   "sort",
	Short: "Sort entries chronologically by date",
	Args:  cobra.NoArgs,
	RunE:  runSort,
}

func runSort(cmd *cobra.Command, args []string) error {
	path, err := resolveFilePath()
	if err != nil {
		return err
	}

	syncBefore(path)
	defer syncAfter(path)

	journal, err := openJournal(path, engine.ReflectionSettings{}, time.Now())
	if err != nil {
		return err
	}

	outcome, err := journal.Sort()
	report := func() {
		fmt.Printf("Sorted %d entries in %s\n", outcome.Count, path)
		gitCommit(path, fmt.Sprintf("seneca: sort %d entries", outcome.Count))
	}
	if err != nil {
		return engine.OnCommittedWrite(err, report)
	}
	if outcome.Count == 0 {
		fmt.Println("No entries to sort.")
		return nil
	}
	// A no-op sort must not churn the backup, the file, or the git history.
	if !outcome.Changed {
		fmt.Printf("Already sorted: %d entries in %s\n", outcome.Count, path)
		return nil
	}

	report()
	return nil
}
