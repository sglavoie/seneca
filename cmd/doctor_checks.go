package cmd

import (
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"github.com/sglavoie/seneca/review/provider"
	"github.com/sglavoie/seneca/review/usage"
	"os"
	"path/filepath"
	"strings"
)

// The checks that inspect Seneca's environment: configuration, the filesystem,
// git, and the AI harness. Every call here is a stat, a read, or `git
// rev-parse`.

// checkDoctorConfig decodes the config and resolves the reflections path,
// reporting which of the three sources won.
func checkDoctorConfig(env doctorEnv, report *doctorReport) (string, bool) {
	cfgPath := configFilePath(env.home)
	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		report.add(doctorCheckConfig, doctorInfo, "no config file at %s; using the built-in defaults", cfgPath)
	}

	settings, err := loadReflectionSettings(env.home)
	if err != nil {
		report.add(doctorCheckConfig, doctorError, "%v", err)
	} else {
		report.add(doctorCheckConfig, doctorOK, "%s %s; %s",
			engine.Plural(len(settings.Questions), "question", "questions"), questionsSource(env.home, cfgPath), describeAnchor(settings))
	}

	path, err := resolveFilePathForHome(env.home, env.getenv)
	if err != nil {
		report.add(doctorCheckConfig, doctorError, "cannot resolve the reflections path: %v", err)
		return "", false
	}
	report.add(doctorCheckConfig, doctorOK, "reflections path %s (from %s)", path, reflectionsPathSource(env))
	return path, true
}

// checkDoctorJournalSettings reports the settings the journal carries itself.
// A journal without them says nothing here: config.toml is then in force and
// the config check has already described it.
func checkDoctorJournalSettings(env doctorEnv, path string, report *doctorReport) {
	state, initialized, err := engine.LoadJournalSettings(path)
	if err != nil {
		report.add(doctorCheckConfig, doctorError, "%v", err)
		return
	}
	if !initialized {
		return
	}
	report.add(doctorCheckConfig, doctorOK, "journal settings %s are in force: %s; %s",
		state.Path, engine.Plural(len(state.Settings.Questions), "question", "questions"), describeAnchor(state.Settings))
	for _, key := range supersededConfigKeys(env.home) {
		report.add(doctorCheckConfig, doctorInfo, "%s in %s is superseded by the journal settings and is not used",
			key, configFilePath(env.home))
	}
}

// questionsSource names where the questions came from, so a report never claims
// a config file supplied the built-in defaults.
func questionsSource(home, cfgPath string) string {
	if cfg, err := loadConfigFile(home); err == nil && cfg.Seneca.Questions != nil {
		return "configured in " + cfgPath
	}
	return "from the built-in defaults"
}

func describeAnchor(settings engine.ReflectionSettings) string {
	idx := settings.AnchorQuestionIndex
	if idx <= 0 || idx > len(settings.Questions) {
		return "the anchor question is disabled"
	}
	return fmt.Sprintf("anchor question %d is %q", idx, settings.Questions[idx-1])
}

// reflectionsPathSource names which resolution step supplied the path, mirroring
// the order in resolveFilePathForHome.
func reflectionsPathSource(env doctorEnv) string {
	if env.getenv("SENECA_FILE") != "" {
		return "SENECA_FILE"
	}
	if configured, err := loadConfiguredReflectionsPath(env.home); err == nil && configured != "" {
		return "seneca.reflections_path"
	}
	return "the built-in default"
}

// checkDoctorPaths reports what the filesystem will allow. A missing directory
// is informational because the daily command offers to create it; a symlink is
// an error because persistence refuses one outright.
func checkDoctorPaths(path string, report *doctorReport) {
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		report.add(doctorCheckPaths, doctorInfo, "directory %s does not exist yet; seneca offers to create it on the first write", dir)
	case err != nil:
		report.add(doctorCheckPaths, doctorError, "cannot inspect directory %s: %v", dir, err)
	case !info.IsDir():
		report.add(doctorCheckPaths, doctorError, "%s exists but is not a directory", dir)
	case info.Mode().Perm()&0300 != 0300:
		report.add(doctorCheckPaths, doctorWarn, "directory %s has mode %04o; writes may be refused", dir, info.Mode().Perm())
	default:
		report.add(doctorCheckPaths, doctorOK, "directory %s exists with mode %04o", dir, info.Mode().Perm())
	}

	checkDoctorWritableFile(doctorCheckPaths, "reflections file", path, report)
	checkDoctorWritableFile(doctorCheckPaths, "review archive", engine.ArchivePathFor(path), report)
}

