package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/aws"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

const (
	defaultCluster  = "platform-prod"
	defaultLogGroup = "/ecs/platform-prod"
)

var (
	ecsCluster      string
	logGroup        string
	logsSince       string
	logsLimit       int32
	forceDeployment bool
	waitStable      bool
	taskDefFile     string
)

var awsCmd = &cobra.Command{
	Use:   "aws",
	Short: "AWS operations (ECS, CloudWatch)",
	Long:  `Manage AWS resources - ECS services, CloudWatch logs, and more.`,
}

// ECS Commands
var ecsCmd = &cobra.Command{
	Use:   "ecs",
	Short: "ECS service management",
}

var ecsStatusCmd = &cobra.Command{
	Use:   "status [service]",
	Short: "Show ECS service status",
	Long: `Show status of ECS services.

Without arguments, shows all services in the cluster.
With a service name, shows detailed status for that service.

Examples:
  lw aws ecs status
  lw aws ecs status django
  lw aws ecs status --cluster my-cluster`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		client, err := aws.NewECSClient(ctx, ecsCluster)
		if err != nil {
			return err
		}

		if len(args) == 0 {
			// List all services
			return showAllServicesStatus(ctx, client)
		}

		// Show specific service
		return showServiceStatus(ctx, client, args[0])
	},
}

var ecsDeployCmd = &cobra.Command{
	Use:   "deploy <service>",
	Short: "Deploy ECS service (force new deployment)",
	Long: `Force a new deployment of an ECS service.

This triggers ECS to pull the latest image and restart all tasks.

Examples:
  lw aws ecs deploy django
  lw aws ecs deploy django --wait`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		serviceName := args[0]

		client, err := aws.NewECSClient(ctx, ecsCluster)
		if err != nil {
			return err
		}

		fmt.Printf("Deploying %s...\n", color.CyanString(serviceName))

		if err := client.UpdateService(ctx, serviceName, forceDeployment); err != nil {
			return err
		}

		fmt.Println(color.GreenString("✓ Deployment initiated"))

		if waitStable {
			fmt.Println("Waiting for service to stabilize...")
			if err := client.WaitForStableService(ctx, serviceName); err != nil {
				return fmt.Errorf("service did not stabilize: %w", err)
			}
			fmt.Println(color.GreenString("✓ Service is stable"))
		}

		return nil
	},
}

var ecsApplyTaskDefCmd = &cobra.Command{
	Use:   "apply-task-def <service>",
	Short: "Register a task definition from file and deploy it",
	Long: `Register a new task definition from a JSON file and update the service to use it.

This is the emergency deploy path when GitHub Actions is unavailable.

Examples:
  lw aws ecs apply-task-def platform-prod-frontend --file .aws/task-definition-frontend.json
  lw aws ecs apply-task-def platform-prod-frontend --file .aws/task-definition-frontend.json --wait`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		serviceName := args[0]

		if taskDefFile == "" {
			return fmt.Errorf("--file is required")
		}

		client, err := aws.NewECSClient(ctx, ecsCluster)
		if err != nil {
			return err
		}

		fmt.Printf("Registering task definition from %s...\n", color.CyanString(taskDefFile))
		taskDefArn, err := client.RegisterTaskDefinitionFromFile(ctx, taskDefFile)
		if err != nil {
			return err
		}
		fmt.Printf("%s Registered: %s\n", color.GreenString("✓"), taskDefArn)

		fmt.Printf("Updating service %s...\n", color.CyanString(serviceName))
		if err := client.UpdateServiceWithTaskDef(ctx, serviceName, taskDefArn); err != nil {
			return err
		}
		fmt.Println(color.GreenString("✓ Deployment initiated"))

		if waitStable {
			fmt.Println("Waiting for service to stabilize...")
			if err := client.WaitForStableService(ctx, serviceName); err != nil {
				return fmt.Errorf("service did not stabilize: %w", err)
			}
			fmt.Println(color.GreenString("✓ Service is stable"))
		}

		return nil
	},
}

var ecsTasksCmd = &cobra.Command{
	Use:   "tasks <service>",
	Short: "List running tasks for a service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		serviceName := args[0]

		client, err := aws.NewECSClient(ctx, ecsCluster)
		if err != nil {
			return err
		}

		tasks, err := client.GetRunningTasks(ctx, serviceName)
		if err != nil {
			return err
		}

		if len(tasks) == 0 {
			fmt.Println(color.YellowString("No running tasks"))
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Task ID", "Status", "Health", "Started"})
		table.SetBorder(false)

		for _, task := range tasks {
			taskID := extractShortID(*task.TaskArn)
			status := *task.LastStatus
			health := "N/A"
			if task.HealthStatus != "" {
				health = string(task.HealthStatus)
			}
			started := "N/A"
			if task.StartedAt != nil {
				started = task.StartedAt.Format("15:04:05")
			}

			table.Append([]string{taskID, status, health, started})
		}

		table.Render()
		return nil
	},
}

// Logs Commands
var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "CloudWatch log operations",
}

var logsShowCmd = &cobra.Command{
	Use:   "show [log-group]",
	Short: "Show recent logs",
	Long: `Show recent logs from CloudWatch.

Examples:
  lw aws logs show
  lw aws logs show /ecs/my-service
  lw aws logs show --since 1h --limit 100`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		group := logGroup
		if len(args) > 0 {
			group = args[0]
		}

		client, err := aws.NewLogsClient(ctx)
		if err != nil {
			return err
		}

		since, err := time.ParseDuration(logsSince)
		if err != nil {
			since = 30 * time.Minute
		}

		events, err := client.GetRecentLogs(ctx, group, since, logsLimit)
		if err != nil {
			return err
		}

		if len(events) == 0 {
			fmt.Println(color.YellowString("No logs found"))
			return nil
		}

		for _, event := range events {
			timestamp := color.HiBlackString(event.Timestamp.Format("15:04:05"))
			fmt.Printf("%s %s\n", timestamp, event.Message)
		}

		return nil
	},
}

