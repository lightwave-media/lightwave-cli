package testutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/cli"
)

// RunHandler invokes the dispatcher-registered handler at `key` with
// the given positional args and flag map, captures everything written
// to stdout during the call, and returns (stdout, err). The flag map
// shape matches what the dispatcher hands handlers in production:
// long flag name (no leading "--") → value.
//
// If no handler is registered for `key`, RunHandler returns an error
// rather than failing the test outright — callers can assert that
// path when testing the registry surface itself.
//
// stdout capture works by swapping os.Stdout for an os.Pipe; the
// reader side drains in a goroutine so writes that exceed the pipe
// buffer don't deadlock.
func RunHandler(t *testing.T, key string, args []string, flags map[string]any) (string, error) {
	t.Helper()

	h, ok := cli.LookupHandler(key)
	if !ok {
		return "", fmt.Errorf("no handler registered for %q", key)
	}

	oldStdout := os.Stdout

	r, w, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("os.Pipe: %w", err)
	}

	os.Stdout = w

	var buf bytes.Buffer

	done := make(chan struct{})

	go func() {
		_, _ = io.Copy(&buf, r)

		close(done)
	}()

	handlerErr := h(context.Background(), args, flags)

	// Close the write end so the goroutine sees EOF and exits, then
	// restore stdout. We block on `done` to ensure the buffer is fully
	// drained before returning the captured string.
	_ = w.Close()
	os.Stdout = oldStdout

	<-done

	_ = r.Close()

	return buf.String(), handlerErr
}
