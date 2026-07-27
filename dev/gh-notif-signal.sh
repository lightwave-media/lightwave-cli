#!/usr/bin/env bash
# gh-notif-signal.sh — GitHub notifications → nullwatch + conditional nullclaw dispatch.
#
# Polls GitHub notifications, dedupes via gh_notif_to_event.py, posts spans to
# nullwatch, auto-dispatches one allowlisted failure class per poll, and records
# everything else as a nulltickets stub (span + local state).
#
# Usage:
#   ./dev/gh-notif-signal.sh
#   LW_GH_NOTIF_DRY_RUN=1 ./dev/gh-notif-signal.sh
#
# Environment:
#   LW_GH_NOTIF_DRY_RUN=1   Print [DRY RUN]; skip POSTs and notification mutates
#   LW_GH_NOTIF_ALLOWLIST   Comma-separated workflow name substrings (default: github-org-sync)

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

NULLWATCH_SPANS_URL="http://localhost:19800/api/nullwatch/v1/spans"
NULLCLAW_BASE_URL="http://localhost:19800/api/instances/nullclaw"
NULLTICKETS_URL="http://localhost:7700/tasks"
# Default pipeline: bootstrap (override with LW_GH_NOTIF_PIPELINE_ID)
PIPELINE_ID="${LW_GH_NOTIF_PIPELINE_ID:-048a367e-0b49-488b-9b1f-a1ed988eff37}"
STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/lightwave"
STATE_FILE="${STATE_DIR}/gh-notif-fingerprints.json"
EVENTS_FILE="/tmp/lw-gh-notif-events.json"
INSTANCE="v_core-package-developer"
ALLOWLIST="${LW_GH_NOTIF_ALLOWLIST:-github-org-sync}"
DRY_RUN="${LW_GH_NOTIF_DRY_RUN:-}"

log() {
  if [[ -n "$DRY_RUN" ]]; then
    echo "[DRY RUN] $*"
  else
    echo "$*"
  fi
}

safe_post() {
  local url="$1"
  local body="$2"
  if [[ -n "$DRY_RUN" ]]; then
    echo "[DRY RUN] POST $url"
    echo "[DRY RUN] body: $body"
    return 0
  fi
  curl --max-time 10 --silent \
    -X POST \
    -H "Content-Type: application/json" \
    -d "$body" \
    "$url" \
    || echo "WARNING: curl POST to $url failed (non-fatal)"
}

mkdir -p "$STATE_DIR"
if [[ ! -f "$STATE_FILE" ]]; then
  echo '{"fingerprints":{}}' > "$STATE_FILE"
fi

echo "==> Fetching + classifying GitHub notifications …"
uv run python3 dev/gh_notif_to_event.py --fetch > "$EVENTS_FILE"

EVENT_COUNT=$(python3 -c "import json; print(len(json.load(open('$EVENTS_FILE'))))")
echo "==> ${EVENT_COUNT} unique fingerprint(s)"
python3 -c "import json; ev=json.load(open('$EVENTS_FILE'));
print('top:', [(e['repo'], e['workflow'], e['occurrence_count']) for e in sorted(ev, key=lambda x: -x['occurrence_count'])[:8]])"

if [[ "$EVENT_COUNT" -eq 0 ]]; then
  log "Inbox empty — nothing to do"
  exit 0
fi

# ---------------------------------------------------------------------------
# Load prior state; skip fingerprints that are in-flight or recently dispatched
# Cap ticket_only to LW_GH_NOTIF_TICKET_CAP (default 5) by occurrence_count.
# ---------------------------------------------------------------------------

TICKET_CAP="${LW_GH_NOTIF_TICKET_CAP:-5}"

python3 - "$EVENTS_FILE" "$STATE_FILE" "$ALLOWLIST" "$TICKET_CAP" <<'PY' > /tmp/lw-gh-notif-plan.json
import json, sys
from datetime import datetime, timezone

events_path, state_path, allowlist_raw, ticket_cap_s = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
events = json.load(open(events_path))
state = json.load(open(state_path))
fps = state.setdefault("fingerprints", {})
allow = [a.strip() for a in allowlist_raw.split(",") if a.strip()]
ticket_cap = int(ticket_cap_s)

def is_allowlisted(workflow: str) -> bool:
    w = (workflow or "").lower()
    return any(a.lower() in w for a in allow)

now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
dispatch_candidate = None
ticket_candidates = []
skipped = []

