package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Journal settings are the questions and the anchor selection every device
// that writes one journal must agree on. They live in one versioned document
// in the journal's own metadata directory, so they travel with the journal
// rather than with a machine.
//
// # Precedence
//
// A journal without the document is uninitialized: it uses the settings its
// Journal was opened with — on the desktop, config.toml — exactly as before.
// Initializing is explicit, seeds the document from those settings, and from
// then on the document is authoritative: what a Journal was opened with is
// ignored for the questions and the anchor, and is never written back.
//
// # One unit
//
// The questions and the anchor index are one unit with one revision. The index
// means nothing without the list it points into, so neither is validated,
// replaced, or compared without the other, and a change carries the revision
// it was made on.
//
// # History
//
// Changing the settings rewrites no Markdown. Answers are found by their
// heading text and follow-through snapshots are stored in their entry, so both
// read the same under any settings. The one thing settings ever decided about
// an entry already written is where the anchor of an entry with no role marker
// is; the document records the questions and anchor in force when it was
// created as `legacy`, and such entries are read by that for good.
//
// # Practice
//
// Applying a practice pack replaces the questions and the anchor like any
// other change, and also records the pack, whole, with the journal day it was
// applied on. That history is what tells a review which practice each day was
// written under. A document with no practice is version 1, byte for byte what
// it was before packs existed; one with a practice is version 2.

const (
	settingsFileName = "settings.json"
	// JournalSettingsVersion is the newest version of the settings document
	// this engine reads and writes. It reads every version from 1.
	JournalSettingsVersion = 2
	// settingsVersionWithoutPractice is what a document that records no
	// practice is written as, so that an older engine can still read it.
	settingsVersionWithoutPractice = 1
)

// SettingsPathFor is where a journal's settings document is kept.
func SettingsPathFor(reflectionsPath string) string {
	return filepath.Join(JournalMetadataDirFor(reflectionsPath), settingsFileName)
}

type settingsUnit struct {
	Questions           []string `json:"questions"`
	AnchorQuestionIndex int      `json:"anchor_question_index"`
}

func unitOf(settings ReflectionSettings) settingsUnit {
	return settingsUnit{
		Questions:           append([]string{}, settings.Questions...),
		AnchorQuestionIndex: settings.AnchorQuestionIndex,
	}
}

func (u settingsUnit) settings() ReflectionSettings {
	return ReflectionSettings{Questions: u.Questions, AnchorQuestionIndex: u.AnchorQuestionIndex}
}

// settingsDocument is the file. It holds nothing that differs between devices
// or between runs — no timestamp, no device name — so two clients that record
// the same settings write the same bytes.
type settingsDocument struct {
	Version    int          `json:"version"`
	Reflection settingsUnit `json:"reflection"`
	// Legacy is Reflection as it was when the document was created. It is
	// never changed afterwards.
	Legacy settingsUnit `json:"legacy"`
	// Practice is every pack applied, oldest first, one a day at most.
	Practice []PracticePeriod `json:"practice,omitempty"`
}

// PracticePeriod is a pack and the journal day it was applied on. It stays in
// force until the next one.
type PracticePeriod struct {
	From string       `json:"from"`
	Pack PracticePack `json:"pack"`
}

// encode writes the document under the version its contents need.
func (doc settingsDocument) encode() ([]byte, error) {
	doc.Version = settingsVersionWithoutPractice
	if len(doc.Practice) > 0 {
		doc.Version = JournalSettingsVersion
	}
	return encodeSettings(doc)
}

func validatePractice(practice []PracticePeriod) error {
	for i, period := range practice {
		if !IsCalendarDate(period.From) {
			return fmt.Errorf("practice[%d]: from %q is not a calendar date", i, period.From)
		}
		if i > 0 && period.From <= practice[i-1].From {
			return fmt.Errorf("practice[%d]: from %s does not follow %s", i, period.From, practice[i-1].From)
		}
		if err := period.Pack.Validate(); err != nil {
			return fmt.Errorf("practice[%d]: %w", i, err)
		}
	}
	return nil
}

func encodeSettings(v any) ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	// A question is prose: `<`, `>`, and `&` are written as themselves.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encoding the journal settings: %w", err)
	}
	return out.Bytes(), nil
}

// SettingsRevisionOf is the revision of the questions and the anchor taken
// together. It depends on nothing else, so it is the same on every device that
// holds the same settings, and for an uninitialized journal whose configuration
// already matches.
func SettingsRevisionOf(settings ReflectionSettings) Revision {
	// A string list and an integer always encode.
	encoded, _ := encodeSettings(unitOf(settings))
	return RevisionOf(encoded)
}

func decodeSettings(path string, data []byte) (settingsDocument, error) {
	refuse := func(err error) (settingsDocument, error) {
		return settingsDocument{}, &SettingsDocumentError{Path: path, err: err}
	}

	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return refuse(err)
	}
	if probe.Version > JournalSettingsVersion {
		return refuse(fmt.Errorf("it is version %d, written by a newer seneca", probe.Version))
	}
	if probe.Version < settingsVersionWithoutPractice {
		return refuse(fmt.Errorf("version %d is not one this seneca understands", probe.Version))
	}

	var doc settingsDocument
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return refuse(err)
	}
	if err := ValidateReflectionSettings(doc.Reflection.settings()); err != nil {
		return refuse(fmt.Errorf("reflection: %w", err))
	}
	if err := ValidateReflectionSettings(doc.Legacy.settings()); err != nil {
		return refuse(fmt.Errorf("legacy: %w", err))
	}
	// The version follows from the practice, so a document whose version says
	// otherwise was not written by Seneca.
	if hasPractice := len(doc.Practice) > 0; hasPractice != (doc.Version == JournalSettingsVersion) {
		if hasPractice {
			return refuse(fmt.Errorf("version %d cannot record a practice", doc.Version))
		}
		return refuse(fmt.Errorf("version %d must record a practice", doc.Version))
	}
	if err := validatePractice(doc.Practice); err != nil {
		return refuse(err)
	}
	return doc, nil
}

// SettingsDocumentError reports a settings document that cannot be used. The
// journal refuses to guess at questions: nothing falls back to the device's
// configuration once a document exists.
type SettingsDocumentError struct {
	Path string
	err  error
}

func (e *SettingsDocumentError) Error() string {
	return fmt.Sprintf("the journal settings %s cannot be used: %v", e.Path, e.err)
}

func (e *SettingsDocumentError) Unwrap() error { return e.err }

// ErrSettingsNotInitialized refuses to change journal settings that were never
// initialized: until then the device's own configuration is in force and is
// what should be edited.
var ErrSettingsNotInitialized = errors.New("this journal has no settings of its own yet; initialize them first")

// StaleSettingsError refuses something made under questions and an anchor the
// journal no longer has: a reflection whose form was opened before they
// changed, when Date is set, and otherwise a settings change derived from an
// older revision. Nothing was written.
type StaleSettingsError struct {
	Path    string
	Date    string
	Base    Revision
	Current Revision
}

func (e *StaleSettingsError) Error() string {
	if e.Date != "" {
		return fmt.Sprintf("the journal's questions changed after the reflection for %s was opened; "+
			"review the answers against the current questions before saving (nothing was written)", e.Date)
	}
	return fmt.Sprintf("the journal settings %s changed after they were read; nothing was written", e.Path)
}

// AnchorMovedError refuses a change to the questions that says nothing about
// the anchor when the anchor question is no longer among them. Keeping the
// index would quietly make a different question the anchor.
type AnchorMovedError struct {
	Index int
	// Was is the anchor question being replaced.
	Was string
	// Now is the question the unchanged index would point at, empty when the
	// new list is too short to have one.
	Now string
}

func (e *AnchorMovedError) Error() string {
	now := "no question at all"
	if e.Now != "" {
		now = fmt.Sprintf("%q", e.Now)
	}
	return fmt.Sprintf("the anchor question %q is not among the new questions, and anchor question %d would become %s; "+
		"state the anchor together with the questions (0 disables it)", e.Was, e.Index, now)
}

// SettingsState is the questions and anchor selection in force for a journal,
// and where they came from.
type SettingsState struct {
	// Path is where the journal's settings document is, or would be. It is
	// empty for a journal opened without a reflections path.
	Path string
	// Initialized reports whether the document exists. When it does not,
	// Settings are the ones the Journal was opened with.
	Initialized bool
	// Settings are the questions and anchor selection in force.
	Settings ReflectionSettings
	// Legacy is how an entry with no anchor role marker is read: the settings
	// the document was created from, or Settings while there is no document.
	Legacy ReflectionSettings
	// Revision identifies Settings. A draft and a settings change carry it
	// back so that a change made meanwhile is noticed.
	Revision Revision
	// Version is the settings document's version, 0 when there is none.
	Version int
	// Practice is every pack applied, oldest first. It is empty for a journal
	// that never applied one.
	Practice []PracticePeriod
}

// CurrentPractice is the pack applied last, if any.
func (s SettingsState) CurrentPractice() (PracticePeriod, bool) {
	if len(s.Practice) == 0 {
		return PracticePeriod{}, false
	}
	return s.Practice[len(s.Practice)-1], true
}

// EffectiveSettings reports the questions and anchor selection in force,
// completing an interrupted commit first. It creates nothing.
func (j *Journal) EffectiveSettings() (SettingsState, error) {
	if err := j.Recover(); err != nil {
		return SettingsState{}, err
	}
	return j.readSettings()
}

// readSettings is EffectiveSettings without recovery, for a caller that holds
// the journal lock.
func (j *Journal) readSettings() (SettingsState, error) {
	if j.reflectionsPath == "" {
		return j.uninitializedSettings(""), nil
	}
	state, initialized, err := LoadJournalSettings(j.reflectionsPath)
	if err != nil {
		return SettingsState{}, err
	}
	if !initialized {
		return j.uninitializedSettings(state.Path), nil
	}
	return state, nil
}

// LoadJournalSettings reads the settings document of the journal at
// reflectionsPath for a read-only caller. A journal without one is reported,
// never initialized: the returned state then carries only the path the
// document would have.
func LoadJournalSettings(reflectionsPath string) (SettingsState, bool, error) {
	path := SettingsPathFor(reflectionsPath)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return SettingsState{Path: path}, false, nil
	}
	if err != nil {
		return SettingsState{}, false, fmt.Errorf("reading the journal settings %s: %w", path, err)
	}
	doc, err := decodeSettings(path, data)
	if err != nil {
		return SettingsState{}, false, err
	}
	return stateOf(path, doc), true, nil
}

func (j *Journal) uninitializedSettings(path string) SettingsState {
	return SettingsState{
		Path:     path,
		Settings: j.settings,
		Legacy:   j.settings,
		Revision: SettingsRevisionOf(j.settings),
	}
}

func stateOf(path string, doc settingsDocument) SettingsState {
	settings := doc.Reflection.settings()
	return SettingsState{
		Path:        path,
		Initialized: true,
		Settings:    settings,
		Legacy:      doc.Legacy.settings(),
		Revision:    SettingsRevisionOf(settings),
		Version:     doc.Version,
		Practice:    append([]PracticePeriod{}, doc.Practice...),
	}
}

// SettingsOutcome reports what InitializeSettings or UpdateSettings did.
type SettingsOutcome struct {
	// Changed is false when the document already said what was asked for and
	// was left alone.
	Changed bool
	// State is the settings in force afterwards.
	State SettingsState
	// Previous is the settings in force before, equal to State.Settings when
	// nothing changed.
	Previous ReflectionSettings
}

// InitializeSettings gives the journal settings of its own, seeded from the
// ones this Journal was opened with. It is the explicit step after which the
// journal, not the device, decides the questions and the anchor.
//
// A journal that already has settings is left exactly as it is and reported:
// the second device to enroll adopts what the first recorded, whatever its own
// configuration says. No Markdown is read or written.
func (j *Journal) InitializeSettings() (SettingsOutcome, error) {
	if err := j.requireReflections(); err != nil {
		return SettingsOutcome{}, err
	}
	path := SettingsPathFor(j.reflectionsPath)

	var outcome SettingsOutcome
	err := j.Commit(func(c *Commit) error {
		tx, err := c.Metadata(path)
		if err != nil {
			return err
		}
		if tx.Exists() {
			doc, err := decodeSettings(path, tx.Original())
			if err != nil {
				return err
			}
			state := stateOf(path, doc)
			outcome = SettingsOutcome{State: state, Previous: state.Settings}
			return nil
		}

		if err := ValidateReflectionSettings(j.settings); err != nil {
			return fmt.Errorf("the configuration to seed the journal settings from is not valid: %w", err)
		}
		doc := settingsDocument{
			Version:    settingsVersionWithoutPractice,
			Reflection: unitOf(j.settings),
			Legacy:     unitOf(j.settings),
		}
		encoded, err := doc.encode()
		if err != nil {
			return err
		}
		outcome = SettingsOutcome{Changed: true, State: stateOf(path, doc), Previous: j.settings}
		return tx.Commit(encoded)
	})
	if err != nil {
		return committedOutcome(outcome, err)
	}
	return outcome, nil
}

