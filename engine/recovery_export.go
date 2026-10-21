package engine

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A recovery export is the author's writing, whole, in a directory of its own:
// the reflections file and the review archive byte for byte, the journal's
// settings, every version of every open conflict, and the writing no save has
// stored yet. It is made only when the author asks for it, because unlike a
// diagnostic it holds everything they wrote.
//
// It holds no sync identity and no sync history. A journal restored from it is
// a journal that never took part in sync: it starts a new synced journal or
// joins one as a device of its own, so no restored copy ever speaks as the
// device it was copied from.
//
// The export appears whole or not at all. It is written into a hidden sibling
// of the destination and renamed into place last; an interrupted export leaves
// no destination, only the hidden directory, which the next export removes.

const (
	RecoveryExportFormat  = "seneca-recovery"
	RecoveryExportVersion = 1
	recoveryManifestName  = "MANIFEST.json"
	recoveryReadmeName    = "README.txt"
	recoveryPartialPrefix = ".partial-"
)

// RecoveryFile is one file of an export.
type RecoveryFile struct {
	// Path is relative to the export, with forward slashes.
	Path   string   `json:"path"`
	Bytes  int      `json:"bytes"`
	Digest Revision `json:"digest"`
	// Kind is reflections, archive, settings, conflict, draft, or review-draft.
	Kind string `json:"kind"`
}

// RecoveryExport describes an export that was written.
type RecoveryExport struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Created string `json:"created"`
	// Journal is the synced journal the writing came from, when it was one.
	// It names the journal, not a device, and is never adopted on restore.
	Journal string         `json:"journal,omitempty"`
	Files   []RecoveryFile `json:"files"`
	// Directory is where the export is. It is not part of the manifest.
	Directory string `json:"-"`
}

// Count returns how many files of a kind the export holds.
func (e RecoveryExport) Count(kind string) int {
	n := 0
	for _, file := range e.Files {
		if file.Kind == kind {
			n++
		}
	}
	return n
}

// ErrRecoveryDestinationExists refuses to export over something already there.
var ErrRecoveryDestinationExists = errors.New("the export destination already exists; choose a new directory")

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// ExportRecovery writes the journal's writing to destination, which must not
// exist; its parent must. draftsDir, when not empty, is a directory of unsaved
// writing the platform keeps itself, copied in as it is. Nothing in the journal
// changes.
func (j *Journal) ExportRecovery(destination, draftsDir string) (RecoveryExport, error) {
	if err := j.requireReflections(); err != nil {
		return RecoveryExport{}, err
	}
	destination = filepath.Clean(destination)
	if _, err := os.Lstat(destination); err == nil {
		return RecoveryExport{}, ErrRecoveryDestinationExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return RecoveryExport{}, fmt.Errorf("checking the export destination: %w", err)
	}
	parent := filepath.Dir(destination)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		return RecoveryExport{}, fmt.Errorf("the export's directory %s does not exist", parent)
	}
	removeAbandonedExports(destination)

	// Read everything under the lock, so the export is one moment of the
	// journal and never half of a save.
	lock, err := j.lock()
	if err != nil {
		return RecoveryExport{}, err
	}
	files, journalID, err := j.recoveryContents(draftsDir)
	lock.release()
	if err != nil {
		return RecoveryExport{}, err
	}

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return RecoveryExport{}, err
	}
	partial := filepath.Join(parent, "."+filepath.Base(destination)+recoveryPartialPrefix+hex.EncodeToString(suffix))
	if err := os.Mkdir(partial, 0700); err != nil {
		return RecoveryExport{}, fmt.Errorf("starting the export: %w", err)
	}
	finished := false
	defer func() {
		// A failure leaves nothing; only a process that stops here leaves
		// the hidden directory, for the next export to remove.
		if !finished {
			_ = os.RemoveAll(partial)
		}
	}()
	export := RecoveryExport{
		Format:  RecoveryExportFormat,
		Version: RecoveryExportVersion,
		Created: j.clock().Format("2006-01-02T15:04:05Z07:00"),
		Journal: journalID,
		Files:   []RecoveryFile{},
	}
	fs := j.fs.resolved()
	for _, file := range files {
		path := filepath.Join(partial, filepath.FromSlash(file.path))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return RecoveryExport{}, fmt.Errorf("writing the export: %w", err)
		}
		if err := fs.AtomicWriteFile(path, file.data, 0600); err != nil {
			return RecoveryExport{}, fmt.Errorf("writing the export: %w", err)
		}
		export.Files = append(export.Files, RecoveryFile{
			Path: file.path, Bytes: len(file.data), Digest: RevisionOf(file.data), Kind: file.kind,
		})
	}
	manifest, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return RecoveryExport{}, err
	}
	if err := fs.AtomicWriteFile(filepath.Join(partial, recoveryManifestName), append(manifest, '\n'), 0600); err != nil {
		return RecoveryExport{}, fmt.Errorf("writing the export: %w", err)
	}
	if err := fs.AtomicWriteFile(filepath.Join(partial, recoveryReadmeName), []byte(recoveryReadme), 0600); err != nil {
		return RecoveryExport{}, fmt.Errorf("writing the export: %w", err)
	}
	if err := fs.Rename(partial, destination); err != nil {
		return RecoveryExport{}, fmt.Errorf("finishing the export: %w", err)
	}
	finished = true
	export.Directory = destination
	return export, nil
}

