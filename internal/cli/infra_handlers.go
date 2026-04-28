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
)

// Schema-driven infra handlers. commands.yaml v3.0.0 declares 7 commands:
// list, plan, apply, validate, output, run-all, status. All wrap
// internal/infra.TerragruntRunner; status is a placeholder until ECS
// describe-services is wired in deploy.

func init() {
	RegisterHandler("infra.list", infraListHandler)
	RegisterHandler("infra.plan", infraPlanHandler)
	RegisterHandler("infra.apply", infraApplyHandler)
	RegisterHandler("infra.validate", infraValidateHandler)
	RegisterHandler("infra.output", infraOutputHandler)
	RegisterHandler("infra.run-all", infraRunAllHandler)
	RegisterHandler("infra.status", infraStatusHandler)
}

func newInfraRunner(flags map[string]any) *infra.TerragruntRunner {
	cfg := config.Get()
	env := flagStrOr(flags, "env", "prod")
	region := flagStrOr(flags, "region", "us-east-1")
	return infra.NewTerragruntRunner(
		filepath.Join(cfg.Paths.LightwaveRoot, "Infrastructure"),
		env, region,
	)
}

func infraListHandler(ctx context.Context, _ []string, flags map[string]any) error {
	runner := newInfraRunner(flags)
	units, err := runner.ListUnits(ctx)
	if err != nil {
		return err
	}
	if asJSON(flags) {
		return emitJSON(units)
	}
	if len(units) == 0 {
		fmt.Println(color.YellowString("No units found"))
		return nil
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Path", "Type"})
	table.SetBorder(false)
	for _, unit := range units {
		t := "unit"
		if filepath.Base(unit) == "terragrunt.stack.hcl" {
			t = "stack"
		}
		table.Append([]string{unit, t})
	}
	table.Render()
	fmt.Printf("\n%s units\n", color.CyanString("%d", len(units)))
	return nil
}

func infraPlanHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw infra plan <path>")
	}
	runner := newInfraRunner(flags)
	result, err := runner.Plan(ctx, args[0])
	if err != nil {
		return err
	}
	if result.HasChanges {
		fmt.Println(color.YellowString("\n⚠ Changes detected"))
	} else {
		fmt.Println(color.GreenString("\n✓ No changes"))
	}
	return nil
}

func infraApplyHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw infra apply <path> [--auto-approve]")
	}
	runner := newInfraRunner(flags)
	auto := flagBool(flags, "auto-approve")
	if !auto {
		if !promptYesNo(fmt.Sprintf("Apply %s? May modify infrastructure.", args[0])) {
			fmt.Println("Cancelled")
			return nil
		}
	}
	if err := runner.Apply(ctx, args[0], auto); err != nil {
		return err
	}
	fmt.Println(color.GreenString("\n✓ Apply complete"))
	return nil
}

func infraValidateHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw infra validate <path>")
	}
	runner := newInfraRunner(flags)
	if err := runner.Validate(ctx, args[0]); err != nil {
		return err
	}
	fmt.Println(color.GreenString("✓ Configuration is valid"))
	return nil
}

func infraOutputHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw infra output <path>")
	}
	runner := newInfraRunner(flags)
	outputs, err := runner.Output(ctx, args[0])
	if err != nil {
		return err
	}
	if asJSON(flags) {
		return emitJSON(outputs)
	}
	if len(outputs) == 0 {
		fmt.Println(color.YellowString("No outputs"))
		return nil
	}
	for k, v := range outputs {
		fmt.Printf("%s: %s\n", color.CyanString(k), v)
	}
	return nil
}

func infraRunAllHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw infra run-all <command>")
	}
	runner := newInfraRunner(flags)
	return runner.RunAll(ctx, args[0])
}

// infraStatusHandler is a stub until ECS describe-services lands in deploy.
// Surface the gap to the caller rather than silent no-op.
func infraStatusHandler(_ context.Context, args []string, _ map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw infra status <domain-id>")
	}
	return fmt.Errorf("infra status: not yet wired (use `lw aws ecs status` for ECS health, or `lw deploy status <env>`)")
}
