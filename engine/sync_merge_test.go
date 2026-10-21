package engine

import (
	"strings"
	"testing"
)

// entryOf builds an entry from its heading and parts, joined the way Seneca
// writes one.
func entryOf(parts ...string) string {
	return strings.Join(parts, "\n\n")
}

const (
	mergeDay     = "## 2026-08-11"
	mergeNotice  = "### Notice?\nAgain."
	mergeHandle  = "### Handle?\nSlowly."
	mergeCarry   = "<!-- seneca:role name=anchor -->\n### Carry?\nKeep going."
	mergeFollow  = "<!-- seneca:follow-through source=2026-08-10 -->\n### Follow-through\n> Start before nine.\n\nStarted at ten."
	mergeUnknown = "### A heading of the author's own\n<!-- seneca:mood v=1 level=4 -->\nSomething seneca does not ask about."
)

func TestMergeEntryTakesEachIndependentlyChangedSection(t *testing.T) {
	t.Parallel()

	base := entryOf(mergeDay, mergeFollow, mergeNotice, mergeHandle, mergeCarry)
	ours := entryOf(mergeDay, mergeFollow, "### Notice?\nAgain, and earlier.", mergeHandle, mergeCarry)
	theirs := entryOf(mergeDay, mergeFollow, mergeNotice, mergeHandle, "<!-- seneca:role name=anchor -->\n### Carry?\nStop at six.")
	want := entryOf(mergeDay, mergeFollow, "### Notice?\nAgain, and earlier.", mergeHandle, "<!-- seneca:role name=anchor -->\n### Carry?\nStop at six.")

	for name, sides := range map[string][2]string{"ours first": {ours, theirs}, "theirs first": {theirs, ours}} {
		merged, conflict := mergeEntry(base, sides[0], sides[1])
		if conflict != "" || merged != want {
			t.Fatalf("%s: expected both changes, got conflict %q and\n%s", name, conflict, merged)
		}
	}
	entry := NewReflectionEntry(want)
	if len(entry.Diagnostics) > 0 || entry.AnchorAnswer(nil, 0) != "Stop at six." {
		t.Fatalf("the merged entry lost the anchor's marker: %+v", entry.Diagnostics)
	}
}

func TestMergeEntryKeepsAnAnswerAndItsMarkersTogether(t *testing.T) {
	t.Parallel()

	base := entryOf(mergeDay, mergeFollow, mergeNotice, mergeCarry)
	// One side answers the follow-through; the other moves the anchor role to
	// another section, which changes two sections and nothing inside either.
	ours := entryOf(mergeDay, "<!-- seneca:follow-through source=2026-08-10 -->\n### Follow-through\n> Start before nine.\n\nStarted at nine, for once.", mergeNotice, mergeCarry)
	theirs := entryOf(mergeDay, mergeFollow, "<!-- seneca:role name=anchor -->\n### Notice?\nAgain.", "### Carry?\nKeep going.")

	merged, conflict := mergeEntry(base, ours, theirs)
	if conflict != "" {
		t.Fatalf("expected a merge, got conflict %q", conflict)
	}
	entry := NewReflectionEntry(merged)
	follow, ok := entry.FollowThroughSection()
	if !ok || follow.SourceDate != "2026-08-10" || follow.SourceIntention != "Start before nine." || follow.Response != "Started at nine, for once." {
		t.Fatalf("the follow-through did not stay one unit: %+v", follow)
	}
	if entry.AnchorAnswer(nil, 0) != "Again." || strings.Count(merged, "seneca:role") != 1 {
		t.Fatalf("the anchor role did not travel with its section:\n%s", merged)
	}

	// A follow-through is one unit: its snapshot and its response are never
	// taken from different devices.
	snapshot := entryOf(mergeDay, "<!-- seneca:follow-through source=2026-08-10 -->\n### Follow-through\n> Start before eight.\n\nStarted at ten.", mergeNotice, mergeCarry)
	if merged, conflict := mergeEntry(base, ours, snapshot); conflict == "" {
		t.Fatalf("expected a conflict over the follow-through, got\n%s", merged)
	}
}

