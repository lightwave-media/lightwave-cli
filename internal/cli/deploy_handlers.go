package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/aws"
)

// Schema-driven deploy handlers. commands.yaml v3.0.0 declares 4 commands:
// run, status, logs, rollback.
//
// All target ECS clusters named `platform-<env>` (matches existing
// `lw aws ecs ...` and `lw check ecs` conventions). Wraps internal/aws
// helpers — does not duplicate them.

func init() {
	RegisterHandler("deploy.run", deployRunHandler)
	RegisterHandler("deploy.status", deployStatusHandler)
	RegisterHandler("deploy.logs", deployLogsHandler)
	RegisterHandler("deploy.rollback", deployRollbackHandler)
}

func deployClusterFor(env string) string {
	return fmt.Sprintf("platform-%s", env)
}

func deployRunHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw deploy run <env> [--service=<name>] [--dry-run]")
	}
	env := args[0]
	service := flagStr(flags, "service")
	if service == "" {
		return fmt.Errorf("--service is required (which ECS service to redeploy)")
	}
	if flagBool(flags, "dry-run") {
		fmt.Printf("[dry-run] would force new deployment of %s on cluster %s\n",
			color.CyanString(service), color.CyanString(deployClusterFor(env)))
		return nil
	}
	client, err := aws.NewECSClient(ctx, deployClusterFor(env))
	if err != nil {
		return err
	}
	fmt.Printf("Deploying %s to %s...\n", color.CyanString(service), color.CyanString(env))
	if err := client.UpdateService(ctx, service, true); err != nil {
		return err
	}
	fmt.Println(color.GreenString("✓ Deployment initiated"))
	return nil
}

func deployStatusHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw deploy status <env> [--service=<name>] [--json]")
	}
	env := args[0]
	client, err := aws.NewECSClient(ctx, deployClusterFor(env))
	if err != nil {
		return err
	}
	if svc := flagStr(flags, "service"); svc != "" {
		status, err := client.GetServiceStatus(ctx, svc)
		if err != nil {
			return err
		}
		if asJSON(flags) {
			return emitJSON(status)
		}
		fmt.Printf("%s on %s\n", color.CyanString(status.Name), color.CyanString(env))
		fmt.Printf("  status:        %s\n", status.Status)
		fmt.Printf("  desired:       %d\n", status.DesiredCount)
		fmt.Printf("  running:       %d\n", status.RunningCount)
		fmt.Printf("  pending:       %d\n", status.PendingCount)
		fmt.Printf("  task-def:      %s\n", status.TaskDefinition)
		fmt.Printf("  last-deploy:   %s\n", status.LastDeployment)
		if status.Healthy {
			fmt.Println(color.GreenString("  healthy:       yes"))
		} else {
			fmt.Println(color.YellowString("  healthy:       no"))
		}
		return nil
	}
	services, err := client.ListServices(ctx)
	if err != nil {
		return err
	}
	if asJSON(flags) {
		return emitJSON(services)
	}
	for _, s := range services {
		fmt.Println(s)
	}
	return nil
}

func deployLogsHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw deploy logs <service> [--env=<env>] [--follow] [--since=<duration>]")
	}
	service := args[0]
	env := flagStrOr(flags, "env", "prod")
	logGroup := fmt.Sprintf("/ecs/%s-%s", deployClusterFor(env), service)
	client, err := aws.NewLogsClient(ctx)
	if err != nil {
		return err
	}
	if !flagBool(flags, "follow") {
		// One-shot read: fall through to TailLogs but cancel after a brief idle.
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return streamLogs(ctx, client, logGroup, "")
	}
	return streamLogs(ctx, client, logGroup, "")
}

func streamLogs(ctx context.Context, client *aws.LogsClient, group, prefix string) error {
	events, err := client.TailLogs(ctx, group, prefix)
	if err != nil {
		return err
	}
	for ev := range events {
		fmt.Printf("%s %s\n", ev.Timestamp.Format(time.RFC3339), ev.Message)
	}
	return nil
}

// deployRollbackHandler is a stub — rollback semantics depend on whether
// you're rolling task-def revisions or container image tags. Surface the
// gap rather than silently no-op.
func deployRollbackHandler(_ context.Context, args []string, _ map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw deploy rollback <env> [--service=<name>] [--version=<rev>]")
	}
	return fmt.Errorf("deploy rollback: not yet wired (ECS task-def revision rollback needs --version flag implementation; for emergency rollback use `lw aws ecs apply-task-def`)")
}
