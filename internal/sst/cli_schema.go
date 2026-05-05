package sst

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// CLIConfig mirrors lightwave.schema.pydantic.models.cli.CLIConfig.
// Source of truth: packages/lightwave-core/lightwave/schema/definitions/config/cli/commands.yaml.
type CLIConfig struct {
	Version       string
	Domains       []CLIDomain
	StatusAliases map[string]string
	GlobalFlags   []string
}

// CLIDomain groups related commands under a single namespace.
type CLIDomain struct {
	Name        string
	Description string
	Commands    []CLICommand
}

// CLICommand describes a single subcommand exposed by `lw <domain> <command>`.
type CLICommand struct {
	Name        string   `yaml:"name"`
	Args        []string `yaml:"args,omitempty"`
	Flags       []string `yaml:"flags,omitempty"`
	Description string   `yaml:"description,omitempty"`
}

// rawCLIConfig is the on-disk shape used during decoding. Domains are
// preserved as a yaml.Node so insertion order survives.
type rawCLIConfig struct {
	Meta          rawMeta           `yaml:"_meta"`
	Domains       yaml.Node         `yaml:"domains"`
	StatusAliases map[string]string `yaml:"status_aliases"`
	GlobalFlags   []string          `yaml:"global_flags"`
}

type rawMeta struct {
	Version string `yaml:"version"`
}

type rawDomain struct {
	Description string       `yaml:"description"`
	Commands    []CLICommand `yaml:"commands"`
}

// decode walks the raw YAML and yields a deterministic, ordered CLIConfig.
func (r *rawCLIConfig) decode() (*CLIConfig, error) {
	cfg := &CLIConfig{
		Version:       r.Meta.Version,
		StatusAliases: r.StatusAliases,
		GlobalFlags:   r.GlobalFlags,
	}

	if r.Domains.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("domains: expected mapping, got kind %d", r.Domains.Kind)
	}

	// MappingNode children alternate key, value, key, value...
	content := r.Domains.Content
	if len(content)%2 != 0 {
		return nil, fmt.Errorf("domains: odd number of mapping children (%d)", len(content))
	}

	for i := 0; i < len(content); i += 2 {
		keyNode := content[i]
		valNode := content[i+1]

		var raw rawDomain
		if err := valNode.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decode domain %q: %w", keyNode.Value, err)
		}

		cfg.Domains = append(cfg.Domains, CLIDomain{
			Name:        keyNode.Value,
			Description: raw.Description,
			Commands:    raw.Commands,
		})
	}

	return cfg, nil
}
