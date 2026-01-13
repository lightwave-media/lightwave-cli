import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import { existsSync } from "fs";
import { readdir } from "fs/promises";
import { join } from "path";
import { exec, make } from "../utils/exec.js";
import {
  findWorkspaceRoot,
  getPackagePath,
  getDomainPath,
} from "../utils/paths.js";

export const buildCommand = new Command("build").description(
  "Build commands for packages and domains",
);

/**
 * lw build:ui
 * Build the lightwave-ui package
 */
buildCommand
  .command("ui")
  .description("Build lightwave-ui package")
  .action(async () => {
    const spinner = ora("Building lightwave-ui").start();
    try {
      const uiPath = getPackagePath("lightwave-ui");
      await exec("pnpm", ["build"], { cwd: uiPath });
      spinner.succeed("Built lightwave-ui");
    } catch (err) {
      spinner.fail(`Build failed: ${err}`);
      process.exit(1);
    }
  });

/**
 * lw build:domain <name>
 * Build a specific domain (npm assets)
 */
buildCommand
  .command("domain <name>")
  .description("Build a domain project (e.g., cineos.io)")
  .action(async (name: string) => {
    const spinner = ora(`Building ${name}`).start();
    try {
      const domainPath = getDomainPath(name);
      if (!existsSync(domainPath)) {
        spinner.fail(`Domain not found: ${name}`);
        process.exit(1);
      }
      await make("npm-build", domainPath);
      spinner.succeed(`Built ${name}`);
    } catch (err) {
      spinner.fail(`Build failed: ${err}`);
      process.exit(1);
    }
  });

/**
 * lw build:all
 * Build everything
 */
buildCommand
  .command("all")
  .description("Build all packages and domains")
  .action(async () => {
    console.log(chalk.blue("\n=== Building LightWave Workspace ===\n"));

    // Build packages first
    const packagesSpinner = ora("Building packages").start();
    try {
      const uiPath = getPackagePath("lightwave-ui");
      await exec("pnpm", ["build"], { cwd: uiPath, silent: true });
      packagesSpinner.succeed("Built packages");
    } catch (err) {
      packagesSpinner.fail(`Package build failed: ${err}`);
    }

    // Build domains
    const root = findWorkspaceRoot();
    const domainsDir = join(root, "domains");
    const domains = await readdir(domainsDir);

    for (const domain of domains) {
      const domainPath = join(domainsDir, domain);
      if (!existsSync(join(domainPath, "Makefile"))) continue;

      const spinner = ora(`Building ${domain}`).start();
      try {
        await make("npm-build", domainPath, true);
        spinner.succeed(`Built ${domain}`);
      } catch (err) {
        spinner.warn(`${domain} build skipped (containers not running?)`);
      }
    }

    console.log(chalk.green("\n✓ Build complete\n"));
  });

/**
 * lw build:typecheck
 * Run TypeScript type checking across workspace
 */
buildCommand
  .command("typecheck")
  .description("Run TypeScript type checking")
  .action(async () => {
    console.log(chalk.blue("\n=== Type Checking ===\n"));

    // Check lightwave-ui
    const uiSpinner = ora("Checking lightwave-ui").start();
    try {
      const uiPath = getPackagePath("lightwave-ui");
      await exec("pnpm", ["typecheck"], { cwd: uiPath, silent: true });
      uiSpinner.succeed("lightwave-ui OK");
    } catch (err) {
      uiSpinner.fail("lightwave-ui has type errors");
    }

    // Check lightwave-cli
    const cliSpinner = ora("Checking lightwave-cli").start();
    try {
      const cliPath = getPackagePath("lightwave-cli");
      await exec("pnpm", ["typecheck"], { cwd: cliPath, silent: true });
      cliSpinner.succeed("lightwave-cli OK");
    } catch (err) {
      cliSpinner.fail("lightwave-cli has type errors");
    }
  });
