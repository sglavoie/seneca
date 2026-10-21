package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReflectionRecordedOnDateFindsSavedEntry(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "reflections.md")
	content := "## 2026-05-25\n\nEarlier.\n\n---\n\n## 2026-05-26\n\nToday.\n\n---\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing reflections: %v", err)
	}

	recorded, err := reflectionRecordedOnDate(path, "2026-05-26")
	if err != nil {
		t.Fatalf("reflectionRecordedOnDate returned error: %v", err)
	}
	if !recorded {
		t.Fatal("expected saved entry to be detected")
	}
}

func TestReflectionRecordedOnDateIgnoresOtherDatesAndMalformedContent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "reflections.md")
	content := "not an entry\n\n---\n\n## 2026-05-25\n\nEarlier.\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing reflections: %v", err)
	}

	recorded, err := reflectionRecordedOnDate(path, "2026-05-26")
	if err != nil {
		t.Fatalf("reflectionRecordedOnDate returned error: %v", err)
	}
	if recorded {
		t.Fatal("expected unrelated content not to count as today's entry")
	}
}

func TestReflectionRecordedOnDateTreatsMissingFileAsNotRecorded(t *testing.T) {
	t.Parallel()

	recorded, err := reflectionRecordedOnDate(filepath.Join(t.TempDir(), "missing.md"), "2026-05-26")
	if err != nil {
		t.Fatalf("reflectionRecordedOnDate returned error: %v", err)
	}
	if recorded {
		t.Fatal("expected a missing file not to count as a recorded entry")
	}
}

func TestReflectionRecordedOnDateReturnsReadErrors(t *testing.T) {
	t.Parallel()

	_, err := reflectionRecordedOnDate(t.TempDir(), "2026-05-26")
	if err == nil || !strings.Contains(err.Error(), "reading reflections file:") {
		t.Fatalf("expected wrapped read error, got %v", err)
	}
}

func TestRunDailyReminderSuppressesNotificationForSavedEntry(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "reflections.md")
	if err := os.WriteFile(path, []byte("## 2026-05-26\n\nSaved but not committed.\n"), 0644); err != nil {
		t.Fatalf("writing reflections: %v", err)
	}

	var output bytes.Buffer
	notified := false
	err := runDailyReminder(path, "2026-05-26", func(string) error {
		notified = true
		return nil
	}, &output)
	if err != nil {
		t.Fatalf("runDailyReminder returned error: %v", err)
	}
	if notified {
		t.Fatal("expected notification to be suppressed")
	}
	if !strings.Contains(output.String(), "already recorded") {
		t.Fatalf("unexpected output: %q", output.String())
	}
}

func TestRunDailyReminderNotifiesWhenEntryIsMissing(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	var notifiedFor string
	err := runDailyReminder(filepath.Join(t.TempDir(), "missing.md"), "2026-05-26", func(date string) error {
		notifiedFor = date
		return nil
	}, &output)
	if err != nil {
		t.Fatalf("runDailyReminder returned error: %v", err)
	}
	if notifiedFor != "2026-05-26" {
		t.Fatalf("notified for %q, want today's date", notifiedFor)
	}
	if !strings.Contains(output.String(), "Notification sent") {
		t.Fatalf("unexpected output: %q", output.String())
	}
}

func TestRunDailyReminderReturnsNotifierError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("notification unavailable")
	err := runDailyReminder(filepath.Join(t.TempDir(), "missing.md"), "2026-05-26", func(string) error {
		return wantErr
	}, &bytes.Buffer{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected notifier error, got %v", err)
	}
}
