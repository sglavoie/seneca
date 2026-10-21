package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Nothing about a review reaches disk without the author saying so. The review
// itself is cheap to regenerate; the archive is append-only and the focus
// timeline is the input to every later reflection, so an accidental save is
// expensive in a way an accidental discard is not. Hence four explicit
// decisions, a discard default, and no path where closing the terminal writes
// anything.

type reviewDecisionKind int

const (
	// reviewDiscard writes nothing at all. It is what cancelling, EOF, and
	// every failure short of a disk error resolve to.
	reviewDiscard reviewDecisionKind = iota
	// reviewAcceptFocus saves the review and adopts the recommendation verbatim.
	reviewAcceptFocus
	// reviewEditFocus saves the review and adopts the author's rewrite of it.
	reviewEditFocus
	// reviewSaveWithoutFocus saves the review and leaves the focus timeline
	// untouched, so whatever was already active stays active.
	reviewSaveWithoutFocus
)

// reviewDecision is the author's answer. focusText is populated for accept and
// edit alike, so persistence never has to ask which one it came from.
type reviewDecision struct {
	kind      reviewDecisionKind
	focusText string
}

func (d reviewDecision) saves() bool { return d.kind != reviewDiscard }

// acceptedFocus is the text to record as the new focus, empty when the decision
// changes no focus.
func (d reviewDecision) acceptedFocus() string {
	switch d.kind {
	case reviewAcceptFocus, reviewEditFocus:
		return strings.TrimSpace(d.focusText)
	default:
		return ""
	}
}

// reviewDecider is the injectable seam between a finished review and the
// author. Orchestration tests supply a fake; only the TTY path uses the real
// terminal one.
type reviewDecider func(window reviewWindow, review reviewResponse, focus reviewFocusContext) (reviewDecision, error)

type reviewDecisionPhase int

const (
	decisionPhaseMenu reviewDecisionPhase = iota
	decisionPhaseEditing
)

type reviewDecisionModel struct {
	body           string
	recommendation string
	editor         textarea.Model
	phase          reviewDecisionPhase
	decision       reviewDecision
	editError      string
	width          int
	height         int
	scrollOffset   int
	headingStyle   lipgloss.Style
	statusStyle    lipgloss.Style
	errorStyle     lipgloss.Style
}

func newReviewDecisionModel(body, recommendation string, isDark bool) reviewDecisionModel {
	ld := lipgloss.LightDark(isDark)

	editor := textarea.New()
	editor.SetWidth(76)
	editor.SetHeight(5)
	editor.CharLimit = 0
	editor.SetStyles(textarea.DefaultStyles(isDark))
	// Prefilled with the recommendation: editing is a revision of the model's
	// sentence, not a blank page.
	editor.SetValue(recommendation)
	editor.Focus()

	return reviewDecisionModel{
		body:           body,
		recommendation: recommendation,
		editor:         editor,
		headingStyle:   lipgloss.NewStyle().Bold(true).Foreground(ld(lipgloss.Color("62"), lipgloss.Color("62"))),
		statusStyle:    lipgloss.NewStyle().Foreground(ld(lipgloss.Color("244"), lipgloss.Color("241"))),
		errorStyle:     lipgloss.NewStyle().Foreground(ld(lipgloss.Color("160"), lipgloss.Color("203"))),
	}
}

func (m reviewDecisionModel) Init() tea.Cmd { return textarea.Blink }

func (m reviewDecisionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		width := msg.Width - 2
		if width < 20 {
			width = 20
		}
		m.editor.SetWidth(width)
		return m, nil

	case tea.KeyPressMsg:
		if m.phase == decisionPhaseEditing {
			return m.updateEditing(msg)
		}
		return m.updateMenu(msg)
	}

	if m.phase != decisionPhaseEditing {
		return m, nil
	}
	var cmd tea.Cmd
	m.editor, cmd = m.editor.Update(msg)
	return m, cmd
}

