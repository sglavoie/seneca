package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Shared settings are held to what two devices need of them: both read the same
// questions whatever their own configuration says, the questions and the anchor
// only ever change together, a form opened under old questions is not saved
// under new ones, and nothing already written is read differently afterwards.

// otherDevice opens the same journal the way a second device would: its own
// Journal, and a configuration of its own that disagrees with the first.
func otherDevice(t *testing.T, journal *Journal) *Journal {
	t.Helper()

	other, err := Open(Options{
		ReflectionsPath: journal.ReflectionsPath(),
		Settings:        ReflectionSettings{Questions: []string{"Only this device asks this?"}, AnchorQuestionIndex: 1},
		Clock:           journal.clock,
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	return other
}

func mustSettings(t *testing.T, journal *Journal) SettingsState {
	t.Helper()

	state, err := journal.EffectiveSettings()
	if err != nil {
		t.Fatalf("EffectiveSettings returned error: %v", err)
	}
	return state
}

func mustInitialize(t *testing.T, journal *Journal) SettingsOutcome {
	t.Helper()

	outcome, err := journal.InitializeSettings()
	if err != nil {
		t.Fatalf("InitializeSettings returned error: %v", err)
	}
	return outcome
}

func anchorAt(index int) *int { return &index }

func TestAnUninitializedJournalKeepsTheConfigurationItWasOpenedWith(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20, dayEntry("2026-08-19", "Noticed.", "", "Carry this."))
	dir := filepath.Dir(journal.ReflectionsPath())

	state := mustSettings(t, journal)
	if state.Initialized || !equalStrings(state.Settings.Questions, continuityQuestions) || state.Settings.AnchorQuestionIndex != 3 {
		t.Fatalf("expected the opened settings in force, got %+v", state)
	}
	if state.Path != SettingsPathFor(journal.ReflectionsPath()) {
		t.Fatalf("state.Path = %q", state.Path)
	}
	// Another device with other configuration reads its own: nothing is shared
	// until the journal is initialized.
	if got := mustSettings(t, otherDevice(t, journal)).Settings.Questions; len(got) != 1 {
		t.Fatalf("expected the other device's own questions, got %q", got)
	}

	draft, err := journal.PrepareReflection("2026-08-20", false)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	if !equalStrings(draft.Questions, continuityQuestions) || draft.Context.PriorText != "Carry this." {
		t.Fatalf("expected the legacy questions and the positional anchor, got %+v", draft)
	}
	if _, err := journal.SaveReflection(draft, writtenResult("Today.", "", ""), false); err != nil {
		t.Fatalf("SaveReflection returned error: %v", err)
	}
	// Reading settings, and writing a reflection, never initializes anything.
	requireNoSenecaDir(t, dir)

	if _, err := journal.UpdateSettings(state.Revision, SettingsChange{AnchorQuestionIndex: anchorAt(1)}); !errors.Is(err, ErrSettingsNotInitialized) {
		t.Fatalf("expected an update of an uninitialized journal to be refused, got %v", err)
	}
	requireNoSenecaDir(t, dir)
}

func TestInitializationSeedsOnceAndBothDevicesReadTheSameSettings(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20,
		dayEntry("2026-08-18", "First.", "", "Carry one."),
		dayEntry("2026-08-19", "Second.", "", "Carry two."))
	archive := journal.ArchivePath()
	mustAppend(t, archive, NewFocusSetRecord(archiveTime(10, 9, 0), FocusOriginManual, "", "Stay with it."))
	reflectionsBefore := readFileString(t, journal.ReflectionsPath())
	archiveBefore := readFileString(t, archive)
	other := otherDevice(t, journal)

	first := mustInitialize(t, journal)
	if !first.Changed || !first.State.Initialized {
		t.Fatalf("expected the first initialization to write, got %+v", first)
	}
	document := readFileString(t, first.State.Path)
	if !strings.Contains(document, `"version": 1`) {
		t.Fatalf("expected a versioned document, got:\n%s", document)
	}

	// The second device enrolls with different configuration and adopts what is
	// there instead of seeding again.
	second := mustInitialize(t, other)
	if second.Changed {
		t.Fatal("expected the second initialization to change nothing")
	}
	if got := readFileString(t, first.State.Path); got != document {
		t.Fatalf("expected the settings document untouched, got:\n%s", got)
	}

	mine, theirs := mustSettings(t, journal), mustSettings(t, other)
	if mine.Revision != theirs.Revision || mine.Revision != first.State.Revision {
		t.Fatalf("expected one revision on both devices, got %s and %s", mine.Revision, theirs.Revision)
	}
	if !equalStrings(theirs.Settings.Questions, continuityQuestions) || theirs.Settings.AnchorQuestionIndex != 3 {
		t.Fatalf("expected the seeded settings on the other device, got %+v", theirs.Settings)
	}
	draft, err := other.PrepareReflection("2026-08-20", false)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	if !equalStrings(draft.Questions, continuityQuestions) || draft.AnchorQuestionIndex != 3 || draft.Context.PriorText != "Carry two." {
		t.Fatalf("expected the other device to ask the journal's questions, got %+v", draft)
	}

	// Initialization is a metadata write only.
	if got := readFileString(t, journal.ReflectionsPath()); got != reflectionsBefore {
		t.Fatal("expected the reflections file byte for byte as it was")
	}
	if got := readFileString(t, archive); got != archiveBefore {
		t.Fatal("expected the archive byte for byte as it was")
	}
	for _, path := range []string{journal.ReflectionsPath() + BackupSuffix, archive + BackupSuffix, first.State.Path + BackupSuffix} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected no backup %s, got %v", path, err)
		}
	}
}

