# The spec/ + docs/ factory — every LightWave repo, always current

> One blueprint library (lightwave-core) + one CLI (`lw`) + one scheduled
> agent (nullclaw) keep every repo's `spec/` (intent) and `docs/` (reality)
> deterministic and fresh. This document is the architecture-of-record.

Companion: [`docs/req-lightwave-core-spec-docs-factory.md`](./req-lightwave-core-spec-docs-factory.md)
— the paired-PR REQ enumerating exactly what lightwave-core must ship so
this CLI can finish wiring.

---

## 1. The contract: `spec/` vs `docs/`

Two directories scaffold into every LightWave repo. They serve opposite
purposes and are kept honest by opposite mechanisms.

| Dir | Purpose | Voice | Honesty mechanism | May lead code? |
| --- | --- | --- | --- | --- |
| `spec/` | **Aspirational** — what the code is meant to become. PRDs, plans, ADRs, design intent, agent prompts that describe a future state. | First-person, future tense. | Linted for **shape** (frontmatter + sections per schema). Cannot be validated against code because it MAY be ahead of code. | Yes — that's the point. |
| `docs/` | **Descriptive** — what the code actually IS, right now. Architecture-of-record, data-flow mermaid, public contract surface, dependency graph, runbooks. | Third-person, present tense. | Linted for **shape** AND **truth** — `lw docs sync` regenerates from code/schemas; `lw docs check` flags drift as exit 1. | No. If it leads code, it's drift. |

> Rule of thumb: if a sentence in `docs/` becomes false the moment someone
> merges a PR, it lives in `spec/`. If a sentence in `spec/` is already
> true today, it belongs in `docs/` (or both — `spec/` keeps the rationale,
> `docs/` keeps the snapshot).

### Why two directories, not one

Conflating intent and reality is the single most common documentation
failure mode. A reader who can't tell whether `## Authentication` is
"this is how it works today" or "this is how we want it to work" can
trust neither. The split is operational: humans + agents author `spec/`
freely; `docs/` is partly authored, partly machine-generated, and gated
by drift detection.

---

## 2. What lightwave-core must ship (answer first)

Listed exhaustively so the REQ ([`req-lightwave-core-spec-docs-factory.md`](./req-lightwave-core-spec-docs-factory.md))
maps 1:1 to deliverables. Nothing here lands in this repo until core
ships these stamps.

### 2.1 Blueprints (`src/boilerplate/blueprints/`)

Two new blueprints registered in `blueprints/__index.yaml`:

```yaml
# Append to lightwave-core/src/boilerplate/blueprints/__index.yaml
blueprints:
  # ... existing ...
  spec-repo: spec-repo   # scaffolds <repo>/spec/ with PRD/ADR/plan skeletons
  docs-repo: docs-repo   # scaffolds <repo>/docs/ with architecture/data-flow/contracts skeletons
```

Each blueprint is a Gruntwork-style directory: `boilerplate.yml` manifest
+ parameterized files. Variables (typed, defaulted) per the
`boilerplate_file` convention already declared in
`policy/governance/template_kinds.yaml`.

### 2.2 Schemas (`src/schemas/policy/governance/`)

Three new schema files, each producing both `go:<Type>` and
`json_schema:<name>` per the lightwave-core generation convention.

**a) `spec_artifact_kinds.yaml`** — closed registry of `spec/` document kinds:

```yaml
spec_artifact_kinds:
  - kind: prd
    description: "Product Requirements Document — what + why, no how"
    extension: .md
    frontmatter_required: [kind, status, owner, created_at]
    required_sections: [Problem, Audience, Success Metrics, Out of Scope]
    validator: lw spec lint
  - kind: adr
    description: "Architecture Decision Record — Michael Nygard format"
    extension: .md
    frontmatter_required: [kind, status, decided_at]
    required_sections: [Context, Decision, Consequences]
  - kind: design
    description: "Detailed design — how to build what the PRD asked for"
    extension: .md
    frontmatter_required: [kind, status, prd_ref]
    required_sections: [Background, Goals, Non-Goals, Detailed Design, Alternatives Considered]
  - kind: plan
    description: "Execution plan — milestones, ownership, sequencing"
    extension: .md
    frontmatter_required: [kind, status, owner]
    required_sections: [Milestones, Risks, Definition of Done]
```

**b) `doc_artifact_kinds.yaml`** — closed registry of `docs/` document kinds, each with a `refresh_source` declaring how `lw docs sync` regenerates it:

