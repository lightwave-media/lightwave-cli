/**
 * Task management commands - bridges createOS (Django) with Git workflow
 *
 * Commands:
 *   lw task list              List tasks (from createOS/Django)
 *   lw task info <id>         Show task details
 *   lw task create <title>    Create new task
 *   lw task start <id>        Create branch, update status -> "Active (In progress)"
 *   lw task status <id> <s>   Update task status
 *   lw task done <id>         Mark task as Archived
 *   lw task pr <id>           Create PR with task links
 *   lw task commit <id>       Stage and commit with formatted message
 *
 * Backend Selection:
 *   --backend django|notion   Select backend (default: django)
 *   LW_BACKEND=notion         Environment variable to use Notion instead
 */

import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import { writeFileSync, unlinkSync } from "fs";
import { tmpdir } from "os";
import { join } from "path";
import { exec } from "../utils/exec.js";
import {
  queryTasks,
  getTask,
  updateTaskStatus,
  updateTask,
  updateTaskPriority,
  assignTaskToEpic,
  assignTaskToSprint,
  deleteTask,
  generateBranchName,
  generateCommitMessage,
  createTask,
  getEpic,
  getSprint,
  queryEpics,
  querySprints,
  getTaskContext,
} from "../utils/notion.js";
import {
  getBackend,
  queryTasksDjango,
  getTaskDjango,
  updateTaskStatusDjango,
  updateTaskDjango,
  createTaskDjango,
  startTaskDjango,
  doneTaskDjango,
  deleteTaskDjango,
  updateTaskPriorityDjango,
  assignTaskToEpicDjango,
  assignTaskToSprintDjango,
  getEpicDjango,
  getSprintDjango,
} from "../utils/createos.js";
import { getViewByName, queryViews } from "../utils/views.js";
import {
  VALID_STATUSES,
  STATUS_ALIASES,
  VALID_PRIORITIES,
  PRIORITY_ALIASES,
} from "../types/notion.js";
import type {
  NotionTaskStatus,
  TaskPriority,
  AgentStatus,
  AssignedAgent,
  NotionTaskType,
  NotionTask,
} from "../types/notion.js";

import { pushTaskUpdate } from "../utils/taskSync.js";

/**
 * Resolve backend from command option or environment.
 * Default is Django (createOS) as the Single Source of Truth.
 */
function resolveBackend(optionBackend?: string): "django" | "notion" {
  if (optionBackend) {
    if (optionBackend === "notion") {
      return "notion";
    }
    return "django"; // django, createos, or any other value defaults to django
  }
  const envBackend = getBackend();
  // Default to django unless explicitly set to notion
  return envBackend === "notion" ? "notion" : "django";
}

/**
 * Resolve status alias to actual Notion status
 */
function resolveStatus(input: string): NotionTaskStatus | null {
  // Check exact match first
  if (VALID_STATUSES.includes(input as NotionTaskStatus)) {
    return input as NotionTaskStatus;
  }
  // Check aliases
  const alias = STATUS_ALIASES[input.toLowerCase()];
  if (alias) {
    return alias;
  }
  return null;
}

/**
 * Resolve priority alias to actual Notion priority
 */
function resolvePriority(input: string): TaskPriority | null {
  // Check exact match first
  if (VALID_PRIORITIES.includes(input as TaskPriority)) {
    return input as TaskPriority;
  }
  // Check aliases
  const alias = PRIORITY_ALIASES[input.toLowerCase()];
  if (alias) {
    return alias;
  }
  return null;
}

export const taskCommand = new Command("task").description(
  "Task management - createOS (Django) is the source of truth",
);

/**
 * Get chalk color for status
 */
function getStatusColor(status: NotionTaskStatus) {
  const colors: Record<NotionTaskStatus, (s: string) => string> = {
    "On Hold": chalk.yellow,
    "Active (Approved for work)": chalk.blue,
    "Next Up": chalk.cyan,
    Future: chalk.gray,
    "Active (In progress)": chalk.magenta,
    "Active (In Review)": chalk.blue,
    Archived: chalk.green,
    Cancelled: chalk.gray,
  };
  return colors[status] || chalk.white;
}

// =============================================================================
// lw task list
// =============================================================================

