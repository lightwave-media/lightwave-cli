/**
 * Agent Management Commands
 *
 * Commands for managing virtual agents in the Agent Team system.
 *
 * Usage:
 *   lw agent list              # List all registered agents
 *   lw agent status            # Show agent system health
 *   lw agent run <agent> <req> # Run an agent with a request
 */

import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import { exec } from "../utils/exec.js";
import { findWorkspaceRoot, getDomainPath } from "../utils/paths.js";

export const agentCommand = new Command("agent").description(
  "Manage virtual agents",
);

// =============================================================================
// AGENT TYPES
// =============================================================================

interface AgentInfo {
  name: string;
  description: string;
  type: string;
  capabilities: string[];
}

const AGENT_INFO: Record<string, AgentInfo> = {
  v_core: {
    name: "V-Core",
    description: "General coordinator, routes to specialists",
    type: "coordinator",
    capabilities: ["task analysis", "routing", "coordination"],
  },
  v_senior_developer: {
    name: "V-Senior Developer",
    description: "Code implementation and bug fixes",
    type: "specialist",
    capabilities: ["coding", "debugging", "refactoring"],
  },
  v_software_architect: {
    name: "V-Software Architect",
    description: "System design and architecture",
    type: "specialist",
    capabilities: ["architecture", "planning", "design"],
  },
  v_write: {
    name: "V-Writer",
    description: "Content creation and copywriting",
    type: "specialist",
    capabilities: ["content", "marketing", "documentation"],
  },
  v_general_manager: {
    name: "V-Manager",
    description: "Task delegation and team coordination",
    type: "coordinator",
    capabilities: ["triage", "assignment", "reporting"],
  },
  v_accountant: {
    name: "V-Accountant",
    description: "Financial operations and bookkeeping",
    type: "specialist",
    capabilities: ["invoicing", "expenses", "budgeting"],
  },
  v_cinematographer: {
    name: "V-Cinematographer",
    description: "Video production and cinematography",
    type: "specialist",
    capabilities: ["video", "shot planning", "production"],
  },
  v_photographer: {
    name: "V-Photographer",
    description: "Photography and image editing",
    type: "specialist",
    capabilities: ["photography", "editing", "portraits"],
  },
  v_emergence: {
    name: "V-Emergence",
    description: "Second brain knowledge graph and pattern detection",
    type: "specialist",
    capabilities: [
      "knowledge graph",
      "pattern detection",
      "insights",
      "truth analysis",
    ],
  },
};

// =============================================================================
// LIST COMMAND
// =============================================================================

agentCommand
  .command("list")
  .description("List all virtual agents")
  .option("--json", "Output as JSON")
  .action(async (options) => {
    console.log(chalk.blue("\n=== Virtual Agent Team ===\n"));

    if (options.json) {
      console.log(JSON.stringify(AGENT_INFO, null, 2));
      return;
    }

    // Group by type
    const coordinators = Object.entries(AGENT_INFO).filter(
      ([, info]) => info.type === "coordinator",
    );
    const specialists = Object.entries(AGENT_INFO).filter(
      ([, info]) => info.type === "specialist",
    );

    console.log(chalk.yellow("Coordinators:"));
    for (const [key, info] of coordinators) {
      console.log(`  ${chalk.green(key.padEnd(22))} ${info.name}`);
      console.log(`  ${"".padEnd(22)} ${chalk.gray(info.description)}`);
    }

    console.log(chalk.yellow("\nSpecialists:"));
    for (const [key, info] of specialists) {
      console.log(`  ${chalk.green(key.padEnd(22))} ${info.name}`);
      console.log(`  ${"".padEnd(22)} ${chalk.gray(info.description)}`);
    }

    console.log(
      chalk.gray(`\nTotal: ${Object.keys(AGENT_INFO).length} agents`),
    );
  });

// =============================================================================
// STATUS COMMAND
// =============================================================================

agentCommand
  .command("status")
  .description("Show agent system health and statistics")
  .action(async () => {
    const spinner = ora("Fetching agent status...").start();

    try {
      const root = findWorkspaceRoot();
      const lwmCorePath = getDomainPath("lwm_core");

      // Run Django management command to get status
      const result = await exec(
        `cd "${lwmCorePath}" && docker compose exec -T web python manage.py shell -c "
from apps.ai.models import AgentExecution, ExecutionStatus, BugReport
from apps.ai.orchestrator.executor import _AGENT_REGISTRY
from django.utils import timezone
from datetime import timedelta

# Get stats
total_executions = AgentExecution.objects.count()
completed = AgentExecution.objects.filter(status=ExecutionStatus.COMPLETED).count()
failed = AgentExecution.objects.filter(status=ExecutionStatus.FAILED).count()
running = AgentExecution.objects.filter(status=ExecutionStatus.RUNNING).count()

# Recent activity (24h)
yesterday = timezone.now() - timedelta(hours=24)
recent = AgentExecution.objects.filter(started_at__gte=yesterday).count()

# Bug stats
total_bugs = BugReport.objects.count()
pending_bugs = BugReport.objects.filter(fix_status__in=['detected', 'analyzing']).count()

# Registered agents
registered = list(_AGENT_REGISTRY.keys())

print(f'registered:{len(registered)}')
print(f'total:{total_executions}')
print(f'completed:{completed}')
print(f'failed:{failed}')
print(f'running:{running}')
print(f'recent:{recent}')
print(f'bugs:{total_bugs}')
print(f'pending_bugs:{pending_bugs}')
print(f'agents:{registered}')
"`,
        [],
        { silent: true },
      );

      spinner.stop();

      // Parse result
      const lines = result.stdout.split("\n");
      const stats: Record<string, string> = {};
      for (const line of lines) {
        const [key, value] = line.split(":");
        if (key && value) {
          stats[key.trim()] = value.trim();
        }
      }

      console.log(chalk.blue("\n=== Agent System Status ===\n"));

      console.log(chalk.yellow("Registered Agents:"));
      console.log(`  ${chalk.green(stats.registered || "0")} agents loaded`);

      console.log(chalk.yellow("\nExecution Statistics:"));
      console.log(`  Total:     ${stats.total || "0"}`);
      console.log(`  Completed: ${chalk.green(stats.completed || "0")}`);
      console.log(`  Failed:    ${chalk.red(stats.failed || "0")}`);
      console.log(`  Running:   ${chalk.cyan(stats.running || "0")}`);
      console.log(`  Last 24h:  ${stats.recent || "0"}`);

      console.log(chalk.yellow("\nBug Reports:"));
      console.log(`  Total:     ${stats.bugs || "0"}`);
      console.log(`  Pending:   ${chalk.yellow(stats.pending_bugs || "0")}`);

      // Health check
      const failRate =
        parseInt(stats.total || "0") > 0
          ? (parseInt(stats.failed || "0") / parseInt(stats.total || "1")) * 100
          : 0;

      console.log(chalk.yellow("\nSystem Health:"));
      if (failRate < 10) {
        console.log(
          chalk.green("  ✓ Healthy") +
            ` (${failRate.toFixed(1)}% failure rate)`,
        );
      } else if (failRate < 25) {
        console.log(
          chalk.yellow("  ⚠ Warning") +
            ` (${failRate.toFixed(1)}% failure rate)`,
        );
      } else {
        console.log(
          chalk.red("  ✗ Unhealthy") +
            ` (${failRate.toFixed(1)}% failure rate)`,
        );
      }
    } catch (error) {
      spinner.fail("Failed to fetch status");
      console.log(
        chalk.gray("\nMake sure Docker is running and the web service is up."),
      );
      console.log(chalk.gray("Run: cd lwm_core && make start-bg"));
    }
  });

// =============================================================================
// RUN COMMAND
// =============================================================================

agentCommand
  .command("run <agent> [request]")
  .description("Run an agent with a request")
  .option("-t, --task <id>", "Process a specific task by ID")
  .option("--dry-run", "Show what would be executed")
  .action(async (agent, request, options) => {
    // Validate agent
    if (!AGENT_INFO[agent]) {
      console.log(chalk.red(`\nUnknown agent: ${agent}`));
      console.log(chalk.gray("Available agents:"));
      for (const key of Object.keys(AGENT_INFO)) {
        console.log(chalk.gray(`  - ${key}`));
      }
      process.exit(1);
    }

    if (!request && !options.task) {
      console.log(chalk.red("\nProvide a request or --task option"));
      process.exit(1);
    }

    if (options.dryRun) {
      console.log(chalk.yellow("\n[DRY RUN] Would execute:"));
      console.log(`  Agent: ${agent}`);
      console.log(`  Request: ${request || `Task ${options.task}`}`);
      return;
    }

    const spinner = ora(`Running ${AGENT_INFO[agent].name}...`).start();

    try {
      const root = findWorkspaceRoot();
      const lwmCorePath = getDomainPath("lwm_core");

      // Build command
      let cmd = `cd "${lwmCorePath}" && docker compose exec -T web python manage.py run_agent`;
      if (options.task) {
        cmd += ` ${options.task}`;
      }
      cmd += ` --agent ${agent}`;
      if (request) {
        cmd += ` --request "${request.replace(/"/g, '\\"')}"`;
      }

      const result = await exec(cmd, [], { silent: true });

      spinner.stop();

      console.log(chalk.blue(`\n=== ${AGENT_INFO[agent].name} Result ===\n`));
      console.log(result.stdout);

      if (result.stderr) {
        console.log(chalk.yellow("\nWarnings:"));
        console.log(chalk.gray(result.stderr));
      }
    } catch (error: any) {
      spinner.fail("Agent execution failed");
      console.log(chalk.red(error.message || error));
    }
  });
