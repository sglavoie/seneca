package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var deleteSource bool

func init() {
	mergeCmd.Flags().BoolVar(&deleteSource, "delete", false, "Remove source file after successful merge")
	rootCmd.AddCommand(mergeCmd)
}

var mergeCmd = &cobra.Command{
	Use:   "merge <source-file>",
	Short: "Merge entries from a source file into the main reflections file",
	Args:  cobra.ExactArgs(1),
	RunE:  runMerge,
}

func runMerge(cmd *cobra.Command, args []string) error {
	srcPath := args[0]
	dstPath, err := resolveFilePath()
	if err != nil {
		return err
	}
	same, err := engine.SameFilePath(srcPath, dstPath)
	if err != nil {
		return err
	}
	if same {
		return engine.ErrMergeSameFile
	}

	if err := ensureDir(dstPath); err != nil {
		return err
	}

	syncBefore(dstPath)
	defer syncAfter(dstPath)

	journal, err := openJournal(dstPath, engine.ReflectionSettings{}, time.Now())
	if err != nil {
		return err
	}
	outcome, err := journal.Merge(srcPath)
	if err != nil {
		// A committed failure leaves the merged content in the destination, so
		// say so and record it in git — but keep the source, because deleting it
		// while the destination's replacement is unflushed could lose both.
		return engine.OnCommittedWrite(err, func() {
			fmt.Printf("Merged %d entries from %s into %s; %s was kept\n",
				outcome.Merged, srcPath, dstPath, srcPath)
			gitCommitPaths(fmt.Sprintf("seneca: merge %d entries", outcome.Merged), dstPath)
		})
	}

	// Only now that the destination holds the merged content is it safe to
	// drop the source.
	if deleteSource {
		if err := os.Remove(srcPath); err != nil {
			return fmt.Errorf("removing source file: %w", err)
		}
	}

	fmt.Printf("Merged %d entries from %s into %s\n", outcome.Merged, srcPath, dstPath)
	commitPaths := []string{dstPath}
	if deleteSource {
		commitPaths = append(commitPaths, srcPath)
	}
	gitCommitPaths(fmt.Sprintf("seneca: merge %d entries", outcome.Merged), commitPaths...)
	return nil
}
