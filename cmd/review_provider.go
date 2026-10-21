package cmd

import (
	"context"
	"errors"
	"github.com/sglavoie/seneca/review/provider"
	"os"
)

// obtainReviewResponse is the single place a review gets its raw reply. A CLI
// harness and a direct provider both answer with the bytes parseReviewResponse
// reads, so everything after this call — validation, the decision, saving, and
// the non-TTY report — never learns which one ran. Only the usage ledger reads
// what else the reply carries.
func obtainReviewResponse(cfg reviewConfig, prompt string) (reviewReply, error) {
	if cfg.Harness.isProvider() {
		return defaultProviderReviewer().review(cfg, prompt)
	}
	raw, err := runReviewHarness(cfg, prompt)
	if err != nil {
		// A failed CLI's output is not a review, but the usage and cost it
		// reported were still billed and belong in the ledger.
		failed := claudeHarnessReply(cfg, raw)
		failed.raw = nil
		return failed, err
	}
	return claudeHarnessReply(cfg, raw), nil
}

// providerReviewer holds what a direct-provider review reads from the world:
// the environment and the Keychain for the key, and the endpoint. Tests replace
// all three, so none of them ever reaches the real Keychain or the real API.
type providerReviewer struct {
	getenv   func(string) string
	keychain provider.KeychainReader
	baseURL  string
}

func defaultProviderReviewer() providerReviewer {
	return providerReviewer{getenv: os.Getenv, keychain: provider.SecurityKeychain}
}

// providerKeySource is where a provider harness's API key is looked up.
func providerKeySource(h harnessName) provider.KeySource {
	if h == harnessOpenAI {
		return provider.OpenAIKey
	}
	return provider.AnthropicKey
}

func (p providerReviewer) review(cfg reviewConfig, prompt string) (reviewReply, error) {
	key, label, err := provider.ResolveKey(providerKeySource(cfg.Harness), p.getenv, p.keychain)
	if err != nil {
		return reviewReply{}, err
	}

	// --timeout bounds the whole call, exactly as it bounds a CLI harness, and
	// a deadline reads the same way to the author whichever ran.
	limit := cfg.harnessTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()

	var answer provider.Reply
	switch cfg.Harness {
	case harnessOpenAI:
		answer, err = provider.OpenAI(ctx, provider.OpenAIRequest{
			APIKey:   key,
			KeyLabel: label,
			BaseURL:  p.baseURL,
			Model:    cfg.Model,
			Effort:   cfg.Effort,
			Schema:   reviewSchema,
			Prompt:   prompt,
		})
	default:
		answer, err = provider.Anthropic(ctx, provider.AnthropicRequest{
			APIKey:   key,
			KeyLabel: label,
			BaseURL:  p.baseURL,
			Model:    cfg.Model,
			Effort:   cfg.Effort,
			Schema:   reviewSchema,
			Prompt:   prompt,
		})
	}
	// A refused or truncated reply still carries the usage it was billed for.
	reply := reviewReply{raw: answer.Text, model: answer.Model, tokens: answer.Usage}
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return reply, &harnessTimeoutError{harness: cfg.Harness, limit: limit}
		}
		return reply, err
	}
	return reply, nil
}
