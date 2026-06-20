# release-cycle — hourly release-train gate (ADR-0032)

The 1-hour cron half of the release-train pipeline. Each hour it runs the
release-engineer merge gate in **report mode** for every release-candidate repo
and writes the result to a log. Nothing is merged automatically until you
opt in (see "Enabling auto-merge").

## What it does

`release-cycle.sh` reads the `[<repo>]` sections of
`~/.lightwave/config/releases.toml` (the release-pin source of truth) and, for
each, runs:

```
lw release merge <repo> --dry-run
```

which lists the open PRs that are **eligible** — mergeable, not draft, CI green,
and covered by an active CTO sign-off (`lw release sign-off <repo> --by <cto>`).
A repo with no sign-off recorded reports a closed gate and merges nothing.

## Prerequisite

`lw release` ships via the Homebrew tap. Until a tag containing the `release`
domain is published and `brew upgrade lw` has run, `lw release merge` is
unavailable and the cycle will log a gate-check failure every hour. **Install
the LaunchAgent only after the verb is on your `lw`** (`lw release merge --help`
resolves).

## Install (operator)

```bash
mkdir -p ~/.lightwave/logs
REPO="$HOME/dev/lightwave-cli"   # or wherever this checkout lives
sed -e "s#__REPO__#$REPO#g" -e "s#__HOME__#$HOME#g" \
  "$REPO/dev/release-cycle/com.lightwave.release-cycle.plist" \
  > ~/Library/LaunchAgents/com.lightwave.release-cycle.plist
launchctl load ~/Library/LaunchAgents/com.lightwave.release-cycle.plist
```

Logs land in `~/.lightwave/logs/release-cycle.log`. Run it once by hand first:

```bash
mise run release-cycle   # or: bash dev/release-cycle/release-cycle.sh
```

## Enabling auto-merge

The cycle is dry-run by design — the CTO sign-off gates *whether* a repo's PRs
may merge, but a human still watches the report before the swarm trusts it to
act. Once you trust the flow, change `--dry-run` to `--yes` in
`release-cycle.sh`. The gate (sign-off + green CI) still applies; `--yes` only
removes the "report instead of act" guard.

## Uninstall

```bash
launchctl unload ~/Library/LaunchAgents/com.lightwave.release-cycle.plist
rm ~/Library/LaunchAgents/com.lightwave.release-cycle.plist
```
