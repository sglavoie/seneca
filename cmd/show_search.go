package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
)

// printSearch prints each matching entry's date followed by its snippets, then
// a one-line tally. On a terminal the date is bold and the phrase highlighted;
// piped output is plain text.
func printSearch(out io.Writer, entries []engine.ReflectionEntry, phrase string, narrowed, styled bool) error {
	phrase = strings.TrimSpace(phrase)
	pattern := engine.SearchPattern(phrase)
	hits := engine.SearchEntries(entries, pattern)

	scope := ""
	if narrowed {
		scope = " in that range"
	}
	if len(hits) == 0 {
		fmt.Fprintf(out, "No entry%s mentions %q.\n", scope, phrase)
		return nil
	}

	date, mark, dim := func(s string) string { return s }, func(s string) string { return s }, func(s string) string { return s }
	if styled {
		ld := lipgloss.LightDark(lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
		dateStyle := lipgloss.NewStyle().Bold(true)
		markStyle := lipgloss.NewStyle().Bold(true).Foreground(ld(lipgloss.Color("130"), lipgloss.Color("214")))
		dimStyle := lipgloss.NewStyle().Foreground(ld(lipgloss.Color("244"), lipgloss.Color("241")))
		date = func(s string) string { return dateStyle.Render(s) }
		mark = func(s string) string { return markStyle.Render(s) }
		dim = func(s string) string { return dimStyle.Render(s) }
	}

	lines := 0
	for i, hit := range hits {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, date(hit.Date))
		for _, snippet := range hit.Snippets {
			fmt.Fprintln(out, "  "+pattern.ReplaceAllStringFunc(snippet, mark))
			lines++
		}
	}
	fmt.Fprintln(out)
	verb := "mention"
	if len(hits) == 1 {
		verb = "mentions"
	}
	fmt.Fprintln(out, dim(fmt.Sprintf("%s%s %s %q in %s; `seneca show --date DATE` reads one in full.",
		engine.Plural(len(hits), "entry", "entries"), scope, verb, phrase, engine.Plural(lines, "line", "lines"))))
	return nil
}
