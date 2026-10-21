package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Persistence is the single mutation authority for Seneca's on-disk files.
// Nothing outside this file may call os.WriteFile, os.OpenFile, or os.Rename on
// a reflections file, a review archive, or a `.bak` sibling.
//
// # Transaction shape
//
// Every mutation is a read-modify-write:
//
//	tx, err := fs.OpenReflectionFile(path) // read + parse, or an empty new file
//	... derive the new entry list from tx.Entries() ...
//	err = tx.CommitEntries(entries)        // validate, back up, replace atomically
//
// commit refuses to touch the primary file until validation passes, so a
// rejected document leaves the original bytes and the `.bak` sibling untouched.
//
// # Atomic replace
//
// AtomicWriteFile writes a temporary file in the destination's own directory,
// syncs it, closes it, renames it over the destination, then syncs the
// directory. The temporary file is removed on every failure path. The
// destination keeps its original permissions; a new file gets 0644. A symlink
// destination is refused outright: renaming over one would silently replace the
// link instead of its target.
//
// # Failure seams
//
// FS holds the injectable operations (write, sync, close, rename, syncDir,
// backup, and the commit boundaries of commit.go). It is a value carried by
// each transaction, never a process global:
// a Journal is opened with one, and a test that needs a failure at one stage
// opens its own journal with that stage replaced, so fault-injection tests are
// free to run in parallel.
//
// # Committed failures
//
// Exactly one stage runs after the rename: flushing the destination's directory
// so the replacement survives a crash. A failure there is not write-free — the
// destination already holds the new bytes — so it is reported as a
// *CommittedWriteError rather than as an ordinary error. Mutation callers check
// it with WriteCommitted and finish their post-write work (reporting, git)
// through OnCommittedWrite, because rerunning the command would append a second
// archive record or re-save a reflection that is already on disk.
//
// # Locking and grouped commits
//
// A transaction opened directly from an FS is the unlocked primitive. Every
// Journal mutation opens its transactions inside Journal.Commit instead, which
// holds the journal lock (journal_lock.go) from the read to the replacement and
// turns several files changed together into one recoverable unit (commit.go).

const (
	// DefaultFileMode is the permission for a file Seneca creates itself.
	DefaultFileMode os.FileMode = 0644
	BackupSuffix                = ".bak"
	tempFilePattern             = ".seneca-*.tmp"
)

// FS is the injectable filesystem boundary. Every field is a real syscall
// wrapper in production; tests replace individual fields to force a
// deterministic failure at one transactional stage. The zero value is the real
// filesystem: a nil field falls back to its production implementation.
type FS struct {
	WriteFile func(f *os.File, data []byte) (int, error)
	SyncFile  func(f *os.File) error
	CloseFile func(f *os.File) error
	Rename    func(oldpath, newpath string) error
	SyncDir   func(dir *os.File) error
	// Backup persists the pre-mutation bytes beside path. Left nil, it writes
	// path+".bak" atomically through this same FS.
	Backup func(path string, data []byte, mode os.FileMode) error
	// CommitStage is called at each boundary of a grouped commit and of its
	// recovery. An error from it is treated as the process dying right there:
	// it is returned at once and nothing is cleaned up. Left nil, it does
	// nothing; only tests set it.
	CommitStage func(stage CommitStage, applied int) error
}

// DefaultFS returns the real filesystem with every syscall seam filled in, so a
// caller can wrap one stage and still reach the production implementation.
func DefaultFS() FS {
	return FS{
		WriteFile: func(f *os.File, data []byte) (int, error) { return f.Write(data) },
		SyncFile:  func(f *os.File) error { return f.Sync() },
		CloseFile: func(f *os.File) error { return f.Close() },
		Rename:    os.Rename,
		SyncDir:   func(dir *os.File) error { return dir.Sync() },
	}
}

// resolved fills every nil seam with its production implementation.
func (fs FS) resolved() FS {
	defaults := DefaultFS()
	if fs.WriteFile == nil {
		fs.WriteFile = defaults.WriteFile
	}
	if fs.SyncFile == nil {
		fs.SyncFile = defaults.SyncFile
	}
	if fs.CloseFile == nil {
		fs.CloseFile = defaults.CloseFile
	}
	if fs.Rename == nil {
		fs.Rename = defaults.Rename
	}
	if fs.SyncDir == nil {
		fs.SyncDir = defaults.SyncDir
	}
	if fs.Backup == nil {
		seams := fs
		fs.Backup = func(path string, data []byte, mode os.FileMode) error {
			return seams.writeBackupFile(path, data, mode)
		}
	}
	return fs
}

// AtomicWriteFile replaces path's contents in one rename. mode is used only
// when path does not already exist; an existing file keeps its permissions.
func (fs FS) AtomicWriteFile(path string, data []byte, mode os.FileMode) error {
	fs = fs.resolved()
	if err := refuseSymlink(path); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspecting %s: %w", path, err)
	}
	if mode == 0 {
		mode = DefaultFileMode
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, tempFilePattern)
	if err != nil {
		return fmt.Errorf("creating temporary file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	// Every failure below must leave the destination untouched and remove the
	// temporary file.
	cleanup := func(wrapped error) error {
		_ = fs.CloseFile(tmp)
		_ = os.Remove(tmpPath)
		return wrapped
	}

	if err := tmp.Chmod(mode); err != nil {
		return cleanup(fmt.Errorf("setting permissions on temporary file: %w", err))
	}
	if n, err := fs.WriteFile(tmp, data); err != nil {
		return cleanup(fmt.Errorf("writing temporary file: %w", err))
	} else if n != len(data) {
		return cleanup(fmt.Errorf("writing temporary file: wrote %d of %d bytes", n, len(data)))
	}
	if err := fs.SyncFile(tmp); err != nil {
		return cleanup(fmt.Errorf("syncing temporary file: %w", err))
	}
	if err := fs.CloseFile(tmp); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("closing temporary file: %w", err)
	}
	if err := fs.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replacing %s: %w", path, err)
	}

	// Past this point the destination is already replaced, so a failure is a
	// committed one: the caller must not be told the write can be retried.
	if err := fs.syncDirectory(dir); err != nil {
		return &CommittedWriteError{Path: path, err: err}
	}
	return nil
}

