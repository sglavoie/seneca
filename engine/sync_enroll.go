package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Enrolling gives a journal an identity and a first transaction; joining gives
// a second device that identity and one of its own.
//
// # Enrolling
//
// The first device draws a journal identity and a device identity, gives the
// journal settings of its own when it has none, and records everything the
// journal holds as the genesis transaction: every entry, every archive record
// in the order the archive has them, and the settings. Nothing in the Markdown
// changes.
//
// # Joining
//
// A second device reads the journal's identity from the exchange and draws a
// device identity. It records nothing of its own journal yet and writes
// nothing to it: whatever its files already hold is recorded by its first
// import as a change of its own, beside the genesis rather than on top of it,
// so writing that exists on both devices is compared and never silently
// replaced.

// syncDescriptor is what an exchange says about itself: which journal it
// carries and where that journal's history begins.
type syncDescriptor struct {
	Protocol int    `json:"protocol"`
	Journal  string `json:"journal"`
	Genesis  string `json:"genesis"`
}

func decodeSyncDescriptor(data []byte) (syncDescriptor, error) {
	var descriptor syncDescriptor
	if err := decodeSyncJSON(data, &descriptor); err != nil {
		return syncDescriptor{}, fmt.Errorf("the exchange's journal descriptor cannot be used: %w", err)
	}
	if !syncIdentityPattern.MatchString(descriptor.Journal) || !recordIDPattern.MatchString(descriptor.Genesis) {
		return syncDescriptor{}, errors.New("the exchange's journal descriptor names no journal or no genesis transaction")
	}
	return descriptor, nil
}

// claimSync records, in c, that this journal is the one in its directory that
// takes part in sync, and refuses when another already is.
func (j *Journal) claimSync(c *Commit) error {
	dir := filepath.Dir(j.reflectionsPath)
	name := filepath.Base(j.reflectionsPath)
	// Like the lock, enrolling never creates the journal's own directory.
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("enrolling the journal: %w", err)
	}
	if claimed, ok := readSyncClaim(dir); ok {
		if claimed != name {
			return &SyncClaimError{Dir: dir, Journal: claimed}
		}
		return nil
	}
	tx, err := c.Metadata(SyncClaimPathFor(dir))
	if err != nil {
		return err
	}
	encoded, err := encodeSyncJSON(syncClaim{Version: syncStateVersion, Journal: name})
	if err != nil {
		return err
	}
	return tx.Commit(encoded)
}

// journalFiles are a journal's three files opened inside one commit.
type journalFiles struct {
	tx    map[syncSource]*FileTransaction
	views map[syncSource]sourceView
}

// openJournalFiles re-reads the reflections file, the archive, and the settings
// under the lock, as artifacts. A file that cannot be opened is a view with a
// problem, not a failure: sync leaves such a file alone.
func (w *syncWork) openJournalFiles() journalFiles {
	files := journalFiles{tx: map[syncSource]*FileTransaction{}, views: map[syncSource]sourceView{}}
	for _, source := range syncSources {
		path := w.path(source)
		var tx *FileTransaction
		var err error
		if source == syncSourceSettings {
			tx, err = w.commit.Metadata(path)
		} else if tx, err = w.journal.fs.BeginFileTransaction(path); err == nil {
			w.commit.adopt(tx)
		}
		if err != nil {
			files.views[source] = sourceView{problem: err.Error()}
			continue
		}
		files.tx[source] = tx
		files.views[source] = viewSource(source, path, tx.original, tx.exists)
	}
	return files
}

