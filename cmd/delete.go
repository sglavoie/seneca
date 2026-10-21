package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

var (
	deleteDateFlag string
	deleteYesFlag  bool
)

func init() {
	deleteCmd.Flags().StringVar(&deleteDateFlag, "date", "", "Day to delete (YYYY-MM-DD, today, or yesterday)")
	deleteCmd.Flags().BoolVarP(&deleteYesFlag, "yes", "y", false, "Delete without asking first")
	if err := deleteCmd.MarkFlagRequired("date"); err != nil {
		panic(fmt.Sprintf("marking --date required: %v", err))
	}
	rootCmd.AddCommand(deleteCmd)
}

var deleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete one day's reflection",
	Long: "Delete the reflection saved for --date. The entry is shown first and the\n" +
		"deletion asks for confirmation unless --yes is given. The previous file is\n" +
		"kept as a .bak beside the journal, and a synced journal passes the deletion\n" +
		"on to its other devices. Drafts and recordings for the day are not touched.",
	Args: cobra.NoArgs,
	RunE: runDelete,
}

func runDelete(cmd *cobra.Command, args []string) error {
	now := time.Now()
	date, err := engine.ParseDayFlag("date", deleteDateFlag, now)
	if err != nil {
		return err
	}
	path, err := resolveFilePath()
	if err != nil {
		return err
	}

	syncBefore(path)
	defer syncAfter(path)

	journal, err := openJournal(path, engine.ReflectionSettings{}, now)
	if err != nil {
		return err
	}
	confirm := askConfirmation(os.Stdin, cmd.OutOrStdout(), term.IsTerminal(os.Stdin.Fd()))
	if deleteYesFlag {
		confirm = func(string) (bool, error) { return true, nil }
	}
	return deleteReflection(cmd.OutOrStdout(), journal, path, date, confirm)
}

// deleteReflection removes date's entry once the author has seen it and said
// yes. The deletion is conditional on the bytes that were shown, so an entry
// changed in the meantime is refused rather than deleted unseen.
func deleteReflection(out io.Writer, journal *engine.Journal, path, date string, confirm func(prompt string) (bool, error)) error {
	entries, err := journal.Entries()
	if err != nil {
		return err
	}
	index, found := engine.FindEntry(entries, date)
	if !found {
		return fmt.Errorf("no reflection saved for %s; nothing was written", date)
	}
	entry := entries[index]

	fmt.Fprintf(out, "%s\n\n", strings.TrimRight(entry.Raw, "\n"))
	ok, err := confirm(fmt.Sprintf("Delete the reflection for %s? [y/N] ", date))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(out, "Kept: nothing was deleted.")
		return nil
	}

	err = journal.DeleteReflection(date, engine.RevisionOf([]byte(entry.Raw)))
	report := func() {
		fmt.Fprintf(out, "Deleted the reflection for %s. The previous file is kept as %s%s.\n", date, path, engine.BackupSuffix)
		gitCommit(path, fmt.Sprintf("seneca: delete reflection %s", date))
	}
	if err != nil {
		// A committed failure means the entry is already gone, so it is reported
		// and committed like a success; rerunning would find nothing to delete.
		return engine.OnCommittedWrite(err, report)
	}
	report()
	return nil
}

// errDeleteNeedsYes refuses a deletion nobody can confirm: without a terminal
// there is no one to answer the question, and silence is not a yes.
var errDeleteNeedsYes = errors.New("not a terminal: rerun with --yes to delete without asking; nothing was written")

// askConfirmation reads a y/N answer from in. Anything but y or yes keeps the
// entry.
func askConfirmation(in io.Reader, out io.Writer, interactive bool) func(string) (bool, error) {
	return func(prompt string) (bool, error) {
		if !interactive {
			return false, errDeleteNeedsYes
		}
		fmt.Fprint(out, prompt)
		answer, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && answer == "" {
			return false, nil
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
			return true, nil
		default:
			return false, nil
		}
	}
}
