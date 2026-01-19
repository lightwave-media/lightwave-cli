/**
 * Automation Management Commands
 *
 * Commands for managing scheduled automations in the Agent Team system.
 *
 * Usage:
 *   lw automation list              # List all automations
 *   lw automation trigger <name>    # Manually trigger an automation
 *   lw automation logs [name]       # View automation run logs
 */

import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import { exec } from "../utils/exec.js";
import { findWorkspaceRoot, getDomainPath } from "../utils/paths.js";

export const automationCommand = new Command("automation").description(
  "Manage scheduled automations",
);

// =============================================================================
// AUTOMATION INFO
// =============================================================================

interface AutomationInfo {
  name: string;
  description: string;
  schedule: string;
  handler: string;
}

const AUTOMATIONS: Record<string, AutomationInfo> = {
  database_audit: {
    name: "Database Audit",
    description: "Check data consistency, orphaned records, integrity",
    schedule: "Daily at midnight",
    handler: "v_auditor",
  },
  content_review: {
    name: "Content Review",
    description: "Review recent content for quality and brand alignment",
    schedule: "Daily at 00:30",
    handler: "v_editor",
  },
  task_triage: {
    name: "Task Triage",
    description: "Triage pending tasks and assign to agents",
    schedule: "Hourly at :00",
    handler: "v_manager",
  },
  heal_bugs: {
    name: "Bug Healing",
    description: "Process detected bugs for self-healing repairs",
    schedule: "Every 4 hours at :15",
    handler: "v_developer",
  },
  emergence_cycle: {
    name: "Emergence Cycle",
    description: "Build knowledge graph, detect patterns, surface insights",
    schedule: "Daily at 1:00 AM",
    handler: "v_emergence",
  },
};

// =============================================================================
// LIST COMMAND
// =============================================================================

automationCommand
  .command("list")
  .description("List all scheduled automations")
  .option("--json", "Output as JSON")
  .action(async (options) => {
    console.log(chalk.blue("\n=== Scheduled Automations ===\n"));

    if (options.json) {
      console.log(JSON.stringify(AUTOMATIONS, null, 2));
      return;
    }

    for (const [key, info] of Object.entries(AUTOMATIONS)) {
      console.log(`${chalk.green(key)}`);
      console.log(`  Name:     ${info.name}`);
      console.log(`  Schedule: ${chalk.cyan(info.schedule)}`);
      console.log(`  Handler:  ${info.handler}`);
      console.log(`  ${chalk.gray(info.description)}`);
      console.log();
    }

    console.log(
      chalk.gray("Use 'lw automation trigger <name>' to run manually"),
    );
  });

// =============================================================================
// TRIGGER COMMAND
// =============================================================================

automationCommand
  .command("trigger <name>")
  .description("Manually trigger an automation")
  .option("--sync", "Wait for completion")
  .option("--dry-run", "Show what would be executed")
  .action(async (name, options) => {
    // Validate automation
    if (!AUTOMATIONS[name]) {
      console.log(chalk.red(`\nUnknown automation: ${name}`));
      console.log(chalk.gray("Available automations:"));
      for (const key of Object.keys(AUTOMATIONS)) {
        console.log(chalk.gray(`  - ${key}`));
      }
      process.exit(1);
    }

    const info = AUTOMATIONS[name];

    if (options.dryRun) {
      console.log(chalk.yellow("\n[DRY RUN] Would trigger:"));
      console.log(`  Automation: ${name}`);
      console.log(`  Description: ${info.description}`);
      console.log(`  Handler: ${info.handler}`);
      return;
    }

    const spinner = ora(`Triggering ${info.name}...`).start();

    try {
      const root = findWorkspaceRoot();
      const lwmCorePath = getDomainPath("lwm_core");

      let cmd = `cd "${lwmCorePath}" && docker compose exec -T web python manage.py trigger_automation ${name}`;
      if (options.sync) {
        cmd += " --sync";
      }

      const result = await exec(cmd, [], { silent: true });

      spinner.stop();

      console.log(chalk.blue(`\n=== ${info.name} ===\n`));
      console.log(result.stdout);

      if (!options.sync) {
        console.log(
          chalk.gray("\nAutomation queued. Check Celery logs for status."),
        );
        console.log(chalk.gray("Use --sync to wait for completion."));
      }
    } catch (error: any) {
      spinner.fail("Failed to trigger automation");
      console.log(chalk.red(error.message || error));
    }
  });

// =============================================================================
// LOGS COMMAND
// =============================================================================

automationCommand
  .command("logs [name]")
  .description("View automation run history")
  .option("-n, --limit <number>", "Number of runs to show", "10")
  .action(async (name, options) => {
    const spinner = ora("Fetching automation logs...").start();

    try {
      const root = findWorkspaceRoot();
      const lwmCorePath = getDomainPath("lwm_core");

      const limit = parseInt(options.limit) || 10;
      const nameFilter = name ? `automation_name='${name}'` : "1=1";

      const result = await exec(
        `cd "${lwmCorePath}" && docker compose exec -T web python manage.py shell -c "
from apps.ai.models import AutomationRun
from django.utils import timezone

runs = AutomationRun.objects.filter(${nameFilter}).order_by('-scheduled_time')[:${limit}]

for run in runs:
    status_icon = '✓' if run.status == 'completed' else '✗' if run.status == 'failed' else '⏳'
    duration = ''
    if run.completed_at and run.started_at:
        delta = run.completed_at - run.started_at
        duration = f'{delta.total_seconds():.1f}s'

    print(f'{status_icon}|{run.automation_name}|{run.status}|{run.scheduled_time.strftime(\"%Y-%m-%d %H:%M\")}|{run.tasks_processed}|{duration}')
"`,
        [],
        { silent: true },
      );

      spinner.stop();

      console.log(chalk.blue("\n=== Automation Runs ===\n"));

      const lines = result.stdout.trim().split("\n").filter(Boolean);

      if (lines.length === 0) {
        console.log(chalk.gray("No automation runs found"));
        return;
      }

      // Header
      console.log(
        chalk.gray(
          "Status".padEnd(8) +
            "Automation".padEnd(20) +
            "State".padEnd(12) +
            "Scheduled".padEnd(18) +
            "Tasks".padEnd(8) +
            "Duration",
        ),
      );
      console.log(chalk.gray("-".repeat(80)));

      for (const line of lines) {
        const [icon, automation, status, scheduled, tasks, duration] =
          line.split("|");

        const statusColor =
          status === "completed"
            ? chalk.green
            : status === "failed"
              ? chalk.red
              : chalk.yellow;

        console.log(
          `${icon.padEnd(8)}` +
            `${automation.padEnd(20)}` +
            `${statusColor(status.padEnd(12))}` +
            `${scheduled.padEnd(18)}` +
            `${tasks.padEnd(8)}` +
            `${duration || "-"}`,
        );
      }
    } catch (error: any) {
      spinner.fail("Failed to fetch logs");
      console.log(chalk.gray("\nMake sure Docker is running."));
    }
  });
