// Package secrets turns the SSM /lightwave/prod/ tree into a session's
// environment. It is the one loader behind `lw config env`; every harness
// adapter (Claude Code session-start hook, Pi extension, a shell's
// `eval "$(lw config env)"`) calls that verb rather than reading SSM itself.
//
// Values are returned to the caller and rendered to stdout only. Nothing in
// this package logs, caches or writes them.
package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const (
	// Region is where the /lightwave/prod/ tree lives (CLAUDE.md §5).
	Region = "us-east-1"
	// Path is the parameter tree read, recursively.
	Path = "/lightwave/prod/"
)

// ParameterPager is the slice of the SSM client this package needs. Tests
// fake it; production passes the client from NewClient.
type ParameterPager interface {
	GetParametersByPath(
		ctx context.Context,
		in *ssm.GetParametersByPathInput,
		opts ...func(*ssm.Options),
	) (*ssm.GetParametersByPathOutput, error)
}

// Param is one SSM parameter: its full name and decrypted value.
type Param struct {
	Name  string
	Value string
}

// Pair is one environment variable ready to export.
type Pair struct {
	Key   string
	Value string
}

// NewClient builds an SSM client from the default credential chain — the
// harness configuration already sets AWS_PROFILE, so no profile is chosen here.
func NewClient(ctx context.Context) (ParameterPager, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(Region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	return ssm.NewFromConfig(cfg), nil
}

// Fetch walks every page of Path and returns the decrypted parameters.
func Fetch(ctx context.Context, client ParameterPager) ([]Param, error) {
	var (
		params []Param
		token  *string
	)

	for {
		out, err := client.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
			Path:           aws.String(Path),
			Recursive:      aws.Bool(true),
			WithDecryption: aws.Bool(true),
			NextToken:      token,
		})
		if err != nil {
			return nil, fmt.Errorf("get parameters by path %s: %w", Path, err)
		}

		for _, p := range out.Parameters {
			if p.Name == nil || p.Value == nil {
				continue
			}

			params = append(params, Param{Name: *p.Name, Value: *p.Value})
		}

		if out.NextToken == nil || *out.NextToken == "" {
			return params, nil
		}

		token = out.NextToken
	}
}

var envNameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// EnvName maps a parameter name to the environment variable it exports as:
// the Path prefix is stripped, remaining slashes become underscores, and the
// result is upper-cased. It returns "" when the result is not a valid name.
func EnvName(name string) string {
	rest := strings.TrimPrefix(name, Path)
	key := strings.ToUpper(strings.ReplaceAll(rest, "/", "_"))

	if !envNameRE.MatchString(key) {
		return ""
	}

	return key
}

// Resolve maps parameters to export pairs, sorted by key. Two parameters can
// map to one key (the store holds both OPENROUTER_API_KEY and
// openrouter/api_key): the flat name wins and the loser is reported in notes,
// as is any parameter whose name cannot be an environment variable.
func Resolve(params []Param) ([]Pair, []string) {
	sorted := make([]Param, len(params))
	copy(sorted, params)
	// Flat names first, so a nested name never claims a key a flat one holds.
	sort.SliceStable(sorted, func(i, j int) bool {
		ni, nj := isNested(sorted[i].Name), isNested(sorted[j].Name)
		if ni != nj {
			return !ni
		}

		return sorted[i].Name < sorted[j].Name
	})

	var (
		pairs []Pair
		notes []string
		owner = map[string]string{}
	)

	for _, p := range sorted {
		key := EnvName(p.Name)
		if key == "" {
			notes = append(notes, fmt.Sprintf("skipped %s (not a valid environment variable name)", p.Name))

			continue
		}

		if prior, taken := owner[key]; taken {
			notes = append(notes, fmt.Sprintf("skipped %s (collides with %s from %s)", p.Name, key, prior))

			continue
		}

		owner[key] = p.Name
		pairs = append(pairs, Pair{Key: key, Value: p.Value})
	}

	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Key < pairs[j].Key })

	return pairs, notes
}

func isNested(name string) bool {
	return strings.Contains(strings.TrimPrefix(name, Path), "/")
}

// ShellQuote wraps a value in single quotes for POSIX shells, the one quoting
// form where nothing inside is interpreted.
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// RenderShell writes one `export KEY='value'` line per pair.
func RenderShell(pairs []Pair) string {
	var b strings.Builder
	for _, p := range pairs {
		b.WriteString("export ")
		b.WriteString(p.Key)
		b.WriteString("=")
		b.WriteString(ShellQuote(p.Value))
		b.WriteString("\n")
	}

	return b.String()
}

// RenderJSON writes the pairs as a single flat object.
func RenderJSON(pairs []Pair) ([]byte, error) {
	obj := make(map[string]string, len(pairs))
	for _, p := range pairs {
		obj[p.Key] = p.Value
	}

	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode env as JSON: %w", err)
	}

	return append(out, '\n'), nil
}
