package engine

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A grouped commit changes Markdown and the metadata that describes it as one
// unit. These tests stop it at every boundary — by a crash and by a failing
// filesystem call — and hold what is left to one rule: every file is as it was
// before the commit, or every file is as the commit meant it.

// groupFixture is a journal with an archive, existing settings, and room for a
// sync record and an import receipt.
type groupFixture struct {
	dir         string
	reflections string
	archive     string
	settings    string
	syncRecord  string
	receipt     string
}

const (
	oldSettings = `{"questions":["Notice?","Handle?","Carry?"]}` + "\n"
	newSettings = `{"questions":["Notice?","Handle?","Carry?","Thank?"]}` + "\n"
	newSync     = `{"transaction":"t-1","entries":["2026-08-20"]}` + "\n"
	newReceipt  = `{"imported":"t-0"}` + "\n"
)

func newGroupFixture(t *testing.T) groupFixture {
	t.Helper()

	dir := t.TempDir()
	meta := JournalMetadataDirFor(filepath.Join(dir, "reflections.md"))
	g := groupFixture{
		dir:         dir,
		reflections: filepath.Join(dir, "reflections.md"),
		archive:     filepath.Join(dir, ArchiveFileName),
		settings:    filepath.Join(meta, "settings.json"),
		syncRecord:  filepath.Join(meta, "sync", "t-1.json"),
		receipt:     filepath.Join(meta, "receipts", "t-0.json"),
	}
	seed := dayEntry("2026-08-18", "Noticed.", "Handled.", "Start before nine.") + EntryTerminator +
		dayEntry("2026-08-19", "Again.", "", "Keep going.") + EntryTerminator
	if err := os.WriteFile(g.reflections, []byte(seed), 0644); err != nil {
		t.Fatalf("seeding the journal: %v", err)
	}
	mustAppend(t, g.archive, NewFocusSetRecord(archiveTime(17, 9, 0), FocusOriginManual, "", "The first focus."))
	if err := os.MkdirAll(filepath.Dir(g.settings), 0755); err != nil {
		t.Fatalf("seeding the metadata directory: %v", err)
	}
	if err := os.WriteFile(g.settings, []byte(oldSettings), 0644); err != nil {
		t.Fatalf("seeding the settings: %v", err)
	}
	return g
}

