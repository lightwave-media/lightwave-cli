#!/usr/bin/env bun
/**
 * Untitled UI Lock Validator
 *
 * PreToolUse validator that BLOCKS edits to purchased Untitled UI source files.
 * These primitives (color tokens, _alt scales, typography, design tokens) are
 * held to a higher standard than anything an LLM should write, and the design
 * intent (e.g. _alt mapping to neutral grays in dark mode) is NOT a defect.
 *
 * Incident: 2026-04-28 — agent rewrote all 10 --color-utility-brand-*_alt
 * dark-mode mappings in theme.css and styles/lightwave-ui-theme.css from
 * var(--color-utility-gray-*) to var(--color-brand-*) believing the gray
 * mapping was "wrong". Joel: "Do not touch any of the CSS, it is meticulously
 * done. It is way higher standard than you can write. I've purchased this
 * code from UntitledUI."
 *
 * Brain memory: ~/.brain/memory/feedback/untitled_ui_purchased_dont_edit.yaml
 *
 * Override: set environment variable LW_ALLOW_UI_EDIT=1 to bypass for a
 * single explicit, file-named edit Joel has approved. The lock re-engages
 * the next session.
 */

interface UntitledUiLockViolation {
  pattern: string;
  message: string;
}

/**
 * Locked paths under packages/lightwave-ui/src/.
 * These are purchased Untitled UI source. Edits require explicit unlock.
 */
const LOCKED_PATTERNS: RegExp[] = [
  // CSS primitives — color tokens, typography, base styles
  /packages\/lightwave-ui\/src\/theme\.css$/,
  /packages\/lightwave-ui\/src\/colors\.css$/,
  /packages\/lightwave-ui\/src\/typography\.css$/,
  /packages\/lightwave-ui\/src\/globals\.css$/,
  /packages\/lightwave-ui\/src\/styles\/.*\.css$/,
];

/**
 * Check if a file path is under the Untitled UI lock.
 */
function isLockedPath(filePath: string): boolean {
  return LOCKED_PATTERNS.some((p) => p.test(filePath));
}

/**
 * Validate an edit against the Untitled UI lock.
 * Returns a violation if the file is locked AND no override is active.
 */
export function validateUntitledUiLock(
  filePath: string,
): UntitledUiLockViolation | null {
  if (!isLockedPath(filePath)) {
    return null;
  }

  // Override: explicit per-session unlock for an approved edit
  if (process.env.LW_ALLOW_UI_EDIT === "1") {
    return null;
  }

  return {
    pattern: filePath,
    message:
      `BLOCKED: ${filePath} is purchased Untitled UI source.\n` +
      `Color primitives, _alt scales, utility token mappings, and typography ` +
      `tokens MUST NOT be edited to "fix" what looks like a brand color problem.\n\n` +
      `The "_alt" suffix mapping to neutral grays in dark mode is DESIGN INTENT, not a defect.\n\n` +
      `Debug at the CONSUMER instead — work through this checklist:\n` +
      `  1. Read the route/component using the token (NOT the .css file).\n` +
      `  2. Is a different utility class more appropriate? (e.g. brand-* vs utility-brand-*_alt)\n` +
      `  3. Is the component missing a "dark:" variant for dark mode?\n` +
      `  4. Is a route wrapper forcing the wrong theme via document.documentElement?\n` +
      `  5. Is themeInitScript in __root.tsx the only thing touching <html>.classList?\n` +
      `  If steps 1–5 don't surface the bug, STOP and ASK Joel.\n\n` +
      `Reference memories:\n` +
      `  ~/.brain/memory/feedback/untitled_ui_purchased_dont_edit.yaml (hard rule)\n` +
      `  ~/.brain/memory/feedback/no_route_theme_overrides.yaml (theme ownership)\n` +
      `  ~/.brain/memory/failures/dark_mode_flash_misdiagnosis.yaml (the SFR that caused this lock)\n\n` +
      `Override (rare, requires Joel approval): LW_ALLOW_UI_EDIT=1 for the session.`,
  };
}
