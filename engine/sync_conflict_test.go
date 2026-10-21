package engine

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// rewrite replaces one day's entry in the reflections file the way an editor
// does, leaving every other entry's bytes alone. Empty raw removes the day.
func (p *peer) rewrite(date, raw string) {
	p.t.Helper()

	doc := ParseReflectionDocument(readFileString(p.t, p.reflections()))
	var entries []ReflectionEntry
	found := false
	for _, entry := range doc.Entries {
		if entry.Date != date {
			entries = append(entries, entry)
			continue
		}
		found = true
		if raw != "" {
			entries = append(entries, NewReflectionEntry(raw))
		}
	}
	if !found && raw != "" {
		entries = append(entries, NewReflectionEntry(raw))
	}
	p.overwrite(RenderReflectionEntries(entries))
}

// recordIDs lists the archive's records in file order.
func (p *peer) recordIDs() []string {
	p.t.Helper()

	archive := ParseReviewArchive(readFileString(p.t, p.archive()))
	if diagnostics := archive.MutationDiagnostics(); len(diagnostics) > 0 {
		p.t.Fatalf("%s: the archive is not valid: %v", p.label, diagnostics)
	}
	ids := make([]string, len(archive.Records))
	for i, record := range archive.Records {
		ids[i] = record.RecordID()
	}
	return ids
}

func (p *peer) focus(date string) FocusState {
	p.t.Helper()

	state, _, err := p.journal.Focus(date)
	if err != nil {
		p.t.Fatalf("%s: Focus(%s) returned error: %v", p.label, date, err)
	}
	return state
}

func (p *peer) disputes() []FocusDispute {
	p.t.Helper()

	disputes, err := p.journal.FocusDisputes()
	if err != nil {
		p.t.Fatalf("%s: FocusDisputes returned error: %v", p.label, err)
	}
	return disputes
}

func (p *peer) resolve(id string, resolution SyncResolution) SyncResolved {
	p.t.Helper()

	resolved, err := p.journal.ResolveSyncConflict(id, resolution)
	if err != nil {
		p.t.Fatalf("%s: ResolveSyncConflict returned error: %v", p.label, err)
	}
	return resolved
}

// settleAll exchanges between every pair of peers until nothing is left to
// send or apply.
func settleAll(t *testing.T, peers ...*peer) {
	t.Helper()

	for round := 0; round < 4; round++ {
		for _, from := range peers {
			from.publish()
			for _, to := range peers {
				if to != from {
					carry(t, from, to, nil)
					to.pull()
				}
			}
		}
	}
}

// conflictSet describes the conflicts a peer holds by what competes in them,
// which is what two peers agree on while each shows its own version.
func conflictSet(p *peer) []string {
	p.t.Helper()

	var set []string
	for _, conflict := range p.conflicts() {
		set = append(set, fmt.Sprintf("%s %s %v", conflict.Artifact, conflict.ID, conflict.Versions()))
	}
	return set
}

func requireNoConflicts(t *testing.T, peers ...*peer) {
	t.Helper()

	for _, p := range peers {
		if conflicts := p.conflicts(); len(conflicts) > 0 {
			t.Fatalf("%s: expected no conflicts, got %+v", p.label, conflicts)
		}
		if status := p.status(); len(status.Pending) > 0 || len(status.Conflicts) > 0 {
			t.Fatalf("%s: expected nothing pending, got %+v", p.label, status)
		}
	}
}

func TestIndependentSectionsOfOneDayMergeOnBothDevices(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	a.rewrite("2026-08-10", dayEntry("2026-08-10", "Noticed more.", "Handled.", "Start before nine."))
	b.rewrite("2026-08-10", dayEntry("2026-08-10", "Noticed.", "Handled.", "Start before eight."))
	a.pull()
	a.publish()
	carry(t, a, b, nil)
	imported := b.pull()
	if len(imported.Merged) != 1 || imported.Merged[0] != entryArtifact("2026-08-10") || len(imported.Conflicts) != 0 {
		t.Fatalf("expected the day to be merged, got %+v", imported)
	}
	settle(t, a, b)

	requireSameArtifacts(t, a, b)
	requireNoConflicts(t, a, b)
	want := NewReflectionEntry(dayEntry("2026-08-10", "Noticed more.", "Handled.", "Start before eight.")).Raw
	if got := a.artifacts()[entryArtifact("2026-08-10")]; got != want {
		t.Fatalf("expected both devices' changes in the day, got\n%s", got)
	}
	// Each device recorded the merge as a transaction of its own that covers
	// both versions.
	merge := b.manifest(imported.Recorded[len(imported.Recorded)-1])
	if merge.Origin != SyncOriginMerge || len(merge.Changes) != 1 || len(merge.Changes[0].Resolves) != 1 {
		t.Fatalf("expected the merge to be recorded as a covering transaction, got %+v", merge)
	}
}

