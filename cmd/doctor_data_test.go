package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"strings"
	"testing"
)

func TestDoctorReportsCorruptReflectionsAsErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "duplicate dates block every write",
			content: dayEntry("2026-08-23", "a", "b", "c") + "\n---\n\n" + dayEntry("2026-08-23", "d", "e", "f") + "\n---\n\n",
			want:    "duplicate entry dates: 2026-08-23",
		},
		{
			name:    "an impossible calendar date is malformed",
			content: "## 2026-02-30\n\n### Notice?\nNoticed.\n\n---\n\n",
			want:    "malformed-date",
		},
		{
			name:    "content before the first heading is not an entry",
			content: "Some preamble prose.\n\n" + dayEntry("2026-08-23", "a", "b", "c") + "\n---\n\n",
			want:    "invalid-entry",
		},
		{
			name:    "a malformed marker only blocks its own day",
			content: "## 2026-08-23\n<!-- seneca:entry -->\n\n### Notice?\nNoticed.\n\n---\n\n",
			want:    "creating or editing this day is refused",
		},
		{
			name:    "a repeated section is ambiguous",
			content: "## 2026-08-23\n<!-- seneca:entry v=2 -->\n\n### Notice?\nOne.\n\n### Notice?\nTwo.\n\n---\n\n",
			want:    "duplicate-section",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newDoctorHome(t)
			h.write(t, h.reflections, tc.content)

			report := diagnose(h.env())

			requireDoctor(t, report, doctorError, tc.want)
			if err := reportDoctor(&strings.Builder{}, report); err == nil {
				t.Fatal("expected a report with errors to fail")
			}
		})
	}
}

func TestDoctorReportsLegacyEntriesAndOrderAsInformation(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections,
		dayEntry("2026-08-24", "a", "b", "c")+"\n---\n\n"+
			dayEntry("2026-08-23", "d", "e", "f")+"\n---\n\n")

	report := diagnose(h.env())

	requireNoDoctor(t, report, doctorError)
	requireDoctor(t, report, doctorInfo, "2 entries predate the v2 marker")
	requireDoctor(t, report, doctorInfo, "entries are not in date order")

	// The report is read by one person about their own journal; counting to one
	// and reading like a machine is a small betrayal of that.
	h.write(t, h.reflections, dayEntry("2026-08-23", "d", "e", "f")+"\n---\n\n")
	requireDoctor(t, diagnose(h.env()), doctorInfo, "1 entry predates the v2 marker")
}

func TestDoctorRefusesToVouchForAForeignArchive(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections, doctorHealthyJournal)
	h.write(t, h.archive, "# Somebody else's notes\n\nNot seneca's file.\n")

	report := diagnose(h.env())

	requireDoctor(t, report, doctorError, "carries no `<!-- seneca:archive -->` marker")
}

func TestDoctorReportsArchiveDiagnosticsAndVersion(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "a focus record missing its event attribute",
			content: "<!-- seneca:archive v=2 -->\n\n## Focus set — 2026-08-24\n<!-- seneca:focus v=2 id=abc created=2026-08-24T09:00:00Z effective=2026-08-24 origin=manual -->\n\nText.\n\n---\n",
			want:    "missing-attribute",
		},
		{
			name:    "a revision naming no earlier review",
			content: "<!-- seneca:archive v=2 -->\n\n## Weekly review — 2026-08-17 to 2026-08-23\n<!-- seneca:review v=2 id=abc created=2026-08-24T09:00:00Z period=weekly start=2026-08-17 end=2026-08-23 revises=nothing -->\n\nBody.\n\n---\n",
			want:    "invalid-revision",
		},
		{
			name:    "an archive from a newer seneca",
			content: "<!-- seneca:archive v=99 -->\n",
			want:    "declares archive schema v99",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newDoctorHome(t)
			h.write(t, h.reflections, doctorHealthyJournal)
			h.write(t, h.archive, tc.content)

			report := diagnose(h.env())

			requireDoctor(t, report, doctorError, tc.want)
		})
	}
}

func TestDoctorCatchesARecordEditedAfterItWasWritten(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections, doctorHealthyJournal)
	mustAppend(t, h.archive, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Protect the first hour."))

	// The marker — and therefore the id — is untouched; only the body changes.
	original := readFileString(t, h.archive)
	h.write(t, h.archive, strings.Replace(original, "Protect the first hour.", "Protect the whole day.", 1))

	report := diagnose(h.env())

	requireDoctor(t, report, doctorError, "does not match its own content")
}

func TestDoctorReportsAnInactiveFocusAsInformation(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections, doctorHealthyJournal)
	mustAppend(t, h.archive, engine.NewFocusSetRecord(archiveTime(20, 9, 0), engine.FocusOriginManual, "", "Protect the first hour."))
	mustAppend(t, h.archive, engine.NewFocusClearRecord(archiveTime(22, 9, 0)))

	report := diagnose(h.env())

	requireNoDoctor(t, report, doctorError)
	requireDoctor(t, report, doctorInfo, "no focus is active; the timeline ends with a clear event on 2026-08-22")
}

func TestLatestEntryDateIgnoresFileOrder(t *testing.T) {
	entries := []engine.ReflectionEntry{{Date: "2026-09-10"}, {Date: ""}, {Date: "2026-10-02"}, {Date: "2026-03-07"}}
	if got := latestEntryDate(entries); got != "2026-10-02" {
		t.Fatalf("got %q, want 2026-10-02", got)
	}
	if got := latestEntryDate(nil); got != "" {
		t.Fatalf("expected no date for an empty journal, got %q", got)
	}
}
