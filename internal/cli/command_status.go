package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Command trust policy — "a release tag must mean something."
//
// Only commands verified to work end-to-end are exposed. Everything unverified
// is DECOMMISSIONED: hidden from `--help` and refusing to run, with a message
// pointing at docs/command-status.md and the restore path. The
// command_surface_test guard fails the build if a visible command is not in
// VerifiedCommands, so the surface can't silently regrow.

// VerifiedCommands is the active, end-to-end-verified surface. Adding a name
// here is a promise: the command has a passing e2e/smoke test. Restoring a
// decommissioned command = verify it, add a test, move it here, delete its row
// from DecommissionedCommands.
var VerifiedCommands = map[string]bool{
	// cobra built-ins
	"help":       true,
	"completion": true,

	// verified native commands (see docs/command-status.md for the test backing each)
	"version":  true,
	"config":   true,
	"health":   true,
	"memory":   true,
	"worktree": true,
	"audit":    true,
	"scaffold": true,
	"ui":       true,
	"research": true,
	"docs":     true, // spec/+docs/ factory — test backing in internal/docsfactory/*_test.go
}

// DecommissionedCommands are taken OFFLINE pending end-to-end verification.
// The value is what's required to bring it back. Kept in source (not deleted)
// so restoration is a one-line move once a verification harness for it exists.
var DecommissionedCommands = map[string]string{
	"aws":     "live AWS credentials + ECS; needs an e2e harness",
	"github":  "gh CLI + platform repo + Postgres",
	"council": "Augusta service (localhost:9700)",
	"msg":     "gateway service (localhost:9701)",
	"v_core":  "vcore daemon binary (lightwave-sys)",
	"agent":   "spawns real agent processes; provision path is a stub",
	"make":    "monorepo Makefiles (absent in this repo)",
	"test":    "monorepo make targets",
	"setup":   "monorepo make targets",
	"cdn":     "make + live S3",
	"content": "make + Django stack",
	"drift":   "make + Django stack",
	"email":   "make + Django stack",
	"codegen": "lightwave-core journey YAMLs",
	"browser": "macOS osascript automation; flaky (audit verdict: drop)",
	"spec":    "legacy parked tree pending schema merge",
	"sst":     "depends on ~/.brain corpus state",
}

// applyDecommissions hides and disables every decommissioned command and its
// whole subtree on the assembled root. Idempotent; called from Execute().
func applyDecommissions(root *cobra.Command) {
	for _, c := range root.Commands() {
		reason, offline := DecommissionedCommands[c.Name()]
		if !offline {
			continue
		}

		disableSubtree(c, c.Name(), reason)
	}
}

// disableSubtree marks a command (and recursively its subcommands) hidden and
// makes any invocation return a clear offline error.
func disableSubtree(c *cobra.Command, path, reason string) {
	c.Hidden = true
	c.Args = cobra.ArbitraryArgs
	c.RunE = func(_ *cobra.Command, _ []string) error {
		return fmt.Errorf("`lw %s` is decommissioned (offline): %s — see docs/command-status.md", path, reason)
	}

	for _, sub := range c.Commands() {
		disableSubtree(sub, path+" "+sub.Name(), reason)
	}
}
