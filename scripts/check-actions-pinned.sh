#!/usr/bin/env bash
# check-actions-pinned.sh
#
# Fails when any THIRD-PARTY GitHub Action is referenced by a mutable tag or
# branch instead of a full commit SHA.
#
# Why this exists: a tag is mutable by design, so whoever controls it controls
# what executes inside a job holding this repo's secrets and cloud identity.
# lightwave-core#178 found ~60 such references across the org. Pinning them by
# hand is a one-time fix; without a check, the next added action reintroduces
# the problem, which is remediation item 3 of that issue.
#
# Deliberately self-contained — no jq, no yq, no network. It is meant to be
# copied verbatim into any repo that wants the same gate, and wired into that
# repo's `mise run ci` so it runs locally and in CI as the same bytes.
#
# Env inputs:
#   WORKFLOW_DIR — where to scan (default: ".github/workflows")
#
# Exit: 0 when every third-party reference is SHA-pinned, 1 otherwise.

set -euo pipefail

WORKFLOW_DIR="${WORKFLOW_DIR:-.github/workflows}"

if [ ! -d "${WORKFLOW_DIR}" ]; then
  echo "check-actions-pinned: no ${WORKFLOW_DIR}/ — nothing to check."
  exit 0
fi

# What counts as already-safe:
#   ./path                  local to this repo, resolved at this commit
#   owner/repo@<40 hex>     immutable
#
# Comment lines are skipped: usage examples routinely show `...@<SHA> # vX.Y.Z`
# as documentation, and failing on the line that teaches the rule would be a
# gate arguing with its own docs.
offenders=""
while IFS= read -r hit; do
  [ -n "${hit}" ] || continue
  offenders="${offenders}${hit}"$'\n'
done < <(
  grep -rnE '^[[:space:]]*-?[[:space:]]*uses:[[:space:]]*[^[:space:]]+' \
    "${WORKFLOW_DIR}" --include='*.yml' --include='*.yaml' 2>/dev/null |
    grep -vE '^[^:]+:[0-9]+:[[:space:]]*#' |
    grep -vE 'uses:[[:space:]]*\./' |
    grep -vE 'uses:[[:space:]]*[^[:space:]]+@[0-9a-f]{40}([[:space:]]|$)' ||
    true
)

if [ -z "${offenders}" ]; then
  echo "check-actions-pinned: all third-party actions pinned to a SHA."
  exit 0
fi

echo "❌ Unpinned third-party action(s) — a mutable tag is not a pin:"
echo ""
printf '%s' "${offenders}" | sed 's/^/  /'
echo ""
echo "Pin each to a full commit SHA, keeping the version in a trailing comment:"
echo "    uses: actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803 # v6.1.0"
echo ""
echo "Resolve the SHA for the tag you are ALREADY on — not the latest release:"
echo "    gh api repos/<owner>/<repo>/git/ref/tags/<tag> --jq '.object.sha,.object.type'"
echo ""
echo "Two traps that make a wrong pin look right:"
echo "  1. If that prints type 'tag', it is an ANNOTATED tag and the SHA is the"
echo "     tag object, not the commit. Dereference it:"
echo "         gh api repos/<owner>/<repo>/git/tags/<sha> --jq .object.sha"
echo "     A tag-object SHA looks correct and does not resolve."
echo "  2. 'releases/latest' may be a different MAJOR than the one in use. A pin"
echo "     changes what is immutable, never what runs."
exit 1
