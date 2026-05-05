package sst

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// CLIConfigPath returns the absolute path to commands.yaml from the workspace root.
func CLIConfigPath(lightwaveRoot string) string {
	return filepath.Join(
		lightwaveRoot,
		"packages", "lightwave-core", "lightwave", "schema", "definitions",
		"config", "cli", "commands.yaml",
	)
}

// LoadCLIConfig reads commands.yaml and returns the validated, ordered CLIConfig.
func LoadCLIConfig(lightwaveRoot string) (*CLIConfig, error) {
	path := CLIConfigPath(lightwaveRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CLI config %s: %w", path, err)
	}

	var raw rawCLIConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse CLI config: %w", err)
	}

	cfg, err := raw.decode()
	if err != nil {
		return nil, fmt.Errorf("decode CLI config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate CLI config: %w", err)
	}

	return cfg, nil
}

// Validate enforces the rules in commands.yaml `_validation`:
// every domain has commands, every command has a name, descriptions present,
// names unique within scope.
func (c *CLIConfig) Validate() error {
	if c.Version == "" {
		return fmt.Errorf("missing _meta.version")
	}

	seenDomain := map[string]bool{}
	for _, d := range c.Domains {
		if d.Name == "" {
			return fmt.Errorf("domain with empty name")
		}
		if seenDomain[d.Name] {
			return fmt.Errorf("duplicate domain %q", d.Name)
		}
		seenDomain[d.Name] = true

		if len(d.Commands) == 0 {
			return fmt.Errorf("domain %q has no commands", d.Name)
		}

		seenCmd := map[string]bool{}
		for _, cmd := range d.Commands {
			if cmd.Name == "" {
				return fmt.Errorf("domain %q has command with empty name", d.Name)
			}
			if seenCmd[cmd.Name] {
				return fmt.Errorf("domain %q has duplicate command %q", d.Name, cmd.Name)
			}
			seenCmd[cmd.Name] = true

			if cmd.Description == "" {
				return fmt.Errorf("%s.%s missing description", d.Name, cmd.Name)
			}
		}
	}

	return nil
}
