package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"strings"
	"testing"
	"time"
)

func arcTestRecord(id string, period engine.ReviewPeriod, start, end string) engine.ReviewRecord {
	return engine.ReviewRecord{ID: id, Period: period, Start: start, End: end}
}

func arcRecordIDs(records []engine.ReviewRecord) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	return ids
}

func assertArcRecordIDs(t *testing.T, got []engine.ReviewRecord, want ...string) {
	t.Helper()
	gotIDs := arcRecordIDs(got)
	if strings.Join(gotIDs, ",") != strings.Join(want, ",") {
		t.Fatalf("expected sources %v, got %v", want, gotIDs)
	}
}

func TestSelectArcWindowExplicitQuarterBoundaries(t *testing.T) {
	t.Parallel()

	// Late enough that no quarter under test is clamped or refused as future.
	now := time.Date(2027, time.December, 31, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		value string
		start string
		end   string
	}{
		{"2026-Q1", "2026-01-01", "2026-03-31"},
		{"2026-Q2", "2026-04-01", "2026-06-30"},
		{"2026-Q3", "2026-07-01", "2026-09-30"},
		// Q4 ends by rolling over into the next year, then stepping back a day.
		{"2026-Q4", "2026-10-01", "2026-12-31"},
		// A leap year lengthens Q1 by a day.
		{"2024-Q1", "2024-01-01", "2024-03-31"},
	}

	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			t.Parallel()

			window, err := selectArcWindow(nil, now, tc.value, true, "", false)
			if err != nil {
				t.Fatalf("selectArcWindow returned error: %v", err)
			}
			if window.startDate() != tc.start || window.endDate() != tc.end {
				t.Fatalf("expected %s to %s, got %s to %s", tc.start, tc.end, window.startDate(), window.endDate())
			}
			if window.Title != "Quarterly review" || window.PromptLabel != "quarterly" {
				t.Fatalf("unexpected title %q / prompt label %q", window.Title, window.PromptLabel)
			}
			if window.Period != engine.ReviewPeriodQuarterly {
				t.Fatalf("expected quarterly period, got %q", window.Period)
			}
			if window.PeriodLabel != tc.start+" to "+tc.end {
				t.Fatalf("unexpected period label %q", window.PeriodLabel)
			}
			if window.SourceKind != sourceReviews {
				t.Fatalf("expected a review-sourced window, got %q", window.SourceKind)
			}
			if len(window.Entries) != 0 {
				t.Fatalf("expected no entries on an arc window, got %d", len(window.Entries))
			}
		})
	}
}

func TestSelectArcWindowBareQuarterUsesCurrentQuarter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		now   time.Time
		start string
		end   string
	}{
		{"q1", time.Date(2026, time.February, 15, 8, 0, 0, 0, time.UTC), "2026-01-01", "2026-02-15"},
		{"q2", time.Date(2026, time.May, 31, 8, 0, 0, 0, time.UTC), "2026-04-01", "2026-05-31"},
		{"q3", time.Date(2026, time.August, 1, 8, 0, 0, 0, time.UTC), "2026-07-01", "2026-08-01"},
		{"q4", time.Date(2026, time.November, 20, 8, 0, 0, 0, time.UTC), "2026-10-01", "2026-11-20"},
		// The last day of Q4 is the one bare case where nothing is clamped.
		{"q4-complete", time.Date(2026, time.December, 31, 8, 0, 0, 0, time.UTC), "2026-10-01", "2026-12-31"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, value := range []string{"", "current"} {
				window, err := selectArcWindow(nil, tc.now, value, true, "", false)
				if err != nil {
					t.Fatalf("selectArcWindow(%q) returned error: %v", value, err)
				}
				if window.startDate() != tc.start || window.endDate() != tc.end {
					t.Fatalf("selectArcWindow(%q): expected %s to %s, got %s to %s",
						value, tc.start, tc.end, window.startDate(), window.endDate())
				}
			}
		})
	}
}

