#!/usr/bin/env bash
# release_prepare.sh — worktree → hook-safe commit → rebase main → ci → release:qa
# Usage: dev/release_prepare.sh [--yes]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=dev/release_common.sh
source "${ROOT}/dev/release_common.sh"

APPLY=false
if [[ "${1:-}" == "--yes" ]]; then
  APPLY=true
fi

release_export_dev_env
TOP="$(release_git_toplevel)"
BASE="$(release_default_branch)"

cd "${TOP}"
echo "== release prepare: ${TOP} (base origin/${BASE})"

STATUS="$(git status --porcelain | grep -v '^?? bin/' | grep -v '^?? lw$' || true)"
if [[ -n "${STATUS}" ]]; then
  if [[ "${APPLY}" != "true" ]]; then
    echo "dirty working tree — re-run with --yes to stage, commit, and continue"
    git status -sb
    exit 1
  fi
  release_stage_release_files
  git add -u
  if [[ -z "$(git diff --cached --name-only)" ]]; then
    echo "nothing staged after release_stage_release_files"
    exit 1
  fi
  MSG_FILE="$(mktemp)"
  release_format_commit_file "${MSG_FILE}"
  release_validate_commit_msg "${MSG_FILE}"
  git commit -F "${MSG_FILE}"
  rm -f "${MSG_FILE}"
  echo "✓ committed"
else
  echo "● working tree clean — skip commit"
fi

git fetch origin
if git rebase "origin/${BASE}"; then
  echo "✓ rebased onto origin/${BASE}"
else
  echo "✗ rebase failed — resolve conflicts and re-run" >&2
  exit 1
fi

if command -v mise >/dev/null 2>&1 && mise tasks ls 2>/dev/null | grep -q '\bci\b'; then
  mise run ci
  echo "✓ mise run ci"
else
  echo "warn: no mise ci task — skipping" >&2
fi

if [[ -f "${ROOT}/dev/release_qa_pass.sh" ]]; then
  bash "${ROOT}/dev/release_qa_pass.sh" "${LW_QA_ARTEFACT_DIR}"
  echo "✓ release_qa_pass"
fi

echo "✓ release prepare complete"
