package sst

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// CLIConfigPath returns the absolute path to commands.yaml from the workspace root.
func CLIConfigPath(lightwaveRoot string) string {
	return filepath.Join(
		lightwaveRoot,
		"lightwave-core", "src", "schemas", "interfaces",
		"cli", "commands.yaml",
	)
}

func cliSchemaDir(lightwaveRoot string) string {
	return filepath.Join(
		lightwaveRoot,
		"lightwave-core", "src", "schemas", "interfaces", "cli",
	)
}

type domainFragment struct {
	Domain      string       `yaml:"domain"`
	Description string       `yaml:"description"`
	Status      string       `yaml:"_status"`
	Commands    []CLICommand `yaml:"commands"`
}

// LoadCLIConfig reads commands.yaml, merges domain fragments (e.g. voice_domain.yaml, release_domain.yaml),
// and returns the validated CLIConfig.
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

	if err := mergeDomainFragments(cfg, cliSchemaDir(lightwaveRoot)); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate CLI config: %w", err)
	}

	return cfg, nil
}

func mergeDomainFragments(cfg *CLIConfig, dir string) error {
	fragments := []string{"voice_domain.yaml", "release_domain.yaml"}

	for _, name := range fragments {
		path := filepath.Join(dir, name)

		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return fmt.Errorf("read domain fragment %s: %w", path, err)
		}

		var frag domainFragment
		if err := yaml.Unmarshal(data, &frag); err != nil {
			return fmt.Errorf("parse domain fragment %s: %w", name, err)
		}

		if frag.Domain == "" {
			return fmt.Errorf("domain fragment %s: missing domain field", name)
		}

		cfg.Domains = append(cfg.Domains, CLIDomain{
			Name:        frag.Domain,
			Description: frag.Description,
			Status:      frag.Status,
			Commands:    frag.Commands,
		})
	}

	return nil
}

// Validate enforces the rules in commands.yaml `_validation`:
// every domain has commands, every command has a name, descriptions present,
// names unique within scope.
func (c *CLIConfig) Validate() error {
	if c.Version == "" {
		return errors.New("missing _meta.version")
	}

	seenDomain := map[string]bool{}

	for _, d := range c.Domains {
		if d.Name == "" {
			return errors.New("domain with empty name")
		}

		if seenDomain[d.Name] {
			return fmt.Errorf("duplicate domain %q", d.Name)
		}

		seenDomain[d.Name] = true

		if len(d.Commands) == 0 {
			return fmt.Errorf("domain %q has no commands", d.Name)
		}

		if err := validateCommandTree(d.Name, "", d.Commands); err != nil {
			return err
		}
	}

	return nil
}
