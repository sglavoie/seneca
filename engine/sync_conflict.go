package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A conflict is an artifact two devices changed in ways that could not be
// merged. Nothing about it is decided for the author.
//
// # What a conflict holds
//
// The file keeps the version this device wrote, so an import never replaces
// writing with someone else's. Every other version is held beside it, with its
// bytes and the bytes all of them were made on, until one change covers them
// all. Each device shows its own version and holds the other's, so the two
// agree on what competes even while their files differ in that one artifact.
//
// # Resolving
//
// A resolution is a transaction like any other: it names the versions it
// settles and the revision that replaces them, and every device that holds one
// of those versions takes the result. It can keep one version, keep a
// deletion, or write something new.
//
// A conflict is named by the versions that compete in it. A resolution names
// the conflict it answers, and is refused when the versions are no longer the
// ones the author was shown: another change arrived, or the file was edited.
// The journal is left as it is then, and the conflict is listed as it now is.
//
// # Deletion and edit
//
// A day deleted on one device and edited on another is a conflict whose
// competing version is the deletion. The edited bytes, and the bytes the
// deletion removed, are held either way.
//
// # The archive
//
// Records are only ever added. Two devices that each add records keep all of
// them; no record is rewritten to point at another, so two reviews saved for
// one period on two devices stay two branches. Their place in the file comes
// from history, not from the order they arrived in: see syncOrder.
//
// # Disputed focus
//
// Two focus changes effective the same day, made by devices that had not seen
// each other's, leave no way to say which came last. That day's focus is
// disputed: reading it reports the competing changes instead of a winner. A
// focus change made after both settles it.
//
// # Recovery
//
// A journal file that is missing, empty, or has lost most of what was recorded
// of it is never read as deletions. It stays as it is until the author either
// restores it from what history holds, or accepts the deletions.

// syncOrder places an archive record among the others: by the transaction that
// first wrote it, and its position in that transaction. A transaction is
// deeper than everything it was made on top of, so a record always follows the
// records its author could see; records made independently are ordered by
// their transactions' identities, the same way on every device. The genesis
// comes first among the transactions that start a history, which keeps the
// archive as it was when the journal was enrolled.
type syncOrder struct {
	Depth       int    `json:"depth"`
	Transaction string `json:"transaction"`
	Index       int    `json:"index"`
}

func (o syncOrder) before(other syncOrder, genesis string) bool {
	switch {
	case o.Depth != other.Depth:
		return o.Depth < other.Depth
	case o.Transaction == other.Transaction:
		return o.Index < other.Index
	case o.Transaction == genesis || other.Transaction == genesis:
		return o.Transaction == genesis
	default:
		return o.Transaction < other.Transaction
	}
}

// syncFocusDispute is a day whose focus two devices changed independently.
type syncFocusDispute struct {
	Effective string `json:"effective"`
	// Records are the competing focus records, sorted.
	Records []string `json:"records"`
}

func (d syncFocusDispute) id() string {
	return conflictID("focus:"+d.Effective, d.Records)
}

// conflictID names a conflict by what competes in it, the same on every device
// that holds the same versions.
func conflictID(subject string, versions []string) string {
	sorted := append([]string{}, versions...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(subject + "\x00" + strings.Join(sorted, "\x00")))
	return hex.EncodeToString(sum[:])
}

