package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"sort"
	"strings"
	"time"
)

// Arc reviews — `--quarter` and `--year` — span longer than a month and read a
// different kind of evidence. A weekly or monthly review synthesizes what the
// author wrote; an arc review synthesizes what Seneca already concluded, so its
// sources are saved review records rather than reflection entries.
//
// This lives beside selectReviewWindow rather than inside it. The entry path's
// scanning loop has nothing to say about review records, and keeping the two
// selectors separate means adding an arc period can never change what a weekly
// review sees.

// selectArcWindow builds the window for an arc review and gathers the saved
// reviews that ground it. records must arrive newest first, the order
// ReviewArchive.reviewRecords returns them in, because the supersede rule keeps
// the first record it meets for a window identity.
func selectArcWindow(
	records []engine.ReviewRecord,
	now time.Time,
	quarterValue string,
	quarterSet bool,
	yearValue string,
	yearSet bool,
) (reviewWindow, error) {
	today := truncateDate(now)

	var (
		start       time.Time
		end         time.Time
		title       string
		promptLabel string
		period      engine.ReviewPeriod
	)

	switch {
	case quarterSet && yearSet:
		return reviewWindow{}, fmt.Errorf("conflicting period: use --quarter or --year, not both")
	case quarterSet:
		quarterStart, err := parseQuarterStart(quarterValue, today)
		if err != nil {
			return reviewWindow{}, err
		}
		start = quarterStart
		end = quarterStart.AddDate(0, 3, -1)
		title = "Quarterly review"
		promptLabel = "quarterly"
		period = engine.ReviewPeriodQuarterly
	case yearSet:
		yearStart, err := parseYearStart(yearValue, today)
		if err != nil {
			return reviewWindow{}, err
		}
		start = yearStart
		end = yearStart.AddDate(1, 0, -1)
		title = "Yearly review"
		promptLabel = "yearly"
		period = engine.ReviewPeriodYearly
	default:
		return reviewWindow{}, fmt.Errorf("missing arc period: set --quarter or --year")
	}

	// A period still running is reviewed up to today, never past it.
	if end.After(today) {
		end = today
	}

	window := reviewWindow{
		Title:        title,
		Period:       period,
		PromptLabel:  promptLabel,
		Start:        start,
		End:          end,
		ExpectedDays: int(end.Sub(start).Hours()/24) + 1,
		SourceKind:   sourceReviews,
	}
	window.PeriodLabel = fmt.Sprintf("%s to %s", start.Format("2006-01-02"), end.Format("2006-01-02"))
	window.Reviews = arcSourceReviews(records, window)
	return window, nil
}

// arcSourceReviews picks the saved reviews an arc window synthesizes.
//
// Only strictly shorter periods qualify: a quarterly review reads weeks and
// months, a yearly review also reads quarters. Without that rule an arc review
// would ingest its own prior output and call it evidence. A record whose period
// this binary does not know ranks 0 and is dropped — it is preserved in the
// file verbatim, but it is never handed to a model.
func arcSourceReviews(records []engine.ReviewRecord, window reviewWindow) []engine.ReviewRecord {
	limit := periodRank(window.Period)
	seen := make(map[string]struct{}, len(records))
	var selected []engine.ReviewRecord
	for _, record := range records {
		rank := periodRank(record.Period)
		if rank == 0 || rank >= limit {
			continue
		}

		recordEnd, err := time.ParseInLocation("2006-01-02", record.End, window.End.Location())
		if err != nil {
			continue
		}
		if recordEnd.Before(window.Start) || recordEnd.After(window.End) {
			continue
		}

		// A revision and the record it supersedes share this identity, the same
		// one latestReviewForPeriod matches on. records is newest first, so the
		// first one seen is the surviving revision; counting both would double
		// that week's evidence in the prompt.
		key := arcSourceKey(record)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		selected = append(selected, record)
	}

	// Ascending by end, then start, so the prompt reads forward through the
	// period. The sort is stable, so records that tie on both keys stay in the
	// newest-first order they arrived in.
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].End != selected[j].End {
			return selected[i].End < selected[j].End
		}
		return selected[i].Start < selected[j].Start
	})
	return selected
}

// arcSourceKey is the window identity a revision shares with the record it
// supersedes: period, start, and end. The separator cannot occur in any of the
// three, so distinct triples cannot collide.
func arcSourceKey(record engine.ReviewRecord) string {
	return string(record.Period) + "\x00" + record.Start + "\x00" + record.End
}

// parseQuarterStart resolves a --quarter value to the first day of that
// quarter. Bare or `current` means the quarter today falls in.
func parseQuarterStart(value string, today time.Time) (time.Time, error) {
	key := value
	if key == "" || key == "current" {
		key = quarterKey(today)
	}

	year, quarter, err := parseQuarterKey(key)
	if err != nil {
		return time.Time{}, err
	}

	start := time.Date(year, time.Month((quarter-1)*3+1), 1, 0, 0, 0, 0, today.Location())
	if start.After(today) {
		return time.Time{}, fmt.Errorf("invalid --quarter %q: review quarter cannot be in the future", key)
	}
	return start, nil
}

// parseYearStart resolves a --year value to January 1st of that year. Bare or
// `current` means this year.
func parseYearStart(value string, today time.Time) (time.Time, error) {
	key := value
	if key == "" || key == "current" {
		key = today.Format("2006")
	}

	start, err := time.ParseInLocation("2006", key, today.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --year %q: expected YYYY", key)
	}
	if start.After(today) {
		return time.Time{}, fmt.Errorf("invalid --year %q: review year cannot be in the future", key)
	}
	return start, nil
}

// parseQuarterKey reads YYYY-QN. There is no time layout for a quarter, so the
// parse is written out; the year still goes through time.ParseInLocation so a
// non-numeric year is rejected by the same rule --month uses. The Q is matched
// case-insensitively because a lowercase `2026-q3` is a typo, not a request for
// something else.
func parseQuarterKey(key string) (int, int, error) {
	yearPart, quarterPart, found := strings.Cut(strings.ToUpper(key), "-Q")
	if !found {
		return 0, 0, quarterFormatError(key)
	}

	yearStart, err := time.ParseInLocation("2006", yearPart, time.UTC)
	if err != nil {
		return 0, 0, quarterFormatError(key)
	}
	if len(quarterPart) != 1 || quarterPart[0] < '1' || quarterPart[0] > '4' {
		return 0, 0, quarterFormatError(key)
	}

	return yearStart.Year(), int(quarterPart[0] - '0'), nil
}

func quarterFormatError(key string) error {
	return fmt.Errorf("invalid --quarter %q: expected YYYY-Q1 through YYYY-Q4", key)
}

// quarterKey names the quarter a date falls in.
func quarterKey(date time.Time) string {
	return fmt.Sprintf("%04d-Q%d", date.Year(), (int(date.Month())-1)/3+1)
}