func (g groupFixture) journal(t *testing.T, seams FS) *Journal {
	t.Helper()

	journal, err := Open(Options{
		ReflectionsPath: g.reflections,
		Settings:        journalSettings,
		Clock:           fixedNow(20),
		FS:              seams,
		LockWait:        5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	return journal
}

// commit writes a new day, a focus change, new settings, a sync record, and an
// import receipt as one commit.
func (g groupFixture) commit(journal *Journal) error {
	return journal.Commit(func(c *Commit) error {
		reflections, err := c.Reflections()
		if err != nil {
			return err
		}
		if err := reflections.Validate(); err != nil {
			return err
		}
		added := NewReflectionEntry(dayEntry("2026-08-20", "Grouped.", "", "Carried."))
		if err := reflections.CommitEntries(append(reflections.Entries(), added)); err != nil {
			return err
		}

		archive, err := c.Archive()
		if err != nil {
			return err
		}
		if err := archive.AppendRecords(NewFocusSetRecord(archiveTime(20, 21, 0), FocusOriginManual, "", "The grouped focus.")); err != nil {
			return err
		}

		for _, artifact := range []struct{ path, body string }{
			{g.settings, newSettings},
			{g.syncRecord, newSync},
			{g.receipt, newReceipt},
		} {
			tx, err := c.Metadata(artifact.path)
			if err != nil {
				return err
			}
			if err := tx.Commit([]byte(artifact.body)); err != nil {
				return err
			}
		}
		return nil
	})
}

// tree reads every file under the fixture except the `.bak` siblings, keyed by
// its path relative to the journal directory. Anything a commit leaves behind —
// staged contents, a record, a lock — shows up in it.
func (g groupFixture) tree(t *testing.T) map[string]string {
	t.Helper()

	found := map[string]string{}
	err := filepath.WalkDir(g.dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || strings.HasSuffix(path, BackupSuffix) {
			return err
		}
		rel, err := filepath.Rel(g.dir, path)
		if err != nil {
			return err
		}
		found[filepath.ToSlash(rel)] = readFileString(t, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", g.dir, err)
	}
	return found
}

func sameTree(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for name, content := range a {
		if other, ok := b[name]; !ok || other != content {
			return false
		}
	}
	return true
}

func treeNames(tree map[string]string) []string {
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	return names
}

// groupStates returns the two coherent states of the fixture: untouched, and
// after a commit nothing interrupted.
func groupStates(t *testing.T) (before, after map[string]string) {
	t.Helper()

	g := newGroupFixture(t)
	before = g.tree(t)
	if err := g.commit(g.journal(t, FS{})); err != nil {
		t.Fatalf("the uninterrupted commit returned error: %v", err)
	}
	after = g.tree(t)
	if sameTree(before, after) {
		t.Fatal("the fixture commit changed nothing")
	}
	return before, after
}

// settle is what the next Seneca process does: read, which completes a
// recorded commit, then commit, which sweeps whatever an unrecorded one staged.
func (g groupFixture) settle(t *testing.T) {
	t.Helper()

	survivor := g.journal(t, FS{})
	if _, err := survivor.Entries(); err != nil {
		t.Fatalf("reading after the interruption returned error: %v", err)
	}
	if err := survivor.Commit(func(*Commit) error { return nil }); err != nil {
		t.Fatalf("the next commit returned error: %v", err)
	}
	validation, err := survivor.Validate()
	if err != nil || !validation.Writable() {
		t.Fatalf("expected a journal that still accepts writes, got %+v, %v", validation, err)
	}
}

func TestGroupedCommitWritesEveryFileAndLeavesNoScaffolding(t *testing.T) {
	t.Parallel()

	g := newGroupFixture(t)
	oldReflections := readFileString(t, g.reflections)
	oldArchive := readFileString(t, g.archive)

	if err := g.commit(g.journal(t, FS{})); err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}

	if got := readFileString(t, g.reflections); !strings.Contains(got, "## 2026-08-20") || !strings.HasPrefix(got, "## 2026-08-18") {
		t.Fatalf("expected the new day after the existing ones, got:\n%s", got)
	}
	if got := readFileString(t, g.archive); !strings.Contains(got, "The grouped focus.") || !strings.Contains(got, "The first focus.") {
		t.Fatalf("expected both focus records, got:\n%s", got)
	}
	for path, want := range map[string]string{g.settings: newSettings, g.syncRecord: newSync, g.receipt: newReceipt} {
		if got := readFileString(t, path); got != want {
			t.Fatalf("%s holds %q, want %q", path, got, want)
		}
	}

	// The Markdown keeps its backups; Seneca's own metadata gets none.
	if got := readFileString(t, g.reflections+BackupSuffix); got != oldReflections {
		t.Fatalf("expected the replaced journal in its backup, got:\n%s", got)
	}
	if got := readFileString(t, g.archive+BackupSuffix); got != oldArchive {
		t.Fatalf("expected the replaced archive in its backup, got:\n%s", got)
	}
	for _, path := range []string{g.settings, g.syncRecord, g.receipt} {
		if _, err := os.Stat(path + BackupSuffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected no backup beside %s, got %v", path, err)
		}
	}

	for _, gone := range []string{CommitDirFor(g.dir), LockPathFor(g.dir), DirectoryMetadataDirFor(g.reflections)} {
		if _, err := os.Stat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected %s to be gone after the commit, got %v", gone, err)
		}
	}
}

func TestCrashAtEveryCommitBoundaryRecoversToACoherentState(t *testing.T) {
	t.Parallel()

	before, after := groupStates(t)
	crash := errors.New("the process died")

	boundaries := []struct {
		stage    CommitStage
		applied  int
		lands    bool
		midState bool
	}{
		{stage: CommitStaged},
		{stage: CommitBackedUp},
		{stage: CommitRecorded, lands: true},
		{stage: CommitApplied, applied: 1, lands: true, midState: true},
		{stage: CommitApplied, applied: 2, lands: true, midState: true},
		{stage: CommitApplied, applied: 3, lands: true, midState: true},
		{stage: CommitApplied, applied: 4, lands: true, midState: true},
		{stage: CommitApplied, applied: 5, lands: true},
		{stage: CommitCleared, applied: 5, lands: true},
	}
	for _, boundary := range boundaries {
		t.Run(string(boundary.stage)+"-"+string(rune('0'+boundary.applied)), func(t *testing.T) {
			t.Parallel()

			g := newGroupFixture(t)
			dying := FS{CommitStage: func(stage CommitStage, applied int) error {
				if stage == boundary.stage && applied == boundary.applied {
					return crash
				}
				return nil
			}}
			if err := g.commit(g.journal(t, dying)); !errors.Is(err, crash) {
				t.Fatalf("expected the commit to stop at the boundary, got %v", err)
			}

			if boundary.midState {
				// The boundary is real: the first file is new and the last
				// has not been written.
				if got := readFileString(t, g.reflections); got != after["reflections.md"] {
					t.Fatal("expected the first file to be replaced before the crash")
				}
				if _, err := os.Stat(g.receipt); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("expected the last file not to be written before the crash, got %v", err)
				}
			}

			g.settle(t)

			want, name := before, "untouched"
			if boundary.lands {
				want, name = after, "fully written"
			}
			if got := g.tree(t); !sameTree(got, want) {
				t.Fatalf("expected the journal %s after recovery, found files %v", name, treeNames(got))
			}
		})
	}
}

