//nolint:testpackage // reads the rootCmd singleton to see what init() hand-attached
package cli

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// A domain can reach the root twice: once from a hardcoded rootCmd.AddCommand
// in init(), and again from the schema dispatcher. That is what happened to
// `runbook` — #335 shipped it on a hardcoded tree, #338 registered its handlers,
// and the stamp publishes the domain with no `_status`, so both attached and
// `lw --help` listed it twice.
//
// legacyHardcodedDomains() is the mechanism that prevents it: a domain still
// wired by hand must be listed there so the dispatcher skips it. This guards the
// class, not just runbook.
//
// Deliberately does NOT assemble onto rootCmd. An earlier draft called the real
// assembly path, which mutates that singleton — and TestCommandSurface_
// OnlyVerifiedExposed reads it, so under `-shuffle=on` the two raced for order
// and the suite failed ~40% of runs. rootCmd is only read here; the dispatcher
// side is built onto a throwaway root.
//
//nolint:paralleltest // reads the rootCmd singleton; must stay serial
func TestNoDomainIsBothHandWiredAndDispatched(t *testing.T) {
	if _, err := config.Load(); err != nil {
		t.Skipf("config unavailable: %v", err)
	}

	handWired := map[string]bool{}
	for _, cmd := range rootCmd.Commands() {
		handWired[cmd.Name()] = true
	}

	// Empty override set: attach every domain the stamp publishes, so the
	// overlap below is measured against the full dispatcher surface rather
	// than one already filtered by the exemption list under test.
	dispatched := &cobra.Command{Use: "lw"}
	if err := BuildDispatched(dispatched, map[string]bool{}); err != nil {
		t.Skipf("dispatcher unavailable (schema not checked out?): %v", err)
	}

	exempt := legacyHardcodedDomains()

	collisions := []string{}

	for _, cmd := range dispatched.Commands() {
		name := cmd.Name()
		if handWired[name] && !exempt[name] {
			collisions = append(collisions, name)
		}
	}

	require.Empty(t, collisions,
		"these domains are attached by both init() and the dispatcher, so `lw --help` lists them twice — "+
			"either add each to legacyHardcodedDomains() or drop its rootCmd.AddCommand")
}
