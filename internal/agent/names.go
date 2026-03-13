package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// namePools maps each role to its pool of thematic name words.
var namePools = map[Role][]string{
	RoleBackend: {
		"atlas", "forge", "vault", "nexus", "titan",
		"anvil", "bolt", "core", "delta", "echo",
	},
	RoleFrontend: {
		"wave", "bloom", "crest", "pixel", "prism",
		"spark", "vista", "aura", "glow", "flux",
	},
	RoleInfra: {
		"terra", "cloud", "stack", "orbit", "grid",
		"mesh", "node", "shard", "relay", "bridge",
	},
	RoleVCore: {
		"mind", "nerve", "cortex", "synth", "logic",
		"pulse", "arc", "cipher", "matrix", "omega",
	},
}

// GenerateName returns a name in the format "<role>-<word>-<4char-hex>".
// Uses crypto/rand for the random word selection and hex token.
func GenerateName(role Role) (string, error) {
	pool, ok := namePools[role]
	if !ok {
		return "", fmt.Errorf("unknown role: %s", role)
	}

	// Pick a random word from the pool.
	idxBuf := make([]byte, 1)
	if _, err := rand.Read(idxBuf); err != nil {
		return "", fmt.Errorf("generate random index: %w", err)
	}
	word := pool[int(idxBuf[0])%len(pool)]

	// Generate 4-char hex token (2 random bytes = 4 hex chars).
	token, err := randomHex(2)
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	return fmt.Sprintf("%s-%s-%s", role, word, token), nil
}

// TmuxSessionName returns the tmux session name for an agent.
func TmuxSessionName(agentName string) string {
	return "lw-" + agentName
}

// BranchName returns the git branch name for an agent.
func BranchName(agentName string) string {
	return "lw/" + agentName
}

// randomHex returns n random bytes encoded as hex.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
