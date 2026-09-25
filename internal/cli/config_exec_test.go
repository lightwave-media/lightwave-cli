//nolint:testpackage // swaps the unexported SSM-client and exec seams
package cli

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/secrets"
)

const execSentinel = "sentinel-value-9c41"

type execFakeGetter struct {
	store   map[string]string
	listErr error
	calls   int
}

// DescribeParameters lists every store key by name, as SSM does, or fails
// with listErr.
func (f *execFakeGetter) DescribeParameters(
	_ context.Context,
	_ *ssm.DescribeParametersInput,
	_ ...func(*ssm.Options),
) (*ssm.DescribeParametersOutput, error) {
	f.calls++
	if f.listErr != nil {
		return nil, f.listErr
	}

	out := &ssm.DescribeParametersOutput{}
	for name := range f.store {
		out.Parameters = append(out.Parameters, types.ParameterMetadata{Name: aws.String(secrets.Path + name)})
	}

	return out, nil
}

func (f *execFakeGetter) GetParameters(
	_ context.Context,
	in *ssm.GetParametersInput,
	_ ...func(*ssm.Options),
) (*ssm.GetParametersOutput, error) {
	f.calls++
	out := &ssm.GetParametersOutput{}
	for _, name := range in.Names {
		if value, ok := f.store[strings.TrimPrefix(name, secrets.Path)]; ok {
			out.Parameters = append(out.Parameters, types.Parameter{Name: aws.String(name), Value: aws.String(value)})
		}
	}

	return out, nil
}

type execRecord struct {
	binary string
	argv   []string
	env    []string
	called bool
}

// withExecSeams isolates HOME, swaps the SSM client and exec for fakes, and
// returns the recorder plus the command's captured stdout/stderr.
func withExecSeams(t *testing.T, store map[string]string, only []string) (*execRecord, *execFakeGetter, *cobra.Command, *bytes.Buffer) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	getter := &execFakeGetter{store: store}
	rec := &execRecord{}

	prevClient, prevExec, prevOnly := newConfigExecClient, execProcess, configExecOnly
	t.Cleanup(func() { newConfigExecClient, execProcess, configExecOnly = prevClient, prevExec, prevOnly })

	newConfigExecClient = func(context.Context) (secrets.NamedClient, error) { return getter, nil }
	execProcess = func(binary string, argv []string, env []string) error {
		rec.called, rec.binary, rec.argv, rec.env = true, binary, argv, env

		return nil
	}
	configExecOnly = only

	out := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(out)
	cmd.SetErr(out)

	return rec, getter, cmd, out
}

// requireSecretsUnavailable asserts err asks for exit 78 (EX_CONFIG).
func requireSecretsUnavailable(t *testing.T, err error) {
	t.Helper()

	code, ok := ExitCode(err)
	require.True(t, ok, "a failure before exec carries an exit code")
	assert.Equal(t, exitSecretsUnavailable, code)
}

func envValues(env []string, key string) []string {
	var values []string
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == key {
			values = append(values, v)
		}
	}

	return values
}

//nolint:paralleltest // swaps package-level seams and HOME
func TestConfigExecPutsNamedKeysOnlyInTheChildEnvironment(t *testing.T) {
	t.Setenv("NULLTICKETS_API_TOKEN", "stale-inherited-copy")
	t.Setenv("UNREQUESTED", "must-not-appear")
	rec, _, cmd, out := withExecSeams(t,
		map[string]string{"NULLTICKETS_API_TOKEN": execSentinel, "UNREQUESTED": "must-not-appear"},
		[]string{"NULLTICKETS_API_TOKEN"})

	require.NoError(t, runConfigExec(cmd, []string{"sh", "-c", "true"}))

	require.True(t, rec.called)
	sh, err := exec.LookPath("sh")
	require.NoError(t, err)
	assert.Equal(t, sh, rec.binary)
	assert.Equal(t, []string{"sh", "-c", "true"}, rec.argv)
	assert.Equal(t, []string{execSentinel}, envValues(rec.env, "NULLTICKETS_API_TOKEN"), "fetched value replaces the inherited copy")
	assert.Empty(t, envValues(rec.env, "UNREQUESTED"))
	for _, arg := range rec.argv {
		assert.NotContains(t, arg, execSentinel)
	}
	assert.Empty(t, out.String())
}

