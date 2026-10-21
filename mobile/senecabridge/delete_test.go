package senecabridge

import "testing"

func TestDeleteEntryIsConditionalAndReplaySafe(t *testing.T) {
	d := newDevice(t, true)
	const date = "2026-03-05"
	var entry entryResult
	d.must("entry.get", map[string]any{"date": date}, &entry)
	before := d.file()
	for _, base := range []string{"", "absent"} {
		d.refused("entry.delete", d.id(), deleteEntryParams{Date: date, Base: base}, "missing-base")
	}
	d.refused("entry.delete", d.id(), deleteEntryParams{Date: "bad", Base: entry.Revision}, "invalid-date")
	d.refused("entry.delete", d.id(), deleteEntryParams{Date: date, Base: "stale"}, "stale-reflection")
	if d.file() != before {
		t.Fatal("refusal changed the journal")
	}
	params := deleteEntryParams{Date: date, Base: entry.Revision}
	id := d.id()
	first := d.send("entry.delete", id, params)
	if !first.OK {
		t.Fatalf("delete failed: %+v", first.Error)
	}
	d.refused("entry.get", "", map[string]any{"date": date}, "not-found")
	replay := d.send("entry.delete", id, params)
	if !replay.OK || !replay.Replayed {
		t.Fatalf("delete was not replayed: %+v", replay)
	}
	// Even after a replacement is written, replaying the original delete must
	// never remove it.
	draft := d.prepare(date)
	d.must("reflection.save", saveParams(draft.Draft, "", "Replacement.", "", ""), nil)
	replacement := d.file()
	replay = d.send("entry.delete", id, params)
	if !replay.OK || !replay.Replayed || d.file() != replacement {
		t.Fatal("replayed delete removed a replacement")
	}
}