// trio is a pair with a third device that joined the same journal.
func trio(t *testing.T) (desk, phone, tablet *peer) {
	t.Helper()

	desk, phone = pair(t)
	tablet = newPeer(t, "tablet", 3)
	carry(t, desk, tablet, nil)
	tablet.join()
	requireNoDiagnostics(t, tablet.pull())
	return desk, phone, tablet
}

// deskSnapshot is everything a delivery order must not change.
type deskSnapshot struct {
	artifacts map[string]string
	records   []string
	conflicts []string
	shown     []string
	disputes  []string
}

func snapshotOf(p *peer) deskSnapshot {
	p.t.Helper()

	snapshot := deskSnapshot{artifacts: p.artifacts(), records: p.recordIDs(), conflicts: conflictSet(p)}
	for _, conflict := range p.conflicts() {
		snapshot.shown = append(snapshot.shown, string(conflict.Ours.Revision))
	}
	for _, dispute := range p.disputes() {
		snapshot.disputes = append(snapshot.disputes, dispute.ID)
	}
	return snapshot
}

func (s deskSnapshot) String() string {
	return fmt.Sprintf("artifacts %q\nrecords %v\nconflicts %v\nshown %v\ndisputes %v", s.artifacts, s.records, s.conflicts, s.shown, s.disputes)
}

func (s deskSnapshot) equal(other deskSnapshot) bool {
	return sameTree(s.artifacts, other.artifacts) && equalStrings(s.records, other.records) &&
		equalStrings(s.conflicts, other.conflicts) && equalStrings(s.shown, other.shown) && equalStrings(s.disputes, other.disputes)
}

