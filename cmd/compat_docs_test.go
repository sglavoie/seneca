package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The pages under docs/mobile are contracts later work is held to, so they are
// held to the code in turn: a write path or a command that appears in one place
// has to appear in the other.

const compatDocsDir = "../docs/mobile"

func compatDoc(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(compatDocsDir, name))
	if err != nil {
		t.Fatalf("reading contract page %s: %v", name, err)
	}
	return string(data)
}

func TestCompatContractPagesExistAndAreLinked(t *testing.T) {
	t.Parallel()

	index := compatDoc(t, "README.md")
	for _, page := range []string{
		"write-path-inventory.md",
		"engine-and-bridge.md",
		"bridge.md",
		"journal-layout.md",
		"sync-invariants.md",
		"sync-format.md",
		"icloud-transport.md",
		"cli-compatibility.md",
	} {
		if strings.TrimSpace(compatDoc(t, page)) == "" {
			t.Errorf("contract page %s is empty", page)
		}
		if !strings.Contains(index, "("+page+")") {
			t.Errorf("docs/mobile/README.md does not link %s", page)
		}
	}
	if !strings.Contains(readmeText(t), "(docs/mobile/README.md)") {
		t.Error("README does not link the mobile contracts")
	}

	// The index describes the fixtures; a fixture it does not name is one
	// nobody knows the purpose of.
	entries, err := os.ReadDir(compatFixtureDir)
	if err != nil {
		t.Fatalf("listing %s: %v", compatFixtureDir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		if !strings.Contains(index, "`"+name) {
			t.Errorf("docs/mobile/README.md does not describe the fixture %s", name)
		}
	}
}

// mutationCallRe matches every call through which production code changes
// something outside its own memory: the two transaction commits, direct
// filesystem mutations, and launching another program.
var mutationCallRe = regexp.MustCompile(`\.CommitEntries\(|\.AppendRecords\(|\bos\.(Remove|RemoveAll|MkdirAll|Mkdir|Rename|WriteFile|OpenFile|Create|CreateTemp)\b|\bexec\.Command(Context)?\(`)

// compatNonJournalWriters are the files whose only matches cannot reach a
// journal: they manage the review harness's own process tree, probe for a
// repository, or raise a notification.
var compatNonJournalWriters = map[string]string{
	"cmd/doctor.go":                         "`git rev-parse --git-dir`, a read",
	"cmd/review_harness_process_unix.go":    "`ps`, to find the harness's descendants",
	"cmd/review_harness_process_windows.go": "`taskkill` and a process listing, for the same reason",
	"cmd/testnotify.go":                     "`osascript`, to show an alert",
}

func TestCompatWritePathInventoryNamesEveryMutatingFile(t *testing.T) {
	t.Parallel()

	inventory := compatDoc(t, "write-path-inventory.md")

	// Both halves are scanned: the commands, which decide when a journal is
	// written, and the engine, which does the writing. Files are named the way
	// the inventory names them, relative to the module root.
	found := map[string]bool{}
	for _, pkg := range []string{"cmd", "engine"} {
		files, err := filepath.Glob(filepath.Join("..", pkg, "*.go"))
		if err != nil {
			t.Fatalf("listing package %s: %v", pkg, err)
		}
		sort.Strings(files)
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "//") || !mutationCallRe.MatchString(line) {
					continue
				}
				found[pkg+"/"+filepath.Base(path)] = true
				break
			}
		}
	}

	for file := range found {
		if _, exempt := compatNonJournalWriters[file]; exempt {
			continue
		}
		if !strings.Contains(inventory, "`"+file+"`") {
			t.Errorf("%s changes something on disk or runs a program, and the write-path inventory does not name it", file)
		}
	}
	for file, why := range compatNonJournalWriters {
		if !found[file] {
			t.Errorf("%s is exempted from the inventory (%s) but no longer matches; drop the exemption", file, why)
		}
	}
	// The inventory must not outlive the code it describes either.
	for _, match := range regexp.MustCompile("`((?:cmd|engine)/[a-z_]+\\.go)`").FindAllStringSubmatch(inventory, -1) {
		if _, err := os.Stat(filepath.Join("..", match[1])); err != nil {
			t.Errorf("the write-path inventory names %s, which does not exist", match[1])
		}
	}
}

