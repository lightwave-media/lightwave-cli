# REQ → lightwave-core: stamps for the spec/ + docs/ factory

> Paired-PR style request from lightwave-cli to lightwave-core. Mirrors the
> "stamp vs print" rule in `~/.claude/CLAUDE.md` §7: schemas live in
> lightwave-core, runtime consumers (this CLI, nullclaw) consume them.
> Companion: [`docs/spec-docs-factory.md`](./spec-docs-factory.md) — the
> architecture this REQ unblocks.

**Owner of this REQ:** lightwave-cli maintainers.
**Owner of the corresponding PR:** lightwave-core maintainers.
**Status:** draft — not yet filed against lightwave-core.

This REQ lists every deliverable lightwave-core must ship before
lightwave-cli can wire the `lw docs sync / docs check / spec lint`
verbs and before nullclaw can run the cross-repo refresh loop. Each
item lists the path, the schema shape, and the consumer that needs it.

---

## 0. Acceptance criteria

The lightwave-core PR is accepted when:

- [ ] All schemas in §1 validate under `lw check schema` and generate `go:` types via the existing `_meta.generates` pipeline.
- [ ] Both blueprints in §2 render successfully under `boilerplate render --check` on a clean tmpdir.
- [ ] `commands.yaml` entries in §3 pass `LW_CHECK_SCHEMA_STRICT=1 lw check schema` against an unreleased lightwave-cli with the matching handlers registered (joint smoke test).
- [ ] `repo_registry.yaml` in §4 lists every active LightWave repo and validates against its own schema.
- [ ] CHANGELOG entry in lightwave-core under the next minor version: `feat(schemas): doc/spec artifact registries + repo_doc_manifest + repo_registry`.

---

## 1. Schemas (3 new files)

All under `src/schemas/policy/governance/` per the precedent set by
`template_kinds.yaml`. Each ships with `_meta.version`, `_meta.schema_id`,
and `_meta.generates` for the `go:` + `json_schema:` outputs.

### 1.1 `src/schemas/policy/governance/spec_artifact_kinds.yaml`

Closed registry of `spec/` document kinds. Mirrors the
`template_kinds.yaml` shape: `kind`, `description`, `extension`,
`frontmatter_required`, `required_sections`, `validator`.

```yaml
_meta:
  version: "0.1.0"
  schema_id: "lightwave://schemas/policy/governance/spec_artifact_kinds"
  generates:
    - "go:SpecArtifactKind"
    - "json_schema:spec_artifact_kinds"
  depends_on: []

spec_artifact_kinds:
  - kind: prd
    description: "Product Requirements Document — what + why, no how"
    extension: ".md"
    frontmatter_required: [kind, status, owner, created_at]
    frontmatter_status_enum: [draft, accepted, superseded, archived]
    required_sections: [Problem, Audience, "Success Metrics", "Out of Scope"]
    validator: "lw spec lint"

  - kind: adr
    description: "Architecture Decision Record — Michael Nygard format"
    extension: ".md"
    frontmatter_required: [kind, status, decided_at]
    frontmatter_status_enum: [proposed, accepted, superseded, deprecated]
    required_sections: [Context, Decision, Consequences]
    validator: "lw spec lint"

  - kind: design
    description: "Detailed design — how to build what the PRD asked for"
    extension: ".md"
    frontmatter_required: [kind, status, prd_ref]
    required_sections: [Background, Goals, "Non-Goals", "Detailed Design", "Alternatives Considered"]
    validator: "lw spec lint"

  - kind: plan
    description: "Execution plan — milestones, ownership, sequencing"
    extension: ".md"
    frontmatter_required: [kind, status, owner]
    required_sections: [Milestones, Risks, "Definition of Done"]
    validator: "lw spec lint"
```

**Consumer:** `lw spec lint` (lightwave-cli) reads this to know what
sections + frontmatter to require per kind. nullclaw reads this to
choose the right blueprint when drafting a new spec.

### 1.2 `src/schemas/policy/governance/doc_artifact_kinds.yaml`

Closed registry of `docs/` document kinds. **Critical addition over
spec kinds:** every entry declares `refresh_source` — the inputs
`lw docs sync` reads to regenerate the artifact. Drift detection
compares the artifact's `source_commit` frontmatter to the current
source SHA; mismatch → drift.

