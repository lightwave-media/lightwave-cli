#!/usr/bin/env bash
# Smoke tests for the lw check discipline pre-commit hooks.
# Exits 0 if all tests pass, 1 on any failure.
#
# Tests use a temp-dir git repo so they don't disturb the real working tree.
# Each script is invoked as the actual pre-commit harness would — git
# computes diff --cached against the temp HEAD; the script reads staged files
# from $@ and the diff.

set -euo pipefail

# When invoked from a git hook (the pre-push smoke test), git exports
# GIT_DIR/GIT_INDEX_FILE/etc. pointing at the REAL repo. Inherited, those
# override the per-test `git init` below, so `git add`/`git commit` stage into
# the real index (leaving pollution) instead of each temp repo. Unset them so
# every temp repo is genuinely hermetic, whether run standalone or under a hook.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY \
  GIT_COMMON_DIR GIT_PREFIX GIT_CONFIG_PARAMETERS 2>/dev/null || true

# Resolve paths relative to this script — works whether invoked from repo root
# or from within scripts/precommit/.
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
companion_hook="$script_dir/check-companion-test.sh"
brain_hook="$script_dir/brain-memory-link.sh"

if [[ ! -x "$companion_hook" || ! -x "$brain_hook" ]]; then
  echo "FAIL: hook scripts not executable" >&2
  exit 1
fi

pass_count=0
fail_count=0

# ---------------------------------------------------------------------------
# Test harness: each test runs in a fresh temp git repo, returns exit status.
# ---------------------------------------------------------------------------

run_test() {
  local name="$1"
  shift
  local expected_exit="$1"
  shift
  # Remaining args are passed to the hook command via the test body.

  local tmpdir
  tmpdir=$(mktemp -d)
  trap "rm -rf $tmpdir" RETURN

  pushd "$tmpdir" >/dev/null
  git init --quiet
  git config user.email "test@example.com"
  git config user.name "Test"
  git config commit.gpgsign false
  mkdir -p internal/cli

  # Test body provided by caller via env var; eval'd here.
  set +e
  eval "$TEST_BODY"
  local actual=$?
  set -e

  popd >/dev/null

  if [[ $actual -eq $expected_exit ]]; then
    echo "PASS: $name"
    pass_count=$((pass_count + 1))
  else
    echo "FAIL: $name (expected exit $expected_exit, got $actual)"
    fail_count=$((fail_count + 1))
  fi
}

# ---------------------------------------------------------------------------
# check-companion-test.sh
# ---------------------------------------------------------------------------

TEST_BODY='
echo "package cli" > internal/cli/check_foo.go
git add internal/cli/check_foo.go
"'"$companion_hook"'" internal/cli/check_foo.go
'
run_test "check-companion-test: missing _test.go fails" 1

TEST_BODY='
echo "package cli" > internal/cli/check_foo.go
echo "package cli" > internal/cli/check_foo_test.go
git add internal/cli/check_foo.go internal/cli/check_foo_test.go
"'"$companion_hook"'" internal/cli/check_foo.go internal/cli/check_foo_test.go
'
run_test "check-companion-test: with companion passes" 0

TEST_BODY='
echo "package cli" > internal/cli/check_handlers.go
git add internal/cli/check_handlers.go
"'"$companion_hook"'" internal/cli/check_handlers.go
'
run_test "check-companion-test: umbrella check_handlers.go is skipped" 0

TEST_BODY='
echo "package cli" > internal/cli/other_file.go
git add internal/cli/other_file.go
"'"$companion_hook"'" internal/cli/other_file.go
'
run_test "check-companion-test: non-check files ignored" 0

# ---------------------------------------------------------------------------
# brain-memory-link.sh — uses a fake brain root via env override
# ---------------------------------------------------------------------------

TEST_BODY='
fake_home=$(mktemp -d)
mkdir -p "$fake_home/.brain/memory/failures"
echo "stub" > "$fake_home/.brain/memory/failures/2026-05-22-test.yaml"
HOME="$fake_home" \
  bash -c "
    echo \"package cli\" > internal/cli/check_bar.go
    echo \"// linked-incident: failures/2026-05-22-test.yaml\" >> internal/cli/check_bar.go
    git add internal/cli/check_bar.go
    \"$brain_hook\" internal/cli/check_bar.go
  "
'
run_test "brain-memory-link: linked-incident referencing existing yaml passes" 0

TEST_BODY='
fake_home=$(mktemp -d)
mkdir -p "$fake_home/.brain/memory/failures"
HOME="$fake_home" \
  bash -c "
    echo \"package cli\" > internal/cli/check_baz.go
    git add internal/cli/check_baz.go
    \"$brain_hook\" internal/cli/check_baz.go
  "
'
run_test "brain-memory-link: missing comment fails" 1

TEST_BODY='
fake_home=$(mktemp -d)
mkdir -p "$fake_home/.brain/memory/failures"
HOME="$fake_home" \
  bash -c "
    echo \"package cli\" > internal/cli/check_qux.go
    echo \"// linked-incident: failures/does-not-exist.yaml\" >> internal/cli/check_qux.go
    git add internal/cli/check_qux.go
    \"$brain_hook\" internal/cli/check_qux.go
  "
'
run_test "brain-memory-link: stale link to missing file fails" 1

TEST_BODY='
echo "package cli" > internal/cli/check_handlers.go
echo "// no linked-incident here" >> internal/cli/check_handlers.go
git add internal/cli/check_handlers.go
"'"$brain_hook"'" internal/cli/check_handlers.go
'
run_test "brain-memory-link: umbrella check_handlers.go is skipped" 0

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

echo ""
echo "Tests passed: $pass_count"
echo "Tests failed: $fail_count"

if [[ $fail_count -gt 0 ]]; then
  exit 1
fi
exit 0
