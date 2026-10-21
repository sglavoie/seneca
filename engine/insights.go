package engine

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// Follow-through insights are structural reporting over what the journal
// already holds: the intentions carried from one day to the next, whether a
// later day answered them, the wordings and words that return, and how long
// each focus stood. Nothing is inferred by a model and nothing is scored: every
// count can be checked by hand against `seneca show` and `seneca focus history`.

// InsightsDays is the window a report covers when none is chosen: the last 30
// days, today included.
const InsightsDays = 30

// IntentionStatus is what became of an intention.
type IntentionStatus string

const (
	// IntentionAnswered: a later entry carried it and wrote a response.
	IntentionAnswered IntentionStatus = "answered"
	// IntentionUnanswered: a later entry carried it and left the response blank.
	IntentionUnanswered IntentionStatus = "unanswered"
	// IntentionNotCarried: entries were written after it, and none carried it,
	// as when they were written without context.
	IntentionNotCarried IntentionStatus = "not-carried"
	// IntentionOpen: nothing has been written since; the next entry carries it.
	IntentionOpen IntentionStatus = "open"
	// IntentionUntracked: written before the journal carried any intention,
	// when no later day could have answered it.
	IntentionUntracked IntentionStatus = "untracked"
)

// IntentionOutcome is one intention: the anchor answer of the entry dated
// Date, and what the entries after it did with it.
type IntentionOutcome struct {
	Date   string
	Text   string
	Status IntentionStatus
	// CarriedOn are the dates of the entries whose follow-through answers
	// this intention, oldest first.
	CarriedOn []string
	// AnsweredOn is the first of them with a response; empty unless answered.
	AnsweredOn string
	Response   string
}

// RepeatedIntention is an intention written in the same words on more than
// one day, ignoring case, punctuation, and spacing. Text is its latest wording.
type RepeatedIntention struct {
	Text  string
	Dates []string
}

// RecurringWord is a word that appears in the intentions of several days.
type RecurringWord struct {
	Word  string
	Dates []string
}

// FocusSpan is one focus event and how long it stood: up to the next event
// in the timeline, or still current. The pairing is `seneca focus history`'s.
type FocusSpan struct {
	Event  FocusEventKind
	Text   string
	Origin FocusOrigin
	From   string
	// Until is the effective date of the event that replaced it, empty while
	// it is current.
	Until string
	// Days is the calendar days from From to Until, or to today while current.
	Days int
	// ReplacedBy is the kind of event that replaced it, empty while current;
	// ReplacedByText is the focus it set.
	ReplacedBy     FocusEventKind
	ReplacedByText string
}

// Current reports whether the span is still in force.
func (s FocusSpan) Current() bool { return s.Until == "" }

// Insights is the report over the days From to To, inclusive.
type Insights struct {
	From string
	To   string
	// Intentions are those written in the window, oldest first. What became
	// of them is judged on every entry written since, inside the window or
	// after it.
	Intentions []IntentionOutcome
	Answered   int
	Unanswered int
	NotCarried int
	Open       int
	Untracked  int
	// Tracked is the first day whose intention a later entry carried: the
	// journal carried none before it. Empty when it never carried one.
	Tracked string
	// Repeated holds the intentions written in the same words more than once
	// in the window, the most repeated first.
	Repeated []RepeatedIntention
	// RecurringWords are the words found in the intentions of at least
	// RecurringWordDays days in the window, the most frequent first.
	RecurringWords []RecurringWord
	// Focus holds the focus spans that overlap the window, oldest first.
	Focus []FocusSpan
	// FocusProblem says why the focus could not be read: the archive is
	// damaged. The intentions are reported without it.
	FocusProblem string
}

// RecurringWordDays is how many days' intentions a word must appear in to be
// reported, and recurringWordLimit how many are reported at most.
const (
	RecurringWordDays  = 3
	recurringWordLimit = 12
)

// InsightsWindow is the default window ending on today: the last `days` days,
// today included.
func InsightsWindow(today string, days int) (from, to string) {
	end, err := time.Parse(CalendarDateLayout, today)
	if err != nil || days <= 0 {
		return "", today
	}
	return end.AddDate(0, 0, -(days - 1)).Format(CalendarDateLayout), today
}

// Insights reports on the days from `from` to `to`, inclusive. An empty from
// starts at the first entry; an empty to ends today. A damaged archive leaves
// the focus out and says why; a damaged journal is refused.
func (j *Journal) Insights(from, to string) (Insights, error) {
	state, err := j.EffectiveSettings()
	if err != nil {
		return Insights{}, err
	}
	entries, err := j.Entries()
	if err != nil {
		return Insights{}, err
	}
	today := j.Today()
	if to == "" {
		to = today
	}
	var events []FocusEvent
	problem := ""
	archive, exists, err := j.ReadArchive()
	switch {
	case err != nil:
		return Insights{}, err
	case exists && len(archive.Diagnostics) > 0:
		problem = (&UnreadableContextError{path: j.archivePath, Purpose: "the focus history", Remedy: "run `seneca doctor` to inspect it"}).Error()
	case exists:
		events = archive.FocusEvents()
	}
	insights := BuildInsights(entries, state.Legacy, events, from, to, today)
	insights.FocusProblem = problem
	return insights, nil
}

// BuildInsights computes the report from the entries, read with legacy
// settings where an entry carries no anchor marker, and the focus events in
// archive order.
func BuildInsights(entries []ReflectionEntry, legacy ReflectionSettings, events []FocusEvent, from, to, today string) Insights {
	report := Insights{From: from, To: to}
	inWindow := func(date string) bool {
		return (from == "" || date >= from) && (to == "" || date <= to)
	}

	dated := make([]ReflectionEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Date != "" {
			dated = append(dated, entry)
		}
	}
	sort.SliceStable(dated, func(i, j int) bool { return dated[i].Date < dated[j].Date })
	if report.From == "" && len(dated) > 0 {
		report.From = dated[0].Date
	}

	type carried struct {
		date     string
		response string
	}
	answers := map[string][]carried{}
	for _, entry := range dated {
		if follow, ok := entry.FollowThroughSection(); ok && follow.SourceDate != "" {
			answers[follow.SourceDate] = append(answers[follow.SourceDate], carried{entry.Date, follow.Response})
			if report.Tracked == "" || follow.SourceDate < report.Tracked {
				report.Tracked = follow.SourceDate
			}
		}
	}
	latest := ""
	if len(dated) > 0 {
		latest = dated[len(dated)-1].Date
	}

	for _, entry := range dated {
		if !inWindow(entry.Date) {
			continue
		}
		text := entry.AnchorAnswer(legacy.Questions, legacy.AnchorQuestionIndex)
		if text == "" {
			continue
		}
		outcome := IntentionOutcome{Date: entry.Date, Text: text}
		for _, answer := range answers[entry.Date] {
			outcome.CarriedOn = append(outcome.CarriedOn, answer.date)
			if outcome.AnsweredOn == "" && strings.TrimSpace(answer.response) != "" {
				outcome.AnsweredOn = answer.date
				outcome.Response = answer.response
			}
		}
		switch {
		case outcome.AnsweredOn != "":
			outcome.Status = IntentionAnswered
			report.Answered++
		case len(outcome.CarriedOn) > 0:
			outcome.Status = IntentionUnanswered
			report.Unanswered++
		case latest > entry.Date && (report.Tracked == "" || entry.Date < report.Tracked):
			outcome.Status = IntentionUntracked
			report.Untracked++
		case latest > entry.Date:
			outcome.Status = IntentionNotCarried
			report.NotCarried++
		default:
			outcome.Status = IntentionOpen
			report.Open++
		}
		report.Intentions = append(report.Intentions, outcome)
	}

	report.Repeated = repeatedIntentions(report.Intentions)
	report.RecurringWords = recurringWords(report.Intentions)

	for _, span := range FocusSpans(events, today) {
		if focusSpanOverlaps(span, from, to) {
			report.Focus = append(report.Focus, span)
		}
	}
	return report
}

