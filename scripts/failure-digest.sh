#!/usr/bin/env bash
# Summarise a `go test -v` transcript down to what a person needs on a red run.
#
# #345: `Tests (ubuntu-latest)` failed and the entire record was
#
#   internal/agent | Failed 0.35s
#
# no test name, no assertion, no race report. base-test.yml pipes `go test -v`
# straight into the JUnit converter, so the job log holds XML rather than test
# output, and `group_suite: true` collapses the per-test detail on the way out.
# The transcript was already being written to test-output.txt and thrown away.
#
# A re-run passes and destroys the only evidence, so the digest goes in the job
# LOG, not only an artifact: an artifact needs a deliberate download and expires,
# and by then the question is usually just "was that mine, or a flake".
#
# Lives in a script rather than inline YAML so scripts/failure-digest-test.sh can
# drive it both directions on every CI run. An extraction that has only ever been
# read, never run, is the same unverified gate #345 is about.
#
# Usage: failure-digest.sh <test-output.txt>
# Exit:  0 always — a digest must never be the reason a run fails.

set -uo pipefail

transcript="${1:-test-output.txt}"

echo '--- shuffle seeds (reproduce with: go test -shuffle=<seed> <pkg>) ---'
grep -E '^-test\.shuffle ' "$transcript" 2>/dev/null ||
  echo '(no seed — the run did not reach the test binary)'

echo
echo '--- failures and race reports ---'
# -B as well as -A, and that is not symmetry for its own sake: `go test -v`
# prints the assertion BEFORE the `--- FAIL` line it belongs to, so trailing
# context alone names the test and drops the reason — which is the more useful
# half. testify's Error Trace / Error / Test / Messages block runs ~10 lines.
grep -nE '^(--- FAIL|    --- FAIL|FAIL\b|panic:|WARNING: DATA RACE)' -B 15 -A 20 "$transcript" 2>/dev/null ||
  echo '(no FAIL line — the failure was before or outside the test run)'

exit 0
