package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/sst"
	"github.com/spf13/cobra"
)

// Schema-driven check handlers. 13 handlers registered below. The matching
// `check:` domain in lightwave-core's commands.yaml is what wires them to
// the dispatcher; without it, all 13 are orphans (registered, not in schema)
// and `lw check` is unreachable from the binary. The schema-side declaration
// is drafted at packages/lightwave-cli/docs/pending-schema-additions.yaml
// and ships in a separate PR against the lightwave-core repo. `lw check
// schema` lists the orphans during the transitional state — this is
// expected, not a regression.
//
// Shape notes:
//   - ci/ruff/types/domains/deps/smoke thin-wrap an existing Make target.
//   - locks/git/docker/aws are direct probes (git/exec, no Make hop).
//   - schema reuses the Phase 3 drift validator core (in this file below).
//   - compose delegates to compose.verify so docker-compose drift is
//     reported by one validator, not two.
//   - ruff honours --staged: scope to `git diff --cached --name-only` *.py
//     instead of the full Make target. Other handlers may grow --staged
//     support in follow-up PRs.

func init() {
	RegisterHandler("check.ci", checkCIHandler)
	RegisterHandler("check.ruff", checkRuffHandler)
	RegisterHandler("check.types", checkTypesHandler)
	RegisterHandler("check.domains", checkDomainsHandler)
	RegisterHandler("check.schema", checkSchemaHandler)
	RegisterHandler("check.locks", checkLocksHandler)
	RegisterHandler("check.deps", checkDepsHandler)
	RegisterHandler("check.git", checkGitHandler)
	RegisterHandler("check.aws", checkAWSHandler)
	RegisterHandler("check.docker", checkDockerHandler)
	RegisterHandler("check.ecs", checkECSHandler)
	RegisterHandler("check.smoke", checkSmokeHandler)
	RegisterHandler("check.compose", checkComposeHandler)
}

func checkCIHandler(_ context.Context, _ []string, flags map[string]any) error {
	dir, err := resolveMakeDir("root")
	if err != nil {
		return err
	}

	target := "ci-local"
	if flagBool(flags, "skip-tests") {
		target = "ci-local-fast"
	}

	return runMake(dir, target)
}

func checkRuffHandler(_ context.Context, _ []string, flags map[string]any) error {
	if flagBool(flags, "staged") {
		return runRuffStaged(flagBool(flags, "fix"))
	}

	dir, err := resolveMakeDir("platform")
	if err != nil {
		return err
	}

	if flagBool(flags, "fix") {
		return runMake(dir, "ruff-fix")
	}

	return runMake(dir, "ruff")
}

// runRuffStaged scopes ruff to the staged .py files only, bypassing the
// platform-scoped Make target so a stage anywhere in the monorepo (core,
// packages, tooling) is covered. ruff resolves its own config from each
// file's parent dir, so no --config flag is needed.
func runRuffStaged(fix bool) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	root := cfg.Paths.LightwaveRoot

	files, err := stagedPythonFiles(root)
	if err != nil {
		return err
	}

	if len(files) == 0 {
		fmt.Println(color.GreenString("✓ no staged .py files — ruff skipped"))
		return nil
	}

	args := []string{"check"}
	if fix {
		args = append(args, "--fix")
	}

	args = append(args, files...)
	c := exec.Command("ruff", args...)
	c.Dir = root
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	return c.Run()
}

// stagedPythonFiles returns staged .py paths relative to the repo root.
// --diff-filter=ACMR drops deleted entries so ruff doesn't try to open
// paths that no longer exist on disk.
func stagedPythonFiles(root string) ([]string, error) {
	c := exec.Command("git", "diff", "--cached", "--name-only", "--diff-filter=ACMR")
	c.Dir = root

	out, err := c.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff --cached: %s", strings.TrimSpace(string(out)))
	}

	var files []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".py") {
			files = append(files, line)
		}
	}

	return files, nil
}