// refuseSymlink fails when path itself is a symbolic link. Renaming over one
// would replace the link and leave its target stale.
func refuseSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspecting %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return &SymlinkDestinationError{path: path}
	}
	return nil
}

// syncDirectory flushes the rename itself so the replacement survives a crash.
func (fs FS) syncDirectory(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening directory %s: %w", dir, err)
	}
	if err := fs.SyncDir(handle); err != nil {
		_ = handle.Close()
		return fmt.Errorf("syncing directory %s: %w", dir, err)
	}
	if err := handle.Close(); err != nil {
		return fmt.Errorf("closing directory %s: %w", dir, err)
	}
	return nil
}

// writeBackupFile persists the pre-mutation bytes to path+".bak" atomically.
// Only the latest backup is kept, matching the historical behavior.
func (fs FS) writeBackupFile(path string, data []byte, mode os.FileMode) error {
	err := fs.AtomicWriteFile(path+BackupSuffix, data, mode)
	if err == nil {
		return nil
	}
	// A committed failure on the backup is still write-free for the file being
	// mutated: the backup runs first, so the primary has not been touched.
	// Unwrap it so the mutation is never reported as committed.
	var committed *CommittedWriteError
	if errors.As(err, &committed) {
		return fmt.Errorf("writing backup: %s was replaced but not flushed to disk: %w", committed.Path, committed.err)
	}
	return fmt.Errorf("writing backup: %w", err)
}

// FileTransaction is a read-modify-write over a single file. The bytes read at
// open time are the backup source, so a mutation never backs up content it
// already replaced.
type FileTransaction struct {
	path     string
	original []byte
	exists   bool
	mode     os.FileMode
	fs       FS
	// stage is set while the transaction belongs to a Journal.Commit: Commit
	// then hands the bytes to the commit instead of writing them.
	stage func(tx *FileTransaction, data []byte) error
	// artifact marks Seneca's own metadata: no `.bak` sibling is kept for it
	// and its directory is created by the write.
	artifact bool
}

// BeginFileTransaction reads path. A missing file is not an error: the
// transaction runs in new-file mode and commits with DefaultFileMode.
func (fs FS) BeginFileTransaction(path string) (*FileTransaction, error) {
	tx := &FileTransaction{path: path, mode: DefaultFileMode, fs: fs.resolved()}

	if err := refuseSymlink(path); err != nil {
		return nil, err
	}

	info, err := os.Stat(path)
	switch {
	case err == nil:
		if info.IsDir() {
			return nil, fmt.Errorf("path %s is a directory", path)
		}
		tx.exists = true
		tx.mode = info.Mode().Perm()
	case errors.Is(err, os.ErrNotExist):
		return tx, nil
	default:
		return nil, fmt.Errorf("inspecting %s: %w", path, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	tx.original = data
	return tx, nil
}

// unchanged reports whether data would be a no-op replacement.
func (tx *FileTransaction) Unchanged(data []byte) bool {
	return tx.exists && string(tx.original) == string(data)
}

// Exists reports whether the file was there when the transaction opened.
func (tx *FileTransaction) Exists() bool { return tx.exists }

// Original returns a copy of the bytes read when the transaction opened.
func (tx *FileTransaction) Original() []byte { return append([]byte(nil), tx.original...) }

// Revision identifies what the transaction read: RevisionAbsent for a missing
// file, the digest of its bytes otherwise.
func (tx *FileTransaction) Revision() Revision {
	if !tx.exists {
		return RevisionAbsent
	}
	return RevisionOf(tx.original)
}

// RequireBase refuses a change derived from a revision the file no longer
// holds. base is what the caller read before the author, or another program,
// took their time; the transaction holds what is there now.
func (tx *FileTransaction) RequireBase(base Revision) error {
	if current := tx.Revision(); current != base {
		return &StaleFileError{Path: tx.path, Base: base, Current: current}
	}
	return nil
}

// needsBackup reports whether the pre-mutation bytes are worth a `.bak`. An
// empty or missing original is not backed up, preserving the historical
// behavior, and neither is Seneca's own metadata.
func (tx *FileTransaction) needsBackup() bool {
	return !tx.artifact && tx.exists && len(tx.original) > 0
}

// commit backs up the pre-mutation bytes and then replaces the file. Both
// writes are atomic, and the backup lands first so a failed replacement still
// leaves a recoverable copy. Inside a Journal.Commit the bytes are staged
// instead, and the commit writes them once every file has been validated.
func (tx *FileTransaction) Commit(data []byte) error {
	if tx.stage != nil {
		return tx.stage(tx, data)
	}
	return tx.commitDirect(data)
}

func (tx *FileTransaction) commitDirect(data []byte) error {
	if err := refuseSymlink(tx.path); err != nil {
		return err
	}
	if tx.needsBackup() {
		if err := tx.fs.Backup(tx.path, tx.original, tx.mode); err != nil {
			return err
		}
	}
	return tx.fs.AtomicWriteFile(tx.path, data, tx.mode)
}
