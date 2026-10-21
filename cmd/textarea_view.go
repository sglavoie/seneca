package cmd

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Rendering the daily form: the editing screen, the review screen, the context
// viewer, and the scroll arithmetic the three share.

func (m reflectionModel) View() tea.View {
	if m.phase == phaseContext {
		return m.scrollableView(
			m.contextBody(m.contextWrapWidth()),
			m.contextScrollOffset,
			[]string{"Esc/Ctrl+O back"},
		)
	}

	if m.phase == phaseReviewing {
		bodyContent := m.reviewBody(m.reviewWrapWidth())
		footerParts := []string{"Enter/Ctrl+S save", "e edit", "Esc discard"}
		if m.context.HasContext() {
			footerParts = append(footerParts, "Ctrl+O context")
		}
		if m.confirmingDiscard {
			footerParts = []string{discardPrompt}
		}
		return m.scrollableView(bodyContent, m.scrollOffset, footerParts)
	}

	var b strings.Builder

	// Context sits above everything: it is what today is answering, and it
	// stays put while the author tabs between fields.
	if lines := m.contextLines(m.width); len(lines) > 0 {
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteString("\n\n")
	}

	header := lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("Seneca -- Evening Reflection  %s", m.date))
	b.WriteString(header)
	b.WriteString("\n\n")

	// Positional step indicator: ● for active, ○ for the rest. Conveys
	// "where am I" without revealing "how close to done" — a numeric
	// progress bar would reintroduce the finish-line we are removing.
	// If this still reads as progress in practice, delete the block.
	steps := make([]string, len(m.fields))
	for i := range m.fields {
		if i == m.active {
			steps[i] = m.titleActiveStyle.Render("●")
		} else {
			steps[i] = m.titleInactiveStyle.Render("○")
		}
	}
	b.WriteString(strings.Join(steps, " "))
	b.WriteString("\n\n")

	title := m.renderFieldTitle(m.active, questionTitleWidth(m.width))
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(m.statusStyle.Render(strings.Repeat("─", min(60, m.width))))
	b.WriteString("\n")
	b.WriteString(m.textareas[m.active].View())
	b.WriteString("\n")

	statusParts := []string{"Tab move", "Ctrl+S/D save", "Esc cancel"}
	if m.context.HasContext() {
		statusParts = append(statusParts, "Ctrl+O context")
	}
	status := m.statusStyle.Render(strings.Join(statusParts, "   "))
	if m.confirmingDiscard {
		status = m.titleActiveStyle.Render(discardPrompt)
	}
	b.WriteString(status)

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

// discardPrompt replaces the footer while leaving would throw writing away.
const discardPrompt = "Discard what you wrote?  y discard   n/Esc keep writing"

// scrollableView pins a one-line footer beneath a vertically scrollable body.
// Both the review and context screens use it so their viewport calculations
// stay identical while their offsets remain independent.
func (m reflectionModel) scrollableView(body string, offset int, footerParts []string) tea.View {
	allLines := strings.Split(body, "\n")
	visibleLines := m.height - 1
	if visibleLines < 1 {
		visibleLines = 1
	}
	maxOffset := maxScrollOffset(len(allLines), m.height)
	if maxOffset > 0 {
		footerParts = append(footerParts, "↑/k up   ↓/j down")
	}
	offset = clampScrollOffset(offset, len(allLines), m.height)
	end := offset + visibleLines
	if end > len(allLines) {
		end = len(allLines)
	}
	visible := strings.Join(allLines[offset:end], "\n")
	footer := m.statusStyle.Render(strings.Join(footerParts, "   "))

	v := tea.NewView(visible + "\n" + footer)
	v.AltScreen = true
	return v
}

// contextLines renders the persistent continuity block: the focus in effect and
// the prior intention labelled with the date it was actually written.
func (m reflectionModel) contextLines(width int) []string {
	var lines []string
	if m.context.Focus != "" {
		lines = append(lines, m.contextLine("Focus", m.context.Focus, width))
	}
	if m.context.FocusDispute != "" {
		lines = append(lines, m.contextLine("Focus (disputed)", m.context.FocusDispute, width))
	}
	if m.context.HasPrior() {
		lines = append(lines, m.contextLine(m.context.IntentionLabel(), m.context.PriorText, width))
	}
	return lines
}

