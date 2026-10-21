package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/review/usage"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	// AnthropicDefaultModel is the model a review uses when none is configured.
	AnthropicDefaultModel = "claude-opus-5-5"
	// AnthropicDefaultEffort is sent explicitly: Opus 5.5's own default is
	// medium, one level below what a review deserves.
	AnthropicDefaultEffort = "high"

	// anthropicMaxTokens leaves room for adaptive thinking plus the review.
	// Streaming is what makes a ceiling this high safe from HTTP timeouts.
	anthropicMaxTokens = 64000
)

// AnthropicRequest is one review call.
type AnthropicRequest struct {
	APIKey string
	// KeyLabel names where APIKey came from, so a rejected key is reported
	// against the source that supplied it.
	KeyLabel string
	// BaseURL overrides the API endpoint; empty means the SDK's default. Tests
	// point it at a local server.
	BaseURL string
	Model   string
	Effort  string
	// Schema is the JSON Schema the reply must follow, sent as
	// output_config.format.
	Schema string
	Prompt string
}

// Anthropic runs one review through the Messages API and returns the reply's
// text: the structured JSON the review schema asks for, unparsed. ctx carries
// the review's deadline. The reply's usage is filled whenever a response came
// back, including one refused or cut short, because those tokens were billed.
func Anthropic(ctx context.Context, request AnthropicRequest) (Reply, error) {
	model := request.Model
	if model == "" {
		model = AnthropicDefaultModel
	}
	effort := request.Effort
	if effort == "" {
		effort = AnthropicDefaultEffort
	}

	options := []option.RequestOption{option.WithAPIKey(request.APIKey)}
	if request.BaseURL != "" {
		options = append(options, option.WithBaseURL(request.BaseURL))
	}
	client := anthropic.NewClient(options...)

	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: anthropicMaxTokens,
		Thinking: anthropic.BetaThinkingConfigParamUnion{
			OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{},
		},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffort(effort),
			Format: anthropic.BetaJSONOutputFormatParam{Schema: json.RawMessage(request.Schema)},
		},
		// A policy decline is re-served by the server's default fallback model
		// inside the same call, so only a decline that survives it reaches the
		// author.
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(request.Prompt)),
		},
	}

	stream := client.Beta.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	message := anthropic.BetaMessage{}
	for stream.Next() {
		if err := message.Accumulate(stream.Current()); err != nil {
			return Reply{}, fmt.Errorf("reading the Anthropic response stream: %w", err)
		}
	}
	if err := stream.Err(); err != nil {
		return Reply{}, describeAnthropicError(err, request.KeyLabel)
	}

	reply := Reply{Model: model, Usage: anthropicUsage(message.Usage)}
	// The server's fallback may have answered in the requested model's place,
	// and it is billed at its own rates.
	if message.Model != "" {
		reply.Model = string(message.Model)
	}

	switch message.StopReason {
	case anthropic.BetaStopReasonEndTurn:
	case anthropic.BetaStopReasonRefusal:
		return reply, &RefusalError{
			Model:       model,
			Category:    string(message.StopDetails.Category),
			Explanation: message.StopDetails.Explanation,
		}
	case anthropic.BetaStopReasonMaxTokens:
		return reply, &TruncatedError{Model: model, MaxTokens: anthropicMaxTokens}
	default:
		return reply, &UnexpectedStopError{Model: model, Reason: string(message.StopReason)}
	}

	// A fallback block marks where a declining model's output gave way to the
	// next model's, so only the text after the last one is the answer: what
	// came before it is a partial reply the server abandoned.
	var text strings.Builder
	for _, block := range message.Content {
		switch variant := block.AsAny().(type) {
		case anthropic.BetaTextBlock:
			text.WriteString(variant.Text)
		case anthropic.BetaFallbackBlock:
			text.Reset()
		}
	}
	reply.Text = []byte(text.String())
	return reply, nil
}

// anthropicUsage normalizes the Messages API's usage. Its input_tokens already
// excludes cache reads and writes; a cache write the API did not break down by
// TTL is counted as the 5-minute kind, the cheaper of the two.
func anthropicUsage(u anthropic.BetaUsage) *usage.Tokens {
	tokens := &usage.Tokens{
		Input:        u.InputTokens,
		CacheRead:    u.CacheReadInputTokens,
		CacheWrite5m: u.CacheCreation.Ephemeral5mInputTokens,
		CacheWrite1h: u.CacheCreation.Ephemeral1hInputTokens,
		Output:       u.OutputTokens,
	}
	if unsplit := u.CacheCreationInputTokens - tokens.CacheWrite5m - tokens.CacheWrite1h; unsplit > 0 {
		tokens.CacheWrite5m += unsplit
	}
	if u.OutputTokensDetails.JSON.ThinkingTokens.Valid() {
		thinking := u.OutputTokensDetails.ThinkingTokens
		tokens.Thinking = &thinking
	}
	return tokens
}

// describeAnthropicError turns an API failure into the next move. A deadline
// is left as it is: the caller owns the deadline and reports it.
func describeAnthropicError(err error, keyLabel string) error {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("calling the Anthropic API: %w", err)
	}
	return describeAPIStatus("Anthropic", apiErr.StatusCode, keyLabel, err)
}
