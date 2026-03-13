# Sprint 1: lw CLI CRUD Commands

## Objective
Add create/update/list commands for tasks, sprints, stories, and epics to the `lw` Go CLI.

## Existing Code (DO NOT REWRITE)
- `internal/cli/task.go` — has `task list` and `task info` commands (working)
- `internal/cli/root.go` — root command setup
- `internal/db/tasks.go` — has `ListTasks` and `GetTask` functions
- `internal/db/connection.go` — pgx connection pool with tenant schema support
- `internal/config/config.go` — viper-based config (DB on localhost:5433, user postgres, db lightwave_platform, tenant lwm_core)

## Database Schema (lwm_core schema)

### createos_task (24 columns)
- id (uuid, PK), title (varchar), description (text), acceptance_criteria (text)
- status (varchar: approved/next_up/in_progress/in_review/archived/on_hold/cancelled)
- priority (varchar: p1_urgent/p2_high/p3_medium/p4_low)
- task_type (varchar: feature/fix/hotfix/chore/docs), task_category (varchar)
- due_date (date), do_date (date)
- agent_status (varchar), assigned_agent (varchar)
- branch_name (varchar), pr_url (varchar)
- ai_summary (text), note (text), notion_id (varchar)
- domain_id (uuid), epic_id (uuid FK), parent_task_id (uuid FK), sprint_id (uuid FK), user_story_id (uuid FK)

### createos_sprint (12 columns)
- id (uuid, PK), name (varchar), status (varchar: active/completed/planned)
- objectives (text), start_date (date), end_date (date)
- quality_score (numeric), notion_id (varchar)
- domain_id (uuid), epic_id (uuid FK)

### createos_userstory (22 columns)
- id (uuid, PK), name (varchar), description (text)
- acceptance_criteria (jsonb), status (varchar), user_type (varchar)
- priority (varchar), story_points (smallint)
- current_interview_round (varchar), discovery_status (varchar)
- interview_transcript (jsonb), personas (jsonb), user_flows (jsonb)
- edge_cases (jsonb), technical_constraints (jsonb), rbac_requirements (jsonb)
- research_notes (text), notion_id (varchar)
- epic_id (uuid FK), sprint_id (uuid FK)

### createos_epic (17 columns)
- id (uuid, PK), name (varchar), status (varchar: active/completed/planned)
- subtitle (varchar), log_line (text), priority (varchar)
- start_date (date), target_date (date)
- github_repo (varchar), notion_id (varchar)
- budget_tier (varchar), estimated_budget (numeric)
- client (varchar), production_company (varchar), domain_id (uuid)

## Tasks

### 1. `lw task create` (P1)
Add to `internal/cli/task.go` and `internal/db/tasks.go`.

Flags: --title (required), --description, --priority (default p3_medium), --type (default feature), --category, --epic, --sprint, --story
- Generate UUID v4 for id
- Insert into createos_task
- Print: "Created task <short-id>: <title>"

### 2. `lw task update <id>` (P1)
Flags: --status, --priority, --agent, --branch, --pr-url, --title, --description
- Update only specified fields (don't null out unspecified ones)
- Match by short ID prefix (like GetTask does)
- Print: "Updated task <short-id>"

### 3. `lw sprint create` and `lw sprint list` (P2)
New file: `internal/cli/sprint.go` and `internal/db/sprints.go`

`lw sprint create`: --name (required), --objectives, --epic, --start-date, --end-date, --status (default planned)
`lw sprint list`: --status filter, --epic filter, --limit

### 4. `lw story create` and `lw story list` (P2)
New file: `internal/cli/story.go` and `internal/db/stories.go`

`lw story create`: --name (required), --description, --priority (default p3_medium), --epic, --sprint, --user-type
`lw story list`: --status filter, --epic filter, --sprint filter, --limit

### 5. `lw epic list` (P3)
New file: `internal/cli/epic.go` and `internal/db/epics.go`

`lw epic list`: --status filter, --limit
Show: name, status, priority, github_repo, task count (subquery)

## Code Quality Rules
- Follow existing patterns in task.go and tasks.go exactly
- Use tablewriter for table output, fatih/color for colored output
- Use pgx for database access
- Generate UUIDs with github.com/google/uuid (add to go.mod if not present)
- Keep functions focused — one function per query
- No speculation, no dead code, no TODO comments
- Run `go build ./...` to verify compilation
- Run `go vet ./...` for static analysis

## Registration
All new commands must be registered in `internal/cli/root.go` — check how taskCmd is registered and follow the same pattern.

## Verification
After implementation:
1. `go build ./cmd/lw/` must succeed
2. `go vet ./...` must pass
3. `lw task list` must still work (don't break existing)
4. New commands print help when run with --help
