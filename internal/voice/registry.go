package voice

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ConfigDir returns ~/.lightwave/config/voice.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".lightwave", "config", "voice"), nil
}

type Registry struct {
	PersonaBindings map[string]string `yaml:"persona_bindings"`
	ManagedBy       string            `yaml:"managed_by"`
	DefaultProfile  string            `yaml:"default_profile"`
	Precedence      []string          `yaml:"precedence"`
	SchemaVersion   int               `yaml:"schema_version"`
}

type Profile struct {
	ProfileID    string   `yaml:"profile_id"`
	Kind         string   `yaml:"kind"`
	Engine       string   `yaml:"engine"`
	VoiceID      string   `yaml:"voice_id"`
	Language     string   `yaml:"language"`
	Goal         string   `yaml:"goal"`
	OutputFormat string   `yaml:"output_format"`
	ToneTags     []string `yaml:"tone_tags"`
	SpeakingRate float64  `yaml:"speaking_rate"`
	Pitch        float64  `yaml:"pitch"`
}

func LoadRegistry() (*Registry, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, "registry.yaml")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read voice registry: %w", err)
	}

	var reg Registry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse voice registry: %w", err)
	}

	return &reg, nil
}

func ResolveProfileID(reg *Registry, persona string) string {
	if reg == nil {
		return "default"
	}

	if id, ok := reg.PersonaBindings[persona]; ok && id != "" {
		return id
	}

	if reg.DefaultProfile != "" {
		return reg.DefaultProfile
	}

	return "default"
}

func LoadProfile(profileID string) (*Profile, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, "profiles", profileID+".yaml")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile %q: %w", profileID, err)
	}

	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse profile %q: %w", profileID, err)
	}

	return &p, nil
}

func ListProfileIDs() ([]string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(filepath.Join(dir, "profiles"))
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}

	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		name := e.Name()
		if filepath.Ext(name) != ".yaml" {
			continue
		}

		out = append(out, name[:len(name)-len(".yaml")])
	}

	return out, nil
}

func SaveRegistry(reg *Registry) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	b, err := yaml.Marshal(reg)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "registry.yaml"), b, filePerm)
}
