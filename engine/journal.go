package engine

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Journal is the headless engine: one reflections file, the review archive
// beside it, the questions the author answers, a clock, and a filesystem.
// Everything a command line or a phone does to a journal goes through it.
//
// Nothing here reads the environment, the terminal, or the wall clock, prints,
// prompts, or runs another program. The caller resolves the paths, loads the
// settings, supplies the clock, and — on the desktop — commits to Git after a
// mutation reports what it wrote.
type Journal struct {
	reflectionsPath string
	archivePath     string
	settings        ReflectionSettings
	clock           func() time.Time
	fs              FS
	lockWaitFor     time.Duration
	random          io.Reader
}

// Options is everything a Journal is opened with. No field has an ambient
// default except ArchivePath, which is derived from ReflectionsPath.
type Options struct {
	// ReflectionsPath is the resolved reflections file. It may be empty for a
	// journal opened only to read or change the review archive.
	ReflectionsPath string
	// ArchivePath is the review archive. Empty means the archive beside
	// ReflectionsPath.
	ArchivePath string
	// Settings are the validated questions and anchor selection. They are
	// needed only by the operations that read or write a reflection.
	Settings ReflectionSettings
	// Clock is the journal's only source of the current time.
	Clock func() time.Time
	// FS is the filesystem mutations write through. The zero value is the
	// real one.
	FS FS
	// LockWait is how long a commit waits for another writer to finish before
	// giving up without writing. Zero means DefaultLockWait.
	LockWait time.Duration
	// Random is where journal and device identities are drawn from when a
	// journal is enrolled in sync. Nil means the system's secure source.
	Random io.Reader
}

// Open binds a journal to its locations, settings, clock, and filesystem. It
// touches no file: a journal that does not exist yet is an ordinary journal,
// and each operation reads what it needs when it runs.
func Open(opts Options) (*Journal, error) {
	if opts.ReflectionsPath == "" && opts.ArchivePath == "" {
		return nil, errors.New("opening journal: a reflections path or an archive path is required")
	}
	if opts.Clock == nil {
		return nil, errors.New("opening journal: a clock is required")
	}
	archivePath := opts.ArchivePath
	if archivePath == "" {
		archivePath = ArchivePathFor(opts.ReflectionsPath)
	}
	random := opts.Random
	if random == nil {
		random = rand.Reader
	}
	return &Journal{
		random:          random,
		reflectionsPath: opts.ReflectionsPath,
		archivePath:     archivePath,
		settings:        opts.Settings,
		clock:           opts.Clock,
		fs:              opts.FS,
		lockWaitFor:     opts.LockWait,
	}, nil
}

// ReflectionsPath is the reflections file this journal reads and writes.
func (j *Journal) ReflectionsPath() string { return j.reflectionsPath }

// ArchivePath is the review archive this journal reads and writes.
func (j *Journal) ArchivePath() string { return j.archivePath }

// Settings are the questions and anchor selection the journal was opened with:
// this device's configuration. They are what the journal uses until its own
// settings are initialized, and what initialization seeds them from;
// EffectiveSettings says which are in force.
func (j *Journal) Settings() ReflectionSettings { return j.settings }

// Now is the journal's clock.
func (j *Journal) Now() time.Time { return j.clock() }

// Today is the clock's calendar date.
func (j *Journal) Today() string { return j.clock().Format(CalendarDateLayout) }

func (j *Journal) requireReflections() error {
	if j.reflectionsPath == "" {
		return errors.New("this journal was opened without a reflections path")
	}
	return nil
}

// OpenReflections reads and parses the reflections file, completing an
// interrupted commit first. It is the read half only: a mutation re-reads under
// the journal lock through Commit, so that what it replaces is what it read.
func (j *Journal) OpenReflections() (*ReflectionTransaction, error) {
	if err := j.requireReflections(); err != nil {
		return nil, err
	}
	if err := j.Recover(); err != nil {
		return nil, err
	}
	return j.fs.OpenReflectionFile(j.reflectionsPath)
}

// OpenArchive reads and parses the review archive, completing an interrupted
// commit first. Like OpenReflections it is the read half only.
func (j *Journal) OpenArchive() (*ArchiveTransaction, error) {
	if err := j.Recover(); err != nil {
		return nil, err
	}
	return j.fs.OpenReviewArchive(j.archivePath)
}

