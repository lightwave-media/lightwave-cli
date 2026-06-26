// Package sst provides nested command flattening for domain fragments and voice subcommands.
package sst

import "fmt"

// flattenCommandKeys walks nested command groups and yields leaf handler keys.
func flattenCommandKeys(domain string, prefix string, cmds []CLICommand) []string {
	out := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		name := cmd.Name
		if prefix != "" {
			name = prefix + "." + cmd.Name
		}

		if len(cmd.Commands) > 0 {
			out = append(out, flattenCommandKeys(domain, name, cmd.Commands)...)
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