// Two journals seeded from the same configuration hold the same bytes: the
// document carries nothing that depends on the device or the moment.
func TestTheSameConfigurationSeedsTheSameDocumentEverywhere(t *testing.T) {
	t.Parallel()

	one := mustInitialize(t, newTestJournal(t, FS{}, 3))
	two := mustInitialize(t, newTestJournal(t, FS{}, 27, dayEntry("2026-08-19", "Noticed.", "", "")))

	if a, b := readFileString(t, one.State.Path), readFileString(t, two.State.Path); a != b {
		t.Fatalf("expected identical documents, got:\n%s\nand:\n%s", a, b)
	}
	if one.State.Revision != two.State.Revision || one.State.Revision != SettingsRevisionOf(journalSettings) {
		t.Fatalf("expected one revision for one configuration, got %s and %s", one.State.Revision, two.State.Revision)
	}
}

func TestInitializationRefusesAnInvalidSeedAndWritesNothing(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "reflections.md")
	for name, seed := range map[string]ReflectionSettings{
		"no questions":           {},
		"a blank question":       {Questions: []string{"One?", " "}},
		"a repeated question":    {Questions: []string{"One?", "One?"}},
		"an anchor past the end": {Questions: []string{"One?"}, AnchorQuestionIndex: 2},
	} {
		journal, err := Open(Options{ReflectionsPath: path, Settings: seed, Clock: fixedNow(20)})
		if err != nil {
			t.Fatalf("Open returned error: %v", err)
		}
		if _, err := journal.InitializeSettings(); err == nil {
			t.Errorf("%s: expected the seed to be refused", name)
		}
		requireNoSenecaDir(t, filepath.Dir(path))
	}
}

