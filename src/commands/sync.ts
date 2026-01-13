import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import { readFile, writeFile } from "fs/promises";
import { existsSync } from "fs";
import { join } from "path";
import { exec } from "../utils/exec.js";
import {
  findWorkspaceRoot,
  getPackagePath,
  getDomainPath,
} from "../utils/paths.js";

export const syncCommand = new Command("sync").description(
  "Sync dependencies and versions across workspace",
);

/**
 * lw sync:ui
 * Update @lightwave-media/ui version across all domains
 */
syncCommand
  .command("ui")
  .description("Sync lightwave-ui version across all domains")
  .option("--dry-run", "Preview what would be updated")
  .action(async (options) => {
    const root = findWorkspaceRoot();
    const uiPath = getPackagePath("lightwave-ui");

    // Get current version
    const uiPackageJson = JSON.parse(
      await readFile(join(uiPath, "package.json"), "utf-8"),
    );
    const currentVersion = uiPackageJson.version;

    console.log(chalk.blue("\n=== Sync @lightwave-media/ui ===\n"));
    console.log(chalk.yellow("Current version:"), currentVersion);

    const { readdir } = await import("fs/promises");
    const domainsDir = join(root, "domains");
    const domains = await readdir(domainsDir);

    for (const domain of domains) {
      const domainPath = join(domainsDir, domain);
      const packageJsonPath = join(domainPath, "package.json");

      if (!existsSync(packageJsonPath)) continue;

      try {
        const packageJson = JSON.parse(
          await readFile(packageJsonPath, "utf-8"),
        );
        const deps = packageJson.dependencies || {};
        const devDeps = packageJson.devDependencies || {};

        const uiDep =
          deps["@lightwave-media/ui"] || devDeps["@lightwave-media/ui"];

        if (uiDep) {
          const installedVersion = uiDep.replace(/[\^~]/, "");
          if (installedVersion !== currentVersion) {
            console.log(
              chalk.yellow(`  ${domain}:`),
              chalk.red(installedVersion),
              "→",
              chalk.green(currentVersion),
            );

            if (!options.dryRun) {
              // Update package.json
              if (deps["@lightwave-media/ui"]) {
                deps["@lightwave-media/ui"] = `^${currentVersion}`;
              }
              if (devDeps["@lightwave-media/ui"]) {
                devDeps["@lightwave-media/ui"] = `^${currentVersion}`;
              }
              await writeFile(
                packageJsonPath,
                JSON.stringify(packageJson, null, 2) + "\n",
              );
            }
          } else {
            console.log(
              chalk.gray(`  ${domain}: up to date (${currentVersion})`),
            );
          }
        }
      } catch (err) {
        console.log(chalk.gray(`  ${domain}: error reading package.json`));
      }
    }

    if (options.dryRun) {
      console.log(chalk.yellow("\n(dry run - no changes made)"));
    } else {
      console.log(
        chalk.green("\n✓ Versions updated. Run pnpm install in each domain."),
      );
    }
  });

/**
 * lw sync:deps
 * Install/update dependencies across all domains
 */
syncCommand
  .command("deps")
  .description("Run pnpm install across all domains")
  .option("--domain <name>", "Only sync specific domain")
  .action(async (options) => {
    const root = findWorkspaceRoot();
    const { readdir } = await import("fs/promises");

    if (options.domain) {
      const domainPath = getDomainPath(options.domain);
      const spinner = ora(`Installing deps: ${options.domain}`).start();
      try {
        await exec("pnpm", ["install"], { cwd: domainPath, silent: true });
        spinner.succeed(`${options.domain}: deps installed`);
      } catch (err) {
        spinner.fail(`${options.domain}: failed`);
      }
      return;
    }

    const domainsDir = join(root, "domains");
    const domains = await readdir(domainsDir);

    console.log(chalk.blue("\n=== Syncing Dependencies ===\n"));

    for (const domain of domains) {
      const domainPath = join(domainsDir, domain);
      if (!existsSync(join(domainPath, "package.json"))) continue;

      const spinner = ora(`${domain}`).start();
      try {
        await exec("pnpm", ["install"], { cwd: domainPath, silent: true });
        spinner.succeed(`${domain}`);
      } catch (err) {
        spinner.fail(`${domain}`);
      }
    }
  });

/**
 * lw sync:python
 * Run uv sync across all domains
 */
syncCommand
  .command("python")
  .description("Sync Python dependencies across all domains")
  .action(async () => {
    const root = findWorkspaceRoot();
    const { readdir } = await import("fs/promises");
    const domainsDir = join(root, "domains");
    const domains = await readdir(domainsDir);

    console.log(chalk.blue("\n=== Syncing Python Dependencies ===\n"));

    for (const domain of domains) {
      const domainPath = join(domainsDir, domain);
      if (!existsSync(join(domainPath, "pyproject.toml"))) continue;

      const spinner = ora(`${domain}`).start();
      try {
        await exec(
          "docker",
          ["compose", "run", "--rm", "web", "uv", "sync", "--frozen"],
          {
            cwd: domainPath,
            silent: true,
          },
        );
        spinner.succeed(`${domain}`);
      } catch (err) {
        spinner.warn(`${domain} (containers not running?)`);
      }
    }
  });

