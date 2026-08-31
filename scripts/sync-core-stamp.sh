#!/usr/bin/env bash
# Refresh internal/corestamp from a lightwave-core checkout.
#
# lightwave-cli is public; lightwave-core is private. Importing
# github.com/lightwave-media/lightwave-core/bindings/go as a module breaks
# `go build`/`go install` for anyone outside the org and forces a
# private-module token into every CI job, so the binding is vendored instead.
#
# The cost of vendoring is staleness: internal/corestamp is a snapshot and can
# lag the canonical stamp. This script is the single supported way to refresh
# it. Run it after any change under lightwave-core/src/schemas that lw needs to
# see without a checkout.
#
# Usage:
#   scripts/sync-core-stamp.sh [path-to-lightwave-core]
#
# Defaults to a sibling checkout at ../lightwave-core, or $LW_LIGHTWAVE_ROOT
# /lightwave-core when that is set.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dst="$repo/internal/corestamp"

if [ $# -ge 1 ]; then
  core="$1"
elif [ -n "${LW_LIGHTWAVE_ROOT:-}" ]; then
  core="$LW_LIGHTWAVE_ROOT/lightwave-core"
else
  core="$repo/../lightwave-core"
fi

binding="$core/bindings/go"
[ -d "$binding" ] || {
  echo "no lightwave-core Go binding at $binding" >&2
  echo "pass the checkout path: scripts/sync-core-stamp.sh /path/to/lightwave-core" >&2
  exit 1
}

# Atomic swap: stage beside the target, then replace — so a mid-copy failure
# (disk full, perms) leaves the existing mirror intact rather than half-gone.
rm -rf "${dst}/schemas.tmp"
cp -R "$binding/schemas" "${dst}/schemas.tmp"
rm -rf "${dst}/schemas"
mv "${dst}/schemas.tmp" "${dst}/schemas"

# The loader carries the stamp version lw reports; keep it with the schemas it
# describes. Package docs and the package clause are lightwave-cli's own — only
# the Version constant is lifted across.
version="$(sed -n 's/^const Version = "\(.*\)"$/\1/p' "$binding/loader.go")"
[ -n "$version" ] || { echo "could not read Version from $binding/loader.go" >&2; exit 1; }

tmp="$(mktemp)"
sed "s/^const Version = \".*\"$/const Version = \"$version\"/" "$dst/loader.go" > "$tmp"
mv "$tmp" "$dst/loader.go"

echo "synced $binding/schemas -> internal/corestamp/schemas (stamp v$version)"
echo "run 'mise run ci' and commit internal/corestamp/"
