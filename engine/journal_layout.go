package engine

import "path/filepath"

// Journal layout is the map of what lives beside a reflections file. The
// reflections file and the review archive are the author's Markdown; everything
// else Seneca keeps sits under one hidden directory, split by scope:
//
//	<dir>/<reflections-filename>                     the journal
//	<dir>/seneca-reviews.md                          the review archive, one per <dir>
//	<dir>/.seneca/journals/<reflections-filename>/   this journal's private metadata
//	<dir>/.seneca/directory/                         state scoped to <dir> itself
//
// A journal's metadata directory is keyed by the reflections *filename*, so two
// journals configured in one directory — `reflections.md` and `work.md`, say —
// never read or write each other's settings, identity, or sync state. The
// archive is deliberately not scoped the same way: ArchivePathFor has always
// returned one `seneca-reviews.md` per directory, and moving it would orphan
// every existing archive. Anything that must be decided once for the shared
// archive therefore belongs under `directory/`, which no journal filename can
// collide with. docs/mobile/journal-layout.md states what that means for
// journals that share a directory.
//
// Nothing here creates a directory. Like the archive, the metadata is created
// by the first command that has something to store in it, never by a read.

const (
	// JournalMetadataRootName is the one hidden directory Seneca owns inside a
	// journal's directory.
	JournalMetadataRootName = ".seneca"
	// JournalMetadataJournalsName holds one subdirectory per journal filename.
	JournalMetadataJournalsName = "journals"
	// JournalMetadataDirectoryName holds state shared by every journal in the
	// directory, because the review archive is shared by them too.
	JournalMetadataDirectoryName = "directory"
)

// JournalMetadataDirFor derives a journal's private metadata directory from its
// resolved reflections path, so a SENECA_FILE override moves the metadata with
// the journal exactly as it moves the archive.
func JournalMetadataDirFor(reflectionsPath string) string {
	return filepath.Join(filepath.Dir(reflectionsPath), JournalMetadataRootName, JournalMetadataJournalsName, filepath.Base(reflectionsPath))
}

// DirectoryMetadataDirFor derives the metadata directory shared by every
// journal beside reflectionsPath.
func DirectoryMetadataDirFor(reflectionsPath string) string {
	return filepath.Join(filepath.Dir(reflectionsPath), JournalMetadataRootName, JournalMetadataDirectoryName)
}
