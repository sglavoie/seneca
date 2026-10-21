package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Inspecting is reading a journal's sync history without touching the journal:
// no lock is taken, no interrupted commit is completed, and nothing is created.
// It is what a report uses, where Journal.SyncStatus is what an operation that
// is about to write uses.

// LoadSyncStatus reads the sync history of the journal at reflectionsPath as
// its state file describes it. enrolled is false, with no error, for a journal
// that does not take part in sync.
//
// An interrupted commit is not completed first, so the state read is the one
// before that commit; PendingCommit says whether there is one.
func LoadSyncStatus(reflectionsPath string) (status SyncStatus, enrolled bool, err error) {
	path := SyncStatePathFor(reflectionsPath)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return SyncStatus{}, false, nil
	}
	if err != nil {
		return SyncStatus{}, false, fmt.Errorf("reading the sync state %s: %w", path, err)
	}
	state, err := decodeSyncState(path, data)
	if err != nil {
		return SyncStatus{}, false, err
	}
	return state.status(), true, nil
}

// PendingCommit reports whether a commit that changes several files in dir was
// interrupted and waits for the next operation on the journal to settle it.
func PendingCommit(dir string) bool {
	_, err := os.Lstat(filepath.Join(CommitDirFor(dir), commitRecordName))
	return err == nil
}

// SyncExchangeView is how a journal's history compares with an exchange.
type SyncExchangeView struct {
	// Found is false when the exchange holds no journal yet.
	Found bool
	// Unpublished counts this device's transactions the exchange lacks.
	Unpublished int
	// Unimported counts the other devices' transactions in the exchange that
	// this device does not hold. One that is still arriving counts too.
	Unimported int
	// Incompatible counts those of them written under a newer sync protocol:
	// they wait until this device's seneca is updated.
	Incompatible int
	// Impersonated counts transactions in the exchange under this device's
	// identity that this device never recorded: another installation, restored
	// from a copy of this one, is using its identity.
	Impersonated int
}

// InspectSyncExchange compares the history of the journal at reflectionsPath
// with what the exchange lists. It reads only the manifests this device does
// not hold, to tell which of them it cannot read yet, and changes neither
// side. A *SyncIdentityError means the exchange belongs to
// another journal.
func InspectSyncExchange(reflectionsPath string, transport SyncTransport) (SyncExchangeView, error) {
	path := SyncStatePathFor(reflectionsPath)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return SyncExchangeView{}, ErrSyncNotEnrolled
	}
	if err != nil {
		return SyncExchangeView{}, fmt.Errorf("reading the sync state %s: %w", path, err)
	}
	state, err := decodeSyncState(path, data)
	if err != nil {
		return SyncExchangeView{}, err
	}

	var view SyncExchangeView
	if view.Found, err = checkExchange(transport, state); err != nil {
		return SyncExchangeView{}, err
	}
	local, err := listSyncManifests(filepath.Join(SyncDirFor(reflectionsPath), syncTransactionsName))
	if err != nil {
		return SyncExchangeView{}, fmt.Errorf("listing the sync history: %w", err)
	}
	var remote []SyncTransactionRef
	if view.Found {
		if remote, err = transport.Transactions(); err != nil {
			return SyncExchangeView{}, fmt.Errorf("listing the exchange: %w", err)
		}
	}

	held := make(map[string]bool, len(local))
	for _, ref := range local {
		held[ref.ID] = true
	}
	there := make(map[string]bool, len(remote))
	for _, ref := range remote {
		there[ref.ID] = true
		switch {
		case held[ref.ID]:
		case ref.Device == state.Device:
			view.Impersonated++
		default:
			view.Unimported++
			// Reading a manifest changes nothing; one that has not arrived
			// whole is counted when it has.
			data, err := transport.Transaction(ref)
			if err != nil || SyncTransactionID(data) != ref.ID {
				continue
			}
			var protocol *SyncProtocolError
			if _, err := DecodeSyncTransaction(data); errors.As(err, &protocol) && protocol.Protocol > SyncProtocolVersion {
				view.Incompatible++
			}
		}
	}
	for _, ref := range local {
		if ref.Device == state.Device && !there[ref.ID] {
			view.Unpublished++
		}
	}
	return view, nil
}
