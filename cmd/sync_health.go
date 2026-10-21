package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"runtime"
)

// Sync health on the desktop: the engine's content-free findings, said in the
// command line's words with the command that deals with each. Doctor reports
// them under `sync`, `seneca sync status` repeats the ones it does not already
// show, and `seneca doctor --diagnostics` prints them as data to share.

const doctorCheckSync = "sync"

// syncAdvice says what a finding means here and what to do about it.
func syncAdvice(finding engine.DiagnosticFinding) string {
	n := finding.Count
	switch finding.Code {
	case engine.FindingSyncStateNewer:
		return "the sync state was written by a newer seneca; nothing is exchanged until seneca is updated on this Mac (or run `seneca sync leave` and join again)"
	case engine.FindingSyncStateUnreadable:
		return "the sync state cannot be read; nothing is exchanged. Run `seneca sync leave`, then `seneca sync enroll --join`: the Markdown is kept and this Mac rejoins as a new device"
	case engine.FindingExchangeForeign:
		return "the exchange folder carries another journal; point [sync].exchange at this journal's folder"
	case engine.FindingExchangeNewer:
		return "the exchange folder was started by a newer seneca; update seneca on this Mac"
	case engine.FindingExchangeUnreachable:
		return "the exchange folder cannot be read; everything is saved here and is sent once it can be (check that iCloud Drive is signed in and the folder exists)"
	case engine.FindingExchangeEmpty:
		return "the exchange folder holds no journal yet; `seneca sync once` publishes this one"
	case engine.FindingIdentityReused:
		return fmt.Sprintf("%s in the exchange carry this Mac's device identity but were not recorded here: another installation was restored from a copy of this one. "+
			"On the copy, run `seneca sync leave`, then `seneca sync enroll --join`", plural(n, "change", "changes"))
	case engine.FindingChangesIncompatible:
		return fmt.Sprintf("%s from another device use a newer sync protocol and wait; update seneca on this Mac to read them", plural(n, "change", "changes"))
	case engine.FindingChangesUnpublished:
		return fmt.Sprintf("%s recorded here not in the exchange yet; `seneca sync once` sends them", plural(n, "change", "changes"))
	case engine.FindingChangesUnimported:
		return fmt.Sprintf("%s from other devices not imported yet; `seneca sync once` brings them in", plural(n, "change", "changes"))
	case engine.FindingChangesWaiting:
		return fmt.Sprintf("%s held until the changes they follow arrive; nothing to do", plural(n, "change", "changes"))
	case engine.FindingChangesBlocked:
		return fmt.Sprintf("%s cannot be applied to the files as they are; `seneca sync status` says why", plural(n, "change", "changes"))
	case engine.FindingFileSuspended:
		return fmt.Sprintf("sync stopped reading %s that went missing or lost most of its content; `seneca sync status` names it and `seneca sync recover` decides",
			plural(n, "journal file", "journal files"))
	case engine.FindingConflictsOpen:
		return fmt.Sprintf("%s wait for you to choose; `seneca sync conflicts` shows them", plural(n, "conflict", "conflicts"))
	case engine.FindingCommitInterrupted:
		return "an interrupted save waits; the next command that writes completes it"
	case engine.FindingReflectionsInvalid, engine.FindingReflectionsUnreadable:
		return "the reflections file has problems; see the reflections lines above"
	case engine.FindingArchiveInvalid:
		return "the review archive has problems; see the archive lines above"
	case engine.FindingSettingsInvalid:
		return "the journal's settings cannot be read; see the config lines above"
	}
	return finding.Code
}

func doctorSeverityOf(severity engine.DiagnosticSeverity) doctorSeverity {
	switch severity {
	case engine.DiagnosticError:
		return doctorError
	case engine.DiagnosticWarn:
		return doctorWarn
	}
	return doctorInfo
}

// doctorSyncSetup resolves the exchange the way sync does, from doctor's
// injected environment.
func doctorSyncSetup(env doctorEnv, path string) (syncSetup, error) {
	setup := syncSetup{home: env.home, reflections: path, exchange: env.getenv(syncExchangeEnv)}
	if setup.exchange == "" {
		exchange, err := loadConfiguredSyncExchange(env.home)
		if err != nil {
			return syncSetup{}, err
		}
		setup.exchange = exchange
	}
	return setup, nil
}

