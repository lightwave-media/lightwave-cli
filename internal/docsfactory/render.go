package docsfactory

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

// RenderOptions controls `lw docs render`.
type RenderOptions struct {
	DryRun bool
}

// RenderResult summarizes site outputs written.
type RenderResult struct {
	Written []string
	Skipped []string
}

// RenderSite derives docs/site/*.html from canonical docs/ markdown kinds.
func RenderSite(repoRoot string, schemas *Schemas, opts RenderOptions) (*RenderResult, error) {
	docsDir := filepath.Join(repoRoot, "docs")
	siteDir := filepath.Join(repoRoot, "docs", "site")
	if _, err := os.Stat(docsDir); err != nil {
		return nil, fmt.Errorf("stat %s: %w", docsDir, err)
	}
	if !opts.DryRun {
		if err := os.MkdirAll(siteDir, 0o755); err != nil {
			return nil, err
		}
	}

	result := &RenderResult{}
	err := filepath.WalkDir(docsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path == siteDir {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".md" && ext != ".mdx" {
			return nil
		}
		rel, _ := filepath.Rel(docsDir, path)
		if strings.HasPrefix(rel, "site/") {
			return nil
		}
		kind := KindFromDocsPath(path, docsDir, schemas)
		if kind == "" {
			return nil
		}
		outName := strings.TrimSuffix(rel, ext) + ".html"
		outPath := filepath.Join(siteDir, outName)
		if opts.DryRun {
			result.Written = append(result.Written, filepath.Join("docs/site", outName))
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		htmlBody := markdownToHTML(stripFrontmatter(body))
		page := wrapHTMLPage(kind, htmlBody)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(outPath, []byte(page), 0o644); err != nil {
			return err
		}
		result.Written = append(result.Written, filepath.Join("docs/site", outName))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func stripFrontmatter(data []byte) []byte {
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return data
	}
	if idx := strings.Index(s[4:], "\n---\n"); idx >= 0 {
		return []byte(s[4+idx+5:])
	}
	return data
}

func markdownToHTML(md []byte) string {
	var b strings.Builder
	for _, line := range strings.Split(string(md), "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "# "):
			b.WriteString("<h1>" + html.EscapeString(strings.TrimPrefix(trim, "# ")) + "</h1>\n")
		case strings.HasPrefix(trim, "## "):
			b.WriteString("<h2>" + html.EscapeString(strings.TrimPrefix(trim, "## ")) + "</h2>\n")
		case trim == "":
			b.WriteString("\n")
		default:
			b.WriteString("<p>" + html.EscapeString(trim) + "</p>\n")
		}
	}
	return b.String()
}

func wrapHTMLPage(title, body string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>%s</title>
  <meta name="generator" content="lw docs render">
</head>
<body>
%s
</body>
</html>
`, html.EscapeString(title), body)
}
