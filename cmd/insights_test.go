package cmd

import (
	"strings"
	"testing"
)

// The insights report reads the journal and the archive and writes nothing.
// Its counts are the ones `seneca show` and `seneca focus history` show by
// hand on the compatibility journal.

func TestInsightsReportTheFixtureJournalAndWriteNothing(t *testing.T) {
	j, _ := newSettingsJournal(t)
	before := snapshotDir(t, j.dir)

	out, err := runCompatCLI(t, "insights", "--all")
	if err != nil {
		t.Fatalf("seneca insights returned error: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Follow-through insights, 2026-03-02 to ",
		"(the whole journal)",
		// The 4th's intention is answered by the 5th's follow-through; the
		// 5th's is open. The legacy days ask other questions and carry none.
		"Intentions: 2\n",
		"  answered       1 ",
		"  open           1 ",
		"2026-03-05  open         Walk before the dip, not after.",
		// The four focus events, paired as `seneca focus history` pairs them.
		"2026-03-01 → 2026-03-08",
		"2026-03-08 → 2026-03-15",
		"(then cleared)",
		"2026-03-15 → 2026-03-16",
		"no focus",
		"2026-03-16 → current",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Finish the draft before lunch.") {
		t.Errorf("an answered intention is counted, not listed:\n%s", out)
	}
	if after := snapshotDir(t, j.dir); !equalStrings(before, after) {
		t.Fatalf("insights wrote to the journal: %q", after)
	}
}

func TestInsightsWindowLeavesOutWhatFellOutsideIt(t *testing.T) {
	newSettingsJournal(t)

	out, err := runCompatCLI(t, "insights", "--from", "2026-03-05", "--to", "2026-03-07")
	if err != nil {
		t.Fatalf("seneca insights returned error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Follow-through insights, 2026-03-05 to 2026-03-07\n") || !strings.Contains(out, "Intentions: 1\n") {
		t.Errorf("expected only the 5th's intention:\n%s", out)
	}
	if !strings.Contains(out, "2026-03-01 → 2026-03-08") || strings.Contains(out, "2026-03-08 → 2026-03-15") {
		t.Errorf("expected only the focus that stood from the 5th to the 7th:\n%s", out)
	}

	// The focus replaced on the 8th did not stand on the 8th.
	out, err = runCompatCLI(t, "insights", "--from", "2026-03-08", "--to", "2026-03-10")
	if err != nil {
		t.Fatalf("seneca insights returned error: %v\n%s", err, out)
	}
	if strings.Contains(out, "2026-03-01 → 2026-03-08") || !strings.Contains(out, "2026-03-08 → 2026-03-15") {
		t.Errorf("expected the span from the 8th alone:\n%s", out)
	}
}

func TestInsightsRefuseTwoWindows(t *testing.T) {
	newSettingsJournal(t)

	for _, args := range [][]string{
		{"--all", "--days", "7"},
		{"--days", "7", "--from", "2026-03-01"},
		{"--days", "0"},
		{"--from", "2026-03-10", "--to", "2026-03-01"},
	} {
		out, err := runCompatCLI(t, append([]string{"insights"}, args...)...)
		if err == nil {
			t.Errorf("seneca insights %v: expected a refusal, got:\n%s", args, out)
		}
	}
}
