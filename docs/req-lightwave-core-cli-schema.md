# REQ → lightwave-core: stand up the CLI command-surface schema (the `lw` stamp)

**Requesting repo:** `lightwave-cli`
**Target repo:** `lightwave-media/lightwave-core`
**Status:** draft / for review
**Blocks:** Phase 2 of the CLI rebuild (see `lightwave-cli/docs/cli-command-surface-audit.md`)
**Related:** `docs/pending-schema-additions.yaml` (drafted additions that never landed)

---

## Why

`lw` is meant to be schema-driven: its command surface is the **stamp** in
lightwave-core, and `internal/cli/dispatcher.go` builds the cobra tree from it
at startup. `lw check schema` is the drift gate (blocking in CI via
`LW_CHECK_SCHEMA_STRICT=1`).

Today that contract is broken at the source:

1. **No stamp exists.** `src/schemas/interfaces/cli/` contains only `.gitkeep`.
   The `interfaces/__index.yaml` notes CLI verbs land here "during Phase 8" —
   they never did.
2. **The loader points at a path that doesn't exist.**
   `internal/sst/cli_loader.go:CLIConfigPath` reads
   `<lightwave_root>/packages/lightwave-core/lightwave/schema/definitions/config/cli/commands.yaml`,
   and `lightwave_root` defaults to `~/dev/lightwave-media` (absent on dev
   machines; the real repo is `~/dev/lightwave-core` using `src/schemas/`).
   So the dispatcher reads nothing and the drift gate has no baseline.
3. **Two incompatible shapes are implied.** The loader expects a **monolithic
   `commands.yaml`** (`_meta.version` + `domains[].commands[]`). lightwave-core's
   `interfaces/__index.yaml` envisions **per-verb** schemas under
   `interfaces/cli/` that double as `--help` source *and* agent-tool
   definitions, role-filtered by `policy/governance/role-tool-permissions/`.

This req asks lightwave-core to (a) pick the canonical shape + path, (b) land
the actual command surface, and (c) stamp the new `media` domain's data schema.
`lightwave-cli` will make the matching loader/dispatcher change in lockstep.

## Required shape consumed by the dispatcher (today)

`cli_loader.go` / `cli_schema.go` decode and validate exactly:

```yaml
_meta:
  version: "x.y.z"          # required (non-empty)
domains:
  - name: "task"            # required, unique
    description: "…"        # required
    commands:
      - name: "create"      # required, unique within domain
        description: "…"    # required
        args:  ["title"]    # optional positional args (→ MinimumNArgs)
        flags: ["--dry-run","--yes","--doc"]  # optional; bool/stringSlice
                                              # disambiguated in dispatcher.go
```

Validation enforced in `LoadCLIConfig` → `Validate()`: version present; every
domain has ≥1 command; names unique; descriptions present. Flag *types* are not
yet encoded in the schema (the dispatcher carries `booleanFlags` /
`stringArrayFlags` lookup tables) — see R6.

---

## Requirements

### R1 — Decide canonical path + shape, and reconcile the loader
Pick one and we wire `lightwave-cli` to match:

- **Option A (fastest):** author a single
  `src/schemas/interfaces/cli/commands.yaml` in the dispatcher shape above.
  We update `CLIConfigPath` to resolve `<lightwave_root>/src/schemas/interfaces/cli/commands.yaml`
  and fix the `lightwave_root` default (`~/dev/lightwave-media` → the real
  lightwave-core checkout).
- **Option B (matches the interfaces vision):** author **per-verb** schemas
  under `interfaces/cli/<domain>/<verb>.yaml` and **generate** the aggregate
  `commands.yaml` the loader reads (build step in lightwave-core). Each verb
  schema carries the agent-tool definition + role tags too.

**Recommendation:** ship **A now** to unblock the drift gate, with the file
structured so a later codegen step (B) can emit it — i.e., treat `commands.yaml`
as a generated artifact target, not a hand-edited forever-file.

Either way **the loader path must point at a path that exists** — this is the
single hard blocker.

### R2 — Land the already-drafted additions
Port `lightwave-cli/docs/pending-schema-additions.yaml` into the stamp:
- `check:` domain — 13 verbs (ci, ruff, types, domains, schema, locks, deps,
  git, aws, docker, ecs, smoke, compose)
- `hooks:` domain — install, doctor, sync, circuit-breaker.check
- `local:` additions — `exec`, `install-frontend`
- `task.create` flag additions — `--skip-paperclip`, `--skip-github`
- `task.done` rewrite — args `[id]`, flags `--dry-run --yes --issue --repo` +
  new description (manual fallback for the v_core task_done SOP)

These handlers already exist in `lightwave-cli`; they are orphaned until the
schema entries land.

