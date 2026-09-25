package secrets_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
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

func TestClientsIgnoreAConfiguredEndpoint(t *testing.T) {
	t.Parallel()

	cfg := aws.Config{Region: secrets.Region, BaseEndpoint: aws.String("https://collector.example")}

	assert.Nil(t, secrets.NewSSM(&cfg).Options().BaseEndpoint)
	assert.Nil(t, secrets.NewSTS(&cfg).Options().BaseEndpoint)
}

func TestCallerFromARNAcceptsOnlyAnAssumedRoleSession(t *testing.T) {
	t.Parallel()

	c, err := secrets.CallerFromARN("arn:aws:sts::738605694078:assumed-role/lightwave-secret-rotator/op_joel")
	require.NoError(t, err)
	assert.Equal(t, secrets.Caller{Role: "lightwave-secret-rotator", Session: "op_joel"}, c)
	assert.True(t, c.Operator())

	for _, arn := range []string{
		"arn:aws:iam::738605694078:user/lightwave-admin",
		"arn:aws:sts::738605694078:assumed-role/lightwave-secret-rotator/",
		"",
	} {
		_, err := secrets.CallerFromARN(arn)
		require.ErrorIs(t, err, secrets.ErrRefused, arn)
	}
}

type apiError struct{ code, message string }

func (e apiError) Error() string     { return e.code + ": " + e.message }
func (e apiError) ErrorCode() string { return e.code }

func TestAWSErrorKeepsTheCodeAndDropsTheMessage(t *testing.T) {
	t.Parallel()

	err := secrets.AWSError(apiError{code: "ValidationException", message: "value 'abc123' failed"})
	require.EqualError(t, err, "ValidationException")
}

type pagingDescriber struct {
	pages []*ssm.DescribeParametersOutput
}

func (p *pagingDescriber) DescribeParameters(context.Context, *ssm.DescribeParametersInput, ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error) {
	page := p.pages[0]
	p.pages = p.pages[1:]

	return page, nil
}

func TestMetaReadsPastAnEmptyFilteredPage(t *testing.T) {
	t.Parallel()

	api := &pagingDescriber{pages: []*ssm.DescribeParametersOutput{
		{NextToken: aws.String("next")},
		{Parameters: []types.ParameterMetadata{{Type: types.ParameterTypeSecureString, KeyId: aws.String("alias/aws/ssm"), Tier: types.ParameterTierStandard, Version: 3}}},
	}}

	meta, err := secrets.MetaReaderFor(api).Meta(context.Background(), secrets.Path+"KEY")
	require.NoError(t, err)
	assert.Equal(t, secrets.ParamMeta{Type: "SecureString", KeyID: "alias/aws/ssm", Tier: "Standard", Version: 3}, meta)

	_, err = secrets.MetaReaderFor(&pagingDescriber{pages: []*ssm.DescribeParametersOutput{{}}}).Meta(context.Background(), secrets.Path+"KEY")
	require.ErrorContains(t, err, "does not exist")
}

type recordingPutter struct{ in *ssm.PutParameterInput }

func (r *recordingPutter) PutParameter(_ context.Context, in *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	r.in = in

	return &ssm.PutParameterOutput{Version: 9}, nil
}

func TestPutWritesAStandardSecureStringUnderTheDefaultKey(t *testing.T) {
	t.Parallel()

	api := &recordingPutter{}
	version, err := secrets.WriterFor(api, secrets.Caller{}).Put(context.Background(), secrets.Path+"KEY", []byte("v"))
	require.NoError(t, err)

	assert.Equal(t, int64(9), version)
	assert.Equal(t, types.ParameterTypeSecureString, api.in.Type)
	assert.Equal(t, types.ParameterTierStandard, api.in.Tier)
	assert.Nil(t, api.in.KeyId, "the default aws/ssm key")
	assert.True(t, aws.ToBool(api.in.Overwrite))
}

func TestProbeEnvCarriesNothingButPathHomeAndTheAgentProfile(t *testing.T) {
	t.Parallel()

	parent := map[string]string{
		"PATH": "/usr/bin", "HOME": "/Users/x", "OPENROUTER_API_KEY": "sk-or-inherited", "AWS_PROFILE": "lightwave-secret-rotator",
	}

	env := secrets.ProbeEnv(func(k string) string { return parent[k] })
	assert.ElementsMatch(t, []string{"AWS_PROFILE=lightwave-agent", "PATH=/usr/bin", "HOME=/Users/x"}, env)
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
