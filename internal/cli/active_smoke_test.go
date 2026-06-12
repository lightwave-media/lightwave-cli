//nolint:testpackage // needs internal access to command RunE + helpers
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These smoke tests back the VerifiedCommands entries that lack command-level
// coverage elsewhere. They exercise each active command's real logic so that
// "verified" is not just a claim.

//nolint:paralleltest // versionCmd is a shared global
func TestVersion_Runs(t *testing.T) {
	require.NotNil(t, versionCmd.RunE)
	assert.NoError(t, versionCmd.RunE(versionCmd, nil))
}

//nolint:paralleltest // exercises audit's core scanner
func TestAudit_DetectsPlantedSecret(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "settings.py")
	require.NoError(t, os.WriteFile(bad, []byte(`SECRET_KEY = "supersecretvalue123"`+"\n"), 0o644))

	findings, summary, err := collectSecurity(dir)
	require.NoError(t, err)
	require.NotNil(t, summary)
	assert.NotEmpty(t, findings, "security scanner should flag a hardcoded credential")
}

//nolint:paralleltest // exercises health's binary probe
func TestHealth_ChecksBinaries(t *testing.T) {
	ok := checkBinary("go", "go", false)
	assert.Equal(t, "ok", ok.Status, "go must resolve on PATH in CI/dev")

	missing := checkBinary("nope", "definitely-not-a-real-binary-xyz", false)
	assert.Equal(t, "fail", missing.Status)
}

//nolint:paralleltest // exercises ui component arg validation
func TestUIComponent_RejectsBadSpec(t *testing.T) {
	err := runUIComponent(uiComponentCmd, []string{"no-slash-here"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "<category>/<Name>")
}