// refreshDisputes works out, from the archive as the commit leaves it, which
// days have a disputed focus. Among the focus changes effective one day, a
// change is superseded by any other made with it in sight; when more than one
// is left and they do not say the same thing, the day is disputed.
func (w *syncWork) refreshDisputes(records []syncUnit) error {
	type point struct {
		id    string
		focus FocusEvent
		order syncOrder
	}
	days := map[string][]point{}
	for _, unit := range records {
		archive := ParseReviewArchive(string(unit.data))
		if len(archive.Records) != 1 || archive.Records[0].Focus == nil {
			continue
		}
		focus := *archive.Records[0].Focus
		order := w.state.Artifacts[unit.id].Order
		if focus.Effective == "" || order == nil {
			continue
		}
		days[focus.Effective] = append(days[focus.Effective], point{id: focus.ID, focus: focus, order: *order})
	}

	disputes := []syncFocusDispute{}
	for effective, points := range days {
		if len(points) < 2 {
			continue
		}
		var standing []point
		for i, candidate := range points {
			superseded := false
			for j, other := range points {
				if i == j {
					continue
				}
				if other.order.Transaction == candidate.order.Transaction {
					superseded = superseded || other.order.Index > candidate.order.Index
					continue
				}
				seen, err := w.ancestors(other.order.Transaction)
				if err != nil {
					return err
				}
				superseded = superseded || seen[candidate.order.Transaction]
			}
			if !superseded {
				standing = append(standing, candidate)
			}
		}
		outcomes := map[string]bool{}
		dispute := syncFocusDispute{Effective: effective}
		for _, candidate := range standing {
			outcomes[string(candidate.focus.Event)+"\x00"+candidate.focus.Text] = true
			dispute.Records = append(dispute.Records, candidate.id)
		}
		if len(outcomes) < 2 {
			continue
		}
		sort.Strings(dispute.Records)
		disputes = append(disputes, dispute)
	}
	sort.Slice(disputes, func(i, j int) bool { return disputes[i].Effective < disputes[j].Effective })
	w.state.Disputes = disputes
	return nil
}

// focusDisputesFor reads the disputed days of the journal that exchanges the
// archive at archivePath. A journal that does not take part in sync has none,
// and neither does one whose sync state cannot be read: a dispute is something
// history says, and without history the archive is read as it always was.
func focusDisputesFor(archivePath string) []syncFocusDispute {
	dir := filepath.Dir(archivePath)
	name, ok := readSyncClaim(dir)
	if !ok {
		return nil
	}
	path := SyncStatePathFor(filepath.Join(dir, name))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	state, err := decodeSyncState(path, data)
	if err != nil {
		return nil
	}
	return state.Disputes
}

// FocusCandidate is one of the focus changes competing for a disputed day.
type FocusCandidate struct {
	// Record is the archive record that made the change.
	Record string
	Event  FocusEventKind
	// Text is the focus the change set, empty for a clear.
	Text string
}

// DisputeSummary says what each competing change set, one per line, for a
// reader who has to choose between them. It is empty for a settled focus.
func (s FocusState) DisputeSummary() string {
	if !s.Disputed {
		return ""
	}
	lines := []string{fmt.Sprintf("Changed on more than one device for %s, and not settled:", s.Effective)}
	for _, candidate := range s.Candidates {
		if candidate.Event == FocusEventClear {
			lines = append(lines, "- (cleared)")
			continue
		}
		lines = append(lines, "- "+strings.Join(strings.Fields(candidate.Text), " "))
	}
	return strings.Join(lines, "\n")
}

// disputed marks a focus state as disputed when the change that decides it is
// one of several made independently for the same day. A disputed state is not
// active and carries no text: there is no focus to present as settled.
func (a ReviewArchive) disputed(state FocusState, date string, disputes []syncFocusDispute) FocusState {
	if len(disputes) == 0 {
		return state
	}
	effective := ""
	for _, event := range a.FocusEvents() {
		if event.Effective <= date && event.Effective >= effective {
			effective = event.Effective
		}
	}
	for _, dispute := range disputes {
		if dispute.Effective != effective {
			continue
		}
		competing := map[string]bool{}
		for _, id := range dispute.Records {
			competing[id] = true
		}
		var candidates []FocusCandidate
		for _, event := range a.FocusEvents() {
			if competing[event.ID] {
				candidates = append(candidates, FocusCandidate{Record: event.ID, Event: event.Event, Text: event.Text})
			}
		}
		if len(candidates) < 2 {
			return state
		}
		return FocusState{Disputed: true, Effective: effective, Candidates: candidates}
	}
	return state
}

// ResolveFocus is the focus in effect on a date for the archive at
// archivePath, read as parsed: FocusOn, with a day two devices changed
// independently reported as disputed rather than decided by file order.
func ResolveFocus(archive ReviewArchive, archivePath, date string) FocusState {
	return archive.disputed(archive.FocusOn(date), date, focusDisputesFor(archivePath))
}

