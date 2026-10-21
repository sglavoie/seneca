package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigFilePathUsesSenecaConfigDir(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	got := configFilePath(home)
	want := filepath.Join(home, ".config", "seneca", "config.toml")
	if got != want {
		t.Fatalf("expected config path %q, got %q", want, got)
	}
}

func TestLoadReflectionSettingsDefaultsWhenConfigMissing(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	settings, err := loadReflectionSettings(home)
	if err != nil {
		t.Fatalf("loadReflectionSettings returned error: %v", err)
	}

	want := engine.DefaultReflectionQuestions()
	if !equalStrings(settings.Questions, want) {
		t.Fatalf("expected default questions %q, got %q", want, settings.Questions)
	}
	if settings.AnchorQuestionIndex != 3 {
		t.Fatalf("expected default anchor index 3, got %d", settings.AnchorQuestionIndex)
	}

	settings.Questions[0] = "mutated"
	settings, err = loadReflectionSettings(home)
	if err != nil {
		t.Fatalf("loadReflectionSettings returned error after mutation: %v", err)
	}
	if settings.Questions[0] != want[0] {
		t.Fatalf("expected defensive default question copy, got %q", settings.Questions[0])
	}
}

func TestLoadReflectionSettingsDefaultsWhenSenecaConfigMissing(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeConfigFile(t, home, "[editor]\ncommand = [\"vim\"]\n")

	settings, err := loadReflectionSettings(home)
	if err != nil {
		t.Fatalf("loadReflectionSettings returned error: %v", err)
	}

	if !equalStrings(settings.Questions, engine.DefaultReflectionQuestions()) {
		t.Fatalf("expected default questions, got %q", settings.Questions)
	}
	if settings.AnchorQuestionIndex != 3 {
		t.Fatalf("expected default anchor index 3, got %d", settings.AnchorQuestionIndex)
	}
}

func TestLoadReflectionSettingsCustomQuestions(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeConfigFile(t, home, `[seneca]
questions = [
  "What did I notice today?",
  "What did I handle well?",
]
`)

	settings, err := loadReflectionSettings(home)
	if err != nil {
		t.Fatalf("loadReflectionSettings returned error: %v", err)
	}

	want := []string{"What did I notice today?", "What did I handle well?"}
	if !equalStrings(settings.Questions, want) {
		t.Fatalf("expected custom questions %q, got %q", want, settings.Questions)
	}
	if settings.AnchorQuestionIndex != 0 {
		t.Fatalf("expected omitted anchor to hide when question 3 is absent, got %d", settings.AnchorQuestionIndex)
	}
}

func TestLoadConfiguredReflectionsPath(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	path := filepath.Join(home, "journal", "reflections.md")
	writeConfigFile(t, home, fmt.Sprintf(`[seneca]
reflections_path = %q
`, path))

	got, err := loadConfiguredReflectionsPath(home)
	if err != nil {
		t.Fatalf("loadConfiguredReflectionsPath returned error: %v", err)
	}

	if got != path {
		t.Fatalf("expected configured reflections path %q, got %q", path, got)
	}
}

func TestLoadConfiguredReflectionsPathExpandsHomeRelativePath(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeConfigFile(t, home, `[seneca]
reflections_path = "~/trail/reflections.md"
`)

	got, err := loadConfiguredReflectionsPath(home)
	if err != nil {
		t.Fatalf("loadConfiguredReflectionsPath returned error: %v", err)
	}

	want := filepath.Join(home, "trail", "reflections.md")
	if got != want {
		t.Fatalf("expected expanded reflections path %q, got %q", want, got)
	}
}

func TestLoadReflectionSettingsDisabledAnchor(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeConfigFile(t, home, `[seneca]
questions = ["One?", "Two?", "Three?"]
anchor_question_index = 0
`)

	settings, err := loadReflectionSettings(home)
	if err != nil {
		t.Fatalf("loadReflectionSettings returned error: %v", err)
	}
	if settings.AnchorQuestionIndex != 0 {
		t.Fatalf("expected disabled anchor index 0, got %d", settings.AnchorQuestionIndex)
	}
}

func TestLoadReflectionSettingsValidAnchor(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeConfigFile(t, home, `[seneca]
questions = ["One?", "Two?", "Three?"]
anchor_question_index = 2
`)

	settings, err := loadReflectionSettings(home)
	if err != nil {
		t.Fatalf("loadReflectionSettings returned error: %v", err)
	}
	if settings.AnchorQuestionIndex != 2 {
		t.Fatalf("expected anchor index 2, got %d", settings.AnchorQuestionIndex)
	}
}

