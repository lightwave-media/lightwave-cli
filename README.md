# lightwave-cli

[![CI](https://github.com/lightwave-media/lightwave-cli/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/lightwave-media/lightwave-cli/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/lightwave-media/lightwave-cli)](https://goreportcard.com/report/github.com/lightwave-media/lightwave-cli)
[![Go Reference](https://pkg.go.dev/badge/github.com/lightwave-media/lightwave-cli.svg)](https://pkg.go.dev/github.com/lightwave-media/lightwave-cli)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

`lw` is the LightWave platform's deterministic CLI surface — the single
front door agents and operators use instead of raw vendor tooling. It
mediates between:

- the **agile artifact stamp** (`lightwave-core` SST) that declares the
  shapes everything conforms to,
- the **platform's PostgreSQL** and its API, where epics, stories,
  sprints and tasks actually live, and
- **vendor APIs** — AWS and GitHub.

Every operation here is a tool an agent can call deterministically
instead of hallucinating prose. Repo-quality discipline — ratcheted
golangci-lint, hook gates, JUnit test reports, an OS+arch build matrix,
and a schema-drift gate — keeps that surface honest across every consumer.

## Why a CLI and not a prompt

An agent fails a step only when the model gets it wrong **and** nothing
catches it. Model capability lowers the first probability; `lw` lowers
the second — compilers, schema validation, drift gates and tests are
cheap, deterministic, and identical on every run.

That is the whole argument for this repo. It is not competing with model
capability; it is what makes a cheaper model a rational choice for a
given step. The corollary is that a check which reports success without
actually covering the surface is worse than no check: it lowers the
apparent failure rate and not the real one. Gates here are expected to
prove their own coverage.

## Install

```sh
mise run install
```

Builds from source and installs to `~/.local/bin/lw` in about three
seconds. That directory precedes `/opt/homebrew/bin` on PATH, so the
build you just made is the `lw` your shell and the project hooks
resolve. The task verifies exactly that and warns if something still
shadows it.

`lw version` reports `git describe` output — e.g. `3.14.0-5-gf91df3c-dirty`
— so a source build is always distinguishable from a clean tagged
release. Override the destination with `LW_INSTALL_DIR`.

**Why not Homebrew?** `lw` is the command spine of this machine's shell.
Moving a binary from this repo onto this machine should not require a
tag, a CI run, a GoReleaser pipeline, a separate tap repo and a
`brew upgrade`. That chain exists to serve other people's machines. The
`lightwave-media/homebrew-tap` repo was deleted on 2026-09-16 and the
`brews:` block is gone from `.goreleaser.yaml`; tagged releases still
publish cross-platform tarballs to the GitHub Releases page.

**Still don't `go install ./cmd/lw`.** It writes `~/go/bin/lw`, which is
*behind* `/opt/homebrew/bin` on PATH — so if a Homebrew `lw` is still
installed it wins and you will believe your change is live when it
isn't. `mise run install` exists to make that failure impossible.

## Quickstart

```sh
# Bring up the local platform stack (preflight-gated)
lw local up

# Run the code-quality and drift checks CI runs
lw check

# Create a task end-to-end — the title is positional
lw task create "describe the change" --type=fix --label=cli --assign=<github-user>

# Explore the surface
lw --help
lw <command> --help
```

Each top-level command owns a group of verbs (`lw task --help`,
`lw db --help`, `lw check --help`). An unknown verb exits non-zero
rather than printing help and reporting success — **including with
`--help` on the line**, which is how callers ask whether a command
exists. Until #426 that form exited 0 and printed the parent's help, so
probing for a verb returned the same answer whether it existed or not.

> **Note on wording.** These groups are spelled `domains:` in the
> `commands.yaml` stamp and in `internal/cli/dispatcher.go`. That is a
> CLI-internal sense of the word and is unrelated to createOS **Life
> Domains**. This README says *top-level command* to keep the two
> apart; the schema key is tracked for rename separately.

### Exposed today

45 top-level commands ship in the current build:

```
audit      check      codegen    completion compose    config
context    create     db         deploy     docs       epic
factory    failure    git        health     help       home
hooks      infra      issue      kickoff    lineage    lint
local      mcp        memory     plan       process    release
research   runbook    scaffold   schema     scrum      self
session    site       sprint     story      task       ui
version    voice      worktree
```

Not everything in the source tree is exposed. Commands whose backing
stack is gone are decommissioned in `internal/cli/command_status.go` —
hidden from `--help` and refusing to run — so a release tag never
advertises a command that cannot work. Regenerate the list above with
`lw --help`.

## The surface is schema-driven

The command surface is declared in `lightwave-core` at
`src/schemas/interfaces/cli/commands.yaml` and dispatched at startup by
`internal/cli/dispatcher.go`. Two invariants hold in both directions:

- **no schema entry without a registered Go handler** — otherwise the
  subcommand is reachable but unimplemented;
- **no registered handler without a schema entry** — otherwise the
  command is unreachable from the dispatcher's tree.

`lw check schema` enforces both. It is **a required CI gate on every
PR**, not a build-time check — drift blocks the merge. Run it yourself
before pushing anything that adds, renames, or removes a handler or a
`commands.yaml` entry:

```sh
LW_CHECK_SCHEMA_STRICT=1 ./bin/lw check schema
```

Exit 1 on drift; without the env var the same report prints and exits 0.

Commands still under construction carry `_status: in_development` in the
stamp: the dispatcher hides them and the drift gate excludes them, so a
surface can be declared before its handlers exist without publishing a
command nobody can invoke.

## Releasing

**The tag is the version.** Push a tag and the org release plane does the
rest — GoReleaser builds the binaries, creates the GitHub release, and
pushes the formula update to the tap.

```sh
lw release tag --dry-run   # compute the next SemVer, change nothing
lw release tag             # tag and push it
```

The next version is derived from conventional commits since the last tag
(`!` or `BREAKING CHANGE` → major, `feat` → minor, otherwise patch).
Tagging is refused unless `HEAD` is `origin/main`.

## Contributing

Repo conventions live in **[AGENTS.md](AGENTS.md)** — it is canonical and
read by every agent surface; `CLAUDE.md` is a thin pointer to it. It
covers test patterns, git discipline, the `lw check` subcommand
requirements, the destructive-command `--dry-run`/`--yes` standard, the
release path, and the push circuit breaker. Read it before opening a PR.

**Done means** `mise run ci` green plus a test that pins the change.

## License

MIT — see [LICENSE](LICENSE).