func TestQuestionsAndAnchorAreValidatedAndReplacedAsOneUnit(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20)
	initial := mustInitialize(t, journal).State
	document := readFileString(t, initial.Path)

	reworded := []string{"Notice?", "Handle?", "Intend?"}
	refused := map[string]SettingsChange{
		"an anchor past the questions":           {AnchorQuestionIndex: anchorAt(4)},
		"a negative anchor":                      {AnchorQuestionIndex: anchorAt(-1)},
		"an anchor past the new questions":       {Questions: []string{"Notice?", "Carry?"}, AnchorQuestionIndex: anchorAt(3)},
		"no questions":                           {Questions: []string{}},
		"a blank question":                       {Questions: []string{"Notice?", "  "}, AnchorQuestionIndex: anchorAt(1)},
		"a repeated question":                    {Questions: []string{"Notice?", "Notice?"}, AnchorQuestionIndex: anchorAt(1)},
		"questions that reword the anchor":       {Questions: reworded},
		"questions that drop the anchor's place": {Questions: []string{"Notice?", "Handle?"}},
	}
	for name, change := range refused {
		if _, err := journal.UpdateSettings(initial.Revision, change); err == nil {
			t.Errorf("%s: expected the change to be refused", name)
		}
		if got := readFileString(t, initial.Path); got != document {
			t.Fatalf("%s: expected nothing written, got:\n%s", name, got)
		}
	}

	// Rewording the anchor question is a choice only the author can make: the
	// refusal names both questions the index would have confused.
	var moved *AnchorMovedError
	if _, err := journal.UpdateSettings(initial.Revision, SettingsChange{Questions: reworded}); !errors.As(err, &moved) {
		t.Fatalf("expected an AnchorMovedError, got %v", err)
	} else if moved.Index != 3 || moved.Was != "Carry?" || moved.Now != "Intend?" {
		t.Fatalf("unexpected refusal: %+v", moved)
	}

	// A reorder alone keeps the anchor on its question, not on its number.
	reordered, err := journal.UpdateSettings(initial.Revision, SettingsChange{Questions: []string{"Carry?", "Notice?", "Handle?"}})
	if err != nil {
		t.Fatalf("UpdateSettings returned error: %v", err)
	}
	if !reordered.Changed || reordered.State.Settings.AnchorQuestionIndex != 1 || reordered.Previous.AnchorQuestionIndex != 3 {
		t.Fatalf("expected the anchor to follow its question to 1, got %+v", reordered)
	}

	// Stated together, both change in one write.
	both, err := journal.UpdateSettings(reordered.State.Revision, SettingsChange{Questions: reworded, AnchorQuestionIndex: anchorAt(3)})
	if err != nil {
		t.Fatalf("UpdateSettings returned error: %v", err)
	}
	if got := mustSettings(t, journal); got.Revision != both.State.Revision || !equalStrings(got.Settings.Questions, reworded) || got.Settings.AnchorQuestionIndex != 3 {
		t.Fatalf("expected the reworded questions with anchor 3, got %+v", got)
	}

	// Saying what is already there writes nothing.
	same, err := journal.UpdateSettings(both.State.Revision, SettingsChange{Questions: reworded, AnchorQuestionIndex: anchorAt(3)})
	if err != nil || same.Changed {
		t.Fatalf("expected a no-op, got %+v, %v", same, err)
	}
	// What the journal was seeded with is kept for reading old entries.
	if got := mustSettings(t, journal).Legacy; !equalStrings(got.Questions, continuityQuestions) || got.AnchorQuestionIndex != 3 {
		t.Fatalf("expected the legacy reading unchanged, got %+v", got)
	}
}

// An anchor chosen against one list of questions is not applied to another:
// the second device's change is refused, not merged field by field.
func TestASettingsChangeMadeOnAnOlderRevisionIsRefused(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20)
	base := mustInitialize(t, journal).State
	other := otherDevice(t, journal)

	if _, err := journal.UpdateSettings(base.Revision, SettingsChange{Questions: []string{"Carry?", "Notice?", "Handle?"}}); err != nil {
		t.Fatalf("UpdateSettings returned error: %v", err)
	}
	after := readFileString(t, base.Path)

	var stale *StaleSettingsError
	if _, err := other.UpdateSettings(base.Revision, SettingsChange{AnchorQuestionIndex: anchorAt(2)}); !errors.As(err, &stale) {
		t.Fatalf("expected the older change to be refused as stale, got %v", err)
	}
	if stale.Base != base.Revision || stale.Current != mustSettings(t, journal).Revision || stale.Date != "" {
		t.Fatalf("unexpected refusal: %+v", stale)
	}
	if got := readFileString(t, base.Path); got != after {
		t.Fatal("expected the settings left as the first device wrote them")
	}
	if _, err := other.UpdateSettings("", SettingsChange{AnchorQuestionIndex: anchorAt(2)}); err == nil {
		t.Fatal("expected a change with no base revision to be refused")
	}
}

