// Package release implements ADR-0035 feature-flag evaluation and release train gates.
package release

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/lightwave-media/lightwave-cli/internal/config"
)

const voiceCommandsFlag = "lw_voice_commands"

const (
	flagDirPerm  = 0o755
	flagFilePerm = 0o644
)

// FlagDef is the stamp shape from config/flags/registry.yaml.
type FlagDef struct {
	FlagKey      string `yaml:"flag_key"`
	Description  string `yaml:"description"`
	Owner        string `yaml:"owner"`
	GatesRelease string `yaml:"gates_release,omitempty"`
	Notes        string `yaml:"notes,omitempty"`
	Default      bool   `yaml:"default"`
}

type flagRegistry struct {
	Flags []FlagDef `yaml:"flags"`
}

// IsEnabled reports whether flagKey is on. Precedence: LW_FEATURE_<KEY> env,
// then ~/.lightwave/config/flags.toml print, then registry default.
func IsEnabled(flagKey string) (bool, error) {
	if v, ok := envOverride(flagKey); ok {
		return v, nil
	}

	def, err := loadDefault(flagKey)
	if err != nil {
		return false, err
	}

	printPath := flagsPrintPath()

	data, err := os.ReadFile(printPath)
	if errors.Is(err, os.ErrNotExist) {
		return def, nil
	}

	if err != nil {
		return false, fmt.Errorf("read flags print %s: %w", printPath, err)
	}

	var state map[string]bool
	if err := toml.Unmarshal(data, &state); err != nil {
		return false, fmt.Errorf("parse flags print: %w", err)
	}

	if v, ok := state[flagKey]; ok {
		return v, nil
	}

	return def, nil
}

// SetFlag writes the on/off state for flagKey to flags.toml.
func SetFlag(flagKey string, enabled bool) error {
	if _, err := loadDefault(flagKey); err != nil {
		return err
	}

	printPath := flagsPrintPath()
	state := map[string]bool{}

	if data, err := os.ReadFile(printPath); err == nil {
		_ = toml.Unmarshal(data, &state)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read flags print: %w", err)
	}

	state[flagKey] = enabled

	out, err := toml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal flags print: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(printPath), flagDirPerm); err != nil {
		return err
	}

	return os.WriteFile(printPath, out, flagFilePerm)
}

// ListFlags returns effective on/off for every registered flag.
func ListFlags() ([]struct {
	Key     string
	Enabled bool
	Default bool
}, error) {
	reg, err := loadRegistry()
	if err != nil {
		return nil, err
	}

	out := make([]struct {
		Key     string
		Enabled bool
		Default bool
	}, 0, len(reg.Flags))

	for _, f := range reg.Flags {
		on, err := IsEnabled(f.FlagKey)
		if err != nil {
			return nil, err
		}

		out = append(out, struct {
			Key     string
			Enabled bool
			Default bool
		}{Key: f.FlagKey, Enabled: on, Default: f.Default})
	}

	return out, nil
}

// GateVoice returns an error when lw_voice_commands is off.
func GateVoice() error {
	on, err := IsEnabled(voiceCommandsFlag)
	if err != nil {
		return err
	}

	if on {
		return nil
	}

	return fmt.Errorf(
		"lw voice is off (flag %q) — enable: lw release flag %s --on | rollback: lw release flag %s --off",
		voiceCommandsFlag, voiceCommandsFlag, voiceCommandsFlag,
	)
}

// MergeAutonomous reports whether feature PR auto-merge may skip CTO sign-off.
func MergeAutonomous() (bool, error) {
	hold, err := IsEnabled("release_merge_hold")
	if err != nil {
		return false, err
	}

	if hold {
		return false, nil
	}

	return IsEnabled("autonomous_release_merge")
}

// ReleasePRAutonomous reports whether Release PR auto-merge is allowed.
func ReleasePRAutonomous() (bool, error) {
	hold, err := IsEnabled("release_merge_hold")
	if err != nil {
		return false, err
	}

	if hold {
		return false, nil
	}

	return IsEnabled("autonomous_release_pr_merge")
}

