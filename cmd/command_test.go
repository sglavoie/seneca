package cmd

import (
	"bytes"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The command surface is the contract with the author's fingers: a renamed
// command or a dropped flag breaks a habit and a crontab. This table is the
// whole surface, and it is meant to be edited deliberately.
var commandSurface = map[string][]string{
	"seneca":                      {"blank", "date"},
	"seneca backup":               nil,
	"seneca delete":               {"date", "yes"},
	"seneca doctor":               {"diagnostics"},
	"seneca export":               {"from", "month", "to"},
	"seneca focus":                {"date"},
	"seneca focus clear":          nil,
	"seneca focus history":        {"last"},
	"seneca focus set":            nil,
	"seneca insights":             {"all", "days", "from", "to"},
	"seneca merge":                {"delete"},
	"seneca open":                 nil,
	"seneca pack":                 nil,
	"seneca pack apply":           nil,
	"seneca pack export":          nil,
	"seneca pack list":            nil,
	"seneca pack show":            nil,
	"seneca review":               {"effort", "harness", "model", "month", "quarter", "timeout", "week", "year"},
	"seneca review history":       {"last", "period"},
	"seneca settings":             nil,
	"seneca settings init":        nil,
	"seneca settings set":         {"anchor", "question"},
	"seneca show":                 {"date", "from", "last", "month", "on-this-day", "random", "search", "to"},
	"seneca sort":                 nil,
	"seneca sync":                 nil,
	"seneca sync conflicts":       nil,
	"seneca sync enroll":          {"join", "name"},
	"seneca sync leave":           nil,
	"seneca sync once":            nil,
	"seneca sync recover":         {"accept-deletions", "restore"},
	"seneca sync resolve":         {"clear", "file", "keep", "text"},
	"seneca sync service":         nil,
	"seneca sync service install": nil,
	"seneca sync service remove":  nil,
	"seneca sync status":          nil,
	"seneca sync watch":           {"interval", "poll"},
	"seneca test-notification":    nil,
	"seneca usage":                {"json"},
}

// walkCommands returns every command in the tree keyed by its full path.
func walkCommands(root *cobra.Command) map[string]*cobra.Command {
	found := map[string]*cobra.Command{}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		found[c.CommandPath()] = c
		for _, child := range c.Commands() {
			if child.Name() == "help" || child.Name() == "completion" {
				continue
			}
			walk(child)
		}
	}
	walk(root)
	return found
}

func localFlagNames(c *cobra.Command) []string {
	var names []string
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		names = append(names, f.Name)
	})
	sort.Strings(names)
	return names
}

func TestEveryCommandAndFlagIsAccountedFor(t *testing.T) {
	commands := walkCommands(rootCmd)

	for path, want := range commandSurface {
		cmd, ok := commands[path]
		if !ok {
			t.Errorf("command %q is missing from the tree", path)
			continue
		}
		if got := localFlagNames(cmd); !equalStrings(got, want) {
			t.Errorf("%q flags = %v, want %v", path, got, want)
		}
	}
	for path := range commands {
		if _, expected := commandSurface[path]; !expected {
			t.Errorf("command %q exists but is not listed in commandSurface; add it here and to the README", path)
		}
	}
}

func TestEveryCommandDocumentsAndValidatesItself(t *testing.T) {
	for path, cmd := range walkCommands(rootCmd) {
		if strings.TrimSpace(cmd.Short) == "" {
			t.Errorf("%q has no Short description", path)
		}
		if cmd.Args == nil && cmd.HasSubCommands() {
			// A pure group delegates argument checking to its children.
			continue
		}
		if cmd.Args == nil {
			t.Errorf("%q accepts any arguments; give it an explicit Args validator", path)
		}
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if strings.TrimSpace(f.Usage) == "" {
				t.Errorf("%q flag --%s has no usage text", path, f.Name)
			}
		})
	}
}

// helpFor renders a command's help exactly as `--help` would, without running
// anything. Cobra writes help to the command's own writer, so nothing here
// touches the real stdout.
func helpFor(t *testing.T, cmd *cobra.Command) string {
	t.Helper()

	var out bytes.Buffer
	cmd.SetOut(&out)
	t.Cleanup(func() { cmd.SetOut(nil) })
	if err := cmd.Help(); err != nil {
		t.Fatalf("rendering help for %q: %v", cmd.CommandPath(), err)
	}
	return out.String()
}

func TestHelpListsEveryCommandAndFlag(t *testing.T) {
	rootHelp := helpFor(t, rootCmd)
	for path := range commandSurface {
		name := strings.TrimPrefix(path, "seneca ")
		if name == "seneca" || strings.Contains(name, " ") {
			continue // the root itself, and subcommands listed by their parent
		}
		if !strings.Contains(rootHelp, name) {
			t.Errorf("root help does not mention %q:\n%s", name, rootHelp)
		}
	}

	for path, cmd := range walkCommands(rootCmd) {
		help := helpFor(t, cmd)
		for _, flag := range commandSurface[path] {
			if !strings.Contains(help, "--"+flag) {
				t.Errorf("help for %q does not mention --%s:\n%s", path, flag, help)
			}
		}
	}
}

// readmeText returns the README beside the module, which is the author-facing
// half of the contract commandSurface guards: the surface test's failure
// message says "add it here and to the README", and this is what makes the
// second half fail too instead of relying on someone remembering.
func readmeText(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatalf("reading README: %v", err)
	}
	return string(data)
}

// documentationText is the README plus the pages under docs/ it links to, which
// together are the author-facing documentation.
func documentationText(t *testing.T) string {
	t.Helper()

	pages, err := filepath.Glob(filepath.Join("..", "docs", "*.md"))
	if err != nil {
		t.Fatalf("listing docs: %v", err)
	}
	text := readmeText(t)
	for _, page := range pages {
		data, err := os.ReadFile(page)
		if err != nil {
			t.Fatalf("reading %s: %v", page, err)
		}
		text += "\n" + string(data)
	}
	return text
}

func TestREADMEDocumentsEveryCommandAndFlag(t *testing.T) {
	readme := documentationText(t)

	for path, flags := range commandSurface {
		if path != "seneca" && !strings.Contains(readme, path) {
			t.Errorf("README does not document the command %q", path)
		}
		for _, flag := range flags {
			if !strings.Contains(readme, "--"+flag) {
				t.Errorf("README does not document --%s of %q", flag, path)
			}
		}
	}
}

// repositoryRootMarkers name the entries this repository keeps at its own root
// and nowhere below it. `.git` is the one a checkout has, and requiring it was
// what made this test fatal for a copy holding only tracked source — a source
// archive, a vendored tree, a module cache entry — which has no `.git`
// anywhere. The other two are tracked, so they travel with such a copy.
var repositoryRootMarkers = []string{".git", ".githooks", ".markdownlint.json"}

