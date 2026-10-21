package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestNarrowAndShortWindowsStillLeaveRoomForTheContextBlock(t *testing.T) {
	t.Parallel()

	m := sized(continuityForm(t), 30, 24)

	lines := m.contextLines(30)
	if len(lines) != 2 {
		t.Fatalf("expected focus and intention lines, got %v", lines)
	}
	for _, line := range lines {
		if got := lipgloss.Width(line); got > 30 {
			t.Fatalf("expected the context line to fit 30 columns, got %d in %q", got, line)
		}
	}
	// Two context lines plus their blank line come out of the textarea budget.
	plain := sized(newReflectionModel(reflectionTUIConfig{
		questions: continuityQuestions,
		date:      "2026-08-24",
	}, false), 30, 24)
	if got, bare := m.textareas[0].Height(), plain.textareas[0].Height(); got != bare-3 {
		t.Fatalf("expected the context block to cost 3 rows, got %d against %d", got, bare)
	}

	// A window too short for any of it clamps instead of going negative.
	tiny := sized(continuityForm(t), 12, 4)
	if got := tiny.textareas[0].Height(); got != 5 {
		t.Fatalf("expected the minimum textarea height, got %d", got)
	}
	if view := tiny.View(); view.Content == "" {
		t.Fatal("expected a tiny window to still render")
	}
}

func TestReviewScrollingClampsToTheRenderedBody(t *testing.T) {
	t.Parallel()

	m := sized(continuityForm(t), 60, 8)
	m = typeInto(m, "A fairly long follow-through response that wraps.")
	m = send(m, key("ctrl+s"))

	total := m.reviewContentLines()
	if total != len(strings.Split(m.reviewBody(m.reviewWrapWidth()), "\n")) {
		t.Fatal("expected the clamp to count the same body the view renders")
	}
	if total <= m.height-1 {
		t.Fatalf("expected the body to overflow an 8-row window, got %d lines", total)
	}

	// Scrolling up at the top is a no-op.
	if got := send(m, key("k")); got.scrollOffset != 0 {
		t.Fatalf("expected no scroll above the top, got %d", got.scrollOffset)
	}

	scrolled := m
	for range total + 5 {
		scrolled = send(scrolled, key("j"))
	}
	maxOffset := total - (m.height - 1)
	if scrolled.scrollOffset != maxOffset {
		t.Fatalf("expected the offset clamped to %d, got %d", maxOffset, scrolled.scrollOffset)
	}
	if !strings.Contains(scrolled.View().Content, "↑/k up") {
		t.Fatal("expected scroll hints in the footer when the body overflows")
	}

	// Returning to editing resets the offset.
	if resumed := send(scrolled, key("e")); resumed.scrollOffset != 0 {
		t.Fatalf("expected editing to reset the scroll offset, got %d", resumed.scrollOffset)
	}
}

func TestContextViewerRendersCompleteMultilineValuesAtTerminalWidth(t *testing.T) {
	t.Parallel()

	focus := "Protect an uninterrupted first hour for the archive migration, even when the inbox feels urgent.\n\nThen write down what changed."
	intention := "Finish the parser and validate every malformed record.\nKeep the original bytes available for comparison."
	m := newReflectionModel(reflectionTUIConfig{
		questions: continuityQuestions,
		date:      "2026-08-24",
		context: engine.ContinuityContext{
			Focus:             focus,
			PriorDate:         "2026-08-18",
			PriorText:         intention,
			ShowFollowThrough: true,
		},
	}, false)
	m = send(sized(m, 32, 30), key("ctrl+o"))

	if m.phase != phaseContext {
		t.Fatal("expected Ctrl+O to open the context viewer")
	}
	body := m.contextBody(m.contextWrapWidth())
	for _, want := range []string{
		"Focus",
		"Intention 2026-08-18",
		"feels urgent.",
		"Then write down what changed.",
		"every malformed record.",
		"Keep the original bytes",
		"available for comparison.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected complete context fragment %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "…") {
		t.Fatalf("expected no ellipses in the complete context viewer:\n%s", body)
	}
	if !strings.Contains(body, "urgent.\n\nThen") {
		t.Fatalf("expected the focus paragraph break to be preserved:\n%s", body)
	}
	for _, line := range strings.Split(body, "\n") {
		if width := lipgloss.Width(line); width > m.contextWrapWidth() {
			t.Fatalf("expected line width <= %d, got %d in %q", m.contextWrapWidth(), width, line)
		}
	}
}

func TestContextViewerReturnsToEditingWithoutChangingEditorState(t *testing.T) {
	t.Parallel()

	for _, closeKey := range []string{"esc", "ctrl+o"} {
		t.Run(closeKey, func(t *testing.T) {
			m := sized(continuityForm(t), 60, 12)
			m = typeInto(m, "First field")
			m = send(m, key("tab"))
			m = typeInto(m, "Second field")

			active := m.active
			value := m.textareas[active].Value()
			line := m.textareas[active].Line()
			column := m.textareas[active].Column()
			m = send(m, key("ctrl+o"), key("j"), key(closeKey))

			if m.phase != phaseEditing || m.active != active {
				t.Fatalf("expected editing field %d after closing context, got phase %v field %d", active, m.phase, m.active)
			}
			if got := m.textareas[active].Value(); got != value {
				t.Fatalf("expected editor value %q to survive, got %q", value, got)
			}
			if m.textareas[active].Line() != line || m.textareas[active].Column() != column {
				t.Fatalf("expected cursor at %d:%d, got %d:%d", line, column, m.textareas[active].Line(), m.textareas[active].Column())
			}
			if !m.textareas[active].Focused() {
				t.Fatal("expected the active textarea to retain focus")
			}
		})
	}
}

