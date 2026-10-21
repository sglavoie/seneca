package senecabridge

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/seneca/engine"
)

// newDeviceFrom is a device over a copy of a whole fixture journal: its
// Markdown and, for a synced one, its .seneca history.
func newDeviceFrom(t *testing.T, fixture string) *device {
	t.Helper()
	root := t.TempDir()
	d := &device{t: t, bridge: NewBridge(), dir: filepath.Join(root, "journal"), stateDir: filepath.Join(root, "state")}
	for rel, data := range tree(t, filepath.Join(fixture, "journal")) {
		path := filepath.Join(d.dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d.open(fixtureNow)
	return d
}

func dates(entries []entrySummary) []string {
	var days []string
	for _, entry := range entries {
		days = append(days, entry.Date)
	}
	// The file keeps the order days were written in; a backfilled day may
	// come after a later one.
	sort.Strings(days)
	return days
}

func TestHistoryQueriesAnswerAsTheCommandLineDoes(t *testing.T) {
	d := newDeviceFrom(t, browsingFixture)
	all := []string{"2026-01-02", "2026-02-02", "2026-03-02", "2026-03-03", "2026-03-04", "2026-03-05"}

	cases := []struct {
		name          string
		query         map[string]any
		want          []string
		before, after string
	}{
		{"everything", nil, all, "", ""},
		{"a month", map[string]any{"month": "2026-03"}, all[2:], "2026-02-02", ""},
		{"an empty month", map[string]any{"month": "2025-12"}, nil, "", "2026-01-02"},
		{"a range", map[string]any{"from": "2026-02-01", "to": "2026-03-03"}, all[1:4], "2026-01-02", "2026-03-04"},
		{"a range open at its start", map[string]any{"to": "2026-02-02"}, all[:2], "", "2026-03-02"},
		{"a range open at its end", map[string]any{"from": "2026-03-04"}, all[4:], "2026-03-03", ""},
		{"one day", map[string]any{"from": "2026-03-03", "to": "2026-03-03"}, []string{"2026-03-03"}, "2026-03-02", "2026-03-04"},
		{"on this day", map[string]any{"on_this_day": true}, all[:3], "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var listed entriesResult
			d.must("entries.list", tc.query, &listed)
			if !reflect.DeepEqual(dates(listed.Entries), tc.want) || listed.Total != len(all) ||
				listed.NearestBefore != tc.before || listed.NearestAfter != tc.after {
				t.Fatalf("entries.list %v = %v (total %d, nearest %q %q), want %v (nearest %q %q)",
					tc.query, dates(listed.Entries), listed.Total, listed.NearestBefore, listed.NearestAfter, tc.want, tc.before, tc.after)
			}
		})
	}

	// The search covers what the query keeps, with the command line's snippets.
	var everywhere, inMarch searchResult
	d.must("entries.search", map[string]any{"phrase": "SYNTHETIC"}, &everywhere)
	d.must("entries.search", map[string]any{"phrase": "synthetic", "month": "2026-03"}, &inMarch)
	if len(everywhere.Hits) <= len(inMarch.Hits) || len(inMarch.Hits) == 0 {
		t.Fatalf("search everywhere = %d days, in March = %d days", len(everywhere.Hits), len(inMarch.Hits))
	}
	for _, hit := range inMarch.Hits {
		if !strings.HasPrefix(hit.Date, "2026-03") {
			t.Fatalf("a search of March found %s", hit.Date)
		}
	}
	var narrowed searchResult
	d.must("entries.search", map[string]any{"phrase": "walk", "from": "2026-01-01", "to": "2026-01-31"}, &narrowed)
	if len(narrowed.Hits) != 1 || narrowed.Hits[0].Date != "2026-01-02" ||
		!reflect.DeepEqual(narrowed.Hits[0].Snippets, []string{"Synthetic: kept the walk.", "Walk again tomorrow."}) {
		t.Fatalf("search of January = %+v", narrowed.Hits)
	}

	for _, query := range []map[string]any{
		{"month": "2026-03", "from": "2026-03-01"},
		{"on_this_day": true, "to": "2026-03-01"},
		{"from": "2026-03-05", "to": "2026-03-01"},
		{"month": "2026-13"},
		{"from": "2026-02-30"},
	} {
		d.refused("entries.list", "", query, "invalid-query")
		query["phrase"] = "walk"
		d.refused("entries.search", "", query, "invalid-query")
	}
}

func TestSavedReviewsOfEveryPeriodAndTheirRevisions(t *testing.T) {
	d := newDeviceFrom(t, browsingFixture)
	var listed reviewsResult
	d.must("reviews.list", nil, &listed)

	periods := map[string]int{}
	byID := map[string]reviewSummary{}
	for _, review := range listed.Reviews {
		periods[review.Period]++
		byID[review.ID] = review
	}
	if !reflect.DeepEqual(periods, map[string]int{"weekly": 2, "monthly": 1, "quarterly": 2, "yearly": 1}) {
		t.Fatalf("periods = %v", periods)
	}
	// Each revision names what it revises, and is named back.
	revisions := 0
	for _, review := range listed.Reviews {
		if review.Revises == "" {
			continue
		}
		revisions++
		earlier, found := byID[review.Revises]
		if !found || !earlier.Superseded || !reflect.DeepEqual(earlier.RevisedBy, []string{review.ID}) ||
			earlier.Period != review.Period || earlier.Start != review.Start || earlier.End != review.End {
			t.Fatalf("%s revises %+v", review.ID, earlier)
		}
		if review.Superseded || len(review.RevisedBy) != 0 {
			t.Fatalf("the newest revision %+v is said to be revised", review)
		}
	}
	if revisions != 2 {
		t.Fatalf("%d revisions listed, want the weekly and the quarterly", revisions)
	}

	for _, period := range engine.ArchiveReviewPeriodNames() {
		var kept reviewsResult
		d.must("reviews.list", map[string]any{"period": period}, &kept)
		if len(kept.Reviews) != periods[period] {
			t.Fatalf("reviews.list %s = %d reviews, want %d", period, len(kept.Reviews), periods[period])
		}
		for _, review := range kept.Reviews {
			// A revision is listed beside the period's other reviews, linked
			// to its earlier version even when that one is listed too.
			if review.Period != period || !reflect.DeepEqual(review, byID[review.ID]) {
				t.Fatalf("reviews.list %s gave %+v", period, review)
			}
		}
	}
	d.refused("reviews.list", "", map[string]any{"period": "daily"}, "invalid-query")

	var quarterly reviewResult
	for _, review := range listed.Reviews {
		if review.Period == "quarterly" && review.Superseded {
			d.must("review.get", map[string]any{"id": review.ID}, &quarterly)
		}
	}
	if quarterly.Body != "Synthetic quarterly review." || len(quarterly.RevisedBy) != 1 {
		t.Fatalf("the first quarterly review = %+v", quarterly)
	}
}

func TestAConflictedDayIsReadableAndKeepsNewWritingOutUntilItIsSettled(t *testing.T) {
	d := newDeviceFrom(t, conflictedFixture)
	before := d.file()

	var listed entriesResult
	d.must("entries.list", nil, &listed)
	for _, entry := range listed.Entries {
		if entry.Conflicted != (entry.Date == conflictedDay) {
			t.Fatalf("entry %s conflicted = %v", entry.Date, entry.Conflicted)
		}
	}
	var entry entryResult
	d.must("entry.get", map[string]any{"date": conflictedDay}, &entry)
	if !entry.Conflicted || !strings.Contains(entry.Raw, "from the phone") {
		t.Fatalf("the conflicted day reads %+v", entry)
	}

	var open conflictsResult
	d.must("conflicts.list", nil, &open)
	if len(open.Conflicts) != 1 || len(open.FocusDisputes) != 1 {
		t.Fatalf("conflicts = %+v", open)
	}
	conflict := open.Conflicts[0]
	if conflict.Date != conflictedDay || !conflict.Base.Deleted || !strings.Contains(conflict.Ours.Content, "from the phone") ||
		len(conflict.Theirs) != 1 || !strings.Contains(conflict.Theirs[0].Content, "from the Mac") {
		t.Fatalf("conflict = %+v", conflict)
	}

	// Writing for the day is refused, write-free, until the author chooses.
	prepared := d.prepare(conflictedDay)
	save := saveParams(prepared.Draft, "", "Typed while the day was conflicted.", "", "")
	refusal := d.refused("reflection.save", "conflicted-save", save, "conflicted")
	if refusal.Details["date"] != conflictedDay {
		t.Fatalf("refusal = %+v", refusal)
	}
	if d.file() != before {
		t.Fatal("a refused save changed the journal")
	}
	// Another day is written as usual.
	d.must("reflection.save", saveParams(d.prepare("2026-04-01").Draft, "", "An unrelated day.", "", ""), nil)

	// The author edits the competing versions into one, and the day is free.
	edited := conflict.Theirs[0].Content + "\n\n### Kept from the phone\n\nNoticed the rain too."
	var resolved syncResolved
	d.must("conflict.resolve", map[string]any{"id": conflict.ID, "content": edited}, &resolved)
	d.must("entry.get", map[string]any{"date": conflictedDay}, &entry)
	if entry.Conflicted || !strings.Contains(entry.Raw, "Noticed the rain too.") || !strings.Contains(entry.Raw, "from the Mac") {
		t.Fatalf("after the edit the day reads %+v", entry)
	}
	prepared = d.prepare(conflictedDay)
	d.must("reflection.save", saveParams(prepared.Draft, "", "Saved once the day was settled.", "", ""), nil)
	d.must("entry.get", map[string]any{"date": conflictedDay}, &entry)
	if !strings.Contains(entry.Raw, "Saved once the day was settled.") || !strings.Contains(entry.Raw, "Noticed the rain too.") {
		t.Fatalf("the save after settling reads %q", entry.Raw)
	}

	dispute := open.FocusDisputes[0]
	var change focusChange
	d.must("focus_dispute.resolve", map[string]any{"id": dispute.ID, "record": dispute.Candidates[0].Record}, &change)
	d.must("conflicts.list", nil, &open)
	if len(open.Conflicts) != 0 || len(open.FocusDisputes) != 0 {
		t.Fatalf("conflicts after settling = %+v", open)
	}
}