func TestCrashDuringRecoveryIsRecoveredByTheNextRead(t *testing.T) {
	t.Parallel()

	_, after := groupStates(t)
	crash := errors.New("the process died")
	g := newGroupFixture(t)

	dieAt := func(stage CommitStage, applied int) FS {
		return FS{CommitStage: func(s CommitStage, n int) error {
			if s == stage && n == applied {
				return crash
			}
			return nil
		}}
	}
	if err := g.commit(g.journal(t, dieAt(CommitRecorded, 0))); !errors.Is(err, crash) {
		t.Fatalf("expected the commit to stop at its commit point, got %v", err)
	}
	// The first process to recover dies too, two files in.
	if _, err := g.journal(t, dieAt(CommitApplied, 2)).Entries(); !errors.Is(err, crash) {
		t.Fatalf("expected recovery to stop at the boundary, got %v", err)
	}

	g.settle(t)
	if got := g.tree(t); !sameTree(got, after) {
		t.Fatalf("expected the journal fully written after the second recovery, found files %v", treeNames(got))
	}
}

// failNth returns a counter that fails on its nth call only, and reports how
// many calls it saw.
func failNth(n int) (fail func() error, calls *int) {
	calls = new(int)
	return func() error {
		*calls++
		if *calls == n {
			return injectedFailure
		}
		return nil
	}, calls
}

func TestAFailureAtEveryFilesystemCallLeavesACoherentState(t *testing.T) {
	t.Parallel()

	before, after := groupStates(t)

	seams := map[string]func(fail func() error) FS{
		"write": func(fail func() error) FS {
			return FS{WriteFile: func(f *os.File, data []byte) (int, error) {
				if err := fail(); err != nil {
					return 0, err
				}
				return f.Write(data)
			}}
		},
		"sync": func(fail func() error) FS {
			return FS{SyncFile: func(f *os.File) error {
				if err := fail(); err != nil {
					return err
				}
				return f.Sync()
			}}
		},
		"close": func(fail func() error) FS {
			return FS{CloseFile: func(f *os.File) error {
				if err := fail(); err != nil {
					_ = f.Close()
					return err
				}
				return f.Close()
			}}
		},
		"rename": func(fail func() error) FS {
			return FS{Rename: func(oldpath, newpath string) error {
				if err := fail(); err != nil {
					return err
				}
				return os.Rename(oldpath, newpath)
			}}
		},
		"syncDir": func(fail func() error) FS {
			return FS{SyncDir: func(dir *os.File) error {
				if err := fail(); err != nil {
					return err
				}
				return dir.Sync()
			}}
		},
	}

	for name, build := range seams {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			landed, refused := 0, 0
			for n := 1; ; n++ {
				if n > 200 {
					t.Fatal("the commit never ran out of filesystem calls to fail")
				}
				g := newGroupFixture(t)
				fail, calls := failNth(n)
				err := g.commit(g.journal(t, build(fail)))
				if *calls < n {
					// Every call the commit makes has now been failed once.
					if err != nil {
						t.Fatalf("the uninterrupted commit returned error: %v", err)
					}
					break
				}
				if err == nil {
					t.Fatalf("call %d failed and the commit reported success", n)
				}

				if WriteCommitted(err) {
					// Reported as landed: after the next process has had its
					// turn, every file must be new.
					landed++
					g.settle(t)
					if got := g.tree(t); !sameTree(got, after) {
						t.Fatalf("call %d: reported as written (%v) but found files %v", n, err, treeNames(got))
					}
					continue
				}
				// Reported as failed: nothing may have changed, right now,
				// with no recovery to help.
				refused++
				if got := g.tree(t); !sameTree(got, before) {
					t.Fatalf("call %d: reported as failed (%v) but found files %v", n, err, treeNames(got))
				}
				if leftovers := tempFileNames(t, g.dir); len(leftovers) != 0 {
					t.Fatalf("call %d: temporary files left behind: %v", n, leftovers)
				}
			}
			if refused == 0 {
				t.Fatal("expected a failure before the commit point to be reported as write-free")
			}
			if (name == "rename" || name == "syncDir") && landed == 0 {
				t.Fatalf("expected a %s failure after the commit point to be reported as written", name)
			}
		})
	}
}

