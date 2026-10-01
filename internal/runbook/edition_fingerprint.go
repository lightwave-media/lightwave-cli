package runbook

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// editionFingerprint binds the directory (including helpers and inputs) and
// explicit in-Core path assets. The separately pinned clean Core commit covers
// shared tracked validators and schemas; external programs remain dependencies.
func editionFingerprint(core string, edition *Edition) (string, error) {
	root, err := filepath.EvalSymlinks(core)
	if err != nil {
		return "", err
	}

	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}

	dir, err := filepath.Abs(filepath.Dir(edition.Path))
	if err != nil {
		return "", err
	}

	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}

	paths := []string{dir}

	for i := range edition.Steps {
		if path := edition.Steps[i].Path; path != "" {
			paths = append(paths, filepath.Join(dir, path))
		}
	}

	raw, err := os.ReadFile(edition.Path)
	if err != nil {
		return "", err
	}

	for _, match := range inputsRe.FindAllStringSubmatch(string(raw), -1) {
		if path := parseAttrs(match[1])["path"]; path != "" {
			paths = append(paths, filepath.Join(dir, path))
		}
	}

	files := map[string]string{}
	for _, path := range paths {
		if err := fingerprintPath(root, path, files); err != nil {
			return "", fmt.Errorf("%w: %w", ErrEditionMismatch, err)
		}
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}

	sort.Strings(names)

	hash := sha256.New()
	for _, name := range names {
		_, _ = fmt.Fprintf(hash, "%q %s\n", name, files[name])
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func fingerprintPath(root, path string, files map[string]string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("edition asset is outside Core: %s", path)
	}

	resolved, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		// Preserve ordinary Apply's missing-script error while pinning absence.
		files[filepath.ToSlash(rel)] = "missing"
		return nil
	}

	if err != nil {
		return err
	}

	if resolved != path {
		return fmt.Errorf("edition asset uses a symlink: %s", rel)
	}

	return filepath.WalkDir(path, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			return nil
		}

		if !entry.Type().IsRegular() {
			return fmt.Errorf("edition asset is not a regular file: %s", file)
		}

		name, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}

		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}

		sum := sha256.Sum256(raw)
		files[filepath.ToSlash(name)] = hex.EncodeToString(sum[:])

		return nil
	})
}

func verifyEdition(opts *ApplyOpts, inst *Instance, edition *Edition) error {
	hash := edition.Hash // Legacy instances retain the old MDX-only check.

	if inst.ExecutionStarted != nil {
		var err error

		hash, err = editionFingerprint(opts.CoreRoot, edition)
		if err != nil {
			return err
		}
	}

	if hash != inst.EditionHash {
		return ErrEditionMismatch
	}

	if inst.EditionCommit != "" {
		commit, err := cleanCommit(opts.CoreRoot)
		if err != nil || commit != inst.EditionCommit {
			return fmt.Errorf("%w: Core source revision changed or is not clean", ErrEditionMismatch)
		}
	}

	return nil
}

// recheckEdition also observes changes made by an executed verifier itself.
func recheckEdition(opts *ApplyOpts, inst *Instance, entry Entry) error {
	edition, err := LoadEdition(opts.CoreRoot, entry)
	if err == nil {
		err = verifyEdition(opts, inst, edition)
	}

	if err == nil {
		return nil
	}

	inst.Commit = ""

	inst.Status = StatusFailed
	if saveErr := Save(opts.Cwd, inst); saveErr != nil {
		return saveErr
	}

	return err
}
