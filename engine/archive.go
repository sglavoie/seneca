package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The review archive is Seneca's durable memory: every saved review and every
// focus change lands here as one record, and nothing is ever rewritten. It sits
// beside the resolved reflections file as `seneca-reviews.md`.
//
// # File shape
//
//	# Seneca Review Archive
//	<!-- seneca:archive v=3 -->
//
//	<prose the author may edit freely>
//
//	## Weekly review — 2026-08-17 to 2026-08-23
//	<!-- seneca:review v=3 id=… created=… period=weekly start=… end=… -->
//
//	<review markdown>
//
//	---
//
//	## Focus set — 2026-08-24
//	<!-- seneca:focus v=3 id=… created=… event=set effective=… origin=manual -->
//
//	Ship the archive layer.
//
//	---
//
// # Record boundaries
//
// A record begins at a `## ` heading whose *next* line is a Seneca marker.
// Nothing else delimits a record, so a review body may contain `---` rules and
// `## ` headings without splitting the file. Everything before the first record
// is the header; everything the parser does not recognize stays byte-identical
// inside the record it follows. The `---` line Seneca renders after each record
// is cosmetic: it is body content, and reading a record's body drops trailing
// rules and blank lines.
//
// # Refusals
//
// A state-changing command refuses a file that exists but carries no
// `<!-- seneca:archive -->` marker (it belongs to something else), a file whose
// marker declares no schema version or an unreadable one (nothing vouches for
// the shape of what follows), a file whose header declares a schema version
// this binary does not understand, and any
// record with a malformed marker, a duplicate ID, a revision reference that
// does not name an earlier review, or an ID that no longer matches the record's
// own content. Reads tolerate all of these and warn.
//
// # Focus timeline
//
// Focus is an event stream shared by weekly and monthly reviews alike. The
// event with the greatest `effective` date on or before a target date wins;
// ties break by position in the file, which is creation order because the
// archive is append-only. A `clear` event that wins means no active focus.
//
// # Schema version
//
// v3 added the quarterly and yearly review periods. An older binary rejects
// those as an invalid `period`, so the header version is the honest signal that
// a file may now contain records it cannot read. A v2 header is still read
// happily, and the first append rewrites its marker line to v3 — see
// upgradedHeader. Only that one line is Seneca's to rewrite; records are
// append-only forever.

const (
	ArchiveFileName      = "seneca-reviews.md"
	ArchiveSchemaVersion = 3

	MarkerKindArchive = "archive"
	markerKindReview  = "review"
	markerKindFocus   = "focus"

	archiveHeadingPrefix = "## "
	archiveRecordRule    = "---"
	archiveTitle         = "# Seneca Review Archive"
)

type ArchiveRecordKind string

const (
	// ArchiveRecordUnknown is a record written by a newer Seneca. It is
	// preserved verbatim and otherwise ignored.
	ArchiveRecordUnknown ArchiveRecordKind = ""
	ArchiveRecordReview  ArchiveRecordKind = markerKindReview
	ArchiveRecordFocus   ArchiveRecordKind = markerKindFocus
)

type ReviewPeriod string

const (
	ReviewPeriodWeekly    ReviewPeriod = "weekly"
	ReviewPeriodMonthly   ReviewPeriod = "monthly"
	ReviewPeriodQuarterly ReviewPeriod = "quarterly"
	ReviewPeriodYearly    ReviewPeriod = "yearly"
)

// ArchiveReviewPeriods is the closed set of periods a `seneca:review` marker may
// declare, shortest span first. The parser's allow-list and periodRank both read
// it, so the vocabulary cannot drift between what is accepted and what is
// ordered.
var ArchiveReviewPeriods = []ReviewPeriod{
	ReviewPeriodWeekly,
	ReviewPeriodMonthly,
	ReviewPeriodQuarterly,
	ReviewPeriodYearly,
}

// ArchiveReviewPeriodNames is ArchiveReviewPeriods as the plain strings
// requireArchiveEnum compares against and names in its diagnostic.
func ArchiveReviewPeriodNames() []string {
	names := make([]string, len(ArchiveReviewPeriods))
	for i, period := range ArchiveReviewPeriods {
		names[i] = string(period)
	}
	return names
}

type FocusEventKind string

const (
	FocusEventSet   FocusEventKind = "set"
	FocusEventClear FocusEventKind = "clear"
)

type FocusOrigin string

const (
	FocusOriginManual FocusOrigin = "manual"
	FocusOriginReview FocusOrigin = "review"
)

// ReviewRecord is one saved weekly or monthly review. revises names the record
// this one supersedes, empty for a first save of a window.
type ReviewRecord struct {
	ID        string
	CreatedAt string
	Period    ReviewPeriod
	Start     string
	End       string
	Revises   string
	Title     string
	Body      string
}

// FocusEvent is one entry in the focus timeline.
type FocusEvent struct {
	ID        string
	createdAt string
	Event     FocusEventKind
	Effective string
	Origin    FocusOrigin
	Source    string
	Text      string
}

// FocusState is the focus in effect at some date, derived entirely from events.
type FocusState struct {
	Active    bool
	Text      string
	Effective string
	Origin    FocusOrigin
	Source    string
	eventID   string
	createdAt string
	// Disputed is set when two devices changed the focus for Effective
	// independently. The state is then not Active and has no Text: Candidates
	// are the changes competing, and none of them is settled.
	Disputed   bool
	Candidates []FocusCandidate
}

// ArchiveRecord is one `## ` block. raw is byte-preserved; review and focus are
// the parsed views over it, and at most one is set.
type ArchiveRecord struct {
	Kind        ArchiveRecordKind
	Raw         string
	headingLine string
	markerLine  string
	body        string
	Review      *ReviewRecord
	Focus       *FocusEvent
}

// recordID is the record's stable identity, empty for an unrecognized kind.
func (r ArchiveRecord) RecordID() string {
	switch {
	case r.Review != nil:
		return r.Review.ID
	case r.Focus != nil:
		return r.Focus.ID
	default:
		return ""
	}
}

// expectedRecordID recomputes the identity a record of a kind Seneca writes
// must carry, from exactly the fields newReviewRecord and NewFocusRecord hash.
// ok is false for a record kind this binary does not derive IDs for.
func (r ArchiveRecord) ExpectedRecordID() (string, bool) {
	switch {
	case r.Review != nil:
		v := r.Review
		return newArchiveRecordID(markerKindReview, v.CreatedAt,
			[]string{string(v.Period), v.Start, v.End, v.Revises}, v.Body), true
	case r.Focus != nil:
		f := r.Focus
		return newArchiveRecordID(markerKindFocus, f.createdAt,
			[]string{string(f.Event), f.Effective, string(f.Origin), f.Source}, f.Text), true
	default:
		return "", false
	}
}

// ReviewArchive is a parsed archive file.
type ReviewArchive struct {
	Header    string
	HasMarker bool
	// headerVersion is meaningful only when headerVersionOK; headerVersionRaw
	// keeps whatever the marker actually declared, so a refusal can quote it.
	HeaderVersion    int
	HeaderVersionOK  bool
	headerVersionRaw string
	Records          []ArchiveRecord
	Diagnostics      []ArchiveDiagnostic
}

// ArchivePathFor derives the archive path from a resolved reflections path, so
// a SENECA_FILE override moves the archive with it.
func ArchivePathFor(reflectionsPath string) string {
	return filepath.Join(filepath.Dir(reflectionsPath), ArchiveFileName)
}

// LoadReviewArchive reads an archive for a read-only command. A missing file is
// reported, never created.
func LoadReviewArchive(path string) (ReviewArchive, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ReviewArchive{}, false, nil
		}
		return ReviewArchive{}, false, fmt.Errorf("reading %s: %w", path, err)
	}
	return ParseReviewArchive(string(data)), true, nil
}

// reviewRecords returns saved reviews newest first.
func (a ReviewArchive) ReviewRecords() []ReviewRecord {
	var records []ReviewRecord
	for i := len(a.Records) - 1; i >= 0; i-- {
		if a.Records[i].Review != nil {
			records = append(records, *a.Records[i].Review)
		}
	}
	return records
}

// focusEvents returns the focus timeline in creation order. Weekly reviews,
// monthly reviews, and manual changes all feed this one list.
func (a ReviewArchive) FocusEvents() []FocusEvent {
	var events []FocusEvent
	for _, record := range a.Records {
		if record.Focus != nil && record.Focus.Effective != "" {
			events = append(events, *record.Focus)
		}
	}
	return events
}

// focusOn resolves the focus in effect on a YYYY-MM-DD date. The latest
// effective event wins; a tie goes to the later one in the file, which is the
// later creation because the archive is append-only.
func (a ReviewArchive) FocusOn(date string) FocusState {
	events := a.FocusEvents()
	best := -1
	for i, event := range events {
		if event.Effective > date {
			continue
		}
		if best < 0 || event.Effective >= events[best].Effective {
			best = i
		}
	}
	if best < 0 || events[best].Event != FocusEventSet {
		return FocusState{}
	}
	winner := events[best]
	return FocusState{
		Active:    true,
		Text:      winner.Text,
		Effective: winner.Effective,
		Origin:    winner.Origin,
		Source:    winner.Source,
		eventID:   winner.ID,
		createdAt: winner.createdAt,
	}
}

// latestReviewForPeriod finds the newest saved review covering the same window,
// which a new save supersedes as a revision.
func (a ReviewArchive) latestReviewForPeriod(period ReviewPeriod, start, end string) (ReviewRecord, bool) {
	for i := len(a.Records) - 1; i >= 0; i-- {
		record := a.Records[i].Review
		if record == nil {
			continue
		}
		if record.Period == period && record.Start == start && record.End == end {
			return *record, true
		}
	}
	return ReviewRecord{}, false
}

// reviewDueAfterDays is how long the newest review may sit before Seneca
// mentions the writing it has not seen. It is longer than a month, so a
// monthly review cadence never trips it.
const reviewDueAfterDays = 31

// ReviewDueNote says when entries have piled up past the newest saved review:
// how many, how long ago that review's window ended, and which month to review. It is a reminder, not
// a warning, and stays silent with no saved review, nothing newer to review, or
// a review recent enough to still be the current word.
func ReviewDueNote(entries []ReflectionEntry, archive ReviewArchive, today string) (string, bool) {
	lastEnd := ""
	for _, record := range archive.ReviewRecords() {
		if record.End > lastEnd {
			lastEnd = record.End
		}
	}
	if lastEnd == "" {
		return "", false
	}
	if days, ok := daysSince(lastEnd, today); !ok || days <= reviewDueAfterDays {
		return "", false
	}
	since, newest := 0, ""
	for _, entry := range entries {
		if entry.Date > lastEnd {
			since++
			if entry.Date > newest {
				newest = entry.Date
			}
		}
	}
	if since == 0 {
		return "", false
	}
	// Name the month holding the newest of them: the default weekly window
	// may well be empty by the time a review is overdue.
	return fmt.Sprintf("%s written since the latest review ended %s; `seneca review --month=%s` takes stock of the latest",
		Plural(since, "entry", "entries"), DatedRelative(lastEnd, today), newest[:len("2006-01")]), true
}
