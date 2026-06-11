---
name: lightwave-cli
description: >-
  Drive the LightWave `lw` CLI — the factory front door for the whole monorepo.
  Use this skill WHENEVER you are working in any lightwave repo and need to:
  scaffold a React component or marketing section (lw scaffold / lw ui), run
  cited web research (lw research), run the commit/push gates (lw check), manage
  tasks/sprints/stories/epics, spin the local dev stack (lw local), do AWS/ECS
  ops triage (lw aws), reach Postgres (lw db), or reconcile CDN/schema drift.
  Reach for `lw` BEFORE hand-rolling a generator, writing a one-off script, or
  running raw `aws`/`gh`/`make` — even if the user doesn't say "lw". If a task
  smells like "generate a component", "research X", "check before committing",
  "what tasks are open", or "tail prod logs", this skill applies.
allowed-tools: Bash(lw:*), Bash(lw scaffold:*), Bash(lw research:*), Bash(lw check:*)
---

# LightWave CLI (`lw`)

`lw` is one Go binary that wraps the whole LightWave factory: scaffolding,
research, agile artifacts, gates, ops, and dev environment. The guiding idea:
**every repo uses the same stamped tools instead of reinventing per-repo
scripts.** When you reach for a shell one-liner, check first whether `lw`
already does it — it usually does, and doing it through `lw` keeps validation,
provenance, and conventions intact.

## Install & sanity

`lw` ships via Homebrew tap, never `go install`:

```bash
brew install lightwave-media/tap/lw   # or: brew upgrade lw
lw version          # prints version + per-subsystem API map
lw --help           # lists every command
```

If `lw <cmd>` prints `warning: schema-dispatched commands unavailable …`, that's
expected on a machine without the `lightwave-core` checkout: hardcoded commands
(scaffold, research, aws, version, …) still work; schema-driven domains (task,
check, db, sprint, …) need the `commands.yaml` stamp present. It is a warning,
not an error.

## Agent rules of engagement

- **Always non-interactive.** Pass `--yes` / `--non-interactive` and provide all
  inputs via flags so nothing blocks on a prompt. `lw scaffold` is already
  non-interactive by design.
- **Prefer `--json`** when a command offers it, then parse — don't scrape the
  human table. (`lw research --json`, `lw version --json`, `lw health --json`,
  many others.)
- **Destructive commands** ship `--dry-run` (preview) and `--yes` (skip the
  prompt). Run `--dry-run` first, show the user, then `--yes`.
- **Secrets live in AWS**, not env. `lw` reads keys from SSM (e.g.
  `/lightwave/prod/PERPLEXITY_API_KEY`); set the matching env var only as a
  dev override.

---

## The scaffolding tool (`lw scaffold` / `lw ui`)

`lw` resolves a **blueprint by name** from the canonical lightwave-core library
and shells out to the Gruntwork `boilerplate` engine. It does **not** template
anything itself — so never write a parallel generator; add a blueprint instead.

```bash
# Generic: render any blueprint into an output folder
lw scaffold <blueprint> -o <dir> [--var k=v]... [--var-file f]... [--no-hooks]

# Sugar for lightwave-ui components (maps <category>/<Name> to vars)
lw ui component <category>/<Name> [-o <dir>]
```

**Blueprint discovery** (first match wins): `--blueprints-dir` →
`$LW_BLUEPRINTS_DIR` → `<lightwave_root>/src/boilerplate/blueprints`. A blueprint
is a directory containing a `boilerplate.yml`. A missing library or unknown
blueprint produces a clear error — list the dir to see what's available.

**Example 1 — marketing section (joelschaeffer-site):**
Input: scaffold a PricingTable marketing section
Output:
```bash
lw scaffold site-section -o src/components/marketing \
  --var category=marketing --var component_name=PricingTable
```

**Example 2 — lightwave-ui component:**
Input: new application-tier DataTable component
Output:
```bash
lw ui component application/DataTable
# == lw scaffold react-component --var category=application --var component_name=DataTable
```

**Engine note:** `boilerplate` must be a *tagged* release on `PATH` or
`~/go/bin`. A `development` build fails blueprints that declare
`required_version`. Install with
`go install github.com/gruntwork-io/boilerplate@latest`.

