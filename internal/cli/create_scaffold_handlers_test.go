package cli_test

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/cli"
	"github.com/lightwave-media/lightwave-cli/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateScaffold_DryRun proves each create.* handler is registered and its
// --dry-run path previews the right blueprint + vars without shelling out to
// the boilerplate engine. RunHandler swaps os.Stdout, so these are serial.
//
//nolint:paralleltest // RunHandler mutates process-global os.Stdout
func TestCreateScaffold_DryRun(t *testing.T) {
	cases := []struct {
		key      string
		arg      string
		wantBP   string
		wantVars []string
	}{
		{
			key:      "create.website",
			arg:      "my-site",
			wantBP:   "website",
			wantVars: []string{"project_name=my-site"},
		},
		{
			key:      "create.webapp",
			arg:      "app.foo.com",
			wantBP:   "webapp-v1",
			wantVars: []string{"domain=app.foo.com", "today="},
		},
		{
			key:      "create.desktop-app",
			arg:      "createos.io",
			wantBP:   "desktop-app-v1",
			wantVars: []string{"domain=createos.io", "tenet=createos", "app_name=Createos", "bundle_id=io.createos.app", "today="},
		},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			out, err := testutil.RunHandler(t, tc.key, []string{tc.arg}, map[string]any{"dry-run": true})
			require.NoError(t, err, "dry-run should not error")
			assert.Contains(t, out, "would render blueprint", "dry-run preview missing")
			assert.Contains(t, out, tc.wantBP, "wrong blueprint slug")

			for _, v := range tc.wantVars {
				assert.Contains(t, out, v, "missing var %q", v)
			}
		})
	}
}

// TestCreateScaffold_BlueprintOverride proves --blueprint replaces the default.
// Uses create.webapp because its schema actually declares --blueprint (website's
// does not — see the flag-surface note in create_scaffold_handlers.go).
//
//nolint:paralleltest // RunHandler mutates process-global os.Stdout
func TestCreateScaffold_BlueprintOverride(t *testing.T) {
	out, err := testutil.RunHandler(t, "create.webapp",
		[]string{"app.foo.com"},
		map[string]any{"dry-run": true, "blueprint": "custom-bp"})
	require.NoError(t, err)
	assert.Contains(t, out, `"custom-bp"`, "override blueprint not used")
}

// TestCreateScaffold_MissingArg proves a non-interactive call with no positional
// arg (stdin at EOF) falls through to a usage error rather than hanging.
//
//nolint:paralleltest // RunHandler mutates process-global os.Stdout
func TestCreateScaffold_MissingArg(t *testing.T) {
	_, err := testutil.RunHandler(t, "create.webapp", nil, map[string]any{"dry-run": true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "usage: lw create webapp")
}

func TestDeriveTenet(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"createos.io": "createos",
		"app.foo.com": "app",
		"single":      "single",
	}
	for in, want := range cases {
		assert.Equal(t, want, cli.DeriveTenet(in), "input %q", in)
	}
}

func TestReverseDomain(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "io.createos", cli.ReverseDomain("createos.io"))
	assert.Equal(t, "com.foo.app", cli.ReverseDomain("app.foo.com"))
	assert.Equal(t, "single", cli.ReverseDomain("single"))
}

func TestTitleFirst(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "Createos", cli.TitleFirst("createos"))
	assert.Empty(t, cli.TitleFirst(""))
	assert.Equal(t, "X", cli.TitleFirst("x"))
}
