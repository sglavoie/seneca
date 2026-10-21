package engine

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sync history is what one device keeps about a journal it exchanges: its
// identity, every transaction it holds, the contents those name, and what it
// last recorded of each artifact. All of it is under the journal's own metadata
// directory:
//
//	.seneca/journals/<reflections filename>/sync/
//	├── state.json                              this device's view; never exchanged
//	├── transactions/<device>/<sequence>-<id>.json   immutable manifests
//	└── objects/<sha-256>                       contents, named by their digest
//	.seneca/directory/sync.json                 which journal in the directory is enrolled
//
// # State
//
// state.json holds the journal and device identities, this device's sequence,
// the heads of the history it has applied, and for every artifact the revision
// last recorded and the transaction that wrote it. The Markdown is the journal;
// the state is only what was last *recorded* of it, so a difference between the
// two is a change nobody has recorded yet, never an error.
//
// # Retained bases
//
// Every revision a manifest names is kept in objects/, including the ones a
// later change replaced. Reconciling two changes needs the bytes they were both
// made on, and those bytes are here, not in Git and not in a `.bak`.
//
// # Applied and pending
//
// A stored transaction is either applied — it is part of the history the heads
// describe, and the Markdown reflects it — or pending, with the reason it could
// not be applied yet. Nothing a transaction changes is written until it is
// applied whole.

const (
	syncDirName          = "sync"
	syncStateFileName    = "state.json"
	syncTransactionsName = "transactions"
	syncObjectsName      = "objects"
	syncClaimFileName    = "sync.json"
	syncManifestSuffix   = ".json"
	syncStateVersion     = 1
	syncSequenceWidth    = 8
)

// SyncDirFor is where the journal at reflectionsPath keeps its sync history.
func SyncDirFor(reflectionsPath string) string {
	return filepath.Join(JournalMetadataDirFor(reflectionsPath), syncDirName)
}

// SyncStatePathFor is this device's sync state for the journal.
func SyncStatePathFor(reflectionsPath string) string {
	return filepath.Join(SyncDirFor(reflectionsPath), syncStateFileName)
}

// SyncClaimPathFor records which journal in dir takes part in sync. The review
// archive is shared by every journal in a directory, so at most one of them can
// exchange it.
func SyncClaimPathFor(dir string) string {
	return filepath.Join(dir, JournalMetadataRootName, JournalMetadataDirectoryName, syncClaimFileName)
}

// syncManifestName is a manifest's file name, the same in a device's history
// and in an exchange.
func syncManifestName(sequence int, id string) string {
	return fmt.Sprintf("%0*d-%s%s", syncSequenceWidth, sequence, id, syncManifestSuffix)
}

func syncObjectName(digest Revision) string {
	return strings.TrimPrefix(string(digest), revisionDigestPrefix)
}

// SyncTransactionRef names a stored manifest without reading it.
type SyncTransactionRef struct {
	Device   string
	Sequence int
	ID       string
}

// ParseSyncManifestName reads a manifest's file name as an exchange lists it.
func ParseSyncManifestName(device, name string) (SyncTransactionRef, bool) {
	return parseSyncManifestName(device, name)
}

// parseSyncManifestName reads a manifest's file name. ok is false for anything
// else that happens to sit in the directory.
func parseSyncManifestName(device, name string) (SyncTransactionRef, bool) {
	stem, found := strings.CutSuffix(name, syncManifestSuffix)
	if !found || !syncIdentityPattern.MatchString(device) {
		return SyncTransactionRef{}, false
	}
	digits, id, found := strings.Cut(stem, "-")
	if !found || len(digits) < syncSequenceWidth || !recordIDPattern.MatchString(id) {
		return SyncTransactionRef{}, false
	}
	sequence, err := strconv.Atoi(digits)
	if err != nil || sequence < 1 {
		return SyncTransactionRef{}, false
	}
	return SyncTransactionRef{Device: device, Sequence: sequence, ID: id}, true
}

// listSyncManifests lists the manifests under a transactions directory, sorted
// by device and sequence. A missing directory holds none.
func listSyncManifests(dir string) ([]SyncTransactionRef, error) {
	devices, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var refs []SyncTransactionRef
	for _, device := range devices {
		if !device.IsDir() {
			continue
		}
		names, err := os.ReadDir(filepath.Join(dir, device.Name()))
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if ref, ok := parseSyncManifestName(device.Name(), name.Name()); ok {
				refs = append(refs, ref)
			}
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		a, b := refs[i], refs[j]
		if a.Device != b.Device {
			return a.Device < b.Device
		}
		if a.Sequence != b.Sequence {
			return a.Sequence < b.Sequence
		}
		return a.ID < b.ID
	})
	return refs, nil
}