**Don't have a blueprint for the shape you need?** Add it to
`lightwave-core/src/boilerplate/blueprints/<name>/` (a `boilerplate.yml` +
parameterized files) and register it in that dir's `__index.yaml`. `lw scaffold`
is generic — no CLI change needed.

---

## The research tool (`lw research`)

Perplexity-backed, scriptable, cited research — the agent-callable counterpart to
the interactive deep-research flow.

```bash
lw research "what changed in the EU AI Act in 2026?"
lw research --deep "survey agentic retrieval architectures" -o report.md
lw research --recency week --domains arxiv.org "search-as-code prior art"
lw research --json "latest Go 1.24 release notes" | jq .citations
```

`--deep` selects `sonar-deep-research` (slower, richer). Key resolves from SSM
`/lightwave/prod/PERPLEXITY_API_KEY` (or `PERPLEXITY_API_KEY` env for dev).

---

## Top 10 best ways to use `lw`

A ranked starting set. Run `lw <cmd> --help` for the full surface of each.

1. **Scaffold from canonical blueprints** — `lw scaffold <bp> -o <dir> --var …`
   / `lw ui component <cat>/<Name>`. One templating engine, one stamped library,
   every repo. Beats hand-writing component/section boilerplate.

2. **Cited research without leaving the terminal** — `lw research --json "<q>"`.
   Feed citations straight into a PR description or a decision doc.

3. **Run the gates before you commit** — `lw check` (umbrella) and
   `lw check schema` (handler↔schema drift). This is the Definition of Done;
   run it instead of guessing whether CI will pass. `lw check ci --staged`
   scopes to staged files for a fast pre-commit pass.

4. **Manage agile artifacts against the platform DB** — `lw task list` /
   `lw task create …` / `lw task start <id>` / `lw task done <id>`, plus
   `lw sprint current`, `lw story show`, `lw epic tasks`. Direct Postgres reads,
   so they're instant.

5. **Isolated agent sessions via worktrees** — `lw worktree create <issue>
   --type feature --description <slug>` to start a sealed session off
   `origin/main`, `lw worktree status --current` for gate checks, `lw worktree
   gc <issue>` (or `prune --dry-run`) to clean up. Keeps every agent off `main`.

6. **Spin the local dev stack** — `lw local up` / `down` / `health` /
   `logs` / `restart`, and `lw local exec <service>` to run inside a compose
   service. One command instead of remembering compose incantations.

7. **AWS/ECS ops triage** — `lw aws ecs status` (service health),
   `lw aws logs tail` (live CloudWatch), `lw aws ecr push <service>` (emergency
   image deploy). Agents get vetted ops surface instead of raw `aws`.

8. **Direct Postgres access** — `lw db shell`, `lw db dump`, `lw db migrate`.
   Honor `--dry-run`/`--yes` on the destructive ones.

9. **Drift & dependency triage** — `lw cdn reconcile --dry-run` (bucket vs SST
   allowlist), `lw schema drift` / `lw schema reconcile`, and `lw health
   --json` (binaries, paths, DB, services). Run these before "why is X broken?".

10. **v_core orchestration primitives** — `lw agent spawn …` (sealed
    sub-session), `lw memory put/get` (persisted state), `lw msg send …`
    (gateway-mediated notifications), `lw v_core status`. The building blocks the
    resident orchestrator uses; reach for them when wiring autonomous flows.

Honorable mentions: `lw make <scope> <target>` (escape hatch to any Makefile
target), `lw audit run` (security/quality/drift scan), `lw codegen journeys`
(Playwright tests from journey YAML), `lw config get/set`.

---

## Gotchas

- **Distribution is tap-only.** Source changes don't reach any shell until a new
  tag ships (GoReleaser → tap → `brew upgrade lw`). Never `go install ./cmd/lw`
  or overwrite `/opt/homebrew/bin/lw` by hand — a stale shadowed binary will
  silently run old code in hooks. For local testing use `make build && ./lw …`.
- **Schema-driven vs hardcoded.** task/check/db/sprint/etc. are schema-dispatched
  and need lightwave-core's `commands.yaml`; scaffold/research/aws/worktree/etc.
  are always available. The startup warning tells you which mode you're in.
- **Schema additions need a paired lightwave-core PR.** Adding a `commands.yaml`
  entry without a Go handler (or vice-versa) is drift that `lw check schema`
  fails on. Land both together.
