package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

// Backup is the explicit recovery export: everything written in the journal,
// in a new directory the author names. Unlike `seneca doctor --diagnostics` it
// holds the author's whole journal, so it is only ever made on request.

func init() {
	rootCmd.AddCommand(backupCmd)
}

var backupCmd = &cobra.Command{
	Use:   "backup <new-directory>",
	Short: "Copy all writing into a new directory, for recovery",
	Long: "Write the reflections file and the review archive byte for byte, the journal's\n" +
		"settings, every version of every open sync conflict, and unsaved writing into\n" +
		"a new directory, with a MANIFEST.json of SHA-256 digests. The directory must not\n" +
		"exist; it appears whole or not at all. No sync identity is copied, so a journal\n" +
		"restored from it joins sync as a new device. Nothing in the journal changes.",
	Args: cobra.ExactArgs(1),
	RunE: runBackup,
}

func runBackup(cmd *cobra.Command, args []string) error {
	path, err := resolveFilePath()
	if err != nil {
		return err
	}
	journal, err := openJournal(path, engine.ReflectionSettings{}, time.Now())
	if err != nil {
		return err
	}
	destination, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	export, err := journal.ExportRecovery(destination, "")
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Backed up to %s: %s", export.Directory, plural(len(export.Files), "file", "files"))
	if n := export.Count("conflict"); n > 0 {
		fmt.Fprintf(out, ", including %s of open conflicts", plural(n, "version", "versions"))
	}
	if n := export.Count("draft") + export.Count("review-draft"); n > 0 {
		fmt.Fprintf(out, " and %s", plural(n, "unsaved draft", "unsaved drafts"))
	}
	fmt.Fprintln(out, ".")
	fmt.Fprintln(out, "It holds your whole journal; keep it private. README.txt in it says how to restore.")
	return nil
}
