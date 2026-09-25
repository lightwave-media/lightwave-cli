//nolint:testpackage // swaps the unexported SSM-client and exec seams
package cli

import (
	"bytes"
	"context"
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
	store map[string]string
	calls int
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

	newConfigExecClient = func(context.Context) (secrets.ParameterGetter, error) { return getter, nil }
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
	assert.NotContains(t, err.Error(), execSentinel)
	assert.False(t, rec.called, "no exec with a partial environment")
	assert.NotContains(t, out.String(), execSentinel)
}

//nolint:paralleltest // swaps package-level seams and HOME
func TestConfigExecRequiresOnly(t *testing.T) {
	rec, getter, cmd, _ := withExecSeams(t, map[string]string{"A": "a"}, nil)

	require.ErrorContains(t, runConfigExec(cmd, []string{"sh"}), "--only is required")
	assert.Zero(t, getter.calls, "no SSM read without --only")
	assert.False(t, rec.called)
}

//nolint:paralleltest // swaps package-level seams and HOME
func TestConfigExecRefusesAnUnknownCommandBeforeReadingSSM(t *testing.T) {
	rec, getter, cmd, _ := withExecSeams(t, map[string]string{"A": "a"}, []string{"A"})

	require.Error(t, runConfigExec(cmd, []string{"no-such-binary-4c1e"}))
	assert.Zero(t, getter.calls)
	assert.False(t, rec.called)
}

//nolint:paralleltest // parses into the package-level --only slice
func TestConfigExecLeavesTheChildsFlagsToTheChild(t *testing.T) {
	prev := configExecOnly
	t.Cleanup(func() {
		configExecOnly = prev
		_ = configExecCmd.Flags().Set("only", "")
	})

	flags := configExecCmd.Flags()
	require.NoError(t, flags.Parse([]string{"--only", "A,B", "curl", "-s", "--only", "x"}))

	assert.Equal(t, []string{"A", "B"}, configExecOnly)
	assert.Equal(t, []string{"curl", "-s", "--only", "x"}, flags.Args())
}
