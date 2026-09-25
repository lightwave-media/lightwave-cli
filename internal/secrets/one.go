package secrets

import "context"

// FetchOne reads a single key by name, for a command that hands one secret to
// one child or client and nothing else. Sessions no longer carry store keys
// (owner memo 2026-09-24, CLAUDE.md §24), so a command that used to find its
// key in the environment asks for it here. Errors carry the name only.
func FetchOne(ctx context.Context, key string) (string, error) {
	client, err := NewNamedClient(ctx)
	if err != nil {
		return "", err
	}

	pairs, err := FetchNamed(ctx, client, []string{key})
	if err != nil {
		return "", err
	}

	return pairs[0].Value, nil
}
