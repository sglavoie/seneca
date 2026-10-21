package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Round-trip preservation over the synthetic journal: what Seneca reads and
// writes back must be the bytes the author left, apart from the one entry or
// the one appended record a command is asked to change.

func TestCompatReflectionsFixtureRoundTripsByteForByte(t *testing.T) {
	t.Parallel()

	source := compatFixture(t, "journal/reflections.md")
	doc := engine.ParseReflectionDocument(source)

	if got := doc.Render(); got != source {
		t.Fatalf("expected parse then render to reproduce the journal byte for byte\n--- got ---\n%s\n--- want ---\n%s", got, source)
	}
	if len(doc.Diagnostics) != 0 {
		t.Fatalf("expected the fixture journal to parse clean, got %v", doc.Diagnostics)
	}
	if err := engine.ValidateReflectionDocument(engine.ReflectionsFileLabel, "reflections.md", doc); err != nil {
		t.Fatalf("expected the fixture journal to stay writable: %v", err)
	}

	var dates []string
	for _, entry := range doc.Entries {
		dates = append(dates, entry.Date)
	}
	if want := []string{"2026-03-02", "2026-03-03", "2026-03-04", "2026-03-05"}; !equalStrings(dates, want) {
		t.Fatalf("entry dates = %v, want %v", dates, want)
	}

	// Legacy entries carry no Seneca metadata and are not given any by a read.
	for _, legacy := range doc.Entries[:2] {
		if legacy.Metadata.Present {
			t.Errorf("%s: expected a legacy entry to carry no entry marker", legacy.Date)
		}
	}
	for _, current := range doc.Entries[2:] {
		if !current.Metadata.Present || current.Metadata.Version != engine.ReflectionSchemaVersion {
			t.Errorf("%s: expected a v%d entry marker, got %+v", current.Date, engine.ReflectionSchemaVersion, current.Metadata)
		}
	}
}

func TestCompatReflectionsFixtureKeepsMarkdownSenecaDoesNotOwn(t *testing.T) {
	t.Parallel()

	doc := engine.ParseReflectionDocument(compatFixture(t, "journal/reflections.md"))

	// Each of these is something no configured question names. All of it has
	// to still be there, in the entry it was written in.
	owned := map[string][]string{
		"2026-03-02": {"### Dream log\nA long staircase, counted twice."},
		"2026-03-03": {
			"A line of preamble prose the author typed above the first question.",
			"<!-- an ordinary HTML comment Seneca does not own -->",
			"Synthetic: answered messages during the quiet minutes ---",
			"- a list item\n- another list item",
			"<!-- seneca:mood value=steady -->",
			"#### A deeper heading inside an answer\nStill part of the answer above.",
		},
		"2026-03-05": {"### Gratitude\nSynthetic: a section no configured question names."},
	}
	for date, fragments := range owned {
		idx, found := doc.Find(date)
		if !found {
			t.Fatalf("fixture entry %s is missing", date)
		}
		for _, fragment := range fragments {
			if !strings.Contains(doc.Entries[idx].RenderRaw(), fragment) {
				t.Errorf("%s: expected %q preserved", date, fragment)
			}
		}
	}

	// The legacy answers are still readable under the questions they were
	// written for, and say nothing under questions written since.
	legacy := doc.Entries[0]
	if got := legacy.Answers(engine.DefaultReflectionQuestions())[2]; got != "Start with ten quiet minutes." {
		t.Errorf("legacy answer under its own question = %q", got)
	}
	for i, answer := range legacy.Answers(compatQuestions) {
		if answer != "" {
			t.Errorf("expected no legacy answer under reworded question %d, got %q", i+1, answer)
		}
	}
}