func envOverride(flagKey string) (bool, bool) {
	envKey := "LW_FEATURE_" + strings.ToUpper(strings.ReplaceAll(flagKey, "-", "_"))

	v, ok := os.LookupEnv(envKey)
	if !ok {
		return false, false
	}

	return strings.EqualFold(v, "1") || strings.EqualFold(v, "true"), true
}

func loadDefault(flagKey string) (bool, error) {
	reg, err := loadRegistry()
	if err != nil {
		return false, err
	}

	for _, f := range reg.Flags {
		if f.FlagKey == flagKey {
			return f.Default, nil
		}
	}

	return false, fmt.Errorf("unknown feature flag %q (not in registry stamp)", flagKey)
}

func loadRegistry() (*flagRegistry, error) {
	if _, err := syncFlagsRegistryQuiet(); err != nil {
		return nil, err
	}

	path := flagsRegistryPath()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read flag registry %s: %w", path, err)
	}

	var reg flagRegistry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse flag registry: %w", err)
	}

	if len(reg.Flags) == 0 {
		return nil, errors.New("flag registry is empty")
	}

	return &reg, nil
}

func flagsRegistryPath() string {
	if p := os.Getenv("LW_FLAGS_REGISTRY"); p != "" {
		return p
	}

	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".lightwave", "config", "flags", "registry.yaml")
}

func syncFlagsRegistryQuiet() (bool, error) {
	// Lazy import avoided — duplicate narrow sync to keep release package free of homepolicy cycle.
	// homepolicy.SyncFlagsRegistry is invoked from cli handlers; release uses inline sync.
	stamp, err := flagsStampPath()
	if err != nil {
		dest := flagsRegistryPath()
		if _, statErr := os.Stat(dest); statErr == nil {
			return false, nil
		}

		return false, err
	}

	dest := flagsRegistryPath()

	return syncFileIfDrift(stamp, dest)
}

func flagsStampPath() (string, error) {
	if p := os.Getenv("LW_FLAGS_STAMP"); p != "" {
		return p, nil
	}

	if bp := os.Getenv("LW_BLUEPRINTS_DIR"); bp != "" {
		candidate := filepath.Join(bp, "lightwave-home", "config", "flags", "registry.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	coreRoot := lightwaveCoreRootForFlags()

	stamp := filepath.Join(coreRoot, "src", "boilerplate", "blueprints", "lightwave-home", "config", "flags", "registry.yaml")
	if _, err := os.Stat(stamp); err != nil {
		return "", fmt.Errorf("flags stamp not found at %s (run lw home sync or set LW_BLUEPRINTS_DIR): %w", stamp, err)
	}

	return stamp, nil
}

func lightwaveCoreRootForFlags() string {
	cfg := config.Get()
	if cfg != nil && cfg.Paths.LightwaveRoot != "" {
		return filepath.Join(cfg.Paths.LightwaveRoot, "lightwave-core")
	}

	home, _ := os.UserHomeDir()

	return filepath.Join(home, "dev", "lightwave-core")
}

func syncFileIfDrift(stamp, dest string) (bool, error) {
	stampData, err := os.ReadFile(stamp)
	if err != nil {
		return false, fmt.Errorf("read flags stamp %s: %w", stamp, err)
	}

	destData, err := os.ReadFile(dest)
	if err == nil && bytes.Equal(stampData, destData) {
		return false, nil
	}

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read flags print %s: %w", dest, err)
	}

	if err := os.MkdirAll(filepath.Dir(dest), flagDirPerm); err != nil {
		return false, err
	}

	if err := os.WriteFile(dest, stampData, flagFilePerm); err != nil {
		return false, err
	}

	return true, nil
}

func flagsPrintPath() string {
	if p := os.Getenv("LW_FLAGS_PRINT"); p != "" {
		return p
	}

	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".lightwave", "config", "flags.toml")
}