func checkTypesHandler(_ context.Context, _ []string, _ map[string]any) error {
	dir, err := resolveMakeDir("platform")
	if err != nil {
		return err
	}

	return runMake(dir, "npm-type-check")
}

func checkDomainsHandler(_ context.Context, _ []string, _ map[string]any) error {
	dir, err := resolveMakeDir("platform")
	if err != nil {
		return err
	}

	return runMake(dir, "lint-api-domains")
}

// EnvCheckSchemaStrict makes `lw check schema` exit non-zero when ANY
// drift is detected (missing handlers OR orphaned handlers). Default
// behavior stays informational (exit 0, print the report) so local devs
// can see drift without their workflow breaking, but CI sets the env so
// drift cannot land silently. See docs in CLAUDE.md "Schema-Driven CLI"
// section.
const EnvCheckSchemaStrict = "LW_CHECK_SCHEMA_STRICT"

// checkSchemaHandler is the Phase 3 drift validator, re-shaped for the
// dispatcher. Default mode: report drift, exit 0. With LW_CHECK_SCHEMA_STRICT=1
// set in env, exits 1 when drift is detected — that's the form CI uses
// (configured in .github/workflows/base-test.yml). Closes the gap the
// gruntwork-harden mission's PR9 identified: silent drift on PRs that
// added handlers without schema entries or vice versa.
func checkSchemaHandler(_ context.Context, _ []string, flags map[string]any) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	schema, err := sst.LoadCLIConfig(cfg.Paths.LightwaveRoot)
	if err != nil {
		return fmt.Errorf("load CLI schema: %w", err)
	}

	// KeysPublished excludes in_development domains so the strict gate does
	// not fire on commands declared before their Go handler companion lands.
	schemaKeys := schema.KeysPublished()
	registryKeys := RegisteredKeys()

	schemaSet := make(map[string]bool, len(schemaKeys))
	for _, k := range schemaKeys {
		schemaSet[k] = true
	}

	registrySet := make(map[string]bool, len(registryKeys))
	for _, k := range registryKeys {
		registrySet[k] = true
	}

	var missing, orphaned []string

	for _, k := range schemaKeys {
		if !registrySet[k] {
			missing = append(missing, k)
		}
	}

	for _, k := range registryKeys {
		if !schemaSet[k] {
			orphaned = append(orphaned, k)
		}
	}

	sort.Strings(missing)
	sort.Strings(orphaned)

	// Third direction: invocable but unstamped. Compared against Keys() rather
	// than KeysPublished() on purpose — an in_development command IS stamped,
	// it is just not dispatched yet, so counting it here would report drift
	// against an entry that already exists.
	surfaceKeys := cobraSurfaceKeys(rootCmd)
	stamped := make(map[string]bool, len(schema.Keys()))

	for _, k := range schema.Keys() {
		stamped[k] = true
	}

	var unstamped []string

	for _, k := range surfaceKeys {
		if !stamped[k] {
			unstamped = append(unstamped, k)
		}
	}

	sort.Strings(unstamped)

	report := schemaDriftReport{
		SchemaVersion:     schema.Version,
		DomainCount:       len(schema.Domains),
		CommandCount:      len(schemaKeys),
		HandlerCount:      len(registryKeys),
		SurfaceCount:      len(surfaceKeys),
		MissingHandlers:   missing,
		OrphanedHandlers:  orphaned,
		UnstampedCommands: unstamped,
	}
	if len(schemaKeys) > 0 {
		matched := len(schemaKeys) - len(missing)
		report.HandlerMatchRatio = float64(matched) / float64(len(schemaKeys))
	}

	if asJSON(flags) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("encode drift report: %w", err)
		}
	} else {
		printSchemaDriftHuman(report)
	}

	if os.Getenv(EnvCheckSchemaStrict) != "1" {
		return nil
	}

	if len(missing) > 0 || len(orphaned) > 0 {
		return fmt.Errorf("schema↔handler drift detected (%d missing, %d orphaned); see report above. Cure: add the missing handler OR add/remove the schema entry, then re-run `lw check schema`",
			len(missing), len(orphaned))
	}

	// Ratchet, not a threshold: only GROWTH past the recorded backlog fails.
	// See unstampedBaseline for why the existing 36 do not block every PR.
	if len(unstamped) > unstampedBaseline {
		return fmt.Errorf("unstamped commands grew to %d (baseline %d); see report above. Cure: declare the new command in lightwave-core's interfaces/cli/commands.yaml, or lower unstampedBaseline if you removed one",
			len(unstamped), unstampedBaseline)
	}

	return nil
}

// lockFileNames are the lock files a workspace repo may own. A repo that
// tracks neither contributes nothing — absence is not drift.
var lockFileNames = []string{"uv.lock", "pnpm-lock.yaml"}

// checkLocksHandler verifies that every tracked lock file in the workspace is
// committed (no uncommitted changes that would drift CI).
//
// It asks each repository, not the workspace parent. The parent is not a git
// repository in the flat sibling layout, so the previous version ran git where
// git could not work and read the resulting usage text as a diff — reporting
// both lock files dirty on every machine, including machines where no repo has
// ever tracked uv.lock (#444).
func checkLocksHandler(ctx context.Context, _ []string, _ map[string]any) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	repos, err := workspaceGitRepos(cfg.Paths.LightwaveRoot)
	if err != nil {
		return err
	}

	var (
		dirty   []string
		tracked int
	)

	for _, repo := range repos {
		for _, name := range lockFileNames {
			isTracked, trackErr := fileIsTracked(ctx, repo, name)
			if trackErr != nil {
				return trackErr
			}

			if !isTracked {
				continue
			}

			tracked++

			out, diffErr := runGitDiff(ctx, repo, name)
			if diffErr != nil {
				return diffErr
			}

			if strings.TrimSpace(out) != "" {
				dirty = append(dirty, filepath.Join(filepath.Base(repo), name))
			}
		}
	}

	if len(dirty) > 0 {
		return fmt.Errorf("uncommitted lock changes: %s", strings.Join(dirty, ", "))
	}

	fmt.Println(color.GreenString("✓ lock files clean (%d tracked across %d repo(s))", tracked, len(repos)))

	return nil
}

// checkDepsHandler verifies workspace dependency consistency. Delegates to
// the Make target (no direct Go impl — pnpm/uv would re-implement work).
func checkDepsHandler(_ context.Context, _ []string, _ map[string]any) error {
	dir, err := resolveMakeDir("root")
	if err != nil {
		return err
	}

	return runMake(dir, "deps-check")
}

// checkGitHandler ensures every workspace repo's working tree is clean (no
// uncommitted changes to tracked files; untracked files are allowed).
//
// Same correction as checkLocksHandler: the workspace parent is not a git
// repository, and `git diff --quiet` run there exits 129 for usage — which the
// previous version reported as "uncommitted changes in tracked files" (#444).
func checkGitHandler(ctx context.Context, _ []string, _ map[string]any) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	repos, err := workspaceGitRepos(cfg.Paths.LightwaveRoot)
	if err != nil {
		return err
	}

	var dirty []string

	for _, repo := range repos {
		changed, changedErr := hasUncommittedChanges(ctx, repo)
		if changedErr != nil {
			return changedErr
		}

		if changed {
			dirty = append(dirty, filepath.Base(repo))
		}
	}

	if len(dirty) > 0 {
		return fmt.Errorf("uncommitted changes in tracked files: %s", strings.Join(dirty, ", "))
	}

	fmt.Println(color.GreenString("✓ working tree clean (%d repo(s))", len(repos)))

	return nil
}

// checkAWSHandler verifies AWS credentials resolve via STS.
func checkAWSHandler(_ context.Context, _ []string, _ map[string]any) error {
	c := exec.Command("aws", "sts", "get-caller-identity")

	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("AWS credentials not configured: %s", strings.TrimSpace(string(out)))
	}

	fmt.Println(color.GreenString("✓ AWS credentials valid"))

	return nil
}

