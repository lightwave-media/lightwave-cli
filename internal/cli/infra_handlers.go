package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

func newInfraRunner(flags map[string]any) (*infra.TerragruntRunner, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, errors.New("no configuration found; run `lw config init` to initialize")
	}

	env := flagStrOr(flags, "env", "prod")
	region := flagStrOr(flags, "region", "us-east-1")

	return infra.NewTerragruntRunner(
		filepath.Join(cfg.Paths.LightwaveRoot, "lightwave-infrastructure-live"),
		env, region,
	), nil
}

// unitPathSegments is the <env>/<region> prefix every unit path carries before
// the unit name itself. A shorter path is not addressable as a unit.
const unitPathSegments = 2

// filterUnits keeps the units whose <env>/<region>/... prefix matches. An empty
// env or region matches everything, so `lw infra list` with no flags shows the
// whole repo — the behaviour #367 asked for.
func filterUnits(units []string, env, region string) []string {
	if env == "" && region == "" {
		return units
	}

	kept := make([]string, 0, len(units))

	for _, u := range units {
		parts := strings.Split(u, string(filepath.Separator))
		if len(parts) < unitPathSegments {
			continue
		}

		if env != "" && parts[0] != env {
			continue
		}

		if region != "" && parts[1] != region {
			continue
		}

		kept = append(kept, u)
	}

	return kept
}

func infraListHandler(ctx context.Context, _ []string, flags map[string]any) error {
	runner, err := newInfraRunner(flags)
	if err != nil {
		return err
	}

	units, err := runner.ListUnits(ctx)
	if err != nil {
		return err
	}

	// --env / --region narrow the listing. They are filters here rather than a
	// working directory: before #367 they selected the ONLY tree that was
	// walked, defaulted to prod/us-east-1, and were never registered as flags —
	// so the default was the only reachable value and every other region was
	// invisible with no way to ask for it.
	units = filterUnits(units, flagStr(flags, "env"), flagStr(flags, "region"))

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
		return errors.New("usage: lw infra plan <path>")
	}

	runner, err := newInfraRunner(flags)
	if err != nil {
		return err
	}

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
		return errors.New("usage: lw infra apply <path> [--auto-approve]")
	}

	runner, err := newInfraRunner(flags)
	if err != nil {
		return err
	}

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
		return errors.New("usage: lw infra validate <path>")
	}

	runner, err := newInfraRunner(flags)
	if err != nil {
		return err
	}

	if err := runner.Validate(ctx, args[0]); err != nil {
		return err
	}

	fmt.Println(color.GreenString("✓ Configuration is valid"))

	return nil
}

func infraOutputHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw infra output <path>")
	}

	runner, err := newInfraRunner(flags)
	if err != nil {
		return err
	}

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
		return errors.New("usage: lw infra run-all <command>")
	}

	runner, err := newInfraRunner(flags)
	if err != nil {
		return err
	}

	return runner.RunAll(ctx, args[0])
}

// infraStatusHandler reports that the verb is unimplemented, and points only at
// commands that exist.
//
// It had two faults, both of which made the failure misleading rather than
// merely unhelpful (#367):
//
// It demanded `<domain-id>`, an argument the stamp does not declare. The
// dispatcher therefore passed none and every invocation died on the usage line
// — a usage error for an argument no caller could have known to supply, for a
// command that refuses regardless.
//
// It then sent the reader to `lw aws ecs status`. `aws` is DECOMMISSIONED
// (command_status.go: "live AWS credentials + ECS; needs an e2e harness"), so
// the suggested cure is itself offline. A pointer to a plausible-sounding dead
// command is worse than admitting the gap, because the reader spends their time
// on the wrong thing — the same reasoning as errDjangoRetired in #318.
//
// `lw deploy status` and `lw infra output` are real and shipped, so those are
// what it names.
func infraStatusHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errors.New(
		"`lw infra status` is declared in the stamp but has no implementation. " +
			"For service health use `lw deploy status <env>`; for a unit's recorded " +
			"state use `lw infra output <env>/<region>/<unit>`. " +
			"Tracked in lightwave-cli#367")
}