taskCommand
  .command("list")
  .description("List tasks (filterable by status, domain, epic, view)")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: django)",
  )
  .option(
    "--status <status>",
    "Filter by status (comma-separated for multiple)",
  )
  .option("--all", "Show all statuses including waiting/future")
  .option(
    "--domain <name>",
    "Filter by Life Domain (e.g., 'Product Development')",
  )
  .option("--epic <name>", "Filter by Epic/Project name or ID")
  .option("--sprint <name>", "Filter by Sprint name or ID")
  .option("--user-story <name>", "Filter by User Story name or ID")
  // New view-based filtering
  .option("--view <name>", "Use a named view from CLI Views database")
  // New property filters
  .option(
    "--priority <priority>",
    "Filter by priority (1st, 2nd, 3rd or alias)",
  )
  .option(
    "--task-type <type>",
    "Filter by Task Type (Software Dev, General, etc.)",
  )
  .option("--agent-status <status>", "Filter by Agent Status")
  .option(
    "--assigned-agent <agent>",
    "Filter by Assigned Agent (v_core, v_senior_developer, etc.)",
  )
  .option("--assignee <name>", "Filter by human assignee name")
  // Date filters
  .option("--due-before <date>", "Filter tasks due before date (ISO 8601)")
  .option("--due-after <date>", "Filter tasks due after date (ISO 8601)")
  // Hierarchy filters
  .option("--has-subtasks", "Only show tasks with subtasks")
  .option("--is-parent", "Only show parent tasks")
  .option("--parent <task-id>", "Filter by parent task ID")
  // Orphan filter
  .option(
    "--orphan",
    "Show only tasks with no epic, sprint, or user story linked",
  )
  .option("--limit <n>", "Max number of tasks", "20")
  .option("--format <format>", "Output format: table, json", "table")
  .action(async (options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Fetching tasks from ${backendLabel}...`).start();

    try {
      // Handle view-based filtering
      if (options.view) {
        spinner.text = `Loading view "${options.view}"...`;
        try {
          const view = await getViewByName(options.view);
          if (!view) {
            spinner.fail(`View not found: "${options.view}"`);
            console.log(
              chalk.gray("\nUse 'lw task views' to list available views."),
            );
            process.exit(1);
          }
          spinner.text = `Applying view "${view.name}"...`;
          // View-based filtering uses the stored filter directly
          // For now, we'll just inform user that view filtering requires CLI Views DB setup
          spinner.info(`View "${view.name}" found - filter will be applied`);
        } catch (err) {
          const error = err as Error;
          if (error.message.includes("not configured")) {
            spinner.warn("CLI Views database not configured yet.");
            console.log(chalk.gray("Falling back to standard filters."));
          } else {
            throw err;
          }
        }
      }

      // Parse status filter
      let statusFilter: NotionTaskStatus[] | undefined;
      if (options.status) {
        statusFilter = options.status
          .split(",")
          .map(
            (s: string) =>
              resolveStatus(s.trim()) || (s.trim() as NotionTaskStatus),
          );
      } else if (!options.all && !options.view) {
        // Default: show actionable statuses (unless using a view)
        statusFilter = [
          "Active (Approved for work)",
          "Next Up",
          "Active (In progress)",
        ];
      }

      // Resolve priority alias if provided
      let priorityFilter: string | undefined;
      if (options.priority) {
        const resolved = resolvePriority(options.priority);
        priorityFilter = resolved || options.priority;
      }

      const queryOptions = {
        status: statusFilter,
        limit: options.orphan ? 200 : parseInt(options.limit, 10), // Fetch more for client-side orphan filtering
        domain: options.domain,
        epic: options.epic,
        sprint: options.sprint,
        userStory: options.userStory,
        // New property filters
        priority: priorityFilter,
        taskType: options.taskType as NotionTaskType | undefined,
        agentStatus: options.agentStatus as AgentStatus | undefined,
        assignedAgent: options.assignedAgent as AssignedAgent | undefined,
        assignee: options.assignee,
        // Date filters
        dueBefore: options.dueBefore,
        dueAfter: options.dueAfter,
        // Hierarchy filters
        hasSubtasks: options.hasSubtasks,
        isParent: options.isParent,
        parentTask: options.parent,
        // View filter (if configured)
        view: options.view,
      };

      // Use appropriate backend
      let tasks: NotionTask[] =
        backend === "django"
          ? await queryTasksDjango(queryOptions)
          : await queryTasks(queryOptions);

      // Filter orphan tasks (no epic, sprint, or user story linked)
      if (options.orphan) {
        tasks = tasks.filter(
          (t) =>
            !t.epicId &&
            !t.sprintId &&
            (!t.userStoryIds || t.userStoryIds.length === 0),
        );
        // Apply limit after orphan filter
        tasks = tasks.slice(0, parseInt(options.limit, 10));
      }

      spinner.stop();

      if (tasks.length === 0) {
        console.log(chalk.yellow("No tasks found matching criteria."));
        return;
      }

      if (options.format === "json") {
        console.log(JSON.stringify(tasks, null, 2));
        return;
      }

      // Table format
      console.log(chalk.blue(`\n=== Tasks (${backendLabel}) ===\n`));
      console.log(
        chalk.gray(
          `${"ID".padEnd(10)} ${"Status".padEnd(18)} ${"Title".padEnd(50)}`,
        ),
      );
      console.log(chalk.gray("-".repeat(80)));

      for (const task of tasks) {
        const statusColor = getStatusColor(task.status);
        const truncatedTitle =
          task.title.length > 48
            ? task.title.substring(0, 48) + "..."
            : task.title;
        console.log(
          `${chalk.cyan(task.shortId.padEnd(10))} ` +
            `${statusColor(task.status.padEnd(18))} ` +
            `${truncatedTitle}`,
        );
      }

      console.log(chalk.gray(`\n${tasks.length} task(s) shown`));
    } catch (err) {
      spinner.fail("Failed to fetch tasks");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw task views - List available CLI Views
// =============================================================================

taskCommand
  .command("views")
  .description("List available CLI Views from Notion")
  .option("--format <format>", "Output format: table, json", "table")
  .action(async (options) => {
    const spinner = ora("Fetching CLI Views from Notion...").start();

    try {
      const views = await queryViews({ database: "Tasks", activeOnly: true });

      spinner.stop();

      if (views.length === 0) {
        console.log(chalk.yellow("No views found."));
        console.log(
          chalk.gray("\nCreate views in the CLI Views database in Notion."),
        );
        console.log(
          chalk.gray("See scripts/create-views-db.ts for setup instructions."),
        );
        return;
      }

      if (options.format === "json") {
        console.log(JSON.stringify(views, null, 2));
        return;
      }

      // Table format
      console.log(chalk.blue("\n=== Available Task Views ===\n"));
      console.log(
        chalk.gray(`${"Name".padEnd(25)} ${"Description".padEnd(50)}`),
      );
      console.log(chalk.gray("-".repeat(77)));

      for (const view of views) {
        const description = view.description || "(no description)";
        const truncatedDesc =
          description.length > 48
            ? description.substring(0, 48) + "..."
            : description;
        console.log(
          `${chalk.cyan(view.name.padEnd(25))} ` +
            `${chalk.gray(truncatedDesc)}`,
        );
      }

      console.log(chalk.gray(`\n${views.length} view(s) available`));
      console.log(chalk.gray('\nUsage: lw task list --view "<view name>"'));
    } catch (err) {
      const error = err as Error;
      if (error.message.includes("not configured")) {
        spinner.warn("CLI Views database not configured.");
        console.log(chalk.gray("\nTo enable view-based filtering:"));
        console.log(chalk.gray("1. Create the CLI Views database in Notion"));
        console.log(
          chalk.gray("2. Update NOTION_DB_IDS.cliViews in types/notion.ts"),
        );
        console.log(
          chalk.gray("\nSee scripts/create-views-db.ts for details."),
        );
      } else {
        spinner.fail("Failed to fetch views");
        console.error(chalk.red(error.message));
        process.exit(1);
      }
    }
  });

// =============================================================================
// lw task info <id>
// =============================================================================

taskCommand
  .command("info <task-id>")
  .description("Show detailed task information")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: django)",
  )
  .option("--format <format>", "Output format: text, json", "text")
  .option("--context", "Show Epic, Sprint, and Document context")
  .action(async (taskId: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading task from ${backendLabel}...`).start();

    try {
      const task =
        backend === "django"
          ? await getTaskDjango(taskId)
          : await getTask(taskId);
      if (!task) {
        spinner.fail(`Task not found: ${taskId}`);
        process.exit(1);
      }

      spinner.stop();

      if (options.format === "json") {
        console.log(JSON.stringify(task, null, 2));
        return;
      }

      console.log(chalk.blue(`\n=== Task Details (${backendLabel}) ===\n`));
      console.log(chalk.yellow("ID:"), task.shortId);
      console.log(chalk.yellow("Full ID:"), task.id);
      console.log(chalk.yellow("Title:"), task.title);
      console.log(
        chalk.yellow("Status:"),
        getStatusColor(task.status)(task.status),
      );
      console.log(chalk.yellow("Type:"), task.taskType);
      if (task.priority) {
        console.log(chalk.yellow("Priority:"), task.priority);
      }
      console.log(chalk.yellow("URL:"), task.url);
      console.log(chalk.yellow("Created:"), task.createdTime);
      console.log(chalk.yellow("Updated:"), task.lastEditedTime);

      if (task.description) {
        console.log(chalk.yellow("\nDescription:"));
        console.log(chalk.gray(task.description));
      }

      if (task.acceptanceCriteria) {
        console.log(chalk.yellow("\nAcceptance Criteria:"));
        console.log(chalk.gray(task.acceptanceCriteria));
      }

      // Show context (Epic, Sprint) if requested or if IDs are present
      if (options.context || task.epicId || task.sprintId) {
        console.log(chalk.blue("\n=== Context ===\n"));

        // Show Epic context
        if (task.epicId) {
          const epicSpinner = ora("Loading epic...").start();
          try {
            const epic =
              backend === "django"
                ? await getEpicDjango(task.epicId)
                : await getEpic(task.epicId);
            epicSpinner.stop();
            if (epic) {
              console.log(chalk.yellow("Epic:"), chalk.cyan(epic.name));
              console.log(chalk.gray(`  ID: ${epic.shortId}`));
              console.log(chalk.gray(`  Status: ${epic.status}`));
              if (epic.githubRepoLink) {
                console.log(chalk.gray(`  GitHub: ${epic.githubRepoLink}`));
              }
            }
          } catch {
            epicSpinner.stop();
          }
        } else {
          console.log(chalk.yellow("Epic:"), chalk.gray("(none)"));
        }

        // Show Sprint context
        if (task.sprintId) {
          const sprintSpinner = ora("Loading sprint...").start();
          try {
            const sprint =
              backend === "django"
                ? await getSprintDjango(task.sprintId)
                : await getSprint(task.sprintId);
            sprintSpinner.stop();
            if (sprint) {
              console.log(chalk.yellow("Sprint:"), chalk.cyan(sprint.name));
              console.log(chalk.gray(`  ID: ${sprint.shortId}`));
              console.log(chalk.gray(`  Status: ${sprint.status}`));
              if (sprint.startDate) {
                const endStr = sprint.endDate ? ` → ${sprint.endDate}` : "";
                console.log(
                  chalk.gray(`  Dates: ${sprint.startDate}${endStr}`),
                );
              }
            }
          } catch {
            sprintSpinner.stop();
          }
        } else {
          console.log(chalk.yellow("Sprint:"), chalk.gray("(none)"));
        }

        // Show document count if available
        if (task.documentIds && task.documentIds.length > 0) {
          console.log(
            chalk.yellow("Documents:"),
            chalk.cyan(`${task.documentIds.length} linked`),
          );
        }
      }

      // Show suggested branch name
      const branchName = generateBranchName(task);
      console.log(chalk.yellow("\nSuggested Branch:"), chalk.cyan(branchName));
    } catch (err) {
      spinner.fail("Failed to load task");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw task context <id>
// =============================================================================

taskCommand
  .command("context <task-id>")
  .description("Load full task context with linked documents (for Claude)")
  .option("--format <format>", "Output format: markdown, json", "markdown")
  .action(async (taskId: string, options) => {
    const spinner = ora("Loading task context from Notion...").start();

    try {
      const context = await getTaskContext(taskId);
      if (!context) {
        spinner.fail(`Task not found: ${taskId}`);
        process.exit(1);
      }

      spinner.stop();

      if (options.format === "json") {
        console.log(JSON.stringify(context, null, 2));
        return;
      }

      // Markdown format for Claude consumption
      const { task, epic, sprint, documents, layers } = context;

      console.log(`# Task Context: ${task.title}\n`);
      console.log(`_Layers loaded: ${layers.join(", ")}_\n`);

      // Task section
      console.log("## Task\n");
      console.log(`- **ID**: ${task.shortId}`);
      console.log(`- **Status**: ${task.status}`);
      if (task.priority) console.log(`- **Priority**: ${task.priority}`);
      console.log(`- **Type**: ${task.taskType}`);
      console.log(`- **URL**: ${task.url}`);

      if (task.description) {
        console.log("\n### Description\n");
        console.log(task.description);
      }

      if (task.acceptanceCriteria) {
        console.log("\n### Acceptance Criteria\n");
        console.log(task.acceptanceCriteria);
      }

      // Epic section
      if (epic) {
        console.log("\n## Epic\n");
        console.log(`- **Name**: ${epic.name}`);
        console.log(`- **ID**: ${epic.shortId}`);
        console.log(`- **Status**: ${epic.status}`);
        if (epic.projectType) console.log(`- **Type**: ${epic.projectType}`);
        if (epic.githubRepoLink)
          console.log(`- **GitHub**: ${epic.githubRepoLink}`);
        if (epic.logLine) {
          console.log("\n### Log Line\n");
          console.log(epic.logLine);
        }
      }

      // Sprint section
      if (sprint) {
        console.log("\n## Sprint\n");
        console.log(`- **Name**: ${sprint.name}`);
        console.log(`- **ID**: ${sprint.shortId}`);
        console.log(`- **Status**: ${sprint.status}`);
        if (sprint.startDate) {
          const endStr = sprint.endDate ? ` → ${sprint.endDate}` : "";
          console.log(`- **Dates**: ${sprint.startDate}${endStr}`);
        }
        if (sprint.objectives) {
          console.log("\n### Objectives\n");
          console.log(sprint.objectives);
        }
      }

      // Life Domain section
      if (context.lifeDomain) {
        console.log("\n## Life Domain\n");
        console.log(`- **Name**: ${context.lifeDomain.name}`);
        if (context.lifeDomain.type)
          console.log(`- **Type**: ${context.lifeDomain.type}`);
      }

      // Parent Task section
      if (context.parentTask) {
        console.log("\n## Parent Task\n");
        console.log(`- **Name**: ${context.parentTask.title}`);
        console.log(`- **ID**: ${context.parentTask.shortId}`);
        console.log(`- **Status**: ${context.parentTask.status}`);
        console.log(`- **URL**: ${context.parentTask.url}`);
      }

      // Subtasks section
      if (context.subtasks && context.subtasks.length > 0) {
        console.log("\n## Subtasks\n");
        for (const sub of context.subtasks) {
          console.log(`- **${sub.title}** (${sub.shortId}) - ${sub.status}`);
        }
      }

      // User Stories section
      if (context.userStories && context.userStories.length > 0) {
        console.log("\n## User Stories\n");
        for (const story of context.userStories) {
          console.log(
            `- **${story.name}** (${story.shortId}) - ${story.status}`,
          );
          if (story.userType) console.log(`  - User Type: ${story.userType}`);
        }
      }

      // Agent Info section (if task has agent data)
      if (task.agentStatus || task.assignedAgent || task.assignee) {
        console.log("\n## Assignment\n");
        if (task.assignee) console.log(`- **Assignee**: ${task.assignee}`);
        if (task.assignedAgent)
          console.log(`- **Assigned Agent**: ${task.assignedAgent}`);
        if (task.agentStatus)
          console.log(`- **Agent Status**: ${task.agentStatus}`);
      }

      // Due Date section
      if (task.dueDate || task.doDate) {
        console.log("\n## Scheduling\n");
        if (task.dueDate) console.log(`- **Due Date**: ${task.dueDate}`);
        if (task.doDate) console.log(`- **Do Date**: ${task.doDate}`);
      }

      // Documents section
      if (documents.length > 0) {
        console.log("\n## Linked Documents\n");
        for (const doc of documents) {
          console.log(`### ${doc.name} (${doc.shortId})\n`);
          if (doc.contentType) console.log(`_Type: ${doc.contentType}_`);
          if (doc.version) console.log(`_Version: ${doc.version}_`);
          console.log("");
          if (doc.content) {
            console.log(doc.content);
          }
          console.log("\n---\n");
        }
      }

      // Suggested branch
      const branchName = generateBranchName(task);
      console.log("## Suggested Branch\n");
      console.log(`\`${branchName}\``);
    } catch (err) {
      spinner.fail("Failed to load task context");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw task create
// =============================================================================

taskCommand
  .command("create <title>")
  .description("Create a new task")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: django)",
  )
  .option(
    "--priority <priority>",
    "Task priority (high, medium, low or 1, 2, 3)",
  )
  .option("--description <text>", "Task description/notes")
  .option("--epic <epic-id>", "Assign to epic")
  .option("--dry-run", "Preview what would be created")
  .option("--start", "Immediately start the task after creation")
  .action(async (title: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Creating task in ${backendLabel}...`).start();

    try {
      // Resolve priority if provided
      let priority: TaskPriority | undefined;
      if (options.priority) {
        priority = resolvePriority(options.priority) || undefined;
        if (!priority) {
          spinner.fail(`Invalid priority: ${options.priority}`);
          console.log(
            chalk.gray("Valid priorities: high, medium, low (or 1, 2, 3)"),
          );
          process.exit(1);
        }
      }

      if (options.dryRun) {
        spinner.stop();
        console.log(
          chalk.blue(`\n=== Task Creation Preview (${backendLabel}) ===\n`),
        );
        console.log(chalk.gray("Title:"), title);
        if (priority) console.log(chalk.gray("Priority:"), priority);
        if (options.description)
          console.log(chalk.gray("Description:"), options.description);
        if (options.epic) console.log(chalk.gray("Epic:"), options.epic);
        console.log(chalk.yellow("\n(dry run - no task created)"));
        return;
      }

      // Create the task with options
      const task =
        backend === "django"
          ? await createTaskDjango(title, {
              description: options.description,
              priority,
              epicId: options.epic,
            })
          : await createTask(title);
      spinner.succeed("Task created");

      console.log(chalk.blue(`\n=== New Task (${backendLabel}) ===\n`));
      console.log(chalk.yellow("ID:"), task.shortId);
      console.log(chalk.yellow("Full ID:"), task.id);
      console.log(chalk.yellow("Title:"), task.title);
      console.log(chalk.yellow("Status:"), task.status);
      console.log(chalk.yellow("URL:"), task.url);

      // Push to backend for WebSocket broadcast
      await pushTaskUpdate({
        id: task.id,
        short_id: task.shortId,
        title: task.title,
        status: task.status,
        task_type: task.taskType,
        url: task.url,
      });

      // If --start flag, immediately start the task
      if (options.start) {
        const branchName = generateBranchName(task);

        spinner.start("Creating branch and starting task...");

        // Create and checkout branch
        await exec("git", ["checkout", "-b", branchName]);

        // Push to remote
        await exec("git", ["push", "-u", "origin", branchName]);

        // Update status
        if (backend === "django") {
          await startTaskDjango(task.id);
        } else {
          await updateTaskStatus(task.id, "Active (In progress)");
        }
        spinner.succeed("Task started");

        // Push updated status
        await pushTaskUpdate({
          id: task.id,
          short_id: task.shortId,
          title: task.title,
          status: "Active (In progress)",
          task_type: task.taskType,
          branch: branchName,
          url: task.url,
        });

        console.log(chalk.green(`\n✓ Ready to work on task!`));
        console.log(chalk.gray(`  Branch: ${branchName}`));
      } else {
        console.log(
          chalk.gray(`\nRun: lw task start ${task.shortId} to begin work`),
        );
      }
    } catch (err) {
      spinner.fail("Failed to create task");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw task start <id>
// =============================================================================

taskCommand
  .command("start <task-id>")
  .description(
    "Start a task: creates branch, updates status to 'Active (In progress)'",
  )
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: django)",
  )
  .option("--dry-run", "Preview what would happen")
  .option("--no-push", "Don't push branch to remote")
  .option("--branch <name>", "Override auto-generated branch name")
  .action(async (taskId: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading task from ${backendLabel}...`).start();

    try {
      // 1. Fetch task from backend
      const task =
        backend === "django"
          ? await getTaskDjango(taskId)
          : await getTask(taskId);
      if (!task) {
        spinner.fail(`Task not found: ${taskId}`);
        process.exit(1);
      }

      spinner.text = `Found task: ${task.title}`;

      // 2. Generate branch name
      const branchName = options.branch || generateBranchName(task);

      // 3. Show plan
      spinner.stop();
      console.log(chalk.blue("\n=== Task Start Plan ===\n"));
      console.log(chalk.gray("Task:"), task.title);
      console.log(chalk.gray("ID:"), task.shortId);
      console.log(chalk.gray("Current Status:"), task.status);
      console.log(chalk.gray("Branch:"), chalk.cyan(branchName));
      console.log(chalk.gray("Notion URL:"), task.url);

      if (options.dryRun) {
        console.log(chalk.yellow("\n(dry run - no changes made)"));
        console.log(chalk.gray("\nWould execute:"));
        console.log(chalk.gray(`  git checkout -b ${branchName}`));
        if (options.push !== false) {
          console.log(chalk.gray(`  git push -u origin ${branchName}`));
        }
        console.log(
          chalk.gray(`  Update Notion status -> "Active (In progress)"`),
        );
        return;
      }

      // 4. Check if branch already exists
      const existingBranch = await exec(
        "git",
        ["branch", "--list", branchName],
        {
          silent: true,
        },
      );
      if (existingBranch.stdout.trim()) {
        console.log(
          chalk.yellow(
            `\nBranch ${branchName} already exists. Checking out...`,
          ),
        );
        await exec("git", ["checkout", branchName]);
      } else {
        // 5. Create and checkout branch
        spinner.start("Creating branch...");
        await exec("git", ["checkout", "-b", branchName]);
        spinner.succeed(`Created branch: ${branchName}`);

        // 6. Push to remote (unless --no-push)
        if (options.push !== false) {
          spinner.start("Pushing to remote...");
          await exec("git", ["push", "-u", "origin", branchName]);
          spinner.succeed("Pushed to remote");
        }
      }

      // 7. Update status in backend
      spinner.start(`Updating ${backendLabel} status...`);
      if (backend === "django") {
        await startTaskDjango(task.id);
      } else {
        await updateTaskStatus(task.id, "Active (In progress)");
      }
      spinner.succeed(
        `Updated ${backendLabel} status to 'Active (In progress)'`,
      );

      // 8. Push to backend for WebSocket broadcast
      await pushTaskUpdate({
        id: task.id,
        short_id: task.shortId,
        title: task.title,
        status: "Active (In progress)",
        task_type: task.taskType,
        branch: branchName,
        url: task.url,
      });

      console.log(chalk.green("\n✓ Ready to work on task!"));
      console.log(chalk.gray(`  Branch: ${branchName}`));
      console.log(chalk.gray(`  Notion: ${task.url}`));
    } catch (err) {
      spinner.fail("Failed to start task");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw task status <id> <status>
// =============================================================================

taskCommand
  .command("status <task-id> <status>")
  .description("Manually update task status")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: django)",
  )
  .option("--dry-run", "Preview without updating")
  .action(async (taskId: string, newStatus: string, options) => {
    // Resolve status (supports aliases like "in-progress" -> "Active (In progress)")
    const resolvedStatus = resolveStatus(newStatus);
    if (!resolvedStatus) {
      console.error(chalk.red(`Invalid status: ${newStatus}`));
      console.log(chalk.gray("Valid statuses:"), VALID_STATUSES.join(", "));
      console.log(
        chalk.gray("Aliases:"),
        Object.keys(STATUS_ALIASES).join(", "),
      );
      process.exit(1);
    }

    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading task from ${backendLabel}...`).start();

    try {
      const task =
        backend === "django"
          ? await getTaskDjango(taskId)
          : await getTask(taskId);
      if (!task) {
        spinner.fail(`Task not found: ${taskId}`);
        process.exit(1);
      }

      spinner.stop();
      console.log(
        chalk.blue(`\n=== Update Task Status (${backendLabel}) ===\n`),
      );
      console.log(chalk.gray("Task:"), task.title);
      console.log(chalk.gray("Current:"), task.status);
      console.log(chalk.gray("New:"), chalk.cyan(resolvedStatus));

      if (options.dryRun) {
        console.log(chalk.yellow("\n(dry run - no changes made)"));
        return;
      }

      spinner.start(`Updating ${backendLabel} status...`);
      if (backend === "django") {
        await updateTaskStatusDjango(task.id, resolvedStatus);
      } else {
        await updateTaskStatus(task.id, resolvedStatus);
      }
      spinner.succeed(`Status updated to "${resolvedStatus}"`);

      // Push to backend for WebSocket broadcast
      await pushTaskUpdate({
        id: task.id,
        short_id: task.shortId,
        title: task.title,
        status: resolvedStatus,
        task_type: task.taskType,
        url: task.url,
      });
    } catch (err) {
      spinner.fail("Failed to update status");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw task done <id>
// =============================================================================

taskCommand
  .command("done <task-id>")
  .description("Mark task as done")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: django)",
  )
  .option("--dry-run", "Preview without updating")
  .action(async (taskId: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading task from ${backendLabel}...`).start();

    try {
      const task =
        backend === "django"
          ? await getTaskDjango(taskId)
          : await getTask(taskId);
      if (!task) {
        spinner.fail(`Task not found: ${taskId}`);
        process.exit(1);
      }

      spinner.stop();
      console.log(chalk.blue(`\n=== Mark Task Done (${backendLabel}) ===\n`));
      console.log(chalk.gray("Task:"), task.title);
      console.log(chalk.gray("Current Status:"), task.status);
      console.log(chalk.gray("New Status:"), chalk.green("Archived"));

      if (options.dryRun) {
        console.log(chalk.yellow("\n(dry run - no changes made)"));
        return;
      }

      spinner.start(`Updating ${backendLabel} status...`);
      if (backend === "django") {
        await doneTaskDjango(task.id);
      } else {
        await updateTaskStatus(task.id, "Archived");
      }
      spinner.succeed("Task marked as done");

      // Push to backend for WebSocket broadcast
      await pushTaskUpdate({
        id: task.id,
        short_id: task.shortId,
        title: task.title,
        status: "Archived",
        task_type: task.taskType,
        url: task.url,
      });

      console.log(chalk.green(`\n✓ Task ${task.shortId} completed!`));
    } catch (err) {
      spinner.fail("Failed to update task");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw task delete <id>
// =============================================================================

taskCommand
  .command("delete <task-id>")
  .description("Delete (trash) a task")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: django)",
  )
  .option("--dry-run", "Preview without deleting")
  .action(async (taskId: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading task from ${backendLabel}...`).start();

    try {
      const task =
        backend === "django"
          ? await getTaskDjango(taskId)
          : await getTask(taskId);
      if (!task) {
        spinner.fail(`Task not found: ${taskId}`);
        process.exit(1);
      }

      spinner.stop();
      console.log(chalk.blue(`\n=== Delete Task (${backendLabel}) ===\n`));
      console.log(chalk.gray("Task:"), task.title);
      console.log(chalk.gray("ID:"), task.shortId);
      console.log(chalk.gray("Status:"), task.status);

      if (options.dryRun) {
        console.log(chalk.yellow("\n(dry run - task not deleted)"));
        return;
      }

      spinner.start(`Deleting task from ${backendLabel}...`);
      if (backend === "django") {
        await deleteTaskDjango(task.id);
      } else {
        await deleteTask(task.id);
      }
      spinner.succeed("Task deleted (moved to trash)");

      console.log(chalk.green(`\n✓ Task ${task.shortId} deleted!`));
    } catch (err) {
      spinner.fail("Failed to delete task");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw task pr <id>
// =============================================================================

taskCommand
  .command("pr <task-id>")
  .description("Create a PR with task details in body")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: django)",
  )
  .option("--dry-run", "Preview PR without creating")
  .option("--draft", "Create as draft PR")
  .option("--base <branch>", "Base branch (default: main)")
  .action(async (taskId: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading task from ${backendLabel}...`).start();

    try {
      // 1. Fetch task
      const task =
        backend === "django"
          ? await getTaskDjango(taskId)
          : await getTask(taskId);
      if (!task) {
        spinner.fail(`Task not found: ${taskId}`);
        process.exit(1);
      }

      // 2. Get current branch
      const branchResult = await exec("git", ["branch", "--show-current"], {
        silent: true,
      });
      const currentBranch = branchResult.stdout.trim();

      if (currentBranch === "main" || currentBranch === "master") {
        spinner.fail("Cannot create PR from main/master branch");
        process.exit(1);
      }

      // 3. Get commit messages for the PR body
      const baseBranch = options.base || "main";
      const commits = await getCommitMessages(baseBranch);

      // 4. Build PR title and body
      const prTitle = `${task.title} [${task.shortId}]`;
      const prBody = buildPrBody(task, commits);

      spinner.stop();
      console.log(chalk.blue(`\n=== PR Preview (${backendLabel}) ===\n`));
      console.log(chalk.gray("Title:"), prTitle);
      console.log(
        chalk.gray("Branch:"),
        currentBranch,
        "->",
        options.base || "main",
      );
      console.log(chalk.gray("\nBody:"));
      console.log(chalk.gray("-".repeat(60)));
      console.log(prBody);
      console.log(chalk.gray("-".repeat(60)));

      if (options.dryRun) {
        console.log(chalk.yellow("\n(dry run - PR not created)"));
        return;
      }

      // 5. Ensure branch is pushed
      spinner.start("Ensuring branch is pushed...");
      await exec("git", ["push", "-u", "origin", currentBranch]);
      spinner.succeed("Branch pushed");

      // 6. Create PR using gh CLI
      spinner.start("Creating pull request...");

      // Write body to temp file to avoid shell escaping issues
      const bodyFile = join(tmpdir(), `lw-pr-body-${Date.now()}.md`);
      writeFileSync(bodyFile, prBody);

      try {
        const ghArgs = [
          "pr",
          "create",
          "--title",
          `"${prTitle.replace(/"/g, '\\"')}"`,
          "--body-file",
          bodyFile,
          "--base",
          options.base || "main",
        ];

        if (options.draft) {
          ghArgs.push("--draft");
        }

        const prResult = await exec("gh", ghArgs, { silent: true });

        if (prResult.code !== 0) {
          spinner.fail("Failed to create pull request");
          const errorMsg = prResult.stderr.trim() || prResult.stdout.trim();
          console.error(chalk.red(errorMsg));
          process.exit(1);
        }

        const prUrl = prResult.stdout.trim();
        spinner.succeed("Pull request created");

        console.log(chalk.green(`\n✓ PR created: ${prUrl}`));
        console.log(chalk.gray(`  Task: ${task.url}`));
      } finally {
        // Clean up temp file
        try {
          unlinkSync(bodyFile);
        } catch {
          // Ignore cleanup errors
        }
      }
    } catch (err) {
      spinner.fail("Failed to create PR");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

/**
 * Build PR body from task with rich context
 */
function buildPrBody(
  task: {
    shortId: string;
    url: string;
    title: string;
    description: string | null;
    acceptanceCriteria: string | null;
    aiSummary?: string | null;
    note?: string | null;
    epicName?: string | null;
    sprintName?: string | null;
    userStoryName?: string | null;
    lifeDomainName?: string | null;
    taskType?: string;
    priority?: string | null;
  },
  commits: string[] = [],
): string {
  const sections: string[] = [];

  // Summary section
  sections.push("## Summary\n");

  // Use AI summary if available, otherwise description, otherwise generate from title
  if (task.aiSummary) {
    sections.push(task.aiSummary);
  } else if (task.description) {
    sections.push(task.description);
  } else {
    // Generate summary from title
    sections.push(`Implements: ${task.title}`);
  }

  // Context section - only if we have meaningful context
  const contextItems: string[] = [];
  if (task.epicName) contextItems.push(`**Epic**: ${task.epicName}`);
  if (task.sprintName) contextItems.push(`**PR**: ${task.sprintName}`);
  if (task.userStoryName)
    contextItems.push(`**User Story**: ${task.userStoryName}`);
  if (task.lifeDomainName)
    contextItems.push(`**Domain**: ${task.lifeDomainName}`);
  if (task.priority) contextItems.push(`**Priority**: ${task.priority}`);

  if (contextItems.length > 0) {
    sections.push("\n### Context\n");
    sections.push(contextItems.join("\n"));
  }

  // Changes section - from commit messages
  if (commits.length > 0) {
    sections.push("\n### Changes\n");
    commits.forEach((commit) => {
      sections.push(`- ${commit}`);
    });
  }

  // Notes section
  if (task.note) {
    sections.push("\n### Notes\n");
    sections.push(task.note);
  }

  // Commit link
  sections.push(`\n**Commit**: [${task.shortId}](${task.url})`);

  // Test Plan section
  sections.push("\n## Test Plan\n");
  if (task.acceptanceCriteria) {
    sections.push(task.acceptanceCriteria);
  } else {
    sections.push("- [ ] _Add test steps here_");
  }

  sections.push("\n---");
  sections.push("Generated with `lw task pr`");

  return sections.join("\n");
}

/**
 * Get commit messages for the current branch compared to base
 */
async function getCommitMessages(baseBranch: string): Promise<string[]> {
  try {
    const result = await exec(
      "git",
      ["log", `${baseBranch}..HEAD`, "--pretty=format:%s"],
      { silent: true },
    );
    if (result.code !== 0 || !result.stdout.trim()) {
      return [];
    }
    return result.stdout
      .trim()
      .split("\n")
      .filter((msg) => msg.trim());
  } catch {
    return [];
  }
}

// =============================================================================
// lw task priority <id> <priority>
// =============================================================================

taskCommand
  .command("priority <task-id> <priority>")
  .description("Set task priority (1, 2, 3 or high, medium, low)")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: django)",
  )
  .option("--dry-run", "Preview without updating")
  .action(async (taskId: string, priorityInput: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading task from ${backendLabel}...`).start();

    try {
      const task =
        backend === "django"
          ? await getTaskDjango(taskId)
          : await getTask(taskId);
      if (!task) {
        spinner.fail(`Task not found: ${taskId}`);
        process.exit(1);
      }

      // Resolve priority alias
      const resolvedPriority = resolvePriority(priorityInput);
      if (!resolvedPriority) {
        spinner.fail(`Invalid priority: ${priorityInput}`);
        console.log(chalk.gray("\nValid priorities:"));
        console.log(chalk.gray("  1, 1st, first, high  → 1st Priority"));
        console.log(chalk.gray("  2, 2nd, second, medium → 2nd Priority"));
        console.log(chalk.gray("  3, 3rd, third, low   → 3rd Priority"));
        process.exit(1);
      }

      spinner.stop();
      console.log(
        chalk.blue(`\n=== Update Task Priority (${backendLabel}) ===\n`),
      );
      console.log(chalk.gray("Task:"), task.title);
      console.log(chalk.gray("Current:"), task.priority || "(none)");
      console.log(chalk.gray("New:"), chalk.cyan(resolvedPriority));

      if (options.dryRun) {
        console.log(chalk.yellow("\n(dry run - no changes made)"));
        return;
      }

      spinner.start(`Updating ${backendLabel} priority...`);
      if (backend === "django") {
        await updateTaskPriorityDjango(task.id, resolvedPriority);
      } else {
        await updateTaskPriority(task.id, resolvedPriority);
      }
      spinner.succeed(`Priority updated to "${resolvedPriority}"`);

      console.log(chalk.green(`\n✓ Task ${task.shortId} priority set!`));
    } catch (err) {
      spinner.fail("Failed to update priority");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw task epic <id> <epic-id>
// =============================================================================

taskCommand
  .command("epic <task-id> <epic-id>")
  .description("Assign task to an epic (use 'none' to clear)")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: django)",
  )
  .option("--dry-run", "Preview without updating")
  .action(async (taskId: string, epicInput: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading task from ${backendLabel}...`).start();

    try {
      const task =
        backend === "django"
          ? await getTaskDjango(taskId)
          : await getTask(taskId);
      if (!task) {
        spinner.fail(`Task not found: ${taskId}`);
        process.exit(1);
      }

      let epicId: string | null = null;
      let epicName = "(none)";

      if (epicInput.toLowerCase() !== "none") {
        // Look up epic
        spinner.text = `Looking up epic in ${backendLabel}...`;
        const epic =
          backend === "django"
            ? await getEpicDjango(epicInput)
            : await getEpic(epicInput);
        if (!epic) {
          spinner.fail(`Epic not found: ${epicInput}`);
          process.exit(1);
        }
        epicId = epic.id;
        epicName = epic.name;
      }

      spinner.stop();
      console.log(
        chalk.blue(`\n=== Assign Task to Epic (${backendLabel}) ===\n`),
      );
      console.log(chalk.gray("Task:"), task.title);
      console.log(chalk.gray("Current Epic:"), task.epicName || "(none)");
      console.log(chalk.gray("New Epic:"), chalk.cyan(epicName));

      if (options.dryRun) {
        console.log(chalk.yellow("\n(dry run - no changes made)"));
        return;
      }

      spinner.start(`Assigning to epic in ${backendLabel}...`);
      if (backend === "django") {
        await assignTaskToEpicDjango(task.id, epicId);
      } else {
        await assignTaskToEpic(task.id, epicId);
      }
      spinner.succeed(`Task assigned to "${epicName}"`);

      console.log(chalk.green(`\n✓ Task ${task.shortId} epic updated!`));
    } catch (err) {
      spinner.fail("Failed to assign epic");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw task sprint <id> <sprint-id>
// =============================================================================

taskCommand
  .command("sprint <task-id> <sprint-id>")
  .description("Assign task to a sprint (use 'none' to clear)")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: django)",
  )
  .option("--dry-run", "Preview without updating")
  .action(async (taskId: string, sprintInput: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading task from ${backendLabel}...`).start();

    try {
      const task =
        backend === "django"
          ? await getTaskDjango(taskId)
          : await getTask(taskId);
      if (!task) {
        spinner.fail(`Task not found: ${taskId}`);
        process.exit(1);
      }

      let sprintId: string | null = null;
      let sprintName = "(none)";

      if (sprintInput.toLowerCase() !== "none") {
        // Look up sprint
        spinner.text = `Looking up sprint in ${backendLabel}...`;
        const sprint =
          backend === "django"
            ? await getSprintDjango(sprintInput)
            : await getSprint(sprintInput);
        if (!sprint) {
          spinner.fail(`Sprint not found: ${sprintInput}`);
          process.exit(1);
        }
        sprintId = sprint.id;
        sprintName = sprint.name;
      }

      spinner.stop();
      console.log(
        chalk.blue(`\n=== Assign Task to Sprint (${backendLabel}) ===\n`),
      );
      console.log(chalk.gray("Task:"), task.title);
      console.log(chalk.gray("Current Sprint:"), task.sprintName || "(none)");
      console.log(chalk.gray("New Sprint:"), chalk.cyan(sprintName));

      if (options.dryRun) {
        console.log(chalk.yellow("\n(dry run - no changes made)"));
        return;
      }

      spinner.start(`Assigning to sprint in ${backendLabel}...`);
      if (backend === "django") {
        await assignTaskToSprintDjango(task.id, sprintId);
      } else {
        await assignTaskToSprint(task.id, sprintId);
      }
      spinner.succeed(`Task assigned to "${sprintName}"`);

      console.log(chalk.green(`\n✓ Task ${task.shortId} sprint updated!`));
    } catch (err) {
      spinner.fail("Failed to assign sprint");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw task commit <id>
// =============================================================================

taskCommand
  .command("commit <task-id>")
  .description("Stage all changes and commit with formatted message")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: django)",
  )
  .option("--dry-run", "Show commit message without committing")
  .option("--no-stage", "Skip git add, commit only staged changes")
  .option("--scope <scope>", "Override scope in commit message")
  .option("--story <story-id>", "Include user story ID in commit message")
  .option(
    "-m, --message <msg>",
    "Override commit description (body still uses task)",
  )
  .action(async (taskId: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading task from ${backendLabel}...`).start();

    try {
      const task =
        backend === "django"
          ? await getTaskDjango(taskId)
          : await getTask(taskId);
      if (!task) {
        spinner.fail(`Task not found: ${taskId}`);
        process.exit(1);
      }

      spinner.stop();

      // Generate commit message
      const commitMessage = generateCommitMessage(task, {
        userStoryId: options.story,
        scope: options.scope,
      });

      // Allow override of the first line
      const finalMessage = options.message
        ? commitMessage.replace(/^.+\n/, `${options.message}\n`)
        : commitMessage;

      console.log(chalk.blue(`\n=== Task Commit (${backendLabel}) ===\n`));
      console.log(chalk.yellow("Task:"), task.title);
      console.log(chalk.yellow("ID:"), task.shortId);
      console.log(chalk.yellow("\nCommit message:"));
      console.log(chalk.gray("---"));
      console.log(finalMessage);
      console.log(chalk.gray("---"));

      if (options.dryRun) {
        console.log(chalk.yellow("\n[Dry run - no changes made]"));
        return;
      }

      // Stage changes (unless --no-stage)
      if (options.stage !== false) {
        spinner.start("Staging changes...");
        await exec("git add -A");
        spinner.succeed("Changes staged");
      }

      // Check if there are changes to commit
      const { stdout: statusOutput } = await exec("git status --porcelain");
      if (!statusOutput.trim()) {
        console.log(chalk.yellow("\nNo changes to commit."));
        return;
      }

      // Write commit message to temp file for multi-line support
      const tempFile = join(tmpdir(), `commit-msg-${task.shortId}.txt`);
      writeFileSync(tempFile, finalMessage);

      try {
        spinner.start("Creating commit...");
        await exec(`git commit -F "${tempFile}"`);
        spinner.succeed("Commit created");
      } finally {
        // Clean up temp file
        try {
          unlinkSync(tempFile);
        } catch {
          // Ignore cleanup errors
        }
      }

      console.log(chalk.green("\nTask committed successfully!"));
      console.log(
        chalk.gray(
          `\nTask ${task.shortId} is now a commit on the current branch.`,
        ),
      );
    } catch (err) {
      spinner.fail("Failed to commit task");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });
