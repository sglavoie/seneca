package senecabridge

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/seneca/engine"
)

// --- bridge.describe ---------------------------------------------------------

type bridgeDescription struct {
	BridgeVersion     int      `json:"bridge_version"`
	Operations        []string `json:"operations"`
	ReflectionSchema  int      `json:"reflection_schema"`
	ArchiveSchema     int      `json:"archive_schema"`
	SettingsVersion   int      `json:"settings_version"`
	SyncProtocol      int      `json:"sync_protocol"`
	ArchiveFileName   string   `json:"archive_file_name"`
	DefaultQuestions  []string `json:"default_questions"`
	ReviewPeriodNames []string `json:"review_periods"`
}

func describeBridge(c *call) (any, error) {
	if _, err := params[struct{}](c); err != nil {
		return nil, err
	}
	return bridgeDescription{
		BridgeVersion:     Version,
		Operations:        operationNames(),
		ReflectionSchema:  engine.ReflectionSchemaVersion,
		ArchiveSchema:     engine.ArchiveSchemaVersion,
		SettingsVersion:   engine.JournalSettingsVersion,
		SyncProtocol:      engine.SyncProtocolVersion,
		ArchiveFileName:   engine.ArchiveFileName,
		DefaultQuestions:  engine.DefaultReflectionQuestions(),
		ReviewPeriodNames: engine.ArchiveReviewPeriodNames(),
	}, nil
}

// --- journal.open, journal.status, journal.close -----------------------------

type openParams struct {
	// ReflectionsPath is the reflections file, absolute. It need not exist.
	ReflectionsPath string `json:"reflections_path"`
	// StateDir is the installation's own directory for the request ledger.
	StateDir string `json:"state_dir"`
	// Questions and AnchorQuestionIndex are this device's settings: in force
	// until the journal has its own. Left out, they are Seneca's defaults.
	Questions           []string `json:"questions,omitempty"`
	AnchorQuestionIndex *int     `json:"anchor_question_index,omitempty"`
	// TimeZone is the IANA zone the journal's days are counted in. Left out,
	// it is UTC.
	TimeZone string `json:"time_zone,omitempty"`
	// Now pins the journal's clock to one RFC 3339 instant, for tests and
	// previews. The app leaves it out.
	Now string `json:"now,omitempty"`
}

type journalStatusResult struct {
	ReflectionsPath   string `json:"reflections_path"`
	ArchivePath       string `json:"archive_path"`
	Today             string `json:"today"`
	ReflectionsExist  bool   `json:"reflections_exist"`
	ArchiveExists     bool   `json:"archive_exists"`
	EntryCount        int    `json:"entry_count"`
	ReviewCount       int    `json:"review_count"`
	Writable          bool   `json:"writable"`
	ReflectionsReason string `json:"reflections_reason,omitempty"`
	ArchiveReason     string `json:"archive_reason,omitempty"`
	// ReviewDue is the note the daily command prints when entries have piled
	// up past the newest saved review.
	ReviewDue    string        `json:"review_due,omitempty"`
	Settings     settingsState `json:"settings"`
	SyncEnrolled bool          `json:"sync_enrolled"`
}

func openJournalOp(c *call) (any, error) {
	p, err := params[openParams](c)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(p.ReflectionsPath) {
		return nil, refuse("invalid-params", "reflections_path must be an absolute path")
	}
	if !filepath.IsAbs(p.StateDir) {
		return nil, refuse("invalid-params", "state_dir must be an absolute path")
	}
	settings, err := engine.ResolveReflectionSettings(p.Questions, p.AnchorQuestionIndex)
	if err != nil {
		return nil, refuse("invalid-settings", "%v", err)
	}
	opened := &openJournal{
		reflectionsPath: filepath.Clean(p.ReflectionsPath),
		stateDir:        filepath.Clean(p.StateDir),
		settings:        settings,
		location:        time.UTC,
	}
	if p.TimeZone != "" {
		location, err := time.LoadLocation(p.TimeZone)
		if err != nil {
			return nil, refuse("invalid-params", "time_zone %q is not a known zone", p.TimeZone)
		}
		opened.location = location
	}
	if p.Now != "" {
		fixed, err := time.Parse(time.RFC3339, p.Now)
		if err != nil {
			return nil, refuse("invalid-params", "now %q is not an RFC 3339 instant", p.Now)
		}
		if p.TimeZone != "" {
			fixed = fixed.In(opened.location)
		}
		opened.fixed = &fixed
	}
	journal, err := opened.journal(c.bridge)
	if err != nil {
		return nil, err
	}
	status, err := statusOf(journal)
	if err != nil {
		return nil, err
	}
	// The journal is open only once it has been read.
	c.bridge.open = opened
	return status, nil
}

