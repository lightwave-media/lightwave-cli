#!/usr/bin/env bash
# Enforces CLAUDE.md "lw check Subcommand Requirements" §8:
#   "Test fixture — internal/cli/check_<name>_test.go with one fixture
#    proving the check fires on a known-bad input and one proving it stays
#    silent on a known-good input."
#
# For every staged NEW (added) file matching internal/cli/check_*.go
# (excluding the umbrella check_handlers.go), require a sibling
# internal/cli/check_<name>_test.go to also exist (staged or already in tree).
#
# Closes lightwave-platform#633 — P-4 hook consolidation for lightwave-cli.
#
# Pre-commit calls this with the list of staged Go files as $@.
# Standalone invocation: ./scripts/precommit/check-companion-test.sh <files...>
# Exit code: 0 = pass, 1 = violation found.

set -euo pipefail

violations=()

for file in "$@"; do
  case "$file" in
    internal/cli/check_handlers.go) continue ;;  # umbrella, skipped
    internal/cli/check_*.go)
      # Skip if this is itself a test file
      case "$file" in
        *_test.go) continue ;;
      esac

      # Skip if file is being deleted (not really "new")
      status=$(git diff --cached --name-status -- "$file" 2>/dev/null | awk '{print $1}' | head -1)
      if [[ "$status" == "D" ]]; then
        continue
      fi

      base="${file%.go}"
      companion="${base}_test.go"

      # Companion test must exist as a tracked file OR as a staged add.
      if [[ ! -f "$companion" ]]; then
        # Also check if it's staged (added in this commit)
        if ! git diff --cached --name-only --diff-filter=A | grep -Fxq "$companion"; then
          violations+=("  $file — missing $companion (CLAUDE.md \"lw check Subcommand Requirements\" §8)")
        fi
      fi
      ;;
  esac
done

if [[ ${#violations[@]} -gt 0 ]]; then
  echo "lw check discipline violation: every internal/cli/check_<name>.go needs a sibling _test.go" >&2
  printf '%s\n' "${violations[@]}" >&2
  echo "" >&2
  echo "Add the companion test file with one known-bad fixture (proving the" >&2
  echo "check fires) and one known-good fixture (proving it stays silent)." >&2
  echo "Example: internal/cli/check_theme_test.go alongside check_theme.go." >&2
  exit 1
fi

exit 0
