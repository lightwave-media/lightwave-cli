package secrets_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/secrets"
)

// fakePager serves two pages so the walk has to follow NextToken.
type fakePager struct {
	calls []*ssm.GetParametersByPathInput
}

func (f *fakePager) GetParametersByPath(
	_ context.Context,
	in *ssm.GetParametersByPathInput,
	_ ...func(*ssm.Options),
) (*ssm.GetParametersByPathOutput, error) {
	f.calls = append(f.calls, in)
	if in.NextToken == nil {
		return &ssm.GetParametersByPathOutput{
			Parameters: []types.Parameter{
				{Name: aws.String(secrets.Path + "OPENROUTER_API_KEY"), Value: aws.String("flat")},
			},
			NextToken: aws.String("page-2"),
		}, nil
	}

	return &ssm.GetParametersByPathOutput{
		Parameters: []types.Parameter{
			{Name: aws.String(secrets.Path + "openrouter/api_key"), Value: aws.String("nested")},
			{Name: aws.String(secrets.Path + "JWT_PRIVATE_KEY"), Value: aws.String("it's multi\nline")},
		},
	}, nil
}

func TestFetchFollowsEveryPageWithDecryption(t *testing.T) {
	t.Parallel()

	pager := &fakePager{}
	params, err := secrets.Fetch(context.Background(), pager)
	require.NoError(t, err)
	assert.Len(t, params, 3)
	require.Len(t, pager.calls, 2)

	for _, call := range pager.calls {
		assert.Equal(t, secrets.Path, aws.ToString(call.Path))
		assert.True(t, aws.ToBool(call.Recursive))
		assert.True(t, aws.ToBool(call.WithDecryption))
	}

	assert.Equal(t, "page-2", aws.ToString(pager.calls[1].NextToken))
}

func TestResolveFlatNameWinsCollisionAndReportsIt(t *testing.T) {
	t.Parallel()

	pairs, notes := secrets.Resolve([]secrets.Param{
		{Name: secrets.Path + "openrouter/api_key", Value: "nested"},
		{Name: secrets.Path + "OPENROUTER_API_KEY", Value: "flat"},
		{Name: secrets.Path + "ZONE", Value: "z"},
	})

	assert.Equal(t, []secrets.Pair{
		{Key: "OPENROUTER_API_KEY", Value: "flat"},
		{Key: "ZONE", Value: "z"},
	}, pairs)
	require.Len(t, notes, 1)
	assert.Contains(t, notes[0], "openrouter/api_key")
	assert.Contains(t, notes[0], "collides with OPENROUTER_API_KEY")
	// The note names the winner's parameter, never a value.
	assert.NotContains(t, notes[0], "flat")
}

func TestResolveSkipsNamesThatCannotBeVariables(t *testing.T) {
	t.Parallel()

	pairs, notes := secrets.Resolve([]secrets.Param{
		{Name: secrets.Path + "9starts-with-digit", Value: "x"},
		{Name: secrets.Path + "has-dash", Value: "x"},
		{Name: secrets.Path + "nested/api_key", Value: "ok"},
	})

	assert.Equal(t, []secrets.Pair{{Key: "NESTED_API_KEY", Value: "ok"}}, pairs)
	assert.Len(t, notes, 2)
}

func TestEnvName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "TMDB_API_KEY", secrets.EnvName(secrets.Path+"TMDB_API_KEY"))
	assert.Equal(t, "OPENROUTER_API_KEY", secrets.EnvName(secrets.Path+"openrouter/api_key"))
	assert.Empty(t, secrets.EnvName(secrets.Path+"bad.name"))
	assert.Empty(t, secrets.EnvName(secrets.Path))
}

func TestRenderShellQuotesForPOSIX(t *testing.T) {
	t.Parallel()

	out := secrets.RenderShell([]secrets.Pair{
		{Key: "A", Value: "plain"},
		{Key: "B", Value: "it's $HOME `x`"},
	})

	assert.Equal(t, "export A='plain'\nexport B='it'\\''s $HOME `x`'\n", out)
}

func TestRenderJSONIsOneFlatObject(t *testing.T) {
	t.Parallel()

	out, err := secrets.RenderJSON([]secrets.Pair{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}})
	require.NoError(t, err)

	var got map[string]string
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, map[string]string{"A": "1", "B": "2"}, got)
}