func journalStatus(c *call) (any, error) {
	if _, err := params[struct{}](c); err != nil {
		return nil, err
	}
	return statusOf(c.journal)
}

func statusOf(journal *engine.Journal) (journalStatusResult, error) {
	validation, err := journal.Validate()
	if err != nil {
		return journalStatusResult{}, err
	}
	state, err := journal.EffectiveSettings()
	if err != nil {
		return journalStatusResult{}, err
	}
	status := journalStatusResult{
		ReflectionsPath:  journal.ReflectionsPath(),
		ArchivePath:      journal.ArchivePath(),
		Today:            journal.Today(),
		ReflectionsExist: validation.ReflectionsExist,
		ArchiveExists:    validation.ArchiveExists,
		EntryCount:       len(validation.Reflections.Entries),
		ReviewCount:      len(validation.Archive.ReviewRecords()),
		Writable:         validation.Writable(),
		Settings:         settingsStateOf(state),
	}
	if validation.ReflectionsReason != nil {
		status.ReflectionsReason = validation.ReflectionsReason.Error()
	}
	if validation.ArchiveReason != nil {
		status.ArchiveReason = validation.ArchiveReason.Error()
	}
	if note, due := journal.ReviewDueNote(validation.Reflections.Entries); due {
		status.ReviewDue = note
	}
	if _, enrolled, err := engine.LoadSyncStatus(journal.ReflectionsPath()); err == nil {
		status.SyncEnrolled = enrolled
	}
	return status, nil
}

type closedResult struct {
	Closed bool `json:"closed"`
}

func closeJournal(c *call) (any, error) {
	if _, err := params[struct{}](c); err != nil {
		return nil, err
	}
	was := c.bridge.open != nil
	c.bridge.open = nil
	return closedResult{Closed: was}, nil
}

// --- entries -----------------------------------------------------------------

type entrySummary struct {
	Date string `json:"date"`
	// Revision identifies the entry's bytes; a reflection for this day is
	// prepared and saved against it.
	Revision string `json:"revision"`
	// Relative places the day against the journal's today: "yesterday",
	// "24 days ago".
	Relative string `json:"relative"`
	// Preview is the first line the author wrote that day.
	Preview string `json:"preview"`
	// Conflicted reports that two devices wrote the day differently and the
	// author has not chosen yet. This device's version is the one listed.
	Conflicted bool `json:"conflicted"`
}

// entryQuery narrows the entries as `seneca show` does: one month, a range of
// days, or the earlier days written on today's day of a month.
type entryQuery struct {
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Month     string `json:"month,omitempty"`
	OnThisDay bool   `json:"on_this_day,omitempty"`
}

func (q entryQuery) engine() (engine.EntryQuery, error) {
	query := engine.EntryQuery{From: q.From, To: q.To, Month: q.Month, OnThisDay: q.OnThisDay}
	if err := query.Validate(); err != nil {
		return query, refuse("invalid-query", "%v", err)
	}
	return query, nil
}

type entriesResult struct {
	Today string `json:"today"`
	// Total counts every entry of the journal, whatever the query kept.
	Total   int            `json:"total"`
	Entries []entrySummary `json:"entries"`
	// NearestBefore and NearestAfter are the closest days written outside a
	// month or range the query names, so an empty answer can say where the
	// writing is.
	NearestBefore string `json:"nearest_before,omitempty"`
	NearestAfter  string `json:"nearest_after,omitempty"`
}

const previewRunes = 120

func listEntries(c *call) (any, error) {
	p, err := params[entryQuery](c)
	if err != nil {
		return nil, err
	}
	query, err := p.engine()
	if err != nil {
		return nil, err
	}
	entries, err := c.journal.Entries()
	if err != nil {
		return nil, err
	}
	today := c.journal.Today()
	conflicted := conflictedDays(c.journal)
	kept := engine.FilterEntries(entries, query, today)
	result := entriesResult{Today: today, Total: len(entries), Entries: make([]entrySummary, 0, len(kept))}
	for _, entry := range kept {
		result.Entries = append(result.Entries, entrySummary{
			Date:       entry.Date,
			Revision:   string(engine.RevisionOf([]byte(entry.Raw))),
			Relative:   engine.RelativeDay(entry.Date, today),
			Preview:    previewOf(entry),
			Conflicted: conflicted[entry.Date],
		})
	}
	if from, to, ok := query.Span(); ok {
		result.NearestBefore, result.NearestAfter = engine.NearestEntries(entries, from, to)
	}
	return result, nil
}

// conflictedDays names the days whose entry waits for the author to choose
// between two devices' versions. It only reads the sync state; a journal that
// does not sync, or whose state cannot be read, has none.
func conflictedDays(journal *engine.Journal) map[string]bool {
	days := map[string]bool{}
	status, enrolled, err := engine.LoadSyncStatus(journal.ReflectionsPath())
	if err != nil || !enrolled {
		return days
	}
	for _, artifact := range status.Conflicts {
		if date, ok := strings.CutPrefix(artifact, "entry:"); ok {
			days[date] = true
		}
	}
	return days
}