//nolint:paralleltest // swaps package-level seams and HOME
func TestConfigExecFailsClosedBeforeExecWhenAKeyIsMissing(t *testing.T) {
	rec, _, cmd, out := withExecSeams(t,
		map[string]string{"PRESENT": execSentinel},
		[]string{"PRESENT", "ABSENT"})

	err := runConfigExec(cmd, []string{"sh", "-c", "true"})

	require.ErrorContains(t, err, "ABSENT")
	requireSecretsUnavailable(t, err)
	assert.NotContains(t, err.Error(), execSentinel)
	assert.False(t, rec.called, "no exec with a partial environment")
	assert.NotContains(t, out.String(), execSentinel)
}

// A parent that still holds store keys (the retired session dump) must not
// pass the ones the command did not name, including nested names under the
// variable `lw config env` exports them as.
//
//nolint:paralleltest // swaps package-level seams and HOME
func TestConfigExecStripsInheritedStoreKeysTheCommandDidNotName(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "inherited-store-key")
	t.Setenv("OPENROUTER_API_KEY", "inherited-nested-store-key")
	t.Setenv("LW_NOT_A_STORE_KEY", "kept")
	rec, _, cmd, _ := withExecSeams(t,
		map[string]string{
			"NULLTICKETS_API_TOKEN": execSentinel,
			"RESEND_API_KEY":        "store-value",
			"openrouter/api_key":    "store-value",
		},
		[]string{"NULLTICKETS_API_TOKEN"})

	require.NoError(t, runConfigExec(cmd, []string{"sh", "-c", "true"}))

	require.True(t, rec.called)
	assert.Equal(t, []string{execSentinel}, envValues(rec.env, "NULLTICKETS_API_TOKEN"))
	assert.Empty(t, envValues(rec.env, "RESEND_API_KEY"))
	assert.Empty(t, envValues(rec.env, "OPENROUTER_API_KEY"))
	assert.Equal(t, []string{"kept"}, envValues(rec.env, "LW_NOT_A_STORE_KEY"))
}

//nolint:paralleltest // swaps package-level seams and HOME
func TestConfigExecFailsClosedWhenTheStoreNamesCannotBeListed(t *testing.T) {
	rec, getter, cmd, out := withExecSeams(t,
		map[string]string{"PRESENT": execSentinel},
		[]string{"PRESENT"})
	getter.listErr = errors.New("AccessDeniedException: not authorized to perform ssm:DescribeParameters")

	err := runConfigExec(cmd, []string{"sh", "-c", "true"})

	require.ErrorContains(t, err, "AccessDeniedException")
	requireSecretsUnavailable(t, err)
	assert.NotContains(t, err.Error(), execSentinel)
	assert.False(t, rec.called, "no exec when inherited store keys cannot be identified")
	assert.NotContains(t, out.String(), execSentinel)
}

// A value pasted into --only by mistake is never echoed back.
//
//nolint:paralleltest // swaps package-level seams and HOME
func TestConfigExecNeverEchoesARejectedOnlyItem(t *testing.T) {
	rec, getter, cmd, out := withExecSeams(t, map[string]string{"A": "a"}, []string{"A", execSentinel})

	err := runConfigExec(cmd, []string{"sh", "-c", "true"})

	require.ErrorContains(t, err, "item 2")
	requireSecretsUnavailable(t, err)
	assert.NotContains(t, err.Error(), execSentinel)
	assert.Zero(t, getter.calls, "no SSM read when a name is invalid")
	assert.False(t, rec.called)
	assert.NotContains(t, out.String(), execSentinel)
}

//nolint:paralleltest // swaps package-level seams and HOME
func TestConfigExecRequiresOnly(t *testing.T) {
	rec, getter, cmd, _ := withExecSeams(t, map[string]string{"A": "a"}, nil)

	err := runConfigExec(cmd, []string{"sh"})
	require.ErrorContains(t, err, "--only is required")
	requireSecretsUnavailable(t, err)
	assert.Zero(t, getter.calls, "no SSM read without --only")
	assert.False(t, rec.called)
}

//nolint:paralleltest // swaps package-level seams and HOME
func TestConfigExecRefusesAnUnknownCommandBeforeReadingSSM(t *testing.T) {
	rec, getter, cmd, _ := withExecSeams(t, map[string]string{"A": "a"}, []string{"A"})

	err := runConfigExec(cmd, []string{"no-such-binary-4c1e"})
	require.ErrorContains(t, err, "not found on PATH")
	assert.NotContains(t, err.Error(), "no-such-binary-4c1e", "a pasted value must not be echoed")
	_, coded := ExitCode(err)
	assert.False(t, coded, "a missing command is not a secrets failure")
	assert.Zero(t, getter.calls)
	assert.False(t, rec.called)
}

