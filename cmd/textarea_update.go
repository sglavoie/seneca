package cmd

import (
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// Key handling for the daily form. Each phase owns its own key map; nothing
// here renders.

func (m reflectionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		w := msg.Width - 2
		if w < 20 {
			w = 20
		}
		chrome := 9
		// The context block costs its own lines plus the blank line under it.
		if n := len(m.contextLines(msg.Width)); n > 0 {
			chrome += n + 1
		}
		taHeight := msg.Height - chrome
		if taHeight < 5 {
			taHeight = 5
		}
		for i := range m.textareas {
			m.textareas[i].SetWidth(w)
			m.textareas[i].SetHeight(taHeight)
		}
		m.contextScrollOffset = clampScrollOffset(m.contextScrollOffset, m.contextContentLines(), m.height)
		return m, nil

	case tea.KeyPressMsg:
		if m.confirmingDiscard {
			switch msg.String() {
			// A second Ctrl+C is the conventional way to insist.
			case "y", "ctrl+c":
				m.saved = false
				return m, tea.Quit
			case "n", "esc":
				m.confirmingDiscard = false
				if m.phase == phaseEditing {
					return m, textarea.Blink
				}
			}
			return m, nil
		}
		if m.phase == phaseContext {
			switch msg.String() {
			case "esc", "ctrl+o":
				m.phase = m.contextReturnPhase
				if m.phase == phaseEditing {
					return m, textarea.Blink
				}
				return m, nil
			case "up", "k":
				if m.contextScrollOffset > 0 {
					m.contextScrollOffset--
				}
				return m, nil
			case "down", "j":
				m.contextScrollOffset = clampedScrollDown(m.contextScrollOffset, m.contextContentLines(), m.height)
				return m, nil
			}
			return m, nil
		}
		if m.phase == phaseReviewing {
			switch msg.String() {
			case "ctrl+o":
				if m.context.HasContext() {
					m.openContextViewer()
				}
				return m, nil
			case "esc", "ctrl+c":
				return m.requestDiscard()
			// Ctrl+S/D is what reached this screen, so pressing it again
			// confirms rather than doing nothing.
			case "enter", "ctrl+s", "ctrl+d":
				m.saved = true
				return m, tea.Quit
			case "e":
				m.phase = phaseEditing
				m.scrollOffset = 0
				m.textareas[m.active].Focus()
				return m, textarea.Blink
			case "up", "k":
				if m.scrollOffset > 0 {
					m.scrollOffset--
				}
				return m, nil
			case "down", "j":
				m.scrollOffset = clampedScrollDown(m.scrollOffset, m.reviewContentLines(), m.height)
				return m, nil
			}
			return m, nil
		}
		switch msg.String() {
		case "ctrl+o":
			if m.context.HasContext() {
				m.openContextViewer()
			}
			return m, nil
		case "ctrl+s", "ctrl+d":
			if m.allFieldsEmpty() {
				// Bypass the review screen — root.go prints "Nothing to record."
				m.saved = true
				return m, tea.Quit
			}
			m.scrollOffset = 0
			m.phase = phaseReviewing
			return m, nil
		case "esc", "ctrl+c":
			return m.requestDiscard()
		case "tab":
			m.textareas[m.active].Blur()
			m.active = (m.active + 1) % len(m.textareas)
			m.textareas[m.active].Focus()
			return m, textarea.Blink
		case "shift+tab":
			m.textareas[m.active].Blur()
			m.active = (m.active - 1 + len(m.textareas)) % len(m.textareas)
			m.textareas[m.active].Focus()
			return m, textarea.Blink
		}
	}

	if m.phase == phaseReviewing {
		return m, nil
	}

	// Only update the active textarea
	var cmd tea.Cmd
	m.textareas[m.active], cmd = m.textareas[m.active].Update(msg)
	return m, cmd
}

// allFieldsEmpty reports whether every editable field is empty. The quoted
// source intention lives outside the fields, so it can never flip this.
func (m reflectionModel) allFieldsEmpty() bool {
	for _, ta := range m.textareas {
		if ta.Value() != "" {
			return false
		}
	}
	return true
}

// hasUnsavedWriting reports whether any field differs from what the form opened
// with. Reopening a saved day and leaving it untouched is not unsaved writing.
func (m reflectionModel) hasUnsavedWriting() bool {
	for i, ta := range m.textareas {
		if i >= len(m.initialValues) || ta.Value() != m.initialValues[i] {
			return true
		}
	}
	return false
}

// requestDiscard leaves at once when there is nothing to lose, and otherwise
// asks first: one stray Esc must not cost an evening's writing.
func (m reflectionModel) requestDiscard() (tea.Model, tea.Cmd) {
	if !m.hasUnsavedWriting() {
		m.saved = false
		return m, tea.Quit
	}
	m.confirmingDiscard = true
	return m, nil
}

// openContextViewer remembers where the author came from and gives each visit
// a fresh position at the top. The caller's textarea and review-scroll state
// are otherwise untouched.
func (m *reflectionModel) openContextViewer() {
	m.contextReturnPhase = m.phase
	m.contextScrollOffset = 0
	m.phase = phaseContext
}
