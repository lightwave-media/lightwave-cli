{{! KICKOFF instance — lightwave-core CLI command-surface schema (+ media). Filled from src/boilerplate/templates/prompts/user_prompt.md }}

<task>
  <title>Stand up the CLI command-surface schema in lightwave-core (the lw stamp) + media domain</title>
  <description>Implement REQ docs/req-lightwave-core-cli-schema.md: create the CLI command-surface stamp that lightwave-cli's dispatcher consumes, reconcile the stale/missing loader path, land the pending check/hooks/local/task additions, declare the migrated keeper domains, add the new media domain block, and stamp the media-asset + media-manifest data schemas. Paired PRs in lightwave-core and lightwave-cli kept in lockstep so LW_CHECK_SCHEMA_STRICT=1 lw check schema stays green.</description>
  <status>todo</status>
  <priority>high</priority>
  <task_type>feature</task_type>
  <task_category>engineering</task_category>
  <domain>schema</domain>
  <user_story_ref>us-cli-rebuild</user_story_ref>
  <epic_ref>epic-cli-rebuild-and-media</epic_ref>
  <branch_name>feat/cli-command-surface-schema</branch_name>
</task>

<context>
  <epic>
    <name>Rebuild the lightwave-cli command surface; add the media domain</name>
    <log_line>Keep the chassis, rebuild commands in place against current conventions, add lw media — all schema-driven from the lightwave-core stamp.</log_line>
    <priority>high</priority>
  </epic>
  <user_story>
    <name>A real, drift-gated CLI stamp</name>
    <description>As the CLI maintainer, I want lw's command surface declared in lightwave-core and enforced by lw check schema, so the dispatcher has a real baseline and media is managed from one source.</description>
    <user_type>maintainer</user_type>
    <priority>high</priority>
    <acceptance_criteria>
      - given: the CLI loader
        when: it reads the stamp
        then: the path resolves to a file that EXISTS and LoadCLIConfig validates clean
      - given: the full keeper set + media
        when: LW_CHECK_SCHEMA_STRICT=1 ./bin/lw check schema runs
        then: it exits 0 — no missing handlers, no orphaned handlers
      - given: media-asset / media-manifest
        when: lightwave-core schema validation runs
        then: both validate and appear in the relevant __index.yaml
    </acceptance_criteria>
  </user_story>
  <related_specs>
    - kind: REQ
      ref: lightwave-cli://docs/req-lightwave-core-cli-schema.md
      title: REQ → lightwave-core — stand up the CLI command-surface schema (READ FIRST — full R1–R7 + sequencing + open questions)
    - kind: AUDIT
      ref: lightwave-cli://docs/cli-command-surface-audit.md
      title: CLI command-surface keep/rebuild/drop matrix (domain inventory + subcommand lists + media proposal)
    - kind: SCHEMA
      ref: lightwave-cli://docs/pending-schema-additions.yaml
      title: Pre-drafted check/hooks/local/task additions to port into the stamp
    - kind: CODE
      ref: lightwave-cli://internal/sst/cli_loader.go
      title: CLIConfigPath + LoadCLIConfig — the exact path + shape the dispatcher consumes
    - kind: CODE
      ref: lightwave-cli://internal/cli/dispatcher.go
      title: BuildDispatched + booleanFlags/stringArrayFlags tables (flag-type disambiguation; see R6)
    - kind: SCHEMA
      ref: lightwave-core://src/schemas/interfaces/__index.yaml
      title: Interfaces registry — the per-verb-schema vision the stamp must reconcile with (A vs B in R1)
    - kind: SCHEMA
      ref: lightwave-core://src/schemas/policy/governance/config_cli_contract.yaml
      title: Config CLI completeness contract (house schema conventions to mirror)
  </related_specs>
</context>