func TestMergeEntryPreservesWhatSenecaDoesNotKnow(t *testing.T) {
	t.Parallel()

	head := mergeDay + "\n<!-- seneca:entry v=2 created=2026-08-11T21:00:00Z flavor=plain -->\n\nA line the author wrote above the questions."
	base := entryOf(head, mergeNotice, mergeUnknown, mergeCarry)
	ours := entryOf(head, "### Notice?\nAgain, and earlier.", mergeUnknown, mergeCarry)
	theirs := entryOf(head, mergeNotice, mergeUnknown, "<!-- seneca:role name=anchor -->\n### Carry?\nStop at six.")

	merged, conflict := mergeEntry(base, ours, theirs)
	if conflict != "" {
		t.Fatalf("expected a merge, got conflict %q", conflict)
	}
	for _, kept := range []string{head, mergeUnknown, "Again, and earlier.", "Stop at six."} {
		if !strings.Contains(merged, kept) {
			t.Errorf("the merge dropped or rewrote:\n%s\nfrom\n%s", kept, merged)
		}
	}
	if err := validateArtifactContent(entryArtifact("2026-08-11"), []byte(merged)); err != nil {
		t.Fatalf("the merged entry is not one seneca would write: %v", err)
	}
}

func TestMergeEntryAllowsOneSideToAddOrRemoveSections(t *testing.T) {
	t.Parallel()

	base := entryOf(mergeDay, mergeNotice, mergeHandle, mergeCarry)
	added := entryOf(mergeDay, mergeNotice, mergeHandle, mergeCarry, mergeUnknown)
	edited := entryOf(mergeDay, "### Notice?\nAgain, and earlier.", mergeHandle, mergeCarry)
	if merged, conflict := mergeEntry(base, added, edited); conflict != "" ||
		merged != entryOf(mergeDay, "### Notice?\nAgain, and earlier.", mergeHandle, mergeCarry, mergeUnknown) {
		t.Fatalf("expected the new section beside the edit, got conflict %q and\n%s", conflict, merged)
	}

	removed := entryOf(mergeDay, mergeNotice, mergeCarry)
	if merged, conflict := mergeEntry(base, edited, removed); conflict != "" ||
		merged != entryOf(mergeDay, "### Notice?\nAgain, and earlier.", mergeCarry) {
		t.Fatalf("expected the untouched section to go, got conflict %q and\n%s", conflict, merged)
	}
}

func TestMergeEntryRefusesToGuess(t *testing.T) {
	t.Parallel()

	base := entryOf(mergeDay, mergeNotice, mergeHandle, mergeCarry)
	for name, sides := range map[string][2]string{
		"both changed one section": {
			entryOf(mergeDay, "### Notice?\nOne way.", mergeHandle, mergeCarry),
			entryOf(mergeDay, "### Notice?\nAnother way.", mergeHandle, mergeCarry),
		},
		"both changed the lines above the sections": {
			entryOf(mergeDay+"\n\nA note.", mergeNotice, mergeHandle, mergeCarry),
			entryOf(mergeDay+"\n\nAnother note.", mergeNotice, mergeHandle, mergeCarry),
		},
		"a repeated heading": {
			entryOf(mergeDay, mergeNotice, mergeHandle, mergeCarry, "### Notice?\nA second answer."),
			entryOf(mergeDay, mergeNotice, "### Handle?\nFaster.", mergeCarry),
		},
		"both changed which sections there are": {
			entryOf(mergeDay, mergeNotice, mergeCarry),
			entryOf(mergeDay, mergeHandle, mergeNotice, mergeCarry),
		},
		"a section removed and changed": {
			entryOf(mergeDay, mergeNotice, mergeCarry),
			entryOf(mergeDay, mergeNotice, "### Handle?\nFaster.", mergeCarry),
		},
		"a malformed marker": {
			entryOf(mergeDay, "<!-- seneca:role -->\n### Notice?\nAgain.", mergeHandle, mergeCarry),
			entryOf(mergeDay, mergeNotice, "### Handle?\nFaster.", mergeCarry),
		},
		"other line endings": {
			strings.ReplaceAll(entryOf(mergeDay, "### Notice?\nOne way.", mergeHandle, mergeCarry), "\n", "\r\n"),
			entryOf(mergeDay, mergeNotice, "### Handle?\nFaster.", mergeCarry),
		},
	} {
		for _, order := range [][2]string{sides, {sides[1], sides[0]}} {
			if merged, conflict := mergeEntry(base, order[0], order[1]); conflict == "" {
				t.Errorf("%s: expected a conflict, got\n%s", name, merged)
			}
		}
	}
}

