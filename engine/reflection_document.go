package engine

import (
	"fmt"
	"strings"
)

// The reflection document model is the single parsing and rendering authority
// for the reflections file. Nothing outside this file may split entries, match
// `###` headings, or hand-assemble entry Markdown.
//
// # Marker grammar (finalized)
//
// Every Seneca marker is a standalone HTML comment line:
//
//	<!-- seneca:<kind> key=value key="quoted value" -->
//
// Values are bare when they contain no whitespace, quote, or `-->`; otherwise
// they are double quoted with `\"` and `\\` escapes. Attribute order is
// preserved. Three kinds are recognized:
//
//	<!-- seneca:entry v=2 id=<sha256> created=<RFC3339Nano> -->
//	    Entry metadata. Must sit between the `## <date>` heading and the first
//	    `### ` section. `v` is required.
//	<!-- seneca:role name=anchor -->
//	    Role of the section on the immediately following `### ` heading. The
//	    role travels with the section, never with the question text, so
//	    reordering or rewording configured questions cannot break continuity.
//	<!-- seneca:follow-through source=<YYYY-MM-DD> -->
//	    Marks the immediately following `### ` section as a follow-through. Its
//	    body is a leading blockquote holding the source intention snapshot,
//	    then a blank line, then the narrative response.
//
// # Parser invariants
//
//   - Entry boundaries and `raw` text are byte-preserved. renderRaw returns the
//     source verbatim, so untouched entries survive sort and merge unchanged.
//   - An entry boundary is a standalone separator line — a line whose bytes are
//     exactly `---`. The line break that ends it may be LF, CRLF, or bare CR,
//     the same set splitReflectionLines accepts, so which day a section belongs
//     to never depends on the editor that wrote the file. Three dashes anywhere
//     else on a line, including at the end of prose, are ordinary text.
//   - Unknown headings, unknown markers, comments, and preamble prose are kept
//     in their original order and are never dropped or reordered.
//   - Markers bind to the `### ` heading that immediately follows them. A blank
//     line between a marker and a heading demotes the marker to body content.
//   - Structural parsing normalizes CRLF to LF; `raw` keeps the original bytes.
//   - Parsing never fails. Every problem surfaces as a ReflectionDiagnostic and
//     the entry still round-trips.
//   - render(RenderCanonicalV2) is idempotent: parsing its output and rendering
//     again yields identical text.

const (
	entryHeadingPrefix   = "## "
	sectionHeadingPrefix = "### "
	// entrySeparatorLine is the whole boundary grammar: a line that is exactly
	// these bytes. Matching per line rather than on the `\n---\n` substring is
	// what makes a CRLF or mixed-ending file split into the same entries as an
	// LF one, and what keeps prose ending in three dashes out of the grammar.
	entrySeparatorLine = "---"
	// EntryTerminator is the framing render emits after every entry. It is the
	// one thing normalized on the way out: entry bytes are replayed verbatim,
	// but the separator around them is always written with LF breaks.
	EntryTerminator = "\n" + entrySeparatorLine + "\n\n"

	ReflectionSchemaVersion = 2
	followThroughHeading    = "Follow-through"
)

// ReflectionDiagnosticCode names a recoverable parse problem. Later sessions
// decide which operations treat which codes as fatal; parsing itself never is.
type ReflectionDiagnosticCode string

const (
	DiagMissingDate       ReflectionDiagnosticCode = "missing-date"
	DiagMalformedDate     ReflectionDiagnosticCode = "malformed-date"
	diagMalformedMetadata ReflectionDiagnosticCode = "malformed-metadata"
	diagDuplicateSection  ReflectionDiagnosticCode = "duplicate-section"
	diagInvalidEntry      ReflectionDiagnosticCode = "invalid-entry"
)

type ReflectionDiagnostic struct {
	Code       ReflectionDiagnosticCode
	EntryIndex int
	date       string
	detail     string
}

func (d ReflectionDiagnostic) String() string {
	where := d.date
	if where == "" {
		where = fmt.Sprintf("entry %d", d.EntryIndex)
	}
	return fmt.Sprintf("%s: %s (%s)", d.Code, d.detail, where)
}

// ReflectionDocument is a parsed reflections file.
type ReflectionDocument struct {
	Entries     []ReflectionEntry
	Diagnostics []ReflectionDiagnostic
}

type RenderMode int

const (
	// renderLegacy emits the pre-v2 on-disk shape: no Seneca markers.
	renderLegacy RenderMode = iota
	// RenderCanonicalV2 emits the canonical v2 shape with markers.
	RenderCanonicalV2
)

