package cmd

import (
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

// The previous focus is rendered first everywhere: a review that opens with
// what was already being attempted reads as a continuation, while one that
// opens with fresh themes reads as a fresh start.

const (
	reviewFocusAssessmentTitle = "Previous focus"
	reviewStrugglesTitle       = "Recurring struggles"
	reviewWinsTitle            = "Recurring wins"
	reviewCarryForwardTitle    = "Carry-forward intentions"
	reviewNextFocusTitle       = "Next focus"
	reviewAcceptedFocusTitle   = "Accepted focus"
	reviewNoFocusNote          = "No focus was in effect for this period."
	reviewNoThemeNote          = "No recurring theme found in this section."
)

func renderReviewTTY(window reviewWindow, review reviewResponse, focus reviewFocusContext) string {
	isDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	ld := lipgloss.LightDark(isDark)
	bold := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Foreground(ld(lipgloss.Color("244"), lipgloss.Color("241")))
	accent := lipgloss.NewStyle().Foreground(ld(lipgloss.Color("62"), lipgloss.Color("62"))).Bold(true)

	ruleWidth := 72
	if width, _, err := term.GetSize(os.Stdout.Fd()); err == nil && width > 0 && width < ruleWidth {
		ruleWidth = width
	}
	rule := dim.Render(strings.Repeat("─", ruleWidth))

	var parts []string
	parts = append(parts, bold.Render(window.Title))
	parts = append(parts, dim.Render(fmt.Sprintf("%s  %s", window.PeriodLabel, window.coverageLine())))
	parts = append(parts, renderFocusAssessmentTTY(review.FocusAssessment, focus, accent, dim))
	parts = append(parts, renderReviewSectionTTY(reviewStrugglesTitle, review.Struggles, accent, dim))
	parts = append(parts, renderReviewSectionTTY(reviewWinsTitle, review.Wins, accent, dim))
	parts = append(parts, renderReviewSectionTTY(reviewCarryForwardTitle, review.CarryForward, accent, dim))
	parts = append(parts, accent.Render(reviewNextFocusTitle))
	parts = append(parts, review.FocusRecommendation)

	return strings.Join(parts, "\n\n"+rule+"\n\n")
}

func renderFocusAssessmentTTY(assessment *focusAssessment, focus reviewFocusContext, headingStyle, dimStyle lipgloss.Style) string {
	lines := []string{headingStyle.Render(reviewFocusAssessmentTitle)}
	if !focus.applies() || assessment == nil {
		return strings.Join(append(lines, dimStyle.Render(reviewNoFocusNote)), "\n")
	}

	lines = append(lines, focus.state.Text)
	lines = append(lines, dimStyle.Render(focusProvenanceLine(focus)))
	lines = append(lines, "", assessment.Summary)
	for _, evidence := range assessment.Evidence {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  %s: %s", evidence.Date, evidence.Snippet)))
	}
	return strings.Join(lines, "\n")
}

func renderReviewSectionTTY(title string, items []reviewItem, headingStyle, dimStyle lipgloss.Style) string {
	if len(items) == 0 {
		return headingStyle.Render(title) + "\n" + dimStyle.Render(reviewNoThemeNote)
	}

	lines := []string{headingStyle.Render(title)}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("• %s — %s", item.Theme, item.Summary))
		for _, evidence := range item.Evidence {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("  %s: %s", evidence.Date, evidence.Snippet)))
		}
	}
	return strings.Join(lines, "\n")
}

func renderReviewPlain(window reviewWindow, review reviewResponse, focus reviewFocusContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", window.Title)
	fmt.Fprintf(&b, "%s\n", window.PeriodLabel)
	fmt.Fprintf(&b, "%s\n\n", window.coverageLine())
	writePlainFocusAssessment(&b, review.FocusAssessment, focus)
	b.WriteByte('\n')
	writePlainReviewSection(&b, reviewStrugglesTitle, review.Struggles)
	b.WriteByte('\n')
	writePlainReviewSection(&b, reviewWinsTitle, review.Wins)
	b.WriteByte('\n')
	writePlainReviewSection(&b, reviewCarryForwardTitle, review.CarryForward)
	b.WriteByte('\n')
	b.WriteString(reviewNextFocusTitle + "\n")
	b.WriteString(review.FocusRecommendation)
	return strings.TrimSpace(b.String())
}

