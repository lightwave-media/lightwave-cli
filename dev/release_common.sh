#!/usr/bin/env bash
# Shared helpers for release prepare/ship (ADR-0035 delivery conveyor).
set -euo pipefail

release_repo_root() {
  cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd
}

release_git_toplevel() {
  git -C "$(release_repo_root)" rev-parse --show-toplevel
}

release_default_branch() {
  git -C "$(release_git_toplevel)" symbolic-ref --quiet refs/remotes/origin/HEAD 2>/dev/null \
    | sed 's@^refs/remotes/origin/@@' || echo "main"
}

release_gh_repo() {
  local remote url owner name
  remote="$(git -C "$(release_git_toplevel)" remote get-url origin 2>/dev/null || true)"
  url="${remote}"
  url="${url#git@github.com:}"
  url="${url#https://github.com/}"
  url="${url%.git}"
  owner="${url%%/*}"
  name="${url##*/}"
  if [[ -z "${owner}" || -z "${name}" || "${owner}" == "${url}" ]]; then
    echo ""
    return 1
  fi
  printf '%s/%s' "${owner}" "${name}"
}

release_validate_commit_msg() {
  local msg_file="$1"
  local bun cli
  bun="$(command -v bun 2>/dev/null || true)"
  [[ -z "${bun}" && -x "${HOME}/.bun/bin/bun" ]] && bun="${HOME}/.bun/bin/bun"
  cli="${HOME}/.lightwave/lib/commit/cli.ts"
  if [[ -n "${bun}" && -f "${cli}" ]]; then
    "${bun}" "${cli}" validate-file "${msg_file}"
    return
  fi
  echo "warn: commit validator unavailable — skipping pre-check" >&2
}

release_format_commit_file() {
  # Writes a hook-valid Conventional Commit to $1.
  local out="$1"
  local type="${LW_RELEASE_COMMIT_TYPE:-feat}"
  local scope="${LW_RELEASE_COMMIT_SCOPE:-release}"
  local subject="${LW_RELEASE_COMMIT_SUBJECT:-adr-0035 delivery conveyor}"
  local body="${LW_RELEASE_COMMIT_BODY:-Automated lw release prepare/ship delivery path.}"
  local closes="${LW_RELEASE_CLOSES:-}"

  {
    printf '%s(%s): %s\n' "${type}" "${scope}" "${subject}"
    echo
    fold -s -w 72 <<< "${body}" | sed '/^$/d'
    if [[ -n "${closes}" ]]; then
      echo
      printf 'Closes #%s\n' "${closes}"
    fi
  } > "${out}"
}

release_stage_release_files() {
  local root
  root="$(release_repo_root)"
  cd "${root}"

  if [[ -f dev/release_qa_pass.sh ]]; then
  git add dev/release_qa_pass.sh dev/release_prepare.sh dev/release_ship.sh dev/release_common.sh 2>/dev/null || true
  git add dev/release-cycle/ 2>/dev/null || true
    git add internal/cli/release_handlers.go internal/cli/release_handlers_test.go 2>/dev/null || true
    git add internal/cli/dispatcher_dev_domains_test.go internal/release/ internal/sst/cli_loader.go 2>/dev/null || true
    git add internal/cli/dispatcher.go .github/workflows/release-auto-merge.yml mise.toml go.mod go.sum 2>/dev/null || true
  fi

  if [[ -f src/schemas/interfaces/cli/release_domain.yaml ]]; then
    git add src/schemas bindings/go/schemas spec/adr/0035*.md 2>/dev/null || true
    git add src/boilerplate/blueprints/lightwave-home/config/flags/ 2>/dev/null || true
    git add src/boilerplate/templates/release/ 2>/dev/null || true
    git add dev/release_*.sh src/schemas/workflows/sops/lw_dev_session.yaml 2>/dev/null || true
  fi

  # Never stage local build artefacts.
  git reset HEAD -- bin/ lw 2>/dev/null || true
}

release_export_dev_env() {
  export LW_CLI_DEV_DOMAINS=1
  export LW_QA_ARTEFACT_DIR="${LW_QA_ARTEFACT_DIR:-/tmp/qa-release-pass}"
  if [[ -d "${HOME}/dev/lightwave-core/src/boilerplate/blueprints/lightwave-home/config/flags" ]]; then
    export LW_FLAGS_STAMP="${HOME}/dev/lightwave-core/src/boilerplate/blueprints/lightwave-home/config/flags/registry.yaml"
  fi
}