// repositoryRootAbove returns the nearest ancestor of dir, dir itself included,
// that carries one of those markers. Nearest rather than topmost is deliberate:
// a copy extracted underneath an unrelated checkout must bind to its own root
// rather than to the outer repository that happens to contain it.
func repositoryRootAbove(dir string) (string, bool) {
	for {
		for _, marker := range repositoryRootMarkers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// moduleDirFromRepositoryRoot returns the module's own directory as a slash
// path relative to the repository root, which is where a reader following the
// install block is standing. Derived rather than written down, so moving the
// module fails the README test instead of silently outdating it. The false
// return means there is no repository around this module at all, which makes a
// root-relative install directory unknowable rather than wrong.
func moduleDirFromRepositoryRoot(t *testing.T) (string, bool) {
	t.Helper()

	module, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving the module directory: %v", err)
	}
	root, ok := repositoryRootAbove(module)
	if !ok {
		return "", false
	}
	rel, err := filepath.Rel(root, module)
	if err != nil {
		t.Fatalf("relating %s to the repository root %s: %v", module, root, err)
	}
	return filepath.ToSlash(rel), true
}

// installSequenceIssues reports what is wrong with an install block for a module
// living at moduleDir relative to the repository root. A reader has to change
// into the module directory before `just install` resolves, so the line and its
// position both matter; taking the README as an argument is what lets the two
// refusals be proven against a written-down block rather than only against this
// repository's own README.
func installSequenceIssues(readme, moduleDir string) []string {
	changeDir, installedAt := -1, -1
	for i, line := range strings.Split(readme, "\n") {
		switch {
		case changeDir < 0 && strings.TrimSpace(line) == "cd "+moduleDir:
			changeDir = i
		case installedAt < 0 && strings.Contains(line, "just install"):
			installedAt = i
		}
	}

	var issues []string
	if changeDir < 0 {
		issues = append(issues, fmt.Sprintf("README does not say `cd %s`; the module lives there and `just install` does not resolve from the repository root without it", moduleDir))
	}
	if changeDir >= 0 && installedAt >= 0 && changeDir > installedAt {
		issues = append(issues, fmt.Sprintf("README runs `just install` (line %d) before changing into %s (line %d)", installedAt+1, moduleDir, changeDir+1))
	}
	return issues
}

// The install directory, the recipe and the Go requirement are the three lines
// a reader follows before anything else works, and each of them went stale
// once. Pin them to what the repository actually contains.
func TestREADMEInstallInstructionsMatchTheRepository(t *testing.T) {
	readme := readmeText(t)

	if strings.Contains(readme, "make install") {
		t.Error("README still says `make install`; the build is driven by the justfile")
	}
	if !strings.Contains(readme, "just install") {
		t.Error("README does not name `just install`, the recipe that installs Seneca")
	}

	if moduleDir, ok := moduleDirFromRepositoryRoot(t); ok {
		for _, issue := range installSequenceIssues(readme, moduleDir) {
			t.Error(issue)
		}
	} else {
		t.Logf("no repository root above the module (none of %s); the install directory is stated relative to one, so only the sequence rules below are checked", strings.Join(repositoryRootMarkers, ", "))
	}

	gomod, err := os.ReadFile("../go.mod")
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	version := ""
	for _, line := range strings.Split(string(gomod), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "go "); ok {
			version = strings.TrimSpace(rest)
			break
		}
	}
	if version == "" {
		t.Fatalf("go.mod declares no go directive:\n%s", gomod)
	}
	if want := "Go " + version; !strings.Contains(readme, want) {
		t.Errorf("README does not state the Go requirement %q that go.mod declares", want)
	}

	// The check above only fires when this repository's README is wrong, which
	// it is not; these written-down blocks are what prove it still refuses a
	// wrong directory and a recipe that runs before the reader has moved.
	t.Run("the sequence rules refuse a broken install block", func(t *testing.T) {
		cases := []struct {
			name   string
			block  string
			refuse string
		}{
			{
				name:   "the correct block",
				block:  "```bash\ncd some/module\njust install\n```\n",
				refuse: "",
			},
			{
				name:   "a cd into the wrong directory",
				block:  "```bash\ncd module\njust install\n```\n",
				refuse: "does not say `cd some/module`",
			},
			{
				name:   "just install before the cd",
				block:  "```bash\njust install\ncd some/module\n```\n",
				refuse: "before changing into some/module",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				issues := installSequenceIssues(tc.block, "some/module")
				if tc.refuse == "" {
					if len(issues) > 0 {
						t.Errorf("a correct install block was refused: %s", strings.Join(issues, "; "))
					}
					return
				}
				if len(issues) != 1 {
					t.Fatalf("want exactly one issue naming %q, got %v", tc.refuse, issues)
				}
				if !strings.Contains(issues[0], tc.refuse) {
					t.Errorf("issue %q does not name %q", issues[0], tc.refuse)
				}
			})
		}
	})
}

