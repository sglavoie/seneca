package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The sync format is what two devices exchange: immutable transactions and the
// contents they name.
//
// # Artifacts
//
// A journal is exchanged as artifacts, never as files. An artifact is the unit
// a merge can reason about:
//
//	entry:<YYYY-MM-DD>   one day's entry, as its raw bytes
//	record:<id>          one archive record, as its raw bytes
//	settings             the journal settings document
//
// A whole-file digest changes when a rewrite settles the spacing around a
// separator; an entry's bytes and a record's bytes do not, so an artifact has a
// stable revision on every device.
//
// # Transactions
//
// A transaction is one manifest: who wrote it (journal, device, and that
// device's sequence number), what it was written on top of (its causal
// parents), and for each artifact it changed the revision it replaced and the
// revision it wrote, or an explicit deletion. Its identity is the SHA-256 of
// its bytes, so it cannot be edited without becoming a different transaction.
// The time in it is for the reader; order comes from the parents alone.
//
// An artifact a transaction does not mention is unchanged. Nothing is ever
// deleted by being absent.

// SyncProtocolVersion is the version of the manifest and of the exchange
// layout this engine reads and writes.
const SyncProtocolVersion = 1

// SyncOrigin says how a transaction came to be.
type SyncOrigin string

const (
	// SyncOriginGenesis is the snapshot a journal was enrolled with.
	SyncOriginGenesis SyncOrigin = "genesis"
	// SyncOriginLocal is a change made through the engine.
	SyncOriginLocal SyncOrigin = "local"
	// SyncOriginExternal is a change found in the files: an editor, Git, or
	// anything else that writes Markdown without the engine.
	SyncOriginExternal SyncOrigin = "external"
	// SyncOriginMerge is what a device wrote when it reconciled two independent
	// changes that did not overlap.
	SyncOriginMerge SyncOrigin = "merge"
	// SyncOriginResolution is the author's answer to a conflict.
	SyncOriginResolution SyncOrigin = "resolution"
)

// SyncChange is what a transaction did to one artifact.
type SyncChange struct {
	Artifact string `json:"artifact"`
	// Base is the revision the change was made on: RevisionAbsent for an
	// artifact that did not exist.
	Base Revision `json:"base"`
	// New is the revision written. It is empty when Deleted.
	New Revision `json:"new,omitempty"`
	// Deleted marks an explicit deletion, the only way an artifact goes away.
	Deleted bool `json:"deleted,omitempty"`
	// Resolves names the other revisions this change considered besides its
	// base: the competing versions a merge or a resolution settled, including
	// the one it kept when it kept one of them. A change follows from any
	// revision it covers, so a device holding any of them takes the result
	// without asking again.
	Resolves []Revision `json:"resolves,omitempty"`
}

// covers reports whether the change replaces revision: its base, or a
// competing version it settled.
func (c SyncChange) covers(revision Revision) bool {
	if revision == c.Base {
		return true
	}
	for _, resolved := range c.Resolves {
		if resolved == revision {
			return true
		}
	}
	return false
}

// coveringChange is the change that replaces every revision in covered with
// result. The first covered revision that is not the result becomes the base;
// the rest are named as resolved, the result among them when it was one of the
// versions considered. ok is false when nothing but the result is covered,
// which is no change at all.
func coveringChange(artifact string, covered []Revision, result Revision) (SyncChange, bool) {
	change := SyncChange{Artifact: artifact}
	based := false
	seen := map[Revision]bool{}
	for _, revision := range covered {
		if seen[revision] {
			continue
		}
		seen[revision] = true
		if !based && revision != result {
			change.Base, based = revision, true
			continue
		}
		change.Resolves = append(change.Resolves, revision)
	}
	if !based {
		return SyncChange{}, false
	}
	sort.Slice(change.Resolves, func(i, j int) bool { return change.Resolves[i] < change.Resolves[j] })
	if result == RevisionAbsent {
		change.Deleted = true
	} else {
		change.New = result
	}
	return change, true
}

// result is the revision the artifact holds after the change.
func (c SyncChange) result() Revision {
	if c.Deleted {
		return RevisionAbsent
	}
	return c.New
}

