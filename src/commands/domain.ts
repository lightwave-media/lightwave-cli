import { Command } from "commander";
import chalk from "chalk";
import { join } from "path";
import { findWorkspaceRoot } from "../utils/paths.js";
import { exec } from "../utils/exec.js";

export const domainCommand = new Command("domain").description(
  "Manage site domains - create, update, list, DNS setup",
);

/**
 * Helper to run setup_domain management command
 */
async function runSetupDomain(
  args: string[],
  options: { silent?: boolean } = {},
): Promise<{ stdout: string; stderr: string }> {
  const root = findWorkspaceRoot();
  const lwmPath = join(root, "lightwave-platform", "lwm_core");

  return exec(
    "docker",
    [
      "compose",
      "exec",
      "-T",
      "web",
      "python",
      "manage.py",
      "setup_domain",
      ...args,
    ],
    { cwd: lwmPath, silent: options.silent ?? true },
  );
}

// =============================================================================
// lw domain list
// =============================================================================

domainCommand
  .command("list")
  .description("List all configured domains")
  .option("--tenant <schema>", "Tenant schema name")
  .action(async (options) => {
    const args: string[] = ["--list"];
    if (options.tenant) args.push("--tenant", options.tenant);
    console.log(chalk.blue("\n=== Configured Domains ===\n"));

    try {
      await runSetupDomain(args, { silent: false });
    } catch (err: any) {
      console.log(chalk.red(`Failed to list domains: ${err.message}`));
    }
  });

// =============================================================================
// lw domain show <domain>
// =============================================================================

domainCommand
  .command("show <domain>")
  .description("Show domain configuration details")
  .option("--tenant <schema>", "Tenant schema name")
  .action(async (domain: string, options) => {
    const args: string[] = [domain];
    if (options.tenant) args.push("--tenant", options.tenant);

    try {
      await runSetupDomain(args, { silent: false });
    } catch (err: any) {
      console.log(chalk.red(`Failed to show domain: ${err.message}`));
    }
  });

// =============================================================================
// lw domain create <domain>
// =============================================================================

domainCommand
  .command("create <domain>")
  .description("Create a new domain configuration")
  .option("--brand-name <name>", "Brand name (default: derived from domain)")
  .option("--brand-tagline <tagline>", "Brand tagline")
  .option(
    "--brand-color <color>",
    "Theme color (blue-dark, indigo, purple, violet)",
  )
  .option("--auth-mode <mode>", "Auth mode (subscription, ecommerce, private)")
  .option("--use-teams", "Enable teams feature")
  .option("--use-subscriptions", "Enable subscriptions feature")
  .option("--use-chat", "Enable chat feature")
  .option("--dry-run", "Preview without creating")
  .option("--tenant <schema>", "Tenant schema name")
  .action(async (domain: string, options) => {
    const args: string[] = [domain, "--create"];

    if (options.tenant) args.push("--tenant", options.tenant);
    if (options.brandName) args.push("--brand-name", options.brandName);
    if (options.brandTagline)
      args.push("--brand-tagline", options.brandTagline);
    if (options.brandColor) args.push("--brand-color", options.brandColor);
    if (options.authMode) args.push("--auth-mode", options.authMode);
    if (options.useTeams) args.push("--use-teams");
    if (options.useSubscriptions) args.push("--use-subscriptions");
    if (options.useChat) args.push("--use-chat");
    if (options.dryRun) args.push("--dry-run");

    console.log(chalk.blue(`\nCreating domain: ${domain}\n`));

    try {
      await runSetupDomain(args, { silent: false });
      if (!options.dryRun) {
        console.log(chalk.green(`\n✓ Domain created: ${domain}`));
        console.log(chalk.gray("  Run: lw domain show " + domain));
      }
    } catch (err: any) {
      console.log(chalk.red(`Failed to create domain: ${err.message}`));
    }
  });

// =============================================================================
// lw domain update <domain>
// =============================================================================

domainCommand
  .command("update <domain>")
  .description("Update domain configuration")
  .option("--brand-name <name>", "Brand name")
  .option("--brand-tagline <tagline>", "Brand tagline")
  .option(
    "--brand-color <color>",
    "Theme color (blue-dark, indigo, purple, violet)",
  )
  .option("--auth-mode <mode>", "Auth mode (subscription, ecommerce, private)")
  .option("--use-teams", "Enable teams feature")
  .option("--use-subscriptions", "Enable subscriptions feature")
  .option("--use-chat", "Enable chat feature")
  .option("--dry-run", "Preview without updating")
  .option("--tenant <schema>", "Tenant schema name")
  .action(async (domain: string, options) => {
    const args: string[] = [domain, "--update"];

    if (options.tenant) args.push("--tenant", options.tenant);
    if (options.brandName) args.push("--brand-name", options.brandName);
    if (options.brandTagline)
      args.push("--brand-tagline", options.brandTagline);
    if (options.brandColor) args.push("--brand-color", options.brandColor);
    if (options.authMode) args.push("--auth-mode", options.authMode);
    if (options.useTeams) args.push("--use-teams");
    if (options.useSubscriptions) args.push("--use-subscriptions");
    if (options.useChat) args.push("--use-chat");
    if (options.dryRun) args.push("--dry-run");

    console.log(chalk.blue(`\nUpdating domain: ${domain}\n`));

    try {
      await runSetupDomain(args, { silent: false });
      if (!options.dryRun) {
        console.log(chalk.green(`\n✓ Domain updated: ${domain}`));
      }
    } catch (err: any) {
      console.log(chalk.red(`Failed to update domain: ${err.message}`));
    }
  });

// =============================================================================
// lw domain setup-dns <domain>
// =============================================================================

domainCommand
  .command("setup-dns <domain>")
  .description("Set up DNS records in Cloudflare")
  .option("--dry-run", "Preview without creating DNS records")
  .option("--tenant <schema>", "Tenant schema name")
  .action(async (domain: string, options) => {
    const args: string[] = [domain, "--setup-dns"];

    if (options.tenant) args.push("--tenant", options.tenant);
    if (options.dryRun) args.push("--dry-run");

    console.log(chalk.blue(`\nSetting up DNS for: ${domain}\n`));

    try {
      await runSetupDomain(args, { silent: false });
      if (!options.dryRun) {
        console.log(chalk.green(`\n✓ DNS configured for: ${domain}`));
      }
    } catch (err: any) {
      console.log(chalk.red(`Failed to setup DNS: ${err.message}`));
    }
  });

// =============================================================================
// lw domain zones
// =============================================================================

domainCommand
  .command("zones")
  .description("List Cloudflare zones")
  .action(async () => {
    console.log(chalk.blue("\n=== Cloudflare Zones ===\n"));

    try {
      await runSetupDomain(["--zones"], { silent: false });
    } catch (err: any) {
      console.log(chalk.red(`Failed to list zones: ${err.message}`));
    }
  });
