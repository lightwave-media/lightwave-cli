package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/release"
	"gopkg.in/yaml.v3"
)

const releaseGHOrg = "lightwave-media"

func init() {
	RegisterHandler("release.flag", releaseFlagHandler)
	RegisterHandler("release.cut", releaseCutHandler)
	RegisterHandler("release.sign-off", releaseSignOffHandler)
	RegisterHandler("release.merge", releaseMergeHandler)
	RegisterHandler("release.prepare", releasePrepareHandler)
	RegisterHandler("release.ship", releaseShipHandler)
}

type signoff struct {
	ApprovedBy string `yaml:"approved_by"`
	SHA        string `yaml:"sha,omitempty"`
	Note       string `yaml:"note,omitempty"`
}

type signoffLedger struct {
	Signoffs map[string]signoff `yaml:"signoffs"`
}

type prCandidate struct {
	Title     string
	Number    int
	Draft     bool
	Mergeable bool
	CIGreen   bool
}

type checkRollupEntry struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

func releaseFlagHandler(_ context.Context, args []string, flags map[string]any) (err error) {
	start := time.Now()

	defer func() {
		outcome := "pass"
		code := 0
		detail := ""

		if err != nil {
			outcome = "fail"
			code = 1
			detail = err.Error()
		}

		m := map[string]any{"list": flagBool(flags, "list")}
		if len(args) > 0 {
			m["flag_key"] = args[0]
		}

		emitOperatorCLI("release.flag", outcome, detail, code, start, m)
	}()

	if flagBool(flags, "list") || len(args) == 0 {
		items, err := release.ListFlags()
		if err != nil {
			return err
		}

		for _, item := range items {
			state := "off"
			if item.Enabled {
				state = "on"
			}

			fmt.Printf("%-32s %s (default=%t)\n", item.Key, state, item.Default)
		}

		return nil
	}

	key := args[0]
	on := flagBool(flags, "on")
	off := flagBool(flags, "off")

	switch {
	case on && off:
		return errors.New("use exactly one of --on or --off")
	case !on && !off:
		enabled, err := release.IsEnabled(key)
		if err != nil {
			return err
		}

		fmt.Printf("%s=%t\n", key, enabled)

		return nil
	case on:
		if err := release.SetFlag(key, true); err != nil {
			return err
		}

		fmt.Printf("%s flag %s ON — rollback: lw release flag %s --off\n",
			color.GreenString("✓"), key, key)

		return nil
	default:
		if err := release.SetFlag(key, false); err != nil {
			return err
		}

		fmt.Printf("%s flag %s OFF\n", color.YellowString("●"), key)

		return nil
	}
}

func releaseCutHandler(_ context.Context, _ []string, flags map[string]any) error {
	version := flagString(flags, "version")
	flagKey := flagString(flags, "flag")
	note := flagString(flags, "note")

	if version == "" || flagKey == "" {
		return errors.New("usage: lw release cut --version <tag> --flag <key> [--note <text>]")
	}

	auditPath := releaseCutAuditPath()
	entry := map[string]string{
		"version": version,
		"flag":    flagKey,
		"note":    note,
	}

	data, _ := os.ReadFile(auditPath)

	var entries []map[string]string
	if len(data) > 0 {
		_ = yaml.Unmarshal(data, &entries)
	}

	entries = append(entries, entry)

	out, err := yaml.Marshal(entries)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(auditPath), gitDirPerm); err != nil {
		return err
	}

	if err := os.WriteFile(auditPath, out, gitFilePerm); err != nil {
		return err
	}

	fmt.Printf("%s registered cut %s wrapping flag %s\n", color.GreenString("✓"), version, flagKey)

	return nil
}

func releaseSignOffHandler(_ context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw release sign-off <repo> --by <cto> [--sha <sha>] [--note <text>] [--clear]")
	}

	repo := shortRepo(args[0])

	ledger, err := loadSignoffs()
	if err != nil {
		return err
	}

	if flagBool(flags, "clear") {
		delete(ledger.Signoffs, repo)

		if err := saveSignoffs(ledger); err != nil {
			return err
		}

		fmt.Printf("%s cleared sign-off for %s\n", color.YellowString("●"), repo)

		return nil
	}

	by := flagString(flags, "by")
	if by == "" {
		return errors.New("--by <cto> is required to record a sign-off")
	}

	ledger.Signoffs[repo] = signoff{
		ApprovedBy: by,
		SHA:        flagString(flags, "sha"),
		Note:       flagString(flags, "note"),
	}
	if err := saveSignoffs(ledger); err != nil {
		return err
	}

	fmt.Printf("%s sign-off recorded for %s by %s\n", color.GreenString("✓"), repo, by)

	return nil
}

