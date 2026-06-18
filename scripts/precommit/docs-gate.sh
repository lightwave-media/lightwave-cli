#!/usr/bin/env bash
# Pre-commit docs factory gate: sync → render (when lightwave-core schemas exist).
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

LW="lw"
if [[ -f go.mod ]] && grep -q lightwave-cli go.mod 2>/dev/null; then
  LW="go run ./cmd/lw"
fi

if ! command -v lw >/dev/null 2>&1 && [[ "$LW" == "lw" ]]; then
  echo "docs-gate: lw not on PATH — skip"
  exit 0
fi

if [[ ! -d docs ]]; then
  exit 0
fi

STAGED="$(git diff --cached --name-only 2>/dev/null || true)"
if [[ -n "$STAGED" ]] && ! echo "$STAGED" | grep -qE '^(docs/|internal/docsfactory/|internal/cli/docs|scripts/precommit/docs-gate)'; then
  exit 0
fi

resolve_core_root() {
  if [[ -n "${LW_LIGHTWAVE_CORE:-}" && -d "${LW_LIGHTWAVE_CORE}/src/schemas" ]]; then
    echo "${LW_LIGHTWAVE_CORE}"
    return 0
  fi
  local candidate
  for candidate in \
    "$ROOT/../lightwave-core" \
    "${LW_PATHS_LIGHTWAVE_ROOT:-}/lightwave-core" \
    "$HOME/dev/lightwave-core"; do
    if [[ -n "$candidate" && -d "$candidate/src/schemas" ]]; then
      echo "$candidate"
      return 0
    fi
  done
  return 1
}

if ! CORE_ROOT="$(resolve_core_root)"; then
  echo "docs-gate: lightwave-core schemas not found — skip (set LW_LIGHTWAVE_CORE on dev machines)"
  exit 0
fi

export LW_LIGHTWAVE_CORE="$CORE_ROOT"

$LW docs sync || {
  $LW failure record --kind docs-drift --summary "docs sync failed in pre-commit" 2>/dev/null || true
  exit 1
}
$LW docs render || exit 1
# Full strict check runs in mise run ci / pre-push; pre-existing authored docs
# shape debt in lightwave-cli is tracked separately from this gate.
git add docs/ 2>/dev/null || true
exit 0
