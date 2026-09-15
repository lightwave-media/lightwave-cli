// Package maintenance measures the rendered ~/.lightwave print against the
// shape lightwave-core stamps for it, and repairs the drift it is safe to
// repair.
//
// ~/.lightwave is a PRINT. Its shape is declared by data/meta/homedir.yaml,
// and data/meta/homedir_zones.yaml classifies each top-level dir into a zone
// that says whether losing its contents is recoverable. Slop is the gap: files
// and directories the print has grown that the stamp never declared, logs past
// their rotation budget, empty orphans, broken links.
//
// A tested detector already existed at ~/.lightwave/lib/maintenance/slop.ts.
// This is a port rather than a wrapper, because shelling out to bun would add
// to the exec surface #349 is trying to shrink, and because the detector needs
// to be callable from a handler that CI can run without a bun install.
package maintenance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Shape is what the stamp declares about the print.
type Shape struct {
	// TopLevel is every top-level dir homedir.yaml declares.
	TopLevel map[string]bool
	// Zone maps a top-level dir to its zone name.
	Zone map[string]string
	// Wipeable is the subset whose zone carries wipe_on_reset: true.
	Wipeable map[string]bool
	// ZonesVersion is homedir_zones.yaml's declared version, for reporting.
	ZonesVersion string
}

// dirSpec is one entry in homedir.yaml's example.dirs.
type dirSpec struct {
	Path string `yaml:"path"`
	// NestedDirs is read so the parse fails loudly if the shape changes, and
	// because a future verb will want the nested structure. Top-level scanning
	// needs only Path.
	NestedDirs []dirSpec `yaml:"nested_dirs"`
}

type homedirDoc struct {
	Example struct {
		Dirs []dirSpec `yaml:"dirs"`
	} `yaml:"example"`
}

type zoneSpec struct {
	Dirs        []string `yaml:"dirs"`
	WipeOnReset bool     `yaml:"wipe_on_reset"`
}

type zonesDoc struct {
	Example struct {
		Zones   map[string]zoneSpec `yaml:"zones"`
		Version string              `yaml:"version"`
	} `yaml:"example"`
}

// LoadShape reads both halves of the home stamp.
//
// It refuses a stamp that parses to nothing. The TypeScript detector matched
// `^ {4}- path: "..."` with a regular expression, so a re-indent or a move to
// a block list would have yielded an empty declared-set and reported every
// directory on the machine as undeclared — or, with the comparison the other
// way, reported a clean print. A check whose subject does not exist must say
// so, not return a plausible answer.
func LoadShape(lightwaveRoot string) (Shape, error) {
	base := filepath.Join(lightwaveRoot, "lightwave-core", "src", "schemas", "data", "meta")

	var home homedirDoc
	if err := readYAML(filepath.Join(base, "homedir.yaml"), &home); err != nil {
		return Shape{}, err
	}

	if len(home.Example.Dirs) == 0 {
		return Shape{}, errors.New("homedir.yaml declared no directories — the stamp is unreadable, not empty")
	}

	shape := Shape{
		TopLevel: make(map[string]bool, len(home.Example.Dirs)),
		Zone:     map[string]string{},
		Wipeable: map[string]bool{},
	}

	for _, d := range home.Example.Dirs {
		if d.Path != "" {
			shape.TopLevel[d.Path] = true
		}
	}

	var zones zonesDoc
	if err := readYAML(filepath.Join(base, "homedir_zones.yaml"), &zones); err != nil {
		return Shape{}, err
	}

	if len(zones.Example.Zones) == 0 {
		return Shape{}, errors.New("homedir_zones.yaml classified no directories — the stamp is unreadable, not empty")
	}

	shape.ZonesVersion = zones.Example.Version

	for name, spec := range zones.Example.Zones {
		for _, dir := range spec.Dirs {
			shape.Zone[dir] = name

			if spec.WipeOnReset {
				shape.Wipeable[dir] = true
			}
		}
	}

	return shape, nil
}

func readYAML(path string, into any) error {
	data, err := os.ReadFile(path) //nolint:gosec // a stamp path derived from config
	if err != nil {
		return fmt.Errorf("read home stamp %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, into); err != nil {
		return fmt.Errorf("parse home stamp %s: %w", path, err)
	}

	return nil
}

// Unclassified reports dirs homedir.yaml declares that no zone classifies.
//
// homedir_zones.yaml's own notes say every dir name MUST be a top-level dir
// declared in homedir.yaml, and that the two move in lockstep. Nothing checked
// it. An unclassified dir has no wipe policy, so every destructive verb here
// has to treat it as not-wipeable, which is safe but silent — this makes it
// visible instead.
func (s Shape) Unclassified() []string {
	var missing []string

	for dir := range s.TopLevel {
		if s.Zone[dir] == "" {
			missing = append(missing, dir)
		}
	}

	sortStrings(missing)

	return missing
}

// Phantom reports dirs a zone classifies that homedir.yaml does not declare —
// the same lockstep claim, checked in the other direction.
func (s Shape) Phantom() []string {
	var extra []string

	for dir := range s.Zone {
		if !s.TopLevel[dir] {
			extra = append(extra, dir)
		}
	}

	sortStrings(extra)

	return extra
}
