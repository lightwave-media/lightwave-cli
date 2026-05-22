package testutil_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/cli"
	"github.com/lightwave-media/lightwave-cli/internal/testutil"
)

// One-shot handler registration. handler_registry panics on duplicate
// keys so we guard with sync.Once — `go test -count=N` would otherwise
// blow up on the second run.
var registerEcho sync.Once

func registerEchoHandler() {
	registerEcho.Do(func() {
		cli.RegisterHandler("testutil.echo", func(_ context.Context, args []string, flags map[string]any) error {
			fmt.Printf("args=%v flags=%v\n", args, flags)
			return nil
		})
		cli.RegisterHandler("testutil.error", func(_ context.Context, _ []string, _ map[string]any) error {
			return errors.New("synthetic handler error")
		})
	})
}

func TestRunHandler_UnknownKey_ReturnsError(t *testing.T) {
	t.Parallel()
	out, err := testutil.RunHandler(t, "no.such.handler.exists", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no handler registered")
	assert.Empty(t, out)
}

func TestRunHandler_CapturesStdoutOfRegisteredHandler(t *testing.T) {
	registerEchoHandler()
	t.Parallel()
	out, err := testutil.RunHandler(t, "testutil.echo",
		[]string{"hello", "world"},
		map[string]any{"json": true},
	)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out, "args=[hello world]"), "got: %q", out)
	assert.Contains(t, out, "flags=map[json:true]")
}

func TestRunHandler_PropagatesHandlerError(t *testing.T) {
	registerEchoHandler()
	t.Parallel()
	out, err := testutil.RunHandler(t, "testutil.error", nil, nil)
	require.Error(t, err)
	assert.Equal(t, "synthetic handler error", err.Error())
	assert.Empty(t, out)
}

// Cannot use t.Parallel here: we mutate process-wide env. Setenv
// auto-restores via t.Cleanup, so the test stays isolated despite
// running serially.
//
//nolint:paralleltest // intentionally serial — process-wide env mutation
func TestNewPool_SkipsWhenEnvUnset(t *testing.T) {
	if os.Getenv(testutil.EnvTestDBURL) != "" {
		t.Skipf("%s is set in this environment; cannot test skip path here", testutil.EnvTestDBURL)
	}

	t.Run("skip-fires", func(sub *testing.T) {
		// NewPool calls sub.Skip; the parent test sees that as a pass.
		// We're really asserting "no panic, no fatal" — Skip is the
		// expected exit path.
		_ = testutil.NewPool(sub)
		sub.Fatal("NewPool should have skipped before reaching here")
	})
}
