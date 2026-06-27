package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
)

// create_scaffold_handlers wires the project-bootstrap half of the `create`
// domain: `lw create {website,webapp,desktop-app}`. The schema entries live in
// lightwave-core commands.yaml; this registers their handlers (create.repo is
// handled separately in create_repo_handlers.go).
//
// Each handler is the S1 "wizard engine" (lightwave-cli#97): collect the
// required input (positional arg, or an interactive prompt when absent),
// preview under --dry-run, then render the variant's blueprint via the
// Gruntwork engine. The Cloudflare-DNS / SSM seed steps and the richer
// template sets are the S2/S3 scaffold runners (lightwave-cli#98, #99) and are
// intentionally NOT done here.
//
// Note: the three variants' schema flag sets in commands.yaml are not yet
// uniform (website has --output/--domain but not --yes/--blueprint/--json;
// webapp/desktop-app the reverse). The shared handler reads the union; reads of
// a flag a variant doesn't declare return "" and fall through to defaults. A
// lightwave-core issue tracks reconciling the flag surface.

// createVariant describes one `lw create <kind>` subcommand: the blueprint it
// renders, the role of its positional arg, and how the blueprint's boilerplate
// variables are derived from the CLI inputs.
type createVariant struct {
	buildVars  func(primary, domain string) []string
	kind       string // schema key suffix: website | webapp | desktop-app
	blueprint  string // default blueprint slug (overridable via --blueprint)
	primaryArg string // role of the positional arg: "project-name" or "domain"
}

func init() {
	for _, v := range createVariants() {
		RegisterHandler("create."+v.kind, makeCreateHandler(v))
	}
}

// createVariants returns the three project-bootstrap variants and their
// blueprint variable mappings (verified against each blueprint's
// boilerplate.yml required-variable set).
func createVariants() []createVariant {
	return []createVariant{
		{
			kind:       "website",
			blueprint:  "website",
			primaryArg: "project-name",
			buildVars: func(primary, domain string) []string {
				vars := []string{"project_name=" + primary}
				if domain != "" {
					vars = append(vars, "domain="+domain)
				}

				return vars
			},
		},
		{
			kind:       "webapp",
			blueprint:  "webapp-v1",
			primaryArg: "domain",
			buildVars: func(primary, _ string) []string {
				return []string{"domain=" + primary, "today=" + today()}
			},
		},
		{
			kind:       "desktop-app",
			blueprint:  "desktop-app-v1",
			primaryArg: "domain",
			// desktop-app-v1 requires tenet/app_name/bundle_id with no defaults
			// and the schema exposes no flags for them, so derive them from the
			// domain. The S3 runner (lightwave-cli#99) may add explicit flags.
			buildVars: func(primary, _ string) []string {
				tenet := deriveTenet(primary)

				return []string{
					"domain=" + primary,
					"tenet=" + tenet,
					"app_name=" + titleFirst(tenet),
					"bundle_id=" + reverseDomain(primary) + ".app",
					"today=" + today(),
				}
			},
		},
	}
}

func makeCreateHandler(v createVariant) func(context.Context, []string, map[string]any) error {
	return func(ctx context.Context, args []string, flags map[string]any) error {
		start := time.Now()
		verb := "create." + v.kind

		primary := ""
		if len(args) > 0 {
			primary = strings.TrimSpace(args[0])
		}

		if primary == "" {
			primary = promptLine(fmt.Sprintf("%s for the new %s: ", v.primaryArg, v.kind))
		}

		if primary == "" {
			return fmt.Errorf("usage: lw create %s <%s>", v.kind, v.primaryArg)
		}

		slug := v.blueprint
		if override := flagStr(flags, "blueprint"); override != "" {
			slug = override
		}

		out := flagStr(flags, "output")
		if out == "" {
			home, _ := os.UserHomeDir()
			out = filepath.Join(home, "dev", primary)
		}

		vars := v.buildVars(primary, flagStr(flags, "domain"))

		if flagBool(flags, "dry-run") {
			fmt.Printf("would render blueprint %q → %s\n  vars: %s\n", slug, out, strings.Join(vars, " "))
			emitOperatorCLI(verb, "dry-run", slug, 0, start, nil)

			return nil
		}

		// No confirm prompt: scaffolding is additive (blueprint.Render refuses to
		// overwrite existing files), matching create.repo. --dry-run is the
		// preview path. This also keeps the website variant — whose schema omits
		// --yes — usable non-interactively.
		if err := renderCreate(ctx, slug, out, vars); err != nil {
			emitOperatorCLI(verb, "error", err.Error(), 1, start, nil)
			return err
		}

		fmt.Printf("created %s at %s (blueprint %q)\n", v.kind, out, slug)
		emitOperatorCLI(verb, "ok", out, 0, start, map[string]any{"blueprint": slug})

		return nil
	}
}

// renderCreate resolves the blueprint and renders it into out with vars.
func renderCreate(ctx context.Context, slug, out string, vars []string) error {
	bpPath, err := blueprint.Resolve(blueprint.BlueprintsDir(lightwaveRoot()), slug)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(out, codegenDirPerm); err != nil {
		return err
	}

	return blueprint.Render(ctx, &blueprint.RenderOptions{
		BlueprintPath: bpPath,
		OutputFolder:  out,
		Vars:          vars,
	})
}

// promptLine reads one trimmed line from stdin, returning "" on EOF/error so a
// non-interactive caller (CI, tests) falls through to a usage error rather than
// hanging.
func promptLine(question string) string {
	fmt.Print(question)

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return ""
	}

	return strings.TrimSpace(line)
}

func today() string {
	return time.Now().Format("2006-01-02")
}

// deriveTenet takes the first DNS label of a domain as the tenet slug
// (createos.io → createos, app.foo.com → app).
func deriveTenet(domain string) string {
	label, _, _ := strings.Cut(domain, ".")
	if label == "" {
		return domain
	}

	return label
}

// reverseDomain reverses the dot-separated labels of a domain
// (createos.io → io.createos), the stem of a reverse-DNS bundle id.
func reverseDomain(domain string) string {
	parts := strings.Split(domain, ".")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}

	return strings.Join(parts, ".")
}

// titleFirst upper-cases the first rune (createos → Createos).
func titleFirst(s string) string {
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}
