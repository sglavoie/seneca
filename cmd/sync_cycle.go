package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// One exchange is the same four steps whoever asks for it: `seneca sync once`,
// the background watcher, or a command about to change the journal.
//
//  1. Look at the repository. While Git is rewriting the working tree, or a
//     journal file is unmerged, nothing is imported: the files are not the
//     author's yet.
//  2. Import. What other devices delivered is applied to the Markdown by the
//     engine, as one commit of its own.
//  3. Commit to Git the journal files the import replaced. A failure here is
//     remembered and tried again; the import is never repeated for it.
//  4. Publish what this device recorded. Publishing writes files into the
//     exchange directory and returns; delivery is the file service's business.
//  5. Publish a receipt of what this device has applied, once nothing it
//     imported still waits for its Git commit. The receipt is what another
//     device shows its author as "arrived"; see engine/sync_receipt.go.
//
// Before any of it, files of the exchange that iCloud Drive lists and has not
// downloaded are asked for; see sync_cloud.go. They are imported by the
// exchange that finds them here.
//
// Each step stands by itself. A failed Git commit does not undo an import, a
// deferred import does not stop publishing, and none of them stops a local
// save.

// syncExchangeEnv overrides `[sync].exchange`, the way SENECA_FILE overrides
// the reflections path.
const syncExchangeEnv = "SENECA_SYNC_EXCHANGE"

// syncSetup is what this device exchanges: a journal, and the directory it
// reaches other devices through.
type syncSetup struct {
	home        string
	reflections string
	// exchange is empty when none is configured.
	exchange string
}

func resolveSyncSetup() (syncSetup, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return syncSetup{}, fmt.Errorf("getting home directory: %w", err)
	}
	path, err := resolveFilePathForHome(home, os.Getenv)
	if err != nil {
		return syncSetup{}, err
	}
	return syncSetupFor(home, path)
}

func syncSetupFor(home, reflectionsPath string) (syncSetup, error) {
	setup := syncSetup{home: home, reflections: reflectionsPath, exchange: os.Getenv(syncExchangeEnv)}
	if setup.exchange == "" {
		exchange, err := loadConfiguredSyncExchange(home)
		if err != nil {
			return syncSetup{}, err
		}
		setup.exchange = exchange
	}
	return setup, nil
}

// transport is nil when no exchange is configured: the journal's history is
// still recorded, and waits.
func (s syncSetup) transport() engine.SyncTransport {
	if cloud := s.cloud(); cloud != nil {
		return cloud
	}
	return nil
}

// cloud is the exchange as this Mac holds it right now: what of it is here,
// and what is only listed. It is nil when no exchange is configured.
func (s syncSetup) cloud() *cloudExchange {
	if s.exchange == "" {
		return nil
	}
	return newCloudExchange(s.exchange)
}

func (s syncSetup) requireExchange() error {
	if s.exchange == "" {
		return fmt.Errorf("no exchange directory is configured: set [sync].exchange in %s, or %s", configFilePath(s.home), syncExchangeEnv)
	}
	return nil
}

// managed are the journal files sync may replace, and so the only paths a sync
// commit ever names. The history under `.seneca/` beside them is this device's
// own and is never staged.
func (s syncSetup) managed() []string {
	return []string{s.reflections, engine.ArchivePathFor(s.reflections), engine.SettingsPathFor(s.reflections)}
}

const (
	syncDesktopDirName    = "desktop"
	gitPendingFileName    = "git-pending.json"
	gitPendingVersion     = 1
	syncImportCommitTitle = "seneca: sync import"
)

// gitPending is the note that journal files sync replaced are not in Git yet.
// It is what lets a failed commit be retried without importing anything again.
type gitPending struct {
	Version   int      `json:"version"`
	Message   string   `json:"message"`
	Paths     []string `json:"paths"`
	Since     string   `json:"since"`
	Attempts  int      `json:"attempts"`
	LastError string   `json:"last_error,omitempty"`
}

// gitPendingPathFor is where that note is kept: beside the journal's sync
// history, device-local, and never committed itself.
func gitPendingPathFor(reflectionsPath string) string {
	return filepath.Join(engine.JournalMetadataDirFor(reflectionsPath), syncDesktopDirName, gitPendingFileName)
}

func readGitPending(reflectionsPath string) (gitPending, bool) {
	data, err := os.ReadFile(gitPendingPathFor(reflectionsPath))
	if err != nil {
		return gitPending{}, false
	}
	var pending gitPending
	if err := json.Unmarshal(data, &pending); err != nil || pending.Version != gitPendingVersion || len(pending.Paths) == 0 {
		return gitPending{}, false
	}
	return pending, true
}

func writeGitPending(reflectionsPath string, pending gitPending) error {
	path := gitPendingPathFor(reflectionsPath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	pending.Version = gitPendingVersion
	sort.Strings(pending.Paths)
	data, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return err
	}
	return journalFS.AtomicWriteFile(path, append(data, '\n'), 0600)
}

