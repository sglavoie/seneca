package engine

import (
	"fmt"
	"strings"
)

// Rendering an entry. render(RenderCanonicalV2) is idempotent: parsing its
// output and rendering again yields identical text.

// renderRaw returns the byte-preserved source for an untouched entry.
func (e ReflectionEntry) RenderRaw() string { return e.Raw }

// renderCanonical returns the canonical v2 form of this entry.
func (e ReflectionEntry) RenderCanonical() string { return e.render(RenderCanonicalV2) }

func (e ReflectionEntry) render(mode RenderMode) string {
	var b strings.Builder

	b.WriteString(e.canonicalHeadingLine())
	b.WriteByte('\n')

	if mode == RenderCanonicalV2 {
		b.WriteString(renderEntryMarker(e.Metadata))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	if preamble := e.preambleContent(); preamble != "" {
		b.WriteString(preamble)
		b.WriteString("\n\n")
	}

	for _, section := range e.Sections {
		b.WriteString(section.render(mode))
	}
	return b.String()
}

// preambleContent is everything between the date heading and the first
// section. The consumed entry marker is not part of it; render re-emits that
// from metadata.
func (e ReflectionEntry) preambleContent() string {
	return joinContentLines(e.preambleLines)
}

// joinContentLines drops blank lines at both edges but never touches the
// indentation of a content line: stripping leading whitespace could promote
// ` ###` into a real heading on the next parse.
func joinContentLines(lines []string) string {
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}

func (s ReflectionSection) render(mode RenderMode) string {
	var b strings.Builder
	if mode == RenderCanonicalV2 {
		for _, line := range s.canonicalMarkerLines() {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	b.WriteString(canonicalSectionHeadingLine(s.headingLine))
	b.WriteByte('\n')

	// The body is the source of truth even for a follow-through: FollowThrough
	// is a parsed view over it. Build sections with newFollowThroughSection so
	// the two never drift.
	b.WriteString(s.bodyContent())
	b.WriteString("\n\n")
	return b.String()
}

// canonicalMarkerLines regenerates the recognized markers and passes unknown
// ones through verbatim so foreign tooling survives a rewrite.
func (s ReflectionSection) canonicalMarkerLines() []string {
	var lines []string
	if s.role != "" {
		lines = append(lines, fmt.Sprintf("<!-- seneca:%s name=%s -->", markerKindRole, formatMarkerValue(s.role)))
	}
	if s.Follow != nil {
		lines = append(lines, fmt.Sprintf("<!-- seneca:%s source=%s -->", markerKindFollowThrough, formatMarkerValue(s.Follow.SourceDate)))
	}
	for i, marker := range s.markers {
		switch marker.kind {
		case markerKindRole, markerKindFollowThrough:
			continue
		}
		if i < len(s.markerLines) {
			lines = append(lines, s.markerLines[i])
		}
	}
	return lines
}

func renderEntryMarker(md EntryMetadata) string {
	version := md.Version
	if version == 0 {
		version = ReflectionSchemaVersion
	}
	parts := []string{fmt.Sprintf("v=%d", version)}
	if md.id != "" {
		parts = append(parts, "id="+formatMarkerValue(md.id))
	}
	if md.createdAt != "" {
		parts = append(parts, "created="+formatMarkerValue(md.createdAt))
	}
	for _, key := range md.extraOrder {
		parts = append(parts, key+"="+formatMarkerValue(md.extra[key]))
	}
	return "<!-- seneca:" + markerKindEntry + " " + strings.Join(parts, " ") + " -->"
}

// sectionHeadingText reports whether a line opens a `### ` section and returns
// the heading text verbatim. The exact-heading contract is load-bearing:
// heading text is never trimmed, so a configured question only matches a
// section whose heading equals it byte for byte.
func sectionHeadingText(line string) (string, bool) {
	if heading, ok := strings.CutPrefix(line, sectionHeadingPrefix); ok {
		return heading, true
	}
	if strings.TrimRight(line, " \t") == "###" {
		return "", true
	}
	return "", false
}

// canonicalSectionHeadingLine drops trailing whitespace a re-parse could not
// recover, keeping render/parse stable.
func canonicalSectionHeadingLine(headingLine string) string {
	if trimmed := strings.TrimRight(headingLine, " \t"); trimmed != "" {
		return trimmed
	}
	return "###"
}

// isEntryHeadingLine accepts `## <anything>` and a bare `##`, so that a
// dateless heading survives a render/parse cycle unchanged.
func isEntryHeadingLine(line string) bool {
	return strings.HasPrefix(line, entryHeadingPrefix) || strings.TrimRight(line, " \t") == "##"
}

// canonicalHeadingLine reproduces the source heading byte for byte. Trailing
// spaces and tabs are kept rather than tidied away: they are exactly what makes
// a date-prefixed heading non-exact, so dropping them here would hand the entry
// the date that readDateHeading refused to give it.
func (e ReflectionEntry) canonicalHeadingLine() string {
	if heading := e.HeadingLine; heading != "" {
		return heading
	}
	if e.Date == "" {
		return "##"
	}
	return entryHeadingPrefix + e.Date
}

// splitReflectionLines normalizes CRLF and bare CR to LF for the structural
// model. The entry's raw text keeps the original bytes.
func splitReflectionLines(raw string) []string {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.Split(normalized, "\n")
}
