package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// A practice pack is a way of keeping the evening journal, packaged: the
// questions, which of them is the anchor, a suggested reminder time, the
// guidance reviews are given, and a description. Applying one goes through the
// journal settings, so nothing already written is rewritten; the settings
// document keeps a copy of every pack applied, with the day it was applied, so
// reviews know which practice each day was written under.

// PracticePackFormat is the version of the pack file this engine reads and
// writes.
const PracticePackFormat = 1

// PracticePackFileSuffix ends the name of a pack file.
const PracticePackFileSuffix = ".seneca-pack.json"

const (
	maxPackFileBytes        = 64 << 10
	maxPackIDRunes          = 40
	maxPackNameRunes        = 60
	maxPackDescriptionRunes = 280
	// MaxPackGuidanceRunes bounds the guidance, which goes into every review
	// prompt the pack covers.
	MaxPackGuidanceRunes = 1000
)

var packIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// PracticePack is one pack, as its file states it. Field order is the file's.
type PracticePack struct {
	Format              int      `json:"seneca_pack"`
	ID                  string   `json:"id"`
	Version             int      `json:"version"`
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	Questions           []string `json:"questions"`
	AnchorQuestionIndex int      `json:"anchor_question_index"`
	ReminderTime        string   `json:"reminder_time,omitempty"`
	Guidance            string   `json:"guidance,omitempty"`
}

// Settings are the questions and anchor the pack asks for.
func (p PracticePack) Settings() ReflectionSettings {
	return ReflectionSettings{Questions: append([]string{}, p.Questions...), AnchorQuestionIndex: p.AnchorQuestionIndex}
}

// ErrInvalidPracticePack reports a pack this engine refuses.
var ErrInvalidPracticePack = errors.New("invalid practice pack")

func invalidPack(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPracticePack, fmt.Sprintf(format, args...))
}

// Validate holds the pack to the rules of its file.
func (p PracticePack) Validate() error {
	if p.Format != PracticePackFormat {
		if p.Format > PracticePackFormat {
			return invalidPack("seneca_pack %d was written by a newer seneca", p.Format)
		}
		return invalidPack("seneca_pack must be %d", PracticePackFormat)
	}
	if utf8.RuneCountInString(p.ID) > maxPackIDRunes || !packIDPattern.MatchString(p.ID) {
		return invalidPack("id %q must be 1 to %d lowercase letters, digits, and single hyphens, starting with a letter", p.ID, maxPackIDRunes)
	}
	if p.Version < 1 {
		return invalidPack("version must be 1 or more")
	}
	if err := packLine("name", p.Name, true, maxPackNameRunes); err != nil {
		return err
	}
	if err := packLine("description", p.Description, false, maxPackDescriptionRunes); err != nil {
		return err
	}
	if err := ValidateReflectionSettings(p.Settings()); err != nil {
		return invalidPack("%v", err)
	}
	if p.ReminderTime != "" {
		if _, err := NormalizeReminderTimes([]string{p.ReminderTime}); err != nil {
			return invalidPack("reminder_time %q is not a time of day written as HH:MM", p.ReminderTime)
		}
	}
	if !utf8.ValidString(p.Guidance) {
		return invalidPack("guidance is not valid UTF-8")
	}
	if n := utf8.RuneCountInString(p.Guidance); n > MaxPackGuidanceRunes {
		return invalidPack("guidance is %d characters; at most %d", n, MaxPackGuidanceRunes)
	}
	if p.Guidance != strings.TrimSpace(p.Guidance) {
		return invalidPack("guidance must not begin or end with white space")
	}
	return nil
}

func packLine(field, value string, required bool, maxRunes int) error {
	switch {
	case !utf8.ValidString(value):
		return invalidPack("%s is not valid UTF-8", field)
	case required && strings.TrimSpace(value) == "":
		return invalidPack("%s must not be blank", field)
	case strings.TrimSpace(value) != value:
		return invalidPack("%s must not begin or end with white space", field)
	case strings.ContainsAny(value, "\r\n"):
		return invalidPack("%s must be one line", field)
	}
	if n := utf8.RuneCountInString(value); n > maxRunes {
		return invalidPack("%s is %d characters; at most %d", field, n, maxRunes)
	}
	return nil
}

