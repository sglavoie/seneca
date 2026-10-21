package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var focusDateFlag string

func init() {
	focusCmd.Flags().StringVar(&focusDateFlag, "date", "", "Show the focus in effect on a date (YYYY-MM-DD, today, or yesterday)")
	focusCmd.AddCommand(focusSetCmd)
	focusCmd.AddCommand(focusClearCmd)
	rootCmd.AddCommand(focusCmd)
}

var focusCmd = &cobra.Command{
	Use:   "focus",
	Short: "Show the current focus",
	Long: "Show the focus in effect today, or on a past date with --date.\n" +
		"Focus lives in the review archive beside the reflections file; no AI is involved.",
	Args: cobra.NoArgs,
	RunE: runFocusShow,
}

var focusSetCmd = &cobra.Command{
	Use:   "set <text>",
	Short: "Set the focus by hand",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runFocusSet,
}

var focusClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear the active focus",
	Args:  cobra.NoArgs,
	RunE:  runFocusClear,
}

func runFocusShow(cmd *cobra.Command, args []string) error {
	on := time.Now().Format("2006-01-02")
	if focusDateFlag != "" {
		day, err := engine.ParseDayFlag("date", focusDateFlag, time.Now())
		if err != nil {
			return err
		}
		on = day
	}

	path, err := resolveArchivePath()
	if err != nil {
		return err
	}
	return showFocus(cmd.OutOrStdout(), path, on, time.Now().Format(engine.CalendarDateLayout))
}

func runFocusSet(cmd *cobra.Command, args []string) error {
	path, err := resolveArchivePath()
	if err != nil {
		return err
	}
	if err := ensureDir(path); err != nil {
		return err
	}
	defer syncAround(path)()
	return setFocus(cmd.OutOrStdout(), path, time.Now(), args)
}

func runFocusClear(cmd *cobra.Command, args []string) error {
	path, err := resolveArchivePath()
	if err != nil {
		return err
	}
	defer syncAround(path)()
	return clearFocus(cmd.OutOrStdout(), path, time.Now())
}

// showFocus reports the focus in effect on a date, and how long before today it
// was set. A missing archive is a report, never a reason to create one.
func showFocus(out io.Writer, path, on, today string) error {
	archive, exists, err := engine.LoadReviewArchive(path)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(out, "No focus recorded yet: no review archive at %s\n", path)
		return nil
	}
	warnArchiveDiagnostics(archive)

	state := engine.ResolveFocus(archive, path, on)
	if state.Disputed {
		// Neither change is shown as the focus: which came last is not
		// something the archive can say.
		fmt.Fprintf(out, "Focus disputed on %s.\n%s\nSet the focus again to settle it.\n", on, state.DisputeSummary())
		return nil
	}
	if !state.Active {
		fmt.Fprintf(out, "No focus in effect on %s.\n", on)
		return nil
	}
	set := state.Effective
	if rel := engine.RelativeDay(state.Effective, today); rel != "" {
		set += ", " + rel
	}
	fmt.Fprintf(out, "Focus (set %s, %s):\n%s\n", set, state.Origin, state.Text)
	return nil
}

// setFocus records a manual focus-set event effective today.
func setFocus(out io.Writer, path string, now time.Time, args []string) error {
	journal, err := openArchiveJournal(path, now)
	if err != nil {
		return err
	}

	change, err := journal.SetFocus(strings.Join(args, " "))
	report := func() {
		fmt.Fprintf(out, "Focus set for %s:\n%s\n", change.Effective, change.Text)
		commitArchivePaths(fmt.Sprintf("seneca: focus set %s", change.Effective), path)
	}
	// A committed failure means the event is already in the archive, so it is
	// reported and committed like a success; rerunning would record it twice.
	if err != nil {
		return engine.OnCommittedWrite(err, report)
	}

	report()
	return nil
}

// clearFocus records a clear event. With no focus active it is a read-only
// no-op: no archive is created, nothing is written, and nothing is committed.
func clearFocus(out io.Writer, path string, now time.Time) error {
	journal, err := openArchiveJournal(path, now)
	if err != nil {
		return err
	}

	change, err := journal.ClearFocus()
	report := func() {
		fmt.Fprintf(out, "Focus cleared for %s (was: %s)\n", change.Effective, change.Previous.Text)
		commitArchivePaths(fmt.Sprintf("seneca: focus clear %s", change.Effective), path)
	}
	if err != nil {
		return engine.OnCommittedWrite(err, report)
	}
	if !change.Changed {
		fmt.Fprintf(out, "No focus is active on %s; nothing to clear.\n", change.Effective)
		return nil
	}

	report()
	return nil
}
