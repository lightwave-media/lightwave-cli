package secrets

// Internal tests of the AWS adapter's unexported pieces; testpackage skips
// *internal_test.go.

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientsIgnoreAConfiguredEndpoint(t *testing.T) {
	t.Parallel()

	cfg := aws.Config{Region: Region, BaseEndpoint: aws.String("https://collector.example")}

	assert.Nil(t, newSSM(&cfg).Options().BaseEndpoint)
	assert.Nil(t, newSTS(&cfg).Options().BaseEndpoint)
}

func TestCallerFromARNAcceptsOnlyAnAssumedRoleSession(t *testing.T) {
	t.Parallel()

	c, err := callerFromARN("arn:aws:sts::738605694078:assumed-role/lightwave-secret-rotator/op_joel")
	require.NoError(t, err)
	assert.Equal(t, Caller{Role: "lightwave-secret-rotator", Session: "op_joel"}, c)
	assert.True(t, c.Operator())

	for _, arn := range []string{
		"arn:aws:iam::738605694078:user/lightwave-admin",
		"arn:aws:sts::738605694078:assumed-role/lightwave-secret-rotator/",
		"",
	} {
		_, err := callerFromARN(arn)
		require.ErrorIs(t, err, ErrRefused, arn)
	}
}

type apiError struct{ code, message string }

func (e apiError) Error() string     { return e.code + ": " + e.message }
func (e apiError) ErrorCode() string { return e.code }

func TestAWSErrorKeepsTheCodeAndDropsTheMessage(t *testing.T) {
	t.Parallel()

	err := awsError(apiError{code: "ValidationException", message: "value 'abc123' failed"})
	require.EqualError(t, err, "ValidationException")
}

type pagingDescriber struct {
	pages []*ssm.DescribeParametersOutput
}

func (p *pagingDescriber) DescribeParameters(
	context.Context, *ssm.DescribeParametersInput, ...func(*ssm.Options),
) (*ssm.DescribeParametersOutput, error) {
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

	meta, err := ssmMeta{api: api}.Meta(context.Background(), Path+"KEY")
	require.NoError(t, err)
	assert.Equal(t, ParamMeta{Type: "SecureString", KeyID: "alias/aws/ssm", Tier: "Standard", Version: 3}, meta)

	_, err = ssmMeta{api: &pagingDescriber{pages: []*ssm.DescribeParametersOutput{{}}}}.Meta(context.Background(), Path+"KEY")
	require.ErrorContains(t, err, "does not exist")
}

type recordingPutter struct{ in *ssm.PutParameterInput }

func (r *recordingPutter) PutParameter(
	_ context.Context, in *ssm.PutParameterInput, _ ...func(*ssm.Options),
) (*ssm.PutParameterOutput, error) {
	r.in = in

	return &ssm.PutParameterOutput{Version: 9}, nil
}

func TestPutWritesAStandardSecureStringUnderTheDefaultKey(t *testing.T) {
	t.Parallel()

	api := &recordingPutter{}
	version, err := rotatorWriter{api: api}.Put(context.Background(), Path+"KEY", []byte("v"))
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

	env := probeEnv(func(k string) string { return parent[k] })
	assert.ElementsMatch(t, []string{"AWS_PROFILE=lightwave-agent", "PATH=/usr/bin", "HOME=/Users/x"}, env)
}
