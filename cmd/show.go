package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"math/rand"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// Selection is one of: default-latest, --last, --date, --month, --from/--to,
// --on-this-day, or --random. --search narrows the whole journal, a --month, or a --from/--to
// range to the entries that mention a phrase, so a theme can be traced through
// the writing without leaving Seneca.
var (
	showLast   int
	showDate   string
	showRandom bool
	showMonth  string
	showFrom   string
	showTo     string
	showSearch string
	showOnDay  bool
)

func init() {
	showCmd.Flags().IntVar(&showLast, "last", 0, "Show last N entries")
	showCmd.Flags().StringVar(&showDate, "date", "", "Show entries for a specific date (YYYY-MM-DD, today, or yesterday)")
	showCmd.Flags().BoolVar(&showRandom, "random", false, "Show one randomly selected entry")
	showCmd.Flags().StringVar(&showMonth, "month", "", "Show entries for a month (YYYY-MM; --month alone for current)")
	showCmd.Flags().Lookup("month").NoOptDefVal = "current"
	showCmd.Flags().StringVar(&showFrom, "from", "", "Show entries on or after a date (YYYY-MM-DD, today, or yesterday)")
	showCmd.Flags().StringVar(&showTo, "to", "", "Show entries on or before a date (YYYY-MM-DD, today, or yesterday)")
	showCmd.Flags().BoolVar(&showOnDay, "on-this-day", false, "Show earlier entries written on today's day of the month")
	showCmd.Flags().StringVar(&showSearch, "search", "", "List the lines that mention a phrase, ignoring case (alone, or with --month or --from/--to)")
	rootCmd.AddCommand(showCmd)
}

var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Display reflection entries",
	Args:  cobra.NoArgs,
	RunE:  runShow,
}

func runShow(cmd *cobra.Command, args []string) error {
	monthSet := cmd.Flags().Changed("month")
	lastSet := cmd.Flags().Changed("last")
	rangeSet := showFrom != "" || showTo != ""
	searchSet := cmd.Flags().Changed("search")
	now := time.Now()
	today := now.Format(engine.CalendarDateLayout)

	set := 0
	for _, on := range []bool{lastSet, showDate != "", showRandom, monthSet, rangeSet, showOnDay} {
		if on {
			set++
		}
	}
	if set > 1 {
		return fmt.Errorf("--last, --date, --month, --from/--to, --on-this-day, --random are mutually exclusive")
	}
	if lastSet && showLast <= 0 {
		return fmt.Errorf("invalid --last %d: expected a positive number", showLast)
	}
	if searchSet {
		if strings.TrimSpace(showSearch) == "" {
			return fmt.Errorf("invalid --search: the phrase is blank")
		}
		if lastSet || showDate != "" || showRandom || showOnDay {
			return fmt.Errorf("--search combines only with --month or --from/--to")
		}
	}
	var from, to string
	if showFrom != "" {
		day, err := engine.ParseDayFlag("from", showFrom, now)
		if err != nil {
			return err
		}
		from = day
	}
	if showTo != "" {
		day, err := engine.ParseDayFlag("to", showTo, now)
		if err != nil {
			return err
		}
		to = day
	}
	if from != "" && to != "" && from > to {
		return fmt.Errorf("invalid range: --from %s is after --to %s", from, to)
	}

	path, err := resolveFilePath()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	entries := engine.ParseReflectionDocument(string(data)).Entries

	out := cmd.OutOrStdout()
	var results []engine.ReflectionEntry
	empty := "No entries found."
	switch {
	case showRandom:
		if len(entries) > 0 {
			results = []engine.ReflectionEntry{entries[rand.Intn(len(entries))]}
		}
	case monthSet:
		monthKey := showMonth
		if monthKey == "" || monthKey == "current" {
			monthKey = now.Format("2006-01")
		}
		if _, err := time.Parse("2006-01", monthKey); err != nil {
			return fmt.Errorf("invalid --month %q: expected YYYY-MM", monthKey)
		}
		results = engine.EntriesInMonth(entries, monthKey)
		empty = fmt.Sprintf("No entries in %s.%s", monthKey, nearestEntriesHint(entries, monthKey+"-01", monthKey+"-31", today))
	case showDate != "":
		day, err := engine.ParseDayFlag("date", showDate, now)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Date == day {
				results = append(results, e)
			}
		}
		empty = fmt.Sprintf("No entry for %s.%s", day, nearestEntriesHint(entries, day, day, today))
	case rangeSet:
		results = entriesInRange(entries, from, to)
		empty = fmt.Sprintf("No entries %s.%s", describeRange(from, to), nearestEntriesHint(entries, orFloor(from), orCeiling(to), today))
	case showOnDay:
		results = entriesOnThisDay(entries, today)
		empty = fmt.Sprintf("No earlier entry was written on the %s of a month.", ordinalDay(today))
	case searchSet:
		results = entries
	default:
		n := showLast
		if n == 0 {
			n = 1
		}
		if n > len(entries) {
			n = len(entries)
		}
		if n > 0 {
			results = entries[len(entries)-n:]
		}
	}

	if searchSet {
		return printSearch(out, results, showSearch, monthSet || rangeSet, term.IsTerminal(os.Stdout.Fd()))
	}

	if len(results) == 0 {
		fmt.Fprintln(out, empty)
		return nil
	}

	if term.IsTerminal(os.Stdout.Fd()) {
		fmt.Fprintln(out, renderTTY(results))
	} else {
		var parts []string
		for _, e := range results {
			parts = append(parts, e.Raw)
		}
		fmt.Fprintln(out, strings.Join(parts, "\n---\n\n"))
	}

	return nil
}

