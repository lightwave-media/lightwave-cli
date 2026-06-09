# CLI Command-Surface Audit → keep/rebuild/drop matrix

## Context

`lightwave-cli` (`lw`) has grown two parallel command surfaces and we want to
**keep the chassis, rebuild the command surface in place, and add `lw media`**.
This task is **Phase 1: a read-only audit** that produces a keep/rebuild/drop
matrix sized by effort, flags `media` as the first new domain, and recommends a
rebuild sequence. **No code changes** — the only deliverable is an audit
markdown doc. Phase 2 (rebuild) waits for approval.

### What the audit found about the architecture

There are **two** command surfaces in `internal/cli/`:

1. **Schema-dispatched domains** — declared in lightwave-core's
   `config/cli/commands.yaml` (the "stamp"), dispatched by
   `dispatcher.go:BuildDispatched`, handlers registered via
   `RegisterHandler("<domain>.<name>", …)` in `*_handlers.go` `init()`.
   `lw check schema` gates drift between the two
   (`LW_CHECK_SCHEMA_STRICT=1` in CI). These already conform to the current
   model → mostly **keep / light rebuild**.

2. **Legacy hardcoded cobra trees** — attached directly in
   `root.go init()` (`rootCmd.AddCommand(...)`), **not** schema-driven, not
   covered by the drift gate. These are the prime **rebuild / drop / migrate**
   candidates. The migration pattern is established: move the tree into
   `commands.yaml` + a `*_handlers.go`, remove the hardcoded `AddCommand`.

Key facts that shape verdicts:
- `commands.yaml` lives in the **sibling lightwave-core repo**
  (`paths.lightwave_root` defaults to `~/dev/lightwave-media`, **absent on this
  machine**). Several domains (`check`, `hooks`, `agent`, `memory`, `msg`,
  `v_core`) are **orphaned** — handler exists, schema entry pending (tracked in
  `docs/pending-schema-additions.yaml`). Migrating a legacy command requires a
  paired lightwave-core PR.
- `cdn.go` is the **reference template for `media`**: native S3 via
  `internal/aws`, reads SST `assets.yaml` (`cdn.paths`) + `domains.yaml`,
  ships `--dry-run` + `--yes`. `ecr.go` is a second `internal/aws` consumer.
- Many legacy commands are **thin `make` wrappers** (`make`, `test`, `setup`,
  `email`, `content`, `drift`, `cdn push/pull`) — they shell to monorepo
  Makefile targets via `resolveMakeDir` + `runMake`.

## Deliverable

A new doc at **`docs/cli-command-surface-audit.md`** containing the matrix
below verbatim, plus the `media` proposal and rebuild sequence. Committed on
branch `chore/cli-command-surface-audit` (per lightwave-git skill). Zero code
changes (`git diff --stat` shows only the doc).

Effort key: **S** ≤ ½ day · **M** ~1–2 days · **L** ~3+ days.

---

## Matrix A — Schema-dispatched domains (already conform)

| Domain.cmds | Current state | Fits today? | Effort | Verdict | Rationale |
|---|---|---|---|---|---|
| **check** (ci, ruff, types, domains, schema, locks, deps, git, aws, docker, ecs, smoke, compose) | Native Go gates; some shell to `make`. Orphaned in schema (pending). | Yes — core to DoD | M | **keep** | The gate surface AGENTS.md mandates. Land the pending schema entries; verify each obeys the `lw check` 8-rule contract. |
| **task** (list, info, create, start, status, pr, done) | DB-backed + paperclip/github fan-out; `done` mid-rewrite (see pending doc). | Mostly | M | **rebuild** | Central workflow; predates current conventions. Finish `done` rewrite + `--skip-paperclip/--skip-github`; pin tests. |
| **sprint** (list, current, tasks) | DB readers | Yes | S | **keep** | Conform; touch-as-you-go tests. |
| **story** (list, show, link) | DB readers | Yes | S | **keep** | Same. |
| **epic** (list, info, tasks) | DB readers | Yes | S | **keep** | Same. |
| **db** (shell, dump, restore, reset, migrate, makemigrations, check, schema-init/list/drop, migrate-schemas) | Postgres ops; chassis-adjacent | Yes | S | **keep** | Pairs with the Repository/Postgres chassis. Confirm destructive ones honor `--dry-run/--yes`. |
| **infra** (list, plan, apply, validate, output, run-all, status) | Terragrunt runner (`internal/infra`) | Yes | S | **keep** | Chassis. |
| **deploy** (run, status, logs, rollback) | Deploy orchestration | Likely | M | **rebuild** | Verify against current ECS/release flow; overlaps `aws ecs`. Pin tests. |
| **plan** (sync, generate) | Plan artifact ops | Unclear | M | **review→rebuild** | Confirm still used vs markdown task flow; rebuild or drop. |
| **schema** (validate, drift, reconcile, generate, coverage) | SST schema tooling | Yes | S | **keep** | Core SST surface. Note `drift`/`reconcile` overlap legacy `drift` cmd (consolidate there). |
| **spec** (validate, show, generate-tasks, coverage, history) | Schema-dispatched **plus** a legacy `specCmd` parked in `legacyHardcodedDomains()` | Partially | M | **rebuild** | Two `spec` trees coexist. Merge legacy `spec generate <task-id>` semantics, drop the hardcoded parking. |
| **context** (init, refresh, show) | **Stubbed** | No | S | **drop** | Remove stubs from schema + handlers unless a concrete consumer exists. |
| **scaffold** (app, model, api, test) | **Stubbed** | No | S | **drop** | Same — `lw codegen`/templates cover real needs. |
| **compose** (generate, verify) | docker-compose ↔ SST | Yes | S | **keep** | `check compose` delegates here. |
| **hooks** (install, doctor, sync, circuit-breaker.check) | Native; orphaned in schema (pending) | Yes | S | **keep** | Enforces the push-circuit-breaker + pre-commit discipline. Land schema entries. |
| **local** (up, down, logs, health, restart, preflight, setup, clean, exec, install-frontend) | Dev compose lifecycle | Yes | S | **keep** | Replaced legacy `dev`. `install-frontend` already `--force/--yes`. |