// SyncConflictVersion is one version of an artifact in a conflict.
type SyncConflictVersion struct {
	// Revision is RevisionAbsent for a deletion, or for a base that did not
	// exist.
	Revision Revision
	// Transaction and Device say who wrote it, when that is known.
	Transaction string
	Device      string
	// Content is the version's bytes, empty for a deletion.
	Content string
}

// Deleted reports whether the version is the artifact's absence.
func (v SyncConflictVersion) Deleted() bool { return v.Revision == RevisionAbsent }

// SyncConflict is an artifact that waits for the author to choose.
type SyncConflict struct {
	// ID names the conflict by the versions competing in it. It is the same on
	// every device that holds the same versions, and changes when they do.
	ID       string
	Artifact string
	// Date is the day concerned, for an entry.
	Date   string
	Reason string
	// Base is what the competing versions were made on.
	Base SyncConflictVersion
	// Ours is the version this device's file holds.
	Ours SyncConflictVersion
	// Theirs are the versions held beside it.
	Theirs []SyncConflictVersion
}

// Versions lists every competing revision, sorted: what two devices agree on
// when they hold the same conflict.
func (c SyncConflict) Versions() []Revision {
	versions := []Revision{c.Ours.Revision}
	for _, other := range c.Theirs {
		versions = append(versions, other.Revision)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions
}

func (s syncState) conflictID(artifact string) string {
	versions := []string{string(s.recorded(artifact))}
	for _, other := range s.Conflicts[artifact].Theirs {
		versions = append(versions, string(other.Digest))
	}
	return conflictID(artifact, versions)
}

// SyncConflicts lists the artifacts that wait for the author to choose, with
// every version held of each. It changes nothing.
func (j *Journal) SyncConflicts() ([]SyncConflict, error) {
	state, enrolled, err := j.readSyncState()
	if err != nil {
		return nil, err
	}
	if !enrolled {
		return nil, ErrSyncNotEnrolled
	}
	objects := filepath.Join(SyncDirFor(j.reflectionsPath), syncObjectsName)
	refs, err := listSyncManifests(filepath.Join(SyncDirFor(j.reflectionsPath), syncTransactionsName))
	if err != nil {
		return nil, fmt.Errorf("listing the sync history: %w", err)
	}
	devices := make(map[string]string, len(refs))
	for _, ref := range refs {
		devices[ref.ID] = ref.Device
	}
	version := func(revision Revision, transaction string) (SyncConflictVersion, error) {
		found := SyncConflictVersion{Revision: revision, Transaction: transaction, Device: devices[transaction]}
		if revision == RevisionAbsent {
			return found, nil
		}
		data, err := readSyncObject(objects, revision)
		if err != nil {
			return found, fmt.Errorf("reading a held version: %w", err)
		}
		found.Content = string(data)
		return found, nil
	}

	artifacts := make([]string, 0, len(state.Conflicts))
	for artifact := range state.Conflicts {
		artifacts = append(artifacts, artifact)
	}
	sort.Strings(artifacts)
	conflicts := make([]SyncConflict, 0, len(artifacts))
	for _, artifact := range artifacts {
		held := state.Conflicts[artifact]
		conflict := SyncConflict{
			ID:       state.conflictID(artifact),
			Artifact: artifact,
			Date:     strings.TrimPrefix(artifact, entryArtifactPrefix),
			Reason:   held.Reason,
		}
		if conflict.Date == artifact {
			conflict.Date = ""
		}
		if conflict.Ours, err = version(state.recorded(artifact), state.Artifacts[artifact].Transaction); err != nil {
			return nil, fmt.Errorf("%s: %w", artifact, err)
		}
		// The base is for the reader to compare against; a conflict is still
		// one without it.
		if conflict.Base, err = version(held.Base, ""); err != nil {
			conflict.Base = SyncConflictVersion{Revision: held.Base}
		}
		for _, other := range held.Theirs {
			theirs, err := version(other.Digest, other.Transaction)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", artifact, err)
			}
			conflict.Theirs = append(conflict.Theirs, theirs)
		}
		conflicts = append(conflicts, conflict)
	}
	return conflicts, nil
}

// SyncResolution is the author's answer to a conflict.
type SyncResolution struct {
	// Keep is the revision to keep: one of the conflict's versions, or
	// RevisionAbsent to keep a deletion that competes in it.
	Keep Revision
	// Content is what to write instead of any of them. It is used when Keep
	// is empty.
	Content string
}

// SyncResolved reports what ResolveSyncConflict did.
type SyncResolved struct {
	Artifact string
	// Revision is what the artifact now holds.
	Revision Revision
	// Transaction is the resolution, to be published like any other.
	Transaction string
	// Written are the journal files the resolution replaced.
	Written []string
}

// StaleConflictError refuses an answer to a conflict that is no longer the one
// the author was shown: a version in it changed, another arrived, or it was
// settled elsewhere. The journal's Markdown was not touched; an edit found in
// it is recorded, so the conflict listed next is the one that is there now.
type StaleConflictError struct {
	ID string
}

func (e *StaleConflictError) Error() string {
	return fmt.Sprintf("conflict %s is no longer what it was when it was opened: a version in it changed or it was settled meanwhile; "+
		"look at the conflicts again and answer what is there now (the journal was not changed)", ShortRecordID(e.ID))
}

// ErrNothingToRecover reports a recovery asked of a journal file that needs
// none.
var ErrNothingToRecover = errors.New("that journal file is being read normally; there is nothing to recover")

// syncEdit runs one change to an enrolled journal that manages its own
// history: it records what the files hold first, lets edit change the working
// journal and the state, then writes the files that changed and the history
// together.
func (j *Journal) syncEdit(edit func(w *syncWork, files journalFiles, working *workingJournal) error) ([]string, error) {
	var written []string
	err := j.Commit(func(c *Commit) error {
		c.syncSealed = true
		written = nil
		w, enrolled, err := beginSyncWork(c, j.reflectionsPath)
		if err != nil {
			return err
		}
		if !enrolled {
			return ErrSyncNotEnrolled
		}
		files := w.openJournalFiles()
		if err := w.observe(files.views); err != nil {
			return err
		}
		working := newWorkingJournal(files.views)
		if err := edit(w, files, &working); err != nil {
			return err
		}
		written, err = w.finish(files, working)
		return err
	})
	return written, err
}

// finish stages every journal file the working journal no longer matches, then
// the history. It returns the files staged.
func (w *syncWork) finish(files journalFiles, working workingJournal) ([]string, error) {
	var written []string
	for _, source := range syncSources {
		if !working.dirty[source] {
			continue
		}
		tx := files.tx[source]
		if tx == nil {
			return nil, fmt.Errorf("the %s file changed under the import", source)
		}
		if err := tx.Commit(working.render(source)); err != nil {
			return nil, err
		}
		written = append(written, w.path(source))
	}
	// An archive that cannot be read says nothing about the focus.
	records := working.records
	if _, unusable := w.unusable[syncSourceArchive]; unusable {
		records = nil
	}
	return written, w.stage(records)
}

// place puts the working archive in the order history gives it and holds every
// source the edit touched to the rules a local command holds its file to.
func (w *syncWork) place(working *workingJournal) error {
	if working.reorder {
		working.order(func(artifact string) *syncOrder { return w.state.Artifacts[artifact].Order }, w.state.Genesis)
		working.reorder = false
	}
	for _, source := range syncSources {
		if !working.touched[source] {
			continue
		}
		if err := working.validate(source, w.path(source)); err != nil {
			return err
		}
	}
	working.touched = map[syncSource]bool{}
	return nil
}

// ResolveSyncConflict answers the conflict named id: it writes the chosen
// version to the journal and records the resolution as a transaction that
// covers every version that competed, so each device holding one of them takes
// the same result.
//
// The conflict is looked at again under the lock, after anything an editor
// changed is recorded. When it is no longer the one named, the journal is left
// as it is and the error is a *StaleConflictError.
func (j *Journal) ResolveSyncConflict(id string, resolution SyncResolution) (SyncResolved, error) {
	var resolved SyncResolved
	var stale *StaleConflictError
	written, err := j.syncEdit(func(w *syncWork, _ journalFiles, working *workingJournal) error {
		resolved, stale = SyncResolved{}, nil
		artifact := ""
		for candidate := range w.state.Conflicts {
			if w.state.conflictID(candidate) == id {
				artifact = candidate
			}
		}
		if artifact == "" {
			// What an editor changed stays recorded, so the conflict the
			// author is shown next is the one that is there now.
			stale = &StaleConflictError{ID: id}
			return nil
		}
		source, _ := artifactSource(artifact)
		if problem, unusable := w.unusable[source]; unusable {
			return fmt.Errorf("the conflict cannot be settled while its file cannot be written: %s", problem)
		}

		ours := w.state.recorded(artifact)
		versions := []Revision{ours}
		for _, other := range w.state.Conflicts[artifact].Theirs {
			versions = append(versions, other.Digest)
		}
		result := resolution.Keep
		var contents []byte
		switch {
		case result == "":
			contents = []byte(resolution.Content)
			if source == syncSourceReflections {
				contents = []byte(trimEntryFraming(resolution.Content))
			}
			if err := validateArtifactContent(artifact, contents); err != nil {
				return fmt.Errorf("that cannot be written as %s: %w (nothing was written)", artifact, err)
			}
			result = RevisionOf(contents)
		case !holdsRevision(versions, result):
			return fmt.Errorf("revision %s is not one of the versions competing for %s (nothing was written)", result, artifact)
		case result != RevisionAbsent:
			var err error
			if contents, err = w.object(result); err != nil {
				return fmt.Errorf("reading the version to keep: %w", err)
			}
		}

		change, ok := coveringChange(artifact, versions, result)
		if !ok {
			return fmt.Errorf("the versions competing for %s are all the same", artifact)
		}
		if err := change.check(); err != nil {
			return fmt.Errorf("%w (nothing was written)", err)
		}
		if result != ours {
			working.set(artifact, contents, result == RevisionAbsent)
		}
		kept := map[Revision][]byte{}
		if result != RevisionAbsent {
			kept[result] = contents
		}
		transaction, err := w.record(SyncOriginResolution, []SyncChange{change}, kept)
		if err != nil {
			return err
		}
		if err := w.place(working); err != nil {
			return fmt.Errorf("the journal would not accept that: %w (nothing was written)", err)
		}
		resolved = SyncResolved{Artifact: artifact, Revision: result, Transaction: transaction}
		return nil
	})
	if err == nil && stale != nil {
		return SyncResolved{}, stale
	}
	resolved.Written = written
	if err != nil {
		return committedOutcome(resolved, err)
	}
	return resolved, nil
}

func holdsRevision(versions []Revision, revision Revision) bool {
	for _, version := range versions {
		if version == revision {
			return true
		}
	}
	return false
}

// FocusDispute is a day whose focus two devices changed independently.
type FocusDispute struct {
	// ID names the dispute by the changes competing in it.
	ID        string
	Effective string
	// Candidates are the competing changes, in archive order.
	Candidates []FocusCandidate
}

// FocusDisputes lists the days whose focus is disputed. A journal that does
// not take part in sync has none. It changes nothing.
func (j *Journal) FocusDisputes() ([]FocusDispute, error) {
	archive, exists, err := j.ReadArchive()
	if err != nil || !exists {
		return nil, err
	}
	var disputes []FocusDispute
	for _, dispute := range focusDisputesFor(j.archivePath) {
		state := archive.disputed(FocusState{}, dispute.Effective, []syncFocusDispute{dispute})
		if !state.Disputed {
			continue
		}
		disputes = append(disputes, FocusDispute{ID: dispute.id(), Effective: dispute.Effective, Candidates: state.Candidates})
	}
	return disputes, nil
}

// FocusResolution is the author's answer to a disputed focus.
type FocusResolution struct {
	// Record keeps what one of the competing changes said.
	Record string
	// Text sets a focus of the author's own instead. It is used when Record is
	// empty; with both empty the focus for that day is cleared.
	Text string
}

// ResolveFocusDispute settles the disputed focus named id by adding one focus
// change for the same day, made with every competing change in sight. No
// record is rewritten or removed: the competing changes stay in the archive.
//
// Like a conflict, a dispute that is no longer the one named is refused with a
// *StaleConflictError.
func (j *Journal) ResolveFocusDispute(id string, resolution FocusResolution) (FocusChange, error) {
	var change FocusChange
	var stale *StaleConflictError
	_, err := j.syncEdit(func(w *syncWork, _ journalFiles, working *workingJournal) error {
		change, stale = FocusChange{}, nil
		if problem, unusable := w.unusable[syncSourceArchive]; unusable {
			return fmt.Errorf("the focus cannot be settled while the archive cannot be written: %s", problem)
		}
		if w.archiveChanged {
			if err := w.refreshDisputes(working.records); err != nil {
				return err
			}
		}
		var dispute *syncFocusDispute
		for i := range w.state.Disputes {
			if w.state.Disputes[i].id() == id {
				dispute = &w.state.Disputes[i]
			}
		}
		if dispute == nil {
			stale = &StaleConflictError{ID: id}
			return nil
		}

		event, origin, source, text := FocusEventSet, FocusOriginManual, "", strings.TrimSpace(resolution.Text)
		switch {
		case resolution.Record != "":
			if !holdsRevision(revisionsOf(dispute.Records), Revision(resolution.Record)) {
				return fmt.Errorf("record %s is not one of the focus changes competing for %s (nothing was written)",
					ShortRecordID(resolution.Record), dispute.Effective)
			}
			at, found := sourceView{units: working.records}.find(recordArtifact(resolution.Record))
			if !found {
				stale = &StaleConflictError{ID: id}
				return nil
			}
			kept := ParseReviewArchive(string(working.records[at].data)).Records[0].Focus
			event, origin, source, text = kept.Event, kept.Origin, kept.Source, kept.Text
		case text == "":
			event = FocusEventClear
		}

		record := NewFocusRecord(j.clock(), event, dispute.Effective, origin, source, text)
		artifact := recordArtifact(record.Focus.ID)
		if _, exists := (sourceView{units: working.records}).find(artifact); exists {
			return fmt.Errorf("the archive already holds that exact focus change, made at this same instant; try again (nothing was written)")
		}
		contents := []byte(normalizeRecordRaw(record.Raw))
		working.set(artifact, contents, false)
		digest := RevisionOf(contents)
		if _, err := w.record(SyncOriginResolution,
			[]SyncChange{{Artifact: artifact, Base: RevisionAbsent, New: digest}}, map[Revision][]byte{digest: contents}); err != nil {
			return err
		}
		if err := w.place(working); err != nil {
			return fmt.Errorf("the archive would not accept that: %w (nothing was written)", err)
		}
		change = FocusChange{Changed: true, Effective: dispute.Effective, Text: record.Focus.Text, Record: record}
		return nil
	})
	if err == nil && stale != nil {
		return FocusChange{}, stale
	}
	if err != nil {
		return committedOutcome(change, err)
	}
	return change, nil
}

func revisionsOf(ids []string) []Revision {
	revisions := make([]Revision, len(ids))
	for i, id := range ids {
		revisions[i] = Revision(id)
	}
	return revisions
}

// SyncRecovery is what to do about a journal file that is not being read.
type SyncRecovery string

const (
	// SyncRecoverRestore puts back, from what history holds, every artifact
	// the file no longer has. A file that cannot be read at all is replaced
	// whole; what it held stays in its `.bak`.
	SyncRecoverRestore SyncRecovery = "restore"
	// SyncRecoverAcceptDeletions records what is missing from the file as
	// deleted, on the author's word.
	SyncRecoverAcceptDeletions SyncRecovery = "accept-deletions"
)

// SyncRecovered reports what RecoverSyncFile did.
type SyncRecovered struct {
	// Restored are the artifacts put back in the file.
	Restored []string
	// Deleted are the artifacts recorded as deleted.
	Deleted []string
	// Written are the journal files replaced.
	Written []string
}

// RecoverSyncFile ends the suspension of one of the journal's files: "reflections",
// "archive", or "settings". A file is suspended when it went missing, was
// emptied, or lost most of what was recorded of it at once; until the author
// says which it was, nothing is read from it as a deletion and nothing arriving
// is written to it.
//
// Restoring writes back what history holds and deletes nothing. Accepting the
// deletions records them, and they reach the other devices like any change.
// The settings cannot be deleted, only restored. A file that is being read
// normally is refused with ErrNothingToRecover.
func (j *Journal) RecoverSyncFile(file string, action SyncRecovery) (SyncRecovered, error) {
	source := syncSource(file)
	if _, known := map[syncSource]bool{syncSourceReflections: true, syncSourceArchive: true, syncSourceSettings: true}[source]; !known {
		return SyncRecovered{}, fmt.Errorf("%q is not one of the journal's files: reflections, archive, or settings", file)
	}
	var recovered SyncRecovered
	var err error
	switch action {
	case SyncRecoverRestore:
		recovered, err = j.restoreSyncFile(source)
	case SyncRecoverAcceptDeletions:
		if source == syncSourceSettings {
			return SyncRecovered{}, errors.New("the journal settings cannot be deleted; restore them instead")
		}
		recovered, err = j.acceptSyncDeletions(source)
	default:
		return SyncRecovered{}, fmt.Errorf("%q is not a recovery: restore or accept-deletions", action)
	}
	if err != nil {
		return committedOutcome(recovered, err)
	}
	return recovered, nil
}

func (j *Journal) restoreSyncFile(source syncSource) (SyncRecovered, error) {
	var recovered SyncRecovered
	written, err := j.syncEdit(func(w *syncWork, files journalFiles, working *workingJournal) error {
		recovered = SyncRecovered{}
		if _, unusable := w.unusable[source]; !unusable {
			return ErrNothingToRecover
		}
		view := files.views[source]
		if view.problem != "" && source == syncSourceArchive {
			// Nothing vouches for the header of an archive that cannot be read.
			working.archive = ReviewArchive{}
		}
		for _, artifact := range w.state.held(source) {
			if _, found := view.find(artifact); found {
				continue
			}
			contents, err := w.object(w.state.recorded(artifact))
			if err != nil {
				return fmt.Errorf("history no longer holds %s to restore it from: %w", artifact, err)
			}
			working.set(artifact, contents, false)
			recovered.Restored = append(recovered.Restored, artifact)
		}
		// A file that could not be read is replaced even when history has
		// nothing to put in it.
		working.dirty[source] = true
		working.touched[source] = true
		if err := w.place(working); err != nil {
			return fmt.Errorf("the restored %s file would not be one seneca writes: %w (nothing was written)", source, err)
		}
		delete(w.state.Suspended, source)
		delete(w.unusable, source)
		return nil
	})
	recovered.Written = written
	return recovered, err
}

func (j *Journal) acceptSyncDeletions(source syncSource) (SyncRecovered, error) {
	var recovered SyncRecovered
	err := j.Commit(func(c *Commit) error {
		c.syncSealed = true
		recovered = SyncRecovered{}
		w, enrolled, err := beginSyncWork(c, j.reflectionsPath)
		if err != nil {
			return err
		}
		if !enrolled {
			return ErrSyncNotEnrolled
		}
		files := w.openJournalFiles()
		if problem := files.views[source].problem; problem != "" {
			return fmt.Errorf("the %s file cannot be read, so nothing in it can be told from what is missing; repair or restore it: %s", source, problem)
		}
		// Reading the files settles which of them are suspended; only then is
		// there something to accept.
		w.unrecorded(files.views)
		if w.state.Suspended[source] == "" {
			return ErrNothingToRecover
		}
		w.accepted[source] = true
		w.diagnostics = nil
		changes, contents := w.unrecorded(files.views)
		for _, change := range changes {
			if of, _ := artifactSource(change.Artifact); of == source && change.Deleted {
				recovered.Deleted = append(recovered.Deleted, change.Artifact)
			}
		}
		if len(changes) > 0 {
			if _, err := w.record(SyncOriginExternal, changes, contents); err != nil {
				return err
			}
		}
		_, err = w.finish(files, newWorkingJournal(files.views))
		return err
	})
	return recovered, err
}
