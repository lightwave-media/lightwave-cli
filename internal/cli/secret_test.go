//nolint:testpackage // swaps the unexported map, AWS and host-effect seams
package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/secrets"
)

const secretFixtureKey = "NULLTICKETS_API_TOKEN"

const secretMapFixture = `records:
  - name: NULLTICKETS_API_TOKEN
    ssm_path: /lightwave/prod/NULLTICKETS_API_TOKEN
    status: active
    rotation_mode: generate
    writer_identity: lightwave-secret-rotator
    store: ssm
    generate: {length: 48, charset: hex}
  - name: NOTION_API_KEY
    ssm_path: /lightwave/prod/NOTION_API_KEY
    status: active
    rotation_mode: human_inbox
    writer_identity: lightwave-secret-rotator
    store: ssm
`

const trackerFixture = `id: nulltickets-tracker
daemon_id: nulltickets
secret_loadings:
  - {ssm_path: /lightwave/prod/NULLTICKETS_API_TOKEN, target_env_var: NULLTICKETS_API_TOKEN}
refresh: {action: kickstart, target: com.nullhub.server}
verify: {probe: 'exit 0'}
`

type secretFakeMeta struct{}

func (secretFakeMeta) Meta(context.Context, string) (secrets.ParamMeta, error) {
	return secrets.ParamMeta{Type: "SecureString", KeyID: "alias/aws/ssm", Tier: "Standard", Version: 1}, nil
}

type secretFakeWriter struct{ written []string }

func (*secretFakeWriter) Caller() secrets.Caller {
	return secrets.Caller{Role: "lightwave-secret-rotator", Session: "op_joel"}
}

func (w *secretFakeWriter) Put(_ context.Context, _ string, value []byte) (int64, error) {
	w.written = append(w.written, string(value))

	return 2, nil
}

func (w *secretFakeWriter) Create(ctx context.Context, path string, value []byte, _ map[string]string) (int64, error) {
	return w.Put(ctx, path, value)
}

type secretRig struct {
	writer *secretFakeWriter
	ledger string
	kicked []string
	awsHit bool
}

// withSecretSeams points the map at a fixture, fakes AWS and the host, and
// restores everything afterwards. Serial: it swaps package globals.
func withSecretSeams(t *testing.T, profile string) *secretRig {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "daemon_secrets"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret_rotation.yaml"), []byte(secretMapFixture), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "daemon_secrets", "tracker.yaml"), []byte(trackerFixture), 0o600))

	r := &secretRig{writer: &secretFakeWriter{}, ledger: filepath.Join(dir, "ledger.jsonl")}

	prevMap, prevLedger, prevMeta, prevWriter := secretMapDir, secretLedgerPath, newSecretMeta, newOperatorWriter
	prevKick, prevProber, prevProfile, prevDry := secretKickstart, secretProber, secretRotateProfile, secretRotateDryRun

	t.Cleanup(func() {
		secretMapDir, secretLedgerPath, newSecretMeta, newOperatorWriter = prevMap, prevLedger, prevMeta, prevWriter
		secretKickstart, secretProber, secretRotateProfile, secretRotateDryRun = prevKick, prevProber, prevProfile, prevDry
	})

	secretMapDir = func() (string, error) { return dir, nil }
	secretLedgerPath = func() (string, error) { return r.ledger, nil }
	newSecretMeta = func(context.Context) (secrets.MetaReader, error) {
		r.awsHit = true

		return secretFakeMeta{}, nil
	}
	newOperatorWriter = func(context.Context, string) (secrets.ValueWriter, error) {
		r.awsHit = true

		return r.writer, nil
	}
	secretKickstart = func(_ context.Context, label string) error {
		r.kicked = append(r.kicked, label)

		return nil
	}
	secretProber = func(string) func(context.Context, []string, string) (string, error) {
		return func(context.Context, []string, string) (string, error) { return "200", nil }
	}
	secretRotateProfile, secretRotateDryRun = profile, false

	return r
}

func secretCommand() (*cobra.Command, *bytes.Buffer) {
	out := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(out)
	cmd.SetErr(out)

	return cmd, out
}

