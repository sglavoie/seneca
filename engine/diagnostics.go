package engine

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Diagnostics are what a device can say about a journal without saying
// anything written in it. They hold versions, counts, and stable codes, and
// nothing else: no entry text, no question, no focus, no review, no date, no
// path, no device name, no message an error carried. Both the desktop and the
// phone collect them through here, so what one shares the other shares too,
// and a test holds the result to that rule.
//
// Collecting changes nothing: it reads files, takes no lock, completes no
// interrupted save, and creates nothing.

const (
	DiagnosticsFormat  = "seneca-diagnostics"
	DiagnosticsVersion = 1
)

// DiagnosticSeverity says how much a finding matters.
type DiagnosticSeverity string

const (
	// DiagnosticInfo is normal and worth knowing.
	DiagnosticInfo DiagnosticSeverity = "info"
	// DiagnosticWarn needs the author eventually; nothing is lost meanwhile.
	DiagnosticWarn DiagnosticSeverity = "warn"
	// DiagnosticError stops something until the author acts.
	DiagnosticError DiagnosticSeverity = "error"
)

// Finding codes. Each is stable, so a platform can say what to do about it in
// its own words.
const (
	FindingReflectionsUnreadable = "reflections-unreadable"
	FindingReflectionsInvalid    = "reflections-invalid"
	FindingArchiveInvalid        = "archive-invalid"
	FindingSettingsInvalid       = "settings-invalid"
	FindingCommitInterrupted     = "commit-interrupted"
	FindingSyncStateNewer        = "sync-state-newer"
	FindingSyncStateUnreadable   = "sync-state-unreadable"
	FindingExchangeUnreachable   = "exchange-unreachable"
	FindingExchangeForeign       = "exchange-foreign"
	FindingExchangeNewer         = "exchange-newer-protocol"
	FindingExchangeEmpty         = "exchange-empty"
	FindingChangesUnpublished    = "changes-unpublished"
	FindingChangesUnimported     = "changes-unimported"
	FindingChangesIncompatible   = "changes-incompatible"
	FindingIdentityReused        = "identity-reused"
	FindingChangesWaiting        = "changes-waiting"
	FindingChangesBlocked        = "changes-blocked"
	FindingFileSuspended         = "file-suspended"
	FindingConflictsOpen         = "conflicts-open"
)

// DiagnosticFinding is one thing that is not as it should be, or worth
// knowing: a code, how much it matters, and how many things it concerns.
type DiagnosticFinding struct {
	Code     string             `json:"code"`
	Severity DiagnosticSeverity `json:"severity"`
	Count    int                `json:"count"`
}

// DiagnosticVersions are the formats this engine reads and writes.
type DiagnosticVersions struct {
	ReflectionSchema int `json:"reflection_schema"`
	ArchiveSchema    int `json:"archive_schema"`
	Settings         int `json:"settings"`
	SyncProtocol     int `json:"sync_protocol"`
	SyncState        int `json:"sync_state"`
}

// DiagnosticJournal is the shape of the journal, in counts.
type DiagnosticJournal struct {
	ReflectionsPresent bool `json:"reflections_present"`
	Entries            int  `json:"entries"`
	LegacyEntries      int  `json:"legacy_entries"`
	ParseProblems      int  `json:"parse_problems"`
	ArchivePresent     bool `json:"archive_present"`
	Reviews            int  `json:"reviews"`
	FocusEvents        int  `json:"focus_events"`
	ArchiveProblems    int  `json:"archive_problems"`
	OwnSettings        bool `json:"own_settings"`
	Questions          int  `json:"questions"`
	RetainedDrafts     int  `json:"retained_drafts"`
	InterruptedCommit  bool `json:"interrupted_commit"`
}

