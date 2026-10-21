package engine

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Reminders are the evening nudges to write the day's reflection. The desktop
// shows them from a LaunchAgent and the iPhone schedules them as local
// notifications; both follow one rule, which lives here: a day the journal on
// this device already holds an entry for is complete, and gets no reminder.
//
// Completion is what this device knows. A reflection saved on another device
// suppresses a reminder here only once it has arrived and been imported; until
// then the reminder still fires. Nothing here asks Git or the exchange.

// DefaultReminderTimes are the desktop's evening schedule, as local times of
// day. A device that has not chosen its own times uses these.
var DefaultReminderTimes = []string{"20:00", "21:00", "21:30", "22:00"}

// MaxReminderTimes bounds the times of one evening, so a week of reminders
// stays well inside what a phone keeps pending.
const MaxReminderTimes = 8

// MaxReminderDays bounds how many days ahead reminders are planned.
const MaxReminderDays = 14

const reminderTimeLayout = "15:04"

// ErrInvalidReminderTimes reports a schedule the planner refuses.
var ErrInvalidReminderTimes = errors.New("invalid reminder times")

// NormalizeReminderTimes validates times of day written as HH:MM, and returns
// them sorted with duplicates removed. Empty means the default schedule.
func NormalizeReminderTimes(times []string) ([]string, error) {
	if len(times) == 0 {
		return append([]string{}, DefaultReminderTimes...), nil
	}
	seen := map[string]bool{}
	var normalized []string
	for _, value := range times {
		parsed, err := time.Parse(reminderTimeLayout, value)
		if err != nil || parsed.Format(reminderTimeLayout) != value {
			return nil, fmt.Errorf("%w: %q is not a time of day written as HH:MM", ErrInvalidReminderTimes, value)
		}
		if !seen[value] {
			seen[value] = true
			normalized = append(normalized, value)
		}
	}
	if len(normalized) > MaxReminderTimes {
		return nil, fmt.Errorf("%w: at most %d reminders an evening, not %d", ErrInvalidReminderTimes, MaxReminderTimes, len(normalized))
	}
	sort.Strings(normalized)
	return normalized, nil
}

// ReminderComplete reports whether date needs no reminder: the journal on this
// device holds an entry for it. It is the rule the desktop reminder has always
// used, so a saved entry silences the evening whether or not it is committed.
func ReminderComplete(entries []ReflectionEntry, date string) bool {
	_, found := FindEntry(entries, date)
	return found
}

// PlannedReminder is one reminder to schedule.
type PlannedReminder struct {
	// Date is the journal day the reminder is about.
	Date string
	// Time is the time of day it was planned for, HH:MM.
	Time string
	// At is the instant it fires. On a day the clocks skip the planned time,
	// it fires at the same wall-clock offset after the skip.
	At time.Time
}

// ReminderPlan is what to schedule from now on.
type ReminderPlan struct {
	Today string
	Times []string
	// Complete are the planned days that need no reminder, in date order.
	Complete []string
	// Reminders are the reminders still ahead, earliest first.
	Reminders []PlannedReminder
}

// PlanReminders plans the reminders of days days, starting with now's day in
// now's location: every time of every day that is not complete and is still
// ahead of now. A day is complete when entries holds it.
func PlanReminders(entries []ReflectionEntry, now time.Time, times []string, days int) (ReminderPlan, error) {
	normalized, err := NormalizeReminderTimes(times)
	if err != nil {
		return ReminderPlan{}, err
	}
	if days < 1 || days > MaxReminderDays {
		return ReminderPlan{}, fmt.Errorf("%w: plan between 1 and %d days ahead, not %d", ErrInvalidReminderTimes, MaxReminderDays, days)
	}
	location := now.Location()
	year, month, day := now.Date()
	plan := ReminderPlan{
		Today:     now.Format(CalendarDateLayout),
		Times:     normalized,
		Complete:  []string{},
		Reminders: []PlannedReminder{},
	}
	for offset := range days {
		midnight := time.Date(year, month, day+offset, 0, 0, 0, 0, location)
		date := midnight.Format(CalendarDateLayout)
		if ReminderComplete(entries, date) {
			plan.Complete = append(plan.Complete, date)
			continue
		}
		for _, value := range normalized {
			clock, _ := time.Parse(reminderTimeLayout, value)
			at := time.Date(midnight.Year(), midnight.Month(), midnight.Day(), clock.Hour(), clock.Minute(), 0, 0, location)
			if !at.After(now) {
				continue
			}
			plan.Reminders = append(plan.Reminders, PlannedReminder{Date: date, Time: value, At: at})
		}
	}
	return plan, nil
}
