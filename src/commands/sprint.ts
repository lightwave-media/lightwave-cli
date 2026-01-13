/**
 * Sprint management commands
 *
 * Commands:
 *   lw sprint list              List sprints from Notion
 *   lw sprint current           Show current active sprint
 *   lw sprint info <id>         Show sprint details
 *   lw sprint start <id>        Create sprint branch from epic
 *   lw sprint merge <id>        Merge sprint to epic
 */

import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import {
  querySprints,
  getCurrentSprint,
  getSprint,
  queryTasks,
  getEpic,
  generateSprintBranchName,
  generateEpicBranchName,
  updateSprintStatus,
} from "../utils/notion.js";
import { exec } from "../utils/exec.js";
import type { SprintStatus, NotionSprint } from "../types/notion.js";

export const sprintCommand = new Command("sprint").description(
  "Sprint management - view and track sprints from Notion",
);

/**
 * Get chalk color for sprint status
 */
function getStatusColor(status: SprintStatus) {
  const colors: Record<SprintStatus, (s: string) => string> = {
    "Not Started": chalk.gray,
    "In Progress": chalk.magenta,
    Completed: chalk.green,
    Cancelled: chalk.red,
  };
  return colors[status] || chalk.white;
}

/**
 * Format date range for display
 */
function formatDateRange(start: string | null, end: string | null): string {
  if (!start) return "No dates";
  if (!end) return start;
  return `${start} → ${end}`;
}

// =============================================================================
// lw sprint list
// =============================================================================

sprintCommand
  .command("list")
  .description("List sprints from Notion")
  .option(
    "--status <status>",
    "Filter by status (Not Started, In Progress, Completed, Cancelled)",
  )
  .option("--all", "Show all statuses")
  .option("--domain <name>", "Filter by Life Domain")
  .option("--limit <n>", "Max number of sprints", "20")
  .option("--format <format>", "Output format: table, json", "table")
  .action(async (options) => {
    const spinner = ora("Fetching sprints from Notion...").start();

    try {
      // Parse status filter
      let statusFilter: SprintStatus[] | undefined;
      if (options.status) {
        statusFilter = options.status
          .split(",")
          .map((s: string) => s.trim() as SprintStatus);
      } else if (!options.all) {
        // Default: show active sprints
        statusFilter = ["In Progress", "Not Started"];
      }

      const sprints = await querySprints({
        status: statusFilter,
        domain: options.domain,
        limit: parseInt(options.limit, 10),
      });

      spinner.stop();

      if (sprints.length === 0) {
        console.log(chalk.yellow("No sprints found matching criteria."));
        return;
      }

      if (options.format === "json") {
        console.log(JSON.stringify(sprints, null, 2));
        return;
      }

      // Table format
      console.log(chalk.blue("\n=== Sprints ===\n"));
      console.log(
        chalk.gray(
          `${"ID".padEnd(10)} ${"Status".padEnd(14)} ${"Dates".padEnd(26)} ${"Name".padEnd(40)}`,
        ),
      );
      console.log(chalk.gray("-".repeat(92)));

      for (const sprint of sprints) {
        const statusColor = getStatusColor(sprint.status);
        const dateRange = formatDateRange(sprint.startDate, sprint.endDate);
        const truncatedName =
          sprint.name.length > 38
            ? sprint.name.substring(0, 38) + "..."
            : sprint.name;
        console.log(
          `${chalk.cyan(sprint.shortId.padEnd(10))} ` +
            `${statusColor(sprint.status.padEnd(14))} ` +
            `${chalk.gray(dateRange.padEnd(26))} ` +
            `${truncatedName}`,
        );
      }

      console.log(chalk.gray(`\n${sprints.length} sprint(s) shown`));
    } catch (err) {
      spinner.fail("Failed to fetch sprints");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw sprint current
// =============================================================================

sprintCommand
  .command("current")
  .description("Show the current active sprint")
  .option("--format <format>", "Output format: text, json", "text")
  .option("--tasks", "Also show tasks in this sprint")
  .action(async (options) => {
    const spinner = ora("Loading current sprint...").start();

    try {
      const sprint = await getCurrentSprint();

      if (!sprint) {
        spinner.stop();
        console.log(chalk.yellow("No active sprint found."));
        console.log(chalk.gray("Run `lw sprint list` to see all sprints."));
        return;
      }

      spinner.stop();

      if (options.format === "json") {
        console.log(JSON.stringify(sprint, null, 2));
        return;
      }

      displaySprintDetails(sprint);

      // Optionally show tasks
      if (options.tasks) {
        await displaySprintTasks(sprint);
      }
    } catch (err) {
      spinner.fail("Failed to load sprint");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw sprint info <id>
// =============================================================================

sprintCommand
  .command("info <sprint-id>")
  .description("Show detailed sprint information")
  .option("--format <format>", "Output format: text, json", "text")
  .option("--tasks", "Also show tasks in this sprint")
  .action(async (sprintId: string, options) => {
    const spinner = ora("Loading sprint from Notion...").start();

    try {
      const sprint = await getSprint(sprintId);
      if (!sprint) {
        spinner.fail(`Sprint not found: ${sprintId}`);
        process.exit(1);
      }

      spinner.stop();

      if (options.format === "json") {
        console.log(JSON.stringify(sprint, null, 2));
        return;
      }

      displaySprintDetails(sprint);

      // Optionally show tasks
      if (options.tasks) {
        await displaySprintTasks(sprint);
      }
    } catch (err) {
      spinner.fail("Failed to load sprint");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

/**
 * Display sprint details in formatted text
 */
function displaySprintDetails(sprint: NotionSprint): void {
  console.log(chalk.blue("\n=== Sprint Details ===\n"));
  console.log(chalk.yellow("ID:"), sprint.shortId);
  console.log(chalk.yellow("Full ID:"), sprint.id);
  console.log(chalk.yellow("Name:"), sprint.name);
  console.log(
    chalk.yellow("Status:"),
    getStatusColor(sprint.status)(sprint.status),
  );
  console.log(
    chalk.yellow("Dates:"),
    formatDateRange(sprint.startDate, sprint.endDate),
  );
  console.log(chalk.yellow("URL:"), sprint.url);

  if (sprint.qualityScore !== null) {
    console.log(chalk.yellow("Quality Score:"), sprint.qualityScore);
  }

  if (sprint.objectives) {
    console.log(chalk.yellow("\nObjectives:"));
    console.log(chalk.gray(sprint.objectives));
  }

  console.log(chalk.yellow("\nLinked Items:"));
  console.log(chalk.gray(`  Epics: ${sprint.epicIds.length}`));
  console.log(chalk.gray(`  Tasks: ${sprint.taskIds.length}`));
  console.log(chalk.gray(`  User Stories: ${sprint.userStoryIds.length}`));
}

/**
 * Display tasks in a sprint
 */
async function displaySprintTasks(sprint: NotionSprint): Promise<void> {
  const spinner = ora("Loading sprint tasks...").start();

  try {
    const tasks = await queryTasks({ sprint: sprint.name });
    spinner.stop();

    if (tasks.length === 0) {
      console.log(chalk.gray("\nNo tasks in this sprint."));
      return;
    }

    console.log(chalk.blue(`\n=== Tasks in Sprint (${tasks.length}) ===\n`));
    console.log(
      chalk.gray(
        `${"ID".padEnd(10)} ${"Status".padEnd(18)} ${"Title".padEnd(50)}`,
      ),
    );
    console.log(chalk.gray("-".repeat(80)));

    for (const task of tasks) {
      const truncatedTitle =
        task.title.length > 48
          ? task.title.substring(0, 48) + "..."
          : task.title;
      console.log(
        `${chalk.cyan(task.shortId.padEnd(10))} ` +
          `${task.status.padEnd(18)} ` +
          `${truncatedTitle}`,
      );
    }
  } catch (err) {
    spinner.fail("Failed to load sprint tasks");
    console.error(chalk.red((err as Error).message));
  }
}

// =============================================================================
// lw sprint start <id>
// =============================================================================

sprintCommand
  .command("start <sprint-id>")
  .description("Create sprint branch from epic branch")
  .option("--dry-run", "Show what would be done without making changes")
  .option("--no-push", "Create branch locally without pushing")
  .action(async (sprintId: string, options) => {
    const spinner = ora("Loading sprint from Notion...").start();

    try {
      const sprint = await getSprint(sprintId);
      if (!sprint) {
        spinner.fail(`Sprint not found: ${sprintId}`);
        process.exit(1);
      }

      // Get the linked epic (required for proper branching)
      let epic = null;
      if (sprint.epicIds.length > 0) {
        epic = await getEpic(sprint.epicIds[0]);
      }

      spinner.stop();

      const sprintBranchName = generateSprintBranchName(sprint, epic);
      const epicBranchName = epic ? generateEpicBranchName(epic) : "main";

      console.log(chalk.blue("\n=== Start Sprint ===\n"));
      console.log(chalk.yellow("Sprint:"), sprint.name);
      console.log(chalk.yellow("ID:"), sprint.shortId);
      if (epic) {
        console.log(chalk.yellow("Epic:"), epic.name);
      }
      console.log(chalk.yellow("Base:"), chalk.gray(epicBranchName));
      console.log(chalk.yellow("Branch:"), chalk.cyan(sprintBranchName));

      if (options.dryRun) {
        console.log(chalk.gray("\n[Dry run - no changes made]"));
        console.log(chalk.gray("Would:"));
        console.log(
          chalk.gray(`  1. git checkout ${epicBranchName} && git pull`),
        );
        console.log(chalk.gray(`  2. git checkout -b ${sprintBranchName}`));
        if (options.push !== false) {
          console.log(
            chalk.gray(`  3. git push -u origin ${sprintBranchName}`),
          );
        }
        console.log(chalk.gray('  4. Update sprint status to "In Progress"'));
        return;
      }

      // Ensure we're on epic branch and up to date
      spinner.start(`Updating ${epicBranchName} branch...`);
      await exec(`git checkout ${epicBranchName} && git pull`);
      spinner.succeed(`${epicBranchName} branch updated`);

      // Create sprint branch
      spinner.start(`Creating branch ${sprintBranchName}...`);
      await exec(`git checkout -b ${sprintBranchName}`);
      spinner.succeed(`Created branch ${sprintBranchName}`);

      // Push to remote (unless --no-push)
      if (options.push !== false) {
        spinner.start("Pushing to remote...");
        await exec(`git push -u origin ${sprintBranchName}`);
        spinner.succeed("Pushed to remote");
      }

      // Update sprint status in Notion
      spinner.start("Updating sprint status in Notion...");
      await updateSprintStatus(sprint.id, "In Progress");
      spinner.succeed("Sprint status updated to In Progress");

      console.log(chalk.green("\nSprint started successfully!"));
      console.log(
        chalk.gray(
          `\nNow commit tasks to this branch. When done, run 'lw sprint merge ${sprint.shortId}'`,
        ),
      );
    } catch (err) {
      spinner.fail("Failed to start sprint");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw sprint merge <id>
// =============================================================================

sprintCommand
  .command("merge <sprint-id>")
  .description("Merge sprint branch to epic branch")
  .option("--dry-run", "Show what would be done without making changes")
  .action(async (sprintId: string, options) => {
    const spinner = ora("Loading sprint from Notion...").start();

    try {
      const sprint = await getSprint(sprintId);
      if (!sprint) {
        spinner.fail(`Sprint not found: ${sprintId}`);
        process.exit(1);
      }

      // Get the linked epic
      let epic = null;
      if (sprint.epicIds.length > 0) {
        epic = await getEpic(sprint.epicIds[0]);
      }

      spinner.stop();

      const sprintBranchName = generateSprintBranchName(sprint, epic);
      const epicBranchName = epic ? generateEpicBranchName(epic) : "main";

      console.log(chalk.blue("\n=== Merge Sprint ===\n"));
      console.log(chalk.yellow("Sprint:"), sprint.name);
      console.log(chalk.yellow("Branch:"), chalk.cyan(sprintBranchName));
      console.log(chalk.yellow("Target:"), epicBranchName);

      if (options.dryRun) {
        console.log(chalk.gray("\n[Dry run - no changes made]"));
        console.log(chalk.gray("Would:"));
        console.log(
          chalk.gray(`  1. git checkout ${epicBranchName} && git pull`),
        );
        console.log(chalk.gray(`  2. git merge ${sprintBranchName} --no-ff`));
        console.log(chalk.gray(`  3. git push origin ${epicBranchName}`));
        console.log(chalk.gray('  4. Update sprint status to "Completed"'));
        return;
      }

      // Checkout epic branch and pull
      spinner.start(`Updating ${epicBranchName} branch...`);
      await exec(`git checkout ${epicBranchName} && git pull`);
      spinner.succeed(`${epicBranchName} branch updated`);

      // Merge sprint branch
      spinner.start(`Merging ${sprintBranchName} to ${epicBranchName}...`);
      await exec(
        `git merge ${sprintBranchName} --no-ff -m "Merge sprint: ${sprint.name}"`,
      );
      spinner.succeed(`Merged ${sprintBranchName}`);

      // Push to epic branch
      spinner.start(`Pushing to ${epicBranchName}...`);
      await exec(`git push origin ${epicBranchName}`);
      spinner.succeed(`Pushed to ${epicBranchName}`);

      // Update sprint status in Notion
      spinner.start("Updating sprint status in Notion...");
      await updateSprintStatus(sprint.id, "Completed");
      spinner.succeed("Sprint status updated to Completed");

      console.log(chalk.green("\nSprint merged successfully!"));
      console.log(
        chalk.gray(
          `\nThe sprint branch '${sprintBranchName}' can now be deleted if desired.`,
        ),
      );
      if (epic) {
        console.log(
          chalk.gray(
            `When the epic is complete, run 'lw epic merge ${epic.shortId}'`,
          ),
        );
      }
    } catch (err) {
      spinner.fail("Failed to merge sprint");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });
