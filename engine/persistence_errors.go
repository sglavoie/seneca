package engine

import (
	"errors"
	"fmt"
	"strings"
)

// The refusals and the one committed-failure report the persistence layer
// raises. A CommittedWriteError is the only error here that leaves new bytes on
// disk, which is why it is a distinct type.

// SymlinkDestinationError reports a refusal to replace a symlink.
type SymlinkDestinationError struct{ path string }

func (e *SymlinkDestinationError) Error() string {
	return fmt.Sprintf("refusing to write %s: destination is a symlink", e.path)
}

// CommittedWriteError reports a failure that happened after the replacement was
// already renamed into place: the destination holds the new bytes and only the
// durability step failed. Every other write failure in this package is
// write-free and safe to retry; this one is not, which is why it is a distinct
// type rather than another wrapped error.
type CommittedWriteError struct {
	Path string
	err  error
}

func (e *CommittedWriteError) Error() string {
	return fmt.Sprintf("%s already holds the new contents, but flushing the change to disk failed: %v; "+
		"the write happened — do not rerun the command, check the filesystem and the file instead",
		e.Path, e.err)
}

func (e *CommittedWriteError) Unwrap() error { return e.err }

// PendingCommitError reports a grouped commit that passed its commit point and
// then could not finish: the change is durably recorded, some of its files may
// already hold their new contents, and the next Seneca operation on the journal
// completes the rest. Like a CommittedWriteError it must not be retried.
type PendingCommitError struct {
	// Record is the commit record the next operation recovers from.
	Record string
	err    error
}

func (e *PendingCommitError) Error() string {
	return fmt.Sprintf("the save is recorded in %s but could not be finished: %v; "+
		"the next seneca command completes it — do not rerun this one", e.Record, e.err)
}

func (e *PendingCommitError) Unwrap() error { return e.err }

// WriteCommitted reports whether err left the new bytes on disk, or durably
// recorded on their way there. A caller that treats it as a write-free failure
// would retry a mutation that already landed.
func WriteCommitted(err error) bool {
	var committed *CommittedWriteError
	var pending *PendingCommitError
	return errors.As(err, &committed) || errors.As(err, &pending)
}

// StaleFileError refuses a change derived from bytes the file no longer holds.
// Nothing was written.
type StaleFileError struct {
	Path    string
	Base    Revision
	Current Revision
}

func (e *StaleFileError) Error() string {
	return fmt.Sprintf("%s changed after it was read; nothing was written", e.Path)
}

// StaleReflectionError refuses to save a reflection over an entry that changed,
// appeared, or disappeared after the reflection was opened. Saving would
// replace writing its author never saw. Nothing was written.
type StaleReflectionError struct {
	Path    string
	Date    string
	Base    Revision
	Current Revision
}

func (e *StaleReflectionError) Error() string {
	what := "changed"
	switch {
	case e.Base == RevisionAbsent:
		what = "was written"
	case e.Current == RevisionAbsent:
		what = "was removed"
	}
	return fmt.Sprintf("the entry for %s %s in %s after this reflection was opened; nothing was written",
		e.Date, what, e.Path)
}

// RecoveryError reports an interrupted commit that could not be completed. The
// journal refuses further writes until it is: finishing half of a grouped
// change, or writing past it, would leave files that disagree.
type RecoveryError struct {
	// Dir holds the commit record and the contents it was writing.
	Dir string
	err error
}

func (e *RecoveryError) Error() string {
	return fmt.Sprintf("an interrupted save recorded in %s could not be completed: %v; "+
		"nothing else was written. The contents it was writing are in that directory; "+
		"remove the directory to abandon the interrupted save", e.Dir, e.err)
}

func (e *RecoveryError) Unwrap() error { return e.err }

// OnCommittedWrite runs finish when err says the write already landed, so the
// command still reports what is on disk and still records it in git, and then
// returns err unchanged so the command still exits nonzero.
func OnCommittedWrite(err error, finish func()) error {
	if WriteCommitted(err) {
		finish()
	}
	return err
}

// DuplicateDatesError reports repeated `## <date>` headings inside one file.
// The dates are sorted so the report is stable.
type DuplicateDatesError struct {
	label string
	path  string
	dates []string
}

func (e *DuplicateDatesError) Error() string {
	return fmt.Sprintf("%s %s has duplicate entry dates: %s; "+
		"edit the file so each `## <date>` heading is unique, then retry (nothing was written)",
		e.label, e.path, strings.Join(e.dates, ", "))
}

// MergeConflictError reports dates present in both merge inputs. Merge never
// resolves such a conflict on its own.
type MergeConflictError struct {
	SrcPath string
	DstPath string
	Dates   []string
}

func (e *MergeConflictError) Error() string {
	return fmt.Sprintf("merge aborted: %d conflicting date(s) exist in both %s and %s: %s; "+
		"no file, backup, or git state was changed",
		len(e.Dates), e.SrcPath, e.DstPath, strings.Join(e.Dates, ", "))
}

// InvalidDocumentError reports parser diagnostics that make a whole-file
// rewrite lossy or ambiguous.
type InvalidDocumentError struct {
	label       string
	path        string
	diagnostics []ReflectionDiagnostic
}

func (e *InvalidDocumentError) Error() string {
	details := make([]string, 0, len(e.diagnostics))
	for _, d := range e.diagnostics {
		details = append(details, d.String())
	}
	return fmt.Sprintf("%s %s cannot be rewritten safely: %s",
		e.label, e.path, strings.Join(details, "; "))
}