func clearGitPending(reflectionsPath string) {
	path := gitPendingPathFor(reflectionsPath)
	if err := os.Remove(path); err == nil {
		// Leave no empty directory behind for a note that is gone.
		_ = os.Remove(filepath.Dir(path))
	}
}

// noteGitPending adds the files a sync operation replaced to the note.
func noteGitPending(reflectionsPath, message string, now time.Time, written []string) error {
	if len(written) == 0 {
		return nil
	}
	pending, found := readGitPending(reflectionsPath)
	if !found {
		pending = gitPending{Message: message, Since: now.Format(time.RFC3339)}
	}
	if pending.Message != message {
		// Two kinds of change waiting for one commit: say only what is true of
		// both.
		pending.Message = syncImportCommitTitle
	}
	known := map[string]bool{}
	for _, path := range pending.Paths {
		known[path] = true
	}
	for _, path := range written {
		if !known[path] {
			pending.Paths = append(pending.Paths, path)
		}
	}
	return writeGitPending(reflectionsPath, pending)
}

// settleGit commits what the note names, and drops the note once Git has it or
// has nothing to take. A failure keeps the note, with what went wrong, for the
// next attempt. Outside a repository there is nothing to retry.
func settleGit(reflectionsPath string) (bool, error) {
	pending, found := readGitPending(reflectionsPath)
	if !found {
		return false, nil
	}
	var paths []string
	for _, path := range pending.Paths {
		if gitKnows(path) {
			paths = append(paths, path)
		}
	}
	committed, err := commitPaths(pending.Message, paths...)
	var failure *gitError
	if errors.As(err, &failure) && failure.stage != gitStageRepo {
		pending.Attempts++
		pending.LastError = failure.Error()
		if writeErr := writeGitPending(reflectionsPath, pending); writeErr != nil {
			return false, fmt.Errorf("%w (and the note to retry it could not be kept: %v)", err, writeErr)
		}
		return false, err
	}
	clearGitPending(reflectionsPath)
	return committed, nil
}

// syncReport is what one exchange did, step by step.
type syncReport struct {
	enrolled bool
	// deferred says why nothing was imported or committed this time.
	deferred string
	// downloading counts files of the exchange that are listed on this Mac
	// and not downloaded yet.
	downloading int
	imported    engine.SyncImported
	importErr   error
	committed   bool
	gitErr      error
	published   []string
	publishErr  error
}

// runSyncCycle is one exchange for the journal, as described at the top of
// this file. It never returns an error: every step reports into the result,
// and a step that failed is tried again by the next cycle.
func runSyncCycle(setup syncSetup, now time.Time, publish bool) syncReport {
	var report syncReport
	_, enrolled, err := engine.LoadSyncStatus(setup.reflections)
	if err != nil {
		report.importErr = err
		return report
	}
	if !enrolled {
		return report
	}
	report.enrolled = true

	// Import reads no settings: the ones the journal carries are an artifact.
	journal, err := openJournal(setup.reflections, engine.ReflectionSettings{}, now)
	if err != nil {
		report.importErr = err
		return report
	}
	var transport engine.SyncTransport
	if cloud := setup.cloud(); cloud != nil {
		transport = cloud
		report.downloading = cloud.request()
	}

	if busy := gitBusy(setup.managed()...); busy != "" {
		report.deferred = busy
	} else {
		imported, err := journal.ImportSync(transport)
		report.imported = imported
		report.importErr = err
		if err == nil || engine.WriteCommitted(err) {
			if noteErr := noteGitPending(setup.reflections, syncImportCommitTitle, now, imported.Written); noteErr != nil {
				report.gitErr = fmt.Errorf("the imported files could not be marked for a git commit: %w", noteErr)
			}
		}
		if report.gitErr == nil {
			report.committed, report.gitErr = settleGit(setup.reflections)
		}
	}

	if publish && transport != nil {
		published, err := journal.PublishSync(transport)
		report.published = published.Transactions
		report.publishErr = err
		if err == nil && report.applicationSettled(setup.reflections) {
			_, report.publishErr = journal.PublishSyncReceipt(transport)
		}
	}
	return report
}

// applicationSettled reports whether everything this device imported is
// where the author keeps it: in the Markdown, and in Git when the journal is
// in a repository. Only then does it tell other devices so.
func (r syncReport) applicationSettled(reflectionsPath string) bool {
	if r.deferred != "" || r.gitErr != nil || (r.importErr != nil && !engine.WriteCommitted(r.importErr)) {
		return false
	}
	_, pending := readGitPending(reflectionsPath)
	return !pending
}

// incomplete reports whether something in the exchange was still arriving.
func (r syncReport) incomplete() bool {
	for _, diagnostic := range r.imported.Diagnostics {
		if diagnostic.Code == engine.SyncDiagIncomplete {
			return true
		}
	}
	return false
}

// unsettled reports whether the cycle left something a sooner retry could
// finish: a failed step, a deferred import, or a delivery still arriving.
func (r syncReport) unsettled() bool {
	return r.deferred != "" || r.importErr != nil || r.gitErr != nil || r.publishErr != nil || r.incomplete() || r.downloading > 0
}

