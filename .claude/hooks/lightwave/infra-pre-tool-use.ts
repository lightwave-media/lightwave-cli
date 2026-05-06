#!/usr/bin/env bun
/**
 * Infrastructure PreToolUse Hook
 *
 * Domain-specific validations for Infrastructure/Terragrunt work:
 * 1. AWS_PROFILE verification - must be lightwave-admin-new
 * 2. Environment awareness - detect prod vs non-prod from path
 * 3. Terragrunt config reading - inject context before changes
 * 4. State file safety - warn about state-modifying operations
 *
 * Aligned with "hallucination prevention" - forces reading configs before changes.
 */

import type { PreToolUseHookInput, PreToolUseHookOutput } from "./types";
import { existsSync, readFileSync } from "fs";
import { dirname, join, relative } from "path";

const INFRA_ROOT = "/Users/joelschaeffer/dev/lightwave-media/Infrastructure";
const REQUIRED_AWS_PROFILE = "lightwave-admin-new";

// Dangerous Terragrunt/Tofu commands that need extra scrutiny
const DANGEROUS_COMMANDS = [
  "terragrunt destroy",
  "terragrunt apply",
  "tofu destroy",
  "tofu apply",
  "terragrunt run-all destroy",
  "terragrunt run-all apply",
  "terraform destroy",
  "terraform apply",
];

// State-modifying commands
const STATE_COMMANDS = [
  "terragrunt state",
  "tofu state",
  "terraform state",
  "terragrunt import",
  "tofu import",
  "terraform import",
];

// =============================================================================
// Validation Functions
// =============================================================================

interface ValidationResult {
  valid: boolean;
  warning?: string;
  context?: string;
  block?: boolean;
}

/**
 * Production-tier detection.
 *
 * A command is treated as targeting production if any of the following
 * contains `/prod/` or `/_global/`:
 *   - the command text itself (e.g. `cd live/prod/... && terragrunt apply`)
 *   - the `--terragrunt-working-dir` target path
 *   - the current working directory
 *
 * `_global` is treated as production-tier per the Infrastructure layout
 * (cross-region prod resources). Catalog/module repos and `live/non-prod/*`
 * are non-prod.
 *
 * Pure function — exported for unit testing without env mocking.
 */
export function isProductionContext(args: {
  command: string;
  targetPath: string;
  cwd: string;
}): boolean {
  const { command, targetPath, cwd } = args;
  return (
    command.includes("/prod/") ||
    command.includes("/_global/") ||
    targetPath.includes("/prod/") ||
    targetPath.includes("/_global/") ||
    cwd.includes("/prod/") ||
    cwd.includes("/_global/")
  );
}

/**
 * Verify AWS_PROFILE is set correctly
 */
function validateAwsProfile(): ValidationResult {
  const awsProfile = process.env.AWS_PROFILE;

  if (!awsProfile) {
    return {
      valid: false,
      block: true,
      warning: `❌ AWS_PROFILE is not set. Required: ${REQUIRED_AWS_PROFILE}`,
      context:
        "Run `export AWS_PROFILE=lightwave-admin-new` before executing AWS/Terragrunt commands.",
    };
  }

  if (awsProfile !== REQUIRED_AWS_PROFILE) {
    return {
      valid: false,
      block: true,
      warning: `❌ AWS_PROFILE is "${awsProfile}" but must be "${REQUIRED_AWS_PROFILE}"`,
      context: `Run \`export AWS_PROFILE=${REQUIRED_AWS_PROFILE}\` to fix this.`,
    };
  }

  return { valid: true };
}

/**
 * Detect environment (prod/non-prod) from file path
 */
function detectEnvironment(
  filePath: string,
): "prod" | "non-prod" | "_global" | "unknown" {
  const relativePath = relative(INFRA_ROOT, filePath);

  if (relativePath.startsWith("live/prod/")) return "prod";
  if (relativePath.startsWith("live/non-prod/")) return "non-prod";
  if (relativePath.startsWith("live/_global/")) return "_global";

  return "unknown";
}

/**
 * Read account.hcl for context
 */
function readAccountConfig(filePath: string): string | null {
  // Walk up from file path to find account.hcl
  let dir = dirname(filePath);
  const liveDir = join(INFRA_ROOT, "live");

  while (dir.startsWith(liveDir) && dir !== liveDir) {
    const accountHcl = join(dir, "account.hcl");
    if (existsSync(accountHcl)) {
      try {
        return readFileSync(accountHcl, "utf-8");
      } catch {
        return null;
      }
    }
    dir = dirname(dir);
  }

  return null;
}

/**
 * Read region.hcl for context
 */
function readRegionConfig(filePath: string): string | null {
  let dir = dirname(filePath);
  const liveDir = join(INFRA_ROOT, "live");

  while (dir.startsWith(liveDir) && dir !== liveDir) {
    const regionHcl = join(dir, "region.hcl");
    if (existsSync(regionHcl)) {
      try {
        return readFileSync(regionHcl, "utf-8");
      } catch {
        return null;
      }
    }
    dir = dirname(dir);
  }

  return null;
}

/**
 * Validate Write/Edit operations on Infrastructure files
 */
