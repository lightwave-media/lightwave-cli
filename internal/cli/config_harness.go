package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const harnessBlueprintName = "agent-harness-config"

var (
	harnessOutputFolder string
	harnessHomeDir      string
	harnessOwner        string
	harnessAWSProfile   string
	harnessNoHooks      bool
	harnessRoot         string
	harnessDryRun       bool
	harnessRenderDryRun bool
	harnessCodexConfig  string
)

var configHarnessCmd = &cobra.Command{
	Use:   "harness",
	Short: "Render, validate, and apply local agent harness configuration",
	Long: `Render, validate, and apply local agent harness configuration.

The stamp lives in lightwave-core's agent-harness-config blueprint. The
rendered print lives under ~/.lightwave/config/agent-harnesses. App-owned
configs such as ~/.codex/config.toml are updated only by explicit apply
commands.`,
}

var configHarnessRenderCmd = &cobra.Command{
	Use:   "render",
	Short: "Render ~/.lightwave/config/agent-harnesses from the core blueprint",
	RunE: func(cmd *cobra.Command, args []string) error {
		out := harnessOutputFolder
		if out == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}

			out = filepath.Join(home, ".lightwave")
		}

		homeDir, err := defaultHomeDir(harnessHomeDir)
		if err != nil {
			return err
		}

		cfg := config.Get()
		if cfg == nil {
			return errors.New("config not loaded")
		}

		blueprintsDir := blueprint.BlueprintsDir(cfg.Paths.LightwaveRoot)

		path, err := blueprint.Resolve(blueprintsDir, harnessBlueprintName)
		if err != nil {
			return err
		}

		vars := []string{
			"home_dir=" + homeDir,
			"owner=" + harnessOwner,
			"aws_profile=" + harnessAWSProfile,
		}

		if err := blueprint.Render(cmd.Context(), &blueprint.RenderOptions{
			BlueprintPath: path,
			OutputFolder:  out,
			Vars:          vars,
			NoHooks:       harnessNoHooks,
			DryRun:        harnessRenderDryRun,
		}); err != nil {
			return err
		}

		if harnessRenderDryRun {
			return nil
		}

		fmt.Printf("rendered agent harness config to %s\n", filepath.Join(out, "config", "agent-harnesses"))

		return nil
	},
}

var configHarnessApplyCmd = &cobra.Command{
	Use:   "apply <harness>",
	Short: "Apply a rendered harness fragment to an app config",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "codex":
			return applyCodexHarness()
		default:
			return fmt.Errorf("unsupported harness %q (supported: codex)", args[0])
		}
	},
}

var configHarnessValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate rendered agent harness fragments",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := defaultHarnessRoot(harnessRoot)
		if err != nil {
			return err
		}

		if err := validateHarnessPrint(root); err != nil {
			return err
		}

		fmt.Printf("agent harness config valid: %s\n", root)

		return nil
	},
}

func init() {
	configHarnessRenderCmd.Flags().StringVarP(&harnessOutputFolder, "output-folder", "o", "", "output root (default: ~/.lightwave)")
	configHarnessRenderCmd.Flags().StringVar(&harnessHomeDir, "home-dir", "", "home directory used in rendered fragments (default: current user home)")
	configHarnessRenderCmd.Flags().StringVar(&harnessOwner, "owner", "Joel", "human owner label for the harness print")
	configHarnessRenderCmd.Flags().StringVar(&harnessAWSProfile, "aws-profile", "lightwave-admin", "AWS profile for harness shell environments")
	configHarnessRenderCmd.Flags().BoolVar(&harnessRenderDryRun, "dry-run", false,
		"preview the files that would be written; write nothing")
	configHarnessRenderCmd.Flags().BoolVar(&harnessNoHooks, "no-hooks", false, "skip blueprint hooks")

	configHarnessApplyCmd.Flags().StringVar(&harnessRoot, "root", "", "rendered harness root (default: ~/.lightwave/config/agent-harnesses)")
	configHarnessApplyCmd.Flags().StringVar(&harnessCodexConfig, "codex-config", "", "Codex config path (default: ~/.codex/config.toml)")
	configHarnessApplyCmd.Flags().BoolVar(&harnessDryRun, "dry-run", false, "preview changes without writing")

	configHarnessValidateCmd.Flags().StringVar(&harnessRoot, "root", "", "rendered harness root (default: ~/.lightwave/config/agent-harnesses)")

	configHarnessCmd.AddCommand(configHarnessRenderCmd)
	configHarnessCmd.AddCommand(configHarnessApplyCmd)
	configHarnessCmd.AddCommand(configHarnessValidateCmd)
	configCmd.AddCommand(configHarnessCmd)
}

func defaultHomeDir(value string) (string, error) {
	if value != "" {
		return filepath.Abs(value)
	}

	return os.UserHomeDir()
}

func defaultHarnessRoot(value string) (string, error) {
	if value != "" {
		return filepath.Abs(value)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".lightwave", "config", "agent-harnesses"), nil
}

func defaultCodexConfig(value string) (string, error) {
	if value != "" {
		return filepath.Abs(value)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".codex", "config.toml"), nil
}

