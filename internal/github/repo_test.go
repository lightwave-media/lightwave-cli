package github_test

import (
	"testing"

	gh "github.com/lightwave-media/lightwave-cli/internal/github"
	"github.com/stretchr/testify/assert"
)

// TestQualifyRepo covers #372: --repo was forwarded to gh verbatim and --org was
// collected but never composed into the slug, so the invocation the conventions
// ask for failed with `expected the "[HOST/]OWNER/REPO"`.
func TestQualifyRepo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		repo string
		org  string
		want string
	}{
		{
			name: "bare name gains the org — the #372 case",
			repo: "lightwave-cli",
			org:  "lightwave-media",
			want: "lightwave-media/lightwave-cli",
		},
		{
			name: "already qualified is left alone",
			repo: "lightwave-media/lightwave-core",
			org:  "lightwave-media",
			want: "lightwave-media/lightwave-core",
		},
		{
			name: "a different owner is not rewritten to the org",
			repo: "someone-else/their-repo",
			org:  "lightwave-media",
			want: "someone-else/their-repo",
		},
		{
			name: "host-qualified passes through — gh accepts HOST/OWNER/REPO",
			repo: "github.com/lightwave-media/lightwave-cli",
			org:  "lightwave-media",
			want: "github.com/lightwave-media/lightwave-cli",
		},
		{
			name: "empty repo stays empty so the caller's own resolution runs",
			repo: "",
			org:  "lightwave-media",
			want: "",
		},
		{
			name: "no org means nothing to qualify with",
			repo: "lightwave-cli",
			org:  "",
			want: "lightwave-cli",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, gh.QualifyRepo(tc.repo, tc.org))
		})
	}
}
