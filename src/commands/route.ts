/**
 * v_core Agent Routing CLI
 *
 * Commands:
 *   lw route <request>        Route a request to optimal agent
 *   lw route agents           List available agents
 *   lw route test             Test routing with sample requests
 *
 * This is the deterministic foundation for AI agent routing.
 * All routing decisions flow through this CLI command.
 */

import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";

// =============================================================================
// CONFIGURATION
// =============================================================================

const DEFAULT_API_URL = "http://localhost:8000";

function getApiUrl(): string {
  return process.env.CREATEOS_API_URL || DEFAULT_API_URL;
}

// =============================================================================
// API TYPES
// =============================================================================

interface RoutingIntent {
  category: string;
  confidence: number;
  keywords: string[];
}

interface DomainContext {
  task_id: string | null;
  epic_id: string | null;
  sprint_id: string | null;
  task_category: string | null;
  has_context: boolean;
  detection_method: string;
}

interface RoutingResponse {
  selected_agent: string;
  confidence: number;
  intent: RoutingIntent;
  domain_context: DomainContext;
  context?: string; // Hydrated context markdown
  routing_time_ms: number;
  top_3_agents?: [string, number][];
}

interface AgentInfo {
  id: string;
  name: string;
  description: string;
  intents: string[];
  categories: string[];
  priority: number;
}

interface AgentsResponse {
  agents: AgentInfo[];
}

// =============================================================================
// API CLIENT
// =============================================================================

/**
 * Route a request through the v_core routing API
 */
async function routeRequest(
  request: string,
  options: {
    taskId?: string;
    epicId?: string;
    sprintId?: string;
    hydrate?: boolean;
  } = {},
): Promise<RoutingResponse> {
  const apiUrl = getApiUrl();
  const url = `${apiUrl}/api/ai/route/`;

  const context: Record<string, string> = {};
  if (options.taskId) context.current_task = options.taskId;
  if (options.epicId) context.current_epic = options.epicId;
  if (options.sprintId) context.current_sprint = options.sprintId;

  const response = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      request,
      context: Object.keys(context).length > 0 ? context : undefined,
      hydrate: options.hydrate ?? false,
    }),
  });

  if (!response.ok) {
    const error = await response.text();
    throw new Error(`Routing API error: ${response.status} - ${error}`);
  }

  return response.json();
}

/**
 * List available agents from the API
 */
async function listAgents(): Promise<AgentInfo[]> {
  const apiUrl = getApiUrl();
  const url = `${apiUrl}/api/ai/agents/`;

  const response = await fetch(url);

  if (!response.ok) {
    throw new Error(`Agents API error: ${response.status}`);
  }

  const data: AgentsResponse = await response.json();
  return data.agents;
}

// =============================================================================
// DISPLAY HELPERS
// =============================================================================

const AGENT_COLORS: Record<string, (s: string) => string> = {
  v_core: chalk.gray,
  v_general_manager: chalk.blue,
  v_software_architect: chalk.magenta,
  v_senior_developer: chalk.cyan,
  v_write: chalk.green,
  v_speak: chalk.greenBright,
  v_accountant: chalk.yellow,
  v_cinematographer: chalk.red,
  v_photographer: chalk.redBright,
};

function getAgentColor(agent: string): (s: string) => string {
  return AGENT_COLORS[agent] || chalk.white;
}

function formatConfidence(confidence: number): string {
  if (confidence >= 0.8)
    return chalk.green(`${(confidence * 100).toFixed(0)}%`);
  if (confidence >= 0.6)
    return chalk.yellow(`${(confidence * 100).toFixed(0)}%`);
  return chalk.red(`${(confidence * 100).toFixed(0)}%`);
}

function formatAgent(agent: string): string {
  const color = getAgentColor(agent);
  // Convert v_senior_developer to V-Senior Developer
  const displayName = agent
    .replace(/^v_/, "v-")
    .split("_")
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
  return color(displayName);
}

// =============================================================================
// COMMANDS
// =============================================================================

export const routeCommand = new Command("route").description(
  "v_core agent routing - route requests to optimal AI agent",
);

