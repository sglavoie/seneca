package engine

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Exchanging is publishing this device's transactions to a place another
// device can read, and importing the ones found there.
//
// # Publishing
//
// A device publishes only its own transactions, each with the contents it
// names, contents first. What is published is never edited or removed. The set
// to publish is whatever this device recorded that the exchange does not hold,
// so nothing has to remember what was sent: a publish that was interrupted, or
// an exchange that lost files, is repaired by publishing again.
//
// # Importing
//
// A transaction is imported only whole: its manifest must be what its name
// says, and every revision it writes must have arrived intact and be something
// a local command would write. Anything less is reported and looked at again
// next time; none of it is stored or applied. A transaction already held is
// not read again, so delivering it twice does nothing.
//
// An imported transaction is applied when the ones it was made on top of are
// applied. A change in it follows from what this device has when the artifact
// is at a revision the change covers and was last written by something the
// change's author had seen. Order of arrival does not matter; a transaction
// that arrives before its parents waits for them.
//
// A change to an artifact this device changed independently is merged with
// this device's version when the two do not overlap; see sync_merge.go. When
// they do, the file keeps this device's version and the other is held beside
// it as a conflict, with its contents, until the author chooses; see
// sync_conflict.go. Neither version is lost, neither is chosen, and nothing
// else the transaction changed waits for it.
//
// Before anything arriving is applied, the files are compared with what was
// last recorded of them, and what an editor or Git changed is recorded as a
// transaction of this device's own. An import therefore never overwrites
// writing it has not seen.

// SyncTransport is a place transactions and their contents are exchanged
// through. Everything in it is immutable and named by what it holds.
//
// A read of something that has not arrived returns an error that
// errors.Is(err, fs.ErrNotExist). That is never a deletion.
type SyncTransport interface {
	// Descriptor reads which journal the exchange carries.
	Descriptor() ([]byte, error)
	PutDescriptor(data []byte) error
	// Transactions lists the manifests present, by name alone.
	Transactions() ([]SyncTransactionRef, error)
	Transaction(ref SyncTransactionRef) ([]byte, error)
	PutTransaction(ref SyncTransactionRef, data []byte) error
	Object(digest Revision) ([]byte, error)
	PutObject(digest Revision, data []byte) error
}

const syncDescriptorFileName = "journal.json"

// DirectoryTransport exchanges through a directory both devices can reach: a
// folder on one disk, or one a file-syncing service carries between them.
//
//	<root>/journal.json                              which journal this is
//	<root>/transactions/<device>/<sequence>-<id>.json
//	<root>/objects/<sha-256>
//
// Each file is written whole, under a temporary name, and renamed into place.
type DirectoryTransport struct {
	root string
	fs   FS
}

// NewDirectoryTransport exchanges through root, which is created by the first
// publish.
func NewDirectoryTransport(root string) *DirectoryTransport {
	return &DirectoryTransport{root: root}
}

// Root is the directory exchanged through.
func (t *DirectoryTransport) Root() string { return t.root }

func (t *DirectoryTransport) put(path string, data []byte) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	return uncommitted(t.fs.AtomicWriteFile(path, data, DefaultFileMode))
}

func (t *DirectoryTransport) manifestPath(ref SyncTransactionRef) string {
	return filepath.Join(t.root, syncTransactionsName, ref.Device, syncManifestName(ref.Sequence, ref.ID))
}

func (t *DirectoryTransport) objectPath(digest Revision) string {
	return filepath.Join(t.root, syncObjectsName, syncObjectName(digest))
}

// ManifestPath, ObjectPath, and ReceiptPath say where in the directory a file
// is or would be, for an adapter that has to know whether the file service
// underneath has delivered it.
func (t *DirectoryTransport) ManifestPath(ref SyncTransactionRef) string { return t.manifestPath(ref) }

func (t *DirectoryTransport) ObjectPath(digest Revision) string { return t.objectPath(digest) }

func (t *DirectoryTransport) ReceiptPath(ref SyncReceiptRef) string { return t.receiptPath(ref) }

func (t *DirectoryTransport) Descriptor() ([]byte, error) {
	return os.ReadFile(filepath.Join(t.root, syncDescriptorFileName))
}

func (t *DirectoryTransport) PutDescriptor(data []byte) error {
	return t.put(filepath.Join(t.root, syncDescriptorFileName), data)
}

func (t *DirectoryTransport) Transactions() ([]SyncTransactionRef, error) {
	return listSyncManifests(filepath.Join(t.root, syncTransactionsName))
}

func (t *DirectoryTransport) Transaction(ref SyncTransactionRef) ([]byte, error) {
	return os.ReadFile(t.manifestPath(ref))
}

func (t *DirectoryTransport) PutTransaction(ref SyncTransactionRef, data []byte) error {
	return t.put(t.manifestPath(ref), data)
}

func (t *DirectoryTransport) Object(digest Revision) ([]byte, error) {
	if !syncDigestPattern.MatchString(string(digest)) {
		return nil, fmt.Errorf("%q is not a content digest", digest)
	}
	return os.ReadFile(t.objectPath(digest))
}

func (t *DirectoryTransport) PutObject(digest Revision, data []byte) error {
	if !syncDigestPattern.MatchString(string(digest)) {
		return fmt.Errorf("%q is not a content digest", digest)
	}
	return t.put(t.objectPath(digest), data)
}

// checkExchange holds the exchange to this device's journal. found is false
// when the exchange has no descriptor yet.
func checkExchange(transport SyncTransport, state syncState) (found bool, err error) {
	data, err := transport.Descriptor()
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading the exchange's journal descriptor: %w", err)
	}
	descriptor, err := decodeSyncDescriptor(data)
	if err != nil {
		return false, err
	}
	if descriptor.Journal != state.Journal {
		return false, &SyncIdentityError{Local: state.Journal, Remote: descriptor.Journal}
	}
	if descriptor.Genesis != state.Genesis {
		return false, &SyncIdentityError{Local: state.Journal, Remote: descriptor.Journal,
			Detail: fmt.Sprintf("both are journal %s, but its history begins at %s here and at %s there",
				state.Journal, ShortRecordID(state.Genesis), ShortRecordID(descriptor.Genesis))}
	}
	return true, nil
}

// SyncPublished reports what PublishSync sent.
type SyncPublished struct {
	// Transactions are the manifests written to the exchange, oldest first.
	Transactions []string
}

// PublishSync copies to the exchange every transaction this device recorded
// that the exchange does not hold, each after the contents it names. It reads
// the journal's history and changes nothing in the journal; publishing again
// sends only what is still missing.
func (j *Journal) PublishSync(transport SyncTransport) (SyncPublished, error) {
	state, enrolled, err := j.readSyncState()
	if err != nil {
		return SyncPublished{}, err
	}
	if !enrolled {
		return SyncPublished{}, ErrSyncNotEnrolled
	}
	found, err := checkExchange(transport, state)
	if err != nil {
		return SyncPublished{}, err
	}
	if !found {
		encoded, err := encodeSyncJSON(syncDescriptor{Protocol: SyncProtocolVersion, Journal: state.Journal, Genesis: state.Genesis})
		if err != nil {
			return SyncPublished{}, err
		}
		if err := transport.PutDescriptor(encoded); err != nil {
			return SyncPublished{}, fmt.Errorf("publishing the journal descriptor: %w", err)
		}
	}

	dir := SyncDirFor(j.reflectionsPath)
	local, err := listSyncManifests(filepath.Join(dir, syncTransactionsName))
	if err != nil {
		return SyncPublished{}, fmt.Errorf("listing the sync history: %w", err)
	}
	remote, err := transport.Transactions()
	if err != nil {
		return SyncPublished{}, fmt.Errorf("listing the exchange: %w", err)
	}
	there := make(map[string]bool, len(remote))
	for _, ref := range remote {
		there[ref.ID] = true
	}

	var published SyncPublished
	for _, ref := range local {
		if ref.Device != state.Device || there[ref.ID] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, syncTransactionsName, ref.Device, syncManifestName(ref.Sequence, ref.ID)))
		if err != nil {
			return published, fmt.Errorf("reading transaction %s: %w", ShortRecordID(ref.ID), err)
		}
		tx, err := DecodeSyncTransaction(data)
		if err != nil || SyncTransactionID(data) != ref.ID {
			return published, fmt.Errorf("transaction %s in the sync history is damaged", ShortRecordID(ref.ID))
		}
		for _, change := range tx.Changes {
			if change.Deleted {
				continue
			}
			contents, err := readSyncObject(filepath.Join(dir, syncObjectsName), change.New)
			if err != nil {
				return published, fmt.Errorf("reading the contents of %s for transaction %s: %w", change.Artifact, ShortRecordID(ref.ID), err)
			}
			if err := transport.PutObject(change.New, contents); err != nil {
				return published, fmt.Errorf("publishing the contents of %s: %w", change.Artifact, err)
			}
		}
		// The manifest goes last: a reader that finds it can expect its contents.
		if err := transport.PutTransaction(ref, data); err != nil {
			return published, fmt.Errorf("publishing transaction %s: %w", ShortRecordID(ref.ID), err)
		}
		published.Transactions = append(published.Transactions, ref.ID)
	}
	return published, nil
}

// SyncImported reports what ImportSync did.
type SyncImported struct {
	// Applied are the transactions applied to the journal, in the order they
	// were.
	Applied []string
	// Recorded are the transactions this device recorded for changes it found
	// in the files.
	Recorded []string
	// Written are the journal files the import replaced.
	Written []string
	// Merged are the artifacts this import reconciled by itself: two devices
	// changed them independently, and the changes did not overlap.
	Merged []string
	// Conflicts are the artifacts that wait for the author to choose. See
	// Journal.SyncConflicts.
	Conflicts []string
	// Pending is every stored transaction still not applied.
	Pending []SyncPending
	// Diagnostics is everything noticed and not acted on.
	Diagnostics []SyncDiagnostic
}

// arrival is a transaction read whole from an exchange.
type arrival struct {
	ref      SyncTransactionRef
	data     []byte
	tx       SyncTransaction
	contents map[Revision][]byte
}

// fetch reads from the exchange every transaction this device does not hold,
// and keeps the ones that arrived whole and valid.
func (j *Journal) fetch(transport SyncTransport, state syncState) ([]arrival, []SyncDiagnostic, error) {
	dir := SyncDirFor(j.reflectionsPath)
	local, err := listSyncManifests(filepath.Join(dir, syncTransactionsName))
	if err != nil {
		return nil, nil, fmt.Errorf("listing the sync history: %w", err)
	}
	held := make(map[string]bool, len(local))
	for _, ref := range local {
		held[ref.ID] = true
	}
	remote, err := transport.Transactions()
	if err != nil {
		return nil, nil, fmt.Errorf("listing the exchange: %w", err)
	}

	var arrivals []arrival
	var diagnostics []SyncDiagnostic
	refuse := func(code SyncDiagnosticCode, ref SyncTransactionRef, subject, detail string) {
		diagnostics = append(diagnostics, SyncDiagnostic{Code: code, Transaction: ref.ID, Subject: subject, Detail: detail})
	}
next:
	for _, ref := range remote {
		// What this device wrote it already holds, and what it holds it does
		// not read twice.
		if held[ref.ID] || ref.Device == state.Device {
			continue
		}
		data, err := transport.Transaction(ref)
		if err != nil {
			refuse(SyncDiagIncomplete, ref, "", fmt.Sprintf("the manifest has not arrived: %v", err))
			continue
		}
		if SyncTransactionID(data) != ref.ID {
			refuse(SyncDiagIncomplete, ref, "", "the manifest does not match the identity in its name; it is damaged or still arriving")
			continue
		}
		tx, err := DecodeSyncTransaction(data)
		var protocol *SyncProtocolError
		switch {
		case errors.As(err, &protocol) && protocol.Protocol > SyncProtocolVersion:
			refuse(SyncDiagNewerProtocol, ref, "", err.Error())
			continue
		case err != nil:
			refuse(SyncDiagMalformed, ref, "", err.Error())
			continue
		case tx.Device != ref.Device || tx.Sequence != ref.Sequence:
			refuse(SyncDiagMalformed, ref, "", "the manifest names a different device or sequence than its file does")
			continue
		case tx.Journal != state.Journal:
			refuse(SyncDiagForeignJournal, ref, "", fmt.Sprintf("it belongs to journal %s", tx.Journal))
			continue
		}

		got := arrival{ref: ref, data: data, tx: tx, contents: map[Revision][]byte{}}
		for _, change := range tx.Changes {
			if change.Deleted {
				continue
			}
			contents, err := readSyncObject(filepath.Join(dir, syncObjectsName), change.New)
			if err != nil {
				contents, err = transport.Object(change.New)
			}
			switch {
			case err != nil:
				refuse(SyncDiagIncomplete, ref, change.Artifact, fmt.Sprintf("its contents have not arrived: %v", err))
				continue next
			case RevisionOf(contents) != change.New:
				refuse(SyncDiagIncomplete, ref, change.Artifact, "its contents do not match their digest; they are damaged or still arriving")
				continue next
			}
			if err := validateArtifactContent(change.Artifact, contents); err != nil {
				refuse(SyncDiagInvalidContent, ref, change.Artifact, err.Error())
				continue next
			}
			got.contents[change.New] = contents
		}
		arrivals = append(arrivals, got)
	}
	return arrivals, diagnostics, nil
}

