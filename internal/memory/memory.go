// Package memory is the v_core persisted-state KV store referenced in
// Phase 3 of the EB-001 plan (lw memory put|get|list).
//
// Storage model: filesystem. Each (namespace, key) maps to a single file
// under `~/.lightwave/memory/<namespace>/<key>`. Values are raw bytes;
// callers decide their own encoding (JSON, YAML, plain text). Namespaces
// are keyed to agent user-id per the plan — `v_core` writes under
// `v_core/`, `cpe` under `platform-engineer/`, etc.
//
// Phase A only. Phase B (EB-005) will sync this into a platform-side
// table; the CLI surface stays identical.
package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNotFound is returned by Get when no value exists at (namespace, key).
var ErrNotFound = errors.New("memory entry not found")

// Root returns the on-disk root for memory storage, creating it if
// missing (0o700 — entries may hold sensitive state like API tokens
// when v_core orchestrates).
func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".lightwave", "memory")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return dir, nil
}

// Put writes value at (namespace, key), creating the namespace directory
// if missing. Write is atomic (tmp + rename) so concurrent readers never
// see a partial value.
func Put(namespace, key string, value []byte) (string, error) {
	if err := validateNamespace(namespace); err != nil {
		return "", err
	}
	if err := validateKey(key); err != nil {
		return "", err
	}
	root, err := Root()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, namespace, key)
	// Hierarchical keys (foo/bar/baz) need their intermediate subdirs.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, value, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

// Get returns the value stored at (namespace, key). Returns ErrNotFound
// (wrapped) when no entry exists.
func Get(namespace, key string) ([]byte, error) {
	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}
	root, err := Root()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, namespace, key)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s/%s", ErrNotFound, namespace, key)
		}
		return nil, err
	}
	return data, nil
}

// List returns every key under namespace, sorted. Walks subdirectories
// so hierarchical keys (foo/bar/baz) round-trip with their full path.
// Hidden files (leading `.`) and atomic-write tmp files are skipped.
func List(namespace string) ([]string, error) {
	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}
	root, err := Root()
	if err != nil {
		return nil, err
	}
	nsDir := filepath.Join(root, namespace)
	if _, err := os.Stat(nsDir); errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	} else if err != nil {
		return nil, err
	}

	var keys []string
	walkErr := filepath.WalkDir(nsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".tmp") {
			return nil
		}
		rel, err := filepath.Rel(nsDir, path)
		if err != nil {
			return err
		}
		// Normalise to forward slashes so the key surface is OS-independent.
		keys = append(keys, filepath.ToSlash(rel))
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Strings(keys)
	return keys, nil
}

// Delete removes (namespace, key). Returns nil even when the entry was
// already missing — delete is idempotent.
func Delete(namespace, key string) error {
	if err := validateNamespace(namespace); err != nil {
		return err
	}
	if err := validateKey(key); err != nil {
		return err
	}
	root, err := Root()
	if err != nil {
		return err
	}
	path := filepath.Join(root, namespace, key)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Namespaces returns every directory directly under Root(), sorted.
// Useful for `lw memory list` with no --namespace flag.
func Namespaces() ([]string, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// validateNamespace rejects empty / traversal patterns. Allowed runes:
// alnum, dash, underscore, dot — same charset as a unix-friendly slug.
// `.` and `..` as a whole token are rejected to prevent path traversal.
func validateNamespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("namespace is required")
	}
	if ns == "." || ns == ".." {
		return fmt.Errorf("namespace %q is reserved", ns)
	}
	for _, r := range ns {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return fmt.Errorf("namespace %q contains illegal rune %q (allowed: a-z A-Z 0-9 - _ .)", ns, r)
		}
	}
	return nil
}

// validateKey accepts the namespace charset plus `/` for hierarchical
// keys (e.g. `tasks/T-0001/status`). Each path segment is checked for
// `.`/`..` traversal.
func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("key is required")
	}
	if strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
		return fmt.Errorf("key %q must not have leading or trailing slash", key)
	}
	for seg := range strings.SplitSeq(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("key %q contains invalid segment %q", key, seg)
		}
		for _, r := range seg {
			switch {
			case r >= 'a' && r <= 'z':
			case r >= 'A' && r <= 'Z':
			case r >= '0' && r <= '9':
			case r == '-' || r == '_' || r == '.':
			default:
				return fmt.Errorf("key %q contains illegal rune %q (allowed per segment: a-z A-Z 0-9 - _ .)", key, r)
			}
		}
	}
	return nil
}
