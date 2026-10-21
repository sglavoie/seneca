package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"github.com/sglavoie/seneca/review/provider"
	"github.com/sglavoie/seneca/review/usage"
	"io"
	"os"
	"path/filepath"
	"time"
)

// The usage ledger: one content-free line per review attempt, so what reviews
// actually cost is measured rather than estimated. It is desktop-only and
// device-local, kept beside the Git retry note, and never committed, synced,
// or read by the engine.

const usageLedgerFileName = "usage.jsonl"

// usageLedgerPathFor is where a journal's ledger is kept.
func usageLedgerPathFor(reflectionsPath string) string {
	return filepath.Join(engine.JournalMetadataDirFor(reflectionsPath), syncDesktopDirName, usageLedgerFileName)
}

// reviewReply is what the harness seam yields: the raw reply parseReviewResponse
// reads, and what the ledger records about the call that produced it. Every
// field but raw is optional; a CLI harness that reports nothing leaves them
// empty.
type reviewReply struct {
	raw []byte
	// model is the model that answered, when the harness says.
	model  string
	tokens *usage.Tokens
	// costUSD is a cost the harness itself reported.
	costUSD *float64
}

// claudeHarnessReply reads the usage and cost the claude CLI's result event
// carries. Any other CLI harness reports neither, and its reply is the raw
// bytes alone.
func claudeHarnessReply(cfg reviewConfig, raw []byte) reviewReply {
	reply := reviewReply{raw: raw, model: cfg.Model}
	if cfg.Harness != harnessClaude {
		return reply
	}
	event, ok := claudeResultEvent(bytes.TrimSpace(raw))
	if !ok {
		return reply
	}
	if event.Usage != nil {
		u := event.Usage
		tokens := &usage.Tokens{
			Input:        u.InputTokens,
			CacheRead:    u.CacheReadInputTokens,
			CacheWrite5m: u.CacheCreation.Ephemeral5m,
			CacheWrite1h: u.CacheCreation.Ephemeral1h,
			Output:       u.OutputTokens,
		}
		if unsplit := u.CacheCreationInputTokens - tokens.CacheWrite5m - tokens.CacheWrite1h; unsplit > 0 {
			tokens.CacheWrite5m += unsplit
		}
		if u.OutputTokensDetails != nil && u.OutputTokensDetails.ThinkingTokens != nil {
			tokens.Thinking = u.OutputTokensDetails.ThinkingTokens
		}
		reply.tokens = tokens
	}
	reply.costUSD = event.TotalCostUSD
	if reply.model == "" {
		// The CLI may consult a smaller model on the side; the one that cost
		// the most is the one that wrote the review.
		best := -1.0
		for model, spent := range event.ModelUsage {
			if spent.CostUSD > best || (spent.CostUSD == best && model < reply.model) {
				reply.model, best = model, spent.CostUSD
			}
		}
	}
	return reply
}

type claudeResult struct {
	Type         string   `json:"type"`
	TotalCostUSD *float64 `json:"total_cost_usd"`
	Usage        *struct {
		InputTokens              int64 `json:"input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheCreation            struct {
			Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
			Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
		} `json:"cache_creation"`
		OutputTokensDetails *struct {
			ThinkingTokens *int64 `json:"thinking_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
	ModelUsage map[string]struct {
		CostUSD float64 `json:"costUSD"`
	} `json:"modelUsage"`
}

// claudeResultEvent finds the result event in the CLI's JSON output, which is
// either that event alone or, with --verbose, the whole event list. Output
// from a failed run may carry stderr lines beside the event, so when the whole
// does not parse, a line that is the event alone still counts.
func claudeResultEvent(raw []byte) (claudeResult, bool) {
	if event, ok := parseClaudeResultEvent(raw); ok {
		return event, true
	}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) > 0 && line[0] == '{' {
			if event, ok := parseClaudeResultEvent(line); ok {
				return event, true
			}
		}
	}
	return claudeResult{}, false
}

