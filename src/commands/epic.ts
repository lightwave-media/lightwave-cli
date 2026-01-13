/**
 * Epic/Project management commands
 *
 * Commands:
 *   lw epic list              List epics from Notion/createOS
 *   lw epic info <id>         Show epic details with tasks and docs
 *   lw epic start <id>        Create epic branch from main
 *   lw epic merge <id>        Merge epic to main, tag release
 */

import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import {
  queryEpics,
  getEpic,
  queryTasks,
  querySprints,
  generateEpicBranchName,
  updateEpicStatus,
  createEpic,
  findLifeDomainByName,
} from "../utils/notion.js";
import {
  getBackend,
  queryEpicsDjango,
  getEpicDjango,
  queryTasksDjango,
  querySprintsDjango,
  createEpicDjango,
  updateEpicStatusDjango,
  findDomainByNameDjango,
} from "../utils/createos.js";
import { exec } from "../utils/exec.js";
import type { EpicStatus, NotionEpic } from "../types/notion.js";

/**
 * Resolve which backend to use
 */
function resolveBackend(optionBackend?: string): "django" | "notion" {
  if (optionBackend) {
    return optionBackend.toLowerCase() === "notion" ? "notion" : "django";
  }
  const defaultBackend = getBackend();
  return defaultBackend === "both" ? "django" : defaultBackend;
}

export const epicCommand = new Command("epic").description(
  "Epic/Project management - view and track epics",
);

/**
 * Get chalk color for epic status
 */
