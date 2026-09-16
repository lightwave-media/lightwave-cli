package sst

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// CLIConfig mirrors lightwave.schema.pydantic.models.cli.CLIConfig.
// Source of truth: lightwave-core/src/schemas/interfaces/cli/commands.yaml.
type CLIConfig struct {
	Version       string
	Domains       []CLIDomain
	StatusAliases map[string]string
	GlobalFlags   []string
}

// StatusInDevelopment is the _status value that exempts a domain OR a single
// command from the handler-lockstep gate. Either is skipped by the dispatcher
// unless LW_CLI_DEV_DOMAINS=1 is set. Domain-level: commands.yaml v1.1.0.
// Command-level: v1.7.0.
const StatusInDevelopment = "in_development"

// CLIDomain groups related commands under a single namespace.
type CLIDomain struct {
	Name        string
	Description string
	Status      string // optional; "in_development" | "published" | "deprecated"
	Commands    []CLICommand
}

// CLICommand describes a single subcommand exposed by `lw <domain> <command>`.
// Nested groups use Commands (e.g. voice profile list → handler key voice.profile.list).
type CLICommand struct {
	Name string `yaml:"name"`
	// Status is optional and carries the same meaning as CLIDomain.Status, for
	// one verb instead of a whole namespace. A PUBLISHED domain gaining an
	// unbuilt verb had no safe landing order without it: declare first and the
	// strict gate reports a missing handler until the CLI catches up; register
	// the handler first and the same gate calls it an orphan. Marking the
	// domain would have hidden every working sibling verb to shelter one.
	Status      string       `yaml:"_status,omitempty"`
	Args        []string     `yaml:"args,omitempty"`
	Flags       []string     `yaml:"flags,omitempty"`
	Description string       `yaml:"description,omitempty"`
	Commands    []CLICommand `yaml:"commands,omitempty"`
}

// InDevelopment reports whether this command is declared but not yet built.
func (c *CLICommand) InDevelopment() bool { return c.Status == StatusInDevelopment }

// rawCLIConfig is the on-disk shape used during decoding. Domains are
// preserved as a yaml.Node so insertion order survives.
type rawCLIConfig struct {
	StatusAliases map[string]string `yaml:"status_aliases"`
	Meta          rawMeta           `yaml:"_meta"`
	GlobalFlags   []string          `yaml:"global_flags"`
	Domains       yaml.Node         `yaml:"domains"`
}

type rawMeta struct {
	Version string `yaml:"version"`
}

type rawDomain struct {
	Description string       `yaml:"description"`
	Status      string       `yaml:"_status"`
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
			Status:      raw.Status,
			Commands:    raw.Commands,
		})
	}

	return cfg, nil
}
