package engine

import (
	"fmt"
	"strings"
	"time"
)

// Every date a person types on the command line passes through here, so
// `export --from`, `--date`, `show --date`, and `focus --date` refuse exactly
// the same inputs with the same words.

const CalendarDateLayout = "2006-01-02"

// IsCalendarDate accepts only a canonical date that names a real day. The
// layout is fixed-width, so `2026-9-4` is refused rather than guessed at, and
// `2026-02-29` is refused because 2026 is not a leap year — a bound that does
// not exist would silently select the wrong days.
func IsCalendarDate(value string) bool {
	_, err := time.Parse(CalendarDateLayout, value)
	return err == nil
}

// RequireCalendarDate validates a flag that takes only a calendar date. Export
// stays strict on purpose: a packet's range is evidence, and "yesterday" would
// make the same command select different days on different evenings.
func RequireCalendarDate(flag, value string) error {
	if !IsCalendarDate(value) {
		return fmt.Errorf("invalid --%s %q: expected a real calendar date as YYYY-MM-DD", flag, value)
	}
	return nil
}

// ParseDayFlag reads a flag that names a day to act on now: a calendar date, or
// `today`/`yesterday` relative to now, since backfilling the night before is the
// common case.
func ParseDayFlag(flag, value string, now time.Time) (string, error) {
	switch strings.ToLower(value) {
	case "today":
		return now.Format(CalendarDateLayout), nil
	case "yesterday":
		return now.AddDate(0, 0, -1).Format(CalendarDateLayout), nil
	}
	if !IsCalendarDate(value) {
		return "", fmt.Errorf("invalid --%s %q: expected a real calendar date as YYYY-MM-DD, today, or yesterday", flag, value)
	}
	return value, nil
}

// RelativeDay says how far a canonical date lies from today, in whole calendar
// days: "today", "yesterday", "24 days ago", "tomorrow", or "in 3 days". A date
// is easier to place than to count, so every place that names a past day the
// author may want to act on also says how long ago it was. It returns "" when
// either value is not a calendar date, so callers can append it unconditionally.
func RelativeDay(date, today string) string {
	days, ok := daysSince(date, today)
	if !ok {
		return ""
	}
	switch {
	case days == 0:
		return "today"
	case days == 1:
		return "yesterday"
	case days == -1:
		return "tomorrow"
	case days > 1:
		return fmt.Sprintf("%d days ago", days)
	default:
		return fmt.Sprintf("in %d days", -days)
	}
}

// daysSince counts the calendar days from date to today; negative when date is
// still ahead. It reports false when either value is not a calendar date.
func daysSince(date, today string) (int, bool) {
	from, err := time.Parse(CalendarDateLayout, date)
	if err != nil {
		return 0, false
	}
	to, err := time.Parse(CalendarDateLayout, today)
	if err != nil {
		return 0, false
	}
	// Both are UTC midnights, so the difference is an exact number of days.
	return int(to.Sub(from).Hours() / 24), true
}

// DatedRelative renders a date followed by its distance from today, such as
// "2026-09-10 (24 days ago)", or the bare date when it cannot be placed.
func DatedRelative(date, today string) string {
	if rel := RelativeDay(date, today); rel != "" {
		return date + " (" + rel + ")"
	}
	return date
}

// plural takes both forms rather than appending an "s": "entry" and "entries"
// are the two words this report needs most.
func Plural(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}