function getStatusColor(status: EpicStatus) {
  const colors: Record<EpicStatus, (s: string) => string> = {
    "Not Started": chalk.gray,
    "In Progress": chalk.magenta,
    Completed: chalk.green,
    "On Hold": chalk.yellow,
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
// lw epic list
// =============================================================================

epicCommand
  .command("list")
  .description("List epics/projects")
  .option(
    "--status <status>",
    "Filter by status (Not Started, In Progress, Completed, On Hold, Cancelled)",
  )
  .option("--all", "Show all statuses")
  .option(
    "--domain <name>",
    "Filter by Life Domain (e.g., 'Product Development')",
  )
  .option("--limit <n>", "Max number of epics", "20")
  .option("--format <format>", "Output format: table, json", "table")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: from LW_BACKEND env)",
  )
  .action(async (options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Fetching epics from ${backendLabel}...`).start();

    try {
      // Parse status filter
      let statusFilter: EpicStatus[] | undefined;
      if (options.status) {
        statusFilter = options.status
          .split(",")
          .map((s: string) => s.trim() as EpicStatus);
      } else if (!options.all) {
        // Default: show active epics
        statusFilter = ["In Progress", "Not Started"];
      }

      let epics: NotionEpic[];
      if (backend === "django") {
        // Django expects single status for now
        const djangoStatus = statusFilter?.[0];
        epics = await queryEpicsDjango({
          status: djangoStatus,
          domain: options.domain,
          limit: parseInt(options.limit, 10),
        });
      } else {
        epics = await queryEpics({
          status: statusFilter,
          domain: options.domain,
          limit: parseInt(options.limit, 10),
        });
      }

      spinner.stop();

      if (epics.length === 0) {
        console.log(chalk.yellow("No epics found matching criteria."));
        return;
      }

      if (options.format === "json") {
        console.log(JSON.stringify(epics, null, 2));
        return;
      }

      // Table format
      console.log(chalk.blue("\n=== Epics/Projects ===\n"));
      console.log(
        chalk.gray(
          `${"ID".padEnd(10)} ${"Status".padEnd(14)} ${"Type".padEnd(15)} ${"Name".padEnd(45)}`,
        ),
      );
      console.log(chalk.gray("-".repeat(86)));

      for (const epic of epics) {
        const statusColor = getStatusColor(epic.status);
        const projectType = epic.projectType || "-";
        const truncatedName =
          epic.name.length > 43
            ? epic.name.substring(0, 43) + "..."
            : epic.name;
        console.log(
          `${chalk.cyan(epic.shortId.padEnd(10))} ` +
            `${statusColor(epic.status.padEnd(14))} ` +
            `${chalk.gray(projectType.padEnd(15))} ` +
            `${truncatedName}`,
        );
      }

      console.log(chalk.gray(`\n${epics.length} epic(s) shown`));
    } catch (err) {
      spinner.fail("Failed to fetch epics");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw epic create <name>
// =============================================================================

epicCommand
  .command("create <name>")
  .description("Create a new epic/project")
  .option("--subtitle <text>", "Epic subtitle")
  .option("--log-line <text>", "Epic log line / description")
  .option(
    "--type <type>",
    "Project type (Software, Creative, Business, Personal)",
  )
  .option(
    "--priority <priority>",
    "Priority (1st Priority, 2nd Priority, 3rd Priority)",
  )
  .option("--domain <name>", "Life Domain to link to")
  .option("--start <date>", "Start date (YYYY-MM-DD)")
  .option("--end <date>", "End date (YYYY-MM-DD)")
  .option("--dry-run", "Preview what would be created")
  .option("--start-work", "Also create branch and set to In Progress")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: from LW_BACKEND env)",
  )
  .action(async (name: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Creating epic in ${backendLabel}...`).start();

    try {
      // Resolve life domain if provided
      let lifeDomainId: string | undefined;
      if (options.domain) {
        spinner.text = "Resolving life domain...";
        const domain =
          backend === "django"
            ? await findDomainByNameDjango(options.domain)
            : await findLifeDomainByName(options.domain);
        if (!domain) {
          spinner.fail(`Life domain not found: ${options.domain}`);
          process.exit(1);
        }
        lifeDomainId = domain.id;
      }

      if (options.dryRun) {
        spinner.stop();
        console.log(chalk.blue("\n=== Preview Epic ===\n"));
        console.log(chalk.yellow("Backend:"), backendLabel);
        console.log(chalk.yellow("Name:"), name);
        if (options.subtitle)
          console.log(chalk.yellow("Subtitle:"), options.subtitle);
        if (options.logLine)
          console.log(chalk.yellow("Log Line:"), options.logLine);
        if (options.type) console.log(chalk.yellow("Type:"), options.type);
        if (options.priority)
          console.log(chalk.yellow("Priority:"), options.priority);
        if (options.domain)
          console.log(chalk.yellow("Domain:"), options.domain);
        if (options.start) console.log(chalk.yellow("Start:"), options.start);
        if (options.end) console.log(chalk.yellow("End:"), options.end);
        console.log(chalk.gray("\n(dry run - no changes made)"));
        return;
      }

      let epic: NotionEpic;
      if (backend === "django") {
        epic = await createEpicDjango(name, {
          subtitle: options.subtitle,
          logLine: options.logLine,
          projectType: options.type,
          priority: options.priority,
          lifeDomainId,
          startDate: options.start,
          endDate: options.end,
        });
      } else {
        epic = await createEpic(name, {
          subtitle: options.subtitle,
          logLine: options.logLine,
          projectType: options.type,
          priority: options.priority,
          lifeDomainId,
          startDate: options.start,
          endDate: options.end,
        });
      }

      spinner.succeed("Epic created!");

      console.log(chalk.blue("\n=== Epic Created ===\n"));
      console.log(chalk.yellow("ID:"), chalk.cyan(epic.shortId));
      console.log(chalk.yellow("Name:"), epic.name);
      console.log(
        chalk.yellow("Status:"),
        getStatusColor(epic.status)(epic.status),
      );
      console.log(chalk.yellow("URL:"), epic.url);

      // Optionally start work on the epic
      if (options.startWork) {
        const branchName = generateEpicBranchName(epic);

        spinner.start("Creating epic branch...");
        await exec("git checkout main && git pull");
        await exec(`git checkout -b ${branchName}`);
        await exec(`git push -u origin ${branchName}`);
        if (backend === "django") {
          await updateEpicStatusDjango(epic.id, "In Progress");
        } else {
          await updateEpicStatus(epic.id, "In Progress");
        }
        spinner.succeed(`Branch created: ${branchName}`);

        console.log(chalk.yellow("\nBranch:"), chalk.cyan(branchName));
      } else {
        console.log(
          chalk.gray(`\nTo start work: lw epic start ${epic.shortId}`),
        );
      }
    } catch (err) {
      spinner.fail("Failed to create epic");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw epic info <id>
// =============================================================================

epicCommand
  .command("info <epic-id>")
  .description("Show detailed epic information with tasks and documents")
  .option("--format <format>", "Output format: text, json", "text")
  .option("--tasks", "Also show tasks in this epic")
  .option("--sprints", "Also show sprints linked to this epic")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: from LW_BACKEND env)",
  )
  .action(async (epicId: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading epic from ${backendLabel}...`).start();

    try {
      const epic =
        backend === "django"
          ? await getEpicDjango(epicId)
          : await getEpic(epicId);
      if (!epic) {
        spinner.fail(`Epic not found: ${epicId}`);
        process.exit(1);
      }

      spinner.stop();

      if (options.format === "json") {
        console.log(JSON.stringify(epic, null, 2));
        return;
      }

      displayEpicDetails(epic);

      // Optionally show tasks
      if (options.tasks) {
        await displayEpicTasks(epic, backend);
      }

      // Optionally show sprints
      if (options.sprints) {
        await displayEpicSprints(epic, backend);
      }
    } catch (err) {
      spinner.fail("Failed to load epic");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

/**
 * Display epic details in formatted text
 */
function displayEpicDetails(epic: NotionEpic): void {
  console.log(chalk.blue("\n=== Epic Details ===\n"));
  console.log(chalk.yellow("ID:"), epic.shortId);
  console.log(chalk.yellow("Full ID:"), epic.id);
  console.log(chalk.yellow("Name:"), epic.name);
  console.log(
    chalk.yellow("Status:"),
    getStatusColor(epic.status)(epic.status),
  );

  if (epic.subtitle) {
    console.log(chalk.yellow("Subtitle:"), epic.subtitle);
  }

  if (epic.projectType) {
    console.log(chalk.yellow("Type:"), epic.projectType);
  }

  if (epic.priority) {
    console.log(chalk.yellow("Priority:"), epic.priority);
  }

  console.log(
    chalk.yellow("Timeline:"),
    formatDateRange(epic.startDate, epic.endDate),
  );

  if (epic.totalStoryPoints !== null) {
    console.log(chalk.yellow("Story Points:"), epic.totalStoryPoints);
  }

  console.log(chalk.yellow("URL:"), epic.url);

  if (epic.githubRepoLink) {
    console.log(chalk.yellow("GitHub:"), epic.githubRepoLink);
  }

  if (epic.logLine) {
    console.log(chalk.yellow("\nLog Line:"));
    console.log(chalk.gray(epic.logLine));
  }

  console.log(chalk.yellow("\nLinked Items:"));
  console.log(chalk.gray(`  Sprints: ${epic.sprintIds.length}`));
  console.log(chalk.gray(`  Tasks: ${epic.taskIds.length}`));
  console.log(chalk.gray(`  User Stories: ${epic.userStoryIds.length}`));
  console.log(chalk.gray(`  Documents: ${epic.documentIds.length}`));
}

/**
 * Display tasks in an epic
 */
async function displayEpicTasks(
  epic: NotionEpic,
  backend: "django" | "notion" = "django",
): Promise<void> {
  const spinner = ora("Loading epic tasks...").start();

  try {
    const tasks =
      backend === "django"
        ? await queryTasksDjango({ epic: epic.shortId })
        : await queryTasks({ epic: epic.name });
    spinner.stop();

    if (tasks.length === 0) {
      console.log(chalk.gray("\nNo tasks in this epic."));
      return;
    }

    console.log(chalk.blue(`\n=== Tasks in Epic (${tasks.length}) ===\n`));
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
    spinner.fail("Failed to load epic tasks");
    console.error(chalk.red((err as Error).message));
  }
}

/**
 * Display sprints linked to an epic
 */
async function displayEpicSprints(
  epic: NotionEpic,
  backend: "django" | "notion" = "django",
): Promise<void> {
  const spinner = ora("Loading epic sprints...").start();

  try {
    let epicSprints;
    if (backend === "django") {
      // Django API supports direct epic filtering
      epicSprints = await querySprintsDjango({ epic: epic.shortId });
    } else {
      // Query sprints - we'll need to filter by those linked to this epic
      const allSprints = await querySprints({ limit: 100 });
      epicSprints = allSprints.filter((s) => s.epicIds.includes(epic.id));
    }

    spinner.stop();

    if (epicSprints.length === 0) {
      console.log(chalk.gray("\nNo sprints linked to this epic."));
      return;
    }

    console.log(
      chalk.blue(`\n=== Sprints in Epic (${epicSprints.length}) ===\n`),
    );
    console.log(
      chalk.gray(
        `${"ID".padEnd(10)} ${"Status".padEnd(14)} ${"Dates".padEnd(26)} ${"Name".padEnd(30)}`,
      ),
    );
    console.log(chalk.gray("-".repeat(82)));

    for (const sprint of epicSprints) {
      const dateRange = formatDateRange(sprint.startDate, sprint.endDate);
      const truncatedName =
        sprint.name.length > 28
          ? sprint.name.substring(0, 28) + "..."
          : sprint.name;
      console.log(
        `${chalk.cyan(sprint.shortId.padEnd(10))} ` +
          `${sprint.status.padEnd(14)} ` +
          `${chalk.gray(dateRange.padEnd(26))} ` +
          `${truncatedName}`,
      );
    }
  } catch (err) {
    spinner.fail("Failed to load epic sprints");
    console.error(chalk.red((err as Error).message));
  }
}

// =============================================================================
// lw epic start <id>
// =============================================================================

epicCommand
  .command("start <epic-id>")
  .description("Create epic branch from main and set status to In Progress")
  .option("--dry-run", "Show what would be done without making changes")
  .option("--no-push", "Create branch locally without pushing")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: from LW_BACKEND env)",
  )
  .action(async (epicId: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading epic from ${backendLabel}...`).start();

    try {
      const epic =
        backend === "django"
          ? await getEpicDjango(epicId)
          : await getEpic(epicId);
      if (!epic) {
        spinner.fail(`Epic not found: ${epicId}`);
        process.exit(1);
      }

      spinner.stop();

      const branchName = generateEpicBranchName(epic);

      console.log(chalk.blue("\n=== Start Epic ===\n"));
      console.log(chalk.yellow("Epic:"), epic.name);
      console.log(chalk.yellow("ID:"), epic.shortId);
      console.log(chalk.yellow("Branch:"), chalk.cyan(branchName));

      if (options.dryRun) {
        console.log(chalk.gray("\n[Dry run - no changes made]"));
        console.log(chalk.gray("Would:"));
        console.log(chalk.gray("  1. git checkout main && git pull"));
        console.log(chalk.gray(`  2. git checkout -b ${branchName}`));
        if (options.push !== false) {
          console.log(chalk.gray(`  3. git push -u origin ${branchName}`));
        }
        console.log(chalk.gray('  4. Update epic status to "In Progress"'));
        return;
      }

      // Ensure we're on main and up to date
      spinner.start("Updating main branch...");
      await exec("git checkout main && git pull");
      spinner.succeed("Main branch updated");

      // Create epic branch
      spinner.start(`Creating branch ${branchName}...`);
      await exec(`git checkout -b ${branchName}`);
      spinner.succeed(`Created branch ${branchName}`);

      // Push to remote (unless --no-push)
      if (options.push !== false) {
        spinner.start("Pushing to remote...");
        await exec(`git push -u origin ${branchName}`);
        spinner.succeed("Pushed to remote");
      }

      // Update epic status
      spinner.start(`Updating epic status in ${backendLabel}...`);
      if (backend === "django") {
        await updateEpicStatusDjango(epic.id, "In Progress");
      } else {
        await updateEpicStatus(epic.id, "In Progress");
      }
      spinner.succeed("Epic status updated to In Progress");

      console.log(chalk.green("\nEpic started successfully!"));
      console.log(
        chalk.gray(
          `\nNext: Create a sprint with 'lw sprint start <sprint-id>'`,
        ),
      );
    } catch (err) {
      spinner.fail("Failed to start epic");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw epic merge <id>
// =============================================================================

epicCommand
  .command("merge <epic-id>")
  .description("Merge epic branch to main and tag release")
  .option("--dry-run", "Show what would be done without making changes")
  .option("--no-tag", "Skip creating a release tag")
  .option("--tag-version <version>", "Specify version for tag (e.g., 1.2.0)")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: from LW_BACKEND env)",
  )
  .action(async (epicId: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading epic from ${backendLabel}...`).start();

    try {
      const epic =
        backend === "django"
          ? await getEpicDjango(epicId)
          : await getEpic(epicId);
      if (!epic) {
        spinner.fail(`Epic not found: ${epicId}`);
        process.exit(1);
      }

      spinner.stop();

      const branchName = generateEpicBranchName(epic);

      // Generate tag name
      const version = options.tagVersion || `1.0.0-${epic.shortId}`;
      const tagName = `v${version}`;

      console.log(chalk.blue("\n=== Merge Epic ===\n"));
      console.log(chalk.yellow("Epic:"), epic.name);
      console.log(chalk.yellow("Branch:"), chalk.cyan(branchName));
      console.log(chalk.yellow("Target:"), "main");
      if (options.tag !== false) {
        console.log(chalk.yellow("Tag:"), chalk.green(tagName));
      }

      if (options.dryRun) {
        console.log(chalk.gray("\n[Dry run - no changes made]"));
        console.log(chalk.gray("Would:"));
        console.log(chalk.gray("  1. git checkout main && git pull"));
        console.log(chalk.gray(`  2. git merge ${branchName} --no-ff`));
        console.log(chalk.gray("  3. git push origin main"));
        if (options.tag !== false) {
          console.log(
            chalk.gray(`  4. git tag -a ${tagName} -m "Epic: ${epic.name}"`),
          );
          console.log(chalk.gray(`  5. git push origin ${tagName}`));
        }
        console.log(chalk.gray('  6. Update epic status to "Completed"'));
        return;
      }

      // Checkout main and pull
      spinner.start("Updating main branch...");
      await exec("git checkout main && git pull");
      spinner.succeed("Main branch updated");

      // Merge epic branch
      spinner.start(`Merging ${branchName} to main...`);
      await exec(
        `git merge ${branchName} --no-ff -m "Merge epic: ${epic.name}"`,
      );
      spinner.succeed(`Merged ${branchName}`);

      // Push to main
      spinner.start("Pushing to main...");
      await exec("git push origin main");
      spinner.succeed("Pushed to main");

      // Create and push tag (unless --no-tag)
      if (options.tag !== false) {
        spinner.start(`Creating tag ${tagName}...`);
        await exec(`git tag -a ${tagName} -m "Epic: ${epic.name}"`);
        await exec(`git push origin ${tagName}`);
        spinner.succeed(`Created and pushed tag ${tagName}`);
      }

      // Update epic status
      spinner.start(`Updating epic status in ${backendLabel}...`);
      if (backend === "django") {
        await updateEpicStatusDjango(epic.id, "Completed");
      } else {
        await updateEpicStatus(epic.id, "Completed");
      }
      spinner.succeed("Epic status updated to Completed");

      console.log(chalk.green("\nEpic merged successfully!"));
      console.log(
        chalk.gray(
          `\nThe epic branch '${branchName}' can now be deleted if desired.`,
        ),
      );
    } catch (err) {
      spinner.fail("Failed to merge epic");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });
