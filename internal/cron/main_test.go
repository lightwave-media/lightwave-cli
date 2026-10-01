package cron_test

import (
	"os"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/testutil/gitfixture"
)

// TestMain isolates the process: LoadJobs shells out to git with the process
// environment, and under a hook's GIT_DIR it would read the hook's repository
// instead of the fixture.
func TestMain(m *testing.M) {
	gitfixture.Isolate()
	os.Exit(m.Run())
}
