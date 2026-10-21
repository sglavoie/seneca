package cmd

import (
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(testNotifyCmd)
}

var testNotifyCmd = &cobra.Command{
	Use:   "test-notification",
	Short: "Test the daily reflection reminder notification",
	Args:  cobra.NoArgs,
	RunE:  runTestNotify,
}

func runTestNotify(cmd *cobra.Command, args []string) error {
	filePath, err := resolveFilePath()
	if err != nil {
		return err
	}

	today := time.Now().Format("2006-01-02")
	return runDailyReminder(filePath, today, showMissingReflectionAlert, cmd.OutOrStdout())
}

func runDailyReminder(filePath, today string, notify func(string) error, output io.Writer) error {
	recorded, err := reflectionRecordedOnDate(filePath, today)
	if err != nil {
		return err
	}
	if recorded {
		fmt.Fprintf(output, "Reflection already recorded for %s — no notification needed.\n", today)
		return nil
	}

	if err := notify(today); err != nil {
		return err
	}

	fmt.Fprintf(output, "Notification sent for %s.\n", today)
	return nil
}

func reflectionRecordedOnDate(filePath, date string) (bool, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("reading reflections file: %w", err)
	}

	// The iPhone plans its reminders by the same rule.
	return engine.ReminderComplete(engine.ParseReflectionDocument(string(data)).Entries, date), nil
}

func showMissingReflectionAlert(today string) error {
	osaScriptPath, err := exec.LookPath("osascript")
	if err != nil {
		return fmt.Errorf("finding osascript: %w", err)
	}

	script := fmt.Sprintf(
		`display alert "Seneca — Evening reflection missing" message "No reflection recorded for %s." as critical giving up after 3600`,
		today,
	)
	if output, err := exec.Command(osaScriptPath, "-e", script).CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return fmt.Errorf("showing reminder: %w: %s", err, detail)
		}
		return fmt.Errorf("showing reminder: %w", err)
	}
	return nil
}
