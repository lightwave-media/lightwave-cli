package secrets

// named.go — fetch a declared set of keys BY NAME, for `lw config exec`.
//
// GetParameters (never GetParametersByPath) is the point: an IAM policy can be
// scoped to parameter ARNs, so a persona can later be granted exactly the keys
// its secret map lists. Fetch (env.go) reads the whole tree and stays the
// human-terminal loader behind `lw config env`.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

const (
	// AgentProfile is the read-only profile agents and daemons use when the
	// harness has not set AWS_PROFILE (brain/context/conventions.md).
	AgentProfile = "lightwave-agent"
	// getParametersLimit is the SSM API maximum names per GetParameters call.
	getParametersLimit = 10
	// describeParametersLimit is the SSM API maximum page size for
	// DescribeParameters.
	describeParametersLimit = 50
)

// ParameterGetter is the slice of the SSM client FetchNamed needs.
type ParameterGetter interface {
	GetParameters(
		ctx context.Context,
		in *ssm.GetParametersInput,
		opts ...func(*ssm.Options),
	) (*ssm.GetParametersOutput, error)
}

// ParameterLister is the slice of the SSM client StoreEnvNames needs.
// DescribeParameters returns metadata only, never a value.
type ParameterLister interface {
	DescribeParameters(
		ctx context.Context,
		in *ssm.DescribeParametersInput,
		opts ...func(*ssm.Options),
	) (*ssm.DescribeParametersOutput, error)
}

// NamedClient reads named keys and lists the store's names.
type NamedClient interface {
	ParameterGetter
	ParameterLister
}

// NewNamedClient builds the client for FetchNamed and StoreEnvNames:
// AWS_PROFILE when the harness set one, else AgentProfile, and SDK logging
// off so no request or response body can reach a log.
func NewNamedClient(ctx context.Context) (NamedClient, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(Region),
		awsconfig.WithClientLogMode(0),
	}
	if os.Getenv("AWS_PROFILE") == "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(AgentProfile))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	return ssm.NewFromConfig(cfg), nil
}

// FetchNamed returns one pair per key, in the order given, each read from
// Path+key. It fails closed: an invalid name, or any key the store does not
// return, is an error naming the keys and never a partial result. Errors
// carry names only.
func FetchNamed(ctx context.Context, client ParameterGetter, keys []string) ([]Pair, error) {
	names, err := uniqueKeys(keys)
	if err != nil {
		return nil, err
	}

	values := make(map[string]string, len(names))
	for start := 0; start < len(names); start += getParametersLimit {
		batch := names[start:min(start+getParametersLimit, len(names))]
		if err := fetchBatch(ctx, client, batch, values); err != nil {
			return nil, err
		}
	}

	var missing []string

	pairs := make([]Pair, 0, len(names))
	for _, key := range names {
		value, ok := values[key]
		if !ok {
			missing = append(missing, key)

			continue
		}

		pairs = append(pairs, Pair{Key: key, Value: value})
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("not in %s: %s", Path, strings.Join(missing, ", "))
	}

	return pairs, nil
}

// StoreEnvNames lists every parameter under Path, by name only, as the
// environment variable name `lw config env` would export it under (EnvName),
// so nested parameters map too. A caller uses it to strip inherited store keys
// it did not ask for.
func StoreEnvNames(ctx context.Context, client ParameterLister) (map[string]bool, error) {
	pages := ssm.NewDescribeParametersPaginator(client, &ssm.DescribeParametersInput{
		ParameterFilters: []types.ParameterStringFilter{{
			Key:    aws.String("Path"),
			Option: aws.String("Recursive"),
			Values: []string{Path},
		}},
		MaxResults: aws.Int32(describeParametersLimit),
	})

	names := map[string]bool{}

	for pages.HasMorePages() {
		out, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list parameter names under %s: %w", Path, err)
		}

		for i := range out.Parameters {
			if key := EnvName(aws.ToString(out.Parameters[i].Name)); key != "" {
				names[key] = true
			}
		}
	}

	return names, nil
}

func fetchBatch(ctx context.Context, client ParameterGetter, batch []string, into map[string]string) error {
	paths := make([]string, len(batch))
	for i, key := range batch {
		paths[i] = Path + key
	}

	out, err := client.GetParameters(ctx, &ssm.GetParametersInput{
		Names:          paths,
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("get parameters %s: %w", strings.Join(batch, ", "), err)
	}

	for _, p := range out.Parameters {
		if p.Name == nil || p.Value == nil {
			continue
		}

		into[strings.TrimPrefix(*p.Name, Path)] = *p.Value
	}

	return nil
}

// uniqueKeys validates each key as a flat environment variable name and drops
// repeats, keeping first-seen order. A rejected item is reported by position
// and length only, never by its text: it may be a value pasted by mistake.
func uniqueKeys(keys []string) ([]string, error) {
	seen := make(map[string]bool, len(keys))

	var names, invalid []string

	for i, raw := range keys {
		key := strings.TrimSpace(raw)
		if !envNameRE.MatchString(key) {
			invalid = append(invalid, fmt.Sprintf("item %d (%d chars)", i+1, utf8.RuneCountInString(key)))

			continue
		}

		if !seen[key] {
			seen[key] = true
			names = append(names, key)
		}
	}

	if len(invalid) > 0 {
		return nil, fmt.Errorf("not a key name (want UPPER_SNAKE under %s): %s", Path, strings.Join(invalid, ", "))
	}

	if len(names) == 0 {
		return nil, errors.New("no keys named")
	}

	return names, nil
}
