/**
 * Auth commands - scaffolds Django auth templates with React islands
 *
 * Commands:
 *   lw auth scaffold <domain>     Generate auth templates for a domain
 *   lw auth list                  List available auth variants
 *   lw auth check <domain>        Check auth setup for a domain
 */

import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import { mkdir, writeFile, readFile } from "fs/promises";
import { existsSync } from "fs";
import { join } from "path";
import { getDomainPath, getPackagePath } from "../utils/paths.js";

export const authCommand = new Command("auth")
  .alias("a")
  .description("Auth template scaffolding with React islands");

/**
 * Auth template variants available
 */
const AUTH_VARIANTS = [
  "login-combined",
  "signup-combined",
  "forgot-password-step-1",
  "forgot-password-step-2",
  "forgot-password-step-3",
  "forgot-password-step-4",
  "email-verification",
] as const;

type AuthVariant = (typeof AUTH_VARIANTS)[number];

/**
 * Django template mappings for auth variants
 */
const TEMPLATE_MAPPINGS: Record<AuthVariant, { path: string; description: string }> = {
  "login-combined": {
    path: "account/login.html",
    description: "Login page with email/password and social auth",
  },
  "signup-combined": {
    path: "account/signup.html",
    description: "Signup page with form and social auth",
  },
  "forgot-password-step-1": {
    path: "account/password_reset.html",
    description: "Password reset request form",
  },
  "forgot-password-step-2": {
    path: "account/password_reset_done.html",
    description: "Check email confirmation page",
  },
  "forgot-password-step-3": {
    path: "account/password_reset_from_key.html",
    description: "Set new password form",
  },
  "forgot-password-step-4": {
    path: "account/password_reset_from_key_done.html",
    description: "Password reset success page",
  },
  "email-verification": {
    path: "account/verification_sent.html",
    description: "Email verification sent page",
  },
};

/**
 * lw auth list
 * List available auth variants
 */
authCommand
  .command("list")
  .description("List available auth variants")
  .action(async () => {
    console.log(chalk.blue("\n=== Auth Variants ===\n"));

    for (const variant of AUTH_VARIANTS) {
      const mapping = TEMPLATE_MAPPINGS[variant];
      console.log(chalk.yellow(variant));
      console.log(chalk.gray(`  Template: ${mapping.path}`));
      console.log(chalk.gray(`  ${mapping.description}\n`));
    }
  });

/**
 * lw auth scaffold <domain>
 * Generate auth templates for a domain
 */
authCommand
  .command("scaffold <domain>")
  .description("Generate auth templates with React islands (e.g., lw auth scaffold lightwave-media.site)")
  .option("--dry-run", "Preview what would be created")
  .option("--force", "Overwrite existing templates")
  .option("--variant <variant>", "Only scaffold a specific variant")
  .action(async (domain: string, options) => {
    const domainPath = getDomainPath(domain);

    if (!existsSync(domainPath)) {
      console.log(chalk.red(`Domain not found: ${domain}`));
      console.log(chalk.gray(`Expected: ${domainPath}`));
      return;
    }

    const templatesDir = join(domainPath, "templates");
    if (!existsSync(templatesDir)) {
      console.log(chalk.red(`Templates directory not found: ${templatesDir}`));
      return;
    }

    console.log(chalk.blue(`\n=== Auth Scaffold: ${domain} ===\n`));

    const variants = options.variant
      ? [options.variant as AuthVariant]
      : AUTH_VARIANTS;

    const spinner = ora("Generating templates...").start();

    try {
      for (const variant of variants) {
        if (!TEMPLATE_MAPPINGS[variant]) {
          spinner.warn(`Unknown variant: ${variant}`);
          continue;
        }

        const mapping = TEMPLATE_MAPPINGS[variant];
        const templatePath = join(templatesDir, mapping.path);
        const templateDir = join(templatePath, "..");

        // Check if template already exists
        if (existsSync(templatePath) && !options.force) {
          spinner.info(`Skipping ${mapping.path} (exists, use --force to overwrite)`);
          continue;
        }

        if (options.dryRun) {
          spinner.info(`Would create: ${mapping.path}`);
          continue;
        }

        // Create directory if needed
        await mkdir(templateDir, { recursive: true });

        // Generate template content
        const content = generateAuthTemplate(variant, domain);
        await writeFile(templatePath, content);
        spinner.succeed(`Created: ${mapping.path}`);
      }

      spinner.stop();

      if (!options.dryRun) {
        console.log(chalk.green("\n✓ Auth templates created!"));
        console.log(chalk.gray("\nNext steps:"));
        console.log(chalk.gray("  1. Update assets/javascript/islands.ts to register auth islands"));
        console.log(chalk.gray("  2. Configure social providers in settings.py"));
        console.log(chalk.gray("  3. Run: pnpm build"));
      }
    } catch (err) {
      spinner.fail(`Failed: ${err}`);
    }
  });

