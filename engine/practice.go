package engine

import (
	"errors"
	"fmt"
	"time"
)

// ApplyPracticePack makes pack the journal's practice: its questions and
// anchor replace the journal's as one unit, and the pack is recorded, whole,
// as in force from the journal's today. base is the settings revision the pack
// was chosen against; a change made meanwhile refuses it with a
// *StaleSettingsError. No Markdown is read or written.
//
// A journal without settings of its own gets them in the same commit, with the
// settings it was opened with recorded as its legacy settings, so that entries
// already written are read exactly as InitializeSettings would have left them.
//
// Applying the pack already in force changes nothing but questions edited
// since, which it puts back. A second pack the same day replaces the first in
// the history: the practice is one a day.
func (j *Journal) ApplyPracticePack(base Revision, pack PracticePack) (SettingsOutcome, error) {
	if err := j.requireReflections(); err != nil {
		return SettingsOutcome{}, err
	}
	if base == "" {
		return SettingsOutcome{}, errors.New("this practice change carries no base revision; read the settings with EffectiveSettings first")
	}
	if err := pack.Validate(); err != nil {
		return SettingsOutcome{}, err
	}
	pack.Questions = append([]string{}, pack.Questions...)
	path := SettingsPathFor(j.reflectionsPath)
	today := j.Today()

	var outcome SettingsOutcome
	err := j.Commit(func(c *Commit) error {
		tx, err := c.Metadata(path)
		if err != nil {
			return err
		}
		var doc settingsDocument
		if tx.Exists() {
			if doc, err = decodeSettings(path, tx.Original()); err != nil {
				return err
			}
		} else {
			if err := ValidateReflectionSettings(j.settings); err != nil {
				return fmt.Errorf("the configuration to seed the journal settings from is not valid: %w", err)
			}
			doc = settingsDocument{Reflection: unitOf(j.settings), Legacy: unitOf(j.settings)}
		}
		previous := doc.Reflection.settings()
		if current := SettingsRevisionOf(previous); current != base {
			return &StaleSettingsError{Path: path, Base: base, Current: current}
		}

		last := len(doc.Practice) - 1
		inForce := last >= 0 && samePack(doc.Practice[last].Pack, pack)
		if tx.Exists() && inForce && sameSettings(previous, pack.Settings()) {
			state := stateOf(path, doc)
			outcome = SettingsOutcome{State: state, Previous: previous}
			return nil
		}

		doc.Reflection = unitOf(pack.Settings())
		switch {
		case inForce:
			// Only the questions come back; the pack's day stays its own.
		case last >= 0 && doc.Practice[last].From >= today:
			// A day already holds a pack, or a device whose clock ran ahead
			// recorded one later than today: the newest pack takes its place
			// rather than breaking the order of the history.
			doc.Practice[last].Pack = pack
		default:
			doc.Practice = append(doc.Practice, PracticePeriod{From: today, Pack: pack})
		}

		encoded, err := doc.encode()
		if err != nil {
			return err
		}
		if doc, err = decodeSettings(path, encoded); err != nil {
			return err
		}
		outcome = SettingsOutcome{Changed: true, State: stateOf(path, doc), Previous: previous}
		return tx.Commit(encoded)
	})
	if err != nil {
		return committedOutcome(outcome, err)
	}
	return outcome, nil
}

// PracticeSpan is a run of days inside a window and the pack in force on
// them. Pack is nil for days before any pack was applied.
type PracticeSpan struct {
	From string
	To   string
	Pack *PracticePack
}

// PracticeDuring divides the days from start to end, both included, by the
// pack in force on them. It returns nothing when no pack was in force on any
// of them, so that a window no pack covered is told nothing about packs.
func PracticeDuring(practice []PracticePeriod, start, end string) []PracticeSpan {
	if len(practice) == 0 || start > end || practice[0].From > end {
		return nil
	}

	var spans []PracticeSpan
	add := func(from, to string, pack *PracticePack) {
		if from < start {
			from = start
		}
		if to > end {
			to = end
		}
		if from <= to {
			spans = append(spans, PracticeSpan{From: from, To: to, Pack: pack})
		}
	}
	add(start, dayBefore(practice[0].From), nil)
	for i := range practice {
		to := end
		if i+1 < len(practice) {
			to = dayBefore(practice[i+1].From)
		}
		pack := practice[i].Pack
		add(practice[i].From, to, &pack)
	}
	return spans
}

func dayBefore(date string) string {
	day, err := time.Parse(CalendarDateLayout, date)
	if err != nil {
		return date
	}
	return day.AddDate(0, 0, -1).Format(CalendarDateLayout)
}
