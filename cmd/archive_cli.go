package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"os"
)

// commitArchivePaths is the git seam for archive mutations. Tests replace it to
// prove a successful mutation produces exactly one commit.
var commitArchivePaths = gitCommitPaths

func resolveArchivePath() (string, error) {
	path, err := resolveFilePath()
	if err != nil {
		return "", err
	}
	return engine.ArchivePathFor(path), nil
}

// warnArchiveDiagnostics reports parse problems a read tolerated, so a broken
// record cannot silently change what the author sees.
func warnArchiveDiagnostics(archive engine.ReviewArchive) {
	for _, d := range archive.Diagnostics {
		fmt.Fprintf(os.Stderr, "warning: review archive: %s\n", d)
	}
}
