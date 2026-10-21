package engine

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Parsing an archive file into a header and records. It never fails; see
// ReviewArchive.diagnostics.

// ParseReviewArchive splits content into a header and records. It never fails;
// see archive.diagnostics.
func ParseReviewArchive(content string) ReviewArchive {
	var archive ReviewArchive
	lines := strings.Split(content, "\n")

	var starts []int
	for i := 0; i+1 < len(lines); i++ {
		if !strings.HasPrefix(trimCR(lines[i]), archiveHeadingPrefix) {
			continue
		}
		if _, ok := parseReflectionMarker(trimCR(lines[i+1])); !ok {
			continue
		}
		starts = append(starts, i)
	}

	headerEnd := len(lines)
	if len(starts) > 0 {
		headerEnd = starts[0]
	}
	archive.Header = strings.Join(lines[:headerEnd], "\n")
	archive.HasMarker, archive.HeaderVersion, archive.HeaderVersionOK, archive.headerVersionRaw =
		parseArchiveHeaderMarker(lines[:headerEnd])

	for i, start := range starts {
		end := len(lines)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		record, diagnostics := parseArchiveRecord(strings.Join(lines[start:end], "\n"))
		index := len(archive.Records)
		for j := range diagnostics {
			diagnostics[j].Record = index
		}
		archive.Records = append(archive.Records, record)
		archive.Diagnostics = append(archive.Diagnostics, diagnostics...)
	}

	archive.validateRecordGraph()
	return archive
}

// parseArchiveHeaderMarker looks for the archive marker in the header. Only a
// well-formed marker counts: a corrupted one must not authorize a mutation. The
// version is reported exactly as declared — a missing or unreadable one is
// never substituted for the current schema, because a version this binary
// invented is a claim nobody made about the records that follow.
func parseArchiveHeaderMarker(lines []string) (hasMarker bool, version int, versionOK bool, raw string) {
	for _, line := range lines {
		marker, ok := parseReflectionMarker(trimCR(line))
		if !ok || marker.malformed || marker.kind != MarkerKindArchive {
			continue
		}
		raw = strings.TrimSpace(marker.attr("v"))
		version, err := parseMarkerInt(raw)
		if err != nil || version < 1 {
			return true, 0, false, raw
		}
		return true, version, true, raw
	}
	return false, 0, false, ""
}

func parseArchiveRecord(raw string) (ArchiveRecord, []ArchiveDiagnostic) {
	lines := strings.Split(raw, "\n")
	record := ArchiveRecord{
		Raw:         raw,
		headingLine: trimCR(lines[0]),
		markerLine:  trimCR(lines[1]),
	}
	record.body = archiveRecordBody(lines[2:])

	var diagnostics []ArchiveDiagnostic
	marker, ok := parseReflectionMarker(record.markerLine)
	if !ok {
		// Unreachable: the splitter only starts a record on a marker line.
		return record, diagnostics
	}
	if marker.malformed {
		return record, append(diagnostics, ArchiveDiagnostic{
			Code:   DiagArchiveMalformedMarker,
			Detail: marker.detail,
		})
	}

	switch marker.kind {
	case markerKindReview:
		record.Kind = ArchiveRecordReview
		review := ReviewRecord{
			ID:        requireArchiveAttr(marker, "id", &diagnostics),
			CreatedAt: requireArchiveTimestamp(marker, "created", &diagnostics),
			Period:    ReviewPeriod(requireArchiveEnum(marker, "period", ArchiveReviewPeriodNames(), &diagnostics)),
			Start:     requireArchiveDate(marker, "start", &diagnostics),
			End:       requireArchiveDate(marker, "end", &diagnostics),
			Revises:   strings.TrimSpace(marker.attr("revises")),
			Title:     archiveHeadingTitle(record.headingLine),
			Body:      record.body,
		}
		requireArchiveVersion(marker, &diagnostics)
		record.Review = &review
	case markerKindFocus:
		record.Kind = ArchiveRecordFocus
		focus := FocusEvent{
			ID:        requireArchiveAttr(marker, "id", &diagnostics),
			createdAt: requireArchiveTimestamp(marker, "created", &diagnostics),
			Event:     FocusEventKind(requireArchiveEnum(marker, "event", []string{string(FocusEventSet), string(FocusEventClear)}, &diagnostics)),
			Effective: requireArchiveDate(marker, "effective", &diagnostics),
			Origin:    FocusOrigin(requireArchiveEnum(marker, "origin", []string{string(FocusOriginManual), string(FocusOriginReview)}, &diagnostics)),
			Source:    strings.TrimSpace(marker.attr("source")),
		}
		requireArchiveVersion(marker, &diagnostics)
		if focus.Event == FocusEventSet {
			focus.Text = record.body
			if focus.Text == "" {
				diagnostics = append(diagnostics, ArchiveDiagnostic{
					Code:   DiagArchiveInvalidAttr,
					Detail: "focus set record has no focus text",
				})
			}
		}
		if focus.Origin == FocusOriginReview && focus.Source == "" {
			diagnostics = append(diagnostics, ArchiveDiagnostic{
				Code:   DiagArchiveMissingAttr,
				Detail: "seneca:focus origin=review is missing the required source attribute",
			})
		}
		record.Focus = &focus
	default:
		record.Kind = ArchiveRecordUnknown
	}

	return record, diagnostics
}

