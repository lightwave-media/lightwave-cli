/**
 * User Story management commands
 *
 * Commands:
 *   lw story list              List user stories from Notion
 *   lw story info <id>         Show user story details
 *   lw story create <name>     Create a new user story
 */

import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import {
  queryUserStories,
  getUserStory,
  createUserStory,
  findEpicByName,
  findSprintByName,
  queryTasks,
} from "../utils/notion.js";
import type { NotionUserStory } from "../types/notion.js";

export const storyCommand = new Command("story").description(
  "User Story management - view and track user stories from Notion",
);

/**
 * Get chalk color for story status
 */
function getStatusColor(status: string) {
  const colors: Record<string, (s: string) => string> = {
    "Not Started": chalk.gray,
    "In Progress": chalk.magenta,
    Completed: chalk.green,
    "On Hold": chalk.yellow,
    Cancelled: chalk.red,
  };
  return colors[status] || chalk.white;
}

// =============================================================================
// lw story list
// =============================================================================

storyCommand
  .command("list")
  .description("List user stories from Notion")
  .option("--status <status>", "Filter by status")
  .option("--all", "Show all statuses")
  .option("--epic <name-or-id>", "Filter by epic")
  .option("--sprint <name-or-id>", "Filter by sprint")
  .option("--limit <n>", "Max number of stories", "20")
  .option("--format <format>", "Output format: table, json", "table")
  .action(async (options) => {
    const spinner = ora("Fetching user stories from Notion...").start();

    try {
      // Resolve epic if provided
      let epicId: string | undefined;
      if (options.epic) {
        const epic = await findEpicByName(options.epic);
        if (epic) epicId = epic.id;
      }

      // Resolve sprint if provided
      let sprintId: string | undefined;
      if (options.sprint) {
        const sprint = await findSprintByName(options.sprint);
        if (sprint) sprintId = sprint.id;
      }

      // Parse status filter
      let statusFilter: string[] | undefined;
      if (options.status) {
        statusFilter = options.status.split(",").map((s: string) => s.trim());
      } else if (!options.all) {
        // Default: show active stories
        statusFilter = ["In Progress", "Not Started"];
      }

      const stories = await queryUserStories({
        status: statusFilter,
        epicId,
        sprintId,
        limit: parseInt(options.limit, 10),
      });

      spinner.stop();

      if (stories.length === 0) {
        console.log(chalk.yellow("No user stories found matching criteria."));
        return;
      }

      if (options.format === "json") {
        console.log(JSON.stringify(stories, null, 2));
        return;
      }

      // Table format
      console.log(chalk.blue("\n=== User Stories ===\n"));
      console.log(
        chalk.gray(
          `${"ID".padEnd(10)} ${"Status".padEnd(14)} ${"Priority".padEnd(14)} ${"Name".padEnd(50)}`,
        ),
      );
      console.log(chalk.gray("-".repeat(90)));

      for (const story of stories) {
        const statusColor = getStatusColor(story.status);
        const priority = story.priority || "-";
        const truncatedName =
          story.name.length > 48
            ? story.name.substring(0, 48) + "..."
            : story.name;
        console.log(
          `${chalk.cyan(story.shortId.padEnd(10))} ` +
            `${statusColor(story.status.padEnd(14))} ` +
            `${chalk.gray(priority.padEnd(14))} ` +
            `${truncatedName}`,
        );
      }

      console.log(chalk.gray(`\n${stories.length} user story(ies) shown`));
    } catch (err) {
      spinner.fail("Failed to fetch user stories");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw story info <id>
// =============================================================================

storyCommand
  .command("info <story-id>")
  .description("Show detailed user story information")
  .option("--format <format>", "Output format: text, json", "text")
  .option("--tasks", "Also show tasks linked to this story")
  .action(async (storyId: string, options) => {
    const spinner = ora("Loading user story from Notion...").start();

    try {
      const story = await getUserStory(storyId);
      if (!story) {
        spinner.fail(`User story not found: ${storyId}`);
        process.exit(1);
      }

      spinner.stop();

      if (options.format === "json") {
        console.log(JSON.stringify(story, null, 2));
        return;
      }

      displayStoryDetails(story);

      // Optionally show tasks
      if (options.tasks) {
        await displayStoryTasks(story);
      }
    } catch (err) {
      spinner.fail("Failed to load user story");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

/**
 * Display user story details in formatted text
 */
function displayStoryDetails(story: NotionUserStory): void {
  console.log(chalk.blue("\n=== User Story Details ===\n"));
  console.log(chalk.yellow("ID:"), story.shortId);
  console.log(chalk.yellow("Full ID:"), story.id);
  console.log(chalk.yellow("Name:"), story.name);
  console.log(
    chalk.yellow("Status:"),
    getStatusColor(story.status)(story.status),
  );

  if (story.priority) {
    console.log(chalk.yellow("Priority:"), story.priority);
  }

  if (story.userType) {
    console.log(chalk.yellow("User Type:"), story.userType);
  }

  console.log(chalk.yellow("URL:"), story.url);

  console.log(chalk.yellow("\nLinked Items:"));
  console.log(chalk.gray(`  Tasks: ${story.taskIds.length}`));
  if (story.epicId) {
    console.log(chalk.gray(`  Epic: ${story.epicId.substring(0, 8)}`));
  }
  if (story.sprintId) {
    console.log(chalk.gray(`  Sprint: ${story.sprintId.substring(0, 8)}`));
  }
}

/**
 * Display tasks linked to a user story
 */
async function displayStoryTasks(story: NotionUserStory): Promise<void> {
  const spinner = ora("Loading story tasks...").start();

  try {
    const tasks = await queryTasks({ userStory: story.name });
    spinner.stop();

    if (tasks.length === 0) {
      console.log(chalk.gray("\nNo tasks linked to this user story."));
      return;
    }

    console.log(
      chalk.blue(`\n=== Tasks in User Story (${tasks.length}) ===\n`),
    );
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
    spinner.fail("Failed to load story tasks");
    console.error(chalk.red((err as Error).message));
  }
}

// =============================================================================
// lw story create <name>
// =============================================================================

storyCommand
  .command("create <name>")
  .description("Create a new user story in Notion")
  .option(
    "--user-type <type>",
    "User type (e.g., 'Admin', 'End User', 'Developer')",
  )
  .option(
    "--priority <priority>",
    "Priority (1st Priority, 2nd Priority, 3rd Priority)",
  )
  .option("--epic <name-or-id>", "Link to epic")
  .option("--sprint <name-or-id>", "Link to sprint")
  .option("--dry-run", "Preview what would be created")
  .action(async (name: string, options) => {
    const spinner = ora("Creating user story in Notion...").start();

    try {
      // Resolve epic if provided
      let epicId: string | undefined;
      let epicName: string | undefined;
      if (options.epic) {
        spinner.text = "Resolving epic...";
        const epic = await findEpicByName(options.epic);
        if (!epic) {
          spinner.fail(`Epic not found: ${options.epic}`);
          process.exit(1);
        }
        epicId = epic.id;
        epicName = epic.name;
      }

      // Resolve sprint if provided
      let sprintId: string | undefined;
      let sprintName: string | undefined;
      if (options.sprint) {
        spinner.text = "Resolving sprint...";
        const sprint = await findSprintByName(options.sprint);
        if (!sprint) {
          spinner.fail(`Sprint not found: ${options.sprint}`);
          process.exit(1);
        }
        sprintId = sprint.id;
        sprintName = sprint.name;
      }

      if (options.dryRun) {
        spinner.stop();
        console.log(chalk.blue("\n=== Preview User Story ===\n"));
        console.log(chalk.yellow("Name:"), name);
        if (options.userType)
          console.log(chalk.yellow("User Type:"), options.userType);
        if (options.priority)
          console.log(chalk.yellow("Priority:"), options.priority);
        if (epicName) console.log(chalk.yellow("Epic:"), epicName);
        if (sprintName) console.log(chalk.yellow("Sprint:"), sprintName);
        console.log(chalk.gray("\n(dry run - no changes made)"));
        return;
      }

      spinner.text = "Creating user story...";
      const story = await createUserStory(name, {
        userType: options.userType,
        priority: options.priority,
        epicId,
        sprintId,
      });

      spinner.succeed("User story created!");

      console.log(chalk.blue("\n=== User Story Created ===\n"));
      console.log(chalk.yellow("ID:"), chalk.cyan(story.shortId));
      console.log(chalk.yellow("Name:"), story.name);
      console.log(
        chalk.yellow("Status:"),
        getStatusColor(story.status)(story.status),
      );
      if (epicName) console.log(chalk.yellow("Epic:"), epicName);
      if (sprintName) console.log(chalk.yellow("Sprint:"), sprintName);
      console.log(chalk.yellow("URL:"), story.url);

      console.log(
        chalk.gray(
          `\nTo add tasks: lw task create "<task name>" --story ${story.shortId}`,
        ),
      );
    } catch (err) {
      spinner.fail("Failed to create user story");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });
