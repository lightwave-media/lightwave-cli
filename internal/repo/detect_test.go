package repo_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/repo"
)

func TestDetect_ClassifiesByOriginURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		remote string
		want   repo.ID
	}{
		{"cli ssh", "git@github.com:lightwave-media/lightwave-cli.git", repo.LightwaveCLI},
		{"sys https", "https://github.com/lightwave-media/lightwave-sys", repo.LightwaveSys},
		{"platform", "git@github.com:lightwave-media/lightwave-platform.git", repo.LightwavePlatform},
		{"core", "git@github.com:lightwave-media/lightwave-core.git", repo.LightwaveCore},
		{"ui", "git@github.com:lightwave-media/lightwave-ui.git", repo.LightwaveUI},
		{"media marketing", "git@github.com:lightwave-media/lightwave-media.git", repo.LightwaveMedia},
		{"unknown", "git@github.com:someoneelse/whatever.git", repo.Other},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := makeRepo(t, tc.remote, "")

			info, err := repo.Detect(dir)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}

			if info.ID != tc.want {
				t.Fatalf("ID = %q, want %q", info.ID, tc.want)
			}

			if info.Root != dir {
				t.Fatalf("Root = %q, want %q", info.Root, dir)
			}
		})
	}
}

func TestDetect_FallsBackToDirectoryName(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	dir := filepath.Join(parent, "lightwave-cli")

	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := exec.CommandContext(context.Background(), "git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}

	info, err := repo.Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if info.ID != repo.LightwaveCLI {
		t.Fatalf("ID = %q, want %q", info.ID, repo.LightwaveCLI)
	}
}

func TestDetect_WalksUpToGitRoot(t *testing.T) {
	t.Parallel()

	root := makeRepo(t, "git@github.com:lightwave-media/lightwave-sys.git", "")
	sub := filepath.Join(root, "a", "b", "c")

	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	info, err := repo.Detect(sub)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if info.Root != root {
		t.Fatalf("Root = %q, want %q", info.Root, root)
	}

	if info.ID != repo.LightwaveSys {
		t.Fatalf("ID = %q, want %q", info.ID, repo.LightwaveSys)
	}
}

func TestDetect_NoGitErrors(t *testing.T) {
	t.Parallel()

	if _, err := repo.Detect("/tmp"); err == nil {
		t.Fatalf("expected error for non-repo path")
	}
}

func makeRepo(t *testing.T, remote, name string) string {
	t.Helper()

	parent := t.TempDir()
	dir := parent

	if name != "" {
		dir = filepath.Join(parent, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	if err := exec.CommandContext(ctx, "git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	if remote != "" {
		if err := exec.CommandContext(ctx, "git", "-C", dir, "remote", "add", "origin", remote).Run(); err != nil {
			t.Fatalf("git remote add: %v", err)
		}
	}

	return dir
}