// ReadReflections parses the reflections file for a read-only caller. A missing
// file is reported, never created, and parsing never fails: what is wrong with
// the document is in its diagnostics.
func (j *Journal) ReadReflections() (ReflectionDocument, bool, error) {
	if err := j.requireReflections(); err != nil {
		return ReflectionDocument{}, false, err
	}
	if err := j.Recover(); err != nil {
		return ReflectionDocument{}, false, err
	}
	data, err := os.ReadFile(j.reflectionsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ReflectionDocument{}, false, nil
		}
		return ReflectionDocument{}, false, fmt.Errorf("reading %s: %w", j.reflectionsPath, err)
	}
	return ParseReflectionDocument(string(data)), true, nil
}

// Entries returns the journal's entries in file order, refusing a document a
// mutation would refuse: one with an undatable entry or a repeated date.
func (j *Journal) Entries() ([]ReflectionEntry, error) {
	tx, err := j.OpenReflections()
	if err != nil {
		return nil, err
	}
	if err := tx.Validate(); err != nil {
		return nil, err
	}
	return tx.Entries(), nil
}

// ReadArchive parses the review archive for a read-only caller. A missing file
// is reported, never created; a damaged one is returned with its diagnostics.
func (j *Journal) ReadArchive() (ReviewArchive, bool, error) {
	if err := j.Recover(); err != nil {
		return ReviewArchive{}, false, err
	}
	return LoadReviewArchive(j.archivePath)
}

// Validation is what Validate found in the journal's two documents.
type Validation struct {
	Reflections       ReflectionDocument
	ReflectionsExist  bool
	ReflectionsReason error
	Archive           ReviewArchive
	ArchiveExists     bool
	ArchiveReason     error
}

// Writable reports whether both documents would accept a mutation.
func (v Validation) Writable() bool {
	return v.ReflectionsReason == nil && v.ArchiveReason == nil
}

// Validate parses both documents and reports, for each, the refusal a mutation
// would raise — nil when it would be accepted. It writes nothing. The returned
// error is reserved for a file that could not be read at all.
func (j *Journal) Validate() (Validation, error) {
	var v Validation
	if j.reflectionsPath != "" {
		tx, err := j.OpenReflections()
		if err != nil {
			return Validation{}, err
		}
		v.Reflections = tx.doc
		v.ReflectionsExist = tx.exists
		v.ReflectionsReason = tx.Validate()
	}

	tx, err := j.OpenArchive()
	if err != nil {
		return Validation{}, err
	}
	v.Archive = tx.Archive
	v.ArchiveExists = tx.exists
	v.ArchiveReason = tx.Validate()
	return v, nil
}

// Context assembles what a reflection for date shows above the questions: the
// focus in effect and the intention being followed through. blank hides all of
// it without reading the archive.
func (j *Journal) Context(date string, blank bool) (ContinuityContext, error) {
	state, err := j.EffectiveSettings()
	if err != nil {
		return ContinuityContext{}, err
	}
	entries, err := j.Entries()
	if err != nil {
		return ContinuityContext{}, err
	}
	return j.contextFor(state, entries, date, blank)
}

func (j *Journal) contextFor(state SettingsState, entries []ReflectionEntry, date string, blank bool) (ContinuityContext, error) {
	ctx, err := continuityContext(entries, j.archivePath, date, state.Settings, state.Legacy, blank)
	if err != nil {
		return ContinuityContext{}, err
	}
	// Age is measured from the real today rather than the entry date, so a
	// backfill still reads truthfully.
	ctx.PriorAge = RelativeDay(ctx.PriorDate, j.Today())
	return ctx, nil
}

// FutureDateError refuses a reflection dated after the journal's today. A
// reflection looks back on a day; one dated ahead would surface tomorrow as an
// entry already written.
type FutureDateError struct {
	Date  string
	Today string
}

func (e *FutureDateError) Error() string {
	return fmt.Sprintf("%s is in the future; a reflection can only be written for today or earlier", e.Date)
}

// ErrEmptyReflection refuses to save a reflection in which nothing was written.
var ErrEmptyReflection = errors.New("nothing to record: the reflection is empty")

