package cmd

import (
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Sync on the desktop: the journal exchanges its changes with the author's
// other devices through a directory a file service carries between them. The
// engine does the exchanging; these commands decide when, say what happened,
// and put the result in Git.

var (
	syncEnrollJoin bool
	syncEnrollName string

	syncResolveKeep  string
	syncResolveFile  string
	syncResolveText  string
	syncResolveClear bool

	syncRecoverRestore bool
	syncRecoverAccept  bool
)

func init() {
	syncEnrollCmd.Flags().BoolVar(&syncEnrollJoin, "join", false, "Join the journal the exchange already carries instead of starting one")
	syncEnrollCmd.Flags().StringVar(&syncEnrollName, "name", "", "A label for this device (default: its host name)")

	syncResolveCmd.Flags().StringVar(&syncResolveKeep, "keep", "", "The version to keep: ours, theirs, or the start of a version's or record's id")
	syncResolveCmd.Flags().StringVar(&syncResolveFile, "file", "", "Write the contents of this file instead of any competing version")
	syncResolveCmd.Flags().StringVar(&syncResolveText, "text", "", "For a disputed focus: set this focus instead of any competing one")
	syncResolveCmd.Flags().BoolVar(&syncResolveClear, "clear", false, "For a disputed focus: leave that day with no focus")

	syncRecoverCmd.Flags().BoolVar(&syncRecoverRestore, "restore", false, "Put back what the file lost, from the journal's history")
	syncRecoverCmd.Flags().BoolVar(&syncRecoverAccept, "accept-deletions", false, "Record what the file lost as deleted, on every device")

	syncCmd.AddCommand(syncEnrollCmd, syncOnceCmd, syncStatusCmd, syncConflictsCmd, syncResolveCmd, syncRecoverCmd, syncLeaveCmd)
	rootCmd.AddCommand(syncCmd)
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Exchange the journal with your other devices",
	Long: "Exchange the journal's changes with your other devices through a directory a\n" +
		"file service carries between them, named by [sync].exchange in config.toml or\n" +
		"SENECA_SYNC_EXCHANGE. Every device keeps its own Markdown and works offline.",
}

var syncEnrollCmd = &cobra.Command{
	Use:   "enroll",
	Short: "Make this journal take part in sync",
	Long: "Start a synced journal from this one and publish it to the exchange, or with\n" +
		"--join make this device another device of the journal the exchange already\n" +
		"carries. A journal that is already enrolled is reported as it is.",
	Args: cobra.NoArgs,
	RunE: runSyncEnroll,
}

var syncOnceCmd = &cobra.Command{
	Use:   "once",
	Short: "Import what arrived, commit it, and publish what is new here",
	Args:  cobra.NoArgs,
	RunE:  runSyncOnce,
}

var syncStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report where sync stands without changing anything",
	Long: "Report this device's view of the journal's sync history, what waits in the\n" +
		"exchange, and what waits for you. Nothing is imported, recovered, published,\n" +
		"or committed.",
	Args: cobra.NoArgs,
	RunE: runSyncStatus,
}

var syncConflictsCmd = &cobra.Command{
	Use:   "conflicts",
	Short: "Show every change that waits for you to choose",
	Args:  cobra.NoArgs,
	RunE:  runSyncConflicts,
}

var syncResolveCmd = &cobra.Command{
	Use:   "resolve <id>",
	Short: "Settle one conflict or disputed focus",
	Long: "Settle the conflict or disputed focus whose id starts with <id>, as listed by\n" +
		"`seneca sync conflicts`. Keep one of the competing versions with --keep, or\n" +
		"write something else with --file (an entry, a record, the settings) or --text\n" +
		"(a focus); --clear leaves a disputed day with no focus. Every version stays in\n" +
		"the journal's history whichever is kept.",
	Args: cobra.ExactArgs(1),
	RunE: runSyncResolve,
}

var syncRecoverCmd = &cobra.Command{
	Use:   "recover <reflections|archive|settings>",
	Short: "Decide what to do about a journal file sync stopped reading",
	Long: "A journal file that went missing, was emptied, or lost most of its content at\n" +
		"once is not read as deletions until you say which it was. --restore puts back\n" +
		"what it lost; --accept-deletions records the losses as deletions.",
	Args: cobra.ExactArgs(1),
	RunE: runSyncRecover,
}