<definition_of_done>
- [DOD-1] CLI loader resolves the stamp at a path that exists; lightwave_root default corrected; LoadCLIConfig validates clean (verified by running lw locally) [auto]
- [DOD-2] LW_CHECK_SCHEMA_STRICT=1 ./bin/lw check schema exits 0 across keeper set + media (verified in CI schema-drift gate) [auto]
- [DOD-3] Pending additions (check/hooks/local/task) + migrated keeper domains (aws, worktree, agent, v_core, memory, msg, sst, health, codegen, content) declared in the stamp (verified via review against the audit matrix)
- [DOD-4] media domain block present; media-asset + media-manifest data schemas stamped per house conventions and indexed (verified via lightwave-core schema validation) [auto]
- [DOD-5] No orphaned handlers, no schema verbs without handlers; dropped domains (browser, test, legacy spec, context, scaffold) NOT present (verified via lw check schema)
- [DOD-6] Paired PRs (lightwave-core + lightwave-cli) reference each other and land in the R1→R3 sequence from the REQ (verified via review)
</definition_of_done>

<testing>
  <strategy_ref>schema-drift-gate</strategy_ref>
  <harness>mise run ci</harness>
  <test_levels>
    - unit (cli_loader/cli_schema decode + validate)
    - integration (lw check schema against the new stamp)
    - schema-validation (lightwave-core media-asset/manifest)
  </test_levels>
  <techniques>
    - static drift check: commands.yaml ↔ RegisterHandler set
    - load the real stamp via the corrected path and assert no validation error
  </techniques>
</testing>

<tools_available>
- Read
- Grep
- Edit / Write
- Bash (go, mise, git, gh)
</tools_available>

<skills_loaded_in_order>
- position: 0
  name: lightwave-git
  category: git
  trigger: any branch/commit/PR op across the paired repos
  why_now: two-repo lockstep PRs — branch discipline + commit hygiene are load-bearing here
</skills_loaded_in_order>

<interaction>
  <type>kick</type>
  <from>joel</from>
  <timestamp>2026-06-08T00:00:00Z</timestamp>
</interaction>

<instructions>
START by reading docs/req-lightwave-core-cli-schema.md in full — it is the spec. Then
read the audit (docs/cli-command-surface-audit.md) for the per-domain subcommand lists,
and cli_loader.go / cli_schema.go for the exact shape the dispatcher consumes.

Resolve the three OPEN QUESTIONS with Joel BEFORE writing schema files — do not pick
silently:
  1. R1 shape: hand-authored monolithic commands.yaml now (Option A, recommended) vs
     per-verb interfaces/cli/<domain>/<verb>.yaml + codegen (Option B).
  2. R6: encode flag types in the schema (and delete the dispatcher lookup tables) or
     keep the tables for now.
  3. R5: confirm data/assets/ as the home for media-asset/media-manifest and the exact
     assets.yaml cdn.paths keys media sync targets.

The single HARD blocker is R1: the loader currently points at a path that does not exist
(packages/lightwave-core/lightwave/schema/definitions/config/cli/commands.yaml under a
lightwave_root that defaults to the absent ~/dev/lightwave-media). The real repo uses
src/schemas/. Fix the path + lightwave_root default in lightwave-cli in the SAME PR that
creates the stamp file in lightwave-core — they must land together or the drift gate has
no baseline.

Work the REQ's paired-PR sequence (PR-1 path+pending+registry, PR-2 keepers, PR-3
media). Each lightwave-cli change follows the established migration pattern: confirm the
commands.yaml entry → register the handler (gate green) → for rebuilds, rewrite handler
fresh (old code is reference only, then delete) → pin a test. Destructive subcommands
ship --dry-run + --yes. Do NOT add the dropped domains.

DoD per the table above; overall done = mise run ci green + lw check schema green across
both repos.

ENV NOTE: go-vet needs the mise-managed go 1.24.13. New shells are fixed (mise shims on
PATH via ~/.zshrc); if a hook fires in a stale shell, prefix the commit with
`env -u GOROOT` until you `exec zsh`.
</instructions>

<attachments>
- kind: doc
  ref: lightwave-cli://docs/req-lightwave-core-cli-schema.md
  description: The REQ — authoritative spec (R1–R7, acceptance criteria, sequencing, open questions)
- kind: doc
  ref: lightwave-cli://docs/cli-command-surface-audit.md
  description: keep/rebuild/drop matrix — domain inventory, subcommand lists, media proposal
- kind: doc
  ref: lightwave-cli://docs/pending-schema-additions.yaml
  description: pre-drafted check/hooks/local/task schema additions to port
</attachments>
