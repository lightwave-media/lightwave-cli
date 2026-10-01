package cli

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	gh "github.com/lightwave-media/lightwave-cli/internal/github"
)

// The merge gate the loop applies before it arms or squashes a green PR. A
// check that is green but not required could not have said no: platform#710
// merged through four green checks none of which the branch required. So the
// loop merges only where the base branch requires checks, every required one
// passed at head, the repo is not lightwave-core, and the diff stays outside
// the paths ADR-0058 reserves for an operator. Anything else is held for a
// person: the PR stays open and ready, the lane keeps working.

const (
	// The policy file that names the operator-only paths, read from core at main.
	coreRepo               = "lightwave-media/lightwave-core"
	branchProtectionPolicy = "src/schemas/policy/governance/branch_protection.yaml"
	mergeBase              = "main"

	reconcileHeld = "held"

	holdCore            = "core_never_armed"
	holdNoRequiredCheck = "no_required_check"
	holdRequiredPending = "required_check_not_passed"
	holdOperatorPath    = "needs_operator_path"
	holdUnverifiable    = "cannot_verify"
)

// Seams for tests: the three reads the gate makes.
var (
	requiredChecks   = gh.RequiredChecks
	pullRequestFiles = gh.PullRequestFiles
	readRepoFile     = gh.ReadRepoFile
)

// mergeGate holds the operator paths for one reconcile run, read once.
type mergeGate struct {
	err    error
	paths  []string
	loaded bool
}

// hold says why pr of repo must wait for a person, or "" when the loop may
// merge it. Every failure to read is a hold: a gate that cannot see does not
// say yes.
func (g *mergeGate) hold(repo string, pr *gh.PullRequest) string {
	if repo == coreRepo {
		return holdCore + ": ADR-0058 keeps arming off for lightwave-core; an operator merges it"
	}

	required, err := requiredChecks(repo, mergeBase)
	if err != nil {
		return holdUnverifiable + ": " + err.Error()
	}

	if len(required) == 0 {
		return holdNoRequiredCheck + ": " + mergeBase + " of " + repo + " requires no status check, so nothing could have said no"
	}

	if missing := requiredNotPassed(required, pr.Checks); missing != "" {
		return holdRequiredPending + ": required check " + missing + " has not passed at head"
	}

	paths, err := g.operatorPaths()
	if err != nil {
		return holdUnverifiable + ": " + err.Error()
	}

	files, err := pullRequestFiles(repo, pr.Number)
	if err != nil {
		return holdUnverifiable + ": " + err.Error()
	}

	for _, file := range files {
		for _, pattern := range paths {
			if globMatch(pattern, file) {
				return holdOperatorPath + ": " + file + " matches " + pattern
			}
		}
	}

	return ""
}

// requiredNotPassed names the first required check that is not SUCCESS on the
// PR's head, or "" when every one passed. A skipped check is not a pass.
func requiredNotPassed(required []string, checks []gh.CheckOutcome) string {
	passed := map[string]bool{}

	for _, c := range checks {
		if c.Conclusion == "SUCCESS" {
			passed[c.Name] = true
		}
	}

	for _, r := range required {
		if !passed[r] {
			return r
		}
	}

	return ""
}

func (g *mergeGate) operatorPaths() ([]string, error) {
	if !g.loaded {
		g.paths, g.err = loadOperatorPaths()
		g.loaded = true
	}

	return g.paths, g.err
}

// loadOperatorPaths reads merge_policy.needs_operator_paths from core at main.
// An empty list is refused: the policy always names some, so empty means the
// file moved or changed shape, and the gate must not read that as "none".
func loadOperatorPaths() ([]string, error) {
	raw, err := readRepoFile(coreRepo, branchProtectionPolicy, mergeBase)
	if err != nil {
		return nil, err
	}

	// The policy instance sits under `example:`, as auditRemoteBranchProtection
	// reads it.
	var policy struct {
		Example struct {
			MergePolicy struct {
				NeedsOperatorPaths []string `yaml:"needs_operator_paths"`
			} `yaml:"merge_policy"`
		} `yaml:"example"`
	}
	if err := yaml.Unmarshal(raw, &policy); err != nil {
		return nil, fmt.Errorf("parse %s: %w", branchProtectionPolicy, err)
	}

	paths := policy.Example.MergePolicy.NeedsOperatorPaths
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s names no merge_policy.needs_operator_paths", branchProtectionPolicy)
	}

	return paths, nil
}

// globMatch matches a repo-relative path against a policy pattern: `**` spans
// directories, `*` and `?` stay within one.
func globMatch(pattern, path string) bool {
	var re strings.Builder

	re.WriteString("^")

	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; {
		case c == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
			re.WriteString(".*")

			i++
		case c == '*':
			re.WriteString("[^/]*")
		case c == '?':
			re.WriteString("[^/]")
		default:
			re.WriteString(regexp.QuoteMeta(string(c)))
		}
	}

	re.WriteString("$")

	return regexp.MustCompile(re.String()).MatchString(path)
}
