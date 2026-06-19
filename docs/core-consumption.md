# Consuming lightwave-core from Go

lightwave-cli and lightwave-platform load SST **schemas** from the embedded
Go binding (`github.com/lightwave-media/lightwave-core/bindings/go`), not from
a sibling filesystem checkout. That matches Terragrunt’s “pin a git ref” model,
but at **build time** via `go:embed`.

## Default (embedded)

```go
import "github.com/lightwave-media/lightwave-cli/internal/corebind"

raw, err := corebind.ReadSchema("interfaces/cli/commands")
ver := corebind.Version() // e.g. "0.5.1"
```

The version in `go.mod` is the SST contract. Bump it when core releases.

`lw version` prints `core_schema` alongside the binary version.

## Live schema editing

While changing schemas in `lightwave-core/src/schemas/` before the next release:

```bash
export LW_CLI_LIVE_SCHEMAS=1
export LW_LIGHTWAVE_CORE=~/dev/lightwave-core   # optional if using ~/dev layout
lw check schema
```

Platform backend: `LW_PLATFORM_LIVE_SCHEMAS=1` with the same core root.

## Local binding work

`go.mod` carries a `replace` to `../lightwave-core/bindings/go` for the flat
`~/dev` workspace. After schema changes, run in core:

```bash
cd ~/dev/lightwave-core && ./scripts/sync-go-binding.sh
```

## Private module / dropping replace (CI + releases)

lightwave-core is private. To fetch the tagged module instead of `replace`:

```bash
go env -w GOPRIVATE=github.com/lightwave-media/*
export GIT_TERMINAL_PROMPT=0
# CI: add secret LIGHTWAVE_CORE_TOKEN with Contents:Read on lightwave-core
git config --global url."https://${LIGHTWAVE_CORE_TOKEN}@github.com/".insteadOf "https://github.com/"
go get github.com/lightwave-media/lightwave-core/bindings/go@v0.5.1
```

Then remove the `replace` line from `go.mod`. CI checkouts use the `dev/`
layout (see `base-test.yml`) until the token path is provisioned.

Release tags cut **two** refs on the same commit: `v0.5.1` and
`bindings/go/v0.5.1`.

## Blueprints (filesystem)

Gruntwork boilerplate trees under `src/boilerplate/blueprints/` are **not** in
the Go binding.

| Env | Purpose |
|---|---|
| `LW_BLUEPRINTS_DIR` | Override blueprint library path |
| `LW_BLUEPRINTS_REF` | Reserved — Terragrunt-style git pin (not implemented) |

Default resolution: `$LW_LIGHTWAVE_CORE/src/boilerplate/blueprints` or
`~/dev/lightwave-core/src/boilerplate/blueprints`.

## Platform API

`GET /system/schemas/{key}` returns SST metadata (required fields, title) from
the embedded binding. `GET /health` includes `core_schema`.

## Release environments (mise)

Version pins and feature flags per environment live in
`lightwave-core/releases.toml`. mise tasks wrap them:

```bash
mise run env:production    # show stable pins (embedded SST, no replace)
mise run env:local         # live schemas + go replace
mise run pin:production    # align go.mod to tagged core module
mise run ci:local            # CI with local profile flags
```

Fleet-wide from `~/dev`: `mise run env:production`.

See `lightwave-core/docs/Guides/release-environments.md`.

## Migration status

| Area | Status |
|---|---|
| `commands.yaml` / dispatcher | embedded |
| docsfactory governance | embedded |
| `lw version` core_schema | done |
| codegen journeys | requires core checkout (`corebind.JourneysDir`) — journeys not stamped yet |
| strategy alignment (`github pick`) | pending `policy/governance/strategy_alignment` stamp |
| blueprints / scaffold | filesystem + `LW_BLUEPRINTS_*` |

See `lightwave-core/docs/Guides/consume.md` for the release-train doctrine.
