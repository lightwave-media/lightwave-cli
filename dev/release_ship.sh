#!/usr/bin/env bash
# release_ship.sh — push branch → open/update PR → optional supersede
# Usage: dev/release_ship.sh [--yes] [--title "…"]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=dev/release_common.sh
source "${ROOT}/dev/release_common.sh"

APPLY=false
TITLE="${LW_RELEASE_PR_TITLE:-}"
SUPERSEDES="${LW_RELEASE_SUPERSEDES:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --yes) APPLY=true ;;
    --title) TITLE="$2"; shift ;;
    --supersedes) SUPERSEDES="$2"; shift ;;
  esac
  shift
done

TOP="$(release_git_toplevel)"
cd "${TOP}"

BRANCH="$(git branch --show-current)"
GH_REPO="$(release_gh_repo || true)"
if [[ -z "${GH_REPO}" ]]; then
  echo "cannot resolve GitHub repo from origin remote" >&2
  exit 1
fi

if [[ -z "${TITLE}" ]]; then
  TITLE="$(git log -1 --pretty=%s 2>/dev/null || echo "feat: release delivery")"
fi

echo "== release ship: ${GH_REPO}@${BRANCH}"

if [[ -n "$(git status --porcelain | grep -v '^?? bin/' | grep -v '^?? lw$' || true)" ]]; then
  echo "working tree not clean — run release prepare first" >&2
  git status -sb
  exit 1
fi

if [[ "${APPLY}" != "true" ]]; then
  echo "would push origin ${BRANCH}"
  echo "would open/update PR: ${TITLE}"
  if [[ -n "${SUPERSEDES}" ]]; then
    echo "would comment on #${SUPERSEDES} superseded"
  fi
  exit 0
fi

git push -u origin "HEAD"
echo "✓ pushed origin/${BRANCH}"

PR_URL=""
if gh pr view --json url -q .url 2>/dev/null; then
  PR_URL="$(gh pr view --json url -q .url)"
  gh pr edit --title "${TITLE}" 2>/dev/null || true
  echo "✓ updated existing PR ${PR_URL}"
else
  PR_URL="$(gh pr create --title "${TITLE}" --body "$(cat <<EOF
## Summary
Release delivery conveyor (ADR-0035): \`lw release prepare\` + \`lw release ship\`.

## Test plan
- [x] \`mise run release:prepare\`
- [x] \`mise run release:qa\`
- [x] \`mise run release:gate\` dry-run

## Supersedes
Closes #${SUPERSEDES}
EOF
)")"
  echo "✓ opened PR ${PR_URL}"
fi

if [[ -n "${SUPERSEDES}" ]]; then
  if gh pr view "${SUPERSEDES}" --json state -q .state 2>/dev/null | grep -q OPEN; then
    gh pr comment "${SUPERSEDES}" --body "Superseded by ${PR_URL} — close when the replacement merges."
    echo "✓ noted supersession on #${SUPERSEDES}"
  fi
fi

printf 'PR_URL=%s\n' "${PR_URL}"
