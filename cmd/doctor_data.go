package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"sort"
	"strings"
)

// The checks that read what Seneca already wrote: the reflections file and the
// review archive. Both report exactly what a writing command would refuse.

// checkDoctorReflections parses the journal and reports every diagnostic the
// writing commands treat as fatal, plus the shape of what is stored. It returns
// the parsed entries so later checks can compare the archive against them.
func checkDoctorReflections(path, today string, report *doctorReport) []engine.ReflectionEntry {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		report.add(doctorCheckReflections, doctorInfo, "no reflections file yet at %s; the first save creates it", path)
		return nil
	case err != nil:
		report.add(doctorCheckReflections, doctorError, "cannot read %s: %v", path, err)
		return nil
	}

	doc := engine.ParseReflectionDocument(string(data))
	if len(doc.Entries) == 0 {
		report.add(doctorCheckReflections, doctorInfo, "%s holds no entries yet", path)
		return nil
	}
	report.add(doctorCheckReflections, doctorOK, "%s parsed from %s", engine.Plural(len(doc.Entries), "entry", "entries"), path)

	// Every parser diagnostic is corrupt or ambiguous data. Some block only the
	// entry they belong to; the message says which, so the author knows whether
	// today's reflection is blocked or only an edit of one past day.
	for _, d := range doc.Diagnostics {
		report.add(doctorCheckReflections, doctorError, "%s; %s", d, reflectionDiagnosticImpact(d.Code))
	}

	if dates := engine.DuplicateEntryDates(doc.Entries); len(dates) > 0 {
		report.add(doctorCheckReflections, doctorError,
			"duplicate entry dates: %s; every command that writes refuses this file until each `## <date>` heading is unique",
			strings.Join(dates, ", "))
	}

	legacy, unsupported := 0, 0
	for _, entry := range doc.Entries {
		if !entry.Metadata.Present {
			legacy++
			continue
		}
		if entry.Metadata.Version > engine.ReflectionSchemaVersion {
			unsupported++
		}
	}
	if legacy > 0 {
		report.add(doctorCheckReflections, doctorInfo,
			"%s the v2 marker; they are read as written and are never migrated in bulk",
			engine.Plural(legacy, "entry predates", "entries predate"))
	}
	if unsupported > 0 {
		report.add(doctorCheckReflections, doctorWarn,
			"%s declare a schema newer than v%d; upgrade seneca before editing them", engine.Plural(unsupported, "entry", "entries"), engine.ReflectionSchemaVersion)
	}
	if latest := latestEntryDate(doc.Entries); latest != "" {
		report.add(doctorCheckReflections, doctorInfo, "latest entry %s", engine.DatedRelative(latest, today))
	}
	if !sortedByDate(doc.Entries) {
		report.add(doctorCheckReflections, doctorInfo, "entries are not in date order; `seneca sort` rewrites them chronologically")
	}
	return doc.Entries
}

// reflectionDiagnosticImpact says what a diagnostic actually costs, which is
// what the author needs in order to decide whether to fix it now.
func reflectionDiagnosticImpact(code engine.ReflectionDiagnosticCode) string {
	switch {
	case engine.RewriteFatalCodes[code]:
		return "every command that writes this file refuses it"
	case engine.RerenderFatalCodes[code]:
		return "creating or editing this day is refused; other days still write"
	default:
		return "reads tolerate it"
	}
}

// latestEntryDate is the newest dated entry wherever it sits in the file.
func latestEntryDate(entries []engine.ReflectionEntry) string {
	latest := ""
	for _, entry := range entries {
		if entry.Date > latest {
			latest = entry.Date
		}
	}
	return latest
}

func sortedByDate(entries []engine.ReflectionEntry) bool {
	return sort.SliceIsSorted(entries, func(i, j int) bool {
		return entries[i].Date < entries[j].Date
	})
}

// checkDoctorArchive validates the archive the way a state-changing command
// would, and additionally re-derives every record ID from its own content — a
// check no command performs, because only an outside edit can break it. It
// returns the parsed archive when there is one worth reading further.
func checkDoctorArchive(path, today string, report *doctorReport) (engine.ReviewArchive, bool) {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		report.add(doctorCheckArchive, doctorInfo, "no review archive yet at %s; the first saved review or focus creates it", path)
		return engine.ReviewArchive{}, false
	case err != nil:
		report.add(doctorCheckArchive, doctorError, "cannot read %s: %v", path, err)
		return engine.ReviewArchive{}, false
	}

	if strings.TrimSpace(string(data)) == "" {
		report.add(doctorCheckArchive, doctorInfo, "review archive %s is empty; the first saved review or focus fills it", path)
		return engine.ReviewArchive{}, false
	}

	archive := engine.ParseReviewArchive(string(data))
	if err := archive.HeaderError(path); err != nil {
		// Same judgement a state-changing command makes, from the same code, so
		// doctor can never vouch for a header a write would refuse.
		report.add(doctorCheckArchive, doctorError, "%v", err)
		if !archive.HasMarker {
			// Nothing below describes this file; it is not Seneca's.
			return engine.ReviewArchive{}, false
		}
	}

	for _, d := range archive.Diagnostics {
		report.add(doctorCheckArchive, doctorError, "%s; every command that writes the archive refuses it", d)
	}

	reviews, focusEvents, unknown := 0, 0, 0
	for _, record := range archive.Records {
		switch record.Kind {
		case engine.ArchiveRecordReview:
			reviews++
		case engine.ArchiveRecordFocus:
			focusEvents++
		default:
			unknown++
		}
	}
	report.add(doctorCheckArchive, doctorOK, "%s and %s in %s",
		engine.Plural(reviews, "saved review", "saved reviews"), engine.Plural(focusEvents, "focus event", "focus events"), path)
	if unknown > 0 {
		report.add(doctorCheckArchive, doctorInfo,
			"%s were written by a newer seneca; they are preserved verbatim and ignored", engine.Plural(unknown, "record", "records"))
	}

	if records := archive.ReviewRecords(); len(records) > 0 {
		report.add(doctorCheckArchive, doctorInfo, "latest saved review: %s, %s to %s", strings.ToLower(records[0].Title), records[0].Start, engine.DatedRelative(records[0].End, today))
	}

	checkDoctorRecordIDs(archive, report)
	checkDoctorFocusTimeline(archive, today, report)
	return archive, true
}

// checkDoctorRecordIDs reports records whose stored ID is not the one Seneca
// would have derived for their content. It calls the same
// recordIntegrityDiagnostics every archive mutation validates against, so
// doctor can never call a record corrupt that a write would still accept.
func checkDoctorRecordIDs(archive engine.ReviewArchive, report *doctorReport) {
	broken := archive.RecordIntegrityDiagnostics()
	for _, d := range broken {
		report.add(doctorCheckArchive, doctorError,
			"record %d %s; every command that writes the archive refuses it", d.Record+1, d.Detail)
	}
	if len(broken) == 0 && len(archive.Records) > 0 {
		report.add(doctorCheckArchive, doctorOK, "every record id matches its content")
	}
}

// checkDoctorFocusTimeline reports what the focus resolution rule returns for
// the newest event in the file, which is what the daily form will show.
func checkDoctorFocusTimeline(archive engine.ReviewArchive, today string, report *doctorReport) {
	events := archive.FocusEvents()
	if len(events) == 0 {
		report.add(doctorCheckArchive, doctorInfo, "no focus has ever been set")
		return
	}

	latest := events[len(events)-1].Effective
	for _, event := range events {
		if event.Effective > latest {
			latest = event.Effective
		}
	}
	state := archive.FocusOn(latest)
	if !state.Active {
		report.add(doctorCheckArchive, doctorInfo, "no focus is active; the timeline ends with a clear event on %s", latest)
		return
	}
	report.add(doctorCheckArchive, doctorOK, "focus active since %s (%s): %s", state.Effective, focusAgeAndOrigin(state, today), firstLine(state.Text))
}

func firstLine(text string) string {
	if line, _, found := strings.Cut(text, "\n"); found {
		return line + " …"
	}
	return text
}

// focusAgeAndOrigin reads "40 days ago, review", or just the origin when the
// effective date cannot be placed.
func focusAgeAndOrigin(state engine.FocusState, today string) string {
	if rel := engine.RelativeDay(state.Effective, today); rel != "" {
		return rel + ", " + string(state.Origin)
	}
	return string(state.Origin)
}
