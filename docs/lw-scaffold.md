# `lw scaffold` — the factory front door

`lw` resolves a blueprint by **name** from the canonical lightwave-core blueprint
library and shells out to the Gruntwork **boilerplate** engine. lw does not
template anything itself — it wraps the engine.

## Commands

```
lw scaffold <blueprint> -o <dir> [--var k=v]... [--var-file f]... [--no-hooks]
lw ui component <category>/<Name> [-o <dir>]      # sugar over scaffold react-component
```

- `lw scaffold` always runs the engine `--non-interactive`; every variable
  comes from `--var`/`--var-file` (blueprint defaults fill the rest). Zero
  prompts — safe for CI/agents.
- `lw ui component application/DataTable` ⇒
  `lw scaffold react-component --var category=application --var component_name=DataTable`,
  defaulting output to `<lightwave_root>/packages/lightwave-ui/src/components`.

## Blueprint discovery (in order)

1. `--blueprints-dir <path>`
2. `$LW_BLUEPRINTS_DIR`
3. `<lightwave_root>/src/boilerplate/blueprints` (config `paths.lightwave_root`)

A blueprint is a directory under that library containing a `boilerplate.yml`.
Missing library or missing blueprint produces a clear error.

## Engine

Found via `PATH` (`boilerplate`), else `~/go/bin/boilerplate`. Install:
`go install github.com/gruntwork-io/boilerplate@latest` (use a **tagged**
release — a `development` build fails blueprints that declare `required_version`).

## Downstream usage

A marketing section in joelschaeffer-site (once the `site-section` blueprint is
finalized in lightwave-core):

```
lw scaffold site-section \
  -o src/components/marketing \
  --var category=marketing --var component_name=PricingTable
```

A lightwave-ui component:

```
lw ui component application/DataTable
```

## Notes

- `lw` is distributed via the Homebrew tap, **not** `go install` (AGENTS.md).
  `make build` produces a local `./lw` for testing; ship changes by tagging a
  release (GoReleaser → tap), then `brew upgrade lw`. Do not overwrite
  `/opt/homebrew/bin/lw` by hand.
- Startup degrades gracefully when the schema `commands.yaml` stamp is absent:
  hardcoded commands (`version`, `scaffold`, `ui`, `aws`, …) work; a one-line
  warning notes that schema-dispatched domains are unavailable. `lw check schema`
  remains the contract gate.
- The `site-section` blueprint targets a single `.tsx` (PascalCase export, props
  type, nav handling) + a Zod schema entry — a different shape than the
  lightwave-ui `react-component` blueprint. `lw scaffold <any-blueprint>` is
  generic, so no CLI change is needed when it lands.