// removeAbandonedExports removes what interrupted exports to destination left.
func removeAbandonedExports(destination string) {
	pattern := filepath.Join(filepath.Dir(destination), "."+filepath.Base(destination)+recoveryPartialPrefix+"*")
	matches, _ := filepath.Glob(pattern)
	for _, match := range matches {
		_ = os.RemoveAll(match)
	}
}

type recoveryContent struct {
	path string
	kind string
	data []byte
}

func (j *Journal) recoveryContents(draftsDir string) ([]recoveryContent, string, error) {
	if err := j.recoverLocked(); err != nil {
		return nil, "", err
	}
	var files []recoveryContent
	add := func(source, path, kind string) error {
		data, err := os.ReadFile(source)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading %s for the export: %w", source, err)
		}
		files = append(files, recoveryContent{path: path, kind: kind, data: data})
		return nil
	}
	// The files keep their own names, so a restore is a copy.
	if err := add(j.reflectionsPath, filepath.Base(j.reflectionsPath), "reflections"); err != nil {
		return nil, "", err
	}
	if err := add(j.archivePath, filepath.Base(j.archivePath), "archive"); err != nil {
		return nil, "", err
	}
	if err := add(SettingsPathFor(j.reflectionsPath), "settings/"+settingsFileName, "settings"); err != nil {
		return nil, "", err
	}
	addDir := func(dir, into, kind string) error {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading %s for the export: %w", dir, err)
		}
		for _, entry := range entries {
			if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if err := add(filepath.Join(dir, entry.Name()), into+"/"+entry.Name(), kind); err != nil {
				return err
			}
		}
		return nil
	}
	if err := addDir(filepath.Join(JournalMetadataDirFor(j.reflectionsPath), draftsDirName), "drafts/retained", "draft"); err != nil {
		return nil, "", err
	}
	if err := addDir(filepath.Join(DirectoryMetadataDirFor(j.archivePath), draftsDirName), "drafts/reviews", "review-draft"); err != nil {
		return nil, "", err
	}
	if draftsDir != "" {
		if err := addDir(draftsDir, "drafts/device", "draft"); err != nil {
			return nil, "", err
		}
	}

	// Every version of every open conflict: the one the journal shows is in
	// its file already, and the others exist only in the sync history.
	journalID := ""
	state, enrolled, err := j.readSyncState()
	if err == nil && enrolled {
		journalID = state.Journal
		conflicts, err := j.SyncConflicts()
		if err != nil {
			return nil, "", err
		}
		for _, conflict := range conflicts {
			dir := "conflicts/" + unsafeName.ReplaceAllString(conflict.Artifact, "-")
			put := func(name string, version SyncConflictVersion) {
				if version.Revision == "" || version.Deleted() {
					return
				}
				files = append(files, recoveryContent{path: dir + "/" + name, kind: "conflict", data: []byte(version.Content)})
			}
			put("this-device.md", conflict.Ours)
			put("base.md", conflict.Base)
			for i, other := range conflict.Theirs {
				put(fmt.Sprintf("other-%d.md", i+1), other)
			}
		}
	}
	sort.SliceStable(files, func(a, b int) bool { return files[a].path < files[b].path })
	return files, journalID, nil
}

const recoveryReadme = `This is a recovery export of a Seneca journal. It holds everything written in
it: the reflections file and the review archive exactly as they were, the
journal's settings (settings/), every version of every conflict that was still
open (conflicts/), and writing that had not been saved yet (drafts/).
MANIFEST.json lists each file with its SHA-256.

It holds no sync identity. To restore it:

1. Put the reflections file and the review archive in the journal's directory
   of a new installation, before that installation syncs anything.
2. Start syncing from it, or join the journal your other devices already
   exchange. Either way it takes part as a new device; it never speaks as the
   device this export was made on.

Keep this export private: it is your whole journal.
`
