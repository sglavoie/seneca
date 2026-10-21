package engine

import (
	"fmt"
	"regexp"
	"strings"
)

// Seneca markers are standalone HTML comment lines; see the grammar in
// reflection_document.go. This file is the only place that reads or writes
// their syntax.

// Marker kinds and roles.
const (
	markerKindEntry         = "entry"
	markerKindRole          = "role"
	markerKindFollowThrough = "follow-through"

	roleAnchor = "anchor"
)

var (
	senecaMarkerRe       = regexp.MustCompile(`^<!--\s*seneca:([A-Za-z][A-Za-z0-9_-]*)\s*(.*?)\s*-->$`)
	senecaMarkerPrefixRe = regexp.MustCompile(`^<!--\s*seneca:`)
)

// ReflectionMarker is one parsed `<!-- seneca:... -->` line.
type ReflectionMarker struct {
	kind      string
	attrs     map[string]string
	order     []string
	raw       string
	malformed bool
	detail    string
}

func (m ReflectionMarker) attr(key string) string { return m.attrs[key] }

// parseReflectionMarker recognizes a standalone Seneca marker line. The second
// result is false for any line that is not a Seneca marker at all; a line that
// looks like one but does not parse comes back marked malformed.
func parseReflectionMarker(line string) (ReflectionMarker, bool) {
	trimmed := strings.TrimSpace(line)
	if !senecaMarkerPrefixRe.MatchString(trimmed) {
		return ReflectionMarker{}, false
	}

	m := senecaMarkerRe.FindStringSubmatch(trimmed)
	if m == nil {
		return ReflectionMarker{
			raw:       line,
			malformed: true,
			detail:    fmt.Sprintf("marker %q is not a well-formed seneca comment", trimmed),
		}, true
	}

	marker := ReflectionMarker{kind: m[1], raw: line, attrs: map[string]string{}}
	attrs, order, err := parseMarkerAttributes(m[2])
	if err != nil {
		marker.malformed = true
		marker.detail = fmt.Sprintf("marker %q: %v", trimmed, err)
		return marker, true
	}
	marker.attrs = attrs
	marker.order = order
	return marker, true
}

func parseMarkerAttributes(text string) (map[string]string, []string, error) {
	attrs := map[string]string{}
	var order []string

	runes := []rune(text)
	i := 0
	for i < len(runes) {
		for i < len(runes) && isMarkerSpace(runes[i]) {
			i++
		}
		if i >= len(runes) {
			break
		}

		keyStart := i
		for i < len(runes) && runes[i] != '=' && !isMarkerSpace(runes[i]) {
			i++
		}
		key := string(runes[keyStart:i])
		if key == "" {
			return nil, nil, fmt.Errorf("empty attribute name")
		}
		if i >= len(runes) || runes[i] != '=' {
			return nil, nil, fmt.Errorf("attribute %q is missing a value", key)
		}
		i++ // consume '='

		var value string
		if i < len(runes) && runes[i] == '"' {
			i++
			var b strings.Builder
			closed := false
			for i < len(runes) {
				switch runes[i] {
				case '\\':
					if i+1 >= len(runes) {
						return nil, nil, fmt.Errorf("attribute %q has a dangling escape", key)
					}
					b.WriteRune(runes[i+1])
					i += 2
					continue
				case '"':
					i++
					closed = true
				default:
					b.WriteRune(runes[i])
					i++
					continue
				}
				break
			}
			if !closed {
				return nil, nil, fmt.Errorf("attribute %q has an unterminated quoted value", key)
			}
			value = b.String()
		} else {
			valueStart := i
			for i < len(runes) && !isMarkerSpace(runes[i]) {
				i++
			}
			value = string(runes[valueStart:i])
		}

		if _, ok := attrs[key]; ok {
			return nil, nil, fmt.Errorf("attribute %q is repeated", key)
		}
		attrs[key] = value
		order = append(order, key)
	}
	return attrs, order, nil
}

func isMarkerSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

// formatMarkerValue quotes a value only when a bare form would be ambiguous.
func formatMarkerValue(value string) string {
	needsQuote := value == "" || strings.ContainsAny(value, " \t\"\\") || strings.Contains(value, "-->")
	if !needsQuote {
		return value
	}
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}

func parseMarkerInt(value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("empty integer")
	}
	n := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid integer %q", value)
		}
		n = n*10 + int(r-'0')
		if n > 1<<20 {
			return 0, fmt.Errorf("integer %q is out of range", value)
		}
	}
	return n, nil
}