// SyncTransaction is one immutable manifest.
type SyncTransaction struct {
	Protocol int    `json:"protocol"`
	Journal  string `json:"journal"`
	Device   string `json:"device"`
	// Sequence counts this device's transactions from 1.
	Sequence int `json:"sequence"`
	// Parents are the transactions this one was made on top of, sorted.
	Parents []string     `json:"parents"`
	Created string       `json:"created"`
	Origin  SyncOrigin   `json:"origin"`
	Changes []SyncChange `json:"changes"`
}

// SyncProtocolError reports a manifest or a descriptor written under a protocol
// this engine does not understand. It is held, never guessed at.
type SyncProtocolError struct {
	Protocol int
}

func (e *SyncProtocolError) Error() string {
	if e.Protocol > SyncProtocolVersion {
		return fmt.Sprintf("it uses sync protocol %d, written by a newer seneca; this one understands %d", e.Protocol, SyncProtocolVersion)
	}
	return fmt.Sprintf("sync protocol %d is not one this seneca understands", e.Protocol)
}

var (
	syncIdentityPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	syncDigestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func encodeSyncJSON(v any) ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// decodeSyncJSON reads one protocol-versioned document strictly: a field this
// version does not define is an error, not something to carry along.
func decodeSyncJSON(data []byte, into any) error {
	var probe struct {
		Protocol int `json:"protocol"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if probe.Protocol != SyncProtocolVersion {
		return &SyncProtocolError{Protocol: probe.Protocol}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the document")
	}
	return nil
}

// encodeSyncTransaction is the one encoding of a manifest. Its digest is the
// transaction's identity.
func encodeSyncTransaction(tx SyncTransaction) ([]byte, error) {
	if tx.Parents == nil {
		tx.Parents = []string{}
	}
	if tx.Changes == nil {
		tx.Changes = []SyncChange{}
	}
	data, err := encodeSyncJSON(tx)
	if err != nil {
		return nil, fmt.Errorf("encoding the sync transaction: %w", err)
	}
	return data, nil
}

// SyncTransactionID is the identity of the manifest stored as data.
func SyncTransactionID(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// DecodeSyncTransaction reads and validates a manifest. A *SyncProtocolError
// means it may be perfectly good under a protocol this engine does not know.
func DecodeSyncTransaction(data []byte) (SyncTransaction, error) {
	var tx SyncTransaction
	if err := decodeSyncJSON(data, &tx); err != nil {
		return SyncTransaction{}, err
	}
	if err := tx.validate(); err != nil {
		return SyncTransaction{}, err
	}
	return tx, nil
}

// check holds one change to the protocol's rules.
func (change SyncChange) check() error {
	if _, ok := artifactSource(change.Artifact); !ok {
		return fmt.Errorf("%q is not an artifact this protocol defines", change.Artifact)
	}
	if change.Base != RevisionAbsent && !syncDigestPattern.MatchString(string(change.Base)) {
		return fmt.Errorf("artifact %s has base %q, which is not a revision", change.Artifact, change.Base)
	}
	switch {
	case change.Deleted && change.New != "":
		return fmt.Errorf("artifact %s is both deleted and written", change.Artifact)
	case change.Deleted && change.Base == RevisionAbsent:
		return fmt.Errorf("artifact %s is deleted without a revision to delete", change.Artifact)
	case change.Deleted && change.Artifact == settingsArtifact:
		return errors.New("the journal settings cannot be deleted")
	case !change.Deleted && !syncDigestPattern.MatchString(string(change.New)):
		return fmt.Errorf("artifact %s has new revision %q, which is not a revision", change.Artifact, change.New)
	case !change.Deleted && change.New == change.Base:
		return fmt.Errorf("artifact %s is changed to the revision it already had", change.Artifact)
	}
	for i, resolved := range change.Resolves {
		switch {
		case resolved != RevisionAbsent && !syncDigestPattern.MatchString(string(resolved)):
			return fmt.Errorf("artifact %s resolves %q, which is not a revision", change.Artifact, resolved)
		case resolved == change.Base:
			return fmt.Errorf("artifact %s resolves the revision it names as its base", change.Artifact)
		case i > 0 && change.Resolves[i-1] >= resolved:
			return fmt.Errorf("artifact %s does not list the revisions it resolves in order, once each", change.Artifact)
		}
	}
	return nil
}

func (tx SyncTransaction) validate() error {
	if !syncIdentityPattern.MatchString(tx.Journal) {
		return fmt.Errorf("journal %q is not a journal identity", tx.Journal)
	}
	if !syncIdentityPattern.MatchString(tx.Device) {
		return fmt.Errorf("device %q is not a device identity", tx.Device)
	}
	if tx.Sequence < 1 {
		return fmt.Errorf("sequence %d is not a positive number", tx.Sequence)
	}
	if _, err := time.Parse(time.RFC3339, tx.Created); err != nil {
		return fmt.Errorf("created %q is not an RFC3339 timestamp", tx.Created)
	}
	switch tx.Origin {
	case SyncOriginGenesis, SyncOriginLocal, SyncOriginExternal, SyncOriginMerge, SyncOriginResolution:
	default:
		return fmt.Errorf("origin %q is not one this protocol defines", tx.Origin)
	}
	if tx.Origin == SyncOriginGenesis && len(tx.Parents) > 0 {
		return errors.New("a genesis transaction has no parents")
	}
	if !sort.StringsAreSorted(tx.Parents) {
		return errors.New("parents are not sorted")
	}
	for i, parent := range tx.Parents {
		if !recordIDPattern.MatchString(parent) {
			return fmt.Errorf("parent %q is not a transaction identity", parent)
		}
		if i > 0 && tx.Parents[i-1] == parent {
			return fmt.Errorf("parent %s is named twice", parent)
		}
	}

	seen := make(map[string]bool, len(tx.Changes))
	for _, change := range tx.Changes {
		if err := change.check(); err != nil {
			return err
		}
		if seen[change.Artifact] {
			return fmt.Errorf("artifact %s is changed twice", change.Artifact)
		}
		seen[change.Artifact] = true
	}
	return nil
}

// syncSource is one of the files a journal's artifacts live in.
type syncSource string

const (
	syncSourceReflections syncSource = "reflections"
	syncSourceArchive     syncSource = "archive"
	syncSourceSettings    syncSource = "settings"
)

// syncSources is every source, in the order a snapshot lists them.
var syncSources = []syncSource{syncSourceReflections, syncSourceArchive, syncSourceSettings}

const (
	entryArtifactPrefix  = "entry:"
	recordArtifactPrefix = "record:"
	settingsArtifact     = "settings"
	// opaqueRecordPrefix starts the identity of an archive record of a kind this
	// engine does not know. Such a record carries no ID Seneca can check, so it
	// is named by its bytes.
	opaqueRecordPrefix = "x"
)

func entryArtifact(date string) string { return entryArtifactPrefix + date }

func recordArtifact(id string) string { return recordArtifactPrefix + id }

// artifactSource names the file an artifact lives in, and refuses an identifier
// this protocol does not define.
func artifactSource(artifact string) (syncSource, bool) {
	switch {
	case artifact == settingsArtifact:
		return syncSourceSettings, true
	case strings.HasPrefix(artifact, entryArtifactPrefix):
		return syncSourceReflections, IsCalendarDate(strings.TrimPrefix(artifact, entryArtifactPrefix))
	case strings.HasPrefix(artifact, recordArtifactPrefix):
		id := strings.TrimPrefix(artifact, recordArtifactPrefix)
		return syncSourceArchive, recordIDPattern.MatchString(strings.TrimPrefix(id, opaqueRecordPrefix))
	default:
		return "", false
	}
}

// normalizeRecordRaw is an archive record as an artifact: its bytes with the
// blank lines that only separate it from the next record settled to one line
// break, the way an append writes it.
func normalizeRecordRaw(raw string) string {
	return strings.TrimRight(raw, "\n") + "\n"
}

// recordUnitID is the artifact identity of an archive record whose normalized
// bytes are raw.
func recordUnitID(record ArchiveRecord, raw string) string {
	if record.Kind != ArchiveRecordUnknown {
		return record.RecordID()
	}
	sum := sha256.Sum256([]byte(raw))
	return opaqueRecordPrefix + hex.EncodeToString(sum[:])
}

// validateArtifactContent holds an artifact's bytes to the rules its file is
// held to, before they are recorded or applied: an entry is exactly one datable
// entry for its day, a record is exactly one record whose identity matches its
// content, and settings are a document this engine can use.
func validateArtifactContent(artifact string, data []byte) error {
	source, ok := artifactSource(artifact)
	if !ok {
		return fmt.Errorf("%q is not an artifact this protocol defines", artifact)
	}
	switch source {
	case syncSourceReflections:
		date := strings.TrimPrefix(artifact, entryArtifactPrefix)
		raw := string(data)
		if len(splitEntryChunks(raw)) != 1 {
			return fmt.Errorf("the entry for %s holds an entry separator", date)
		}
		entry := NewReflectionEntry(raw)
		if entry.Raw != raw || raw == "" {
			return fmt.Errorf("the entry for %s is not exactly one entry", date)
		}
		if hasFatalDiagnostic(entry.Diagnostics, RewriteFatalCodes) || entry.Date != date {
			return fmt.Errorf("the entry for %s is dated %q", date, entry.Date)
		}
	case syncSourceArchive:
		id := strings.TrimPrefix(artifact, recordArtifactPrefix)
		raw := string(data)
		archive := ParseReviewArchive(raw)
		if len(archive.Records) != 1 || archive.Header != "" || normalizeRecordRaw(raw) != raw {
			return fmt.Errorf("record %s is not exactly one archive record", ShortRecordID(id))
		}
		record := archive.Records[0]
		for _, diagnostic := range archive.Diagnostics {
			// Whether a revision names an earlier review is a question for the
			// archive the record joins, not for the record alone.
			if diagnostic.Code != DiagArchiveInvalidRevision {
				return fmt.Errorf("record %s: %s", ShortRecordID(id), diagnostic)
			}
		}
		if got := recordUnitID(record, raw); got != id {
			return fmt.Errorf("record %s names itself %s", ShortRecordID(id), ShortRecordID(got))
		}
		if want, ok := record.ExpectedRecordID(); ok && want != id {
			return fmt.Errorf("record %s does not match its own content", ShortRecordID(id))
		}
	case syncSourceSettings:
		if _, err := decodeSettings(settingsFileName, data); err != nil {
			return err
		}
	}
	return nil
}

// syncUnit is one artifact as its file holds it.
type syncUnit struct {
	id   string
	data []byte
}

// sourceView is one file read as artifacts, in file order.
type sourceView struct {
	units []syncUnit
	// problem is set when the file cannot be read as artifacts: the refusal a
	// mutation would raise. Such a file is left exactly as it is.
	problem string
	// archive is the parsed archive, for its header.
	archive ReviewArchive
}

func (v sourceView) find(id string) (int, bool) {
	for i, unit := range v.units {
		if unit.id == id {
			return i, true
		}
	}
	return -1, false
}

func (v *sourceView) add(id string, data []byte) {
	if v.problem != "" {
		return
	}
	if _, found := v.find(id); found {
		v.problem = fmt.Sprintf("artifact %s appears twice", id)
		return
	}
	if err := validateArtifactContent(id, data); err != nil {
		v.problem = err.Error()
		return
	}
	v.units = append(v.units, syncUnit{id: id, data: data})
}

// viewSource reads one file's bytes as artifacts. A file that does not exist
// holds none.
func viewSource(source syncSource, path string, data []byte, exists bool) sourceView {
	var view sourceView
	if !exists {
		return view
	}
	switch source {
	case syncSourceReflections:
		doc := ParseReflectionDocument(string(data))
		if err := ValidateReflectionDocument(ReflectionsFileLabel, path, doc); err != nil {
			view.problem = err.Error()
			return view
		}
		for _, entry := range doc.Entries {
			view.add(entryArtifact(entry.Date), []byte(entry.Raw))
		}
	case syncSourceArchive:
		if strings.TrimSpace(string(data)) == "" {
			return view
		}
		view.archive = ParseReviewArchive(string(data))
		if err := view.archive.HeaderError(path); err != nil {
			view.problem = err.Error()
			return view
		}
		if err := archiveDiagnosticsError("review archive", path, view.archive.MutationDiagnostics()); err != nil {
			view.problem = err.Error()
			return view
		}
		for _, record := range view.archive.Records {
			raw := normalizeRecordRaw(record.Raw)
			view.add(recordArtifact(recordUnitID(record, raw)), []byte(raw))
		}
	case syncSourceSettings:
		if _, err := decodeSettings(path, data); err != nil {
			view.problem = err.Error()
			return view
		}
		view.add(settingsArtifact, data)
	}
	if view.problem != "" {
		view.units = nil
	}
	return view
}
