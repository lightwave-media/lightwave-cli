#!/usr/bin/env bun
/**
 * Design Pattern Validator
 *
 * PreToolUse validator that catches common anti-patterns in frontend .tsx files:
 * - Inline SVG icon components (use @untitledui/icons)
 * - Raw <button className= (use Button from lightwave-ui)
 * - Raw <nav className= (use SidebarNavigation from lightwave-ui)
 * - Raw <table (use Table compound from lightwave-ui)
 * - Raw <h2 className= (use SectionHeader.Heading from lightwave-ui)
 *
 * Only applies to files under src/frontend/src/ (not lightwave-ui source).
 */

interface DesignPatternViolation {
  pattern: string;
  message: string;
}

const PATTERNS: { regex: RegExp; message: string }[] = [
  {
    regex: /(?:function|const)\s+\w+Icon\s*[=(][\s\S]*?<svg/,
    message:
      "Use @untitledui/icons instead of inline SVG icon components. Import: `import { IconName } from '@untitledui/icons'`",
  },
  {
    regex: /<button\s+className=/,
    message:
      "Use Button from lightwave-ui instead of raw <button>. Import: `import { Button } from '@lightwave-media/ui/components/base/buttons/button'`",
  },
  {
    regex: /<nav\s+className=/,
    message:
      "Use SidebarNavigationSimple from lightwave-ui instead of raw <nav>. Import: `import { SidebarNavigationSimple } from '@lightwave-media/ui/components/application/app-navigation/sidebar-navigation/sidebar-simple'`",
  },
  {
    regex: /<table[\s>]/,
    message:
      "Use Table compound component from lightwave-ui instead of raw <table>. Import from `@lightwave-media/ui/components/application/tables`",
  },
  {
    regex: /<h2\s+className=/,
    message:
      "Use SectionHeader.Heading from lightwave-ui instead of raw <h2>. Import: `import { SectionHeader } from '@lightwave-media/ui/components/application/section-headers/section-headers'`",
  },
];

/**
 * Check if a file path is within the frontend app source (not lightwave-ui).
 */
function isFrontendAppFile(filePath: string): boolean {
  return (
    filePath.includes("src/frontend/src/") &&
    !filePath.includes("packages/lightwave-ui/") &&
    filePath.endsWith(".tsx")
  );
}

/**
 * Validate content for design pattern violations.
 * Returns the first violation found, or null if clean.
 */
export function validateDesignPatterns(
  filePath: string,
  content: string,
): DesignPatternViolation | null {
  if (!isFrontendAppFile(filePath)) {
    return null;
  }

  for (const { regex, message } of PATTERNS) {
    if (regex.test(content)) {
      return { pattern: regex.source, message };
    }
  }

  return null;
}
