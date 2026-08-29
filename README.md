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

Every domain operation here is a tool an agent can call deterministically
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
brew install lightwave-media/tap/lw
```

Tagged releases ship via the `lightwave-media/homebrew-tap` repository —
`brew upgrade lw` picks up the latest pinned build.

Don't `go install ./cmd/lw` to "use the new version locally": the tap
binary at `/opt/homebrew/bin/lw` shadows `~/go/bin/lw` on PATH, and
project hooks shell out to the PATH-resolved `lw`. You will believe your
change is live when it isn't. See [AGENTS.md](AGENTS.md) →
*Updating `lw` — Ship Via Tap, Not `go install`*.

## Quickstart

```sh
# Bring up the local platform stack (preflight-gated)
lw local up

# Run the code-quality and drift checks CI runs
lw check

# Create a task end-to-end — the title is positional
lw task create "describe the change" --type=fix --prd=<path/to/prd.md>

# Explore the surface
lw --help
lw <domain> --help
```

`lw --help` lists the domains that are currently exposed; each has its
own verbs (`lw task --help`, `lw db --help`, `lw check --help`). An
unknown verb exits non-zero rather than printing help and reporting
success.

Not every domain in the source tree is exposed. Commands verified to
work end-to-end ship; commands whose backing stack is gone are
decommissioned in `internal/cli/command_status.go` and hidden, so a
release tag never advertises a command that cannot run.

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

Domains still under construction carry `_status: in_development` in the
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