for ev in events:
    fp = ev["fingerprint"]
    prior = fps.get(fp) or {}
    status = prior.get("status")
    if status == "in_flight":
        skipped.append({"fingerprint": fp, "reason": "in_flight"})
        continue
    if status in {"dispatched", "ticketed"}:
        if prior.get("occurrence_count", 0) >= ev.get("occurrence_count", 0):
            skipped.append({"fingerprint": fp, "reason": f"already_{status}"})
            continue
    if is_allowlisted(ev.get("workflow", "")) and ev.get("event_type") == "gh.notif.ci_failed":
        if dispatch_candidate is None:
            dispatch_candidate = ev
        else:
            ticket_candidates.append(ev)
    else:
        ticket_candidates.append(ev)

ticket_candidates.sort(key=lambda e: -int(e.get("occurrence_count") or 0))
ticket_only = ticket_candidates[:ticket_cap]
deferred = len(ticket_candidates) - len(ticket_only)

plan = {
    "timestamp": now,
    "dispatch": dispatch_candidate,
    "ticket_only": ticket_only,
    "skipped": skipped,
    "ticket_deferred": deferred,
}
json.dump(plan, sys.stdout, indent=2)
print()
PY

echo "==> Plan summary:"
python3 -c "import json; p=json.load(open('/tmp/lw-gh-notif-plan.json')); d=p.get('dispatch');
print('dispatch:', (d or {}).get('fingerprint'));
print('ticket_only:', len(p.get('ticket_only') or []), 'deferred:', p.get('ticket_deferred'));
print('skipped:', len(p.get('skipped') or []))"
echo ""

# ---------------------------------------------------------------------------
# Post a span for every non-skipped event (dispatch + ticket_only)
# ---------------------------------------------------------------------------

post_span() {
  local name="$1"
  local event_json="$2"
  local escaped
  escaped=$(python3 -c "import json,sys; print(json.dumps(sys.argv[1]))" "$event_json")
  local body
  body="{\"resourceSpans\":[{\"scopeSpans\":[{\"spans\":[{\"name\":\"${name}\",\"attributes\":[{\"key\":\"event\",\"value\":{\"stringValue\":${escaped}}}]}]}]}]}"
  log "Posting span ${name} …"
  safe_post "$NULLWATCH_SPANS_URL" "$body"
}

# ticket-only path
python3 - <<'PY' > /tmp/lw-gh-ticket-events.jsonl
import json
plan = json.load(open("/tmp/lw-gh-notif-plan.json"))
for ev in plan.get("ticket_only") or []:
    print(json.dumps(ev))
if plan.get("dispatch"):
    print(json.dumps(plan["dispatch"]))
PY

while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  etype=$(python3 -c "import json,sys; print(json.loads(sys.argv[1]).get('event_type','gh.notif.other'))" "$line")
  post_span "$etype" "$line"
done < /tmp/lw-gh-ticket-events.jsonl

# ---------------------------------------------------------------------------
# File nulltickets stubs for non-dispatched (once per fingerprint)
# ---------------------------------------------------------------------------

while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  FP=$(python3 -c "import json,sys; print(json.loads(sys.argv[1])['fingerprint'])" "$line")
  ALREADY=$(python3 -c "import json,sys; s=json.load(open(sys.argv[1])); print(s.get('fingerprints',{}).get(sys.argv[2],{}).get('status',''))" "$STATE_FILE" "$FP")
  if [[ "$ALREADY" == "ticketed" || "$ALREADY" == "dispatched" || "$ALREADY" == "in_flight" ]]; then
    log "Skip ticket (already $ALREADY): $FP"
    continue
  fi
  TITLE=$(python3 -c "import json,sys; d=json.loads(sys.argv[1]); print(f\"[gh-notif] {d.get('repo')}: {d.get('workflow')} — {d.get('title','')[:80]}\")" "$line")
  BODY=$(python3 -c "import json,sys; ev=json.loads(sys.argv[2]); print(json.dumps({'pipeline_id': sys.argv[3], 'title': sys.argv[1], 'description': 'GitHub notification (ticket-only; not auto-dispatched).\\n\\n' + json.dumps(ev, indent=2), 'priority': 2, 'metadata': {'source': 'gh-notif-signal', 'fingerprint': ev.get('fingerprint'), 'workflow': ev.get('workflow'), 'repo': ev.get('repo')}}))" "$TITLE" "$line" "$PIPELINE_ID")
  log "Filing nulltickets stub: $TITLE"
  safe_post "$NULLTICKETS_URL" "$BODY"
  python3 - "$STATE_FILE" "$FP" "$line" <<'PY'
