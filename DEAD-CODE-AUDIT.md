# Dead Code Audit: lightwave-cli

**Date**: 2026-03-11
**Auditor**: Claude Code (automated sweep)

## Summary

Go CLI tool. Relatively clean codebase. Main issues: 11 unused native FFI bindings (CLI uses exec.Command fallbacks instead), 2 scaffolding packages (agent/tmux) with no CLI commands wired to them, and 1 unused import.

---

## 1. Unused Native FFI Bindings (~150 lines)

**File**: `internal/native/native.go`

These exported functions wrap the Rust lightwave-sys C FFI but are never called — the CLI uses exec.Command equivalents instead:

| Function | Line | CLI Uses Instead |
|---|---|---|
| `BrowserNavigate` | L302 | AppleScript |
| `BrowserScreenshot` | L314 | screencapture command |
| `BrowserClick` | L329 | AppleScript System Events |
| `BrowserType` | L338 | AppleScript keystroke |
| `ListProcesses` | L365 | `ps` command |
| `GetProcessInfo` | L380 | `ps` command |
| `KillProcess` | L395 | `os.FindProcess` |
| `RunShell` | L411 | `exec.Command` |
| `ReadFile` | L433 | `os.ReadFile` |
| `WriteFile` | L453 | `os.WriteFile` |
| `ListDir` | L476 | `os.ReadDir` |

**Confidence**: HIGH — zero calls found across entire codebase.

**Action**: Remove all 11 functions. The CLI already works without them. If native perf is needed later, re-add selectively.

---

## 2. Scaffolding Packages — Not Wired to CLI (~400 lines)

Two complete packages exist but no CLI command invokes them:

### 2.1 `internal/agent/` + `internal/agent/manager/`
- `Load`, `ListAll`, `Save`, `SetState`, `BaseDir`, `ReposDir`
- `NewManager`, `Spawn`, `List`, `Kill`
- `GenerateName`, `TmuxSessionName`, `BranchName` (names subpackage)
- Complete agent lifecycle system (worktrees, tmux sessions, Claude CLI invocation)
- Zero CLI commands call any of this

### 2.2 `internal/tmux/`
- `New`, `NewSession`, `KillSession`, `SendKeys`, plus 10+ session management methods
- Only used by agent/manager — which itself is unused

**Confidence**: MEDIUM — these may be planned features (`lw agent spawn`, etc.)

**Action**: Either wire up CLI commands or remove. If keeping, add a TODO with timeline.

---

## 3. Redundant Implementations (5 Feature Areas)

The CLI implements features twice — once via exec.Command (used), once via native FFI (unused):

| Feature | Used (CLI) | Unused (Native) |
|---|---|---|
| Window management | AppleScript + osascript | FFI: ListWindows, FocusWindow, CaptureWindow |
| Clipboard | pbpaste/pbcopy | FFI: GetClipboardText, SetClipboardText |
| Notifications | osascript -e | FFI: SendNotification |
| AppleScript | exec.Command("osascript") | FFI: RunAppleScript |
| Process mgmt | ps + os.FindProcess | FFI: ListProcesses, KillProcess |

**Action**: Pick one implementation per feature, delete the other.

---

## 4. Unused Import

**File**: `internal/cli/task.go:7`
- `"strings"` imported but only used in suppression line: `var _ = strings.TrimSpace`
- **Action**: Remove import and suppression line.

---

## 5. Clean Findings

- All go.mod dependencies are actively used (`go mod tidy` clean)
- All unexported helper functions are properly scoped and called
- All CLI commands are wired and functional
- No commented-out code blocks

---

## Estimated Impact
- ~150 lines of unused FFI bindings
- ~400 lines of scaffolding code (agent/tmux)
- 1 trivial unused import
