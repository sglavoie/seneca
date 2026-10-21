package engine

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// `seneca show --search` lists the written lines that mention a phrase, newest
// entry last, so a theme can be followed through months of writing without
// reading every entry. It only reads, exactly like the rest of `show`.

// SearchContextRunes is how much text is kept on each side of the first match
// in a long line; journal answers are often one long paragraph per line.
const SearchContextRunes = 60

// SearchHit is one entry that mentions the phrase, with each matching line
// already cut down to a snippet around its first match.
type SearchHit struct {
	Date     string
	Snippets []string
}

// SearchPattern matches the phrase literally and without regard to case. A
// regexp rather than strings.ToLower keeps match offsets valid on the original
// line even where lowering a letter changes its byte length.
func SearchPattern(phrase string) *regexp.Regexp {
	return regexp.MustCompile("(?i)" + regexp.QuoteMeta(strings.TrimSpace(phrase)))
}

// SearchEntries looks only at what the author wrote: entry and section
// headings are skipped, because the configured questions repeat in every entry
// and would make a search for "today" match the whole journal. Seneca's
// markers are skipped for the same reason `show` hides them.
func SearchEntries(entries []ReflectionEntry, pattern *regexp.Regexp) []SearchHit {
	var hits []SearchHit
	for _, e := range entries {
		var snippets []string
		for _, line := range VisibleEntryLines(e.Raw) {
			if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
				continue
			}
			if loc := pattern.FindStringIndex(line); loc != nil {
				snippets = append(snippets, SnippetAround(strings.TrimSpace(line), pattern))
			}
		}
		if len(snippets) > 0 {
			hits = append(hits, SearchHit{Date: e.Date, Snippets: snippets})
		}
	}
	return hits
}

// SnippetAround keeps SearchContextRunes runes on either side of the first
// match and marks each cut with an ellipsis.
func SnippetAround(line string, pattern *regexp.Regexp) string {
	loc := pattern.FindStringIndex(line)
	if loc == nil {
		return line
	}
	start := loc[0]
	for n := 0; n < SearchContextRunes && start > 0; n++ {
		_, size := utf8.DecodeLastRuneInString(line[:start])
		start -= size
	}
	end := loc[1]
	for n := 0; n < SearchContextRunes && end < len(line); n++ {
		_, size := utf8.DecodeRuneInString(line[end:])
		end += size
	}
	snippet := line[start:end]
	if start > 0 {
		snippet = "…" + strings.TrimLeft(snippet, " ")
	}
	if end < len(line) {
		snippet = strings.TrimRight(snippet, " ") + "…"
	}
	return snippet
}

// VisibleEntryLines drops Seneca's marker comments from an entry on the way to
// a terminal. They are structure, not writing, and a v2 entry would otherwise
// greet its author with `<!-- seneca:entry v=2 -->` every evening. The piped
// output path still prints raw bytes, so nothing is lost downstream.
func VisibleEntryLines(raw string) []string {
	lines := splitReflectionLines(raw)
	visible := make([]string, 0, len(lines))
	for _, line := range lines {
		if marker, ok := parseReflectionMarker(line); ok && !marker.malformed {
			continue
		}
		visible = append(visible, line)
	}
	return visible
}
