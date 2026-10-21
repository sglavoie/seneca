package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"github.com/sglavoie/seneca/review/provider"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// resolveReviewConfig layers the three sources of a review's settings, highest
// precedence last: the configured `[review]` table, the environment, then the
// flags. The file settings arrive already validated and are passed in rather
// than read here, so this stays a pure function of its arguments.
func resolveReviewConfig(
	harnessFlag string,
	harnessFlagSet bool,
	modelFlag string,
	modelFlagSet bool,
	effortFlag string,
	effortFlagSet bool,
	timeout time.Duration,
	getenv func(string) string,
	file reviewSettings,
) (reviewConfig, error) {
	rawHarness := string(file.Harness)
	if fromEnv := strings.TrimSpace(getenv("SENECA_HARNESS")); fromEnv != "" {
		rawHarness = fromEnv
	}
	if harnessFlagSet {
		rawHarness = harnessFlag
	}
	if strings.TrimSpace(rawHarness) == "" {
		return reviewConfig{}, fmt.Errorf("missing harness: set --harness, SENECA_HARNESS, or [review].harness in config.toml to claude, copilot, codex, anthropic, or openai")
	}

	harness, err := parseHarnessName(rawHarness)
	if err != nil {
		return reviewConfig{}, err
	}

	model := file.Model
	if fromEnv := strings.TrimSpace(getenv("SENECA_MODEL")); fromEnv != "" {
		model = fromEnv
	}
	if modelFlagSet {
		model = modelFlag
	}
	defaultModel, defaultEffort := harness.providerDefaults()
	model = strings.TrimSpace(model)
	if model == "" {
		model = defaultModel
	}

	// Effort layers exactly like the harness and the model, and remembers
	// which source wrote it so a refusal names the one to change.
	rawEffort, effortSource := file.Effort, "[review].effort"
	if fromEnv := strings.TrimSpace(getenv("SENECA_EFFORT")); fromEnv != "" {
		rawEffort, effortSource = fromEnv, "SENECA_EFFORT"
	}
	if effortFlagSet {
		rawEffort, effortSource = effortFlag, "--effort"
	}
	effort := ""
	if effortFlagSet || strings.TrimSpace(rawEffort) != "" {
		effort, err = parseReviewEffort(rawEffort)
		if err != nil {
			return reviewConfig{}, fmt.Errorf("%s: %w", effortSource, err)
		}
		if !harness.honorsEffort() {
			return reviewConfig{}, fmt.Errorf("%s: the %s harness cannot honor an effort setting; remove it, or choose --harness anthropic or openai", effortSource, harness)
		}
	}
	if effort == "" {
		effort = defaultEffort
	}

	// The timeout has no environment or file source, so it arrives already
	// validated by the flag. It is resolved here anyway so one function still
	// builds the whole reviewConfig.
	return reviewConfig{
		Harness: harness,
		Model:   model,
		Effort:  effort,
		Timeout: timeout,
	}, nil
}

func parseHarnessName(value string) (harnessName, error) {
	switch harnessName(strings.ToLower(strings.TrimSpace(value))) {
	case harnessClaude, harnessCopilot, harnessCodex, harnessAnthropic, harnessOpenAI:
		return harnessName(strings.ToLower(strings.TrimSpace(value))), nil
	default:
		return "", fmt.Errorf("invalid harness %q: expected claude, copilot, codex, anthropic, or openai", value)
	}
}

// isProvider reports whether a harness calls a model API directly instead of
// running a CLI.
func (h harnessName) isProvider() bool {
	return h == harnessAnthropic || h == harnessOpenAI
}

// honorsEffort reports whether a harness can pass an effort level on. Only the
// direct providers can; the CLI harnesses have no such setting to forward.
func (h harnessName) honorsEffort() bool {
	return h.isProvider()
}

// providerDefaults is the model and effort a provider harness uses when none is
// configured. A CLI harness has neither: it keeps its own default model and
// takes no effort.
func (h harnessName) providerDefaults() (model, effort string) {
	switch h {
	case harnessAnthropic:
		return provider.AnthropicDefaultModel, provider.AnthropicDefaultEffort
	case harnessOpenAI:
		return provider.OpenAIDefaultModel, provider.OpenAIDefaultEffort
	default:
		return "", ""
	}
}

// parseReviewEffort accepts exactly the levels a provider takes.
func parseReviewEffort(value string) (string, error) {
	effort := strings.ToLower(strings.TrimSpace(value))
	if slices.Contains(reviewEfforts, effort) {
		return effort, nil
	}
	return "", fmt.Errorf("invalid effort %q: expected %s", value, strings.Join(reviewEfforts, ", "))
}

// completeHarnessNames offers exactly the names parseHarnessName accepts, and
// never falls back to file names: a path is never a valid harness.
func completeHarnessNames(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return []string{string(harnessClaude), string(harnessCopilot), string(harnessCodex), string(harnessAnthropic), string(harnessOpenAI)}, cobra.ShellCompDirectiveNoFileComp
}

// completeReviewEfforts offers exactly the levels parseReviewEffort accepts.
func completeReviewEfforts(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return slices.Clone(reviewEfforts), cobra.ShellCompDirectiveNoFileComp
}

func selectReviewWindow(entries []engine.ReflectionEntry, now time.Time, weekly bool, monthValue string, monthSet bool) (reviewWindow, error) {
	today := truncateDate(now)
	start := today.AddDate(0, 0, -6)
	end := today
	title := "Weekly review"
	promptLabel := "weekly"
	period := engine.ReviewPeriodWeekly

	if monthSet {
		monthKey := monthValue
		if monthKey == "" || monthKey == "current" {
			monthKey = today.Format("2006-01")
		}
		monthStart, err := time.ParseInLocation("2006-01", monthKey, today.Location())
		if err != nil {
			return reviewWindow{}, fmt.Errorf("invalid --month %q: expected YYYY-MM", monthKey)
		}
		if monthStart.After(today) {
			return reviewWindow{}, fmt.Errorf("invalid --month %q: review month cannot be in the future", monthKey)
		}
		start = monthStart
		end = monthStart.AddDate(0, 1, -1)
		if end.After(today) {
			end = today
		}
		title = "Monthly review"
		promptLabel = "monthly"
		period = engine.ReviewPeriodMonthly
	} else if weekly {
		start = today.AddDate(0, 0, -6)
		end = today
	}

	window := reviewWindow{
		Title:        title,
		Period:       period,
		PromptLabel:  promptLabel,
		Start:        start,
		End:          end,
		ExpectedDays: int(end.Sub(start).Hours()/24) + 1,
	}
	window.PeriodLabel = fmt.Sprintf("%s to %s", start.Format("2006-01-02"), end.Format("2006-01-02"))

	covered := make(map[string]struct{})
	for _, candidate := range entries {
		if candidate.Date == "" {
			continue
		}
		entryDate, err := time.ParseInLocation("2006-01-02", candidate.Date, today.Location())
		if err != nil {
			continue
		}
		if entryDate.Before(start) || entryDate.After(end) {
			continue
		}
		window.Entries = append(window.Entries, candidate)
		covered[candidate.Date] = struct{}{}
	}
	sort.SliceStable(window.Entries, func(i, j int) bool {
		return window.Entries[i].Date < window.Entries[j].Date
	})
	window.CoveredDays = len(covered)
	return window, nil
}

func truncateDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
