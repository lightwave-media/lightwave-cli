package release_test

import (
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/release"
	"github.com/stretchr/testify/require"
)

func TestRequireQaReleasePass_SkipsWhenFlagOff(t *testing.T) { //nolint:paralleltest // pinFlags uses t.Setenv
	pinFlags(t)

	require.NoError(t, release.RequireQaReleasePass())
}

func TestRequireQaReleasePass_BlocksWithoutVerdict(t *testing.T) {
	home := pinFlags(t)
	require.NoError(t, release.SetFlag("autonomous_qa_release_pass", true))

	err := release.RequireQaReleasePass()
	require.Error(t, err)

	dir := filepath.Join(home, ".lightwave", "artefacts", "release-qa", "latest")
	t.Setenv("LW_QA_ARTEFACT_DIR", dir)

	require.NoError(t, release.WriteStubVerdict(dir))
	require.NoError(t, release.RequireQaReleasePass())
}
