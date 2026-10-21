package senecabridge

import (
	"errors"
	"io/fs"

	"github.com/sglavoie/seneca/engine"
)

// describeError turns an engine refusal into a problem: a stable code, and the
// message the command line already gives for it.
func describeError(err error) *problem {
	var own *problem
	if errors.As(err, &own) {
		return own
	}
	p := &problem{Code: errorCode(err), Message: err.Error()}
	p.Retryable = p.Code == "journal-busy"
	p.Details = errorDetails(err)
	return p
}

func errorCode(err error) string {
	var (
		staleReflection *engine.StaleReflectionError
		staleSettings   *engine.StaleSettingsError
		staleConflict   *engine.StaleConflictError
		staleFile       *engine.StaleFileError
		busy            *engine.JournalBusyError
		future          *engine.FutureDateError
		anchorMoved     *engine.AnchorMovedError
		settingsDoc     *engine.SettingsDocumentError
		invalidDoc      *engine.InvalidDocumentError
		duplicates      *engine.DuplicateDatesError
		invalidArchive  *engine.InvalidArchiveError
		foreignArchive  *engine.ForeignArchiveError
		newerArchive    *engine.UnsupportedArchiveVersionError
		unreadArchive   *engine.UnreadableArchiveVersionError
		context         *engine.UnreadableContextError
		symlink         *engine.SymlinkDestinationError
		recovery        *engine.RecoveryError
		committed       *engine.CommittedWriteError
		pending         *engine.PendingCommitError
		identity        *engine.SyncIdentityError
		claim           *engine.SyncClaimError
		protocol        *engine.SyncProtocolError
		stateVersion    *engine.SyncStateVersionError
	)
	switch {
	case errors.As(err, &committed):
		return "write-not-flushed"
	case errors.As(err, &pending):
		return "commit-pending"
	case errors.Is(err, engine.ErrConflictedReflection):
		return "conflicted"
	case errors.As(err, &staleReflection):
		return "stale-reflection"
	case errors.As(err, &staleSettings):
		return "stale-settings"
	case errors.As(err, &staleConflict):
		return "stale-conflict"
	case errors.As(err, &staleFile):
		return "stale-file"
	case errors.As(err, &busy):
		return "journal-busy"
	case errors.As(err, &future):
		return "future-date"
	case errors.Is(err, engine.ErrEmptyReflection):
		return "empty-reflection"
	case errors.Is(err, engine.ErrBlankFocus):
		return "blank-focus"
	case errors.As(err, &anchorMoved):
		return "anchor-moved"
	case errors.Is(err, engine.ErrSettingsNotInitialized):
		return "settings-not-initialized"
	case errors.As(err, &settingsDoc):
		return "invalid-settings-document"
	case errors.As(err, &invalidDoc), errors.As(err, &duplicates):
		return "invalid-reflections"
	case errors.As(err, &context):
		return "unreadable-context"
	case errors.As(err, &invalidArchive), errors.As(err, &foreignArchive),
		errors.As(err, &newerArchive), errors.As(err, &unreadArchive):
		return "invalid-archive"
	case errors.As(err, &symlink):
		return "symlink-destination"
	case errors.As(err, &recovery):
		return "recovery-blocked"
	case errors.As(err, &identity):
		return "sync-identity"
	case errors.As(err, &claim):
		return "sync-claim"
	case errors.As(err, &protocol):
		return "sync-protocol"
	case errors.Is(err, engine.ErrSyncNotEnrolled):
		return "sync-not-enrolled"
	case errors.As(err, &stateVersion):
		return "sync-state-version"
	case errors.Is(err, engine.ErrNothingToRecover):
		return "nothing-to-recover"
	case errors.Is(err, engine.ErrInvalidReminderTimes):
		return "invalid-reminders"
	case errors.Is(err, engine.ErrInvalidPracticePack):
		return "invalid-pack"
	case errors.Is(err, engine.ErrRecoveryDestinationExists):
		return "destination-exists"
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission):
		return "file-unavailable"
	}
	// Everything else the engine refuses is a refusal of the request as it
	// was made: the message says what, and no code is promised for it.
	return "refused"
}

// errorDetails carries what a caller needs to act on a refusal without
// reading its message.
func errorDetails(err error) map[string]string {
	var (
		staleReflection *engine.StaleReflectionError
		staleSettings   *engine.StaleSettingsError
		staleConflict   *engine.StaleConflictError
		future          *engine.FutureDateError
	)
	switch {
	case errors.As(err, &staleReflection):
		return map[string]string{"date": staleReflection.Date, "base": string(staleReflection.Base), "current": string(staleReflection.Current)}
	case errors.As(err, &staleSettings):
		details := map[string]string{"base": string(staleSettings.Base), "current": string(staleSettings.Current)}
		if staleSettings.Date != "" {
			details["date"] = staleSettings.Date
		}
		return details
	case errors.As(err, &staleConflict):
		return map[string]string{"id": staleConflict.ID}
	case errors.As(err, &future):
		return map[string]string{"date": future.Date, "today": future.Today}
	}
	return nil
}
