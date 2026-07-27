"""
gh_notif_to_event.py — GitHub notifications → structured events.

Reads `gh api notifications` JSON from stdin (or fetches with --fetch),
dedupes by fingerprint (repo|workflow|title), and emits a JSON array of
events to stdout. First stage of the remote-CI feedback loop:

    gh api notifications --paginate | python3 dev/gh_notif_to_event.py
    python3 dev/gh_notif_to_event.py --fetch

Exit code is always 0 so the pipeline never breaks on parse failures.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from collections import defaultdict
from datetime import datetime, timezone
from typing import Any

# Workflow name extracted from notification titles like:
#   "github-org-sync workflow run failed for main branch"
#   ".github/workflows/github-org-sync.yml workflow run failed for main branch"
_WORKFLOW_FROM_TITLE = re.compile(
    r"^(?:\.github/workflows/)?(.+?)(?:\.yml)?\s+workflow\s+run",
    re.IGNORECASE,
)


def _fetch_notifications() -> list[dict[str, Any]]:
    try:
        raw = subprocess.check_output(  # noqa: S603
            ["gh", "api", "notifications", "--paginate"],
            stderr=subprocess.DEVNULL,
            text=True,
        )
    except Exception as exc:  # noqa: BLE001
        print(f"WARNING: gh api notifications failed: {exc}", file=sys.stderr)
        return []
    if not raw.strip():
        return []
    # --paginate may concatenate multiple JSON arrays; accept one array or NDJSON-ish concat
    try:
        data = json.loads(raw)
        if isinstance(data, list):
            return data
        return [data]
    except json.JSONDecodeError:
        # Multiple arrays concatenated by paginate in some gh versions
        items: list[dict[str, Any]] = []
        decoder = json.JSONDecoder()
        idx = 0
        raw_s = raw.strip()
        while idx < len(raw_s):
            while idx < len(raw_s) and raw_s[idx].isspace():
                idx += 1
            if idx >= len(raw_s):
                break
            obj, end = decoder.raw_decode(raw_s, idx)
            if isinstance(obj, list):
                items.extend(obj)
            elif isinstance(obj, dict):
                items.append(obj)
            idx = end
        return items


def _workflow_from_title(title: str) -> str:
    m = _WORKFLOW_FROM_TITLE.match(title.strip())
    if m:
        return m.group(1).strip()
    # Fall back: first token-ish segment before " workflow"
    if " workflow" in title.lower():
        return title.split(" workflow", 1)[0].strip()
    return title.strip() or "unknown"


def _classify(notif: dict[str, Any]) -> dict[str, Any]:
    reason = notif.get("reason") or "other"
    subject = notif.get("subject") or {}
    title = subject.get("title") or ""
    stype = subject.get("type") or ""
    repo = (notif.get("repository") or {}).get("full_name") or "unknown"
    updated = notif.get("updated_at") or ""
    thread_id = str(notif.get("id") or "")
    subject_url = subject.get("url") or ""
    # CheckSuite / workflow failure notifications
    if reason == "ci_activity" or stype in {"CheckSuite", "CheckRun"}:
        workflow = _workflow_from_title(title)
        event_type = "gh.notif.ci_failed"
        signal = "remote_workflow"
    elif reason == "review_requested" or stype == "PullRequest":
        workflow = "pull_request"
        event_type = "gh.notif.review_requested"
        signal = "review_requested"
    else:
        workflow = stype or reason or "other"
        event_type = "gh.notif.other"
        signal = "other"

    fingerprint = f"{repo}|{workflow}|{title}"
    return {
        "event_type": event_type,
        "signal": signal,
        "repo": repo,
        "workflow": workflow,
        "title": title,
        "fingerprint": fingerprint,
        "updated_at": updated,
        "thread_id": thread_id,
        "subject_url": subject_url,
        "reason": reason,
        "subject_type": stype,
    }


def _dedupe(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    buckets: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in rows:
        buckets[row["fingerprint"]].append(row)

    events: list[dict[str, Any]] = []
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    for fingerprint, group in buckets.items():
        group_sorted = sorted(group, key=lambda r: r.get("updated_at") or "")
        latest = group_sorted[-1]
        thread_ids = [r["thread_id"] for r in group_sorted if r.get("thread_id")]
        events.append(
            {
                "event_type": latest["event_type"],
                "timestamp": now,
                "repo": latest["repo"],
                "workflow": latest["workflow"],
                "title": latest["title"],
                "fingerprint": fingerprint,
                "signal": latest["signal"],
                "occurrence_count": len(group),
                "latest_updated_at": latest.get("updated_at") or now,
                "thread_ids": thread_ids,
                "subject_url": latest.get("subject_url") or "",
                "run_url": "",  # filled by signal.sh when resolvable
                "reason": latest.get("reason") or "",
            }
        )

    # Oldest first so chronic failures surface before brand-new noise
    events.sort(key=lambda e: e.get("latest_updated_at") or "")
    return events


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--fetch",
        action="store_true",
        help="Fetch notifications via `gh api` instead of reading stdin",
    )
    args = parser.parse_args()

    if args.fetch:
        notifs = _fetch_notifications()
    else:
        raw = sys.stdin.read()
        if not raw.strip():
            notifs = []
        else:
            try:
                data = json.loads(raw)
                notifs = data if isinstance(data, list) else [data]
            except json.JSONDecodeError as exc:
                print(f"WARNING: invalid JSON on stdin: {exc}", file=sys.stderr)
                notifs = []

    classified = [_classify(n) for n in notifs if isinstance(n, dict)]
    events = _dedupe(classified)
    print(json.dumps(events, indent=2))


if __name__ == "__main__":
    main()
