package cli

// check_stamp.go — lw check stamp
//
// linked-incident: failures/stamp-contract-version-drift.yaml
// linked-incident (release train): lightwave-media/lightwave-core#552
//
// Anti-pattern caught:
//
//	BEFORE: internal/cli/git_handlers.go encoded worktree_policy semantics in Go
//	        constants under a comment reading "worktree_policy.yaml v1.1.0".
//	        lightwave-core moved that contract to v2.0.0 and inverted two of its
//	        rules — .claude/worktrees stopped being forbidden and became
//	        supplemental, while the old <repo>/.worktrees became forbidden. A
//	        comment is not an assertion, so nothing failed. `lw git audit` then
//	        reported the stamp backwards across 48 checkouts: 14
//	        forbidden_worktree_root, 4 naming_violation and 2
//	        legacy_worktree_layout findings were all false, and the one tree
//	        already in the canonical location was reported as legacy.
//	AFTER:  the version each contract declares is compared against the version
//	        this binary claims to implement, and a mismatch fails CI naming the
//	        contract and both versions.
//
// The comparison reads the EMBEDDED mirror rather than a lightwave-core
// checkout, so it runs in public CI with no private-repo token — the same
// reason the mirror is vendored at all (internal/corestamp/loader.go).
//
// Two of the three assertions here are not new. VerifyVersionMatchesSourceTag
// and VerifyEmbeddedDigest were written, tested, and never called from
// production code. Wiring them IS the fix for that half: a detector nothing
// calls catches nothing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/fatih/color"

	"github.com/lightwave-media/lightwave-cli/internal/corestamp"
)

func init() {
	RegisterHandler("check.stamp", checkStampHandler)
}

// implementedContractVersions pins every stamped contract whose SEMANTICS this
// binary hard-codes in Go. Listing a contract here is a claim that the Go code
// implements that exact version, so only add an entry after reading the
// contract — a wrong pin is worse than no pin, because it reports agreement
// that was never checked.
//
// Keys are registry keys: the path under src/schemas without the .yaml suffix.
//
// These two are the contracts lightwave-cli#380 reimplemented. Widening the
// table means auditing each addition the same way.
var implementedContractVersions = map[string]string{
	"policy/security/worktree_policy": "2.0.0",
	"data/meta/worktree_home_policy":  "2.0.0",
}

type stampFinding struct {
	Code     string `json:"code"`
	Contract string `json:"contract,omitempty"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Message  string `json:"message"`
}

// Field order is govet fieldalignment's, not reading order: the two strings and
// the slice carry the pointer words, so grouping them ahead of the ints keeps
// the GC scan at 40 bytes instead of 48.
type stampReport struct {
	Version       string         `json:"version"`
	SourceTag     string         `json:"source_tag"`
	Findings      []stampFinding `json:"findings"`
	ContractsPin  int            `json:"contracts_pinned"`
	FindingsCount int            `json:"findings_count"`
}

func checkStampHandler(_ context.Context, _ []string, flags map[string]any) error {
	report := stampReport{
		Version:      corestamp.Version,
		SourceTag:    corestamp.SourceTag,
		ContractsPin: len(implementedContractVersions),
	}

	// Every assertion runs. Returning on the first would hide a contract
	// mismatch behind a stale digest, and seeing all of it is the point.
	if err := corestamp.VerifyVersionMatchesSourceTag(); err != nil {
		report.Findings = append(report.Findings, stampFinding{
			Code:    "version_tag_mismatch",
			Message: err.Error(),
		})
	}

	if err := corestamp.VerifyEmbeddedDigest(); err != nil {
		report.Findings = append(report.Findings, stampFinding{
			Code:    "digest_mismatch",
			Message: err.Error(),
		})
	}

	report.Findings = append(report.Findings, contractVersionFindings(implementedContractVersions)...)
	report.FindingsCount = len(report.Findings)

	if asJSON(flags) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("encode stamp report: %w", err)
		}

		return stampResultError(report.FindingsCount)
	}

	printStampReport(&report)

	return stampResultError(report.FindingsCount)
}

// contractVersionFindings compares each pinned contract against the version the
// embedded contract declares. Keys are sorted so output and tests are stable.
func contractVersionFindings(pins map[string]string) []stampFinding {
	keys := make([]string, 0, len(pins))
	for key := range pins {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	var findings []stampFinding

	for _, key := range keys {
		declared, err := declaredContractVersion(key)
		if err != nil {
			findings = append(findings, stampFinding{
				Code:     "contract_unreadable",
				Contract: key,
				Expected: pins[key],
				Message: fmt.Sprintf("%s: pinned at %s but the embedded contract could not be read: %v",
					key, pins[key], err),
			})

			continue
		}

		if declared == pins[key] {
			continue
		}

		findings = append(findings, stampFinding{
			Code:     "contract_version_drift",
			Contract: key,
			Expected: pins[key],
			Actual:   declared,
			Message: fmt.Sprintf("%s: this binary implements %s but the embedded contract declares %s — "+
				"update the Go code to the new contract, then move the pin; "+
				"if only the mirror is behind, run scripts/sync-core-stamp.sh",
				key, pins[key], declared),
		})
	}

	return findings
}

// declaredContractVersion reads _meta.version from an embedded schema. A
// contract with no version cannot be compared, and saying so beats treating a
// missing field as agreement.
func declaredContractVersion(key string) (string, error) {
	doc, err := corestamp.LoadSchema(key)
	if err != nil {
		return "", err
	}

	meta, ok := doc["_meta"].(map[string]any)
	if !ok {
		return "", errors.New("no _meta mapping")
	}

	version, ok := meta["version"].(string)
	if !ok || version == "" {
		return "", errors.New("_meta.version is missing or not a string")
	}

	return version, nil
}

func printStampReport(report *stampReport) {
	fmt.Printf("● stamp %s (source %s) — %d contract(s) pinned\n",
		report.Version, report.SourceTag, report.ContractsPin)

	if len(report.Findings) == 0 {
		fmt.Println(color.GreenString("✓ embedded stamp is coherent: version, digest and every pinned contract agree"))

		return
	}

	for _, f := range report.Findings {
		fmt.Printf("  %s [%s] %s\n", color.RedString("FAIL"), f.Code, f.Message)
	}
}

// stampResultError maps findings to the exit convention in AGENTS.md: 0 clean,
// non-zero when violations exist. A tool error surfaces from the handler's own
// error returns, not from here.
func stampResultError(findings int) error {
	if findings == 0 {
		return nil
	}

	return fmt.Errorf("stamp drift: %d finding(s) — see report above", findings)
}
