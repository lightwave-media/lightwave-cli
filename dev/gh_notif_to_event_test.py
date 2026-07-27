#!/usr/bin/env python3
"""Pin gh_notif_to_event dedupe: many identical org-sync rows → one fingerprint."""

from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "dev" / "gh_notif_to_event.py"


def _sample_notif(repo: str, title: str, nid: str, updated: str) -> dict:
    return {
        "id": nid,
        "reason": "ci_activity",
        "updated_at": updated,
        "repository": {"full_name": repo},
        "subject": {
            "title": title,
            "type": "CheckSuite",
            "url": "https://api.github.com/repos/x/y/check-suites/1",
        },
    }


def main() -> int:
    payload = [
        _sample_notif(
            "lightwave-media/lightwave-cli",
            "github-org-sync workflow run failed for main branch",
            "1",
            "2026-07-23T00:00:00Z",
        ),
        _sample_notif(
            "lightwave-media/lightwave-cli",
            "github-org-sync workflow run failed for main branch",
            "2",
            "2026-07-23T06:00:00Z",
        ),
        _sample_notif(
            "lightwave-media/lightwave-ai",
            "github-org-sync workflow run failed for main branch",
            "3",
            "2026-07-23T12:00:00Z",
        ),
        _sample_notif(
            "lightwave-media/lightwave-cli",
            "release-auto-merge workflow run failed for main branch",
            "4",
            "2026-07-23T08:00:00Z",
        ),
        {
            "id": "5",
            "reason": "review_requested",
            "updated_at": "2026-07-20T16:00:00Z",
            "repository": {"full_name": "lightwave-media/lightwave-core"},
            "subject": {
                "title": "chore(ci): bump actions/setup-go from 5 to 7",
                "type": "PullRequest",
                "url": "https://api.github.com/repos/x/y/pulls/1",
            },
        },
    ]
    proc = subprocess.run(  # noqa: S603
        [sys.executable, str(SCRIPT)],
        input=json.dumps(payload),
        text=True,
        capture_output=True,
        check=False,
    )
    assert proc.returncode == 0, proc.stderr
    events = json.loads(proc.stdout)
    fps = {e["fingerprint"] for e in events}
    assert len(events) == 4, f"expected 4 fingerprints, got {len(events)}: {fps}"
    org_cli = [
        e
        for e in events
        if e["repo"] == "lightwave-media/lightwave-cli" and e["workflow"] == "github-org-sync"
    ]
    assert len(org_cli) == 1
    assert org_cli[0]["occurrence_count"] == 2
    assert org_cli[0]["event_type"] == "gh.notif.ci_failed"
    review = [e for e in events if e["event_type"] == "gh.notif.review_requested"]
    assert len(review) == 1
    print("ok: dedupe collapsed sample inbox to 4 fingerprints")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