func TestCompatFollowThroughSnapshotSurvivesTheSourceEntryChanging(t *testing.T) {
	t.Parallel()

	doc := engine.ParseReflectionDocument(compatFixture(t, "journal/reflections.md"))
	idx, _ := doc.Find("2026-03-05")

	stored, ok := doc.Entries[idx].FollowThroughSection()
	if !ok {
		t.Fatal("expected the 2026-03-05 entry to carry a follow-through")
	}
	if stored.SourceDate != "2026-03-04" || stored.SourceIntention != "Finish the draft before lunch." ||
		stored.Response != "Synthetic: the draft was done by eleven." {
		t.Fatalf("unexpected follow-through snapshot: %+v", stored)
	}
	// The anchor is found by its role marker, not by the question text.
	if got := doc.Entries[idx].AnchorAnswer([]string{"Reworded?", "Reworded too?", "Entirely new wording?"}, 3); got != "Walk before the dip, not after." {
		t.Fatalf("anchor answer under reworded questions = %q", got)
	}

	// Rewriting the day it answers must not reach into the stored snapshot.
	entries := append([]engine.ReflectionEntry(nil), doc.Entries...)
	source, _ := engine.FindEntry(entries, "2026-03-04")
	entries[source].ApplyAnswers(compatQuestions, []string{"Changed.", "Changed.", "A different intention."}, 3)
	entries[source].RebuildRaw(engine.RenderCanonicalV2)

	ctx, err := engine.ContinuityContextFor(entries, filepath.Join(t.TempDir(), engine.ArchiveFileName), "2026-03-05", compatQuestions, 3, false)
	if err != nil {
		t.Fatalf("continuityContextFor returned error: %v", err)
	}
	if ctx.PriorDate != "2026-03-04" || ctx.PriorText != "Finish the draft before lunch." {
		t.Fatalf("expected the stored snapshot, not a recomputed one, got %+v", ctx)
	}
}

// A journal written before the framing was settled keeps a blank line ahead of
// each separator. The entries are the same entries; only the punctuation
// between them is Seneca's, and the first whole-file write settles it.
func TestCompatLegacyFramingIsNormalizedOnceAndOnlyBetweenEntries(t *testing.T) {
	canonical := compatFixture(t, "journal/reflections.md")
	legacy := compatFixture(t, "legacy-framing.md")
	if legacy == canonical {
		t.Fatal("the legacy-framing fixture no longer differs from the canonical journal")
	}

	want := engine.ParseReflectionDocument(canonical).Entries
	got := engine.ParseReflectionDocument(legacy).Entries
	if len(got) != len(want) {
		t.Fatalf("expected %d entries under legacy framing, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i].RenderRaw() != want[i].RenderRaw() {
			t.Errorf("%s: framing changed the entry's own bytes", want[i].Date)
		}
	}

	// Sorting an already ordered journal is the smallest whole-file write: it
	// changes the framing and nothing else, and a second run changes nothing.
	j := newCompatJournal(t)
	j.write(t, j.reflections, legacy)
	if err := runSort(nil, nil); err != nil {
		t.Fatalf("runSort returned error: %v", err)
	}
	if rewritten := readFileString(t, j.reflections); rewritten != canonical {
		t.Fatalf("expected the first rewrite to settle on canonical framing:\n%s", rewritten)
	}
	if backup := readFileString(t, j.reflections+engine.BackupSuffix); backup != legacy {
		t.Fatalf("expected the backup to hold the legacy-framed journal")
	}

	settled := snapshotDir(t, j.dir)
	if err := runSort(nil, nil); err != nil {
		t.Fatalf("second runSort returned error: %v", err)
	}
	if after := snapshotDir(t, j.dir); !equalStrings(settled, after) {
		t.Fatalf("expected a second sort to change nothing:\nbefore %q\nafter  %q", settled, after)
	}
}

// compatSave runs the daily read-modify-write against the fixture copy exactly
// as runReflection does after the form closes.
func compatSave(t *testing.T, j compatJournal, date string, answers []string, response string) {
	t.Helper()

	tx, err := journalFS.OpenReflectionFile(j.reflections)
	if err != nil {
		t.Fatalf("openReflectionFile returned error: %v", err)
	}
	if err := tx.Validate(); err != nil {
		t.Fatalf("validate returned error: %v", err)
	}
	ctx, err := engine.ContinuityContextFor(tx.Entries(), j.archive, date, compatQuestions, 3, false)
	if err != nil {
		t.Fatalf("continuityContextFor returned error: %v", err)
	}
	entries, _, err := engine.ApplyReflectionResult(tx.Entries(), engine.ReflectionSave{
		Label:               tx.Label,
		Path:                j.reflections,
		Date:                date,
		Questions:           compatQuestions,
		AnchorQuestionIndex: 3,
		Context:             ctx,
		Result:              engine.ReflectionResult{Saved: true, Answers: answers, FollowThrough: response},
	})
	if err != nil {
		t.Fatalf("applyReflectionResult returned error: %v", err)
	}
	if err := tx.CommitEntries(entries); err != nil {
		t.Fatalf("commitEntries returned error: %v", err)
	}
}