func TestLoadReflectionSettingsInvalidConfigs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "empty questions",
			content: `[seneca]
questions = []
`,
			want: "seneca.questions must contain at least one question",
		},
		{
			name: "blank question",
			content: `[seneca]
questions = ["One?", "   "]
`,
			want: "seneca.questions[1] must not be blank",
		},
		{
			name: "duplicate question",
			content: `[seneca]
questions = ["One?", "One?"]
`,
			want: `seneca.questions contains duplicate question "One?"`,
		},
		{
			name: "negative anchor",
			content: `[seneca]
questions = ["One?"]
anchor_question_index = -1
`,
			want: "seneca.anchor_question_index must be 0 or a 1-based question index",
		},
		{
			name: "out of range anchor",
			content: `[seneca]
questions = ["One?", "Two?"]
anchor_question_index = 3
`,
			want: "seneca.anchor_question_index 3 is out of range for 2 questions",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			writeConfigFile(t, home, tc.content)

			_, err := loadReflectionSettings(home)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestLoadConfiguredReflectionsPathInvalidConfigs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "blank reflections path",
			content: `[seneca]
reflections_path = "   "
`,
			want: "seneca.reflections_path must not be blank",
		},
		{
			name: "relative reflections path",
			content: `[seneca]
reflections_path = "reflections.md"
`,
			want: "seneca.reflections_path must be an absolute path or start with ~/",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			writeConfigFile(t, home, tc.content)

			_, err := loadConfiguredReflectionsPath(home)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestLoadReviewSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		content     string
		wantHarness harnessName
		wantModel   string
	}{
		{
			name: "harness and model",
			content: `[review]
harness = "claude"
model = "opus"
`,
			wantHarness: harnessClaude,
			wantModel:   "opus",
		},
		{
			// Absent is the common case and never an error: a config file that
			// predates the section keeps loading exactly as it did.
			name:    "no review section",
			content: "[editor]\ncommand = [\"vim\"]\n\n[seneca]\nanchor_question_index = 0\n",
		},
		{
			name: "harness alone leaves the model to the harness default",
			content: `[review]
harness = "codex"
`,
			wantHarness: harnessCodex,
		},
		{
			name: "harness is normalized like the flag",
			content: `[review]
harness = " Copilot "
`,
			wantHarness: harnessCopilot,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			writeConfigFile(t, home, tc.content)

			settings, err := loadReviewSettings(home)
			if err != nil {
				t.Fatalf("loadReviewSettings returned error: %v", err)
			}
			if settings.Harness != tc.wantHarness {
				t.Fatalf("expected harness %q, got %q", tc.wantHarness, settings.Harness)
			}
			if settings.Model != tc.wantModel {
				t.Fatalf("expected model %q, got %q", tc.wantModel, settings.Model)
			}
		})
	}
}

func TestLoadReviewSettingsDefaultsWhenConfigMissing(t *testing.T) {
	t.Parallel()

	settings, err := loadReviewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("loadReviewSettings returned error: %v", err)
	}
	if settings != (reviewSettings{}) {
		t.Fatalf("expected no configured review settings, got %+v", settings)
	}
}

// A blank value is not an absent one: the pointer fields exist so a key written
// and left empty is reported rather than silently ignored.
func TestLoadReviewSettingsInvalidConfigs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "unsupported harness",
			content: `[review]
harness = "gemini"
`,
			want: `review.harness: invalid harness "gemini"`,
		},
		{
			name: "blank harness",
			content: `[review]
harness = "   "
`,
			want: "review.harness must not be blank",
		},
		{
			name: "blank model",
			content: `[review]
harness = "claude"
model = ""
`,
			want: "review.model must not be blank",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			writeConfigFile(t, home, tc.content)

			_, err := loadReviewSettings(home)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// An absent model loads clean; the same file with the key written blank does
// not. That is the whole point of the pointer field.
func TestReviewModelDistinguishesAbsentFromBlank(t *testing.T) {
	t.Parallel()

	absent := t.TempDir()
	writeConfigFile(t, absent, "[review]\nharness = \"claude\"\n")
	settings, err := loadReviewSettings(absent)
	if err != nil {
		t.Fatalf("loadReviewSettings returned error for an absent model: %v", err)
	}
	if settings.Model != "" {
		t.Fatalf("expected no model, got %q", settings.Model)
	}

	blank := t.TempDir()
	writeConfigFile(t, blank, "[review]\nharness = \"claude\"\nmodel = \"\"\n")
	if _, err := loadReviewSettings(blank); err == nil {
		t.Fatal("expected a blank model to be refused")
	}
}

func TestReflectionSettingsDriveFormattingParsingAndAnchor(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeConfigFile(t, home, `[seneca]
questions = [
  "What did I notice today?",
  "What did I handle well?",
  "What should I carry into tomorrow?",
]
anchor_question_index = 2
`)

	settings, err := loadReflectionSettings(home)
	if err != nil {
		t.Fatalf("loadReflectionSettings returned error: %v", err)
	}

	answers := []string{
		"A tight feedback loop.",
		"I paused before answering a tense message.",
		"Start with the hard draft.",
	}
	formatted := engine.FormatReflectionEntry("2026-05-26", settings.Questions, answers)

	for _, question := range settings.Questions {
		if !strings.Contains(formatted, "### "+question+"\n") {
			t.Fatalf("expected formatted entry to contain custom heading %q:\n%s", question, formatted)
		}
	}
	for _, defaultQuestion := range engine.DefaultReflectionQuestions() {
		if strings.Contains(formatted, "### "+defaultQuestion+"\n") {
			t.Fatalf("expected formatted entry to omit default heading %q:\n%s", defaultQuestion, formatted)
		}
	}

	parsed := engine.NewReflectionEntry(formatted).Answers(settings.Questions)
	if !equalStrings(parsed, answers) {
		t.Fatalf("expected parsed answers %q, got %q", answers, parsed)
	}

	anchor := engine.MostRecentAnchor([]engine.ReflectionEntry{
		engine.NewReflectionEntry(formatted),
	}, "2026-05-27", settings.Questions, settings.AnchorQuestionIndex)
	if anchor.Text != answers[1] {
		t.Fatalf("expected configured anchor answer %q, got %q", answers[1], anchor.Text)
	}
	if anchor.Date != "2026-05-26" {
		t.Fatalf("expected anchor source date 2026-05-26, got %q", anchor.Date)
	}
}

func writeConfigFile(t *testing.T, home, content string) {
	t.Helper()

	path := configFilePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("creating config directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}
}