func TestCompetingSettingsChangesYieldExactlyOneWinner(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20)
	base := mustInitialize(t, journal).State

	const writers = 6
	var wg sync.WaitGroup
	results := make([]error, writers)
	for i := 0; i < writers; i++ {
		device := otherDevice(t, journal)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each device wants a different unit; all decided on one base.
			questions := []string{"Notice?", "Handle?", "Carry?", strings.Repeat("More?", i+1)}
			_, results[i] = device.UpdateSettings(base.Revision, SettingsChange{Questions: questions, AnchorQuestionIndex: anchorAt(i%4 + 1)})
		}(i)
	}
	wg.Wait()

	winner := -1
	for i, err := range results {
		var stale *StaleSettingsError
		switch {
		case err == nil && winner < 0:
			winner = i
		case err == nil:
			t.Fatalf("writers %d and %d both changed the settings", winner, i)
		case !errors.As(err, &stale):
			t.Fatalf("writer %d: expected a stale refusal, got %v", i, err)
		}
	}
	if winner < 0 {
		t.Fatal("expected one writer to win")
	}
	// The unit on disk is one writer's questions with that writer's anchor.
	got := mustSettings(t, journal).Settings
	if got.Questions[3] != strings.Repeat("More?", winner+1) || got.AnchorQuestionIndex != winner%4+1 {
		t.Fatalf("expected writer %d's questions and anchor together, got %+v", winner, got)
	}
}

func TestADraftOpenedUnderOlderSettingsIsRefusedUntilReopened(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20, dayEntry("2026-08-19", "Noticed.", "", "Carry this."))
	state := mustInitialize(t, journal).State
	other := otherDevice(t, journal)

	draft, err := journal.PrepareReflection("2026-08-20", false)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	if draft.SettingsRevision != state.Revision {
		t.Fatalf("expected the draft to carry the settings revision, got %q", draft.SettingsRevision)
	}

	// While the form is open the other device changes the questions.
	reworded := []string{"Notice?", "Handle?", "Intend?"}
	if _, err := other.UpdateSettings(state.Revision, SettingsChange{Questions: reworded, AnchorQuestionIndex: anchorAt(3)}); err != nil {
		t.Fatalf("UpdateSettings returned error: %v", err)
	}
	before := readFileString(t, journal.ReflectionsPath())

	var stale *StaleSettingsError
	_, err = journal.SaveReflection(draft, writtenResult("Today.", "", "Tomorrow."), false)
	if !errors.As(err, &stale) || stale.Date != "2026-08-20" || stale.Base != state.Revision {
		t.Fatalf("expected the save to be refused over the settings, got %v", err)
	}
	if WriteCommitted(err) {
		t.Fatal("a stale-settings refusal must be write-free")
	}
	if got := readFileString(t, journal.ReflectionsPath()); got != before {
		t.Fatal("expected the journal untouched")
	}

	// The writing is kept, and reopening lines it up with the new questions.
	if _, err := journal.RetainDraft(draft, writtenResult("Today.", "", "Tomorrow."), err.Error()); err != nil {
		t.Fatalf("RetainDraft returned error: %v", err)
	}
	kept, found, err := journal.RetainedDraft("2026-08-20")
	if err != nil || !found || kept.SettingsRevision != state.Revision {
		t.Fatalf("expected the kept draft with its settings revision, got %+v, %v, %v", kept, found, err)
	}
	reopened, err := journal.PrepareReflection("2026-08-20", false)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	if !equalStrings(reopened.Questions, reworded) {
		t.Fatalf("expected the new questions on reopening, got %q", reopened.Questions)
	}
	if got := kept.AnswersFor(reopened.Questions); !equalStrings(got, []string{"Today.", "", ""}) {
		t.Fatalf("AnswersFor = %q", got)
	}
	if got := kept.Unasked(reopened.Questions); len(got) != 1 || got[0].Question != "Carry?" || got[0].Answer != "Tomorrow." {
		t.Fatalf("expected the answer to the replaced question surfaced, got %+v", got)
	}
	if _, err := journal.SaveReflection(reopened, writtenResult("Today.", "", "Tomorrow."), false); err != nil {
		t.Fatalf("expected the reviewed draft to save, got %v", err)
	}
}

