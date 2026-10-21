package engine

import (
	"errors"
	"strings"
	"testing"
)

func TestASharePacketIsTheReviewAndTheWritingWithoutSenecasMarkers(t *testing.T) {
	entries := []ReflectionEntry{
		insightEntry("2026-10-02", "Call the bank.", "2026-10-01", "Walked twenty minutes."),
		insightEntry("2026-10-01", "Walk after lunch.", "", ""),
	}
	review := &ReviewRecord{
		ID: "9778153b0f3a", Period: ReviewPeriodWeekly, Start: "2026-09-28", End: "2026-10-04",
		Title: "Weekly review — 2026-09-28 to 2026-10-04", Body: "\nSynthetic weekly review.\n\n## Wins\n\nThe walks.\n",
	}
	selected, err := selectShareEntries(entries, []string{"2026-10-02", "2026-10-01", "2026-10-02"})
	if err != nil {
		t.Fatal(err)
	}
	packet := BuildSharePacket(review, selected, "2026-10-17")

	want := "# Weekly review — 2026-09-28 to 2026-10-04\n\n" +
		"Shared from a Seneca journal on 2026-10-17. It holds a weekly review, and the entries of 2 days: 2026-10-01, 2026-10-02.\n" +
		"\n---\n\nSynthetic weekly review.\n\n## Wins\n\nThe walks.\n" +
		"\n---\n\n## 2026-10-01\n\n### What happened?\nSynthetic day.\n\n### What will I do tomorrow?\nWalk after lunch.\n" +
		// Each entry keeps its sections in its own order.
		"\n---\n\n## 2026-10-02\n\n### What happened?\nSynthetic day.\n\n### What will I do tomorrow?\nCall the bank.\n\n### Follow-through\n> quoted\n\nWalked twenty minutes.\n"
	if packet.Markdown != want {
		t.Errorf("packet:\n%q\nwant:\n%q", packet.Markdown, want)
	}
	if strings.Contains(packet.Markdown, "<!--") || strings.Contains(packet.Markdown, review.ID) {
		t.Error("a packet carries no marker and no record identity")
	}
	if packet.FileName != "Seneca weekly review 2026-09-28 to 2026-10-04" || packet.ReviewID != review.ID {
		t.Errorf("packet %+v", packet)
	}
}

func TestASharePacketOfDaysAlone(t *testing.T) {
	entries := []ReflectionEntry{insightEntry("2026-10-01", "Walk after lunch.", "", "")}
	packet := BuildSharePacket(nil, entries, "2026-10-17")
	if packet.Title != "Reflections, 2026-10-01" || packet.FileName != "Seneca reflections 2026-10-01" {
		t.Errorf("packet %+v", packet)
	}
	if !strings.HasPrefix(packet.Markdown, "# Reflections, 2026-10-01\n\nShared from a Seneca journal on 2026-10-17. It holds the entries of 1 day: 2026-10-01.\n") {
		t.Errorf("packet:\n%s", packet.Markdown)
	}
}

func TestASharePacketNeverLeavesOutADayInSilence(t *testing.T) {
	entries := []ReflectionEntry{insightEntry("2026-10-01", "Walk after lunch.", "", "")}
	var selection *ShareSelectionError
	if _, err := selectShareEntries(entries, []string{"2026-10-01", "2026-10-03"}); !errors.As(err, &selection) || !selection.NotFound {
		t.Errorf("a day not written: %v", err)
	}
	if _, err := selectShareEntries(entries, []string{"October"}); !errors.As(err, &selection) || selection.NotFound {
		t.Errorf("not a date: %v", err)
	}
}
