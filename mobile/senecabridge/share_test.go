package senecabridge

import (
	"reflect"
	"strings"
	"testing"
)

func TestASharePacketIsTheReviewAloneUnlessDaysAreChosen(t *testing.T) {
	d := newDevice(t, true)
	before := d.file()
	var reviews reviewsResult
	d.must("reviews.list", map[string]any{"period": "weekly"}, &reviews)
	review := reviews.Reviews[0]

	var alone sharePacketResult
	d.must("share.packet", map[string]any{"review_id": review.ID}, &alone)
	if alone.Title != review.Title || alone.ReviewID != review.ID || len(alone.Dates) != 0 {
		t.Errorf("review alone: %+v", alone)
	}
	if strings.Contains(alone.Markdown, "## 2026-03-0") {
		t.Errorf("a review alone holds no entry:\n%s", alone.Markdown)
	}

	var with sharePacketResult
	d.must("share.packet", map[string]any{"review_id": review.ID, "dates": []string{"2026-03-05", "2026-03-04"}}, &with)
	if !reflect.DeepEqual(with.Dates, []string{"2026-03-04", "2026-03-05"}) {
		t.Errorf("dates %v", with.Dates)
	}
	for _, want := range []string{"# " + review.Title, "## 2026-03-04", "## 2026-03-05", "### Follow-through\n> Finish the draft before lunch."} {
		if !strings.Contains(with.Markdown, want) {
			t.Errorf("expected %q in:\n%s", want, with.Markdown)
		}
	}
	// Written for a reader: no marker, no record, no file.
	for _, never := range []string{"<!--", review.ID, d.dir} {
		if strings.Contains(with.Markdown, never) {
			t.Errorf("the packet carries %q:\n%s", never, with.Markdown)
		}
	}
	if d.file() != before {
		t.Fatal("share.packet wrote to the journal")
	}
}

func TestASharePacketRefusesWhatTheJournalDoesNotHold(t *testing.T) {
	d := newDevice(t, true)
	d.refused("share.packet", "", map[string]any{}, "invalid-query")
	d.refused("share.packet", "", map[string]any{"dates": []string{"March"}}, "invalid-query")
	d.refused("share.packet", "", map[string]any{"dates": []string{"2026-03-06"}}, "not-found")
	d.refused("share.packet", "", map[string]any{"review_id": "0000"}, "not-found")
}