// Main route command: lw route <request>
routeCommand
  .argument("[request...]", "Natural language request to route")
  .option("--task <id>", "Current task context (short ID)")
  .option("--epic <id>", "Current epic context (short ID)")
  .option("--sprint <id>", "Current sprint context (short ID)")
  .option("--hydrate", "Include full context in response")
  .option("--format <format>", "Output format: text, json, agent", "text")
  .option("--quiet", "Only output the agent name (for scripting)")
  .action(async (requestParts: string[], options) => {
    const request = requestParts.join(" ");

    if (!request || request.trim().length < 3) {
      console.log(chalk.yellow("Usage: lw route <request>"));
      console.log(
        chalk.gray('Example: lw route "Help me refactor the auth module"'),
      );
      return;
    }

    const spinner = ora("Routing request...").start();

    try {
      const result = await routeRequest(request, {
        taskId: options.task,
        epicId: options.epic,
        sprintId: options.sprint,
        hydrate: options.hydrate,
      });

      spinner.stop();

      // Quiet mode - just output agent name
      if (options.quiet) {
        console.log(result.selected_agent);
        return;
      }

      // JSON format
      if (options.format === "json") {
        console.log(JSON.stringify(result, null, 2));
        return;
      }

      // Agent-only format (for scripting)
      if (options.format === "agent") {
        console.log(result.selected_agent);
        return;
      }

      // Text format (default)
      console.log(chalk.blue("\n=== v_core Routing ===\n"));

      // Selected agent
      console.log(
        chalk.yellow("Agent:"),
        formatAgent(result.selected_agent),
        formatConfidence(result.confidence),
      );

      // Intent
      console.log(
        chalk.yellow("Intent:"),
        chalk.white(result.intent.category),
        chalk.gray(`(${(result.intent.confidence * 100).toFixed(0)}%)`),
      );

      if (result.intent.keywords.length > 0) {
        console.log(
          chalk.yellow("Keywords:"),
          chalk.gray(result.intent.keywords.join(", ")),
        );
      }

      // Context
      if (result.domain_context.has_context) {
        console.log(chalk.yellow("\nContext Detected:"));
        if (result.domain_context.task_id) {
          console.log(chalk.gray(`  Task: ${result.domain_context.task_id}`));
        }
        if (result.domain_context.epic_id) {
          console.log(chalk.gray(`  Epic: ${result.domain_context.epic_id}`));
        }
        if (result.domain_context.task_category) {
          console.log(
            chalk.gray(`  Category: ${result.domain_context.task_category}`),
          );
        }
      }

      // Timing
      console.log(
        chalk.gray(`\nRouted in ${result.routing_time_ms.toFixed(0)}ms`),
      );

      // Hydrated context
      if (options.hydrate && result.context) {
        console.log(chalk.yellow("\n--- Context ---"));
        console.log(result.context);
      }
    } catch (err) {
      spinner.fail("Routing failed");
      console.error(chalk.red((err as Error).message));

      // Helpful error message
      if ((err as Error).message.includes("ECONNREFUSED")) {
        console.log(
          chalk.yellow("\nIs the Django server running? Try: make start-bg"),
        );
      }
      process.exit(1);
    }
  });

// List agents: lw route agents
routeCommand
  .command("agents")
  .description("List available agents and their capabilities")
  .option("--format <format>", "Output format: table, json", "table")
  .action(async (options) => {
    const spinner = ora("Fetching agents...").start();

    try {
      const agents = await listAgents();
      spinner.stop();

      if (options.format === "json") {
        console.log(JSON.stringify(agents, null, 2));
        return;
      }

      console.log(chalk.blue("\n=== Available Agents ===\n"));

      for (const agent of agents) {
        const color = getAgentColor(agent.id);
        console.log(color(`${agent.name}`), chalk.gray(`(${agent.id})`));
        console.log(chalk.white(`  ${agent.description}`));
        console.log(
          chalk.gray(`  Intents: ${agent.intents.slice(0, 3).join(", ")}`),
        );
        console.log("");
      }

      console.log(chalk.gray(`${agents.length} agents available`));
    } catch (err) {
      spinner.fail("Failed to fetch agents");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// Test routing: lw route test
routeCommand
  .command("test")
  .description("Test routing with sample requests")
  .action(async () => {
    const testRequests = [
      "Help me refactor the authentication module",
      "What should I work on next?",
      "Write copy for the landing page",
      "Review the Q4 financials",
      "Help with cinematography for the next shoot",
      "Debug the login error",
      "Plan the next sprint",
      "Write unit tests for the API",
    ];

    console.log(chalk.blue("\n=== Routing Test ===\n"));
    console.log(
      chalk.gray(
        `${"Request".padEnd(45)} ${"Agent".padEnd(25)} ${"Confidence".padEnd(10)} Time`,
      ),
    );
    console.log(chalk.gray("-".repeat(95)));

    for (const request of testRequests) {
      try {
        const result = await routeRequest(request);
        const truncatedReq =
          request.length > 43 ? request.substring(0, 43) + ".." : request;

        console.log(
          `${truncatedReq.padEnd(45)} ` +
            `${formatAgent(result.selected_agent).padEnd(35)} ` +
            `${formatConfidence(result.confidence).padEnd(15)} ` +
            `${chalk.gray(result.routing_time_ms.toFixed(0) + "ms")}`,
        );
      } catch (err) {
        console.log(
          `${request.substring(0, 43).padEnd(45)} ${chalk.red("ERROR")}`,
        );
      }
    }

    console.log("");
  });
