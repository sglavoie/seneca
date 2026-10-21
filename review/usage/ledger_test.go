package usage

import (
	"strings"
	"testing"
)

func TestReadLedgerSkipsWhatItCannotReadAndKeepsTheRest(t *testing.T) {
	t.Parallel()

	ledger := `{"v":1,"time":"2026-09-07T20:00:00Z","kind":"weekly","harness":"codex","latency_ms":1,"outcome":"saved"}

{"v":2,"time":"2026-09-08T20:00:00Z","kind":"weekly","harness":"codex","latency_ms":1,"outcome":"saved"}
{"v":1,"kind":"weekly"}
garbage
{"v":1,"time":"2026-09-09T20:00:00Z","kind":"monthly","harness":"codex","latency_ms":1,"outcome":"piped"}
`
	records, skipped, err := ReadLedger(strings.NewReader(ledger))
	if err != nil || skipped != 3 || len(records) != 2 {
		t.Fatalf("got %d records, %d skipped, %v", len(records), skipped, err)
	}
}

func TestLineRoundTripsAndStampsTheVersion(t *testing.T) {
	t.Parallel()

	cost := 0.25
	line, err := Record{Time: "2026-09-07T20:00:00Z", Kind: "weekly", Harness: "anthropic", CostUSD: &cost, Outcome: OutcomeSaved}.Line()
	if err != nil || !strings.HasSuffix(string(line), "}\n") || !strings.HasPrefix(string(line), `{"v":1,`) {
		t.Fatalf("unexpected line %q (%v)", line, err)
	}
	if strings.Contains(string(line), "tokens") {
		t.Fatalf("expected unknown usage to be omitted, got %s", line)
	}
	records, _, _ := ReadLedger(strings.NewReader(string(line)))
	if len(records) != 1 || *records[0].CostUSD != 0.25 {
		t.Fatalf("round trip lost the record: %+v", records)
	}
}

func TestSummarizeGroupsByMonthThenKindWithNearestRankPercentiles(t *testing.T) {
	t.Parallel()

	var records []Record
	for i, cost := range []float64{0.5, 0.1, 0.4, 0.2, 0.3, 0.6, 0.7, 0.8, 0.9, 1.0} {
		c := cost
		thinking := int64(1000 * (i + 1))
		records = append(records, Record{Time: "2026-10-02T09:00:00-04:00", Kind: "weekly", CostUSD: &c, Tokens: &Tokens{Thinking: &thinking}})
	}
	records = append(records,
		Record{Time: "2026-10-03T09:00:00-04:00", Kind: "yearly"},
		Record{Time: "2026-10-03T09:00:00-04:00", Kind: "ask"},
		Record{Time: "2026-09-30T23:00:00-04:00", Kind: "monthly"},
	)

	groups := Summarize(records)
	var order []string
	for _, g := range groups {
		order = append(order, g.Month+" "+g.Kind)
	}
	if got := strings.Join(order, ", "); got != "2026-09 monthly, 2026-10 weekly, 2026-10 yearly, 2026-10 ask" {
		t.Fatalf("unexpected order: %s", got)
	}

	weekly := groups[1]
	if weekly.Count != 10 || weekly.CostKnown != 10 || *weekly.CostP50USD != 0.5 || *weekly.CostP90USD != 0.9 {
		t.Fatalf("unexpected cost summary: %+v", weekly)
	}
	if *weekly.ThinkingP50 != 5000 || *weekly.ThinkingP90 != 9000 {
		t.Fatalf("unexpected thinking summary: %d / %d", *weekly.ThinkingP50, *weekly.ThinkingP90)
	}
	if yearly := groups[2]; yearly.Count != 1 || yearly.CostP50USD != nil || yearly.ThinkingP90 != nil || yearly.TotalCostUSD != 0 {
		t.Fatalf("expected no percentiles without values, got %+v", yearly)
	}
}