// ImportSync brings in what the exchange holds. It first records what changed
// in the journal's files since they were last recorded, then stores every
// transaction that arrived whole, then applies each stored transaction that
// can be applied, all as one commit. See the top of this file for the rules.
//
// A *SyncIdentityError means the exchange belongs to another journal and
// nothing was read from it.
func (j *Journal) ImportSync(transport SyncTransport) (SyncImported, error) {
	state, enrolled, err := j.readSyncState()
	if err != nil {
		return SyncImported{}, err
	}
	if !enrolled {
		return SyncImported{}, ErrSyncNotEnrolled
	}
	// The exchange is read before the lock is taken: it may be slow, and
	// everything in it is immutable.
	var arrivals []arrival
	var diagnostics []SyncDiagnostic
	if transport != nil {
		found, err := checkExchange(transport, state)
		if err != nil {
			return SyncImported{}, err
		}
		if found {
			if arrivals, diagnostics, err = j.fetch(transport, state); err != nil {
				return SyncImported{}, err
			}
		}
	}
	return j.integrate(arrivals, diagnostics)
}

// RecordExternalEdits records what changed in the journal's files since they
// were last recorded, and applies any stored transaction that was waiting on
// them. It reads no exchange.
func (j *Journal) RecordExternalEdits() (SyncImported, error) {
	return j.ImportSync(nil)
}

func (j *Journal) integrate(arrivals []arrival, diagnostics []SyncDiagnostic) (SyncImported, error) {
	var imported SyncImported
	err := j.Commit(func(c *Commit) error {
		c.syncSealed = true
		imported = SyncImported{}
		w, enrolled, err := beginSyncWork(c, j.reflectionsPath)
		if err != nil {
			return err
		}
		if !enrolled {
			return ErrSyncNotEnrolled
		}
		w.diagnostics = append(w.diagnostics, diagnostics...)

		files := w.openJournalFiles()
		if err := w.observe(files.views); err != nil {
			return err
		}
		if err := w.store(arrivals); err != nil {
			return err
		}
		working := newWorkingJournal(files.views)
		applied, err := w.applyPending(&working)
		if err != nil {
			return err
		}

		written, err := w.finish(files, working)
		if err != nil {
			return err
		}
		imported.Written = written

		status := w.state.status()
		for _, pending := range status.Pending {
			w.diagnose(SyncDiagPending, pending.Transaction, pending.Artifact, string(pending.Reason)+": "+pending.Detail)
		}
		imported.Applied = applied
		imported.Recorded = w.recorded
		imported.Merged = w.merged
		imported.Conflicts = status.Conflicts
		imported.Pending = status.Pending
		imported.Diagnostics = w.diagnostics
		return nil
	})
	if err != nil {
		return committedOutcome(imported, err)
	}
	return imported, nil
}