func previewOf(entry engine.ReflectionEntry) string {
	for _, section := range entry.Sections {
		for _, line := range strings.Split(section.Body(), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, ">") {
				continue
			}
			if utf8.RuneCountInString(line) > previewRunes {
				return string([]rune(line)[:previewRunes]) + "…"
			}
			return line
		}
	}
	return ""
}

type followThrough struct {
	SourceDate      string `json:"source_date"`
	SourceIntention string `json:"source_intention"`
	Response        string `json:"response"`
}

type entrySection struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
	// FollowThrough is set on the section that answers an earlier intention.
	FollowThrough *followThrough `json:"follow_through,omitempty"`
}

type entryResult struct {
	Date     string `json:"date"`
	Revision string `json:"revision"`
	Relative string `json:"relative"`
	// Raw is the entry exactly as the file holds it, as `seneca show` pipes it.
	Raw string `json:"raw"`
	// Lines is the entry as `seneca show` displays it on a terminal: Raw
	// without Seneca's own markers.
	Lines    []string       `json:"lines"`
	Sections []entrySection `json:"sections"`
	// Conflicted reports that another device's version of the day waits
	// beside this one for the author to choose.
	Conflicted bool `json:"conflicted"`
}

type dateParams struct {
	Date string `json:"date"`
}

func getEntry(c *call) (any, error) {
	p, err := params[dateParams](c)
	if err != nil {
		return nil, err
	}
	if !engine.IsCalendarDate(p.Date) {
		return nil, refuse("invalid-date", "%q is not a calendar date (YYYY-MM-DD)", p.Date)
	}
	entries, err := c.journal.Entries()
	if err != nil {
		return nil, err
	}
	index, found := engine.FindEntry(entries, p.Date)
	if !found {
		return nil, refuse("not-found", "the journal has no entry for %s", p.Date)
	}
	result := entryResultOf(entries[index], c.journal.Today())
	result.Conflicted = conflictedDays(c.journal)[p.Date]
	return result, nil
}

func entryResultOf(entry engine.ReflectionEntry, today string) entryResult {
	result := entryResult{
		Date:     entry.Date,
		Revision: string(engine.RevisionOf([]byte(entry.Raw))),
		Relative: engine.RelativeDay(entry.Date, today),
		Raw:      entry.Raw,
		Lines:    engine.VisibleEntryLines(entry.Raw),
		Sections: make([]entrySection, 0, len(entry.Sections)),
	}
	for _, section := range entry.Sections {
		view := entrySection{Heading: section.Heading, Body: section.Body()}
		if section.Follow != nil {
			view.FollowThrough = &followThrough{
				SourceDate:      section.Follow.SourceDate,
				SourceIntention: section.Follow.SourceIntention,
				Response:        section.Follow.Response,
			}
		}
		result.Sections = append(result.Sections, view)
	}
	return result
}

type searchParams struct {
	Phrase string `json:"phrase"`
	// The search covers what the query keeps, as `seneca show --search` with
	// --month or --from/--to.
	entryQuery
}

type searchHit struct {
	Date     string   `json:"date"`
	Relative string   `json:"relative"`
	Snippets []string `json:"snippets"`
}

type searchResult struct {
	Hits []searchHit `json:"hits"`
}

func searchEntries(c *call) (any, error) {
	p, err := params[searchParams](c)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Phrase) == "" {
		return nil, refuse("invalid-params", "phrase is blank: there is nothing to search for")
	}
	query, err := p.entryQuery.engine()
	if err != nil {
		return nil, err
	}
	entries, err := c.journal.Entries()
	if err != nil {
		return nil, err
	}
	today := c.journal.Today()
	result := searchResult{Hits: []searchHit{}}
	for _, hit := range engine.SearchEntries(engine.FilterEntries(entries, query, today), engine.SearchPattern(p.Phrase)) {
		result.Hits = append(result.Hits, searchHit{
			Date: hit.Date, Relative: engine.RelativeDay(hit.Date, today), Snippets: hit.Snippets,
		})
	}
	return result, nil
}

// --- reflections -------------------------------------------------------------

type reflectionContext struct {
	Focus             string `json:"focus"`
	FocusDispute      string `json:"focus_dispute"`
	PriorDate         string `json:"prior_date"`
	PriorText         string `json:"prior_text"`
	PriorAge          string `json:"prior_age"`
	ShowFollowThrough bool   `json:"show_follow_through"`
	FollowInitial     string `json:"follow_initial"`
}

