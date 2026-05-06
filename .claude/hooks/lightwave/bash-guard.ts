#!/usr/bin/env bun
/**
 * Claude Code PreToolUse Hook — Bash Guard
 *
 * Environment-aware CLI enforcement that intercepts raw shell commands
 * and tells Claude to use `lw` equivalents instead.
 *
 * Graduated enforcement:
 *   local      → deny raw commands with lw equivalents
 *   staging    → + deny destructive DB, raw terragrunt/ecs
 *   production → + deny ALL raw infra commands
 *
 * Rules and data live in bash-guard-rules.ts.
 */

import type { PreToolUseHookInput, PreToolUseHookOutput } from "./types";
import {
  type EnvLevel,
  ALL_RULES,
  envSeverity,
  detectCommandEnv,
  isAllowlisted,
} from "./bash-guard-rules";

// =============================================================================
// Quote / Heredoc Stripping
// =============================================================================

/**
 * Replace the bodies of quoted strings and heredocs with empty placeholders so
 * rule regexes don't match against argument *contents*. We still want to match
 * real invocations like `aws ecs deploy svc`, but not strings like
 * `paperclipai issue comment X --body "aws ecs deploy"` or python heredocs that
 * happen to contain those tokens.
 *
 * Order matters: heredocs first (they may contain quotes), then double quotes
 * (with backslash-escape awareness), then single quotes, then backticks.
 */
export function stripStringsAndHeredocs(cmd: string): string {
  let s = cmd;
  // Heredocs: <<TAG / <<'TAG' / <<"TAG" / <<-TAG ... \nTAG\n
  s = s.replace(
    /<<-?\s*(['"]?)([A-Za-z_][A-Za-z0-9_]*)\1[\s\S]*?\n[ \t]*\2[ \t]*(?=\n|$)/g,
    "<<HEREDOC",
  );
  // Double-quoted (handle backslash escapes)
  s = s.replace(/"(?:\\.|[^"\\])*"/g, '""');
  // Single-quoted (no escape semantics inside)
  s = s.replace(/'[^']*'/g, "''");
  // Backtick command substitution
  s = s.replace(/`[^`]*`/g, "``");
  return s;
}

// =============================================================================
// Command Splitting
// =============================================================================

/**
 * Split compound commands on &&, ||, ; while respecting quotes.
 */
export function splitCompoundCommand(command: string): string[] {
  const segments: string[] = [];
  let current = "";
  let inSingle = false;
  let inDouble = false;
  let i = 0;

  while (i < command.length) {
    const ch = command[i];

    if (ch === "'" && !inDouble) {
      inSingle = !inSingle;
      current += ch;
      i++;
      continue;
    }
    if (ch === '"' && !inSingle) {
      inDouble = !inDouble;
      current += ch;
      i++;
      continue;
    }

    if (!inSingle && !inDouble) {
      if (
        (ch === "&" && command[i + 1] === "&") ||
        (ch === "|" && command[i + 1] === "|")
      ) {
        segments.push(current.trim());
        current = "";
        i += 2;
        continue;
      }
      if (ch === ";") {
        segments.push(current.trim());
        current = "";
        i++;
        continue;
      }
    }

    current += ch;
    i++;
  }

  const last = current.trim();
  if (last) segments.push(last);

  return segments.filter(Boolean);
}

// =============================================================================
// Deny Message Formatting
// =============================================================================

function formatDeny(
  env: EnvLevel,
  rawCommand: string,
  suggestion: string,
): string {
  if (env === "production") {
    return [
      `BLOCKED [PRODUCTION]: Raw infrastructure command against production.`,
      ``,
      `  Command:  ${rawCommand}`,
      `  Use:      ${suggestion}`,
      ``,
      `Production operations MUST go through the lw CLI for audit trail and safety.`,
    ].join("\n");
  }

  const label = env === "staging" ? "STAGING" : "local";
  return [
    `BLOCKED [${label}]: Raw command has lw CLI equivalent.`,
    ``,
    `  Command:  ${rawCommand}`,
    `  Use:      ${suggestion}`,
    ``,
    `Run 'lw --help' for all available commands.`,
  ].join("\n");
}

function formatWarn(rawCommand: string, suggestion: string): string {
  return [
    `[CLI Hint] This command may have an lw equivalent.`,
    ``,
    `  Command:  ${rawCommand}`,
    `  Try:      ${suggestion}`,
    ``,
    `Run 'lw --help' for all available commands.`,
  ].join("\n");
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

  if (input.tool_name !== "Bash") {
    console.log(JSON.stringify({}));
    return;
  }

  const command = input.tool_input?.command as string | undefined;
  if (!command) {
    console.log(JSON.stringify({}));
    return;
  }

  const lwEnv = (process.env.LW_ENV || "local") as EnvLevel;
  const baseEnv: EnvLevel = ["local", "staging", "production"].includes(lwEnv)
    ? lwEnv
    : "local";

  const segments = splitCompoundCommand(command);
  const denials: string[] = [];
  const warnings: string[] = [];

  for (const segment of segments) {
    if (isAllowlisted(segment)) continue;

    // Strip string/heredoc *bodies* before pattern matching so we don't match
    // against argument contents (e.g. `paperclipai issue comment X --body "aws
    // ecs deploy"` was falsely flagged as a raw aws ecs invocation). Same for
    // env detection — a domain name inside a quoted comment body shouldn't
    // upgrade the segment to production.
    const matchTarget = stripStringsAndHeredocs(segment);

    const detectedEnv = detectCommandEnv(matchTarget);
    const effectiveEnv: EnvLevel =
      detectedEnv && envSeverity(detectedEnv) > envSeverity(baseEnv)
        ? detectedEnv
        : baseEnv;

    for (const rule of ALL_RULES) {
      if (!rule.match(matchTarget)) continue;

      if (rule.minEnv && envSeverity(effectiveEnv) < envSeverity(rule.minEnv)) {
        continue;
      }

      const suggestion =
        typeof rule.suggestion === "function"
          ? rule.suggestion(segment)
          : rule.suggestion;

      if (rule.warnOnly) {
        warnings.push(formatWarn(segment, suggestion));
      } else {
        denials.push(formatDeny(effectiveEnv, segment, suggestion));
      }

      break;
    }
  }

  if (denials.length > 0) {
    const output: PreToolUseHookOutput = {
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: denials.join("\n\n"),
      },
    };
    console.log(JSON.stringify(output));
    return;
  }

  if (warnings.length > 0) {
    const output: PreToolUseHookOutput = {
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        additionalContext: warnings.join("\n\n"),
      },
    };
    console.log(JSON.stringify(output));
    return;
  }

  console.log(JSON.stringify({}));
}

// Only run when invoked directly (not when imported by tests).
if (import.meta.main) {
  main().catch((err) => {
    console.error("bash-guard hook error:", err);
    // Deny on error — fail closed, not open
    const output: PreToolUseHookOutput = {
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: `Bash guard hook error — denying by default. Error: ${err?.message || String(err)}`,
      },
    };
    console.log(JSON.stringify(output));
  });
}
