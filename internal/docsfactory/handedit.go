package docsfactory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HandEditViolation names one generated doc that appears hand-edited.
type HandEditViolation struct {
	Path   string
	Kind   string
	Reason string
}

var handEditSentinels = []string{
	"<!-- HAND-EDITED -->",
	"# HAND-EDITED",
	"%% HAND-EDITED",
}

// CheckHandEdits scans generated kinds for forbidden hand-edit markers.
func CheckHandEdits(repoRoot string, schemas *Schemas) ([]HandEditViolation, error) {
	docsDir := filepath.Join(repoRoot, "docs")
	if _, err := os.Stat(docsDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []HandEditViolation
	err := filepath.WalkDir(docsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		if strings.HasPrefix(path, filepath.Join(docsDir, "site")) {
			return nil
		}
		kind := KindFromDocsPath(path, docsDir, schemas)
		if kind == "" {
			return nil
		}
		dk, ok := schemas.DocKindByName(kind)
		if !ok || !dk.Generated() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		for _, sentinel := range handEditSentinels {
			if strings.Contains(text, sentinel) {
				rel, _ := filepath.Rel(repoRoot, path)
				out = append(out, HandEditViolation{
					Path:   rel,
					Kind:   kind,
					Reason: "hand-edit sentinel present on generated kind",
				})
				break
			}
		}
		return nil
	})
	return out, err
}

// CheckRenderStale fails when docs/site is older than any generated canonical doc.
func CheckRenderStale(repoRoot string, schemas *Schemas) ([]string, error) {
	siteDir := filepath.Join(repoRoot, "docs", "site")
	if _, err := os.Stat(siteDir); os.IsNotExist(err) {
		return []string{"docs/site/ missing — run lw docs render"}, nil
	}
	docsDir := filepath.Join(repoRoot, "docs")
	var stale []string
	err := filepath.WalkDir(docsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		ext := filepath.Ext(path)
		if ext != ".md" && ext != ".mdx" {
			return nil
		}
		if strings.HasPrefix(path, siteDir) {
			return nil
		}
		kind := KindFromDocsPath(path, docsDir, schemas)
		if kind == "" {
			return nil
		}
		dk, ok := schemas.DocKindByName(kind)
		if !ok || !dk.Generated() {
			return nil
		}
		srcInfo, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(docsDir, path)
		outHTML := filepath.Join(siteDir, strings.TrimSuffix(rel, ext)+".html")
		outInfo, err := os.Stat(outHTML)
		if err != nil {
			stale = append(stale, fmt.Sprintf("%s → %s missing", rel, filepath.Join("docs/site", filepath.Base(outHTML))))
			return nil
		}
		if outInfo.ModTime().Before(srcInfo.ModTime()) {
			stale = append(stale, fmt.Sprintf("%s newer than rendered site output", rel))
		}
		return nil
	})
	return stale, err
}