// Initializing with the questions a form was already opened under changes
// nothing the author was shown, so the draft is still good.
func TestInitializingWithTheSameSettingsDoesNotStaleAnOpenDraft(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20)
	draft, err := journal.PrepareReflection("2026-08-20", false)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	mustInitialize(t, secondWriter(t, journal, 0))

	if _, err := journal.SaveReflection(draft, writtenResult("Today.", "", ""), false); err != nil {
		t.Fatalf("expected the draft to save, got %v", err)
	}

	// Initializing with other settings is a change like any other.
	stranger := newTestJournal(t, FS{}, 20)
	draft, err = stranger.PrepareReflection("2026-08-20", false)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	mustInitialize(t, otherDevice(t, stranger))
	var stale *StaleSettingsError
	if _, err := stranger.SaveReflection(draft, writtenResult("Today.", "", ""), false); !errors.As(err, &stale) {
		t.Fatalf("expected the draft to be refused, got %v", err)
	}
}

// legacyAndMarkedJournal holds three days written before the journal had
// settings: two legacy entries and one canonical entry that answers a
// follow-through.
func legacyAndMarkedJournal(t *testing.T) *Journal {
	t.Helper()

	journal := newTestJournal(t, FS{}, 25,
		dayEntry("2026-08-17", "Noticed one.", "Handled one.", "Carry one."),
		dayEntry("2026-08-18", "Noticed two.", "Handled two.", "Carry two."))
	draft, err := journal.PrepareReflection("2026-08-19", false)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	result := ReflectionResult{Saved: true, Answers: []string{"Noticed three.", "Handled three.", "Carry three."}, FollowThrough: "Did carry two."}
	if _, err := journal.SaveReflection(draft, result, true); err != nil {
		t.Fatalf("SaveReflection returned error: %v", err)
	}
	return journal
}

func TestChangingSettingsDoesNotReinterpretWhatWasWritten(t *testing.T) {
	t.Parallel()

	journal := legacyAndMarkedJournal(t)
	state := mustInitialize(t, journal).State
	written := readFileString(t, journal.ReflectionsPath())

	// What each day reads as before anything changes.
	entriesBefore, err := journal.Entries()
	if err != nil {
		t.Fatalf("Entries returned error: %v", err)
	}
	legacyDayBefore, err := journal.Context("2026-08-18", false)
	if err != nil {
		t.Fatalf("Context returned error: %v", err)
	}
	answeredDayBefore, err := journal.Context("2026-08-19", false)
	if err != nil {
		t.Fatalf("Context returned error: %v", err)
	}
	if legacyDayBefore.PriorText != "Carry one." || answeredDayBefore.PriorText != "Carry two." || answeredDayBefore.FollowInitial != "Did carry two." {
		t.Fatalf("unexpected starting context: %+v / %+v", legacyDayBefore, answeredDayBefore)
	}

	// Reorder so that position 3 — where the anchor used to be — is now a
	// different question, then also move the anchor, then disable and restore.
	reordered := []string{"Carry?", "Notice?", "Handle?"}
	changes := []SettingsChange{
		{Questions: reordered, AnchorQuestionIndex: anchorAt(3)},
		{AnchorQuestionIndex: anchorAt(2)},
		{Questions: []string{"Handle?", "Brand new?"}, AnchorQuestionIndex: anchorAt(2)},
	}
	for i, change := range changes {
		outcome, err := journal.UpdateSettings(state.Revision, change)
		if err != nil {
			t.Fatalf("change %d: UpdateSettings returned error: %v", i, err)
		}
		state = outcome.State

		if got := readFileString(t, journal.ReflectionsPath()); got != written {
			t.Fatalf("change %d: expected the reflections file byte for byte as written", i)
		}
		// Historical answers are found by their own headings.
		entries, err := journal.Entries()
		if err != nil {
			t.Fatalf("change %d: Entries returned error: %v", i, err)
		}
		for n, entry := range entries {
			if got, want := entry.Answers(continuityQuestions), entriesBefore[n].Answers(continuityQuestions); !equalStrings(got, want) {
				t.Fatalf("change %d: %s answers = %q, want %q", i, entry.Date, got, want)
			}
		}
		// A legacy entry's intention is still the answer it was when written,
		// not whatever now sits at the configured position.
		legacyDay, err := journal.Context("2026-08-18", false)
		if err != nil {
			t.Fatalf("change %d: Context returned error: %v", i, err)
		}
		if legacyDay.PriorDate != "2026-08-17" || legacyDay.PriorText != "Carry one." {
			t.Fatalf("change %d: the legacy intention was reinterpreted: %+v", i, legacyDay)
		}
		// A stored follow-through snapshot is history.
		answeredDay, err := journal.Context("2026-08-19", false)
		if err != nil {
			t.Fatalf("change %d: Context returned error: %v", i, err)
		}
		answeredDay.PriorAge, answeredDayBefore.PriorAge = "", ""
		if answeredDay != answeredDayBefore {
			t.Fatalf("change %d: the follow-through snapshot changed: %+v", i, answeredDay)
		}
		// A new day follows the marked anchor of the newest entry.
		newDay, err := journal.Context("2026-08-20", false)
		if err != nil {
			t.Fatalf("change %d: Context returned error: %v", i, err)
		}
		if newDay.PriorDate != "2026-08-19" || newDay.PriorText != "Carry three." {
			t.Fatalf("change %d: the newest intention was reinterpreted: %+v", i, newDay)
		}
	}

	// Disabling the anchor stops carrying intentions forward and removes
	// nothing: the stored snapshot still answers its day.
	if _, err := journal.UpdateSettings(state.Revision, SettingsChange{AnchorQuestionIndex: anchorAt(0)}); err != nil {
		t.Fatalf("UpdateSettings returned error: %v", err)
	}
	if ctx, err := journal.Context("2026-08-20", false); err != nil || ctx.ShowFollowThrough {
		t.Fatalf("expected no follow-through with the anchor disabled, got %+v, %v", ctx, err)
	}
	if ctx, err := journal.Context("2026-08-19", false); err != nil || ctx.PriorText != "Carry two." || ctx.FollowInitial != "Did carry two." {
		t.Fatalf("expected the stored snapshot kept, got %+v, %v", ctx, err)
	}
	if got := readFileString(t, journal.ReflectionsPath()); got != written {
		t.Fatal("expected the reflections file byte for byte as written")
	}
}