// reflectionDraft is the read half of a reflection. The app shows it and
// hands it back unchanged with what the author wrote: it is the state the
// save is conditional on.
type reflectionDraft struct {
	Date                string            `json:"date"`
	Questions           []string          `json:"questions"`
	AnchorQuestionIndex int               `json:"anchor_question_index"`
	Initial             []string          `json:"initial"`
	IsEdit              bool              `json:"is_edit"`
	Context             reflectionContext `json:"context"`
	Base                string            `json:"base"`
	SettingsRevision    string            `json:"settings_revision"`
}

type prepareParams struct {
	// Date is the day reflected on. Left out, it is the journal's today.
	Date string `json:"date,omitempty"`
	// Blank hides the focus and the intention being followed through.
	Blank bool `json:"blank,omitempty"`
}

type prepareResult struct {
	Draft reflectionDraft `json:"draft"`
	Today string          `json:"today"`
	// IntentionLabel names the prior intention as the daily form does:
	// "Intention 2026-08-18 (3 days ago)". Empty when there is none.
	IntentionLabel string `json:"intention_label"`
	// Retained is writing kept for this day that a save could not store.
	Retained *retainedDraft `json:"retained,omitempty"`
}

func prepareReflection(c *call) (any, error) {
	p, err := params[prepareParams](c)
	if err != nil {
		return nil, err
	}
	date := p.Date
	if date == "" {
		date = c.journal.Today()
	}
	if !engine.IsCalendarDate(date) {
		return nil, refuse("invalid-date", "%q is not a calendar date (YYYY-MM-DD)", date)
	}
	draft, err := c.journal.PrepareReflection(date, p.Blank)
	if err != nil {
		return nil, err
	}
	result := prepareResult{Draft: draftView(draft), Today: c.journal.Today()}
	if draft.Context.HasPrior() {
		result.IntentionLabel = draft.Context.IntentionLabel()
	}
	kept, found, err := c.journal.RetainedDraft(date)
	if err != nil {
		return nil, err
	}
	if found {
		view := retainedView(kept, draft.Questions)
		result.Retained = &view
	}
	return result, nil
}

func draftView(draft engine.ReflectionDraft) reflectionDraft {
	return reflectionDraft{
		Date:                draft.Date,
		Questions:           draft.Questions,
		AnchorQuestionIndex: draft.AnchorQuestionIndex,
		Initial:             draft.Initial,
		IsEdit:              draft.IsEdit,
		Context: reflectionContext{
			Focus:             draft.Context.Focus,
			FocusDispute:      draft.Context.FocusDispute,
			PriorDate:         draft.Context.PriorDate,
			PriorText:         draft.Context.PriorText,
			PriorAge:          draft.Context.PriorAge,
			ShowFollowThrough: draft.Context.ShowFollowThrough,
			FollowInitial:     draft.Context.FollowInitial,
		},
		Base:             string(draft.Base),
		SettingsRevision: string(draft.SettingsRevision),
	}
}

func (d reflectionDraft) engine() engine.ReflectionDraft {
	return engine.ReflectionDraft{
		Date:                d.Date,
		Questions:           d.Questions,
		AnchorQuestionIndex: d.AnchorQuestionIndex,
		Initial:             d.Initial,
		IsEdit:              d.IsEdit,
		Context: engine.ContinuityContext{
			Focus:             d.Context.Focus,
			FocusDispute:      d.Context.FocusDispute,
			PriorDate:         d.Context.PriorDate,
			PriorText:         d.Context.PriorText,
			PriorAge:          d.Context.PriorAge,
			ShowFollowThrough: d.Context.ShowFollowThrough,
			FollowInitial:     d.Context.FollowInitial,
		},
		Base:             engine.Revision(d.Base),
		SettingsRevision: engine.Revision(d.SettingsRevision),
	}
}

type writing struct {
	// Draft is the draft reflection.prepare returned, unchanged.
	Draft reflectionDraft `json:"draft"`
	// Answers holds one answer per question of the draft, in order.
	Answers       []string `json:"answers"`
	FollowThrough string   `json:"follow_through,omitempty"`
}

func (w writing) check() error {
	if !engine.IsCalendarDate(w.Draft.Date) {
		return refuse("invalid-date", "%q is not a calendar date (YYYY-MM-DD)", w.Draft.Date)
	}
	if len(w.Answers) != len(w.Draft.Questions) {
		return refuse("invalid-params", "the draft asks %d questions and %d answers were sent", len(w.Draft.Questions), len(w.Answers))
	}
	return nil
}

func (w writing) result() engine.ReflectionResult {
	return engine.ReflectionResult{Saved: true, Answers: w.Answers, FollowThrough: w.FollowThrough}
}

type savedReflection struct {
	Date   string `json:"date"`
	IsEdit bool   `json:"is_edit"`
	// Entry is the day as the journal now holds it.
	Entry entryResult `json:"entry"`
}

