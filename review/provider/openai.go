package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/review/usage"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

const (
	// OpenAIDefaultModel is the model a review uses when none is configured.
	OpenAIDefaultModel = "gpt-6-astra"
	// OpenAIDefaultEffort is the reasoning effort sent when none is configured.
	OpenAIDefaultEffort = "high"

	// openAISchemaName names the structured output in the request.
	openAISchemaName = "seneca_review"
)

// OpenAIRequest is one review call.
type OpenAIRequest struct {
	APIKey string
	// KeyLabel names where APIKey came from, so a rejected key is reported
	// against the source that supplied it.
	KeyLabel string
	// BaseURL overrides the API endpoint; empty means the SDK's default. Tests
	// point it at a local server.
	BaseURL string
	Model   string
	Effort  string
	// Schema is the JSON Schema the reply must follow, sent as a strict
	// text.format.
	Schema string
	Prompt string
}

// OpenAI runs one review through the Responses API and returns the reply's
// output text: the structured JSON the review schema asks for, unparsed. ctx
// carries the review's deadline. The reply's usage is filled whenever a
// response came back, including one refused or cut short.
func OpenAI(ctx context.Context, request OpenAIRequest) (Reply, error) {
	model := request.Model
	if model == "" {
		model = OpenAIDefaultModel
	}
	effort := request.Effort
	if effort == "" {
		effort = OpenAIDefaultEffort
	}

	var schema map[string]any
	if err := json.Unmarshal([]byte(request.Schema), &schema); err != nil {
		return Reply{}, fmt.Errorf("decoding the review schema: %w", err)
	}

	options := []option.RequestOption{option.WithAPIKey(request.APIKey)}
	if request.BaseURL != "" {
		options = append(options, option.WithBaseURL(request.BaseURL))
	}
	client := openai.NewClient(options...)

	format := responses.ResponseFormatTextConfigParamOfJSONSchema(openAISchemaName, schema)
	format.OfJSONSchema.Strict = openai.Bool(true)
	params := responses.ResponseNewParams{
		Model:     shared.ResponsesModel(model),
		Input:     responses.ResponseNewParamsInputUnion{OfString: openai.String(request.Prompt)},
		Reasoning: shared.ReasoningParam{Effort: shared.ReasoningEffort(effort)},
		Text:      responses.ResponseTextConfigParam{Format: format},
		// The review is read once, here; nothing about it needs to stay on
		// OpenAI's side for a later turn.
		Store: openai.Bool(false),
	}

	response, err := client.Responses.New(ctx, params)
	if err != nil {
		return Reply{}, describeOpenAIError(err, request.KeyLabel)
	}

	reply := Reply{Model: model, Usage: openAIUsage(response.Usage)}
	if response.Model != "" {
		reply.Model = string(response.Model)
	}

	for _, item := range response.Output {
		for _, content := range item.Content {
			if content.Type == "refusal" {
				return reply, &RefusalError{Model: model, Explanation: content.Refusal}
			}
		}
	}

	switch response.Status {
	case responses.ResponseStatusCompleted:
	case responses.ResponseStatusIncomplete:
		switch reason := response.IncompleteDetails.Reason; reason {
		case "max_output_tokens":
			return reply, &TruncatedError{Model: model}
		case "content_filter":
			return reply, &RefusalError{Model: model, Category: reason}
		default:
			return reply, &UnexpectedStopError{Model: model, Reason: "incomplete: " + reason}
		}
	case responses.ResponseStatusFailed:
		if message := strings.TrimSpace(response.Error.Message); message != "" {
			return reply, fmt.Errorf("the OpenAI API failed the review (%s): %s (nothing was written)", response.Error.Code, message)
		}
		return reply, &UnexpectedStopError{Model: model, Reason: string(response.Status)}
	default:
		return reply, &UnexpectedStopError{Model: model, Reason: string(response.Status)}
	}

	reply.Text = []byte(response.OutputText())
	return reply, nil
}

// openAIUsage normalizes the Responses API's usage, whose input_tokens counts
// cached tokens too. OpenAI caches automatically and bills no cache write.
func openAIUsage(u responses.ResponseUsage) *usage.Tokens {
	if !u.JSON.InputTokens.Valid() && !u.JSON.OutputTokens.Valid() {
		return nil
	}
	cached := u.InputTokensDetails.CachedTokens
	tokens := &usage.Tokens{
		Input:     u.InputTokens - cached,
		CacheRead: cached,
		Output:    u.OutputTokens,
	}
	if u.OutputTokensDetails.JSON.ReasoningTokens.Valid() {
		reasoning := u.OutputTokensDetails.ReasoningTokens
		tokens.Thinking = &reasoning
	}
	return tokens
}

// describeOpenAIError turns an API failure into the next move. A deadline is
// left as it is: the caller owns the deadline and reports it.
func describeOpenAIError(err error, keyLabel string) error {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("calling the OpenAI API: %w", err)
	}
	return describeAPIStatus("OpenAI", apiErr.StatusCode, keyLabel, err)
}