func applyCodexHarness() error {
	root, err := defaultHarnessRoot(harnessRoot)
	if err != nil {
		return err
	}

	configPath, err := defaultCodexConfig(harnessCodexConfig)
	if err != nil {
		return err
	}

	fragmentPath := filepath.Join(root, "codex", "config.toml.fragment")

	fragment, err := os.ReadFile(fragmentPath)
	if err != nil {
		return fmt.Errorf("read Codex fragment: %w", err)
	}

	settings, err := codexFragmentSettings(fragment)
	if err != nil {
		return err
	}

	current, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read Codex config: %w", err)
	}

	next := ensureCodexShellEnvironment(string(current), settings)

	if harnessDryRun {
		fmt.Printf("Codex config: %s\n", configPath)
		fmt.Printf("Fragment:     %s\n\n", fragmentPath)
		fmt.Println("Would ensure [shell_environment_policy.set] contains:")
		printSettings(settings)

		if string(current) == next {
			fmt.Println("\nNo changes needed.")
		}

		return nil
	}

	info, err := os.Stat(configPath)
	if err != nil {
		return fmt.Errorf("stat Codex config: %w", err)
	}

	if err := os.WriteFile(configPath, []byte(next), info.Mode().Perm()); err != nil {
		return fmt.Errorf("write Codex config: %w", err)
	}

	fmt.Printf("applied Codex harness config to %s\n", configPath)

	return nil
}

func validateHarnessPrint(root string) error {
	required := []string{
		"README.md",
		"harnesses.yaml",
		filepath.Join("codex", "config.toml.fragment"),
		filepath.Join("claude", "settings.fragment.json"),
		filepath.Join("pi", "settings.fragment.json"),
		filepath.Join("lightwave", "runtime.yaml"),
	}
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			return fmt.Errorf("missing %s: %w", rel, err)
		}
	}

	var doc any

	for _, rel := range []string{"harnesses.yaml", filepath.Join("lightwave", "runtime.yaml")} {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return err
		}

		if err := yaml.Unmarshal(body, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
	}

	for _, rel := range []string{filepath.Join("claude", "settings.fragment.json"), filepath.Join("pi", "settings.fragment.json")} {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return err
		}

		var parsed any

		if err := json.Unmarshal(body, &parsed); err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
	}

	body, err := os.ReadFile(filepath.Join(root, "codex", "config.toml.fragment"))
	if err != nil {
		return err
	}

	if _, err := codexFragmentSettings(body); err != nil {
		return err
	}

	return nil
}

type codexFragment struct {
	ShellEnvironmentPolicy struct {
		Set map[string]string `toml:"set"`
	} `toml:"shell_environment_policy"`
}

func codexFragmentSettings(body []byte) (map[string]string, error) {
	var parsed codexFragment
	if err := toml.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse Codex fragment TOML: %w", err)
	}

	settings := parsed.ShellEnvironmentPolicy.Set
	if len(settings) == 0 {
		return nil, errors.New("codex fragment missing [shell_environment_policy.set]")
	}

	return settings, nil
}

func ensureCodexShellEnvironment(current string, settings map[string]string) string {
	const header = "[shell_environment_policy.set]"

	lines := strings.Split(strings.TrimRight(current, "\n"), "\n")
	if current == "" {
		lines = nil
	}

	start := -1

	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			start = i
			break
		}
	}

	if start == -1 {
		out := append([]string{}, lines...)
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}

		out = append(out, header)

		for _, key := range sortedKeys(settings) {
			out = append(out, tomlAssignment(key, settings[key]))
		}

		return strings.Join(out, "\n") + "\n"
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			end = i
			break
		}
	}

	seen := map[string]bool{}
	out := make([]string, 0, len(lines)+len(settings))

	out = append(out, lines[:start+1]...)

	for _, line := range lines[start+1 : end] {
		key, ok := tomlKey(line)
		if ok {
			if value, wanted := settings[key]; wanted {
				out = append(out, tomlAssignment(key, value))
				seen[key] = true

				continue
			}
		}

		out = append(out, line)
	}

	insertAt := len(out)
	for insertAt > start+1 && strings.TrimSpace(out[insertAt-1]) == "" {
		insertAt--
	}

	missing := make([]string, 0)

	for _, key := range sortedKeys(settings) {
		if !seen[key] {
			missing = append(missing, tomlAssignment(key, settings[key]))
		}
	}

	if len(missing) > 0 {
		out = append(out[:insertAt], append(missing, out[insertAt:]...)...)
	}

	out = append(out, lines[end:]...)

	return strings.Join(out, "\n") + "\n"
}

func tomlKey(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}

	before, _, ok := strings.Cut(trimmed, "=")
	if !ok {
		return "", false
	}

	key := strings.TrimSpace(before)
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", false
	}

	return key, true
}

func tomlAssignment(key, value string) string {
	return key + " = " + strconv.Quote(value)
}

func sortedKeys(settings map[string]string) []string {
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}

func printSettings(settings map[string]string) {
	for _, key := range sortedKeys(settings) {
		fmt.Println(tomlAssignment(key, settings[key]))
	}
}
