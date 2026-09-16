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

# Name the destination explicitly. `git push -u origin HEAD` lets the
# destination be inherited from the upstream, and a worktree created the way the
# tooling itself recommends — `worktree add -b <branch> origin/main` — tracks
# origin/main. That is how this pushed HEAD -> main from a feature branch, and a
# fast-forwardable main would have taken the commit with no PR (#491).
git push -u origin "HEAD:refs/heads/${BRANCH}"

# Confirm against the remote rather than reporting the intent. This line used to
# print unconditionally, so it named the feature branch even on the run that
# targeted main.
PUSHED_SHA="$(git ls-remote --heads origin "${BRANCH}" | cut -f1)"
if [[ "${PUSHED_SHA}" != "$(git rev-parse HEAD)" ]]; then
  echo "push did not land: origin/${BRANCH} is ${PUSHED_SHA:-absent}, HEAD is $(git rev-parse HEAD)" >&2
  exit 1
fi
echo "✓ pushed origin/${BRANCH} @ ${PUSHED_SHA}"

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
