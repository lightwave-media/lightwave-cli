# lightwave-cli

[![CI](https://github.com/lightwave-media/lightwave-cli/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/lightwave-media/lightwave-cli/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/lightwave-media/lightwave-cli)](https://goreportcard.com/report/github.com/lightwave-media/lightwave-cli)
[![Go Reference](https://pkg.go.dev/badge/github.com/lightwave-media/lightwave-cli.svg)](https://pkg.go.dev/github.com/lightwave-media/lightwave-cli)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

`lw` is the LightWave platform's deterministic CLI surface — the
middleman between local-first agent runtimes (ZeroClaw),
canonical multi-tenant Django storage, the agile artifact stamp
(`lightwave-core` SST), and vendor APIs (AWS, GitHub, Paperclip).

Every domain operation that lives here is a tool agents can call
deterministically instead of hallucinating prose. Repo-quality
discipline (linting, ratcheted golangci-lint, hook gates, JUnit
test reports, OS+arch build matrix) keeps generated code clean
across every consumer.

## Install

```sh
brew install lightwave-media/tap/lw
```

Tagged releases ship via the `lightwave-media/homebrew-tap`
repository — `brew upgrade lw` picks up the latest pinned build.
Don't `go install ./cmd/lw` to "use the new version locally"; the
tap binary at `/opt/homebrew/bin/lw` will shadow the `~/go/bin/lw`
build, and project hooks shell out to the PATH-resolved `lw`. See
[CLAUDE.md](CLAUDE.md) → *Updating `lw` — Ship Via Tap, Not `go install`*
for the long version.

## Quickstart

```sh
# Bring up the local LightWave platform stack
lw dev start

# Run code-quality + drift checks (mirrors CI)
lw check

# Run the test suite (Django + Go)
lw test

# Create a new task end-to-end (createOS + Paperclip + GitHub fan-out)
lw task create --title="describe the change" --type=fix --prd-ref=<slug>

# Inspect schema-driven CLI surface
lw --help
lw <domain> --help
```

`lw --help` enumerates every domain at the top level. Each domain
exposes its own subcommand surface — `lw task --help`, `lw db --help`,
`lw check --help`, and so on. The surface is driven by the SST schema
at `packages/lightwave-core/lightwave/schema/definitions/config/cli/commands.yaml`;
drift between schema entries and registered Go handlers is caught at
build time by `lw check schema`.

## Contributing

Repo-local conventions are documented in [CLAUDE.md](CLAUDE.md) —
test patterns, git discipline, the `lw check` requirements, the
release pipeline, the push circuit breaker, and the bash-guard's
CLI-first enforcement. Read it before opening a PR.

Cross-repo guardrails (formatting, secret management, validity
contracts) live in [`~/dev/lightwave-media/CLAUDE.md`](../../CLAUDE.md)
and [`~/.claude/CLAUDE.md`](https://github.com/lightwave-media/claude-config).

## License

MIT — see [LICENSE](LICENSE).