```yaml
doc_artifact_kinds:
  - kind: architecture
    description: "Architecture-of-record — components, boundaries, dependencies"
    extension: .md
    frontmatter_required: [kind, generator_version, source_commit]
    required_sections: [Components, Boundaries, Cross-Repo Dependencies]
    refresh_source:
      - kind: file_tree
        roots: [internal/, cmd/, src/]
      - kind: go_packages
        when: go_mod_present
  - kind: data-flow
    description: "Request/event flow across processes — mermaid sequence diagrams"
    extension: .mmd        # standalone mermaid; renderable on GitHub
    sibling_extension: .md # narrative wrapper that embeds the .mmd
    refresh_source:
      - kind: handlers
        glob: "**/dispatcher.go"
      - kind: openapi
        glob: "**/openapi.{yaml,json}"
  - kind: contract
    description: "Public surface — exported API, CLI verbs, env vars, file outputs"
    extension: .yaml       # machine-readable; diffable; lintable by JSON Schema
    refresh_source:
      - kind: go_exports
        when: go_mod_present
      - kind: commands_yaml
        path: lightwave-core://src/schemas/interfaces/cli/commands.yaml
  - kind: dependency-graph
    description: "Direct + transitive dependency inventory + SBOM-style snapshot"
    extension: .json
    refresh_source:
      - kind: package_managers
        tools: [go, pnpm, cargo, pip]
  - kind: runbook
    description: "How to operate the running system — on-call, incident response"
    extension: .md
    frontmatter_required: [kind, owner, on_call_rotation_ref]
    required_sections: [Symptoms, Diagnosis, Remediation, Escalation]
```

**c) `repo_doc_manifest.yaml`** — declares **what every LightWave repo must have**. Drift = missing or stale doc relative to this manifest.

```yaml
# Per-tier defaults. A repo's tier is read from its top-level CLAUDE.md.
defaults:
  spec_required: [prd, adr]                          # every repo
  docs_required: [architecture, contract, dependency-graph]
  docs_recommended: [data-flow, runbook]
tiers:
  cli:
    docs_required: [architecture, contract, dependency-graph, runbook]
  service:
    docs_required: [architecture, data-flow, contract, dependency-graph, runbook]
  library:
    docs_required: [architecture, contract, dependency-graph]
freshness:
  max_age_days: 30           # auto-stale after this
  stale_action: warn         # warn | drift_pr | block_merge
  source_change_action: drift_pr  # when source file mtime > doc mtime
```

### 2.3 Discovery: `repo_registry.yaml`

Add at `src/schemas/data/meta/repo_registry.yaml`. Stamp the LightWave
estate so nullclaw can discover what to refresh:

```yaml
repos:
  - name: lightwave-cli
    tier: cli
    remote: github.com/lightwave-media/lightwave-cli
  - name: lightwave-core
    tier: library
    remote: github.com/lightwave-media/lightwave-core
  # ...
```

The runtime print is `~/.lightwave/repos.yaml` — generated from the stamp
on first read (CLAUDE.md §7).

---

## 3. The CLI surface

### 3.1 Scaffold (existing engine, new blueprints)

```bash
lw scaffold spec-repo --var repo_name=lightwave-cli --var tier=cli
lw scaffold docs-repo --var repo_name=lightwave-cli --var tier=cli
```

Both shell into the Gruntwork `boilerplate` engine via the existing
`internal/scaffold/` wrapper. **No parallel generator.** The current
`scaffold_handlers.go` stubs (`scaffold app|model|api|test`) are
unrelated app-skeleton verbs and stay where they are; the blueprint
verbs ride the engine directly per the `site-section` precedent.

### 3.2 New verbs (all gated by `docs/command-status.md`)

| Verb | Purpose | Exit codes | Destructive flags |
| --- | --- | --- | --- |
| `lw docs sync` | Regenerate `docs/` artifacts from code/schemas, idempotent. Re-runs every refresh_source declared in `doc_artifact_kinds.yaml`. | 0 clean, 1 wrote changes (in `--check` mode this is drift), 2 tool error | `--dry-run`, `--yes`, `--check` (no writes, exit 1 on drift) |
| `lw docs check` | Pure drift detector. Alias for `lw docs sync --check`. Wired into `lw check`. | 0 clean, 1 drift found, 2 tool error | n/a (read-only) |
| `lw spec lint` | Validate `spec/` shape: every file's frontmatter + sections match its `spec_artifact_kinds` entry. | 0 clean, 1 violations, 2 tool error | `--fix` for mechanical fixes (missing frontmatter keys with defaults) |

