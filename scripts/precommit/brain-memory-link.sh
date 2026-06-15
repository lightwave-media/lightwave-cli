#!/usr/bin/env bash
# Enforces CLAUDE.md "lw check Subcommand Requirements" §1:
#   "Linked incident — point to a brain memory entry
#    (~/.lightwave/brain/memory/failures/*.yaml or feedback/*.yaml) that describes
#    the bug it prevents. If you can't link one, the check isn't justified yet."
#
# For every staged NEW internal/cli/check_<name>.go (excluding the umbrella),
# require a `// linked-incident: <path>` comment near the top of the file
# pointing at an existing brain memory entry.
#
# Closes lightwave-platform#633 — P-4 hook consolidation for lightwave-cli.
#
# Pre-commit calls this with the list of staged Go files as $@.
# Standalone invocation: ./scripts/precommit/brain-memory-link.sh <files...>
# Exit code: 0 = pass, 1 = violation found.

set -euo pipefail

brain_root="${HOME}/.lightwave/brain/memory"

violations=()

for file in "$@"; do
  case "$file" in
    internal/cli/check_handlers.go) continue ;;
    internal/cli/check_*.go)
      case "$file" in
        *_test.go) continue ;;
      esac

      # Only check files that are NEW in this commit (added, not modified).
      # Pre-existing check files predate this gate.
      if ! git diff --cached --name-only --diff-filter=A | grep -Fxq "$file"; then
        continue
      fi

      # Grep first 50 lines of the staged version for the marker.
      staged_content=$(git diff --cached -- "$file" | grep '^+' | head -60 | sed 's/^+//')

      if ! echo "$staged_content" | grep -Eq '^[[:space:]]*//[[:space:]]*linked-incident:[[:space:]]*'; then
        violations+=("  $file — missing '// linked-incident: <path>' comment (CLAUDE.md §1)")
        continue
      fi

      # Extract the referenced path and verify it exists under ~/.brain/memory/
      incident_path=$(echo "$staged_content" \
        | grep -E '^[[:space:]]*//[[:space:]]*linked-incident:[[:space:]]*' \
        | head -1 \
        | sed -E 's|^[[:space:]]*//[[:space:]]*linked-incident:[[:space:]]*||' \
        | tr -d '[:space:]')

      # Allow either an absolute path under ~/.brain or a relative path
      # starting with "failures/" or "feedback/"
      resolved=""
      case "$incident_path" in
        /*) resolved="$incident_path" ;;
        \~/*) resolved="${incident_path/#\~/$HOME}" ;;
        failures/*|feedback/*) resolved="$brain_root/$incident_path" ;;
        *) resolved="$brain_root/$incident_path" ;;
      esac

      if [[ ! -f "$resolved" ]]; then
        violations+=("  $file — linked-incident points to $incident_path but $resolved is not a file")
      fi
      ;;
  esac
done

if [[ ${#violations[@]} -gt 0 ]]; then
  echo "lw check discipline violation: every new check needs a brain-memory linked incident" >&2
  printf '%s\n' "${violations[@]}" >&2
  echo "" >&2
  echo "Add a comment near the top of the file:" >&2
  echo "  // linked-incident: failures/YYYY-MM-DD-<slug>.yaml" >&2
  echo "or" >&2
  echo "  // linked-incident: feedback/YYYY-MM-DD-<slug>.yaml" >&2
  echo "" >&2
  echo "If you cannot link an incident, the check isn't justified yet —" >&2
  echo "speculative or aesthetic checks do not ship (CLAUDE.md \"Don't ship\")." >&2
  exit 1
fi

exit 0
