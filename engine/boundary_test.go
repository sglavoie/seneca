package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The engine is the half of Seneca that also runs on a phone. These tests hold
// the boundary docs/mobile/engine-and-bridge.md describes: nothing it links
// can draw a terminal, parse a command line, or start another program, and
// nothing in it reaches for the environment, the wall clock, or the terminal
// behind its caller's back.

func TestEngineDependsOnTheStandardLibraryOnly(t *testing.T) {
	t.Parallel()

	command := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("listing the engine's dependencies: %v", err)
	}
	for _, path := range strings.Fields(string(output)) {
		if path != "github.com/sglavoie/seneca/engine" {
			t.Errorf("the engine depends on %s; only the standard library may be linked into the mobile-bound tree", path)
		}
	}
}

func TestEngineLinksNoTerminalOrSubprocessCode(t *testing.T) {
	t.Parallel()

	output, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("listing the engine's dependencies: %v", err)
	}
	deps := strings.Fields(string(output))
	if len(deps) < 10 {
		t.Fatalf("expected a full dependency listing, got %q", deps)
	}

	forbidden := map[string]string{
		"os/exec":                        "desktop subprocesses: git, the editor, the AI harness",
		"os/signal":                      "process control belongs to the desktop adapter",
		"charm.land/":                    "terminal UI",
		"github.com/charmbracelet":       "terminal UI",
		"github.com/spf13/":              "the command-line framework",
		"github.com/BurntSushi/":         "machine configuration loading",
		"github.com/sglavoie/seneca/cmd": "the desktop adapter itself",
	}
	for _, dep := range deps {
		for prefix, why := range forbidden {
			if dep == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(dep, prefix) {
				t.Errorf("the engine links %s (%s)", dep, why)
			}
		}
	}
}

// ambientRe matches the ways code finds something out for itself instead of
// being told: the wall clock, the environment, the home directory, the
// terminal's streams, and printing.
var ambientRe = regexp.MustCompile(`\btime\.Now\(|\bos\.(Getenv|LookupEnv|Environ|UserHomeDir|UserConfigDir|Getwd|Stdin|Stdout|Stderr|Args)\b|\bfmt\.(Print|Printf|Println)\(`)

func TestEngineReadsNoAmbientState(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package: %v", err)
	}
	scanned := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		scanned++
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		for number, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if found := ambientRe.FindString(line); found != "" {
				t.Errorf("%s:%d uses %s; the engine takes its clock, paths, and output from its caller", file, number+1, strings.TrimSuffix(found, "("))
			}
		}
	}
	if scanned == 0 {
		t.Fatal("found no engine source to scan")
	}
}

// A package-level variable that is assigned after declaration is process-wide
// state. The engine keeps none: the filesystem seams travel with each journal.
func TestEngineHoldsNoSwappableGlobals(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("persistence.go")
	if err != nil {
		t.Fatalf("reading persistence.go: %v", err)
	}
	if regexp.MustCompile(`(?m)^var \w+ FS\b|^func init\(`).Match(data) {
		t.Fatal("persistence.go declares a package-level filesystem; FS must be carried by each journal and transaction")
	}
}