// checkDockerHandler verifies the Docker daemon is reachable.
func checkDockerHandler(_ context.Context, _ []string, _ map[string]any) error {
	c := exec.Command("docker", "info")
	if err := c.Run(); err != nil {
		return errors.New("docker daemon not running")
	}

	fmt.Println(color.GreenString("✓ Docker daemon running"))

	return nil
}

// checkECSHandler reports ECS service health.
//
// The cluster comes from deployClusterFor, the same resolver `lw deploy` uses.
// It derived `"platform-" + env` until now — the Django-era name #368 removed
// from the deploy group. That fix missed this verb, so `lw check ecs` went on
// querying a cluster that no longer exists and failing with
// ClusterNotFoundException, while `lw deploy status` against the same
// environment worked. The deploy package comment even cited "`lw check ecs`
// conventions" as precedent for the name, which is how a convention outlives
// the thing it named.
//
// Sharing the resolver rather than copying the default is the point: two copies
// would drift again the next time the cluster is renamed.
func checkECSHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw check ecs <service> [--environment=<env>]")
	}

	env := flagStrOr(flags, "environment", "prod")

	c := exec.CommandContext(ctx, "aws", "ecs", "describe-services",
		"--cluster", deployClusterFor(env), "--services", args[0],
		"--query", "services[0].{Status:status,Desired:desiredCount,Running:runningCount}",
		"--output", "table")
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	return c.Run()
}

// checkComposeHandler delegates to compose.verify so docker-compose.yml stays
// in sync with SST. Defaults to --env=local — staging/production verification
// is meaningful too but is a CI concern, not a pre-commit concern.
func checkComposeHandler(ctx context.Context, args []string, flags map[string]any) error {
	if _, set := flags["env"]; !set {
		// Force the default explicitly so composeVerifyHandler's validation
		// path is consistent regardless of how `lw check compose` is invoked.
		if flags == nil {
			flags = map[string]any{}
		}

		flags["env"] = "local"
	}

	return composeVerifyHandler(ctx, args, flags)
}

func checkSmokeHandler(_ context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw check smoke <target> [--env=<env>]")
	}

	dir, err := resolveMakeDir("platform")
	if err != nil {
		return err
	}

	extra := []string{"TARGET=" + args[0]}
	if env := flagStr(flags, "env"); env != "" {
		extra = append(extra, "ENV="+env)
	}

	return runMake(dir, "smoke", extra...)
}

// runGitDiff returns the diff output for a path. Empty string = clean.
// runGitDiff returns the diff for one path inside repo.
//
// It reads stdout ONLY, and propagates failure. The previous version called
// CombinedOutput and then discarded the error on both branches — the if and the
// fallthrough returned the same value — so when git failed, its stderr became
// the return value and every caller testing for non-empty output read the error
// text as a diff (#444).
func runGitDiff(ctx context.Context, repo, path string) (string, error) {
	c := exec.CommandContext(ctx, "git", "diff", "--", path)
	c.Dir = repo

	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("git diff %s in %s: %w", path, repo, err)
	}

	return string(out), nil
}

// fileIsTracked reports whether repo tracks path. An absent or untracked file
// is not drift; treating it as drift is how the old check named uv.lock on
// machines where no repository has ever contained one.
func fileIsTracked(ctx context.Context, repo, path string) (bool, error) {
	c := exec.CommandContext(ctx, "git", "ls-files", "--", path)
	c.Dir = repo

	out, err := c.Output()
	if err != nil {
		return false, fmt.Errorf("git ls-files %s in %s: %w", path, repo, err)
	}

	return strings.TrimSpace(string(out)) != "", nil
}

