package senecabridge

import "github.com/sglavoie/seneca/engine"

// Follow-through insights: what the engine reports of the intentions and the
// focus over a window of days, as a view. Nothing is computed here.

type insightsParams struct {
	// Days is the window ending today, today included; 30 when neither it
	// nor From is given.
	Days int `json:"days"`
	// From and To bound the window instead, inclusive. An empty To is today.
	From string `json:"from"`
	To   string `json:"to"`
}

type intentionOutcome struct {
	Date       string   `json:"date"`
	Text       string   `json:"text"`
	Status     string   `json:"status"`
	CarriedOn  []string `json:"carried_on"`
	AnsweredOn string   `json:"answered_on"`
	Response   string   `json:"response"`
}

type datedWords struct {
	Text  string   `json:"text"`
	Dates []string `json:"dates"`
}

type focusSpan struct {
	Event          string `json:"event"`
	Text           string `json:"text"`
	Origin         string `json:"origin"`
	From           string `json:"from"`
	Until          string `json:"until"`
	Days           int    `json:"days"`
	ReplacedBy     string `json:"replaced_by"`
	ReplacedByText string `json:"replaced_by_text"`
}

type insightsResult struct {
	From           string             `json:"from"`
	To             string             `json:"to"`
	Intentions     []intentionOutcome `json:"intentions"`
	Answered       int                `json:"answered"`
	Unanswered     int                `json:"unanswered"`
	NotCarried     int                `json:"not_carried"`
	Open           int                `json:"open"`
	Untracked      int                `json:"untracked"`
	Tracked        string             `json:"tracked"`
	Repeated       []datedWords       `json:"repeated"`
	RecurringWords []datedWords       `json:"recurring_words"`
	Focus          []focusSpan        `json:"focus"`
	FocusProblem   string             `json:"focus_problem"`
}

func getInsights(c *call) (any, error) {
	p, err := params[insightsParams](c)
	if err != nil {
		return nil, err
	}
	for _, date := range []string{p.From, p.To} {
		if date != "" && !engine.IsCalendarDate(date) {
			return nil, refuse("invalid-query", "%q is not a calendar date (YYYY-MM-DD)", date)
		}
	}
	switch {
	case p.Days < 0:
		return nil, refuse("invalid-query", "days must be positive, not %d", p.Days)
	case p.Days > 0 && (p.From != "" || p.To != ""):
		return nil, refuse("invalid-query", "days and from/to are two windows; give one")
	case p.From != "" && p.To != "" && p.From > p.To:
		return nil, refuse("invalid-query", "from %s is after to %s", p.From, p.To)
	}
	from, to := p.From, p.To
	if from == "" && to == "" {
		days := p.Days
		if days == 0 {
			days = engine.InsightsDays
		}
		from, to = engine.InsightsWindow(c.journal.Today(), days)
	}
	report, err := c.journal.Insights(from, to)
	if err != nil {
		return nil, err
	}
	return insightsResultOf(report), nil
}

func insightsResultOf(report engine.Insights) insightsResult {
	result := insightsResult{
		From: report.From, To: report.To,
		Answered: report.Answered, Unanswered: report.Unanswered, NotCarried: report.NotCarried,
		Open: report.Open, Untracked: report.Untracked, Tracked: report.Tracked,
		Intentions:     make([]intentionOutcome, 0, len(report.Intentions)),
		Repeated:       make([]datedWords, 0, len(report.Repeated)),
		RecurringWords: make([]datedWords, 0, len(report.RecurringWords)),
		Focus:          make([]focusSpan, 0, len(report.Focus)),
		FocusProblem:   report.FocusProblem,
	}
	for _, intention := range report.Intentions {
		result.Intentions = append(result.Intentions, intentionOutcome{
			Date: intention.Date, Text: intention.Text, Status: string(intention.Status),
			CarriedOn: append([]string{}, intention.CarriedOn...), AnsweredOn: intention.AnsweredOn, Response: intention.Response,
		})
	}
	for _, repeated := range report.Repeated {
		result.Repeated = append(result.Repeated, datedWords{Text: repeated.Text, Dates: repeated.Dates})
	}
	for _, word := range report.RecurringWords {
		result.RecurringWords = append(result.RecurringWords, datedWords{Text: word.Word, Dates: word.Dates})
	}
	for _, span := range report.Focus {
		result.Focus = append(result.Focus, focusSpan{
			Event: string(span.Event), Text: span.Text, Origin: string(span.Origin), From: span.From, Until: span.Until,
			Days: span.Days, ReplacedBy: string(span.ReplacedBy), ReplacedByText: span.ReplacedByText,
		})
	}
	return result
}
