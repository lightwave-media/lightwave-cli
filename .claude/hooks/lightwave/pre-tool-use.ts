#!/usr/bin/env bun
/**
 * Claude Code PreToolUse Hook
 *
 * Validates file operations BEFORE they happen:
 * 1. File naming conventions (kebab-case for TS, snake_case for Python)
 * 2. File placement (correct directories based on type)
 * 3. Protected file detection (don't modify without awareness)
 * 4. Consistency checks (similar to existing code patterns)
 *
 * This catches issues BEFORE files are created, enabling iterative improvement.
 */

import type { PreToolUseHookInput, PreToolUseHookOutput } from "./types";
import { validateDesignPatterns } from "./validators/design-patterns";
import { validateUntitledUiLock } from "./validators/untitled-ui-lock";

// =============================================================================
// Configuration
// =============================================================================

const NAMING_RULES: Record<string, RegExp> = {
  // TypeScript/JavaScript: kebab-case or PascalCase for components
  ".ts": /^[a-z][a-z0-9-]*(\.(spec|test|config))?\.ts$/,
  ".tsx": /^[A-Z][a-zA-Z0-9]*\.tsx$|^[a-z][a-z0-9-]*\.tsx$/,
  ".js": /^[a-z][a-z0-9-]*\.js$/,
  ".jsx": /^[A-Z][a-zA-Z0-9]*\.jsx$|^[a-z][a-z0-9-]*\.jsx$/,
  // Python: snake_case
  ".py": /^[a-z0-9][a-z0-9_]*\.py$/,
  // Config files: kebab-case or dot-prefixed
  ".json": /^[a-z][a-z0-9-]*\.json$|^\.[a-z][a-z0-9-]*\.json$/,
  ".yaml": /^[a-z][a-z0-9_-]*\.ya?ml$|^\.[a-z][a-z0-9_-]*\.ya?ml$/,
  ".yml": /^[a-z][a-z0-9_-]*\.ya?ml$|^\.[a-z][a-z0-9_-]*\.ya?ml$/,
};

// Files that require explicit acknowledgement before modification
const PROTECTED_FILES = [
  ".env",
  ".env.local",
  ".env.production",
  "package-lock.json",
  "pnpm-lock.yaml",
  "uv.lock",
  ".secrets.baseline",
  "pyproject.toml", // Core config
  "tsconfig.json", // Core config
];

// Directory patterns for file placement validation
const DIRECTORY_RULES: Record<string, string[]> = {
  // Python apps go in apps/
  "apps/": [".py"],
  // React components go in components/ or islands/
  "components/": [".tsx", ".jsx"],
  "islands/": [".tsx"],
  // Tests go in tests/ or __tests__/
  "tests/": [".py", ".ts", ".tsx"],
  "__tests__/": [".ts", ".tsx", ".test.ts", ".test.tsx"],
  // Hooks go in hooks/ or .claude/hooks/
  "hooks/": [".ts", ".tsx"],
  ".claude/hooks/": [".ts", ".sh"],
};

// Files that should not be created (they already exist or are managed elsewhere)
const EXISTING_PATTERNS = [
  /^\.git\//,
  /^node_modules\//,
  /^\.venv\//,
  /^__pycache__\//,
  /\.pyc$/,
];

// =============================================================================
// Validation Functions
// =============================================================================

interface ValidationResult {
  valid: boolean;
  warning?: string;
  suggestion?: string;
}

function validateNamingConvention(filePath: string): ValidationResult {
  const fileName = filePath.split("/").pop() || "";
  const ext = fileName.match(/\.[a-z]+$/)?.[0] || "";

  // Skip validation for special files
  if (fileName.startsWith(".") || fileName.includes("__")) {
    return { valid: true };
  }

  const rule = NAMING_RULES[ext];
  if (!rule) {
    return { valid: true }; // No rule for this extension
  }

  if (!rule.test(fileName)) {
    const conventions: Record<string, string> = {
      ".py": "snake_case (e.g., my_module.py)",
      ".ts": "kebab-case (e.g., my-module.ts)",
      ".tsx": "PascalCase for components (e.g., MyComponent.tsx) or kebab-case",
      ".js": "kebab-case (e.g., my-module.js)",
      ".jsx": "PascalCase for components or kebab-case",
    };

    return {
      valid: false,
      warning: `File name "${fileName}" doesn't follow naming convention`,
      suggestion: `Expected: ${conventions[ext] || "lowercase with dashes/underscores"}`,
    };
  }

  return { valid: true };
}

function validateProtectedFile(filePath: string): ValidationResult {
  const fileName = filePath.split("/").pop() || "";

  for (const protected_file of PROTECTED_FILES) {
    if (
      fileName === protected_file ||
      filePath.endsWith(`/${protected_file}`)
    ) {
      return {
        valid: true, // Allow but warn
        warning: `⚠️ Modifying protected file: ${protected_file}`,
        suggestion:
          "Ensure this change is intentional and won't break the build",
      };
    }
  }

  return { valid: true };
}

