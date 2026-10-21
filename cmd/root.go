package cmd

import (
	"bufio"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var dateFlag string
var blankFlag bool

var rootCmd = &cobra.Command{
	Use:   "seneca",
	Short: "Evening Stoic reflection",
	RunE:  runReflection,
	// main prints the error once; cobra printing it too doubles every message.
	SilenceErrors: true,
	// Flags parsed by now, so a failure is about the journal, not about how the
	// command was typed: answer it without a screenful of usage.
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cmd.SilenceUsage = true
	},
}

// reflectionForm is the terminal seam of the daily command: everything before
// it prepares the form and everything after it saves what the form returned.
// Tests replace it to drive the whole command without a terminal.
var reflectionForm = runReflectionTUI

func init() {
	rootCmd.Flags().StringVar(&dateFlag, "date", "", "Override entry date (YYYY-MM-DD, today, or yesterday)")
	rootCmd.Flags().BoolVar(&blankFlag, "blank", false, "Hide all previous context: focus, prior intention, and follow-through")
}

func runReflection(cmd *cobra.Command, args []string) error {
	now := time.Now()
	today := now.Format("2006-01-02")
	if dateFlag != "" {
		day, err := engine.ParseDayFlag("date", dateFlag, now)
		if err != nil {
			return err
		}
		// A reflection looks back on a day; one dated ahead is a typo that
		// would otherwise surface tomorrow as an entry already written.
		if day > today {
			return fmt.Errorf("invalid --date %q: %s is in the future; a reflection can only be written for today or earlier", dateFlag, day)
		}
		today = day
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}
	settings, err := loadReflectionSettings(home)
	if err != nil {
		return err
	}

	path, err := resolveFilePathForHome(home, os.Getenv)
	if err != nil {
		return err
	}

	if err := ensureDir(path); err != nil {
		return err
	}

	// On a synced journal, what other devices already delivered comes in before
	// the form is prepared, and what is saved here is published on the way out.
	syncBefore(path)
	defer syncAfter(path)

	journal, err := openJournal(path, settings, now)
	if err != nil {
		return err
	}

	// Fail closed before the TUI: rejecting the file after the author has
	// written their reflection would throw the writing away.
	draft, err := journal.PrepareReflection(today, blankFlag)
	if err != nil {
		return err
	}

	// Writing a failed save could not store is put back into the form, so the
	// author picks up where the save left them. The questions are the draft's:
	// once the journal has settings of its own they, not config.toml, decide.
	form := reflectionTUIConfig{
		questions: draft.Questions,
		date:      today,
		initial:   draft.Initial,
		context:   draft.Context,
	}
	keptPath, restored := restoreKeptDraft(os.Stdout, journal, draft, &form)

	// The form runs with no lock held: the journal is re-read, and the day's
	// entry compared with the one shown here, only when the author saves.
	result, err := reflectionForm(form)
	if err != nil {
		return err
	}
	if !result.Saved {
		fmt.Println("Reflection cancelled.")
		if restored {
			fmt.Printf("The unsaved writing is still kept in %s; delete that file to discard it.\n", keptPath)
		}
		return nil
	}
	if result.Empty() {
		fmt.Println("Nothing to record.")
		return nil
	}

	// A committed failure means the day is already saved, so it is announced and
	// committed like a success; rerunning would re-edit an entry already on disk.
	saved, err := journal.SaveReflection(draft, result, dateFlag != "")
	if err != nil {
		if !engine.WriteCommitted(err) {
			keepDraft(os.Stderr, journal, draft, result, err)
		}
		return engine.OnCommittedWrite(err, func() {
			discardKeptDraft(journal, today)
			_ = commitReflection(path, today, saved.IsEdit)
		})
	}
	discardKeptDraft(journal, today)

	if err := commitReflection(path, today, saved.IsEdit); err != nil {
		return err
	}
	if !blankFlag {
		printReviewDue(os.Stdout, saved.Entries, journal.ArchivePath(), journal.Today())
	}
	return nil
}