//nolint:paralleltest // parses into the package-level --only slice
func TestConfigExecLeavesTheChildsFlagsToTheChild(t *testing.T) {
	prev := configExecOnly
	t.Cleanup(func() { configExecOnly = prev })

	// A fresh command, so the parse leaves the real command's flag untouched.
	fresh := &cobra.Command{}
	configExecFlags(fresh)

	flags := fresh.Flags()
	require.NoError(t, flags.Parse([]string{"--only", "A,B", "curl", "-s", "--only", "x"}))

	assert.Equal(t, []string{"A", "B"}, splitOnly(configExecOnly))
	assert.Equal(t, []string{"curl", "-s", "--only", "x"}, flags.Args())
}

// pflag's comma-separated slice parses values as CSV and echoes one it cannot
// parse. A JSON-shaped value pasted into --only must reach our own validation,
// which reports it by position and length only.
//
//nolint:paralleltest // swaps package-level seams and HOME
func TestConfigExecNeverEchoesAValueFlagParsingWouldReject(t *testing.T) {
	rec, getter, cmd, out := withExecSeams(t, map[string]string{"A": "a"}, nil)

	fresh := &cobra.Command{}
	configExecFlags(fresh)
	pasted := `{"a":"` + execSentinel + `"}`
	require.NoError(t, fresh.Flags().Parse([]string{"--only", pasted, "sh", "-c", "true"}))

	err := runConfigExec(cmd, fresh.Flags().Args())

	require.ErrorContains(t, err, "item 1")
	requireSecretsUnavailable(t, err)
	assert.NotContains(t, err.Error(), execSentinel)
	assert.Zero(t, getter.calls)
	assert.False(t, rec.called)
	assert.NotContains(t, out.String(), execSentinel)
}

//nolint:paralleltest // swaps package-level seams and HOME
func TestConfigExecReadsRepeatedOnlyFlags(t *testing.T) {
	rec, _, cmd, _ := withExecSeams(t, map[string]string{"A": "a", "B": "b", "C": "c"}, []string{"A,B", "C"})

	require.NoError(t, runConfigExec(cmd, []string{"sh", "-c", "true"}))

	require.True(t, rec.called)
	for key, value := range map[string]string{"A": "a", "B": "b", "C": "c"} {
		assert.Equal(t, []string{value}, envValues(rec.env, key))
	}
}

func TestConfigExecRefusesToStartWithoutACommand(t *testing.T) {
	t.Parallel()

	err := configExecCmd.Args(configExecCmd, nil)

	require.ErrorContains(t, err, "name the command")
	requireSecretsUnavailable(t, err)
}

//nolint:paralleltest // swaps package-level seams and HOME
func TestConfigExecReportsAnExecFailureWithoutValuesAndWithoutExitCode78(t *testing.T) {
	_, _, cmd, out := withExecSeams(t, map[string]string{"A": execSentinel}, []string{"A"})
	execProcess = func(string, []string, []string) error { return errors.New("exec format error") }

	err := runConfigExec(cmd, []string{"sh", "-c", "true"})

	require.ErrorContains(t, err, "exec format error")
	assert.NotContains(t, err.Error(), execSentinel)
	_, coded := ExitCode(err)
	assert.False(t, coded, "after the environment is assembled, a failure is not EX_CONFIG")
	assert.NotContains(t, out.String(), execSentinel)
}

// pflag quotes the whole token in "unknown shorthand flag" and "bad flag
// syntax" errors, so `-only=<value>` would print the value.
//
//nolint:paralleltest // configExecFlags binds the package-level --only slice
func TestConfigExecFlagErrorsNeverEchoTheToken(t *testing.T) {
	prev := configExecOnly
	t.Cleanup(func() { configExecOnly = prev })

	fresh := &cobra.Command{}
	configExecFlags(fresh)
	parseErr := fresh.Flags().Parse([]string{"-only=" + execSentinel, "--", "true"})
	require.Error(t, parseErr)
	require.Contains(t, parseErr.Error(), execSentinel, "pflag's own message carries the token")

	err := configExecCmd.FlagErrorFunc()(configExecCmd, parseErr)

	require.ErrorContains(t, err, "--only KEY")
	requireSecretsUnavailable(t, err)
	assert.NotContains(t, err.Error(), execSentinel)
}