var syncLeaveCmd = &cobra.Command{
	Use:   "leave",
	Short: "Stop taking part in sync, keeping every file",
	Long: "Take this journal out of sync. The Markdown, the archive, and the settings stay\n" +
		"exactly as they are; the sync history and this device's identity are moved\n" +
		"aside under .seneca and never used again. Afterwards `seneca sync enroll\n" +
		"--join` makes this device a new device of the journal, and its writing is\n" +
		"compared with the others' rather than written over them. Use it to disconnect\n" +
		"a device, or on a copy restored from another device's files.",
	Args: cobra.NoArgs,
	RunE: runSyncLeave,
}

func runSyncLeave(cmd *cobra.Command, args []string) error {
	setup, err := resolveSyncSetup()
	if err != nil {
		return err
	}
	journal, err := openJournal(setup.reflections, engine.ReflectionSettings{}, time.Now())
	if err != nil {
		return err
	}
	left, err := journal.LeaveSync()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if left.Device != "" {
		fmt.Fprintf(out, "This journal no longer takes part in sync; device %s is retired.\n", engine.ShortRecordID(left.Device))
	} else {
		fmt.Fprintln(out, "This journal no longer takes part in sync.")
	}
	fmt.Fprintf(out, "Every file is as it was. The sync history is kept at %s.\n", left.Kept)
	if left.Conflicts > 0 {
		fmt.Fprintf(out, "%s were open; this journal keeps the version it showed, and the others are in that history.\n",
			plural(left.Conflicts, "conflict", "conflicts"))
	}
	fmt.Fprintln(out, "To take part again as a new device: `seneca sync enroll --join`. Remove [sync].exchange and run `seneca sync service remove` to stop for good.")
	return nil
}

func deviceName() string {
	if name := strings.TrimSpace(syncEnrollName); name != "" {
		return name
	}
	if host, err := os.Hostname(); err == nil {
		return host
	}
	return "desktop"
}

func runSyncEnroll(cmd *cobra.Command, args []string) error {
	now := time.Now()
	journal, home, err := settingsJournal(now)
	if err != nil {
		return err
	}
	setup, err := syncSetupFor(home, journal.ReflectionsPath())
	if err != nil {
		return err
	}
	if err := setup.requireExchange(); err != nil {
		return err
	}
	transport := setup.transport()
	out := cmd.OutOrStdout()

	_, already, err := journal.SyncStatus()
	if err != nil {
		return err
	}
	_, descriptorErr := transport.Descriptor()
	carried := descriptorErr == nil
	if descriptorErr != nil && !errors.Is(descriptorErr, fs.ErrNotExist) {
		return fmt.Errorf("reading the exchange %s: %w", setup.exchange, descriptorErr)
	}

	if syncEnrollJoin {
		status, err := journal.JoinSync(transport, deviceName())
		if err != nil && !engine.WriteCommitted(err) {
			return err
		}
		if already {
			fmt.Fprintf(out, "Already part of journal %s as %s.\n", engine.ShortRecordID(status.Journal), describeDevice(status))
		} else {
			fmt.Fprintf(out, "Joined journal %s as %s.\n", engine.ShortRecordID(status.Journal), describeDevice(status))
		}
		// The journal's history, and this device's own writing, are exchanged
		// now rather than left for the first command to stumble on.
		report := runSyncCycle(setup, now, true)
		report.print(out, cmd.ErrOrStderr())
		return err
	}

	if carried && !already {
		return fmt.Errorf("the exchange %s already carries a journal; run `seneca sync enroll --join` to make this device part of it (nothing was written)", setup.exchange)
	}
	// Enrolling gives the journal settings of its own when it has none, which
	// is a change to a shared file like `seneca settings init` makes.
	settings, err := journal.EffectiveSettings()
	if err != nil {
		return err
	}
	status, err := journal.EnrollSync(deviceName())
	if err != nil && !engine.WriteCommitted(err) {
		return err
	}
	if already {
		fmt.Fprintf(out, "Already enrolled: journal %s as %s.\n", engine.ShortRecordID(status.Journal), describeDevice(status))
	} else {
		fmt.Fprintf(out, "Enrolled journal %s as %s.\n", engine.ShortRecordID(status.Journal), describeDevice(status))
		if !settings.Initialized {
			fmt.Fprintf(out, "Journal settings initialized in %s.\n", settings.Path)
			commitArchivePaths("seneca: settings init", settings.Path)
		}
	}
	if err != nil {
		return err
	}
	published, err := journal.PublishSync(transport)
	if err != nil {
		return fmt.Errorf("the journal is enrolled, but publishing to %s failed (run `seneca sync once` to try again): %w", setup.exchange, err)
	}
	if n := len(published.Transactions); n > 0 {
		fmt.Fprintf(out, "Published %s to %s.\n", plural(n, "change", "changes"), setup.exchange)
	}
	return nil
}

