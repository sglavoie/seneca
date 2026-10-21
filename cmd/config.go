package cmd

import (
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type fileConfig struct {
	Editor editorConfig     `toml:"editor"`
	Seneca senecaConfig     `toml:"seneca"`
	Review reviewFileConfig `toml:"review"`
	Sync   syncFileConfig   `toml:"sync"`
}

// syncFileConfig is the `[sync]` table. The exchange is a path on this machine,
// so it is device-local like the reflections path and never part of the
// journal's shared settings.
type syncFileConfig struct {
	Exchange *string `toml:"exchange"`
}

type editorConfig struct {
	Command []string `toml:"command"`
}

type senecaConfig struct {
	Questions           []string `toml:"questions"`
	AnchorQuestionIndex *int     `toml:"anchor_question_index"`
	ReflectionsPath     *string  `toml:"reflections_path"`
}

// reviewFileConfig is the `[review]` table: the settings `seneca review`
// would otherwise need on every invocation. Both fields are pointers for the
// same reason ReflectionsPath is one — an absent key means "no opinion, fall
// back", while a key written blank is a mistake worth naming.
type reviewFileConfig struct {
	Harness *string `toml:"harness"`
	Model   *string `toml:"model"`
	Effort  *string `toml:"effort"`
}

// reviewSettings is the validated `[review]` table. A zero value means the
// section was absent, which is the common case and never an error.
type reviewSettings struct {
	Harness harnessName
	Model   string
	Effort  string
}

func configFilePath(home string) string {
	return filepath.Join(home, ".config", "seneca", "config.toml")
}

func loadConfigFile(home string) (fileConfig, error) {
	var cfg fileConfig
	cfgPath := configFilePath(home)
	if _, err := toml.DecodeFile(cfgPath, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("loading config %s: %w", cfgPath, err)
	}
	return cfg, nil
}

func loadReflectionSettings(home string) (engine.ReflectionSettings, error) {
	cfg, err := loadConfigFile(home)
	if err != nil {
		return engine.ReflectionSettings{}, err
	}
	return cfg.Seneca.reflectionSettings()
}

// supersededConfigKeys names the `[seneca]` keys config.toml sets that a journal
// with settings of its own no longer reads. They are left in the file: the
// journal's settings were seeded from them, and an uninitialized journal on the
// same machine still uses them.
func supersededConfigKeys(home string) []string {
	cfg, err := loadConfigFile(home)
	if err != nil {
		return nil
	}
	var keys []string
	if cfg.Seneca.Questions != nil {
		keys = append(keys, "seneca.questions")
	}
	if cfg.Seneca.AnchorQuestionIndex != nil {
		keys = append(keys, "seneca.anchor_question_index")
	}
	return keys
}

func loadReviewSettings(home string) (reviewSettings, error) {
	cfg, err := loadConfigFile(home)
	if err != nil {
		return reviewSettings{}, err
	}
	return cfg.Review.reviewSettings()
}

func loadConfiguredReflectionsPath(home string) (string, error) {
	cfg, err := loadConfigFile(home)
	if err != nil {
		return "", err
	}
	return cfg.Seneca.configuredReflectionsPath(home)
}

// loadConfiguredSyncExchange reads `[sync].exchange`: the directory this device
// exchanges through. Empty means none is configured.
func loadConfiguredSyncExchange(home string) (string, error) {
	cfg, err := loadConfigFile(home)
	if err != nil {
		return "", err
	}
	if cfg.Sync.Exchange == nil {
		return "", nil
	}
	return normalizeConfiguredPath(home, *cfg.Sync.Exchange, "sync.exchange")
}

func (cfg senecaConfig) reflectionSettings() (engine.ReflectionSettings, error) {
	return engine.ResolveReflectionSettings(cfg.Questions, cfg.AnchorQuestionIndex)
}

func (cfg senecaConfig) configuredReflectionsPath(home string) (string, error) {
	if cfg.ReflectionsPath == nil {
		return "", nil
	}
	return normalizeConfiguredPath(home, *cfg.ReflectionsPath, "seneca.reflections_path")
}

// reviewSettings validates the `[review]` table at load time rather than at
// review time, so a harness typo in config.toml is something `seneca doctor`
// reports instead of something the author discovers when they finally ask for a
// review. A blank value is refused the way a blank reflections_path is: writing
// a key and leaving it empty is a mistake, and silently treating it as absent
// would hide it.
func (cfg reviewFileConfig) reviewSettings() (reviewSettings, error) {
	var settings reviewSettings

	if cfg.Harness != nil {
		if strings.TrimSpace(*cfg.Harness) == "" {
			return reviewSettings{}, fmt.Errorf("review.harness must not be blank")
		}
		harness, err := parseHarnessName(*cfg.Harness)
		if err != nil {
			return reviewSettings{}, fmt.Errorf("review.harness: %w", err)
		}
		settings.Harness = harness
	}

	if cfg.Model != nil {
		if strings.TrimSpace(*cfg.Model) == "" {
			return reviewSettings{}, fmt.Errorf("review.model must not be blank")
		}
		settings.Model = strings.TrimSpace(*cfg.Model)
	}

	if cfg.Effort != nil {
		if strings.TrimSpace(*cfg.Effort) == "" {
			return reviewSettings{}, fmt.Errorf("review.effort must not be blank")
		}
		effort, err := parseReviewEffort(*cfg.Effort)
		if err != nil {
			return reviewSettings{}, fmt.Errorf("review.effort: %w", err)
		}
		settings.Effort = effort
	}

	return settings, nil
}

func normalizeConfiguredPath(home, value, field string) (string, error) {
	path := strings.TrimSpace(value)
	if path == "" {
		return "", fmt.Errorf("%s must not be blank", field)
	}
	if strings.HasPrefix(path, "~/") {
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be an absolute path or start with ~/", field)
	}
	return path, nil
}
