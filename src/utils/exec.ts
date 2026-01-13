import { spawn } from "child_process";
import chalk from "chalk";

export interface ExecOptions {
  cwd?: string;
  silent?: boolean;
}

/**
 * Execute a shell command and return the result
 */
export function exec(
  command: string,
  args: string[] = [],
  options: ExecOptions = {},
): Promise<{ stdout: string; stderr: string; code: number }> {
  return new Promise((resolve, reject) => {
    const proc = spawn(command, args, {
      cwd: options.cwd || process.cwd(),
      shell: true,
      stdio: options.silent ? "pipe" : "inherit",
    });

    let stdout = "";
    let stderr = "";

    if (options.silent) {
      proc.stdout?.on("data", (data) => (stdout += data.toString()));
      proc.stderr?.on("data", (data) => (stderr += data.toString()));
    }

    proc.on("close", (code) => {
      resolve({ stdout, stderr, code: code || 0 });
    });

    proc.on("error", (err) => {
      reject(err);
    });
  });
}

/**
 * Execute a make command in a directory
 */
export async function make(
  target: string,
  cwd: string,
  silent = false,
): Promise<void> {
  if (!silent) {
    console.log(chalk.blue(`→ make ${target}`));
  }
  const result = await exec("make", [target], { cwd, silent });
  if (result.code !== 0) {
    throw new Error(`make ${target} failed with code ${result.code}`);
  }
}
