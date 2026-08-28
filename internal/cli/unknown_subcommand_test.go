package cli_test

import (
	"bytes"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/cli"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestTree builds root -> group -> leaf, matching the shape the dispatcher
// produces: a group with no Run of its own.
func newTestTree() (root, group *cobra.Command) {
	root = &cobra.Command{Use: "lw"}
	group = &cobra.Command{Use: "release", Short: "release things"}
	leaf := &cobra.Command{Use: "tag", RunE: func(*cobra.Command, []string) error { return nil }}

	group.AddCommand(leaf)
	root.AddCommand(group)

	return root, group
}

func runTree(t *testing.T, args ...string) error {
	t.Helper()

	root, _ := newTestTree()
	cli.RejectUnknownSubcommands(root)
	root.SetArgs(args)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	return root.Execute()
}

// The bug: cobra returns flag.ErrHelp for a non-runnable group, which ExecuteC
// turns into "print help, exit 0". A mistyped verb in CI therefore went green.
func TestRejectUnknownSubcommands_UnknownVerbErrors(t *testing.T) {
	t.Parallel()

	err := runTree(t, "release", "nosuchverb")

	require.Error(t, err, "an unknown verb must not exit 0 — that is what greened CI steps in #303")
	assert.Contains(t, err.Error(), `unknown command "nosuchverb"`)
	assert.Contains(t, err.Error(), "lw release", "the error should name the group that was addressed")
}

func TestRejectUnknownSubcommands_PreservesValidUse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
	}{
		{"bare group still prints help", []string{"release"}},
		{"group help flag", []string{"release", "--help"}},
		{"real leaf still runs", []string{"release", "tag"}},
	}

	for _, testCase := range cases {
		tt := testCase
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, runTree(t, tt.args...))
		})
	}
}

// A group that states its own Args has made a deliberate choice; the sweep must
// not overwrite it.
func TestRejectUnknownSubcommands_LeavesExplicitArgsAlone(t *testing.T) {
	t.Parallel()

	root, group := newTestTree()
	group.Args = cobra.ArbitraryArgs
	group.RunE = func(*cobra.Command, []string) error { return nil }

	cli.RejectUnknownSubcommands(root)
	root.SetArgs([]string{"release", "anything"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	assert.NoError(t, root.Execute())
}