func releaseMergeHandler(ctx context.Context, args []string, flags map[string]any) (err error) {
	start := time.Now()

	defer func() {
		outcome := "pass"
		code := 0
		detail := ""

		if err != nil {
			outcome = "fail"
			code = 1
			detail = err.Error()
		}

		m := map[string]any{
			"release_pr": flagBool(flags, "release-pr"),
			"apply":      flagBool(flags, "yes"),
		}
		if len(args) > 0 {
			m["repo"] = args[0]
		}

		emitOperatorCLI("release.merge", outcome, detail, code, start, m)
	}()

	if len(args) < 1 {
		return errors.New("usage: lw release merge <repo> [--pr N] [--yes] [--release-pr]")
	}

	repo := resolveRepo(args[0])
	apply := flagBool(flags, "yes")
	onlyPR := flagString(flags, "pr")
	releasePR := flagBool(flags, "release-pr")

	autonomous, err := release.MergeAutonomous()
	if err != nil {
		return err
	}

	releasePRAuto, err := release.ReleasePRAutonomous()
	if err != nil {
		return err
	}

	if releasePR && !releasePRAuto {
		fmt.Printf("%s autonomous_release_pr_merge is off — enable: lw release flag autonomous_release_pr_merge --on\n",
			color.RedString("✗"))

		return nil
	}

	if releasePR {
		if err := release.RequireQaReleasePass(); err != nil {
			fmt.Printf("%s %v\n", color.RedString("✗"), err)

			return nil
		}
	}

	ledger, err := loadSignoffs()
	if err != nil {
		return err
	}

	sign, signed := ledger.Signoffs[shortRepo(repo)]
	if !autonomous && !signed {
		fmt.Printf("%s no CTO sign-off for %s — run: lw release sign-off %s --by <cto>\n",
			color.RedString("✗"), repo, shortRepo(repo))

		return nil
	}

	candidates, err := fetchReleaseCandidates(ctx, repo)
	if err != nil {
		return err
	}

	eligible := make([]prCandidate, 0, len(candidates))

	for _, c := range candidates {
		if releasePR && !strings.HasPrefix(c.Title, "chore(main): release") {
			continue
		}

		if !releasePR && strings.HasPrefix(c.Title, "chore(main): release") {
			continue
		}

		if onlyPR != "" && strconv.Itoa(c.Number) != onlyPR {
			continue
		}

		if ok, reason := eligibleToMerge(c); !ok {
			fmt.Printf("  skip  #%-4d %s — %s\n", c.Number, c.Title, reason)
			continue
		}

		eligible = append(eligible, c)
	}

	if len(eligible) == 0 {
		fmt.Printf("%s nothing eligible to merge for %s\n", color.CyanString("●"), repo)
		return nil
	}

	for _, c := range eligible {
		if !apply {
			fmt.Printf("  would merge #%-4d %s\n", c.Number, c.Title)
			continue
		}

		if err := mergePR(ctx, repo, c.Number); err != nil {
			fmt.Fprintf(os.Stderr, "  failed #%d: %v\n", c.Number, err)
			continue
		}

		fmt.Printf("  %s merged #%-4d %s\n", color.GreenString("✓"), c.Number, c.Title)
	}

	verb := "would merge"
	if apply {
		verb = "merged"
	}

	by := "autonomous (ADR-0035)"
	if signed {
		by = sign.ApprovedBy
	}

	fmt.Printf("%s %s %d PR(s) for %s (%s)\n",
		color.CyanString("●"), verb, len(eligible), repo, by)

	return nil
}

func eligibleToMerge(c prCandidate) (bool, string) {
	switch {
	case c.Draft:
		return false, "draft"
	case !c.Mergeable:
		return false, "not mergeable"
	case !c.CIGreen:
		return false, "CI not green"
	default:
		return true, ""
	}
}

func checksGreen(rollup []checkRollupEntry) bool {
	if len(rollup) == 0 {
		return false
	}

	for _, e := range rollup {
		switch {
		case e.State != "":
			if e.State != "SUCCESS" {
				return false
			}
		case e.Status != "COMPLETED":
			return false
		case e.Conclusion != "SUCCESS" && e.Conclusion != "NEUTRAL" && e.Conclusion != "SKIPPED":
			return false
		}
	}

	return true
}