// checkDoctorSync reports where sync stands, read-only: it lists and reads the
// exchange and requests nothing from iCloud.
func checkDoctorSync(env doctorEnv, path string, report *doctorReport) {
	setup, err := doctorSyncSetup(env, path)
	if err != nil {
		report.add(doctorCheckSync, doctorError, "%v", err)
		return
	}
	diagnostics := engine.CollectDiagnostics(path, setup.transport())
	status, enrolled, statusErr := engine.LoadSyncStatus(path)
	switch {
	case statusErr == nil && !enrolled:
		// A journal outside sync reads as it always has; one with a folder
		// configured is told how to start.
		if setup.exchange != "" {
			report.add(doctorCheckSync, doctorInfo, "an exchange folder is configured and this journal does not take part in sync yet; `seneca sync enroll` starts, `--join` joins")
		}
		return
	case statusErr == nil:
		report.add(doctorCheckSync, doctorOK, "journal %s; this device is %s; %s recorded here",
			engine.ShortRecordID(status.Journal), describeDevice(status), plural(status.Sequence, "change", "changes"))
		if setup.exchange == "" {
			report.add(doctorCheckSync, doctorWarn, "no exchange folder is configured; changes are recorded and wait (set [sync].exchange in %s)", configFilePath(env.home))
		}
	}
	quiet := true
	for _, finding := range diagnostics.Findings {
		switch finding.Code {
		case engine.FindingReflectionsInvalid, engine.FindingReflectionsUnreadable, engine.FindingArchiveInvalid, engine.FindingSettingsInvalid:
			// Already reported by their own checks.
			continue
		}
		quiet = quiet && finding.Severity == engine.DiagnosticInfo
		report.add(doctorCheckSync, doctorSeverityOf(finding.Severity), "%s", syncAdvice(finding))
	}
	if pending, found := readGitPending(path); found {
		quiet = false
		report.add(doctorCheckSync, doctorWarn, "imported changes since %s are not committed to Git yet; the next sync retries", pending.Since)
	}
	if enrolled && setup.exchange != "" && diagnostics.Sync.Exchange.Found && quiet &&
		diagnostics.Sync.Exchange.Unpublished == 0 && diagnostics.Sync.Exchange.Unimported == 0 {
		report.add(doctorCheckSync, doctorOK, "nothing waits to be sent or imported")
	}
}

// sharedDiagnostics is what `seneca doctor --diagnostics` prints: the engine's
// content-free report and the program around it. It names no path, no
// question, no date, and nothing written in the journal.
type sharedDiagnostics struct {
	engine.Diagnostics
	Program struct {
		Platform   string `json:"platform"`
		Git        bool   `json:"git"`
		Harness    bool   `json:"harness"`
		Exchange   bool   `json:"exchange"`
		Service    bool   `json:"service"`
		GitPending bool   `json:"git_pending"`
	} `json:"program"`
}

func writeSharedDiagnostics(out io.Writer, env doctorEnv) error {
	path, err := resolveFilePathForHome(env.home, env.getenv)
	if err != nil {
		return errors.New("the reflections path cannot be resolved; run `seneca doctor` for the reason")
	}
	setup, err := doctorSyncSetup(env, path)
	if err != nil {
		setup = syncSetup{home: env.home, reflections: path}
	}
	shared := sharedDiagnostics{Diagnostics: engine.CollectDiagnostics(path, setup.transport())}
	shared.Program.Platform = runtime.GOOS + "/" + runtime.GOARCH
	_, gitErr := env.lookPath("git")
	shared.Program.Git = gitErr == nil
	var review reviewSettings
	if cfg, err := loadConfigFile(env.home); err == nil {
		review, _ = cfg.Review.reviewSettings()
	}
	configured, _ := doctorHarnessSource(env, review)
	shared.Program.Harness = configured != ""
	shared.Program.Exchange = setup.exchange != ""
	if plist, err := launchAgentPath(env.home); err == nil {
		_, statErr := os.Stat(plist)
		shared.Program.Service = statErr == nil
	}
	_, shared.Program.GitPending = readGitPending(path)
	encoded, err := json.MarshalIndent(shared, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", encoded)
	return err
}
