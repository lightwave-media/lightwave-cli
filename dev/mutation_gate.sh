#!/usr/bin/env bash
# Changed-code mutation gate. Gremlins' threshold flags returned success during
# a measured 28.21% mutator-coverage run, so this wrapper evaluates its JSON
# independently instead of trusting the candidate tool's exit status.

set -euo pipefail

readonly GREMLINS_VERSION="v0.6.0"
readonly DIFF_REF="${LW_MUTATION_DIFF_REF:-origin/main}"
readonly ENFORCEMENT="${LW_MUTATION_ENFORCEMENT:-observe}"
readonly MIN_EFFICACY="${LW_MUTATION_MIN_EFFICACY:-100}"
readonly MIN_COVERAGE="${LW_MUTATION_MIN_COVERAGE:-80}"

case "$ENFORCEMENT" in
    observe | warn | block) ;;
    *)
        echo "mutation gate: invalid LW_MUTATION_ENFORCEMENT=$ENFORCEMENT" >&2
        exit 2
        ;;
esac

command -v go >/dev/null 2>&1 || {
    echo "mutation gate: Go is required; restore the declared toolchain." >&2
    exit 2
}
command -v jq >/dev/null 2>&1 || {
    echo "mutation gate: jq is required to verify machine-readable results." >&2
    exit 2
}

REPORT="$(mktemp)"
readonly REPORT
trap 'rm -f "$REPORT"' EXIT

go run "github.com/go-gremlins/gremlins/cmd/gremlins@${GREMLINS_VERSION}" \
    unleash \
    --diff "$DIFF_REF" \
    --output "$REPORT" \
    --output-statuses lctv

EFFICACY="$(jq -r '.test_efficacy' "$REPORT")"
readonly EFFICACY
COVERAGE="$(jq -r '.mutations_coverage' "$REPORT")"
readonly COVERAGE

printf 'mutation gate: efficacy=%s%% coverage=%s%% enforcement=%s\n' \
    "$EFFICACY" "$COVERAGE" "$ENFORCEMENT"

if jq -e \
    --argjson min_efficacy "$MIN_EFFICACY" \
    --argjson min_coverage "$MIN_COVERAGE" \
    '.test_efficacy >= $min_efficacy and .mutations_coverage >= $min_coverage' \
    "$REPORT" >/dev/null; then
    echo "mutation gate: thresholds satisfied"
    exit 0
fi

cat >&2 <<SIGNAL
mutation gate: DEVELOPMENT signal
  Violated invariant: changed-code tests must kill at least ${MIN_EFFICACY}% of
  covered mutants and cover at least ${MIN_COVERAGE}% of viable mutants.
  Expected structure: boundary and branch tests reject changed behavior.
  Cure: add focused tests for LIVED and NOT COVERED locations, then rerun
  'mise run mutation'.
  Do not: lower the threshold, exclude the file, skip the task, or weaken the
  assertion merely to make this gate green.
SIGNAL

if [ "$ENFORCEMENT" = "block" ]; then
    exit 1
fi

echo "mutation gate: advisory during ratchet soak; evidence retained in output" >&2
