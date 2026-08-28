package git_test

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/git"
	"github.com/stretchr/testify/assert"
)

func TestOriginSlugFromURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		url  string
		want string
	}{
		{"scp style", "git@github.com:lightwave-media/lightwave-cli.git", "lightwave-media/lightwave-cli"},
		{"scp style without .git", "git@github.com:lightwave-media/lightwave-cli", "lightwave-media/lightwave-cli"},
		{"https", "https://github.com/lightwave-media/lightwave-cli.git", "lightwave-media/lightwave-cli"},
		{"https without .git", "https://github.com/lightwave-media/lightwave-cli", "lightwave-media/lightwave-cli"},
		{"trailing slash", "https://github.com/lightwave-media/lightwave-cli/", "lightwave-media/lightwave-cli"},
		{"ssh scheme", "ssh://git@github.com/lightwave-media/lightwave-cli.git", "lightwave-media/lightwave-cli"},
		{"surrounding whitespace", "  git@github.com:lightwave-media/lightwave-core.git\n", "lightwave-media/lightwave-core"},
		{"a different org is preserved", "git@github.com:kiwi-dev-la/nixos-config.git", "kiwi-dev-la/nixos-config"},

		// Anything that cannot name both an owner and a repo yields "" so the
		// caller falls back rather than passing a malformed slug to gh.
		{"empty", "", ""},
		{"single segment", "lightwave-cli", ""},
		{"host only", "https://github.com/", ""},
	}

	for _, testCase := range cases {
		tt := testCase
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, git.OriginSlugFromURL(tt.url))
		})
	}
}

// The bug this pins: a checkout's directory name is arbitrary — worktrees are
// named after tickets — so deriving the repo from the path gives the wrong
// answer. Only the remote is authoritative. Same reasoning as dev/hooks/pre-push
// (#330), which is why both now agree.
func TestOriginSlugFromURL_IgnoresDirectoryNaming(t *testing.T) {
	t.Parallel()

	const worktreeStyleRemote = "git@github.com:lightwave-media/lightwave-cli.git"

	assert.Equal(t,
		"lightwave-media/lightwave-cli",
		git.OriginSlugFromURL(worktreeStyleRemote),
		"slug must come from the remote, never from a worktree directory like .worktrees/2026-08-27-arm-drift-gate")
}
