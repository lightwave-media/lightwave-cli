# lightwave-cli v2 empty-tree

Major empty-tree rewrite branch (`v2/empty-tree`). Old tags on `main` stay.

## Verbs

| Verb | Role |
|------|------|
| `lw home` | Render `lightwave-home` blueprint |
| `lw schema` | Load schemas from `github.com/lightwave-media/lightwave-core` |
| `lw check` | Local CI-parity (`mise run ci`) |
| `lw mcp serve` | MCP listener (delegates until native) |

No `internal/corestamp`. Schemas come from the core Go module only.

## Module

```go
require github.com/lightwave-media/lightwave-core v0.0.0
```

Local `replace` points at a checkout until a GitHub tag publishes the embed.