// focusSpanOverlaps reports whether a span stood on any day of the window. A
// span stands up to the day before Until, except one superseded the day it
// took effect, which stands on that day alone.
func focusSpanOverlaps(span FocusSpan, from, to string) bool {
	if to != "" && span.From > to {
		return false
	}
	switch {
	case span.Current() || from == "":
		return true
	case span.Until == span.From:
		return span.From >= from
	default:
		return span.Until > from
	}
}

// FocusSpans pairs each focus event, in archive order, with the one after it,
// exactly as `seneca focus history` shows them.
func FocusSpans(events []FocusEvent, today string) []FocusSpan {
	spans := make([]FocusSpan, 0, len(events))
	for i, event := range events {
		span := FocusSpan{Event: event.Event, Text: event.Text, Origin: event.Origin, From: event.Effective}
		end := today
		if i+1 < len(events) {
			next := events[i+1]
			span.Until = next.Effective
			span.ReplacedBy = next.Event
			span.ReplacedByText = next.Text
			end = next.Effective
		}
		if days, ok := daysSince(span.From, end); ok && days > 0 {
			span.Days = days
		}
		spans = append(spans, span)
	}
	return spans
}

// intentionKey is an intention reduced to its words: lowercase, with every
// run of punctuation and spacing made one space.
func intentionKey(text string) string {
	return strings.Join(intentionWords(text), " ")
}

func intentionWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func repeatedIntentions(intentions []IntentionOutcome) []RepeatedIntention {
	groups := map[string]*RepeatedIntention{}
	var order []string
	for _, intention := range intentions {
		key := intentionKey(intention.Text)
		if key == "" {
			continue
		}
		group, ok := groups[key]
		if !ok {
			group = &RepeatedIntention{}
			groups[key] = group
			order = append(order, key)
		}
		group.Text = intention.Text
		group.Dates = append(group.Dates, intention.Date)
	}
	var repeated []RepeatedIntention
	for _, key := range order {
		if group := groups[key]; len(group.Dates) > 1 {
			repeated = append(repeated, *group)
		}
	}
	sort.SliceStable(repeated, func(i, j int) bool { return len(repeated[i].Dates) > len(repeated[j].Dates) })
	return repeated
}

func recurringWords(intentions []IntentionOutcome) []RecurringWord {
	dates := map[string][]string{}
	for _, intention := range intentions {
		seen := map[string]bool{}
		for _, word := range intentionWords(intention.Text) {
			if seen[word] || len([]rune(word)) < 4 || insightStopwords[word] {
				continue
			}
			seen[word] = true
			dates[word] = append(dates[word], intention.Date)
		}
	}
	var words []RecurringWord
	for word, days := range dates {
		if len(days) >= RecurringWordDays {
			words = append(words, RecurringWord{Word: word, Dates: days})
		}
	}
	sort.Slice(words, func(i, j int) bool {
		if len(words[i].Dates) != len(words[j].Dates) {
			return len(words[i].Dates) > len(words[j].Dates)
		}
		return words[i].Word < words[j].Word
	})
	if len(words) > recurringWordLimit {
		words = words[:recurringWordLimit]
	}
	return words
}

// insightStopwords are the common English and French words of four letters or
// more that say nothing about what an intention is about. Shorter words are
// never counted.
var insightStopwords = func() map[string]bool {
	words := strings.Fields(`
		able about after again also always around back because been before
		being better could does doing done each even every from give good have
		into just keep know last less like make maybe more most much must need next
		only other over really should some spend still stuff such sure take than
		that their them then there these they thing things think this those
		through time today tomorrow very want well what when where which while
		will with would your yours myself
		alors aussi autre avant avec avoir bien cela cette comme dans demain
		depuis donc elle encore être faire fois leur mais même moins notre
		pendant peut plus pour quand quelque sans sont sous suis tout toute
		toutes tous très votre aujourd`)
	set := make(map[string]bool, len(words))
	for _, word := range words {
		set[word] = true
	}
	return set
}()