func saveReflection(c *call) (any, error) {
	p, err := params[writing](c)
	if err != nil {
		return nil, err
	}
	if err := p.check(); err != nil {
		return nil, err
	}
	draft := p.Draft.engine()
	// A day two devices wrote differently is settled first; what the author
	// writes meanwhile stays a draft on the device.
	if status, enrolled, err := c.journal.SyncStatus(); err == nil && enrolled &&
		slices.Contains(status.Conflicts, "entry:"+draft.Date) {
		return nil, &problem{
			Code:    "conflicted",
			Message: fmt.Sprintf("%s was written differently on two devices; choose a version before saving more writing for it. Nothing was written.", draft.Date),
			Details: map[string]string{"date": draft.Date, "artifact": "entry:" + draft.Date},
		}
	}
	// A day other than today may belong anywhere in the file, as it does for
	// `seneca --date`.
	saved, err := c.journal.SaveReflection(draft, p.result(), draft.Date != c.journal.Today())
	if err != nil && !engine.WriteCommitted(err) {
		return nil, err
	}
	result := savedReflection{Date: saved.Date, IsEdit: saved.IsEdit}
	if index, found := engine.FindEntry(saved.Entries, saved.Date); found {
		result.Entry = entryResultOf(saved.Entries[index], c.journal.Today())
	}
	// What was kept for the day is saved now.
	_ = c.journal.DiscardDraft(saved.Date)
	return result, err
}

type deleteEntryParams struct {
	Date string `json:"date"`
	Base string `json:"base"`
}

type deletedEntry struct {
	Date string `json:"date"`
}

func deleteEntry(c *call) (any, error) {
	p, err := params[deleteEntryParams](c)
	if err != nil {
		return nil, err
	}
	if !engine.IsCalendarDate(p.Date) {
		return nil, refuse("invalid-date", "%q is not a calendar date (YYYY-MM-DD)", p.Date)
	}
	if p.Base == "" || p.Base == string(engine.RevisionAbsent) {
		return nil, refuse("missing-base", "entry.delete must name an existing entry's revision")
	}
	err = c.journal.DeleteReflection(p.Date, engine.Revision(p.Base))
	if err != nil && !engine.WriteCommitted(err) {
		return nil, err
	}
	return deletedEntry{Date: p.Date}, err
}

// --- drafts ------------------------------------------------------------------

type retainedDraft struct {
	Date             string `json:"date"`
	RetainedAt       string `json:"retained_at"`
	Reason           string `json:"reason"`
	Base             string `json:"base"`
	SettingsRevision string `json:"settings_revision"`
	// Answers are lined up with the questions now asked, by their text.
	Answers       []string `json:"answers"`
	FollowThrough string   `json:"follow_through"`
	// Unasked are the kept answers to questions the journal no longer asks.
	Unasked []unaskedAnswer `json:"unasked"`
}

type unaskedAnswer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

func retainedView(kept engine.RetainedDraft, questions []string) retainedDraft {
	view := retainedDraft{
		Date:             kept.Date,
		RetainedAt:       kept.RetainedAt,
		Reason:           kept.Reason,
		Base:             string(kept.Base),
		SettingsRevision: string(kept.SettingsRevision),
		Answers:          kept.AnswersFor(questions),
		FollowThrough:    kept.FollowThrough,
		Unasked:          []unaskedAnswer{},
	}
	for _, unasked := range kept.Unasked(questions) {
		view.Unasked = append(view.Unasked, unaskedAnswer{Question: unasked.Question, Answer: unasked.Answer})
	}
	return view
}

type draftResult struct {
	Found bool           `json:"found"`
	Draft *retainedDraft `json:"draft,omitempty"`
}

func getDraft(c *call) (any, error) {
	p, err := params[dateParams](c)
	if err != nil {
		return nil, err
	}
	if !engine.IsCalendarDate(p.Date) {
		return nil, refuse("invalid-date", "%q is not a calendar date (YYYY-MM-DD)", p.Date)
	}
	kept, found, err := c.journal.RetainedDraft(p.Date)
	if err != nil {
		return nil, err
	}
	if !found {
		return draftResult{}, nil
	}
	state, err := c.journal.EffectiveSettings()
	if err != nil {
		return nil, err
	}
	view := retainedView(kept, state.Settings.Questions)
	return draftResult{Found: true, Draft: &view}, nil
}

type retainParams struct {
	Draft         reflectionDraft `json:"draft"`
	Answers       []string        `json:"answers"`
	FollowThrough string          `json:"follow_through,omitempty"`
	// Reason is why the writing is being kept instead of saved.
	Reason string `json:"reason"`
}

type retainResult struct {
	Date string `json:"date"`
}

