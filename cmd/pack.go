package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// A practice pack is the questions, the anchor, a suggested reminder time, and
// the guidance reviews are given, applied to the journal in one step. The
// built-in packs come with the engine; any other is a *.seneca-pack.json file.
// Applying one goes through the journal settings, so it is committed like
// `seneca settings set` and rewrites no entry.

func init() {
	packCmd.AddCommand(packListCmd)
	packCmd.AddCommand(packShowCmd)
	packCmd.AddCommand(packApplyCmd)
	packCmd.AddCommand(packExportCmd)
	rootCmd.AddCommand(packCmd)
}

var packCmd = &cobra.Command{
	Use:   "pack",
	Short: "Choose the practice the journal keeps",
	Long: "A practice pack sets the journal's questions and anchor, suggests a reminder\n" +
		"time, and gives reviews guidance for that practice. `seneca pack` lists the\n" +
		"built-in packs and marks the one in force.",
	Args: cobra.NoArgs,
	RunE: runPackList,
}

var packListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the built-in packs",
	Args:  cobra.NoArgs,
	RunE:  runPackList,
}

var packShowCmd = &cobra.Command{
	Use:   "show <id or file>",
	Short: "Show a pack's questions, anchor, reminder time, and guidance",
	Long: "Show a built-in pack named by its id, or a *.seneca-pack.json file. Nothing\n" +
		"is written.",
	Args: cobra.ExactArgs(1),
	RunE: runPackShow,
}

var packApplyCmd = &cobra.Command{
	Use:   "apply <id or file>",
	Short: "Make a pack the journal's practice",
	Long: "Replace the journal's questions and anchor with the pack's, and record the\n" +
		"pack as in force from today, on every device. Entries already written are not\n" +
		"touched and read as they always have. A journal without settings of its own\n" +
		"gets them in the same step. Reminders are chosen per device: the pack's\n" +
		"reminder time is printed, not scheduled.",
	Args: cobra.ExactArgs(1),
	RunE: runPackApply,
}

var packExportCmd = &cobra.Command{
	Use:   "export <id>",
	Short: "Print a built-in pack as a file to start a custom pack from",
	Long: "Print a built-in pack as a *.seneca-pack.json document on standard output.\n" +
		"Give the copy an id of its own before applying it.",
	Args: cobra.ExactArgs(1),
	RunE: runPackExport,
}

// resolvePack reads a pack from a built-in id or from a file. An argument that
// names a file is always a file, so a custom pack can never be mistaken for a
// built-in one.
func resolvePack(arg string) (engine.PracticePack, error) {
	looksLikeFile := strings.HasSuffix(arg, ".json") || strings.ContainsRune(arg, os.PathSeparator)
	if !looksLikeFile {
		if _, err := os.Stat(arg); err != nil {
			pack, ok := engine.BuiltinPracticePack(arg)
			if !ok {
				return engine.PracticePack{}, fmt.Errorf("no built-in pack is called %q and no such file exists; `seneca pack list` names the built-in packs", arg)
			}
			return pack, nil
		}
	}
	data, err := os.ReadFile(arg)
	if err != nil {
		return engine.PracticePack{}, fmt.Errorf("reading the pack: %w", err)
	}
	pack, err := engine.ParsePracticePack(data)
	if err != nil {
		return engine.PracticePack{}, fmt.Errorf("%s: %w", arg, err)
	}
	return pack, nil
}

func runPackList(cmd *cobra.Command, args []string) error {
	journal, _, err := settingsJournal(time.Now())
	if err != nil {
		return err
	}
	state, err := journal.EffectiveSettings()
	if err != nil {
		return err
	}
	current, hasCurrent := state.CurrentPractice()

	out := cmd.OutOrStdout()
	for _, pack := range engine.BuiltinPracticePacks() {
		mark := " "
		if hasCurrent && current.Pack.ID == pack.ID {
			mark = "*"
		}
		fmt.Fprintf(out, "%s %-10s %s\n", mark, pack.ID, pack.Name)
		fmt.Fprintf(out, "  %-10s %s\n", "", pack.Description)
	}
	fmt.Fprintln(out)
	printPracticeInForce(out, state)
	fmt.Fprintln(out, "`seneca pack show <id>` shows a pack; `seneca pack apply <id or file>` applies one.")
	return nil
}

func runPackShow(cmd *cobra.Command, args []string) error {
	pack, err := resolvePack(args[0])
	if err != nil {
		return err
	}
	printPack(cmd.OutOrStdout(), pack)
	return nil
}

func runPackExport(cmd *cobra.Command, args []string) error {
	pack, ok := engine.BuiltinPracticePack(args[0])
	if !ok {
		return fmt.Errorf("no built-in pack is called %q; `seneca pack list` names them", args[0])
	}
	encoded, err := engine.EncodePracticePack(pack)
	if err != nil {
		return err
	}
	_, err = cmd.OutOrStdout().Write(encoded)
	return err
}

func runPackApply(cmd *cobra.Command, args []string) error {
	pack, err := resolvePack(args[0])
	if err != nil {
		return err
	}
	journal, _, err := settingsJournal(time.Now())
	if err != nil {
		return err
	}
	if err := ensureDir(journal.ReflectionsPath()); err != nil {
		return err
	}
	syncBefore(journal.ReflectionsPath())
	defer syncAfter(journal.ReflectionsPath())

	state, err := journal.EffectiveSettings()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	outcome, err := journal.ApplyPracticePack(state.Revision, pack)
	report := func() {
		fmt.Fprintf(out, "Practice: %s, from %s, in %s:\n", pack.Name, journal.Today(), outcome.State.Path)
		printSettings(out, outcome.State.Settings)
		if pack.ReminderTime != "" {
			fmt.Fprintf(out, "Suggested reminder: %s. Reminders are set on each device; this one schedules nothing.\n", pack.ReminderTime)
		}
		fmt.Fprintln(out, "Entries already written keep their own questions.")
		commitArchivePaths("seneca: pack apply "+pack.ID, outcome.State.Path)
	}
	if err != nil {
		return engine.OnCommittedWrite(err, report)
	}
	if !outcome.Changed {
		fmt.Fprintf(out, "%s is already the journal's practice; nothing was changed.\n", pack.Name)
		return nil
	}
	report()
	return nil
}

func printPack(out io.Writer, pack engine.PracticePack) {
	fmt.Fprintf(out, "%s (%s, version %d)\n", pack.Name, pack.ID, pack.Version)
	if pack.Description != "" {
		fmt.Fprintln(out, pack.Description)
	}
	fmt.Fprintln(out, "Questions:")
	printSettings(out, pack.Settings())
	if pack.ReminderTime != "" {
		fmt.Fprintf(out, "Suggested reminder: %s\n", pack.ReminderTime)
	}
	if pack.Guidance != "" {
		fmt.Fprintf(out, "Review guidance: %s\n", pack.Guidance)
	}
}

// printPracticeInForce names the pack the journal keeps and since when, and
// says so when the questions were edited after it was applied.
func printPracticeInForce(out io.Writer, state engine.SettingsState) {
	current, ok := state.CurrentPractice()
	if !ok {
		fmt.Fprintln(out, "Practice: no pack applied.")
		return
	}
	fmt.Fprintf(out, "Practice: %s (%s), since %s", current.Pack.Name, current.Pack.ID, current.From)
	if engine.SettingsRevisionOf(current.Pack.Settings()) != state.Revision {
		fmt.Fprint(out, "; the questions were changed after it was applied")
	}
	fmt.Fprintln(out, ".")
}