//nolint:paralleltest // swaps package-level seams
func TestSecretListPrintsNamesPolicyAndConsumerCounts(t *testing.T) {
	withSecretSeams(t, "")
	cmd, out := secretCommand()

	require.NoError(t, runSecretList(cmd, nil))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"NAME", "STATUS", "MODE", "CONSUMERS"}, strings.Fields(lines[0]))
	assert.Equal(t, []string{"NOTION_API_KEY", "active", "human_inbox", "0"}, strings.Fields(lines[1]))
	assert.Equal(t, []string{secretFixtureKey, "active", "generate", "1"}, strings.Fields(lines[2]))
}

//nolint:paralleltest // swaps package-level seams
func TestSecretRotateRefusesAnInboxKeyBeforeTouchingAWS(t *testing.T) {
	r := withSecretSeams(t, "lightwave-secret-rotator")
	cmd, _ := secretCommand()

	err := runSecretRotate(cmd, []string{"NOTION_API_KEY"})
	require.ErrorIs(t, err, secrets.ErrRefused)

	code, ok := ExitCode(err)
	require.True(t, ok)
	assert.Equal(t, secrets.ExitRefused, code)
	assert.False(t, r.awsHit, "a refusal the map decides needs no AWS call")
}

//nolint:paralleltest // swaps package-level seams and LW_PERSONA
func TestSecretRotateRefusesAnAgentWithNoPersona(t *testing.T) {
	withSecretSeams(t, "")
	t.Setenv("LW_PERSONA", "")
	cmd, _ := secretCommand()

	err := runSecretRotate(cmd, []string{secretFixtureKey})
	require.ErrorIs(t, err, secrets.ErrRefused)

	code, _ := ExitCode(err)
	assert.Equal(t, secrets.ExitRefused, code)
}

// withInboxStdin feeds value to inbox as piped (non-terminal) stdin.
func withInboxStdin(t *testing.T, value string) {
	t.Helper()

	prevIn, prevTerm := secretInboxIn, secretIsTerminal

	t.Cleanup(func() { secretInboxIn, secretIsTerminal = prevIn, prevTerm })

	secretInboxIn = strings.NewReader(value)
	secretIsTerminal = func(int) bool { return false }
}

//nolint:paralleltest // swaps package-level seams
func TestSecretInboxReadsPipedStdinAndPrintsNamesAndVersionsOnly(t *testing.T) {
	r := withSecretSeams(t, "lightwave-secret-rotator")
	const value = "ntn_pipedFixtureValue0123456789"
	withInboxStdin(t, value+"\n")
	cmd, out := secretCommand()

	require.NoError(t, runSecretInbox(cmd, []string{"NOTION_API_KEY"}))

	assert.Equal(t, "NOTION_API_KEY: written v1 -> v2 as op_joel\n", out.String())
	assert.Equal(t, []string{value}, r.writer.written, "one trailing newline is dropped")

	ledger, err := os.ReadFile(r.ledger)
	require.NoError(t, err)
	assert.NotContains(t, string(ledger), value)
	assert.NotContains(t, out.String(), value)
}

//nolint:paralleltest // swaps package-level seams
func TestSecretInboxRefusesAGenerateKeyBeforeReadingStdin(t *testing.T) {
	r := withSecretSeams(t, "lightwave-secret-rotator")
	withInboxStdin(t, "should-not-be-read")
	cmd, _ := secretCommand()

	err := runSecretInbox(cmd, []string{secretFixtureKey})
	require.ErrorIs(t, err, secrets.ErrRefused)

	code, _ := ExitCode(err)
	assert.Equal(t, secrets.ExitRefused, code)
	assert.False(t, r.awsHit, "a refusal the map decides needs no AWS call")
	assert.Empty(t, r.writer.written)
}

//nolint:paralleltest // swaps package-level seams
func TestSecretRotatePrintsAndRecordsNamesAndVersionsOnly(t *testing.T) {
	r := withSecretSeams(t, "lightwave-secret-rotator")
	cmd, out := secretCommand()

	require.NoError(t, runSecretRotate(cmd, []string{secretFixtureKey}))

	assert.Equal(t,
		secretFixtureKey+": rotated v1 -> v2 as op_joel; refreshed: com.nullhub.server; probes ok: nulltickets-tracker\n",
		out.String())
	assert.Equal(t, []string{"com.nullhub.server"}, r.kicked)

	ledger, err := os.ReadFile(r.ledger)
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(ledger), "\n"))

	require.Len(t, r.writer.written, 1)
	assert.NotContains(t, out.String(), r.writer.written[0])
	assert.NotContains(t, string(ledger), r.writer.written[0])
}
