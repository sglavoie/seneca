package cmd

import (
	"encoding/json"
	"errors"
	"github.com/sglavoie/seneca/review/usage"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingRuntime is fakeReview's runtime with the ledger captured instead of
// written.
func recordingRuntime(f *fakeReview, now time.Time, records *[]usage.Record) reviewRuntime {
	runtime := f.runtime(now)
	runtime.recordUsage = func(r usage.Record) { *records = append(*records, r) }
	return runtime
}

func requireOneRecord(t *testing.T, records []usage.Record) usage.Record {
	t.Helper()
	if len(records) != 1 {
		t.Fatalf("expected exactly one usage record per attempt, got %d: %+v", len(records), records)
	}
	return records[0]
}

func TestEveryReviewAttemptRecordsItsOutcomeOnce(t *testing.T) {
	valid := reviewPayload(`{"summary":"You mostly held the focus.","evidence":[]}`)
	for _, tc := range []struct {
		name string
		fake *fakeReview
		want usage.Outcome
	}{
		{"saved", &fakeReview{tty: true, response: valid, decision: reviewDecision{kind: reviewSaveWithoutFocus}}, usage.OutcomeSaved},
		{"discarded", &fakeReview{tty: true, response: valid, decision: reviewDecision{kind: reviewDiscard}}, usage.OutcomeDiscarded},
		{"aborted at the decision", &fakeReview{tty: true, response: valid, decideErr: io.EOF}, usage.OutcomeDiscarded},
		{"piped", &fakeReview{tty: false, response: valid}, usage.OutcomePiped},
		{"invalid response", &fakeReview{tty: true, response: `{"wins":[]}`}, usage.OutcomeInvalidResponse},
		{"timeout", &fakeReview{tty: true, harnessErr: &harnessTimeoutError{harness: harnessClaude, limit: time.Minute}}, usage.OutcomeTimeout},
		{"harness failure", &fakeReview{tty: true, harnessErr: errors.New("exit status 1")}, usage.OutcomeFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := archiveWithFocus(t, 18, "Ship the archive layer.")
			var records []usage.Record
			_, _ = quietly(t, func() error {
				return executeReview(recordingRuntime(tc.fake, archiveTime(24, 20, 0), &records), reviewConfig{Harness: harnessCodex}, path, savedWindow(t))
			})
			record := requireOneRecord(t, records)
			if record.Outcome != tc.want {
				t.Fatalf("expected outcome %s, got %s", tc.want, record.Outcome)
			}
			if record.Kind != "weekly" || record.WindowStart != "2026-08-17" || record.WindowEnd != "2026-08-23" || record.Harness != "codex" {
				t.Fatalf("unexpected record identity: %+v", record)
			}
			// A CLI harness other than claude reports outcome and latency only.
			if record.Tokens != nil || record.CostUSD != nil || record.Provider != "" {
				t.Fatalf("expected no usage from the codex harness, got %+v", record)
			}
		})
	}
}

func TestAnEmptyWindowRecordsNoAttempt(t *testing.T) {
	path := archiveWithFocus(t, 18, "Ship the archive layer.")
	var records []usage.Record
	window := testReviewWindow(t, "2026-08-17", "2026-08-23")
	if err := executeReview(recordingRuntime(&fakeReview{tty: true}, archiveTime(24, 20, 0), &records), reviewConfig{Harness: harnessClaude}, path, window); err != nil {
		t.Fatalf("executeReview returned error: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected no record without a harness call, got %+v", records)
	}
}