// EnrollSync makes this device the first of a journal that takes part in sync:
// it draws the journal's identity and records everything the journal holds as
// the genesis transaction. name is a label for this device, for a person to
// read; it decides nothing.
//
// The journal gets settings of its own if it has none, seeded from the ones
// this Journal was opened with, so every device that joins asks the same
// questions. No Markdown is written. A journal already enrolled is reported as
// it is.
func (j *Journal) EnrollSync(name string) (SyncStatus, error) {
	if err := j.requireReflections(); err != nil {
		return SyncStatus{}, err
	}
	journalID, err := j.newSyncIdentity()
	if err != nil {
		return SyncStatus{}, err
	}
	deviceID, err := j.newSyncIdentity()
	if err != nil {
		return SyncStatus{}, err
	}

	var status SyncStatus
	err = j.Commit(func(c *Commit) error {
		c.syncSealed = true
		w, enrolled, err := beginSyncWork(c, j.reflectionsPath)
		if err != nil {
			return err
		}
		if enrolled {
			status = w.state.status()
			return nil
		}
		if err := j.claimSync(c); err != nil {
			return err
		}

		files := w.openJournalFiles()
		if settings := files.tx[syncSourceSettings]; settings != nil && !settings.Exists() {
			if err := ValidateReflectionSettings(j.settings); err != nil {
				return fmt.Errorf("the configuration to seed the journal settings from is not valid: %w", err)
			}
			unit := unitOf(j.settings)
			encoded, err := settingsDocument{Reflection: unit, Legacy: unit}.encode()
			if err != nil {
				return err
			}
			if err := settings.Commit(encoded); err != nil {
				return err
			}
			files.views[syncSourceSettings] = viewSource(syncSourceSettings, w.settingsPath, encoded, true)
		}
		for _, source := range syncSources {
			if problem := files.views[source].problem; problem != "" {
				return fmt.Errorf("this journal cannot be enrolled until its %s file is repaired: %s", source, problem)
			}
		}

		w.state = syncState{Version: syncStateVersion, Journal: journalID, Device: deviceID, Name: name}
		changes, contents := w.unrecorded(files.views)
		genesis, err := w.record(SyncOriginGenesis, changes, contents)
		if err != nil {
			return err
		}
		w.state.Genesis = genesis
		status = w.state.status()
		return w.stage(nil)
	})
	if err != nil {
		return committedOutcome(status, err)
	}
	return status, nil
}

// JoinSync makes this device another device of the journal the exchange
// carries. It reads the journal's identity from the exchange, draws a device
// identity, and writes nothing else: the journal's history, and this device's
// own writing, are exchanged by the import and publish that follow.
//
// A device that already belongs to that journal is reported as it is; one that
// belongs to another journal is refused with a *SyncIdentityError.
func (j *Journal) JoinSync(transport SyncTransport, name string) (SyncStatus, error) {
	if err := j.requireReflections(); err != nil {
		return SyncStatus{}, err
	}
	data, err := transport.Descriptor()
	if errors.Is(err, fs.ErrNotExist) {
		return SyncStatus{}, errors.New("the exchange holds no journal yet: publish from the device that enrolled it, and wait for it to arrive")
	}
	if err != nil {
		return SyncStatus{}, fmt.Errorf("reading the exchange's journal descriptor: %w", err)
	}
	descriptor, err := decodeSyncDescriptor(data)
	if err != nil {
		return SyncStatus{}, err
	}
	deviceID, err := j.newSyncIdentity()
	if err != nil {
		return SyncStatus{}, err
	}

	var status SyncStatus
	err = j.Commit(func(c *Commit) error {
		c.syncSealed = true
		w, enrolled, err := beginSyncWork(c, j.reflectionsPath)
		if err != nil {
			return err
		}
		if enrolled {
			if w.state.Journal != descriptor.Journal {
				return &SyncIdentityError{Local: w.state.Journal, Remote: descriptor.Journal}
			}
			status = w.state.status()
			return nil
		}
		if err := j.claimSync(c); err != nil {
			return err
		}
		w.state = syncState{
			Version: syncStateVersion,
			Journal: descriptor.Journal,
			Device:  deviceID,
			Name:    name,
			Genesis: descriptor.Genesis,
		}
		status = w.state.status()
		return w.stage(nil)
	})
	if err != nil {
		return committedOutcome(status, err)
	}
	return status, nil
}