// checkDoctorWritableFile reports the two file-level states persistence cares
// about: a symlink it refuses, and a mode that would fail the replacement.
// Permissions are read from the mode bits alone — doctor never attempts a write
// to find out, so a file owned by another user can still look writable here.
func checkDoctorWritableFile(check, label, path string, report *doctorReport) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return
	case err != nil:
		report.add(check, doctorError, "cannot inspect %s %s: %v", label, path, err)
	case info.Mode()&os.ModeSymlink != 0:
		report.add(check, doctorError, "%s %s is a symlink; seneca refuses to write through one", label, path)
	case info.IsDir():
		report.add(check, doctorError, "%s %s is a directory", label, path)
	case info.Mode().Perm()&0200 == 0:
		report.add(check, doctorWarn, "%s %s has mode %04o; writes may be refused", label, path, info.Mode().Perm())
	}
}

// checkDoctorGit reports whether the optional auto-commit will happen. Both
// failures are warnings: seneca writes the file either way and only skips the
// commit.
func checkDoctorGit(env doctorEnv, path string, report *doctorReport) {
	binary, err := env.lookPath("git")
	if err != nil {
		report.add(doctorCheckGit, doctorWarn, "git is not on PATH; seneca writes files but skips every commit")
		return
	}

	dir := filepath.Dir(path)
	if err := env.gitRepo(dir); err != nil {
		report.add(doctorCheckGit, doctorWarn, "git is at %s but %s is not in a repository; commits are skipped", binary, dir)
		return
	}
	report.add(doctorCheckGit, doctorOK, "git is at %s and %s is in a repository", binary, dir)
}

// checkDoctorHarness reports on the AI harness `seneca review` would run, and
// which of its sources supplies it. It never executes it: an authenticated CLI
// call is exactly what a read-only diagnosis must not make.
//
// Doctor sees only the standing half of the chain. `--harness` belongs to one
// `seneca review` invocation and doctor has no such flag, so what is reported
// here is what a review with no flags would use; a `--harness` still outranks
// it.
//
// It returns the harness and settings it resolved, empty when none is usable,
// so the usage check can price what a review would run.
func checkDoctorHarness(env doctorEnv, report *doctorReport) (harnessName, reviewSettings) {
	var settings reviewSettings
	invalidConfig := false
	// A config file that cannot be decoded at all is checkDoctorConfig's to
	// report; only the [review] table's own validation belongs here.
	if cfg, err := loadConfigFile(env.home); err == nil {
		var reviewErr error
		if settings, reviewErr = cfg.Review.reviewSettings(); reviewErr != nil {
			report.add(doctorCheckHarness, doctorError, "%v", reviewErr)
			invalidConfig = true
		}
	}

	// The environment outranks the file, so an invalid [review].harness is
	// reported without stopping the rest of the check when SENECA_HARNESS still
	// supplies a usable value.
	configured, source := doctorHarnessSource(env, settings)
	if configured == "" {
		if !invalidConfig {
			report.add(doctorCheckHarness, doctorInfo, "no harness configured; `seneca review` needs --harness, SENECA_HARNESS, or review.harness")
		}
		return "", settings
	}

	name, err := parseHarnessName(configured)
	if err != nil {
		report.add(doctorCheckHarness, doctorError, "%s: %v", source, err)
		return "", settings
	}

	checkDoctorEffort(env, settings, name, report)
	if name.isProvider() {
		checkDoctorProviderKey(env, name, source, providerKeySource(name), report)
		return name, settings
	}

	binary, err := env.lookPath(string(name))
	if err != nil {
		report.add(doctorCheckHarness, doctorWarn, "harness %s (from %s) is configured but is not on PATH; `seneca review` will fail", name, source)
		return name, settings
	}
	report.add(doctorCheckHarness, doctorOK, "harness %s (from %s) is at %s (not run)", name, source, binary)
	return name, settings
}

// doctorHarnessSource names which resolution step supplies the harness,
// mirroring the order in resolveReviewConfig minus the flag tier doctor cannot
// see. It returns the raw value so an invalid one is still reported against the
// source that wrote it.
func doctorHarnessSource(env doctorEnv, settings reviewSettings) (string, string) {
	if value := strings.TrimSpace(env.getenv("SENECA_HARNESS")); value != "" {
		return value, "SENECA_HARNESS"
	}
	if settings.Harness != "" {
		return string(settings.Harness), "review.harness"
	}
	return "", ""
}