function validateInfraFileOperation(filePath: string): ValidationResult {
  const relativePath = relative(INFRA_ROOT, filePath);
  const env = detectEnvironment(filePath);

  // Check if modifying live infrastructure
  if (relativePath.startsWith("live/")) {
    const accountConfig = readAccountConfig(filePath);
    const regionConfig = readRegionConfig(filePath);

    let context = `\n📍 Infrastructure Context:\n`;
    context += `   Environment: ${env}\n`;
    context += `   Path: ${relativePath}\n`;

    if (accountConfig) {
      const accountMatch = accountConfig.match(/account_name\s*=\s*"([^"]+)"/);
      const accountIdMatch = accountConfig.match(
        /aws_account_id\s*=\s*"([^"]+)"/,
      );
      if (accountMatch) context += `   Account: ${accountMatch[1]}\n`;
      if (accountIdMatch) context += `   Account ID: ${accountIdMatch[1]}\n`;
    }

    if (regionConfig) {
      const regionMatch = regionConfig.match(/aws_region\s*=\s*"([^"]+)"/);
      if (regionMatch) context += `   Region: ${regionMatch[1]}\n`;
    }

    // Prod environment gets extra warning
    if (env === "prod") {
      return {
        valid: true,
        warning: `⚠️ PRODUCTION INFRASTRUCTURE: ${relativePath}`,
        context: context + `\n🚨 This is PRODUCTION. Double-check all changes.`,
      };
    }

    return {
      valid: true,
      context,
    };
  }

  // Catalog changes are generally safer
  if (relativePath.startsWith("catalog/")) {
    return {
      valid: true,
      context: `📦 Modifying catalog module: ${relativePath}\n   This affects all environments using this module.`,
    };
  }

  return { valid: true };
}

/**
 * Validate Bash commands for Terragrunt/Tofu operations
 */
function validateBashCommand(command: string): ValidationResult {
  const isTerragruntCmd =
    command.includes("terragrunt") ||
    command.includes("tofu") ||
    command.includes("terraform");

  if (!isTerragruntCmd) {
    return { valid: true };
  }

  // First, verify AWS_PROFILE
  const profileCheck = validateAwsProfile();
  if (!profileCheck.valid) {
    return profileCheck;
  }

  const cwd = process.cwd();

  // Check for dangerous commands. Production tier denies; non-prod warns.
  for (const dangerous of DANGEROUS_COMMANDS) {
    if (command.includes(dangerous)) {
      const pathMatch = command.match(
        /--terragrunt-working-dir[=\s]+"?([^"\s]+)"?/,
      );
      const targetPath = pathMatch ? pathMatch[1] : "current directory";

      if (isProductionContext({ command, targetPath, cwd })) {
        return {
          valid: false,
          block: true,
          warning: `🚨 BLOCKED: destructive production command: ${dangerous}`,
          context: `Target: ${targetPath}\n\nThis hook denies destructive Terragrunt/Tofu/Terraform ops (apply, destroy, run-all variants) against production paths (live/prod/*, live/_global/*). If intentional, run from a non-prod context.`,
        };
      }

      return {
        valid: true,
        warning: `⚠️ Destructive command (non-prod): ${dangerous}`,
        context: `Target: ${targetPath}\nNon-prod tier — proceeding with warning. Review the plan output before proceeding.`,
      };
    }
  }

  // Check for state commands. Production tier denies; non-prod warns.
  for (const stateCmd of STATE_COMMANDS) {
    if (command.includes(stateCmd)) {
      const pathMatch = command.match(
        /--terragrunt-working-dir[=\s]+"?([^"\s]+)"?/,
      );
      const targetPath = pathMatch ? pathMatch[1] : "current directory";

      if (isProductionContext({ command, targetPath, cwd })) {
        return {
          valid: false,
          block: true,
          warning: `🔒 BLOCKED: state operation against production: ${stateCmd}`,
          context: `State operations against production can cause drift and are denied by policy. Use a non-prod context if you need to inspect state without modification.`,
        };
      }

      return {
        valid: true,
        warning: `🔒 State-modifying command (non-prod): ${stateCmd}`,
        context: `State operations can cause drift. Ensure you understand the implications.`,
      };
    }
  }

  // For plan commands, just add context
  if (
    command.includes("plan") ||
    command.includes("validate") ||
    command.includes("init")
  ) {
    return {
      valid: true,
      context: `✅ AWS_PROFILE=${REQUIRED_AWS_PROFILE} verified`,
    };
  }

  return {
    valid: true,
    context: `✅ AWS_PROFILE=${REQUIRED_AWS_PROFILE} verified`,
  };
}

// =============================================================================
// Main Hook Logic
// =============================================================================

async function readInput(): Promise<PreToolUseHookInput> {
  const chunks: Buffer[] = [];
  for await (const chunk of Bun.stdin.stream()) {
    chunks.push(Buffer.from(chunk));
  }
  const input = Buffer.concat(chunks).toString("utf-8");
  return JSON.parse(input);
}

async function main() {
  const input = await readInput();

  let result: ValidationResult = { valid: true };

  // Handle Write/Edit on Infrastructure files
  if (input.tool_name === "Write" || input.tool_name === "Edit") {
    const filePath = input.tool_input.file_path as string;
    if (filePath && filePath.startsWith(INFRA_ROOT)) {
      result = validateInfraFileOperation(filePath);
    }
  }

  // Handle Bash commands for Terragrunt/Tofu
  if (input.tool_name === "Bash") {
    const command = input.tool_input.command as string;
    if (command) {
      result = validateBashCommand(command);
    }
  }

  // Build output
  if (result.block) {
    const output: PreToolUseHookOutput = {
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: `${result.warning}\n\n${result.context || ""}`,
      },
    };
    console.log(JSON.stringify(output));
    return;
  }

  if (result.warning || result.context) {
    const output: PreToolUseHookOutput = {
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        additionalContext: `${result.warning ? result.warning + "\n" : ""}${result.context || ""}`,
      },
    };
    console.log(JSON.stringify(output));
    return;
  }

  // All good
  console.log(JSON.stringify({}));
}

if (import.meta.main) {
  main().catch((err) => {
    console.error("Infrastructure PreToolUse hook error:", err);
    console.log(JSON.stringify({}));
  });
}
