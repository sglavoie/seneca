package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The archive schema version moved from 2 to 3 to admit quarterly and yearly
// reviews. These tests pin the two halves of that promise: an older file is
// restamped on the one line Seneca owns and nowhere else, and a header that
// never declared a usable version is still refused rather than invented.

// seedArchive writes archive content directly, bypassing the transaction, so a
// test can start from a file an older Seneca — or an author — left behind.
func seedArchive(t *testing.T, content string) string {
	t.Helper()

	path := newArchivePath(t)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("seeding archive: %v", err)
	}
	return path
}

// v2Archive is a complete file as Seneca v2 wrote it: its own header, prose the
// author added, and one genuine record whose id matches its content.
func v2Archive(t *testing.T) string {
	t.Helper()

	// Render the record through Seneca so its id is real and the whole file
	// survives mutationDiagnostics; then walk the markers back to v2.
	path := newArchivePath(t)
	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Ship the archive layer."))
	current := "v=" + strconv.Itoa(engine.ArchiveSchemaVersion)
	content := strings.ReplaceAll(readFileString(t, path), current, "v=2")

	// Author prose between the marker and the first record, to prove the upgrade
	// does not reach past the one line it owns.
	return strings.Replace(content, "\n\n## Focus set", "\n\nMy own note at the top.\n\n## Focus set", 1)
}

func TestArchiveAppendUpgradesAnOlderHeaderVersion(t *testing.T) {
	t.Parallel()

	before := v2Archive(t)
	path := seedArchive(t, before)

	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(25, 9, 0), engine.FocusOriginManual, "", "Ship the arc reviews."))
	after := readFileString(t, path)

	wantMarker := "<!-- seneca:archive v=" + strconv.Itoa(engine.ArchiveSchemaVersion) + " -->"
	if !strings.Contains(after, wantMarker) {
		t.Fatalf("expected the header marker upgraded to %s, got:\n%s", wantMarker, after)
	}
	if strings.Contains(after, "<!-- seneca:archive v=2 -->") {
		t.Fatalf("expected no v=2 archive marker to survive, got:\n%s", after)
	}

	// Every other byte of the old file has to still be there, in order: the only
	// difference is the one marker line, and the record appended after it.
	upgradedPrefix := strings.Replace(before, "<!-- seneca:archive v=2 -->", wantMarker, 1)
	if !strings.HasPrefix(after, upgradedPrefix) {
		t.Fatalf("expected only the archive marker line to change, got:\n%s\n--- want prefix ---\n%s", after, upgradedPrefix)
	}

	// The pre-existing v=2 *record* marker is not Seneca's to rewrite.
	if !strings.Contains(after, "<!-- seneca:focus v=2 ") {
		t.Fatalf("expected the existing record to keep its own v=2 marker, got:\n%s", after)
	}
	if !strings.Contains(after, "Ship the arc reviews.") {
		t.Fatalf("expected the appended record, got:\n%s", after)
	}

	// The upgraded file is one the next command accepts — appendRecords re-parses
	// before committing, so this also proves the rewrite went through that gate.
	if err := mustOpenArchive(t, path).Validate(); err != nil {
		t.Fatalf("expected the upgraded archive to validate, got %v", err)
	}
}

// TestArchiveUpgradeLeavesAnAlreadyCurrentHeaderAlone guards against churn: a
// file already at the current version must produce no spurious diff.
func TestArchiveUpgradeLeavesAnAlreadyCurrentHeaderAlone(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Ship the archive layer."))
	before := readFileString(t, path)

	archive := engine.ParseReviewArchive(before)
	if got := archive.UpgradedHeader(); got != archive.Header {
		t.Fatalf("expected a current header to be returned unchanged, got:\n%s", got)
	}

	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(25, 9, 0), engine.FocusOriginManual, "", "Ship the arc reviews."))
	if got := readFileString(t, path); !strings.HasPrefix(got, before) {
		t.Fatalf("expected the existing bytes untouched, got:\n%s", got)
	}
}

