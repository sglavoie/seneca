package engine

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// A reflection entry is one dated `## ` block: its heading, the preamble before
// the first section, and the `### ` sections under it. Parsing is byte-lossless;
// every problem is reported as a diagnostic instead of an error.

// A heading carries a canonical entry date only when the line is byte-for-byte
// `## YYYY-MM-DD` — not even a trailing space or tab is allowed, because the
// promise is an exact heading and a suffix Seneca silently swallowed would be a
// heading the author never wrote. DateHeadingPrefixRe recognizes the near miss —
// a canonical date followed by anything at all — so it can be diagnosed rather
// than silently accepted as that day's entry.
var (
	dateRe              = regexp.MustCompile(`^## (\d{4}-\d{2}-\d{2})$`)
	DateHeadingPrefixRe = regexp.MustCompile(`^## (\d{4}-\d{2}-\d{2})`)
)

// EntryMetadata holds the `<!-- seneca:entry -->` payload. Unrecognized
// attributes survive in extra so future versions stay forward compatible.
type EntryMetadata struct {
	Present    bool
	Version    int
	id         string
	createdAt  string
	extra      map[string]string
	extraOrder []string
}

// ReflectionSection is one `### ` block plus the markers bound to it.
type ReflectionSection struct {
	Heading     string
	headingLine string
	markerLines []string
	markers     []ReflectionMarker
	bodyLines   []string
	role        string
	Follow      *FollowThrough
}

// body is the answer text for this section.
func (s ReflectionSection) Body() string {
	return strings.TrimSpace(strings.Join(s.bodyLines, "\n"))
}

// bodyContent is what render emits. Unlike body it keeps the indentation of
// content lines, so rendering cannot turn indented text into a heading.
func (s ReflectionSection) bodyContent() string {
	return joinContentLines(s.bodyLines)
}

func (s *ReflectionSection) setBody(body string) {
	if body == "" {
		s.bodyLines = nil
		return
	}
	s.bodyLines = strings.Split(body, "\n")
}

// ReflectionEntry is one dated entry. raw is the verbatim source chunk; the
// structured fields are a lossless view over it.
type ReflectionEntry struct {
	Date          string
	Raw           string
	HeadingLine   string
	preambleLines []string
	Sections      []ReflectionSection
	Metadata      EntryMetadata
	Diagnostics   []ReflectionDiagnostic
}

func parseReflectionEntry(raw string) ReflectionEntry {
	e := ReflectionEntry{Raw: raw}

	lines := splitReflectionLines(raw)
	start := 0
	if len(lines) > 0 && isEntryHeadingLine(lines[0]) {
		e.HeadingLine = lines[0]
		start = 1
		e.readDateHeading()
	} else {
		e.diag(diagInvalidEntry, "entry does not start with a `## ` heading")
	}

	var pendingLines []string
	var pendingMarkers []ReflectionMarker

	current := -1
	appendBody := func(line string) {
		if current < 0 {
			e.preambleLines = append(e.preambleLines, line)
			return
		}
		e.Sections[current].bodyLines = append(e.Sections[current].bodyLines, line)
	}
	flushPending := func() {
		for _, line := range pendingLines {
			appendBody(line)
		}
		pendingLines, pendingMarkers = nil, nil
	}

	for _, line := range lines[start:] {
		if heading, ok := sectionHeadingText(line); ok {
			e.Sections = append(e.Sections, ReflectionSection{
				Heading:     heading,
				headingLine: line,
				markerLines: pendingLines,
				markers:     pendingMarkers,
			})
			pendingLines, pendingMarkers = nil, nil
			current = len(e.Sections) - 1
			continue
		}

		if marker, ok := parseReflectionMarker(line); ok {
			if marker.malformed {
				e.diag(diagMalformedMetadata, marker.detail)
			}
			if marker.kind == markerKindEntry && !marker.malformed && current < 0 && len(pendingLines) == 0 {
				// Consumed into metadata; render regenerates it canonically.
				e.applyEntryMarker(marker)
				continue
			}
			pendingLines = append(pendingLines, line)
			pendingMarkers = append(pendingMarkers, marker)
			continue
		}

		// Any other line breaks marker adjacency, so pending markers were
		// prose after all and belong in the body.
		flushPending()
		appendBody(line)
	}
	flushPending()

	e.bindSectionMarkers()
	e.flagDuplicateSections()
	return e
}

// readDateHeading assigns the entry's canonical date from its heading. Only a
// heading line that is byte-for-byte `## YYYY-MM-DD` names a day: a heading that
// merely starts with a date — including one whose only extra bytes are trailing
// spaces or tabs — says something Seneca cannot interpret, so it is diagnosed
// and left undated rather than answering to that date for a targeted edit.
func (e *ReflectionEntry) readDateHeading() {
	heading := e.HeadingLine
	switch {
	case dateRe.MatchString(heading):
		e.Date = dateRe.FindStringSubmatch(heading)[1]
		if _, err := time.Parse("2006-01-02", e.Date); err != nil {
			e.diag(DiagMalformedDate, fmt.Sprintf("%q is not a real calendar date", e.Date))
		}
	case DateHeadingPrefixRe.MatchString(heading):
		e.diag(DiagMalformedDate, fmt.Sprintf(
			"heading %q is not exactly `## YYYY-MM-DD`, so seneca cannot tell which day it records", heading))
	default:
		e.diag(DiagMissingDate, fmt.Sprintf("heading %q has no YYYY-MM-DD date", heading))
	}
}