type syncClaim struct {
	Version int `json:"version"`
	// Journal is the reflections filename of the enrolled journal.
	Journal string `json:"journal"`
}

// readSyncClaim reports the journal enrolled in dir, if one is.
func readSyncClaim(dir string) (string, bool) {
	data, err := os.ReadFile(SyncClaimPathFor(dir))
	if err != nil {
		return "", false
	}
	var claim syncClaim
	if err := json.Unmarshal(data, &claim); err != nil || claim.Version != syncStateVersion {
		return "", false
	}
	if claim.Journal == "" || claim.Journal != filepath.Base(claim.Journal) {
		return "", false
	}
	return claim.Journal, true
}

// SyncClaimError refuses to enroll a second journal in a directory.
type SyncClaimError struct {
	Dir     string
	Journal string
}

func (e *SyncClaimError) Error() string {
	return fmt.Sprintf("%s in %s already takes part in sync, and journals in one directory share one review archive; "+
		"keep a second synced journal in a directory of its own (nothing was written)", e.Journal, e.Dir)
}

// SyncIdentityError refuses to exchange with a different journal. Two journals
// never merge because they were pointed at the same folder.
type SyncIdentityError struct {
	// Local is the journal this device is enrolled in.
	Local string
	// Remote is the journal the exchange or the manifest belongs to.
	Remote string
	// Detail says which of the two identities disagreed when the journals match.
	Detail string
}

func (e *SyncIdentityError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("this journal and the exchange disagree: %s (nothing was exchanged)", e.Detail)
	}
	return fmt.Sprintf("this journal is %s and the exchange belongs to journal %s; nothing was exchanged", e.Local, e.Remote)
}

// ErrSyncNotEnrolled reports a sync operation on a journal that was never
// enrolled or joined.
var ErrSyncNotEnrolled = errors.New("this journal does not take part in sync; enroll it or join an enrolled journal first")

// SyncPendingReason says why a stored transaction is not applied yet.
type SyncPendingReason string

const (
	// SyncPendingWaiting: a transaction it was made on top of has not arrived.
	SyncPendingWaiting SyncPendingReason = "waiting"
	// SyncPendingLocalFile: the file it would change cannot be read as a
	// journal file here, or is missing. The file is left as it is.
	SyncPendingLocalFile SyncPendingReason = "local-file"
	// SyncPendingRefused: applying it would produce a file a local command
	// would refuse to write.
	SyncPendingRefused SyncPendingReason = "refused"
)

type syncPending struct {
	Reason   SyncPendingReason `json:"reason"`
	Artifact string            `json:"artifact,omitempty"`
	Detail   string            `json:"detail,omitempty"`
}

type syncArtifactState struct {
	// Digest is the revision last recorded, RevisionAbsent once deleted.
	Digest Revision `json:"digest"`
	// Transaction is the transaction that wrote it.
	Transaction string `json:"transaction"`
	// Order places an archive record among the others. See syncOrder.
	Order *syncOrder `json:"order,omitempty"`
}

// syncCompeting is a version of an artifact held beside the one in the file.
type syncCompeting struct {
	// Digest is the competing revision, RevisionAbsent for a deletion.
	Digest Revision `json:"digest"`
	// Transaction is the transaction that wrote it.
	Transaction string `json:"transaction"`
}

// syncConflictState is an artifact two devices changed in ways that could not
// be merged. The file keeps one version; the others are held here, with their
// contents in objects/, until a change that covers them all arrives.
type syncConflictState struct {
	// Base is the revision the competing changes were made on.
	Base   Revision        `json:"base"`
	Theirs []syncCompeting `json:"theirs"`
	Reason string          `json:"reason,omitempty"`
}

type syncState struct {
	Version  int      `json:"version"`
	Journal  string   `json:"journal"`
	Device   string   `json:"device"`
	Name     string   `json:"name,omitempty"`
	Genesis  string   `json:"genesis"`
	Sequence int      `json:"sequence"`
	Heads    []string `json:"heads"`
	// Artifacts is what was last recorded of every artifact ever seen.
	Artifacts map[string]syncArtifactState `json:"artifacts"`
	// Pending is every stored transaction that is not applied, with why.
	Pending map[string]syncPending `json:"pending"`
	// Suspended names each source whose file went missing while history held
	// artifacts for it. Nothing is read from such a file as a deletion.
	Suspended map[syncSource]string `json:"suspended"`
	// Conflicts is every artifact with versions that could not be merged.
	Conflicts map[string]syncConflictState `json:"conflicts"`
	// Disputes is every day whose focus two devices changed independently.
	Disputes []syncFocusDispute `json:"disputes"`
}

