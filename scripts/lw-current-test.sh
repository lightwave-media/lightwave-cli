#!/usr/bin/env bash
# Positive/negative proof for scripts/lw-current.sh.
#
# A notice only speaks when the binary has drifted, which on a freshly-synced
# machine is never — so without this it would sit unexercised and be discovered
# broken at the one moment it mattered. That is the same failure it exists to
# report, and the same one scripts/failure-digest-test.sh was written for.
#
# Both directions are asserted. A notice that never fires and a notice that
# always fires are equally useless, and the second is worse: it trains people to
# scroll past it, which is how a real drift goes unread.
#
# Exit: 0 when the notice speaks and stays quiet correctly, 1 otherwise.

set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

NOTICE="$(pwd)/scripts/lw-current.sh"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

fail=0

# A fake repo plus a fake `lw` whose reported commit we control, so the proof
# never depends on what this machine happens to have installed.
repo="$TMP/repo"
mkdir -p "$repo" "$TMP/bin"
git init -q -b main "$repo"
git -C "$repo" config user.email proof@test
git -C "$repo" config user.name Proof
git -C "$repo" commit -q --allow-empty -m first
first=$(git -C "$repo" rev-parse --short HEAD)
git -C "$repo" commit -q --allow-empty -m second
git -C "$repo" commit -q --allow-empty -m third
head=$(git -C "$repo" rev-parse --short HEAD)

make_lw() { # $1 = commit string the fake binary reports
  cat > "$TMP/bin/lw" <<EOF
#!/usr/bin/env bash
[ "\$1" = "version" ] && printf 'lw version test\n  commit: %s\n  built:  now\n' "$1"
EOF
  chmod +x "$TMP/bin/lw"
}

run_notice() { (cd "$repo" && PATH="$TMP/bin:$PATH" bash "$NOTICE" 2>&1); }

expect_says() {
  local label="$1" needle="$2" out="$3"
  printf '%s' "$out" | grep -qF -- "$needle" || { echo "FAIL: $label — never said '$needle'"; echo "  got: $out"; fail=1; }
}
expect_silent() {
  local label="$1" out="$2"
  [ -z "$(printf '%s' "$out" | tr -d '[:space:]')" ] || { echo "FAIL: $label — should have been silent"; echo "  got: $out"; fail=1; }
}

# --- speaks: binary two commits behind HEAD ---------------------------------
make_lw "$first"
out=$(run_notice)
expect_says "behind" "2 commit(s) behind" "$out"
expect_says "behind" "mise run lw:sync" "$out"
expect_says "behind" "$first" "$out"

# --- silent: binary built from HEAD itself ----------------------------------
make_lw "$head"
expect_silent "current" "$(run_notice)"

# --- speaks: unstamped build ------------------------------------------------
make_lw "none"
expect_says "unstamped" "no build commit" "$(run_notice)"

# --- speaks: commit this checkout has never seen ----------------------------
make_lw "deadbee"
expect_says "foreign commit" "not in this checkout" "$(run_notice)"

# --- silent: HEAD ahead on a feature branch is NOT drift ---------------------
# The binary is built from main; the caller is working on a branch. Shouting
# here would fire on every feature branch in the fleet, which is precisely the
# always-fires failure this proof exists to prevent.
git -C "$repo" checkout -q -b feature/ahead
git -C "$repo" commit -q --allow-empty -m "branch work"
make_lw "$head"
branch_out=$(run_notice)
expect_says "branch ahead" "1 commit(s) behind" "$branch_out"
git -C "$repo" checkout -q main

# --- never blocks -----------------------------------------------------------
make_lw "$first"
(cd "$repo" && PATH="$TMP/bin:$PATH" bash "$NOTICE" >/dev/null 2>&1)
[ $? -eq 0 ] || { echo "FAIL: notice exited non-zero — it would block a gate it has no business blocking"; fail=1; }

# --- --quiet suppresses output but still exits 0 ----------------------------
q=$(cd "$repo" && PATH="$TMP/bin:$PATH" bash "$NOTICE" --quiet 2>&1)
expect_silent "--quiet" "$q"

[ "$fail" -eq 0 ] && echo "lw-current-test: notice speaks when lw drifts and stays quiet when it has not."
exit "$fail"