func describeDevice(status engine.SyncStatus) string {
	if status.Name == "" {
		return "device " + engine.ShortRecordID(status.Device)
	}
	return fmt.Sprintf("%q (device %s)", status.Name, engine.ShortRecordID(status.Device))
}

func runSyncOnce(cmd *cobra.Command, args []string) error {
	setup, err := resolveSyncSetup()
	if err != nil {
		return err
	}
	report := runSyncCycle(setup, time.Now(), true)
	if !report.enrolled && report.importErr == nil {
		return engine.ErrSyncNotEnrolled
	}
	out := cmd.OutOrStdout()
	report.print(out, cmd.ErrOrStderr())
	if setup.exchange == "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: sync: %v; nothing was exchanged\n", setup.requireExchange())
	}
	if !report.worked() && !report.unsettled() {
		fmt.Fprintln(out, "Nothing to exchange.")
	}
	// A failed Git commit is a warning here as it is for every command: the
	// journal is saved, and the commit is retried.
	return errors.Join(report.importErr, report.publishErr)
}

func runSyncStatus(cmd *cobra.Command, args []string) error {
	setup, err := resolveSyncSetup()
	if err != nil {
		return err
	}
	return printSyncStatus(cmd.OutOrStdout(), setup)
}

// printSyncStatus reports from what is on disk. It opens no journal, so no
// interrupted save is completed and no lock is taken.
func printSyncStatus(out io.Writer, setup syncSetup) error {
	status, enrolled, err := engine.LoadSyncStatus(setup.reflections)
	if err != nil {
		var version *engine.SyncStateVersionError
		if errors.As(err, &version) {
			code := engine.FindingSyncStateUnreadable
			if version.Newer() {
				code = engine.FindingSyncStateNewer
			}
			fmt.Fprintf(out, "Sync: %s\n", syncAdvice(engine.DiagnosticFinding{Code: code, Count: 1}))
		}
		return err
	}
	if !enrolled {
		fmt.Fprintf(out, "Sync: %s does not take part in sync.\n", setup.reflections)
		fmt.Fprintln(out, "Run `seneca sync enroll` to start, or `seneca sync enroll --join` on a second device.")
		return nil
	}
	fmt.Fprintf(out, "Sync: journal %s, this device is %s\n", engine.ShortRecordID(status.Journal), describeDevice(status))
	fmt.Fprintf(out, "Recorded here: %s\n", plural(status.Sequence, "change", "changes"))

	if setup.exchange == "" {
		fmt.Fprintf(out, "Exchange: none configured (set [sync].exchange in %s)\n", configFilePath(setup.home))
	} else {
		fmt.Fprintf(out, "Exchange: %s\n", setup.exchange)
		view, err := engine.InspectSyncExchange(setup.reflections, setup.transport())
		switch {
		case err != nil:
			fmt.Fprintf(out, "  %v\n", err)
		case !view.Found:
			fmt.Fprintln(out, "  holds no journal yet; `seneca sync once` publishes this one")
		default:
			fmt.Fprintf(out, "  not published yet: %d\n  not imported yet: %d\n", view.Unpublished, view.Unimported)
			printSyncDelivery(out, setup)
			if view.Incompatible > 0 {
				fmt.Fprintf(out, "  %s\n", syncAdvice(engine.DiagnosticFinding{Code: engine.FindingChangesIncompatible, Count: view.Incompatible}))
			}
			if view.Impersonated > 0 {
				fmt.Fprintf(out, "  %s\n", syncAdvice(engine.DiagnosticFinding{Code: engine.FindingIdentityReused, Count: view.Impersonated}))
			}
		}
		if n := setup.cloud().downloading(); n > 0 {
			fmt.Fprintf(out, "  still downloading from iCloud: %s\n", plural(n, "file", "files"))
		}
	}

	if engine.PendingCommit(filepath.Dir(setup.reflections)) {
		fmt.Fprintln(out, "An interrupted save waits to be completed by the next command that writes; this report shows the journal before it.")
	}
	for _, pending := range status.Pending {
		line := fmt.Sprintf("Waiting: change %s (%s)", engine.ShortRecordID(pending.Transaction), pending.Reason)
		if pending.Detail != "" {
			line += ": " + pending.Detail
		}
		fmt.Fprintln(out, line)
	}
	files := make([]string, 0, len(status.Suspended))
	for file := range status.Suspended {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		fmt.Fprintf(out, "Not read: the %s file (%s). Run `seneca sync recover %s --restore` or `--accept-deletions`.\n", file, status.Suspended[file], file)
	}
	if n := len(status.Conflicts) + len(status.DisputedFocus); n > 0 {
		fmt.Fprintf(out, "Conflicts: %d wait for you; run `seneca sync conflicts`\n", n)
	} else {
		fmt.Fprintln(out, "Conflicts: none")
	}

	if pending, found := readGitPending(setup.reflections); found {
		line := fmt.Sprintf("Git: imported changes since %s are not committed yet", pending.Since)
		if pending.LastError != "" {
			line += fmt.Sprintf(" (%s; last: %s)", plural(pending.Attempts, "attempt", "attempts"), pending.LastError)
		}
		fmt.Fprintln(out, line)
	}
	if busy := gitBusy(setup.managed()...); busy != "" {
		fmt.Fprintf(out, "Git: %s; nothing is imported until that is finished\n", busy)
	}

	if plist, err := launchAgentPath(setup.home); err == nil {
		if _, err := os.Stat(plist); err == nil {
			fmt.Fprintf(out, "Background service: installed (%s)\n", plist)
		} else {
			fmt.Fprintln(out, "Background service: not installed")
		}
	}
	return nil
}