// TestArchiveUpgradeNeverInventsAMissingVersion is the refusal side of the
// upgrade: a marker that declares no usable version says nothing about the
// records below it, and a version Seneca supplied would be a claim nobody made.
// The write must still be refused, and the file left byte-identical.
func TestArchiveUpgradeNeverInventsAMissingVersion(t *testing.T) {
	cases := []struct {
		name   string
		marker string
	}{
		{name: "no version attribute", marker: "<!-- seneca:archive -->"},
		{name: "unreadable version", marker: "<!-- seneca:archive v=two -->"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := strings.Replace(v2Archive(t), "<!-- seneca:archive v=2 -->", tc.marker, 1)
			path := seedArchive(t, content)

			archive := engine.ParseReviewArchive(content)
			if archive.HeaderVersionOK {
				t.Fatalf("expected %s to read as an unusable version", tc.marker)
			}
			if got := archive.UpgradedHeader(); got != archive.Header {
				t.Fatalf("expected no upgrade of an unreadable version, got:\n%s", got)
			}

			tx := mustOpenArchive(t, path)
			err := tx.AppendRecords(engine.NewFocusSetRecord(archiveTime(25, 9, 0), engine.FocusOriginManual, "", "Ship the arc reviews."))
			var versionErr *engine.UnreadableArchiveVersionError
			if !errors.As(err, &versionErr) {
				t.Fatalf("expected an unreadable-version refusal, got %v", err)
			}
			if got := readFileString(t, path); got != content {
				t.Fatalf("expected the archive untouched, got:\n%s", got)
			}
		})
	}
}

// TestArchiveUpgradeRefusesANewerHeaderVersion pins the other direction: only a
// *lower* declared version is tolerated. A v3 binary reads v2; it never touches
// a file written by something it does not understand.
func TestArchiveUpgradeRefusesANewerHeaderVersion(t *testing.T) {
	t.Parallel()

	newer := strconv.Itoa(engine.ArchiveSchemaVersion + 1)
	content := strings.Replace(v2Archive(t), "<!-- seneca:archive v=2 -->", "<!-- seneca:archive v="+newer+" -->", 1)
	path := seedArchive(t, content)

	tx := mustOpenArchive(t, path)
	err := tx.AppendRecords(engine.NewFocusSetRecord(archiveTime(25, 9, 0), engine.FocusOriginManual, "", "Ship the arc reviews."))
	var versionErr *engine.UnsupportedArchiveVersionError
	if !errors.As(err, &versionErr) {
		t.Fatalf("expected an unsupported-version refusal, got %v", err)
	}
	if got := readFileString(t, path); got != content {
		t.Fatalf("expected the archive untouched, got:\n%s", got)
	}
}

// TestArchiveUpgradeCreatesTheCurrentHeaderFromEmpty keeps the empty-file path
// honest: nothing to upgrade, so the full header is synthesized at the version
// this binary writes.
func TestArchiveUpgradeCreatesTheCurrentHeaderFromEmpty(t *testing.T) {
	t.Parallel()

	path := newArchivePath(t)
	mustAppend(t, path, engine.NewFocusSetRecord(archiveTime(24, 9, 0), engine.FocusOriginManual, "", "Ship the archive layer."))

	got := readFileString(t, path)
	if !strings.HasPrefix(got, engine.RenderArchiveHeader()) {
		t.Fatalf("expected a full v%d header on a fresh archive, got:\n%s", engine.ArchiveSchemaVersion, got)
	}
}

// TestArchiveUpgradeKeepsUnknownHeaderAttributes proves the rewrite is scoped to
// the version: an attribute a newer Seneca put on the marker rides along.
func TestArchiveUpgradeKeepsUnknownHeaderAttributes(t *testing.T) {
	t.Parallel()

	content := strings.Replace(v2Archive(t),
		"<!-- seneca:archive v=2 -->", `<!-- seneca:archive v=2 origin="a tool i wrote" -->`, 1)
	archive := engine.ParseReviewArchive(content)

	want := "<!-- seneca:archive v=" + strconv.Itoa(engine.ArchiveSchemaVersion) + ` origin="a tool i wrote" -->`
	if got := archive.UpgradedHeader(); !strings.Contains(got, want) {
		t.Fatalf("expected %s in the upgraded header, got:\n%s", want, got)
	}
}

