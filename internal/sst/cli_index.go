package sst

import "fmt"

// CommandKey returns the canonical handler-registry key for a command.
// Format: "<domain>.<command>", e.g. "task.list".
func CommandKey(domain, command string) string {
	return fmt.Sprintf("%s.%s", domain, command)
}

// Index flattens the CLI config to a map keyed by "<domain>.<command>"
// for O(1) handler lookup at dispatch time.
func (c *CLIConfig) Index() map[string]CLICommand {
	out := make(map[string]CLICommand, len(c.Domains)*8)
	for _, d := range c.Domains {
		for _, cmd := range d.Commands {
			out[CommandKey(d.Name, cmd.Name)] = cmd
		}
	}
	return out
}

// Keys returns the deterministic ordered list of "<domain>.<command>" keys.
func (c *CLIConfig) Keys() []string {
	out := make([]string, 0, len(c.Domains)*8)
	for _, d := range c.Domains {
		for _, cmd := range d.Commands {
			out = append(out, CommandKey(d.Name, cmd.Name))
		}
	}
	return out
}

// FindDomain returns the domain by name, or nil if absent.
func (c *CLIConfig) FindDomain(name string) *CLIDomain {
	for i := range c.Domains {
		if c.Domains[i].Name == name {
			return &c.Domains[i]
		}
	}
	return nil
}