// DiagnosticExchange is how the journal's history compares with the
// exchange, in counts.
type DiagnosticExchange struct {
	Checked      bool `json:"checked"`
	Found        bool `json:"found"`
	Unpublished  int  `json:"unpublished"`
	Unimported   int  `json:"unimported"`
	Incompatible int  `json:"incompatible"`
	Impersonated int  `json:"impersonated"`
}

// DiagnosticSync is where sync stands, in counts.
type DiagnosticSync struct {
	Enrolled bool `json:"enrolled"`
	Sequence int  `json:"sequence"`
	Heads    int  `json:"heads"`
	// Pending counts the held transactions by why they wait.
	Pending map[string]int `json:"pending"`
	// Suspended names the journal files sync stopped reading: reflections,
	// archive, or settings.
	Suspended     []string           `json:"suspended"`
	Conflicts     int                `json:"conflicts"`
	DisputedFocus int                `json:"disputed_focus"`
	Exchange      DiagnosticExchange `json:"exchange"`
}

// Diagnostics is a content-free report on one journal.
type Diagnostics struct {
	Format   string              `json:"format"`
	Version  int                 `json:"version"`
	Versions DiagnosticVersions  `json:"versions"`
	Journal  DiagnosticJournal   `json:"journal"`
	Sync     DiagnosticSync      `json:"sync"`
	Findings []DiagnosticFinding `json:"findings"`
}

// Has reports whether a finding with code is present.
func (d Diagnostics) Has(code string) bool {
	_, ok := d.Finding(code)
	return ok
}

// Finding returns the finding with code.
func (d Diagnostics) Finding(code string) (DiagnosticFinding, bool) {
	for _, finding := range d.Findings {
		if finding.Code == code {
			return finding, true
		}
	}
	return DiagnosticFinding{}, false
}