// restoreKeptDraft fills the form from the writing a failed save kept for this
// day, and says so. A draft that cannot be read is reported and left alone: it
// must never stand between the author and the form.
func restoreKeptDraft(out io.Writer, journal *engine.Journal, draft engine.ReflectionDraft, form *reflectionTUIConfig) (string, bool) {
	path := engine.DraftPathFor(journal.ReflectionsPath(), draft.Date)
	kept, found, err := journal.RetainedDraft(draft.Date)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v; starting from the journal instead\n", err)
		return path, false
	}
	if !found {
		return path, false
	}

	form.initial = kept.AnswersFor(form.questions)
	if form.context.ShowFollowThrough {
		form.context.FollowInitial = kept.FollowThrough
	}
	fmt.Fprintf(out, "Restored the unsaved writing for %s kept on %s.\n", draft.Date, keptDraftTime(kept.RetainedAt))
	if !equalQuestions(kept.Questions, form.questions) {
		// The answers were written under other questions. They are lined up by
		// question text, and what no longer has a question is shown here,
		// because saving discards the kept copy.
		fmt.Fprintln(out, "The journal's questions changed since it was written; review each answer against its question before saving.")
		for _, unasked := range kept.Unasked(form.questions) {
			fmt.Fprintf(out, "No longer asked, so not in the form:\n### %s\n%s\n", unasked.Question, unasked.Answer)
		}
	}
	if draft.IsEdit && kept.Base != draft.Base {
		// The author is about to see their own words where the journal holds
		// someone else's save; say what saving will do before they decide.
		fmt.Fprintf(out, "The journal already holds a different entry for %s. Saving replaces it; the replaced entry stays in %s.\n",
			draft.Date, journal.ReflectionsPath()+engine.BackupSuffix)
	}
	return path, true
}

func equalQuestions(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func keptDraftTime(stamp string) string {
	if at, err := time.Parse(time.RFC3339, stamp); err == nil {
		return at.Format("2006-01-02 at 15:04")
	}
	return stamp
}

// keepDraft stores the writing a save could not, so the failure costs nothing
// but a rerun. When even that fails the writing is printed: the terminal is the
// last place left to keep it.
func keepDraft(out io.Writer, journal *engine.Journal, draft engine.ReflectionDraft, result engine.ReflectionResult, cause error) {
	path, err := journal.RetainDraft(draft, result, cause.Error())
	if err == nil {
		fmt.Fprintf(out, "Your writing was not saved, but it is kept in %s.\nRun `seneca --date %s` to pick it up again.\n", path, draft.Date)
		return
	}
	fmt.Fprintf(out, "Your writing was not saved and could not be kept (%v). Here it is:\n\n", err)
	if strings.TrimSpace(result.FollowThrough) != "" {
		fmt.Fprintf(out, "### Follow-through\n%s\n\n", result.FollowThrough)
	}
	for i, question := range draft.Questions {
		if i < len(result.Answers) && result.Answers[i] != "" {
			fmt.Fprintf(out, "### %s\n%s\n\n", question, result.Answers[i])
		}
	}
}

// discardKeptDraft drops the kept writing once the day is in the journal.
func discardKeptDraft(journal *engine.Journal, date string) {
	if err := journal.DiscardDraft(date); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
}

// printReviewDue mentions writing the newest review has not seen, after the
// entry is safely saved. It is a courtesy: an unreadable or damaged archive
// prints nothing here, because the save has already succeeded and doctor is
// where archive problems are reported.
func printReviewDue(out io.Writer, entries []engine.ReflectionEntry, archivePath, today string) {
	archive, exists, err := engine.LoadReviewArchive(archivePath)
	if err != nil || !exists || len(archive.Diagnostics) > 0 {
		return
	}
	if note, due := engine.ReviewDueNote(entries, archive, today); due {
		fmt.Fprintln(out, note+".")
	}
}

func commitReflection(path, today string, isEdit bool) error {
	commitMsg := fmt.Sprintf("seneca: reflection %s", today)
	if isEdit {
		commitMsg = fmt.Sprintf("seneca: edit reflection %s", today)
	}
	fmt.Printf("Reflection saved to %s\n", path)
	gitCommit(path, commitMsg)
	return nil
}

func Execute() error {
	return rootCmd.Execute()
}

func resolveFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	return resolveFilePathForHome(home, os.Getenv)
}

func resolveFilePathForHome(home string, getenv func(string) string) (string, error) {
	if path := getenv("SENECA_FILE"); path != "" {
		return path, nil
	}
	configuredPath, err := loadConfiguredReflectionsPath(home)
	if err != nil {
		return "", err
	}
	if configuredPath != "" {
		return configuredPath, nil
	}
	return defaultReflectionsPath(home), nil
}

func defaultReflectionsPath(home string) string {
	return filepath.Join(home, "Documents", "Seneca", "reflections.md")
}

func ensureDir(filePath string) error {
	dir := filepath.Dir(filePath)
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("path %s exists but is not a directory", dir)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("checking directory: %w", err)
	}
	fmt.Printf("Directory %s does not exist. Create it? [y/N] ", dir)
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading input: %w", err)
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "y" && answer != "yes" {
		return fmt.Errorf("aborted: directory does not exist")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	return nil
}
