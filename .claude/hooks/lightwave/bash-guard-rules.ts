/**
 * Bash Guard — Rule definitions and allowlist
 *
 * Exported to bash-guard.ts. All block/warn rules live here.
 */

// =============================================================================
// Types
// =============================================================================

export type EnvLevel = "local" | "staging" | "production";

export interface BlockRule {
  name: string;
  match: (segment: string) => boolean;
  suggestion: string | ((segment: string) => string);
  warnOnly?: boolean;
  minEnv?: EnvLevel; // Only active at this env level or higher; undefined = always
}

// =============================================================================
// Environment Detection
// =============================================================================

export function envSeverity(env: EnvLevel): number {
  return { local: 0, staging: 1, production: 2 }[env];
}

export function detectCommandEnv(segment: string): EnvLevel | null {
  if (/--env\s+prod/.test(segment)) return "production";
  if (/api\.lightwave-media\.ltd/.test(segment)) return "production";
  if (/lightwave-media\.site/.test(segment)) return "production";
  if (/--cluster\s+prod/.test(segment)) return "production";
  if (/prod-platform/.test(segment)) return "production";
  if (/staging\.lightwave-media/.test(segment)) return "staging";
  if (/--env\s+(non-prod|staging)/.test(segment)) return "staging";
  return null;
}

// =============================================================================
// Allowlist — never blocked regardless of environment
// =============================================================================

const ALLOWLIST_PREFIXES = [
  "git ",
  "lw ",
  "lw\n",
  "ls",
  "pwd",
  "cat ",
  "head ",
  "tail ",
  "mkdir ",
  "curl ",
  "wget ",
  "npm ",
  "pnpm ",
  "bun ",
  "brew ",
  "uv ",
  "pip ",
  "go ",
  "cargo ",
  "grep ",
  "rg ",
  "jq ",
  "gh ",
  "open ",
  "pbcopy",
  "pbpaste",
  "chmod ",
  "env ",
  "source ",
  "export ",
  "echo ",
  "printf ",
  "wc ",
  "sort ",
  "uniq ",
  "diff ",
  "touch ",
  "cp ",
  "mv ",
  "rm ",
  "find ",
  "which ",
  "type ",
  "readlink ",
  "realpath ",
  "basename ",
  "dirname ",
  "xargs ",
  "tr ",
  "cut ",
  "sed ",
  "awk ",
  "tee ",
  "pre-commit install",
  "docker build",
  "docker push",
  "docker pull",
  "docker ps",
  "docker images",
  "docker inspect",
  "docker tag",
];

export function isAllowlisted(segment: string): boolean {
  const trimmed = segment.trim();
  if (
    trimmed === "ls" ||
    trimmed === "pwd" ||
    trimmed === "env" ||
    trimmed === "lw"
  ) {
    return true;
  }
  if (trimmed === "cd" || trimmed.startsWith("cd ")) return true;
  return ALLOWLIST_PREFIXES.some((prefix) => trimmed.startsWith(prefix));
}

// =============================================================================
// Block Rules — Local (always active)
// =============================================================================

function extractManageCmd(s: string): string {
  const m = s.match(/manage\.py\s+(\S+)/);
  return m
    ? `lw make platform dj-manage CMD="${m[1]}"`
    : 'lw make platform dj-manage CMD="<cmd>"';
}

const LOCAL_BLOCK_RULES: BlockRule[] = [
  {
    name: "docker-compose-up",
    match: (s) => /docker\s+compose\s+up/.test(s),
    suggestion: "lw dev start",
  },
  {
    name: "docker-compose-down",
    match: (s) => /docker\s+compose\s+down/.test(s),
    suggestion: "lw dev stop",
  },
  {
    name: "docker-compose-logs",
    match: (s) => /docker\s+compose\s+logs/.test(s),
    suggestion: "lw dev logs",
  },
  {
    name: "docker-compose-exec-bash",
    match: (s) => /docker\s+compose\s+exec\s+\S+\s+\/?(?:ba)?sh/.test(s),
    suggestion: "lw dev ssh",
  },
  {
    name: "docker-compose-exec-shell-plus",
    match: (s) => /docker\s+compose\s+exec\s+.*shell_plus/.test(s),
    suggestion: "lw dev shell",
  },
  {
    name: "docker-compose-exec-migrate",
    match: (s) => /docker\s+compose\s+exec\s+.*manage\.py\s+migrate/.test(s),
    suggestion: "lw db migrate",
  },
  {
    name: "docker-compose-exec-test",
    match: (s) => /docker\s+compose\s+exec\s+.*manage\.py\s+test/.test(s),
    suggestion: "lw test",
  },
  {
    name: "docker-compose-exec-manage",
    match: (s) => /docker\s+compose\s+exec\s+.*manage\.py\s+(\S+)/.test(s),
    suggestion: extractManageCmd,
  },
  {
    name: "manage-migrate",
    match: (s) => /python\s+manage\.py\s+migrate/.test(s),
    suggestion: "lw db migrate",
  },
  {
    name: "manage-makemigrations",
    match: (s) => /python\s+manage\.py\s+makemigrations/.test(s),
    suggestion: "lw db fresh",
  },
  {
    name: "manage-test",
    match: (s) => /python\s+manage\.py\s+test/.test(s),
    suggestion: "lw test",
  },
  {
    name: "manage-shell",
    match: (s) => /python\s+manage\.py\s+shell/.test(s),
    suggestion: "lw dev shell",
  },
  {
    name: "manage-send-email",
    match: (s) => /python\s+manage\.py\s+send_email/.test(s),
    suggestion: "lw email send",
  },
  {
    name: "manage-generic",
    match: (s) => /python\s+manage\.py\s+(\S+)/.test(s),
    suggestion: extractManageCmd,
  },
  {
    name: "pre-commit-run",
    match: (s) => /pre-commit\s+run/.test(s),
    suggestion: "lw check",
  },
  {
    name: "ruff",
    match: (s) => /ruff\s+(check|format)/.test(s),
    suggestion: "lw check ruff",
  },
  {
    name: "tsc-noemit",
    match: (s) => /tsc\s+--noEmit/.test(s),
    suggestion: "lw check types",
  },
  {
    name: "pytest",
    match: (s) => /^pytest\b/.test(s.trim()),
    suggestion: "lw test",
  },
  {
    name: "terragrunt",
    match: (s) => /terragrunt\s+\S+/.test(s),
    suggestion: (s) => {
      const m = s.match(/terragrunt\s+(\S+)/);
      return m ? `lw infra ${m[1]} <path>` : "lw infra <cmd> <path>";
    },
  },
  {
    name: "aws-ecs",
    match: (s) => /aws\s+ecs\s+/.test(s),
    suggestion: "lw aws ecs status|deploy|tasks",
  },
  {
    name: "aws-logs",
    match: (s) => /aws\s+logs\s+/.test(s),
    suggestion: "lw aws logs tail|show|list",
  },
  {
    name: "aws-s3-sync",
    match: (s) => /aws\s+s3\s+sync/.test(s),
    suggestion: "lw cdn push|pull",
  },
];