func retainDraft(c *call) (any, error) {
	p, err := params[retainParams](c)
	if err != nil {
		return nil, err
	}
	kept := writing{Draft: p.Draft, Answers: p.Answers, FollowThrough: p.FollowThrough}
	if err := kept.check(); err != nil {
		return nil, err
	}
	if _, err := c.journal.RetainDraft(p.Draft.engine(), kept.result(), p.Reason); err != nil {
		return nil, err
	}
	return retainResult{Date: p.Draft.Date}, nil
}

func discardDraft(c *call) (any, error) {
	p, err := params[dateParams](c)
	if err != nil {
		return nil, err
	}
	if !engine.IsCalendarDate(p.Date) {
		return nil, refuse("invalid-date", "%q is not a calendar date (YYYY-MM-DD)", p.Date)
	}
	if err := c.journal.DiscardDraft(p.Date); err != nil {
		return nil, err
	}
	return retainResult{Date: p.Date}, nil
}

// --- settings ----------------------------------------------------------------

type settingsState struct {
	// Initialized reports whether the journal has settings of its own. Until
	// it does, these are the device's.
	Initialized         bool     `json:"initialized"`
	Questions           []string `json:"questions"`
	AnchorQuestionIndex int      `json:"anchor_question_index"`
	Revision            string   `json:"revision"`
	// Practice is the pack applied last, absent when none ever was.
	Practice *practiceState `json:"practice,omitempty"`
}

type practiceState struct {
	// From is the journal day the pack was applied on.
	From string              `json:"from"`
	Pack engine.PracticePack `json:"pack"`
	// QuestionsChanged reports questions or an anchor edited after the pack
	// was applied; the pack is still the practice.
	QuestionsChanged bool `json:"questions_changed"`
}

func settingsStateOf(state engine.SettingsState) settingsState {
	view := settingsState{
		Initialized:         state.Initialized,
		Questions:           state.Settings.Questions,
		AnchorQuestionIndex: state.Settings.AnchorQuestionIndex,
		Revision:            string(state.Revision),
	}
	if current, ok := state.CurrentPractice(); ok {
		view.Practice = &practiceState{
			From:             current.From,
			Pack:             current.Pack,
			QuestionsChanged: engine.SettingsRevisionOf(current.Pack.Settings()) != state.Revision,
		}
	}
	return view
}

func getSettings(c *call) (any, error) {
	if _, err := params[struct{}](c); err != nil {
		return nil, err
	}
	state, err := c.journal.EffectiveSettings()
	if err != nil {
		return nil, err
	}
	return settingsStateOf(state), nil
}

type settingsOutcome struct {
	Changed  bool          `json:"changed"`
	Settings settingsState `json:"settings"`
}

func initializeSettings(c *call) (any, error) {
	if _, err := params[struct{}](c); err != nil {
		return nil, err
	}
	outcome, err := c.journal.InitializeSettings()
	if err != nil && !engine.WriteCommitted(err) {
		return nil, err
	}
	return settingsOutcome{Changed: outcome.Changed, Settings: settingsStateOf(outcome.State)}, err
}

type updateSettingsParams struct {
	// Base is the revision the change was decided on.
	Base string `json:"base"`
	// Questions and AnchorQuestionIndex each keep what the journal has when
	// left out.
	Questions           []string `json:"questions,omitempty"`
	AnchorQuestionIndex *int     `json:"anchor_question_index,omitempty"`
}

func updateSettings(c *call) (any, error) {
	p, err := params[updateSettingsParams](c)
	if err != nil {
		return nil, err
	}
	if p.Base == "" {
		return nil, refuse("missing-base", "settings.update must name the revision it was decided on")
	}
	outcome, err := c.journal.UpdateSettings(engine.Revision(p.Base), engine.SettingsChange{
		Questions: p.Questions, AnchorQuestionIndex: p.AnchorQuestionIndex,
	})
	if err != nil && !engine.WriteCommitted(err) {
		return nil, err
	}
	return settingsOutcome{Changed: outcome.Changed, Settings: settingsStateOf(outcome.State)}, err
}

// --- packs.list, packs.read, settings.apply_pack -----------------------------

type packList struct {
	Packs []engine.PracticePack `json:"packs"`
}

// listPacks returns the built-in packs, the default practice first. It needs
// no open journal.
func listPacks(c *call) (any, error) {
	if _, err := params[struct{}](c); err != nil {
		return nil, err
	}
	return packList{Packs: engine.BuiltinPracticePacks()}, nil
}

type readPackParams struct {
	// Data is the contents of a *.seneca-pack.json file, as text.
	Data string `json:"data"`
}

type readPackResult struct {
	Pack engine.PracticePack `json:"pack"`
}

// readPack checks a pack file the app was handed, from a link, AirDrop, or a
// practitioner, and returns it as the engine reads it. Nothing is written.
func readPack(c *call) (any, error) {
	p, err := params[readPackParams](c)
	if err != nil {
		return nil, err
	}
	pack, err := engine.ParsePracticePack([]byte(p.Data))
	if err != nil {
		return nil, err
	}
	return readPackResult{Pack: pack}, nil
}

