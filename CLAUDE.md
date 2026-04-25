# LightWave CLI Rules

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