// =============================================================================
// Warn-only rules — Local
// =============================================================================

const LOCAL_WARN_RULES: BlockRule[] = [
  {
    name: "docker-compose-other",
    match: (s) =>
      /docker\s+compose\s+/.test(s) &&
      !LOCAL_BLOCK_RULES.some(
        (r) => r.name.startsWith("docker-compose") && r.match(s),
      ),
    suggestion: "Check 'lw dev --help' for available commands",
    warnOnly: true,
  },
  {
    name: "make-target",
    match: (s) => /^make\s+/.test(s.trim()),
    suggestion: (s) => {
      const m = s.trim().match(/^make\s+(\S+)/);
      return m ? `lw make <scope> ${m[1]}` : "lw make <scope> <target>";
    },
    warnOnly: true,
  },
];

// =============================================================================
// Block Rules — Staging (minEnv: "staging")
// =============================================================================

const STAGING_BLOCK_RULES: BlockRule[] = [
  {
    name: "destructive-sql-drop",
    match: (s) => /DROP\s+(SCHEMA|TABLE|DATABASE)/i.test(s),
    suggestion:
      "Destructive DB operations blocked on staging. Use lw db commands.",
    minEnv: "staging",
  },
  {
    name: "destructive-sql-truncate",
    match: (s) => /TRUNCATE/i.test(s),
    suggestion:
      "Destructive DB operations blocked on staging. Use lw db commands.",
    minEnv: "staging",
  },
  {
    name: "raw-psql-staging",
    match: (s) => /psql\s+/.test(s),
    suggestion: "Use 'lw db' commands for database access",
    minEnv: "staging",
  },
  {
    name: "terragrunt-apply-staging",
    match: (s) => /terragrunt\s+apply/.test(s),
    suggestion: "lw infra apply <path> — required for audit trail",
    minEnv: "staging",
  },
  {
    name: "aws-ecs-update-staging",
    match: (s) => /aws\s+ecs\s+update-service/.test(s),
    suggestion: "lw aws ecs deploy <service>",
    minEnv: "staging",
  },
];

// =============================================================================
// Block Rules — Production (minEnv: "production")
// =============================================================================

const PRODUCTION_BLOCK_RULES: BlockRule[] = [
  {
    name: "all-raw-aws-prod",
    match: (s) => /aws\s+\S+/.test(s),
    suggestion: "Use 'lw aws' for production operations",
    minEnv: "production",
  },
  {
    name: "all-raw-terragrunt-prod",
    match: (s) => /terragrunt\s+/.test(s),
    suggestion: "lw infra <cmd> --env prod",
    minEnv: "production",
  },
  {
    name: "all-raw-psql-prod",
    match: (s) => /psql\s+/.test(s),
    suggestion: "Never run raw SQL against production. Use lw db commands.",
    minEnv: "production",
  },
  {
    name: "all-raw-manage-prod",
    match: (s) => /manage\.py/.test(s),
    suggestion: "Never run raw manage.py against production. Use lw commands.",
    minEnv: "production",
  },
  {
    name: "docker-compose-prod",
    match: (s) => /docker\s+compose/.test(s),
    suggestion: "No direct Docker Compose on production",
    minEnv: "production",
  },
  {
    name: "ssh-prod",
    match: (s) => /ssh\s+/.test(s),
    suggestion: "lw aws ecs tasks — use ECS exec for container access",
    minEnv: "production",
  },
];

// =============================================================================
// Combined export
// =============================================================================

export const ALL_RULES: BlockRule[] = [
  ...LOCAL_BLOCK_RULES,
  ...LOCAL_WARN_RULES,
  ...STAGING_BLOCK_RULES,
  ...PRODUCTION_BLOCK_RULES,
];