func TestContextViewerKeepsReviewPhaseAndScrollPosition(t *testing.T) {
	t.Parallel()

	m := sized(continuityForm(t), 50, 8)
	m = typeInto(m, "A long enough answer to enter the review screen and make its body overflow.")
	m = send(m, key("ctrl+s"), key("j"), key("j"), key("j"))
	wantOffset := m.scrollOffset
	if wantOffset == 0 {
		t.Fatal("expected review scrolling before opening context")
	}
	if !strings.Contains(m.View().Content, "Ctrl+O context") {
		t.Fatal("expected the review footer to advertise context")
	}

	m = send(m, key("ctrl+o"), key("j"))
	if m.phase != phaseContext {
		t.Fatal("expected the context phase from review")
	}
	m = sized(m, 50, 12)
	if m.scrollOffset != wantOffset {
		t.Fatalf("expected review offset %d to remain separate, got %d", wantOffset, m.scrollOffset)
	}
	m = send(m, key("ctrl+o"))
	if m.phase != phaseReviewing || m.scrollOffset != wantOffset {
		t.Fatalf("expected review at offset %d after context, got phase %v offset %d", wantOffset, m.phase, m.scrollOffset)
	}
}

func TestContextScrollingClampsAtBoundariesAndAfterResize(t *testing.T) {
	t.Parallel()

	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("Focus detail %02d", i)
	}
	m := newReflectionModel(reflectionTUIConfig{
		questions: continuityQuestions,
		date:      "2026-08-24",
		context: engine.ContinuityContext{
			Focus: strings.Join(lines, "\n"),
		},
	}, false)
	m = send(sized(m, 30, 6), key("ctrl+o"), key("k"))
	if m.contextScrollOffset != 0 {
		t.Fatalf("expected scrolling above the top to clamp at 0, got %d", m.contextScrollOffset)
	}

	total := m.contextContentLines()
	for range total + 10 {
		m = send(m, key("j"))
	}
	wantMax := maxScrollOffset(total, m.height)
	if m.contextScrollOffset != wantMax {
		t.Fatalf("expected context offset clamped to %d, got %d", wantMax, m.contextScrollOffset)
	}
	if !strings.Contains(m.View().Content, "Focus detail 39") {
		t.Fatal("expected scrolling to reach the complete end of the context")
	}

	m = sized(m, 30, 60)
	if m.contextScrollOffset != 0 {
		t.Fatalf("expected resize to clamp the now-short context to the top, got %d", m.contextScrollOffset)
	}
}

func TestContextShortcutAvailabilityMatchesContinuity(t *testing.T) {
	t.Parallel()

	variants := []struct {
		name        string
		context     engine.ContinuityContext
		wantHeading string
	}{
		{name: "focus only", context: engine.ContinuityContext{Focus: "Protect the morning."}, wantHeading: "Focus"},
		{name: "intention only", context: engine.ContinuityContext{PriorDate: "2026-08-18", PriorText: "Ship the parser.", ShowFollowThrough: true}, wantHeading: "Intention 2026-08-18"},
		{name: "intention with age", context: engine.ContinuityContext{PriorDate: "2026-08-18", PriorAge: "6 days ago", PriorText: "Ship the parser.", ShowFollowThrough: true}, wantHeading: "Intention 2026-08-18 (6 days ago)"},
	}
	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			m := sized(newReflectionModel(reflectionTUIConfig{
				questions: continuityQuestions,
				date:      "2026-08-24",
				context:   tc.context,
			}, false), 80, 30)
			if !strings.Contains(m.View().Content, "Ctrl+O context") {
				t.Fatal("expected the editing footer to advertise context")
			}
			m = send(m, key("ctrl+o"))
			if !strings.Contains(m.View().Content, tc.wantHeading) {
				t.Fatalf("expected context heading %q", tc.wantHeading)
			}
		})
	}

	blank := sized(newReflectionModel(reflectionTUIConfig{
		questions: continuityQuestions,
		date:      "2026-08-24",
	}, false), 80, 30)
	if strings.Contains(blank.View().Content, "Ctrl+O context") {
		t.Fatal("expected a context-free editing footer to omit the shortcut")
	}
	blank = send(blank, key("ctrl+o"))
	if blank.phase != phaseEditing {
		t.Fatal("expected context-free editing to ignore Ctrl+O")
	}
	blank = typeInto(blank, "answer")
	blank = send(blank, key("ctrl+s"))
	if strings.Contains(blank.View().Content, "Ctrl+O context") {
		t.Fatal("expected a context-free review footer to omit the shortcut")
	}
	blank = send(blank, key("ctrl+o"))
	if blank.phase != phaseReviewing {
		t.Fatal("expected context-free review to ignore Ctrl+O")
	}
}
