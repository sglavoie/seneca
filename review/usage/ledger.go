package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
)

// RecordVersion is the ledger line format this build writes and reads.
const RecordVersion = 1

// Tokens is what one request consumed, in the shape every provider's usage
// normalizes to. Input never includes cached tokens, and Output always
// includes thinking.
type Tokens struct {
	Input        int64 `json:"input"`
	CacheRead    int64 `json:"cache_read"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
	Output       int64 `json:"output"`
	// Thinking is the part of Output spent reasoning, when the provider says.
	Thinking *int64 `json:"thinking,omitempty"`
}

// Prompt is every input token, cached or not.
func (t Tokens) Prompt() int64 {
	return t.Input + t.CacheRead + t.CacheWrite5m + t.CacheWrite1h
}

// Outcome is how a review attempt ended.
type Outcome string

const (
	OutcomeSaved           Outcome = "saved"
	OutcomeDiscarded       Outcome = "discarded"
	OutcomePiped           Outcome = "piped"
	OutcomeInvalidResponse Outcome = "invalid_response"
	OutcomeRefused         Outcome = "refused"
	OutcomeTruncated       Outcome = "truncated"
	OutcomeTimeout         Outcome = "timeout"
	// OutcomeSaveFailed is an approved review the archive did not take; its
	// text was kept as a draft.
	OutcomeSaveFailed Outcome = "save_failed"
	// OutcomeFailed is any other failure of the harness or the decision.
	OutcomeFailed Outcome = "failed"
)

// Record is one review attempt. It names no entry, no answer, no review text,
// and no path: only what ran, what it consumed, and how it ended.
type Record struct {
	Version     int    `json:"v"`
	Time        string `json:"time"`
	Kind        string `json:"kind"`
	WindowStart string `json:"window_start"`
	WindowEnd   string `json:"window_end"`
	Harness     string `json:"harness"`
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
	Effort      string `json:"effort,omitempty"`
	// Tokens is absent when the harness did not report usage.
	Tokens *Tokens `json:"tokens,omitempty"`
	// CostUSD is absent when it could not be known; CostSource then is too.
	CostUSD *float64 `json:"cost_usd,omitempty"`
	// CostSource is "pricing YYYY-MM-DD" for a cost computed from the table
	// row verified that day, or "harness" for one the harness reported.
	CostSource string  `json:"cost_source,omitempty"`
	LatencyMS  int64   `json:"latency_ms"`
	Outcome    Outcome `json:"outcome"`
}

// Line is the record as one ledger line, newline included.
func (r Record) Line() ([]byte, error) {
	r.Version = RecordVersion
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Month is the calendar month the attempt was made in, as YYYY-MM, read from
// the recorded local time.
func (r Record) Month() string {
	if len(r.Time) < 7 {
		return ""
	}
	return r.Time[:7]
}

// ReadLedger decodes every line it can. A line that is not a record of this
// version is counted in skipped rather than failing the read, so one damaged
// line never hides the rest.
func ReadLedger(r io.Reader) (records []Record, skipped int, err error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var record Record
		if err := json.Unmarshal(line, &record); err != nil || record.Version != RecordVersion || record.Month() == "" {
			skipped++
			continue
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return records, skipped, fmt.Errorf("reading the usage ledger: %w", err)
	}
	return records, skipped, nil
}

// Group summarizes the attempts of one review kind in one month. Percentiles
// are over the attempts that reported the value; they are nil when none did.
type Group struct {
	Month         string   `json:"month"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count"`
	CostKnown     int      `json:"cost_known"`
	TotalCostUSD  float64  `json:"total_cost_usd"`
	CostP50USD    *float64 `json:"cost_p50_usd"`
	CostP90USD    *float64 `json:"cost_p90_usd"`
	ThinkingKnown int      `json:"thinking_known"`
	ThinkingP50   *int64   `json:"thinking_p50"`
	ThinkingP90   *int64   `json:"thinking_p90"`
}

// kindOrder lists review kinds shortest span first; any other kind sorts after
// them by name.
var kindOrder = map[string]int{"weekly": 0, "monthly": 1, "quarterly": 2, "yearly": 3}

// Summarize groups records by month and kind, oldest month first.
func Summarize(records []Record) []Group {
	type key struct{ month, kind string }
	costs := map[key][]float64{}
	thinking := map[key][]int64{}
	groups := map[key]*Group{}
	for _, record := range records {
		k := key{record.Month(), record.Kind}
		group := groups[k]
		if group == nil {
			group = &Group{Month: k.month, Kind: k.kind}
			groups[k] = group
		}
		group.Count++
		if record.CostUSD != nil {
			group.CostKnown++
			group.TotalCostUSD += *record.CostUSD
			costs[k] = append(costs[k], *record.CostUSD)
		}
		if record.Tokens != nil && record.Tokens.Thinking != nil {
			group.ThinkingKnown++
			thinking[k] = append(thinking[k], *record.Tokens.Thinking)
		}
	}

	out := make([]Group, 0, len(groups))
	for k, group := range groups {
		if values := costs[k]; len(values) > 0 {
			sort.Float64s(values)
			group.CostP50USD = new(nearestRank(values, 50))
			group.CostP90USD = new(nearestRank(values, 90))
		}
		if values := thinking[k]; len(values) > 0 {
			slices.Sort(values)
			group.ThinkingP50 = new(nearestRank(values, 50))
			group.ThinkingP90 = new(nearestRank(values, 90))
		}
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Month != out[j].Month {
			return out[i].Month < out[j].Month
		}
		ri, iKnown := kindOrder[out[i].Kind]
		rj, jKnown := kindOrder[out[j].Kind]
		if iKnown != jKnown {
			return iKnown
		}
		if ri != rj {
			return ri < rj
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// nearestRank is the p-th percentile of sorted values: the smallest value with
// at least p percent of the values at or below it.
func nearestRank[T any](sorted []T, p float64) T {
	rank := max(int(math.Ceil(p/100*float64(len(sorted)))), 1)
	return sorted[rank-1]
}
