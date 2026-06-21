package release

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const qaReleasePassFlag = "autonomous_qa_release_pass"

const blockerCaptureGroups = 2 // full match + capture group

var blockerLine = regexp.MustCompile(`(?m)^QA-RELEASE-VERDICT: blockers=([0-9]+)`)

// QaReleasePassEnabled reports whether the QA release pass gate is active.
func QaReleasePassEnabled() (bool, error) {
	return IsEnabled(qaReleasePassFlag)
}

// RequireQaReleasePass blocks Release PR merge when the flag is on and the
// verdict artefact is missing or reports blockers > 0.
func RequireQaReleasePass() error {
	on, err := QaReleasePassEnabled()
	if err != nil {
		return err
	}

	if !on {
		return nil
	}

	path := qaVerdictPath()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf(
			"autonomous_qa_release_pass is on but verdict missing at %s — run dev/release_qa_pass.sh and emit 06-qa-release-verdict.md with blockers=0",
			path,
		)
	}

	m := blockerLine.FindSubmatch(data)
	if len(m) != blockerCaptureGroups {
		return fmt.Errorf("verdict at %s missing QA-RELEASE-VERDICT line", path)
	}

	if string(m[1]) != "0" {
		return fmt.Errorf("QA release pass blocked (blockers=%s) — see %s", string(m[1]), path)
	}

	return nil
}

func qaVerdictPath() string {
	if p := os.Getenv("LW_QA_RELEASE_VERDICT"); p != "" {
		return p
	}

	if p := os.Getenv("LW_QA_ARTEFACT_DIR"); p != "" {
		return filepath.Join(p, "06-qa-release-verdict.md")
	}

	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".lightwave", "artefacts", "release-qa", "latest", "06-qa-release-verdict.md")
}

// WriteStubVerdict creates a passing stub verdict for local smoke (blockers=0).
func WriteStubVerdict(dir string) error {
	if dir == "" {
		dir = filepath.Dir(qaVerdictPath())
	}

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	body := strings.TrimSpace(`
# QA Release Pass Verdict (stub)

QA-RELEASE-VERDICT: blockers=0 tests_proposed=0
`) + "\n"

	return os.WriteFile(filepath.Join(dir, "06-qa-release-verdict.md"), []byte(body), filePerm)
}
