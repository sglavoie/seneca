package cmd

import (
	"time"

	"github.com/sglavoie/seneca/engine"
)

// The commands are the desktop adapter around the engine: they resolve paths
// from the environment, read the clock once, hand both to an engine.Journal,
// print what it reports, and commit to git afterwards.

// journalFS is the filesystem every command hands to the engine. It is real in
// production; tests swap it through withJournalFS to force a failure at one
// stage of a write, so a test that installs one must not call t.Parallel.
var journalFS = engine.DefaultFS()

// fixedClock pins a journal to the instant its command started, so the date an
// entry is written under and the date a report mentions can never straddle
// midnight.
func fixedClock(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

// openJournal binds the engine to a resolved reflections file and the archive
// beside it.
func openJournal(reflectionsPath string, settings engine.ReflectionSettings, now time.Time) (*engine.Journal, error) {
	return engine.Open(engine.Options{
		ReflectionsPath: reflectionsPath,
		Settings:        settings,
		Clock:           fixedClock(now),
		FS:              journalFS,
	})
}

// openArchiveJournal binds the engine to a review archive alone, for the
// commands that never touch the reflections file.
func openArchiveJournal(archivePath string, now time.Time) (*engine.Journal, error) {
	return engine.Open(engine.Options{
		ArchivePath: archivePath,
		Clock:       fixedClock(now),
		FS:          journalFS,
	})
}
