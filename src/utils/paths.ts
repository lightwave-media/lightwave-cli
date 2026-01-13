import { fileURLToPath } from "url";
import { dirname, join, resolve } from "path";
import { existsSync } from "fs";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

/**
 * Find the workspace root by looking for the .claude directory
 */
export function findWorkspaceRoot(): string {
  let current = process.cwd();

  while (current !== "/") {
    if (
      existsSync(join(current, ".claude")) &&
      existsSync(join(current, "packages"))
    ) {
      return current;
    }
    current = dirname(current);
  }

  throw new Error(
    "Could not find LightWave workspace root (looking for .claude/ directory)",
  );
}

/**
 * Get path to a package in the workspace
 */
export function getPackagePath(packageName: string): string {
  const root = findWorkspaceRoot();
  return join(root, "packages", packageName);
}

/**
 * Get path to a domain project
 */
export function getDomainPath(domainName: string): string {
  const root = findWorkspaceRoot();
  return join(root, "domains", domainName);
}

/**
 * Get templates directory
 */
export function getTemplatesDir(): string {
  return resolve(__dirname, "..", "templates");
}