func (e *ReflectionEntry) diag(code ReflectionDiagnosticCode, detail string) {
	e.Diagnostics = append(e.Diagnostics, ReflectionDiagnostic{Code: code, date: e.Date, detail: detail})
}

func (e *ReflectionEntry) applyEntryMarker(marker ReflectionMarker) {
	if e.Metadata.Present {
		e.diag(diagMalformedMetadata, "entry has more than one seneca:entry marker")
	}
	e.Metadata.Present = true

	for _, key := range marker.order {
		value := marker.attrs[key]
		switch key {
		case "v":
			version, err := parseMarkerInt(value)
			if err != nil {
				e.diag(diagMalformedMetadata, fmt.Sprintf("seneca:entry v=%q is not an integer", value))
				continue
			}
			e.Metadata.Version = version
		case "id":
			e.Metadata.id = value
		case "created":
			e.Metadata.createdAt = value
		default:
			if e.Metadata.extra == nil {
				e.Metadata.extra = map[string]string{}
			}
			e.Metadata.extra[key] = value
			e.Metadata.extraOrder = append(e.Metadata.extraOrder, key)
		}
	}
	if e.Metadata.Version == 0 {
		e.diag(diagMalformedMetadata, "seneca:entry marker is missing the required v attribute")
	}
}

func (e *ReflectionEntry) bindSectionMarkers() {
	for i := range e.Sections {
		section := &e.Sections[i]
		for _, marker := range section.markers {
			if marker.malformed {
				// Already diagnosed while scanning; passed through verbatim.
				continue
			}
			switch marker.kind {
			case markerKindRole:
				name := marker.attr("name")
				if name == "" {
					e.diag(diagMalformedMetadata, "seneca:role marker is missing the required name attribute")
					continue
				}
				section.role = name
			case markerKindFollowThrough:
				source := marker.attr("source")
				if source == "" {
					e.diag(diagMalformedMetadata, "seneca:follow-through marker is missing the required source attribute")
					continue
				}
				if _, err := time.Parse("2006-01-02", source); err != nil {
					e.diag(diagMalformedMetadata, fmt.Sprintf("seneca:follow-through source=%q is not a YYYY-MM-DD date", source))
					continue
				}
				follow := parseFollowThroughBody(source, section.Body())
				section.Follow = &follow
			case markerKindEntry:
				e.diag(diagMalformedMetadata, "seneca:entry marker must precede the first `### ` section")
			}
		}
	}
}

func (e *ReflectionEntry) flagDuplicateSections() {
	seen := make(map[string]struct{}, len(e.Sections))
	for _, section := range e.Sections {
		if _, ok := seen[section.Heading]; ok {
			e.diag(diagDuplicateSection, fmt.Sprintf("section %q appears more than once", section.Heading))
			continue
		}
		seen[section.Heading] = struct{}{}
	}
}

// answers maps configured questions onto section bodies by exact heading. A
// repeated heading resolves to its last occurrence.
func (e ReflectionEntry) Answers(questions []string) []string {
	answers := make([]string, len(questions))
	index := make(map[string]int, len(questions))
	for i, question := range questions {
		index[question] = i
	}
	for _, section := range e.Sections {
		if i, ok := index[section.Heading]; ok {
			answers[i] = section.Body()
		}
	}
	return answers
}

// anchorAnswer returns this entry's anchor text. A `seneca:role name=anchor`
// marker wins over the configured position, so reordering or rewording
// questions cannot silently move the anchor.
func (e ReflectionEntry) AnchorAnswer(questions []string, anchorQuestionIndex int) string {
	for _, section := range e.Sections {
		if section.role == roleAnchor {
			return section.Body()
		}
	}

	idx := anchorQuestionIndex - 1
	if idx < 0 || idx >= len(questions) {
		return ""
	}
	answers := e.Answers(questions)
	return strings.TrimSpace(answers[idx])
}

func (e ReflectionEntry) hasSenecaMarkers() bool {
	if e.Metadata.Present {
		return true
	}
	for _, section := range e.Sections {
		if len(section.markers) > 0 {
			return true
		}
	}
	return false
}

// preferredRenderMode keeps a legacy entry legacy and a v2 entry v2, so
// rewriting one entry never invents markers in an untouched file.
func (e ReflectionEntry) preferredRenderMode() RenderMode {
	if e.hasSenecaMarkers() {
		return RenderCanonicalV2
	}
	return renderLegacy
}