// checkDoctorEffort reports an effort that `seneca review` would refuse: an
// invalid SENECA_EFFORT, or any effort paired with a CLI harness that cannot
// honor it. An invalid [review].effort is already reported by the table's own
// validation.
func checkDoctorEffort(env doctorEnv, settings reviewSettings, name harnessName, report *doctorReport) {
	effort, source := settings.Effort, "review.effort"
	if value := strings.TrimSpace(env.getenv("SENECA_EFFORT")); value != "" {
		parsed, err := parseReviewEffort(value)
		if err != nil {
			report.add(doctorCheckHarness, doctorError, "SENECA_EFFORT: %v", err)
			return
		}
		effort, source = parsed, "SENECA_EFFORT"
	}
	if effort != "" && !name.honorsEffort() {
		report.add(doctorCheckHarness, doctorError, "%s: the %s harness cannot honor an effort setting; `seneca review` will refuse it", source, name)
	}
}

// checkDoctorProviderKey reports which source supplies a provider's API key,
// never the key itself, and never calls the API.
func checkDoctorProviderKey(env doctorEnv, name harnessName, source string, keys provider.KeySource, report *doctorReport) {
	_, label, err := provider.ResolveKey(keys, env.getenv, env.keychain)
	var missing *provider.MissingKeyError
	switch {
	case errors.As(err, &missing):
		report.add(doctorCheckHarness, doctorWarn, "harness %s (from %s) has no API key: set %s or store one in the Keychain as %s; `seneca review` will fail", name, source, keys.EnvVar, keys.Service)
	case err != nil:
		report.add(doctorCheckHarness, doctorWarn, "harness %s (from %s): %v", name, source, err)
	default:
		report.add(doctorCheckHarness, doctorOK, "harness %s (from %s) uses the API key from %s (not called)", name, source, label)
	}
}

// checkDoctorUsage reports how many review attempts the usage ledger holds and
// whether the pricing table can still price a provider's reviews. It counts
// the ledger's lines and never prints one. A journal never reviewed has no
// ledger and gets no line here.
func checkDoctorUsage(env doctorEnv, path string, name harnessName, settings reviewSettings, report *doctorReport) {
	ledger := usageLedgerPathFor(path)
	if file, err := os.Open(ledger); err == nil {
		records, skipped, readErr := usage.ReadLedger(file)
		file.Close()
		switch {
		case readErr != nil:
			report.add(doctorCheckUsage, doctorWarn, "%v", readErr)
		default:
			report.add(doctorCheckUsage, doctorInfo, "the usage ledger %s holds %s", ledger, engine.Plural(len(records), "review attempt", "review attempts"))
		}
		if skipped > 0 {
			report.add(doctorCheckUsage, doctorWarn, "the usage ledger has %s it cannot read; `seneca usage` skips them", engine.Plural(skipped, "line", "lines"))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		report.add(doctorCheckUsage, doctorWarn, "cannot read the usage ledger %s: %v", ledger, err)
	}

	// Only a provider harness is priced from the table; the claude CLI reports
	// its own cost, and the other CLIs report no usage at all.
	if !name.isProvider() {
		return
	}
	table, err := usage.Load(env.home)
	if err != nil {
		report.add(doctorCheckUsage, doctorWarn, "%v; reviews are priced with the built-in table", err)
	}
	if oldest, ok := table.Oldest(); ok {
		age, err := oldest.AgeDays(env.todayDate())
		switch {
		case err != nil:
			report.add(doctorCheckUsage, doctorWarn, "pricing for %s/%s has an unreadable verified date %q", oldest.Provider, oldest.Model, oldest.Verified)
		case age > usage.StaleAfterDays:
			report.add(doctorCheckUsage, doctorWarn, "the pricing table is %d days old: %s/%s was last verified %s; check the rates at %s and record them with a new verified date in %s",
				age, oldest.Provider, oldest.Model, oldest.Verified, oldest.Source, usage.OverridePath(env.home))
		default:
			report.add(doctorCheckUsage, doctorOK, "the pricing table was verified %s or later", oldest.Verified)
		}
	}

	model := doctorReviewModel(env, settings, name)
	if _, ok := table.Lookup(usageProvider(name), model); !ok {
		report.add(doctorCheckUsage, doctorWarn, "%s model %s has no row in the pricing table, so its reviews are recorded without a cost; add one to %s",
			usageProvider(name), model, usage.OverridePath(env.home))
	}
}

// doctorReviewModel is the model a review with no flags would ask for,
// mirroring resolveReviewConfig minus the flag tier.
func doctorReviewModel(env doctorEnv, settings reviewSettings, name harnessName) string {
	if value := strings.TrimSpace(env.getenv("SENECA_MODEL")); value != "" {
		return value
	}
	if model := strings.TrimSpace(settings.Model); model != "" {
		return model
	}
	model, _ := name.providerDefaults()
	return model
}