// hasUncommittedChanges reports whether repo has modifications to tracked files.
//
// `git diff --quiet` exits 1 when there are differences and 128/129 when it
// cannot run at all. Collapsing every non-zero exit into "dirty" is exactly how
// a usage error came to be reported as uncommitted work, so the two are
// separated here: only exit 1 is an answer, anything else is a failure to
// measure and must surface as one.
func hasUncommittedChanges(ctx context.Context, repo string) (bool, error) {
	c := exec.CommandContext(ctx, "git", "diff", "--quiet")
	c.Dir = repo

	err := c.Run()
	if err == nil {
		return false, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}

	return false, fmt.Errorf("git diff --quiet in %s: %w", repo, err)
}

// workspaceGitRepos returns the git work trees a workspace-scoped check should
// inspect.
//
// paths.lightwave_root is the PARENT of the repositories in the flat sibling
// layout — it is not itself a repository, and git run there fails rather than
// answering. Resolving the repositories first is what makes these checks
// measurable at all. A root that IS a repository resolves to itself, so
// pointing lightwave_root at a single checkout keeps working.
//
// A root with no repositories under it returns an error rather than an empty
// sweep: a check that silently examines nothing and prints a tick is worse than
// one that fails, because it reports a guarantee it never established.
func workspaceGitRepos(root string) ([]string, error) {
	if isGitWorkTree(root) {
		return []string{root}, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("reading workspace %s: %w", root, err)
	}

	var repos []string

	// Deliberately no IsDir() precondition: ReadDir reports a SYMLINK as a
	// non-directory, and symlinking a checkout into a root is a supported
	// layout here (it is how LW_LIGHTWAVE_ROOT is pointed at a worktree). A
	// non-directory entry simply has no .git child, so the work-tree test is
	// the only test needed.
	for _, e := range entries {
		if dir := filepath.Join(root, e.Name()); isGitWorkTree(dir) {
			repos = append(repos, dir)
		}
	}

	if len(repos) == 0 {
		return nil, fmt.Errorf("%s is neither a git repository nor a workspace containing one", root)
	}

	return repos, nil
}

// isGitWorkTree reports whether dir is the root of a git work tree. A linked
// worktree carries .git as a FILE rather than a directory, so the entry's type
// is deliberately not part of the test.
func isGitWorkTree(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))

	return err == nil
}

// schemaDriftReport is the JSON shape emitted by `lw check schema --json`.
// Originally lived in check_schema.go (Phase 3 standalone); moved here when
// the dispatcher subsumed the legacy cobra tree (Phase 5 sweep).
type schemaDriftReport struct {
	SchemaVersion     string   `json:"schema_version"`
	MissingHandlers   []string `json:"missing_handlers"`
	OrphanedHandlers  []string `json:"orphaned_handlers"`
	UnstampedCommands []string `json:"unstamped_commands"`
	DomainCount       int      `json:"domain_count"`
	CommandCount      int      `json:"command_count"`
	HandlerCount      int      `json:"handler_count"`
	SurfaceCount      int      `json:"surface_count"`
	HandlerMatchRatio float64  `json:"handler_match_ratio"`
}

// unstampedBaseline is the number of invocable commands absent from the stamp
// when this third drift direction was first measured (#337).
//
// A ratchet, not a threshold. Turning 36 known-unstamped commands into an
// immediate hard failure would have produced a gate whose first act is to block
// every PR, and the reliable outcome of that is the gate being switched off. So
// the bar is "no WORSE than when we started": new unstamped commands fail, the
// existing backlog does not. Lower this number as domains get stamped; raising
// it needs a reason in the commit message.
//
// The same ratcheting posture golangci-lint already runs here with
// --new-from-merge-base.
//
// Measured, not estimated: `lw check schema --json` on main at e1d635c. The
// first count was 75, which was wrong — it included 43 bare domain names,
// because cobra reports a group as Runnable() when it prints its own help. Those
// are domains in the stamp's model, never commands, so "stamping" them would
// have meant inventing entries that must not exist. Leaves only, hence 32.
const unstampedBaseline = 32

