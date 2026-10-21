package engine

import (
	"errors"
	"fmt"
	"slices"
)

// ErrConflictedReflection requires the author to settle a day's sync conflict
// before deleting it.
var ErrConflictedReflection = errors.New("choose a version before deleting this reflection; nothing was written")

// DeleteReflection removes only the saved day the author saw. The revision
// check and removal share the journal lock; the normal commit preserves a
// backup and records the deletion for sync. Drafts and recordings stay local.
func (j *Journal) DeleteReflection(date string, base Revision) error {
	if !IsCalendarDate(date) {
		return fmt.Errorf("invalid reflection date %q: expected YYYY-MM-DD", date)
	}
	if base == "" || base == RevisionAbsent {
		return errors.New("deleting a reflection requires an existing entry's base revision")
	}
	return j.Commit(func(c *Commit) error {
		status, enrolled, err := LoadSyncStatus(j.reflectionsPath)
		if err != nil {
			return err
		}
		if enrolled && slices.Contains(status.Conflicts, "entry:"+date) {
			return fmt.Errorf("%s: %w", date, ErrConflictedReflection)
		}
		tx, err := c.Reflections()
		if err != nil {
			return err
		}
		if err := tx.Validate(); err != nil {
			return err
		}
		entries := tx.Entries()
		if current := entryRevision(entries, date); current != base {
			return &StaleReflectionError{Path: j.reflectionsPath, Date: date, Base: base, Current: current}
		}
		index, _ := FindEntry(entries, date)
		kept := append([]ReflectionEntry(nil), entries[:index]...)
		kept = append(kept, entries[index+1:]...)
		return tx.CommitEntries(kept)
	})
}
