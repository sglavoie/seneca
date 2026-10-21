package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"os"

	"charm.land/bubbles/v2/textarea"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type phase int

const (
	phaseEditing phase = iota
	phaseReviewing
	phaseContext
)

// fieldKind separates the input roles in the daily form. A textarea is no
// longer one-to-one with a configured question: the follow-through field
// answers a previous day's intention and has no configured question behind it.
type fieldKind int

const (
	fieldQuestion fieldKind = iota
	fieldFollowThrough
)

// reflectionField is one editable field: its kind, the prompt shown above it,
// and the configured-question position it maps to (-1 when it maps to none).
type reflectionField struct {
	kind          fieldKind
	title         string
	questionIndex int
}

// reflectionTUIConfig is everything the daily form needs to render itself.
type reflectionTUIConfig struct {
	questions []string
	date      string
	// initial holds per-configured-question starting values when editing.
	initial []string
	context engine.ContinuityContext
}

type reflectionModel struct {
	textareas           []textarea.Model
	fields              []reflectionField
	date                string
	context             engine.ContinuityContext
	active              int
	phase               phase
	saved               bool
	width               int
	height              int
	scrollOffset        int
	contextScrollOffset int
	contextReturnPhase  phase
	// initialValues is what each field held when the form opened, so leaving
	// can tell typed writing apart from an untouched or reopened entry.
	initialValues []string
	// confirmingDiscard holds the form on a y/n prompt before unsaved writing
	// is thrown away.
	confirmingDiscard  bool
	titleActiveStyle   lipgloss.Style
	titleInactiveStyle lipgloss.Style
	statusStyle        lipgloss.Style
}

// buildReflectionFields orders the form: the narrative follow-through first,
// because it closes the previous day before the present one is examined, then
// the configured questions in their configured order.
func buildReflectionFields(questions []string, ctx engine.ContinuityContext) []reflectionField {
	fields := make([]reflectionField, 0, len(questions)+1)
	if ctx.ShowFollowThrough {
		fields = append(fields, reflectionField{
			kind:          fieldFollowThrough,
			title:         followThroughPrompt(ctx.PriorDate),
			questionIndex: -1,
		})
	}
	for i, question := range questions {
		fields = append(fields, reflectionField{
			kind:          fieldQuestion,
			title:         question,
			questionIndex: i,
		})
	}
	return fields
}

// followThroughPrompt asks what happened, not whether the intention was met:
// the answer is a narrative, never a status.
func followThroughPrompt(sourceDate string) string {
	if sourceDate == "" {
		return "How did the last intention actually go?"
	}
	return fmt.Sprintf("How did the intention from %s actually go?", sourceDate)
}

func initialFieldValue(cfg reflectionTUIConfig, field reflectionField) string {
	if field.kind == fieldFollowThrough {
		return cfg.context.FollowInitial
	}
	if field.questionIndex >= 0 && field.questionIndex < len(cfg.initial) {
		return cfg.initial[field.questionIndex]
	}
	return ""
}

func newReflectionModel(cfg reflectionTUIConfig, isDark bool) reflectionModel {
	ld := lipgloss.LightDark(isDark)
	activeColor := ld(lipgloss.Color("62"), lipgloss.Color("62"))
	inactiveColor := ld(lipgloss.Color("246"), lipgloss.Color("240"))
	statusColor := ld(lipgloss.Color("244"), lipgloss.Color("241"))

	fields := buildReflectionFields(cfg.questions, cfg.context)
	tas := make([]textarea.Model, len(fields))
	initialValues := make([]string, len(fields))
	for i, field := range fields {
		ta := textarea.New()
		ta.SetWidth(76)
		ta.SetHeight(5)
		ta.CharLimit = 0
		ta.SetStyles(textarea.DefaultStyles(isDark))
		if value := initialFieldValue(cfg, field); value != "" {
			ta.SetValue(value)
		}
		initialValues[i] = ta.Value()
		if i == 0 {
			ta.Focus()
		}
		tas[i] = ta
	}

	return reflectionModel{
		textareas:     tas,
		initialValues: initialValues,
		fields:        fields,
		date:          cfg.date,
		context:       cfg.context,
		titleActiveStyle: lipgloss.NewStyle().
			Bold(true).
			Foreground(activeColor),
		titleInactiveStyle: lipgloss.NewStyle().
			Foreground(inactiveColor),
		statusStyle: lipgloss.NewStyle().
			Foreground(statusColor),
	}
}

func (m reflectionModel) Init() tea.Cmd {
	return textarea.Blink
}

// collectResult splits the finished textareas back out by field role.
func (m reflectionModel) collectResult(questionCount int) engine.ReflectionResult {
	result := engine.ReflectionResult{Saved: m.saved}
	if !m.saved {
		return result
	}
	result.Answers = make([]string, questionCount)
	for i, field := range m.fields {
		switch field.kind {
		case fieldFollowThrough:
			result.FollowThrough = m.textareas[i].Value()
		case fieldQuestion:
			if field.questionIndex >= 0 && field.questionIndex < questionCount {
				result.Answers[field.questionIndex] = m.textareas[i].Value()
			}
		}
	}
	return result
}

func runReflectionTUI(cfg reflectionTUIConfig) (engine.ReflectionResult, error) {
	isDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	m := newReflectionModel(cfg, isDark)
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return engine.ReflectionResult{}, fmt.Errorf("running TUI: %w", err)
	}

	return result.(reflectionModel).collectResult(len(cfg.questions)), nil
}