## Matrix B — Legacy hardcoded cobra (root.go) — prime rebuild/drop/migrate

| Command (subcmds) | Current state | Fits today? | Effort | Verdict | Rationale |
|---|---|---|---|---|---|
| **config** (show, set, get) | Native, in `root.go` | Yes | S | **keep** | Essential; fine as a root utility (not a schema domain). |
| **version** (`--json`, API map) | Native | Yes | — | **keep** | Release/versioning chassis. |
| **make** (`<scope> [target]`) | Thin Makefile dispatcher | Yes | — | **keep** | Heavily used escape hatch; leave as standalone utility. |
| **aws** → ecs (status, deploy, apply-task-def, tasks), logs (show, tail, list), ecr (push) | Native via `internal/aws` (chassis) | Yes | M | **rebuild→migrate** | Solid chassis code; migrate the tree into `commands.yaml`+handlers so it's drift-gated. `ecr push` is the emergency-deploy path. |
| **cdn** (push, pull media, push media, reconcile) | `reconcile` native+SST+`--dry-run/--yes`; push/pull shell to `make` | Yes | M | **keep + rebuild** | Reference for `media`. Keep `reconcile`; rebuild push/pull natively (or leave as make wrappers) and schema-register. |
| **worktree** (create, list, status, gc, prune) | Native git (`internal/git`); v_core/adapter consumer | Yes | S | **keep→migrate** | Live infra for sealed sessions. Schema-register; `prune` already `--dry-run`. |
| **agent** (spawn, list, status, stop, provision) | Native; sealed sub-session lifecycle; `provision` is a STUB | Yes | M | **keep→migrate** | EB-001 v_core core. Land schema entry; implement/keep `provision` stub flagged. |
| **v_core** (start, stop, status, logs) | Native daemon supervisor (`internal/vcore`) | Yes | S | **keep→migrate** | Orchestrator lifecycle. Schema-register. |
| **memory** (put, get, list, delete) | Native fs KV (`internal/memory`) | Yes | S | **keep→migrate** | v_core persisted state. Schema-register. |
| **msg** (send) | Native HTTP→gateway (dormant until gateway boots) | Yes | S | **keep→migrate** | v_core outbound messaging. Schema-register; `--dry-run` present. |
| **council** (start, status, result, history, cancel, config) | Native HTTP→Augusta (localhost:9700); persona-gated | If Augusta runs | M | **review→keep/drop** | Keep only if Augusta is a live dependency; otherwise quarantine. Confirm with Joel. |
| **sst** (coverage) | Native; walks `~/.brain` YAML lifecycle status | Yes | S | **keep→migrate** | Distinct from `lw schema`. Schema-register under a clear name. |
| **audit** (run, report, diff, list) | Large native scanner (security/gates/quality/drift) | Partially | M | **review→rebuild** | Overlaps `lw check` + `lw drift`. Decide: fold gateable checks into `lw check`, keep `audit` as the scored-report layer, or drop. |
| **health** | Native dependency/connectivity probe | Yes | S | **keep→migrate** | Useful triage. Schema-register. |
| **codegen** (journeys) | Native Playwright generator from journey YAML | Yes | S | **keep→migrate** | Real generator (unlike stubbed `scaffold`). Schema-register; models/api/types are doc'd-but-unimplemented — drop those claims. |
| **content** (apply, diff, promote) | `make dj-manage` wrappers; `--dry-run` present | Yes | M | **keep→rebuild** | CMS migration flow still in use. Schema-register; keep make delegation or thin native wrapper. |
| **drift** (report, reconcile) | `make dj-manage` wrappers | Overlaps | M | **consolidate/drop** | Duplicates `lw schema drift/reconcile`. Pick one home (schema) and drop the standalone. |
| **email** (send) | `make dj-manage` wrapper; `--dry-run` present | Niche | S | **rebuild/keep** | Low priority. Keep as thin wrapper + schema-register, or drop if unused. |
| **test** (smoke, infra) | `make` wrappers | Overlaps | S | **drop/fold** | Redundant with `lw make <scope> test` and `lw check ci`. Fold and drop. |
| **setup** (sync, venv, lock) | `make` wrappers (root scope) | Yes | S | **fold into local** | Dev-env setup; belongs under `lw local`. Migrate + drop standalone. |
| **spec** (legacy `specCmd`) | Parked in `legacyHardcodedDomains()` | No | — | **drop** | Covered by Matrix A `spec` rebuild — remove the parking. |
| **browser** (open, tabs, screenshot, click, type, navigate, execute, start) | macOS `osascript`/JXA + CDP automation | No | S | **drop/quarantine** | Superseded by the chrome-devtools MCP surface; macOS-only, brittle. Remove unless a hook depends on it. |