// contextLine flattens multi-line context into one line that always fits the
// terminal, so the block costs a predictable number of rows in the height
// budget instead of silently wrapping into the form below it.
func (m reflectionModel) contextLine(label, text string, width int) string {
	if width < 20 {
		width = 20
	}
	// A long label — "Intention 2026-08-18 (3 days ago)" — is truncated before the text is,
	// so a narrow terminal never pushes the line past its width.
	label = truncateLine(label, width-4)
	maxLen := width - len([]rune(label)) - 2
	if maxLen < 1 {
		maxLen = 1
	}
	flat := strings.Join(strings.Fields(strings.ReplaceAll(text, "\n", " ")), " ")
	return m.statusStyle.Render(label+": ") + truncateLine(flat, maxLen)
}

func (m reflectionModel) contextWrapWidth() int {
	width := m.width - 2
	if width < 1 {
		width = 1
	}
	return width
}

// contextBody renders the complete continuity values without flattening or
// truncating them. Lip Gloss wraps each physical line to the terminal width;
// explicit line and paragraph breaks remain in place.
func (m reflectionModel) contextBody(wrapWidth int) string {
	if wrapWidth < 1 {
		wrapWidth = 1
	}

	var sections []string
	appendSection := func(label, text string) {
		label = lipgloss.Wrap(label, wrapWidth, "")
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
		sections = append(sections,
			m.titleActiveStyle.Render(label)+"\n"+lipgloss.Wrap(text, wrapWidth, ""),
		)
	}

	if m.context.Focus != "" {
		appendSection("Focus", m.context.Focus)
	}
	if m.context.FocusDispute != "" {
		appendSection("Focus (disputed)", m.context.FocusDispute)
	}
	if m.context.HasPrior() {
		appendSection(m.context.IntentionLabel(), m.context.PriorText)
	}
	return strings.Join(sections, "\n\n")
}

func (m reflectionModel) contextContentLines() int {
	if !m.context.HasContext() {
		return 0
	}
	return len(strings.Split(m.contextBody(m.contextWrapWidth()), "\n"))
}

func (m reflectionModel) renderFieldTitle(index int, width int) string {
	style := m.titleInactiveStyle
	if index == m.active || m.phase == phaseReviewing {
		style = m.titleActiveStyle
	}
	title := fmt.Sprintf("%d. %s", index+1, m.fields[index].title)
	return style.Render(truncateLine(title, width))
}

func questionTitleWidth(width int) int {
	width -= 2
	if width < 20 {
		return 20
	}
	return width
}

func truncateLine(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func (m reflectionModel) reviewWrapWidth() int {
	wrapWidth := m.width - 2
	if wrapWidth < 20 {
		wrapWidth = 20
	}
	return wrapWidth
}

// reviewBody renders the scrollable half of the review screen. It is the single
// source of both the visible text and the line count Update clamps against, so
// the two can never drift.
func (m reflectionModel) reviewBody(wrapWidth int) string {
	wrapStyle := lipgloss.NewStyle().Width(wrapWidth)

	var b strings.Builder
	// Nothing is on disk yet: the confirming keypress is what saves, so the
	// header must not claim otherwise.
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("Seneca -- Review before saving  %s", m.date)))
	b.WriteString("\n\n")

	if lines := m.contextLines(wrapWidth); len(lines) > 0 {
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteString("\n\n")
	}

	for i := range m.fields {
		b.WriteString(m.renderFieldTitle(i, wrapWidth))
		b.WriteString("\n")
		b.WriteString(wrapStyle.Render(m.textareas[i].Value()))
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// reviewContentLines returns the number of lines in the scrollable review body
// so Update can clamp the scroll offset without re-rendering.
func (m reflectionModel) reviewContentLines() int {
	return len(strings.Split(m.reviewBody(m.reviewWrapWidth()), "\n"))
}

// clampedScrollDown increments offset by 1 but never lets it exceed maxOffset.
func clampedScrollDown(offset, totalLines, height int) int {
	maxOffset := maxScrollOffset(totalLines, height)
	offset++
	if offset > maxOffset {
		offset = maxOffset
	}
	return offset
}

func maxScrollOffset(totalLines, height int) int {
	visibleLines := height - 1
	if visibleLines < 1 {
		visibleLines = 1
	}
	maxOffset := totalLines - visibleLines
	if maxOffset < 0 {
		return 0
	}
	return maxOffset
}

func clampScrollOffset(offset, totalLines, height int) int {
	if offset < 0 {
		return 0
	}
	return min(offset, maxScrollOffset(totalLines, height))
}
