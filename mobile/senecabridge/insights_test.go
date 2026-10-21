package senecabridge

import (
	"os"
	"reflect"
	"testing"
)

func TestInsightsAreTheEnginesReportOfTheWindow(t *testing.T) {
	d := newDevice(t, true)
	before := d.file()

	var report insightsResult
	d.must("insights.get", map[string]any{"from": "2026-03-01", "to": "2026-03-31"}, &report)
	if report.From != "2026-03-01" || report.To != "2026-03-31" {
		t.Errorf("window %s to %s", report.From, report.To)
	}
	// The 4th's intention is answered by the 5th; the 5th's is still open.
	// The legacy days ask other questions and carry none.
	statuses := map[string]string{}
	for _, intention := range report.Intentions {
		statuses[intention.Date] = intention.Status
	}
	if !reflect.DeepEqual(statuses, map[string]string{"2026-03-04": "answered", "2026-03-05": "open"}) ||
		report.Answered != 1 || report.Open != 1 || report.Tracked != "2026-03-04" {
		t.Errorf("intentions %+v", report)
	}
	if report.Intentions[0].AnsweredOn != "2026-03-05" || !reflect.DeepEqual(report.Intentions[0].CarriedOn, []string{"2026-03-05"}) {
		t.Errorf("the answer: %+v", report.Intentions[0])
	}
	var events []string
	for _, span := range report.Focus {
		events = append(events, span.Event+" "+span.From+" "+span.Until)
	}
	want := []string{"set 2026-03-01 2026-03-08", "set 2026-03-08 2026-03-15", "clear 2026-03-15 2026-03-16", "set 2026-03-16 "}
	if !reflect.DeepEqual(events, want) {
		t.Errorf("focus spans %q, want %q", events, want)
	}
	if d.file() != before {
		t.Fatal("insights.get wrote to the journal")
	}

	// Thirty days ending on the journal's today, by default.
	var recent insightsResult
	d.must("insights.get", nil, &recent)
	if recent.From != "2026-03-04" || recent.To != "2026-04-02" {
		t.Errorf("default window %s to %s", recent.From, recent.To)
	}
}

func TestInsightsWithoutAReadableArchiveStillReportTheIntentions(t *testing.T) {
	d := newDevice(t, true)
	if err := os.WriteFile(d.dir+"/seneca-reviews.md", []byte("## Focus set — 2026-03-01\n<!-- seneca:focus v=3 torn"), 0o644); err != nil {
		t.Fatal(err)
	}
	var report insightsResult
	d.must("insights.get", map[string]any{"from": "2026-03-01"}, &report)
	if report.FocusProblem == "" || len(report.Focus) != 0 || len(report.Intentions) != 2 {
		t.Errorf("report %+v", report)
	}
}

func TestInsightsRefuseAWindowThatIsNotOne(t *testing.T) {
	d := newDevice(t, true)
	for _, params := range []map[string]any{
		{"days": 7, "from": "2026-03-01"},
		{"days": -1},
		{"from": "2026-03-10", "to": "2026-03-01"},
		{"from": "March"},
	} {
		d.refused("insights.get", "", params, "invalid-query")
	}
}
