package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Refresh the owned transport fields, preserving operator tool policy, timeouts
// and other server settings. Never replace the whole server with the fragment.
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

	currentServers, _ := valid["mcp_servers"].(map[string]any)
	existing, _ := currentServers["lightwave"].(map[string]any)
	desired, _ := lightwave.(map[string]any)

	merged := make(map[string]any, len(existing)+len(desired))
	for key, value := range existing {
		merged[key] = value
	}

	for _, key := range []string{"command", "args", "env", "cwd", "env_vars", "url", "http_headers", "env_http_headers", "bearer_token_env_var"} {
		delete(merged, key)
	}

	for key, value := range desired {
		// Policy is operator-owned, even if a future fragment contains defaults.
		if key == "tools" || key == "enabled_tools" || key == "disabled_tools" {
			if _, present := existing[key]; present {
				continue
			}
		}

		merged[key] = value
	}

	body, err := toml.Marshal(map[string]any{"mcp_servers": map[string]any{"lightwave": merged}})
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

// Apply the one desktop setting Lightwave owns without replacing other desktop
// preferences. Marketplace installation is delegated to Codex's native CLI.
func ensureCodexWorktreeRoot(current string, fragment []byte) (string, error) {
	var parsed map[string]any
	if err := toml.Unmarshal(fragment, &parsed); err != nil {
		return "", err
	}

	desktop, _ := parsed["desktop"].(map[string]any)

	root, exists := desktop["git-worktree-root"]
	if !exists {
		return current, nil
	}

	path, ok := root.(string)
	if !ok || !filepath.IsAbs(path) {
		return "", errors.New("desktop.git-worktree-root must be an absolute path")
	}
	// Reuse the line-preserving section editor used for shell environment.
	return ensureCodexSection(current, "desktop", map[string]string{"git-worktree-root": path}), nil
}