func TestCompatDailyEditRewritesOnlyItsOwnEntry(t *testing.T) {
	j := newCompatJournal(t)
	before := engine.ParseReflectionDocument(compatFixture(t, "journal/reflections.md"))

	compatSave(t, j, "2026-03-05",
		[]string{"Synthetic: edited.", "Synthetic: took the walk anyway.", "Walk before the dip, not after."},
		"Synthetic: the draft was done by eleven.")

	after := engine.ParseReflectionDocument(readFileString(t, j.reflections))
	if len(after.Entries) != len(before.Entries) {
		t.Fatalf("expected %d entries after an edit, got %d", len(before.Entries), len(after.Entries))
	}
	for i, entry := range before.Entries {
		if entry.Date == "2026-03-05" {
			continue
		}
		if after.Entries[i].RenderRaw() != entry.RenderRaw() {
			t.Errorf("%s: an edit to another day changed this entry's bytes", entry.Date)
		}
	}

	edited := after.Entries[3].RenderRaw()
	for _, want := range []string{
		"Synthetic: edited.",
		"<!-- seneca:follow-through source=2026-03-04 -->",
		"> Finish the draft before lunch.",
		"<!-- seneca:role name=anchor -->\n### What will I carry into tomorrow?",
		"### Gratitude\nSynthetic: a section no configured question names.",
	} {
		if !strings.Contains(edited, want) {
			t.Errorf("expected the edited entry to keep %q:\n%s", want, edited)
		}
	}

	// The one backup holds exactly what the edit replaced.
	if got := readFileString(t, j.reflections+engine.BackupSuffix); got != compatFixture(t, "journal/reflections.md") {
		t.Errorf("expected the backup to hold the pre-edit journal, got:\n%s", got)
	}
}

func TestCompatNewDayIsAppendedAfterUntouchedBytes(t *testing.T) {
	j := newCompatJournal(t)
	original := compatFixture(t, "journal/reflections.md")

	compatSave(t, j, "2026-03-06", []string{"Synthetic: a new day.", "Synthetic: handled.", "Keep the noon walk."}, "Synthetic: walked at noon.")

	got := readFileString(t, j.reflections)
	if !strings.HasPrefix(got, original) {
		t.Fatalf("expected the existing journal to remain a byte-identical prefix:\n%s", got)
	}
	added := strings.TrimPrefix(got, original)
	for _, want := range []string{
		"## 2026-03-06\n<!-- seneca:entry v=2 -->",
		"<!-- seneca:follow-through source=2026-03-05 -->\n### Follow-through\n> Walk before the dip, not after.\n\nSynthetic: walked at noon.",
		"<!-- seneca:role name=anchor -->\n### What will I carry into tomorrow?\nKeep the noon walk.",
	} {
		if !strings.Contains(added, want) {
			t.Errorf("expected the new entry to contain %q:\n%s", want, added)
		}
	}
}

func TestCompatSortLeavesAnOrderedJournalAlone(t *testing.T) {
	j := newCompatJournal(t)
	before := snapshotDir(t, j.dir)

	if err := runSort(nil, nil); err != nil {
		t.Fatalf("runSort returned error: %v", err)
	}
	j.requireUntouched(t, before)
}

func TestCompatSortReordersWithoutChangingAnEntry(t *testing.T) {
	j := newCompatJournal(t)
	ordered := compatFixture(t, "journal/reflections.md")

	entries := engine.ParseReflectionDocument(ordered).Entries
	reversed := make([]engine.ReflectionEntry, 0, len(entries))
	for _, entry := range slices.Backward(entries) {
		reversed = append(reversed, entry)
	}
	shuffled := engine.RenderReflectionEntries(reversed)
	j.write(t, j.reflections, shuffled)

	if err := runSort(nil, nil); err != nil {
		t.Fatalf("runSort returned error: %v", err)
	}
	if got := readFileString(t, j.reflections); got != ordered {
		t.Fatalf("expected sort to restore the ordered journal byte for byte:\n%s", got)
	}
	if got := readFileString(t, j.reflections+engine.BackupSuffix); got != shuffled {
		t.Fatalf("expected the backup to hold the pre-sort journal:\n%s", got)
	}
}

