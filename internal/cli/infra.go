package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/infra"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var (
	infraEnv         string
	infraRegion      string
	infraAutoApprove bool
)

var infraCmd = &cobra.Command{
	Use:   "infra",
	Short: "Infrastructure management (Terragrunt)",
	Long: `Manage infrastructure using Terragrunt.

Wraps terragrunt commands with sensible defaults for the LightWave infrastructure.

Examples:
  lw infra list                    # List all units
  lw infra plan services/platform  # Plan a specific unit
  lw infra apply services/platform # Apply changes`,
}

var infraListCmd = &cobra.Command{
	Use:   "list",
	Short: "List infrastructure units",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cfg := config.Get()

		runner := infra.NewTerragruntRunner(
			filepath.Join(cfg.Paths.LightwaveRoot, "Infrastructure"),
			infraEnv,
			infraRegion,
		)

		units, err := runner.ListUnits(ctx)
		if err != nil {
			return err
		}

		if len(units) == 0 {
			fmt.Println(color.YellowString("No units found"))
			return nil
		}

		fmt.Printf("Infrastructure units in %s/%s:\n\n", color.CyanString(infraEnv), color.CyanString(infraRegion))

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Path", "Type"})
		table.SetBorder(false)

		for _, unit := range units {
			unitType := "unit"
			if filepath.Base(unit) == "terragrunt.stack.hcl" {
				unitType = "stack"
			}
			table.Append([]string{unit, unitType})
		}

		table.Render()
		fmt.Printf("\n%s units\n", color.CyanString("%d", len(units)))

		return nil
	},
}

var infraPlanCmd = &cobra.Command{
	Use:   "plan <path>",
	Short: "Run terragrunt plan",
	Long: `Run terragrunt plan for a specific unit or stack.

Examples:
  lw infra plan infrastructure/ecr/platform
  lw infra plan services/platform/django`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cfg := config.Get()
		path := args[0]

		runner := infra.NewTerragruntRunner(
			filepath.Join(cfg.Paths.LightwaveRoot, "Infrastructure"),
			infraEnv,
			infraRegion,
		)

		fmt.Printf("Planning %s in %s/%s...\n\n", color.CyanString(path), infraEnv, infraRegion)

		result, err := runner.Plan(ctx, path)
		if err != nil {
			return err
		}

		// Output already streamed live — just print the summary
		if result.HasChanges {
			fmt.Println(color.YellowString("\n⚠ Changes detected"))
		} else {
			fmt.Println(color.GreenString("\n✓ No changes"))
		}

		return nil
	},
}

var infraApplyCmd = &cobra.Command{
	Use:   "apply <path>",
	Short: "Run terragrunt apply",
	Long: `Apply infrastructure changes for a specific unit or stack.

Examples:
  lw infra apply infrastructure/ecr/platform
  lw infra apply services/platform/django --auto-approve`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cfg := config.Get()
		path := args[0]

		runner := infra.NewTerragruntRunner(
			filepath.Join(cfg.Paths.LightwaveRoot, "Infrastructure"),
			infraEnv,
			infraRegion,
		)

		if !infraAutoApprove {
			fmt.Printf("Apply %s in %s/%s? This may modify infrastructure.\n",
				color.CyanString(path), infraEnv, infraRegion)
			fmt.Print("Type 'yes' to confirm: ")

			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "yes" {
				fmt.Println("Cancelled")
				return nil
			}
		}

		fmt.Printf("Applying %s...\n\n", color.CyanString(path))

		if err := runner.Apply(ctx, path, infraAutoApprove); err != nil {
			return err
		}

		fmt.Println(color.GreenString("\n✓ Apply complete"))
		return nil
	},
}

var infraValidateCmd = &cobra.Command{
	Use:   "validate <path>",
	Short: "Validate terragrunt configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cfg := config.Get()
		path := args[0]

		runner := infra.NewTerragruntRunner(
			filepath.Join(cfg.Paths.LightwaveRoot, "Infrastructure"),
			infraEnv,
			infraRegion,
		)

		fmt.Printf("Validating %s...\n", color.CyanString(path))

		if err := runner.Validate(ctx, path); err != nil {
			return err
		}

		fmt.Println(color.GreenString("✓ Configuration is valid"))
		return nil
	},
}

var infraOutputCmd = &cobra.Command{
	Use:   "output <path>",
	Short: "Show terragrunt outputs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cfg := config.Get()
		path := args[0]

		runner := infra.NewTerragruntRunner(
			filepath.Join(cfg.Paths.LightwaveRoot, "Infrastructure"),
			infraEnv,
			infraRegion,
		)

		outputs, err := runner.Output(ctx, path)
		if err != nil {
			return err
		}

		if len(outputs) == 0 {
			fmt.Println(color.YellowString("No outputs"))
			return nil
		}

		for key, value := range outputs {
			fmt.Printf("%s: %s\n", color.CyanString(key), value)
		}

		return nil
	},
}

var infraRunAllCmd = &cobra.Command{
	Use:   "run-all <command>",
	Short: "Run command across all units",
	Long: `Run a terragrunt command across all units in the environment.

Examples:
  lw infra run-all plan
  lw infra run-all validate`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cfg := config.Get()
		command := args[0]

		runner := infra.NewTerragruntRunner(
			filepath.Join(cfg.Paths.LightwaveRoot, "Infrastructure"),
			infraEnv,
			infraRegion,
		)

		fmt.Printf("Running %s across all units in %s/%s...\n\n",
			color.CyanString(command), infraEnv, infraRegion)

		return runner.RunAll(ctx, command)
	},
}

func init() {
	// Global infra flags
	infraCmd.PersistentFlags().StringVar(&infraEnv, "env", "prod", "Environment (prod, non-prod)")
	infraCmd.PersistentFlags().StringVar(&infraRegion, "region", "us-east-1", "AWS region")

	// Apply flags
	infraApplyCmd.Flags().BoolVar(&infraAutoApprove, "auto-approve", false, "Skip confirmation prompt")

	// Add subcommands
	infraCmd.AddCommand(infraListCmd)
	infraCmd.AddCommand(infraPlanCmd)
	infraCmd.AddCommand(infraApplyCmd)
	infraCmd.AddCommand(infraValidateCmd)
	infraCmd.AddCommand(infraOutputCmd)
	infraCmd.AddCommand(infraRunAllCmd)
}