func TestDeliveryOrderChangesNeitherTheDataNorTheConflicts(t *testing.T) {
	t.Parallel()

	isManifest := func(name string) bool { return strings.HasPrefix(name, syncTransactionsName+"/") }
	orders := map[string]func(t *testing.T, desk, phone, tablet *peer){
		"phone first": func(t *testing.T, desk, phone, tablet *peer) {
			carry(t, phone, desk, nil)
			desk.pull()
			carry(t, tablet, desk, nil)
			desk.pull()
		},
		"tablet first": func(t *testing.T, desk, phone, tablet *peer) {
			carry(t, tablet, desk, nil)
			desk.pull()
			carry(t, phone, desk, nil)
			desk.pull()
		},
		"all at once": func(t *testing.T, desk, phone, tablet *peer) {
			carry(t, tablet, desk, nil)
			carry(t, phone, desk, nil)
			desk.pull()
		},
		"one manifest at a time, newest first": func(t *testing.T, desk, phone, tablet *peer) {
			type delivery struct {
				from *peer
				name string
			}
			var deliveries []delivery
			for _, from := range []*peer{phone, tablet} {
				carry(t, from, desk, func(name string) bool { return !isManifest(name) })
				for _, name := range exchanged(t, from.cloud) {
					if isManifest(name) {
						deliveries = append(deliveries, delivery{from, name})
					}
				}
			}
			// Newest first within each device, the two devices interleaved.
			sort.Slice(deliveries, func(i, j int) bool {
				left, right := deliveries[i].name, deliveries[j].name
				return left[strings.LastIndex(left, "/"):] > right[strings.LastIndex(right, "/"):]
			})
			for _, next := range deliveries {
				carry(t, next.from, desk, func(name string) bool { return name == next.name })
				desk.pull()
			}
		},
	}

	var first *deskSnapshot
	var firstName string
	for _, name := range []string{"phone first", "tablet first", "all at once", "one manifest at a time, newest first"} {
		desk, phone, tablet := trio(t)

		// Both edit the same section of one day: a conflict.
		phone.rewrite("2026-08-10", dayEntry("2026-08-10", "The phone noticed.", "Handled.", "Start before nine."))
		tablet.rewrite("2026-08-10", dayEntry("2026-08-10", "The tablet noticed.", "Handled.", "Start before nine."))
		// Each edits a different section of another day: a merge.
		phone.rewrite("2026-08-11", dayEntry("2026-08-11", "Again, on the phone.", "", "Keep going."))
		tablet.rewrite("2026-08-11", dayEntry("2026-08-11", "Again.", "", "Keep going, on the tablet."))
		// Each writes a day of its own, and both write one the same way.
		phone.write("2026-08-12", "Only on the phone.", "", "")
		tablet.write("2026-08-13", "Only on the tablet.", "", "")
		phone.write("2026-08-14", "The same words.", "", "")
		tablet.write("2026-08-14", "The same words.", "", "")
		// Each changes the focus for the same day, and reviews the same week.
		for _, p := range []*peer{phone, tablet} {
			if _, err := p.journal.SetFocus("The " + p.label + "'s focus."); err != nil {
				t.Fatalf("%s: SetFocus returned error: %v", p.label, err)
			}
			if _, err := p.journal.SaveReview(ReviewSave{Period: ReviewPeriodWeekly, Start: "2026-08-10", End: "2026-08-16", Body: "Reviewed on the " + p.label + "."}); err != nil {
				t.Fatalf("%s: SaveReview returned error: %v", p.label, err)
			}
			p.publish()
		}

		orders[name](t, desk, phone, tablet)
		if pending := desk.status().Pending; len(pending) > 0 {
			t.Fatalf("%s: expected everything delivered to be applied, got %+v", name, pending)
		}
		got := snapshotOf(desk)
		if len(got.conflicts) != 1 || len(got.disputes) != 1 || len(got.records) != 5 {
			t.Fatalf("%s: expected one conflict, one disputed focus, and five records, got\n%s", name, got)
		}
		if merged := got.artifacts[entryArtifact("2026-08-11")]; !strings.Contains(merged, "Again, on the phone.") || !strings.Contains(merged, "Keep going, on the tablet.") {
			t.Fatalf("%s: the day both changed in different sections was not merged: %q", name, merged)
		}
		if first == nil {
			first, firstName = &got, name
		} else if !first.equal(got) {
			t.Fatalf("delivery order changed the outcome.\n%s:\n%s\n%s:\n%s", firstName, first, name, got)
		}

		// Every device ends with the same journal, the same archive order, and
		// the same conflict, whatever each one's file shows for that day.
		settleAll(t, desk, phone, tablet)
		for _, p := range []*peer{phone, tablet} {
			if !equalStrings(conflictSet(p), got.conflicts) || !equalStrings(p.recordIDs(), got.records) {
				t.Fatalf("%s: %s disagrees with the desk:\n%v %v\nagainst\n%v %v", name, p.label, conflictSet(p), p.recordIDs(), got.conflicts, got.records)
			}
			if kept := p.artifacts()[entryArtifact("2026-08-10")]; !strings.Contains(kept, "The "+p.label+" noticed.") {
				t.Fatalf("%s: %s no longer shows its own version of the conflicted day: %q", name, p.label, kept)
			}
			if p.focus("2026-08-20").Active || !p.focus("2026-08-20").Disputed {
				t.Fatalf("%s: %s presents a disputed focus as settled: %+v", name, p.label, p.focus("2026-08-20"))
			}
		}

		// The desk, which wrote neither version, settles it for all three.
		conflict := desk.conflicts()[0]
		desk.resolve(conflict.ID, SyncResolution{Keep: conflict.Theirs[0].Revision})
		settleAll(t, desk, phone, tablet)
		requireNoConflicts(t, desk, phone, tablet)
		requireSameArtifacts(t, desk, phone)
		requireSameArtifacts(t, desk, tablet)
		if got := desk.artifacts()[entryArtifact("2026-08-10")]; got != conflict.Theirs[0].Content {
			t.Fatalf("%s: the resolution did not keep the chosen version: %q", name, got)
		}
	}
}

// conflicted is a pair in which both devices rewrote the same answer of one
// day and have exchanged everything.
func conflicted(t *testing.T) (a, b *peer) {
	t.Helper()

	a, b = pair(t)
	a.rewrite("2026-08-10", dayEntry("2026-08-10", "The desk's version.", "Handled.", "Start before nine."))
	b.rewrite("2026-08-10", dayEntry("2026-08-10", "The phone's version.", "Handled.", "Start before nine."))
	settle(t, a, b)
	if left, right := conflictSet(a), conflictSet(b); len(left) != 1 || !equalStrings(left, right) {
		t.Fatalf("expected both devices to hold the same conflict, got %v and %v", left, right)
	}
	return a, b
}