func (m reviewDecisionModel) updateMenu(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "a":
		m.decision = reviewDecision{kind: reviewAcceptFocus, focusText: m.recommendation}
		return m, tea.Quit
	case "e":
		m.phase = decisionPhaseEditing
		m.editError = ""
		m.editor.Focus()
		return m, textarea.Blink
	case "s":
		m.decision = reviewDecision{kind: reviewSaveWithoutFocus}
		return m, tea.Quit
	case "d", "q", "esc", "ctrl+c":
		m.decision = reviewDecision{kind: reviewDiscard}
		return m, tea.Quit
	case "up", "k":
		if m.scrollOffset > 0 {
			m.scrollOffset--
		}
		return m, nil
	case "down", "j":
		m.scrollOffset = clampedScrollDown(m.scrollOffset, len(strings.Split(m.body, "\n")), m.height)
		return m, nil
	}
	return m, nil
}

func (m reviewDecisionModel) updateEditing(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+s", "ctrl+d":
		if strings.TrimSpace(m.editor.Value()) == "" {
			// A blank focus is not a decision. Silently downgrading it to
			// "save without focus" would quietly keep a focus the author was
			// visibly trying to replace.
			m.editError = "The focus is blank. Write one, or press Esc to go back."
			return m, nil
		}
		m.decision = reviewDecision{kind: reviewEditFocus, focusText: m.editor.Value()}
		return m, tea.Quit
	case "esc":
		m.phase = decisionPhaseMenu
		m.editError = ""
		m.editor.SetValue(m.recommendation)
		return m, nil
	case "ctrl+c":
		m.decision = reviewDecision{kind: reviewDiscard}
		return m, tea.Quit
	}

	var cmd tea.Cmd
	m.editor, cmd = m.editor.Update(msg)
	return m, cmd
}

func (m reviewDecisionModel) View() tea.View {
	if m.phase == decisionPhaseEditing {
		var b strings.Builder
		b.WriteString(m.headingStyle.Render("Edit the focus to adopt"))
		b.WriteString("\n")
		b.WriteString(m.statusStyle.Render("The recommendation above is kept in the saved review either way."))
		b.WriteString("\n\n")
		b.WriteString(m.editor.View())
		b.WriteString("\n")
		if m.editError != "" {
			b.WriteString(m.errorStyle.Render(m.editError))
			b.WriteString("\n")
		}
		b.WriteString(m.statusStyle.Render("Ctrl+S/D save with this focus   Esc back"))

		v := tea.NewView(b.String())
		v.AltScreen = true
		return v
	}

	footer := m.statusStyle.Render(strings.Join([]string{
		"a accept focus",
		"e edit focus",
		"s save only",
		"d discard",
	}, "   "))

	lines := strings.Split(m.body, "\n")
	visibleLines := m.height - 1
	if visibleLines < 1 {
		visibleLines = 1
	}
	maxOffset := len(lines) - visibleLines
	if maxOffset < 0 {
		maxOffset = 0
	}
	if maxOffset > 0 {
		footer += m.statusStyle.Render("   ↑/k up   ↓/j down")
	}
	offset := m.scrollOffset
	if offset > maxOffset {
		offset = maxOffset
	}
	end := offset + visibleLines
	if end > len(lines) {
		end = len(lines)
	}

	v := tea.NewView(strings.Join(lines[offset:end], "\n") + "\n" + footer)
	v.AltScreen = true
	return v
}

// promptReviewDecision is the real terminal decider. Every way of leaving the
// program without choosing — Ctrl+C, a killed program, a closed stdin —
// resolves to discard, because the alternative is writing something the author
// never approved.
func promptReviewDecision(window reviewWindow, review reviewResponse, focus reviewFocusContext) (reviewDecision, error) {
	isDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	model := newReviewDecisionModel(renderReviewTTY(window, review, focus), review.FocusRecommendation, isDark)

	final, err := tea.NewProgram(model).Run()
	if err != nil {
		if isDecisionAbort(err) {
			return reviewDecision{kind: reviewDiscard}, nil
		}
		return reviewDecision{}, fmt.Errorf("running review decision: %w", err)
	}
	return final.(reviewDecisionModel).decision, nil
}

func isDecisionAbort(err error) bool {
	return errors.Is(err, tea.ErrInterrupted) ||
		errors.Is(err, tea.ErrProgramKilled) ||
		errors.Is(err, io.EOF)
}