func writePlainFocusAssessment(b *strings.Builder, assessment *focusAssessment, focus reviewFocusContext) {
	b.WriteString(reviewFocusAssessmentTitle + "\n")
	if !focus.applies() || assessment == nil {
		b.WriteString("- " + reviewNoFocusNote + "\n")
		return
	}
	fmt.Fprintf(b, "%s\n", focus.state.Text)
	fmt.Fprintf(b, "%s\n", focusProvenanceLine(focus))
	fmt.Fprintf(b, "%s\n", assessment.Summary)
	for _, evidence := range assessment.Evidence {
		fmt.Fprintf(b, "  %s: %s\n", evidence.Date, evidence.Snippet)
	}
}

func writePlainReviewSection(b *strings.Builder, title string, items []reviewItem) {
	b.WriteString(title)
	b.WriteByte('\n')
	if len(items) == 0 {
		b.WriteString("- " + reviewNoThemeNote + "\n")
		return
	}
	for _, item := range items {
		fmt.Fprintf(b, "- %s: %s\n", item.Theme, item.Summary)
		for _, evidence := range item.Evidence {
			fmt.Fprintf(b, "  %s: %s\n", evidence.Date, evidence.Snippet)
		}
	}
}

// renderReviewMarkdown is the body stored in the archive and replayed by
// `seneca review history`. It uses `### ` headings so it can never be mistaken
// for a record boundary, which requires a `## ` heading followed by a marker.
//
// An edited focus never overwrites the recommendation: both are kept, so the
// archive shows what the model proposed *and* what the author committed to.
func renderReviewMarkdown(window reviewWindow, review reviewResponse, focus reviewFocusContext, decision reviewDecision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  ·  %s\n\n", window.PeriodLabel, window.coverageLine())

	b.WriteString("### " + reviewFocusAssessmentTitle + "\n\n")
	if focus.applies() && review.FocusAssessment != nil {
		fmt.Fprintf(&b, "> %s\n\n", strings.Join(strings.Fields(focus.state.Text), " "))
		fmt.Fprintf(&b, "%s\n\n", focusProvenanceLine(focus))
		fmt.Fprintf(&b, "%s\n", review.FocusAssessment.Summary)
		writeMarkdownEvidence(&b, review.FocusAssessment.Evidence)
	} else {
		b.WriteString(reviewNoFocusNote + "\n")
	}
	b.WriteByte('\n')

	writeMarkdownReviewSection(&b, reviewStrugglesTitle, review.Struggles)
	writeMarkdownReviewSection(&b, reviewWinsTitle, review.Wins)
	writeMarkdownReviewSection(&b, reviewCarryForwardTitle, review.CarryForward)

	fmt.Fprintf(&b, "### %s\n\n%s\n", reviewNextFocusTitle, review.FocusRecommendation)
	if accepted := decision.acceptedFocus(); accepted != "" {
		fmt.Fprintf(&b, "\n### %s\n\n%s\n", reviewAcceptedFocusTitle, accepted)
	}
	return strings.TrimSpace(b.String())
}

func writeMarkdownReviewSection(b *strings.Builder, title string, items []reviewItem) {
	fmt.Fprintf(b, "### %s\n\n", title)
	if len(items) == 0 {
		b.WriteString(reviewNoThemeNote + "\n\n")
		return
	}
	for _, item := range items {
		fmt.Fprintf(b, "- **%s** — %s\n", item.Theme, item.Summary)
		writeMarkdownEvidence(b, item.Evidence)
	}
	b.WriteByte('\n')
}

func writeMarkdownEvidence(b *strings.Builder, evidence []reviewEvidence) {
	for _, item := range evidence {
		fmt.Fprintf(b, "  - `%s` %s\n", item.Date, item.Snippet)
	}
}

// focusProvenanceLine says where the focus under review came from, so an
// accepted recommendation is never mistaken for one typed by hand.
func focusProvenanceLine(focus reviewFocusContext) string {
	return fmt.Sprintf("Set %s (%s).", focus.state.Effective, focus.state.Origin)
}