func TestSelectArcWindowAcceptsLowercaseQuarter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.December, 31, 8, 0, 0, 0, time.UTC)
	window, err := selectArcWindow(nil, now, "2026-q2", true, "", false)
	if err != nil {
		t.Fatalf("selectArcWindow returned error: %v", err)
	}
	if window.startDate() != "2026-04-01" || window.endDate() != "2026-06-30" {
		t.Fatalf("unexpected window %s to %s", window.startDate(), window.endDate())
	}
}

func TestSelectArcWindowYear(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.UTC)

	explicit, err := selectArcWindow(nil, now, "", false, "2025", true)
	if err != nil {
		t.Fatalf("selectArcWindow returned error: %v", err)
	}
	if explicit.startDate() != "2025-01-01" || explicit.endDate() != "2025-12-31" {
		t.Fatalf("unexpected explicit year window %s to %s", explicit.startDate(), explicit.endDate())
	}
	if explicit.Title != "Yearly review" || explicit.PromptLabel != "yearly" {
		t.Fatalf("unexpected title %q / prompt label %q", explicit.Title, explicit.PromptLabel)
	}
	if explicit.Period != engine.ReviewPeriodYearly {
		t.Fatalf("expected yearly period, got %q", explicit.Period)
	}

	// A year still running is reviewed only as far as today.
	for _, value := range []string{"", "current"} {
		bare, err := selectArcWindow(nil, now, "", false, value, true)
		if err != nil {
			t.Fatalf("selectArcWindow(%q) returned error: %v", value, err)
		}
		if bare.startDate() != "2026-01-01" || bare.endDate() != "2026-09-01" {
			t.Fatalf("selectArcWindow(%q): unexpected window %s to %s", value, bare.startDate(), bare.endDate())
		}
	}
}

func TestSelectArcWindowRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.UTC)

	quarters := []string{"2026-Q5", "2026-Q0", "2026-13", "20xx", "2026", "2026-Q", "2026-Q01", "-Q1", "2026Q1"}
	for _, value := range quarters {
		t.Run("quarter/"+value, func(t *testing.T) {
			t.Parallel()

			_, err := selectArcWindow(nil, now, value, true, "", false)
			if err == nil {
				t.Fatalf("expected --quarter %q to be rejected", value)
			}
			if !strings.Contains(err.Error(), "expected YYYY-Q1 through YYYY-Q4") {
				t.Fatalf("expected a readable format message, got %v", err)
			}
		})
	}

	years := []string{"2026-13", "20xx", "26", "2026-Q1", "202600"}
	for _, value := range years {
		t.Run("year/"+value, func(t *testing.T) {
			t.Parallel()

			_, err := selectArcWindow(nil, now, "", false, value, true)
			if err == nil {
				t.Fatalf("expected --year %q to be rejected", value)
			}
			if !strings.Contains(err.Error(), "expected YYYY") {
				t.Fatalf("expected a readable format message, got %v", err)
			}
		})
	}
}

func TestSelectArcWindowRejectsFuturePeriods(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.UTC)

	if _, err := selectArcWindow(nil, now, "2026-Q4", true, "", false); err == nil ||
		!strings.Contains(err.Error(), "cannot be in the future") {
		t.Fatalf("expected a future quarter to be rejected, got %v", err)
	}
	if _, err := selectArcWindow(nil, now, "", false, "2027", true); err == nil ||
		!strings.Contains(err.Error(), "cannot be in the future") {
		t.Fatalf("expected a future year to be rejected, got %v", err)
	}

	// The quarter today falls in has started, so it is reviewable, clamped.
	window, err := selectArcWindow(nil, now, "2026-Q3", true, "", false)
	if err != nil {
		t.Fatalf("selectArcWindow returned error for the current quarter: %v", err)
	}
	if window.startDate() != "2026-07-01" || window.endDate() != "2026-09-01" {
		t.Fatalf("expected the current quarter clamped to today, got %s to %s", window.startDate(), window.endDate())
	}
}

