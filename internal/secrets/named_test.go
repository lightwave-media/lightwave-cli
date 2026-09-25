package secrets_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/secrets"
)

// fakeGetter serves values from a map, as SSM does: names it does not hold
// come back as InvalidParameters, not as an error.
type fakeGetter struct {
	store map[string]string
	calls []*ssm.GetParametersInput
}

func (f *fakeGetter) GetParameters(
	_ context.Context,
	in *ssm.GetParametersInput,
	_ ...func(*ssm.Options),
) (*ssm.GetParametersOutput, error) {
	f.calls = append(f.calls, in)
	out := &ssm.GetParametersOutput{}
	for _, name := range in.Names {
		value, ok := f.store[strings.TrimPrefix(name, secrets.Path)]
		if !ok {
			out.InvalidParameters = append(out.InvalidParameters, name)

			continue
		}

		out.Parameters = append(out.Parameters, types.Parameter{Name: aws.String(name), Value: aws.String(value)})
	}

	return out, nil
}

func TestFetchNamedReadsByNameInBatchesOfTen(t *testing.T) {
	t.Parallel()

	store := map[string]string{}
	keys := make([]string, 0, 12)
	for i := range 12 {
		key := fmt.Sprintf("KEY_%02d", i)
		store[key] = "v" + key
		keys = append(keys, key)
	}

	getter := &fakeGetter{store: store}
	pairs, err := secrets.FetchNamed(context.Background(), getter, keys)
	require.NoError(t, err)

	require.Len(t, getter.calls, 2)
	assert.Len(t, getter.calls[0].Names, 10)
	assert.Len(t, getter.calls[1].Names, 2)
	assert.Equal(t, secrets.Path+"KEY_00", getter.calls[0].Names[0])
	for _, call := range getter.calls {
		assert.True(t, aws.ToBool(call.WithDecryption))
	}

	require.Len(t, pairs, 12)
	assert.Equal(t, secrets.Pair{Key: "KEY_00", Value: "vKEY_00"}, pairs[0])
	assert.Equal(t, secrets.Pair{Key: "KEY_11", Value: "vKEY_11"}, pairs[11])
}

// A wrapper must never start with half its keys: one missing name fails the
// whole fetch, and the error names the key without echoing any value.
func TestFetchNamedFailsClosedNamingOnlyTheMissingKey(t *testing.T) {
	t.Parallel()

	getter := &fakeGetter{store: map[string]string{"PRESENT": "sentinel-value-7f3a"}}
	pairs, err := secrets.FetchNamed(context.Background(), getter, []string{"PRESENT", "ABSENT"})

	require.ErrorContains(t, err, "ABSENT")
	assert.NotContains(t, err.Error(), "sentinel-value-7f3a")
	assert.Nil(t, pairs)
}

// A value pasted into --only by mistake must not be echoed back: rejected
// items are reported by position and length, never by their text.
func TestFetchNamedReportsRejectedNamesByPositionAndLengthOnly(t *testing.T) {
	t.Parallel()

	const pasted = "sk-sentinel-value-2b7e"

	getter := &fakeGetter{}
	_, err := secrets.FetchNamed(context.Background(), getter, []string{"OK_KEY", pasted, "openrouter/api_key"})

	require.ErrorContains(t, err, "item 2 (22 chars)")
	require.ErrorContains(t, err, "item 3 (18 chars)")
	assert.NotContains(t, err.Error(), pasted)
	assert.NotContains(t, err.Error(), "openrouter/api_key")
	assert.Empty(t, getter.calls, "no SSM call when any name is invalid")
}

func TestFetchNamedRefusesAnEmptyList(t *testing.T) {
	t.Parallel()

	_, err := secrets.FetchNamed(context.Background(), &fakeGetter{}, []string{" ", ""})
	require.Error(t, err)
}

func TestFetchNamedDropsRepeatsAndKeepsOrder(t *testing.T) {
	t.Parallel()

	getter := &fakeGetter{store: map[string]string{"B": "b", "A": "a"}}
	pairs, err := secrets.FetchNamed(context.Background(), getter, []string{"B", "A", "B"})
	require.NoError(t, err)

	assert.Equal(t, []secrets.Pair{{Key: "B", Value: "b"}, {Key: "A", Value: "a"}}, pairs)
	assert.Len(t, getter.calls[0].Names, 2)
}

type deniedGetter struct{}

func (deniedGetter) GetParameters(
	_ context.Context,
	_ *ssm.GetParametersInput,
	_ ...func(*ssm.Options),
) (*ssm.GetParametersOutput, error) {
	return nil, errors.New("AccessDeniedException: not authorized to perform ssm:GetParameters")
}

func TestFetchNamedSurfacesAccessDeniedWithTheKeyNames(t *testing.T) {
	t.Parallel()

	_, err := secrets.FetchNamed(context.Background(), deniedGetter{}, []string{"NULLTICKETS_API_TOKEN"})
	require.ErrorContains(t, err, "NULLTICKETS_API_TOKEN")
	require.ErrorContains(t, err, "AccessDeniedException")
}

// fakeLister pages parameter names the way DescribeParameters does, with a
// numeric NextToken, and records every request.
type fakeLister struct {
	names    []string
	calls    []*ssm.DescribeParametersInput
	pageSize int
}

func (f *fakeLister) DescribeParameters(
	_ context.Context,
	in *ssm.DescribeParametersInput,
	_ ...func(*ssm.Options),
) (*ssm.DescribeParametersOutput, error) {
	f.calls = append(f.calls, in)

	start := 0
	if in.NextToken != nil {
		start, _ = strconv.Atoi(*in.NextToken)
	}

	end := min(start+f.pageSize, len(f.names))
	out := &ssm.DescribeParametersOutput{}

	for _, name := range f.names[start:end] {
		out.Parameters = append(out.Parameters, types.ParameterMetadata{Name: aws.String(secrets.Path + name)})
	}

	if end < len(f.names) {
		out.NextToken = aws.String(strconv.Itoa(end))
	}

	return out, nil
}

func TestStoreEnvNamesListsEveryPageByNameUnderTheTree(t *testing.T) {
	t.Parallel()

	lister := &fakeLister{
		names:    []string{"NULLTICKETS_API_TOKEN", "openrouter/api_key", "RESEND_API_KEY", "not-an-env-name"},
		pageSize: 2,
	}

	names, err := secrets.StoreEnvNames(context.Background(), lister)
	require.NoError(t, err)

	assert.Equal(t, map[string]bool{
		"NULLTICKETS_API_TOKEN": true,
		"OPENROUTER_API_KEY":    true,
		"RESEND_API_KEY":        true,
	}, names)
	require.Len(t, lister.calls, 2)

	filter := lister.calls[0].ParameterFilters
	require.Len(t, filter, 1)
	assert.Equal(t, "Path", aws.ToString(filter[0].Key))
	assert.Equal(t, "Recursive", aws.ToString(filter[0].Option))
	assert.Equal(t, []string{secrets.Path}, filter[0].Values)
}

type deniedLister struct{}

func (deniedLister) DescribeParameters(
	_ context.Context,
	_ *ssm.DescribeParametersInput,
	_ ...func(*ssm.Options),
) (*ssm.DescribeParametersOutput, error) {
	return nil, errors.New("AccessDeniedException: not authorized to perform ssm:DescribeParameters")
}

func TestStoreEnvNamesSurfacesAListingError(t *testing.T) {
	t.Parallel()

	names, err := secrets.StoreEnvNames(context.Background(), deniedLister{})
	require.ErrorContains(t, err, "AccessDeniedException")
	assert.Nil(t, names)
}
