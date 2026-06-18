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
)

func init() {
	RegisterHandler("docs.render", docsRenderHandler)
	RegisterHandler("docs.serve", docsServeHandler)
	RegisterHandler("docs.check.strict", docsCheckStrictHandler)
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
