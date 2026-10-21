package cmd

import (
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// Journal settings are the questions and the anchor selection kept with the
// journal instead of with the machine, so every device asks the same thing.
// Until `seneca settings init` records them, config.toml decides, as it always
// has; afterwards the journal does and the config.toml keys are reported as
// superseded, never rewritten.

var (
	settingsQuestionsFlag []string
	settingsAnchorFlag    int
)

func init() {
	settingsSetCmd.Flags().StringArrayVar(&settingsQuestionsFlag, "question", nil, "A question, in order; repeat the flag to give the whole new list")
	settingsSetCmd.Flags().IntVar(&settingsAnchorFlag, "anchor", 0, "Which question carries the intention forward (1-based; 0 disables it)")
	settingsCmd.AddCommand(settingsInitCmd)
	settingsCmd.AddCommand(settingsSetCmd)
	rootCmd.AddCommand(settingsCmd)
}

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Show the questions and anchor the journal uses",
	Long: "Show the questions and the anchor selection in force, and whether they come\n" +
		"from the journal's own settings or from config.toml. Nothing is written.",
	Args: cobra.NoArgs,
	RunE: runSettingsShow,
}

var settingsInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Record the current questions and anchor in the journal",
	Long: "Give the journal settings of its own, seeded from the questions and anchor\n" +
		"config.toml (or the built-in defaults) supplies now. From then on the journal's\n" +
		"settings decide on every device; config.toml is left as it is. A journal that\n" +
		"already has settings is not changed.",
	Args: cobra.NoArgs,
	RunE: runSettingsInit,
}

var settingsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Change the journal's questions, its anchor, or both",
	Long: "Replace the journal's questions (repeat --question for each, in order), its\n" +
		"anchor selection (--anchor), or both. The two are validated together. New\n" +
		"questions without --anchor keep the anchor on the same question; when that\n" +
		"question is gone, --anchor must be given. Entries already written are not\n" +
		"touched.",
	Args: cobra.NoArgs,
	RunE: runSettingsSet,
}

// settingsJournal opens the journal the way the daily command does: the path
// from the environment, the device's configuration as the opened settings.
func settingsJournal(now time.Time) (*engine.Journal, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", fmt.Errorf("getting home directory: %w", err)
	}
	settings, err := loadReflectionSettings(home)
	if err != nil {
		return nil, "", err
	}
	path, err := resolveFilePathForHome(home, os.Getenv)
	if err != nil {
		return nil, "", err
	}
	journal, err := openJournal(path, settings, now)
	return journal, home, err
}

func runSettingsShow(cmd *cobra.Command, args []string) error {
	journal, home, err := settingsJournal(time.Now())
	if err != nil {
		return err
	}
	state, err := journal.EffectiveSettings()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if !state.Initialized {
		fmt.Fprintf(out, "Journal settings: not initialized (%s does not exist).\n", state.Path)
		fmt.Fprintf(out, "In force, from %s:\n", settingsSeedSource(home))
		printSettings(out, state.Settings)
		fmt.Fprintln(out, "Run `seneca settings init` to record them in the journal.")
		return nil
	}
	fmt.Fprintf(out, "Journal settings: %s (version %d)\n", state.Path, state.Version)
	printSettings(out, state.Settings)
	if len(state.Practice) > 0 {
		printPracticeInForce(out, state)
	}
	fmt.Fprintf(out, "Revision: %s\n", state.Revision)
	printSuperseded(out, home)
	return nil
}

func runSettingsInit(cmd *cobra.Command, args []string) error {
	journal, home, err := settingsJournal(time.Now())
	if err != nil {
		return err
	}
	if err := ensureDir(journal.ReflectionsPath()); err != nil {
		return err
	}

	syncBefore(journal.ReflectionsPath())
	defer syncAfter(journal.ReflectionsPath())

	out := cmd.OutOrStdout()
	outcome, err := journal.InitializeSettings()
	report := func() {
		fmt.Fprintf(out, "Journal settings initialized in %s from %s:\n", outcome.State.Path, settingsSeedSource(home))
		printSettings(out, outcome.State.Settings)
		printSuperseded(out, home)
		commitArchivePaths("seneca: settings init", outcome.State.Path)
	}
	if err != nil {
		return engine.OnCommittedWrite(err, report)
	}
	if !outcome.Changed {
		fmt.Fprintf(out, "Journal settings already exist in %s; nothing was changed.\n", outcome.State.Path)
		printSettings(out, outcome.State.Settings)
		printSuperseded(out, home)
		return nil
	}
	report()
	return nil
}

func runSettingsSet(cmd *cobra.Command, args []string) error {
	var change engine.SettingsChange
	if cmd.Flags().Changed("question") {
		change.Questions = settingsQuestionsFlag
	}
	if cmd.Flags().Changed("anchor") {
		anchor := settingsAnchorFlag
		change.AnchorQuestionIndex = &anchor
	}
	if change.Questions == nil && change.AnchorQuestionIndex == nil {
		return errors.New("nothing to change: pass --question for each question, --anchor, or both")
	}

	journal, _, err := settingsJournal(time.Now())
	if err != nil {
		return err
	}
	syncBefore(journal.ReflectionsPath())
	defer syncAfter(journal.ReflectionsPath())

	state, err := journal.EffectiveSettings()
	if err != nil {
		return err
	}
	if !state.Initialized {
		return fmt.Errorf("%w with `seneca settings init`, or edit config.toml (nothing was written)", engine.ErrSettingsNotInitialized)
	}

	out := cmd.OutOrStdout()
	outcome, err := journal.UpdateSettings(state.Revision, change)
	report := func() {
		fmt.Fprintf(out, "Journal settings updated in %s:\n", outcome.State.Path)
		printSettings(out, outcome.State.Settings)
		commitArchivePaths("seneca: settings update", outcome.State.Path)
	}
	if err != nil {
		return engine.OnCommittedWrite(err, report)
	}
	if !outcome.Changed {
		fmt.Fprintln(out, "The journal settings already say that; nothing was changed.")
		return nil
	}
	report()
	return nil
}

// settingsSeedSource names what supplies the questions and anchor of a journal
// without settings of its own.
func settingsSeedSource(home string) string {
	if len(supersededConfigKeys(home)) > 0 {
		return configFilePath(home)
	}
	return "the built-in defaults"
}

func printSettings(out io.Writer, settings engine.ReflectionSettings) {
	for i, question := range settings.Questions {
		fmt.Fprintf(out, "  %d. %s\n", i+1, question)
	}
	if idx := settings.AnchorQuestionIndex; idx > 0 && idx <= len(settings.Questions) {
		fmt.Fprintf(out, "Anchor: question %d\n", idx)
		return
	}
	fmt.Fprintln(out, "Anchor: disabled")
}

// printSuperseded names the config.toml keys the journal's settings replaced,
// so the author is never left wondering why an edit there changes nothing.
func printSuperseded(out io.Writer, home string) {
	for _, key := range supersededConfigKeys(home) {
		fmt.Fprintf(out, "Superseded: %s in %s is no longer used for this journal.\n", key, configFilePath(home))
	}
}
