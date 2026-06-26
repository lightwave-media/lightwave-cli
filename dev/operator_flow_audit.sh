#!/usr/bin/env bash
# operator_flow_audit.sh — sandboxed e2e operator flow with measured audit output.
# Writes ~/.lightwave/observability/operator-cli.jsonl via lw handlers.
set -euo pipefail

REAL_HOME="${HOME}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLEET_ROOT=""
for candidate in "$(cd "${ROOT}/../.." && pwd)" "$(cd "${ROOT}/../../.." && pwd)" "${REAL_HOME}/dev"; do
  if [[ -d "${candidate}/lightwave-core/src/schemas" ]]; then
    FLEET_ROOT="${candidate}"
    break
  fi
done
if [[ -z "${FLEET_ROOT}" ]]; then
  echo "error: could not locate fleet root (lightwave-core/src/schemas)" >&2
  exit 1
fi

LW="${LW_BIN:-${REAL_HOME}/.local/bin/lw}"
if [[ ! -x "${LW}" ]]; then
  LW="${ROOT}/bin/lw"
fi
if [[ ! -x "${LW}" ]]; then
  (cd "${ROOT}" && go build -o ./bin/lw ./cmd/lw)
  LW="${ROOT}/bin/lw"
fi

SANDBOX="${OPERATOR_AUDIT_SANDBOX:-${ROOT}/.operator-audit-sandbox}"
mkdir -p "${SANDBOX}/.lightwave/observability" "${SANDBOX}/.lightwave/brain/tool-feedback/lw"

export HOME="${SANDBOX}"
export LW_HOME_PRINT="${SANDBOX}/.lightwave"
export LW_OBSERVABILITY_DIR="${SANDBOX}/.lightwave/observability"
export LW_LIGHTWAVE_ROOT="${LW_LIGHTWAVE_ROOT:-${FLEET_ROOT}}"
export LW_CLI_DEV_DOMAINS=1

run_step() {
  local name="$1"
  shift
  echo "== ${name} =="
  if "$@"; then
    echo "  → PASS"
  else
    echo "  → FAIL (exit $?)"
  fi
  echo ""
}

run_step "version" "${LW}" version
run_step "home_sync" "${LW}" home sync
run_step "release_flags" "${LW}" release flag _ --list

echo "== audit tail (operator-cli.jsonl) =="
if [[ -f "${LW_OBSERVABILITY_DIR}/operator-cli.jsonl" ]]; then
  tail -5 "${LW_OBSERVABILITY_DIR}/operator-cli.jsonl" | python3 -m json.tool 2>/dev/null || tail -5 "${LW_OBSERVABILITY_DIR}/operator-cli.jsonl"
else
  echo "(no events yet)"
fi

echo ""
echo "== agent feedback (tool-feedback/lw) =="
FB="${LW_HOME_PRINT}/brain/tool-feedback/lw"
if compgen -G "${FB}/*.jsonl" > /dev/null; then
  tail -3 "${FB}/"*.jsonl
else
  echo "(no lessons — failures emit here)"
fi

echo ""
echo "sandbox: ${SANDBOX}"
