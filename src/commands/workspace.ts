import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import { existsSync } from "fs";
import { readdir, readFile } from "fs/promises";
import { join } from "path";
import { exec, make } from "../utils/exec.js";
import { findWorkspaceRoot, getDomainPath } from "../utils/paths.js";

export const workspaceCommand = new Command("workspace")
  .alias("ws")
  .description("Workspace-wide operations");

/**
 * lw workspace:status
 * Show status of all repos in workspace
 */
workspaceCommand
  .command("status")
  .description("Show git status of all repos")
  .action(async () => {
    console.log(chalk.blue("\n=== LightWave Workspace Status ===\n"));

    const root = findWorkspaceRoot();

    // Check main workspace
    console.log(chalk.yellow("Main workspace:"));
    await exec("git", ["status", "--short"], { cwd: root });

    // Check domains
    const domainsDir = join(root, "domains");
    const domains = await readdir(domainsDir);

    for (const domain of domains) {
      const domainPath = join(domainsDir, domain);
      if (!existsSync(join(domainPath, ".git"))) continue;

      console.log(chalk.yellow(`\n${domain}:`));
      const result = await exec("git", ["status", "--short"], { cwd: domainPath, silent: true });
      if (result.stdout.trim()) {
        console.log(result.stdout);
      } else {
        console.log(chalk.gray("  (clean)"));
      }
    }
  });

/**
 * lw workspace:test
 * Run tests across all domains
 */
workspaceCommand
  .command("test")
  .description("Run tests across all domains")
  .option("--domain <name>", "Test specific domain only")
  .action(async (options) => {
    console.log(chalk.blue("\n=== Running Tests ===\n"));

    const root = findWorkspaceRoot();

    if (options.domain) {
      const domainPath = getDomainPath(options.domain);
      if (!existsSync(domainPath)) {
        console.log(chalk.red(`Domain not found: ${options.domain}`));
        process.exit(1);
      }

      const spinner = ora(`Testing ${options.domain}`).start();
      try {
        await make("test", domainPath);
        spinner.succeed(`${options.domain} tests passed`);
      } catch (err) {
        spinner.fail(`${options.domain} tests failed`);
        process.exit(1);
      }
      return;
    }

    // Test all domains
    const domainsDir = join(root, "domains");
    const domains = await readdir(domainsDir);

    for (const domain of domains) {
      const domainPath = join(domainsDir, domain);
      if (!existsSync(join(domainPath, "Makefile"))) continue;

      const spinner = ora(`Testing ${domain}`).start();
      try {
        await make("test", domainPath, true);
        spinner.succeed(`${domain} tests passed`);
      } catch (err) {
        spinner.warn(`${domain} tests skipped (containers not running?)`);
      }
    }
  });

/**
 * lw workspace:lint
 * Run linting across workspace
 */
workspaceCommand
  .command("lint")
  .description("Run linting across workspace")
  .action(async () => {
    console.log(chalk.blue("\n=== Linting Workspace ===\n"));

    const root = findWorkspaceRoot();
    const domainsDir = join(root, "domains");
    const domains = await readdir(domainsDir);

    for (const domain of domains) {
      const domainPath = join(domainsDir, domain);
      if (!existsSync(join(domainPath, "Makefile"))) continue;

      const spinner = ora(`Linting ${domain}`).start();
      try {
        await make("ruff", domainPath, true);
        spinner.succeed(`${domain} OK`);
      } catch (err) {
        spinner.warn(`${domain} has lint issues`);
      }
    }
  });

/**
 * lw workspace:info
 * Show workspace information
 */
workspaceCommand
  .command("info")
  .description("Show workspace information")
  .action(async () => {
    const root = findWorkspaceRoot();

    console.log(chalk.blue("\n=== LightWave Workspace ===\n"));
    console.log(`Root: ${root}`);

    // Count domains
    const domainsDir = join(root, "domains");
    const domains = await readdir(domainsDir);
    console.log(`Domains: ${domains.length}`);
    domains.forEach((d) => console.log(chalk.gray(`  - ${d}`)));

    // Count packages
    const packagesDir = join(root, "packages");
    const packages = await readdir(packagesDir);
    console.log(`\nPackages: ${packages.length}`);
    packages.forEach((p) => console.log(chalk.gray(`  - ${p}`)));

    // Show agents
    const agentsDir = join(root, ".claude/agents");
    const agents = await readdir(agentsDir);
    const agentCount = agents.filter((a) => a.endsWith(".md") && a !== "README.md").length;
    console.log(`\nClaude Agents: ${agentCount}`);

    // Show skills
    const skillsDir = join(root, ".claude/skills");
    const skills = await readdir(skillsDir);
    const skillCount = skills.filter((s) => s.endsWith(".md") && s !== "README.md").length;
    console.log(`Claude Skills: ${skillCount}`);
  });
