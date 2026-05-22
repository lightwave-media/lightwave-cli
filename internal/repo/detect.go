// Package repo identifies which LightWave repository the working directory
// belongs to so callers (`lw local gate`, doctor, etc.) can dispatch
// per-repo stage implementations.
//
// Detection walks up from the start path looking for a `.git` entry, then
// matches the origin remote URL against known repo names. The git root —
// not the cwd — is the reported Root, so callers can chdir there before
// shelling out.
package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lightwave-media/lightwave-cli/internal/git"
)

// ID enumerates the repos `lw local *` knows how to drive. Anything outside
// the list reports as Other so callers can degrade gracefully (skip the
// repo-specific stages, run only the language-generic ones).
type ID string

const (
	LightwaveCLI      ID = "lightwave-cli"
	LightwaveSys      ID = "lightwave-sys"
	LightwavePlatform ID = "lightwave-platform"
	LightwaveMedia    ID = "lightwave-media"
	LightwaveCore     ID = "lightwave-core"
	LightwaveUI       ID = "lightwave-ui"
	Other             ID = "other"
)

// Info describes a detected repo.
type Info struct {
	ID     ID
	Root   string
	Remote string
}

// Detect walks up from start (defaults to cwd when empty) and returns the
// containing repo. Returns an error only when there is no `.git` anywhere
// up the tree — an unrecognised repo still resolves to Info{ID: Other}.
func Detect(start string) (Info, error) {
	if start == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Info{}, fmt.Errorf("cwd: %w", err)
		}

		start = cwd
	}

	abs, err := filepath.Abs(start)
	if err != nil {
		return Info{}, fmt.Errorf("abs %q: %w", start, err)
	}

	root, err := findGitRoot(abs)
	if err != nil {
		return Info{}, err
	}

	g := git.NewGit(root)
	remote, _ := g.RemoteURL("origin")

	return Info{
		ID:     classify(root, remote),
		Root:   root,
		Remote: remote,
	}, nil
}

// findGitRoot walks parents until it finds a `.git` directory or file
// (worktree pointer). Returns the directory containing it.
func findGitRoot(start string) (string, error) {
	dir := start

	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no git repository at or above %s", start)
		}

		dir = parent
	}
}

// classify picks a repo ID from the origin URL's last path segment first,
// the root directory name second. Substring matching on the full URL is
// wrong because every LightWave URL contains the org name "lightwave-
// media" — matching that would misclassify lightwave-core as lightwave-
// media. Match the repo name only.
func classify(root, remote string) ID {
	if id := nameToID(repoNameFromURL(remote)); id != Other {
		return id
	}

	return nameToID(filepath.Base(root))
}

// repoNameFromURL extracts the repo name from a git URL.
//
//	git@github.com:lightwave-media/lightwave-cli.git → "lightwave-cli"
//	https://github.com/lightwave-media/lightwave-sys → "lightwave-sys"
//	https://github.com/foo/bar.git/                  → "bar"
//
// Empty string when nothing usable is in the URL.
func repoNameFromURL(url string) string {
	s := strings.TrimSpace(url)
	if s == "" {
		return ""
	}

	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	// SSH form uses ':' as the path separator; treat it the same as '/'.
	s = strings.ReplaceAll(s, ":", "/")

	idx := strings.LastIndex(s, "/")
	if idx < 0 {
		return s
	}

	return s[idx+1:]
}

func nameToID(name string) ID {
	switch strings.ToLower(name) {
	case "lightwave-cli":
		return LightwaveCLI
	case "lightwave-sys":
		return LightwaveSys
	case "lightwave-platform":
		return LightwavePlatform
	case "lightwave-media":
		return LightwaveMedia
	case "lightwave-core":
		return LightwaveCore
	case "lightwave-ui":
		return LightwaveUI
	}

	return Other
}