```yaml
_meta:
  version: "0.1.0"
  schema_id: "lightwave://schemas/policy/governance/doc_artifact_kinds"
  generates:
    - "go:DocArtifactKind"
    - "json_schema:doc_artifact_kinds"
  depends_on:
    - "lightwave://schemas/interfaces/cli/commands"  # contract kind reads commands.yaml

doc_artifact_kinds:
  - kind: architecture
    description: "Architecture-of-record — components, boundaries, deps"
    extension: ".md"
    frontmatter_required: [kind, generator_version, source_commit, generated_at]
    required_sections: [Components, Boundaries, "Cross-Repo Dependencies"]
    refresh_source:
      - kind: file_tree
        roots: ["internal/", "cmd/", "src/"]
        ignore_globs: ["**/*_test.go", "**/node_modules/**"]
      - kind: go_packages
        when: "go_mod_present"
    validator: "lw docs check"

  - kind: data-flow
    description: "Request/event flow — mermaid sequence diagrams + narrative"
    extension: ".mmd"          # mermaid source (GitHub renders natively)
    sibling_extension: ".md"   # narrative wrapper; must `include` the .mmd
    refresh_source:
      - kind: handlers
        glob: "**/dispatcher.go"
      - kind: openapi
        glob: "**/openapi.{yaml,json}"
    validator: "lw docs check"

  - kind: contract
    description: "Public surface — CLI verbs + exported Go/TS API + env vars + file outputs"
    extension: ".yaml"
    frontmatter_required: []   # YAML headers via leading `#` comments per generator convention
    refresh_source:
      - kind: go_exports
        when: "go_mod_present"
      - kind: ts_exports
        when: "package_json_present"
      - kind: commands_yaml
        path: "lightwave-core://src/schemas/interfaces/cli/commands.yaml"
    validator: "lw docs check"
    determinism:
      sort: "alphabetical"
      timestamp_precision: "second"

  - kind: dependency-graph
    description: "Direct + transitive dependency inventory; SBOM-style"
    extension: ".json"
    refresh_source:
      - kind: package_managers
        tools: [go, pnpm, cargo, pip]
    validator: "lw docs check"

  - kind: runbook
    description: "How to operate the running system — on-call, incident response"
    extension: ".md"
    frontmatter_required: [kind, owner, on_call_rotation_ref]
    required_sections: [Symptoms, Diagnosis, Remediation, Escalation]
    refresh_source: []          # authored, not generated; only shape-linted
    validator: "lw docs check"
```

**Consumer:** `lw docs sync` reads `refresh_source` to know how to
regenerate each kind. `lw docs check` reads `frontmatter_required` +
`required_sections` to validate shape. nullclaw reads the full registry
to decide what to refresh and what to skip.

### 1.3 `src/schemas/policy/governance/repo_doc_manifest.yaml`

Declares **what every LightWave repo must have**. Per-tier defaults +
overridable freshness policy. This is the "drift = missing or stale doc
relative to manifest" definition.

```yaml
_meta:
  version: "0.1.0"
  schema_id: "lightwave://schemas/policy/governance/repo_doc_manifest"
  generates:
    - "go:RepoDocManifest"
    - "json_schema:repo_doc_manifest"
  depends_on:
    - "lightwave://schemas/policy/governance/spec_artifact_kinds"
    - "lightwave://schemas/policy/governance/doc_artifact_kinds"

defaults:
  spec_required: [prd, adr]
  docs_required: [architecture, contract, dependency-graph]
  docs_recommended: [data-flow, runbook]

tiers:
  cli:
    docs_required: [architecture, contract, dependency-graph, runbook]
  service:
    docs_required: [architecture, data-flow, contract, dependency-graph, runbook]
  library:
    docs_required: [architecture, contract, dependency-graph]
  agent:
    docs_required: [architecture, contract, runbook]

freshness:
  max_age_days: 30
  stale_action: warn           # warn | drift_pr | block_merge
  source_change_action: drift_pr  # any source mtime > doc mtime
  source_commit_action: drift_pr  # source_commit frontmatter != HEAD
