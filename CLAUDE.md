# LightWave CLI Rules

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

### `lw cdn reconcile` Cheat Sheet
- `lw cdn reconcile --dry-run` — show legacy prefixes vs SST allowlist, exit
- `lw cdn reconcile` — interactive: drift table, then `[y/N]` prompt
- `lw cdn reconcile --yes` — skip confirmation (CI/agent)

Allowlist source: `packages/lightwave-core/lightwave/schema/definitions/data/assets/assets.yaml` (`cdn.paths`).
Bucket name source: `cdn.{infrastructure_domain}` from `data/models/domains.yaml`.