Each verb:

1. Lives in `internal/cli/docs_sync.go` / `docs_check.go` / `spec_lint.go` (one file per check, per `lw check` requirements §6).
2. Ships with `_test.go` containing one known-bad fixture (fires) + one known-good fixture (silent) per CLAUDE.md §8.
3. Registers in lightwave-core's `commands.yaml` so `lw check schema` doesn't trip (per `docs/req-lightwave-core-spec-docs-factory.md`).
4. Starts life listed in **`docs/command-status.md`** as either `verified` (with test commit SHA) or `decommissioned`. Anything in between gets removed from the dispatcher.

### 3.3 Trust policy: `docs/command-status.md`

Establishes the registry the CLAUDE.md §"every new verb ships verified or
stays decommissioned" rule needs. One row per verb:

```markdown
| Verb           | Status        | Test file                          | Verified at |
| -------------- | ------------- | ---------------------------------- | ----------- |
| docs sync      | verified      | internal/cli/docs_sync_test.go     | <commit>    |
| docs check     | verified      | internal/cli/docs_check_test.go    | <commit>    |
| spec lint      | verified      | internal/cli/spec_lint_test.go     | <commit>    |
| scaffold app   | decommissioned| —                                  | —           |
```

`lw check status` reads this file and exits 1 if any registered handler
in `dispatcher.go` is missing or marked `decommissioned`. This is the
companion gate to `lw check schema`.

---

## 4. The keep-it-fresh loop

Three triggers, one agent (nullclaw), one source of truth (the manifest).

### 4.1 Triggers

| Trigger | Cadence | Scope | Action |
| --- | --- | --- | --- |
| **Pre-commit** | every commit | files staged in this commit | `lw docs check --since HEAD` — fast subset (touched code paths only). Exit 1 fails the commit; author runs `lw docs sync` to fix. |
| **CI** | every PR | full repo | `lw docs check --all` as a blocking gate alongside `lw check schema` (extends `.github/workflows/schema-drift-check.yml`). |
| **Cron / nullclaw** | nightly + on-merge webhook | every repo in `~/.lightwave/repos.yaml` | nullclaw clones, runs `lw docs sync`, opens a `docs/drift-YYYY-MM-DD` PR if the working tree changed. Per-repo idempotent. |

### 4.2 nullclaw discovery + idempotency

```
nullclaw refresh-docs                       # autonomous
  → reads ~/.lightwave/repos.yaml
  → for each repo:
      1. git clone --depth=1 (cached at ~/.lightwave/workdir/<repo>/)
      2. lw docs sync (writes to working tree only)
      3. if `git diff --exit-code` → no-op, continue
      4. else:
         - branch: docs/auto-refresh-YYYY-MM-DD
         - commit: "docs(auto): refresh from source @ <sha>"
         - PR body: drift report (which kinds changed, which sources triggered)
         - assign to repo's tier owner from repo_registry.yaml
```

Idempotency comes from `lw docs sync` being deterministic — same inputs
(code + schemas at SHA X) → byte-identical output. Determinism rules in
§6.

### 4.3 Brain + memory integration

The corpus compounds because every pass reads and writes.

