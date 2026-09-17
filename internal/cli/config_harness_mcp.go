package cli

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Only replace the owned Lightwave MCP table. Preserve the rest of the file,
// including model preferences, comments, other servers and hook trust.
func ensureCodexMCP(current string, fragment []byte) (string, error) {
	var parsed map[string]any
	if err := toml.Unmarshal(fragment, &parsed); err != nil {
		return "", err
	}

	servers, _ := parsed["mcp_servers"].(map[string]any)

	lightwave, exists := servers["lightwave"]
	if !exists {
		return current, nil
	}

	var valid map[string]any
	if err := toml.Unmarshal([]byte(current), &valid); err != nil {
		return "", fmt.Errorf("invalid existing Codex config: %w", err)
	}

	body, err := toml.Marshal(map[string]any{"mcp_servers": map[string]any{"lightwave": lightwave}})
	if err != nil {
		return "", err
	}

	lines := []string{}
	drop := false

	for _, line := range strings.Split(strings.TrimRight(current, "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			header := strings.ReplaceAll(strings.ReplaceAll(trimmed, "\"", ""), "'", "")
			drop = header == "[mcp_servers.lightwave]" || strings.HasPrefix(header, "[mcp_servers.lightwave.")
		}

		if !drop {
			lines = append(lines, line)
		}
	}

	owned := strings.TrimPrefix(string(body), "[mcp_servers]\n")

	next := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n\n" + strings.TrimLeft(owned, "\n")
	if err := toml.Unmarshal([]byte(next), &valid); err != nil {
		return "", fmt.Errorf("invalid merged Codex config: %w", err)
	}

	return next, nil
}