/**
 * lw sync:versions
 * Show version comparison across workspace
 */
syncCommand
  .command("versions")
  .description("Show package versions across workspace")
  .action(async () => {
    const root = findWorkspaceRoot();
    const { readdir } = await import("fs/promises");

    console.log(chalk.blue("\n=== Package Versions ===\n"));

    // Packages
    console.log(chalk.yellow("Packages:"));
    const packagesDir = join(root, "packages");
    const packages = await readdir(packagesDir);

    for (const pkg of packages) {
      const pkgPath = join(packagesDir, pkg, "package.json");
      if (!existsSync(pkgPath)) continue;

      try {
        const packageJson = JSON.parse(await readFile(pkgPath, "utf-8"));
        console.log(chalk.gray(`  ${packageJson.name}:`), packageJson.version);
      } catch {}
    }

    // Domains
    console.log(chalk.yellow("\nDomains (@lightwave-media/ui):"));
    const domainsDir = join(root, "domains");
    const domains = await readdir(domainsDir);

    for (const domain of domains) {
      const pkgPath = join(domainsDir, domain, "package.json");
      if (!existsSync(pkgPath)) continue;

      try {
        const packageJson = JSON.parse(await readFile(pkgPath, "utf-8"));
        const uiVersion =
          packageJson.dependencies?.["@lightwave-media/ui"] ||
          packageJson.devDependencies?.["@lightwave-media/ui"] ||
          "(not installed)";
        console.log(chalk.gray(`  ${domain}:`), uiVersion);
      } catch {}
    }
  });

/**
 * lw sync:check
 * Check for outdated dependencies
 */
syncCommand
  .command("check")
  .description("Check for outdated dependencies")
  .action(async () => {
    const root = findWorkspaceRoot();

    console.log(chalk.blue("\n=== Checking for Outdated Dependencies ===\n"));

    // Check packages
    console.log(chalk.yellow("lightwave-ui:"));
    await exec("pnpm", ["outdated"], { cwd: getPackagePath("lightwave-ui") });

    console.log(chalk.yellow("\nlightwave-cli:"));
    await exec("pnpm", ["outdated"], { cwd: getPackagePath("lightwave-cli") });
  });

/**
 * lw sync:static
 * Sync static assets to CDN (S3)
 *
 * CDN Pattern: cdn.lightwave-media.ltd/static/{domain}/
 * S3 Bucket: lightwave-cdn-static (assumed, configurable via env)
 */
syncCommand
  .command("static")
  .description("Sync static assets to CDN (S3)")
  .option("--domain <name>", "Domain to sync (default: detect from cwd)")
  .option("--dry-run", "Preview what would be uploaded")
  .option("--delete", "Delete files from S3 that don't exist locally")
  .action(async (options) => {
    const root = findWorkspaceRoot();
    const { readdir, stat } = await import("fs/promises");

    // Determine domain
    let domain = options.domain;
    if (!domain) {
      // Try to detect from current directory
      const cwd = process.cwd();
      const domainsDir = join(root, "domains");
      if (cwd.startsWith(domainsDir)) {
        const relativePath = cwd.slice(domainsDir.length + 1);
        domain = relativePath.split("/")[0];
      }
    }

    if (!domain) {
      console.error(
        chalk.red("Error: Could not detect domain. Use --domain <name>"),
      );
      process.exit(1);
    }

    const domainPath = getDomainPath(domain);
    const staticPath = join(domainPath, "static");

    if (!existsSync(staticPath)) {
      console.error(
        chalk.red(`Error: No static directory found at ${staticPath}`),
      );
      process.exit(1);
    }

    // S3 bucket and path
    const s3Bucket =
      process.env.LIGHTWAVE_CDN_BUCKET || "cdn.lightwave-media.ltd";
    const s3Path = `s3://${s3Bucket}/static/${domain}/`;

    console.log(chalk.blue("\n=== Sync Static Assets to CDN ===\n"));
    console.log(chalk.gray("Domain:"), domain);
    console.log(chalk.gray("Local:"), staticPath);
    console.log(chalk.gray("S3:"), s3Path);
    console.log();

    const args = ["s3", "sync", staticPath, s3Path];

    if (options.dryRun) {
      args.push("--dryrun");
    }
    if (options.delete) {
      args.push("--delete");
    }

    // Add common exclusions
    args.push("--exclude", ".DS_Store");
    args.push("--exclude", "*.map");

    const spinner = ora("Syncing to S3...").start();

    try {
      const result = await exec("aws", args, { silent: false });
      spinner.succeed("Static assets synced to CDN");

      if (options.dryRun) {
        console.log(chalk.yellow("\n(dry run - no changes made)"));
      }

      console.log(
        chalk.green(
          `\n✓ Assets available at: https://cdn.lightwave-media.ltd/static/${domain}/`,
        ),
      );
    } catch (err) {
      spinner.fail("Failed to sync static assets");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });
