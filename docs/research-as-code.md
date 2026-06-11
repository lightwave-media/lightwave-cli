# `lw research` + Search-as-Code (north-star)

## What ships now (MVP)

`lw research <query>` — a Perplexity-backed research primitive. One synchronous
"ask → cited answer" call, scriptable and agent-callable (unlike the interactive
`deep-research` Claude skill, and distinct from `lw council` / Augusta).

- Key: AWS SSM `/lightwave/prod/PERPLEXITY_API_KEY` (SecureString, source of
  truth); `PERPLEXITY_API_KEY` env overrides for dev/CI.
- Models: `sonar-pro` (default, fast) or `sonar-deep-research` via `--deep`.
- Flags: `--json`, `-o/--output`, `--recency`, `--domains`, `--system`, `--model`.
- Code: `internal/research/` (thin Perplexity client) + `internal/cli/research.go`.
  Wired hardcoded in `root.go` (transitional, like `agent`/`memory`/`msg`);
  schema-register once `commands.yaml` lands.

```
lw research "what changed in the EU AI Act in 2026?"
lw research --deep "survey agentic retrieval architectures" -o report.md
lw research --json "latest Go 1.24 release notes" | jq .citations
```

## North-star: Search-as-Code (SaC)

Source: Perplexity, *Rethinking Search as Code Generation*
(https://research.perplexity.ai/articles/rethinking-search-as-code-generation).

**Thesis:** instead of calling a fixed search API that returns processed
results, the model writes code in a secure sandbox to orchestrate low-level
search *primitives* (retrieve / rank / filter / aggregate) — hundreds–thousands
of operations per turn, with persistent cross-turn state. Reported wins on
"wide research" tasks (≈2.5× on WANDR) and large token reductions vs. fixed
pipelines.

**Three-layer stack (theirs):** models generate Python → secure compute
sandbox executes with persistent filesystem state → an Agentic Search SDK
exposes composable primitives (not end-to-end APIs), taught via Agent Skills.

**How `lw research` grows toward it (not built yet):**
1. **Primitives, not just one call** — factor the client into composable ops
   (`search`, `fetch`, `rank`, `dedup`, `summarize`) the way `internal/media`
   will factor `inventory`/`classify`.
2. **Orchestration surface** — `lw research plan <goal>` emits a research
   pipeline; `lw research run` executes it, persisting intermediate state under
   `~/.lightwave/research/<id>/` (mirrors the `agent`/`memory` state pattern).
3. **Sandboxed codegen (the SaC step)** — let an agent assemble a pipeline as
   code over the primitives, executed in a sandbox. This is the ambitious phase;
   gate it behind explicit opt-in and the `--dry-run`/`--yes` standard.

MVP is deliberately small so steps 1–3 are additive, not a rewrite.