// ReflectionDraft is the read half of the daily read-modify-write: everything
// a form needs to show before the author writes, and everything SaveReflection
// needs to fold the writing back in.
type ReflectionDraft struct {
	Date                string
	Questions           []string
	AnchorQuestionIndex int
	// Initial holds the stored answer to each question when Date already has
	// an entry, and is nil otherwise.
	Initial []string
	// IsEdit reports whether Date already has an entry.
	IsEdit  bool
	Context ContinuityContext
	// Base is the revision of Date's entry as the author was shown it:
	// RevisionAbsent for a day with no entry. SaveReflection refuses to write
	// over anything else.
	Base Revision
	// SettingsRevision is the revision of the questions and anchor selection
	// the author was asked under. SaveReflection refuses to save answers the
	// journal no longer asks for in that form.
	SettingsRevision Revision
}

// entryRevision is the revision of date's entry among entries. It is taken
// from the entry's own bytes, which no rewrite of the rest of the file changes.
func entryRevision(entries []ReflectionEntry, date string) Revision {
	if idx, found := FindEntry(entries, date); found {
		return RevisionOf([]byte(entries[idx].Raw))
	}
	return RevisionAbsent
}

// PrepareReflection reads the journal for a reflection dated date. It fails
// closed before the author writes anything: rejecting the file after they have
// written would throw the writing away.
func (j *Journal) PrepareReflection(date string, blank bool) (ReflectionDraft, error) {
	state, err := j.EffectiveSettings()
	if err != nil {
		return ReflectionDraft{}, err
	}
	if len(state.Settings.Questions) == 0 {
		return ReflectionDraft{}, errors.New("this journal was opened without reflection questions")
	}
	if !IsCalendarDate(date) {
		return ReflectionDraft{}, fmt.Errorf("invalid reflection date %q: expected YYYY-MM-DD", date)
	}
	if today := j.Today(); date > today {
		return ReflectionDraft{}, &FutureDateError{Date: date, Today: today}
	}

	seed, err := j.OpenReflections()
	if err != nil {
		return ReflectionDraft{}, err
	}
	if err := seed.Validate(); err != nil {
		return ReflectionDraft{}, err
	}

	draft := ReflectionDraft{
		Date:                date,
		Questions:           state.Settings.Questions,
		AnchorQuestionIndex: state.Settings.AnchorQuestionIndex,
		SettingsRevision:    state.Revision,
	}
	entries := seed.Entries()
	draft.Base = entryRevision(entries, date)
	if idx, found := FindEntry(entries, date); found {
		if err := ValidateEntryRerender(seed.Label, j.reflectionsPath, entries[idx]); err != nil {
			return ReflectionDraft{}, err
		}
		draft.Initial = entries[idx].Answers(draft.Questions)
		draft.IsEdit = true
	}

	draft.Context, err = j.contextFor(state, entries, date, blank)
	if err != nil {
		return ReflectionDraft{}, err
	}
	return draft, nil
}

// SavedReflection reports a reflection that reached the file.
type SavedReflection struct {
	Path string
	Date string
	// IsEdit reports whether an existing entry was rewritten rather than a new
	// one added.
	IsEdit bool
	// Entries is the journal as written.
	Entries []ReflectionEntry
}

// SaveReflection folds what the author wrote into the journal and replaces the
// file. It re-reads under the journal lock first: the author may have spent a
// long time writing, and the modify half of a read-modify-write must act on the
// bytes it replaces. Days written meanwhile by another process are kept; a
// change to the draft's own day is a *StaleReflectionError, a change to the
// questions or the anchor it was written under is a *StaleSettingsError, and
// either way nothing is written. backfill re-sorts by date, since a dated entry may belong anywhere.
//
// A *CommittedWriteError is returned together with a filled-in SavedReflection:
// the day is already on disk, so the caller finishes as it would on success.
func (j *Journal) SaveReflection(draft ReflectionDraft, result ReflectionResult, backfill bool) (SavedReflection, error) {
	if result.Empty() {
		return SavedReflection{}, ErrEmptyReflection
	}
	if draft.Base == "" || draft.SettingsRevision == "" {
		return SavedReflection{}, errors.New("this reflection carries no base revision; prepare it with PrepareReflection")
	}

	var saved SavedReflection
	err := j.Commit(func(c *Commit) error {
		// Checked first: answers to questions the journal has since replaced
		// need the author's eye whatever happened to the day's entry.
		state, err := j.readSettings()
		if err != nil {
			return err
		}
		if state.Revision != draft.SettingsRevision {
			return &StaleSettingsError{Path: state.Path, Date: draft.Date, Base: draft.SettingsRevision, Current: state.Revision}
		}

		tx, err := c.Reflections()
		if err != nil {
			return err
		}
		if err := tx.Validate(); err != nil {
			return err
		}
		if current := entryRevision(tx.Entries(), draft.Date); current != draft.Base {
			return &StaleReflectionError{Path: j.reflectionsPath, Date: draft.Date, Base: draft.Base, Current: current}
		}

		entries, isEdit, err := ApplyReflectionResult(tx.Entries(), ReflectionSave{
			Label:               tx.Label,
			Path:                j.reflectionsPath,
			Date:                draft.Date,
			Questions:           draft.Questions,
			AnchorQuestionIndex: draft.AnchorQuestionIndex,
			Context:             draft.Context,
			Result:              result,
			Backfill:            backfill,
		})
		if err != nil {
			return err
		}
		saved = SavedReflection{Path: j.reflectionsPath, Date: draft.Date, IsEdit: isEdit, Entries: entries}
		return tx.CommitEntries(entries)
	})
	if err != nil {
		return committedOutcome(saved, err)
	}
	return saved, nil
}

// committedOutcome keeps an outcome only when the write it describes reached
// the file despite the error.
func committedOutcome[T any](outcome T, err error) (T, error) {
	if WriteCommitted(err) {
		return outcome, err
	}
	var zero T
	return zero, err
}

// ReviewDueNote says when entries have piled up past the newest saved review.
// It is a courtesy: a missing, unreadable, or damaged archive says nothing.
func (j *Journal) ReviewDueNote(entries []ReflectionEntry) (string, bool) {
	archive, exists, err := j.ReadArchive()
	if err != nil || !exists || len(archive.Diagnostics) > 0 {
		return "", false
	}
	return ReviewDueNote(entries, archive, j.Today())
}

// SortOutcome reports what Sort did.
type SortOutcome struct {
	// Count is the number of entries in the journal.
	Count int
	// Changed is false when the file was already in order and left alone.
	Changed bool
}

// Sort rewrites the reflections file in date order. A journal already in order
// is not touched: no backup, no replacement.
func (j *Journal) Sort() (SortOutcome, error) {
	if err := j.requireReflections(); err != nil {
		return SortOutcome{}, err
	}
	if _, err := os.Stat(j.reflectionsPath); err != nil {
		return SortOutcome{}, fmt.Errorf("reading file: %w", err)
	}
	// A journal already in order is settled by reading alone: no lock is taken
	// and nothing is created, even for an instant.
	seen, err := j.OpenReflections()
	if err != nil {
		return SortOutcome{}, err
	}
	outcome, _, err := planSort(seen)
	if err != nil || !outcome.Changed {
		return outcome, err
	}

	err = j.Commit(func(c *Commit) error {
		tx, err := c.Reflections()
		if err != nil {
			return err
		}
		var entries []ReflectionEntry
		if outcome, entries, err = planSort(tx); err != nil || !outcome.Changed {
			return err
		}
		return tx.CommitEntries(entries)
	})
	if err != nil {
		return committedOutcome(outcome, err)
	}
	return outcome, nil
}

// planSort validates the file and reports what sorting it would do, with the
// entries in date order. A no-op sort must not churn the backup or the file.
func planSort(tx *ReflectionTransaction) (SortOutcome, []ReflectionEntry, error) {
	if err := tx.Validate(); err != nil {
		return SortOutcome{}, nil, err
	}
	entries := tx.Entries()
	if len(entries) == 0 {
		return SortOutcome{}, nil, nil
	}
	sortEntriesByDate(entries)
	outcome := SortOutcome{Count: len(entries)}
	outcome.Changed = !tx.Unchanged([]byte(RenderReflectionEntries(entries)))
	return outcome, entries, nil
}

func sortEntriesByDate(entries []ReflectionEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Date < entries[j].Date
	})
}

// ErrMergeSameFile refuses to merge a journal into itself.
var ErrMergeSameFile = errors.New("source and destination must differ")

// MergeOutcome reports what Merge did.
type MergeOutcome struct {
	// Merged is the number of entries carried over from the source.
	Merged int
}

// Merge adds every entry of the reflections file at sourcePath to this journal
// and rewrites it in date order. The source is read, never changed. A date both
// files hold is reported, never resolved: it could be a correction, a duplicate,
// or two days' writing under one heading, and only the author knows which.
func (j *Journal) Merge(sourcePath string) (MergeOutcome, error) {
	if err := j.requireReflections(); err != nil {
		return MergeOutcome{}, err
	}
	same, err := SameFilePath(sourcePath, j.reflectionsPath)
	if err != nil {
		return MergeOutcome{}, err
	}
	if same {
		return MergeOutcome{}, ErrMergeSameFile
	}

	if _, err := os.Stat(sourcePath); err != nil {
		return MergeOutcome{}, fmt.Errorf("reading source file: %w", err)
	}
	var outcome MergeOutcome
	err = j.Commit(func(c *Commit) error {
		// The source is read, never written, so it needs no lock of its own:
		// whatever replaces it does so in one rename.
		srcTx, err := j.fs.OpenReflectionFile(sourcePath)
		if err != nil {
			return err
		}
		srcTx.Label = "source file"
		if err := srcTx.Validate(); err != nil {
			return err
		}

		dstTx, err := c.Reflections()
		if err != nil {
			return err
		}
		dstTx.Label = "destination file"
		if err := dstTx.Validate(); err != nil {
			return err
		}

		srcEntries := srcTx.Entries()
		dstEntries := dstTx.Entries()
		if conflicts := ConflictingEntryDates(dstEntries, srcEntries); len(conflicts) > 0 {
			return &MergeConflictError{SrcPath: sourcePath, DstPath: j.reflectionsPath, Dates: conflicts}
		}

		all := append(append([]ReflectionEntry(nil), dstEntries...), srcEntries...)
		sortEntriesByDate(all)

		outcome = MergeOutcome{Merged: len(srcEntries)}
		return dstTx.CommitEntries(all)
	})
	if err != nil {
		return committedOutcome(outcome, err)
	}
	return outcome, nil
}

// SameFilePath reports whether two paths name the same file, by identity when
// both exist and by cleaned absolute path otherwise.
func SameFilePath(srcPath, dstPath string) (bool, error) {
	srcAbs, err := filepath.Abs(srcPath)
	if err != nil {
		return false, fmt.Errorf("resolving source path: %w", err)
	}
	dstAbs, err := filepath.Abs(dstPath)
	if err != nil {
		return false, fmt.Errorf("resolving destination path: %w", err)
	}

	srcInfo, srcErr := os.Stat(srcAbs)
	dstInfo, dstErr := os.Stat(dstAbs)
	if srcErr == nil && dstErr == nil && os.SameFile(srcInfo, dstInfo) {
		return true, nil
	}

	return filepath.Clean(srcAbs) == filepath.Clean(dstAbs), nil
}

// Focus resolves the focus in effect on a date and the whole focus timeline
// from one read, so the two can never disagree. A missing archive is no focus;
// a damaged one is refused rather than guessed at.
func (j *Journal) Focus(date string) (FocusState, []FocusEvent, error) {
	if err := j.Recover(); err != nil {
		return FocusState{}, nil, err
	}
	return FocusAndTimelineOnDate(j.archivePath, date)
}

// Reviews returns the saved reviews newest first, refusing a damaged archive.
func (j *Journal) Reviews() ([]ReviewRecord, error) {
	archive, exists, err := j.ReadArchive()
	if err != nil || !exists {
		return nil, err
	}
	if len(archive.Diagnostics) > 0 {
		return nil, &UnreadableContextError{
			path:        j.archivePath,
			Purpose:     "saved reviews",
			Remedy:      "run `seneca doctor` to inspect it",
			diagnostics: archive.Diagnostics,
		}
	}
	return archive.ReviewRecords(), nil
}

// ErrBlankFocus refuses a focus with no text.
var ErrBlankFocus = errors.New("focus text is blank: pass the focus to set, for example `seneca focus set finish the archive`")

// FocusChange reports what SetFocus or ClearFocus did.
type FocusChange struct {
	// Changed is false when ClearFocus found no focus to clear; nothing was
	// written.
	Changed bool
	// Effective is the day the change takes effect: the journal's today.
	Effective string
	// Text is the focus now in effect, empty after a clear.
	Text string
	// Previous is the focus a clear replaced.
	Previous FocusState
	// Record is the archive record written, when Changed.
	Record ArchiveRecord
}

