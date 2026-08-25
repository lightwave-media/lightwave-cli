package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/docsfactory"
	"github.com/lightwave-media/lightwave-cli/internal/docsgate"
	"github.com/lightwave-media/lightwave-cli/internal/version"
)

func init() {
	RegisterHandler("docs.render", docsRenderHandler)
	RegisterHandler("docs.serve", docsServeHandler)
	// `--strict` is a FLAG on `docs check` (commands.yaml docs.check), not a
	// subcommand — docsCheckStrictHandler reads flagBool("strict"). Registering
	// a "docs.check.strict" key too made an orphaned handler that the schema
	// has no entry for, which `lw check schema` reports as drift.
	RegisterHandler("docs.check", docsCheckStrictHandler)
	// docs.sync and docs.spec-lint have worked since they were written, but only
	// via the hardcoded cobra tree in docs.go. `lw check schema` reads the
	// handler registry, so it reported both as unimplemented — two false
	// positives in the drift report that would fail CI once the gate is re-armed
	// (lightwave-cli#301). Registering them makes the report match reality; the
	// cobra commands stay as the interactive entry points.
	RegisterHandler("docs.sync", docsSyncHandler)
	RegisterHandler("docs.spec-lint", docsSpecLintHandler)
}

// docsSyncHandler is the dispatcher entry point for `lw docs sync`. It mirrors
// docsSyncCmd's RunE, reading --repo/--dry-run from the flags map rather than
// the package-level cobra flag vars.
func docsSyncHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo := resolveDocsRepoFromFlags(flags)

	schemas, err := loadDocsSchemas()
	if err != nil {
		return toolError(err)
	}

	dryRun := flagBool(flags, "dry-run")

	res, err := docsfactory.SyncDocs(repo, schemas, docsfactory.SyncOptions{
		GeneratorVersion: version.Version,
		DryRun:           dryRun,
		RegenerateBodies: true,
	})
	if err != nil {
		return toolError(err)
	}

	return reportDocsSync(repo, res, dryRun)
}

// docsSpecLintHandler is the dispatcher entry point for `lw docs spec-lint`,
// mirroring docsSpecLintCmd's RunE.
func docsSpecLintHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo := resolveDocsRepoFromFlags(flags)

	schemas, err := loadDocsSchemas()
	if err != nil {
		return toolError(err)
	}

	res, err := docsfactory.LintSpec(repo, schemas)
	if err != nil {
		return toolError(err)
	}

	return reportSpecLint(repo, res)
}

func resolveDocsRepoFromFlags(flags map[string]any) string {
	if flags != nil {
		if r := flagStr(flags, "repo"); r != "" {
			abs, err := filepath.Abs(r)
			if err == nil {
				return abs
			}
			return r
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func docsRenderHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo := resolveDocsRepoFromFlags(flags)
	schemas, err := loadDocsSchemas()
	if err != nil {
		return toolError(err)
	}
	res, err := docsfactory.RenderSite(repo, schemas, docsfactory.RenderOptions{
		DryRun: flagBool(flags, "dry-run"),
	})
	if err != nil {
		return toolError(err)
	}
	fmt.Printf("%s wrote %d file(s)\n", color.CyanString("docs-render:"), len(res.Written))
	for _, p := range res.Written {
		fmt.Printf("  - %s\n", p)
	}
	return nil
}

func docsServeHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo := resolveDocsRepoFromFlags(flags)
	port := flagStr(flags, "port")
	if port == "" {
		port = "8765"
	}
	siteDir := filepath.Join(repo, "docs", "site")
	if _, err := os.Stat(siteDir); err != nil {
		return fmt.Errorf("docs serve: %s missing — run lw docs render", siteDir)
	}
	addr := ":" + port
	fmt.Printf("%s serving %s at http://127.0.0.1%s\n", color.CyanString("docs-serve:"), siteDir, addr)
	srv := &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: 5 * time.Second,
		Handler:           http.FileServer(http.Dir(siteDir)),
	}
	return srv.ListenAndServe()
}

func docsCheckStrictHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo := resolveDocsRepoFromFlags(flags)
	schemas, err := loadDocsSchemas()
	if err != nil {
		return toolError(err)
	}
	res, err := docsfactory.CheckDocs(repo, schemas)
	if err != nil {
		return toolError(err)
	}
	var hand []docsfactory.HandEditViolation
	if flagBool(flags, "hand-edit") || flagBool(flags, "strict") {
		hand, err = docsfactory.CheckHandEdits(repo, schemas)
		if err != nil {
			return toolError(err)
		}
	}
	stale, err := docsfactory.CheckRenderStale(repo, schemas)
	if err != nil {
		return toolError(err)
	}

	if res.Clean() && len(hand) == 0 && len(stale) == 0 {
		fmt.Println(color.GreenString("docs check --strict: ok"))
		return nil
	}
	if !res.Clean() {
		_ = reportDocsCheck(repo, res)
	}
	for _, v := range hand {
		fmt.Printf("  hand-edit: %s (%s) %s\n", v.Path, v.Kind, v.Reason)
	}
	for _, s := range stale {
		fmt.Printf("  render-stale: %s\n", s)
	}
	cure := "lw docs sync && lw docs render && git add docs/"
	path, _ := docsgate.Emit("docs_drift", "docs check --strict failed", cure)
	if path != "" {
		fmt.Printf("cure JSON: %s\n", path)
	}
	return fmt.Errorf("docs check --strict failed")
}
