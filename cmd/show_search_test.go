package cmd

import (
	"bytes"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

const showSearchJournal = "" +
	"## 2026-08-10\n" +
	"<!-- seneca:entry v=2 -->\n\n" +
	"### What did I notice today?\nLost my Patience in traffic.\n\n" +
	"### What did I handle well?\nNothing in particular.\n\n" +
	"---\n\n" +
	"## 2026-08-20\n\n" +
	"### What did I notice today?\nA calm evening.\n\n" +
	"---\n\n" +
	"## 2026-09-02\n\n" +
	"### What did I notice today?\nMore patience than last week.\nStill short on patience in queues.\n\n" +
	"---\n"

func resetShowFlags() {
	showLast, showDate, showRandom, showMonth = 0, "", false, ""
	showFrom, showTo, showSearch, showOnDay = "", "", "", false
	showCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
}

// runShowCLI drives `seneca show` against a temporary journal. Output is not a
// terminal, so it is the plain form a pipe would carry.
func runShowCLI(t *testing.T, journal string, args ...string) (string, error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "reflections.md")
	if err := os.WriteFile(path, []byte(journal), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENECA_FILE", path)

	var out bytes.Buffer
	resetShowFlags()
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(append([]string{"show"}, args...))
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		resetShowFlags()
		showCmd.SilenceUsage = false
	})

	err := rootCmd.Execute()
	return out.String(), err
}

func TestShowSearchListsMatchingLinesIgnoringCaseAndHeadings(t *testing.T) {
	out, err := runShowCLI(t, showSearchJournal, "--search", "patience")
	if err != nil {
		t.Fatalf("show --search returned error: %v", err)
	}
	want := "" +
		"2026-08-10\n" +
		"  Lost my Patience in traffic.\n" +
		"\n" +
		"2026-09-02\n" +
		"  More patience than last week.\n" +
		"  Still short on patience in queues.\n" +
		"\n" +
		"2 entries mention \"patience\" in 3 lines; `seneca show --date DATE` reads one in full.\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}

	// The questions repeat in every entry; searching them would match it all.
	out, err = runShowCLI(t, showSearchJournal, "--search", "notice")
	if err != nil || out != "No entry mentions \"notice\".\n" {
		t.Fatalf("expected headings to be ignored, got %q, %v", out, err)
	}
	// Markers are structure, not writing.
	out, err = runShowCLI(t, showSearchJournal, "--search", "seneca:entry")
	if err != nil || !strings.HasPrefix(out, "No entry mentions") {
		t.Fatalf("expected markers to be ignored, got %q, %v", out, err)
	}
}

func TestShowSearchNarrowsToAMonthOrRange(t *testing.T) {
	out, err := runShowCLI(t, showSearchJournal, "--search", "patience", "--month=2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "2026-08-10") || !strings.Contains(out, "1 entry in that range mentions \"patience\" in 2 lines") {
		t.Fatalf("expected only September, got:\n%s", out)
	}

	out, err = runShowCLI(t, showSearchJournal, "--search", "patience", "--from", "2026-08-15", "--to", "2026-08-31")
	if err != nil || out != "No entry in that range mentions \"patience\".\n" {
		t.Fatalf("expected no match inside the range, got %q, %v", out, err)
	}
}

func TestShowRangeKeepsInclusiveBoundsAndOpenEnds(t *testing.T) {
	out, err := runShowCLI(t, showSearchJournal, "--from", "2026-08-20", "--to", "2026-09-02")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "## 2026-08-10") || !strings.Contains(out, "## 2026-08-20") || !strings.Contains(out, "## 2026-09-02") {
		t.Fatalf("expected both bounds included and nothing before, got:\n%s", out)
	}

	out, err = runShowCLI(t, showSearchJournal, "--to", "2026-08-15")
	if err != nil || !strings.HasPrefix(out, "## 2026-08-10") || strings.Contains(out, "2026-08-20") {
		t.Fatalf("expected an open start, got %q, %v", out, err)
	}

	out, err = runShowCLI(t, showSearchJournal, "--from", "2026-09-03")
	if err != nil || !strings.HasPrefix(out, "No entries from 2026-09-03 on. Nearest: previous 2026-09-02") {
		t.Fatalf("expected an empty open range to point back, got %q, %v", out, err)
	}
}