// store keeps each arrival in the history as pending: its contents at once,
// its manifest with the commit. One that claims a sequence number another
// transaction of the same device already has is refused.
func (w *syncWork) store(arrivals []arrival) error {
	taken := make(map[string]string, len(w.index))
	slot := func(ref SyncTransactionRef) string { return fmt.Sprintf("%s/%d", ref.Device, ref.Sequence) }
	for id, ref := range w.index {
		taken[slot(ref)] = id
	}
	for _, got := range arrivals {
		if _, stored := w.index[got.ref.ID]; stored {
			continue
		}
		if other, used := taken[slot(got.ref)]; used {
			w.diagnose(SyncDiagSequenceReuse, got.ref.ID, "",
				fmt.Sprintf("device %s already has transaction %s at sequence %d", got.ref.Device, ShortRecordID(other), got.ref.Sequence))
			continue
		}
		for digest, data := range got.contents {
			if err := w.storeObject(digest, data); err != nil {
				return err
			}
		}
		taken[slot(got.ref)] = got.ref.ID
		w.index[got.ref.ID] = got.ref
		w.loaded[got.ref.ID] = got.tx
		w.manifests = append(w.manifests, syncManifest{path: w.manifestPath(got.ref), data: got.data})
		w.state.Pending[got.ref.ID] = syncPending{Reason: SyncPendingWaiting}
	}
	return nil
}

// applyPending applies every pending transaction that can be applied, and
// keeps going until a pass applies none: applying one may be what another was
// waiting for. It returns the ones applied, in order.
func (w *syncWork) applyPending(working *workingJournal) ([]string, error) {
	ids := make([]string, 0, len(w.state.Pending))
	for id := range w.state.Pending {
		ids = append(ids, id)
	}
	// Oldest first within a device, so a device's own chain applies in one pass.
	sort.Slice(ids, func(i, j int) bool {
		a, b := w.index[ids[i]], w.index[ids[j]]
		if a.Sequence != b.Sequence {
			return a.Sequence < b.Sequence
		}
		return a.ID < b.ID
	})

	var applied []string
	for progress := true; progress; {
		progress = false
		for _, id := range ids {
			if _, pending := w.state.Pending[id]; !pending {
				continue
			}
			held, err := w.apply(id, working)
			if err != nil {
				return nil, err
			}
			if held != nil {
				w.state.Pending[id] = *held
				continue
			}
			delete(w.state.Pending, id)
			applied = append(applied, id)
			progress = true
		}
	}
	return applied, nil
}

// applyPlan is what applying one transaction changes in the state, gathered
// before any of it is kept: a transaction is applied whole or not at all.
type applyPlan struct {
	artifacts map[string]syncArtifactState
	// conflicts holds each conflict to keep, and nil for each one that ended.
	conflicts map[string]*syncConflictState
	// merges are the changes this device makes itself to reconcile.
	merges   []SyncChange
	contents map[Revision][]byte
	merged   []string
}

