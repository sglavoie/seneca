package engine

import (
	"strings"
	"testing"
	"time"
)

func TestParseDayFlagAcceptsDatesAndRelativeDays(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 1, 21, 30, 0, 0, time.Local)
	cases := map[string]string{
		"2026-02-14": "2026-02-14",
		"2028-02-29": "2028-02-29",
		"today":      "2026-03-01",
		"yesterday":  "2026-02-28",
		"Yesterday":  "2026-02-28",
	}
	for input, want := range cases {
		got, err := ParseDayFlag("date", input, now)
		if err != nil || got != want {
			t.Fatalf("parseDayFlag(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
}

// Malformed dates are refused identically by every flag that takes one; only
// the keywords differ, and only where a day relative to now makes sense.
func TestDateFlagsRefuseTheSameMalformedDates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 1, 21, 30, 0, 0, time.Local)
	for _, input := range []string{"2026-3-1", "2026-02-29", "2026-04-31", "2026-13-01", "2026-03-01 ", "20260301", "2026/03/01", ""} {
		dayErr := func() error { _, err := ParseDayFlag("date", input, now); return err }()
		exportErr := RequireCalendarDate("from", input)
		if dayErr == nil || exportErr == nil {
			t.Fatalf("expected %q to be refused by both, got --date %v and --from %v", input, dayErr, exportErr)
		}
		if !strings.Contains(dayErr.Error(), "expected a real calendar date as YYYY-MM-DD") ||
			!strings.Contains(exportErr.Error(), "expected a real calendar date as YYYY-MM-DD") {
			t.Fatalf("expected the same wording for %q, got %q and %q", input, dayErr, exportErr)
		}
	}

	if err := RequireCalendarDate("from", "yesterday"); err == nil {
		t.Fatal("expected export to stay strict and refuse a relative day")
	}
	if _, err := ParseDayFlag("date", "tomorrow", now); err == nil {
		t.Fatal("expected an unknown keyword to be refused")
	}
}

func TestRelativeDayPlacesADateAgainstToday(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"2026-10-04": "today",
		"2026-10-03": "yesterday",
		"2026-09-10": "24 days ago",
		"2025-10-04": "365 days ago",
		"2026-10-05": "tomorrow",
		"2026-10-07": "in 3 days",
		"2026-10-4":  "",
		"":           "",
	}
	for date, want := range cases {
		if got := RelativeDay(date, "2026-10-04"); got != want {
			t.Errorf("relativeDay(%q) = %q, want %q", date, got, want)
		}
	}
	// A daylight-saving change between the two dates must not shift the count.
	if got := RelativeDay("2026-03-01", "2026-03-15"); got != "14 days ago" {
		t.Errorf("expected 14 days across the DST change, got %q", got)
	}
	if got := DatedRelative("2026-09-10", "2026-10-04"); got != "2026-09-10 (24 days ago)" {
		t.Errorf("datedRelative = %q", got)
	}
	if got := DatedRelative("someday", "2026-10-04"); got != "someday" {
		t.Errorf("expected an unplaceable date left bare, got %q", got)
	}
}