```

**Consumer:** `lw docs check` reads this manifest (plus the per-repo
`.lwdocs.yaml` override) to compute required-vs-present + freshness.
nullclaw reads `freshness.*_action` to decide what to do on drift.

### 1.4 `src/schemas/data/meta/repo_registry.yaml` (estate registry)

Where lightwave-core stamps the list of LightWave repos. nullclaw
materializes the print at `~/.lightwave/repos.yaml` on first read.

```yaml
_meta:
  version: "0.1.0"
  schema_id: "lightwave://schemas/data/meta/repo_registry"
  generates:
    - "go:RepoRegistry"
    - "json_schema:repo_registry"

tiers_enum: [cli, service, library, agent, infra, docs]  # mirrors repo_doc_manifest.tiers

repos:
  - name: lightwave-cli
    tier: cli
    remote: github.com/lightwave-media/lightwave-cli
    docs_overrides_path: ".lwdocs.yaml"
  - name: lightwave-core
    tier: library
    remote: github.com/lightwave-media/lightwave-core
  - name: nullclaw
    tier: agent
    remote: github.com/lightwave-media/nullclaw
  # ... full estate ...
```

**Consumer:** nullclaw `refresh-docs` iterates `repos`. `lw docs check`
in cross-repo mode (rare) reads this too.

---

## 2. Blueprints (2 new directories)

Both under `src/boilerplate/blueprints/`, registered in
`blueprints/__index.yaml`. Follow the `site-section` precedent
(see §"NOTE: no required_version" in `site-section/boilerplate.yml`)
until a tagged engine build is the standard.

### 2.1 `src/boilerplate/blueprints/spec-repo/`

Scaffolds `<repo>/spec/` with the four kind subdirs + a `0001-` skeleton
per kind. `boilerplate.yml` variables:

```yaml
variables:
  - name: repo_name
    type: string
    description: "Repo this scaffold targets — used in titles + cross-refs"
  - name: tier
    type: enum
    options: [cli, service, library, agent, infra, docs]
    description: "Repo tier — drives recommended kinds via repo_doc_manifest"
  - name: include_design
    type: bool
    default: false
    description: "Emit a spec/design/0001-*.md skeleton (service/agent tiers)"
hooks:
  after:
    - command: "echo"
      args: ["spec/ scaffolded for {{ .repo_name }}. Edit spec/prd/0001-*.md to begin."]
```

Emits:

```
spec/
├── README.md
├── prd/0001-initial.md
├── adr/0001-initial.md
├── plan/0001-initial.md
└── design/0001-initial.md   # only if include_design
```

Every skeleton ships with **valid frontmatter that satisfies its kind's
`frontmatter_required`** so `lw spec lint` passes on first scaffold.

### 2.2 `src/boilerplate/blueprints/docs-repo/`

Scaffolds `<repo>/docs/` with one placeholder per required kind from
`repo_doc_manifest.yaml` for the given tier. Each placeholder is **valid
but empty** — the real content lands on first `lw docs sync`. Variables:

```yaml
variables:
  - name: repo_name
    type: string
  - name: tier
    type: enum
    options: [cli, service, library, agent, infra, docs]
  - name: include_runbook
    type: bool
    default: true
hooks:
  after:
    - command: "echo"
      args: ["docs/ scaffolded for {{ .repo_name }}. Run `lw docs sync` to populate."]
```

Emits (for tier=cli):

```
docs/
├── README.md
├── architecture.md        # placeholder with frontmatter, body says "run lw docs sync"
├── contract.yaml          # placeholder with generator header
├── dependency-graph.json  # placeholder: {}
├── runbook/.gitkeep
├── command-status.md      # CLI-tier only — the verb trust registry
└── .lwdocs.yaml           # local override of manifest defaults
```

### 2.3 `blueprints/__index.yaml` patch

```yaml
blueprints:
  # ... existing ...
  spec-repo: spec-repo
  docs-repo: docs-repo
