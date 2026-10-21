package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Leaving sync is how a device stops taking part, and how a copy of another
// device's journal stops pretending to be that device. The journal's Markdown,
// archive, and settings stay exactly as they are; the sync history and the
// device identity it carries are moved aside, never deleted, and never used
// again. The journal can then start a new synced journal, or join one as a
// device of its own: its writing is recorded by its first import like any
// other device's, beside what the others hold, never over it.

const syncLeftPrefix = "sync-left-"

// SyncLeft reports what LeaveSync did.
type SyncLeft struct {
	// Journal and Device are the identities this device had, when its sync
	// state could be read. Device is retired: no device uses it again.
	Journal string
	Device  string
	// Kept is where the history now lies, beside the journal's metadata. It
	// still holds every version of every conflict that was open.
	Kept string
	// Conflicts counts the conflicts and disputed days left open. Their
	// competing versions are in Kept; the journal keeps the one it showed.
	Conflicts int
}

// LeaveSync takes this journal out of sync. A journal that does not take part
// is refused with ErrSyncNotEnrolled. A sync state this seneca cannot read is
// moved aside too, so a device left with a newer state can start over.
func (j *Journal) LeaveSync() (SyncLeft, error) {
	if err := j.requireReflections(); err != nil {
		return SyncLeft{}, err
	}
	lock, err := j.lock()
	if err != nil {
		return SyncLeft{}, err
	}
	defer lock.release()
	if err := j.recoverLocked(); err != nil {
		return SyncLeft{}, err
	}

	statePath := SyncStatePathFor(j.reflectionsPath)
	data, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return SyncLeft{}, ErrSyncNotEnrolled
	}
	if err != nil {
		return SyncLeft{}, fmt.Errorf("reading the sync state %s: %w", statePath, err)
	}
	var left SyncLeft
	if state, err := decodeSyncState(statePath, data); err == nil {
		left.Journal, left.Device = state.Journal, state.Device
		left.Conflicts = len(state.Conflicts) + len(state.Disputes)
	}

	metadata := JournalMetadataDirFor(j.reflectionsPath)
	stamp := j.clock().UTC().Format("20060102T150405Z")
	for n := 1; ; n++ {
		left.Kept = filepath.Join(metadata, syncLeftPrefix+stamp)
		if n > 1 {
			left.Kept += fmt.Sprintf("-%d", n)
		}
		if _, err := os.Lstat(left.Kept); errors.Is(err, os.ErrNotExist) {
			break
		}
	}
	// One rename: the journal is in sync or out of it, never half of each.
	if err := j.fs.resolved().Rename(SyncDirFor(j.reflectionsPath), left.Kept); err != nil {
		return SyncLeft{}, fmt.Errorf("moving the sync history aside: %w", err)
	}
	// The claim on the directory goes with it, so another journal there may
	// take part instead. A claim left behind by an interruption names this
	// journal, which enrolling again accepts.
	dir := filepath.Dir(j.reflectionsPath)
	if claimed, ok := readSyncClaim(dir); ok && claimed == filepath.Base(j.reflectionsPath) {
		if err := os.Remove(SyncClaimPathFor(dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return left, fmt.Errorf("the journal left sync, but its claim on %s could not be removed: %w", dir, err)
		}
	}
	return left, nil
}
