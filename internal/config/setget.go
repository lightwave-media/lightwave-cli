package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// settableKeys whitelists keys that `lw config set` will accept. Anything
// outside this set is rejected so the YAML doesn't acquire stray fields
// that don't map to the Config struct.
var settableKeys = map[string]bool{
	"environment":       true,
	"tenant":            true,
	"database.url":      true,
	"database.host":     true,
	"database.port":     true,
	"database.name":     true,
	"database.user":     true,
	"database.password": true,
}

// SettableKeys returns the whitelist of keys accepted by Set, sorted.
func SettableKeys() []string {
	keys := make([]string, 0, len(settableKeys))
	for k := range settableKeys {
		keys = append(keys, k)
	}
	return keys
}

// configFilePath returns the canonical config file path
// (~/.config/lw/config.yaml). Existing config search order in Load() prefers
// this location, so reads after a Set() will find the new value.
func configFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "lw", "config.yaml"), nil
}

// Set persists key=value to ~/.config/lw/config.yaml. Creates the file
// (and parent dir) if missing. Validates the key against settableKeys and
// performs lightweight value coercion (port → int).
func Set(key, value string) error {
	if !settableKeys[key] {
		return fmt.Errorf("unknown config key %q (settable: %s)", key, strings.Join(SettableKeys(), ", "))
	}

	path, err := configFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parsing existing config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading existing config: %w", err)
	}

	parts := strings.Split(key, ".")
	leafValue := coerceValue(key, value)
	if err := setNested(root, parts, leafValue); err != nil {
		return err
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	Reset()
	return nil
}

// Lookup returns the resolved value for key (after env + file overlay),
// or the empty string + false if unset / unknown.
func Lookup(key string) (string, bool) {
	if !settableKeys[key] {
		return "", false
	}
	cfg := Get()
	if cfg == nil {
		return "", false
	}
	switch key {
	case "environment":
		return cfg.Environment, cfg.Environment != ""
	case "tenant":
		return cfg.Tenant, cfg.Tenant != ""
	case "database.url":
		return cfg.Database.URL, cfg.Database.URL != ""
	case "database.host":
		return cfg.DisplayHost(), cfg.DisplayHost() != ""
	case "database.port":
		p := cfg.DisplayPort()
		if p == 0 {
			return "", false
		}
		return strconv.Itoa(p), true
	case "database.name":
		return cfg.Database.Name, cfg.Database.Name != ""
	case "database.user":
		return cfg.Database.User, cfg.Database.User != ""
	case "database.password":
		return cfg.Database.Password, cfg.Database.Password != ""
	}
	return "", false
}

func coerceValue(key, value string) any {
	if key == "database.port" {
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return value
}

func setNested(root map[string]any, parts []string, value any) error {
	cur := root
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = value
			return nil
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	return nil
}