func TestSelectArcWindowRequiresExactlyOnePeriod(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.UTC)

	if _, err := selectArcWindow(nil, now, "", false, "", false); err == nil ||
		!strings.Contains(err.Error(), "missing arc period") {
		t.Fatalf("expected a missing-period error, got %v", err)
	}
	if _, err := selectArcWindow(nil, now, "2026-Q1", true, "2026", true); err == nil ||
		!strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected a conflicting-period error, got %v", err)
	}
}

func TestSelectArcWindowKeepsOnlyStrictlyShorterPeriods(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.December, 31, 8, 0, 0, 0, time.UTC)
	records := []engine.ReviewRecord{
		arcTestRecord("unknown", engine.ReviewPeriod("fortnightly"), "2026-07-06", "2026-07-19"),
		arcTestRecord("yearly", engine.ReviewPeriodYearly, "2026-01-01", "2026-09-30"),
		arcTestRecord("quarterly", engine.ReviewPeriodQuarterly, "2026-07-01", "2026-09-30"),
		arcTestRecord("monthly", engine.ReviewPeriodMonthly, "2026-08-01", "2026-08-31"),
		arcTestRecord("weekly", engine.ReviewPeriodWeekly, "2026-07-13", "2026-07-19"),
	}

	quarter, err := selectArcWindow(records, now, "2026-Q3", true, "", false)
	if err != nil {
		t.Fatalf("selectArcWindow returned error: %v", err)
	}
	assertArcRecordIDs(t, quarter.Reviews, "weekly", "monthly")

	year, err := selectArcWindow(records, now, "", false, "2026", true)
	if err != nil {
		t.Fatalf("selectArcWindow returned error: %v", err)
	}
	assertArcRecordIDs(t, year.Reviews, "weekly", "monthly", "quarterly")
}

func TestSelectArcWindowIncludesBoundaryEndDates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.December, 31, 8, 0, 0, 0, time.UTC)
	records := []engine.ReviewRecord{
		arcTestRecord("after", engine.ReviewPeriodWeekly, "2026-09-25", "2026-10-01"),
		arcTestRecord("last", engine.ReviewPeriodWeekly, "2026-09-24", "2026-09-30"),
		arcTestRecord("first", engine.ReviewPeriodWeekly, "2026-06-25", "2026-07-01"),
		arcTestRecord("before", engine.ReviewPeriodWeekly, "2026-06-24", "2026-06-30"),
		// An unreadable end date cannot be placed in the window at all.
		arcTestRecord("unparseable", engine.ReviewPeriodWeekly, "2026-07-01", "not-a-date"),
	}

	window, err := selectArcWindow(records, now, "2026-Q3", true, "", false)
	if err != nil {
		t.Fatalf("selectArcWindow returned error: %v", err)
	}
	assertArcRecordIDs(t, window.Reviews, "first", "last")
}

func TestSelectArcWindowDropsSupersededRevisions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.December, 31, 8, 0, 0, 0, time.UTC)
	// Newest first, as reviewRecords returns them: the revision leads.
	records := []engine.ReviewRecord{
		{ID: "week-29-revised", Period: engine.ReviewPeriodWeekly, Start: "2026-07-13", End: "2026-07-19", Revises: "week-29"},
		{ID: "week-30", Period: engine.ReviewPeriodWeekly, Start: "2026-07-20", End: "2026-07-26"},
		{ID: "week-29", Period: engine.ReviewPeriodWeekly, Start: "2026-07-13", End: "2026-07-19"},
	}

	window, err := selectArcWindow(records, now, "2026-Q3", true, "", false)
	if err != nil {
		t.Fatalf("selectArcWindow returned error: %v", err)
	}
	assertArcRecordIDs(t, window.Reviews, "week-29-revised", "week-30")
}

