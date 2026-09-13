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

// TestUnknownVerbError covers the half RejectUnknownSubcommands structurally
// cannot: cobra returns flag.ErrHelp from the help check BEFORE it calls
// ValidateArgs, so with --help on the line the Args validator never runs and an
// unknown verb exits 0 printing the parent's help.
//
// The damage is not the exit code, it is that `--help` is how a caller asks
// whether a command exists and the answer was identical either way. Measured on
// the shipped v3.13.0 binary and a build of main:
//
//	lw runbook apply --help      exit 0   (a real verb)
//	lw runbook zzzznope --help   exit 0   (nothing at all)
//
// #417 probed six candidate verbs this way and reported all six present.
func TestUnknownVerbError(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		argv    []string
		wantErr bool
	}{
		// The regression. Each of these addresses a verb that does not exist.
		"unknown verb":                  {[]string{"release", "nosuchverb"}, true},
		"unknown verb with --help":      {[]string{"release", "nosuchverb", "--help"}, true},
		"unknown verb with -h":          {[]string{"release", "nosuchverb", "-h"}, true},
		"unknown top-level":             {[]string{"nosuchgroup"}, true},
		"unknown top-level with --help": {[]string{"nosuchgroup", "--help"}, true},

		// Known-good controls. If the detector cannot stay silent on these it
		// is not measuring "does this verb exist", it is just failing.
		"empty argv":          {nil, false},
		"bare group":          {[]string{"release"}, false},
		"group --help":        {[]string{"release", "--help"}, false},
		"group -h":            {[]string{"release", "-h"}, false},
		"real leaf":           {[]string{"release", "tag"}, false},
		"real leaf --help":    {[]string{"release", "tag", "--help"}, false},
		"leaf positional arg": {[]string{"release", "tag", "v1.2.3"}, false},
	}

	for name, testCase := range cases {
		tt := testCase
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root, _ := newTestTree()

			err := cli.UnknownVerbError(root, tt.argv)
			if !tt.wantErr {
				assert.NoError(t, err)

				return
			}

			require.Error(t, err)
		})
	}
}

// TestUnknownVerbError_FlagValueIsNotAVerb pins the false positive this was
// deliberately built to avoid.
//
// Command.Find returns the leftover args with flags AND their values intact, so
// a detector that scanned all of them would read `lw release --config x.yaml`
// as an unknown verb named "x.yaml" and break a working command. Recovering the
// real positional set means re-parsing flags, and a second ParseFlags
// re-appends every repeatable flag — `--label a --label b` would arrive as four
// values. Only the first leftover token is examined, which trades a missed verb
// behind a value-taking flag (harmless) for never inventing one (not harmless).
func TestUnknownVerbError_FlagValueIsNotAVerb(t *testing.T) {
	t.Parallel()

	root, _ := newTestTree()
	root.PersistentFlags().String("config", "", "config file")

	assert.NoError(t, cli.UnknownVerbError(root, []string{"release", "--config", "some/path.yaml"}),
		"a flag's value must never be read as a subcommand name")
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