func parseClaudeResultEvent(raw []byte) (claudeResult, bool) {
	if len(raw) == 0 {
		return claudeResult{}, false
	}
	if raw[0] == '[' {
		var events []claudeResult
		if json.Unmarshal(raw, &events) != nil {
			return claudeResult{}, false
		}
		for _, event := range events {
			if event.Type == "result" {
				return event, true
			}
		}
		return claudeResult{}, false
	}
	var event claudeResult
	if json.Unmarshal(raw, &event) != nil || event.Type != "result" {
		return claudeResult{}, false
	}
	return event, true
}

// usageProvider is whose rates price a harness's usage. Only the harnesses
// that report usage have one.
func usageProvider(h harnessName) string {
	switch h {
	case harnessAnthropic, harnessClaude:
		return "anthropic"
	case harnessOpenAI:
		return "openai"
	default:
		return ""
	}
}

// harnessOutcome names how a failed harness call ended.
func harnessOutcome(err error) usage.Outcome {
	var timeout *harnessTimeoutError
	var refusal *provider.RefusalError
	var truncated *provider.TruncatedError
	switch {
	case errors.As(err, &timeout):
		return usage.OutcomeTimeout
	case errors.As(err, &refusal):
		return usage.OutcomeRefused
	case errors.As(err, &truncated):
		return usage.OutcomeTruncated
	default:
		return usage.OutcomeFailed
	}
}

// newUsageRecord is the attempt as the ledger keeps it, before it is priced.
func newUsageRecord(at time.Time, cfg reviewConfig, window reviewWindow, reply reviewReply, latency time.Duration, outcome usage.Outcome) usage.Record {
	return usage.Record{
		Time:        at.Format(time.RFC3339),
		Kind:        string(window.Period),
		WindowStart: window.startDate(),
		WindowEnd:   window.endDate(),
		Harness:     string(cfg.Harness),
		Provider:    usageProvider(cfg.Harness),
		Model:       reply.model,
		Effort:      cfg.Effort,
		Tokens:      reply.tokens,
		CostUSD:     reply.costUSD,
		LatencyMS:   latency.Milliseconds(),
		Outcome:     outcome,
	}
}

// priceUsageRecord fills the cost. A cost the harness reported stands; one
// computed here names the table row's verified date. A model with no row is
// left unpriced rather than guessed at.
func priceUsageRecord(record *usage.Record, table usage.Table) {
	if record.CostUSD != nil {
		record.CostSource = "harness"
		return
	}
	if record.Tokens == nil || record.Provider == "" || record.Model == "" {
		return
	}
	price, ok := table.Lookup(record.Provider, record.Model)
	if !ok {
		return
	}
	cost := price.Cost(*record.Tokens)
	record.CostUSD = &cost
	record.CostSource = "pricing " + price.Verified
}

// appendUsageRecord adds one line to reflectionsPath's ledger through the
// persistence layer, creating the ledger and its directory on the first write.
// The read and the replacement happen under the journal's directory lock, so
// two reviews finishing at once each build on the other's line instead of
// replacing it.
func appendUsageRecord(reflectionsPath string, record usage.Record) error {
	line, err := record.Line()
	if err != nil {
		return err
	}
	path := usageLedgerPathFor(reflectionsPath)
	return engine.WithDirectoryLock(filepath.Dir(reflectionsPath), 0, func() error {
		existing, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if len(existing) > 0 && existing[len(existing)-1] != '\n' {
			existing = append(existing, '\n')
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		return journalFS.AtomicWriteFile(path, append(existing, line...), 0600)
	})
}

// usageLedgerRecorder is the production recordUsage: it prices the attempt
// with the table in force and appends it. Nothing here can fail a review; a
// problem is one warning line.
func usageLedgerRecorder(reflectionsPath, home string, warn io.Writer) func(usage.Record) {
	return func(record usage.Record) {
		table, err := usage.Load(home)
		if err != nil {
			fmt.Fprintf(warn, "warning: usage ledger: %v; pricing with the built-in table\n", err)
		}
		priceUsageRecord(&record, table)
		if err := appendUsageRecord(reflectionsPath, record); err != nil {
			fmt.Fprintf(warn, "warning: usage ledger: could not record this review: %v\n", err)
		}
	}
}
