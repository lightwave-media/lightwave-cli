/**
 * `lw check` discipline validator (CLAUDE.md "lw check Subcommand Requirements").
 *
 * Closes lightwave-platform#633 — P-4 hook consolidation for lightwave-cli.
 *
 * Fires on Write of internal/cli/check_<name>.go (excluding the umbrella
 * check_handlers.go) and blocks when:
 *
 *   1. The new file lacks a `// linked-incident: <path>` comment within
 *      the first ~50 lines. CLAUDE.md §1 requires every new check to
 *      cite a brain memory entry (failures/*.yaml or feedback/*.yaml)
 *      that describes the bug it prevents. No linked incident → the
 *      check isn't justified yet.
 *
 *   2. No sibling internal/cli/check_<name>_test.go exists on disk.
 *      CLAUDE.md §8 requires a test fixture with known-bad + known-good
 *      inputs. The pre-commit hook also enforces this at commit time;
 *      this validator catches it earlier so the loop is tighter when
 *      working through Claude Code.
 *
 * Edit operations are not blocked — they're presumed to be touching an
 * existing check that already passed the gate. The pre-commit hook still
 * fires on push.
 */

import { existsSync } from "node:fs";
import { dirname, basename } from "node:path";

export interface CheckDisciplineViolation {
  message: string;
  suggestion: string;
}

const CHECK_FILE_RE = /^(?:.*\/)?internal\/cli\/check_([a-z][a-z0-9_-]*)\.go$/;
const UMBRELLA = "check_handlers.go";
const LINKED_INCIDENT_RE =
  /^\s*\/\/\s*linked-incident:\s*\S+/m;

export function validateLwCheckDiscipline(
  toolName: string,
  filePath: string,
  content: string,
): CheckDisciplineViolation | null {
  // Only fire on Write — Edit gets a pass (file already exists and was
  // presumably gated at creation time; pre-commit will re-check).
  if (toolName !== "Write") return null;

  const fileName = basename(filePath);
  if (fileName === UMBRELLA) return null;

  const match = filePath.match(CHECK_FILE_RE);
  if (!match) return null;

  // Skip test files themselves.
  if (fileName.endsWith("_test.go")) return null;

  // (1) linked-incident comment.
  if (!LINKED_INCIDENT_RE.test(content)) {
    return {
      message:
        `New ${filePath} lacks a '// linked-incident: <path>' comment. ` +
        `CLAUDE.md §1 requires every new lw check to cite a brain memory entry ` +
        `(failures/*.yaml or feedback/*.yaml) that describes the bug it prevents.`,
      suggestion:
        `Add near the top of the file:\n` +
        `  // linked-incident: failures/YYYY-MM-DD-<slug>.yaml\n` +
        `or\n` +
        `  // linked-incident: feedback/YYYY-MM-DD-<slug>.yaml\n` +
        `\nIf you cannot link an incident, the check isn't justified yet — speculative or aesthetic checks do not ship.`,
    };
  }

  // (2) companion _test.go on disk.
  const dir = dirname(filePath);
  const checkName = match[1];
  const companion = `${dir}/check_${checkName}_test.go`;
  if (!existsSync(companion)) {
    return {
      message:
        `New ${filePath} has no sibling test file. ` +
        `CLAUDE.md §8 requires ${companion} with one known-bad fixture (proving the check fires) and one known-good fixture (proving it stays silent).`,
      suggestion:
        `Create the test file first (TDD), then return to writing the check. ` +
        `The pre-commit hook will also catch this at commit time.`,
    };
  }

  return null;
}
