package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The queries `seneca show` and `seneca review history` answer, shared with
// the phone's history and reviews screens so a day, a month, a range, or a
// period means the same thing on both. They only read.

// EntryQuery narrows the journal's entries. The zero value keeps every entry.
// Month, a range, and OnThisDay exclude one another.
type EntryQuery struct {
	// From and To bound an inclusive range of days; either may be open ("").
	From string
	To   string
	// Month keeps the days of one month, YYYY-MM.
	Month string
	// OnThisDay keeps the earlier days written on today's day of a month.
	OnThisDay bool
}

// Narrowed reports whether the query keeps less than the whole journal.
func (q EntryQuery) Narrowed() bool {
	return q.From != "" || q.To != "" || q.Month != "" || q.OnThisDay
}

// Validate refuses a query that names no real days or names two ways of
// choosing them at once.
func (q EntryQuery) Validate() error {
	ranged := q.From != "" || q.To != ""
	set := 0
	for _, on := range []bool{ranged, q.Month != "", q.OnThisDay} {
		if on {
			set++
		}
	}
	if set > 1 {
		return errors.New("a month, a range of days, and on this day exclude one another")
	}
	for _, bound := range []string{q.From, q.To} {
		if bound != "" && !IsCalendarDate(bound) {
			return fmt.Errorf("%q is not a calendar date (YYYY-MM-DD)", bound)
		}
	}
	if q.From != "" && q.To != "" && q.From > q.To {
		return fmt.Errorf("the range starts on %s, after it ends on %s", q.From, q.To)
	}
	if q.Month != "" && !IsCalendarMonth(q.Month) {
		return fmt.Errorf("%q is not a month (YYYY-MM)", q.Month)
	}
	return nil
}

// IsCalendarMonth reports whether month is a real month written YYYY-MM.
func IsCalendarMonth(month string) bool {
	parsed, err := time.Parse("2006-01", month)
	return err == nil && parsed.Format("2006-01") == month
}

// FilterEntries keeps the entries the query names, in file order.
func FilterEntries(entries []ReflectionEntry, q EntryQuery, today string) []ReflectionEntry {
	switch {
	case q.Month != "":
		return EntriesInMonth(entries, q.Month)
	case q.From != "" || q.To != "":
		return EntriesInRange(entries, q.From, q.To)
	case q.OnThisDay:
		return EntriesOnThisDay(entries, today)
	default:
		return entries
	}
}

// Span closes the query into the days it covers, for NearestEntries. OnThisDay
// and the whole journal have none.
func (q EntryQuery) Span() (from, to string, ok bool) {
	switch {
	case q.Month != "":
		return q.Month + "-01", q.Month + "-31", true
	case q.From != "" || q.To != "":
		from, to = q.From, q.To
		if from == "" {
			from = "0000-00-00"
		}
		if to == "" {
			to = "9999-99-99"
		}
		return from, to, true
	default:
		return "", "", false
	}
}

// EntriesInMonth keeps the entries of one month, YYYY-MM.
func EntriesInMonth(entries []ReflectionEntry, month string) []ReflectionEntry {
	var kept []ReflectionEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Date, month) {
			kept = append(kept, e)
		}
	}
	return kept
}

// EntriesInRange keeps the dated entries inside an inclusive range in which
// either bound may be open ("").
func EntriesInRange(entries []ReflectionEntry, from, to string) []ReflectionEntry {
	var kept []ReflectionEntry
	for _, e := range entries {
		if e.Date == "" || (from != "" && e.Date < from) || (to != "" && e.Date > to) {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// EntriesOnThisDay keeps the entries written on the same day of an earlier
// month as today — the same date a month, a season, or a year ago. Today's own
// entry is left out: the point is to meet what was written before. A day that
// some months lack, such as the 31st, simply has fewer matches.
func EntriesOnThisDay(entries []ReflectionEntry, today string) []ReflectionEntry {
	var kept []ReflectionEntry
	for _, e := range entries {
		if IsCalendarDate(e.Date) && IsCalendarDate(today) && e.Date < today && e.Date[8:] == today[8:] {
			kept = append(kept, e)
		}
	}
	return kept
}

// NearestEntries names the closest dated entries on either side of the
// inclusive range [from, to], so an empty lookup can say where the writing
// actually is. Dates compare as strings because they are canonical
// YYYY-MM-DD; file order is not trusted.
func NearestEntries(entries []ReflectionEntry, from, to string) (before, after string) {
	for _, e := range entries {
		if e.Date == "" {
			continue
		}
		if e.Date < from && e.Date > before {
			before = e.Date
		}
		if e.Date > to && (after == "" || e.Date < after) {
			after = e.Date
		}
	}
	return before, after
}

// IsArchiveReviewPeriod reports whether name is a review period the archive
// stores: weekly, monthly, quarterly, or yearly.
func IsArchiveReviewPeriod(name string) bool {
	return slices.Contains(ArchiveReviewPeriodNames(), name)
}

// ReviewRecordsOfPeriod keeps the reviews of one period, in the order given.
// An empty period keeps them all.
func ReviewRecordsOfPeriod(records []ReviewRecord, period string) []ReviewRecord {
	if period == "" {
		return records
	}
	var kept []ReviewRecord
	for _, record := range records {
		if string(record.Period) == period {
			kept = append(kept, record)
		}
	}
	return kept
}

// ReviewRevisions names, for each saved review, the reviews that revise it,
// oldest first: the other half of each record's Revises link.
func ReviewRevisions(records []ReviewRecord) map[string][]string {
	revisedBy := map[string][]string{}
	for i := len(records) - 1; i >= 0; i-- {
		if revises := records[i].Revises; revises != "" {
			revisedBy[revises] = append(revisedBy[revises], records[i].ID)
		}
	}
	return revisedBy
}