func TestCompatMergeCarriesEveryEntryByteForByte(t *testing.T) {
	j := newCompatJournal(t)
	incoming := filepath.Join(j.dir, "incoming.md")
	j.write(t, incoming, compatFixture(t, "journal/incoming.md"))

	previous := deleteSource
	deleteSource = false
	defer func() { deleteSource = previous }()

	if err := runMerge(nil, []string{incoming}); err != nil {
		t.Fatalf("runMerge returned error: %v", err)
	}

	merged := engine.ParseReflectionDocument(readFileString(t, j.reflections))
	var dates []string
	raws := map[string]string{}
	for _, entry := range merged.Entries {
		dates = append(dates, entry.Date)
		raws[entry.Date] = entry.RenderRaw()
	}
	want := []string{"2026-03-01", "2026-03-02", "2026-03-03", "2026-03-04", "2026-03-05", "2026-03-06"}
	if !equalStrings(dates, want) {
		t.Fatalf("merged dates = %v, want %v", dates, want)
	}
	for _, name := range []string{"journal/reflections.md", "journal/incoming.md"} {
		for _, entry := range engine.ParseReflectionDocument(compatFixture(t, name)).Entries {
			if raws[entry.Date] != entry.RenderRaw() {
				t.Errorf("%s: merge changed the bytes of the entry from %s", entry.Date, name)
			}
		}
	}

	// Without --delete the source is the author's to remove.
	if got := readFileString(t, incoming); got != compatFixture(t, "journal/incoming.md") {
		t.Errorf("expected the merge source left untouched, got:\n%s", got)
	}
	// The archive is not part of a merge.
	if got := readFileString(t, j.archive); got != compatFixture(t, "journal/seneca-reviews.md") {
		t.Errorf("expected the archive untouched by a merge")
	}
}

func TestCompatMergeRefusesAnOverlappingDateAndWritesNothing(t *testing.T) {
	j := newCompatJournal(t)
	incoming := filepath.Join(j.dir, "incoming.md")
	j.write(t, incoming, "## 2026-03-04\n\n### What did I notice today?\nSynthetic: a competing version of the same day.\n\n---\n\n")
	before := snapshotDir(t, j.dir)

	err := runMerge(nil, []string{incoming})
	if err == nil || !strings.Contains(err.Error(), "2026-03-04") {
		t.Fatalf("expected the overlapping date to be refused by name, got %v", err)
	}
	j.requireUntouched(t, before)
}

func TestCompatArchiveFixtureRoundTripsByteForByte(t *testing.T) {
	t.Parallel()

	source := compatFixture(t, "journal/seneca-reviews.md")
	archive := engine.ParseReviewArchive(source)

	if got := archive.Render(); got != source {
		t.Fatalf("expected parse then render to reproduce the archive byte for byte\n--- got ---\n%s\n--- want ---\n%s", got, source)
	}
	if diagnostics := archive.MutationDiagnostics(); len(diagnostics) != 0 {
		t.Fatalf("expected the fixture archive to stay appendable, got %v", diagnostics)
	}
	if !archive.HasMarker || !archive.HeaderVersionOK || archive.HeaderVersion != engine.ArchiveSchemaVersion {
		t.Fatalf("expected a v%d archive header, got marker=%v version=%d", engine.ArchiveSchemaVersion, archive.HasMarker, archive.HeaderVersion)
	}
	if !strings.Contains(archive.Header, "A note the author typed into the header. Seneca keeps it.") {
		t.Fatalf("expected the author's header prose preserved:\n%s", archive.Header)
	}

	kinds := map[engine.ArchiveRecordKind]int{}
	for i, record := range archive.Records {
		kinds[record.Kind]++
		// Every record Seneca wrote still carries the identity it was given;
		// these IDs are what sync will address records by.
		if want, ok := record.ExpectedRecordID(); ok && record.RecordID() != want {
			t.Errorf("record %d: id %s does not match its content", i, engine.ShortRecordID(record.RecordID()))
		}
	}
	if kinds[engine.ArchiveRecordReview] != 3 || kinds[engine.ArchiveRecordFocus] != 4 || kinds[engine.ArchiveRecordUnknown] != 1 {
		t.Fatalf("record kinds = %v, want 3 reviews, 4 focus events, 1 unknown", kinds)
	}

	reviews := archive.ReviewRecords()
	if reviews[0].Period != engine.ReviewPeriodMonthly || reviews[1].Revises != reviews[2].ID {
		t.Fatalf("expected the monthly review newest and the weekly revision linked to its original, got %+v", reviews)
	}
	if !strings.Contains(reviews[2].Body, "## A heading inside a review body") || !strings.Contains(reviews[2].Body, "Neither does a rule.") {
		t.Fatalf("expected a review body to keep its own headings and rules:\n%s", reviews[2].Body)
	}
}

