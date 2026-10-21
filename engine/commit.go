package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A commit is every file one journal operation changes, written as one unit
// under the journal lock.
//
// # Shape
//
//	err := journal.Commit(func(c *Commit) error {
//		tx, err := c.Reflections()   // re-read under the lock
//		...                          // validate, check the base revision
//		return tx.CommitEntries(...) // staged, not yet written
//	})
//
// The function passed to Commit runs with the lock held, so it must only read,
// validate, and stage: no waiting on a person, no other program. Everything it
// opens is read after the lock is taken, which is what makes a base-revision
// check mean something. Nothing is written until it returns nil.
//
// # One file
//
// A commit that stages one file writes it exactly as a FileTransaction always
// has: back up, then one rename. One rename is already atomic and needs no
// journal.
//
// # Several files
//
// A commit that stages several files — Markdown with the settings, sync
// records, or import receipts that describe it — goes through a commit record
// kept in `<dir>/.seneca/directory/commit/`:
//
//  1. Every file's new contents are written there, named by their digest.
//  2. Every `.bak` is written.
//  3. `record.json` is written, naming each file, the revision it replaces, and
//     the revision it becomes. This rename is the commit point.
//  4. Each file is replaced.
//  5. The record is removed, then the staged contents.
//
// A failure in steps 1–3 leaves every file untouched and is an ordinary,
// retryable error. A failure in step 4 before any file was replaced abandons
// the record and is the same. A failure once a file has been replaced leaves the
// record in place and is a *PendingCommitError: the change is recorded and the
// next operation finishes it.
//
// # Recovery
//
// Every commit, and every read through a Journal, first looks for a record. No
// record means the interrupted commit never reached its commit point: leftover
// staged contents are discarded and the files are as they were. A record means
// it did: each file still at the revision the record replaced is written, each
// file already at its new revision is skipped, and the record is removed. A
// file at neither revision was changed by something else in between; recovery
// stops with a *RecoveryError and writes nothing, because choosing between the
// two is the author's call.

// Revision identifies a file's or an entry's bytes. It is the base an edit was
// made on: a save carries it back so that a change made meanwhile is noticed
// instead of overwritten.
type Revision string

// RevisionAbsent is the revision of something that does not exist.
const RevisionAbsent Revision = "absent"

const revisionDigestPrefix = "sha256:"

// RevisionOf is the revision of data.
func RevisionOf(data []byte) Revision {
	sum := sha256.Sum256(data)
	return Revision(revisionDigestPrefix + hex.EncodeToString(sum[:]))
}

// CommitStage names a boundary of a grouped commit, for FS.CommitStage.
type CommitStage string

const (
	// CommitStaged: the new contents are in the commit directory; no file,
	// backup, or record has been written.
	CommitStaged CommitStage = "staged"
	// CommitBackedUp: the `.bak` siblings are written; there is no record yet.
	CommitBackedUp CommitStage = "backed-up"
	// CommitRecorded: the record is in place. This is the commit point.
	CommitRecorded CommitStage = "recorded"
	// CommitApplied: one more file holds its new contents; applied counts them.
	CommitApplied CommitStage = "applied"
	// CommitCleared: the record is removed; staged contents may remain.
	CommitCleared CommitStage = "cleared"
)

const (
	commitDirName       = "commit"
	commitRecordName    = "record.json"
	commitBlobSuffix    = ".blob"
	commitRecordVersion = 1
	commitPrivateMode   = 0600
)

// CommitDirFor is where a journal in dir keeps the record of a grouped commit
// while it is being written. It exists only during one, or after a crash.
func CommitDirFor(dir string) string {
	return filepath.Join(dir, JournalMetadataRootName, JournalMetadataDirectoryName, commitDirName)
}

type commitRecord struct {
	Version int                `json:"version"`
	Files   []commitRecordFile `json:"files"`
}

type commitRecordFile struct {
	// Path is slash-separated and relative to the journal directory when the
	// file is inside it, so a journal that moved still recovers.
	Path   string   `json:"path"`
	Before Revision `json:"before"`
	After  Revision `json:"after"`
	Blob   string   `json:"blob"`
	Mode   uint32   `json:"mode"`
	// Artifact marks Seneca's own metadata, whose directory the write creates.
	Artifact bool `json:"artifact,omitempty"`
}