// ParseReflectionDocument splits content into entries on the Markdown rule and
// parses each one. It never returns an error; see doc.diagnostics.
//
// A chunk holding nothing but framing is not an entry: a repeated separator, or
// one before the first entry or after the last, is boundary punctuation and is
// dropped rather than diagnosed. entryIndex therefore counts the entries that
// survive, which is what a reader of a diagnostic is counting too.
func ParseReflectionDocument(content string) ReflectionDocument {
	var doc ReflectionDocument

	for _, chunk := range splitEntryChunks(content) {
		chunk = trimEntryFraming(chunk)
		if chunk == "" {
			continue
		}
		entry := parseReflectionEntry(chunk)
		index := len(doc.Entries)
		for i := range entry.Diagnostics {
			entry.Diagnostics[i].EntryIndex = index
		}
		doc.Entries = append(doc.Entries, entry)
		doc.Diagnostics = append(doc.Diagnostics, entry.Diagnostics...)
	}
	return doc
}

// splitEntryChunks cuts content at every standalone separator line and returns
// the verbatim bytes lying between the boundaries, framing included; the caller
// trims that framing and drops what is left empty.
//
// The scan walks the original string and compares whole lines, so nothing is
// normalized before splitting and every chunk still carries the author's own
// bytes — including their line endings. That matters twice over. Normalizing
// first would hand each entry a `raw` text its file never held, and splitting on
// the `\n---\n` substring instead would silently miss every boundary in a CRLF
// file: the whole document would parse as one entry, and every date but the
// first would stop existing as far as find, sort, and merge are concerned.
//
// Only an exact `---` line separates. A line with trailing spaces does not, and
// neither does a paragraph that happens to end in three dashes — a rule the
// previous trailing-`---` trim could not express, because by then the boundary
// and the author's last words looked the same.
func splitEntryChunks(content string) []string {
	var chunks []string
	chunkStart, pos := 0, 0
	for pos < len(content) {
		lineEnd := pos
		for lineEnd < len(content) && content[lineEnd] != '\n' && content[lineEnd] != '\r' {
			lineEnd++
		}
		next := lineEnd
		if next < len(content) {
			if content[next] == '\r' && next+1 < len(content) && content[next+1] == '\n' {
				next++
			}
			next++
		}
		if content[pos:lineEnd] == entrySeparatorLine {
			chunks = append(chunks, content[chunkStart:pos])
			chunkStart = next
		}
		pos = next
	}
	return append(chunks, content[chunkStart:])
}

func (d ReflectionDocument) Find(date string) (int, bool) {
	return FindEntry(d.Entries, date)
}

// render emits the document in raw-preserving form: byte-identical entries
// joined by the established separator.
func (d ReflectionDocument) Render() string {
	return RenderReflectionEntries(d.Entries)
}

func RenderReflectionEntries(entries []ReflectionEntry) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.RenderRaw())
		b.WriteString(EntryTerminator)
	}
	return b.String()
}

func FindEntry(entries []ReflectionEntry, date string) (int, bool) {
	for i, e := range entries {
		if e.Date == date {
			return i, true
		}
	}
	return -1, false
}

// NewReflectionEntry parses a single raw entry chunk.
func NewReflectionEntry(raw string) ReflectionEntry {
	return parseReflectionEntry(trimEntryFraming(raw))
}

// trimEntryFraming removes the blank lines that frame a document or an entry
// chunk while leaving the first and last content lines byte for byte intact.
// strings.TrimSpace cannot do this job: it reaches past the final line break and
// strips a trailing space or tab from the last content line. When that line is a
// date heading — a file or an entry whose last line is `## YYYY-MM-DD `, with
// nothing after it but the terminator — the trim would hand readDateHeading a
// canonical heading the author never wrote, and the suffix that makes the
// heading non-exact would be silently swallowed at exactly the boundary where it
// is hardest to see.
func trimEntryFraming(s string) string {
	start := 0
	for start < len(s) {
		lineEnd := start
		for lineEnd < len(s) && s[lineEnd] != '\n' && s[lineEnd] != '\r' {
			lineEnd++
		}
		if strings.TrimSpace(s[start:lineEnd]) != "" {
			break
		}
		if lineEnd == len(s) {
			return ""
		}
		if s[lineEnd] == '\r' && lineEnd+1 < len(s) && s[lineEnd+1] == '\n' {
			lineEnd++
		}
		start = lineEnd + 1
	}

	end := len(s)
	for end > start {
		lineStart := end
		for lineStart > start && s[lineStart-1] != '\n' && s[lineStart-1] != '\r' {
			lineStart--
		}
		if strings.TrimSpace(s[lineStart:end]) != "" {
			break
		}
		// Drop the blank line together with the break that introduced it, so
		// the preceding content line keeps its own trailing bytes.
		end = lineStart - 1
		if end > start && s[end] == '\n' && s[end-1] == '\r' {
			end--
		}
	}
	if end <= start {
		return ""
	}
	return s[start:end]
}

// followThroughSection returns the entry's follow-through, if any.