/**
 * lw auth check <domain>
 * Check auth setup for a domain
 */
authCommand
  .command("check <domain>")
  .description("Check auth setup and identify missing templates")
  .action(async (domain: string) => {
    const domainPath = getDomainPath(domain);

    if (!existsSync(domainPath)) {
      console.log(chalk.red(`Domain not found: ${domain}`));
      return;
    }

    const templatesDir = join(domainPath, "templates");

    console.log(chalk.blue(`\n=== Auth Check: ${domain} ===\n`));

    let allGood = true;

    for (const variant of AUTH_VARIANTS) {
      const mapping = TEMPLATE_MAPPINGS[variant];
      const templatePath = join(templatesDir, mapping.path);
      const exists = existsSync(templatePath);

      if (exists) {
        // Check if it uses islands
        const content = await readFile(templatePath, "utf-8");
        const usesIslands = content.includes("data-island") || content.includes("data-props");

        if (usesIslands) {
          console.log(chalk.green(`✓ ${variant}`));
          console.log(chalk.gray(`  ${mapping.path} (islands enabled)`));
        } else {
          console.log(chalk.yellow(`○ ${variant}`));
          console.log(chalk.gray(`  ${mapping.path} (exists, no islands)`));
          allGood = false;
        }
      } else {
        console.log(chalk.red(`✗ ${variant}`));
        console.log(chalk.gray(`  ${mapping.path} (missing)`));
        allGood = false;
      }
    }

    if (allGood) {
      console.log(chalk.green("\n✓ All auth templates configured with islands!"));
    } else {
      console.log(chalk.yellow("\n⚠ Some templates need attention"));
      console.log(chalk.gray("  Run: lw auth scaffold " + domain));
    }
  });

/**
 * Generate Django template content for an auth variant
 *
 * Auth templates extend base.html directly for a clean, standalone look.
 * Same React components, different variant via config-driven props.
 * Follows same data-* pattern as hero-root for consistency.
 */
function generateAuthTemplate(variant: AuthVariant, _domain: string): string {
  // Auth templates extend base.html directly (standalone, minimal)
  const baseTemplate = `{% extends "base.html" %}
{% load island_tags %}

{% block title %}${getPageTitle(variant)} | {{ site_title }}{% endblock %}

{% block content %}
{% auth_form_props as auth_form_props %}
{# Auth Island - variant "${variant}", config-driven via props #}
<div id="auth-root"
     data-auth-variant="${variant}"
     data-auth-props="{{ auth_form_props }}"
     data-auth-config="{{ auth_config }}"
     data-company-info="{{ company_info }}"
     data-island-config="{{ island_config }}">
</div>
{% endblock %}
`;

  return baseTemplate;
}

/**
 * Convert variant name to component name
 */
function variantToComponentName(variant: AuthVariant): string {
  const mapping: Record<AuthVariant, string> = {
    "login-combined": "LoginCardCombined",
    "signup-combined": "SignupCardCombined",
    "forgot-password-step-1": "Step1ForgotPassword",
    "forgot-password-step-2": "Step2CheckEmail",
    "forgot-password-step-3": "Step3SetNewPassword",
    "forgot-password-step-4": "Step4Success",
    "email-verification": "EmailVerificationCheckEmail",
  };
  return mapping[variant];
}

/**
 * Get page title for variant
 */
function getPageTitle(variant: AuthVariant): string {
  const titles: Record<AuthVariant, string> = {
    "login-combined": "Sign In",
    "signup-combined": "Create Account",
    "forgot-password-step-1": "Reset Password",
    "forgot-password-step-2": "Check Your Email",
    "forgot-password-step-3": "Set New Password",
    "forgot-password-step-4": "Password Reset Complete",
    "email-verification": "Verify Your Email",
  };
  return titles[variant];
}