func TestResolvingOnEitherDeviceConvergesBoth(t *testing.T) {
	t.Parallel()

	custom := NewReflectionEntry(dayEntry("2026-08-10", "Both versions, reconciled by hand.", "Handled.", "Start before nine.")).Raw
	for _, resolver := range []string{"desk", "phone"} {
		for _, choice := range []string{"ours", "theirs", "something new"} {
			t.Run(resolver+" keeps "+choice, func(t *testing.T) {
				t.Parallel()

				a, b := conflicted(t)
				p := a
				if resolver == "phone" {
					p = b
				}
				conflict := p.conflicts()[0]
				var resolution SyncResolution
				want := custom
				switch choice {
				case "ours":
					resolution, want = SyncResolution{Keep: conflict.Ours.Revision}, conflict.Ours.Content
				case "theirs":
					resolution, want = SyncResolution{Keep: conflict.Theirs[0].Revision}, conflict.Theirs[0].Content
				default:
					resolution = SyncResolution{Content: custom + "\n\n"}
				}

				resolved := p.resolve(conflict.ID, resolution)
				if tx := p.manifest(resolved.Transaction); tx.Origin != SyncOriginResolution || len(tx.Parents) == 0 {
					t.Fatalf("expected the resolution to be a transaction on top of both versions, got %+v", tx)
				}
				settle(t, a, b)

				requireSameArtifacts(t, a, b)
				requireNoConflicts(t, a, b)
				if got := a.artifacts()[entryArtifact("2026-08-10")]; got != want {
					t.Fatalf("expected the chosen version on both devices, got\n%s", got)
				}
				// Neither competing version is gone: both are still held, on
				// both devices, whichever was kept.
				for _, device := range []*peer{a, b} {
					for _, version := range append([]SyncConflictVersion{conflict.Ours, conflict.Base}, conflict.Theirs...) {
						if device.retained(version.Revision) != version.Content {
							t.Fatalf("%s no longer holds a version that competed", device.label)
						}
					}
				}
				if got := len(ParseReflectionDocument(readFileString(t, a.reflections())).Entries); got != 2 {
					t.Fatalf("expected two days and no duplicate, got %d entries", got)
				}
			})
		}
	}
}

func TestAResolutionOpenedBeforeAnotherChangeArrivesIsRefused(t *testing.T) {
	t.Parallel()

	a, b := conflicted(t)
	opened := a.conflicts()[0]

	// The phone rewrites its version again while the desk is deciding.
	b.rewrite("2026-08-10", dayEntry("2026-08-10", "The phone's second thought.", "Handled.", "Start before nine."))
	b.pull()
	b.publish()
	carry(t, b, a, nil)
	a.pull()

	before := a.tree()
	var stale *StaleConflictError
	if _, err := a.journal.ResolveSyncConflict(opened.ID, SyncResolution{Keep: opened.Ours.Revision}); !errors.As(err, &stale) {
		t.Fatalf("expected the outdated answer to be refused, got %v", err)
	}
	if !sameTree(before, a.tree()) {
		t.Fatal("a refused resolution wrote something")
	}

	current := a.conflicts()[0]
	if current.ID == opened.ID || !strings.Contains(current.Theirs[0].Content, "second thought") {
		t.Fatalf("expected the conflict to show the newer version, got %+v", current)
	}
	// A hand edit of this device's own version outdates the answer just the same.
	a.rewrite("2026-08-10", dayEntry("2026-08-10", "The desk's version, touched up.", "Handled.", "Start before nine."))
	if _, err := a.journal.ResolveSyncConflict(current.ID, SyncResolution{Keep: current.Theirs[0].Revision}); !errors.As(err, &stale) {
		t.Fatalf("expected an answer to a version edited since to be refused, got %v", err)
	}
	if got := a.artifacts()[entryArtifact("2026-08-10")]; !strings.Contains(got, "touched up") {
		t.Fatalf("the refused resolution replaced the hand edit: %q", got)
	}

	current = a.conflicts()[0]
	a.resolve(current.ID, SyncResolution{Keep: current.Theirs[0].Revision})
	settle(t, a, b)
	requireSameArtifacts(t, a, b)
	requireNoConflicts(t, a, b)
	if got := b.artifacts()[entryArtifact("2026-08-10")]; !strings.Contains(got, "second thought") {
		t.Fatalf("expected the version chosen in the end, got %q", got)
	}
}

func TestResolutionsMadeOnBothDevicesAtOnceAreNeitherLostNorCrossed(t *testing.T) {
	t.Parallel()

	a, b := conflicted(t)
	// Each device keeps its own version, without seeing the other's answer.
	mine, yours := a.conflicts()[0], b.conflicts()[0]
	a.resolve(mine.ID, SyncResolution{Keep: mine.Ours.Revision})
	b.resolve(yours.ID, SyncResolution{Keep: yours.Ours.Revision})
	settle(t, a, b)

	if left, right := conflictSet(a), conflictSet(b); len(left) != 1 || !equalStrings(left, right) {
		t.Fatalf("expected two answers that disagree to be a conflict again, got %v and %v", left, right)
	}
	for _, p := range []*peer{a, b} {
		if got := p.artifacts()[entryArtifact("2026-08-10")]; !strings.Contains(got, "The "+p.label+"'s version.") {
			t.Fatalf("%s: its own answer was replaced by the other's: %q", p.label, got)
		}
	}

	again := b.conflicts()[0]
	b.resolve(again.ID, SyncResolution{Keep: again.Theirs[0].Revision})
	settle(t, a, b)
	requireSameArtifacts(t, a, b)
	requireNoConflicts(t, a, b)
}

