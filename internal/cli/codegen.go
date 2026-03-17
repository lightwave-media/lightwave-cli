package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	codegenDryRun bool
	codegenForce  bool
)

var codegenCmd = &cobra.Command{
	Use:   "codegen",
	Short: "Generate code from lightwave-core YAML schemas",
	Long: `Generate code artifacts from lightwave-core YAML schemas using boilerplate templates.

Supported generators:
  journeys    Generate Playwright E2E tests from journey YAML specs
  models      Generate Django models from data model YAML specs (planned)
  api         Generate Ninja API endpoints from route YAML specs (planned)
  types       Generate TypeScript types from schema YAML specs (planned)

Examples:
  lw codegen journeys                    # Generate all journey tests
  lw codegen journeys auth               # Generate auth journey tests
  lw codegen journeys auth/login         # Generate single journey test
  lw codegen journeys --dry-run          # Preview without writing files`,
}

var codegenJourneysCmd = &cobra.Command{
	Use:   "journeys [category[/name]]",
	Short: "Generate Playwright E2E tests from journey YAML specs",
	Long: `Reads journey YAML files from lightwave-core and generates Playwright E2E test files.

Each journey YAML contains step-by-step flows with assertions that map directly
to Playwright test expectations. Generated files are placed in tests/e2e/tests/
with a .generated.spec.ts suffix to distinguish from hand-written tests.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runCodegenJourneys,
}

func init() {
	codegenCmd.PersistentFlags().BoolVar(&codegenDryRun, "dry-run", false, "preview changes without writing files")
	codegenCmd.PersistentFlags().BoolVarP(&codegenForce, "force", "f", false, "overwrite existing generated files")

	codegenCmd.AddCommand(codegenJourneysCmd)
}

// JourneyMeta holds the _meta section of a journey YAML
type JourneyMeta struct {
	Version     string `yaml:"version"`
	Description string `yaml:"description"`
	Domain      string `yaml:"domain"`
	FlowID      string `yaml:"flow_id"`
}

// JourneyAssertion holds a single assertion
type JourneyAssertion struct {
	StatusCode          int    `yaml:"status_code,omitempty"`
	ElementPresent      string `yaml:"element_present,omitempty"`
	Field               string `yaml:"field,omitempty"`
	Expected            any    `yaml:"expected,omitempty"`
	RedirectTo          string `yaml:"redirect_to,omitempty"`
	UserIsAuth          any    `yaml:"user_is_authenticated,omitempty"`
	TemplateUsed        string `yaml:"template_used,omitempty"`
	SessionTokenCleared any    `yaml:"session_token_cleared,omitempty"`
	EmailSent           string `yaml:"email_sent,omitempty"`
}

// JourneyStep holds a single step in a journey
type JourneyStep struct {
	Step       int                `yaml:"step"`
	Name       string             `yaml:"name"`
	Endpoint   string             `yaml:"endpoint,omitempty"`
	URL        string             `yaml:"url"`
	Method     string             `yaml:"method"`
	UserType   string             `yaml:"user_type,omitempty"`
	Layout     string             `yaml:"layout,omitempty"`
	FormData   map[string]string  `yaml:"form_data,omitempty"`
	Headers    map[string]string  `yaml:"headers,omitempty"`
	Assertions []JourneyAssertion `yaml:"assertions"`
}

// JourneyFlow holds the flow section
type JourneyFlow struct {
	Name        string        `yaml:"name"`
	Description string        `yaml:"description"`
	Category    string        `yaml:"category"`
	Priority    string        `yaml:"priority"`
	Steps       []JourneyStep `yaml:"steps"`
}

// JourneySpec is the full journey YAML
type JourneySpec struct {
	Meta JourneyMeta `yaml:"_meta"`
	Flow JourneyFlow `yaml:"flow"`
}

func runCodegenJourneys(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	root := cfg.Paths.LightwaveRoot
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "dev", "lightwave-media")
	}

	journeysDir := filepath.Join(root, "packages", "lightwave-core", "lightwave",
		"schema", "definitions", "flows", "journeys")
	outputDir := filepath.Join(root, "tests", "e2e", "tests")

	// Determine filter
	filter := ""
	if len(args) > 0 {
		filter = args[0]
	}

	// Find journey YAML files
	yamlFiles, err := findJourneyYAMLs(journeysDir, filter)
	if err != nil {
		return fmt.Errorf("finding journey YAMLs: %w", err)
	}

	if len(yamlFiles) == 0 {
		fmt.Println(color.YellowString("No journey YAML files found matching filter: %s", filter))
		return nil
	}

	fmt.Printf("Found %s journey YAML files\n", color.CyanString("%d", len(yamlFiles)))

	generated := 0
	skipped := 0
	errors := 0

	for _, yamlFile := range yamlFiles {
		// Parse relative path to get category/name
		relPath, _ := filepath.Rel(journeysDir, yamlFile)
		parts := strings.Split(relPath, string(filepath.Separator))
		if len(parts) < 2 {
			continue
		}

		category := parts[0]
		name := strings.TrimSuffix(parts[len(parts)-1], ".yaml")
		outFile := filepath.Join(outputDir, category, name+".generated.spec.ts")

		// Check if output exists and skip unless forced
		if !codegenForce {
			if _, err := os.Stat(outFile); err == nil {
				skipped++
				if verbose {
					fmt.Printf("  %s %s/%s (exists, use --force to overwrite)\n",
						color.YellowString("SKIP"), category, name)
				}
				continue
			}
		}

		// Parse the journey YAML
		spec, err := parseJourneyYAML(yamlFile)
		if err != nil {
			fmt.Printf("  %s %s/%s: %v\n", color.RedString("ERROR"), category, name, err)
			errors++
			continue
		}

		// Generate the Playwright test
		testCode := generatePlaywrightTest(spec, category, name)

		if codegenDryRun {
			fmt.Printf("  %s %s/%s → %s\n",
				color.CyanString("DRY"), category, name, outFile)
			generated++
			continue
		}

		// Ensure output directory exists
		if err := os.MkdirAll(filepath.Dir(outFile), 0755); err != nil {
			fmt.Printf("  %s mkdir %s: %v\n", color.RedString("ERROR"), filepath.Dir(outFile), err)
			errors++
			continue
		}

		// Write the test file
		if err := os.WriteFile(outFile, []byte(testCode), 0644); err != nil {
			fmt.Printf("  %s write %s: %v\n", color.RedString("ERROR"), outFile, err)
			errors++
			continue
		}

		fmt.Printf("  %s %s/%s → %s\n",
			color.GreenString("GEN"), category, name, outFile)
		generated++
	}

	// Summary
	fmt.Println()
	fmt.Printf("Generated: %s  Skipped: %s  Errors: %s\n",
		color.GreenString("%d", generated),
		color.YellowString("%d", skipped),
		color.RedString("%d", errors))

	// Run prettier on generated files if not dry-run
	if !codegenDryRun && generated > 0 {
		prettierPath, err := exec.LookPath("npx")
		if err == nil {
			fmt.Print("Running prettier on generated files... ")
			prettierCmd := exec.Command(prettierPath, "prettier", "--write",
				filepath.Join(outputDir, "**", "*.generated.spec.ts"))
			prettierCmd.Dir = root
			if err := prettierCmd.Run(); err == nil {
				fmt.Println(color.GreenString("done"))
			} else {
				fmt.Println(color.YellowString("skipped (prettier failed)"))
			}
		}
	}

	return nil
}

func findJourneyYAMLs(dir string, filter string) ([]string, error) {
	var files []string

	if filter != "" {
		// Check if filter is category/name or just category
		parts := strings.SplitN(filter, "/", 2)
		if len(parts) == 2 {
			// Specific journey
			target := filepath.Join(dir, parts[0], parts[1]+".yaml")
			if _, err := os.Stat(target); err == nil {
				return []string{target}, nil
			}
			return nil, fmt.Errorf("journey not found: %s", target)
		}
		// Category filter
		dir = filepath.Join(dir, parts[0])
	}

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if !info.IsDir() && strings.HasSuffix(path, ".yaml") {
			files = append(files, path)
		}
		return nil
	})

	return files, err
}

func parseJourneyYAML(path string) (*JourneySpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var spec JourneySpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, err
	}

	return &spec, nil
}

func generatePlaywrightTest(spec *JourneySpec, category, name string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf(`/**
 * AUTO-GENERATED from lightwave-core journey YAML
 * Source: flows/journeys/%s/%s.yaml
 * Version: %s
 *
 * DO NOT EDIT — regenerate with: lw codegen journeys %s/%s
 */
import { test, expect } from "@playwright/test";

const TENANT = process.env.TENANT ?? "lightwave-media";
const API_BASE = process.env.API_BASE ?? "https://local.lightwave-media.site";
const APP_DOMAIN =
  process.env.APP_DOMAIN ?? "https://app.local.lightwave-media.site";

`, category, name, spec.Meta.Version, category, name))

	sb.WriteString(fmt.Sprintf(`test.describe("%s", () => {
`, spec.Flow.Name))

	for _, step := range spec.Flow.Steps {
		sb.WriteString(fmt.Sprintf(`  test("Step %d: %s", async ({ page, request }) => {
`, step.Step, step.Name))

		// Resolve URL template variables
		url := resolveURLTemplate(step.URL)

		switch step.Method {
		case "GET":
			if step.Endpoint != "" {
				// API call
				sb.WriteString(fmt.Sprintf("    const response = await request.get(`%s`);\n", url))
			} else {
				// Page navigation
				sb.WriteString(fmt.Sprintf("    await page.goto(\"%s\");\n", step.URL))
			}
		case "POST":
			sb.WriteString(fmt.Sprintf("    const response = await request.post(`%s`, {\n", url))
			sb.WriteString("      data: {\n")
			for k, v := range step.FormData {
				sb.WriteString(fmt.Sprintf("        %s: \"%s\",\n", k, v))
			}
			sb.WriteString("      },\n")
			sb.WriteString("    });\n")
		case "DELETE":
			sb.WriteString(fmt.Sprintf("    const response = await request.delete(`%s`);\n", url))
		}

		// Check if any assertion needs the response body
		needsBody := false
		for _, a := range step.Assertions {
			if a.Field != "" || a.UserIsAuth != nil {
				needsBody = true
				break
			}
		}

		// Parse body once if needed
		if needsBody {
			sb.WriteString("    const body = await response.json();\n")
		}

		// Generate assertions
		for _, a := range step.Assertions {
			if a.StatusCode > 0 {
				sb.WriteString(fmt.Sprintf("    expect(response.status()).toBe(%d);\n", a.StatusCode))
			}
			if a.ElementPresent != "" {
				sb.WriteString(fmt.Sprintf("    await expect(page.locator('%s')).toBeVisible();\n", a.ElementPresent))
			}
			if a.Field != "" {
				fieldPath := strings.TrimPrefix(a.Field, "response.")
				expected := fmt.Sprintf("%v", a.Expected)
				switch expected {
				case "present":
					sb.WriteString(fmt.Sprintf("    expect(body).toHaveProperty(\"%s\");\n", fieldPath))
				case "true":
					sb.WriteString(fmt.Sprintf("    expect(body.%s).toBe(true);\n", fieldPath))
				case "false":
					sb.WriteString(fmt.Sprintf("    expect(body.%s).toBe(false);\n", fieldPath))
				default:
					sb.WriteString(fmt.Sprintf("    expect(body.%s).toBe(\"%s\");\n", fieldPath, expected))
				}
			}
			if a.RedirectTo != "" {
				sb.WriteString(fmt.Sprintf("    expect(response.headers()[\"location\"]).toContain(\"%s\");\n", a.RedirectTo))
			}
			if a.UserIsAuth != nil {
				sb.WriteString(fmt.Sprintf("    expect(body.is_authenticated).toBe(%v);\n", a.UserIsAuth))
			}
		}

		sb.WriteString("  });\n\n")
	}

	sb.WriteString("});\n")

	return sb.String()
}

func resolveURLTemplate(url string) string {
	// Replace YAML template vars with JS template literals
	url = strings.ReplaceAll(url, "{{ tenant }}", "${TENANT}")
	url = strings.ReplaceAll(url, "{{ api_base }}", "${API_BASE}")
	url = strings.ReplaceAll(url, "{{ domain }}", "${API_BASE}")
	url = strings.ReplaceAll(url, "{{ app_domain }}", "${APP_DOMAIN}")
	return url
}
