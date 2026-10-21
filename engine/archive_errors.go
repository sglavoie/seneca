package engine

import (
	"fmt"
	"strings"
)

// Archive diagnostics and the refusals built on them. Parsing never fails; a
// state-changing command decides which of these are fatal.

// ArchiveDiagnosticCode names a recoverable archive problem. Parsing never
// fails; a state-changing command decides that these are fatal.
type ArchiveDiagnosticCode string

const (
	DiagArchiveMalformedMarker ArchiveDiagnosticCode = "malformed-marker"
	DiagArchiveMissingAttr     ArchiveDiagnosticCode = "missing-attribute"
	DiagArchiveInvalidAttr     ArchiveDiagnosticCode = "invalid-attribute"
	DiagArchiveDuplicateID     ArchiveDiagnosticCode = "duplicate-id"
	DiagArchiveInvalidRevision ArchiveDiagnosticCode = "invalid-revision"
	DiagArchiveRecordIDBroken  ArchiveDiagnosticCode = "record-id-mismatch"
)

type ArchiveDiagnostic struct {
	Code   ArchiveDiagnosticCode
	Record int
	Detail string
}

func (d ArchiveDiagnostic) String() string {
	if d.Record < 0 {
		return fmt.Sprintf("%s: %s", d.Code, d.Detail)
	}
	return fmt.Sprintf("%s: %s (record %d)", d.Code, d.Detail, d.Record+1)
}

// ForeignArchiveError reports a refusal to append to a file Seneca did not
// write.
type ForeignArchiveError struct{ path string }

func (e *ForeignArchiveError) Error() string {
	return fmt.Sprintf("refusing to modify %s: the file exists but carries no `<!-- seneca:%s -->` marker; "+
		"move it aside and retry (nothing was written)", e.path, MarkerKindArchive)
}

// UnsupportedArchiveVersionError reports an archive written by a newer Seneca.
type UnsupportedArchiveVersionError struct {
	path    string
	version int
}

func (e *UnsupportedArchiveVersionError) Error() string {
	return fmt.Sprintf("refusing to modify %s: it declares archive schema v%d and this Seneca understands v%d; "+
		"upgrade Seneca and retry (nothing was written)", e.path, e.version, ArchiveSchemaVersion)
}

// UnreadableArchiveVersionError reports an archive marker that declares no
// schema version, or one that is not a number. Either way nothing vouches for
// the shape of the records, so a mutation must not assume the current schema.
type UnreadableArchiveVersionError struct {
	path    string
	version string
}

func (e *UnreadableArchiveVersionError) Error() string {
	declared := "declares no archive schema version"
	if e.version != "" {
		declared = fmt.Sprintf("declares %q, which is not a usable archive schema version", e.version)
	}
	return fmt.Sprintf("refusing to modify %s: its `<!-- seneca:%s -->` marker %s; "+
		"restore `v=%d` on that marker and retry (nothing was written)",
		e.path, MarkerKindArchive, declared, ArchiveSchemaVersion)
}

// headerError reports why an archive's header does not authorize a mutation.
// Doctor and every state-changing command share it, so the two can never
// disagree about whether a header is trustworthy. Callers must skip it for a
// missing or empty file, which has no header to trust yet.
func (a ReviewArchive) HeaderError(path string) error {
	switch {
	case !a.HasMarker:
		return &ForeignArchiveError{path: path}
	case !a.HeaderVersionOK:
		return &UnreadableArchiveVersionError{path: path, version: a.headerVersionRaw}
	case a.HeaderVersion > ArchiveSchemaVersion:
		return &UnsupportedArchiveVersionError{path: path, version: a.HeaderVersion}
	default:
		return nil
	}
}

// InvalidArchiveError reports archive diagnostics that block a mutation.
type InvalidArchiveError struct {
	label       string
	path        string
	diagnostics []ArchiveDiagnostic
}

func (e *InvalidArchiveError) Error() string {
	details := make([]string, 0, len(e.diagnostics))
	for _, d := range e.diagnostics {
		details = append(details, d.String())
	}
	return fmt.Sprintf("%s %s cannot be appended to safely: %s; "+
		"fix the file and retry (nothing was written)", e.label, e.path, strings.Join(details, "; "))
}
