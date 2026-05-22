# internal/audits/

Tombstoned-violation snapshots captured at the time a new quality gate was
adopted. **Read-only references** — these files are not consumed by CI; they
exist so future maintainers can measure progress against the day-zero state.

## `golangci-baseline.json`

Captured 2026-05-22 during PR1 (golangci-lint v2.5.0 adoption from the
gruntwork-io/terragrunt reference config).

Snapshot: **2612 issues across 22 linters** at adoption time.

| Linter         | Count |
| -------------- | ----: |
| wsl_v5         |  1732 |
| mnd            |   173 |
| perfsprint     |   153 |
| paralleltest   |   136 |
| staticcheck    |    96 |
| gocritic       |    75 |
| govet          |    68 |
| noctx          |    56 |
| errcheck       |    40 |
| testpackage    |    22 |
| prealloc       |    12 |
| nilerr         |    10 |
| goconst        |     9 |
| dupl           |     8 |
| gosmopolitan   |     5 |
| errchkjson     |     4 |
| errorlint      |     4 |
| unparam        |     4 |
| contextcheck   |     2 |
| exhaustive     |     1 |
| ineffassign    |     1 |
| unused         |     1 |

CI does not enforce this baseline directly (golangci-lint v2 has no native
`--baseline` flag). Instead, CI runs:

```
golangci-lint run --new-from-merge-base=origin/main
```

— which only flags violations in lines changed by the PR. The baseline file
above is the day-zero count; PRs that ratchet existing files clean should
re-snapshot it and lower the table.

To re-snapshot:

```
mkdir -p internal/audits
golangci-lint run \
  --output.text.path= \
  --output.json.path=internal/audits/golangci-baseline.json \
  --issues-exit-code 0 \
  ./...
```
