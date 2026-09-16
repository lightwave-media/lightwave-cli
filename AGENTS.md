# LightWave CLI Rules

### Schema-Driven CLI

`lw`'s command surface is declared in `lightwave-core`'s
`src/schemas/interfaces/cli/commands.yaml` (resolved as
`<lightwave_root>/lightwave-core/src/schemas/interfaces/cli/commands.yaml`,
with `lightwave_root` defaulting to `~/dev` in the flat sibling-repo
layout) and dispatched at startup via `internal/cli/dispatcher.go`.
Two invariants are checked by `lw check schema`:

- **No schema entry without a registered Go handler** — adding a `name: foo` line under a domain in `commands.yaml` without also calling `RegisterHandler("<domain>.foo", …)` from `init()` leaves the subcommand wired to `lw <domain> foo --help` but unimplemented.
- **No registered handler without a schema entry** — registering a handler the schema doesn't know about means the command is unreachable from the dispatcher's cobra tree.

**The schema-drift gate is ARMED (since 2026-08-27, #301).** `ci.yml` calls
`schema-drift-check.yml` on every PR, and `schema-drift` is one of the jobs the
`CI Required Gate` aggregates — so drift blocks the merge. Run it yourself
before pushing any change that adds, renames, or removes a handler or a
`commands.yaml` entry:

```bash
LW_CHECK_SCHEMA_STRICT=1 ./bin/lw check schema
```

Exit 1 on drift; without the env var the same report prints and exits 0.

**Known blind spot (#337):** the gate counts the handler REGISTRY, so commands
wired through the legacy cobra tree (`someCmd.AddCommand(...)`) are invisible to
it. Every such command currently sits in a domain marked `_status:
in_development`, which the check excludes, so the count reads clean. That
masking is incidental — flipping one of those domains active will make the gate
report a missing handler for a command that works. Read #337 first.

### Test Conventions

Every new test file uses these defaults (enforced informally — golangci-lint v2's `paralleltest`, `tparallel`, `thelper`, `testpackage`, and `usetesting` linters catch most drift):

- **Top-level `t.Parallel()`** on every test function (and `tt := testCase; tt.t.Parallel()` inside table-driven subtests).
- **`stretchr/testify/require`** for setup-fatal assertions (`require.NoError(t, err, "context")`); **`stretchr/testify/assert`** for content checks that should report-all-failures rather than fast-fail.
- **External test package** — `package foo_test` not `package foo`, when feasible. Forces tests against the exported API.
- **`internal/testutil`** for shared helpers:
  - `testutil.NewPool(t)` — opens a Postgres pool from `LW_TEST_DB_URL`; calls `t.Skip` when the env var is unset, so the suite stays portable across machines without a DB fixture.
  - `testutil.MakeEpic(t, pool, opts...)` / `MakeSprint(t, pool, epic, opts...)` / `MakeStory(t, pool, epic, opts...)` — create rows with sensible defaults; each registers a `t.Cleanup` that deletes the row on test exit.
  - `testutil.RunHandler(t, key, args, flags) (stdout string, err error)` — invokes a dispatcher-registered handler with stdout capture, for table-driven CLI tests that don't need the cobra wrapper.
- **`t.TempDir()`** for any filesystem fixture — auto-cleaned, no `os.RemoveAll` ceremony.
- **`t.Context()`** (Go 1.24+) in tests that need a context — preferred over `context.Background()` so tests cancel cleanly when the test times out.

Existing tests predating this convention are not auto-migrated; touch-as-you-go.

Shape claims about SST-shaped data (`stamp_bound` + `print_census` in
lightwave-core `test_patterns`) load the stamp or generated types and walk
every current print. Do not copy `commands.yaml` into a handwritten list in
the test; `lw check schema` is that census. Fixtures stay legal for
handler behaviour.

### Git Discipline (READ FIRST)

Before any commit, branch op, stash, cherry-pick, rebase, merge, or worktree, **load the `lightwave-git` skill** and follow it. The defaults are non-negotiable; deviating produces messes that cost full days to clean up. Critical rules at a glance:

- Branch off `origin/main` after `git pull --ff-only`. One concern per branch.
- Stash is for <30 minutes. Drop `claude-session-start:*` auto-saves on sight.
- Worktrees do NOT work for cross-repo Makefile invocations.
- Pre-commit hooks may modify staged files; always re-stage and retry, never `--no-verify`.
- Generated files and lock files belong in `.prettierignore`. Don't commit prettier-reformatted output.
- Cherry-picking 7+ day-old work usually conflicts with current main. Don't grind — re-implement against current main instead.

Full reference: `~/.claude/skills/lightwave-git/SKILL.md` (loaded globally in every Claude Code session).

### Updating `lw` — Build From Source, Not `go install`

```sh
mise run install
```

**This is how a CLI change reaches this machine.** It builds from source and writes `~/.local/bin/lw` in about three seconds. No tag, no CI run, no GoReleaser, no tap, no `brew upgrade`.

That chain used to be mandatory here, and it was the wrong shape for this tool. `lw` is the command spine of the local shell — the binary and the source sit on the same disk. Routing a three-second build through a release pipeline and a package manager was ceremony that made local iteration cost a tagged release. The release train still exists, but it serves *other* machines, not this one.

**Why `~/.local/bin` specifically.** It precedes `/opt/homebrew/bin` on PATH. That is the whole trick: the build you just made is the `lw` your shell and every project hook resolve, with nothing to uninstall or fight. The task asserts this after installing and warns if anything still shadows it.

`lw version` reports `git describe` (`3.14.0-5-gf91df3c-dirty`), so a source build never masquerades as a clean tagged release. Override the destination with `LW_INSTALL_DIR`.

**Still never `go install ./cmd/lw`.** It writes `~/go/bin/lw`, which sits *behind* `/opt/homebrew/bin` on PATH — so a leftover Homebrew `lw` wins and you believe your change is live when it isn't. Project hooks (bash-guard, pre-push gates) shell out to the PATH-resolved `lw`, so a stale binary silently runs old code against your edits. `mise run install` exists to make that class of mistake impossible.

**Never hand-copy a binary into `/opt/homebrew/bin/lw`.** Even an MD5-identical binary placed there can stall on first launch — macOS `syspolicyd` runs a reputation check on adhoc-signed binaries in trusted prefixes — which hangs every project hook that invokes `lw` until the check completes. `~/.local/bin` is not such a prefix, which is another reason the install task targets it.

To publish a release for other machines: commit to `main`, then `git tag vX.Y.Z && git push origin vX.Y.Z`. GoReleaser builds the cross-platform tarballs and attaches them to the GitHub Release. There is no longer a Homebrew formula step — `lightwave-media/homebrew-tap` was deleted on 2026-09-16.

### Destructive Commands: `--dry-run` + `--yes` Standard
Every new destructive `lw` subcommand ships with a `--dry-run` flag (preview only, no side effects) and a `--yes` flag (skip the interactive confirmation prompt for CI/agent use). Default behavior with no flags is interactive: print what will change, prompt, then act on `y`. Established pattern in `db cleanup`, `drift reconcile`, `github sync`, `orchestrator`, `cdn reconcile`.

### SST is the Source of Truth, the CLI Mediates
Vendor-facing destructive operations (S3, ECS, RDS, etc.) belong behind an `lw` subcommand that reads structure from SST YAML — agents do not get raw vendor CLI access. The Claude Code global deny on `aws s3 rm` is intentional. To clean up a bucket: extend `lw cdn` against `assets.yaml` (or the relevant SST file), don't ask for the deny to be loosened.

### `lw check` Subcommand Requirements

Every new `lw check <name>` subcommand exists to catch ONE concrete anti-pattern that has bitten us in production. Speculative or aesthetic checks do not ship.

**Required for every new check subcommand:**

1. **Linked incident** — point to a brain memory entry (`~/.brain/memory/failures/*.yaml` or `feedback/*.yaml`) that describes the bug it prevents. If you can't link one, the check isn't justified yet.
2. **Bad-input example in `--help`** — the long description must include a code snippet of the anti-pattern. Example: `lw check theme --help` shows `useLayoutEffect(() => { document.documentElement.classList.remove('dark-mode') })` and explains why it's wrong.
3. **Scoped, fast** — operates on staged + changed files by default, full repo only with `--all`. Target <2s on a typical changeset; pre-commit budget is tight.
4. **Exit codes** — `0` clean, `1` violations found, `2` tool error (config missing, deps broken). Never exit `0` on warnings; if it's a warning, it's not a check.
5. **`--fix` flag if mechanical** — if the violation has a deterministic fix (delete a line, rename a symbol), provide `--fix`. If it requires judgment (rewrite a CSS token), document it in the violation message and exit non-zero.
6. **One file per check** — implementation lives in `internal/cli/check_<name>.go`. No catch-all check files.
7. **Wired into `lw check`** — must run as part of the default `lw check` (the umbrella). Subcommands that only run on demand are dead.
8. **Test fixture** — `internal/cli/check_<name>_test.go` with one fixture proving the check fires on a known-bad input and one proving it stays silent on a known-good input.

**Don't ship:**
- Style preferences with no incident behind them
- Checks that duplicate ruff/eslint/tsc — extend the existing tool's config instead
- Checks that scan files outside the monorepo
- Checks that hit the network or read AWS — those go under `lw drift` or `lw aws`, not `lw check`

### Push Circuit Breaker

After 3 consecutive CI/pre-commit failures on the same branch, the stop hook blocks further progress and requires escalation.

**State file:** `~/.local/state/lightwave/push-circuit-breaker.json`
- Keyed by branch name
- Fields: `consecutiveFailures` (int), `lastError` (string), `lastAttempt` (ISO8601)

**Rules:**
- If `consecutiveFailures >= 3` on the current branch: do NOT push or attempt further commits — escalate to your manager with the repeating error
- The counter increments on each pre-commit failure; resets to 0 on success
- To manually unblock after manager guidance: delete the branch entry from the state file

### `lw cdn reconcile` Cheat Sheet
- `lw cdn reconcile --dry-run` — show legacy prefixes vs SST allowlist, exit
- `lw cdn reconcile` — interactive: drift table, then `[y/N]` prompt
- `lw cdn reconcile --yes` — skip confirmation (CI/agent)

Allowlist source: legacy-core `assets.yaml` (`cdn.paths`) — not yet re-stamped
into the rebuilt `lightwave-core/src/schemas/`; `cdn` stays decommissioned
until that schema family returns.

## Definition of done

**Done =** `mise run ci` green + a test that pins the change.
