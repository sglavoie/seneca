package engine

import (
	"errors"
	"testing"
	"time"
)

func TestRemindersFollowTheDesktopScheduleAndSkipCompleteDays(t *testing.T) {
	toronto, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	entries := ParseReflectionDocument("## 2026-04-02\n\nWritten.\n\n---\n\n## 2026-04-04\n\nAhead.\n\n---\n").Entries
	now := time.Date(2026, time.April, 1, 20, 30, 0, 0, toronto)

	plan, err := PlanReminders(entries, now, nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Today != "2026-04-01" || len(plan.Times) != 4 || plan.Times[0] != "20:00" || plan.Times[3] != "22:00" {
		t.Fatalf("expected the desktop's evening schedule from today, got %+v", plan)
	}
	if len(plan.Complete) != 2 || plan.Complete[0] != "2026-04-02" || plan.Complete[1] != "2026-04-04" {
		t.Fatalf("expected the written days to be complete, got %q", plan.Complete)
	}
	var got []string
	for _, reminder := range plan.Reminders {
		got = append(got, reminder.Date+" "+reminder.Time)
		if reminder.At.Location() != toronto {
			t.Fatalf("expected reminders in the journal's zone, got %v", reminder.At)
		}
	}
	want := []string{
		// 20:00 today is already past.
		"2026-04-01 21:00", "2026-04-01 21:30", "2026-04-01 22:00",
		"2026-04-03 20:00", "2026-04-03 21:00", "2026-04-03 21:30", "2026-04-03 22:00",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %q, got %q", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %q, got %q", want, got)
		}
	}
	if !ReminderComplete(entries, "2026-04-02") || ReminderComplete(entries, "2026-04-03") {
		t.Fatal("expected completion to be entry presence")
	}
}

func TestRemindersKeepTheirWallClockAcrossZonesAndDaylightSaving(t *testing.T) {
	toronto, _ := time.LoadLocation("America/Toronto")
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	// The night before daylight saving time starts in Toronto.
	now := time.Date(2026, time.March, 7, 12, 0, 0, 0, toronto)
	plan, err := PlanReminders(nil, now, []string{"21:30"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Reminders) != 2 {
		t.Fatalf("expected one reminder a day, got %+v", plan.Reminders)
	}
	if gap := plan.Reminders[1].At.Sub(plan.Reminders[0].At); gap != 23*time.Hour {
		t.Fatalf("expected 21:30 both evenings across the change (23 hours apart), got %v", gap)
	}

	// The same instant seen from Tokyo is another day with other evenings.
	moved, err := PlanReminders(nil, now.In(tokyo), []string{"21:30"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Today != "2026-03-08" || moved.Reminders[0].At.In(tokyo).Format("15:04") != "21:30" {
		t.Fatalf("expected the plan to follow the zone it is made in, got %+v", moved)
	}
}

func TestReminderTimesAreValidatedAndOrdered(t *testing.T) {
	times, err := NormalizeReminderTimes([]string{"22:15", "07:05", "22:15"})
	if err != nil || len(times) != 2 || times[0] != "07:05" || times[1] != "22:15" {
		t.Fatalf("expected sorted unique times, got %q %v", times, err)
	}
	for _, bad := range [][]string{{"7:05"}, {"24:00"}, {"21:60"}, {"9pm"}, {"01:00", "02:00", "03:00", "04:00", "05:00", "06:00", "07:00", "08:00", "09:00"}} {
		if _, err := NormalizeReminderTimes(bad); !errors.Is(err, ErrInvalidReminderTimes) {
			t.Fatalf("expected %q to be refused, got %v", bad, err)
		}
	}
	if _, err := PlanReminders(nil, time.Now(), nil, 0); !errors.Is(err, ErrInvalidReminderTimes) {
		t.Fatalf("expected a plan of no days to be refused, got %v", err)
	}
}
