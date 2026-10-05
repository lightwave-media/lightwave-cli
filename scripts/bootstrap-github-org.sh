#!/usr/bin/env bash
# Bootstrap lightwave-media GitHub org: labels, milestones.
# Idempotent where gh allows (--force labels, skip existing milestones).
set -euo pipefail

ORG_LOGIN="${ORG_LOGIN:-lightwave-media}"

# The six null* standalones (nullclaw, nullhub, nullbuilder, nulltickets,
# nullwatch, nullboiler) are NOT listed: they were folded into
# lightwave-ai/src/<module>/ and archived read-only (lightwave-ai#44). Archived
# repos reject label/milestone writes, so bootstrapping them fails noisily.
# lightwave-ai carries their issues under package:<module> labels.
SWARM_REPOS=(
  lightwave-core
  lightwave-cli
  lightwave-ui
  lightwave-platform
  lightwave-sys
  lightwave-ai
  lightwave-infrastructure-catalog
  lightwave-infrastructure-live
  createOS
  joelschaeffer-site
  homebrew-tap
)

declare -a LABEL_SPECS=(
  "nullhub|0075CA|In the nullhub swarm queue"
  "status:triage|E4E669|Needs scrum-manager triage"
  "status:ready|0E8A16|Ready for an agent to claim"
  "status:in-progress|D93F0B|Claimed by an agent"
  "status:pr-open|6F42C1|PR open awaiting review"
  "status:blocked|B60205|Blocked on dependency or decision"
  "issue-type:bug|D73A4A|Bug report"
  "issue-type:feature|A2EEEF|Feature request"
  "issue-type:spec|0075CA|Spec change"
  "issue-type:agent-task|FEF2C0|Agent task"
  "issue-type:infra|5319E7|Infrastructure change"
  "issue-type:release|0E8A16|Release task"
  "priority:p0|B60205|Release blocker"
  "priority:p1|D93F0B|High priority"
  "repo-tier:core|5319E7|Core platform repo"
  "repo-tier:cli|5319E7|CLI repo"
  "repo-tier:service|5319E7|Service repo"
  "repo-tier:agent|5319E7|Agent repo"
  "repo-tier:infra|5319E7|Infrastructure repo"
  "package:nullclaw|C5DEF5|nullclaw module"
  "package:nullhub|C5DEF5|nullhub module"
  "package:nullbuilder|C5DEF5|nullbuilder module"
  "package:nulltickets|C5DEF5|nulltickets module"
  "package:nullwatch|C5DEF5|nullwatch module"
  "agent:v_cli-developer|BFD4F2|Assigned to v_cli-developer"
  "agent:v_core-package-developer|BFD4F2|Assigned to v_core-package-developer"
  "agent:v_platform-developer|BFD4F2|Assigned to v_platform-developer"
  "agent:v_frontend-developer|BFD4F2|Assigned to v_frontend-developer"
  "agent:v_sys-developer|BFD4F2|Assigned to v_sys-developer"
  "agent:v_localapp-developer|BFD4F2|Assigned to v_localapp-developer"
  "agent:v_staff-engineer|BFD4F2|Assigned to v_staff-engineer"
  "agent:v_lightwave-ai-engineer|BFD4F2|Assigned to v_lightwave-ai-engineer"
  "fix-me|FBCA04|Reserved for OpenHands-compatible fix-up flows"
  "automerge|0E8A16|Reserved for automerge-on-green workflows"
)

declare -a MILESTONE_SPECS=(
  "M0|Phase 0 — factory CLI"
  "M1|Phase 1 — scaffold + org unification"
  "M2|Phase 2 — createOS shell + null* migration start"
  "M3|Phase 3 — nullhub UI parity"
  "M4|Phase 4 — local swarm"
)

apply_labels() {
  local repo=$1
  for spec in "${LABEL_SPECS[@]}"; do
    IFS='|' read -r name color desc <<< "$spec"
    gh label create "$name" --repo "${ORG_LOGIN}/${repo}" --color "$color" --description "$desc" --force >/dev/null 2>&1 || true
  done
  echo "  labels: ${repo}"
}

apply_milestones() {
  local repo=$1
  local existing
  existing=$(gh api "repos/${ORG_LOGIN}/${repo}/milestones" --jq '.[].title' 2>/dev/null || true)
  for spec in "${MILESTONE_SPECS[@]}"; do
    IFS='|' read -r title desc <<< "$spec"
    if echo "$existing" | grep -qx "$title"; then
      continue
    fi
    gh api "repos/${ORG_LOGIN}/${repo}/milestones" -f title="$title" -f description="$desc" -f state=open >/dev/null 2>&1 || echo "    warn: milestone ${title} on ${repo} skipped"
  done
  echo "  milestones: ${repo}"
}

echo "==> Org bootstrap (${ORG_LOGIN})"

if [[ -n "${TARGET_REPO:-}" ]]; then
  apply_labels "$TARGET_REPO"
  apply_milestones "$TARGET_REPO"
  echo "==> Done repo slice: ${TARGET_REPO}"
  exit 0
fi

echo "==> Apply swarm labels"
for repo in "${SWARM_REPOS[@]}"; do
  apply_labels "$repo"
done

echo "==> Apply milestones (swarm repos)"
for repo in lightwave-core lightwave-cli lightwave-ai lightwave-platform lightwave-sys createOS; do
  apply_milestones "$repo"
done

echo "==> Done."