func TestADeletionBesideAnEditIsAConflictEitherSideCanWin(t *testing.T) {
	t.Parallel()

	for _, keep := range []string{"the edit", "the deletion"} {
		t.Run("keeping "+keep, func(t *testing.T) {
			t.Parallel()

			a, b := pair(t)
			original := a.artifacts()[entryArtifact("2026-08-10")]
			a.rewrite("2026-08-10", "")
			edited := NewReflectionEntry(dayEntry("2026-08-10", "Noticed, and thought again.", "Handled.", "Start before nine.")).Raw
			b.rewrite("2026-08-10", edited)
			settle(t, a, b)

			desk, phone := a.conflicts(), b.conflicts()
			if len(desk) != 1 || len(phone) != 1 || desk[0].ID != phone[0].ID {
				t.Fatalf("expected one conflict held by both, got %+v and %+v", desk, phone)
			}
			if !desk[0].Ours.Deleted() || desk[0].Theirs[0].Content != edited || desk[0].Base.Content != original {
				t.Fatalf("the desk does not hold the edit beside its deletion: %+v", desk[0])
			}
			if phone[0].Ours.Content != edited || !phone[0].Theirs[0].Deleted() || phone[0].Base.Content != original {
				t.Fatalf("the phone does not hold the deletion beside its edit: %+v", phone[0])
			}
			if _, still := a.artifacts()[entryArtifact("2026-08-10")]; still {
				t.Fatal("the import put back a day its device deleted")
			}
			if b.artifacts()[entryArtifact("2026-08-10")] != edited {
				t.Fatal("the import deleted a day its device edited")
			}

			if keep == "the edit" {
				a.resolve(desk[0].ID, SyncResolution{Keep: desk[0].Theirs[0].Revision})
			} else {
				b.resolve(phone[0].ID, SyncResolution{Keep: RevisionAbsent})
			}
			settle(t, a, b)
			requireSameArtifacts(t, a, b)
			requireNoConflicts(t, a, b)
			got, present := a.artifacts()[entryArtifact("2026-08-10")]
			if (keep == "the edit") != present || (present && got != edited) {
				t.Fatalf("expected %s to win on both devices, got %q", keep, got)
			}
			// Whatever won, the other is still recoverable.
			for _, p := range []*peer{a, b} {
				if p.retained(RevisionOf([]byte(edited))) != edited || p.retained(RevisionOf([]byte(original))) != original {
					t.Fatalf("%s no longer holds the bytes that lost", p.label)
				}
			}
		})
	}
}

func TestADayWrittenOnBothDevicesIsOneOnlyWhenEquivalent(t *testing.T) {
	t.Parallel()

	stamped := func(created, notice string) string {
		return "## 2026-08-14\n<!-- seneca:entry v=2 created=" + created + " -->\n\n### Notice?\n" + notice
	}

	a, b := pair(t)
	// The same words, saved a few minutes apart.
	a.rewrite("2026-08-14", stamped("2026-08-14T21:00:00Z", "The same words."))
	b.rewrite("2026-08-14", stamped("2026-08-14T21:04:10Z", "The same words."))
	// Different words for another day.
	a.rewrite("2026-08-15", dayEntry("2026-08-15", "Written at the desk.", "", ""))
	b.rewrite("2026-08-15", dayEntry("2026-08-15", "Written on the phone.", "", ""))
	settle(t, a, b)

	if got := a.artifacts()[entryArtifact("2026-08-14")]; got != b.artifacts()[entryArtifact("2026-08-14")] || !strings.Contains(got, "The same words.") {
		t.Fatalf("the equivalent day did not become one: %q against %q", got, b.artifacts()[entryArtifact("2026-08-14")])
	}
	left, right := a.conflicts(), b.conflicts()
	if len(left) != 1 || len(right) != 1 || left[0].ID != right[0].ID || left[0].Artifact != entryArtifact("2026-08-15") {
		t.Fatalf("expected only the day that differs to need an answer, got %+v and %+v", left, right)
	}
	if !left[0].Base.Deleted() || !strings.Contains(left[0].Ours.Content, "desk") || !strings.Contains(left[0].Theirs[0].Content, "phone") {
		t.Fatalf("the conflict does not hold both independent entries: %+v", left[0])
	}
	for _, p := range []*peer{a, b} {
		dates := map[string]int{}
		for _, entry := range ParseReflectionDocument(readFileString(t, p.reflections())).Entries {
			dates[entry.Date]++
		}
		for date, count := range dates {
			if count != 1 {
				t.Fatalf("%s holds %d entries for %s", p.label, count, date)
			}
		}
	}

	merged := NewReflectionEntry(dayEntry("2026-08-15", "Written at the desk, and on the phone.", "", "")).Raw
	a.resolve(left[0].ID, SyncResolution{Content: merged})
	settle(t, a, b)
	requireSameArtifacts(t, a, b)
	requireNoConflicts(t, a, b)
}

