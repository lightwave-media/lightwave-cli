package sst

import "fmt"

// CommandKey returns the canonical handler-registry key for a command.
// Format: "<domain>.<command>", e.g. "task.list" or "voice.profile.list".
func CommandKey(domain, command string) string {
	return fmt.Sprintf("%s.%s", domain, command)
}

// Index flattens the CLI config to a map keyed by "<domain>.<command>"
// for O(1) handler lookup at dispatch time. Nested command groups flatten
// to dotted keys (voice.profile.list).
func (c *CLIConfig) Index() map[string]CLICommand {
	out := make(map[string]CLICommand, len(c.Domains)*8)
	for _, d := range c.Domains {
		flattenCommandIndex(d.Name, "", d.Commands, out)
	}

	return out
}

// Keys returns the deterministic ordered list of leaf "<domain>.<command>" keys.
func (c *CLIConfig) Keys() []string {
	out := make([]string, 0, len(c.Domains)*8)
	for _, d := range c.Domains {
		out = append(out, flattenCommandKeys(d.Name, "", d.Commands)...)
	}

	return out
}

// KeysPublished returns leaf keys for domains that are NOT in_development.
func (c *CLIConfig) KeysPublished() []string {
	out := make([]string, 0, len(c.Domains))

	for _, d := range c.Domains {
		if d.Status == StatusInDevelopment {
			continue
		}

		out = append(out, flattenCommandKeys(d.Name, "", d.Commands)...)
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
