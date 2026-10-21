package usage

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheEmbeddedTableIsSeededFromTheCostsPage(t *testing.T) {
	t.Parallel()

	table := Embedded()
	for _, want := range []struct {
		provider, model          string
		input, output, cacheRead float64
	}{
		{"anthropic", "claude-opus-5-5", 4, 20, 0.20},
		{"anthropic", "claude-fable-5-1", 10, 50, 0.25},
		{"anthropic", "claude-sonnet-5-5", 2, 10, 0.10},
		{"openai", "gpt-6-astra", 10, 50, 1},
		{"openai", "gpt-6.1-sol", 2, 10, 0.10},
		{"google", "gemini-3.1-pro-preview", 2, 12, 0},
	} {
		row, ok := table.Lookup(want.provider, want.model)
		if !ok {
			t.Fatalf("no row for %s/%s", want.provider, want.model)
		}
		if row.Input != want.input || row.Output != want.output || row.CacheRead != want.cacheRead || row.BatchDiscount != 0.5 {
			t.Errorf("%s/%s rates %+v", want.provider, want.model, row)
		}
		if row.Verified != "2026-10-14" || !strings.HasPrefix(row.Source, "https://") {
			t.Errorf("%s/%s verified %q from %q", want.provider, want.model, row.Verified, row.Source)
		}
		if want.provider == "anthropic" && (row.CacheWrite5m != row.Input*1.25 || row.CacheWrite1h != row.Input*2) {
			t.Errorf("%s cache writes %v / %v are not 1.25x and 2x input", want.model, row.CacheWrite5m, row.CacheWrite1h)
		}
	}
	if len(table.Rows) != 6 {
		t.Fatalf("expected six rows, got %d", len(table.Rows))
	}
}

func TestAnOverrideReplacesByRowAndAddsNewRows(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	path := OverridePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	override := `
[[price]]
provider = "anthropic"
model = "claude-opus-5-5"
input = 3.0
output = 15.0
verified = 2026-11-30
source = "https://example.invalid/a"

[[price]]
provider = "openai"
model = "gpt-7-nova"
input = 1.0
output = 2.0
verified = 2026-11-30
source = "https://example.invalid/b"
`
	if err := os.WriteFile(path, []byte(override), 0644); err != nil {
		t.Fatal(err)
	}
	table, err := Load(home)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	opus, _ := table.Lookup("anthropic", "claude-opus-5-5")
	if opus.Input != 3 || opus.CacheRead != 0 || opus.Verified != "2026-11-30" {
		t.Fatalf("expected the override row whole, got %+v", opus)
	}
	if _, ok := table.Lookup("openai", "gpt-7-nova"); !ok {
		t.Fatal("expected the new row to be added")
	}
	if len(table.Rows) != 7 || table.Override != path {
		t.Fatalf("expected seven rows from %s, got %d from %q", path, len(table.Rows), table.Override)
	}
	if oldest, _ := table.Oldest(); oldest.Verified != "2026-10-14" {
		t.Fatalf("expected the oldest row to set the table's age, got %s", oldest.Verified)
	}
}

func TestAnInvalidOverrideIsAnErrorBesideTheEmbeddedTable(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"unknown key": "[[price]]\nprovider = \"a\"\nmodel = \"b\"\ninput = 1.0\noutput = 1.0\nverified = 2026-10-14\nsource = \"s\"\nbogus = 1\n",
		"no verified": "[[price]]\nprovider = \"a\"\nmodel = \"b\"\ninput = 1.0\noutput = 1.0\nsource = \"s\"\n",
		"no source":   "[[price]]\nprovider = \"a\"\nmodel = \"b\"\ninput = 1.0\noutput = 1.0\nverified = 2026-10-14\n",
		"negative":    "[[price]]\nprovider = \"a\"\nmodel = \"b\"\ninput = -1.0\noutput = 1.0\nverified = 2026-10-14\nsource = \"s\"\n",
		"twice":       "[[price]]\nprovider = \"a\"\nmodel = \"b\"\nverified = 2026-10-14\nsource = \"s\"\n[[price]]\nprovider = \"a\"\nmodel = \"b\"\nverified = 2026-10-14\nsource = \"s\"\n",
		"not toml":    "this is not toml",
		"above alone": "[[price]]\nprovider = \"a\"\nmodel = \"b\"\nabove_input_tokens = 10\nverified = 2026-10-14\nsource = \"s\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			path := OverridePath(home)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
			table, err := Load(home)
			if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("expected an error naming %s, got %v", path, err)
			}
			if len(table.Rows) != len(Embedded().Rows) || table.Override != "" {
				t.Fatal("expected the embedded table beside the error")
			}
		})
	}
}

func TestNoOverrideAndNoHomeAreTheEmbeddedTable(t *testing.T) {
	t.Parallel()

	for _, home := range []string{"", t.TempDir()} {
		table, err := Load(home)
		if err != nil || len(table.Rows) != len(Embedded().Rows) || table.Override != "" {
			t.Fatalf("Load(%q) = %d rows, %q, %v", home, len(table.Rows), table.Override, err)
		}
	}
}

func TestCostPricesEveryTokenKindAndTheLongContextTier(t *testing.T) {
	t.Parallel()

	opus, _ := Embedded().Lookup("anthropic", "claude-opus-5-5")
	thinking := int64(5000)
	got := opus.Cost(Tokens{Input: 1000, CacheRead: 2000, CacheWrite5m: 100, CacheWrite1h: 50, Output: 6000, Thinking: &thinking})
	// Thinking is already inside Output and is not charged twice.
	want := (1000*4.0 + 2000*0.2 + 100*5.0 + 50*8.0 + 6000*20.0) / 1e6
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("cost %v, want %v", got, want)
	}

	astra, _ := Embedded().Lookup("openai", "gpt-6-astra")
	if got, want := astra.Cost(Tokens{Input: 272000, Output: 1000}), (272000*10.0+1000*50.0)/1e6; math.Abs(got-want) > 1e-12 {
		t.Fatalf("at the threshold: cost %v, want %v", got, want)
	}
	if got, want := astra.Cost(Tokens{Input: 272000, CacheRead: 1, Output: 1000}), (272000*20.0+1*1.0+1000*75.0)/1e6; math.Abs(got-want) > 1e-12 {
		t.Fatalf("above the threshold: cost %v, want %v", got, want)
	}
}

func TestAgeDays(t *testing.T) {
	t.Parallel()

	row := Price{Verified: "2026-10-14"}
	for today, want := range map[string]int{"2026-10-14": 0, "2026-11-28": 45, "2026-11-29": 46, "2026-04-02": 0} {
		if got, err := row.AgeDays(today); err != nil || got != want {
			t.Errorf("AgeDays(%s) = %d, %v; want %d", today, got, err, want)
		}
	}
}
