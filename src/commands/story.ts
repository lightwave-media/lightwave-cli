/**
 * User Story management commands
 *
 * Commands:
 *   lw story list              List user stories
 *   lw story info <id>         Show user story details
 *   lw story create <name>     Create a new user story
 *   lw story context <id>      Load full story context for Claude
 *   lw story interview <id>    Start or continue an interview session
 *   lw story record <id>       Record an interview message
 *   lw story complete-round <id>  Advance to next interview round
 *   lw story flows <id>        Output user flows as Mermaid diagrams
 *   lw story tasks <id>        Generate tasks from acceptance criteria
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
import {
  getBackend,
  queryUserStoriesDjango,
  getUserStoryDjango,
  createUserStoryDjango,
  findEpicByNameDjango,
  findSprintByNameDjango,
  queryTasksDjango,
  getStoryContextDjango,
  startInterviewDjango,
  recordInterviewDjango,
  completeRoundDjango,
  getStoryFlowsDjango,
  generateTasksFromStoryDjango,
} from "../utils/createos.js";
import type { NotionUserStory } from "../types/notion.js";

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

export const storyCommand = new Command("story").description(
  "User Story management - view and track user stories",
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
  .description("List user stories")
  .option("--status <status>", "Filter by status")
  .option("--all", "Show all statuses")
  .option("--epic <name-or-id>", "Filter by epic")
  .option("--sprint <name-or-id>", "Filter by sprint")
  .option("--orphan", "Show only stories with no epic or sprint linked")
  .option("--limit <n>", "Max number of stories", "20")
  .option("--format <format>", "Output format: table, json", "table")
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: from LW_BACKEND env)",
  )
  .action(async (options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(
      `Fetching user stories from ${backendLabel}...`,
    ).start();

    try {
      // Resolve epic if provided
      let epicId: string | undefined;
      if (options.epic) {
        const epic =
          backend === "django"
            ? await findEpicByNameDjango(options.epic)
            : await findEpicByName(options.epic);
        if (epic) epicId = epic.id;
      }

      // Resolve sprint if provided
      let sprintId: string | undefined;
      if (options.sprint) {
        const sprint =
          backend === "django"
            ? await findSprintByNameDjango(options.sprint)
            : await findSprintByName(options.sprint);
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

      let stories: NotionUserStory[];
      if (backend === "django") {
        stories = await queryUserStoriesDjango({
          status: statusFilter,
          epicId,
          sprintId,
          limit: options.orphan ? 200 : parseInt(options.limit, 10), // Fetch more for client-side orphan filtering
        });
      } else {
        stories = await queryUserStories({
          status: statusFilter,
          epicId,
          sprintId,
          limit: options.orphan ? 200 : parseInt(options.limit, 10),
        });
      }

      // Filter orphan stories (no epic or sprint linked)
      if (options.orphan) {
        stories = stories.filter((s) => !s.epicId && !s.sprintId);
        // Apply limit after orphan filter
        stories = stories.slice(0, parseInt(options.limit, 10));
      }

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
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: from LW_BACKEND env)",
  )
  .action(async (storyId: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Loading user story from ${backendLabel}...`).start();

    try {
      const story =
        backend === "django"
          ? await getUserStoryDjango(storyId)
          : await getUserStory(storyId);
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
        await displayStoryTasks(story, backend);
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

  // Description
  if (story.description) {
    console.log(chalk.yellow("\nDescription:"));
    console.log(chalk.white(story.description));
  }

  // Acceptance Criteria
  if (story.acceptanceCriteria) {
    console.log(chalk.yellow("\nAcceptance Criteria:"));
    console.log(chalk.white(story.acceptanceCriteria));
  }

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
async function displayStoryTasks(
  story: NotionUserStory,
  backend: "django" | "notion" = "django",
): Promise<void> {
  const spinner = ora("Loading story tasks...").start();

  try {
    const tasks =
      backend === "django"
        ? await queryTasksDjango({ userStory: story.name })
        : await queryTasks({ userStory: story.name });
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
  .description("Create a new user story")
  .option("--description <text>", "Detailed description of the story")
  .option(
    "--acceptance-criteria <text>",
    "Criteria that must be met for story completion",
  )
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
  .option(
    "--backend <backend>",
    "Backend to use: django, notion (default: from LW_BACKEND env)",
  )
  .action(async (name: string, options) => {
    const backend = resolveBackend(options.backend);
    const backendLabel = backend === "django" ? "createOS" : "Notion";
    const spinner = ora(`Creating user story in ${backendLabel}...`).start();

    try {
      // Resolve epic if provided
      let epicId: string | undefined;
      let epicName: string | undefined;
      if (options.epic) {
        spinner.text = "Resolving epic...";
        const epic =
          backend === "django"
            ? await findEpicByNameDjango(options.epic)
            : await findEpicByName(options.epic);
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
        const sprint =
          backend === "django"
            ? await findSprintByNameDjango(options.sprint)
            : await findSprintByName(options.sprint);
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
        if (options.description)
          console.log(chalk.yellow("Description:"), options.description);
        if (options.acceptanceCriteria)
          console.log(
            chalk.yellow("Acceptance Criteria:"),
            options.acceptanceCriteria,
          );
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
      let story: NotionUserStory;
      if (backend === "django") {
        story = await createUserStoryDjango(name, {
          description: options.description,
          acceptanceCriteria: options.acceptanceCriteria,
          userType: options.userType,
          priority: options.priority,
          epicId,
          sprintId,
        });
      } else {
        story = await createUserStory(name, {
          userType: options.userType,
          priority: options.priority,
          epicId,
          sprintId,
        });
      }

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

// =============================================================================
// lw story context <id>
// =============================================================================

storyCommand
  .command("context <story-id>")
  .description("Load full story context for Claude consumption")
  .option("--format <format>", "Output format: markdown, json", "markdown")
  .action(async (storyId: string, options) => {
    const spinner = ora("Loading story context...").start();

    try {
      const context = await getStoryContextDjango(storyId);
      if (!context) {
        spinner.fail(`Story not found: ${storyId}`);
        process.exit(1);
      }

      spinner.stop();

      if (options.format === "json") {
        console.log(JSON.stringify(context, null, 2));
        return;
      }

      // Markdown format for Claude consumption
      const { story, epic, sprint, tasks, interviewSessions, layers } = context;

      console.log(`# User Story: ${story.name}\n`);
      console.log(`**ID:** ${story.shortId}`);
      console.log(`**Status:** ${story.status}`);
      console.log(
        `**Discovery:** ${story.discoveryStatus} (${story.interviewProgress.percentComplete}% complete)`,
      );
      console.log(`**Current Round:** ${story.currentInterviewRound}`);

      if (epic) {
        console.log(`**Epic:** ${epic.name}`);
      }
      if (sprint) {
        console.log(`**Sprint:** ${sprint.name}`);
      }

      if (story.description) {
        console.log(`\n## Description\n\n${story.description}`);
      }

      if (story.acceptanceCriteria) {
        console.log(`\n## Acceptance Criteria\n\n${story.acceptanceCriteria}`);
      }

      if (story.personas && story.personas.length > 0) {
        console.log(`\n## Personas\n`);
        for (const persona of story.personas) {
          console.log(
            `- **${persona.name || "Unnamed"}**: ${persona.role || ""}`,
          );
          if (persona.goals) console.log(`  - Goals: ${persona.goals}`);
          if (persona.painPoints)
            console.log(`  - Pain Points: ${persona.painPoints}`);
        }
      }

      if (story.rbacRequirements && story.rbacRequirements.length > 0) {
        console.log(`\n## RBAC Requirements\n`);
        for (const req of story.rbacRequirements) {
          console.log(`- **${req.role || "Role"}**: ${req.permissions || ""}`);
        }
      }

      if (story.technicalConstraints && story.technicalConstraints.length > 0) {
        console.log(`\n## Technical Constraints\n`);
        for (const constraint of story.technicalConstraints) {
          console.log(`- ${constraint}`);
        }
      }

      if (story.edgeCases && story.edgeCases.length > 0) {
        console.log(`\n## Edge Cases\n`);
        for (const edge of story.edgeCases) {
          console.log(
            `- **${edge.scenario || "Scenario"}**: ${edge.expectedBehavior || ""}`,
          );
        }
      }

      if (tasks.length > 0) {
        console.log(`\n## Tasks (${tasks.length})\n`);
        for (const task of tasks) {
          console.log(`- [${task.status}] ${task.shortId}: ${task.title}`);
        }
      }

      if (interviewSessions.length > 0) {
        console.log(`\n## Interview Sessions (${interviewSessions.length})\n`);
        for (const session of interviewSessions) {
          console.log(
            `- ${session.roundTypeDisplay}: ${session.status} (${session.transcript.length} messages)`,
          );
        }
      }

      if (layers.length > 0) {
        console.log(`\n## Layers\n`);
        for (const layer of layers) {
          console.log(`- ${layer}`);
        }
      }
    } catch (err) {
      spinner.fail("Failed to load story context");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw story interview <id>
// =============================================================================

storyCommand
  .command("interview <story-id>")
  .description("Start or continue an interview session")
  .option(
    "--round <round>",
    "Focus on specific round (stakeholder, persona, acceptance, edge_cases, rbac, technical)",
  )
  .action(async (storyId: string, options) => {
    const spinner = ora("Starting interview session...").start();

    try {
      const session = await startInterviewDjango(storyId, options.round);
      spinner.succeed("Interview session ready");

      console.log(chalk.blue("\n=== Interview Session ===\n"));
      console.log(chalk.yellow("Session ID:"), session.shortId);
      console.log(chalk.yellow("Round:"), session.roundTypeDisplay);
      console.log(chalk.yellow("Status:"), session.status);
      console.log(chalk.yellow("Messages:"), session.transcript.length);

      if (session.transcript.length > 0) {
        console.log(chalk.blue("\n--- Transcript ---\n"));
        for (const msg of session.transcript.slice(-5)) {
          const roleColor = msg.role === "user" ? chalk.green : chalk.cyan;
          console.log(`${roleColor(msg.role.toUpperCase())}: ${msg.content}\n`);
        }
        if (session.transcript.length > 5) {
          console.log(
            chalk.gray(
              `... and ${session.transcript.length - 5} earlier messages`,
            ),
          );
        }
      }

      console.log(chalk.gray("\nTo record a response:"));
      console.log(
        chalk.gray(
          `  lw story record ${storyId} --role user --message "Your answer..."`,
        ),
      );
      console.log(chalk.gray("\nTo complete this round:"));
      console.log(chalk.gray(`  lw story complete-round ${storyId}`));
    } catch (err) {
      spinner.fail("Failed to start interview");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw story record <id>
// =============================================================================

storyCommand
  .command("record <story-id>")
  .description("Record an interview message")
  .requiredOption("--role <role>", "Message role: user or assistant")
  .requiredOption("--message <message>", "Message content")
  .option("--findings <json>", "Structured findings as JSON")
  .action(async (storyId: string, options) => {
    const spinner = ora("Recording message...").start();

    try {
      const role = options.role.toLowerCase();
      if (role !== "user" && role !== "assistant") {
        spinner.fail("Role must be 'user' or 'assistant'");
        process.exit(1);
      }

      let findings: Record<string, unknown> | undefined;
      if (options.findings) {
        try {
          findings = JSON.parse(options.findings);
        } catch {
          spinner.fail("Invalid JSON for findings");
          process.exit(1);
        }
      }

      const session = await recordInterviewDjango(storyId, {
        role: role as "user" | "assistant",
        content: options.message,
        findings,
      });

      spinner.succeed("Message recorded");

      console.log(chalk.blue("\n=== Updated Session ===\n"));
      console.log(chalk.yellow("Round:"), session.roundTypeDisplay);
      console.log(chalk.yellow("Messages:"), session.transcript.length);

      // Show latest message
      const latest = session.transcript[session.transcript.length - 1];
      if (latest) {
        console.log(chalk.blue("\nLatest:"));
        const roleColor = latest.role === "user" ? chalk.green : chalk.cyan;
        console.log(
          `${roleColor(latest.role.toUpperCase())}: ${latest.content}`,
        );
      }
    } catch (err) {
      spinner.fail("Failed to record message");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw story complete-round <id>
// =============================================================================

storyCommand
  .command("complete-round <story-id>")
  .description("Complete the current interview round and advance to next")
  .action(async (storyId: string) => {
    const spinner = ora("Completing round...").start();

    try {
      const result = await completeRoundDjango(storyId);
      spinner.succeed(result.message);

      console.log(chalk.blue("\n=== Round Complete ===\n"));
      console.log(chalk.yellow("Previous Round:"), result.previousRound);
      console.log(chalk.yellow("New Round:"), result.newRound);
      console.log(chalk.yellow("Discovery Status:"), result.discoveryStatus);

      if (result.newRound === "complete") {
        console.log(
          chalk.green("\nInterview complete! Story is ready for review."),
        );
        console.log(
          chalk.gray(`\nTo generate tasks: lw story tasks ${storyId}`),
        );
      } else {
        console.log(chalk.gray(`\nTo continue: lw story interview ${storyId}`));
      }
    } catch (err) {
      spinner.fail("Failed to complete round");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw story flows <id>
// =============================================================================

storyCommand
  .command("flows <story-id>")
  .description("Output user flows as Mermaid diagrams")
  .option("--json", "Output raw JSON instead of Mermaid")
  .action(async (storyId: string, options) => {
    const spinner = ora("Loading user flows...").start();

    try {
      const result = await getStoryFlowsDjango(storyId);
      spinner.stop();

      if (options.json) {
        console.log(JSON.stringify(result.flows, null, 2));
        return;
      }

      if (!result.mermaid || result.mermaid.trim() === "") {
        console.log(chalk.yellow("No user flows defined for this story."));
        console.log(
          chalk.gray(
            "\nUser flows are captured during the Persona interview round.",
          ),
        );
        return;
      }

      console.log(chalk.blue("=== User Flows ===\n"));
      console.log(result.mermaid);
    } catch (err) {
      spinner.fail("Failed to load flows");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw story tasks <id>
// =============================================================================

storyCommand
  .command("tasks <story-id>")
  .description("Generate tasks from story acceptance criteria")
  .option("--dry-run", "Preview tasks without creating them")
  .action(async (storyId: string, options) => {
    const spinner = ora(
      options.dryRun ? "Previewing tasks..." : "Generating tasks...",
    ).start();

    try {
      const result = await generateTasksFromStoryDjango(
        storyId,
        options.dryRun,
      );
      spinner.stop();

      if (result.tasks.length === 0) {
        console.log(chalk.yellow("No tasks could be generated."));
        console.log(
          chalk.gray("\nEnsure the story has acceptance criteria defined."),
        );
        return;
      }

      console.log(
        chalk.blue(
          `\n=== ${options.dryRun ? "Preview" : "Generated"} Tasks (${result.tasks.length}) ===\n`,
        ),
      );

      for (const task of result.tasks) {
        if (task.id) {
          console.log(chalk.cyan(`[${task.id.substring(0, 8)}]`), task.title);
        } else {
          console.log(chalk.gray("[ new ]"), task.title);
        }
        if (task.description) {
          console.log(chalk.gray(`  ${task.description}`));
        }
      }

      if (options.dryRun) {
        console.log(
          chalk.gray(`\nRun without --dry-run to create these tasks.`),
        );
      } else {
        console.log(chalk.green(`\n${result.tasks.length} tasks created!`));
      }
    } catch (err) {
      spinner.fail("Failed to generate tasks");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });
