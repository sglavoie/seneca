package engine

import (
	"reflect"
	"testing"
)

var insightQuestions = ReflectionSettings{Questions: []string{"What happened?", "What will I do tomorrow?"}, AnchorQuestionIndex: 2}

// insightEntry is a day with an intention, answering `source` with
// `response` when source is set.
func insightEntry(date, intention, source, response string) ReflectionEntry {
	entry := BuildReflectionEntry(date, insightQuestions.Questions, []string{"Synthetic day.", intention}, insightQuestions.AnchorQuestionIndex)
	if source != "" {
		entry.SetFollowThrough(FollowThrough{SourceDate: source, SourceIntention: "quoted", Response: response})
	}
	entry.upgradeToV2()
	return entry
}

func TestInsightsSayWhatBecameOfEachIntention(t *testing.T) {
	entries := []ReflectionEntry{
		insightEntry("2026-10-01", "Walk after lunch.", "", ""),
		insightEntry("2026-10-02", "Call the bank.", "2026-10-01", "Walked twenty minutes."),
		// The bank was carried but nothing was said about it.
		insightEntry("2026-10-03", "Read before bed.", "2026-10-02", ""),
		// Written without context: the reading was never carried.
		insightEntry("2026-10-04", "Walk after lunch!", "", ""),
		// Nothing after the 5th yet.
		insightEntry("2026-10-05", "walk   after LUNCH", "2026-10-04", "Did it."),
	}
	report := BuildInsights(entries, insightQuestions, nil, "", "", "2026-10-06")

	want := map[string]IntentionStatus{
		"2026-10-01": IntentionAnswered,
		"2026-10-02": IntentionUnanswered,
		"2026-10-03": IntentionNotCarried,
		"2026-10-04": IntentionAnswered,
		"2026-10-05": IntentionOpen,
	}
	for _, intention := range report.Intentions {
		if intention.Status != want[intention.Date] {
			t.Errorf("%s: status %s, want %s", intention.Date, intention.Status, want[intention.Date])
		}
	}
	if len(report.Intentions) != 5 || report.Answered != 2 || report.Unanswered != 1 || report.NotCarried != 1 || report.Open != 1 {
		t.Fatalf("counts %d answered, %d unanswered, %d not carried, %d open of %d",
			report.Answered, report.Unanswered, report.NotCarried, report.Open, len(report.Intentions))
	}
	first := report.Intentions[0]
	if first.AnsweredOn != "2026-10-02" || first.Response != "Walked twenty minutes." || !reflect.DeepEqual(first.CarriedOn, []string{"2026-10-02"}) {
		t.Errorf("first intention: %+v", first)
	}
	if report.From != "2026-10-01" || report.To != "" {
		t.Errorf("whole history window %q to %q", report.From, report.To)
	}

	if len(report.Repeated) != 1 {
		t.Fatalf("repeated: %+v", report.Repeated)
	}
	repeated := report.Repeated[0]
	if repeated.Text != "walk   after LUNCH" || !reflect.DeepEqual(repeated.Dates, []string{"2026-10-01", "2026-10-04", "2026-10-05"}) {
		t.Errorf("the same words in three wordings are one intention: %+v", repeated)
	}
	// "walk" and "lunch" are in three days' intentions; "after" is too
	// common to say anything.
	var words []string
	for _, word := range report.RecurringWords {
		words = append(words, word.Word)
	}
	if !reflect.DeepEqual(words, []string{"lunch", "walk"}) {
		t.Errorf("recurring words %v", words)
	}
}

