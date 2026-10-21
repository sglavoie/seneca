package senecabridge

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/sglavoie/seneca/engine"
)

// Two journals the app's browsing, settings, and conflict screens are tested
// on, beside the round-trip journal. The engine writes both, with fixed
// clocks and fixed identities, so the committed copies are exactly what it
// writes; SENECA_UPDATE_FIXTURES=1 writes them again.
//
//   - browsing: the round-trip journal with two earlier months written, and
//     a quarterly and a yearly review saved, the quarterly one twice, so
//     every review period and a revision of each kind of window are there.
//   - conflicted: the round-trip journal synced between a Mac and the phone,
//     as the phone holds it after both wrote 2026-04-02 differently and both
//     set a different focus that day. exchange/ is what the two published.
const (
	browsingFixture   = "../testdata/browsing"
	conflictedFixture = "../testdata/conflicted"
	conflictedDay     = "2026-04-02"
)

// counter is a deterministic source of identities.
type counter struct{ next byte }

func (c *counter) Read(p []byte) (int, error) {
	for i := range p {
		c.next++
		p[i] = c.next
	}
	return len(p), nil
}

type fixtureJournal struct {
	t       *testing.T
	path    string
	random  *counter
	instant time.Time
}

func newFixtureJournal(t *testing.T, dir string, seed byte, instant string) *fixtureJournal {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"reflections.md", engine.ArchiveFileName} {
		data, err := os.ReadFile(filepath.Join(fixtureDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	j := &fixtureJournal{t: t, path: filepath.Join(dir, "reflections.md"), random: &counter{next: seed}}
	j.at(instant)
	return j
}

// at moves the journal's clock.
func (j *fixtureJournal) at(instant string) *fixtureJournal {
	j.t.Helper()
	parsed, err := time.Parse(time.RFC3339, instant)
	if err != nil {
		j.t.Fatal(err)
	}
	j.instant = parsed
	return j
}

func (j *fixtureJournal) open() *engine.Journal {
	j.t.Helper()
	journal, err := engine.Open(engine.Options{
		ReflectionsPath: j.path,
		Settings:        engine.ReflectionSettings{Questions: fixtureQuestions, AnchorQuestionIndex: fixtureAnchor},
		Clock:           func() time.Time { return j.instant },
		Random:          j.random,
	})
	if err != nil {
		j.t.Fatal(err)
	}
	return journal
}

func (j *fixtureJournal) write(date string, answers ...string) {
	j.t.Helper()
	journal := j.open()
	draft, err := journal.PrepareReflection(date, true)
	if err != nil {
		j.t.Fatalf("preparing %s: %v", date, err)
	}
	if _, err := journal.SaveReflection(draft, engine.ReflectionResult{Saved: true, Answers: answers}, date != journal.Today()); err != nil {
		j.t.Fatalf("saving %s: %v", date, err)
	}
}

func (j *fixtureJournal) review(period engine.ReviewPeriod, start, end, title, body string) {
	j.t.Helper()
	if _, err := j.open().SaveReview(engine.ReviewSave{Period: period, Start: start, End: end, Title: title, Body: body}); err != nil {
		j.t.Fatalf("saving the %s review: %v", period, err)
	}
}

func (j *fixtureJournal) focus(text string) {
	j.t.Helper()
	if _, err := j.open().SetFocus(text); err != nil {
		j.t.Fatalf("setting the focus: %v", err)
	}
}

func writeBrowsingFixture(t *testing.T, root string) {
	j := newFixtureJournal(t, filepath.Join(root, "journal"), 0, "2026-01-02T22:00:00Z")
	j.write("2026-01-02", "Synthetic: the first cold morning.", "Synthetic: kept the walk.", "Walk again tomorrow.")
	j.at("2026-01-03T09:00:00Z").review(engine.ReviewPeriodYearly, "2025-01-01", "2025-12-31",
		"Yearly review — 2025-01-01 to 2025-12-31", "Synthetic yearly review.")
	j.at("2026-02-02T22:00:00Z").write("2026-02-02", "Synthetic: a quiet Monday.", "Synthetic: said no once.", "Say no once more.")
	j.at("2026-04-01T20:00:00Z").review(engine.ReviewPeriodQuarterly, "2026-01-01", "2026-03-31",
		"Quarterly review — 2026-01-01 to 2026-03-31", "Synthetic quarterly review.")
	j.at("2026-04-02T07:00:00Z").review(engine.ReviewPeriodQuarterly, "2026-01-01", "2026-03-31",
		"Quarterly review — 2026-01-01 to 2026-03-31", "Synthetic quarterly review, revised the next morning.")
}

func writeConflictedFixture(t *testing.T, root string) {
	transport := engine.NewDirectoryTransport(filepath.Join(root, "exchange"))
	mac := newFixtureJournal(t, filepath.Join(t.TempDir(), "mac"), 100, "2026-04-02T12:00:00Z")
	if _, err := mac.open().EnrollSync("mac"); err != nil {
		t.Fatal(err)
	}
	if _, err := mac.open().PublishSync(transport); err != nil {
		t.Fatal(err)
	}

	phone := &fixtureJournal{t: t, path: filepath.Join(root, "journal", "reflections.md"), random: &counter{next: 200}}
	if err := os.MkdirAll(filepath.Dir(phone.path), 0o755); err != nil {
		t.Fatal(err)
	}
	phone.at("2026-04-02T12:00:00Z")
	if _, err := phone.open().JoinSync(transport, "phone"); err != nil {
		t.Fatal(err)
	}
	if _, err := phone.open().ImportSync(transport); err != nil {
		t.Fatal(err)
	}

	// Each writes the same day and sets a focus before hearing from the other.
	mac.at("2026-04-02T21:00:00Z").write(conflictedDay,
		"Synthetic: noticed the light at five, from the Mac.", "Synthetic: closed the laptop on time.", "Read before bed.")
	mac.focus("Leave the desk by six.")
	phone.at("2026-04-02T22:00:00Z").write(conflictedDay,
		"Synthetic: noticed the rain, from the phone.", "Synthetic: walked anyway.", "Pack the umbrella.")
	phone.focus("Walk in any weather.")
	if _, err := mac.open().PublishSync(transport); err != nil {
		t.Fatal(err)
	}
	if _, err := phone.open().PublishSync(transport); err != nil {
		t.Fatal(err)
	}
	imported, err := phone.at("2026-04-02T23:00:00Z").open().ImportSync(transport)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Conflicts) != 1 || imported.Conflicts[0] != "entry:"+conflictedDay {
		t.Fatalf("the phone's import = %+v, want one conflict on %s", imported, conflictedDay)
	}
}

// tree reads every file under root by its path relative to root.
func tree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		// A replaced file's backup is the engine's own safety copy, not
		// part of the journal.
		if err != nil || entry.IsDir() || filepath.Ext(path) == ".bak" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return files
}

func TestTheSessionFixturesAreWhatTheEngineWrites(t *testing.T) {
	for _, fixture := range []struct {
		dir   string
		write func(*testing.T, string)
	}{
		{browsingFixture, writeBrowsingFixture},
		{conflictedFixture, writeConflictedFixture},
	} {
		t.Run(filepath.Base(fixture.dir), func(t *testing.T) {
			generated := t.TempDir()
			fixture.write(t, generated)
			want := tree(t, generated)
			if os.Getenv("SENECA_UPDATE_FIXTURES") == "1" {
				if err := os.RemoveAll(fixture.dir); err != nil {
					t.Fatal(err)
				}
				for rel, data := range want {
					path := filepath.Join(fixture.dir, filepath.FromSlash(rel))
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, data, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			got := tree(t, fixture.dir)
			var names []string
			for rel := range want {
				names = append(names, rel)
			}
			for rel := range got {
				if _, ok := want[rel]; !ok {
					names = append(names, rel)
				}
			}
			sort.Strings(names)
			for _, rel := range names {
				if !bytes.Equal(got[rel], want[rel]) {
					t.Errorf("%s/%s is not what the engine writes; run with SENECA_UPDATE_FIXTURES=1", fixture.dir, rel)
				}
			}
		})
	}
}
