// Package sst reads and serves Lightwave schema definitions (SST = source-of-truth).
// Every enforcement check loads its schema at runtime — never hardcodes paths or values.
package sst

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed repo-infra.yaml
var bundledRepoInfra []byte

// RepoInfraPath returns the absolute path to repo-infra.yaml from the workspace root.
func RepoInfraPath(lightwaveRoot string) string {
	return filepath.Join(
		lightwaveRoot,
		"lightwave-core", "src", "schemas", "policy", "governance", "repo-infra.yaml",
	)
}

// RepoInfraConfig mirrors repo-infra.yaml's example block.
type RepoInfraConfig struct {
	Version         string
	RequiredFiles   []InfraFileEntry `json:"required_files"`
	RequiredDirs    []InfraDirEntry  `json:"required_dirs"`
	RequiredActions []InfraAction    `json:"required_actions"`
}

type InfraFileEntry struct {
	Path         string `yaml:"path" json:"path"`
	Purpose      string `yaml:"purpose" json:"purpose"`
	Consumer     string `yaml:"consumer" json:"consumer"`
	Universality string `yaml:"universality" json:"universality"`
}

type InfraDirEntry struct {
	Path         string   `yaml:"path" json:"path"`
	Purpose      string   `yaml:"purpose" json:"purpose"`
	Consumer     string   `yaml:"consumer" json:"consumer"`
	Universality string   `yaml:"universality" json:"universality"`
	Gitignored   []string `yaml:"gitignored,omitempty" json:"gitignored,omitempty"`
}

type InfraAction struct {
	Uses         string `yaml:"uses" json:"uses"`
	Purpose      string `yaml:"purpose" json:"purpose"`
	Consumer     string `yaml:"consumer" json:"consumer"`
	Universality string `yaml:"universality" json:"universality"`
}

// rawRepoInfraSchema is the on-disk YAML shape of repo-infra.yaml.
type rawRepoInfraSchema struct {
	Meta           rawMeta         `yaml:"_meta"`
	RequiredFields []rawField      `yaml:"required_fields"`
	Example        rawRepoInfraDoc `yaml:"example"`
}

type rawField struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Optional bool   `yaml:"optional"`
}

type rawRepoInfraDoc struct {
	ID              string           `yaml:"id"`
	RequiredDirs    []InfraDirEntry  `yaml:"required_dirs"`
	RequiredFiles   []InfraFileEntry `yaml:"required_files"`
	RequiredActions []InfraAction    `yaml:"required_actions"`
}

// LoadRepoInfra reads repo-infra.yaml and returns the enforcement config.
func LoadRepoInfra(lightwaveRoot string) (*RepoInfraConfig, error) {
	path := RepoInfraPath(lightwaveRoot)

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read repo-infra %s: %w", path, err)
		}
		// lightwave-core not checked out — use the bundled snapshot.
		data = bundledRepoInfra
	}

	var raw rawRepoInfraSchema
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse repo-infra %s: %w", path, err)
	}

	cfg := &RepoInfraConfig{
		Version: raw.Meta.Version,
	}

	for _, d := range raw.Example.RequiredDirs {
		if d.Universality == "universal" {
			cfg.RequiredDirs = append(cfg.RequiredDirs, d)
		}
	}

	for _, f := range raw.Example.RequiredFiles {
		if f.Universality == "universal" {
			cfg.RequiredFiles = append(cfg.RequiredFiles, f)
		}
	}

	cfg.RequiredActions = raw.Example.RequiredActions

	if len(cfg.RequiredDirs) == 0 && len(cfg.RequiredFiles) == 0 {
		return nil, fmt.Errorf("repo-infra %s: no universal required dirs or files (malformed schema)", path)
	}

	return cfg, nil
}

// UniversalFilePaths returns the path strings of universal required files.
func (c *RepoInfraConfig) UniversalFilePaths() []string {
	out := make([]string, len(c.RequiredFiles))
	for i, f := range c.RequiredFiles {
		out[i] = f.Path
	}

	return out
}

// UniversalDirPaths returns the path strings of universal required dirs.
func (c *RepoInfraConfig) UniversalDirPaths() []string {
	out := make([]string, len(c.RequiredDirs))
	for i, d := range c.RequiredDirs {
		out[i] = d.Path
	}

	return out
}