func TestEntriesWrittenTwiceAreOneOnlyWhenTheySayTheSame(t *testing.T) {
	t.Parallel()

	desk := entryOf(mergeDay+"\n<!-- seneca:entry v=2 created=2026-08-11T21:00:00Z -->", mergeNotice, mergeCarry)
	phone := entryOf(mergeDay+"\n<!-- seneca:entry v=2 created=2026-08-11T21:04:10Z -->", mergeNotice, mergeCarry)
	if !equivalentEntries(desk, phone) || !equivalentEntries(phone, desk) {
		t.Fatal("two entries that differ only in when they were created are not equivalent")
	}
	other := entryOf(mergeDay+"\n<!-- seneca:entry v=2 created=2026-08-11T21:04:10Z -->", "### Notice?\nAgain!", mergeCarry)
	if equivalentEntries(desk, other) {
		t.Fatal("two entries with different answers are equivalent")
	}
	if equivalentEntries(desk, entryOf(mergeDay, mergeNotice)) {
		t.Fatal("two entries with different sections are equivalent")
	}
}

func TestCoveringChangeNamesEveryVersionItSettles(t *testing.T) {
	t.Parallel()

	artifact := entryArtifact("2026-08-11")
	ours, theirs, merged := RevisionOf([]byte("ours")), RevisionOf([]byte("theirs")), RevisionOf([]byte("merged"))
	for name, tc := range map[string]struct {
		covered []Revision
		result  Revision
		base    Revision
		deleted bool
		covers  []Revision
	}{
		"something new":        {[]Revision{ours, theirs}, merged, ours, false, []Revision{ours, theirs}},
		"this device's own":    {[]Revision{ours, theirs}, ours, theirs, false, []Revision{ours, theirs}},
		"the other device's":   {[]Revision{ours, theirs}, theirs, ours, false, []Revision{ours, theirs}},
		"a deletion kept":      {[]Revision{ours, RevisionAbsent}, RevisionAbsent, ours, true, []Revision{ours, RevisionAbsent}},
		"an edit over deleted": {[]Revision{RevisionAbsent, theirs}, theirs, RevisionAbsent, false, []Revision{RevisionAbsent, theirs}},
	} {
		change, ok := coveringChange(artifact, tc.covered, tc.result)
		if !ok {
			t.Errorf("%s: expected a change", name)
			continue
		}
		if err := change.check(); err != nil {
			t.Errorf("%s: the change is not a valid one: %v", name, err)
		}
		if change.Base != tc.base || change.Deleted != tc.deleted || change.result() != tc.result {
			t.Errorf("%s: unexpected change %+v", name, change)
		}
		for _, revision := range tc.covers {
			if !change.covers(revision) {
				t.Errorf("%s: the change does not cover %s", name, revision)
			}
		}
	}
	if _, ok := coveringChange(artifact, []Revision{ours}, ours); ok {
		t.Error("expected no change when nothing but the result is covered")
	}
}