func TestAFailedBackupAbandonsTheWholeGroup(t *testing.T) {
	t.Parallel()

	before, _ := groupStates(t)
	g := newGroupFixture(t)
	failing := FS{Backup: func(path string, data []byte, mode os.FileMode) error {
		if path == g.archive {
			return injectedFailure
		}
		return FS{}.resolved().Backup(path, data, mode)
	}}

	err := g.commit(g.journal(t, failing))
	if !errors.Is(err, injectedFailure) || WriteCommitted(err) {
		t.Fatalf("expected a write-free backup failure, got %v", err)
	}
	if got := g.tree(t); !sameTree(got, before) {
		t.Fatalf("expected every file untouched, found files %v", treeNames(got))
	}
}

func TestRecoveryRefusesAFileSomethingElseChanged(t *testing.T) {
	t.Parallel()

	crash := errors.New("the process died")
	g := newGroupFixture(t)
	dying := FS{CommitStage: func(stage CommitStage, applied int) error {
		if stage == CommitApplied && applied == 1 {
			return crash
		}
		return nil
	}}
	if err := g.commit(g.journal(t, dying)); !errors.Is(err, crash) {
		t.Fatalf("expected the commit to stop after its first file, got %v", err)
	}

	// An editor saves the archive before any Seneca process runs again.
	edited := readFileString(t, g.archive) + "\nA note typed by hand.\n"
	if err := os.WriteFile(g.archive, []byte(edited), 0644); err != nil {
		t.Fatalf("editing the archive: %v", err)
	}

	survivor := g.journal(t, FS{})
	var refusal *RecoveryError
	if _, err := survivor.Entries(); !errors.As(err, &refusal) {
		t.Fatalf("expected a read to refuse the unfinished commit, got %v", err)
	}
	if _, err := survivor.SetFocus("Anything."); !errors.As(err, &refusal) {
		t.Fatalf("expected a write to refuse the unfinished commit, got %v", err)
	}
	if refusal.Dir != CommitDirFor(g.dir) {
		t.Fatalf("expected the refusal to name the commit directory, got %s", refusal.Dir)
	}
	if got := readFileString(t, g.archive); got != edited {
		t.Fatalf("expected the hand edit left alone, got:\n%s", got)
	}
	if got := readFileString(t, g.settings); got != oldSettings {
		t.Fatalf("expected recovery to write nothing once it refused, settings hold %q", got)
	}

	// The remedy the refusal names: abandon the interrupted save.
	if err := os.RemoveAll(refusal.Dir); err != nil {
		t.Fatalf("removing the commit directory: %v", err)
	}
	if _, err := survivor.Entries(); err != nil {
		t.Fatalf("expected reads to work once the commit is abandoned, got %v", err)
	}
}

func TestRecoveryHoldsARecordFromANewerSeneca(t *testing.T) {
	t.Parallel()

	g := newGroupFixture(t)
	before := g.tree(t)
	dir := CommitDirFor(g.dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("creating the commit directory: %v", err)
	}
	record := filepath.Join(dir, commitRecordName)
	if err := os.WriteFile(record, []byte(`{"version":2,"files":[]}`), 0600); err != nil {
		t.Fatalf("writing the record: %v", err)
	}

	var refusal *RecoveryError
	if err := g.commit(g.journal(t, FS{})); !errors.As(err, &refusal) {
		t.Fatalf("expected the commit to refuse a newer record, got %v", err)
	}
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("expected the newer record to be kept, got %v", err)
	}
	got := g.tree(t)
	delete(got, filepath.ToSlash(filepath.Join(JournalMetadataRootName, JournalMetadataDirectoryName, commitDirName, commitRecordName)))
	if !sameTree(got, before) {
		t.Fatalf("expected nothing written past a record this binary cannot read, found files %v", treeNames(got))
	}
}