// worked reports whether the cycle changed anything anywhere.
func (r syncReport) worked() bool {
	return len(r.imported.Applied) > 0 || len(r.imported.Recorded) > 0 || len(r.imported.Written) > 0 || r.committed || len(r.published) > 0
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// done lists what the cycle did, one line each.
func (r syncReport) done() []string {
	var lines []string
	if n := len(r.imported.Applied); n > 0 {
		lines = append(lines, "Applied "+plural(n, "change", "changes")+" from other devices.")
	}
	if n := len(r.imported.Recorded); n > 0 {
		lines = append(lines, "Recorded "+plural(n, "edit", "edits")+" found in the journal's files.")
	}
	for _, path := range r.imported.Written {
		lines = append(lines, "Updated "+path)
	}
	if n := len(r.imported.Merged); n > 0 {
		lines = append(lines, "Merged without conflict: "+strings.Join(r.imported.Merged, ", "))
	}
	if r.committed {
		lines = append(lines, "Committed: "+syncImportCommitTitle)
	}
	if n := len(r.published); n > 0 {
		lines = append(lines, "Published "+plural(n, "change", "changes")+" to the exchange.")
	}
	return lines
}

// attention lists what the cycle could not do, or what waits for the author.
func (r syncReport) attention() []string {
	var lines []string
	if r.deferred != "" {
		lines = append(lines, fmt.Sprintf("nothing was imported: %s; the journal is left as it is until that is finished", r.deferred))
	}
	if r.importErr != nil {
		lines = append(lines, fmt.Sprintf("importing: %v", r.importErr))
	}
	if n := len(r.imported.Conflicts); n > 0 {
		lines = append(lines, plural(n, "conflict waits", "conflicts wait")+" for you: run `seneca sync conflicts`")
	}
	for _, diagnostic := range r.imported.Diagnostics {
		lines = append(lines, diagnostic.String())
	}
	if r.downloading > 0 {
		lines = append(lines, plural(r.downloading, "file of the exchange is", "files of the exchange are")+
			" still downloading from iCloud; what they carry is imported once they are here")
	}
	if r.gitErr != nil {
		lines = append(lines, fmt.Sprintf("the imported changes are saved but not committed to git, and the commit will be retried: %v", r.gitErr))
	}
	if r.publishErr != nil {
		lines = append(lines, fmt.Sprintf("publishing: %v", r.publishErr))
	}
	return lines
}

func (r syncReport) print(out, errOut io.Writer) {
	for _, line := range r.done() {
		fmt.Fprintln(out, line)
	}
	for _, line := range r.attention() {
		fmt.Fprintf(errOut, "warning: sync: %s\n", line)
	}
}

// enrolledSetup resolves the exchange for a journal that takes part in sync.
// For every other journal it is one failed stat and nothing more, which is
// what keeps the commands exactly what they were without sync.
func enrolledSetup(reflectionsPath string) (syncSetup, bool) {
	if _, err := os.Stat(engine.SyncStatePathFor(reflectionsPath)); err != nil {
		return syncSetup{}, false
	}
	home, err := os.UserHomeDir()
	if err == nil {
		var setup syncSetup
		if setup, err = syncSetupFor(home, reflectionsPath); err == nil {
			return setup, true
		}
	}
	fmt.Fprintf(os.Stderr, "warning: sync: %v\n", err)
	return syncSetup{}, false
}

// syncBefore brings in what other devices have already delivered, before a
// command reads the journal to prepare an edit. It never stops the command:
// whatever goes wrong is a warning, and the edit is made on what is here.
func syncBefore(reflectionsPath string) {
	setup, ok := enrolledSetup(reflectionsPath)
	if !ok {
		return
	}
	runSyncCycle(setup, time.Now(), false).print(os.Stdout, os.Stderr)
}

// syncAround is both halves for a command that knows only the archive: it
// imports now and returns the publish to defer. The journal that takes part in
// sync is the one the reflections file beside the archive names.
func syncAround(archivePath string) func() {
	reflectionsPath, err := resolveFilePath()
	if err != nil || engine.ArchivePathFor(reflectionsPath) != archivePath {
		return func() {}
	}
	syncBefore(reflectionsPath)
	return func() { syncAfter(reflectionsPath) }
}

// syncAfter publishes what a command just recorded. It writes into the
// exchange directory and returns; it does not wait for another device.
func syncAfter(reflectionsPath string) {
	setup, ok := enrolledSetup(reflectionsPath)
	if !ok || setup.exchange == "" {
		return
	}
	journal, err := openJournal(reflectionsPath, engine.ReflectionSettings{}, time.Now())
	if err == nil {
		transport := setup.transport()
		_, err = journal.PublishSync(transport)
		if _, pending := readGitPending(reflectionsPath); err == nil && !pending {
			_, err = journal.PublishSyncReceipt(transport)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: sync: the change is saved here but was not published, and will be by the next sync: %v\n", err)
	}
}
