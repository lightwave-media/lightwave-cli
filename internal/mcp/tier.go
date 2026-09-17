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

	// TierNone means identity did not resolve — no persona was given, the
	// persona file was missing/unreadable, or its declared tier value was
	// unrecognized. It is the empty string deliberately: it is Tier's zero
	// value, so a caller that forgets to check ResolveTier's result still
	// fails closed rather than defaulting to something permissive.
	TierNone Tier = ""
)

type personaFrontmatter struct {
	Tier string `yaml:"tier"`
	Name string `yaml:"name"`
}

// ResolveTier reads ~/.lightwave/config/agents/<persona>.yaml.
//
// Fails closed uniformly: no persona given, an unreadable/missing file, or an
// unrecognized declared tier all return TierNone. This used to default an
// unresolved persona to TierEngineer (ADR-0002's operator-day-to-day
// convenience for running `lw mcp serve` with no --persona flag) — including
// on a TYPO'D persona name, which meant a mistyped --persona silently bought
// the write tier. Identity must resolve before any tool is served; there is
// no "safe default" tier for an identity the server couldn't establish.
func ResolveTier(home, persona string) Tier {
	if persona == "" {
		return TierNone
	}

	path := filepath.Join(home, ".lightwave", "config", "agents", persona+".yaml")

	body, err := os.ReadFile(path)
	if err != nil {
		return TierNone
	}

	var fm personaFrontmatter
	if err := yaml.Unmarshal(body, &fm); err != nil {
		return TierNone
	}

	tier := Tier(strings.ToLower(strings.TrimSpace(fm.Tier)))
	switch tier {
	case TierDeveloper, TierEngineer, TierSingular:
		return tier
	default:
		return TierNone
	}
}

func (t Tier) allowsWrite() bool {
	return t == TierEngineer || t == TierSingular
}

func (t Tier) allowsDispatch() bool {
	return t == TierSingular
}
