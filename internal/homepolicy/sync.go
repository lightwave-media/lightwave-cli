// Package homepolicy syncs operator baseline policy stamps from the
// lightwave-home blueprint into ~/.lightwave without a full home render.
package homepolicy

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
	"github.com/lightwave-media/lightwave-cli/internal/config"
)

// baselinePolicyDirs are stamp subtrees copied when drift is detected.
// Runtime toggles (e.g. flags.toml) are never overwritten — only stamp files.
var baselinePolicyDirs = []string{
	"config/flags",
}

const (
	dirPerm  = 0o755
	filePerm = 0o644
)

// SyncResult lists relative paths updated under ~/.lightwave.
type SyncResult struct {
	Updated []string
}

// HomeBlueprintRoot returns the lightwave-home blueprint directory.
func HomeBlueprintRoot() (string, error) {
	lib, err := blueprintLibrary()
	if err != nil {
		return "", err
	}

	return blueprint.Resolve(lib, "lightwave-home")
}

func blueprintLibrary() (string, error) {
	root := fleetRoot()

	dir := blueprint.BlueprintsDir(root)
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("blueprint library %s: %w (set LW_BLUEPRINTS_DIR or run from ~/dev)", dir, err)
	}

	return dir, nil
}

func fleetRoot() string {
	if bp := os.Getenv(blueprint.EnvBlueprintsDir); bp != "" {
		// .../lightwave-core/src/boilerplate/blueprints → ~/dev
		return filepath.Clean(filepath.Join(bp, "..", "..", "..", ".."))
	}

	root := configLightwaveRoot()
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "dev")
	}

	return root
}

func configLightwaveRoot() string {
	cfg := config.Get()
	if cfg != nil && cfg.Paths.LightwaveRoot != "" {
		return cfg.Paths.LightwaveRoot
	}

	return ""
}

func homePrintRoot() string {
	if p := os.Getenv("LW_HOME_PRINT"); p != "" {
		return p
	}

	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".lightwave")
}

// SyncBaseline copies policy stamp files from lightwave-home into ~/.lightwave
// when content differs. Idempotent; safe to call on every session start.
func SyncBaseline() (*SyncResult, error) {
	bpRoot, err := HomeBlueprintRoot()
	if err != nil {
		return nil, err
	}

	printRoot := homePrintRoot()
	out := &SyncResult{}

	for _, relDir := range baselinePolicyDirs {
		stampDir := filepath.Join(bpRoot, relDir)
		if _, err := os.Stat(stampDir); err != nil {
			continue
		}

		err := filepath.WalkDir(stampDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			if d.IsDir() {
				return nil
			}

			rel, err := filepath.Rel(bpRoot, path)
			if err != nil {
				return err
			}

			updated, err := syncFileIfDrift(filepath.Join(bpRoot, rel), filepath.Join(printRoot, rel))
			if err != nil {
				return err
			}

			if updated {
				out.Updated = append(out.Updated, rel)
			}

			return nil
		})
		if err != nil {
			return out, fmt.Errorf("sync %s: %w", relDir, err)
		}
	}

	return out, nil
}

// SyncFlagsRegistry is the narrow path used before flag evaluation.
func SyncFlagsRegistry() (bool, error) {
	bpRoot, err := HomeBlueprintRoot()
	if err != nil {
		return false, err
	}

	stamp := filepath.Join(bpRoot, "config", "flags", "registry.yaml")
	printRoot := homePrintRoot()
	dest := filepath.Join(printRoot, "config", "flags", "registry.yaml")

	updated, err := syncFileIfDrift(stamp, dest)

	return updated, err
}

func syncFileIfDrift(stamp, dest string) (bool, error) {
	stampData, err := os.ReadFile(stamp)
	if err != nil {
		return false, fmt.Errorf("read stamp %s: %w", stamp, err)
	}

	destData, err := os.ReadFile(dest)
	if err == nil && bytes.Equal(stampData, destData) {
		return false, nil
	}

	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read print %s: %w", dest, err)
	}

	if err := os.MkdirAll(filepath.Dir(dest), dirPerm); err != nil {
		return false, err
	}

	if err := os.WriteFile(dest, stampData, filePerm); err != nil {
		return false, err
	}

	return true, nil
}

// CopyFile is exported for tests that need a direct copy helper.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)

	return err
}