func (s *syncState) fill() {
	if s.Heads == nil {
		s.Heads = []string{}
	}
	if s.Artifacts == nil {
		s.Artifacts = map[string]syncArtifactState{}
	}
	if s.Pending == nil {
		s.Pending = map[string]syncPending{}
	}
	if s.Suspended == nil {
		s.Suspended = map[syncSource]string{}
	}
	if s.Conflicts == nil {
		s.Conflicts = map[string]syncConflictState{}
	}
	if s.Disputes == nil {
		s.Disputes = []syncFocusDispute{}
	}
}

func encodeSyncState(state syncState) ([]byte, error) {
	state.fill()
	data, err := encodeSyncJSON(state)
	if err != nil {
		return nil, fmt.Errorf("encoding the sync state: %w", err)
	}
	return data, nil
}

func decodeSyncState(path string, data []byte) (syncState, error) {
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return syncState{}, fmt.Errorf("the sync state %s cannot be read: %w", path, err)
	}
	if probe.Version != syncStateVersion {
		return syncState{}, &SyncStateVersionError{Path: path, Version: probe.Version}
	}
	var state syncState
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return syncState{}, fmt.Errorf("the sync state %s cannot be read: %w", path, err)
	}
	if !syncIdentityPattern.MatchString(state.Journal) || !syncIdentityPattern.MatchString(state.Device) {
		return syncState{}, fmt.Errorf("the sync state %s names no journal or device", path)
	}
	state.fill()
	return state, nil
}

// SyncStateVersionError reports a sync state this engine cannot read: written
// by a newer seneca, or by none. Nothing is exchanged until the program on this
// device understands it.
type SyncStateVersionError struct {
	Path    string
	Version int
}

func (e *SyncStateVersionError) Error() string {
	return fmt.Sprintf("the sync state %s is version %d; this seneca understands %d", e.Path, e.Version, syncStateVersion)
}

// Newer reports whether a newer seneca wrote the state.
func (e *SyncStateVersionError) Newer() bool { return e.Version > syncStateVersion }

// recorded is the revision last recorded of an artifact.
func (s syncState) recorded(artifact string) Revision {
	if known, ok := s.Artifacts[artifact]; ok && known.Digest != "" {
		return known.Digest
	}
	return RevisionAbsent
}