// apply applies one stored transaction to the working journal and the state,
// whole or not at all. It returns why when it cannot.
//
// Each change is taken on its own. One that writes what the artifact already
// holds needs nothing. One that covers the revision this device holds, made by
// an author who had seen it, follows from it and replaces it. Any other was
// made independently: it is merged with this device's version when the two do
// not overlap, and held beside it as a conflict when they do. A conflict stops
// nothing else in the transaction.
func (w *syncWork) apply(id string, working *workingJournal) (*syncPending, error) {
	tx, err := w.load(id)
	if err != nil {
		return nil, err
	}
	for _, parent := range tx.Parents {
		if !w.applied(parent) {
			return &syncPending{Reason: SyncPendingWaiting,
				Detail: fmt.Sprintf("it was made on top of transaction %s, which is not applied here yet", ShortRecordID(parent))}, nil
		}
	}
	seen, err := w.ancestors(id)
	if err != nil {
		return nil, err
	}

	plan := applyPlan{
		artifacts: map[string]syncArtifactState{},
		conflicts: map[string]*syncConflictState{},
		contents:  map[Revision][]byte{},
	}
	next := working.clone()
	write := func(artifact string, revision Revision, contents []byte) error {
		if revision == RevisionAbsent {
			next.set(artifact, nil, true)
			return nil
		}
		if contents == nil {
			var err error
			if contents, err = w.object(revision); err != nil {
				return fmt.Errorf("reading the contents of %s for transaction %s: %w", artifact, ShortRecordID(id), err)
			}
		}
		next.set(artifact, contents, false)
		return nil
	}

	for index, change := range tx.Changes {
		artifact := change.Artifact
		source, _ := artifactSource(artifact)
		if problem, unusable := w.unusable[source]; unusable {
			return &syncPending{Reason: SyncPendingLocalFile, Artifact: artifact, Detail: problem}, nil
		}
		known := w.state.Artifacts[artifact]
		current := w.state.recorded(artifact)
		conflict, conflicted := w.state.Conflicts[artifact]
		result := change.result()
		held := uncovered(conflict.Theirs, change)
		keep := func(base Revision, theirs []syncCompeting, reason string) {
			if len(theirs) == 0 {
				if conflicted {
					plan.conflicts[artifact] = nil
				}
				return
			}
			plan.conflicts[artifact] = &syncConflictState{Base: base, Theirs: theirs, Reason: reason}
		}

		if source == syncSourceArchive {
			w.archiveChanged = true
			// A record is placed by the earliest transaction that wrote it, so
			// two devices that both held it agree on where it goes.
			if change.Base == RevisionAbsent && !change.Deleted {
				depth, err := w.depth(id)
				if err != nil {
					return nil, err
				}
				order := &syncOrder{Depth: depth, Transaction: id, Index: index}
				if known.Order == nil || order.before(*known.Order, w.state.Genesis) {
					known.Order = order
					plan.artifacts[artifact] = known
					next.reorder = true
				}
			}
		}

		switch {
		case result == current:
			// Both devices arrived at the same bytes; there is nothing to write.
			keep(conflict.Base, held, conflict.Reason)
		case change.covers(current) && (known.Transaction == "" || seen[known.Transaction]):
			if err := write(artifact, result, nil); err != nil {
				return nil, err
			}
			plan.artifacts[artifact] = syncArtifactState{Digest: result, Transaction: id, Order: known.Order}
			keep(conflict.Base, held, conflict.Reason)
		default:
			outcome := mergeOutcome{reason: conflictSeveralVersions}
			base := change.Base
			if conflicted {
				base = conflict.Base
			}
			if len(held) == 0 {
				mine, err := w.covered(known.Transaction, artifact)
				if err != nil {
					return nil, err
				}
				outcome, base = w.reconcile(artifact, change, current, mine, base)
			}
			if outcome.clean {
				switch outcome.revision {
				case result:
					if err := write(artifact, result, nil); err != nil {
						return nil, err
					}
					plan.artifacts[artifact] = syncArtifactState{Digest: result, Transaction: id, Order: known.Order}
				default:
					if outcome.revision != current {
						if err := write(artifact, outcome.revision, outcome.content); err != nil {
							return nil, err
						}
						plan.contents[outcome.revision] = outcome.content
					}
					// The other device has to hear that its version is
					// covered, even when this device's own is the result.
					if merge, ok := coveringChange(artifact, []Revision{current, result}, outcome.revision); ok {
						plan.merges = append(plan.merges, merge)
					}
				}
				plan.merged = append(plan.merged, artifact)
				keep(RevisionAbsent, nil, "")
				continue
			}
			reason := outcome.reason

			theirs := append(held, syncCompeting{Digest: result, Transaction: id})
			// A version this device wrote stays in its file. Between versions
			// of other devices the file shows the one whose transaction sorts
			// first, so the order they arrived in decides nothing.
			if !w.own(known.Transaction) {
				first := 0
				for i := range theirs {
					if theirs[i].Transaction < theirs[first].Transaction {
						first = i
					}
				}
				if theirs[first].Transaction < known.Transaction {
					shown := theirs[first]
					theirs[first] = syncCompeting{Digest: current, Transaction: known.Transaction}
					if err := write(artifact, shown.Digest, nil); err != nil {
						return nil, err
					}
					plan.artifacts[artifact] = syncArtifactState{Digest: shown.Digest, Transaction: shown.Transaction, Order: known.Order}
				}
			}
			sort.Slice(theirs, func(i, j int) bool {
				if theirs[i].Transaction != theirs[j].Transaction {
					return theirs[i].Transaction < theirs[j].Transaction
				}
				return theirs[i].Digest < theirs[j].Digest
			})
			keep(base, theirs, reason)
		}
	}

	if next.reorder {
		next.order(func(artifact string) *syncOrder {
			if planned, ok := plan.artifacts[artifact]; ok {
				return planned.Order
			}
			return w.state.Artifacts[artifact].Order
		}, w.state.Genesis)
	}
	for _, source := range syncSources {
		if next.touched[source] {
			if err := next.validate(source, w.path(source)); err != nil {
				return &syncPending{Reason: SyncPendingRefused, Detail: err.Error()}, nil
			}
		}
	}
	next.touched = map[syncSource]bool{}
	next.reorder = false
	*working = next

	for artifact, state := range plan.artifacts {
		w.state.Artifacts[artifact] = state
	}
	for artifact, conflict := range plan.conflicts {
		if conflict == nil {
			delete(w.state.Conflicts, artifact)
			continue
		}
		w.state.Conflicts[artifact] = *conflict
	}
	heads := []string{id}
	for _, head := range w.state.Heads {
		if !seen[head] {
			heads = append(heads, head)
		}
	}
	sort.Strings(heads)
	w.state.Heads = heads

	if len(plan.merges) > 0 {
		if _, err := w.record(SyncOriginMerge, plan.merges, plan.contents); err != nil {
			return nil, err
		}
	}
	w.merged = append(w.merged, plan.merged...)
	return nil, nil
}