func TestAnthropicUsageIsRecordedAndPricedFromTheTable(t *testing.T) {
	server := (&fakeAnthropic{text: reviewPayload(`{"summary":"Held.","evidence":[]}`)}).start(t)
	reviewer := providerReviewer{getenv: envWith(map[string]string{"ANTHROPIC_API_KEY": fakeAnthropicKey}), keychain: noKeychain, baseURL: server.URL}
	review := &fakeReview{tty: false}
	runtime := providerRuntime(review, archiveTime(24, 20, 0), reviewer)
	var records []usage.Record
	runtime.recordUsage = func(r usage.Record) { records = append(records, r) }

	if err := executeReview(runtime, anthropicConfig(), archiveWithFocus(t, 1, "Old focus."), savedWindow(t)); err != nil {
		t.Fatalf("executeReview returned error: %v", err)
	}
	record := requireOneRecord(t, records)
	priceUsageRecord(&record, usage.Embedded())

	tokens := record.Tokens
	if tokens == nil || tokens.Input != 12 || tokens.CacheRead != 100 || tokens.CacheWrite5m != 8 || tokens.Output != 42 || tokens.Thinking == nil || *tokens.Thinking != 30 {
		t.Fatalf("unexpected tokens: %+v", tokens)
	}
	if record.Provider != "anthropic" || record.Model != "claude-opus-5-5" || record.Effort != "high" || record.Outcome != usage.OutcomePiped {
		t.Fatalf("unexpected record: %+v", record)
	}
	// 12×$4 + 100×$0.20 + 8×$5 + 42×$20, per million tokens.
	requireCost(t, record, 0.000948, "pricing 2026-10-14")
}

func TestOpenAIUsageSeparatesCachedInputAndReasoning(t *testing.T) {
	server := (&fakeOpenAI{text: reviewPayload(`{"summary":"Held.","evidence":[]}`)}).start(t)
	reviewer := providerReviewer{getenv: envWith(map[string]string{"OPENAI_API_KEY": fakeOpenAIKey}), keychain: noKeychain, baseURL: server.URL}
	runtime := providerRuntime(&fakeReview{tty: false}, archiveTime(24, 20, 0), reviewer)
	var records []usage.Record
	runtime.recordUsage = func(r usage.Record) { records = append(records, r) }

	if err := executeReview(runtime, openAIConfig(), archiveWithFocus(t, 1, "Old focus."), savedWindow(t)); err != nil {
		t.Fatalf("executeReview returned error: %v", err)
	}
	record := requireOneRecord(t, records)
	priceUsageRecord(&record, usage.Embedded())
	tokens := record.Tokens
	if tokens == nil || tokens.Input != 8 || tokens.CacheRead != 4 || tokens.Output != 42 || tokens.Thinking == nil || *tokens.Thinking != 30 {
		t.Fatalf("unexpected tokens: %+v", tokens)
	}
	// 8×$10 + 4×$1 + 42×$50, per million tokens.
	requireCost(t, record, 0.002184, "pricing 2026-10-14")
}

func TestARefusedReviewStillRecordsWhatItWasBilledFor(t *testing.T) {
	server := (&fakeAnthropic{stopReason: "refusal", stopDetails: `{"type":"refusal","category":"cyber","explanation":"Declined."}`}).start(t)
	reviewer := providerReviewer{getenv: envWith(map[string]string{"ANTHROPIC_API_KEY": fakeAnthropicKey}), keychain: noKeychain, baseURL: server.URL}
	runtime := providerRuntime(&fakeReview{tty: true}, archiveTime(24, 20, 0), reviewer)
	var records []usage.Record
	runtime.recordUsage = func(r usage.Record) { records = append(records, r) }

	if err := executeReview(runtime, anthropicConfig(), archiveWithFocus(t, 1, "Old focus."), savedWindow(t)); err == nil {
		t.Fatal("expected the refusal to fail the review")
	}
	record := requireOneRecord(t, records)
	if record.Outcome != usage.OutcomeRefused || record.Tokens == nil || record.Tokens.Output != 42 {
		t.Fatalf("expected a refused record with its usage, got %+v", record)
	}
}