// cobraSurfaceKeys returns the dotted key of every runnable command on the
// assembled tree — what `lw <domain> <verb>` actually accepts, which is NOT the
// same set as the handler registry.
//
// The registry holds only what RegisterHandler wired. Commands attached to the
// legacy cobra tree with AddCommand never enter it, so a drift check that reads
// the registry alone cannot see them: `lw check schema` reported "✓ no drift" at
// 100% coverage while 36 working commands were absent from the stamp entirely
// (#337). A green gate blind to a third of the CLI is worse than a red one,
// because green ends the investigation.
//
// Note for maintainers: Commands() lazily sorts the child slice in place, so
// this must stay on one goroutine. See mcp_handlers_test.go for the race that
// caused.
func cobraSurfaceKeys(root *cobra.Command) []string {
	if root == nil {
		return nil
	}

	var out []string

	var walk func(parent *cobra.Command, prefix string)

	walk = func(parent *cobra.Command, prefix string) {
		for _, child := range parent.Commands() {
			name := child.Name()

			// cobra generates these; they are not part of the lw surface.
			if child.Hidden || name == "help" || name == "completion" {
				continue
			}

			key := name
			if prefix != "" {
				key = prefix + "." + name
			}

			// Leaves only. A command that carries subcommands is a group —
			// `lw db`, `lw config harness` — and cobra reports those as
			// Runnable() because they print their own help. The stamp models
			// them as domains and command groups, never as commands, so
			// counting them reported 43 phantom "unstamped commands" whose cure
			// would have been to stamp entries that must not exist.
			if child.Runnable() && !child.HasSubCommands() {
				out = append(out, key)
			}

			walk(child, key)
		}
	}

	walk(root, "")
	sort.Strings(out)

	return out
}

func printSchemaDriftHuman(r schemaDriftReport) {
	fmt.Printf("%s %s\n", color.CyanString("CLI schema:"), r.SchemaVersion)
	fmt.Printf("  domains:  %d\n", r.DomainCount)
	fmt.Printf("  commands: %d\n", r.CommandCount)
	fmt.Printf("  handlers: %d (%.0f%% coverage)\n",
		r.HandlerCount, r.HandlerMatchRatio*100)

	// Only meaningful when the tree was assembled; a handler invoked outside the
	// assembled binary sees an empty surface, and printing "0" there would read
	// as "nothing unstamped" rather than "not measured".
	if r.SurfaceCount > 0 {
		fmt.Printf("  surface:  %d invocable (%d unstamped)\n",
			r.SurfaceCount, len(r.UnstampedCommands))
	}

	if len(r.UnstampedCommands) > 0 {
		fmt.Printf("\n%s (%d, baseline %d)\n",
			color.YellowString("unstamped commands (invocable, absent from the stamp)"),
			len(r.UnstampedCommands), unstampedBaseline)

		for _, k := range r.UnstampedCommands {
			fmt.Printf("  - %s\n", k)
		}
	}

	if len(r.MissingHandlers) == 0 && len(r.OrphanedHandlers) == 0 {
		if len(r.UnstampedCommands) == 0 {
			fmt.Println(color.GreenString("\n✓ no drift"))
		} else {
			fmt.Println(color.YellowString(
				"\n✓ no schema↔handler drift — but the unstamped backlog above is still open"))
		}

		return
	}

	if len(r.MissingHandlers) > 0 {
		fmt.Printf("\n%s (%d)\n",
			color.YellowString("missing handlers (declared in schema, no Go handler)"),
			len(r.MissingHandlers))

		for _, k := range r.MissingHandlers {
			fmt.Printf("  - %s\n", k)
		}
	}

	if len(r.OrphanedHandlers) > 0 {
		fmt.Printf("\n%s (%d)\n",
			color.RedString("orphaned handlers (registered but not in schema)"),
			len(r.OrphanedHandlers))

		for _, k := range r.OrphanedHandlers {
			fmt.Printf("  - %s\n", k)
		}
	}
}