func TestArchiveRecordsAreUnitedInAnOrderHistoryGives(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	genesis := a.recordIDs()

	// Each device adds records without seeing the other's.
	if _, err := a.journal.SetFocus("The desk's next focus."); err != nil {
		t.Fatalf("SetFocus returned error: %v", err)
	}
	b.clock = fixedNow(21)
	b.restart(FS{})
	saved, err := b.journal.SaveReview(ReviewSave{
		Period: ReviewPeriodWeekly, Start: "2026-08-10", End: "2026-08-16", Body: "Reviewed on the phone.", AcceptedFocus: "From the review.",
	})
	if err != nil {
		t.Fatalf("SaveReview returned error: %v", err)
	}
	mine, yours := a.recordIDs(), b.recordIDs()
	settle(t, a, b)

	requireSameArtifacts(t, a, b)
	requireNoConflicts(t, a, b)
	united := a.recordIDs()
	if !equalStrings(united, b.recordIDs()) {
		t.Fatalf("the devices order the archive differently:\n%v\n%v", united, b.recordIDs())
	}
	if len(united) != 4 || united[0] != genesis[0] {
		t.Fatalf("expected the enrolled archive first and every record once, got %v", united)
	}
	// Each device's own records keep the order it wrote them in.
	position := map[string]int{}
	for i, id := range united {
		position[id] = i
	}
	for _, own := range [][]string{mine, yours} {
		for i := 1; i < len(own); i++ {
			if position[own[i-1]] > position[own[i]] {
				t.Fatalf("a device's own records changed order: %v in %v", own, united)
			}
		}
	}
	// The focus a review set still names that review, and follows it.
	archive := ParseReviewArchive(readFileString(t, a.archive()))
	for _, record := range archive.Records {
		if record.Focus != nil && record.Focus.Origin == FocusOriginReview && record.Focus.Source != saved.Review.Review.ID {
			t.Fatalf("the review's focus lost its review: %+v", record.Focus)
		}
		if raw := b.artifacts()[recordArtifact(record.RecordID())]; raw != normalizeRecordRaw(record.Raw) {
			t.Fatalf("record %s does not have the same bytes on both devices", ShortRecordID(record.RecordID()))
		}
	}

	// What is written after both are known goes last, on both.
	if _, err := a.journal.SetFocus("Written after the exchange."); err != nil {
		t.Fatalf("SetFocus returned error: %v", err)
	}
	settle(t, a, b)
	after := b.recordIDs()
	if !equalStrings(after[:4], united) || len(after) != 5 || !equalStrings(after, a.recordIDs()) {
		t.Fatalf("a later record did not go last on both devices: %v after %v", after, united)
	}
}

func TestReviewsSavedOnTwoDevicesStayTwoBranches(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	first, err := a.journal.SaveReview(ReviewSave{Period: ReviewPeriodWeekly, Start: "2026-08-10", End: "2026-08-16", Body: "The first pass."})
	if err != nil {
		t.Fatalf("SaveReview returned error: %v", err)
	}
	settle(t, a, b)

	// Both revise the same review without seeing each other's revision.
	for _, p := range []*peer{a, b} {
		if _, err := p.journal.SaveReview(ReviewSave{Period: ReviewPeriodWeekly, Start: "2026-08-10", End: "2026-08-16", Body: "Revised on the " + p.label + "."}); err != nil {
			t.Fatalf("%s: SaveReview returned error: %v", p.label, err)
		}
	}
	settle(t, a, b)

	requireSameArtifacts(t, a, b)
	requireNoConflicts(t, a, b)
	if !equalStrings(a.recordIDs(), b.recordIDs()) {
		t.Fatalf("the devices order the archive differently:\n%v\n%v", a.recordIDs(), b.recordIDs())
	}
	reviews, err := a.journal.Reviews()
	if err != nil || len(reviews) != 3 {
		t.Fatalf("expected the review and both revisions, got %d reviews and %v", len(reviews), err)
	}
	branches := 0
	for _, review := range reviews {
		if review.ID == first.Review.Review.ID {
			continue
		}
		// Each revision still names the review it was made from, and neither
		// was rewritten to follow the other.
		if review.Revises != first.Review.Review.ID {
			t.Fatalf("a revision was given a relationship it was not saved with: %+v", review)
		}
		branches++
	}
	if branches != 2 {
		t.Fatalf("expected two branches of the review, got %d", branches)
	}
}

