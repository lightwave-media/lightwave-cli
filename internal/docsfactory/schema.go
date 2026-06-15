// Package docsfactory implements the spec/ + docs/ factory verbs:
// `lw docs spec-lint`, `lw docs check`, and `lw docs sync`. It reads its
// shape contracts from three lightwave-core schemas under
// src/schemas/policy/governance/:
//
//   - spec_artifact_kinds.yaml — what spec/ files must look like
//   - doc_artifact_kinds.yaml  — what docs/ files must look like
//   - repo_doc_manifest.yaml   — which kinds every repo must have, by tier
//
// Per-repo `.lwdocs.yaml` overrides the manifest. The package is engine-
// agnostic — it does not call boilerplate; the scaffold path is the
// existing `lw scaffold spec-repo` / `docs-repo` (internal/blueprint).
package docsfactory

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SpecArtifactKind is one entry from spec_artifact_kinds.yaml.
type SpecArtifactKind struct {
	Kind                  string   `yaml:"kind"`
	Description           string   `yaml:"description"`
	Extension             string   `yaml:"extension"`
	FrontmatterRequired   []string `yaml:"frontmatter_required"`
	FrontmatterStatusEnum []string `yaml:"frontmatter_status_enum"`
	RequiredSections      []string `yaml:"required_sections"`
	Validator             string   `yaml:"validator"`
}

// DocArtifactKind is one entry from doc_artifact_kinds.yaml.
type DocArtifactKind struct {
	Kind                   string         `yaml:"kind"`
	Description            string         `yaml:"description"`
	Extension              string         `yaml:"extension"`
	SiblingExtension       string         `yaml:"sibling_extension,omitempty"`
	FrontmatterRequired    []string       `yaml:"frontmatter_required"`
	HeaderCommentsRequired []string       `yaml:"header_comments_required,omitempty"`
	RequiredSections       []string       `yaml:"required_sections"`
	RefreshSource          []RefreshEntry `yaml:"refresh_source"`
	Validator              string         `yaml:"validator"`
}

// RefreshEntry declares one input source for `lw docs sync` regeneration.
type RefreshEntry struct {
	Kind        string   `yaml:"kind"`
	Roots       []string `yaml:"roots,omitempty"`
	IgnoreGlobs []string `yaml:"ignore_globs,omitempty"`
	When        string   `yaml:"when,omitempty"`
	Glob        string   `yaml:"glob,omitempty"`
	Path        string   `yaml:"path,omitempty"`
	Tools       []string `yaml:"tools,omitempty"`
	Symbols     []string `yaml:"symbols,omitempty"`
}

// Generated reports whether a doc kind has any refresh_source entry —
// the discriminator between generated (drift-checked vs HEAD) and
// authored (shape-only-checked) kinds.
func (d DocArtifactKind) Generated() bool {
	return len(d.RefreshSource) > 0
}

// RepoDocManifest captures the contents of repo_doc_manifest.yaml plus any
// per-repo `.lwdocs.yaml` override merged in. Per-tier defaults are flattened
// into Required/Recommended by ResolveForTier.
type RepoDocManifest struct {
	Defaults  manifestSpec            `yaml:"defaults"`
	Tiers     map[string]manifestSpec `yaml:"tiers"`
	Freshness FreshnessPolicy         `yaml:"freshness"`
}

type manifestSpec struct {
	SpecRequired    []string `yaml:"spec_required,omitempty"`
	DocsRequired    []string `yaml:"docs_required,omitempty"`
	DocsRecommended []string `yaml:"docs_recommended,omitempty"`
}

// FreshnessPolicy controls how staleness is surfaced.
type FreshnessPolicy struct {
	MaxAgeDays         int    `yaml:"max_age_days"`
	SourceChangeAction string `yaml:"source_change_action"`
	SourceCommitAction string `yaml:"source_commit_action"`
	StaleAction        string `yaml:"stale_action"`
}

// ResolvedManifest is a tier-resolved, override-merged view of what a single
// repo must have. Consumers (docs check) never read the raw manifest.
type ResolvedManifest struct {
	Tier            string
	SpecRequired    []string
	DocsRequired    []string
	DocsRecommended []string
	IgnoreGlobs     []string
	Freshness       FreshnessPolicy
}

// LWDocsOverride is the shape of a per-repo .lwdocs.yaml. Any field left
// nil/zero means "accept the tier default".
//
// IgnoreGlobs is the escape hatch for in-repo docs/ files that exist but
// aren't part of the doc_artifact_kinds taxonomy (legacy authored docs,
// runbooks not yet migrated, vendor-imported markdown). Files matching any
// glob here are neither shape-linted nor counted toward required-kind
// presence. Globs are filepath.Match patterns against the path relative
// to docs/ (e.g. "lw-*.md", "vendor/**", "*.notes.md").
type LWDocsOverride struct {
	Tier            string           `yaml:"tier,omitempty"`
	DocsRequired    []string         `yaml:"docs_required,omitempty"`
	DocsRecommended []string         `yaml:"docs_recommended,omitempty"`
	SpecRequired    []string         `yaml:"spec_required,omitempty"`
	IgnoreGlobs     []string         `yaml:"ignore_globs,omitempty"`
	Freshness       *FreshnessPolicy `yaml:"freshness,omitempty"`
}

// Schemas bundles the governance schemas the factory consumes, plus the two
// closed enums `lw lint handoff` validates against (handoff block kinds + the
// handoff status lifecycle).
type Schemas struct {
	SpecKinds         []SpecArtifactKind
	DocKinds          []DocArtifactKind
	Manifest          RepoDocManifest
	HandoffBlockKinds []string
	HandoffStatuses   []string
}

// LoadSchemas reads the three governance YAMLs from lightwave-core. It honors
// the LW_LIGHTWAVE_CORE env var first, then falls back to the conventional
// `<lightwaveRoot>/../lightwave-core` sibling layout, then to
// `~/dev/lightwave-core` as a last resort.
func LoadSchemas(lightwaveRoot string) (*Schemas, error) {
	root, err := resolveLightwaveCoreRoot(lightwaveRoot)
	if err != nil {
		return nil, err
	}

	var s Schemas

	if err := loadYAMLField(
		filepath.Join(root, "src", "schemas", "policy", "governance", "spec_artifact_kinds.yaml"),
		"spec_artifact_kinds",
		&s.SpecKinds,
	); err != nil {
		return nil, fmt.Errorf("load spec_artifact_kinds: %w", err)
	}

	if err := loadYAMLField(
		filepath.Join(root, "src", "schemas", "policy", "governance", "doc_artifact_kinds.yaml"),
		"doc_artifact_kinds",
		&s.DocKinds,
	); err != nil {
		return nil, fmt.Errorf("load doc_artifact_kinds: %w", err)
	}

	if err := loadYAMLWhole(
		filepath.Join(root, "src", "schemas", "policy", "governance", "repo_doc_manifest.yaml"),
		&s.Manifest,
	); err != nil {
		return nil, fmt.Errorf("load repo_doc_manifest: %w", err)
	}

	enumsDir := filepath.Join(root, "src", "schemas", "data", "enums")
	if s.HandoffBlockKinds, err = loadEnumValues(filepath.Join(enumsDir, "handoff_block_kinds.yaml")); err != nil {
		return nil, fmt.Errorf("load handoff_block_kinds: %w", err)
	}
	if s.HandoffStatuses, err = loadEnumValues(filepath.Join(enumsDir, "handoff_statuses.yaml")); err != nil {
		return nil, fmt.Errorf("load handoff_statuses: %w", err)
	}

	return &s, nil
}

// SpecKindByName returns the kind entry whose `kind:` matches name.
func (s *Schemas) SpecKindByName(name string) (*SpecArtifactKind, bool) {
	for i := range s.SpecKinds {
		if s.SpecKinds[i].Kind == name {
			return &s.SpecKinds[i], true
		}
	}
	return nil, false
}