func TestCompatCLIPageAccountsForEveryCommandAndGolden(t *testing.T) {
	t.Parallel()

	page := compatDoc(t, "cli-compatibility.md")

	for path := range commandSurface {
		if !strings.Contains(page, "| `"+path+"` |") {
			t.Errorf("cli-compatibility.md has no row for %q", path)
		}
	}

	goldens, err := os.ReadDir(filepath.Join(compatFixtureDir, "golden"))
	if err != nil {
		t.Fatalf("listing the golden files: %v", err)
	}
	for _, golden := range goldens {
		if !strings.Contains(page, "| `"+golden.Name()+"` |") {
			t.Errorf("cli-compatibility.md does not say which command %s freezes", golden.Name())
		}
	}
	for _, tc := range compatReadOnlyCommands {
		if want := "`seneca " + strings.Join(tc.args, " ") + "`"; !strings.Contains(page, want) {
			t.Errorf("cli-compatibility.md does not list %s beside %s", want, tc.golden)
		}
	}
}

// The layout page and journal_layout.go state the same paths.
func TestCompatLayoutPageMatchesTheLayoutCode(t *testing.T) {
	t.Parallel()

	page := compatDoc(t, "journal-layout.md")

	for _, want := range []string{
		"`" + engine.ArchiveFileName + "`",
		"`" + engine.JournalMetadataRootName + "/" + engine.JournalMetadataJournalsName + "/",
		"`" + engine.JournalMetadataRootName + "/" + engine.JournalMetadataDirectoryName + "/`",
		"`engine.ArchivePathFor`", "`engine.JournalMetadataDirFor`", "`engine.DirectoryMetadataDirFor`",
		"`SENECA_FILE`", "`seneca.reflections_path`",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("journal-layout.md does not mention %s", want)
		}
	}
}

// The layout page names every path the engine derives under `.seneca/`.
func TestCompatLayoutPageNamesTheLockCommitAndDraftPaths(t *testing.T) {
	t.Parallel()

	page := compatDoc(t, "journal-layout.md")
	reflections := filepath.Join("d", "reflections.md")
	rel := func(path string) string {
		out, err := filepath.Rel("d", path)
		if err != nil {
			t.Fatalf("relating %s: %v", path, err)
		}
		return filepath.ToSlash(out)
	}

	for _, want := range []string{
		"`" + rel(engine.LockPathFor("d")) + "`",
		"`" + rel(engine.CommitDirFor("d")) + "/`",
		"`" + strings.Replace(rel(engine.DraftPathFor(reflections, "<date>")), "reflections.md", "<reflections filename>", 1) + "`",
		"`" + rel(engine.ReviewDraftPathFor(reflections, "<period>", "<start>", "<end>")) + "`",
		"`" + strings.Replace(rel(engine.SettingsPathFor(reflections)), "reflections.md", "<reflections filename>", 1) + "`",
		"`" + strings.Replace(rel(engine.SyncStatePathFor(reflections)), "reflections.md", "<reflections filename>", 1) + "`",
		"`" + rel(engine.SyncClaimPathFor("d")) + "`",
		"`engine.LockPathFor`", "`engine.CommitDirFor`", "`engine.DraftPathFor`", "`engine.ReviewDraftPathFor`", "`engine.SettingsPathFor`",
		"`engine.SyncStatePathFor`", "`engine.SyncDirFor`", "`engine.SyncClaimPathFor`",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("journal-layout.md does not mention %s", want)
		}
	}
}

// The layout page names the desktop's own files under `.seneca/`, which the
// command layer derives rather than the engine.
func TestCompatLayoutPageNamesTheDesktopPaths(t *testing.T) {
	t.Parallel()

	page := compatDoc(t, "journal-layout.md")
	reflections := filepath.Join("d", "reflections.md")
	for path, helper := range map[string]string{
		gitPendingPathFor(reflections):  "`gitPendingPathFor` in `cmd/sync_cycle.go`",
		usageLedgerPathFor(reflections): "`usageLedgerPathFor` in `cmd/review_usage.go`",
	} {
		rel, err := filepath.Rel("d", path)
		if err != nil {
			t.Fatalf("relating %s: %v", path, err)
		}
		want := "`" + strings.Replace(filepath.ToSlash(rel), "reflections.md", "<reflections filename>", 1) + "`"
		for _, mention := range []string{want, helper} {
			if !strings.Contains(page, mention) {
				t.Errorf("journal-layout.md does not mention %s", mention)
			}
		}
	}
}