// archiveRecordBody is the record's Markdown, minus the cosmetic trailing rule
// and blank lines Seneca renders between records.
func archiveRecordBody(lines []string) string {
	for len(lines) > 0 {
		last := strings.TrimSpace(trimCR(lines[len(lines)-1]))
		if last != "" && last != archiveRecordRule {
			break
		}
		lines = lines[:len(lines)-1]
	}
	trimmed := make([]string, len(lines))
	for i, line := range lines {
		trimmed[i] = trimCR(line)
	}
	return joinContentLines(trimmed)
}

// archiveHeadingTitle recovers the human title from `## <title> — <period>`.
func archiveHeadingTitle(headingLine string) string {
	text := strings.TrimSpace(strings.TrimPrefix(headingLine, archiveHeadingPrefix))
	if title, _, found := strings.Cut(text, " — "); found {
		return strings.TrimSpace(title)
	}
	return text
}

func requireArchiveAttr(marker ReflectionMarker, key string, diagnostics *[]ArchiveDiagnostic) string {
	value := strings.TrimSpace(marker.attr(key))
	if value == "" {
		*diagnostics = append(*diagnostics, ArchiveDiagnostic{
			Code:   DiagArchiveMissingAttr,
			Detail: fmt.Sprintf("seneca:%s marker is missing the required %s attribute", marker.kind, key),
		})
	}
	return value
}

func requireArchiveDate(marker ReflectionMarker, key string, diagnostics *[]ArchiveDiagnostic) string {
	value := requireArchiveAttr(marker, key, diagnostics)
	if value == "" {
		return ""
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		*diagnostics = append(*diagnostics, ArchiveDiagnostic{
			Code:   DiagArchiveInvalidAttr,
			Detail: fmt.Sprintf("seneca:%s %s=%q is not a YYYY-MM-DD date", marker.kind, key, value),
		})
		return ""
	}
	return value
}

func requireArchiveTimestamp(marker ReflectionMarker, key string, diagnostics *[]ArchiveDiagnostic) string {
	value := requireArchiveAttr(marker, key, diagnostics)
	if value == "" {
		return ""
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		*diagnostics = append(*diagnostics, ArchiveDiagnostic{
			Code:   DiagArchiveInvalidAttr,
			Detail: fmt.Sprintf("seneca:%s %s=%q is not an RFC3339 timestamp", marker.kind, key, value),
		})
		return ""
	}
	return value
}

func requireArchiveEnum(marker ReflectionMarker, key string, allowed []string, diagnostics *[]ArchiveDiagnostic) string {
	value := requireArchiveAttr(marker, key, diagnostics)
	if value == "" {
		return ""
	}
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	*diagnostics = append(*diagnostics, ArchiveDiagnostic{
		Code:   DiagArchiveInvalidAttr,
		Detail: fmt.Sprintf("seneca:%s %s=%q is not one of %s", marker.kind, key, value, strings.Join(allowed, ", ")),
	})
	return ""
}

// requireArchiveVersion checks only that `v` is present and numeric. A higher
// version is allowed: unknown attributes ride along untouched in raw.
func requireArchiveVersion(marker ReflectionMarker, diagnostics *[]ArchiveDiagnostic) {
	value := requireArchiveAttr(marker, "v", diagnostics)
	if value == "" {
		return
	}
	if _, err := parseMarkerInt(value); err != nil {
		*diagnostics = append(*diagnostics, ArchiveDiagnostic{
			Code:   DiagArchiveInvalidAttr,
			Detail: fmt.Sprintf("seneca:%s v=%q is not an integer", marker.kind, value),
		})
	}
}

// validateRecordGraph catches identity problems no single record can see:
// repeated IDs, and revisions that do not name an earlier review record.
func (a *ReviewArchive) validateRecordGraph() {
	positions := make(map[string]int, len(a.Records))
	reviews := make(map[string]int, len(a.Records))

	for i, record := range a.Records {
		id := record.RecordID()
		if id == "" {
			continue
		}
		if first, ok := positions[id]; ok {
			a.Diagnostics = append(a.Diagnostics, ArchiveDiagnostic{
				Code:   DiagArchiveDuplicateID,
				Record: i,
				Detail: fmt.Sprintf("record id %s is already used by record %d", id, first+1),
			})
			continue
		}
		positions[id] = i
		if record.Kind == ArchiveRecordReview {
			reviews[id] = i
		}
	}

	for i, record := range a.Records {
		if record.Review == nil || record.Review.Revises == "" {
			continue
		}
		target, ok := reviews[record.Review.Revises]
		switch {
		case !ok:
			a.Diagnostics = append(a.Diagnostics, ArchiveDiagnostic{
				Code:   DiagArchiveInvalidRevision,
				Record: i,
				Detail: fmt.Sprintf("revises=%s names no review record in this archive", record.Review.Revises),
			})
		case target >= i:
			a.Diagnostics = append(a.Diagnostics, ArchiveDiagnostic{
				Code:   DiagArchiveInvalidRevision,
				Record: i,
				Detail: fmt.Sprintf("revises=%s names record %d, which is not earlier in this append-only archive", record.Review.Revises, target+1),
			})
		}
	}
}

// recordIDPattern is the shape newArchiveRecordID produces: a lowercase hex
// SHA-256 digest.
var recordIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// recordIntegrityDiagnostics recomputes every well-formed record's ID and
// reports the ones an append-only write could never have produced: an ID that
// is not a SHA-256 digest, and an ID that does not match the record's own
// metadata and content.
//
// It is deliberately not part of ParseReviewArchive: an ID is a claim about how
// a record was written, not about whether the file can be read, and a read must
// stay lossless. Doctor reports these and every mutation refuses on them, so
// the two can never disagree about what counts as a corrupt record.
//
// Records that already carry a parse diagnostic are skipped: their ID cannot be
// recomputed honestly from fields that failed to parse, and the diagnostic they
// do carry is fatal for a mutation anyway.
func (a ReviewArchive) RecordIntegrityDiagnostics() []ArchiveDiagnostic {
	damaged := make(map[int]bool, len(a.Diagnostics))
	for _, d := range a.Diagnostics {
		if d.Record >= 0 {
			damaged[d.Record] = true
		}
	}

	var diagnostics []ArchiveDiagnostic
	for i, record := range a.Records {
		want, ok := record.ExpectedRecordID()
		if !ok || damaged[i] {
			continue
		}
		got := record.RecordID()
		switch {
		case !recordIDPattern.MatchString(got):
			diagnostics = append(diagnostics, ArchiveDiagnostic{
				Code:   DiagArchiveRecordIDBroken,
				Record: i,
				Detail: fmt.Sprintf("id %q is not a sha-256 digest, so seneca did not write it", got),
			})
		case got != want:
			diagnostics = append(diagnostics, ArchiveDiagnostic{
				Code:   DiagArchiveRecordIDBroken,
				Record: i,
				Detail: fmt.Sprintf("id %s does not match its own content; it was edited after seneca wrote it", ShortRecordID(got)),
			})
		}
	}
	return diagnostics
}

// mutationDiagnostics is everything that blocks a state-changing command: the
// parse diagnostics a read tolerates, plus the record-integrity check. A
// mutation therefore refuses exactly the archives doctor calls corrupt.
func (a ReviewArchive) MutationDiagnostics() []ArchiveDiagnostic {
	integrity := a.RecordIntegrityDiagnostics()
	if len(integrity) == 0 {
		return a.Diagnostics
	}
	combined := make([]ArchiveDiagnostic, 0, len(a.Diagnostics)+len(integrity))
	combined = append(combined, a.Diagnostics...)
	return append(combined, integrity...)
}

// trimCR drops a CRLF carriage return for structural matching. The record's raw
// text keeps the original bytes.
func trimCR(line string) string { return strings.TrimSuffix(line, "\r") }
