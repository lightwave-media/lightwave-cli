#!/usr/bin/env bun
/**
 * Canonical SessionStart hook (Lightwave canonical claude-hooks).
 *
 * Three responsibilities, in order:
 *   1. Print a git-state advisory: branch, dirty count, stash count, last
 *      commit. Warn on default branch, dirty tree, or `claude-session-start:*`
 *      stashes left over from the (now-removed) auto-stash policy. Pure
 *      observation — never stashes, commits, or modifies files.
 *   2. Self-heal the `pre-push` hook: if `.git/hooks/pre-push` is missing,
 *      run `pre-commit install --hook-type pre-push`. This is the only
 *      mutation in this hook and is gated to a missing-file condition.
 *   3. Inject CLAUDE.md files from the project tree (cwd up to repo root)
 *      into the session's additionalContext.
 *
 * Pure functions are exported so they can be unit-tested without a real git
 * repo. Side-effecting functions are kept private to main().
 *
 * What this hook explicitly does NOT do:
 *   - It does NOT `git stash` dirty trees. The previous user-level hook did
 *     this with `claude-session-start:*` named stashes. That behavior is
 *     forbidden — see ~/.claude/CLAUDE.md "FORBIDDEN automatic operations".
 *   - It does NOT auto-commit, auto-add, or otherwise mutate the working tree.
 *   - It does NOT inject the user-level ~/.claude/CLAUDE.md — that is
 *     L1 (user-global) territory. This hook is L2 (org-shared).
 */

import type { SessionStartHookInput, SessionStartHookOutput } from "./types";
import { existsSync, readFileSync } from "fs";
import { dirname, join } from "path";
import { spawnSync } from "child_process";

// =============================================================================
// Pure helpers (unit-tested)
// =============================================================================

export interface GitState {
  inRepo: boolean;
  repoRoot: string | null;
  branch: string | null;
  isDefaultBranch: boolean;
  dirtyFiles: number;
  stashCount: number;
  staleSessionStashes: string[];
  lastCommit: string | null;
}

const DEFAULT_BRANCHES = new Set(["main", "master"]);

/**
 * Build a human-readable advisory string from a GitState. Pure function.
 *
 * Returns null when not in a git repo (caller should emit no advisory).
 */
export function buildAdvisory(state: GitState): string | null {
  if (!state.inRepo) return null;

  const repoName = state.repoRoot?.split("/").pop() ?? "(unknown)";
  const branch = state.branch ?? "(detached HEAD)";
  const lines: string[] = [];

  lines.push(
    `Git: ${repoName} on ${branch}. ${state.dirtyFiles} uncommitted file(s). ${state.stashCount} stash(es).`,
  );
  if (state.lastCommit) lines.push(`Last commit: ${state.lastCommit}`);

  const warnings: string[] = [];

  if (state.isDefaultBranch) {
    warnings.push(
      `On default branch '${branch}'. Create a feature branch before changing files: ` +
        `\`git checkout -b feature/<short-kebab-case>\`.`,
    );
  }

  if (state.dirtyFiles > 0) {
    warnings.push(
      `Working tree has ${state.dirtyFiles} uncommitted file(s). ` +
        `Inspect with \`git status\`. This hook does NOT auto-stash or auto-commit.`,
    );
  }

  if (state.staleSessionStashes.length > 0) {
    warnings.push(
      `Found ${state.staleSessionStashes.length} stale \`claude-session-start:*\` stash(es) ` +
        `from a previous (now-removed) auto-stash policy. ` +
        `Inspect with \`git stash list\`; recover or drop manually.`,
    );
  }

  if (warnings.length > 0) {
    lines.push("");
    lines.push("WARNINGS:");
    for (const w of warnings) lines.push(`  - ${w}`);
  }

  return lines.join("\n");
}

/**
 * Walk from `startDir` up to `gitRoot` (inclusive), collecting CLAUDE.md files
 * found along the way. Files are returned with the deepest first (closest to
 * cwd) — caller decides ordering for context injection.
 *
 * Pure modulo filesystem reads (existsSync). Uses gitRoot as a hard ceiling
 * so canonical-installed copies in arbitrary repos don't accidentally walk
 * into ~/.claude/.
 */
export function findClaudeMdFiles(startDir: string, gitRoot: string): string[] {
  const files: string[] = [];
  let dir = startDir;

  while (true) {
    const claudeMd = join(dir, "CLAUDE.md");
    if (existsSync(claudeMd)) files.push(claudeMd);
    if (dir === gitRoot) break;
    const parent = dirname(dir);
    if (parent === dir) break;
    if (!parent.startsWith(gitRoot)) break;
    dir = parent;
  }

  // Reverse so root-first, deepest-last (most specific wins in injection).
  return files.reverse();
}