func TestCommandsRejectStrayArguments(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"doctor", "please"}, "unknown command"},
		// Positional dates are refused before the required flags are checked,
		// so `seneca export 2026-09-04 2026-09-05` reports the argument rather
		// than sending the author off to set flags they did try to express.
		{[]string{"export", "2026-09-04", "2026-09-05"}, "unknown command"},
		{[]string{"focus", "history", "extra"}, "unknown command"},
		{[]string{"focus", "set"}, "requires at least 1 arg"},
		{[]string{"merge"}, "accepts 1 arg"},
		{[]string{"review", "history", "extra"}, "unknown command"},
		{[]string{"sort", "now"}, "unknown command"},
	}

	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var out bytes.Buffer
			rootCmd.SetOut(&out)
			rootCmd.SetErr(&out)
			rootCmd.SetArgs(tc.args)
			t.Cleanup(func() {
				rootCmd.SetOut(nil)
				rootCmd.SetErr(nil)
				rootCmd.SetArgs(nil)
			})

			err := rootCmd.Execute()
			if err == nil {
				t.Fatalf("expected %v to be refused, got success:\n%s", tc.args, out.String())
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %v to report %q, got %v", tc.args, tc.want, err)
			}
		})
	}
}

func TestRuntimeErrorsPrintOnceWithoutUsage(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"show", "--last", "2", "--random"})
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		showLast, showRandom = 0, false
		showCmd.SilenceUsage = false
	})

	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected conflicting show flags to fail")
	}
	if out.Len() != 0 {
		t.Fatalf("expected cobra to print nothing (main prints the error), got:\n%s", out.String())
	}
}

func TestShowRefusesNonPositiveLast(t *testing.T) {
	for _, value := range []string{"0", "-3"} {
		t.Run(value, func(t *testing.T) {
			var out bytes.Buffer
			rootCmd.SetOut(&out)
			rootCmd.SetErr(&out)
			rootCmd.SetArgs([]string{"show", "--last=" + value})
			t.Cleanup(func() {
				rootCmd.SetOut(nil)
				rootCmd.SetErr(nil)
				rootCmd.SetArgs(nil)
				showLast = 0
				showCmd.Flags().Lookup("last").Changed = false
				showCmd.SilenceUsage = false
			})

			err := rootCmd.Execute()
			if err == nil {
				t.Fatalf("expected --last=%s to be refused", value)
			}
			if want := "invalid --last " + value + ": expected a positive number"; err.Error() != want {
				t.Fatalf("got %q, want %q", err.Error(), want)
			}
		})
	}
}

func TestNearestEntriesHintNamesTheClosestDatesOnEachSide(t *testing.T) {
	entries := []engine.ReflectionEntry{
		{Date: "2026-09-10"}, {Date: "2026-08-01"}, {Date: ""}, {Date: "2026-11-02"}, {Date: "2026-10-20"},
	}
	cases := []struct {
		from, to, want string
	}{
		{"2026-09-11", "2026-09-11", " Nearest: previous 2026-09-10 (yesterday), next 2026-10-20 (in 39 days)."},
		{"2026-10-01", "2026-10-31", " Nearest: previous 2026-09-10 (yesterday), next 2026-11-02 (in 52 days)."},
		{"2026-07-01", "2026-07-31", " Nearest: next 2026-08-01 (41 days ago)."},
		{"2026-12-01", "2026-12-31", " Nearest: previous 2026-11-02 (in 52 days)."},
	}
	for _, tc := range cases {
		if got := nearestEntriesHint(entries, tc.from, tc.to, "2026-09-11"); got != tc.want {
			t.Errorf("nearestEntriesHint(%s, %s) = %q, want %q", tc.from, tc.to, got, tc.want)
		}
	}
	if got := nearestEntriesHint(nil, "2026-09-11", "2026-09-11", "2026-09-11"); got != "" {
		t.Errorf("expected no hint for an empty journal, got %q", got)
	}
}

func TestReviewHarnessCompletesKnownNames(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{cobra.ShellCompRequestCmd, "review", "--harness", "c"})
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("completion failed: %v", err)
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	want := []string{"claude", "copilot", "codex", "anthropic", "openai", fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp)}
	if len(got) < len(want) || strings.Join(got[:len(want)], "\n") != strings.Join(want, "\n") {
		t.Fatalf("expected completions %q, got:\n%s", want, out.String())
	}
}