// DocKindByName returns the doc kind entry whose `kind:` matches name.
func (s *Schemas) DocKindByName(name string) (*DocArtifactKind, bool) {
	for i := range s.DocKinds {
		if s.DocKinds[i].Kind == name {
			return &s.DocKinds[i], true
		}
	}
	return nil, false
}

// ResolveForTier produces the tier-resolved, override-applied manifest for
// one repo. If the tier is unknown the defaults block is used as-is.
func (s *Schemas) ResolveForTier(tier string, override *LWDocsOverride) ResolvedManifest {
	tierSpec, ok := s.Manifest.Tiers[tier]

	merged := ResolvedManifest{
		Tier:            tier,
		SpecRequired:    s.Manifest.Defaults.SpecRequired,
		DocsRequired:    s.Manifest.Defaults.DocsRequired,
		DocsRecommended: s.Manifest.Defaults.DocsRecommended,
		Freshness:       s.Manifest.Freshness,
	}

	if ok {
		if len(tierSpec.SpecRequired) > 0 {
			merged.SpecRequired = tierSpec.SpecRequired
		}
		if len(tierSpec.DocsRequired) > 0 {
			merged.DocsRequired = tierSpec.DocsRequired
		}
		if len(tierSpec.DocsRecommended) > 0 {
			merged.DocsRecommended = tierSpec.DocsRecommended
		}
	}

	if override != nil {
		if override.Tier != "" {
			merged.Tier = override.Tier
		}
		if len(override.DocsRequired) > 0 {
			merged.DocsRequired = override.DocsRequired
		}
		if len(override.DocsRecommended) > 0 {
			merged.DocsRecommended = override.DocsRecommended
		}
		if len(override.SpecRequired) > 0 {
			merged.SpecRequired = override.SpecRequired
		}
		if len(override.IgnoreGlobs) > 0 {
			merged.IgnoreGlobs = override.IgnoreGlobs
		}
		if override.Freshness != nil {
			merged.Freshness = *override.Freshness
		}
	}

	return merged
}

// LoadOverride reads .lwdocs.yaml from a repo root. Missing file is not an
// error — returns nil and lets ResolveForTier use tier defaults.
func LoadOverride(repoRoot string) (*LWDocsOverride, error) {
	path := filepath.Join(repoRoot, ".lwdocs.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var ov LWDocsOverride
	if err := yaml.Unmarshal(data, &ov); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &ov, nil
}

// resolveLightwaveCoreRoot returns the absolute path to a checkout of
// lightwave-core. Honors EnvLightwaveCore first; otherwise falls back to a
// sibling of lightwave-root, then ~/dev/lightwave-core.
const EnvLightwaveCore = "LW_LIGHTWAVE_CORE"

func resolveLightwaveCoreRoot(lightwaveRoot string) (string, error) {
	if v := os.Getenv(EnvLightwaveCore); v != "" {
		return v, nil
	}
	candidates := []string{}
	if lightwaveRoot != "" {
		candidates = append(candidates, filepath.Join(lightwaveRoot, "..", "lightwave-core"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "dev", "lightwave-core"))
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(abs, "src", "schemas")); err == nil {
			return abs, nil
		}
	}
	return "", fmt.Errorf("lightwave-core not found (set %s)", EnvLightwaveCore)
}

// loadYAMLField reads a file and unmarshals the value of one top-level key
// into out. Lets us treat each governance YAML as a single-purpose file
// without exposing _meta and friends.
func loadYAMLField(path, field string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	node, ok := raw[field]
	if !ok {
		return fmt.Errorf("%s: missing top-level key %q", path, field)
	}
	return node.Decode(out)
}

func loadYAMLWhole(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, out)
}

// loadEnumValues reads a lightwave-core enum schema (shape: `options:` list of
// {value, label, ...}) and projects the `value` of each option. Used to load
// the closed handoff_block_kinds + handoff_statuses sets for `lw lint handoff`.
func loadEnumValues(path string) ([]string, error) {
	var opts []struct {
		Value string `yaml:"value"`
	}
	if err := loadYAMLField(path, "options", &opts); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.Value)
	}
	return out, nil
}
