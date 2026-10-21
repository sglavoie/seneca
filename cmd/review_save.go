package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"time"
)

// saveReview persists an approved review, and any focus it changed, as one
// archive transaction. Both records are appended in a single `appendRecords`
// call so a review can never land without the focus it justified, and the git
// commit happens only once the file is actually on disk.
//
// An accepted focus is effective on the day the author accepted it, not on the
// window's end. The window end is what a review *reads* focus from; setting is
// an act of the present, exactly like `seneca focus set`, and back-dating it
// would rewrite days already reflected on.
func saveReview(
	out io.Writer,
	archivePath string,
	now time.Time,
	window reviewWindow,
	review reviewResponse,
	focus reviewFocusContext,
	decision reviewDecision,
) error {
	if !decision.saves() {
		return nil
	}
	if err := ensureDir(archivePath); err != nil {
		return err
	}

	journal, err := openArchiveJournal(archivePath, now)
	if err != nil {
		return err
	}

	save := engine.ReviewSave{
		Period:        window.Period,
		Start:         window.startDate(),
		End:           window.endDate(),
		Title:         window.Title,
		Body:          renderReviewMarkdown(window, review, focus, decision),
		AcceptedFocus: decision.acceptedFocus(),
	}
	saved, err := journal.SaveReview(save)
	report := func() {
		reportSavedReview(out, window, saved.Review, focus, saved.AcceptedFocus, now)
		commitArchivePaths(fmt.Sprintf("seneca: %s review %s to %s",
			window.Period, window.startDate(), window.endDate()), archivePath)
	}
	// A committed failure means the review — and the focus it justified — are
	// already in the archive. Reporting and committing them is the only way a
	// rerun, which would append a revision of a review that already saved, is
	// avoided.
	if err != nil {
		if !engine.WriteCommitted(err) {
			// The harness run and the author's decision are not repeated for
			// free: keep the review's text even though the archive refused it.
			if kept, keepErr := journal.RetainReviewDraft(save); keepErr == nil {
				fmt.Fprintf(os.Stderr, "The review was not saved, but its text is kept in %s.\n", kept)
			}
		}
		return engine.OnCommittedWrite(err, report)
	}

	report()
	return nil
}

func reportSavedReview(
	out io.Writer,
	window reviewWindow,
	reviewRec engine.ArchiveRecord,
	focus reviewFocusContext,
	accepted string,
	now time.Time,
) {
	fmt.Fprintf(out, "Saved %s — %s to %s (id %s)\n",
		window.Title, window.startDate(), window.endDate(), engine.ShortRecordID(reviewRec.Review.ID))
	if revises := reviewRec.Review.Revises; revises != "" {
		fmt.Fprintf(out, "Recorded as a revision of %s\n", engine.ShortRecordID(revises))
	}

	switch {
	case accepted != "":
		fmt.Fprintf(out, "Focus set for %s:\n%s\n", now.Format("2006-01-02"), accepted)
	case focus.applies():
		fmt.Fprintf(out, "Focus unchanged (set %s):\n%s\n", focus.state.Effective, focus.state.Text)
	default:
		fmt.Fprintln(out, "Focus unchanged: none is in effect.")
	}
}