// SettingsChange is a change to the questions, the anchor, or both. A nil
// field keeps what the journal has.
type SettingsChange struct {
	Questions           []string
	AnchorQuestionIndex *int
}

// apply resolves the change against the settings it was made on. New questions
// without a stated anchor keep the anchor on the same question, wherever it
// moved to; when that question is gone the change is refused rather than
// letting the old index choose another.
func (change SettingsChange) apply(current ReflectionSettings) (ReflectionSettings, error) {
	next := ReflectionSettings{
		Questions:           append([]string{}, current.Questions...),
		AnchorQuestionIndex: current.AnchorQuestionIndex,
	}
	if change.Questions != nil {
		next.Questions = append([]string{}, change.Questions...)
	}

	anchor := current.AnchorQuestionIndex
	switch {
	case change.AnchorQuestionIndex != nil:
		next.AnchorQuestionIndex = *change.AnchorQuestionIndex
	case change.Questions != nil && anchor > 0 && anchor <= len(current.Questions):
		was := current.Questions[anchor-1]
		next.AnchorQuestionIndex = 0
		for i, question := range next.Questions {
			if question == was {
				next.AnchorQuestionIndex = i + 1
			}
		}
		if next.AnchorQuestionIndex == 0 {
			moved := &AnchorMovedError{Index: anchor, Was: was}
			if anchor <= len(next.Questions) {
				moved.Now = next.Questions[anchor-1]
			}
			return ReflectionSettings{}, moved
		}
	}

	if err := ValidateReflectionSettings(next); err != nil {
		return ReflectionSettings{}, err
	}
	return next, nil
}

func sameSettings(a, b ReflectionSettings) bool {
	return SettingsRevisionOf(a) == SettingsRevisionOf(b)
}

// UpdateSettings replaces the journal's questions and anchor as one unit. base
// is the revision the change was decided on: when the settings are no longer at
// it — another device changed them meanwhile — the change is refused with a
// *StaleSettingsError, since an index chosen against one list of questions says
// nothing about another. The result is validated whole before anything is
// written, and no Markdown is read or written.
func (j *Journal) UpdateSettings(base Revision, change SettingsChange) (SettingsOutcome, error) {
	if err := j.requireReflections(); err != nil {
		return SettingsOutcome{}, err
	}
	if base == "" {
		return SettingsOutcome{}, errors.New("this settings change carries no base revision; read the settings with EffectiveSettings first")
	}
	path := SettingsPathFor(j.reflectionsPath)

	var outcome SettingsOutcome
	err := j.Commit(func(c *Commit) error {
		tx, err := c.Metadata(path)
		if err != nil {
			return err
		}
		if !tx.Exists() {
			return ErrSettingsNotInitialized
		}
		doc, err := decodeSettings(path, tx.Original())
		if err != nil {
			return err
		}
		state := stateOf(path, doc)
		if state.Revision != base {
			return &StaleSettingsError{Path: path, Base: base, Current: state.Revision}
		}

		next, err := change.apply(state.Settings)
		if err != nil {
			return err
		}
		if sameSettings(next, state.Settings) {
			outcome = SettingsOutcome{State: state, Previous: state.Settings}
			return nil
		}

		doc.Reflection = unitOf(next)
		encoded, err := doc.encode()
		if err != nil {
			return err
		}
		outcome = SettingsOutcome{Changed: true, State: stateOf(path, doc), Previous: state.Settings}
		return tx.Commit(encoded)
	})
	if err != nil {
		return committedOutcome(outcome, err)
	}
	return outcome, nil
}