function validateFilePlacement(filePath: string): ValidationResult {
  const ext = filePath.match(/\.[a-z]+$/)?.[0] || "";

  // Check for misplaced files
  const warnings: string[] = [];

  // Python files outside apps/ or scripts/
  if (ext === ".py") {
    const validPythonDirs = [
      "apps/",
      "scripts/",
      "tests/",
      "lightwave-platform/",
      "packages/",
    ];
    const inValidDir = validPythonDirs.some((dir) => filePath.includes(dir));
    if (!inValidDir && !filePath.startsWith(".")) {
      warnings.push(
        `Python file "${filePath}" may be misplaced. Expected in: apps/, scripts/, tests/, or packages/`,
      );
    }
  }

  // React components outside expected directories
  if (ext === ".tsx" && !filePath.includes("test")) {
    const validReactDirs = [
      "components/",
      "islands/",
      "routes/",
      "app/",
      "src/",
      "frontend/",
    ];
    const inValidDir = validReactDirs.some((dir) => filePath.includes(dir));
    if (!inValidDir) {
      warnings.push(
        `React component "${filePath}" may be misplaced. Expected in: components/, islands/, routes/, or src/`,
      );
    }
  }

  if (warnings.length > 0) {
    return {
      valid: true, // Allow but warn
      warning: warnings.join("\n"),
      suggestion: "Verify this is the correct location for this file type",
    };
  }

  return { valid: true };
}

function validateNotExisting(filePath: string): ValidationResult {
  for (const pattern of EXISTING_PATTERNS) {
    if (pattern.test(filePath)) {
      return {
        valid: false,
        warning: `Cannot create file in protected directory: ${filePath}`,
        suggestion: "This path is managed by the system",
      };
    }
  }

  return { valid: true };
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

  // Only validate Write and Edit operations
  if (input.tool_name !== "Write" && input.tool_name !== "Edit") {
    // Allow other tools without validation
    const output: PreToolUseHookOutput = {};
    console.log(JSON.stringify(output));
    return;
  }

  const filePath = input.tool_input.file_path as string;
  if (!filePath) {
    const output: PreToolUseHookOutput = {};
    console.log(JSON.stringify(output));
    return;
  }

  // Run all validations — skip naming check on Edit (existing file, can't rename)
  const results: ValidationResult[] = [
    ...(input.tool_name === "Write"
      ? [validateNamingConvention(filePath)]
      : []),
    validateProtectedFile(filePath),
    validateFilePlacement(filePath),
    validateNotExisting(filePath),
  ];

  // Untitled UI lock — purchased CSS primitives are read-only by default
  const uiLockViolation = validateUntitledUiLock(filePath);
  if (uiLockViolation) {
    results.push({
      valid: false,
      warning: uiLockViolation.message,
      suggestion:
        "Debug at the consumer level (route or component using the token). " +
        "Do not edit primitives. If absolutely required, ask Joel first.",
    });
  }

  // Design pattern validation for .tsx files
  if (filePath.endsWith(".tsx")) {
    // Extract content: Write has `content`, Edit has `new_string`
    const content =
      (input.tool_input.content as string) ||
      (input.tool_input.new_string as string) ||
      "";
    if (content) {
      const violation = validateDesignPatterns(filePath, content);
      if (violation) {
        results.push({
          valid: false,
          warning: `Design pattern violation: ${violation.message}`,
          suggestion:
            "See /design-system skill for correct component usage patterns.",
        });
      }
    }
  }

  // Collect warnings and errors
  const warnings = results.filter((r) => r.warning).map((r) => r.warning!);
  const suggestions = results
    .filter((r) => r.suggestion)
    .map((r) => r.suggestion!);
  const hasBlockingError = results.some((r) => !r.valid);

  if (hasBlockingError) {
    // Block the operation
    const output: PreToolUseHookOutput = {
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: `File validation failed:\n${warnings.join("\n")}\n\nSuggestions:\n${suggestions.join("\n")}`,
      },
    };
    console.log(JSON.stringify(output));
    return;
  }

  if (warnings.length > 0) {
    // Allow with context (warnings visible to Claude)
    const output: PreToolUseHookOutput = {
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        additionalContext: `[File Validation Warnings]\n${warnings.join("\n")}\n${suggestions.length > 0 ? `\nSuggestions:\n${suggestions.join("\n")}` : ""}`,
      },
    };
    console.log(JSON.stringify(output));
    return;
  }

  // All good
  const output: PreToolUseHookOutput = {};
  console.log(JSON.stringify(output));
}

main().catch((err) => {
  console.error("PreToolUse hook error:", err);
  const output: PreToolUseHookOutput = {};
  console.log(JSON.stringify(output));
});