type applyPackParams struct {
	// Base is the settings revision the pack was chosen against.
	Base string              `json:"base"`
	Pack engine.PracticePack `json:"pack"`
}

func applyPack(c *call) (any, error) {
	p, err := params[applyPackParams](c)
	if err != nil {
		return nil, err
	}
	if p.Base == "" {
		return nil, refuse("missing-base", "settings.apply_pack must name the revision it was decided on")
	}
	outcome, err := c.journal.ApplyPracticePack(engine.Revision(p.Base), p.Pack)
	if err != nil && !engine.WriteCommitted(err) {
		return nil, err
	}
	return settingsOutcome{Changed: outcome.Changed, Settings: settingsStateOf(outcome.State)}, err
}

// --- reviews -----------------------------------------------------------------

type reviewSummary struct {
	ID        string `json:"id"`
	ShortID   string `json:"short_id"`
	CreatedAt string `json:"created_at"`
	Period    string `json:"period"`
	Start     string `json:"start"`
	End       string `json:"end"`
	// Revises names the review this one supersedes, empty for a first save.
	Revises string `json:"revises"`
	// Superseded reports whether a later review revises this one.
	Superseded bool `json:"superseded"`
	// RevisedBy names the reviews that revise this one, oldest first.
	RevisedBy []string `json:"revised_by"`
	Title     string   `json:"title"`
}

type reviewsParams struct {
	// Period keeps the reviews of one period: weekly, monthly, quarterly, or
	// yearly. Left out, every review is listed.
	Period string `json:"period,omitempty"`
}

type reviewsResult struct {
	Reviews []reviewSummary `json:"reviews"`
}

func listReviews(c *call) (any, error) {
	p, err := params[reviewsParams](c)
	if err != nil {
		return nil, err
	}
	if p.Period != "" && !engine.IsArchiveReviewPeriod(p.Period) {
		return nil, refuse("invalid-query", "%q is not a review period: expected one of %s",
			p.Period, strings.Join(engine.ArchiveReviewPeriodNames(), ", "))
	}
	reviews, err := c.journal.Reviews()
	if err != nil {
		return nil, err
	}
	revisedBy := engine.ReviewRevisions(reviews)
	kept := engine.ReviewRecordsOfPeriod(reviews, p.Period)
	result := reviewsResult{Reviews: make([]reviewSummary, 0, len(kept))}
	for _, review := range kept {
		result.Reviews = append(result.Reviews, reviewSummaryOf(review, revisedBy))
	}
	return result, nil
}

func reviewSummaryOf(review engine.ReviewRecord, revisedBy map[string][]string) reviewSummary {
	summary := reviewSummary{
		ID:        review.ID,
		ShortID:   engine.ShortRecordID(review.ID),
		CreatedAt: review.CreatedAt,
		Period:    string(review.Period),
		Start:     review.Start,
		End:       review.End,
		Revises:   review.Revises,
		RevisedBy: append([]string{}, revisedBy[review.ID]...),
		Title:     review.Title,
	}
	summary.Superseded = len(summary.RevisedBy) > 0
	return summary
}

type reviewParams struct {
	ID string `json:"id"`
}

type reviewResult struct {
	reviewSummary
	// Body is the review's Markdown, as saved.
	Body string `json:"body"`
}

func getReview(c *call) (any, error) {
	p, err := params[reviewParams](c)
	if err != nil {
		return nil, err
	}
	reviews, err := c.journal.Reviews()
	if err != nil {
		return nil, err
	}
	for _, review := range reviews {
		if review.ID == p.ID {
			return reviewResult{reviewSummary: reviewSummaryOf(review, engine.ReviewRevisions(reviews)), Body: review.Body}, nil
		}
	}
	return nil, refuse("not-found", "the archive has no review %s", p.ID)
}

// --- focus -------------------------------------------------------------------

type focusCandidate struct {
	Record string `json:"record"`
	Event  string `json:"event"`
	Text   string `json:"text"`
}

type focusState struct {
	Active    bool   `json:"active"`
	Text      string `json:"text"`
	Effective string `json:"effective"`
	// Relative places Effective against the journal's today.
	Relative string `json:"relative"`
	Origin   string `json:"origin"`
	Source   string `json:"source"`
	// Disputed is set when two devices changed the focus for one day
	// independently. There is then no text: the candidates wait to be settled.
	Disputed   bool             `json:"disputed"`
	Candidates []focusCandidate `json:"candidates"`
}

type focusEvent struct {
	ID        string `json:"id"`
	Event     string `json:"event"`
	Effective string `json:"effective"`
	Origin    string `json:"origin"`
	Source    string `json:"source"`
	Text      string `json:"text"`
}