var logsTailCmd = &cobra.Command{
	Use:   "tail [log-group]",
	Short: "Tail logs in real-time",
	Long: `Tail logs from CloudWatch in real-time.

Press Ctrl+C to stop.

Examples:
  lw aws logs tail
  lw aws logs tail /ecs/my-service`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		group := logGroup
		if len(args) > 0 {
			group = args[0]
		}

		client, err := aws.NewLogsClient(ctx)
		if err != nil {
			return err
		}

		fmt.Printf("Tailing %s (Ctrl+C to stop)...\n\n", color.CyanString(group))

		events, err := client.TailLogs(ctx, group, "")
		if err != nil {
			return err
		}

		for event := range events {
			timestamp := color.HiBlackString(event.Timestamp.Format("15:04:05"))
			fmt.Printf("%s %s\n", timestamp, event.Message)
		}

		return nil
	},
}

var logsListCmd = &cobra.Command{
	Use:   "list [prefix]",
	Short: "List log groups",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		prefix := "/ecs/lightwave"
		if len(args) > 0 {
			prefix = args[0]
		}

		client, err := aws.NewLogsClient(ctx)
		if err != nil {
			return err
		}

		groups, err := client.GetLogGroups(ctx, prefix)
		if err != nil {
			return err
		}

		if len(groups) == 0 {
			fmt.Println(color.YellowString("No log groups found"))
			return nil
		}

		for _, g := range groups {
			fmt.Println(g)
		}

		return nil
	},
}

func init() {
	// ECS flags
	ecsCmd.PersistentFlags().StringVar(&ecsCluster, "cluster", defaultCluster, "ECS cluster name")
	ecsDeployCmd.Flags().BoolVar(&forceDeployment, "force", true, "Force new deployment")
	ecsDeployCmd.Flags().BoolVar(&waitStable, "wait", false, "Wait for service to stabilize")
	ecsApplyTaskDefCmd.Flags().StringVar(&taskDefFile, "file", "", "Path to task definition JSON file")
	ecsApplyTaskDefCmd.Flags().BoolVar(&waitStable, "wait", false, "Wait for service to stabilize")

	// Logs flags
	logsCmd.PersistentFlags().StringVar(&logGroup, "group", defaultLogGroup, "Log group name")
	logsShowCmd.Flags().StringVar(&logsSince, "since", "30m", "Time range (e.g., 30m, 1h, 24h)")
	logsShowCmd.Flags().Int32Var(&logsLimit, "limit", 100, "Maximum number of log entries")

	// Add ECS subcommands
	ecsCmd.AddCommand(ecsStatusCmd)
	ecsCmd.AddCommand(ecsDeployCmd)
	ecsCmd.AddCommand(ecsApplyTaskDefCmd)
	ecsCmd.AddCommand(ecsTasksCmd)

	// Add Logs subcommands
	logsCmd.AddCommand(logsShowCmd)
	logsCmd.AddCommand(logsTailCmd)
	logsCmd.AddCommand(logsListCmd)

	// Add to aws command
	awsCmd.AddCommand(ecsCmd)
	awsCmd.AddCommand(ecrCmd)
	awsCmd.AddCommand(logsCmd)
}

func showAllServicesStatus(ctx context.Context, client *aws.ECSClient) error {
	services, err := client.ListServices(ctx)
	if err != nil {
		return err
	}

	if len(services) == 0 {
		fmt.Println(color.YellowString("No services found"))
		return nil
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Service", "Status", "Running", "Desired", "Health"})
	table.SetBorder(false)

	for _, svcArn := range services {
		svcName := extractShortID(svcArn)
		status, err := client.GetServiceStatus(ctx, svcName)
		if err != nil {
			continue
		}

		healthStr := color.RedString("✗")
		if status.Healthy {
			healthStr = color.GreenString("✓")
		}

		table.Append([]string{
			status.Name,
			status.Status,
			fmt.Sprintf("%d", status.RunningCount),
			fmt.Sprintf("%d", status.DesiredCount),
			healthStr,
		})
	}

	table.Render()
	return nil
}

func showServiceStatus(ctx context.Context, client *aws.ECSClient, serviceName string) error {
	status, err := client.GetServiceStatus(ctx, serviceName)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("%s %s\n", color.CyanString("Service:"), status.Name)
	fmt.Printf("%s %s\n", color.CyanString("Status:"), status.Status)
	fmt.Println()

	healthColor := color.RedString
	if status.Healthy {
		healthColor = color.GreenString
	}

	fmt.Printf("%s %s\n", color.CyanString("Running:"), fmt.Sprintf("%d / %d", status.RunningCount, status.DesiredCount))
	fmt.Printf("%s %s\n", color.CyanString("Pending:"), fmt.Sprintf("%d", status.PendingCount))
	fmt.Printf("%s %s\n", color.CyanString("Health:"), healthColor(fmt.Sprintf("%v", status.Healthy)))
	fmt.Println()

	fmt.Printf("%s %s\n", color.CyanString("Task Definition:"), status.TaskDefinition)
	fmt.Printf("%s %s\n", color.CyanString("Last Deployment:"), status.LastDeployment)

	return nil
}

func extractShortID(arn string) string {
	parts := splitLast(arn, "/")
	if len(parts) > 1 {
		return parts[1]
	}
	return arn
}

func splitLast(s, sep string) []string {
	for i := len(s) - 1; i >= 0; i-- {
		if string(s[i]) == sep {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}
