package secrets_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/secrets"
)

func writeMap(t *testing.T, records string, daemons map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "daemon_secrets"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret_rotation.yaml"), []byte(records), 0o600))

	for name, body := range daemons {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "daemon_secrets", name+".yaml"), []byte(body), 0o600))
	}

	return dir
}

const tokenYAML = `records:
  - name: NULLTICKETS_API_TOKEN
    ssm_path: /lightwave/prod/NULLTICKETS_API_TOKEN
    status: active
    rotation_mode: generate
    writer_identity: lightwave-secret-rotator
    owner_persona: v_platform
    store: ssm
    generate: {length: 48, charset: hex}
    aliases: []
    rotators: []
`

func TestLoadMapJoinsConsumersOnThePath(t *testing.T) {
	t.Parallel()

	dir := writeMap(t, tokenYAML, map[string]string{
		"tracker": "id: tracker\ndaemon_id: nulltickets\nsecret_loadings:\n  - {ssm_path: /lightwave/prod/NULLTICKETS_API_TOKEN, target_env_var: NULLTICKETS_API_TOKEN}\nrefresh: {action: kickstart, target: com.nullhub.server}\nverify: {probe: 'exit 0'}\n",
		"other":   "id: other\ndaemon_id: other\nsecret_loadings:\n  - {ssm_path: /lightwave/prod/OTHER, soft_fail: true}\n",
	})

	m, err := secrets.LoadMap(dir)
	require.NoError(t, err)

	rec, err := m.Lookup(secrets.Path + "NULLTICKETS_API_TOKEN")
	require.NoError(t, err)

	consumers := m.Consumers(rec)
	require.Len(t, consumers, 1)
	assert.Equal(t, "tracker", consumers[0].ID)
}

func TestLoadMapFailsClosedOnAMisspeltPolicyField(t *testing.T) {
	t.Parallel()

	dir := writeMap(t, strings.Replace(tokenYAML, "rotators: []", "peers-literal: [config.json]", 1), nil)

	_, err := secrets.LoadMap(dir)
	require.ErrorContains(t, err, "peers-literal")
}

func TestLoadMapNeverEchoesAScalarFromTheFile(t *testing.T) {
	t.Parallel()

	pasted := "sk-or-v1-pasted-by-mistake"
	dir := writeMap(t, strings.Replace(tokenYAML, "length: 48", "length: "+pasted, 1), nil)

	_, err := secrets.LoadMap(dir)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "pasted")
}

func TestLoadMapRefusesAPathOutsideTheProdTree(t *testing.T) {
	t.Parallel()

	dir := writeMap(t, strings.Replace(tokenYAML, "ssm_path: /lightwave/prod/", "ssm_path: /lightwave/test/", 1), nil)

	_, err := secrets.LoadMap(dir)
	require.ErrorContains(t, err, "NULLTICKETS_API_TOKEN")
}

func TestAppendLedgerWritesOnePrivateJSONLinePerRow(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	appendRow := secrets.AppendLedger(path)

	require.NoError(t, appendRow(secrets.LedgerRow{Event: "rotated", Params: []string{"KEY"}}))
	require.NoError(t, appendRow(secrets.LedgerRow{Event: "rotation-verified", Params: []string{"KEY"}}))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	require.Len(t, lines, 2)

	var row secrets.LedgerRow
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &row))
	assert.Equal(t, "rotation-verified", row.Event)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
