# LightWave CLI Rules

### Git Discipline (READ FIRST)

Before any commit, branch op, stash, cherry-pick, rebase, merge, or worktree, **load the `lightwave-git` skill** and follow it. The defaults are non-negotiable; deviating produces messes that cost full days to clean up. Critical rules at a glance:

- Branch off `origin/main` after `git pull --ff-only`. One concern per branch.
- Stash is for <30 minutes. Drop `claude-session-start:*` auto-saves on sight.
- Worktrees do NOT work for cross-repo Makefile invocations.
- Pre-commit hooks may modify staged files; always re-stage and retry, never `--no-verify`.
- Generated files and lock files belong in `.prettierignore`. Don't commit prettier-reformatted output.
- Cherry-picking 7+ day-old work usually conflicts with current main. Don't grind — re-implement against current main instead.

Full reference: `.claude/skills/lightwave-git/SKILL.md`.

### Updating `lw` — Ship Via Tap, Not `go install`

`lw` is distributed via Homebrew tap (`lightwave-media/homebrew-tap`). Every machine running `lw` got it from `brew install lightwave-media/tap/lw`, which pins to a tagged release built by GoReleaser. **Source changes here don't reach any shell until a new tag ships.**

Never `go install ./cmd/lw` to "use the new version locally." It produces `~/go/bin/lw`, which PATH order shadows behind `/opt/homebrew/bin/lw` (the tap binary). You'll think your change is live; it isn't. Worse, project hooks (bash-guard, pre-push gates) shell out to the PATH-resolved `lw`, so a stale Homebrew binary silently runs old code while your edits sit unused.

To ship a CLI change:

1. Commit + push to `lightwave-cli` main.
2. Tag a new version: `git tag vX.Y.Z && git push origin vX.Y.Z`.
3. GoReleaser CI (`.github/workflows/release.yml`) builds binaries, creates the GitHub release, and pushes the formula update to `homebrew-tap`.
4. `brew upgrade lw` on each machine.

Never overwrite `/opt/homebrew/bin/lw` by hand with a fresh `go install` build. Even an MD5-identical binary placed there can stall on first launch (macOS `syspolicyd` reputation check on adhoc-signed binaries in trusted prefixes), which hangs every project hook that invokes `lw` until the check completes. If you need the new code in a hook before the tap ships, pin the hook entry to an absolute path of a known-fast build location (e.g. `~/go/bin/lw`) — and remove the pin once the tap update lands.

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

Allowlist source: `packages/lightwave-core/lightwave/schema/definitions/data/assets/assets.yaml` (`cdn.paths`).
Bucket name source: `cdn.{infrastructure_domain}` from `data/models/domains.yaml`.
