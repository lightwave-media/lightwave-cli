package uisync

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// GitBase returns a BaseProvider that extracts file content from the
// lightwave-ui git checkout at the pinned release tag (v<version>). A
// missing tag or a path absent at that tag yields ok=false — sync then
// treats unequal files as conflicts rather than guessing.
func GitBase(uiRepo string) BaseProvider {
	return func(version, relPath string) ([]byte, bool, error) {
		ref := "v" + strings.TrimPrefix(version, "v")

		cmd := exec.CommandContext(context.Background(), "git", "-C", uiRepo, "show", ref+":"+relPath)

		var out, stderr bytes.Buffer

		cmd.Stdout = &out
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			msg := stderr.String()
			if strings.Contains(msg, "does not exist") ||
				strings.Contains(msg, "invalid object name") ||
				strings.Contains(msg, "unknown revision") ||
				strings.Contains(msg, "exists on disk, but not in") {
				return nil, false, nil
			}

			return nil, false, fmt.Errorf("git show %s:%s: %s", ref, relPath, strings.TrimSpace(msg))
		}

		return out.Bytes(), true, nil
	}
}
