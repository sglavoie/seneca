package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// The journal lock makes a commit exclusive among Seneca processes: the command
// line, a background sync agent, and anything else that opens the same journal.
//
// # Scope
//
// The lock is taken per directory, at `<dir>/.seneca/directory/lock`, because
// that is the scope of what a commit can touch: the review archive is shared by
// every journal in a directory, so two journals side by side must exclude each
// other when either appends to it. A journal whose archive was opened from a
// second directory takes both locks, always in path order.
//
// # What it covers
//
// Recovery of an interrupted commit, the re-read of every file the commit
// replaces, validation, and the replacement itself. It is never held while a
// person writes or while another program runs: Journal.Commit takes it on entry
// and releases it before returning, and nothing a Journal hands out keeps it.
//
// # Shape on disk
//
// The lock is an advisory lock on an open file, so a process that dies releases
// it with no stale state to clean up. The file is removed on release and the
// metadata directories it needed are pruned when empty, so a journal that holds
// nothing else under `.seneca/` has no `.seneca/` between writes.
//
// A directory that does not exist is not locked: there is nothing in it to
// protect, and creating it is the caller's decision, not the lock's.

const (
	journalLockFileName = "lock"
	// DefaultLockWait is how long a commit waits for another writer before it
	// gives up without writing.
	DefaultLockWait  = 10 * time.Second
	lockPollInterval = 10 * time.Millisecond
)

// JournalBusyError reports that another process held the journal lock for the
// whole wait. Nothing was written.
type JournalBusyError struct {
	Path   string
	Waited time.Duration
}

func (e *JournalBusyError) Error() string {
	return fmt.Sprintf("another seneca process is writing this journal (%s was still locked after %s); nothing was written — try again",
		e.Path, e.Waited)
}

// LockPathFor is the lock guarding every journal file in dir.
func LockPathFor(dir string) string {
	return filepath.Join(dir, JournalMetadataRootName, JournalMetadataDirectoryName, journalLockFileName)
}

// journalLock is every directory lock one commit holds.
type journalLock struct {
	held []*fileLock
}

func (l *journalLock) release() {
	for i := len(l.held) - 1; i >= 0; i-- {
		lock := l.held[i]
		lock.release()
		pruneEmptyDirs(filepath.Dir(lock.path), filepath.Dir(filepath.Dir(filepath.Dir(lock.path))))
	}
	l.held = nil
}

// homeDir is the directory the journal's commit record lives under: the
// reflections file's, or the archive's for a journal opened without one.
func (j *Journal) homeDir() string {
	if j.reflectionsPath != "" {
		return filepath.Dir(j.reflectionsPath)
	}
	return filepath.Dir(j.archivePath)
}

// lockDirs lists the directories this journal's commits can write to, in the
// one order every process takes them.
func (j *Journal) lockDirs() []string {
	seen := map[string]bool{}
	var dirs []string
	for _, path := range []string{j.reflectionsPath, j.archivePath} {
		if path == "" {
			continue
		}
		dir := filepath.Dir(path)
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}

func (j *Journal) lockWait() time.Duration {
	if j.lockWaitFor > 0 {
		return j.lockWaitFor
	}
	return DefaultLockWait
}

// lock takes every directory lock, or none.
func (j *Journal) lock() (*journalLock, error) {
	locks := &journalLock{}
	for _, dir := range j.lockDirs() {
		lock, err := acquireDirLock(dir, j.lockWait())
		if err != nil {
			locks.release()
			return nil, err
		}
		if lock != nil {
			locks.held = append(locks.held, lock)
		}
	}
	return locks, nil
}

// WithDirectoryLock runs fn while holding dir's journal lock, waiting up to
// wait for it; zero means DefaultLockWait. It is for a device-local file under
// dir's `.seneca/` that a command reads, changes, and replaces outside any
// commit, such as the desktop's usage ledger: the lock commits take makes that
// read-modify-write exclusive among Seneca processes too. A dir that does not
// exist runs fn unlocked, as a commit would.
func WithDirectoryLock(dir string, wait time.Duration, fn func() error) error {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if wait <= 0 {
		wait = DefaultLockWait
	}
	lock, err := acquireDirLock(dir, wait)
	if err != nil {
		return err
	}
	if lock != nil {
		locks := &journalLock{held: []*fileLock{lock}}
		defer locks.release()
	}
	return fn()
}

// acquireDirLock waits for dir's lock. It returns nil without an error when dir
// does not exist.
func acquireDirLock(dir string, wait time.Duration) (*fileLock, error) {
	path := LockPathFor(dir)
	attempts := int(wait / lockPollInterval)
	for attempt := 0; ; attempt++ {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return nil, nil
		}
		lock, err := tryDirLock(path)
		if lock != nil {
			return lock, nil
		}
		// The holder before us removes the lock file and prunes its
		// directories as it lets go, so making them and opening the file can
		// fail under our feet. That is contention, not a broken disk: only an
		// error of another kind, or one that outlasts the wait, is reported.
		if err != nil && !lockDirRace(err) {
			return nil, fmt.Errorf("locking journal directory %s: %w", dir, err)
		}
		if attempt >= attempts {
			if err != nil {
				return nil, fmt.Errorf("locking journal directory %s: %w", dir, err)
			}
			return nil, &JournalBusyError{Path: path, Waited: wait}
		}
		time.Sleep(lockPollInterval)
	}
}

// tryDirLock makes the lock's directories and takes the lock without waiting.
// It returns nil with no error when another process holds it.
func tryDirLock(path string) (*fileLock, error) {
	for _, made := range []string{filepath.Dir(filepath.Dir(path)), filepath.Dir(path)} {
		if err := os.Mkdir(made, 0755); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	return tryFileLock(path)
}

// pruneEmptyDirs removes dir and then each parent while they are empty,
// stopping before stop. A directory that still holds something ends the walk.
func pruneEmptyDirs(dir, stop string) {
	for dir != stop && len(dir) > len(stop) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
