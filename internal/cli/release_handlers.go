package cli

// release_handlers.go — lw release sign-off / lw release merge
//
// The merge gate from ADR-0032 §D: the release-engineer executes merges, but
// only after the CTO records sign-off. A PR is eligible to merge iff it is
// (a) mergeable, (b) not a draft, and (c) its CI checks are green — AND the CTO
// has an active sign-off recorded for the repo. `lw release sign-off` records
// that approval; `lw release merge` enforces every condition before touching a
// PR.
//
// "Best of both worlds": the CTO gives input (one sign-off per repo) without
// having to drive each merge; the release-engineer (or the hourly cron) does
// the mechanical work, but only inside the gate.
//
// Sign-off ledger: ~/.lightwave/config/release-signoff.yaml (override with
// LW_RELEASE_SIGNOFF for tests). Merge is dry-run unless --yes (cli
// destructive-command standard). Merges use a plain --squash — never --admin:
// the gate already requires green CI, so there is nothing to bypass.

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

	"github.com/fatih/color"
	"gopkg.in/yaml.v3"
)

const releaseGHOrg = "lightwave-media"

func init() {
	RegisterHandler("release.sign-off", releaseSignOffHandler)
	RegisterHandler("release.merge", releaseMergeHandler)
}

// signoff is one CTO approval covering a repo's release-candidate PRs.
type signoff struct {
	ApprovedBy string `yaml:"approved_by"`
	SHA        string `yaml:"sha,omitempty"`  // main HEAD the sign-off was granted against
	Note       string `yaml:"note,omitempty"` // free-text rationale
}

type signoffLedger struct {
	Signoffs map[string]signoff `yaml:"signoffs"`
}

// prCandidate is the merge-relevant projection of an open PR.
type prCandidate struct {
	Title     string
	Number    int
	Draft     bool
	Mergeable bool
	CIGreen   bool
}

// checkRollupEntry mirrors the two shapes GitHub returns inside
// statusCheckRollup: CheckRun (Status+Conclusion) and StatusContext (State).
type checkRollupEntry struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// eligibleToMerge is the pure gate decision for a single PR. The CTO sign-off
// is checked once per repo by the caller; this covers the per-PR conditions.
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

// checksGreen reports whether every check in a PR's rollup has passed. An empty
// rollup is treated as not-green: a PR with no CI is not a release candidate.
func checksGreen(rollup []checkRollupEntry) bool {
	if len(rollup) == 0 {
		return false
	}

	for _, e := range rollup {
		switch {
		case e.State != "": // StatusContext (legacy commit status)
			if e.State != "SUCCESS" {
				return false
			}
		case e.Status != "COMPLETED": // CheckRun still queued/running
			return false
		case e.Conclusion != "SUCCESS" && e.Conclusion != "NEUTRAL" && e.Conclusion != "SKIPPED":
			return false
		}
	}

	return true
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

func releaseMergeHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw release merge <repo> [--pr N] [--yes]")
	}

	repo := resolveRepo(args[0])
	apply := flagBool(flags, "yes")
	onlyPR := flagString(flags, "pr")

	ledger, err := loadSignoffs()
	if err != nil {
		return err
	}

	sign, signed := ledger.Signoffs[shortRepo(repo)]
	if !signed {
		fmt.Printf("%s no CTO sign-off for %s — run: lw release sign-off %s --by <cto>\n",
			color.RedString("✗"), repo, shortRepo(repo))

		return nil // gate closed is a normal outcome, not a tool error
	}

	candidates, err := fetchReleaseCandidates(ctx, repo)
	if err != nil {
		return err
	}

	eligible := make([]prCandidate, 0, len(candidates))

	for _, c := range candidates {
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

	fmt.Printf("%s %s %d PR(s) for %s (signed off by %s)\n",
		color.CyanString("●"), verb, len(eligible), repo, sign.ApprovedBy)

	return nil
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