func TestSelectArcWindowKeepsDistinctWindowsThatShareABound(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.December, 31, 8, 0, 0, 0, time.UTC)
	// Same end, different start, and same start/end but a different period:
	// neither pair is a revision of the other, so all three survive.
	records := []engine.ReviewRecord{
		arcTestRecord("monthly-july", engine.ReviewPeriodMonthly, "2026-07-01", "2026-07-31"),
		arcTestRecord("weekly-july", engine.ReviewPeriodWeekly, "2026-07-01", "2026-07-31"),
		arcTestRecord("weekly-short", engine.ReviewPeriodWeekly, "2026-07-25", "2026-07-31"),
	}

	window, err := selectArcWindow(records, now, "2026-Q3", true, "", false)
	if err != nil {
		t.Fatalf("selectArcWindow returned error: %v", err)
	}
	if len(window.Reviews) != 3 {
		t.Fatalf("expected all 3 distinct windows kept, got %v", arcRecordIDs(window.Reviews))
	}
}

func TestSelectArcWindowOrdersSourcesDeterministically(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.December, 31, 8, 0, 0, 0, time.UTC)
	records := []engine.ReviewRecord{
		arcTestRecord("september", engine.ReviewPeriodMonthly, "2026-09-01", "2026-09-30"),
		arcTestRecord("week-30", engine.ReviewPeriodWeekly, "2026-07-20", "2026-07-26"),
		arcTestRecord("august", engine.ReviewPeriodMonthly, "2026-08-01", "2026-08-31"),
		arcTestRecord("week-35", engine.ReviewPeriodWeekly, "2026-08-24", "2026-08-30"),
		arcTestRecord("july", engine.ReviewPeriodMonthly, "2026-07-01", "2026-07-31"),
		arcTestRecord("week-29", engine.ReviewPeriodWeekly, "2026-07-13", "2026-07-19"),
	}

	first, err := selectArcWindow(records, now, "2026-Q3", true, "", false)
	if err != nil {
		t.Fatalf("selectArcWindow returned error: %v", err)
	}
	assertArcRecordIDs(t, first.Reviews,
		"week-29", "week-30", "july", "week-35", "august", "september")

	second, err := selectArcWindow(records, now, "2026-Q3", true, "", false)
	if err != nil {
		t.Fatalf("selectArcWindow returned error: %v", err)
	}
	assertArcRecordIDs(t, second.Reviews, arcRecordIDs(first.Reviews)...)
}

func TestSelectArcWindowWithNoSourcesIsNotAnError(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.December, 31, 8, 0, 0, 0, time.UTC)
	window, err := selectArcWindow(nil, now, "2026-Q1", true, "", false)
	if err != nil {
		t.Fatalf("selectArcWindow returned error: %v", err)
	}
	if len(window.Reviews) != 0 {
		t.Fatalf("expected no sources, got %v", arcRecordIDs(window.Reviews))
	}
	if window.coverageLine() != "0 source reviews" {
		t.Fatalf("unexpected coverage line %q", window.coverageLine())
	}
}

func TestReviewWindowCoverageLine(t *testing.T) {
	t.Parallel()

	entryWindow := reviewWindow{CoveredDays: 5, ExpectedDays: 7}
	if got := entryWindow.coverageLine(); got != "Coverage 5/7 days" {
		t.Fatalf("unexpected entry coverage line %q", got)
	}

	// An entry-sourced window is the zero value of SourceKind, so every window
	// built before arc reviews existed still reports day coverage.
	if entryWindow.SourceKind != sourceEntries {
		t.Fatalf("expected the zero source kind to be sourceEntries, got %q", entryWindow.SourceKind)
	}

	reviewSourced := reviewWindow{
		SourceKind:  sourceReviews,
		CoveredDays: 5,
		Reviews:     make([]engine.ReviewRecord, 12),
	}
	if got := reviewSourced.coverageLine(); got != "12 source reviews" {
		t.Fatalf("unexpected review coverage line %q", got)
	}

	single := reviewWindow{SourceKind: sourceReviews, Reviews: make([]engine.ReviewRecord, 1)}
	if got := single.coverageLine(); got != "1 source review" {
		t.Fatalf("unexpected singular coverage line %q", got)
	}
}