func TestClaudeHarnessUsageAndCostComeFromItsResultEvent(t *testing.T) {
	t.Parallel()

	raw := `[{"type":"system"},{"type":"result","total_cost_usd":0.0421,` +
		`"usage":{"input_tokens":5,"cache_creation_input_tokens":300,"cache_read_input_tokens":2000,"output_tokens":900,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":300}},` +
		`"modelUsage":{"claude-haiku-5-5":{"costUSD":0.0001},"claude-opus-5-5":{"costUSD":0.042}},"structured_output":{}}]`
	reply := claudeHarnessReply(reviewConfig{Harness: harnessClaude}, []byte(raw))
	if string(reply.raw) != raw {
		t.Fatal("expected the raw reply to pass through untouched")
	}
	if reply.model != "claude-opus-5-5" {
		t.Fatalf("expected the model that cost the most, got %q", reply.model)
	}
	if reply.tokens == nil || reply.tokens.Input != 5 || reply.tokens.CacheRead != 2000 || reply.tokens.CacheWrite1h != 300 || reply.tokens.CacheWrite5m != 0 || reply.tokens.Output != 900 || reply.tokens.Thinking != nil {
		t.Fatalf("unexpected tokens: %+v", reply.tokens)
	}

	record := newUsageRecord(archiveTime(24, 20, 0), reviewConfig{Harness: harnessClaude}, savedWindow(t), reply, 1500*time.Millisecond, usage.OutcomeSaved)
	priceUsageRecord(&record, usage.Embedded())
	requireCost(t, record, 0.0421, "harness")
	if record.LatencyMS != 1500 || record.Provider != "anthropic" {
		t.Fatalf("unexpected record: %+v", record)
	}

	// The single-object form carries the same event.
	single := claudeHarnessReply(reviewConfig{Harness: harnessClaude, Model: "opus"}, []byte(`{"type":"result","total_cost_usd":0.5}`))
	if single.model != "opus" || single.costUSD == nil || *single.costUSD != 0.5 || single.tokens != nil {
		t.Fatalf("unexpected single-event reply: %+v", single)
	}
	// Another CLI's output is never read for usage.
	if other := claudeHarnessReply(reviewConfig{Harness: harnessCodex}, []byte(raw)); other.tokens != nil || other.costUSD != nil {
		t.Fatalf("expected codex output to carry no usage, got %+v", other)
	}
}

func TestAFailedClaudeRunStillRecordsWhatItWasBilledFor(t *testing.T) {
	// The fake prints a stderr line beside its usage-bearing result event and
	// then fails, as a CLI that errors after billing does.
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'warning: something went wrong' >&2\ncat <<'JSON'\n" +
		`{"type":"result","is_error":true,"total_cost_usd":0.0421,"usage":{"input_tokens":5,"output_tokens":900},"modelUsage":{"claude-opus-5-5":{"costUSD":0.0421}}}` +
		"\nJSON\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake harness: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var records []usage.Record
	var replies []reviewReply
	runtime := reviewRuntime{
		now: time.Now,
		harness: func(cfg reviewConfig, prompt string) (reviewReply, error) {
			reply, err := obtainReviewResponse(cfg, prompt)
			replies = append(replies, reply)
			return reply, err
		},
		out:         io.Discard,
		recordUsage: func(r usage.Record) { records = append(records, r) },
	}
	err := executeReview(runtime, reviewConfig{Harness: harnessClaude, Timeout: time.Minute}, archiveWithFocus(t, 1, "Old focus."), savedWindow(t))
	if err == nil {
		t.Fatal("expected a failed harness to fail the review")
	}
	if len(replies) != 1 || replies[0].raw != nil {
		t.Fatalf("expected a failed run's output never to be offered as a review, got %+v", replies)
	}

	record := requireOneRecord(t, records)
	if record.Outcome != usage.OutcomeFailed || record.Model != "claude-opus-5-5" {
		t.Fatalf("unexpected record: %+v", record)
	}
	if record.Tokens == nil || record.Tokens.Input != 5 || record.Tokens.Output != 900 {
		t.Fatalf("expected the reported tokens, got %+v", record.Tokens)
	}
	if record.CostUSD == nil || *record.CostUSD != 0.0421 {
		t.Fatalf("expected the reported cost, got %v", record.CostUSD)
	}
}

