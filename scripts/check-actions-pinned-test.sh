#!/usr/bin/env bash
# Positive/negative proof for scripts/check-actions-pinned.sh.
#
# A gate with no proof it can fire is unverified, whatever its history says.
# Both directions are asserted deliberately: a matcher loosened until it stops
# false-positiving can just as easily stop firing, and a gate that never denies
# is the worse failure because it looks green.
#
# Pattern borrowed from lightwave-infrastructure-live's check-action-pins-test.sh
# (live#43), which had this right before the org-wide sweep did.
#
# Exit: 0 when the gate both blocks and allows correctly, 1 otherwise.

set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

GATE="scripts/check-actions-pinned.sh"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/wf"

fail=0
expect_deny() {
  if WORKFLOW_DIR="$TMP/wf" bash "$GATE" >/dev/null 2>&1; then
    echo "FAIL: $1 was allowed"
    fail=1
  fi
}
expect_allow() {
  if ! WORKFLOW_DIR="$TMP/wf" bash "$GATE" >/dev/null 2>&1; then
    echo "FAIL: $1 was denied"
    fail=1
  fi
}

# --- denies -----------------------------------------------------------------
cat > "$TMP/wf/bad.yml" <<'EOF'
jobs:
  x:
    steps:
      - uses: actions/checkout@v6
EOF
expect_deny "a mutable tag"

# A branch ref is worse than a tag — it adopts whatever lands next run.
cat > "$TMP/wf/bad.yml" <<'EOF'
jobs:
  x:
    steps:
      - uses: dtolnay/rust-toolchain@master
EOF
expect_deny "a branch ref"

# A short SHA is not a pin: it is a prefix, and prefixes are not unique forever.
cat > "$TMP/wf/bad.yml" <<'EOF'
jobs:
  x:
    steps:
      - uses: actions/checkout@d23441a
EOF
expect_deny "an abbreviated SHA"

# --- allows -----------------------------------------------------------------
rm -f "$TMP/wf/bad.yml"
cat > "$TMP/wf/good.yml" <<'EOF'
# Usage example for consumers — documentation, not an invocation:
#   uses: lightwave-media/pipelines-workflows/.github/workflows/ci.yml@<SHA> # vX.Y.Z
jobs:
  x:
    steps:
      - uses: actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803 # v6.1.0
      - uses: ./.github/workflows/local.yml
EOF
expect_allow "a pinned SHA, a local ref, and a commented usage example"

# An empty tree must pass rather than error — a repo with no workflows is not
# a violation, and a gate that fails on absence blocks work it does not govern.
rm -f "$TMP/wf/good.yml"
expect_allow "a workflow dir with no workflows"

if [ "$fail" -eq 0 ]; then
  echo "check-actions-pinned-test: gate blocks and allows correctly."
fi
exit "$fail"