// printSyncDelivery says how far each other device has applied what this one
// recorded, by that device's own receipt. A device that has published none is
// not listed: nothing is known of it, which is not the same as nothing
// arriving.
func printSyncDelivery(out io.Writer, setup syncSetup) {
	delivery, err := engine.InspectSyncDelivery(setup.reflections, setup.transport())
	if err != nil {
		return
	}
	for _, peer := range delivery.Peers {
		if peer.Name == "" && peer.Applied == 0 {
			continue
		}
		name := "device " + engine.ShortRecordID(peer.Device)
		if peer.Name != "" {
			name = fmt.Sprintf("%q", peer.Name)
		}
		fmt.Fprintf(out, "  applied on %s: %d of %d recorded here\n", name, peer.Applied, delivery.Recorded)
	}
}

func shortRevision(revision engine.Revision) string {
	if revision == engine.RevisionAbsent {
		return "deleted"
	}
	return engine.ShortRecordID(strings.TrimPrefix(string(revision), "sha256:"))
}

func indent(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	return "    " + strings.Join(lines, "\n    ") + "\n"
}

func runSyncConflicts(cmd *cobra.Command, args []string) error {
	setup, err := resolveSyncSetup()
	if err != nil {
		return err
	}
	journal, err := openJournal(setup.reflections, engine.ReflectionSettings{}, time.Now())
	if err != nil {
		return err
	}
	conflicts, err := journal.SyncConflicts()
	if err != nil {
		return err
	}
	disputes, err := journal.FocusDisputes()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(conflicts)+len(disputes) == 0 {
		fmt.Fprintln(out, "No conflicts.")
		return nil
	}
	for _, conflict := range conflicts {
		fmt.Fprintf(out, "Conflict %s: %s", engine.ShortRecordID(conflict.ID), conflict.Artifact)
		if conflict.Reason != "" {
			fmt.Fprintf(out, " (%s)", conflict.Reason)
		}
		fmt.Fprintln(out)
		printVersion(out, "ours", conflict.Ours)
		for _, theirs := range conflict.Theirs {
			printVersion(out, "theirs", theirs)
		}
		fmt.Fprintf(out, "  Settle it with `seneca sync resolve %s --keep ours` (or theirs, or a version's id), or --file <path>.\n\n",
			engine.ShortRecordID(conflict.ID))
	}
	for _, dispute := range disputes {
		fmt.Fprintf(out, "Disputed focus %s: %s\n", engine.ShortRecordID(dispute.ID), dispute.Effective)
		for _, candidate := range dispute.Candidates {
			if candidate.Event == engine.FocusEventClear {
				fmt.Fprintf(out, "  %s  (cleared)\n", engine.ShortRecordID(candidate.Record))
				continue
			}
			fmt.Fprintf(out, "  %s  %s\n", engine.ShortRecordID(candidate.Record), strings.Join(strings.Fields(candidate.Text), " "))
		}
		fmt.Fprintf(out, "  Settle it with `seneca sync resolve %s --keep <id>`, --text \"...\", or --clear.\n\n", engine.ShortRecordID(dispute.ID))
	}
	return nil
}

