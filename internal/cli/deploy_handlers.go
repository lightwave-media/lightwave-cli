package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/aws"
	"github.com/lightwave-media/lightwave-cli/internal/config"
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

// deployClusterFor resolves the ECS cluster for an environment.
//
// It returned `"platform-" + env` until #368 — the Django-era name. The cluster
// created by prod/us-east-1/lightwave-platform is `lightwave-platform`, so every
// verb in the group (`run`, `status`, `logs`, `rollback` all share this helper)
// failed with ClusterNotFoundException against a cluster that no longer exists.
//
// Resolution order, most specific first: the per-env map, then the single-target
// value, then the environment name as a last resort. A naming convention cannot
// be corrected once the thing it names stops following it, so the convention is
// no longer the source — but it stays as the final fallback rather than
// returning empty, because a wrong cluster name produces a clear AWS error while
// an empty one produces a confusing SDK failure.
func deployClusterFor(env string) string {
	cfg := config.Get()
	if cfg == nil {
		return env
	}

	return resolveCluster(cfg.Deploy, env)
}

// resolveCluster is the precedence itself, separated from the config singleton
// so it can be tested without reaching through a global.
func resolveCluster(dc config.DeployConfig, env string) string {
	if c, ok := dc.Clusters[env]; ok && c != "" {
		return c
	}

	if dc.Cluster != "" {
		return dc.Cluster
	}

	return env
}

// deployLogGroupFor resolves the CloudWatch log group for a service.
//
// The old derivation was `/ecs/<cluster>-<service>`, wrong in two ways at once:
// it used the Django-era cluster name, and the real group carries no service
// suffix (`/ecs/lightwave-platform`).
//
// Reading `awslogs-group` from the task definition would be better still — it
// is what the service actually writes to, rather than a second convention to
// keep in step — but that needs a DescribeTaskDefinition call the aws package
// does not yet wrap, and one this change cannot verify without live
// credentials. Left as the remaining half of #368 rather than guessed at.
func deployLogGroupFor(env, service string) string {
	cfg := config.Get()
	if cfg == nil {
		return "/ecs/" + env
	}

	return resolveLogGroup(cfg.Deploy, env, service)
}

// resolveLogGroup mirrors resolveCluster: precedence without the global.
//
// service is accepted and unused. The real group is cluster-scoped
// (`/ecs/lightwave-platform`), not per-service, and keeping the parameter makes
// that explicit at every call site rather than leaving a reader to wonder
// whether the service was forgotten.
func resolveLogGroup(dc config.DeployConfig, env, _ string) string {
	if g, ok := dc.LogGroups[env]; ok && g != "" {
		return g
	}

	if dc.LogGroup != "" {
		return dc.LogGroup
	}

	return "/ecs/" + resolveCluster(dc, env)
}

func deployRunHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw deploy run <env> --service=<name> [--image=<uri>] [--dry-run]")
	}

	env := args[0]

	service := flagStr(flags, "service")
	if service == "" {
		return errors.New("--service is required (which ECS service to redeploy)")
	}

	cluster := deployClusterFor(env)

	// New-image rollout when an image is supplied (--image flag or the
	// LW_DEPLOY_IMAGE CI env var): register a new task-def revision with that
	// image, point the service at it, and wait for it to stabilise. Otherwise
	// force a redeploy of the current task definition.
	image := flagStr(flags, "image")
	if image == "" {
		image = os.Getenv("LW_DEPLOY_IMAGE")
	}

	if flagBool(flags, "dry-run") {
		if image != "" {
			fmt.Printf("[dry-run] would roll %s on %s to image %s (register revision, update service, wait stable)\n",
				color.CyanString(service), color.CyanString(cluster), color.CyanString(image))
		} else {
			fmt.Printf("[dry-run] would force new deployment of %s on cluster %s\n",
				color.CyanString(service), color.CyanString(cluster))
		}

		return nil
	}

	client, err := aws.NewECSClient(ctx, cluster)
	if err != nil {
		return err
	}

	if image != "" {
		return deployImageRollout(ctx, client, service, image)
	}

	fmt.Printf("Deploying %s to %s...\n", color.CyanString(service), color.CyanString(env))

	if err := client.UpdateService(ctx, service, true); err != nil {
		return err
	}

	fmt.Println(color.GreenString("✓ Deployment initiated"))

	return nil
}

// deployImageRollout registers a new task-def revision with the given image,
// points the service at it, and blocks until the service stabilises (or the
// deployment circuit breaker rolls it back).
func deployImageRollout(ctx context.Context, client *aws.ECSClient, service, image string) error {
	fmt.Printf("Rolling %s to %s...\n", color.CyanString(service), color.CyanString(image))

	arn, err := client.RegisterRevisionWithImage(ctx, service, image)
	if err != nil {
		return err
	}

	if err := client.UpdateServiceWithTaskDef(ctx, service, arn); err != nil {
		return err
	}

	fmt.Printf("Registered %s; waiting for the service to stabilise...\n", color.CyanString(arn))

	if err := client.WaitForStableService(ctx, service); err != nil {
		return fmt.Errorf("service did not stabilise (the deployment circuit breaker may have rolled it back): %w", err)
	}

	fmt.Println(color.GreenString("✓ Deployment stable"))

	return nil
}

func deployStatusHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw deploy status <env> [--service=<name>] [--json]")
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
		return errors.New("usage: lw deploy logs <service> [--env=<env>] [--follow] [--since=<duration>]")
	}

	service := args[0]
	env := flagStrOr(flags, "env", "prod")
	logGroup := deployLogGroupFor(env, service)

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
		return errors.New("usage: lw deploy rollback <env> [--service=<name>] [--version=<rev>]")
	}

	return errors.New("deploy rollback: not yet wired (ECS task-def revision rollback needs --version flag implementation; for emergency rollback use `lw aws ecs apply-task-def`)")
}
