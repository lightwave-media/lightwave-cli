package cli //nolint:testpackage // exercises unexported devDomainsEnabled

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDevDomainsEnabled_ExplicitEnv(t *testing.T) {
	t.Setenv("LW_CLI_DEV_DOMAINS", "1")
	assert.True(t, devDomainsEnabled())
}

func TestDevDomainsEnabled_OffByDefaultInTestBinary(t *testing.T) {
	t.Setenv("LW_CLI_DEV_DOMAINS", "")
	// go test binary is not ~/.local/bin/lw — domains stay gated unless env set.
	assert.False(t, devDomainsEnabled())
}
