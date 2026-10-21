package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The exchange itself is proven end to end, as separate processes against a
// real repository, by sync_integration_test.go beside main. What is here are
// the small rules those tests cannot pin exactly.

func TestRetryDelayGrowsAndNeverExceedsTheInterval(t *testing.T) {
	t.Parallel()

	poll, interval := 2*time.Second, 5*time.Minute
	previous := time.Duration(0)
	for failures := 1; failures <= 64; failures++ {
		delay := retryDelay(failures, poll, interval)
		if delay < previous {
			t.Fatalf("after %d failures the delay shrank from %s to %s", failures, previous, delay)
		}
		if delay > interval {
			t.Fatalf("after %d failures the delay %s exceeds the interval %s", failures, delay, interval)
		}
		previous = delay
	}
	if got := retryDelay(1, poll, interval); got != poll {
		t.Fatalf("the first retry waits %s, want one poll (%s)", got, poll)
	}
	if got := retryDelay(3, poll, interval); got != 8*time.Second {
		t.Fatalf("the third retry waits %s, want 8s", got)
	}
	if previous != interval {
		t.Fatalf("the delay settles at %s, want the interval %s", previous, interval)
	}
}

func TestLaunchAgentPlistEscapesWhatItIsGiven(t *testing.T) {
	t.Parallel()

	plist := launchAgentPlist("/Apps & Tools/seneca", "/logs/<seneca>.log", map[string]string{
		"HOME":          "/Users/a&b",
		"SENECA_FILE":   "/journal/reflections.md",
		syncExchangeEnv: "",
	})
	for _, want := range []string{
		"<string>/Apps &amp; Tools/seneca</string>",
		"<string>/logs/&lt;seneca&gt;.log</string>",
		"<key>HOME</key>\n\t\t<string>/Users/a&amp;b</string>",
		"<key>SENECA_FILE</key>",
		"<string>sync</string>\n\t\t<string>watch</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("the LaunchAgent does not hold %q:\n%s", want, plist)
		}
	}
	// A variable that is not set is left out, never written empty: an empty
	// SENECA_FILE would read as no override, but an empty PATH would hide git.
	for _, absent := range []string{syncExchangeEnv, "<key>PATH</key>"} {
		if strings.Contains(plist, absent) {
			t.Errorf("the LaunchAgent names %s, which was not given:\n%s", absent, plist)
		}
	}
}

func TestTheExchangeComesFromTheEnvironmentThenTheConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(syncExchangeEnv, "")
	reflections := filepath.Join(t.TempDir(), "reflections.md")

	setup, err := syncSetupFor(home, reflections)
	if err != nil || setup.exchange != "" || setup.transport() != nil {
		t.Fatalf("with nothing configured: exchange=%q transport=%v err=%v", setup.exchange, setup.transport(), err)
	}
	if err := setup.requireExchange(); err == nil || !strings.Contains(err.Error(), "[sync].exchange") {
		t.Fatalf("expected the missing exchange to name its setting, got %v", err)
	}

	cfgPath := configFilePath(home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("[sync]\nexchange = \"~/Cloud/Seneca\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if setup, err = syncSetupFor(home, reflections); err != nil || setup.exchange != filepath.Join(home, "Cloud", "Seneca") {
		t.Fatalf("from config.toml: exchange=%q err=%v", setup.exchange, err)
	}

	t.Setenv(syncExchangeEnv, "/elsewhere")
	if setup, err = syncSetupFor(home, reflections); err != nil || setup.exchange != "/elsewhere" {
		t.Fatalf("from the environment: exchange=%q err=%v", setup.exchange, err)
	}

	t.Setenv(syncExchangeEnv, "")
	if err := os.WriteFile(cfgPath, []byte("[sync]\nexchange = \"relative/path\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = syncSetupFor(home, reflections); err == nil || !strings.Contains(err.Error(), "sync.exchange") {
		t.Fatalf("expected a relative exchange to be refused, got %v", err)
	}
}

func TestTheGitRetryNoteKeepsEveryPathUntilItIsCleared(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	reflections := filepath.Join(dir, "reflections.md")
	if _, found := readGitPending(reflections); found {
		t.Fatal("a journal with no note reports one")
	}

	now := time.Date(2026, time.August, 20, 21, 0, 0, 0, time.UTC)
	if err := noteGitPending(reflections, syncImportCommitTitle, now, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".seneca")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an import that wrote nothing left a note: %v", err)
	}

	archive := filepath.Join(dir, "seneca-reviews.md")
	if err := noteGitPending(reflections, syncImportCommitTitle, now, []string{reflections}); err != nil {
		t.Fatal(err)
	}
	if err := noteGitPending(reflections, syncImportCommitTitle, now.Add(time.Hour), []string{archive, reflections}); err != nil {
		t.Fatal(err)
	}
	pending, found := readGitPending(reflections)
	if !found || !equalStrings(pending.Paths, []string{reflections, archive}) {
		t.Fatalf("note = %+v (found %v), want both paths once each", pending, found)
	}
	if pending.Since != "2026-08-20T21:00:00Z" {
		t.Fatalf("the note is dated %s, want the first import that is still waiting", pending.Since)
	}

	clearGitPending(reflections)
	if _, found := readGitPending(reflections); found {
		t.Fatal("the note survived being cleared")
	}
	if _, err := os.Stat(filepath.Dir(gitPendingPathFor(reflections))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("clearing the note left its directory: %v", err)
	}
}