// =============================================================================
// Side-effecting helpers (private to main)
// =============================================================================

function git(args: string[], cwd: string): string {
  const result = spawnSync("git", args, {
    cwd,
    encoding: "utf-8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  if (result.status !== 0) return "";
  return (result.stdout || "").trim();
}

function readGitState(cwd: string): GitState {
  const repoRoot = git(["rev-parse", "--show-toplevel"], cwd);
  if (!repoRoot) {
    return {
      inRepo: false,
      repoRoot: null,
      branch: null,
      isDefaultBranch: false,
      dirtyFiles: 0,
      stashCount: 0,
      staleSessionStashes: [],
      lastCommit: null,
    };
  }

  const branch = git(["rev-parse", "--abbrev-ref", "HEAD"], repoRoot) || null;
  const status = git(["status", "--porcelain=v1"], repoRoot);
  const dirtyFiles = status ? status.split("\n").filter(Boolean).length : 0;
  const stashList = git(["stash", "list"], repoRoot);
  const stashes = stashList ? stashList.split("\n").filter(Boolean) : [];
  const staleSessionStashes = stashes.filter((s) =>
    /claude-session-start:/.test(s),
  );
  const lastCommit = git(["log", "-1", "--oneline"], repoRoot) || null;

  return {
    inRepo: true,
    repoRoot,
    branch,
    isDefaultBranch: branch ? DEFAULT_BRANCHES.has(branch) : false,
    dirtyFiles,
    stashCount: stashes.length,
    staleSessionStashes,
    lastCommit,
  };
}

/**
 * Self-heal: ensure the pre-push hook is installed in this repo. The
 * pre-push hook is the Hallucination Gate's second layer (Django tests,
 * type-check, Docker validation) — without it, broken code reaches GitHub.
 *
 * Only runs `pre-commit install --hook-type pre-push` if the hook file is
 * missing. This is the single mutation this whole file performs, gated to
 * a "infrastructure absent" condition.
 */
function ensurePrePushHook(repoRoot: string): void {
  const prePushPath = join(repoRoot, ".git", "hooks", "pre-push");
  if (existsSync(prePushPath)) return;

  const result = spawnSync(
    "pre-commit",
    ["install", "--hook-type", "pre-push"],
    { cwd: repoRoot, encoding: "utf-8" },
  );
  if (result.status === 0) {
    process.stderr.write(
      "[session-start] pre-push hook was missing — installed.\n",
    );
  } else {
    process.stderr.write(
      `[session-start] WARNING: failed to install pre-push hook: ${result.stderr}\n`,
    );
  }
}

function readClaudeMd(filePath: string): string | null {
  try {
    const content = readFileSync(filePath, "utf-8").trim();
    if (!content) return null;
    const dirName = dirname(filePath).split("/").pop() || "root";
    return `<!-- From ${dirName}/CLAUDE.md -->\n${content}`;
  } catch {
    return null;
  }
}

// =============================================================================
// Main
// =============================================================================

async function readInput(): Promise<SessionStartHookInput> {
  const chunks: Buffer[] = [];
  for await (const chunk of Bun.stdin.stream()) {
    chunks.push(Buffer.from(chunk));
  }
  const raw = Buffer.concat(chunks).toString("utf-8").trim();
  if (!raw) {
    return {
      session_id: "",
      transcript_path: "",
      cwd: process.cwd(),
      permission_mode: "default",
      hook_event_name: "SessionStart",
      source: "startup",
    };
  }
  return JSON.parse(raw);
}

async function main() {
  const input = await readInput();

  const state = readGitState(input.cwd);
  const advisory = buildAdvisory(state);

  // Only run self-heal and CLAUDE.md injection on fresh startup. On resume
  // or compact, just print the advisory if there is one.
  const isFreshStartup = input.source === "startup";

  const contextParts: string[] = [];
  if (advisory) contextParts.push(advisory);

  if (isFreshStartup && state.inRepo && state.repoRoot) {
    ensurePrePushHook(state.repoRoot);

    const claudeMdFiles = findClaudeMdFiles(input.cwd, state.repoRoot);
    for (const file of claudeMdFiles) {
      const content = readClaudeMd(file);
      if (content) contextParts.push(content);
    }
  }

  if (contextParts.length === 0) {
    console.log(JSON.stringify({}));
    return;
  }

  const additionalContext = contextParts.join("\n\n---\n\n");

  const output: SessionStartHookOutput = {
    hookSpecificOutput: {
      hookEventName: "SessionStart",
      additionalContext,
    },
  };
  console.log(JSON.stringify(output));
}

if (import.meta.main) {
  main().catch((err) => {
    process.stderr.write(`session-start hook error: ${err}\n`);
    console.log(JSON.stringify({}));
  });
}
