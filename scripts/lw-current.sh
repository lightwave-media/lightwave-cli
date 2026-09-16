#!/usr/bin/env bash
# Notice when the `lw` on PATH is older than the source you are standing in.
#
# `lw` is the command spine of this shell: project hooks, agent sessions and
# `mise run ci` itself all shell out to the PATH-resolved binary. It is built
# from source by `mise run lw:sync`, and nothing has ever compared the build to
# the checkout — so a binary drifts behind main silently, for as long as nobody
# thinks to look.
#
# Measured on 2026-09-16: the installed lw was built from f91df3c while main was
# at 199eb2a — six merged PRs behind, for a whole working day. One of those six
# was the fix for `lw epic list`, so every session on this machine kept getting
# the old Django-era error from a defect that had already been fixed and merged.
# Nothing anywhere said so.
#
# NON-BLOCKING, and that is deliberate. main moves several times an hour in this
# fleet, so a gate that failed on any drift would fail constantly for a reason
# unrelated to the change in hand — which is how you teach people to bypass
# gates (lightwave-core#726 is a live example of exactly that damage). This
# prints and exits 0. It is a notice at the moment of work, not a verdict.
#
# Usage: lw-current.sh [--quiet]
# Exit:  always 0.

set -uo pipefail

quiet=0
[ "${1:-}" = "--quiet" ] && quiet=1

say() { [ "$quiet" -eq 1 ] || printf '%s\n' "$*"; }

binary="$(command -v lw 2>/dev/null || true)"
if [ -z "$binary" ]; then
  say "notice: no lw on PATH — install it with: mise run lw:sync"
  exit 0
fi

# The build stamps its own commit via ldflags (see ~/dev/mise.toml lw:sync).
# A binary without one reports "none"/"unknown" and cannot be compared; say so
# rather than guessing, because a silent skip here is the failure this exists
# to end.
built_from="$(lw version 2>/dev/null | awk '/^[[:space:]]*commit:/ {print $2; exit}')"
case "${built_from:-}" in
  "" | none | unknown | dev)
    say "notice: ${binary} reports no build commit — rebuild with: mise run lw:sync"
    exit 0
    ;;
esac

# Compare against the checkout this script lives in, not against a fetch: the
# question is "is the binary behind the source in front of me", and answering it
# must not depend on the network.
if ! git rev-parse --verify -q "${built_from}^{commit}" >/dev/null 2>&1; then
  say "notice: ${binary} was built from ${built_from}, which is not in this checkout"
  say "        rebuild from here with: mise run lw:sync"
  exit 0
fi

head_sha="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"

# Ancestor, not equality. Working on a feature branch legitimately puts HEAD
# ahead of a binary built from main, and that is not drift worth shouting about
# — only a binary built from something HEAD does not contain is stale.
if git merge-base --is-ancestor "$built_from" HEAD 2>/dev/null; then
  behind="$(git rev-list --count "${built_from}..HEAD" 2>/dev/null || echo 0)"
  if [ "${behind:-0}" -gt 0 ]; then
    say "notice: lw is ${behind} commit(s) behind this checkout (built ${built_from}, HEAD ${head_sha})"
    say "        every hook and agent session on this machine runs that binary"
    say "        make it current with: mise run lw:sync"
  fi
  exit 0
fi

# Not an ancestor: the binary came from a branch this checkout has not merged.
say "notice: lw was built from ${built_from}, which is not an ancestor of HEAD (${head_sha})"
say "        that binary carries work this checkout does not have, or vice versa"
say "        make it match: mise run lw:sync"
exit 0