```

---

## 3. `commands.yaml` — new verb registrations

Without these entries, lightwave-cli's `lw check schema` trips on the
new handlers per the dispatcher invariants in
`/Users/joelschaeffer/dev/lightwave-cli/CLAUDE.md` §"Schema-Driven CLI".

Add under the appropriate domain in
`src/schemas/interfaces/cli/commands.yaml`:

```yaml
domains:
  docs:
    description: "Documentation factory — descriptive layer regeneration + drift detection"
    commands:
      - name: sync
        handler_key: docs.sync
        flags:
          - { name: dry-run, type: bool, default: false }
          - { name: yes, type: bool, default: false }
          - { name: check, type: bool, default: false }
          - { name: since, type: string, default: "" }  # e.g. "HEAD" for pre-commit subset
        exit_codes: { 0: clean, 1: changes_written, 2: tool_error }
      - name: check
        handler_key: docs.check
        flags:
          - { name: all, type: bool, default: false }
        exit_codes: { 0: clean, 1: drift, 2: tool_error }

  spec:
    # existing 'spec' domain (generate/from-issue/validate) stays untouched
    commands:
      # ... existing ...
      - name: lint
        handler_key: spec.lint
        flags:
          - { name: fix, type: bool, default: false }
          - { name: all, type: bool, default: false }
        exit_codes: { 0: clean, 1: violations, 2: tool_error }

  scaffold:
    # extend with the two new blueprint verbs (or accept dynamic
    # blueprint dispatch — see §5 alternative)
    commands:
      # ... existing app/model/api/test ...
      - name: spec-repo
        handler_key: scaffold.spec_repo
      - name: docs-repo
        handler_key: scaffold.docs_repo
```

---

## 4. Optional but recommended

### 4.1 `template_kinds.yaml` — extend the existing registry

`policy/governance/template_kinds.yaml` already declares
`documents_freeform` (extension `.md`, structure `markdown-loose`,
frontmatter required). Two refinements would tighten the contract:

- Add a new kind `doc_artifact` with extension `matches-output` (md /
  mmd / yaml / json per `doc_artifact_kinds.yaml`), validator
  `lw docs check`. Keeps the universal registry universal.
- Add a new kind `spec_artifact` mirroring `spec_body` but for
  per-repo artifacts (the existing `spec_body` is for the lightwave-core
  R-P-I-V-R agile templates, which serve a different purpose).

Neither blocks v1, but ships zero-ambiguity if filed together.

### 4.2 JSON-Schema generation

Confirm the `json_schema:<name>` generator emits to
`schemas/generated/json/policy/governance/*.json` so language-agnostic
validators (Python, TS) can consume the registries without going through
the Go types. This matches the `interfaces/cli/commands.yaml` generation
path.

---

## 5. Alternatives considered

**Dynamic blueprint dispatch instead of explicit `commands.yaml` entries
for each blueprint.** `lw scaffold <name>` could read `__index.yaml`
at startup and register each blueprint as a synthetic verb. Pro: adding
a blueprint doesn't require a `commands.yaml` PR. Con: breaks the
"no schema entry without a registered Go handler" invariant —
`lw check schema` becomes more permissive. **Recommend explicit
entries** until a separate decision relaxes the schema invariant for
blueprints specifically.

**Single `docs_artifact_kinds.yaml` covering both spec and docs kinds.**
Tempting but conflates the two contracts: spec has no `refresh_source`,
docs has no `frontmatter_status_enum`. Keeping them split mirrors the
spec/docs split in the repo layout and stays consistent with how
`template_kinds.yaml` already differentiates `spec_body` from
`documents_freeform`.

**Store `repo_registry` only as a print under `~/.lightwave/`.**
Would skip the lightwave-core stamp. Violates CLAUDE.md §7 — the estate
list is a shape (which repos belong in LightWave?), not runtime data.
Stamp it.

---

## 6. Out of scope for this REQ

- Generator implementations (lightwave-cli ships these).
- nullclaw cron config (nullclaw repo ships this; references the
  stamped `repo_registry.yaml`).
- Per-repo `.lwdocs.yaml` overrides (each repo ships its own as part of
  its scaffold PR).
- Migration of existing markdown in `~/.lightwave/docs/` (orthogonal —
  that tree is a different surface and stays as-is).

---

## 7. Filing checklist

When this REQ is converted to a lightwave-core PR:

- [ ] Branch off `origin/main` after `git pull --ff-only` per the
      `lightwave-git` skill defaults.
- [ ] PR title: `feat(schemas): doc/spec artifact registries + repo_doc_manifest`
- [ ] PR body includes `Closes <REQ-issue-#>` and links back to
      `lightwave-cli://docs/spec-docs-factory.md`.
- [ ] Joint smoke test in CI: render both blueprints into a tmpdir;
      `lw spec lint` + `lw docs check` against the rendered tree both
      exit 0.
- [ ] CHANGELOG entry in lightwave-core under the next minor.
- [ ] Tag a release once merged — lightwave-cli's PRs (architecture
      doc §7 step 2+) pin to this tag.
