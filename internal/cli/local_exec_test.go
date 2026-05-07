package cli

import (
	"context"
	"strings"
	"testing"
)

// TestLocalExec_NoArgs_Errors covers the arg-validation guard that runs
// before any docker shell-out. The remaining handler paths require a
// running docker daemon (compose config + ps shell-outs) and are
// covered by the manual smoke tests called out in the PR body.
func TestLocalExec_NoArgs_Errors(t *testing.T) {
	err := localExecHandler(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error when no service provided")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Errorf("expected usage hint in error, got: %v", err)
	}
}