func TestFocusChangedOnTwoDevicesForOneDayIsDisputedUntilSettled(t *testing.T) {
	t.Parallel()

	a, b := pair(t)
	changed, err := a.journal.SetFocus("The desk's focus.")
	if err != nil {
		t.Fatalf("SetFocus returned error: %v", err)
	}
	// Two changes for one day on one device are a sequence, not a dispute.
	if state := a.focus("2026-08-20"); !state.Active || state.Disputed || state.Text != "The desk's focus." {
		t.Fatalf("expected the later of two changes on one device to be in effect, got %+v", state)
	}
	if _, err := b.journal.SetFocus("The phone's focus."); err != nil {
		t.Fatalf("SetFocus returned error: %v", err)
	}
	settle(t, a, b)

	requireSameArtifacts(t, a, b)
	for _, p := range []*peer{a, b} {
		state := p.focus("2026-08-21")
		if state.Active || state.Text != "" || !state.Disputed || state.Effective != "2026-08-20" || len(state.Candidates) != 2 {
			t.Fatalf("%s: expected the focus to be reported as disputed, got %+v", p.label, state)
		}
		texts := state.Candidates[0].Text + " / " + state.Candidates[1].Text
		if !strings.Contains(texts, "desk") || !strings.Contains(texts, "phone") {
			t.Fatalf("%s: the dispute does not show both changes: %q", p.label, texts)
		}
		if got := p.status().DisputedFocus; len(got) != 1 || got[0] != "2026-08-20" {
			t.Fatalf("%s: the status does not name the disputed day: %v", p.label, got)
		}
		// The day before was never in question.
		if before := p.focus("2026-08-19"); before.Active || before.Disputed {
			t.Fatalf("%s: a day before the dispute is affected: %+v", p.label, before)
		}
	}
	left, right := a.disputes(), b.disputes()
	if len(left) != 1 || len(right) != 1 || left[0].ID != right[0].ID {
		t.Fatalf("expected both devices to hold the same dispute, got %+v and %+v", left, right)
	}

	// The phone settles it, a little later, by keeping what the desk said.
	b.clock = func() time.Time { return archiveTime(20, 22, 30) }
	b.restart(FS{})
	settled, err := b.journal.ResolveFocusDispute(right[0].ID, FocusResolution{Record: changed.Record.Focus.ID})
	if err != nil {
		t.Fatalf("ResolveFocusDispute returned error: %v", err)
	}
	if settled.Effective != "2026-08-20" || settled.Text != "The desk's focus." {
		t.Fatalf("unexpected settlement: %+v", settled)
	}
	var stale *StaleConflictError
	if _, err := b.journal.ResolveFocusDispute(right[0].ID, FocusResolution{Text: "Too late."}); !errors.As(err, &stale) {
		t.Fatalf("expected an answer to a settled dispute to be refused, got %v", err)
	}
	settle(t, a, b)

	requireSameArtifacts(t, a, b)
	for _, p := range []*peer{a, b} {
		if state := p.focus("2026-08-21"); !state.Active || state.Disputed || state.Text != "The desk's focus." {
			t.Fatalf("%s: expected the settled focus, got %+v", p.label, state)
		}
		if len(p.disputes()) != 0 || len(p.recordIDs()) != 4 {
			t.Fatalf("%s: expected no dispute and every focus change kept, got %v and %v", p.label, p.disputes(), p.recordIDs())
		}
	}
}

// week is a pair whose journal holds six days, enough for losing most of them
// to be told from deleting a few.
func week(t *testing.T) (a, b *peer) {
	t.Helper()

	a = newPeer(t, "desk", 1)
	var entries []string
	for day := 10; day <= 15; day++ {
		entries = append(entries, dayEntry(fmt.Sprintf("2026-08-%d", day), fmt.Sprintf("Day %d.", day), "", ""))
	}
	a.seedEntries(entries...)
	a.enroll()
	a.publish()

	b = newPeer(t, "phone", 2)
	carry(t, a, b, nil)
	b.join()
	requireNoDiagnostics(t, b.pull())
	return a, b
}

