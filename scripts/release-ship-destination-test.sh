#!/usr/bin/env bash
# Proof for the two invariants that broke in #491, both exercised against real
# git in a temp directory — no network, no fixtures to drift.
#
#   1. release_git_toplevel resolves the CALLER's repo. It used to resolve the
#      directory holding these scripts, so prepare/ship always acted on the
#      canonical lightwave-cli checkout and could not ship the worktree the
#      operator was standing in.
#
#   2. The push names its destination. `git push -u origin HEAD` inherits the
#      destination from the upstream, and a worktree made the documented way
#      (`worktree add -b <branch> origin/main`) tracks origin/main — so ship
#      pushed a feature branch's HEAD at main.
#
# Asserting only (1) would leave the dangerous half unproven, and (2) is the
# half that can put an unreviewed commit on main.
set -euo pipefail

# A git hook exports GIT_DIR and friends, and they outrank `cd`. Without this,
# every git command below addresses the repo that INVOKED the hook rather than
# the fixture — so the test's own commits land on the caller's branch and its
# `branch -M main` tries to rewrite the real main. That happened on the first
# run through the pre-push gate. The other shell gates in mise.toml unset these
# for the same reason; doing it here keeps the script correct however it is run.
# shellcheck source=scripts/fixture-git-env.sh
source "$(dirname "${BASH_SOURCE[0]}")/fixture-git-env.sh"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

fail() {
  echo "release-ship-destination-test: $1" >&2
  exit 1
}

git init --quiet --bare "${TMP}/origin.git"
git clone --quiet "${TMP}/origin.git" "${TMP}/work"
cd "${TMP}/work"
git commit --quiet --allow-empty -m "root"
git branch -M main
git push --quiet -u origin main

# A worktree created exactly as the tooling recommends: new branch, based on
# origin/main, which sets its upstream to origin/main.
git worktree add --quiet "${TMP}/wt" -b feature/ship-probe origin/main
cd "${TMP}/wt"
git commit --quiet --allow-empty -m "work"

# ─── 1. the caller's repo, not the script's ───────────────────────────────────
# shellcheck source=dev/release_common.sh
source "${ROOT}/dev/release_common.sh"

TOP="$(release_git_toplevel)"
[[ "${TOP}" == "$(cd "${TMP}/wt" && pwd -P)" ]] \
  || fail "release_git_toplevel returned '${TOP}', expected the worktree ${TMP}/wt"

# ─── 2. the push lands on the branch, never on the upstream ───────────────────
BRANCH="$(git branch --show-current)"
[[ "$(git rev-parse --abbrev-ref '@{upstream}')" == "origin/main" ]] \
  || fail "fixture is wrong: the probe branch must track origin/main"

MAIN_BEFORE="$(git ls-remote --heads origin main | cut -f1)"
git push --quiet -u origin "HEAD:refs/heads/${BRANCH}"

PUSHED="$(git ls-remote --heads origin "${BRANCH}" | cut -f1)"
[[ "${PUSHED}" == "$(git rev-parse HEAD)" ]] \
  || fail "origin/${BRANCH} is '${PUSHED:-absent}', expected HEAD $(git rev-parse HEAD)"

MAIN_AFTER="$(git ls-remote --heads origin main | cut -f1)"
[[ "${MAIN_AFTER}" == "${MAIN_BEFORE}" ]] \
  || fail "the push moved origin/main from ${MAIN_BEFORE} to ${MAIN_AFTER} — this is #491"

echo "release-ship-destination-test: toplevel follows the caller; the push lands on the branch and leaves main alone."