**Reads** (grounds nullclaw's spec/ proposals — never blocks docs/):

- `~/.lightwave/brain/memory/feedback/*.yaml` — past architectural decisions; surfaced as "related ADRs" in spec/adr-* drafts.
- `~/.lightwave/brain/memory/failures/*.yaml` — past incidents; surfaced as required runbook scenarios.

**Writes** (per-pass artifacts):

```
~/.lightwave/brain/memory/docs-drift/<repo>-YYYY-MM-DD.yaml
  # When drift is found. Includes: repo, kinds_drifted, sources_changed,
  # pr_url, fixed_in_commit (filled when PR merges).

~/.lightwave/brain/memory/sessions/docs-refresh-<repo>-YYYY-MM-DD.yaml
  # Every pass (drift or no-op). Includes: repo, duration_ms, exit_code,
  # bytes_written, kinds_synced. Aggregated weekly by `lw memory report`.
```

The drift-report file is the single artifact a human reads when asking
"what changed?" — it's the manifest's drift surfaced as data, not noise.

---

## 5. Repo layout after scaffold (vertical slice)

What `lw scaffold spec-repo && lw scaffold docs-repo` produces on first
run in any LightWave repo. Shown for `lightwave-cli` specifically (tier:
`cli`).

```
lightwave-cli/
├── spec/                          # human/agent authored, shape-linted
│   ├── README.md                  # explains spec/ purpose + how to add
│   ├── prd/
│   │   └── 0001-spec-docs-factory.md       # this initiative's PRD
│   ├── adr/
│   │   └── 0001-docs-vs-spec-split.md      # why two dirs
│   ├── design/
│   │   └── 0001-spec-docs-factory.md       # detailed design
│   └── plan/
│       └── 0001-spec-docs-factory.md       # milestones
│
├── docs/                          # generated + human-augmented, drift-checked
│   ├── README.md                  # explains docs/ purpose + regeneration
│   ├── architecture.md            # GENERATED — components, boundaries
│   ├── data-flow.mmd              # GENERATED — mermaid sequence
│   ├── data-flow.md               # narrative wrapper around .mmd
│   ├── contract.yaml              # GENERATED — public CLI + Go API surface
│   ├── dependency-graph.json      # GENERATED — SBOM-style snapshot
│   ├── runbook/
│   │   └── lw-down.md             # AUTHORED — operator runbook
│   ├── command-status.md          # AUTHORED — verb trust registry (§3.3)
│   ├── lw-task-done.md            # AUTHORED — existing runbook (untouched)
│   └── release-notarization.md    # AUTHORED — existing runbook (untouched)
│
├── .lwdocs.yaml                   # local override of repo_doc_manifest defaults
│
└── .github/workflows/
    └── docs-drift-check.yml       # CI gate: `lw docs check --all`
```

### 5.1 Sample `spec/` artifact

`spec/adr/0001-docs-vs-spec-split.md`:

```markdown
---
kind: adr
status: accepted
decided_at: 2026-06-11
owner: joel
---

# ADR-0001: Split documentation into spec/ (intent) and docs/ (reality)

## Context

Existing repos mix aspirational ("we plan to support X") and descriptive
("we currently support Y") prose in the same files. Readers can't tell
which sentences are commitments and which are facts.

## Decision

Two directories per repo: `spec/` for intent, `docs/` for reality.
`docs/` is regenerated by `lw docs sync` from code + schemas; `spec/`
is human/agent authored and shape-linted by `lw spec lint`.

## Consequences

- Doc drift becomes mechanical: missing or stale `docs/` artifacts fail
  `lw docs check`, which gates CI and pre-commit.
- `spec/` is allowed to be ahead of code by design; it's never compared
  to code, only to the kind schema.
- Initial scaffolding cost ~one PR per repo, paid via `lw scaffold`.
```

### 5.2 Sample `docs/` artifact (generated)

`docs/contract.yaml` (generated by `lw docs sync` from `commands.yaml` + Go exports):

```yaml
# GENERATED by lw docs sync v1.0.0 — DO NOT EDIT BY HAND
# source_commit: 5c79cbf
# generated_at: 2026-06-11T12:34:56Z
# inputs:
#   - lightwave-core://src/schemas/interfaces/cli/commands.yaml@<sha>
#   - go_exports: internal/...
cli_verbs:
  - name: check schema
    handler: check.schema
    flags: [--all, --strict]
    exit_codes: {0: clean, 1: drift, 2: tool_error}
  - name: docs sync
    handler: docs.sync
    flags: [--dry-run, --yes, --check]
    exit_codes: {0: clean, 1: changes_written, 2: tool_error}
  # ... alphabetically sorted, full surface ...
go_exports:
  internal/cli:
    - RegisterHandler(key string, h Handler)
    # ... sorted ...
env_vars:
  - LW_CHECK_SCHEMA_STRICT
  - LW_TEST_DB_URL
  # ... sorted ...
```

The sort + frontmatter + `source_commit` make diffs strictly meaningful:
any change is either a real surface change or a regenerator bug.

---

## 6. Zero-ambiguity output rules

Apply to every artifact `lw docs sync` writes. Codified in
`doc_artifact_kinds.yaml` and enforced by `lw docs check`.

| Rule | Why |
| --- | --- |
| Generator header on every generated file: `# GENERATED by lw docs sync vX.Y.Z` + `source_commit:` + `generated_at:`. | Makes provenance + reproducibility one-line obvious. |
| All lists alphabetically sorted. | Reorder isn't drift; only adds/removes are. |
| Timestamps UTC, ISO-8601, second precision (no millis). | Stable, locale-free, lossless. |
| Trailing newline + LF line endings + 2-space indent (YAML/JSON) / 4-space (markdown lists). | Locked by shipped `.editorconfig`; diff noise → zero. |
| File-type per purpose, no overlap (see table below). | Each diff goes to the tool that reads it best. |

### 6.1 File-type-per-purpose

| Purpose | Type | Why not the alternatives |
| --- | --- | --- |
| Prose (PRD/ADR/design/plan, runbooks, READMEs) | `.md` (or `.mdx` if the repo already uses MDX) | Renders everywhere; frontmatter for kind discrimination; lintable by markdownlint + custom `lw spec lint`. mdx only when the repo needs JSX embed. |
| Flow + architecture diagrams | `.mmd` (mermaid) + narrative `.md` wrapper | GitHub renders mermaid natively; lintable by `mermaid-cli`; text-diffable. PNG/SVG aren't diff-friendly; PlantUML needs Java. |
| Structured contracts (CLI surface, public Go/TS API, manifest) | `.yaml` | Diff-stable, human-readable, JSON-Schema-validatable. JSON loses comments; XML is heavier and we don't store XML (only use it inside agent prompts per `template_kinds.yaml`). |
| Generated snapshots (dependency graph, SBOM) | `.json` | Machine-only consumer; jq-queryable; large but compresses well in PRs. YAML adds no value when no human edits it. |
| Configuration of the docs system itself (`.lwdocs.yaml`) | `.yaml` | Same reasoning as contracts; consistent with `.lwconfig.yaml`. |

> XML is intentionally absent. The single place it appears in lightwave-core
> is inside `.md` agent prompts (per `template_kinds.yaml` `valid_tags`).
> Never as a storage format for spec/docs artifacts.

---

## 7. Sequencing — how this lands

Paired PRs only; nothing lands half-stamped.

1. **lightwave-core PR**: schemas (§2.2) + blueprints (§2.1) + commands.yaml entries for the new verbs. Tag a release. **Blocking** for steps 2+.
2. **lightwave-cli PR-A**: wire `internal/cli/docs_sync.go`, `docs_check.go`, `spec_lint.go` + tests. Establish `docs/command-status.md` (this repo). Verified entries appear here only after tests land. Ship via tap tag.
3. **lightwave-cli PR-B**: extend `internal/scaffold/` wrapper to dispatch `lw scaffold spec-repo` / `docs-repo` through the boilerplate engine (the existing stubs are unrelated app-skeleton verbs — leave them alone).
4. **nullclaw PR**: add `refresh-docs` subcommand reading `repo_registry.yaml`, with cron entry in `~/.lightwave/config/schedules/`.
5. **Per-repo PRs**: `lw scaffold spec-repo && lw scaffold docs-repo && lw docs sync` in each repo from `repo_registry.yaml`. First scaffolded: lightwave-cli (self-host the system).
6. **Activation**: flip `freshness.stale_action` from `warn` to `drift_pr` in `repo_doc_manifest.yaml` once nullclaw has run one clean weekly cycle.

---

## 8. What this is NOT

To preempt scope drift. None of these belong in v1.

- A static-site generator. `docs/` renders on GitHub directly; if a repo wants a site, that's a downstream `lw docs publish` later.
- A wiki replacement. spec/ and docs/ are in-repo so they version with code; cross-repo discovery is via `lw docs grep` (future).
- An LLM auto-writer for `spec/`. nullclaw drafts proposals from brain memory but a human approves the PR. `docs/` is mechanical from code; `spec/` remains human-owned.
- A schema migration tool. `doc_artifact_kinds.yaml` is versioned (`_meta.version`); migrations are stamp PRs in lightwave-core.

---

## 9. Open questions (decide before merging this doc)

1. **Should `docs/contract.yaml` be checked into git, or generated on demand?** Recommendation: check in. Reviewers need to see the diff at PR time, not approve a generator they then have to run.
2. **Where does `repo_registry.yaml` print to on machines that don't run nullclaw?** Recommendation: nowhere. Only nullclaw + ops-tier humans need the print; everyone else uses git remotes directly.
3. **Should `spec/prd/0001-*` be numbered or slugged?** Recommendation: numbered (`0001-`, `0002-`) for chronological ordering; slug after the number for grep-ability.
4. **Pre-commit cost budget.** `lw docs check --since HEAD` must stay under 2s on a typical changeset to fit the pre-commit budget (per `lw check` §3). Profile early; defer expensive refresh_sources (dependency-graph, openapi parse) to CI-only.
