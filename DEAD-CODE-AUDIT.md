# Dead Code Audit: lightwave-cli

**Updated**: 2026-05-12 — US-006 sweep (EB-001 v_core resident orchestrator)
**Original**: 2026-03-11

## Summary

The 2026-03-11 audit identified ~550 lines of dead code across native FFI bindings, scaffolding packages, and an unused import. The May 2026 sweep verified each finding against current `main`:

- §1 (native FFI) — **REMOVED** between 03-11 and 05-12; `internal/native/` no longer exists.
- §2.1 (`internal/agent/`) — **WIRED IN** by US-004 (lightwave-cli#33). The package is now live; this section is closed.
- §2.2 (`internal/tmux/`) — **REMOVED**; directory no longer exists.
- §3 (redundant implementations) — **N/A** after the native FFI removal.
- §4 (unused `strings` import marker) — **STILL PRESENT**; fixed in this PR.
- §5 (clean findings) — superseded by ongoing tidy work.

## §6 — US-006 finding: no `lw notion *` subcommands to strip

The Phase 3 plan (`~/.claude/plans/2026-05-12-vcore-resident-orchestrator-mvp.md` §3) calls for stripping `lw notion *` Notion subcommands and recording the strip here.

**Finding:** no such subcommands exist in this repo. Never have. `lw --help` has no `notion` entry; `cmd/lw/main.go` registers none.

Notion's presence is purely a **legacy database column name**: `createos_task.notion_id` (and matching columns on `createos_story`, `createos_sprint`, `createos_epic`). The CLI overloads this column for arbitrary external refs — e.g. `"gh-52"` for GitHub Issue #52 (see `internal/cli/task_create.go:241-245`, `internal/cli/github.go:282-294`). The Go wrappers (`db.GetTaskByNotionID`, `db.UpdateTaskNotionID`, `db.TaskCreateOptions.NotionID`) follow the column name.

Renaming `notion_id` → `external_ref` requires:

1. A Django migration in `lightwave-platform` (column rename, index rename).
2. Sync update to lightwave-cli's Go column references after the migration lands.
3. Sync update to any direct-SQL consumers (lightwave-core, lightwave-platform).

That work is **not in scope for US-006** — it belongs in EB-005 (Phase B storage) when the Postgres canonical-store work happens. Tracking handoff: file as a follow-up issue when EB-005 starts.

**Closed:** US-006's "strip Notion CLI surface" deliverable is vacuously satisfied; the renaming work is tracked under EB-005.

## §4 (still present) — unused `strings.TrimSpace` suppression in task.go

The line `var _ = strings.TrimSpace` at `internal/cli/task.go:757` was added when other `strings` uses were removed, to keep the import alive. The file now legitimately uses `strings.ReplaceAll`, `strings.Builder`, `strings.Contains`, `strings.TrimSpace`, `strings.Split` (lines 270, 388, 399-403, 428-429). The suppression is no longer needed.

**Fixed** in this PR.

## Estimated impact

- 1 trivial dead line removed (`task.go:757`)
- 4 stale audit sections retired (§§1-3, audit history preserved above)
- 1 forward-looking section added (§6)

The repo is materially cleaner than the 2026-03-11 baseline — most of the ~550-line dead-code estimate has already shipped.
