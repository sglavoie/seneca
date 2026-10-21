package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Building and rendering the records Seneca appends: saved reviews and focus
// events. Every record carries a SHA-256 identity derived from its own content.

// render reproduces the parsed file byte for byte.
func (a ReviewArchive) Render() string { return a.renderWithHeader(a.Header) }

// renderWithHeader is render with the header substituted, which is how the
// schema-version upgrade replaces one line without touching a record.
func (a ReviewArchive) renderWithHeader(header string) string {
	parts := make([]string, 0, len(a.Records)+1)
	parts = append(parts, header)
	for _, record := range a.Records {
		parts = append(parts, record.Raw)
	}
	return strings.Join(parts, "\n")
}

// appended returns the archive with records added at the end. Existing bytes
// survive untouched apart from trailing-newline normalization and the header's
// schema-version marker; an empty or missing archive gains Seneca's header
// first.
func (a ReviewArchive) appended(records ...ArchiveRecord) string {
	base := a.Render()
	if strings.TrimSpace(base) == "" {
		base = RenderArchiveHeader()
	} else {
		base = a.renderWithHeader(a.UpgradedHeader())
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(base, "\n"))
	b.WriteByte('\n')
	for _, record := range records {
		b.WriteByte('\n')
		b.WriteString(strings.TrimRight(record.Raw, "\n"))
		b.WriteByte('\n')
	}
	return b.String()
}

// upgradedHeader returns the header with its archive marker restamped at the
// version this binary writes, and the header unchanged in every other case.
//
// The marker line is the one line Seneca owns: records are append-only forever,
// and the author's prose around the marker is theirs. So exactly one line can
// change, and only when the file already declared a *readable* older version. A
// missing or unreadable version is never upgraded — headerError refuses those
// outright, and a version Seneca invented would be a claim nobody made about the
// records below it.
func (a ReviewArchive) UpgradedHeader() string {
	if !a.HeaderVersionOK || a.HeaderVersion >= ArchiveSchemaVersion {
		return a.Header
	}

	lines := strings.Split(a.Header, "\n")
	for i, line := range lines {
		// The same first-well-formed-marker rule parseArchiveHeaderMarker used to
		// read the version, so the line rewritten is the line that was believed.
		marker, ok := parseReflectionMarker(trimCR(line))
		if !ok || marker.malformed || marker.kind != MarkerKindArchive {
			continue
		}
		upgraded := renderArchiveMarker(marker, ArchiveSchemaVersion)
		if strings.HasSuffix(line, "\r") {
			upgraded += "\r"
		}
		lines[i] = upgraded
		break
	}
	return strings.Join(lines, "\n")
}

// renderArchiveMarker rebuilds the header marker at a given version, keeping
// every other attribute in the order it was written. For the marker Seneca
// writes this reproduces RenderArchiveHeader's line byte for byte.
func renderArchiveMarker(marker ReflectionMarker, version int) string {
	attrs := make([]string, 0, len(marker.order))
	for _, key := range marker.order {
		if key == "v" {
			attrs = append(attrs, fmt.Sprintf("v=%d", version))
			continue
		}
		attrs = append(attrs, key+"="+formatMarkerValue(marker.attrs[key]))
	}
	return fmt.Sprintf("<!-- seneca:%s %s -->", MarkerKindArchive, strings.Join(attrs, " "))
}

func RenderArchiveHeader() string {
	return archiveTitle + "\n" +
		fmt.Sprintf("<!-- seneca:%s v=%d -->\n", MarkerKindArchive, ArchiveSchemaVersion) +
		"\n" +
		"Append-only. Seneca adds one record per saved review and per focus\n" +
		"change, and never rewrites a record it already wrote. Prose you add\n" +
		"between records is preserved. Seneca refuses to touch this file if the\n" +
		"marker above goes missing.\n"
}

// newReviewRecord builds a saved review for a window and links it to the record
// it revises. Session 5 persists the result through appendRecords.
func (a ReviewArchive) NewReviewRecord(now time.Time, period ReviewPeriod, start, end, title, body string) ArchiveRecord {
	record := ReviewRecord{
		CreatedAt: now.Format(time.RFC3339Nano),
		Period:    period,
		Start:     start,
		End:       end,
		Title:     strings.TrimSpace(title),
		Body:      strings.TrimSpace(body),
	}
	if record.Title == "" {
		record.Title = DefaultReviewTitle(period)
	}
	if prior, ok := a.latestReviewForPeriod(period, start, end); ok {
		record.Revises = prior.ID
	}
	record.ID = newArchiveRecordID(markerKindReview, record.CreatedAt,
		[]string{string(period), start, end, record.Revises}, record.Body)
	return renderReviewRecord(record)
}

func DefaultReviewTitle(period ReviewPeriod) string {
	switch period {
	case ReviewPeriodMonthly:
		return "Monthly review"
	case ReviewPeriodQuarterly:
		return "Quarterly review"
	case ReviewPeriodYearly:
		return "Yearly review"
	default:
		return "Weekly review"
	}
}

// NewFocusRecord is the general focus-event constructor. Session 5 uses it for
// an accepted or edited review recommendation, with origin=review and the
// review record's ID as source.
func NewFocusRecord(now time.Time, event FocusEventKind, effective string, origin FocusOrigin, source, text string) ArchiveRecord {
	focus := FocusEvent{
		createdAt: now.Format(time.RFC3339Nano),
		Event:     event,
		Effective: effective,
		Origin:    origin,
		Source:    source,
		Text:      strings.TrimSpace(text),
	}
	if focus.Event == FocusEventClear {
		focus.Text = ""
	}
	focus.ID = newArchiveRecordID(markerKindFocus, focus.createdAt,
		[]string{string(event), effective, string(origin), source}, focus.Text)
	return renderFocusRecord(focus)
}

func NewFocusSetRecord(now time.Time, origin FocusOrigin, source, text string) ArchiveRecord {
	return NewFocusRecord(now, FocusEventSet, now.Format("2006-01-02"), origin, source, text)
}

func NewFocusClearRecord(now time.Time) ArchiveRecord {
	return NewFocusRecord(now, FocusEventClear, now.Format("2006-01-02"), FocusOriginManual, "", "")
}

func renderReviewRecord(record ReviewRecord) ArchiveRecord {
	heading := fmt.Sprintf("%s%s — %s to %s", archiveHeadingPrefix, record.Title, record.Start, record.End)
	attrs := []string{
		fmt.Sprintf("v=%d", ArchiveSchemaVersion),
		"id=" + formatMarkerValue(record.ID),
		"created=" + formatMarkerValue(record.CreatedAt),
		"period=" + formatMarkerValue(string(record.Period)),
		"start=" + formatMarkerValue(record.Start),
		"end=" + formatMarkerValue(record.End),
	}
	if record.Revises != "" {
		attrs = append(attrs, "revises="+formatMarkerValue(record.Revises))
	}
	marker := fmt.Sprintf("<!-- seneca:%s %s -->", markerKindReview, strings.Join(attrs, " "))

	return ArchiveRecord{
		Kind:        ArchiveRecordReview,
		Raw:         renderArchiveRecordRaw(heading, marker, record.Body),
		headingLine: heading,
		markerLine:  marker,
		body:        record.Body,
		Review:      &record,
	}
}

func renderFocusRecord(focus FocusEvent) ArchiveRecord {
	action := "Focus set"
	body := focus.Text
	if focus.Event == FocusEventClear {
		action = "Focus cleared"
		// Never parsed back: a clear carries no text, only a readable note.
		body = "Focus cleared."
	}
	heading := fmt.Sprintf("%s%s — %s", archiveHeadingPrefix, action, focus.Effective)

	attrs := []string{
		fmt.Sprintf("v=%d", ArchiveSchemaVersion),
		"id=" + formatMarkerValue(focus.ID),
		"created=" + formatMarkerValue(focus.createdAt),
		"event=" + formatMarkerValue(string(focus.Event)),
		"effective=" + formatMarkerValue(focus.Effective),
		"origin=" + formatMarkerValue(string(focus.Origin)),
	}
	if focus.Source != "" {
		attrs = append(attrs, "source="+formatMarkerValue(focus.Source))
	}
	marker := fmt.Sprintf("<!-- seneca:%s %s -->", markerKindFocus, strings.Join(attrs, " "))

	return ArchiveRecord{
		Kind:        ArchiveRecordFocus,
		Raw:         renderArchiveRecordRaw(heading, marker, body),
		headingLine: heading,
		markerLine:  marker,
		body:        body,
		Focus:       &focus,
	}
}

func renderArchiveRecordRaw(heading, marker, body string) string {
	var b strings.Builder
	b.WriteString(heading)
	b.WriteByte('\n')
	b.WriteString(marker)
	b.WriteString("\n\n")
	if body := strings.TrimSpace(body); body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	b.WriteString(archiveRecordRule)
	b.WriteByte('\n')
	return b.String()
}

// newArchiveRecordID derives a stable SHA-256 identity from the record kind,
// its creation time, its period or source metadata, and its content. Fields are
// NUL-separated so no concatenation can be forged from different parts.
func newArchiveRecordID(kind, createdAt string, metadata []string, content string) string {
	digest := sha256.New()
	write := func(value string) {
		digest.Write([]byte(value))
		digest.Write([]byte{0})
	}
	write(kind)
	write(createdAt)
	for _, value := range metadata {
		write(value)
	}
	write(content)
	return hex.EncodeToString(digest.Sum(nil))
}

// ShortRecordID abbreviates a SHA-256 for display. The archive always stores
// the full value.
func ShortRecordID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