func TestStagedContentsWithNoRecordAreNeverApplied(t *testing.T) {
	t.Parallel()

	g := newGroupFixture(t)
	before := g.tree(t)
	dir := CommitDirFor(g.dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("creating the commit directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "abc"+commitBlobSuffix), []byte("half a journal"), 0600); err != nil {
		t.Fatalf("writing staged contents: %v", err)
	}

	// A read has nothing to recover and takes no lock for litter.
	if _, err := g.journal(t, FS{}).Entries(); err != nil {
		t.Fatalf("Entries returned error: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected a read to leave the commit directory alone, got %v", err)
	}
	// The next commit sweeps it.
	if err := g.journal(t, FS{}).Commit(func(*Commit) error { return nil }); err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	if got := g.tree(t); !sameTree(got, before) {
		t.Fatalf("expected the litter swept and nothing else touched, found files %v", treeNames(got))
	}
}

func TestGroupedCommitValidatesEveryFileBeforeWritingAny(t *testing.T) {
	t.Parallel()

	t.Run("a refused archive", func(t *testing.T) {
		t.Parallel()

		g := newGroupFixture(t)
		// Not an archive: no marker vouches for it, so an append is refused.
		if err := os.WriteFile(g.archive, []byte("# Somebody else's notes\n\nNot Seneca's.\n"), 0644); err != nil {
			t.Fatalf("replacing the archive: %v", err)
		}
		before := g.tree(t)

		if err := g.commit(g.journal(t, FS{})); err == nil || WriteCommitted(err) {
			t.Fatalf("expected the group to be refused write-free, got %v", err)
		}
		if got := g.tree(t); !sameTree(got, before) {
			t.Fatalf("expected the reflections staged before the refusal not to be written, found files %v", treeNames(got))
		}
		if _, err := os.Stat(g.reflections + BackupSuffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected no backup from a refused group, got %v", err)
		}
	})

	t.Run("a symlinked destination", func(t *testing.T) {
		t.Parallel()

		g := newGroupFixture(t)
		elsewhere := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(elsewhere, []byte(oldSettings), 0644); err != nil {
			t.Fatalf("writing the link target: %v", err)
		}
		if err := os.Remove(g.settings); err != nil {
			t.Fatalf("removing the settings: %v", err)
		}
		if err := os.Symlink(elsewhere, g.settings); err != nil {
			t.Fatalf("linking the settings: %v", err)
		}
		oldReflections := readFileString(t, g.reflections)

		var refusal *SymlinkDestinationError
		if err := g.commit(g.journal(t, FS{})); !errors.As(err, &refusal) {
			t.Fatalf("expected the symlink to be refused, got %v", err)
		}
		if got := readFileString(t, g.reflections); got != oldReflections {
			t.Fatal("expected the journal untouched when a file of its group is refused")
		}
		if got := readFileString(t, elsewhere); got != oldSettings {
			t.Fatal("expected the link's target untouched")
		}
	})

	t.Run("a file staged twice", func(t *testing.T) {
		t.Parallel()

		g := newGroupFixture(t)
		before := g.tree(t)
		err := g.journal(t, FS{}).Commit(func(c *Commit) error {
			for range 2 {
				tx, err := c.Metadata(g.settings)
				if err != nil {
					return err
				}
				if err := tx.Commit([]byte(newSettings)); err != nil {
					return err
				}
			}
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "already part of this commit") {
			t.Fatalf("expected the second staging to be refused, got %v", err)
		}
		if got := g.tree(t); !sameTree(got, before) {
			t.Fatalf("expected nothing written, found files %v", treeNames(got))
		}
	})
}

func TestMetadataIsConfinedToTheJournalsOwnDirectory(t *testing.T) {
	t.Parallel()

	g := newGroupFixture(t)
	for name, path := range map[string]string{
		"the journal itself":   g.reflections,
		"outside the journal":  filepath.Join(t.TempDir(), "settings.json"),
		"the metadata root":    filepath.Join(g.dir, JournalMetadataRootName),
		"the commit directory": filepath.Join(CommitDirFor(g.dir), "record.json"),
		"the lock":             LockPathFor(g.dir),
		"a climb back out":     filepath.Join(g.dir, JournalMetadataRootName, "..", "reflections.md"),
	} {
		err := g.journal(t, FS{}).Commit(func(c *Commit) error {
			_, err := c.Metadata(path)
			return err
		})
		if err == nil {
			t.Errorf("expected %s (%s) to be refused as a metadata path", name, path)
		}
	}
}

func TestAStaleMetadataBaseIsRefused(t *testing.T) {
	t.Parallel()

	g := newGroupFixture(t)
	base := RevisionOf([]byte(oldSettings))

	// Another writer replaces the settings after they were read.
	raced := `{"questions":["Changed elsewhere?"]}` + "\n"
	if err := os.WriteFile(g.settings, []byte(raced), 0644); err != nil {
		t.Fatalf("replacing the settings: %v", err)
	}
	oldReflections := readFileString(t, g.reflections)

	err := g.journal(t, FS{}).Commit(func(c *Commit) error {
		reflections, err := c.Reflections()
		if err != nil {
			return err
		}
		if err := reflections.CommitEntries(append(reflections.Entries(), NewReflectionEntry(dayEntry("2026-08-20", "Grouped.", "", "")))); err != nil {
			return err
		}
		settings, err := c.Metadata(g.settings)
		if err != nil {
			return err
		}
		if err := settings.RequireBase(base); err != nil {
			return err
		}
		return settings.Commit([]byte(newSettings))
	})

	var stale *StaleFileError
	if !errors.As(err, &stale) || stale.Path != g.settings || stale.Base != base || stale.Current != RevisionOf([]byte(raced)) {
		t.Fatalf("expected a stale refusal naming both revisions, got %v", err)
	}
	if got := readFileString(t, g.settings); got != raced {
		t.Fatalf("expected the other writer's settings kept, got %q", got)
	}
	if got := readFileString(t, g.reflections); got != oldReflections {
		t.Fatal("expected the journal untouched by a group whose settings were stale")
	}
}

func TestASingleMetadataFileIsWrittenWithoutARecord(t *testing.T) {
	t.Parallel()

	journal := newTestJournal(t, FS{}, 20, dayEntry("2026-08-18", "Noticed.", "", ""))
	dir := filepath.Dir(journal.ReflectionsPath())
	identity := filepath.Join(JournalMetadataDirFor(journal.ReflectionsPath()), "identity.json")

	write := func(body string) error {
		return journal.Commit(func(c *Commit) error {
			tx, err := c.Metadata(identity)
			if err != nil {
				return err
			}
			return tx.Commit([]byte(body))
		})
	}
	if err := write("one\n"); err != nil {
		t.Fatalf("the first write returned error: %v", err)
	}
	if err := write("two\n"); err != nil {
		t.Fatalf("the second write returned error: %v", err)
	}
	if got := readFileString(t, identity); got != "two\n" {
		t.Fatalf("expected the second contents, got %q", got)
	}
	if _, err := os.Stat(identity + BackupSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no backup for metadata, got %v", err)
	}
	if _, err := os.Stat(DirectoryMetadataDirFor(journal.ReflectionsPath())); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no lock or commit directory left in %s, got %v", dir, err)
	}

	// A metadata write that fails takes the directory it created with it.
	failing, err := Open(Options{
		ReflectionsPath: journal.ReflectionsPath(),
		Clock:           fixedNow(20),
		FS:              FS{Rename: func(string, string) error { return injectedFailure }},
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	receipts := filepath.Join(JournalMetadataDirFor(journal.ReflectionsPath()), "receipts")
	err = failing.Commit(func(c *Commit) error {
		tx, err := c.Metadata(filepath.Join(receipts, "r-1.json"))
		if err != nil {
			return err
		}
		return tx.Commit([]byte("receipt\n"))
	})
	if !errors.Is(err, injectedFailure) {
		t.Fatalf("expected the injected failure, got %v", err)
	}
	if _, err := os.Stat(receipts); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the empty receipts directory removed, got %v", err)
	}
}