func TestAModelWithNoPriceRowIsRecordedWithoutACost(t *testing.T) {
	t.Parallel()

	record := usage.Record{Provider: "anthropic", Model: "claude-unknown", Tokens: &usage.Tokens{Input: 10, Output: 10}}
	priceUsageRecord(&record, usage.Embedded())
	if record.CostUSD != nil || record.CostSource != "" {
		t.Fatalf("expected no guessed cost, got %+v", record)
	}
}

func requireCost(t *testing.T, record usage.Record, want float64, source string) {
	t.Helper()
	if record.CostUSD == nil || math.Abs(*record.CostUSD-want) > 1e-12 || record.CostSource != source {
		t.Fatalf("expected cost %v from %q, got %v from %q", want, source, record.CostUSD, record.CostSource)
	}
}

func TestTheLedgerIsCreatedByTheFirstWriteAndAppendedAfter(t *testing.T) {
	dir := t.TempDir()
	reflections := filepath.Join(dir, "reflections.md")
	ledger := usageLedgerPathFor(reflections)
	if want := filepath.Join(dir, ".seneca", "journals", "reflections.md", "desktop", "usage.jsonl"); ledger != want {
		t.Fatalf("ledger path %s, want %s", ledger, want)
	}

	var warnings strings.Builder
	record := usageLedgerRecorder(reflections, t.TempDir(), &warnings)
	for _, outcome := range []usage.Outcome{usage.OutcomeSaved, usage.OutcomeRefused} {
		record(usage.Record{Time: "2026-08-24T20:00:00Z", Kind: "weekly", Harness: "anthropic", Provider: "anthropic", Model: "claude-opus-5-5",
			Tokens: &usage.Tokens{Input: 1000000}, Outcome: outcome})
	}
	if warnings.Len() != 0 {
		t.Fatalf("unexpected warnings: %s", warnings.String())
	}

	info, err := os.Stat(ledger)
	if err != nil {
		t.Fatalf("expected the ledger to exist: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected a private ledger, got mode %04o", info.Mode().Perm())
	}
	file, err := os.Open(ledger)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	records, skipped, err := usage.ReadLedger(file)
	if err != nil || skipped != 0 || len(records) != 2 {
		t.Fatalf("expected two readable records, got %d (skipped %d, err %v)", len(records), skipped, err)
	}
	if records[0].Outcome != usage.OutcomeSaved || records[1].Outcome != usage.OutcomeRefused {
		t.Fatalf("expected records in the order written, got %+v", records)
	}
	if records[0].CostUSD == nil || *records[0].CostUSD != 4 || records[0].CostSource != "pricing 2026-10-14" {
		t.Fatalf("expected the recorder to price the attempt, got %+v", records[0])
	}
}

// Reviews that finish at the same instant each keep their line: the ledger's
// read-modify-write runs under the journal lock, so no writer replaces the
// ledger with a copy that lacks another's record.
func TestConcurrentReviewsEachKeepTheirLedgerLine(t *testing.T) {
	dir := t.TempDir()
	reflections := filepath.Join(dir, "reflections.md")
	var warnings safeBuilder
	record := usageLedgerRecorder(reflections, t.TempDir(), &warnings)

	const attempts = 8
	start := make(chan struct{})
	var done sync.WaitGroup
	for i := 0; i < attempts; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			<-start
			record(usage.Record{Time: "2026-08-24T20:00:00Z", Kind: "weekly", Harness: "codex", Outcome: usage.OutcomeSaved})
		}()
	}
	close(start)
	done.Wait()
	if warnings.String() != "" {
		t.Fatalf("unexpected warnings: %s", warnings.String())
	}

	file, err := os.Open(usageLedgerPathFor(reflections))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	records, skipped, err := usage.ReadLedger(file)
	if err != nil || skipped != 0 || len(records) != attempts {
		t.Fatalf("expected %d records, got %d (skipped %d, err %v)", attempts, len(records), skipped, err)
	}
}

