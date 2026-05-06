#!/usr/bin/env bun
/**
 * Canonical Stop hook (Lightwave canonical claude-hooks).
 *
 * Observation-only enforcement. The hook NEVER:
 *   - runs `git add -A` or stages files
 *   - runs `git commit` (auto or otherwise)
 *   - runs `git stash`
 *   - uses `--no-verify`
 *   - runs `--all-files` quality gates (per-turn cost too high)
 *   - retries quality gates after auto-fix (single-pass; non-idempotent
 *     formatters surface their bugs instead of being papered over)
 *
 * The hook DOES:
 *   - report git state (branch, dirty count, staged count, last commit)
 *   - extract files edited this turn from the JSONL transcript
 *   - run formatter/linter in --check (read-only) mode on JUST those files
 *   - emit results via systemMessage (Stop's visible output channel per
 *     types.ts; Stop has no hookSpecificOutput)
 *
 * Replaces all 8 distinct stop.ts implementations across the org. Per the
 * Stage 0 follow-up audit, those implementations contain 25 active
 * FORBIDDEN-op call sites in 8 files across 7 repos. This rewrite removes
 * every one.
 */

import type {
  StopHookInput,
  SubagentStopHookInput,
  StopHookOutput,
} from "./types";
import { spawnSync } from "child_process";
import { existsSync, readFileSync } from "fs";
import { extname, isAbsolute, relative } from "path";

// =============================================================================
// Pure helpers (unit-tested)
// =============================================================================

export type Category = "py" | "ts" | "go" | "tf" | null;

/**
 * Map a file path to a quality-gate category. Returns null when no gate
 * applies (yaml, json, md, png, etc.) — caller should skip.
 */
export function categorize(file: string): Category {
  const ext = extname(file).toLowerCase();
  if (ext === ".py") return "py";
  if (ext === ".ts" || ext === ".tsx" || ext === ".js" || ext === ".jsx")
    return "ts";
  if (ext === ".go") return "go";
  if (ext === ".tf" || ext === ".hcl") return "tf";
  return null;
}

/**
 * Walk a Claude Code JSONL transcript and return absolute paths of files
 * edited this turn. Walks backwards from the end and stops at the most
 * recent user message — that's the boundary of the current turn. Earlier
 * turns are deliberately ignored.
 *
 * Pure modulo `readFileSync`. Returns empty array on any read/parse error.
 */
export function extractEditedFiles(transcriptPath: string): string[] {
  if (!transcriptPath || !existsSync(transcriptPath)) return [];

  let raw: string;
  try {
    raw = readFileSync(transcriptPath, "utf-8");
  } catch {
    return [];
  }

  const lines = raw.trim().split("\n");
  const edited = new Set<string>();

  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i];
    if (!line) continue;
    let entry: unknown;
    try {
      entry = JSON.parse(line);
    } catch {
      continue;
    }

    const message = (
      entry as { message?: { role?: string; content?: unknown } }
    )?.message;
    if (!message) continue;

    if (message.role === "user") break;

    const blocks = message.content;
    if (!Array.isArray(blocks)) continue;

    for (const block of blocks) {
      if (block?.type !== "tool_use") continue;
      const name = block.name;
      if (name !== "Write" && name !== "Edit") continue;
      const fp = block?.input?.file_path;
      if (typeof fp === "string" && fp.length > 0) edited.add(fp);
    }
  }

  return [...edited];
}

export interface CheckResult {
  name: string;
  file: string;
  passed: boolean;
  duration: number;
  output?: string;
}

export interface GitStateSummary {
  branch: string | null;
  dirtyFiles: number;
  stagedFiles: number;
  lastCommit: string | null;
}

/**
 * Build the systemMessage report. Pure function over inputs.
 */
export function buildReport(args: {
  isSubagent: boolean;
  repoName: string;
  state: GitStateSummary;
  editedFiles: string[];
  results: CheckResult[];
}): string {
  const lines: string[] = [];
  const label = args.isSubagent ? "SubagentStop" : "Stop";
  const branch = args.state.branch ?? "(detached)";

  lines.push(`## ${label} — ${args.repoName} on ${branch}`);
  lines.push(
    `${args.state.dirtyFiles} unstaged, ${args.state.stagedFiles} staged. ` +
      `${args.editedFiles.length} file(s) edited this turn.`,
  );
  if (args.state.lastCommit) {
    lines.push(`Last commit: ${args.state.lastCommit}`);
  }

  if (args.editedFiles.length === 0) {
    return lines.join("\n");
  }

  if (args.results.length === 0) {
    lines.push("");
    lines.push("(no quality gates apply to the edited file types)");
    return lines.join("\n");
  }

  const failures = args.results.filter((r) => !r.passed);
  const passed = args.results.length - failures.length;

  if (failures.length === 0) {
    lines.push("");
    lines.push(`Quality gates: ${passed}/${args.results.length} passed.`);
    return lines.join("\n");
  }

  lines.push("");
  lines.push(
    `Quality gate failures (${failures.length}/${args.results.length}):`,
  );
  for (const f of failures) {
    lines.push(`  ✗ ${f.name} on ${f.file} (${f.duration}ms)`);
    if (f.output) {
      const truncated = f.output
        .split("\n")
        .slice(0, 5)
        .map((l) => `      ${l}`)
        .join("\n");
      lines.push(truncated);
    }
  }
  lines.push("");
  lines.push(
    "These are advisories — this hook does NOT auto-fix, auto-stage, or auto-commit. Run the listed tool to fix, then commit explicitly.",
  );

  return lines.join("\n");
}

// =============================================================================
// Side-effecting helpers (private to main)
// =============================================================================

function git(args: string[], cwd: string): string {
  const r = spawnSync("git", args, {
    cwd,
    encoding: "utf-8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  if (r.status !== 0) return "";
  return (r.stdout || "").trim();
}

function getRepoRoot(cwd: string): string | null {
  const root = git(["rev-parse", "--show-toplevel"], cwd);
  return root || null;
}

function readGitState(repoRoot: string): GitStateSummary {
  const branch = git(["rev-parse", "--abbrev-ref", "HEAD"], repoRoot) || null;
  const status = git(["status", "--porcelain=v1"], repoRoot);
  const lines = status ? status.split("\n").filter(Boolean) : [];
  const stagedFiles = lines.filter((l) => l[0] !== " " && l[0] !== "?").length;
  const dirtyFiles = lines.length;
  const lastCommit = git(["log", "-1", "--oneline"], repoRoot) || null;
  return { branch, dirtyFiles, stagedFiles, lastCommit };
}

interface QualityCheck {
  name: string;
  cmd: string[];
}

function buildChecksFor(file: string, repoRoot: string): QualityCheck[] {
  const cat = categorize(file);
  if (!cat) return [];
  const rel = isAbsolute(file) ? relative(repoRoot, file) : file;

  switch (cat) {
    case "py":
      return [
        { name: "ruff check", cmd: ["ruff", "check", rel] },
        {
          name: "ruff format --check",
          cmd: ["ruff", "format", "--check", rel],
        },
      ];
    case "ts":
      return [
        { name: "prettier --check", cmd: ["bunx", "prettier", "--check", rel] },
      ];
    case "go":
      return [{ name: "gofmt -l", cmd: ["gofmt", "-l", rel] }];
    case "tf":
      return [
        {
          name: "terraform fmt -check",
          cmd: ["terraform", "fmt", "-check", rel],
        },
      ];
  }
}

function runCheck(check: QualityCheck, file: string, cwd: string): CheckResult {
  const start = Date.now();
  const r = spawnSync(check.cmd[0], check.cmd.slice(1), {
    cwd,
    encoding: "utf-8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  const duration = Date.now() - start;

  // gofmt special: exit 0 with non-empty stdout means "would change" = fail
  let passed: boolean;
  if (check.name === "gofmt -l") {
    passed = r.status === 0 && (r.stdout || "").trim().length === 0;
  } else {
    passed = r.status === 0;
  }

  const output = passed
    ? undefined
    : ((r.stderr || "") + (r.stdout || "")).slice(0, 1000);
  return { name: check.name, file, passed, duration, output };
}

// =============================================================================
// Main
// =============================================================================

async function readInput(): Promise<StopHookInput | SubagentStopHookInput> {
  const chunks: Buffer[] = [];
  for await (const chunk of Bun.stdin.stream()) chunks.push(Buffer.from(chunk));
  const raw = Buffer.concat(chunks).toString("utf-8").trim();
  if (!raw) throw new Error("empty stdin");
  return JSON.parse(raw);
}

async function main() {
  const input = await readInput();

  // Prevent stop-hook loops
  if (input.stop_hook_active) {
    console.log(JSON.stringify({}));
    return;
  }

  const isSubagent = input.hook_event_name === "SubagentStop";
  const repoRoot = getRepoRoot(input.cwd);
  if (!repoRoot) {
    console.log(JSON.stringify({}));
    return;
  }

  const state = readGitState(repoRoot);
  const repoName = repoRoot.split("/").pop() ?? "(unknown)";

  // Sub-agents: observation only. They're short-lived; the parent Stop event
  // aggregates. Quality gates do not run for SubagentStop.
  const editedFiles = isSubagent
    ? []
    : extractEditedFiles(input.transcript_path);

  const results: CheckResult[] = [];
  if (!isSubagent) {
    for (const file of editedFiles) {
      for (const check of buildChecksFor(file, repoRoot)) {
        results.push(runCheck(check, file, repoRoot));
      }
    }
  }

  const report = buildReport({
    isSubagent,
    repoName,
    state,
    editedFiles,
    results,
  });

  const output: StopHookOutput = { systemMessage: report };
  console.log(JSON.stringify(output));
}

if (import.meta.main) {
  main().catch((err) => {
    process.stderr.write(`stop hook error: ${err}\n`);
    console.log(JSON.stringify({}));
  });
}