// Commit collects the files one journal operation changes. It exists only
// inside Journal.Commit, while the journal lock is held.
type Commit struct {
	journal *Journal
	staged  []stagedChange
	// syncSealed is set by an operation that writes the sync history itself, so
	// the commit does not record it a second time.
	syncSealed bool
}

type stagedChange struct {
	tx   *FileTransaction
	data []byte
}

// Commit runs build under the journal lock and then writes everything it
// staged as one unit. An interrupted earlier commit is completed first. build
// must not wait on a person or run another program: the lock is held until
// Commit returns.
//
// A *CommittedWriteError or *PendingCommitError means the change landed, or is
// recorded and will; every other error means no file changed.
func (j *Journal) Commit(build func(c *Commit) error) error {
	lock, err := j.lock()
	if err != nil {
		return err
	}
	defer lock.release()

	if err := j.recoverLocked(); err != nil {
		return err
	}
	c := &Commit{journal: j}
	if err := build(c); err != nil {
		return err
	}
	c.recordSync()
	return c.apply()
}

// Recover completes a grouped commit that was interrupted, if there is one. It
// is cheap when there is none and creates nothing. Every read through a
// Journal calls it, so a reader never sees half of a grouped change.
func (j *Journal) Recover() error {
	pending := false
	for _, dir := range j.lockDirs() {
		if _, err := os.Lstat(filepath.Join(CommitDirFor(dir), commitRecordName)); err == nil {
			pending = true
		}
	}
	if !pending {
		return nil
	}

	lock, err := j.lock()
	if err != nil {
		return err
	}
	defer lock.release()
	return j.recoverLocked()
}

func (c *Commit) adopt(tx *FileTransaction) {
	tx.stage = c.stage
}

func (c *Commit) stage(tx *FileTransaction, data []byte) error {
	for _, staged := range c.staged {
		if same, err := SameFilePath(staged.tx.path, tx.path); err != nil {
			return err
		} else if same {
			return fmt.Errorf("%s is already part of this commit", tx.path)
		}
	}
	c.staged = append(c.staged, stagedChange{tx: tx, data: append([]byte(nil), data...)})
	return nil
}

// Reflections re-reads the reflections file for this commit.
func (c *Commit) Reflections() (*ReflectionTransaction, error) {
	if err := c.journal.requireReflections(); err != nil {
		return nil, err
	}
	tx, err := c.journal.fs.OpenReflectionFile(c.journal.reflectionsPath)
	if err != nil {
		return nil, err
	}
	c.adopt(tx.FileTransaction)
	return tx, nil
}

// Archive re-reads the review archive for this commit.
func (c *Commit) Archive() (*ArchiveTransaction, error) {
	tx, err := c.journal.fs.OpenReviewArchive(c.journal.archivePath)
	if err != nil {
		return nil, err
	}
	c.adopt(tx.FileTransaction)
	return tx, nil
}

// Metadata re-reads one of Seneca's own files under the journal's `.seneca/`
// directory for this commit: journal settings, a sync record, an import
// receipt. It gets no `.bak`, and its directory is created when it is written.
func (c *Commit) Metadata(path string) (*FileTransaction, error) {
	root, rel, ok := c.journal.metadataRoot(path)
	if !ok {
		return nil, fmt.Errorf("%s is not inside the journal's metadata directory %s", path, root)
	}
	reserved := filepath.Join(JournalMetadataDirectoryName, commitDirName)
	if rel == reserved || strings.HasPrefix(rel, reserved+string(filepath.Separator)) ||
		rel == filepath.Join(JournalMetadataDirectoryName, journalLockFileName) {
		return nil, fmt.Errorf("%s is reserved for the journal's own commits", path)
	}
	tx, err := c.journal.fs.BeginFileTransaction(path)
	if err != nil {
		return nil, err
	}
	tx.artifact = true
	c.adopt(tx)
	return tx, nil
}

// metadataRoot finds the `.seneca/` directory path lies in, among the
// directories this journal locks, and path relative to it. When it lies in
// none, root is the journal's own, for the refusal to name.
func (j *Journal) metadataRoot(path string) (root, rel string, ok bool) {
	dirs := append([]string{j.homeDir()}, j.lockDirs()...)
	for _, dir := range dirs {
		root := filepath.Join(dir, JournalMetadataRootName)
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return root, rel, true
		}
	}
	return filepath.Join(j.homeDir(), JournalMetadataRootName), "", false
}

func (c *Commit) apply() error {
	switch len(c.staged) {
	case 0:
		return nil
	case 1:
		change := c.staged[0]
		if !change.tx.artifact {
			return change.tx.commitDirect(change.data)
		}
		created, err := makeArtifactDir(change.tx.path)
		if err != nil {
			return err
		}
		err = change.tx.commitDirect(change.data)
		if err != nil && !WriteCommitted(err) && created {
			pruneEmptyDirs(filepath.Dir(change.tx.path), c.journal.homeDir())
		}
		return err
	}
	return c.journal.applyGroup(c.staged)
}

// makeArtifactDir creates the directory a metadata file is written into, and
// reports whether it had to.
func makeArtifactDir(path string) (bool, error) {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("creating %s: %w", dir, err)
	}
	return true, nil
}

// uncommitted strips the committed mark from a failure to write something that
// is not a journal file: staged contents and the record are scaffolding, and a
// commit abandoned over them has changed nothing.
func uncommitted(err error) error {
	var committed *CommittedWriteError
	if errors.As(err, &committed) {
		return fmt.Errorf("%s was written but not flushed to disk: %w", committed.Path, committed.err)
	}
	return err
}

func (fs FS) commitStage(stage CommitStage, applied int) error {
	if fs.CommitStage == nil {
		return nil
	}
	return fs.CommitStage(stage, applied)
}

// recordPathFor names path the way a record stores it.
func recordPathFor(home, path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	if rel, err := filepath.Rel(home, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return path
}

func recordedPath(home, recorded string) string {
	if filepath.IsAbs(recorded) {
		return recorded
	}
	return filepath.Join(home, filepath.FromSlash(recorded))
}

// applyGroup writes several files as one recoverable unit; see the top of this
// file for the steps.
func (j *Journal) applyGroup(staged []stagedChange) error {
	fs := j.fs.resolved()
	home := j.homeDir()
	dir := CommitDirFor(home)

	for _, change := range staged {
		if err := refuseSymlink(change.tx.path); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("preparing the commit directory %s: %w", dir, err)
	}
	abandon := func(cause error) error {
		if err := clearCommitDir(dir); err != nil {
			return &PendingCommitError{Record: filepath.Join(dir, commitRecordName), err: cause}
		}
		return cause
	}

	record := commitRecord{Version: commitRecordVersion}
	for _, change := range staged {
		after := RevisionOf(change.data)
		blob := strings.TrimPrefix(string(after), revisionDigestPrefix) + commitBlobSuffix
		if err := fs.AtomicWriteFile(filepath.Join(dir, blob), change.data, commitPrivateMode); err != nil {
			return abandon(fmt.Errorf("staging %s: %w", change.tx.path, uncommitted(err)))
		}
		record.Files = append(record.Files, commitRecordFile{
			Path:     recordPathFor(home, change.tx.path),
			Before:   change.tx.Revision(),
			After:    after,
			Blob:     blob,
			Mode:     uint32(change.tx.mode),
			Artifact: change.tx.artifact,
		})
	}
	if err := fs.commitStage(CommitStaged, 0); err != nil {
		return err
	}

	for _, change := range staged {
		if !change.tx.needsBackup() {
			continue
		}
		if err := fs.Backup(change.tx.path, change.tx.original, change.tx.mode); err != nil {
			return abandon(err)
		}
	}
	if err := fs.commitStage(CommitBackedUp, 0); err != nil {
		return err
	}

	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return abandon(fmt.Errorf("encoding the commit record: %w", err))
	}
	if err := fs.AtomicWriteFile(filepath.Join(dir, commitRecordName), append(encoded, '\n'), commitPrivateMode); err != nil {
		return abandon(fmt.Errorf("recording the commit: %w", uncommitted(err)))
	}
	if err := fs.commitStage(CommitRecorded, 0); err != nil {
		return err
	}
	return j.redo(home, record, true)
}

// recoverLocked completes or discards whatever an interrupted commit left in
// each directory this journal locks. The caller holds the lock.
func (j *Journal) recoverLocked() error {
	for _, home := range j.lockDirs() {
		dir := CommitDirFor(home)
		data, err := os.ReadFile(filepath.Join(dir, commitRecordName))
		if errors.Is(err, os.ErrNotExist) {
			// No record: the commit never reached its commit point, so what it
			// staged is discarded and the files are as they were.
			if err := clearCommitDir(dir); err != nil {
				return &RecoveryError{Dir: dir, err: err}
			}
			continue
		}
		if err != nil {
			return &RecoveryError{Dir: dir, err: err}
		}

		var record commitRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return &RecoveryError{Dir: dir, err: fmt.Errorf("reading the commit record: %w", err)}
		}
		if record.Version != commitRecordVersion {
			return &RecoveryError{Dir: dir, err: fmt.Errorf("the commit record is version %d, written by a newer seneca", record.Version)}
		}
		if err := j.redo(home, record, false); err != nil {
			return err
		}
	}
	return nil
}

// redo brings every file of a recorded commit to its new revision and removes
// the record. initial is true for the commit that wrote the record, which can
// still back out while no file has been replaced; recovery cannot, because
// nobody is left to be told the save failed.
func (j *Journal) redo(home string, record commitRecord, initial bool) error {
	fs := j.fs.resolved()
	dir := CommitDirFor(home)
	recordFile := filepath.Join(dir, commitRecordName)

	// Decide everything before writing anything.
	pending := make([]bool, len(record.Files))
	for i, file := range record.Files {
		path := recordedPath(home, file.Path)
		current, err := revisionAt(path)
		if err != nil {
			return j.refuseRedo(dir, initial, err)
		}
		switch current {
		case file.After:
		case file.Before:
			pending[i] = true
		default:
			return j.refuseRedo(dir, initial, &StaleFileError{Path: path, Base: file.Before, Current: current})
		}
	}

	var warning error
	applied := 0
	for i, file := range record.Files {
		if !pending[i] {
			continue
		}
		path := recordedPath(home, file.Path)
		err := j.applyRecorded(fs, dir, path, file)
		switch {
		case err == nil:
		case WriteCommitted(err):
			if warning == nil {
				warning = err
			}
		case initial && applied == 0:
			return j.refuseRedo(dir, true, err)
		case initial:
			return &PendingCommitError{Record: recordFile, err: err}
		default:
			return &RecoveryError{Dir: dir, err: err}
		}
		applied++
		if err := fs.commitStage(CommitApplied, applied); err != nil {
			return err
		}
	}

	if err := os.Remove(recordFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		if initial {
			return &PendingCommitError{Record: recordFile, err: fmt.Errorf("removing the commit record: %w", err)}
		}
		return &RecoveryError{Dir: dir, err: err}
	}
	if err := fs.commitStage(CommitCleared, applied); err != nil {
		return err
	}
	// The record is gone, so what is left is litter the next commit sweeps.
	_ = clearCommitDir(dir)
	if initial {
		return warning
	}
	// A flush that failed while finishing an older save is not this
	// operation's to report: the bytes are in place.
	return nil
}

// refuseRedo stops before any file is replaced. The commit that wrote the
// record backs out of it, leaving a plain write-free failure; recovery leaves
// the record for the author to settle.
func (j *Journal) refuseRedo(dir string, initial bool, cause error) error {
	if !initial {
		return &RecoveryError{Dir: dir, err: cause}
	}
	if err := clearCommitDir(dir); err != nil {
		return &PendingCommitError{Record: filepath.Join(dir, commitRecordName), err: cause}
	}
	return cause
}

func (j *Journal) applyRecorded(fs FS, dir, path string, file commitRecordFile) error {
	data, err := os.ReadFile(filepath.Join(dir, filepath.Base(file.Blob)))
	if err != nil {
		return fmt.Errorf("reading the staged contents of %s: %w", path, err)
	}
	if RevisionOf(data) != file.After {
		return fmt.Errorf("the staged contents of %s do not match the commit record", path)
	}
	if file.Artifact {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
		}
	}
	return fs.AtomicWriteFile(path, data, os.FileMode(file.Mode))
}

// revisionAt reads the revision a path holds now.
func revisionAt(path string) (Revision, error) {
	if err := refuseSymlink(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return RevisionAbsent, nil
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return RevisionOf(data), nil
}

// clearCommitDir removes the record first — that is what abandons a commit —
// then everything staged beside it, then the directory. A missing directory is
// already clear.
func clearCommitDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, commitRecordName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
	_ = os.Remove(dir)
	return nil
}
