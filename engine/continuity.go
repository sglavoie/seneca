package engine

import (
	"fmt"
	"sort"
	"strings"
)

// ContinuityContext is the previous material a daily reflection carries. It is
// display and snapshot state only: nothing in it is an answer, and nothing in
// it can make an otherwise empty reflection saveable.
type ContinuityContext struct {
	// focus is the focus active on the reflection date, empty when none is.
	Focus string
	// FocusDispute says, when two devices changed the focus for the same day
	// independently, what each of them set. Focus is then empty: nothing is
	// shown as the focus until the author settles it.
	FocusDispute string
	// priorDate and priorText are the intention the follow-through answers,
	// with the date it was actually written — not necessarily yesterday.
	PriorDate string
	PriorText string
	// priorAge says how long ago priorDate was, relative to the real today
	// rather than the entry date, so a backfill still reads truthfully. It is
	// display only and never stored.
	PriorAge string
	// showFollowThrough gates both the field and the persisted section. It is
	// false under --blank, which hides context without discarding it.
	ShowFollowThrough bool
	// followInitial is the response already stored on the edited entry.
	FollowInitial string
}

// hasPrior reports whether there is a prior intention worth showing.
func (c ContinuityContext) HasPrior() bool {
	return c.PriorDate != "" && c.PriorText != ""
}

// intentionLabel names the prior intention by the date it was written and,
// when known, how long ago that was: "Intention 2026-08-18 (3 days ago)".
func (c ContinuityContext) IntentionLabel() string {
	if c.PriorAge == "" {
		return "Intention " + c.PriorDate
	}
	return "Intention " + c.PriorDate + " (" + c.PriorAge + ")"
}

// hasContext reports whether anything is displayed above the form.
func (c ContinuityContext) HasContext() bool {
	return c.Focus != "" || c.FocusDispute != "" || c.HasPrior()
}

// ReflectionResult is what the author left behind, split back out by role.
type ReflectionResult struct {
	Saved         bool
	Answers       []string
	FollowThrough string
}

// empty reports whether the author wrote nothing at all. The source snapshot is
// deliberately not consulted: quoted context never makes an entry saveable.
func (r ReflectionResult) Empty() bool {
	if strings.TrimSpace(r.FollowThrough) != "" {
		return false
	}
	for _, a := range r.Answers {
		if a != "" {
			return false
		}
	}
	return true
}

// PriorIntention is the last intention the author actually wrote, together with
// the day they wrote it. The date travels with the text because it is rarely
// yesterday: skipped days, backfills, and edits all move it.
type PriorIntention struct {
	Date string
	Text string
}

func (p PriorIntention) Present() bool { return p.Text != "" }

// MostRecentAnchor returns the latest anchor answer strictly before `today`.
// It scans by date rather than by file position and skips entries whose anchor
// is empty, so a gap in the journal — or a day the author left the last
// question blank — reaches further back instead of returning nothing.
func MostRecentAnchor(entries []ReflectionEntry, today string, questions []string, anchorQuestionIndex int) PriorIntention {
	settings := ReflectionSettings{Questions: questions, AnchorQuestionIndex: anchorQuestionIndex}
	return mostRecentAnchor(entries, today, settings, settings)
}

// mostRecentAnchor separates the two things the settings decide. current says
// whether an anchor is carried forward at all. legacy says where the anchor of
// an entry with no role marker is: the questions and position in force when the
// journal's settings were first recorded, which later changes never move.
func mostRecentAnchor(entries []ReflectionEntry, today string, current, legacy ReflectionSettings) PriorIntention {
	if current.AnchorQuestionIndex == 0 {
		return PriorIntention{}
	}

	var best PriorIntention
	for _, entry := range entries {
		if entry.Date == "" || entry.Date >= today {
			continue
		}
		if best.Date != "" && entry.Date < best.Date {
			continue
		}
		text := entry.AnchorAnswer(legacy.Questions, legacy.AnchorQuestionIndex)
		if text == "" {
			continue
		}
		best = PriorIntention{Date: entry.Date, Text: text}
	}
	return best
}

// UnreadableContextError reports an archive that cannot be trusted to answer
// "what am I focused on". Daily writing must not silently lose context, and it
// must not be blocked outright either — hence the two ways out. Commands other
// than the daily form override purpose and remedy, because --blank is a daily
// flag and pointing at it from elsewhere would send the author to a dead end.
type UnreadableContextError struct {
	path        string
	Purpose     string
	Remedy      string
	diagnostics []ArchiveDiagnostic
}

func (e *UnreadableContextError) Error() string {
	details := make([]string, 0, len(e.diagnostics))
	for _, d := range e.diagnostics {
		details = append(details, d.String())
	}
	purpose := e.Purpose
	if purpose == "" {
		purpose = "today's context"
	}
	remedy := e.Remedy
	if remedy == "" {
		remedy = "run `seneca doctor` to inspect it, or rerun with --blank to write without previous context"
	}
	return fmt.Sprintf("review archive %s cannot be read for %s: %s; %s (nothing was written)",
		e.path, purpose, strings.Join(details, "; "), remedy)
}

// FocusOnDate resolves the focus in effect on a date. A missing archive is not
// an error: focus is optional and is never created by a read.
func FocusOnDate(archivePath, date string) (FocusState, error) {
	state, _, err := FocusAndTimelineOnDate(archivePath, date)
	return state, err
}