func TestAFileThatLostMostOfItsEntriesIsRecoveredNotPublished(t *testing.T) {
	t.Parallel()

	for _, action := range []SyncRecovery{SyncRecoverRestore, SyncRecoverAcceptDeletions} {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()

			a, b := week(t)
			if _, err := b.journal.RecoverSyncFile("reflections", action); !errors.Is(err, ErrNothingToRecover) {
				t.Fatalf("expected a healthy file to need no recovery, got %v", err)
			}

			// Something truncates the phone's file to its first day.
			b.seedEntries(dayEntry("2026-08-10", "Day 10.", "", ""))
			imported := b.pull()
			if !hasDiagnostic(imported, SyncDiagMassDeletion) || len(imported.Recorded) != 0 {
				t.Fatalf("expected the loss to be reported and nothing recorded, got %+v", imported)
			}
			if b.status().Suspended["reflections"] != syncSuspendedMassDeletion || len(b.publish().Transactions) != 0 {
				t.Fatalf("the loss was published or the file is still being read: %+v", b.status())
			}
			a.write("2026-08-16", "Desk day.", "", "")
			settle(t, a, b)
			if got := a.artifacts(); len(got) != 8 {
				t.Fatalf("the desk lost days to the phone's damaged file: %q", got)
			}

			recovered, err := b.journal.RecoverSyncFile("reflections", action)
			if err != nil {
				t.Fatalf("RecoverSyncFile returned error: %v", err)
			}
			settle(t, a, b)
			requireSameArtifacts(t, a, b)
			if status := b.status(); len(status.Suspended) != 0 || len(status.Pending) != 0 {
				t.Fatalf("expected the file to be read again, got %+v", status)
			}
			days := len(a.artifacts()) - 1
			switch action {
			case SyncRecoverRestore:
				if len(recovered.Restored) != 5 || len(recovered.Deleted) != 0 || days != 7 {
					t.Fatalf("expected five days restored and none lost, got %+v and %d days", recovered, days)
				}
			default:
				if len(recovered.Deleted) != 5 || len(recovered.Restored) != 0 || days != 2 {
					t.Fatalf("expected five days deleted on the author's word, got %+v and %d days", recovered, days)
				}
			}
		})
	}
}

func TestMissingAndCorruptFilesAreRestoredFromHistory(t *testing.T) {
	t.Parallel()

	t.Run("missing", func(t *testing.T) {
		t.Parallel()

		a, b := pair(t)
		for _, path := range []string{b.reflections(), b.archive()} {
			if err := os.Remove(path); err != nil {
				t.Fatalf("removing %s: %v", path, err)
			}
		}
		b.pull()
		for _, file := range []string{"reflections", "archive"} {
			recovered, err := b.journal.RecoverSyncFile(file, SyncRecoverRestore)
			if err != nil || len(recovered.Restored) == 0 || len(recovered.Written) != 1 {
				t.Fatalf("restoring the %s file: %+v, %v", file, recovered, err)
			}
		}
		requireSameArtifacts(t, a, b)
		if !equalStrings(a.recordIDs(), b.recordIDs()) {
			t.Fatal("the restored archive is in another order")
		}
		if status := b.status(); len(status.Suspended) != 0 || b.status().Sequence != 0 {
			t.Fatalf("restoring recorded a change or left a file suspended: %+v", status)
		}
		settle(t, a, b)
		requireSameArtifacts(t, a, b)
	})

	t.Run("corrupt", func(t *testing.T) {
		t.Parallel()

		a, b := pair(t)
		broken := dayEntry("2026-08-10", "One.", "", "") + EntryTerminator + dayEntry("2026-08-10", "Two.", "", "") + EntryTerminator
		b.overwrite(broken)
		if imported := b.pull(); !hasDiagnostic(imported, SyncDiagLocalInvalid) {
			t.Fatalf("expected the corrupt file to be reported, got %+v", imported)
		}
		if _, err := b.journal.RecoverSyncFile("reflections", SyncRecoverAcceptDeletions); err == nil {
			t.Fatal("expected deletions not to be read out of a file that cannot be read")
		}
		if readFileString(t, b.reflections()) != broken {
			t.Fatal("a refused recovery rewrote the file")
		}

		if _, err := b.journal.RecoverSyncFile("reflections", SyncRecoverRestore); err != nil {
			t.Fatalf("RecoverSyncFile returned error: %v", err)
		}
		requireSameArtifacts(t, a, b)
		// What the file held is not thrown away.
		if kept := readFileString(t, b.reflections()+".bak"); kept != broken {
			t.Fatalf("the corrupt file's bytes were not kept: %q", kept)
		}
		settle(t, a, b)
		requireSameArtifacts(t, a, b)
		if got := len(a.artifacts()); got != 4 {
			t.Fatalf("recovering published a change: %d artifacts", got)
		}
	})
}