// ParsePracticePack reads a pack file. Anything it does not recognize is
// refused rather than ignored.
func ParsePracticePack(data []byte) (PracticePack, error) {
	if len(data) > maxPackFileBytes {
		return PracticePack{}, invalidPack("the file is %d bytes; a pack is at most %d", len(data), maxPackFileBytes)
	}
	if !utf8.Valid(data) {
		return PracticePack{}, invalidPack("the file is not valid UTF-8")
	}
	var probe struct {
		Format *int `json:"seneca_pack"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return PracticePack{}, invalidPack("%v", err)
	}
	if probe.Format == nil {
		return PracticePack{}, invalidPack("it has no seneca_pack field, so it is not a Seneca pack")
	}
	if *probe.Format > PracticePackFormat {
		return PracticePack{}, invalidPack("seneca_pack %d was written by a newer seneca", *probe.Format)
	}

	var pack PracticePack
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pack); err != nil {
		return PracticePack{}, invalidPack("%v", err)
	}
	if dec.More() {
		return PracticePack{}, invalidPack("there is more after the pack")
	}
	if err := pack.Validate(); err != nil {
		return PracticePack{}, err
	}
	return pack, nil
}

// EncodePracticePack writes a pack file. The same pack always encodes to the
// same bytes.
func EncodePracticePack(pack PracticePack) ([]byte, error) {
	if err := pack.Validate(); err != nil {
		return nil, err
	}
	return encodeSettings(pack)
}

func samePack(a, b PracticePack) bool {
	ea, errA := encodeSettings(a)
	eb, errB := encodeSettings(b)
	return errA == nil && errB == nil && bytes.Equal(ea, eb)
}

// BuiltinPracticePacks are the packs every Seneca offers, in the order they are
// offered. The first is the default practice.
func BuiltinPracticePacks() []PracticePack {
	packs := make([]PracticePack, len(builtinPacks))
	for i, pack := range builtinPacks {
		pack.Questions = append([]string{}, pack.Questions...)
		packs[i] = pack
	}
	return packs
}

// BuiltinPracticePack finds a built-in pack by its id.
func BuiltinPracticePack(id string) (PracticePack, bool) {
	for _, pack := range BuiltinPracticePacks() {
		if pack.ID == id {
			return pack, true
		}
	}
	return PracticePack{}, false
}

var builtinPacks = []PracticePack{
	{
		Format:      PracticePackFormat,
		ID:          "stoic",
		Version:     1,
		Name:        "Stoic evening review",
		Description: "Seneca's own nightly examination: what went badly, what went well, and what to do differently tomorrow.",
		// The default questions, in their load-bearing order.
		Questions:           DefaultReflectionQuestions(),
		AnchorQuestionIndex: 3,
		ReminderTime:        "21:00",
		Guidance: "This journal keeps the Stoic evening review Seneca describes in On Anger: the day is examined " +
			"without self-reproach, as a judge who means to improve the defendant, not punish him. " +
			"Progress means faults noticed sooner, judgments separated from events, and attention kept on " +
			"what was in the author's control: choices, effort, and responses. Where the entries use Stoic " +
			"terms (virtue, indifferents, assent, the dichotomy of control), use them the same way; do not " +
			"import them where the entries do not.",
	},
	{
		Format:      PracticePackFormat,
		ID:          "step-10",
		Version:     1,
		Name:        "Step 10 nightly inventory",
		Description: "The twelve-step nightly inventory: resentment, selfishness, dishonesty, and fear, amends owed, and how to do better.",
		Questions: []string{
			"Where was I resentful, selfish, dishonest, or afraid today?",
			"Do I owe an apology, or is there something I should discuss with someone?",
			"Where was I kind and loving toward others?",
			"What could I have done better, and what will I do tomorrow?",
		},
		AnchorQuestionIndex: 4,
		ReminderTime:        "21:30",
		Guidance: "This journal keeps a Step 10 nightly inventory from a twelve-step recovery program. " +
			"An honest admission of a defect is progress, not failure: treat it as the program does, as the " +
			"inventory working. Progress means wrongs admitted promptly, amends made or planned, resentments " +
			"named and set down, and more attention to others than to self. Use the program's vocabulary " +
			"(inventory, amends, defects of character, service, sponsor) where the entries do. Never shame, " +
			"never diagnose, and never raise drinking, using, or relapse unless the entries do.",
	},
	{
		Format:      PracticePackFormat,
		ID:          "examen",
		Version:     1,
		Name:        "Ignatian examen",
		Description: "The daily examen of St. Ignatius: gratitude, where God was present, consolation and desolation, and grace for tomorrow.",
		Questions: []string{
			"What am I grateful for today?",
			"Where did I notice God's presence, or feel consolation?",
			"Where did I feel desolation, or turn away?",
			"What do I ask forgiveness or healing for?",
			"What grace do I ask for tomorrow?",
		},
		AnchorQuestionIndex: 5,
		ReminderTime:        "21:00",
		Guidance: "This journal keeps the Ignatian examen. Read consolation and desolation in Ignatius's " +
			"sense: movements toward or away from God, faith, hope, and love, not simply good or bad moods. " +
			"Progress means noticing those movements more readily, gratitude that grows specific, and graces " +
			"asked for that the following days show being received. Use the entries' own language about God " +
			"and prayer; never offer theological judgments or spiritual direction of your own.",
	},
	{
		Format:      PracticePackFormat,
		ID:          "gratitude",
		Version:     1,
		Name:        "Gratitude and intention",
		Description: "Three short questions: what you are grateful for, what went well and why, and one intention for tomorrow.",
		Questions: []string{
			"What am I grateful for today?",
			"What went well, and why?",
			"What is my intention for tomorrow?",
		},
		AnchorQuestionIndex: 3,
		ReminderTime:        "21:00",
		Guidance: "This journal keeps a gratitude and intention practice. Progress means gratitude that becomes " +
			"specific rather than generic, an understanding of why good days went well that the author acts " +
			"on, and intentions that are small enough to keep and are kept. Keep the tone warm and plain, and " +
			"do not turn a light practice into a heavy one.",
	},
	{
		Format:      PracticePackFormat,
		ID:          "virtues",
		Version:     1,
		Name:        "Virtue tracking",
		Description: "Benjamin Franklin's thirteen virtues: where you fell short, what you practised, the good you did, and the virtue to attend to next.",
		Questions: []string{
			"Which virtue did I fall short of today, and how?",
			"Which virtue did I practise well?",
			"What good have I done today?",
			"Which virtue will I attend to tomorrow?",
		},
		AnchorQuestionIndex: 4,
		ReminderTime:        "21:00",
		Guidance: "This journal follows Benjamin Franklin's practice of the thirteen virtues: temperance, " +
			"silence, order, resolution, frugality, industry, sincerity, justice, moderation, cleanliness, " +
			"tranquility, chastity, and humility. Name virtues as Franklin did. Progress means fewer faults " +
			"against the virtue being attended to, and one virtue at a time held long enough to become habit; " +
			"notice when attention moves on before a virtue is established. Franklin counted faults, but " +
			"never turn the entries into scores.",
	},
}