func TestArchiveParsesTheArcReviewPeriods(t *testing.T) {
	t.Parallel()

	cases := []struct {
		period engine.ReviewPeriod
		want   string
	}{
		{period: engine.ReviewPeriodWeekly, want: "Weekly review"},
		{period: engine.ReviewPeriodMonthly, want: "Monthly review"},
		{period: engine.ReviewPeriodQuarterly, want: "Quarterly review"},
		{period: engine.ReviewPeriodYearly, want: "Yearly review"},
	}

	for _, tc := range cases {
		t.Run(string(tc.period), func(t *testing.T) {
			t.Parallel()

			path := newArchivePath(t)
			tx := mustOpenArchive(t, path)
			record := tx.Archive.NewReviewRecord(archiveTime(24, 21, 4), tc.period,
				"2026-01-01", "2026-12-31", "", "Body.")
			if err := tx.AppendRecords(record); err != nil {
				t.Fatalf("appending a %s review returned error: %v", tc.period, err)
			}

			archive := engine.ParseReviewArchive(readFileString(t, path))
			if len(archive.Diagnostics) != 0 {
				t.Fatalf("expected period=%s to parse cleanly, got %v", tc.period, archive.Diagnostics)
			}
			reviews := archive.ReviewRecords()
			if len(reviews) != 1 {
				t.Fatalf("expected one saved review, got %d", len(reviews))
			}
			if reviews[0].Period != tc.period {
				t.Fatalf("expected period %s, got %s", tc.period, reviews[0].Period)
			}
			if reviews[0].Title != tc.want {
				t.Fatalf("expected the default title %q, got %q", tc.want, reviews[0].Title)
			}
		})
	}
}

func TestArchiveRejectsAnUnknownReviewPeriod(t *testing.T) {
	t.Parallel()

	content := "# Seneca Review Archive\n" +
		"<!-- seneca:archive v=" + strconv.Itoa(engine.ArchiveSchemaVersion) + " -->\n" +
		"\n" +
		"## Decade review — 2020-01-01 to 2029-12-31\n" +
		"<!-- seneca:review v=" + strconv.Itoa(engine.ArchiveSchemaVersion) + " id=" + strings.Repeat("a", 64) +
		" created=2026-08-24T21:04:05Z period=decade start=2020-01-01 end=2029-12-31 -->\n" +
		"\n" +
		"Body.\n" +
		"\n" +
		"---\n"

	archive := engine.ParseReviewArchive(content)
	if !hasArchiveDiagnostic(archive.Diagnostics, engine.DiagArchiveInvalidAttr) {
		t.Fatalf("expected an invalid-attribute diagnostic for an unknown period, got %v", archive.Diagnostics)
	}
	if !strings.Contains(archive.Diagnostics[0].Detail, "quarterly") {
		t.Fatalf("expected the diagnostic to name the allowed periods, got %q", archive.Diagnostics[0].Detail)
	}
}

func TestDefaultReviewTitleCoversEveryPeriod(t *testing.T) {
	t.Parallel()

	titles := map[engine.ReviewPeriod]string{
		engine.ReviewPeriodWeekly:    "Weekly review",
		engine.ReviewPeriodMonthly:   "Monthly review",
		engine.ReviewPeriodQuarterly: "Quarterly review",
		engine.ReviewPeriodYearly:    "Yearly review",
	}
	for period, want := range titles {
		if got := engine.DefaultReviewTitle(period); got != want {
			t.Fatalf("defaultReviewTitle(%s) = %q, want %q", period, got, want)
		}
	}
	// An unknown period is not a crash: it falls back to the shortest span.
	if got := engine.DefaultReviewTitle(engine.ReviewPeriod("decade")); got != "Weekly review" {
		t.Fatalf("expected an unknown period to fall back to the weekly title, got %q", got)
	}
}

func TestPeriodRankOrdersPeriodsBySpan(t *testing.T) {
	t.Parallel()

	ranks := []struct {
		period engine.ReviewPeriod
		want   int
	}{
		{period: engine.ReviewPeriodWeekly, want: 1},
		{period: engine.ReviewPeriodMonthly, want: 2},
		{period: engine.ReviewPeriodQuarterly, want: 3},
		{period: engine.ReviewPeriodYearly, want: 4},
		{period: engine.ReviewPeriod(""), want: 0},
		{period: engine.ReviewPeriod("decade"), want: 0},
	}
	for _, tc := range ranks {
		if got := periodRank(tc.period); got != tc.want {
			t.Fatalf("periodRank(%q) = %d, want %d", tc.period, got, tc.want)
		}
	}

	// The property callers rely on: an unknown period never outranks a known one.
	for _, period := range engine.ArchiveReviewPeriods {
		if periodRank(engine.ReviewPeriod("decade")) >= periodRank(period) {
			t.Fatalf("expected an unknown period to rank below %s", period)
		}
	}
}