### R3 — Declare the keeper domains being migrated off hardcoded cobra
Per the audit (Matrix B), these native, chassis-backed domains move from
`root.go` hardcoding into the schema. Declare them so the gate covers them:
`aws` (ecs/logs/ecr), `worktree`, `agent`, `v_core`, `memory`, `msg`, `sst`,
`health`, `codegen`, `content`. (Subcommand lists in the audit doc.)
Dropped/quarantined (do **not** add): `browser`, `test`, legacy `spec` parking,
and the stubbed `context`/`scaffold` — those are removed in lightwave-cli.

### R4 — New `media:` domain block
First new domain. Reuses `internal/aws` (S3) and complements `lw cdn` /
`assets.yaml` — no parallel system.

```yaml
- name: media
  description: "Local media catalog, ingest, and R2 sync"
  commands:
    - name: inventory     # READ-ONLY
      flags: ["--roots","--manifest","--json"]
      description: "Walk roots → catalog path/size/type/dims/content-hash → manifest"
    - name: dedup         # READ-ONLY
      flags: ["--manifest","--json"]
      description: "Report duplicate content-hashes from the manifest"
    - name: classify      # READ-ONLY
      flags: ["--manifest","--json"]
      description: "Propose scope/collection/role; no writes"
    - name: ingest        # WRITE (gated)
      args:  ["source"]
      flags: ["--dry-run","--yes","--collection","--scope"]
      description: "Optimize + derive renditions; place at {scope}/{collection}/{slug}/{role}-{variant}"
    - name: sync          # WRITE (gated)
      flags: ["--dry-run","--yes","--collection"]
      description: "Push derivatives to R2 via internal/aws S3"
```

### R5 — Stamp the `media-asset` + `media-manifest` data schemas
Under `src/schemas/data/` (suggest `data/assets/`), in house conventions
(`_meta` with `schema_id`/`title`/`description`/`generates`/`depends_on`,
`required_fields`, `optional_fields`, `relations`, `example`). Capture at least:
- **tier**: `master` | `derivative` (masters are never served or committed)
- identity: `content_hash`, `source_path`, `mime_type`, `bytes`
- image/video: `width`, `height`, `aspect_ratio`, `duration_s?`
- placement: `scope`, `collection`, `slug`, `role`, `variant`
- canonical key: `{scope}/{collection}/{slug}/{role}-{variant}`
- `media-manifest`: a versioned collection of `media-asset` records (the
  `inventory` output), with `_meta.version` + generation timestamp + roots
  scanned.

Cross-reference `data/assets/assets.yaml` (`cdn.paths`) so `media sync` targets
align with the existing CDN allowlist rather than inventing prefixes.

### R6 — (recommended) Encode flag types in the schema
The dispatcher currently hardcodes which flags are bool vs string-slice
(`dispatcher.go: booleanFlags / stringArrayFlags`). If the stamp gains a
per-flag `type` (`bool|string|stringArray`), we delete those lookup tables and
the schema becomes the single source of truth. Not a blocker; flag if accepting.

### R7 — Registry wiring
Add `cli: cli/__index.yaml` to `interfaces/__index.yaml: schemas`, and create
`interfaces/cli/__index.yaml`. If R5 lands new files under `data/assets/`,
update `data/__index.yaml` accordingly. Per the interfaces index, note the
role-tool-permission tie-in (`policy/governance/role-tool-permissions/`) for
agent-facing verbs.

---

## Acceptance criteria

- `internal/sst/cli_loader.go` resolves the stamp at a path that **exists**, and
  `LoadCLIConfig` validates clean.
- With lightwave-cli's paired PR, `LW_CHECK_SCHEMA_STRICT=1 ./bin/lw check schema`
  is **green** (no missing handlers, no orphaned handlers) across the full
  keeper set + `media`.
- `media-asset` / `media-manifest` schemas validate under lightwave-core's
  schema validation and appear in the relevant `__index.yaml`.
- No handler in lightwave-cli is left orphaned; no schema verb lacks a handler.

## Sequencing (paired PRs)

1. **lightwave-core PR-1:** R1 (path/shape) + R2 (pending additions) + R7.
   **lightwave-cli PR-1:** loader path + `lightwave_root` default fix; remove
   dropped domains (`browser`/`test`/legacy `spec`/`context`/`scaffold`).
2. **lightwave-core PR-2:** R3 (migrated keepers).
   **lightwave-cli PR-2:** move each keeper from `root.go` into `*_handlers.go`.
3. **lightwave-core PR-3:** R4 + R5 (`media` domain + data schema).
   **lightwave-cli PR-3:** `internal/media` + `media_handlers.go`, `inventory`
   first (read-only).

## Open questions for lightwave-core

- A vs B for R1 — hand-authored `commands.yaml` now, or per-verb + codegen?
- Accept R6 (flag types in schema) or keep the dispatcher lookup tables?
- Confirm `data/assets/` as the home for `media-asset`/`media-manifest`, and the
  exact `assets.yaml: cdn.paths` keys `media sync` should target.
