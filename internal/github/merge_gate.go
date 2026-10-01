package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// The reads the issue loop's merge gate makes before it lets a PR merge: what
// the base branch actually requires, which files a PR changes, and a file from
// another repo at a ref. All read-only, all through `gh`.

// RequiredChecks lists the status checks branch of repo requires, from classic
// branch protection and from rulesets together, deduplicated and sorted. An
// unprotected branch requires nothing; that is an empty list, not an error.
func RequiredChecks(repo, branch string) ([]string, error) {
	seen := map[string]bool{}

	classic, err := ghAPI("repos/" + repo + "/branches/" + branch + "/protection/required_status_checks")
	switch {
	case err == nil:
		var protection struct {
			Contexts []string `json:"contexts"`
			Checks   []struct {
				Context string `json:"context"`
			} `json:"checks"`
		}
		if err := json.Unmarshal(classic, &protection); err != nil {
			return nil, fmt.Errorf("parse branch protection of %s: %w", repo, err)
		}

		for _, c := range protection.Contexts {
			seen[c] = true
		}

		for _, c := range protection.Checks {
			seen[c.Context] = true
		}
	case !notProtected(err):
		return nil, fmt.Errorf("read branch protection of %s: %w", repo, err)
	}

	rules, err := ghAPI("repos/" + repo + "/rules/branches/" + branch)
	if err != nil {
		return nil, fmt.Errorf("read rulesets of %s: %w", repo, err)
	}

	var rows []struct {
		Type       string `json:"type"`
		Parameters struct {
			RequiredStatusChecks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(rules, &rows); err != nil {
		return nil, fmt.Errorf("parse rulesets of %s: %w", repo, err)
	}

	for _, r := range rows {
		if r.Type != "required_status_checks" {
			continue
		}

		for _, c := range r.Parameters.RequiredStatusChecks {
			seen[c.Context] = true
		}
	}

	required := make([]string, 0, len(seen))
	for c := range seen {
		if c != "" {
			required = append(required, c)
		}
	}

	sort.Strings(required)

	return required, nil
}

// PullRequestFiles lists the paths PR number of repo changes.
func PullRequestFiles(repo string, number int) ([]string, error) {
	out, err := exec.Command("gh", "pr", "view", strconv.Itoa(number), "--repo", repo, "--json", "files", "--jq", ".files[].path").Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr view %s#%d --json files: %w", repo, number, err)
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}

	return files, nil
}

// ReadRepoFile returns path of repo at ref. Reading a ref, never a local
// checkout: a shared checkout's working tree can be on anyone's branch.
func ReadRepoFile(repo, path, ref string) ([]byte, error) {
	out, err := ghAPI("repos/"+repo+"/contents/"+path+"?ref="+ref, "-H", "Accept: application/vnd.github.raw")
	if err != nil {
		return nil, fmt.Errorf("read %s:%s at %s: %w", repo, path, ref, err)
	}

	return out, nil
}

// ghAPIError keeps what gh printed, so a 404 can be told from a real failure.
type ghAPIError struct {
	err    error
	output string
}

func (e *ghAPIError) Error() string { return e.err.Error() + ": " + strings.TrimSpace(e.output) }

func ghAPI(endpoint string, extra ...string) ([]byte, error) {
	cmd := exec.Command("gh", append([]string{"api", endpoint}, extra...)...)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, &ghAPIError{err: err, output: stderr.String() + string(out)}
	}

	return out, nil
}

// notProtected recognises GitHub's answer for a branch with no classic
// protection, or protection without required status checks.
func notProtected(err error) bool {
	apiErr, ok := err.(*ghAPIError) //nolint:errorlint // ghAPI returns this type unwrapped
	if !ok {
		return false
	}

	return strings.Contains(apiErr.output, "HTTP 404")
}
