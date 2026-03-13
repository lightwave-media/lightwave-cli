#!/usr/bin/env /Users/joelschaeffer/.bun/bin/bun
/**
 * Stop hook for lightwave-cli (Go)
 *
 * Checks: go vet, make lint, make test
 * Then: auto-commit (commits to parent monorepo)
 *
 * Note: lightwave-cli is a monorepo package (not its own git repo).
 * Commits go to the lightwave-media root repo.
 */

import { createHash } from "crypto";
import { tmpdir } from "os";
import { join } from "path";

const PROJECT_ROOT = "/Users/joelschaeffer/dev/lightwave-media/packages/lightwave-cli";
const GIT_ROOT = "/Users/joelschaeffer/dev/lightwave-media";
const RETRY_STATE_FILE = join(tmpdir(), "claude-stop-hook-lw-cli.json");
const MAX_RETRIES = 3;

interface CheckResult { name: string; passed: boolean; error?: string; duration: number }

async function readInput() {
  const chunks: Buffer[] = [];
  for await (const chunk of Bun.stdin.stream()) chunks.push(Buffer.from(chunk));
  return JSON.parse(Buffer.concat(chunks).toString("utf-8"));
}

async function runCmd(name: string, cmd: string[], cwd: string): Promise<CheckResult> {
  const start = Date.now();
  try {
    const proc = Bun.spawn(cmd, { cwd, env: process.env, stdout: "pipe", stderr: "pipe" });
    const stdout = await new Response(proc.stdout).text();
    const stderr = await new Response(proc.stderr).text();
    const exitCode = await proc.exited;
    if (exitCode !== 0) return { name, passed: false, error: stderr || stdout, duration: Date.now() - start };
    return { name, passed: true, duration: Date.now() - start };
  } catch (error) {
    return { name, passed: false, error: error instanceof Error ? error.message : String(error), duration: Date.now() - start };
  }
}

async function hasChanges(): Promise<boolean> {
  // Check for changes in the cli package directory within the monorepo
  const proc = Bun.spawn(["git", "status", "--porcelain", "packages/lightwave-cli/"], { cwd: GIT_ROOT, stdout: "pipe" });
  const out = await new Response(proc.stdout).text();
  await proc.exited;
  return out.trim().length > 0;
}

async function autoCommit(): Promise<CheckResult> {
  const start = Date.now();
  try {
    await (Bun.spawn(["git", "add", "packages/lightwave-cli/"], { cwd: GIT_ROOT, stdout: "pipe", stderr: "pipe" })).exited;
    const diff = Bun.spawn(["git", "diff", "--cached", "--stat"], { cwd: GIT_ROOT, stdout: "pipe" });
    const diffStat = await new Response(diff.stdout).text();
    await diff.exited;
    const commit = Bun.spawn(["git", "commit", "-m", `wip(lightwave-cli): auto-commit from Claude session\n\n${diffStat.trim()}`], { cwd: GIT_ROOT, stdout: "pipe", stderr: "pipe" });
    const stderr = await new Response(commit.stderr).text();
    const stdout = await new Response(commit.stdout).text();
    const exitCode = await commit.exited;
    if (exitCode !== 0) return { name: "Auto-commit", passed: false, error: stderr || stdout, duration: Date.now() - start };
    return { name: "Auto-commit", passed: true, duration: Date.now() - start };
  } catch (error) {
    return { name: "Auto-commit", passed: false, error: error instanceof Error ? error.message : String(error), duration: Date.now() - start };
  }
}

async function getRetryState() {
  try { const f = Bun.file(RETRY_STATE_FILE); if (await f.exists()) { const s = await f.json(); if (Date.now() - s.timestamp < 600000) return s; } } catch {} return null;
}
async function saveRetryState(s: { errorHash: string; attempts: number; timestamp: number }) {
  try { await Bun.write(RETRY_STATE_FILE, JSON.stringify(s)); } catch {}
}
async function clearRetryState() {
  try { const f = Bun.file(RETRY_STATE_FILE); if (await f.exists()) await Bun.write(RETRY_STATE_FILE, ""); } catch {}
}

function handleFailure(checks: CheckResult[], msg: string) {
  const hash = createHash("md5").update(msg).digest("hex").slice(0, 12);
  return { hash, msg };
}

async function main() {
  const input = await readInput();
  if (input.stop_hook_active) { console.log(JSON.stringify({})); return; }

  const checks: CheckResult[] = [];

  // Phase 1: go vet
  const vet = await runCmd("go vet", ["go", "vet", "./..."], PROJECT_ROOT);
  checks.push(vet);

  if (!vet.passed) {
    const msg = `go vet failed:\n${vet.error}`;
    const hash = createHash("md5").update(msg).digest("hex").slice(0, 12);
    const state = await getRetryState();
    const attempts = state?.errorHash === hash ? state.attempts + 1 : 1;
    if (attempts > MAX_RETRIES) { await clearRetryState(); console.log(JSON.stringify({ systemMessage: `## lightwave-cli: Failed after ${MAX_RETRIES} retries\n\n${msg}` })); return; }
    await saveRetryState({ errorHash: hash, attempts, timestamp: Date.now() });
    console.log(JSON.stringify({ decision: "block", reason: `lightwave-cli checks failed (${attempts}/${MAX_RETRIES}):\n\n${msg}` }));
    return;
  }

  // Phase 2: make fmt check (formatting) — may modify files in-place
  const fmt = await runCmd("go fmt", ["make", "fmt"], PROJECT_ROOT);
  checks.push(fmt);

  // Re-stage: go fmt auto-fixes files in-place.
  await (Bun.spawn(["git", "add", "packages/lightwave-cli/"], { cwd: GIT_ROOT, stdout: "pipe", stderr: "pipe" })).exited;

  // Phase 3: make test
  const test = await runCmd("make test", ["make", "test"], PROJECT_ROOT);
  checks.push(test);

  if (!test.passed) {
    const msg = `make test failed:\n${test.error}`;
    const hash = createHash("md5").update(msg).digest("hex").slice(0, 12);
    const state = await getRetryState();
    const attempts = state?.errorHash === hash ? state.attempts + 1 : 1;
    if (attempts > MAX_RETRIES) { await clearRetryState(); console.log(JSON.stringify({ systemMessage: `## lightwave-cli: Failed after ${MAX_RETRIES} retries\n\n${msg}` })); return; }
    await saveRetryState({ errorHash: hash, attempts, timestamp: Date.now() });
    console.log(JSON.stringify({ decision: "block", reason: `lightwave-cli checks failed (${attempts}/${MAX_RETRIES}):\n\n${msg}` }));
    return;
  }

  if (await hasChanges()) checks.push(await autoCommit());
  await clearRetryState();
  const summary = checks.map(c => `${c.passed ? "✓" : "✗"} ${c.name} (${c.duration}ms)`).join("\n");
  console.log(JSON.stringify({ systemMessage: `## lightwave-cli stop hook\n\n${summary}` }));
}

main().catch(err => { console.error("Hook error:", err); console.log(JSON.stringify({})); });
