package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parseReviewResponse decodes a harness reply and checks it against the
// material Seneca actually supplied. Grounding is a parameter rather than a
// package value because a review is only ever as trustworthy as the window it
// was built from.
func parseReviewResponse(raw []byte, grounding reviewGrounding) (reviewResponse, error) {
	payload := strings.TrimSpace(string(raw))
	if payload == "" {
		return reviewResponse{}, fmt.Errorf("empty response")
	}
	if structured, ok, err := extractClaudeStructuredOutput([]byte(payload)); err != nil {
		return reviewResponse{}, err
	} else if ok {
		return parseReviewJSONPayload(structured, grounding)
	}
	if strings.HasPrefix(payload, "```") {
		payload = stripCodeFence(payload)
	}

	if extracted := extractJSONObject(payload); extracted != "" {
		payload = extracted
	}

	return parseReviewJSONPayload([]byte(payload), grounding)
}

// parseReviewJSONPayload is the single gate every harness envelope funnels
// through. It checks the reply against the schema Seneca actually requested
// before decoding it, so an unknown property, an omitted required one, a wrong
// type, or a second JSON value after the response is refused rather than
// silently dropped by the decoder.
func parseReviewJSONPayload(payload []byte, grounding reviewGrounding) (reviewResponse, error) {
	value, err := decodeStrictJSON(payload)
	if err != nil {
		return reviewResponse{}, err
	}
	if err := compiledReviewSchema.validate(value, ""); err != nil {
		return reviewResponse{}, fmt.Errorf("response does not match the requested schema: %w", err)
	}

	var response reviewResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return reviewResponse{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if err := validateReviewResponse(response, grounding); err != nil {
		return reviewResponse{}, err
	}
	return response, nil
}

func extractClaudeStructuredOutput(raw []byte) ([]byte, bool, error) {
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false, nil
	}

	var events []struct {
		Type             string          `json:"type"`
		StructuredOutput json.RawMessage `json:"structured_output"`
	}
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil, false, nil
	}
	for _, event := range events {
		if event.Type == "result" && len(event.StructuredOutput) > 0 {
			return event.StructuredOutput, true, nil
		}
	}
	return nil, true, fmt.Errorf("missing Claude structured_output payload")
}

func stripCodeFence(payload string) string {
	lines := strings.Split(strings.TrimSpace(payload), "\n")
	if len(lines) >= 3 && strings.HasPrefix(lines[0], "```") && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	}
	return payload
}

func extractJSONObject(payload string) string {
	start := strings.IndexAny(payload, "{[")
	end := strings.LastIndexAny(payload, "}]")
	if start == -1 || end == -1 || end < start {
		return ""
	}
	return strings.TrimSpace(payload[start : end+1])
}

func validateReviewResponse(response reviewResponse, grounding reviewGrounding) error {
	if strings.TrimSpace(response.FocusRecommendation) == "" {
		return fmt.Errorf("missing focus_recommendation")
	}
	vocabulary := grounding.vocabulary()
	if err := validateFocusAssessment(response.FocusAssessment, grounding.focus, vocabulary); err != nil {
		return err
	}

	supplied := grounding.entryDateSet()
	for _, section := range []struct {
		name  string
		items []reviewItem
	}{
		{name: "struggles", items: response.Struggles},
		{name: "wins", items: response.Wins},
		{name: "carry_forward", items: response.CarryForward},
	} {
		if err := validateReviewItems(section.name, section.items, supplied, vocabulary); err != nil {
			return err
		}
	}
	return nil
}

// validateFocusAssessment makes the assessment answerable only in the two
// honest shapes: absent when no focus applied, and present with a summary when
// one did. Evidence may only cite a day the focus was actually in force for, so
// an insufficiently-supported focus produces a stated judgement rather than a
// borrowed one. The vocabulary is the window's own, so the refusal sends the
// author back to the material the prompt supplied rather than to reflections an
// arc review never read.
func validateFocusAssessment(assessment *focusAssessment, focus reviewFocusContext, vocabulary reviewEvidenceVocabulary) error {
	if !focus.applies() {
		if assessment != nil {
			return fmt.Errorf("focus_assessment must be null: no focus applied to this review period")
		}
		return nil
	}
	if assessment == nil {
		return fmt.Errorf("missing focus_assessment: a focus set on %s applied to this review period", focus.state.Effective)
	}
	if strings.TrimSpace(assessment.Summary) == "" {
		return fmt.Errorf("focus_assessment is missing summary")
	}

	eligible := focus.eligibleDateSet()
	for i, evidence := range assessment.Evidence {
		if err := validateEvidenceFields(fmt.Sprintf("focus_assessment.evidence[%d]", i), evidence); err != nil {
			return err
		}
		if !eligible[evidence.Date] {
			return fmt.Errorf("focus_assessment.evidence[%d] cites %s, which is not %s %s",
				i, evidence.Date, vocabulary.ineligible, focus.state.Effective)
		}
	}
	return nil
}

// validateReviewItems checks each section's evidence against the dates the
// window actually supplied, naming them in that window's own terms: an arc
// review is refused for citing something that is not a source review end date,
// never for missing a reflection it was never shown.
func validateReviewItems(section string, items []reviewItem, supplied map[string]bool, vocabulary reviewEvidenceVocabulary) error {
	for i, item := range items {
		if strings.TrimSpace(item.Theme) == "" {
			return fmt.Errorf("%s[%d] is missing theme", section, i)
		}
		if strings.TrimSpace(item.Summary) == "" {
			return fmt.Errorf("%s[%d] is missing summary", section, i)
		}
		for j, evidence := range item.Evidence {
			label := fmt.Sprintf("%s[%d].evidence[%d]", section, i, j)
			if err := validateEvidenceFields(label, evidence); err != nil {
				return err
			}
			if !supplied[evidence.Date] {
				return fmt.Errorf("%s cites %s, which is not one of %s", label, evidence.Date, vocabulary.supplied)
			}
		}
	}
	return nil
}

func validateEvidenceFields(label string, evidence reviewEvidence) error {
	if strings.TrimSpace(evidence.Date) == "" {
		return fmt.Errorf("%s is missing date", label)
	}
	if strings.TrimSpace(evidence.Snippet) == "" {
		return fmt.Errorf("%s is missing snippet", label)
	}
	return nil
}
