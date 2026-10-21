package engine

import (
	"fmt"
	"sort"
)

// Validation for reflections files. A mutation fails closed here, before the
// persistence layer touches anything on disk.

// ReflectionsFileLabel names the file in every refusal raised about it, so a
// report reads the same whichever command provoked it — including the read-only
// ones, which validate the same document without opening a transaction.
const ReflectionsFileLabel = "reflections file"

// RewriteFatalCodes are the diagnostics that block a whole-file rewrite. A
// rewrite reorders and re-emits entries by date, so an entry Seneca cannot date
// would move or lose content. malformed-metadata and duplicate-section are not
// fatal here: raw-preserving rendering carries those bytes through untouched.
var RewriteFatalCodes = map[ReflectionDiagnosticCode]bool{
	DiagMissingDate:   true,
	DiagMalformedDate: true,
	diagInvalidEntry:  true,
}

// RerenderFatalCodes additionally block re-rendering one specific entry, where
// structured fields — not raw bytes — become the output.
var RerenderFatalCodes = map[ReflectionDiagnosticCode]bool{
	DiagMissingDate:       true,
	DiagMalformedDate:     true,
	diagInvalidEntry:      true,
	diagMalformedMetadata: true,
	diagDuplicateSection:  true,
}

// ReflectionTransaction pairs a file transaction with the parsed document, so
// callers derive their new entry list from the same bytes they will replace.
type ReflectionTransaction struct {
	*FileTransaction
	Label string
	doc   ReflectionDocument
}

// OpenReflectionFile reads and parses a reflections file for mutation. Parsing
// never fails; validation happens at commit time, or earlier via validate.
func (fs FS) OpenReflectionFile(path string) (*ReflectionTransaction, error) {
	tx, err := fs.BeginFileTransaction(path)
	if err != nil {
		return nil, err
	}
	return &ReflectionTransaction{
		FileTransaction: tx,
		Label:           ReflectionsFileLabel,
		doc:             ParseReflectionDocument(string(tx.original)),
	}, nil
}

func (tx *ReflectionTransaction) Entries() []ReflectionEntry {
	return tx.doc.Entries
}

// validate fails closed on anything that would make a rewrite lossy or
// ambiguous: undatable entries and repeated dates.
func (tx *ReflectionTransaction) Validate() error {
	return ValidateReflectionDocument(tx.Label, tx.path, tx.doc)
}

// commitEntries validates the incoming entries, then persists them in
// raw-preserving form. Nothing on disk changes unless validation passes.
func (tx *ReflectionTransaction) CommitEntries(entries []ReflectionEntry) error {
	if err := validateReflectionEntries(tx.Label, tx.path, entries); err != nil {
		return err
	}
	return tx.Commit([]byte(RenderReflectionEntries(entries)))
}

// ValidateReflectionDocument rejects fatal parser diagnostics and duplicate
// dates in a freshly parsed document.
func ValidateReflectionDocument(label, path string, doc ReflectionDocument) error {
	if err := fatalDiagnosticsError(label, path, doc.Diagnostics, RewriteFatalCodes); err != nil {
		return err
	}
	return duplicateDatesErrorFor(label, path, doc.Entries)
}

// validateReflectionEntries applies the same rules to an entry list assembled
// in memory, catching an entry a command built or edited incorrectly.
func validateReflectionEntries(label, path string, entries []ReflectionEntry) error {
	var diagnostics []ReflectionDiagnostic
	for _, e := range entries {
		diagnostics = append(diagnostics, e.Diagnostics...)
		// An entry assembled in memory carries no diagnostics of its own, so
		// catch an undatable one here — but only when parsing did not already
		// say so, to keep the report free of duplicates.
		if e.Date == "" && !hasFatalDiagnostic(e.Diagnostics, RewriteFatalCodes) {
			diagnostics = append(diagnostics, ReflectionDiagnostic{
				Code:   DiagMissingDate,
				detail: fmt.Sprintf("entry heading %q has no YYYY-MM-DD date", e.canonicalHeadingLine()),
			})
		}
	}
	if err := fatalDiagnosticsError(label, path, diagnostics, RewriteFatalCodes); err != nil {
		return err
	}
	return duplicateDatesErrorFor(label, path, entries)
}

// ValidateEntryRerender guards the stricter case: one entry is about to be
// re-rendered from its structured fields rather than replayed from raw bytes.
func ValidateEntryRerender(label, path string, entry ReflectionEntry) error {
	return fatalDiagnosticsError(label, path, entry.Diagnostics, RerenderFatalCodes)
}

func hasFatalDiagnostic(diagnostics []ReflectionDiagnostic, fatal map[ReflectionDiagnosticCode]bool) bool {
	for _, d := range diagnostics {
		if fatal[d.Code] {
			return true
		}
	}
	return false
}

func fatalDiagnosticsError(label, path string, diagnostics []ReflectionDiagnostic, fatal map[ReflectionDiagnosticCode]bool) error {
	var found []ReflectionDiagnostic
	for _, d := range diagnostics {
		if fatal[d.Code] {
			found = append(found, d)
		}
	}
	if len(found) == 0 {
		return nil
	}
	return &InvalidDocumentError{label: label, path: path, diagnostics: found}
}

func duplicateDatesErrorFor(label, path string, entries []ReflectionEntry) error {
	dates := DuplicateEntryDates(entries)
	if len(dates) == 0 {
		return nil
	}
	return &DuplicateDatesError{label: label, path: path, dates: dates}
}

// DuplicateEntryDates returns each date used by more than one entry, sorted.
func DuplicateEntryDates(entries []ReflectionEntry) []string {
	counts := make(map[string]int, len(entries))
	for _, e := range entries {
		if e.Date == "" {
			continue
		}
		counts[e.Date]++
	}
	var dates []string
	for date, count := range counts {
		if count > 1 {
			dates = append(dates, date)
		}
	}
	sort.Strings(dates)
	return dates
}

// ConflictingEntryDates returns the dates present in both entry lists, sorted.
func ConflictingEntryDates(left, right []ReflectionEntry) []string {
	seen := make(map[string]struct{}, len(left))
	for _, e := range left {
		if e.Date != "" {
			seen[e.Date] = struct{}{}
		}
	}
	found := map[string]struct{}{}
	for _, e := range right {
		if _, ok := seen[e.Date]; ok && e.Date != "" {
			found[e.Date] = struct{}{}
		}
	}
	dates := make([]string, 0, len(found))
	for date := range found {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	return dates
}
