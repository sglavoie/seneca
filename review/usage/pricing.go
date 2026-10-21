// Package usage prices a review's token usage and summarizes the usage ledger.
// It holds no content: every value here is a count, a rate, a date, or a name.
package usage

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// DateLayout is how a `verified` date is written and compared.
const DateLayout = "2006-01-02"

// StaleAfterDays is how old the oldest `verified` date may be before doctor
// asks for the rates to be checked again.
const StaleAfterDays = 45

//go:embed pricing.toml
var embeddedPricing []byte

// Price is one row of the table: a model's rates in USD per million tokens.
type Price struct {
	Provider      string  `toml:"provider"`
	Model         string  `toml:"model"`
	Input         float64 `toml:"input"`
	Output        float64 `toml:"output"`
	CacheRead     float64 `toml:"cache_read"`
	CacheWrite5m  float64 `toml:"cache_write_5m"`
	CacheWrite1h  float64 `toml:"cache_write_1h"`
	BatchDiscount float64 `toml:"batch_discount"`
	// AboveInputTokens, when set, is the prompt size past which AboveInput and
	// AboveOutput replace Input and Output for the whole request.
	AboveInputTokens int64   `toml:"above_input_tokens"`
	AboveInput       float64 `toml:"above_input"`
	AboveOutput      float64 `toml:"above_output"`
	// Verified is the date the rates were last checked, as YYYY-MM-DD.
	Verified string `toml:"-"`
	Source   string `toml:"source"`
}

// Table is the pricing in force: the embedded rows with any override applied.
type Table struct {
	Rows []Price
	// Override is the override file that was applied, or empty.
	Override string
}

// OverridePath is where a home's pricing override lives.
func OverridePath(home string) string {
	return filepath.Join(home, ".config", "seneca", "pricing.toml")
}

// Embedded is the table built into the binary.
func Embedded() Table {
	rows, err := decodePricing(embeddedPricing, "the embedded pricing table")
	if err != nil {
		// Only reachable by editing pricing.toml into something invalid; the
		// package's tests decode it.
		panic(err)
	}
	return Table{Rows: rows}
}

// Load is the embedded table with home's override applied by row. A missing
// override, or no home to look in, is not an error; an unreadable or invalid
// override is, and the embedded table is still returned beside it so a caller
// can carry on with it.
func Load(home string) (Table, error) {
	table := Embedded()
	if home == "" {
		return table, nil
	}
	path := OverridePath(home)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return table, nil
	}
	if err != nil {
		return table, fmt.Errorf("reading %s: %w", path, err)
	}
	overrides, err := decodePricing(data, path)
	if err != nil {
		return table, err
	}
	for _, row := range overrides {
		if i := table.index(row.Provider, row.Model); i >= 0 {
			table.Rows[i] = row
		} else {
			table.Rows = append(table.Rows, row)
		}
	}
	table.Override = path
	return table, nil
}

// pricingRow is a row as written: `verified` is a TOML date.
type pricingRow struct {
	Price
	Verified time.Time `toml:"verified"`
}

func decodePricing(data []byte, name string) ([]Price, error) {
	var file struct {
		Price []pricingRow `toml:"price"`
	}
	meta, err := toml.Decode(string(data), &file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, key := range undecoded {
			keys[i] = key.String()
		}
		return nil, fmt.Errorf("%s: unknown keys %s", name, strings.Join(keys, ", "))
	}

	seen := map[string]bool{}
	rows := make([]Price, 0, len(file.Price))
	for i, raw := range file.Price {
		row := raw.Price
		if raw.Verified.IsZero() {
			return nil, fmt.Errorf("%s: price row %d has no verified date", name, i+1)
		}
		row.Verified = raw.Verified.Format(DateLayout)
		if err := row.validate(); err != nil {
			return nil, fmt.Errorf("%s: price row %d: %w", name, i+1, err)
		}
		key := row.Provider + "/" + row.Model
		if seen[key] {
			return nil, fmt.Errorf("%s: %s is priced twice", name, key)
		}
		seen[key] = true
		rows = append(rows, row)
	}
	return rows, nil
}

func (p Price) validate() error {
	if strings.TrimSpace(p.Provider) == "" || strings.TrimSpace(p.Model) == "" {
		return fmt.Errorf("provider and model are required")
	}
	if strings.TrimSpace(p.Source) == "" {
		return fmt.Errorf("%s/%s has no source URL", p.Provider, p.Model)
	}
	for _, rate := range []float64{p.Input, p.Output, p.CacheRead, p.CacheWrite5m, p.CacheWrite1h, p.AboveInput, p.AboveOutput, p.BatchDiscount} {
		if rate < 0 {
			return fmt.Errorf("%s/%s has a negative rate", p.Provider, p.Model)
		}
	}
	if p.BatchDiscount >= 1 {
		return fmt.Errorf("%s/%s has a batch discount of 100%% or more", p.Provider, p.Model)
	}
	if p.AboveInputTokens < 0 || (p.AboveInputTokens > 0 && (p.AboveInput == 0 || p.AboveOutput == 0)) {
		return fmt.Errorf("%s/%s sets above_input_tokens without both above rates", p.Provider, p.Model)
	}
	return nil
}

func (t Table) index(provider, model string) int {
	for i, row := range t.Rows {
		if row.Provider == provider && row.Model == model {
			return i
		}
	}
	return -1
}

// Lookup finds the row for a provider's model.
func (t Table) Lookup(provider, model string) (Price, bool) {
	if i := t.index(provider, model); i >= 0 {
		return t.Rows[i], true
	}
	return Price{}, false
}

// Oldest is the row whose rates were checked longest ago: the age of the table
// is the age of its stalest row.
func (t Table) Oldest() (Price, bool) {
	if len(t.Rows) == 0 {
		return Price{}, false
	}
	rows := append([]Price(nil), t.Rows...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Verified < rows[j].Verified })
	return rows[0], true
}

// AgeDays is how many whole days separate a row's verified date from today
// (YYYY-MM-DD). A date in the future is zero days old.
func (p Price) AgeDays(today string) (int, error) {
	verified, err := time.Parse(DateLayout, p.Verified)
	if err != nil {
		return 0, err
	}
	now, err := time.Parse(DateLayout, today)
	if err != nil {
		return 0, err
	}
	days := int(now.Sub(verified).Hours() / 24)
	if days < 0 {
		return 0, nil
	}
	return days, nil
}

// Cost prices one standard-tier request.
func (p Price) Cost(tokens Tokens) float64 {
	input, output := p.Input, p.Output
	if p.AboveInputTokens > 0 && tokens.Prompt() > p.AboveInputTokens {
		input, output = p.AboveInput, p.AboveOutput
	}
	return (float64(tokens.Input)*input +
		float64(tokens.CacheRead)*p.CacheRead +
		float64(tokens.CacheWrite5m)*p.CacheWrite5m +
		float64(tokens.CacheWrite1h)*p.CacheWrite1h +
		float64(tokens.Output)*output) / 1e6
}
