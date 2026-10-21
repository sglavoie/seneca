package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"strings"
	"testing"
	"time"
)

func TestResolveReviewConfigPrefersFlags(t *testing.T) {
	t.Parallel()

	cfg, err := resolveReviewConfig(
		"copilot",
		true,
		"gpt-5.4",
		true,
		"",
		false,
		30*time.Minute,
		func(key string) string {
			switch key {
			case "SENECA_HARNESS":
				return "claude"
			case "SENECA_MODEL":
				return "sonnet"
			default:
				return ""
			}
		},
		reviewSettings{Harness: harnessCodex, Model: "opus"},
	)
	if err != nil {
		t.Fatalf("resolveReviewConfig returned error: %v", err)
	}
	if cfg.Harness != harnessCopilot {
		t.Fatalf("expected copilot harness, got %q", cfg.Harness)
	}
	if cfg.Model != "gpt-5.4" {
		t.Fatalf("expected flag model override, got %q", cfg.Model)
	}
	if cfg.Timeout != 30*time.Minute {
		t.Fatalf("expected the harness timeout to reach the config, got %s", cfg.Timeout)
	}
}

func TestResolveReviewConfigRequiresHarness(t *testing.T) {
	t.Parallel()

	_, err := resolveReviewConfig("", false, "", false, "", false, defaultReviewTimeout, func(string) string { return "" }, reviewSettings{})
	if err == nil || !strings.Contains(err.Error(), "missing harness") {
		t.Fatalf("expected missing harness error, got %v", err)
	}
	// The remedy names every source, not only the two it originally had.
	for _, want := range []string{"--harness", "SENECA_HARNESS", "[review].harness"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected the missing-harness remedy to name %s, got %v", want, err)
		}
	}
}

func TestResolveReviewConfigLayersFlagOverEnvOverFile(t *testing.T) {
	t.Parallel()

	file := reviewSettings{Harness: harnessCodex, Model: "gpt-5.5"}
	envOnly := func(key string) string {
		switch key {
		case "SENECA_HARNESS":
			return "copilot"
		case "SENECA_MODEL":
			return "sonnet"
		}
		return ""
	}
	blankEnv := func(string) string { return "" }

	for _, tc := range []struct {
		name        string
		harnessFlag string
		flagSet     bool
		modelFlag   string
		getenv      func(string) string
		file        reviewSettings
		wantHarness harnessName
		wantModel   string
	}{
		{
			name:        "flag beats env beats file",
			harnessFlag: "claude",
			flagSet:     true,
			modelFlag:   "opus",
			getenv:      envOnly,
			file:        file,
			wantHarness: harnessClaude,
			wantModel:   "opus",
		},
		{
			name:        "env beats file",
			getenv:      envOnly,
			file:        file,
			wantHarness: harnessCopilot,
			wantModel:   "sonnet",
		},
		{
			name:        "file alone",
			getenv:      blankEnv,
			file:        file,
			wantHarness: harnessCodex,
			wantModel:   "gpt-5.5",
		},
		{
			name:        "env alone",
			getenv:      envOnly,
			wantHarness: harnessCopilot,
			wantModel:   "sonnet",
		},
		{
			name:        "flag alone",
			harnessFlag: "claude",
			flagSet:     true,
			modelFlag:   "opus",
			getenv:      blankEnv,
			wantHarness: harnessClaude,
			wantModel:   "opus",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := resolveReviewConfig(
				tc.harnessFlag,
				tc.flagSet,
				tc.modelFlag,
				tc.flagSet,
				"",
				false,
				defaultReviewTimeout,
				tc.getenv,
				tc.file,
			)
			if err != nil {
				t.Fatalf("resolveReviewConfig returned error: %v", err)
			}
			if cfg.Harness != tc.wantHarness {
				t.Fatalf("expected harness %q, got %q", tc.wantHarness, cfg.Harness)
			}
			if cfg.Model != tc.wantModel {
				t.Fatalf("expected model %q, got %q", tc.wantModel, cfg.Model)
			}
		})
	}
}

func TestSelectReviewWindowWeekly(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.May, 26, 18, 0, 0, 0, time.UTC)
	window, err := selectReviewWindow([]engine.ReflectionEntry{
		{Date: "2026-05-19", Raw: "old"},
		{Date: "2026-05-20", Raw: "a"},
		{Date: "2026-05-23", Raw: "b"},
		{Date: "2026-05-26", Raw: "c"},
	}, now, false, "", false)
	if err != nil {
		t.Fatalf("selectReviewWindow returned error: %v", err)
	}

	if window.Title != "Weekly review" {
		t.Fatalf("expected weekly title, got %q", window.Title)
	}
	if window.PeriodLabel != "2026-05-20 to 2026-05-26" {
		t.Fatalf("unexpected period label %q", window.PeriodLabel)
	}
	if window.ExpectedDays != 7 {
		t.Fatalf("expected 7 days, got %d", window.ExpectedDays)
	}
	if window.CoveredDays != 3 {
		t.Fatalf("expected coverage of 3 days, got %d", window.CoveredDays)
	}
	if len(window.Entries) != 3 {
		t.Fatalf("expected 3 entries in window, got %d", len(window.Entries))
	}
}