// safeBuilder is a strings.Builder several goroutines can warn into.
type safeBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuilder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuilder) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestALedgerWriteFailureIsAWarningAndTheReviewStillSaves(t *testing.T) {
	path := archiveWithFocus(t, 18, "Ship the archive layer.")
	reflections := filepath.Join(filepath.Dir(path), "reflections.md")
	// A file where the desktop directory belongs makes the write fail.
	metadata := filepath.Dir(filepath.Dir(usageLedgerPathFor(reflections)))
	if err := os.MkdirAll(metadata, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, "desktop"), []byte("in the way"), 0644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeReview{tty: true, response: reviewPayload(`{"summary":"You mostly held the focus.","evidence":[]}`), decision: reviewDecision{kind: reviewSaveWithoutFocus}}
	runtime := fake.runtime(archiveTime(24, 20, 0))
	var warnings strings.Builder
	runtime.recordUsage = usageLedgerRecorder(reflections, t.TempDir(), &warnings)
	if _, err := quietly(t, func() error {
		return executeReview(runtime, reviewConfig{Harness: harnessClaude}, path, savedWindow(t))
	}); err != nil {
		t.Fatalf("expected the review to succeed despite the ledger, got %v", err)
	}
	if !strings.Contains(readFileString(t, path), "You mostly held the focus.") {
		t.Fatal("expected the review to be saved")
	}
	if !strings.Contains(warnings.String(), "warning: usage ledger: could not record this review") {
		t.Fatalf("expected one warning, got %q", warnings.String())
	}
}