// held lists the artifacts of source history holds a live revision of, sorted.
func (s syncState) held(source syncSource) []string {
	var ids []string
	for id, known := range s.Artifacts {
		if known.Digest == RevisionAbsent || known.Digest == "" {
			continue
		}
		if of, ok := artifactSource(id); ok && of == source {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// SyncPending is a stored transaction that is not applied, and why.
type SyncPending struct {
	Transaction string
	Reason      SyncPendingReason
	// Artifact is the artifact that stopped it, when one did.
	Artifact string
	Detail   string
}

// SyncStatus is this device's view of a journal's sync history.
type SyncStatus struct {
	Journal  string
	Device   string
	Name     string
	Genesis  string
	Sequence int
	// Heads are the newest transactions of the history applied here.
	Heads   []string
	Pending []SyncPending
	// Suspended names each journal file that went missing, with why it is not
	// being read.
	Suspended map[string]string
	// Conflicts names each artifact that waits for the author to choose.
	Conflicts []string
	// DisputedFocus names each day whose focus is disputed.
	DisputedFocus []string
}

func (s syncState) status() SyncStatus {
	status := SyncStatus{
		Journal:   s.Journal,
		Device:    s.Device,
		Name:      s.Name,
		Genesis:   s.Genesis,
		Sequence:  s.Sequence,
		Heads:     append([]string{}, s.Heads...),
		Suspended: map[string]string{},
	}
	for source, reason := range s.Suspended {
		status.Suspended[string(source)] = reason
	}
	for artifact := range s.Conflicts {
		status.Conflicts = append(status.Conflicts, artifact)
	}
	sort.Strings(status.Conflicts)
	for _, dispute := range s.Disputes {
		status.DisputedFocus = append(status.DisputedFocus, dispute.Effective)
	}
	ids := make([]string, 0, len(s.Pending))
	for id := range s.Pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		pending := s.Pending[id]
		status.Pending = append(status.Pending, SyncPending{Transaction: id, Reason: pending.Reason, Artifact: pending.Artifact, Detail: pending.Detail})
	}
	return status
}

// SyncStatus reports the journal's sync history as this device holds it,
// completing an interrupted commit first. enrolled is false, with no error, for
// a journal that does not take part in sync. It creates nothing.
func (j *Journal) SyncStatus() (status SyncStatus, enrolled bool, err error) {
	state, enrolled, err := j.readSyncState()
	if err != nil || !enrolled {
		return SyncStatus{}, false, err
	}
	return state.status(), true, nil
}

func (j *Journal) readSyncState() (syncState, bool, error) {
	if err := j.requireReflections(); err != nil {
		return syncState{}, false, err
	}
	if err := j.Recover(); err != nil {
		return syncState{}, false, err
	}
	path := SyncStatePathFor(j.reflectionsPath)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return syncState{}, false, nil
	}
	if err != nil {
		return syncState{}, false, fmt.Errorf("reading the sync state %s: %w", path, err)
	}
	state, err := decodeSyncState(path, data)
	if err != nil {
		return syncState{}, false, err
	}
	return state, true, nil
}

// newSyncIdentity draws a journal or device identity.
func (j *Journal) newSyncIdentity() (string, error) {
	raw := make([]byte, 16)
	if _, err := io.ReadFull(j.random, raw); err != nil {
		return "", fmt.Errorf("drawing a sync identity: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// SyncDiagnosticCode names something sync noticed and did not act on.
type SyncDiagnosticCode string

const (
	// SyncDiagIncomplete: a manifest, or contents it names, has not fully
	// arrived. It is looked at again next time; nothing of it was applied.
	SyncDiagIncomplete SyncDiagnosticCode = "incomplete"
	// SyncDiagMalformed: a manifest that is not what its name says, or not a
	// manifest at all.
	SyncDiagMalformed SyncDiagnosticCode = "malformed"
	// SyncDiagNewerProtocol: a manifest written under a protocol this engine
	// does not understand. It is left where it is.
	SyncDiagNewerProtocol SyncDiagnosticCode = "newer-protocol"
	// SyncDiagForeignJournal: a manifest that belongs to another journal.
	SyncDiagForeignJournal SyncDiagnosticCode = "foreign-journal"
	// SyncDiagInvalidContent: contents a local command would refuse to write.
	SyncDiagInvalidContent SyncDiagnosticCode = "invalid-content"
	// SyncDiagSequenceReuse: a second transaction claiming a device sequence
	// number already used by another.
	SyncDiagSequenceReuse SyncDiagnosticCode = "sequence-reuse"
	// SyncDiagLocalInvalid: a journal file here cannot be read as one. It was
	// left untouched, and nothing of it was recorded.
	SyncDiagLocalInvalid SyncDiagnosticCode = "local-invalid"
	// SyncDiagLocalMissing: a journal file is missing, or holds nothing, while
	// history holds artifacts for it. That is not read as a deletion.
	SyncDiagLocalMissing SyncDiagnosticCode = "local-missing"
	// SyncDiagPending: a complete transaction that could not be applied yet.
	SyncDiagPending SyncDiagnosticCode = "pending"
	// SyncDiagMassDeletion: a journal file lost most of what was recorded of
	// it at once. That is not read as deletions until the author says so.
	SyncDiagMassDeletion SyncDiagnosticCode = "mass-deletion"
)

// SyncDiagnostic is one thing sync noticed and did not act on.
type SyncDiagnostic struct {
	Code SyncDiagnosticCode
	// Transaction is the manifest concerned, when one is.
	Transaction string
	// Subject is the artifact or the journal file concerned, when one is.
	Subject string
	Detail  string
}

func (d SyncDiagnostic) String() string {
	parts := []string{string(d.Code)}
	if d.Transaction != "" {
		parts = append(parts, "transaction "+ShortRecordID(d.Transaction))
	}
	if d.Subject != "" {
		parts = append(parts, d.Subject)
	}
	return strings.Join(parts, " ") + ": " + d.Detail
}

// syncWork is one commit's work on a journal's sync history: the state as read
// under the lock, and what the commit adds to it.
type syncWork struct {
	journal         *Journal
	commit          *Commit
	reflectionsPath string
	archivePath     string
	settingsPath    string

	stateTx *FileTransaction
	state   syncState

	// index names every stored manifest, read once from the directory.
	index map[string]SyncTransactionRef
	// loaded holds the manifests read or written so far.
	loaded map[string]SyncTransaction
	// manifests are the ones this commit adds.
	manifests []syncManifest
	// unusable says, for each source that cannot be read or written now, why.
	unusable map[syncSource]string
	// ancestry remembers the ancestors of a transaction once walked.
	ancestry map[string]map[string]bool
	// depths remembers how far each transaction is from the start of history.
	depths map[string]int
	// accepted names each source whose missing artifacts the author confirmed
	// are deletions.
	accepted map[syncSource]bool
	// archiveChanged is set once this work records or applies a change to an
	// archive record, which is when the focus disputes must be looked at again.
	archiveChanged bool
	// merged names each artifact this work reconciled by itself.
	merged []string

	recorded    []string
	diagnostics []SyncDiagnostic
}

type syncManifest struct {
	path string
	data []byte
}

func (j *Journal) syncArchivePath(reflectionsPath string) string {
	if j.reflectionsPath != "" {
		if same, err := SameFilePath(j.reflectionsPath, reflectionsPath); err == nil && same {
			return j.archivePath
		}
	}
	return ArchivePathFor(reflectionsPath)
}

// beginSyncWork opens the sync history of the journal at reflectionsPath inside
// c. enrolled is false when there is none.
func beginSyncWork(c *Commit, reflectionsPath string) (*syncWork, bool, error) {
	statePath := SyncStatePathFor(reflectionsPath)
	stateTx, err := c.Metadata(statePath)
	if err != nil {
		return nil, false, err
	}
	w := &syncWork{
		journal:         c.journal,
		commit:          c,
		reflectionsPath: reflectionsPath,
		archivePath:     c.journal.syncArchivePath(reflectionsPath),
		settingsPath:    SettingsPathFor(reflectionsPath),
		stateTx:         stateTx,
		loaded:          map[string]SyncTransaction{},
		unusable:        map[syncSource]string{},
		ancestry:        map[string]map[string]bool{},
		depths:          map[string]int{},
		accepted:        map[syncSource]bool{},
	}
	if !stateTx.Exists() {
		return w, false, nil
	}
	if w.state, err = decodeSyncState(statePath, stateTx.Original()); err != nil {
		return nil, false, err
	}
	if err := w.readIndex(); err != nil {
		return nil, false, err
	}
	return w, true, nil
}

func (w *syncWork) dir() string { return SyncDirFor(w.reflectionsPath) }

func (w *syncWork) transactionsDir() string { return filepath.Join(w.dir(), syncTransactionsName) }

func (w *syncWork) objectsDir() string { return filepath.Join(w.dir(), syncObjectsName) }

func (w *syncWork) manifestPath(ref SyncTransactionRef) string {
	return filepath.Join(w.transactionsDir(), ref.Device, syncManifestName(ref.Sequence, ref.ID))
}

func (w *syncWork) readIndex() error {
	refs, err := listSyncManifests(w.transactionsDir())
	if err != nil {
		return fmt.Errorf("listing the sync history: %w", err)
	}
	w.index = make(map[string]SyncTransactionRef, len(refs))
	for _, ref := range refs {
		w.index[ref.ID] = ref
	}
	return nil
}

// applied reports whether a transaction is part of the history applied here:
// stored, and not pending.
func (w *syncWork) applied(id string) bool {
	if _, stored := w.index[id]; !stored {
		return false
	}
	_, pending := w.state.Pending[id]
	return !pending
}

// load reads a stored manifest.
func (w *syncWork) load(id string) (SyncTransaction, error) {
	if tx, ok := w.loaded[id]; ok {
		return tx, nil
	}
	ref, ok := w.index[id]
	if !ok {
		return SyncTransaction{}, fmt.Errorf("transaction %s is not in the sync history", ShortRecordID(id))
	}
	data, err := os.ReadFile(w.manifestPath(ref))
	if err != nil {
		return SyncTransaction{}, fmt.Errorf("reading transaction %s: %w", ShortRecordID(id), err)
	}
	if SyncTransactionID(data) != id {
		return SyncTransaction{}, fmt.Errorf("transaction %s no longer matches its identity", ShortRecordID(id))
	}
	tx, err := DecodeSyncTransaction(data)
	if err != nil {
		return SyncTransaction{}, fmt.Errorf("reading transaction %s: %w", ShortRecordID(id), err)
	}
	w.loaded[id] = tx
	return tx, nil
}

// ancestors is every transaction id was made on top of, directly or not, as far
// as this device holds them.
func (w *syncWork) ancestors(id string) (map[string]bool, error) {
	if known, ok := w.ancestry[id]; ok {
		return known, nil
	}
	found := map[string]bool{}
	queue := []string{id}
	for len(queue) > 0 {
		tx, err := w.load(queue[0])
		queue = queue[1:]
		if err != nil {
			return nil, err
		}
		for _, parent := range tx.Parents {
			if !found[parent] {
				found[parent] = true
				queue = append(queue, parent)
			}
		}
	}
	w.ancestry[id] = found
	return found, nil
}

// storeObject keeps contents under their digest. Objects are immutable and
// named by what they hold, so one written for a commit that then fails is
// harmless, and they are written ahead of the commit rather than through it.
func (w *syncWork) storeObject(digest Revision, data []byte) error {
	if RevisionOf(data) != digest {
		return fmt.Errorf("contents do not match their digest %s", digest)
	}
	path := filepath.Join(w.objectsDir(), syncObjectName(digest))
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := w.journal.fs.AtomicWriteFile(path, data, commitPrivateMode); err != nil {
		return fmt.Errorf("keeping contents %s: %w", digest, uncommitted(err))
	}
	return nil
}

// object reads retained contents and checks them against their digest.
func (w *syncWork) object(digest Revision) ([]byte, error) {
	return readSyncObject(w.objectsDir(), digest)
}

func readSyncObject(dir string, digest Revision) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(dir, syncObjectName(digest)))
	if err != nil {
		return nil, err
	}
	if RevisionOf(data) != digest {
		return nil, fmt.Errorf("retained contents %s no longer match their digest", digest)
	}
	return data, nil
}

func (w *syncWork) diagnose(code SyncDiagnosticCode, transaction, subject, detail string) {
	w.diagnostics = append(w.diagnostics, SyncDiagnostic{Code: code, Transaction: transaction, Subject: subject, Detail: detail})
}

// record adds a transaction of this device's own on top of everything applied:
// its contents are kept, its manifest joins the commit, and the state moves to
// it.
func (w *syncWork) record(origin SyncOrigin, changes []SyncChange, contents map[Revision][]byte) (string, error) {
	for digest, data := range contents {
		if err := w.storeObject(digest, data); err != nil {
			return "", err
		}
	}
	parents := append([]string{}, w.state.Heads...)
	sort.Strings(parents)
	tx := SyncTransaction{
		Protocol: SyncProtocolVersion,
		Journal:  w.state.Journal,
		Device:   w.state.Device,
		Sequence: w.state.Sequence + 1,
		Parents:  parents,
		Created:  w.journal.clock().UTC().Format(time.RFC3339),
		Origin:   origin,
		Changes:  changes,
	}
	if err := tx.validate(); err != nil {
		return "", fmt.Errorf("recording a sync transaction: %w", err)
	}
	data, err := encodeSyncTransaction(tx)
	if err != nil {
		return "", err
	}
	id := SyncTransactionID(data)
	ref := SyncTransactionRef{Device: tx.Device, Sequence: tx.Sequence, ID: id}
	w.manifests = append(w.manifests, syncManifest{path: w.manifestPath(ref), data: data})
	if w.index == nil {
		w.index = map[string]SyncTransactionRef{}
	}
	w.index[id] = ref
	w.loaded[id] = tx

	w.state.fill()
	w.state.Sequence = tx.Sequence
	w.state.Heads = []string{id}
	for i, change := range changes {
		known := w.state.Artifacts[change.Artifact]
		next := syncArtifactState{
			Digest:      change.result(),
			Transaction: id,
			Order:       known.Order,
		}
		if source, _ := artifactSource(change.Artifact); source == syncSourceArchive {
			w.archiveChanged = true
			if next.Order == nil && !change.Deleted {
				depth, err := w.depth(id)
				if err != nil {
					return "", err
				}
				next.Order = &syncOrder{Depth: depth, Transaction: id, Index: i}
			}
		}
		w.state.Artifacts[change.Artifact] = next
		w.settle(change)
	}
	w.recorded = append(w.recorded, id)
	return id, nil
}

// settle drops, from an artifact's conflict, every competing version a change
// of this device's own covers or arrives at. A conflict nothing competes in any
// more is over.
func (w *syncWork) settle(change SyncChange) {
	conflict, conflicted := w.state.Conflicts[change.Artifact]
	if !conflicted {
		return
	}
	conflict.Theirs = uncovered(conflict.Theirs, change)
	if len(conflict.Theirs) == 0 {
		delete(w.state.Conflicts, change.Artifact)
		return
	}
	w.state.Conflicts[change.Artifact] = conflict
}

// uncovered is the competing versions a change leaves standing: the ones it
// neither replaces nor arrives at.
func uncovered(held []syncCompeting, change SyncChange) []syncCompeting {
	var left []syncCompeting
	for _, other := range held {
		if !change.covers(other.Digest) && other.Digest != change.result() {
			left = append(left, other)
		}
	}
	return left
}

// depth is how many transactions lie on the longest path from the start of
// history to id. A transaction is always deeper than everything it was made on
// top of, on every device that holds it.
func (w *syncWork) depth(id string) (int, error) {
	if depth, ok := w.depths[id]; ok {
		return depth, nil
	}
	tx, err := w.load(id)
	if err != nil {
		return 0, err
	}
	depth := 0
	for _, parent := range tx.Parents {
		below, err := w.depth(parent)
		if err != nil {
			return 0, err
		}
		if below+1 > depth {
			depth = below + 1
		}
	}
	w.depths[id] = depth
	return depth, nil
}

// holdsAll reports whether view holds every one of ids, whatever their bytes.
func (v sourceView) holdsAll(ids []string) bool {
	for _, id := range ids {
		if _, found := v.find(id); !found {
			return false
		}
	}
	return true
}

// missing counts how many of ids view does not hold.
func (v sourceView) missing(ids []string) int {
	gone := 0
	for _, id := range ids {
		if _, found := v.find(id); !found {
			gone++
		}
	}
	return gone
}

const (
	syncSuspendedMissing      = "missing"
	syncSuspendedMassDeletion = "mass-deletion"
	// massDeletionFloor is the fewest artifacts that can go missing at once and
	// still be read as a damaged file rather than as an author's deletions.
	massDeletionFloor = 3
)

// massDeletion reports whether losing gone of held artifacts at once is more
// than an author deleting a few of them: most of the file, and not just a
// couple of entries.
func massDeletion(gone, held int) bool {
	return gone >= massDeletionFloor && gone*2 > held
}

// unrecorded compares the files with what history last recorded and returns
// the difference as changes: what an editor, Git, or anything else did to the
// Markdown since. It also settles which sources can be read at all.
//
// A file that cannot be read as a journal file contributes nothing and is left
// alone. A file that is missing, or holds nothing, while history holds
// artifacts for it is not a deletion of them: the source is suspended until
// every one of them is back, and only then are differences read from it again.
func (w *syncWork) unrecorded(views map[syncSource]sourceView) ([]SyncChange, map[Revision][]byte) {
	w.state.fill()
	var changes []SyncChange
	contents := map[Revision][]byte{}
	for _, source := range syncSources {
		view := views[source]
		held := w.state.held(source)
		delete(w.unusable, source)
		switch {
		case view.problem != "":
			w.unusable[source] = view.problem
			w.diagnose(SyncDiagLocalInvalid, "", string(source), view.problem)
			continue
		case w.accepted[source]:
			delete(w.state.Suspended, source)
		case len(view.units) == 0 && len(held) > 0:
			w.state.Suspended[source] = syncSuspendedMissing
		case w.state.Suspended[source] != "" && view.holdsAll(held):
			delete(w.state.Suspended, source)
		case w.state.Suspended[source] == "" && massDeletion(view.missing(held), len(held)):
			w.state.Suspended[source] = syncSuspendedMassDeletion
		}
		switch w.state.Suspended[source] {
		case "":
		case syncSuspendedMassDeletion:
			detail := fmt.Sprintf("the %s file no longer holds %d of the %s recorded of it; that is not read as deletions until the file is restored or the deletions are accepted",
				source, view.missing(held), Plural(len(held), "artifact", "artifacts"))
			w.unusable[source] = detail
			w.diagnose(SyncDiagMassDeletion, "", string(source), detail)
			continue
		default:
			detail := fmt.Sprintf("the %s file is missing or no longer holds what was recorded of it (%s); nothing is read from it as a deletion",
				source, Plural(len(held), "artifact", "artifacts"))
			w.unusable[source] = detail
			w.diagnose(SyncDiagLocalMissing, "", string(source), detail)
			continue
		}

		for _, unit := range view.units {
			digest := RevisionOf(unit.data)
			if base := w.state.recorded(unit.id); base != digest {
				changes = append(changes, SyncChange{Artifact: unit.id, Base: base, New: digest})
				contents[digest] = unit.data
			}
		}
		for _, id := range held {
			if _, found := view.find(id); !found {
				changes = append(changes, SyncChange{Artifact: id, Base: w.state.recorded(id), Deleted: true})
			}
		}
	}
	return changes, contents
}

// observe records what the files hold that history does not, as one
// transaction of external origin.
func (w *syncWork) observe(views map[syncSource]sourceView) error {
	changes, contents := w.unrecorded(views)
	if len(changes) == 0 {
		return nil
	}
	_, err := w.record(SyncOriginExternal, changes, contents)
	return err
}

// recordLocal records what the commit itself changes: for each file it staged,
// the difference between the bytes it read and the bytes it writes.
func (w *syncWork) recordLocal(before, after map[syncSource]sourceView, staged map[syncSource]bool) error {
	var changes []SyncChange
	contents := map[Revision][]byte{}
	for _, source := range syncSources {
		if !staged[source] || after[source].problem != "" {
			continue
		}
		pre, post := before[source], after[source]
		for _, unit := range post.units {
			if i, found := pre.find(unit.id); found && bytes.Equal(pre.units[i].data, unit.data) {
				continue
			}
			digest := RevisionOf(unit.data)
			if base := w.state.recorded(unit.id); base != digest {
				changes = append(changes, SyncChange{Artifact: unit.id, Base: base, New: digest})
				contents[digest] = unit.data
			}
		}
		for _, unit := range pre.units {
			if _, found := post.find(unit.id); found {
				continue
			}
			if base := w.state.recorded(unit.id); base != RevisionAbsent {
				changes = append(changes, SyncChange{Artifact: unit.id, Base: base, Deleted: true})
			}
		}
	}
	if len(changes) == 0 {
		return nil
	}
	_, err := w.record(SyncOriginLocal, changes, contents)
	return err
}

// stage adds this work's manifests and the state they lead to, to the commit.
// records is the archive as the commit leaves it, for the focus disputes; nil
// when the archive cannot be read, which leaves the disputes as they were.
func (w *syncWork) stage(records []syncUnit) error {
	if w.archiveChanged && records != nil {
		if err := w.refreshDisputes(records); err != nil {
			return err
		}
	}
	for _, manifest := range w.manifests {
		tx, err := w.commit.Metadata(manifest.path)
		if err != nil {
			return err
		}
		if err := tx.Commit(manifest.data); err != nil {
			return err
		}
	}
	encoded, err := encodeSyncState(w.state)
	if err != nil {
		return err
	}
	if w.stateTx.Unchanged(encoded) {
		return nil
	}
	return w.stateTx.Commit(encoded)
}

// syncPaths are the three files of the journal at reflectionsPath.
func (w *syncWork) path(source syncSource) string {
	switch source {
	case syncSourceReflections:
		return w.reflectionsPath
	case syncSourceArchive:
		return w.archivePath
	default:
		return w.settingsPath
	}
}

// recordSync records, in the commit itself, what the commit changes in a
// journal that takes part in sync, so a save and its outgoing transaction are
// one recoverable unit.
//
// It never fails a save. Sync is not in the path of one: when the history
// cannot be read or added to, the files are written without it, and the change
// is found in them and recorded the next time the history is.
func (c *Commit) recordSync() {
	if c.syncSealed || len(c.staged) == 0 {
		return
	}
	for _, dir := range c.journal.lockDirs() {
		name, ok := readSyncClaim(dir)
		if !ok {
			continue
		}
		mark := len(c.staged)
		if err := c.sealSync(filepath.Join(dir, name), c.staged[:mark]); err != nil {
			c.staged = c.staged[:mark]
		}
	}
}

func (c *Commit) sealSync(reflectionsPath string, staged []stagedChange) error {
	w, enrolled, err := beginSyncWork(c, reflectionsPath)
	if err != nil || !enrolled {
		return err
	}

	before := map[syncSource]sourceView{}
	after := map[syncSource]sourceView{}
	written := map[syncSource]bool{}
	for _, source := range syncSources {
		path := w.path(source)
		var change *stagedChange
		for i := range staged {
			if same, err := SameFilePath(staged[i].tx.path, path); err == nil && same {
				change = &staged[i]
			}
		}
		if change != nil {
			before[source] = viewSource(source, path, change.tx.original, change.tx.exists)
			after[source] = viewSource(source, path, change.data, true)
			written[source] = true
			continue
		}
		tx, err := c.journal.fs.BeginFileTransaction(path)
		if err != nil {
			before[source] = sourceView{problem: err.Error()}
		} else {
			before[source] = viewSource(source, path, tx.original, tx.exists)
		}
		after[source] = before[source]
	}

	if err := w.observe(before); err != nil {
		return err
	}
	if err := w.recordLocal(before, after, written); err != nil {
		return err
	}
	var records []syncUnit
	if archive := after[syncSourceArchive]; archive.problem == "" {
		records = append([]syncUnit{}, archive.units...)
	}
	return w.stage(records)
}
