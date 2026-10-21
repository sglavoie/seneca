package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Retained drafts are writing that did not reach the journal: a reflection
// whose save failed, was refused, or went stale because its day changed while
// the author wrote, and a review whose archive write failed. They are kept so
// that a failed save never costs the writing.
//
// A draft is device-local. It lives under the journal's `.seneca/` directory,
// is never part of a commit, is never exchanged with another device, and is
// removed when the writing it holds is saved. It is written without the
// journal lock — the lock may be the very thing that failed — which is safe
// because each draft is its own file, replaced in one rename.

const (
	draftsDirName        = "drafts"
	retainedDraftVersion = 1
	draftDirAttempts     = 5
)

// RetainedDraft is one day's unsaved reflection.
type RetainedDraft struct {
	Version int    `json:"version"`
	Date    string `json:"date"`
	// RetainedAt is the journal clock when the draft was kept, RFC 3339.
	RetainedAt string `json:"retained_at"`
	// Reason is why the save did not happen, as it was reported.
	Reason string `json:"reason"`
	// Base is the revision of the day's entry the writing was made on.
	Base Revision `json:"base"`
	// SettingsRevision is the revision of the questions and anchor selection
	// the writing was made under.
	SettingsRevision Revision `json:"settings_revision,omitempty"`
	Questions        []string `json:"questions"`
	Answers          []string `json:"answers"`
	FollowThrough    string   `json:"follow_through,omitempty"`
}

// Unasked returns the kept answers to questions that are not among questions
// any more, in the order they were written: writing AnswersFor has no place
// for, which the author must be shown before the draft is replaced.
func (d RetainedDraft) Unasked(questions []string) []UnaskedAnswer {
	asked := make(map[string]struct{}, len(questions))
	for _, question := range questions {
		asked[question] = struct{}{}
	}
	var unasked []UnaskedAnswer
	for i, question := range d.Questions {
		if _, ok := asked[question]; ok || i >= len(d.Answers) || d.Answers[i] == "" {
			continue
		}
		unasked = append(unasked, UnaskedAnswer{Question: question, Answer: d.Answers[i]})
	}
	return unasked
}

// UnaskedAnswer is a kept answer whose question the journal no longer asks.
type UnaskedAnswer struct {
	Question string
	Answer   string
}

// AnswersFor lines the kept answers up with questions by their text, so a
// draft survives the questions being reordered or reworded in between. A
// question the draft never answered gets an empty answer.
func (d RetainedDraft) AnswersFor(questions []string) []string {
	kept := make(map[string]string, len(d.Questions))
	for i, question := range d.Questions {
		if i < len(d.Answers) {
			kept[question] = d.Answers[i]
		}
	}
	answers := make([]string, len(questions))
	for i, question := range questions {
		answers[i] = kept[question]
	}
	return answers
}

// DraftPathFor is where the unsaved reflection for date is kept.
func DraftPathFor(reflectionsPath, date string) string {
	return filepath.Join(JournalMetadataDirFor(reflectionsPath), draftsDirName, date+".json")
}

// ReviewDraftPathFor is where an unsaved review of a window is kept. It is
// scoped to the directory, like the archive the review was meant for.
func ReviewDraftPathFor(archivePath string, period ReviewPeriod, start, end string) string {
	return filepath.Join(DirectoryMetadataDirFor(archivePath), draftsDirName,
		fmt.Sprintf("review-%s-%s-to-%s.md", period, start, end))
}

// RetainDraft keeps what the author wrote for draft's day and returns where.
// It replaces an older draft for the same day.
func (j *Journal) RetainDraft(draft ReflectionDraft, result ReflectionResult, reason string) (string, error) {
	if err := j.requireReflections(); err != nil {
		return "", err
	}
	if !IsCalendarDate(draft.Date) {
		return "", fmt.Errorf("invalid reflection date %q: expected YYYY-MM-DD", draft.Date)
	}
	encoded, err := json.MarshalIndent(RetainedDraft{
		Version:          retainedDraftVersion,
		Date:             draft.Date,
		RetainedAt:       j.clock().Format(time.RFC3339),
		Reason:           reason,
		Base:             draft.Base,
		SettingsRevision: draft.SettingsRevision,
		Questions:        draft.Questions,
		Answers:          result.Answers,
		FollowThrough:    result.FollowThrough,
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding the draft: %w", err)
	}
	path := DraftPathFor(j.reflectionsPath, draft.Date)
	return path, j.writeDraft(path, append(encoded, '\n'), filepath.Dir(j.reflectionsPath))
}

// RetainReviewDraft keeps the Markdown of a review that could not be archived
// and returns where.
func (j *Journal) RetainReviewDraft(save ReviewSave) (string, error) {
	path := ReviewDraftPathFor(j.archivePath, save.Period, save.Start, save.End)
	body := "# " + save.Title + "\n\n" + save.Body
	if save.AcceptedFocus != "" {
		body += "\n\nAccepted focus: " + save.AcceptedFocus
	}
	return path, j.writeDraft(path, []byte(body+"\n"), filepath.Dir(j.archivePath))
}

func (j *Journal) writeDraft(path string, data []byte, journalDir string) error {
	// Like the lock, a draft never creates the journal's own directory.
	if _, err := os.Stat(journalDir); err != nil {
		return fmt.Errorf("keeping the draft: %w", err)
	}
	// A draft is written without the lock, so a commit finishing at this
	// instant may prune `.seneca/` while the draft's directory is being made
	// inside it. Making it again is all that takes.
	var err error
	for attempt := 0; attempt < draftDirAttempts; attempt++ {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			continue
		}
		err = j.fs.AtomicWriteFile(path, data, commitPrivateMode)
		if err == nil || WriteCommitted(err) || !lockDirRace(err) {
			break
		}
	}
	if err != nil && !WriteCommitted(err) {
		pruneEmptyDirs(filepath.Dir(path), journalDir)
		return fmt.Errorf("keeping the draft: %w", err)
	}
	// A draft that was written but not flushed is still a kept draft.
	return nil
}

// RetainedDraft returns the unsaved reflection kept for date, if there is one.
// It reads one file and creates nothing.
func (j *Journal) RetainedDraft(date string) (RetainedDraft, bool, error) {
	if err := j.requireReflections(); err != nil {
		return RetainedDraft{}, false, err
	}
	if !IsCalendarDate(date) {
		return RetainedDraft{}, false, nil
	}
	path := DraftPathFor(j.reflectionsPath, date)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return RetainedDraft{}, false, nil
	}
	if err != nil {
		return RetainedDraft{}, false, fmt.Errorf("reading the kept draft %s: %w", path, err)
	}
	var draft RetainedDraft
	if err := json.Unmarshal(data, &draft); err != nil {
		return RetainedDraft{}, false, fmt.Errorf("reading the kept draft %s: %w", path, err)
	}
	if draft.Version != retainedDraftVersion || draft.Date != date {
		return RetainedDraft{}, false, fmt.Errorf("the kept draft %s is not one this seneca understands (version %d, date %q)",
			path, draft.Version, draft.Date)
	}
	return draft, true, nil
}

// DiscardDraft removes the draft kept for date, once its writing is saved. No
// draft is not an error.
func (j *Journal) DiscardDraft(date string) error {
	if j.reflectionsPath == "" || !IsCalendarDate(date) {
		return nil
	}
	path := DraftPathFor(j.reflectionsPath, date)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing the kept draft %s: %w", path, err)
	}
	pruneEmptyDirs(filepath.Dir(path), filepath.Dir(j.reflectionsPath))
	return nil
}