// entriesOnThisDay keeps the entries written on the same day of an earlier
// month as today — the same date a month, a season, or a year ago. Today's own
// entry is left out: the point is to meet what was written before. A day that
// some months lack, such as the 31st, simply has fewer matches.
func entriesOnThisDay(entries []engine.ReflectionEntry, today string) []engine.ReflectionEntry {
	return engine.EntriesOnThisDay(entries, today)
}

// ordinalDay names a date's day of the month as "1st", "2nd", "23rd", "11th".
func ordinalDay(date string) string {
	day, err := time.Parse(engine.CalendarDateLayout, date)
	if err != nil {
		return date
	}
	n := day.Day()
	suffix := "th"
	if n < 11 || n > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// entriesInRange keeps the dated entries inside an inclusive range in which
// either bound may be open ("").
func entriesInRange(entries []engine.ReflectionEntry, from, to string) []engine.ReflectionEntry {
	return engine.EntriesInRange(entries, from, to)
}

func describeRange(from, to string) string {
	switch {
	case from == "":
		return "up to " + to
	case to == "":
		return "from " + from + " on"
	default:
		return "from " + from + " to " + to
	}
}

// orFloor and orCeiling close an open bound so the nearest-entry hint can
// compare against it; canonical dates sort as strings.
func orFloor(date string) string {
	if date == "" {
		return "0000-00-00"
	}
	return date
}

func orCeiling(date string) string {
	if date == "" {
		return "9999-99-99"
	}
	return date
}

// nearestEntriesHint names the closest dated entries on either side of the
// inclusive range [from, to], so an empty lookup says where the writing
// actually is instead of leaving the author to guess, and how long ago it was
// relative to today. Dates compare as strings because they are canonical
// YYYY-MM-DD; file order is not trusted.
func nearestEntriesHint(entries []engine.ReflectionEntry, from, to, today string) string {
	before, after := engine.NearestEntries(entries, from, to)
	var parts []string
	if before != "" {
		parts = append(parts, "previous "+engine.DatedRelative(before, today))
	}
	if after != "" {
		parts = append(parts, "next "+engine.DatedRelative(after, today))
	}
	if len(parts) == 0 {
		return ""
	}
	return " Nearest: " + strings.Join(parts, ", ") + "."
}

func renderTTY(entries []engine.ReflectionEntry) string {
	isDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	ld := lipgloss.LightDark(isDark)
	dim := lipgloss.NewStyle().Foreground(ld(lipgloss.Color("244"), lipgloss.Color("241")))
	bold := lipgloss.NewStyle().Bold(true)

	ruleWidth := 60
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 && w < ruleWidth {
		ruleWidth = w
	}
	rule := dim.Render(strings.Repeat("─", ruleWidth))

	var parts []string
	for _, e := range entries {
		var b strings.Builder
		lines := engine.VisibleEntryLines(e.Raw)
		for i, line := range lines {
			switch {
			// Styling only, so a noncanonical dated heading is still shown as
			// the entry heading it visually is; refusing it is the parser's job.
			case engine.DateHeadingPrefixRe.MatchString(line):
				b.WriteString(bold.Render(line))
			case strings.HasPrefix(line, "### "):
				b.WriteString(dim.Render(line))
			default:
				b.WriteString(line)
			}
			if i < len(lines)-1 {
				b.WriteByte('\n')
			}
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, "\n\n"+rule+"\n\n")
}