// own reports whether a transaction is one this device wrote. An artifact
// nothing has written yet is this device's to keep as it is.
func (w *syncWork) own(id string) bool {
	if id == "" {
		return true
	}
	return w.index[id].Device == w.state.Device
}

// covered is the revisions of artifact that transaction id replaced when it
// wrote the one this device holds: what this device's version was made on.
func (w *syncWork) covered(id, artifact string) (map[Revision]bool, error) {
	found := map[Revision]bool{}
	if id == "" {
		return found, nil
	}
	tx, err := w.load(id)
	if err != nil {
		return nil, err
	}
	for _, change := range tx.Changes {
		if change.Artifact != artifact {
			continue
		}
		found[change.Base] = true
		for _, resolved := range change.Resolves {
			found[resolved] = true
		}
	}
	return found, nil
}

// reconcile decides what an artifact holds when change arrives beside a
// version of this device's the change's author had not seen, or did not build
// on. current is that version and mine the revisions it was made on; fallback
// is the base to merge on when the two changes name none in common. It returns
// the outcome and the base it was reached on.
//
// What each change replaced says how the two relate, where the bytes alone
// cannot:
//
//   - Each replaced the other's result: both devices settled the same
//     competing versions, differently. That is a conflict, not a merge.
//   - Only this device replaced the incoming result: the incoming version is
//     one this device has already moved on from, and its own stands.
//   - Only the incoming change replaced this device's revision, by bytes it
//     happened to return to: the incoming version follows from it.
//   - Both replaced exactly one revision in common: that is what both were
//     made on, and the two are merged on it.
//   - Both replaced several in common: two settlements again, a conflict.
func (w *syncWork) reconcile(artifact string, change SyncChange, current Revision, mine map[Revision]bool, fallback Revision) (mergeOutcome, Revision) {
	result := change.result()
	switch {
	case mine[result] && change.covers(current):
		return mergeOutcome{reason: conflictSettledTwice}, fallback
	case mine[result]:
		return mergeOutcome{clean: true, revision: current}, result
	case change.covers(current):
		return mergeOutcome{clean: true, revision: result}, current
	}
	var shared []Revision
	for revision := range mine {
		if change.covers(revision) {
			shared = append(shared, revision)
		}
	}
	switch len(shared) {
	case 0:
		return w.merge(artifact, fallback, current, result), fallback
	case 1:
		return w.merge(artifact, shared[0], current, result), shared[0]
	default:
		return mergeOutcome{reason: conflictSettledTwice}, fallback
	}
}

// workingJournal is the journal's artifacts while an import applies
// transactions to them, before anything is written.
type workingJournal struct {
	entries  []syncUnit
	archive  ReviewArchive
	records  []syncUnit
	settings []byte
	// dirty marks each source that no longer matches its file.
	dirty map[syncSource]bool
	// touched marks each source the transaction being applied changed.
	touched map[syncSource]bool
	// reorder is set when a record was added or placed anew, so the archive
	// must be put back in order before it is written.
	reorder bool
}