func TestSelectReviewWindowCurrentMonth(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.May, 26, 9, 0, 0, 0, time.UTC)
	window, err := selectReviewWindow([]engine.ReflectionEntry{
		{Date: "2026-05-01", Raw: "a"},
		{Date: "2026-05-26", Raw: "b"},
		{Date: "2026-04-30", Raw: "c"},
	}, now, false, "current", true)
	if err != nil {
		t.Fatalf("selectReviewWindow returned error: %v", err)
	}

	if window.Title != "Monthly review" {
		t.Fatalf("expected monthly title, got %q", window.Title)
	}
	if window.ExpectedDays != 26 {
		t.Fatalf("expected current-month elapsed days, got %d", window.ExpectedDays)
	}
	if window.CoveredDays != 2 {
		t.Fatalf("expected 2 covered days, got %d", window.CoveredDays)
	}
}

func TestSelectReviewWindowHonorsLocalDateBoundaries(t *testing.T) {
	t.Parallel()

	pdt := time.FixedZone("PDT", -7*60*60)
	now := time.Date(2026, time.May, 26, 18, 0, 0, 0, pdt)
	window, err := selectReviewWindow([]engine.ReflectionEntry{
		{Date: "2026-05-20", Raw: "first day"},
		{Date: "2026-05-26", Raw: "last day"},
	}, now, false, "", false)
	if err != nil {
		t.Fatalf("selectReviewWindow returned error: %v", err)
	}

	if len(window.Entries) != 2 {
		t.Fatalf("expected both boundary entries in window, got %d", len(window.Entries))
	}
	if window.CoveredDays != 2 {
		t.Fatalf("expected 2 covered days, got %d", window.CoveredDays)
	}
}

func TestBuildReviewPromptUsesDefaultQuestions(t *testing.T) {
	t.Parallel()

	effectiveQuestions := engine.DefaultReflectionQuestions()
	window := reviewWindow{
		PromptLabel:  "weekly",
		PeriodLabel:  "2026-05-20 to 2026-05-26",
		ExpectedDays: 7,
		CoveredDays:  1,
		Entries: []engine.ReflectionEntry{
			engine.NewReflectionEntry(engine.FormatReflectionEntry("2026-05-26", effectiveQuestions, []string{
				"I reached for distractions before the hard work.",
				"I protected my training block.",
				"I should write tomorrow's first task tonight.",
			})),
		},
	}

	prompt := buildReviewPrompt(window, reviewFocusContext{})

	for _, want := range []string{
		"What did I do badly today?: I reached for distractions before the hard work.",
		"What did I do well?: I protected my training block.",
		"What could I do differently tomorrow?: I should write tomorrow's first task tonight.",
		"focus_assessment must be null",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

func TestBuildReviewPromptUsesCustomQuestions(t *testing.T) {
	t.Parallel()

	effectiveQuestions := []string{
		"What did I notice today?",
		"What did I handle well?",
		"What should I carry into tomorrow?",
	}
	window := reviewWindow{
		PromptLabel:  "monthly",
		PeriodLabel:  "2026-05-01 to 2026-05-26",
		ExpectedDays: 26,
		CoveredDays:  1,
		Entries: []engine.ReflectionEntry{
			engine.NewReflectionEntry(engine.FormatReflectionEntry("2026-05-26", effectiveQuestions, []string{
				"My energy dropped after back-to-back calls.",
				"I paused before answering a tense message.",
				"Keep the morning review short and concrete.",
			})),
		},
	}

	prompt := buildReviewPrompt(window, reviewFocusContext{})

	for _, want := range []string{
		"What did I notice today?: My energy dropped after back-to-back calls.",
		"What did I handle well?: I paused before answering a tense message.",
		"What should I carry into tomorrow?: Keep the morning review short and concrete.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
	for _, defaultQuestion := range engine.DefaultReflectionQuestions() {
		if strings.Contains(prompt, defaultQuestion+":") {
			t.Fatalf("expected custom prompt to omit default question %q, got:\n%s", defaultQuestion, prompt)
		}
	}
}

func TestBuildReviewCommand(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		cfg        reviewConfig
		schemaPath string
		wantName   string
		mustHave   []string
	}{
		{
			name:       "claude",
			cfg:        reviewConfig{Harness: harnessClaude, Model: "sonnet"},
			schemaPath: "/tmp/schema.json",
			wantName:   "claude",
			mustHave:   []string{"--print", "--output-format", "json", "--json-schema", reviewSchema, "--model", "sonnet", "prompt"},
		},
		{
			name:       "copilot",
			cfg:        reviewConfig{Harness: harnessCopilot, Model: "gpt-5.4"},
			schemaPath: "/tmp/schema.json",
			wantName:   "copilot",
			mustHave:   []string{"--allow-all-tools", "--silent", "--model", "gpt-5.4", "--prompt", "prompt"},
		},
		{
			name:       "codex",
			cfg:        reviewConfig{Harness: harnessCodex, Model: "gpt-5.5"},
			schemaPath: "/tmp/schema.json",
			wantName:   "codex",
			mustHave:   []string{"exec", "--skip-git-repo-check", "--output-schema", "/tmp/schema.json", "-o", "/tmp/output.json", "--model", "gpt-5.5", "prompt"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			outputPath := ""
			if tc.cfg.Harness == harnessCodex {
				outputPath = "/tmp/output.json"
			}
			name, args, err := buildReviewCommand(tc.cfg, "prompt", tc.schemaPath, outputPath)
			if err != nil {
				t.Fatalf("buildReviewCommand returned error: %v", err)
			}
			if name != tc.wantName {
				t.Fatalf("expected command %q, got %q", tc.wantName, name)
			}
			for _, want := range tc.mustHave {
				if !containsArg(args, want) {
					t.Fatalf("expected args %v to contain %q", args, want)
				}
			}
		})
	}
}