// Editing a day under new questions rewrites the answers the form showed and
// leaves the rest of the entry, and its follow-through snapshot, as stored.
func TestEditingUnderNewSettingsKeepsStoredAnswersAndSnapshots(t *testing.T) {
	t.Parallel()

	journal := legacyAndMarkedJournal(t)
	state := mustInitialize(t, journal).State
	if _, err := journal.UpdateSettings(state.Revision, SettingsChange{Questions: []string{"Carry?", "Brand new?"}, AnchorQuestionIndex: anchorAt(1)}); err != nil {
		t.Fatalf("UpdateSettings returned error: %v", err)
	}

	draft, err := journal.PrepareReflection("2026-08-19", false)
	if err != nil {
		t.Fatalf("PrepareReflection returned error: %v", err)
	}
	if !equalStrings(draft.Initial, []string{"Carry three.", ""}) {
		t.Fatalf("expected stored answers lined up by question text, got %q", draft.Initial)
	}
	result := ReflectionResult{Saved: true, Answers: []string{"Carry three.", "Something new."}, FollowThrough: draft.Context.FollowInitial}
	if _, err := journal.SaveReflection(draft, result, true); err != nil {
		t.Fatalf("SaveReflection returned error: %v", err)
	}

	entries, err := journal.Entries()
	if err != nil {
		t.Fatalf("Entries returned error: %v", err)
	}
	idx, _ := FindEntry(entries, "2026-08-19")
	edited := entries[idx]
	if got := edited.Answers(continuityQuestions); !equalStrings(got, []string{"Noticed three.", "Handled three.", "Carry three."}) {
		t.Fatalf("expected the answers to questions no longer asked kept, got %q", got)
	}
	follow, ok := edited.FollowThroughSection()
	if !ok || follow.SourceDate != "2026-08-18" || follow.SourceIntention != "Carry two." || follow.Response != "Did carry two." {
		t.Fatalf("expected the follow-through snapshot as stored, got %+v", follow)
	}
	// The untouched legacy days are byte for byte what they were.
	for _, date := range []string{"2026-08-17", "2026-08-18"} {
		i, _ := FindEntry(entries, date)
		if !strings.HasPrefix(entries[i].Raw, "## "+date+"\n") || strings.Contains(entries[i].Raw, "seneca:") {
			t.Fatalf("expected %s left a legacy entry, got:\n%s", date, entries[i].Raw)
		}
	}
}