func TestShowRefusesConflictingSearchAndRangeFlags(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--from", "2026-09-02", "--to", "2026-08-01"}, "invalid range: --from 2026-09-02 is after --to 2026-08-01"},
		{[]string{"--from", "2026-9-2"}, "invalid --from \"2026-9-2\""},
		{[]string{"--month", "--to", "2026-08-01"}, "mutually exclusive"},
		{[]string{"--search", "x", "--last", "2"}, "--search combines only with --month or --from/--to"},
		{[]string{"--search", "x", "--random"}, "--search combines only"},
		{[]string{"--search", " "}, "invalid --search: the phrase is blank"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			_, err := runShowCLI(t, showSearchJournal, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}

func TestSnippetAroundTrimsLongLinesAtRuneBoundaries(t *testing.T) {
	t.Parallel()

	pattern := engine.SearchPattern("patience")
	long := strings.Repeat("é", 100) + " patience " + strings.Repeat("ü", 100)
	got := engine.SnippetAround(long, pattern)
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") || !strings.Contains(got, "patience") {
		t.Fatalf("expected a trimmed snippet around the match, got %q", got)
	}
	if n := len([]rune(got)); n != 2*engine.SearchContextRunes+len("patience")+2 {
		t.Fatalf("expected %d runes, got %d: %q", 2*engine.SearchContextRunes+len("patience")+2, n, got)
	}
	if short := engine.SnippetAround("Some patience.", pattern); short != "Some patience." {
		t.Fatalf("expected a short line unchanged, got %q", short)
	}
	// Regexp metacharacters in the phrase are literal.
	if hits := engine.SearchEntries([]engine.ReflectionEntry{{Date: "2026-01-01", Raw: "## 2026-01-01\n\nWhy (really)?\n"}}, engine.SearchPattern("(really)")); len(hits) != 1 {
		t.Fatalf("expected a literal match on punctuation, got %v", hits)
	}
}

func TestOnThisDayKeepsEarlierEntriesOnTheSameDayOfTheMonth(t *testing.T) {
	t.Parallel()

	entries := []engine.ReflectionEntry{
		{Date: "2025-10-04"}, {Date: "2026-08-04"}, {Date: "2026-09-03"},
		{Date: "2026-09-04"}, {Date: "2026-10-04"}, {Date: ""},
	}
	var got []string
	for _, e := range entriesOnThisDay(entries, "2026-10-04") {
		got = append(got, e.Date)
	}
	if strings.Join(got, " ") != "2025-10-04 2026-08-04 2026-09-04" {
		t.Fatalf("expected earlier 4ths only, got %v", got)
	}

	for date, want := range map[string]string{
		"2026-10-01": "1st", "2026-10-02": "2nd", "2026-10-03": "3rd", "2026-10-04": "4th",
		"2026-10-11": "11th", "2026-10-12": "12th", "2026-10-13": "13th",
		"2026-10-21": "21st", "2026-10-22": "22nd", "2026-10-23": "23rd", "2026-10-31": "31st",
	} {
		if got := ordinalDay(date); got != want {
			t.Errorf("ordinalDay(%s) = %q, want %q", date, got, want)
		}
	}
}

func TestShowOnThisDayRefusesOtherSelections(t *testing.T) {
	for _, args := range [][]string{
		{"--on-this-day", "--last", "2"},
		{"--on-this-day", "--month"},
		{"--on-this-day", "--search", "x"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := runShowCLI(t, showSearchJournal, args...); err == nil {
				t.Fatalf("expected %v to be refused", args)
			}
		})
	}
}
