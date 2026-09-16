#!/usr/bin/env bash
# Patch → build → exercise, without touching anything another session uses.
#
# `lw self sync` is the existing fast path and is wrong for iterating: it
# builds from ~/dev/lightwave-cli — a checkout this machine routinely has held
# by another session, on another branch — and installs over ~/.local/bin/lw,
# the binary every other session is running. Both side effects land on people
# who did not ask for your change.
#
# This builds THIS worktree into a sandbox nothing else is on PATH for, and
# points the stamp at whichever lightwave-core tree you name — including one
# with uncommitted edits, via `--ref worktree`. So a CLI change and a schema
# change can be exercised together, live, before either is committed.
#
#   eval "$(dev/patch_loop.sh)"                     # build + enter the sandbox
#   eval "$(dev/patch_loop.sh --core ~/.worktrees/lightwave-core/my-branch)"
#   lw codegen go data --scope local --ref worktree # reads YOUR stamp
#   dev/patch_loop.sh --promote                     # install globally, on purpose
#
# Nothing is promoted automatically. The point is that the blast radius of an
# experiment is one shell.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SANDBOX="${LW_PATCH_SANDBOX:-${TMPDIR:-/tmp}/lw-patch-$(id -u)/$(basename "$REPO")}"
CORE=""
PROMOTE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --core)    CORE="${2:?--core needs a path to a lightwave-core checkout}"; shift 2 ;;
    --promote) PROMOTE=1; shift ;;
    -h|--help) sed -n '2,20p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

say() { printf '# %s\n' "$*" >&2; }

if [ "$PROMOTE" = 1 ]; then
  # Promotion is loud and explicit: it changes what every other session runs.
  say "promoting $REPO -> ~/.local/bin/lw"
  mkdir -p "$HOME/.local/bin"
  go build -C "$REPO" -o "$HOME/.local/bin/lw" ./cmd/lw
  say "promoted: $("$HOME/.local/bin/lw" --version 2>/dev/null || echo '(no --version)')"
  exit 0
fi

mkdir -p "$SANDBOX/bin"
say "building $REPO"
go build -C "$REPO" -o "$SANDBOX/bin/lw" ./cmd/lw

# `lw` resolves lightwave-core as <root>/lightwave-core, so a worktree — whose
# directory is named for its branch, not the repo — only works through a
# symlink. Building that here is what makes `--ref worktree` read the tree you
# are actually editing instead of the canonical checkout, which CLAUDE.md §16
# warns is never safe to read as a committed fact on this machine.
if [ -n "$CORE" ]; then
  CORE="$(cd "$CORE" && pwd)"
  [ -d "$CORE/src/schemas" ] || { echo "not a lightwave-core checkout: $CORE" >&2; exit 2; }
  mkdir -p "$SANDBOX/root"
  ln -sfn "$CORE" "$SANDBOX/root/lightwave-core"
  say "stamp: $CORE"
  printf 'export LW_LIGHTWAVE_ROOT=%q\n' "$SANDBOX/root"
fi

# shellcheck disable=SC2016 # $PATH must stay literal: the caller's shell expands it on eval
printf 'export PATH=%q:"$PATH"\n' "$SANDBOX/bin"
printf 'export LW_PATCH_SANDBOX=%q\n' "$SANDBOX"
say "sandboxed lw: $SANDBOX/bin/lw"
say "global lw is untouched at $(command -v lw 2>/dev/null || echo '<none>')"