func fetchReleaseCandidates(ctx context.Context, repo string) ([]prCandidate, error) {
	out, err := exec.CommandContext(ctx, "gh", "pr", "list",
		"--repo", repo,
		"--state", "open",
		"--json", "number,title,isDraft,mergeable,statusCheckRollup",
		"--limit", "100",
	).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh pr list failed: %w\n%s", err, string(out))
	}

	var raw []struct {
		Title             string             `json:"title"`
		Mergeable         string             `json:"mergeable"`
		StatusCheckRollup []checkRollupEntry `json:"statusCheckRollup"`
		Number            int                `json:"number"`
		IsDraft           bool               `json:"isDraft"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}

	candidates := make([]prCandidate, 0, len(raw))
	for _, r := range raw {
		candidates = append(candidates, prCandidate{
			Number:    r.Number,
			Title:     r.Title,
			Draft:     r.IsDraft,
			Mergeable: r.Mergeable == "MERGEABLE",
			CIGreen:   checksGreen(r.StatusCheckRollup),
		})
	}

	return candidates, nil
}

func mergePR(ctx context.Context, repo string, number int) error {
	out, err := exec.CommandContext(ctx, "gh", "pr", "merge", strconv.Itoa(number),
		"--repo", repo,
		"--squash",
		"--delete-branch",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, string(out))
	}

	return nil
}

func resolveRepo(name string) string {
	if strings.Contains(name, "/") {
		return name
	}

	return releaseGHOrg + "/" + name
}

func shortRepo(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}

func signoffLedgerPath() string {
	if p := os.Getenv("LW_RELEASE_SIGNOFF"); p != "" {
		return p
	}

	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".lightwave", "config", "release-signoff.yaml")
}

func releaseCutAuditPath() string {
	if p := os.Getenv("LW_RELEASE_CUT_AUDIT"); p != "" {
		return p
	}

	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".lightwave", "config", "release-cuts.yaml")
}

func loadSignoffs() (signoffLedger, error) {
	ledger := signoffLedger{Signoffs: map[string]signoff{}}

	data, err := os.ReadFile(signoffLedgerPath())
	if errors.Is(err, os.ErrNotExist) {
		return ledger, nil
	}

	if err != nil {
		return ledger, err
	}

	if err := yaml.Unmarshal(data, &ledger); err != nil {
		return ledger, err
	}

	if ledger.Signoffs == nil {
		ledger.Signoffs = map[string]signoff{}
	}

	return ledger, nil
}

func saveSignoffs(ledger signoffLedger) error {
	data, err := yaml.Marshal(ledger)
	if err != nil {
		return err
	}

	path := signoffLedgerPath()
	if err := os.MkdirAll(filepath.Dir(path), gitDirPerm); err != nil {
		return err
	}

	return os.WriteFile(path, data, gitFilePerm)
}

func releasePrepareHandler(ctx context.Context, _ []string, flags map[string]any) error {
	args := []string{}
	if flagBool(flags, "yes") {
		args = append(args, "--yes")
	}

	return runReleaseScript(ctx, "release_prepare.sh", args...)
}

func releaseShipHandler(ctx context.Context, _ []string, flags map[string]any) error {
	extra := []string{}
	if flagBool(flags, "yes") {
		extra = append(extra, "--yes")
	}

	if t := flagString(flags, "title"); t != "" {
		extra = append(extra, "--title", t)
	}

	if s := flagString(flags, "supersedes"); s != "" {
		extra = append(extra, "--supersedes", s)
	}

	return runReleaseScript(ctx, "release_ship.sh", extra...)
}

func runReleaseScript(ctx context.Context, name string, args ...string) error {
	root, err := releaseRepoRoot()
	if err != nil {
		return err
	}

	script := filepath.Join(root, "dev", name)
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("missing %s (run from lightwave-cli checkout)", script)
	}

	cmd := exec.CommandContext(ctx, "bash", append([]string{script}, args...)...)
	cmd.Dir = root
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	return nil
}

func releaseRepoRoot() (string, error) {
	cfg := config.Get()
	if cfg != nil && cfg.Paths.LightwaveRoot != "" {
		candidate := filepath.Join(cfg.Paths.LightwaveRoot, "lightwave-cli")
		if _, err := os.Stat(filepath.Join(candidate, "dev", "release_prepare.sh")); err == nil {
			return candidate, nil
		}
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(wd, "dev", "release_prepare.sh")); err == nil {
			return wd, nil
		}

		parent := filepath.Dir(wd)
		if parent == wd {
			break
		}

		wd = parent
	}

	return "", errors.New("cannot locate lightwave-cli root (dev/release_prepare.sh)")
}
