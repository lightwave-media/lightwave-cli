#!/usr/bin/env /Users/joelschaeffer/.bun/bin/bun
/**
 * Stop hook for lightwave-cli (Go)
 *
 * Runs pre-commit with auto-retry (up to 3x), then auto-commits.
 * Uses .pre-commit-config.yaml for Go checks: go fmt, go vet, go build.
 */

import { createHash } from "crypto";
import { tmpdir } from "os";
import { join } from "path";

const REPO_ROOT = "/Users/joelschaeffer/dev/lightwave-media/packages/lightwave-cli";
const RETRY_STATE_FILE = join(tmpdir(), "claude-stop-hook-lw-cli.json");
const MAX_RETRIES = 3;

interface StopHookInput {
  session_id: string;
  stop_hook_active: boolean;
  hook_event_name?: string;
}

async function readInput(): Promise<StopHookInput> {
  const chunks: Buffer[] = [];
  for await (const chunk of Bun.stdin.stream()) chunks.push(Buffer.from(chunk));
  const raw = Buffer.concat(chunks).toString("utf-8").trim();
  if (!raw) return { session_id: "", stop_hook_active: false };
  try { return JSON.parse(raw); } catch { return { session_id: "", stop_hook_active: false }; }
}

async function runPreCommit(): Promise<{ passed: boolean; output: string; duration: number }> {
  const start = Date.now();
  try {
    const proc = Bun.spawn(["pre-commit", "run", "--all-files"], {
      cwd: REPO_ROOT,
      env: process.env,
      stdout: "pipe",
      stderr: "pipe",
    });
    const [stdout, stderr] = await Promise.all([
      new Response(proc.stdout).text(),
      new Response(proc.stderr).text(),
    ]);
    const exitCode = await proc.exited;
    return {
      passed: exitCode === 0,
      output: exitCode !== 0 ? (stderr || stdout).slice(0, 4000) : stdout.slice(0, 2000),
      duration: Date.now() - start,
    };
  } catch (error) {
    return {
      passed: false,
      output: error instanceof Error ? error.message : String(error),
      duration: Date.now() - start,
    };
  }
}

async function hasUncommittedChanges(): Promise<boolean> {
  const proc = Bun.spawn(["git", "status", "--porcelain"], { cwd: REPO_ROOT, stdout: "pipe" });
  const output = await new Response(proc.stdout).text();
  await proc.exited;
  return output.trim().length > 0;
}

async function stageAll(): Promise<void> {
  const proc = Bun.spawn(["git", "add", "-A"], { cwd: REPO_ROOT, stdout: "pipe", stderr: "pipe" });
  await proc.exited;
}

async function hasStagedChanges(): Promise<boolean> {
  const p = Bun.spawn(["git", "diff", "--cached", "--quiet"], { cwd: REPO_ROOT });
  const code = await p.exited;
  return code !== 0;
}

async function autoCommit(): Promise<{ passed: boolean; output: string }> {
  try {
    await stageAll();
    if (!(await hasStagedChanges())) return { passed: true, output: "No changes to commit" };

    const proc = Bun.spawn(
      ["git", "commit", "--no-verify", "-m", "wip: session changes\n\nCo-Authored-By: Claude Code <noreply@anthropic.com>"],
      { cwd: REPO_ROOT, env: process.env, stdout: "pipe", stderr: "pipe" }
    );
    const [stdout, stderr] = await Promise.all([
      new Response(proc.stdout).text(),
      new Response(proc.stderr).text(),
    ]);
    const exitCode = await proc.exited;
    return { passed: exitCode === 0, output: exitCode === 0 ? stdout : stderr || stdout };
  } catch (error) {
    return { passed: false, output: error instanceof Error ? error.message : String(error) };
  }
}

async function getRetryState(): Promise<{ errorHash: string; attempts: number; timestamp: number } | null> {
  try {
    const f = Bun.file(RETRY_STATE_FILE);
    if (await f.exists()) {
      const s = await f.json();
      if (Date.now() - s.timestamp < 600000) return s;
    }
  } catch {}
  return null;
}

async function saveRetryState(s: { errorHash: string; attempts: number; timestamp: number }) {
  try { await Bun.write(RETRY_STATE_FILE, JSON.stringify(s)); } catch {}
}

async function clearRetryState() {
  try {
    const f = Bun.file(RETRY_STATE_FILE);
    if (await f.exists()) await Bun.write(RETRY_STATE_FILE, "{}");
  } catch {}
}

async function main() {
  const input = await readInput();
  if (input.stop_hook_active) { console.log(JSON.stringify({})); return; }

  // Stage all changes before running pre-commit
  await stageAll();

  if (!(await hasStagedChanges()) && !(await hasUncommittedChanges())) {
    console.log(JSON.stringify({}));
    return;
  }

  const result = await runPreCommit();

  if (!result.passed) {
    // Formatters (gofmt) auto-fix in-place — re-stage and re-run
    await stageAll();
    const retryResult = await runPreCommit();

    if (retryResult.passed) {
      Object.assign(result, retryResult);
    } else {
      // Still failing — block Claude with retry tracking
      await stageAll();

      const currentHash = createHash("md5").update(retryResult.output).digest("hex").slice(0, 12);
      const state = await getRetryState();
      const attempts = state?.errorHash === currentHash ? state.attempts + 1 : 1;

      if (attempts > MAX_RETRIES) {
        await clearRetryState();
        console.log(JSON.stringify({
          systemMessage: `## lightwave-cli: Pre-commit failed after ${MAX_RETRIES} retries\n\n\`\`\`\n${retryResult.output}\n\`\`\`\n\nManual intervention required.`,
        }));
        return;
      }

      await saveRetryState({ errorHash: currentHash, attempts, timestamp: Date.now() });
      console.log(JSON.stringify({
        decision: "block",
        reason: `lightwave-cli pre-commit failed (${attempts}/${MAX_RETRIES}):\n\n${retryResult.output}\n\nFix the errors and try again.`,
      }));
      return;
    }
  }

  // Checks passed — commit
  const lines: string[] = [`pre-commit passed (${result.duration}ms)`];

  const commitResult = await autoCommit();
  if (commitResult.output !== "No changes to commit") {
    lines.push(commitResult.passed ? "committed" : `commit failed: ${commitResult.output}`);
  }

  await clearRetryState();
  console.log(JSON.stringify({
    systemMessage: `## lightwave-cli stop hook\n\n${lines.join("\n")}`,
  }));
}

main().catch(err => {
  console.error("Hook error:", err);
  console.log(JSON.stringify({}));
});
