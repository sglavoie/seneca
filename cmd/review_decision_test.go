package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

const decisionRecommendation = "Start the hardest task before checking messages."

func decisionModel() reviewDecisionModel {
	body := "Weekly review\n2026-08-17 to 2026-08-23\n\nPrevious focus\nShip the archive layer."
	m := newReviewDecisionModel(body, decisionRecommendation, false)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return next.(reviewDecisionModel)
}

func pressDecision(m reviewDecisionModel, keys ...string) reviewDecisionModel {
	for _, name := range keys {
		next, _ := m.Update(key(name))
		m = next.(reviewDecisionModel)
	}
	return m
}

func typeDecision(m reviewDecisionModel, text string) reviewDecisionModel {
	for _, r := range text {
		next, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next.(reviewDecisionModel)
	}
	return m
}

func TestReviewDecisionMenuOffersAllFourOutcomes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		keys      []string
		wantKind  reviewDecisionKind
		wantFocus string
		wantSaves bool
	}{
		{keys: []string{"a"}, wantKind: reviewAcceptFocus, wantFocus: decisionRecommendation, wantSaves: true},
		{keys: []string{"s"}, wantKind: reviewSaveWithoutFocus, wantSaves: true},
		{keys: []string{"d"}, wantKind: reviewDiscard},
		{keys: []string{"esc"}, wantKind: reviewDiscard},
		{keys: []string{"ctrl+c"}, wantKind: reviewDiscard},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(strings.Join(tc.keys, "+"), func(t *testing.T) {
			t.Parallel()

			m := pressDecision(decisionModel(), tc.keys...)
			if m.decision.kind != tc.wantKind {
				t.Fatalf("expected kind %d, got %d", tc.wantKind, m.decision.kind)
			}
			if m.decision.acceptedFocus() != tc.wantFocus {
				t.Fatalf("expected accepted focus %q, got %q", tc.wantFocus, m.decision.acceptedFocus())
			}
			if m.decision.saves() != tc.wantSaves {
				t.Fatalf("expected saves=%v, got %v", tc.wantSaves, m.decision.saves())
			}
		})
	}
}

func TestReviewDecisionEditStartsPrefilledAndReturnsTheRewrite(t *testing.T) {
	t.Parallel()

	m := pressDecision(decisionModel(), "e")
	if m.phase != decisionPhaseEditing {
		t.Fatal("expected the edit phase")
	}
	if m.editor.Value() != decisionRecommendation {
		t.Fatalf("expected the editor prefilled with the recommendation, got %q", m.editor.Value())
	}

	m.editor.SetValue("")
	m = typeDecision(m, "Open the editor before the inbox.")
	m = pressDecision(m, "ctrl+s")

	if m.decision.kind != reviewEditFocus {
		t.Fatalf("expected an edit decision, got %d", m.decision.kind)
	}
	if got := m.decision.acceptedFocus(); got != "Open the editor before the inbox." {
		t.Fatalf("unexpected edited focus %q", got)
	}
}

func TestReviewDecisionRefusesToAdoptABlankFocus(t *testing.T) {
	t.Parallel()

	m := pressDecision(decisionModel(), "e")
	m.editor.SetValue("   ")
	m = pressDecision(m, "ctrl+d")

	if m.decision.saves() {
		t.Fatal("expected a blank focus to be refused rather than saved")
	}
	if m.phase != decisionPhaseEditing {
		t.Fatal("expected to stay in the edit phase")
	}
	if m.editError == "" {
		t.Fatal("expected an explanation of the refusal")
	}

	// Esc returns to the menu with the recommendation restored, so a half-typed
	// edit never leaks into a later accept.
	m = pressDecision(m, "esc")
	if m.phase != decisionPhaseMenu || m.editor.Value() != decisionRecommendation {
		t.Fatalf("expected esc to restore the menu and the recommendation, got phase %d and %q", m.phase, m.editor.Value())
	}
	m = pressDecision(m, "a")
	if m.decision.acceptedFocus() != decisionRecommendation {
		t.Fatalf("expected the untouched recommendation, got %q", m.decision.acceptedFocus())
	}
}

func TestReviewDecisionCtrlCWhileEditingDiscards(t *testing.T) {
	t.Parallel()

	m := pressDecision(decisionModel(), "e")
	m = typeDecision(m, "half a thought")
	m = pressDecision(m, "ctrl+c")

	if m.decision.saves() {
		t.Fatal("expected ctrl+c while editing to discard")
	}
}

// The zero decision is discard, which is what an unanswered prompt resolves to.
func TestZeroReviewDecisionDiscards(t *testing.T) {
	t.Parallel()

	var decision reviewDecision
	if decision.saves() || decision.acceptedFocus() != "" {
		t.Fatalf("expected the zero decision to be a discard, got %+v", decision)
	}
}

func TestDecisionAbortsAreTreatedAsCancellation(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		tea.ErrInterrupted,
		tea.ErrProgramKilled,
		io.EOF,
		fmt.Errorf("reading stdin: %w", io.EOF),
		fmt.Errorf("%w: closed", tea.ErrProgramKilled),
	} {
		if !isDecisionAbort(err) {
			t.Fatalf("expected %v to count as a cancellation", err)
		}
	}
	if isDecisionAbort(errors.New("the terminal caught fire")) {
		t.Fatal("expected a genuine failure not to be swallowed as a cancellation")
	}
}

func TestReviewDecisionViewShowsTheReviewAndTheChoices(t *testing.T) {
	t.Parallel()

	rendered := decisionModel().View().Content

	for _, want := range []string{"Previous focus", "a accept focus", "e edit focus", "s save only", "d discard"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected the decision view to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestReviewDecisionViewScrollsALongReview(t *testing.T) {
	t.Parallel()

	body := make([]string, 0, 60)
	for i := range 60 {
		body = append(body, fmt.Sprintf("line %d", i))
	}
	m := newReviewDecisionModel(strings.Join(body, "\n"), decisionRecommendation, false)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = next.(reviewDecisionModel)

	if !strings.Contains(m.View().Content, "↓/j down") {
		t.Fatal("expected a scroll hint when the review overflows")
	}
	m = pressDecision(m, "j", "j", "j")
	if m.scrollOffset != 3 {
		t.Fatalf("expected a scroll offset of 3, got %d", m.scrollOffset)
	}
	if strings.Contains(m.View().Content, "line 0\n") {
		t.Fatal("expected the first lines to scroll out of view")
	}
	m = pressDecision(m, "k", "k", "k", "k")
	if m.scrollOffset != 0 {
		t.Fatalf("expected scrolling back to the top, got %d", m.scrollOffset)
	}
}
