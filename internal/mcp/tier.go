package mcp

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Tier is a persona_tiers enum value: developer | engineer | singular.
type Tier string

const (
	TierDeveloper Tier = "developer"
	TierEngineer  Tier = "engineer"
	TierSingular  Tier = "singular"
)

// DefaultTier is ADR-0002: operator day-to-day with no --persona flag.
const DefaultTier = TierEngineer

type personaFrontmatter struct {
	Tier string `yaml:"tier"`
	Name string `yaml:"name"`
}

// ResolveTier reads ~/.lightwave/config/agents/<persona>.yaml.
// Empty persona → engineer. Unknown/unreadable persona → engineer with no error
// (the serve loop must start; tool filtering stays conservative).
func ResolveTier(home, persona string) Tier {
	if persona == "" {
		return DefaultTier
	}

	path := filepath.Join(home, ".lightwave", "config", "agents", persona+".yaml")

	body, err := os.ReadFile(path)
	if err != nil {
		return DefaultTier
	}

	var fm personaFrontmatter
	if err := yaml.Unmarshal(body, &fm); err != nil {
		return DefaultTier
	}

	tier := Tier(strings.ToLower(strings.TrimSpace(fm.Tier)))
	switch tier {
	case TierDeveloper, TierEngineer, TierSingular:
		return tier
	default:
		return DefaultTier
	}
}

func (t Tier) allowsWrite() bool {
	return t == TierEngineer || t == TierSingular
}

func (t Tier) allowsDispatch() bool {
	return t == TierSingular
}
