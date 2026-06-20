#!/usr/bin/env bash
# release-cycle.sh — hourly release-train gate report (ADR-0032).
#
# Runs `lw release merge <repo> --dry-run` for every release-candidate repo
# (the TOML sections in ~/.lightwave/config/releases.toml — the source of
# truth, so this list never drifts). It reports which open PRs are eligible to
# merge (mergeable + green CI + an active CTO sign-off) without merging anything.
#
# This is deliberately dry-run. The merge gate ("release-engineer executes, CTO
# sign-off required") stays a human-in-the-loop report until the sign-off flow
# is trusted; flip --dry-run → --yes here to let the cycle auto-merge.
#
# Invoked hourly by com.lightwave.release-cycle.plist (StartInterval 3600).
set -uo pipefail

PINS="${LW_RELEASES_TOML:-$HOME/.lightwave/config/releases.toml}"
LW="${LW_BIN:-lw}"

echo "=== release-cycle $(date '+%Y-%m-%dT%H:%M:%S%z') ==="

if [ ! -f "$PINS" ]; then
  echo "no release pins at $PINS — nothing to gate"
  exit 0
fi

# Release-candidate repos = the [<repo>] section headers in releases.toml.
repos=$(grep -oE '^\[[a-z0-9-]+\]' "$PINS" | tr -d '[]')
if [ -z "$repos" ]; then
  echo "no release-candidate repos pinned"
  exit 0
fi

for repo in $repos; do
  echo "--- $repo ---"
  "$LW" release merge "$repo" --dry-run || echo "  ($repo gate check failed)"
done

echo "=== release-cycle done ==="
