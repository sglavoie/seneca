package cmd

import (
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"strings"
	"time"
)

// The AI review schema is assembled from named fragments rather than written as
// one line, so the nullable focus assessment and the shared evidence shape stay
// readable. `focus_assessment` is required but nullable: the model must decide
// explicitly, and `null` is the only honest answer when no focus applied to the
// window.
const (
	reviewEvidenceSchema = `{"type":"object","properties":` +
		`{"date":{"type":"string"},"snippet":{"type":"string"}},` +
		`"required":["date","snippet"],"additionalProperties":false}`

	reviewItemSchema = `{"type":"object","properties":` +
		`{"theme":{"type":"string"},"summary":{"type":"string"},` +
		`"evidence":{"type":"array","items":` + reviewEvidenceSchema + `}},` +
		`"required":["theme","summary","evidence"],"additionalProperties":false}`

	reviewItemsSchema = `{"type":"array","items":` + reviewItemSchema + `}`

	focusAssessmentSchema = `{"type":["object","null"],"properties":` +
		`{"summary":{"type":"string"},` +
		`"evidence":{"type":"array","items":` + reviewEvidenceSchema + `}},` +
		`"required":["summary","evidence"],"additionalProperties":false}`

	reviewSchema = `{"type":"object","properties":` +
		`{"focus_assessment":` + focusAssessmentSchema + `,` +
		`"struggles":` + reviewItemsSchema + `,` +
		`"wins":` + reviewItemsSchema + `,` +
		`"carry_forward":` + reviewItemsSchema + `,` +
		`"focus_recommendation":{"type":"string"}},` +
		`"required":["focus_assessment","struggles","wins","carry_forward","focus_recommendation"],` +
		`"additionalProperties":false}`

	maxPromptAnswerRunes = 500

	// A source review is already a summary, so its body reaches the model at
	// far more length than a single reflection answer does — the arc prompt
	// carries a handful of them, not a hundred entries.
	maxPromptReviewBodyRunes = 2000

	// defaultReviewTimeout bounds a harness run. Ten minutes is long enough for
	// a yearly arc review on a slow model and short enough that a harness which
	// has genuinely hung gives the terminal back the same session.
	defaultReviewTimeout = 10 * time.Minute
)

var (
	reviewWeek    bool
	reviewMonth   string
	reviewQuarter string
	reviewYear    string
	reviewHarness string
	reviewModel   string
	reviewEffort  string
	reviewTimeout = defaultReviewTimeout
)

type harnessName string

const (
	harnessClaude  harnessName = "claude"
	harnessCopilot harnessName = "copilot"
	harnessCodex   harnessName = "codex"

	// harnessAnthropic is not a CLI: it calls the Anthropic API directly
	// through github.com/sglavoie/seneca/review/provider.
	harnessAnthropic harnessName = "anthropic"
	// harnessOpenAI calls the OpenAI Responses API directly, the same way.
	harnessOpenAI harnessName = "openai"
)

// reviewEfforts are the effort levels a provider accepts, lowest first.
var reviewEfforts = []string{"low", "medium", "high", "xhigh", "max"}

type reviewConfig struct {
	Harness harnessName
	Model   string
	// Effort is empty unless one was configured; the provider then applies its
	// own default. CLI harnesses never carry one.
	Effort  string
	Timeout time.Duration
}

// harnessTimeout is the deadline a harness run is given. A zero Timeout means
// the caller built the config without going through the flag — every fake
// harness in the test suite does — and a zero deadline would expire before the
// child process started, so the flag's own default stands in for it.
func (c reviewConfig) harnessTimeout() time.Duration {
	if c.Timeout <= 0 {
		return defaultReviewTimeout
	}
	return c.Timeout
}

// reviewTimeoutFlag parses --timeout and refuses a non-positive value where the
// author can still fix it: at parse time, before a file is read or a harness is
// looked up. A zero or negative deadline is not "no limit" — it is a context
// that has already expired, which would kill the harness before it wrote a byte
// and report a timeout nobody chose.
type reviewTimeoutFlag struct{ value *time.Duration }

func (f reviewTimeoutFlag) String() string {
	if f.value == nil {
		return defaultReviewTimeout.String()
	}
	return f.value.String()
}

func (f reviewTimeoutFlag) Set(raw string) error {
	parsed, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("expected a Go duration such as 15m")
	}
	if parsed <= 0 {
		return fmt.Errorf("expected a positive duration such as 15m, got %s", parsed)
	}
	*f.value = parsed
	return nil
}

func (f reviewTimeoutFlag) Type() string { return "duration" }

type reviewEvidence struct {
	Date    string `json:"date"`
	Snippet string `json:"snippet"`
}

type reviewItem struct {
	Theme    string           `json:"theme"`
	Summary  string           `json:"summary"`
	Evidence []reviewEvidence `json:"evidence"`
}

// focusAssessment is the model's verdict on the focus that was already in
// effect. Evidence may be empty — that is what an honest "not enough to say"
// looks like — but the summary never is.
type focusAssessment struct {
	Summary  string           `json:"summary"`
	Evidence []reviewEvidence `json:"evidence"`
}

type reviewResponse struct {
	FocusAssessment     *focusAssessment `json:"focus_assessment"`
	Struggles           []reviewItem     `json:"struggles"`
	Wins                []reviewItem     `json:"wins"`
	CarryForward        []reviewItem     `json:"carry_forward"`
	FocusRecommendation string           `json:"focus_recommendation"`
}

// periodRank orders the review periods by span, shortest first, so a caller can
// ask "is this review's period strictly shorter than mine?" without hard-coding
// the vocabulary. A period this binary does not know ranks 0 — below every
// period Seneca writes — so an unreadable record can never be mistaken for a
// longer span than the one asking.
func periodRank(period engine.ReviewPeriod) int {
	for i, candidate := range engine.ArchiveReviewPeriods {
		if period == candidate {
			return i + 1
		}
	}
	return 0
}

// reviewSourceKind names what a window feeds the model. Weekly and monthly
// reviews read the reflection document; an arc review reads reviews that are
// already saved. The zero value is the entry kind so every window built before
// arc reviews existed keeps meaning exactly what it meant.
type reviewSourceKind string

const (
	sourceEntries reviewSourceKind = ""
	sourceReviews reviewSourceKind = "reviews"
)

// reviewWindow is the span a review answers for and the evidence gathered for
// it. Exactly one of Entries and Reviews is populated, named by SourceKind.
type reviewWindow struct {
	Title        string
	Period       engine.ReviewPeriod
	PeriodLabel  string
	PromptLabel  string
	Start        time.Time
	End          time.Time
	ExpectedDays int
	CoveredDays  int
	SourceKind   reviewSourceKind
	Entries      []engine.ReflectionEntry
	Reviews      []engine.ReviewRecord
	// LatestBefore is the newest entry dated before Start, so an empty entry
	// window can say where the writing actually is. Arc windows leave it blank.
	LatestBefore string
	// Practice divides the window by the practice pack in force, and is empty
	// when no pack was in force on any of its days.
	Practice []engine.PracticeSpan
}

func (w reviewWindow) startDate() string { return w.Start.Format("2006-01-02") }
func (w reviewWindow) endDate() string   { return w.End.Format("2006-01-02") }

// coverageLine describes how much evidence the window holds, in the terms that
// window is measured in. Day coverage is meaningless for an arc review — a
// quarter is not expected to have ninety entries — so a review-sourced window
// counts its sources instead.
func (w reviewWindow) coverageLine() string {
	if w.SourceKind == sourceReviews {
		if len(w.Reviews) == 1 {
			return "1 source review"
		}
		return fmt.Sprintf("%d source reviews", len(w.Reviews))
	}
	return fmt.Sprintf("Coverage %d/%d days", w.CoveredDays, w.ExpectedDays)
}

// sourceCount is how much evidence the window actually holds, counted in the
// list its own source kind names. Nothing else may ask "how many entries?" of
// an arc window: the answer is always zero and always the wrong question.
func (w reviewWindow) sourceCount() int {
	if w.SourceKind == sourceReviews {
		return len(w.Reviews)
	}
	return len(w.Entries)
}

// emptySourceMessage is what a review with nothing to read says before it stops
// without calling a harness. It names the material that was actually looked
// for, so an author who ran --quarter too early is told about saved reviews
// rather than about entries the arc path never reads.
func (w reviewWindow) emptySourceMessage() string {
	if w.SourceKind == sourceReviews {
		return fmt.Sprintf("No saved reviews found for %s (%s to %s).", strings.ToLower(w.Title), w.startDate(), w.endDate())
	}
	msg := fmt.Sprintf("No entries found for %s (%s to %s).", strings.ToLower(w.Title), w.startDate(), w.endDate())
	if w.LatestBefore != "" {
		msg += fmt.Sprintf(" Latest entry %s; try --month=%s.", w.LatestBefore, w.LatestBefore[:7])
	}
	return msg
}

// citableDates lists every date this window may be cited by, in the terms its
// own source kind is measured in: a reflection is dated by the day it was
// written, a source review by the day its window ended. Review dates are
// deduplicated — a weekly and a monthly can end on the same day, and that day
// grounds once — while entry dates are passed through exactly as before.
func (w reviewWindow) citableDates() []string {
	var dates []string
	if w.SourceKind == sourceReviews {
		seen := make(map[string]bool, len(w.Reviews))
		for _, record := range w.Reviews {
			if record.End == "" || seen[record.End] {
				continue
			}
			seen[record.End] = true
			dates = append(dates, record.End)
		}
		return dates
	}
	for _, entry := range w.Entries {
		if entry.Date != "" {
			dates = append(dates, entry.Date)
		}
	}
	return dates
}

// reviewFocusContext is the focus a review has to answer for: the state in
// effect at the *window's end* — not today's — plus the window sources dated on
// or after it took effect. Only those sources can honestly support a verdict
// about it. timeline is every focus change inside the window, which an arc
// review renders as context: a quarter can hold several focuses, and one
// end-of-period verdict would otherwise hide the churn.
type reviewFocusContext struct {
	state         engine.FocusState
	eligibleDates []string
	timeline      []engine.FocusEvent
}

func (c reviewFocusContext) applies() bool { return c.state.Active }

func (c reviewFocusContext) eligibleDateSet() map[string]bool {
	dates := make(map[string]bool, len(c.eligibleDates))
	for _, date := range c.eligibleDates {
		dates[date] = true
	}
	return dates
}

// focusContextForWindow resolves the focus a review must assess. A missing
// archive simply means no focus; it is never created by this read.
func focusContextForWindow(archivePath string, window reviewWindow) (reviewFocusContext, error) {
	state, events, err := engine.FocusAndTimelineOnDate(archivePath, window.endDate())
	if err != nil {
		// The daily wording sends the author to --blank, which review does not
		// have. A review that cannot read the archive cannot be saved either,
		// so repair is the only way forward.
		var unreadable *engine.UnreadableContextError
		if errors.As(err, &unreadable) {
			unreadable.Purpose = "the focus this review has to assess"
			unreadable.Remedy = "run `seneca doctor` to inspect and repair it"
		}
		return reviewFocusContext{}, err
	}

	// The timeline is window context, not a verdict, so it survives even when no
	// focus was in force at the end: a focus set and then cleared inside the
	// window is exactly the churn an arc review should see.
	timeline := focusEventsWithin(events, window)
	if !state.Active {
		return reviewFocusContext{timeline: timeline}, nil
	}

	ctx := reviewFocusContext{state: state, timeline: timeline}
	for _, date := range window.citableDates() {
		// Dates are YYYY-MM-DD, so a string comparison is a date comparison.
		// For an arc window this keeps a source review whose window ended on or
		// after the focus took effect; window.Entries is empty for one.
		if date >= state.Effective {
			ctx.eligibleDates = append(ctx.eligibleDates, date)
		}
	}
	return ctx, nil
}

// focusEventsWithin narrows the archive's whole focus stream to the changes that
// happened inside the window, keeping creation order.
func focusEventsWithin(events []engine.FocusEvent, window reviewWindow) []engine.FocusEvent {
	start, end := window.startDate(), window.endDate()
	var within []engine.FocusEvent
	for _, event := range events {
		if event.Effective >= start && event.Effective <= end {
			within = append(within, event)
		}
	}
	return within
}

// reviewEvidenceVocabulary names the material a review's evidence may be drawn
// from. A reflection is dated by the day it was written and a source review by
// the day its window ended, so calling arc evidence "reflection dates" would
// describe dates the model was never given — in the prompt that asks for the
// citation and, just as much, in the error that refuses one. Both sides read
// their nouns from here so a refusal can never contradict the request.
type reviewEvidenceVocabulary struct {
	missing    string
	dates      string
	material   string
	supplied   string
	ineligible string
}

func evidenceVocabularyFor(source reviewSourceKind) reviewEvidenceVocabulary {
	if source == sourceReviews {
		return reviewEvidenceVocabulary{
			missing:    "No supplied source review ended on or after the focus took effect",
			dates:      "source review end dates",
			material:   "those source reviews",
			supplied:   "the supplied source review end dates",
			ineligible: "a supplied source review ending on or after the focus took effect on",
		}
	}
	return reviewEvidenceVocabulary{
		missing:    "No supplied reflection was written on or after the focus took effect",
		dates:      "reflection dates",
		material:   "those reflections",
		supplied:   "the supplied reflection dates",
		ineligible: "a supplied reflection dated on or after the focus took effect on",
	}
}

// reviewGrounding is what the response is checked against: the dates actually
// supplied to the harness, the focus it was asked to assess, and the kind of
// material those dates name. A model cannot cite a day Seneca never sent it.
type reviewGrounding struct {
	entryDates []string
	focus      reviewFocusContext
	source     reviewSourceKind
}

// newReviewGrounding takes the allow-list from the window itself, so an arc
// review is grounded in its source reviews' end dates and an entry review in
// its reflection dates. The window's source kind rides along so a rejection
// names the material the prompt actually supplied; the check in
// parseReviewResponse is untouched.
func newReviewGrounding(window reviewWindow, focus reviewFocusContext) reviewGrounding {
	return reviewGrounding{focus: focus, entryDates: window.citableDates(), source: window.SourceKind}
}

// vocabulary is the nouns this grounding's refusals speak in. The zero
// reviewGrounding is an entry review, so every grounding built by hand keeps
// the wording it has always had.
func (g reviewGrounding) vocabulary() reviewEvidenceVocabulary {
	return evidenceVocabularyFor(g.source)
}

func (g reviewGrounding) entryDateSet() map[string]bool {
	dates := make(map[string]bool, len(g.entryDates))
	for _, date := range g.entryDates {
		dates[date] = true
	}
	return dates
}