// CollectDiagnostics reports on the journal at reflectionsPath. transport is
// the exchange as this device reaches it, or nil when there is none.
func CollectDiagnostics(reflectionsPath string, transport SyncTransport) Diagnostics {
	d := Diagnostics{
		Format:  DiagnosticsFormat,
		Version: DiagnosticsVersion,
		Versions: DiagnosticVersions{
			ReflectionSchema: ReflectionSchemaVersion,
			ArchiveSchema:    ArchiveSchemaVersion,
			Settings:         JournalSettingsVersion,
			SyncProtocol:     SyncProtocolVersion,
			SyncState:        syncStateVersion,
		},
		Sync:     DiagnosticSync{Pending: map[string]int{}, Suspended: []string{}},
		Findings: []DiagnosticFinding{},
	}
	find := func(code string, severity DiagnosticSeverity, count int) {
		if count > 0 {
			d.Findings = append(d.Findings, DiagnosticFinding{Code: code, Severity: severity, Count: count})
		}
	}

	// The reflections file.
	if data, err := os.ReadFile(reflectionsPath); err == nil {
		doc := ParseReflectionDocument(string(data))
		d.Journal.ReflectionsPresent = true
		d.Journal.Entries = len(doc.Entries)
		d.Journal.ParseProblems = len(doc.Diagnostics) + len(DuplicateEntryDates(doc.Entries))
		for _, entry := range doc.Entries {
			if !entry.Metadata.Present {
				d.Journal.LegacyEntries++
			}
		}
		find(FindingReflectionsInvalid, DiagnosticError, d.Journal.ParseProblems)
	} else if !errors.Is(err, os.ErrNotExist) {
		find(FindingReflectionsUnreadable, DiagnosticError, 1)
	}

	// The review archive.
	archivePath := ArchivePathFor(reflectionsPath)
	if data, err := os.ReadFile(archivePath); err == nil && strings.TrimSpace(string(data)) != "" {
		archive := ParseReviewArchive(string(data))
		d.Journal.ArchivePresent = true
		problems := len(archive.Diagnostics) + len(archive.RecordIntegrityDiagnostics())
		if archive.HeaderError(archivePath) != nil {
			problems++
		}
		d.Journal.ArchiveProblems = problems
		for _, record := range archive.Records {
			switch record.Kind {
			case ArchiveRecordReview:
				d.Journal.Reviews++
			case ArchiveRecordFocus:
				d.Journal.FocusEvents++
			}
		}
		find(FindingArchiveInvalid, DiagnosticError, problems)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		find(FindingArchiveInvalid, DiagnosticError, 1)
	}

	// The journal's own settings.
	if state, own, err := LoadJournalSettings(reflectionsPath); err != nil {
		find(FindingSettingsInvalid, DiagnosticError, 1)
	} else if own {
		d.Journal.OwnSettings = true
		d.Journal.Questions = len(state.Settings.Questions)
	}
	if entries, err := os.ReadDir(filepath.Join(JournalMetadataDirFor(reflectionsPath), draftsDirName)); err == nil {
		for _, entry := range entries {
			if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
				d.Journal.RetainedDrafts++
			}
		}
	}
	if PendingCommit(filepath.Dir(reflectionsPath)) {
		d.Journal.InterruptedCommit = true
		find(FindingCommitInterrupted, DiagnosticInfo, 1)
	}

	// Sync.
	status, enrolled, err := LoadSyncStatus(reflectionsPath)
	var newer *SyncStateVersionError
	switch {
	case errors.As(err, &newer) && newer.Newer():
		find(FindingSyncStateNewer, DiagnosticError, 1)
		return d
	case err != nil:
		find(FindingSyncStateUnreadable, DiagnosticError, 1)
		return d
	case !enrolled:
		return d
	}
	d.Sync.Enrolled = true
	d.Sync.Sequence = status.Sequence
	d.Sync.Heads = len(status.Heads)
	waiting, blocked := 0, 0
	for _, pending := range status.Pending {
		d.Sync.Pending[string(pending.Reason)]++
		if pending.Reason == SyncPendingWaiting {
			waiting++
		} else {
			blocked++
		}
	}
	for file := range status.Suspended {
		d.Sync.Suspended = append(d.Sync.Suspended, file)
	}
	sort.Strings(d.Sync.Suspended)
	d.Sync.Conflicts = len(status.Conflicts)
	d.Sync.DisputedFocus = len(status.DisputedFocus)
	find(FindingChangesWaiting, DiagnosticInfo, waiting)
	find(FindingChangesBlocked, DiagnosticWarn, blocked)
	find(FindingFileSuspended, DiagnosticError, len(d.Sync.Suspended))
	find(FindingConflictsOpen, DiagnosticWarn, d.Sync.Conflicts+d.Sync.DisputedFocus)

	if transport == nil {
		return d
	}
	d.Sync.Exchange.Checked = true
	view, err := InspectSyncExchange(reflectionsPath, transport)
	var identity *SyncIdentityError
	var protocol *SyncProtocolError
	switch {
	case errors.As(err, &identity):
		find(FindingExchangeForeign, DiagnosticError, 1)
	case errors.As(err, &protocol) && protocol.Protocol > SyncProtocolVersion:
		find(FindingExchangeNewer, DiagnosticError, 1)
	case err != nil:
		find(FindingExchangeUnreachable, DiagnosticWarn, 1)
	case !view.Found:
		find(FindingExchangeEmpty, DiagnosticInfo, 1)
	default:
		d.Sync.Exchange = DiagnosticExchange{
			Checked: true, Found: true,
			Unpublished: view.Unpublished, Unimported: view.Unimported,
			Incompatible: view.Incompatible, Impersonated: view.Impersonated,
		}
		find(FindingIdentityReused, DiagnosticError, view.Impersonated)
		find(FindingChangesIncompatible, DiagnosticWarn, view.Incompatible)
		find(FindingChangesUnpublished, DiagnosticInfo, view.Unpublished)
		find(FindingChangesUnimported, DiagnosticInfo, view.Unimported-view.Incompatible)
	}
	return d
}
