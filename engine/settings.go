package engine

import (
	"fmt"
	"strings"
)

type ReflectionSettings struct {
	Questions           []string
	AnchorQuestionIndex int
}

// ResolveReflectionSettings validates the shared journal settings: the
// questions and which of them is the anchor. A nil questions list or a nil
// anchor means "not configured" and falls back to the default; the field names
// in the refusals are the config keys the author typed.
func ResolveReflectionSettings(configuredQuestions []string, configuredAnchor *int) (ReflectionSettings, error) {
	questions := DefaultReflectionQuestions()
	if configuredQuestions != nil {
		var err error
		questions, err = validateReflectionQuestions(configKeyPrefix, configuredQuestions)
		if err != nil {
			return ReflectionSettings{}, err
		}
	}

	anchorIndex := defaultAnchorQuestionIndex(questions)
	if configuredAnchor != nil {
		anchorIndex = *configuredAnchor
		if err := validateAnchorQuestionIndex(configKeyPrefix, anchorIndex, len(questions)); err != nil {
			return ReflectionSettings{}, err
		}
	}

	return ReflectionSettings{
		Questions:           questions,
		AnchorQuestionIndex: anchorIndex,
	}, nil
}

// Order is load-bearing: serial-position effect places primacy on the fault to
// confront and recency on tomorrow's correction. Do not reorder.
func DefaultReflectionQuestions() []string {
	return []string{
		"What did I do badly today?",
		"What did I do well?",
		"What could I do differently tomorrow?",
	}
}

func defaultAnchorQuestionIndex(questions []string) int {
	if len(questions) >= 3 {
		return 3
	}
	return 0
}

// configKeyPrefix names the questions and the anchor the way config.toml does.
// The journal settings document names the same two fields without it.
const configKeyPrefix = "seneca."

// ValidateReflectionSettings holds the questions and the anchor to the rules of
// ResolveReflectionSettings, as one unit and with nothing defaulted: an anchor
// is only valid for the questions it is stated with.
func ValidateReflectionSettings(settings ReflectionSettings) error {
	questions, err := validateReflectionQuestions("", settings.Questions)
	if err != nil {
		return err
	}
	return validateAnchorQuestionIndex("", settings.AnchorQuestionIndex, len(questions))
}

func validateAnchorQuestionIndex(prefix string, anchorIndex, questionCount int) error {
	if anchorIndex < 0 {
		return fmt.Errorf("%sanchor_question_index must be 0 or a 1-based question index", prefix)
	}
	if anchorIndex > questionCount {
		return fmt.Errorf("%sanchor_question_index %d is out of range for %d questions", prefix, anchorIndex, questionCount)
	}
	return nil
}

func validateReflectionQuestions(prefix string, questions []string) ([]string, error) {
	if len(questions) == 0 {
		return nil, fmt.Errorf("%squestions must contain at least one question", prefix)
	}

	validated := make([]string, len(questions))
	seen := make(map[string]struct{}, len(questions))
	for i, question := range questions {
		if strings.TrimSpace(question) == "" {
			return nil, fmt.Errorf("%squestions[%d] must not be blank", prefix, i)
		}
		if _, ok := seen[question]; ok {
			return nil, fmt.Errorf("%squestions contains duplicate question %q", prefix, question)
		}
		seen[question] = struct{}{}
		validated[i] = question
	}
	return validated, nil
}