// FocusAndTimelineOnDate answers both questions a review asks of the focus
// stream — which focus was in force on a date, and how the focus moved — from a
// single read, so the verdict and the timeline can never disagree.
func FocusAndTimelineOnDate(archivePath, date string) (FocusState, []FocusEvent, error) {
	archive, exists, err := LoadReviewArchive(archivePath)
	if err != nil {
		return FocusState{}, nil, err
	}
	if !exists {
		return FocusState{}, nil, nil
	}
	if len(archive.Diagnostics) > 0 {
		return FocusState{}, nil, &UnreadableContextError{path: archivePath, diagnostics: archive.Diagnostics}
	}
	return ResolveFocus(archive, archivePath, date), archive.FocusEvents(), nil
}

// ContinuityContextFor assembles what the daily form shows above the questions.
func ContinuityContextFor(entries []ReflectionEntry, archivePath, date string, questions []string, anchorQuestionIndex int, blank bool) (ContinuityContext, error) {
	settings := ReflectionSettings{Questions: questions, AnchorQuestionIndex: anchorQuestionIndex}
	return continuityContext(entries, archivePath, date, settings, settings, blank)
}

func continuityContext(entries []ReflectionEntry, archivePath, date string, current, legacy ReflectionSettings, blank bool) (ContinuityContext, error) {
	if blank {
		// --blank bypasses archive loading entirely, so a malformed archive
		// never blocks writing. An existing follow-through section is hidden
		// by leaving it out of the form; nothing removes it.
		return ContinuityContext{}, nil
	}

	var ctx ContinuityContext
	focus, err := FocusOnDate(archivePath, date)
	if err != nil {
		return ContinuityContext{}, err
	}
	if focus.Active {
		ctx.Focus = focus.Text
	}
	if focus.Disputed {
		ctx.FocusDispute = focus.DisputeSummary()
	}

	if idx, found := FindEntry(entries, date); found {
		if stored, ok := entries[idx].FollowThroughSection(); ok {
			// An entry that already answers a day keeps answering that day:
			// the stored snapshot is history, not something to recompute from
			// a journal that has moved on since.
			ctx.PriorDate = stored.SourceDate
			ctx.PriorText = stored.SourceIntention
			ctx.FollowInitial = stored.Response
			ctx.ShowFollowThrough = true
			return ctx, nil
		}
	}

	if prior := mostRecentAnchor(entries, date, current, legacy); prior.Present() {
		ctx.PriorDate = prior.Date
		ctx.PriorText = prior.Text
		ctx.ShowFollowThrough = true
	}
	return ctx, nil
}

// followThroughToStore returns the section a contextual save persists. It is
// written even when the response is blank, so the day being answered stays on
// record; a blank response alone never makes the entry saveable.
func (c ContinuityContext) followThroughToStore(response string) (FollowThrough, bool) {
	if !c.ShowFollowThrough || c.PriorDate == "" {
		return FollowThrough{}, false
	}
	return FollowThrough{
		SourceDate:      c.PriorDate,
		SourceIntention: c.PriorText,
		Response:        response,
	}, true
}

// ReflectionSave is the modify half of the daily read-modify-write.
type ReflectionSave struct {
	Label               string
	Path                string
	Date                string
	Questions           []string
	AnchorQuestionIndex int
	Context             ContinuityContext
	Result              ReflectionResult
	// backfill re-sorts by date, since a dated entry may belong anywhere.
	Backfill bool
}

// ApplyReflectionResult folds the TUI result into the entry list and reports
// whether it edited an existing entry.
func ApplyReflectionResult(entries []ReflectionEntry, save ReflectionSave) ([]ReflectionEntry, bool, error) {
	entries = append([]ReflectionEntry(nil), entries...)
	follow, writeFollow := save.Context.followThroughToStore(save.Result.FollowThrough)

	if existingIdx, isEdit := FindEntry(entries, save.Date); isEdit {
		// Rewrite the answered sections in place; unknown headings and any
		// other historical content stay exactly where the author left them.
		edited := entries[existingIdx]
		if err := ValidateEntryRerender(save.Label, save.Path, edited); err != nil {
			return nil, false, err
		}
		edited.ApplyAnswers(save.Questions, save.Result.Answers, save.AnchorQuestionIndex)

		mode := edited.preferredRenderMode()
		if writeFollow {
			edited.SetFollowThrough(follow)
			// A follow-through is expressed only in markers, so the entry has
			// to be canonical v2 from here on.
			mode = RenderCanonicalV2
		}
		if mode == RenderCanonicalV2 {
			edited.Metadata.Present = true
			if edited.Metadata.Version == 0 {
				edited.Metadata.Version = ReflectionSchemaVersion
			}
		}
		edited.RebuildRaw(mode)
		entries[existingIdx] = edited
		return entries, true, nil
	}

	created := BuildReflectionEntry(save.Date, save.Questions, save.Result.Answers, save.AnchorQuestionIndex)
	if writeFollow {
		created.SetFollowThrough(follow)
	}
	// Every new entry is canonical v2. The role marker on the anchor section is
	// what keeps continuity working after the questions are reworded.
	created.upgradeToV2()
	entries = append(entries, created)
	if save.Backfill {
		// A backfill lands in date order; today's entry is already last.
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].Date < entries[j].Date
		})
	}
	return entries, false, nil
}

// FormatReflectionEntry renders a fresh entry in the legacy on-disk shape.
func FormatReflectionEntry(date string, questions []string, answers []string) string {
	return BuildReflectionEntry(date, questions, answers, 0).render(renderLegacy)
}
