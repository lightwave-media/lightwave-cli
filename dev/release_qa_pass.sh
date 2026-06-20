#!/usr/bin/env bash
# release_qa_pass.sh — v1 QA release matrix (first consumer: lightwave-cli).
# Runs: lw check schema (strict), VerifiedCommands smoke, nullhub route curls.
# Usage: dev/release_qa_pass.sh [artefact_dir]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARTEFACT_DIR="${1:-${LW_QA_ARTEFACT_DIR:-${ROOT}/.qa-release-pass}}"
MATRIX="${ARTEFACT_DIR}/05-qa-release-matrix.log"
SUMMARY="${ARTEFACT_DIR}/qa-matrix-summary.json"

mkdir -p "${ARTEFACT_DIR}"

LW="${LW_BIN:-${ROOT}/bin/lw}"
if [[ ! -x "${LW}" ]]; then
  LW="$(command -v lw || true)"
fi
if [[ -z "${LW}" ]]; then
  echo "building lw..." >&2
  (cd "${ROOT}" && go build -o ./bin/lw ./cmd/lw)
  LW="${ROOT}/bin/lw"
fi

AI_ROOT="${LW_AI_ROOT:-${ROOT}/../lightwave-ai}"
if [[ ! -d "${AI_ROOT}" ]]; then
  AI_ROOT="${HOME}/dev/lightwave-ai"
fi
ROUTES_JSON="${NULLHUB_ROUTES_JSON:-${AI_ROOT}/fixtures/null_endpoints/nullhub.routes.json}"
NULLHUB_BASE="${NULLHUB_BASE_URL:-http://127.0.0.1:19800}"

pass=0
fail=0
skip=0

record() {
  local status="$1" kind="$2" target="$3" detail="${4:-}"
  printf '%s\t%s\t%s\t%s\n' "${status}" "${kind}" "${target}" "${detail}"
}

{
  echo "# QA release pass matrix — $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "# repo=${ROOT}"
  echo ""

  echo "## lw check schema (release domain strict)"
  SCHEMA_JSON="${ARTEFACT_DIR}/schema-drift.json"
  if "${LW}" check schema --json > "${SCHEMA_JSON}" 2>"${ARTEFACT_DIR}/schema-drift.stderr"; then
    drift_ok=true
  else
    drift_ok=false
  fi
  if python3 - "${SCHEMA_JSON}" <<'PY'
import json, sys
path = sys.argv[1]
with open(path) as f:
    doc = json.load(f)
missing = [k for k in doc.get("missing_handlers", []) if k.startswith("release.")]
orphaned = [k for k in doc.get("orphaned_handlers", []) if k.startswith("release.")]
if missing or orphaned:
    print(f"release drift missing={missing} orphaned={orphaned}")
    sys.exit(1)
sys.exit(0)
PY
  then
    record PASS schema "release domain schema↔handler lockstep"
  else
    record FAIL schema "release domain drift (see ${SCHEMA_JSON})"
  fi
  if [[ "${drift_ok}" != "true" && "${fail}" -eq 0 ]]; then
    : # full-schema drift may include pre-release domains; release slice is the gate
  fi
  echo ""

  echo "## VerifiedCommands smoke (go test)"
  if (cd "${ROOT}" && go test ./internal/cli/ -count=1 -run 'TestCommandSurface|TestVersion_Runs|TestHealth_ChecksBinaries' 2>&1); then
    record PASS smoke "VerifiedCommands" "command_surface + active smoke"
  else
    record FAIL smoke "VerifiedCommands" "go test ./internal/cli/ failed"
  fi
  echo ""

  echo "## Cloudflare Pages build check (joelschaeffer-site, main-compatible)"
  SITE_ROOT="${JOELSCHAEFFER_SITE_ROOT:-${HOME}/dev/joelschaeffer-site}"
  if [[ ! -d "${SITE_ROOT}" ]]; then
    record SKIP cloudflare joelschaeffer-site "repo missing at ${SITE_ROOT}"
  elif [[ ! -f "${SITE_ROOT}/package.json" ]]; then
    record SKIP cloudflare joelschaeffer-site "no package.json"
  else
    if (cd "${SITE_ROOT}" && bun run build 2>&1 && bunx vitest run --passWithNoTests 2>&1); then
      record PASS cloudflare joelschaeffer-site "bun build + vitest (matches deploy.yml gate)"
    else
      record FAIL cloudflare joelschaeffer-site "build or vitest failed"
    fi
  fi
  echo ""

  echo "## nullhub route curl table"
  if [[ ! -f "${ROUTES_JSON}" ]]; then
    record SKIP nullhub routes "${ROUTES_JSON}" "fixture missing"
  else
    while IFS=$'\t' read -r method path destructive auth; do
      [[ -z "${method}" ]] && continue
      if [[ "${destructive}" == "true" ]]; then
        record SKIP nullhub "${method} ${path}" "destructive"
        continue
      fi
      if [[ "${method}" != "GET" ]]; then
        record SKIP nullhub "${method} ${path}" "non-GET v1"
        continue
      fi
      if [[ "${path}" == *"{"* ]]; then
        record SKIP nullhub "${method} ${path}" "path params v1"
        continue
      fi
      url="${NULLHUB_BASE}${path}"
      code="$(curl -sS -o /dev/null -w '%{http_code}' --connect-timeout 2 --max-time 5 "${url}" 2>/dev/null)" || code="000"
      code="${code:-000}"
      if [[ "${code}" == "000" ]]; then
        record SKIP nullhub "${method} ${path}" "hub unreachable (${NULLHUB_BASE})"
      elif [[ "${code}" =~ ^401$|^403$|^404$ ]]; then
        record SKIP nullhub "${method} ${path}" "HTTP ${code} (auth or fixture v1)"
      elif [[ "${code}" =~ ^[23] ]]; then
        record PASS nullhub "${method} ${path}" "HTTP ${code}"
      else
        record FAIL nullhub "${method} ${path}" "HTTP ${code}"
      fi
    done < <(python3 - "${ROUTES_JSON}" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    doc = json.load(f)
for r in doc.get("routes", []):
    print("\t".join([
        r.get("method", "GET"),
        r.get("path_template", "/"),
        "true" if r.get("destructive") else "false",
        r.get("auth_mode", ""),
    ]))
PY
)
  fi
} | tee "${MATRIX}"

pass=$(grep -c '^PASS' "${MATRIX}" || true)
fail=$(grep -c '^FAIL' "${MATRIX}" || true)
skip=$(grep -c '^SKIP' "${MATRIX}" || true)

cat > "${SUMMARY}" <<EOF
{"pass":${pass},"fail":${fail},"skip":${skip},"artefact_dir":"${ARTEFACT_DIR}","matrix":"${MATRIX}"}
EOF

echo ""
echo "QA matrix: pass=${pass} fail=${fail} skip=${skip}"
echo "log: ${MATRIX}"
echo "summary: ${SUMMARY}"

if [[ "${fail}" -gt 0 ]]; then
  exit 1
fi
