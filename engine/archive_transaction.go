package engine

import "strings"

// ArchiveTransaction is a read-modify-write over the archive file. It inherits
// symlink refusal, permission preservation, and `.bak` recovery from the
// persistence layer, and adds archive-specific validation before any write.
type ArchiveTransaction struct {
	*FileTransaction
	label   string
	Archive ReviewArchive
}

func (fs FS) OpenReviewArchive(path string) (*ArchiveTransaction, error) {
	tx, err := fs.BeginFileTransaction(path)
	if err != nil {
		return nil, err
	}
	return &ArchiveTransaction{
		FileTransaction: tx,
		label:           "review archive",
		Archive:         ParseReviewArchive(string(tx.original)),
	}, nil
}

// validate fails closed on anything that makes appending unsafe. It is safe to
// call before deciding whether a mutation is needed at all.
func (tx *ArchiveTransaction) Validate() error {
	if tx.exists && strings.TrimSpace(string(tx.original)) != "" {
		if err := tx.Archive.HeaderError(tx.path); err != nil {
			return err
		}
	}
	return archiveDiagnosticsError(tx.label, tx.path, tx.Archive.MutationDiagnostics())
}

// appendRecords persists the archive with records added. The rendered result is
// re-parsed and re-validated first, so a write can never produce a file the
// next command would refuse.
func (tx *ArchiveTransaction) AppendRecords(records ...ArchiveRecord) error {
	if err := tx.Validate(); err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}

	data := tx.Archive.appended(records...)
	if err := archiveDiagnosticsError(tx.label, tx.path, ParseReviewArchive(data).MutationDiagnostics()); err != nil {
		return err
	}
	return tx.Commit([]byte(data))
}

func archiveDiagnosticsError(label, path string, diagnostics []ArchiveDiagnostic) error {
	if len(diagnostics) == 0 {
		return nil
	}
	return &InvalidArchiveError{label: label, path: path, diagnostics: diagnostics}
}