### Cross-cutting observations
- **Drift-gate blind spot:** ~23 legacy trees bypass `lw check schema`. The
  biggest single win is migrating the *keepers* into the schema so the gate
  covers the whole surface.
- **`make`-wrapper cluster** (`test`, `setup`, `drift`, `email`, `content`,
  `cdn push/pull`) are cheap to rebuild/fold; decide per-command whether the
  Makefile indirection still earns its place.
- **v_core constellation** (`agent`, `v_core`, `memory`, `msg`, plus
  `worktree`) is coherent and current — keep as a group, migrate together.
- **Overlap clusters to resolve:** `audit`↔`check`↔`drift`; `drift`↔`schema`;
  `deploy`↔`aws ecs`; `test`↔`make`/`check`; legacy `spec`↔schema `spec`.

---

## `media` — first new domain (proposal)

Schema-driven like every other domain: a `media:` block in `commands.yaml`
→ handlers in `internal/cli/media_handlers.go` → new `internal/media` package.
Reuse `internal/aws` (S3, per `cdn.go`/`ecr.go`) and **complement**
`lw cdn`/`assets.yaml` — no parallel system. Stamp a `media-asset` (+ manifest)
schema in lightwave-core first (per global rule #7).

| Subcommand | Tier | Behaviour |
|---|---|---|
| `media inventory` | READ-ONLY | Walk roots → catalog path/size/type/dims/content-hash → manifest. Never moves files. |
| `media dedup` | READ-ONLY | Report duplicate content-hashes from the manifest. |
| `media classify` | READ-ONLY | Propose scope/collection/role from heuristics; no writes. |
| `media ingest` | WRITE (gated) | Optimize + derive renditions, measure aspect, place in canonical `{scope}/{collection}/{slug}/{role}-{variant}`. `--dry-run`+`--yes`. |
| `media sync` | WRITE (gated) | Push derivatives to R2 via `internal/aws` S3. `--dry-run`+`--yes`. |

**Guardrails:** masters vs derivatives are distinct tiers; **masters are never
served or committed**. `inventory`/`dedup`/`classify` are strictly read-only —
never move/delete operator media without explicit approval. Roots: `~/Pictures`,
`~/Documents`, `~/Downloads`, Google-Drive synced path, `/Volumes/*` reels,
2 NAS when mounted.

---

## Recommended rebuild sequence (Phase 2, after approval)

1. **Land pending schema + drift-gate the keepers** — push
   `docs/pending-schema-additions.yaml` entries (`check`, `hooks`, local/task
   additions) into lightwave-core; bring `lw check schema` green with the full
   keeper set. *(unblocks everything; biggest correctness win)*
2. **Drop the dead weight** — `context`, `scaffold` stubs, legacy `spec`
   parking, `browser`, `test`. Remove schema entries + handlers together.
3. **Resolve overlaps** — consolidate `drift`→`schema`; decide
   `audit` vs `check`; `deploy` vs `aws ecs`; fold `setup`→`local`.
4. **Migrate the chassis-backed keepers into schema** — `aws`, `worktree`,
   `health`, `codegen`, `sst`, then the v_core group (`agent`, `v_core`,
   `memory`, `msg`). Mechanical, low-risk.
5. **Rebuild the workflow cores** — `task` (finish `done`), `spec`, `deploy`,
   `content`. Each: confirm schema entry → register handler (gate green) →
   rewrite fresh → pin test → `mise run ci` green.
6. **Build `lw media`** — stamp the schema in lightwave-core; implement
   `inventory` first (read-only, proves the manifest + roots), then `dedup`,
   `classify`, then the gated `ingest`/`sync` reusing `internal/aws`.

Per-command DoD throughout (AGENTS.md): `mise run ci` green + a pinning test;
destructive subcommands ship `--dry-run` + `--yes`.

## Verification (this task)
- `git diff --stat` shows only `docs/cli-command-surface-audit.md` added (DOD-4).
- Matrix covers every `commands.yaml` domain + every `root.go` legacy tree,
  cross-checked against the `RegisterHandler` set (DOD-1).
- Each row carries state · fits-today · effort · verdict · rationale (DOD-2).
- `media` flagged with subcommands + sequence (DOD-3).
- No `mise run ci` needed (no code change); audit is the artifact.
