package cmd

import (
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// The background service is opt-in and is nothing but the watcher under
// launchd: a LaunchAgent that runs `seneca sync watch` for the logged-in user
// and restarts it if it stops. Removing it removes only that. The journal, its
// history, and every command work exactly as they did, and `seneca sync once`
// exchanges by hand.

const (
	launchAgentLabel = "io.sglavoie.seneca.sync"
	// launchctlEnv names the program to run in place of launchctl. It exists
	// so a test can install and remove the service without touching launchd.
	launchctlEnv = "SENECA_LAUNCHCTL"
)

func init() {
	syncServiceCmd.AddCommand(syncServiceInstallCmd, syncServiceRemoveCmd)
	syncCmd.AddCommand(syncServiceCmd)
}

var syncServiceCmd = &cobra.Command{
	Use:   "service",
	Short: "Install or remove the background sync service",
	Long: "The background service is a macOS LaunchAgent that keeps `seneca sync watch`\n" +
		"running while you are logged in, so changes from your other devices reach the\n" +
		"journal and its Git history with no terminal open. It is never installed\n" +
		"unless you ask.",
}

var syncServiceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install and start the background sync service",
	Args:  cobra.NoArgs,
	RunE:  runSyncServiceInstall,
}

var syncServiceRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Stop and remove the background sync service",
	Long: "Stop the background service and delete its LaunchAgent. The journal and its\n" +
		"sync history are not touched: every command keeps working, and `seneca sync\n" +
		"once` exchanges by hand.",
	Args: cobra.NoArgs,
	RunE: runSyncServiceRemove,
}

func launchAgentPath(home string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("the background service is a macOS LaunchAgent; on this system keep `seneca sync watch` running under a supervisor of your own")
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist"), nil
}

func launchAgentLogPath(home string) string {
	return filepath.Join(home, "Library", "Logs", "seneca-sync.log")
}

func launchctl(args ...string) ([]byte, error) {
	program := os.Getenv(launchctlEnv)
	if program == "" {
		program = "launchctl"
	}
	return exec.Command(program, args...).CombinedOutput()
}

func launchdDomain() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

func plistString(value string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(value))
	return "<string>" + b.String() + "</string>"
}

// launchAgentPlist renders the agent. The environment a LaunchAgent starts
// with is nearly empty, so what the watcher resolves the journal, the
// exchange, and git from is written down here as it is now.
func launchAgentPlist(executable, logPath string, env map[string]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	` + plistString(launchAgentLabel) + `
	<key>ProgramArguments</key>
	<array>
		` + plistString(executable) + `
		<string>sync</string>
		<string>watch</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
`)
	for _, key := range []string{"HOME", "PATH", "SENECA_FILE", syncExchangeEnv} {
		if value := env[key]; value != "" {
			b.WriteString("\t\t<key>" + key + "</key>\n\t\t" + plistString(value) + "\n")
		}
	}
	b.WriteString(`	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>30</integer>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	` + plistString(logPath) + `
	<key>StandardErrorPath</key>
	` + plistString(logPath) + `
</dict>
</plist>
`)
	return b.String()
}

func runSyncServiceInstall(cmd *cobra.Command, args []string) error {
	setup, err := resolveSyncSetup()
	if err != nil {
		return err
	}
	plistPath, err := launchAgentPath(setup.home)
	if err != nil {
		return err
	}
	if err := setup.requireExchange(); err != nil {
		return err
	}
	if _, enrolled, err := engine.LoadSyncStatus(setup.reflections); err != nil {
		return err
	} else if !enrolled {
		return fmt.Errorf("%s does not take part in sync yet; run `seneca sync enroll` first (nothing was installed)", setup.reflections)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding the seneca program to run: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}

	logPath := launchAgentLogPath(setup.home)
	plist := launchAgentPlist(executable, logPath, map[string]string{
		"HOME":          setup.home,
		"PATH":          os.Getenv("PATH"),
		"SENECA_FILE":   os.Getenv("SENECA_FILE"),
		syncExchangeEnv: os.Getenv(syncExchangeEnv),
	})
	for _, dir := range []string{filepath.Dir(plistPath), filepath.Dir(logPath)} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := journalFS.AtomicWriteFile(plistPath, []byte(plist), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", plistPath, err)
	}

	// Reinstalling replaces a running agent; one that is not loaded is not an
	// error to stop.
	_, _ = launchctl("bootout", launchdDomain()+"/"+launchAgentLabel)
	if out, err := launchctl("bootstrap", launchdDomain(), plistPath); err != nil {
		return fmt.Errorf("the LaunchAgent was written to %s but launchd did not start it: %v: %s", plistPath, err, strings.TrimSpace(string(out)))
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Background sync installed: %s\n", plistPath)
	fmt.Fprintf(out, "It runs %s sync watch, and logs to %s\n", executable, logPath)
	fmt.Fprintln(out, "Remove it with `seneca sync service remove`.")
	return nil
}

func runSyncServiceRemove(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}
	plistPath, err := launchAgentPath(home)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if _, err := os.Stat(plistPath); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(out, "Background sync is not installed; nothing to remove.")
		return nil
	}
	// Stopping an agent launchd does not have loaded fails, and leaves exactly
	// what is wanted.
	_, _ = launchctl("bootout", launchdDomain()+"/"+launchAgentLabel)
	if err := os.Remove(plistPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", plistPath, err)
	}
	fmt.Fprintf(out, "Background sync removed: %s\n", plistPath)
	fmt.Fprintln(out, "The journal still works as before; run `seneca sync once` to exchange by hand.")
	return nil
}
