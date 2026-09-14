#!/usr/bin/env bash
# Positive/negative proof for scripts/failure-digest.sh.
#
# The digest only ever runs on a red CI run, which is rare by design — so
# without this it would sit unexercised for months and be discovered broken at
# exactly the moment it was needed. That is the shape of #345 itself, and
# repeating it here would be the joke writing itself.
#
# Both directions are asserted: it must NAME the failing test and the shuffle
# seed on a real failing transcript, and it must say plainly that it found
# neither when handed a transcript that has neither. A digest that prints
# nothing on a green input and nothing on a red one looks identical to a working
# one until someone needs it.
#
# Exit: 0 when the digest reports and stays quiet correctly, 1 otherwise.

set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

DIGEST="scripts/failure-digest.sh"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

fail=0

expect_contains() {
  local label="$1" needle="$2" out="$3"
  if ! printf '%s' "$out" | grep -qF -- "$needle"; then
    echo "FAIL: $label — digest never mentioned '$needle'"
    fail=1
  fi
}

# --- a real failing transcript: shape copied from `go test -race -shuffle=on -v`
cat > "$TMP/red.txt" <<'TRANSCRIPT'
-test.shuffle 1789404001775798000
=== RUN   TestThatPasses
=== RUN   TestPidAlive_DeadPID
    agent_test.go:191: pid 4242 reported as alive after Wait()
--- FAIL: TestPidAlive_DeadPID (0.00s)
--- PASS: TestThatPasses (0.00s)
FAIL
FAIL	github.com/lightwave-media/lightwave-cli/internal/agent	0.351s
ok  	github.com/lightwave-media/lightwave-cli/internal/cli	5.577s
TRANSCRIPT

red_out=$(bash "$DIGEST" "$TMP/red.txt")

# The whole point of #345: the digest must answer "which test?" and
# "how do I reproduce it?" without a re-run.
expect_contains "failing transcript" "TestPidAlive_DeadPID" "$red_out"
expect_contains "failing transcript" "1789404001775798000" "$red_out"
expect_contains "failing transcript" "internal/agent" "$red_out"
# -A 25 must carry the assertion along with the FAIL line, not just the header.
expect_contains "failing transcript" "reported as alive after Wait()" "$red_out"

# --- the other direction: a transcript with no failure and no seed.
# This is what a failure BEFORE the test binary looks like (a build or vet
# error), and the digest has to say so rather than print an empty section that
# reads as "nothing went wrong".
cat > "$TMP/green.txt" <<'TRANSCRIPT'
ok  	github.com/lightwave-media/lightwave-cli/internal/cli	5.577s
TRANSCRIPT

green_out=$(bash "$DIGEST" "$TMP/green.txt")

expect_contains "clean transcript" "no seed" "$green_out"
expect_contains "clean transcript" "no FAIL line" "$green_out"

# --- and a missing file, which is what happens when the run dies before `tee`.
missing_out=$(bash "$DIGEST" "$TMP/does-not-exist.txt")
expect_contains "missing transcript" "no FAIL line" "$missing_out"

if ! bash "$DIGEST" "$TMP/does-not-exist.txt" >/dev/null 2>&1; then
  echo "FAIL: digest exited non-zero on a missing transcript — it would mask the real failure"
  fail=1
fi

if [ "$fail" -eq 0 ]; then
  echo "failure-digest-test: digest names the failing test and stays honest when there is none."
fi

exit "$fail"