type focusResult struct {
	Date     string       `json:"date"`
	Focus    focusState   `json:"focus"`
	Timeline []focusEvent `json:"timeline"`
	// Revision identifies the focus timeline; focus.set and focus.clear are
	// conditional on it.
	Revision string `json:"revision"`
}

type focusParams struct {
	// Date is the day asked about. Left out, it is the journal's today.
	Date string `json:"date,omitempty"`
}

func focusStateOf(state engine.FocusState, today string) focusState {
	view := focusState{
		Active:     state.Active,
		Text:       state.Text,
		Effective:  state.Effective,
		Relative:   engine.RelativeDay(state.Effective, today),
		Origin:     string(state.Origin),
		Source:     state.Source,
		Disputed:   state.Disputed,
		Candidates: focusCandidatesOf(state.Candidates),
	}
	return view
}

func focusCandidatesOf(candidates []engine.FocusCandidate) []focusCandidate {
	views := make([]focusCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		views = append(views, focusCandidate{Record: candidate.Record, Event: string(candidate.Event), Text: candidate.Text})
	}
	return views
}

// focusRevision identifies a focus timeline by the changes in it.
func focusRevision(events []engine.FocusEvent) string {
	if len(events) == 0 {
		return string(engine.RevisionAbsent)
	}
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	return string(engine.RevisionOf([]byte(strings.Join(ids, "\n"))))
}

func readFocus(c *call, date string) (focusResult, error) {
	state, events, err := c.journal.Focus(date)
	if err != nil {
		return focusResult{}, err
	}
	result := focusResult{
		Date:     date,
		Focus:    focusStateOf(state, c.journal.Today()),
		Timeline: make([]focusEvent, 0, len(events)),
		Revision: focusRevision(events),
	}
	for _, event := range events {
		result.Timeline = append(result.Timeline, focusEvent{
			ID: event.ID, Event: string(event.Event), Effective: event.Effective,
			Origin: string(event.Origin), Source: event.Source, Text: event.Text,
		})
	}
	return result, nil
}

func getFocus(c *call) (any, error) {
	p, err := params[focusParams](c)
	if err != nil {
		return nil, err
	}
	date := p.Date
	if date == "" {
		date = c.journal.Today()
	}
	if !engine.IsCalendarDate(date) {
		return nil, refuse("invalid-date", "%q is not a calendar date (YYYY-MM-DD)", date)
	}
	return readFocus(c, date)
}

type setFocusParams struct {
	// Base is the focus revision the change was decided on.
	Base string `json:"base"`
	Text string `json:"text"`
}

type clearFocusParams struct {
	Base string `json:"base"`
}

type focusChange struct {
	// Changed is false when a clear found no focus to clear.
	Changed   bool   `json:"changed"`
	Effective string `json:"effective"`
	// Text is the focus now in effect, empty after a clear.
	Text string `json:"text"`
	// Previous is the focus a clear replaced.
	Previous string `json:"previous"`
	// Revision is the focus timeline's revision afterwards.
	Revision string `json:"revision"`
}

// requireFocusBase refuses a focus change decided on a timeline that has
// since gained a change: another device's, or a review's on the desktop.
func requireFocusBase(c *call, base string) error {
	if base == "" {
		return refuse("missing-base", "a focus change must name the focus revision it was decided on")
	}
	current, err := readFocus(c, c.journal.Today())
	if err != nil {
		return err
	}
	if current.Revision != base {
		return &problem{
			Code:    "stale-focus",
			Message: "the focus changed after it was read; nothing was written",
			Details: map[string]string{"base": base, "current": current.Revision},
		}
	}
	return nil
}

func focusChangeOf(c *call, change engine.FocusChange, err error) (any, error) {
	if err != nil && !engine.WriteCommitted(err) {
		return nil, err
	}
	view := focusChange{
		Changed:   change.Changed,
		Effective: change.Effective,
		Text:      change.Text,
		Previous:  change.Previous.Text,
	}
	if after, readErr := readFocus(c, c.journal.Today()); readErr == nil {
		view.Revision = after.Revision
	}
	return view, err
}

func setFocus(c *call) (any, error) {
	p, err := params[setFocusParams](c)
	if err != nil {
		return nil, err
	}
	if err := requireFocusBase(c, p.Base); err != nil {
		return nil, err
	}
	change, err := c.journal.SetFocus(p.Text)
	return focusChangeOf(c, change, err)
}

func clearFocus(c *call) (any, error) {
	p, err := params[clearFocusParams](c)
	if err != nil {
		return nil, err
	}
	if err := requireFocusBase(c, p.Base); err != nil {
		return nil, err
	}
	change, err := c.journal.ClearFocus()
	return focusChangeOf(c, change, err)
}
