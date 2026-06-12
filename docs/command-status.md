---
generated_at: "2026-06-11T23:53:17Z"
generator_version: dev
kind: command-status
source_commit: 11bada2
---

# `lw` command status — trust through subtraction

A release tag must mean something: **only commands verified to work end-to-end
are exposed.** Everything unverified is **decommissioned** — hidden from
`--help` and refusing to run (`lw <cmd>` returns an "offline" error) — until it
earns its way back. The registry lives in `internal/cli/command_status.go`; the
`command_surface_test.go` guard fails the build if a visible command isn't on
the verified list, so the surface can't silently regrow.

## How to bring a command back online

1. Make it work and **prove it** with an end-to-end / smoke test (real I/O, not
   just "doesn't panic").
2. Add its name to `VerifiedCommands` and a row to the **Active** table below
   (with the test that backs it).
3. Delete its row from `DecommissionedCommands`.
4. `go test ./...` green (the guard enforces the rest).

## Verified

| Command | Verified by |
|---|---|
| `version` | `TestVersion_Runs` (executes RunE) |
| `config` | `internal/config` unit tests (set/get/resolve roundtrip) |
| `health` | `TestHealth_ChecksBinaries` (binary probe) |
| `memory` | `internal/memory` unit tests (put/get/list/delete) |
| `worktree` | `internal/git` unit tests (worktree add/remove/prune) |
| `audit` | `TestAudit_DetectsPlantedSecret` (scanner finds a planted secret) |
| `scaffold` | `internal/blueprint` resolution + **real-engine** smoke (generates a file) |
| `ui` | `TestUIComponent_RejectsBadSpec` + shared scaffold path; `add`/`sync`: `internal/uisync` suite (copy+pin, force semantics, three-way table incl. no-base conflict, pin-advance-only-when-clean, git tag base extraction) |
| `research` | `internal/research` unit tests + live Perplexity call |
| `docs` | `internal/docsfactory/{spec_lint,docs_check}_test.go` — 6 spec-lint + 4 docs-check + 3 docs-sync subtests (known-good silent, known-bad fires, freshness vs HEAD, idempotent sync, dry-run no-write) |
| `codegen` | `internal/codegen/zodgen` tests (round-trip golden vs joelschaeffer-site registry, PropField parity, values_ref resolution) + `TestGenerateTypesSmoke`; `journeys` subcommand stays offline (below) |
| `help`, `completion` | cobra built-ins |

> Bar note: `version`/`config`/`health`/`memory`/`worktree`/`audit`/`ui` are
> backed by "runs + unit-tested core logic"; `scaffold`/`research` are backed by
> full end-to-end runs. Raising the lower bar to full e2e is tracked work.

## Decommissioned

| Command | Needs to come back online |
|---|---|
| `aws` | live AWS credentials + ECS; an e2e harness |
| `github` | `gh` CLI + platform repo + Postgres |
| `council` | Augusta service (localhost:9700) |
| `msg` | gateway service (localhost:9701) |
| `v_core` | `vcore` daemon binary (lightwave-sys) |
| `agent` | spawns real agent processes; `provision` is a stub |
| `make` | monorepo Makefiles (absent here) |
| `test` | monorepo make targets |
| `setup` | monorepo make targets |
| `cdn` | make + live S3 |
| `content` | make + Django stack |
| `drift` | make + Django stack |
| `email` | make + Django stack |
| `codegen journeys` | stale lightwave-core discovery path (legacy `packages/` layout); migrate to `src/schemas` + a verified journey fixture |
| `browser` | macOS osascript automation; flaky (audit verdict: drop) |
| `spec` | legacy parked tree pending schema merge — superseded by `lw docs spec-lint` for new in-repo spec/ work |
| `sst` | depends on `~/.brain` corpus state |

## Schema-dispatched domains (separate track)

`task`, `check`, `db`, `sprint`, `story`, `epic`, `infra`, `deploy`, `plan`,
`schema`, `compose`, `hooks`, `local` are attached from lightwave-core's
`commands.yaml` (the R1 stamp). They aren't reachable without that stamp, so
they're outside this hardcoded registry. When the stamp lands, the same trust
bar applies — each must be verified before it's allowed to stay exposed.
