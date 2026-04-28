package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/sst"
	"github.com/spf13/cobra"
)

// BuildDispatched walks the loaded SST schema and attaches a cobra subcommand
// for every (domain, command) that has a registered handler.
//
// During the Phase 4 migration this coexists with the legacy hardcoded
// AddCommand block in root.go: domains that are already represented as
// hand-coded cobra trees are skipped here (overrideDomains) so cobra
// doesn't see two parents with the same Use string. As each domain migrates,
// remove its name from overrideDomains and delete the hand-coded *Cmd from
// the root.go init.
//
// Schema entries without a registered handler are silently skipped — the
// `lw check schema` validator (Phase 3) is the gate that fails CI on drift.
// We intentionally do NOT panic at dispatcher build time, so an in-progress
// migration leaves the binary buildable.
func BuildDispatched(root *cobra.Command, overrideDomains map[string]bool) error {
	cfg := config.Get()
	if cfg == nil {
		return fmt.Errorf("dispatcher: config not loaded")
	}

	schema, err := sst.LoadCLIConfig(cfg.Paths.LightwaveRoot)
	if err != nil {
		return fmt.Errorf("dispatcher: load CLI schema: %w", err)
	}

	for _, domain := range schema.Domains {
		if overrideDomains[domain.Name] {
			continue
		}

		domainCmd := &cobra.Command{
			Use:   domain.Name,
			Short: domain.Description,
		}

		var attached int
		for _, cmd := range domain.Commands {
			key := sst.CommandKey(domain.Name, cmd.Name)
			handler, ok := LookupHandler(key)
			if !ok {
				continue
			}
			domainCmd.AddCommand(buildSubcommand(cmd, key, handler))
			attached++
		}

		if attached == 0 {
			continue
		}
		root.AddCommand(domainCmd)
	}

	return nil
}

// buildSubcommand turns a single CLICommand schema entry + handler into a
// cobra.Command. Flags from the schema become string flags (the most permissive
// shape — handlers parse value semantics themselves). Positional args declared
// in the schema become a MinimumNArgs requirement.
func buildSubcommand(cmd sst.CLICommand, key string, handler Handler) *cobra.Command {
	c := &cobra.Command{
		Use:   buildUseString(cmd),
		Short: cmd.Description,
	}
	if n := len(cmd.Args); n > 0 {
		c.Args = cobra.MinimumNArgs(n)
	}

	flagValues := map[string]*string{}
	flagBools := map[string]*bool{}
	for _, raw := range cmd.Flags {
		name := strings.TrimPrefix(raw, "--")
		if name == "" {
			continue
		}
		// Boolean-style flags by convention: --dry-run, --json, --pretty,
		// --verbose, --quiet, --watch, --force, --confirm, --bg, --build,
		// --follow, --fix, --all, --plan, --fake, --strict, --no-input,
		// --clear, --skip-preflight, --skip-certs, --skip-hosts, --volumes,
		// --images, --html, --xml, --staging, --create-incident, --pull,
		// --push, --from-prelim, --with-goal-tests, --skip-tests, --deploy,
		// --skip-migrate, --adversarial. Anything else takes a string value.
		if isBooleanFlag(name) {
			b := false
			flagBools[name] = &b
			c.Flags().BoolVar(&b, name, false, "")
		} else {
			s := ""
			flagValues[name] = &s
			c.Flags().StringVar(&s, name, "", "")
		}
	}

	c.RunE = func(cobraCmd *cobra.Command, args []string) error {
		flags := make(map[string]any, len(flagValues)+len(flagBools))
		for name, p := range flagValues {
			if cobraCmd.Flags().Changed(name) {
				flags[name] = *p
			}
		}
		for name, p := range flagBools {
			if cobraCmd.Flags().Changed(name) {
				flags[name] = *p
			}
		}
		ctx := cobraCmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		_ = key // reserved for future structured logging
		return handler(ctx, args, flags)
	}

	return c
}

// buildUseString emits "name <arg1> <arg2>" for cobra's Use field.
func buildUseString(cmd sst.CLICommand) string {
	if len(cmd.Args) == 0 {
		return cmd.Name
	}
	parts := make([]string, 0, 1+len(cmd.Args))
	parts = append(parts, cmd.Name)
	for _, a := range cmd.Args {
		parts = append(parts, "<"+a+">")
	}
	return strings.Join(parts, " ")
}

// booleanFlags lists flag names that should be parsed as bools rather than
// strings. The schema doesn't currently encode flag types, so this table is
// the single source of truth for shape disambiguation.
var booleanFlags = map[string]bool{
	"dry-run":         true,
	"json":            true,
	"pretty":          true,
	"verbose":         true,
	"quiet":           true,
	"watch":           true,
	"force":           true,
	"confirm":         true,
	"bg":              true,
	"build":           true,
	"follow":          true,
	"fix":             true,
	"all":             true,
	"plan":            true,
	"fake":            true,
	"strict":          true,
	"no-input":        true,
	"clear":           true,
	"skip-preflight":  true,
	"skip-certs":      true,
	"skip-hosts":      true,
	"skip-tests":      true,
	"skip-migrate":    true,
	"volumes":         true,
	"images":          true,
	"html":            true,
	"xml":             true,
	"staging":         true,
	"create-incident": true,
	"pull":            true,
	"push":            true,
	"from-prelim":     true,
	"with-goal-tests": true,
	"deploy":          true,
	"adversarial":     true,
	"yes":             true,
	"empty":           true,
}

func isBooleanFlag(name string) bool {
	return booleanFlags[name]
}
