# `lw task done` — manual fallback for the v_core merge-cleanup SOP

## What this command is

`lw task done <T-NNNN>` is the manual fallback for the four cleanup
steps documented in
`lightwave-media/docs/software/ddds/DDD-mvp-vertical-slice.md` §5.5
(`task_done.sop.yaml`). v_core normally runs these steps automatically
when the merged-PR webhook arrives. This command is what Joel reaches
for when v_core is offline or a SOP step has hung.

DDD §8 lists this as the recovery row for "Joel-merge webhook missed
(e.g. v_core was offline)".

## What it does

1. **Archive.** Tar+gzips `~/.lightwave/artefacts/<T-NNNN>/` into
   `~/.lightwave/archive/YYYY-MM/<T-NNNN>.tar.gz`.
2. **Cleanup.** Removes the artefacts directory and (if present) the
   worktree at `~/.lightwave/worktrees/<T-NNNN>/`. Tries
   `git worktree remove` first so the parent repo's worktree registry
   stays accurate; falls back to plain `RemoveAll` when the parent repo
   is gone.
3. **Close GitHub issue.** If `--issue <N>` is passed, shells out to
   `gh issue close <N> --reason completed` (with `--repo <R>` when
   `--repo` is set). Skipped with a warning otherwise — the manual
   fallback doesn't always know the GitHub issue number that v_core
   would have read off the webhook payload.
4. **Memory log.** Writes a YAML entry under
   `~/.lightwave/memory/sessions/<T-NNNN>-done-YYYY-MM-DD` recording
   the task id, timestamp, repo, issue, and archive path.

## What it does NOT do

- **Does not flip the markdown frontmatter status.** Use `lw task close
  <T-NNNN>` for that — markdown is the canonical record per
  `documentation-workflow.md` §7.
- **Does not write to Postgres.** The Phase A canonical record is the
  markdown file; the Postgres mirror catches up via `lw task index`.
- **Does not push commits or merge a PR.** The manual fallback assumes
  the PR is already merged on GitHub — that's the trigger condition.

## Usage

```
# Preview only
lw task done T-0042 --dry-run

# Interactive (default — prompts y/N)
lw task done T-0042

# Skip the prompt (CI / agent mode)
lw task done T-0042 --yes

# Close the linked GitHub issue too
lw task done T-0042 --issue 137 --repo lightwave-media/lightwave-cli --yes
```

## Flags

| Flag       | Purpose                                                                |
| ---------- | ---------------------------------------------------------------------- |
| `--dry-run`| Print what would happen, exit 0, no side effects.                      |
| `--yes`    | Skip the interactive confirmation prompt (per CLI destructive-cmd SOP).|
| `--issue`  | GitHub issue number to close. Omit to skip the gh-close step.          |
| `--repo`   | GitHub repo (e.g. `lightwave-media/lightwave-cli`) for `gh --repo`.    |

## Exit codes

| Code | Meaning                                                                  |
| ---- | ------------------------------------------------------------------------ |
| `0`  | All required steps succeeded (or dry-run printed plan).                  |
| `1`  | Required step failed: bad task id, missing artefacts dir, archive write. |

The worktree-remove and `gh issue close` steps are best-effort —
warnings are printed but the command still returns 0 when the archive
and memory-log steps succeeded. This matches the SOP intent: clean up
what's there, never strand the operator.