func TestAnInvalidPricingOverrideWarnsAndPricesWithTheBuiltInTable(t *testing.T) {
	home := t.TempDir()
	override := usage.OverridePath(home)
	if err := os.MkdirAll(filepath.Dir(override), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte("[[price]]\nprovider = \"anthropic\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	reflections := filepath.Join(t.TempDir(), "reflections.md")
	var warnings strings.Builder
	usageLedgerRecorder(reflections, home, &warnings)(usage.Record{Time: "2026-08-24T20:00:00Z", Kind: "weekly", Provider: "anthropic", Model: "claude-opus-5-5", Tokens: &usage.Tokens{Output: 1000000}, Outcome: usage.OutcomeSaved})
	if !strings.Contains(warnings.String(), "pricing with the built-in table") {
		t.Fatalf("expected a pricing warning, got %q", warnings.String())
	}
	if data := readFileString(t, usageLedgerPathFor(reflections)); !strings.Contains(data, `"cost_usd":20`) {
		t.Fatalf("expected the built-in rate, got %s", data)
	}
}

// fixtureLedger covers two months and three kinds, an unpriced attempt, a CLI
// attempt with no usage, and one damaged line.
const fixtureLedger = `{"v":1,"time":"2026-09-07T20:00:00-04:00","kind":"weekly","window_start":"2026-08-31","window_end":"2026-09-06","harness":"anthropic","provider":"anthropic","model":"claude-opus-5-5","effort":"high","tokens":{"input":3000,"cache_read":0,"cache_write_5m":0,"cache_write_1h":0,"output":9000,"thinking":8000},"cost_usd":0.192,"cost_source":"pricing 2026-10-14","latency_ms":41000,"outcome":"saved"}
{"v":1,"time":"2026-09-14T20:00:00-04:00","kind":"weekly","window_start":"2026-09-07","window_end":"2026-09-13","harness":"anthropic","provider":"anthropic","model":"claude-opus-5-5","effort":"high","tokens":{"input":3000,"cache_read":0,"cache_write_5m":0,"cache_write_1h":0,"output":7000,"thinking":6000},"cost_usd":0.152,"cost_source":"pricing 2026-10-14","latency_ms":38000,"outcome":"discarded"}
{"v":1,"time":"2026-09-21T20:00:00-04:00","kind":"weekly","window_start":"2026-09-14","window_end":"2026-09-20","harness":"anthropic","provider":"anthropic","model":"claude-opus-5-5","effort":"high","tokens":{"input":3000,"cache_read":0,"cache_write_5m":0,"cache_write_1h":0,"output":11000,"thinking":10000},"cost_usd":0.232,"cost_source":"pricing 2026-10-14","latency_ms":52000,"outcome":"saved"}
{"v":1,"time":"2026-09-28T20:00:00-04:00","kind":"weekly","window_start":"2026-09-21","window_end":"2026-09-27","harness":"codex","latency_ms":60000,"outcome":"timeout"}
not json at all
{"v":1,"time":"2026-10-01T09:00:00-04:00","kind":"monthly","window_start":"2026-09-01","window_end":"2026-09-30","harness":"openai","provider":"openai","model":"gpt-6-astra","effort":"high","tokens":{"input":7500,"cache_read":0,"cache_write_5m":0,"cache_write_1h":0,"output":12000,"thinking":11000},"cost_usd":0.675,"cost_source":"pricing 2026-10-14","latency_ms":70000,"outcome":"saved"}
{"v":1,"time":"2026-10-01T09:30:00-04:00","kind":"quarterly","window_start":"2026-07-01","window_end":"2026-09-30","harness":"claude","provider":"anthropic","model":"claude-opus-5-5","tokens":{"input":10,"cache_read":0,"cache_write_5m":0,"cache_write_1h":0,"output":2000},"cost_usd":0.05,"cost_source":"harness","latency_ms":90000,"outcome":"piped"}
`

func writeFixtureLedger(t *testing.T, reflections string) {
	t.Helper()
	ledger := usageLedgerPathFor(reflections)
	if err := os.MkdirAll(filepath.Dir(ledger), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledger, []byte(fixtureLedger), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestUsageSummarizesAFixtureLedger(t *testing.T) {
	j := newCompatJournal(t)
	writeFixtureLedger(t, j.reflections)
	before := snapshotTree(t, j.dir)

	out, err := runCompatCLI(t, "usage")
	if err != nil {
		t.Fatalf("seneca usage returned error: %v", err)
	}
	want := "" +
		"Month    Kind       Attempts  Total cost   Cost p50   Cost p90  Thinking p50  Thinking p90\n" +
		"2026-09  weekly            4     $0.5760    $0.1920    $0.2320          8000         10000\n" +
		"2026-10  monthly           1     $0.6750    $0.6750    $0.6750         11000         11000\n" +
		"2026-10  quarterly         1     $0.0500    $0.0500    $0.0500             —             —\n" +
		"\n" +
		"Counted but not priced: 1 attempt with no known cost.\n" +
		"\n" +
		"Skipped: 1 line of the ledger that could not be read.\n"
	if out != want {
		t.Fatalf("unexpected usage table.\ngot:\n%s\nwant:\n%s", out, want)
	}

	out, err = runCompatCLI(t, "usage", "--json")
	if err != nil {
		t.Fatalf("seneca usage --json returned error: %v", err)
	}
	var summary usageSummary
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("decoding --json: %v\n%s", err, out)
	}
	if summary.SkippedLines != 1 || len(summary.Groups) != 3 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	weekly := summary.Groups[0]
	if weekly.Month != "2026-09" || weekly.Kind != "weekly" || weekly.Count != 4 || weekly.CostKnown != 3 || weekly.ThinkingKnown != 3 ||
		math.Abs(weekly.TotalCostUSD-0.576) > 1e-9 || *weekly.CostP50USD != 0.192 || *weekly.CostP90USD != 0.232 ||
		*weekly.ThinkingP50 != 8000 || *weekly.ThinkingP90 != 10000 {
		t.Fatalf("unexpected weekly group: %+v", weekly)
	}
	if quarterly := summary.Groups[2]; quarterly.ThinkingP50 != nil || quarterly.ThinkingP90 != nil {
		t.Fatalf("expected no thinking percentiles where none was reported, got %+v", quarterly)
	}
	if !strings.Contains(out, `"thinking_p50": null`) {
		t.Fatalf("expected unknown percentiles as null, got %s", out)
	}

	if after := snapshotTree(t, j.dir); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatal("expected seneca usage to change nothing")
	}
}

func TestUsageWithoutALedgerSaysSoAndCreatesNothing(t *testing.T) {
	j := newCompatJournal(t)

	out, err := runCompatCLI(t, "usage")
	if err != nil || !strings.HasPrefix(out, "No review attempts recorded yet") {
		t.Fatalf("unexpected output %q (err %v)", out, err)
	}
	out, err = runCompatCLI(t, "usage", "--json")
	if err != nil || strings.TrimSpace(out) != "{\n  \"groups\": [],\n  \"skipped_lines\": 0\n}" {
		t.Fatalf("unexpected JSON %q (err %v)", out, err)
	}
	if _, err := os.Stat(filepath.Join(j.dir, ".seneca")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no metadata directory, got %v", err)
	}
}

// snapshotTree is every file under dir with its contents.
func snapshotTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		out = append(out, path+"="+string(data))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDoctorCountsTheLedgerAndNeverPrintsALine(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections, "")
	writeFixtureLedger(t, h.reflections)

	report := diagnose(h.env())
	requireDoctor(t, report, doctorInfo, "holds 6 review attempts")
	requireDoctor(t, report, doctorWarn, "the usage ledger has 1 line it cannot read")
	for _, result := range report.results {
		for _, content := range []string{"claude-opus-5-5", "2026-09-07", "not json"} {
			if strings.Contains(result.detail, content) {
				t.Fatalf("doctor printed ledger content %q: %s", content, result.detail)
			}
		}
	}
}

func TestDoctorWarnsOnAStalePricingTableAndAnUnpricedModel(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections, "")
	env := h.env()
	env.keychain = noKeychain

	env.getenv = envWith(map[string]string{"SENECA_FILE": h.reflections, "SENECA_HARNESS": "anthropic"})
	env.today = "2026-11-20"
	requireDoctor(t, diagnose(env), doctorOK, "the pricing table was verified 2026-10-14 or later")

	env.today = "2026-12-01"
	stale := diagnose(env)
	requireDoctor(t, stale, doctorWarn, "the pricing table is 48 days old")

	env.today = "2026-10-15"
	env.getenv = envWith(map[string]string{"SENECA_FILE": h.reflections, "SENECA_HARNESS": "openai", "SENECA_MODEL": "gpt-7-nova"})
	requireDoctor(t, diagnose(env), doctorWarn, "openai model gpt-7-nova has no row in the pricing table")

	// An override row prices it, and its fresh date does not hide older rows.
	override := usage.OverridePath(h.home)
	if err := os.MkdirAll(filepath.Dir(override), 0755); err != nil {
		t.Fatal(err)
	}
	h.write(t, override, "[[price]]\nprovider = \"openai\"\nmodel = \"gpt-7-nova\"\ninput = 1.0\noutput = 2.0\nverified = 2026-12-01\nsource = \"https://example.invalid/pricing\"\n")
	env.today = "2026-12-01"
	report := diagnose(env)
	requireDoctor(t, report, doctorWarn, "the pricing table is 48 days old")
	for _, detail := range report.details(doctorWarn) {
		if strings.Contains(detail, "has no row") {
			t.Fatalf("expected the override to price the model, got %s", detail)
		}
	}
}