func printVersion(out io.Writer, label string, version engine.SyncConflictVersion) {
	who := ""
	if version.Device != "" {
		who = "  written by device " + engine.ShortRecordID(version.Device)
	}
	fmt.Fprintf(out, "  %s  %s%s\n", label, shortRevision(version.Revision), who)
	if !version.Deleted() {
		fmt.Fprint(out, indent(version.Content))
	}
}

// keptRevision reads --keep against the versions of one conflict.
func keptRevision(conflict engine.SyncConflict, keep string) (engine.Revision, error) {
	switch keep {
	case "ours":
		return conflict.Ours.Revision, nil
	case "theirs":
		if len(conflict.Theirs) != 1 {
			return "", fmt.Errorf("%d other versions compete for %s; name the one to keep by the start of its id", len(conflict.Theirs), conflict.Artifact)
		}
		return conflict.Theirs[0].Revision, nil
	}
	var matches []engine.Revision
	for _, revision := range conflict.Versions() {
		if strings.HasPrefix(shortRevision(revision), keep) || strings.HasPrefix(strings.TrimPrefix(string(revision), "sha256:"), keep) {
			matches = append(matches, revision)
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("--keep %q names %d of the versions competing for %s; use ours, theirs, or the start of one id shown by `seneca sync conflicts`", keep, len(matches), conflict.Artifact)
	}
	return matches[0], nil
}

func runSyncResolve(cmd *cobra.Command, args []string) error {
	setup, err := resolveSyncSetup()
	if err != nil {
		return err
	}
	// What other devices delivered may have settled this, or changed what
	// competes in it: answer what is there now.
	syncBefore(setup.reflections)
	defer syncAfter(setup.reflections)

	journal, err := openJournal(setup.reflections, engine.ReflectionSettings{}, time.Now())
	if err != nil {
		return err
	}
	conflicts, err := journal.SyncConflicts()
	if err != nil {
		return err
	}
	disputes, err := journal.FocusDisputes()
	if err != nil {
		return err
	}

	prefix := args[0]
	var conflict *engine.SyncConflict
	var dispute *engine.FocusDispute
	found := 0
	for i := range conflicts {
		if strings.HasPrefix(conflicts[i].ID, prefix) {
			conflict = &conflicts[i]
			found++
		}
	}
	for i := range disputes {
		if strings.HasPrefix(disputes[i].ID, prefix) {
			dispute = &disputes[i]
			found++
		}
	}
	if found != 1 {
		return fmt.Errorf("%q names %d of the conflicts waiting; run `seneca sync conflicts` for their ids (nothing was written)", prefix, found)
	}

	out := cmd.OutOrStdout()
	flags := cmd.Flags()
	if dispute != nil {
		if flags.Changed("file") {
			return errors.New("a disputed focus is settled with --keep, --text, or --clear; --file is for an entry, a record, or the settings")
		}
		var resolution engine.FocusResolution
		switch chosen := countTrue(flags.Changed("keep"), flags.Changed("text"), syncResolveClear); {
		case chosen != 1:
			return errors.New("choose one of --keep <id>, --text \"...\", or --clear")
		case flags.Changed("keep"):
			var matches []string
			for _, candidate := range dispute.Candidates {
				if strings.HasPrefix(candidate.Record, syncResolveKeep) {
					matches = append(matches, candidate.Record)
				}
			}
			if len(matches) != 1 {
				return fmt.Errorf("--keep %q names %d of the focus changes competing for %s", syncResolveKeep, len(matches), dispute.Effective)
			}
			resolution.Record = matches[0]
		case flags.Changed("text"):
			if strings.TrimSpace(syncResolveText) == "" {
				return errors.New("--text is blank; use --clear to leave the day with no focus")
			}
			resolution.Text = syncResolveText
		}
		change, err := journal.ResolveFocusDispute(dispute.ID, resolution)
		report := func() {
			if change.Text == "" {
				fmt.Fprintf(out, "Focus for %s settled: no focus.\n", change.Effective)
			} else {
				fmt.Fprintf(out, "Focus for %s settled:\n%s\n", change.Effective, change.Text)
			}
			commitArchivePaths(fmt.Sprintf("seneca: sync resolve focus %s", change.Effective), journal.ArchivePath())
		}
		if err != nil {
			return engine.OnCommittedWrite(err, report)
		}
		report()
		return nil
	}

	if flags.Changed("text") || syncResolveClear {
		return errors.New("--text and --clear settle a disputed focus; this conflict takes --keep or --file")
	}
	var resolution engine.SyncResolution
	switch {
	case flags.Changed("keep") == flags.Changed("file"):
		return errors.New("choose one of --keep <ours|theirs|id> or --file <path>")
	case flags.Changed("keep"):
		if resolution.Keep, err = keptRevision(*conflict, syncResolveKeep); err != nil {
			return err
		}
	default:
		data, err := os.ReadFile(syncResolveFile)
		if err != nil {
			return fmt.Errorf("reading --file: %w", err)
		}
		resolution.Content = string(data)
	}
	resolved, err := journal.ResolveSyncConflict(conflict.ID, resolution)
	report := func() {
		fmt.Fprintf(out, "Resolved %s: it now holds %s.\n", resolved.Artifact, shortRevision(resolved.Revision))
		commitWritten(fmt.Sprintf("seneca: sync resolve %s", resolved.Artifact), resolved.Written)
	}
	if err != nil {
		return engine.OnCommittedWrite(err, report)
	}
	report()
	return nil
}

func countTrue(values ...bool) int {
	n := 0
	for _, value := range values {
		if value {
			n++
		}
	}
	return n
}

// commitWritten commits the journal files a sync operation replaced, and only
// the ones Git can take: a settings document the repository ignores must not
// fail the commit of the reflections file beside it.
func commitWritten(message string, written []string) {
	var paths []string
	for _, path := range written {
		if gitKnows(path) {
			paths = append(paths, path)
		}
	}
	commitArchivePaths(message, paths...)
}

func runSyncRecover(cmd *cobra.Command, args []string) error {
	if syncRecoverRestore == syncRecoverAccept {
		return errors.New("choose one of --restore or --accept-deletions")
	}
	action := engine.SyncRecoverRestore
	if syncRecoverAccept {
		action = engine.SyncRecoverAcceptDeletions
	}
	setup, err := resolveSyncSetup()
	if err != nil {
		return err
	}
	defer syncAfter(setup.reflections)

	journal, err := openJournal(setup.reflections, engine.ReflectionSettings{}, time.Now())
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	recovered, err := journal.RecoverSyncFile(args[0], action)
	report := func() {
		if n := len(recovered.Restored); n > 0 {
			fmt.Fprintf(out, "Restored %s to the %s file.\n", plural(n, "item", "items"), args[0])
		}
		if n := len(recovered.Deleted); n > 0 {
			fmt.Fprintf(out, "Recorded %s as deleted from the %s file.\n", plural(n, "item", "items"), args[0])
		}
		fmt.Fprintf(out, "The %s file is read normally again.\n", args[0])
		commitWritten(fmt.Sprintf("seneca: sync recover %s", args[0]), recovered.Written)
	}
	if err != nil {
		return engine.OnCommittedWrite(err, report)
	}
	report()
	return nil
}