func TestCompatFocusTimelineResolvesFromTheArchiveFixture(t *testing.T) {
	t.Parallel()

	archive := engine.ParseReviewArchive(compatFixture(t, "journal/seneca-reviews.md"))
	weekly := archive.ReviewRecords()[2]

	cases := []struct {
		date, text string
		origin     engine.FocusOrigin
		source     string
	}{
		{date: "2026-02-28"},
		{date: "2026-03-01", text: "Protect the first hour.", origin: engine.FocusOriginManual},
		{date: "2026-03-07", text: "Protect the first hour.", origin: engine.FocusOriginManual},
		{date: "2026-03-08", text: "Walk before the dip.", origin: engine.FocusOriginReview, source: weekly.ID},
		{date: "2026-03-15"},
		{date: "2026-03-16", text: "Finish before starting.", origin: engine.FocusOriginManual},
		{date: "2026-12-31", text: "Finish before starting.", origin: engine.FocusOriginManual},
	}
	for _, tc := range cases {
		state := archive.FocusOn(tc.date)
		if state.Active != (tc.text != "") || state.Text != tc.text || state.Origin != tc.origin || state.Source != tc.source {
			t.Errorf("focus on %s = %+v, want text %q origin %q source %q", tc.date, state, tc.text, tc.origin, tc.source)
		}
	}
}

func TestCompatArchiveAppendKeepsEveryExistingByte(t *testing.T) {
	j := newCompatJournal(t)
	original := compatFixture(t, "journal/seneca-reviews.md")

	var commits []string
	previous := commitArchivePaths
	defer func() { commitArchivePaths = previous }()
	commitArchivePaths = func(message string, paths ...string) {
		commits = append(commits, message+" :: "+strings.Join(paths, ","))
	}

	now := time.Date(2026, time.April, 1, 9, 0, 0, 0, time.UTC)
	if err := setFocus(&strings.Builder{}, j.archive, now, []string{"A", "new", "focus."}); err != nil {
		t.Fatalf("setFocus returned error: %v", err)
	}

	got := readFileString(t, j.archive)
	if !strings.HasPrefix(got, original) {
		t.Fatalf("expected the existing archive to remain a byte-identical prefix:\n%s", got)
	}
	added := engine.ParseReviewArchive(got).Records
	if last := added[len(added)-1]; last.Focus == nil || last.Focus.Text != "A new focus." || last.Focus.Effective != "2026-04-01" {
		t.Fatalf("expected one appended focus event, got %+v", last)
	}
	if want := []string{"seneca: focus set 2026-04-01 :: " + j.archive}; !equalStrings(commits, want) {
		t.Fatalf("commits = %v, want %v", commits, want)
	}
	if got := readFileString(t, j.archive+engine.BackupSuffix); got != original {
		t.Fatalf("expected the backup to hold the pre-append archive")
	}
	// A focus change is not a journal change.
	if got := readFileString(t, j.reflections); got != compatFixture(t, "journal/reflections.md") {
		t.Fatalf("expected the journal untouched by a focus change")
	}
}

func TestCompatLegacyArchiveUpgradesOnlyItsMarkerLine(t *testing.T) {
	t.Parallel()

	legacy := compatFixture(t, "legacy-archive-v2.md")
	path := filepath.Join(t.TempDir(), engine.ArchiveFileName)
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatalf("seeding the legacy archive: %v", err)
	}

	// A read understands the older schema as it stands.
	parsed := engine.ParseReviewArchive(legacy)
	if len(parsed.Diagnostics) != 0 || parsed.HeaderVersion != 2 {
		t.Fatalf("expected a clean v2 archive, got version %d and %v", parsed.HeaderVersion, parsed.Diagnostics)
	}
	if state := parsed.FocusOn("2026-03-10"); state.Text != "Protect the first hour." {
		t.Fatalf("expected the v2 focus to resolve, got %+v", state)
	}

	mustAppend(t, path, engine.NewFocusClearRecord(time.Date(2026, time.March, 20, 9, 0, 0, 0, time.UTC)))

	got := readFileString(t, path)
	want := strings.Replace(legacy, "<!-- seneca:archive v=2 -->", "<!-- seneca:archive v=3 -->", 1)
	if !strings.HasPrefix(got, want) {
		t.Fatalf("expected only the header marker line to change ahead of the appended record:\n%s", got)
	}
	// The records a v2 binary wrote keep their own v=2 markers forever.
	if strings.Count(got, " v=2 ") != 2 {
		t.Fatalf("expected both v2 record markers left as written:\n%s", got)
	}
}
