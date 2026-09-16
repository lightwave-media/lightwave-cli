// Package sst provides nested command flattening for domain fragments and voice subcommands.
package sst

import "fmt"

// flattenCommandKeys walks nested command groups and yields leaf handler keys.
// flattenCommandKeys walks a command tree into dotted leaf keys.
//
// skipInDev drops commands marked `_status: in_development`, and a marked
// group drops its whole subtree — a verb under an unbuilt group is not
// reachable either, so counting it as published would report a missing handler
// for something nothing can call.
func flattenCommandKeys(domain string, prefix string, cmds []CLICommand, skipInDev bool) []string {
	out := make([]string, 0, len(cmds))
	for i := range cmds {
		cmd := &cmds[i]
		if skipInDev && cmd.InDevelopment() {
			continue
		}

		name := cmd.Name
		if prefix != "" {
			name = prefix + "." + cmd.Name
		}

		if len(cmd.Commands) > 0 {
			out = append(out, flattenCommandKeys(domain, name, cmd.Commands, skipInDev)...)
			continue
		}

		out = append(out, CommandKey(domain, name))
	}

	return out
}

func flattenCommandIndex(domain string, prefix string, cmds []CLICommand, out map[string]CLICommand) {
	for _, cmd := range cmds {
		name := cmd.Name
		if prefix != "" {
			name = prefix + "." + cmd.Name
		}

		if len(cmd.Commands) > 0 {
			flattenCommandIndex(domain, name, cmd.Commands, out)
			continue
		}

		out[CommandKey(domain, name)] = cmd
	}
}

func validateCommandTree(domain, prefix string, cmds []CLICommand) error {
	seen := map[string]bool{}

	for _, cmd := range cmds {
		if cmd.Name == "" {
			return fmt.Errorf("domain %q has command with empty name", domain)
		}

		if seen[cmd.Name] {
			return fmt.Errorf("domain %q has duplicate command %q", domain, cmd.Name)
		}

		seen[cmd.Name] = true

		full := cmd.Name
		if prefix != "" {
			full = prefix + "." + cmd.Name
		}

		if len(cmd.Commands) > 0 {
			if err := validateCommandTree(domain, full, cmd.Commands); err != nil {
				return err
			}

			continue
		}

		if cmd.Description == "" {
			return fmt.Errorf("%s.%s missing description", domain, full)
		}
	}

	return nil
}