import json, sys
from datetime import datetime, timezone
path, fp, line = sys.argv[1], sys.argv[2], sys.argv[3]
ev = json.loads(line)
state = json.load(open(path))
state.setdefault("fingerprints", {})[fp] = {
    "status": "ticketed",
    "occurrence_count": ev.get("occurrence_count", 1),
    "ticketed_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
}
json.dump(state, open(path, "w"), indent=2)
PY
done < <(python3 -c "import json; plan=json.load(open('/tmp/lw-gh-notif-plan.json'));
[print(json.dumps(e)) for e in (plan.get('ticket_only') or [])]")

# ---------------------------------------------------------------------------
# One allowlisted dispatch
# ---------------------------------------------------------------------------

DISPATCH_JSON=$(python3 -c "import json; d=json.load(open('/tmp/lw-gh-notif-plan.json')).get('dispatch'); print(json.dumps(d) if d else '')")

if [[ -z "$DISPATCH_JSON" || "$DISPATCH_JSON" == "null" ]]; then
  log "No allowlisted fingerprint to dispatch"
else
  FP=$(python3 -c "import json,sys; print(json.loads(sys.argv[1])['fingerprint'])" "$DISPATCH_JSON")
  REPO=$(python3 -c "import json,sys; print(json.loads(sys.argv[1])['repo'])" "$DISPATCH_JSON")
  WORKFLOW=$(python3 -c "import json,sys; print(json.loads(sys.argv[1])['workflow'])" "$DISPATCH_JSON")
  TITLE=$(python3 -c "import json,sys; print(json.loads(sys.argv[1])['title'])" "$DISPATCH_JSON")
  COUNT=$(python3 -c "import json,sys; print(json.loads(sys.argv[1])['occurrence_count'])" "$DISPATCH_JSON")

  STATUS_URL="$NULLCLAW_BASE_URL/$INSTANCE/status"
  DISPATCH_URL="$NULLCLAW_BASE_URL/$INSTANCE/agent"

  AGENT_MSG=$(cat <<EOF
GitHub notification allowlisted failure (auto-dispatch).
Repo: $REPO
Workflow: $WORKFLOW
Title: $TITLE
Occurrences (deduped): $COUNT
Fingerprint: $FP

Instructions:
1. Pull failed run logs: gh run list -R $REPO --workflow=${WORKFLOW}.yml --limit 1 then gh run view --log-failed.
2. Fix the root cause (cross-repo checkout with GITHUB_TOKEN fails on private siblings — prefer self-checkout + vendored bootstrap, or LIGHTWAVE_ORG_SYNC_TOKEN). Do NOT silence with continue-on-error.
3. Open/update one PR; re-run the workflow; mark related notification threads read only when green.
4. Report fingerprint $FP and what you changed.
EOF
)

  AGENT_BODY=$(python3 -c "import json,sys; print(json.dumps({'message': sys.argv[1], 'event': json.loads(sys.argv[2])}))" "$AGENT_MSG" "$DISPATCH_JSON")

  log "Checking instance $INSTANCE …"
  if [[ -n "$DRY_RUN" ]]; then
    echo "[DRY RUN] GET $STATUS_URL"
    echo "[DRY RUN] POST $DISPATCH_URL"
    echo "[DRY RUN] agent body: $AGENT_BODY"
  else
    if ! curl --max-time 10 --silent --fail "$STATUS_URL" > /dev/null 2>&1; then
      echo "WARNING: nullclaw instance $INSTANCE unreachable — skipping dispatch"
    else
      log "Dispatching to $INSTANCE for $FP …"
      safe_post "$DISPATCH_URL" "$AGENT_BODY"

      # Mark notification threads read only after successful dispatch
      THREAD_IDS=$(python3 -c "import json,sys; print(' '.join(json.loads(sys.argv[1]).get('thread_ids') or []))" "$DISPATCH_JSON")
      for tid in $THREAD_IDS; do
        gh api -X PATCH "/notifications/threads/${tid}" >/dev/null 2>&1 \
          || echo "WARNING: mark-read failed for thread $tid"
      done

      python3 - "$STATE_FILE" "$FP" "$COUNT" <<'PY'
import json, sys
from datetime import datetime, timezone
path, fp, count = sys.argv[1], sys.argv[2], int(sys.argv[3])
state = json.load(open(path))
state.setdefault("fingerprints", {})[fp] = {
    "status": "dispatched",
    "occurrence_count": count,
    "dispatched_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
}
json.dump(state, open(path, "w"), indent=2)
print(f"state updated: {fp} → dispatched")
PY
    fi
  fi
fi

log "Done."
exit 0