func TestASettingsDocumentThatCannotBeUsedIsRefusedNotGuessedAt(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"not JSON":               "questions = []\n",
		"a newer version":        `{"version": 3, "reflection": {"questions": ["A?"], "anchor_question_index": 0}}`,
		"no version":             `{"reflection": {"questions": ["A?"], "anchor_question_index": 0}}`,
		"an unknown field":       `{"version": 1, "reflection": {"questions": ["A?"], "anchor_question_index": 0}, "legacy": {"questions": ["A?"], "anchor_question_index": 0}, "theme": "dark"}`,
		"an anchor out of range": `{"version": 1, "reflection": {"questions": ["A?"], "anchor_question_index": 2}, "legacy": {"questions": ["A?"], "anchor_question_index": 0}}`,
		"no legacy reading":      `{"version": 1, "reflection": {"questions": ["A?"], "anchor_question_index": 1}}`,
	}
	for name, content := range cases {
		journal := newTestJournal(t, FS{}, 20, dayEntry("2026-08-19", "Noticed.", "", ""))
		path := SettingsPathFor(journal.ReflectionsPath())
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("creating the metadata directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("writing the settings: %v", err)
		}

		var unusable *SettingsDocumentError
		if _, err := journal.EffectiveSettings(); !errors.As(err, &unusable) {
			t.Errorf("%s: expected EffectiveSettings to refuse, got %v", name, err)
		}
		// The device's own questions are not a fallback once a document exists.
		if _, err := journal.PrepareReflection("2026-08-20", false); !errors.As(err, &unusable) {
			t.Errorf("%s: expected PrepareReflection to refuse, got %v", name, err)
		}
		if _, err := journal.InitializeSettings(); !errors.As(err, &unusable) {
			t.Errorf("%s: expected InitializeSettings to refuse, got %v", name, err)
		}
		if _, err := journal.UpdateSettings(SettingsRevisionOf(journalSettings), SettingsChange{AnchorQuestionIndex: anchorAt(0)}); !errors.As(err, &unusable) {
			t.Errorf("%s: expected UpdateSettings to refuse, got %v", name, err)
		}
		if got := readFileString(t, path); got != content {
			t.Errorf("%s: expected the document left alone", name)
		}
	}
}

func TestLoadJournalSettingsReadsWithoutCreating(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20)
	state, initialized, err := LoadJournalSettings(journal.ReflectionsPath())
	if err != nil || initialized || state.Path != SettingsPathFor(journal.ReflectionsPath()) {
		t.Fatalf("expected an uninitialized journal reported, got %+v, %v, %v", state, initialized, err)
	}
	requireNoSenecaDir(t, filepath.Dir(journal.ReflectionsPath()))

	want := mustInitialize(t, journal).State
	state, initialized, err = LoadJournalSettings(journal.ReflectionsPath())
	if err != nil || !initialized || state.Revision != want.Revision {
		t.Fatalf("expected the initialized settings, got %+v, %v, %v", state, initialized, err)
	}
}

// Questions are prose; the document stores them as written.
func TestSettingsKeepQuestionTextExactly(t *testing.T) {
	t.Parallel()

	questions := []string{"What did I <avoid> & why?", "Où ai-je progressé ?"}
	path := filepath.Join(t.TempDir(), "reflections.md")
	journal, err := Open(Options{ReflectionsPath: path, Settings: ReflectionSettings{Questions: questions, AnchorQuestionIndex: 2}, Clock: fixedNow(20)})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	state := mustInitialize(t, journal).State
	if document := readFileString(t, state.Path); !strings.Contains(document, questions[0]) || !strings.Contains(document, questions[1]) {
		t.Fatalf("expected the questions verbatim in the document, got:\n%s", document)
	}
	if got := mustSettings(t, journal).Settings.Questions; !equalStrings(got, questions) {
		t.Fatalf("questions round-tripped as %q", got)
	}
}