func newWorkingJournal(views map[syncSource]sourceView) workingJournal {
	working := workingJournal{
		entries: append([]syncUnit{}, views[syncSourceReflections].units...),
		archive: views[syncSourceArchive].archive,
		records: append([]syncUnit{}, views[syncSourceArchive].units...),
		dirty:   map[syncSource]bool{},
		touched: map[syncSource]bool{},
	}
	if units := views[syncSourceSettings].units; len(units) == 1 {
		working.settings = units[0].data
	}
	return working
}

func (w workingJournal) clone() workingJournal {
	next := w
	next.entries = append([]syncUnit{}, w.entries...)
	next.records = append([]syncUnit{}, w.records...)
	next.dirty = map[syncSource]bool{}
	for source, dirty := range w.dirty {
		next.dirty[source] = dirty
	}
	next.touched = map[syncSource]bool{}
	return next
}

// set writes, replaces, or removes one artifact. A new entry goes where its
// date belongs among the others; a new record goes at the end of the archive
// until order places it.
func (w *workingJournal) set(artifact string, data []byte, deleted bool) {
	source, _ := artifactSource(artifact)
	w.dirty[source] = true
	w.touched[source] = true
	if source == syncSourceSettings {
		w.settings = data
		return
	}

	units := &w.records
	if source == syncSourceReflections {
		units = &w.entries
	}
	at := -1
	for i, unit := range *units {
		if unit.id == artifact {
			at = i
		}
	}
	switch {
	case deleted && at >= 0:
		*units = append((*units)[:at], (*units)[at+1:]...)
	case deleted:
	case at >= 0:
		(*units)[at] = syncUnit{id: artifact, data: data}
	default:
		at = len(*units)
		if source == syncSourceArchive {
			w.reorder = true
		}
		if source == syncSourceReflections {
			for i, unit := range *units {
				if unit.id > artifact {
					at = i
					break
				}
			}
		}
		*units = append(*units, syncUnit{})
		copy((*units)[at+1:], (*units)[at:])
		(*units)[at] = syncUnit{id: artifact, data: data}
	}
}

// order puts the archive's records where history places them. A record history
// has not placed keeps its position among the others like it.
func (w *workingJournal) order(of func(artifact string) *syncOrder, genesis string) {
	before := make([]string, len(w.records))
	for i, unit := range w.records {
		before[i] = unit.id
	}
	sort.SliceStable(w.records, func(i, j int) bool {
		a, b := of(w.records[i].id), of(w.records[j].id)
		switch {
		case a == nil || b == nil:
			return a == nil && b != nil
		default:
			return a.before(*b, genesis)
		}
	})
	for i, unit := range w.records {
		if before[i] != unit.id {
			w.dirty[syncSourceArchive] = true
			w.touched[syncSourceArchive] = true
			return
		}
	}
}

// render is the bytes a source's file holds once the working journal is
// written.
func (w workingJournal) render(source syncSource) []byte {
	switch source {
	case syncSourceReflections:
		entries := make([]ReflectionEntry, 0, len(w.entries))
		for _, unit := range w.entries {
			entries = append(entries, NewReflectionEntry(string(unit.data)))
		}
		return []byte(RenderReflectionEntries(entries))
	case syncSourceArchive:
		header := w.archive.UpgradedHeader()
		if strings.TrimSpace(header) == "" {
			header = RenderArchiveHeader()
		}
		var b strings.Builder
		b.WriteString(strings.TrimRight(header, "\n"))
		b.WriteByte('\n')
		for _, unit := range w.records {
			b.WriteByte('\n')
			b.Write(unit.data)
		}
		return []byte(b.String())
	default:
		return w.settings
	}
}

// validate holds a source, as it would be written, to the rules a local
// command holds its file to.
func (w workingJournal) validate(source syncSource, path string) error {
	data := w.render(source)
	if view := viewSource(source, path, data, true); view.problem != "" {
		return errors.New(view.problem)
	}
	return nil
}