func TestIntentionsBeforeTheJournalCarriedAnyAreUntracked(t *testing.T) {
	entries := []ReflectionEntry{
		insightEntry("2026-03-01", "Written before follow-through existed.", "", ""),
		insightEntry("2026-03-02", "The first one carried.", "", ""),
		insightEntry("2026-03-03", "Then one left behind.", "2026-03-02", "Done."),
		insightEntry("2026-03-04", "And the last.", "", ""),
	}
	report := BuildInsights(entries, insightQuestions, nil, "", "", "2026-03-05")
	if report.Tracked != "2026-03-02" {
		t.Errorf("tracked from %q", report.Tracked)
	}
	got := []IntentionStatus{}
	for _, intention := range report.Intentions {
		got = append(got, intention.Status)
	}
	want := []IntentionStatus{IntentionUntracked, IntentionAnswered, IntentionNotCarried, IntentionOpen}
	if !reflect.DeepEqual(got, want) || report.Untracked != 1 || report.NotCarried != 1 {
		t.Errorf("statuses %v, untracked %d, not carried %d", got, report.Untracked, report.NotCarried)
	}

	// A journal that never carried one tracks nothing.
	never := BuildInsights(entries[:2], insightQuestions, nil, "", "", "2026-03-05")
	if never.Untracked != 1 || never.Open != 1 || never.Tracked != "" {
		t.Errorf("never carried: %+v", never)
	}
}

func TestInsightsCoverOnlyTheWindowButJudgeOnLaterDays(t *testing.T) {
	entries := []ReflectionEntry{
		insightEntry("2026-09-01", "Before the window.", "", ""),
		insightEntry("2026-10-10", "Inside the window.", "2026-09-01", "Done."),
		insightEntry("2026-10-20", "After the window.", "2026-10-10", "Answered after the window."),
	}
	report := BuildInsights(entries, insightQuestions, nil, "2026-10-01", "2026-10-15", "2026-10-20")
	if len(report.Intentions) != 1 || report.Intentions[0].Date != "2026-10-10" {
		t.Fatalf("intentions %+v", report.Intentions)
	}
	if report.Intentions[0].Status != IntentionAnswered || report.Intentions[0].AnsweredOn != "2026-10-20" {
		t.Errorf("an answer written after the window still counts: %+v", report.Intentions[0])
	}
}

func TestInsightsWindowCountsTodayAsTheLastDay(t *testing.T) {
	from, to := InsightsWindow("2026-10-17", InsightsDays)
	if from != "2026-09-18" || to != "2026-10-17" {
		t.Errorf("window %s to %s", from, to)
	}
}

func TestFocusSpansSayHowLongEachStoodAndWhatReplacedIt(t *testing.T) {
	events := []FocusEvent{
		{Event: FocusEventSet, Effective: "2026-09-01", Origin: FocusOriginManual, Text: "Patience in traffic"},
		{Event: FocusEventSet, Effective: "2026-09-15", Origin: FocusOriginReview, Text: "One thing at a time"},
		{Event: FocusEventClear, Effective: "2026-10-01", Origin: FocusOriginManual},
		{Event: FocusEventSet, Effective: "2026-10-10", Origin: FocusOriginManual, Text: "Walk daily"},
	}
	spans := FocusSpans(events, "2026-10-17")
	if len(spans) != 4 {
		t.Fatalf("spans %+v", spans)
	}
	if spans[0].Days != 14 || spans[0].Until != "2026-09-15" || spans[0].ReplacedBy != FocusEventSet || spans[0].ReplacedByText != "One thing at a time" {
		t.Errorf("first span %+v", spans[0])
	}
	if spans[1].ReplacedBy != FocusEventClear || spans[1].Days != 16 {
		t.Errorf("a focus cleared: %+v", spans[1])
	}
	if !spans[3].Current() || spans[3].Days != 7 {
		t.Errorf("the current focus counts to today: %+v", spans[3])
	}

	// Only the spans overlapping the window.
	report := BuildInsights(nil, insightQuestions, events, "2026-10-05", "2026-10-17", "2026-10-17")
	if len(report.Focus) != 2 || report.Focus[0].Event != FocusEventClear || report.Focus[1].Text != "Walk daily" {
		t.Errorf("focus in the window %+v", report.Focus)
	}
}
