/**
 * Bug Management Commands
 *
 * Commands for managing auto-detected bugs in the self-healing system.
 *
 * Usage:
 *   lw bugs list              # List detected bugs
 *   lw bugs show <id>         # Show bug details
 *   lw bugs heal <id>         # Trigger healing for a bug
 */

import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import { exec } from "../utils/exec.js";
import { findWorkspaceRoot, getDomainPath } from "../utils/paths.js";

export const bugsCommand = new Command("bugs").description(
  "Manage auto-detected bugs",
);

// =============================================================================
// LIST COMMAND
// =============================================================================

bugsCommand
  .command("list")
  .description("List detected bugs")
  .option(
    "--status <status>",
    "Filter by status (detected, analyzing, fixed, rejected)",
  )
  .option("-n, --limit <number>", "Number of bugs to show", "20")
  .option("--json", "Output as JSON")
  .action(async (options) => {
    const spinner = ora("Fetching bugs...").start();

    try {
      const root = findWorkspaceRoot();
      const lwmCorePath = getDomainPath("lwm_core");

      const limit = parseInt(options.limit) || 20;
      const statusFilter = options.status
        ? `fix_status='${options.status}'`
        : "1=1";

      const result = await exec(
        `cd "${lwmCorePath}" && docker compose exec -T web python manage.py shell -c "
import json
from apps.ai.models import BugReport

bugs = BugReport.objects.filter(${statusFilter}).order_by('-created_at')[:${limit}]

bug_list = []
for bug in bugs:
    bug_list.append({
        'id': str(bug.id)[:8],
        'error_type': bug.error_type,
        'fix_status': bug.fix_status,
        'auto_detected': bug.auto_detected,
        'created_at': bug.created_at.strftime('%Y-%m-%d %H:%M'),
        'execution_id': str(bug.source_execution_id)[:8] if bug.source_execution_id else None,
    })

if ${options.json ? "True" : "False"}:
    print(json.dumps(bug_list, indent=2))
else:
    for b in bug_list:
        status_icon = '🔴' if b['fix_status'] == 'detected' else '🟡' if b['fix_status'] == 'analyzing' else '🟢' if b['fix_status'] == 'fixed' else '⚪'
        print(f\"{status_icon}|{b['id']}|{b['error_type'][:30]}|{b['fix_status']}|{b['created_at']}\")
"`,
        [],
        { silent: true },
      );

      spinner.stop();

      if (options.json) {
        console.log(result.stdout);
        return;
      }

      console.log(chalk.blue("\n=== Bug Reports ===\n"));

      const lines = result.stdout.trim().split("\n").filter(Boolean);

      if (lines.length === 0 || lines[0].startsWith("[]")) {
        console.log(chalk.gray("No bugs found"));
        console.log(chalk.gray("\nThis is good! No auto-detected bugs."));
        return;
      }

      // Header
      console.log(
        chalk.gray(
          "  " +
            "ID".padEnd(10) +
            "Error Type".padEnd(32) +
            "Status".padEnd(14) +
            "Created",
        ),
      );
      console.log(chalk.gray("-".repeat(75)));

      for (const line of lines) {
        if (!line.includes("|")) continue;

        const [icon, id, errorType, status, created] = line.split("|");

        const statusColor =
          status === "detected"
            ? chalk.red
            : status === "analyzing"
              ? chalk.yellow
              : status === "fixed"
                ? chalk.green
                : chalk.gray;

        console.log(
          `${icon} ` +
            `${chalk.cyan(id.padEnd(10))}` +
            `${errorType.padEnd(32)}` +
            `${statusColor(status.padEnd(14))}` +
            `${created}`,
        );
      }

      console.log(chalk.gray(`\nUse 'lw bugs show <id>' for details`));
      console.log(chalk.gray(`Use 'lw bugs heal <id>' to trigger repair`));
    } catch (error: any) {
      spinner.fail("Failed to fetch bugs");
      console.log(chalk.gray("\nMake sure Docker is running."));
    }
  });

// =============================================================================
// SHOW COMMAND
// =============================================================================

bugsCommand
  .command("show <id>")
  .description("Show bug details")
  .action(async (id) => {
    const spinner = ora("Fetching bug details...").start();

    try {
      const root = findWorkspaceRoot();
      const lwmCorePath = getDomainPath("lwm_core");

      const result = await exec(
        `cd "${lwmCorePath}" && docker compose exec -T web python manage.py shell -c "
import json
from apps.ai.models import BugReport

try:
    bug = BugReport.objects.get(id__startswith='${id}')
except BugReport.DoesNotExist:
    print('NOT_FOUND')
    exit()

print('ID:', bug.id)
print('ERROR_TYPE:', bug.error_type)
print('STATUS:', bug.fix_status)
print('AUTO_DETECTED:', bug.auto_detected)
print('CREATED:', bug.created_at.strftime('%Y-%m-%d %H:%M:%S'))
if bug.healed_at:
    print('HEALED:', bug.healed_at.strftime('%Y-%m-%d %H:%M:%S'))
if bug.proposed_fix:
    print('PROPOSED_FIX:', bug.proposed_fix)
print('---DETAILS---')
print(json.dumps(bug.error_details or {}, indent=2))
"`,
        [],
        { silent: true },
      );

      spinner.stop();

      if (result.stdout.includes("NOT_FOUND")) {
        console.log(chalk.red(`\nBug not found: ${id}`));
        process.exit(1);
      }

      console.log(chalk.blue("\n=== Bug Details ===\n"));

      const lines = result.stdout.split("\n");
      let inDetails = false;

      for (const line of lines) {
        if (line === "---DETAILS---") {
          inDetails = true;
          console.log(chalk.yellow("\nError Details:"));
          continue;
        }

        if (inDetails) {
          console.log(chalk.gray(line));
        } else if (line.startsWith("ID:")) {
          console.log(`${chalk.gray("ID:")} ${chalk.cyan(line.slice(4))}`);
        } else if (line.startsWith("ERROR_TYPE:")) {
          console.log(`${chalk.gray("Error Type:")} ${line.slice(12)}`);
        } else if (line.startsWith("STATUS:")) {
          const status = line.slice(8);
          const statusColor =
            status === "detected"
              ? chalk.red
              : status === "fixed"
                ? chalk.green
                : chalk.yellow;
          console.log(`${chalk.gray("Status:")} ${statusColor(status)}`);
        } else if (line.startsWith("PROPOSED_FIX:")) {
          console.log(chalk.yellow("\nProposed Fix:"));
          console.log(line.slice(14));
        } else if (line.includes(":")) {
          const [key, ...rest] = line.split(":");
          console.log(`${chalk.gray(key + ":")} ${rest.join(":")}`);
        }
      }
    } catch (error: any) {
      spinner.fail("Failed to fetch bug details");
      console.log(chalk.red(error.message || error));
    }
  });

// =============================================================================
// HEAL COMMAND
// =============================================================================

bugsCommand
  .command("heal <id>")
  .description("Trigger healing for a bug")
  .option("--dry-run", "Show what would be done")
  .action(async (id, options) => {
    if (options.dryRun) {
      console.log(chalk.yellow("\n[DRY RUN] Would heal:"));
      console.log(`  Bug ID: ${id}`);
      console.log(`  Action: Route to v_developer for analysis and repair`);
      return;
    }

    const spinner = ora("Initiating bug healing...").start();

    try {
      const root = findWorkspaceRoot();
      const lwmCorePath = getDomainPath("lwm_core");

      const result = await exec(
        `cd "${lwmCorePath}" && docker compose exec -T web python manage.py shell -c "
from apps.ai.models import BugReport, BugFixStatus
from apps.ai.healing import BugReporter

try:
    bug = BugReport.objects.get(id__startswith='${id}')
except BugReport.DoesNotExist:
    print('NOT_FOUND')
    exit()

if bug.fix_status == 'fixed':
    print('ALREADY_FIXED')
    exit()

# Update status to analyzing
bug.fix_status = BugFixStatus.ANALYZING
bug.save()

print('QUEUED')
print(f'Bug {bug.id} queued for healing')
print(f'Current status: {bug.fix_status}')
"`,
        [],
        { silent: true },
      );

      spinner.stop();

      if (result.stdout.includes("NOT_FOUND")) {
        console.log(chalk.red(`\nBug not found: ${id}`));
        process.exit(1);
      }

      if (result.stdout.includes("ALREADY_FIXED")) {
        console.log(chalk.yellow(`\nBug ${id} is already fixed`));
        return;
      }

      console.log(chalk.green(`\n✓ Bug ${id} queued for healing`));
      console.log(
        chalk.gray("\nThe heal_bugs automation will process this bug."),
      );
      console.log(
        chalk.gray(
          "You can trigger it manually with: lw automation trigger heal_bugs",
        ),
      );
    } catch (error: any) {
      spinner.fail("Failed to initiate healing");
      console.log(chalk.red(error.message || error));
    }
  });
