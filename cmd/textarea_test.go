package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTruncateLineCapsLongQuestionTitle(t *testing.T) {
	t.Parallel()

	got := truncateLine("1234567890", 5)

	if got != "1234…" {
		t.Fatalf("expected truncated line, got %q", got)
	}
}

// key builds the KeyPressMsg for the small set of keystrokes the form binds.
func key(name string) tea.KeyPressMsg {
	switch name {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	case "ctrl+d":
		return tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+o":
		return tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}
	default:
		runes := []rune(name)
		return tea.KeyPressMsg{Code: runes[0], Text: name}
	}
}

func send(m reflectionModel, msgs ...tea.Msg) reflectionModel {
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(reflectionModel)
	}
	return m
}

func sized(m reflectionModel, width, height int) reflectionModel {
	return send(m, tea.WindowSizeMsg{Width: width, Height: height})
}

func typeInto(m reflectionModel, text string) reflectionModel {
	for _, r := range text {
		m = send(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func continuityForm(t *testing.T) reflectionModel {
	t.Helper()

	return newReflectionModel(reflectionTUIConfig{
		questions: continuityQuestions,
		date:      "2026-08-24",
		context: engine.ContinuityContext{
			Focus:             "Protect the first hour.",
			PriorDate:         "2026-08-18",
			PriorText:         "Finish the migration.",
			ShowFollowThrough: true,
		},
	}, false)
}

func TestFormPutsFollowThroughFirstAndTabsThroughEveryField(t *testing.T) {
	t.Parallel()

	m := sized(continuityForm(t), 100, 40)

	if len(m.fields) != len(continuityQuestions)+1 {
		t.Fatalf("expected one field per question plus the follow-through, got %d", len(m.fields))
	}
	if m.fields[0].kind != fieldFollowThrough || m.fields[0].questionIndex != -1 {
		t.Fatalf("expected the follow-through first, got %+v", m.fields[0])
	}
	if !strings.Contains(m.fields[0].title, "2026-08-18") {
		t.Fatalf("expected the prompt to name the source date, got %q", m.fields[0].title)
	}
	for i, question := range continuityQuestions {
		field := m.fields[i+1]
		if field.kind != fieldQuestion || field.title != question || field.questionIndex != i {
			t.Fatalf("expected configured question %d after the follow-through, got %+v", i, field)
		}
	}

	// Tab wraps forward through every field, shift+tab back.
	for i := range m.fields {
		if m.active != i {
			t.Fatalf("expected field %d active, got %d", i, m.active)
		}
		m = send(m, key("tab"))
	}
	if m.active != 0 {
		t.Fatalf("expected tab to wrap to the first field, got %d", m.active)
	}
	m = send(m, key("shift+tab"))
	if m.active != len(m.fields)-1 {
		t.Fatalf("expected shift+tab to wrap to the last field, got %d", m.active)
	}
}

func TestFormRoutesTypingToTheActiveFieldAndSplitsTheResultByRole(t *testing.T) {
	t.Parallel()

	m := sized(continuityForm(t), 100, 40)
	m = typeInto(m, "It took the morning.")
	m = send(m, key("tab"))
	m = typeInto(m, "Rushed.")
	m = send(m, key("tab"), key("tab"))
	m = typeInto(m, "Start smaller.")
	m = send(m, key("ctrl+s"), key("enter"))

	if !m.saved {
		t.Fatal("expected Enter in the review phase to save")
	}
	got := m.collectResult(len(continuityQuestions))
	if got.FollowThrough != "It took the morning." {
		t.Fatalf("expected the follow-through response, got %q", got.FollowThrough)
	}
	want := []string{"Rushed.", "", "Start smaller."}
	if !equalStrings(got.Answers, want) {
		t.Fatalf("expected answers %q, got %q", want, got.Answers)
	}
}

func TestFormWithoutContinuityKeepsOneFieldPerQuestion(t *testing.T) {
	t.Parallel()

	m := sized(newReflectionModel(reflectionTUIConfig{
		questions: continuityQuestions,
		date:      "2026-08-24",
		initial:   []string{"Noticed.", "Handled.", "Ship the parser."},
	}, false), 100, 40)

	if len(m.fields) != len(continuityQuestions) {
		t.Fatalf("expected no follow-through field, got %d fields", len(m.fields))
	}
	if lines := m.contextLines(100); len(lines) != 0 {
		t.Fatalf("expected no context block, got %v", lines)
	}
	got := send(m, key("ctrl+s"), key("enter")).collectResult(len(continuityQuestions))
	if !equalStrings(got.Answers, []string{"Noticed.", "Handled.", "Ship the parser."}) {
		t.Fatalf("expected the initial values preserved, got %q", got.Answers)
	}
}

func TestFormSeedsTheFollowThroughWithTheStoredResponse(t *testing.T) {
	t.Parallel()

	m := newReflectionModel(reflectionTUIConfig{
		questions: continuityQuestions,
		date:      "2026-08-24",
		initial:   []string{"Noticed.", "", ""},
		context: engine.ContinuityContext{
			PriorDate:         "2026-08-18",
			PriorText:         "Finish the migration.",
			FollowInitial:     "First pass.",
			ShowFollowThrough: true,
		},
	}, false)
	m.saved = true

	if got := m.collectResult(len(continuityQuestions)); got.FollowThrough != "First pass." {
		t.Fatalf("expected the stored response seeded, got %q", got.FollowThrough)
	}
}

func TestSaveTransitionsThroughReviewAndDiscardsOnEscape(t *testing.T) {
	t.Parallel()

	m := sized(continuityForm(t), 100, 40)
	m = typeInto(m, "Wrote it down.")

	m = send(m, key("ctrl+s"))
	if m.phase != phaseReviewing {
		t.Fatal("expected Ctrl+S to enter the review phase")
	}
	if m.saved {
		t.Fatal("expected nothing saved until Enter confirms")
	}

	// `e` returns to editing without losing anything.
	back := send(m, key("e"))
	if back.phase != phaseEditing || back.saved {
		t.Fatalf("expected `e` to resume editing, got phase %v saved %v", back.phase, back.saved)
	}
	if got := back.textareas[0].Value(); got != "Wrote it down." {
		t.Fatalf("expected the text to survive the round trip, got %q", got)
	}

	if discarded := send(m, key("esc"), key("y")); discarded.saved {
		t.Fatal("expected Esc then y in the review phase to discard")
	}
	if saved := send(m, key("enter")); !saved.saved {
		t.Fatal("expected Enter in the review phase to save")
	}
}

func TestReviewPhaseConfirmsWithTheSameKeyThatOpenedIt(t *testing.T) {
	t.Parallel()

	for _, confirm := range []string{"ctrl+s", "ctrl+d"} {
		m := sized(continuityForm(t), 100, 40)
		m = typeInto(m, "Wrote it down.")
		m = send(m, key("ctrl+s"))
		if m.phase != phaseReviewing {
			t.Fatalf("expected the review phase before pressing %s", confirm)
		}
		if saved := send(m, key(confirm)); !saved.saved {
			t.Fatalf("expected %s in the review phase to save", confirm)
		}
	}
}

func TestReviewCopyDoesNotClaimTheReflectionIsSavedYet(t *testing.T) {
	t.Parallel()

	m := sized(continuityForm(t), 100, 40)
	m = typeInto(m, "Wrote it down.")
	m = send(m, key("ctrl+s"))

	body := m.reviewBody(m.reviewWrapWidth())
	if strings.Contains(body, "Reflection saved") {
		t.Fatalf("expected the review header not to claim a save:\n%s", body)
	}
	if !strings.Contains(body, "Review before saving") {
		t.Fatalf("expected review-phase copy, got:\n%s", body)
	}
	// The context the answers respond to is visible while reviewing them.
	if !strings.Contains(body, "Protect the first hour.") || !strings.Contains(body, "Finish the migration.") {
		t.Fatalf("expected focus and prior intention in the review body:\n%s", body)
	}
	if !strings.Contains(body, "Wrote it down.") {
		t.Fatalf("expected the follow-through response in the review body:\n%s", body)
	}
	for _, question := range continuityQuestions {
		if !strings.Contains(body, question) {
			t.Fatalf("expected question %q in the review body:\n%s", question, body)
		}
	}
}

func TestEmptyFormSkipsReviewAndQuitsForNothingToRecord(t *testing.T) {
	t.Parallel()

	m := sized(continuityForm(t), 100, 40)
	m = send(m, key("ctrl+s"))

	// The quoted source intention is on screen but is not an answer, so the
	// form must still short-circuit past the review.
	if m.phase != phaseEditing {
		t.Fatal("expected an empty form to bypass the review phase")
	}
	if !m.saved {
		t.Fatal("expected the empty form to quit through the save path")
	}
	if got := m.collectResult(len(continuityQuestions)); !got.Empty() {
		t.Fatalf("expected an empty result, got %+v", got)
	}

	// One character in the follow-through is enough to earn the review screen.
	typed := send(typeInto(sized(continuityForm(t), 100, 40), "x"), key("ctrl+s"))
	if typed.phase != phaseReviewing {
		t.Fatal("expected follow-through text alone to reach the review phase")
	}
}

func TestEscapeInEditingCancelsWithoutSaving(t *testing.T) {
	t.Parallel()

	m := send(typeInto(sized(continuityForm(t), 100, 40), "Wrote it down."), key("esc"), key("y"))
	if m.saved {
		t.Fatal("expected Esc to cancel")
	}
	if got := m.collectResult(len(continuityQuestions)); got.Saved || got.Answers != nil {
		t.Fatalf("expected a cancelled result to carry no answers, got %+v", got)
	}
}

func TestLeavingWithUnsavedWritingAsksFirst(t *testing.T) {
	t.Parallel()

	for _, leave := range []string{"esc", "ctrl+c"} {
		m := typeInto(sized(continuityForm(t), 100, 40), "Wrote it down.")
		m = send(m, key(leave))
		if !m.confirmingDiscard {
			t.Fatalf("expected %s with unsaved writing to ask before discarding", leave)
		}
		if !strings.Contains(m.View().Content, "Discard what you wrote?") {
			t.Fatalf("expected the discard prompt in the footer after %s", leave)
		}

		// Typing while the prompt is up must not reach the textarea.
		kept := send(m, key("x"), key("n"))
		if kept.confirmingDiscard || kept.phase != phaseEditing {
			t.Fatalf("expected n to return to editing, got confirming=%v phase=%v", kept.confirmingDiscard, kept.phase)
		}
		if got := kept.textareas[0].Value(); got != "Wrote it down." {
			t.Fatalf("expected the writing to survive, got %q", got)
		}
		if kept := send(m, key("esc")); kept.confirmingDiscard {
			t.Fatal("expected Esc on the prompt to keep writing")
		}
	}
}

func TestSecondCtrlCDiscardsWithoutSaving(t *testing.T) {
	t.Parallel()

	m := typeInto(sized(continuityForm(t), 100, 40), "Wrote it down.")
	m = send(m, key("ctrl+c"), key("ctrl+c"))
	if m.saved {
		t.Fatal("expected a second Ctrl+C to discard")
	}
	if got := m.collectResult(len(continuityQuestions)); got.Saved {
		t.Fatalf("expected a discarded result, got %+v", got)
	}
}

func TestLeavingAnUntouchedFormDoesNotAsk(t *testing.T) {
	t.Parallel()

	// Reopening a saved day prefills it; leaving without changes loses nothing.
	m := newReflectionModel(reflectionTUIConfig{
		questions: continuityQuestions,
		date:      "2026-08-20",
		initial:   []string{"Already written."},
	}, true)
	m = send(sized(m, 100, 40), key("esc"))
	if m.confirmingDiscard {
		t.Fatal("expected Esc on an unchanged form to leave without asking")
	}
}

func TestReviewPhaseAsksBeforeDiscarding(t *testing.T) {
	t.Parallel()

	m := send(typeInto(sized(continuityForm(t), 100, 40), "Wrote it down."), key("ctrl+s"), key("esc"))
	if !m.confirmingDiscard || m.phase != phaseReviewing {
		t.Fatalf("expected Esc on the review screen to ask, got confirming=%v phase=%v", m.confirmingDiscard, m.phase)
	}
	if !strings.Contains(m.View().Content, "Discard what you wrote?") {
		t.Fatal("expected the discard prompt on the review screen")
	}
	if back := send(m, key("n")); back.confirmingDiscard || back.phase != phaseReviewing {
		t.Fatal("expected n to return to the review screen")
	}
}