// SetFocus records a manual focus effective today.
func (j *Journal) SetFocus(text string) (FocusChange, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return FocusChange{}, ErrBlankFocus
	}

	record := NewFocusSetRecord(j.clock(), FocusOriginManual, "", text)
	change := FocusChange{Changed: true, Effective: record.Focus.Effective, Text: text, Record: record}
	err := j.Commit(func(c *Commit) error {
		tx, err := c.Archive()
		if err != nil {
			return err
		}
		return tx.AppendRecords(record)
	})
	if err != nil {
		return committedOutcome(change, err)
	}
	return change, nil
}

// ClearFocus records a clear event. With no focus active it is a read-only
// no-op: no archive is created and nothing is written.
func (j *Journal) ClearFocus() (FocusChange, error) {
	now := j.clock()
	today := now.Format(CalendarDateLayout)

	// With no focus to clear, reading settles it: no lock is taken and nothing
	// is created, even for an instant.
	seen, err := j.OpenArchive()
	if err != nil {
		return FocusChange{}, err
	}
	// Validate before the no-op check so a broken archive is reported rather
	// than mistaken for an empty focus timeline.
	if err := seen.Validate(); err != nil {
		return FocusChange{}, err
	}
	if !seen.Archive.FocusOn(today).Active {
		return FocusChange{Effective: today}, nil
	}

	change := FocusChange{Effective: today}
	err = j.Commit(func(c *Commit) error {
		tx, err := c.Archive()
		if err != nil {
			return err
		}
		if err := tx.Validate(); err != nil {
			return err
		}

		// Another process may have cleared it since.
		state := tx.Archive.FocusOn(today)
		if !state.Active {
			return nil
		}

		record := NewFocusClearRecord(now)
		change = FocusChange{Changed: true, Effective: today, Previous: state, Record: record}
		return tx.AppendRecords(record)
	})
	if err != nil {
		return committedOutcome(change, err)
	}
	return change, nil
}

// ReviewSave is an approved review ready to be archived. The engine does not
// write reviews: Body is the Markdown its author — on the desktop, an AI
// harness and the person who approved its answer — produced.
type ReviewSave struct {
	Period ReviewPeriod
	Start  string
	End    string
	Title  string
	Body   string
	// AcceptedFocus, when not empty, is set as the focus in the same write.
	AcceptedFocus string
}

// SavedReview reports a review that reached the archive.
type SavedReview struct {
	// Review is the review record written.
	Review ArchiveRecord
	// AcceptedFocus is the focus set alongside it, empty when none was.
	AcceptedFocus string
	// FocusEffective is the day that focus takes effect: the journal's today.
	FocusEffective string
}

// SaveReview archives a review and any focus it changed in one write, so a
// review can never land without the focus it justified.
//
// An accepted focus is effective on the day the author accepted it, not on the
// review window's end. The window end is what a review reads focus from;
// setting is an act of the present, and back-dating it would rewrite days
// already reflected on.
func (j *Journal) SaveReview(save ReviewSave) (SavedReview, error) {
	now := j.clock()

	var saved SavedReview
	err := j.Commit(func(c *Commit) error {
		tx, err := c.Archive()
		if err != nil {
			return err
		}
		// Validate before building anything, so a refusal reads as a refusal
		// rather than as a failure halfway through a save.
		if err := tx.Validate(); err != nil {
			return err
		}

		// Built from the archive as it is under the lock, so a review saved by
		// another process meanwhile is the one this record revises.
		review := tx.Archive.NewReviewRecord(now, save.Period, save.Start, save.End, save.Title, save.Body)
		records := []ArchiveRecord{review}
		saved = SavedReview{Review: review}
		if save.AcceptedFocus != "" {
			records = append(records, NewFocusSetRecord(now, FocusOriginReview, review.Review.ID, save.AcceptedFocus))
			saved.AcceptedFocus = save.AcceptedFocus
			saved.FocusEffective = now.Format(CalendarDateLayout)
		}
		return tx.AppendRecords(records...)
	})
	if err != nil {
		return committedOutcome(saved, err)
	}
	return saved, nil
}
