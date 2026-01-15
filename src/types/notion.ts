/**
 * Notion types for lightwave-cli
 *
 * Database IDs:
 * - Life Domains: b1e7c26b-7b52-4f60-9885-d73bcf1b76df
 * - Projects & Epics: fdb63184-3ec6-416c-b3a1-195fc2ef5d2c
 * - User Stories: 21a39364-b3be-80da-8e95-d321490f69f7
 * - Sprints: 21539364-b3be-802a-832d-de8d9cefcd9a
 * - Tasks: b8701544-1206-407e-934e-07485fe2f639
 * - Documents: b05d9a8d-7c67-4ba1-830f-bdc02edebc99
 *
 * Task Status values:
 * - On Hold
 * - Active (Approved for work)
 * - Next Up
 * - Future
 * - Active (In progress)
 * - Active (In Review)
 * - Archived
 * - Cancelled
 */

// Database IDs
export const NOTION_DB_IDS = {
  lifeDomains: "b1e7c26b-7b52-4f60-9885-d73bcf1b76df",
  epics: "fdb63184-3ec6-416c-b3a1-195fc2ef5d2c",
  userStories: "21a39364-b3be-80da-8e95-d321490f69f7",
  sprints: "21539364-b3be-802a-832d-de8d9cefcd9a",
  tasks: "b8701544-1206-407e-934e-07485fe2f639",
  documents: "b05d9a8d-7c67-4ba1-830f-bdc02edebc99",
  // CLI Views database - stores named filter configurations
  cliViews: "2e239364-b3be-8133-863b-f6d0ac454ea0",
} as const;

// Exact status names from Notion (case-sensitive)
export type NotionTaskStatus =
  | "On Hold"
  | "Active (Approved for work)"
  | "Next Up"
  | "Future"
  | "Active (In progress)"
  | "Active (In Review)"
  | "Archived"
  | "Cancelled";

export const VALID_STATUSES: NotionTaskStatus[] = [
  "On Hold",
  "Active (Approved for work)",
  "Next Up",
  "Future",
  "Active (In progress)",
  "Active (In Review)",
  "Archived",
  "Cancelled",
];

// CLI-friendly aliases that map to actual Notion statuses
export const STATUS_ALIASES: Record<string, NotionTaskStatus> = {
  hold: "On Hold",
  "on-hold": "On Hold",
  approved: "Active (Approved for work)",
  next: "Next Up",
  "next-up": "Next Up",
  future: "Future",
  "in-progress": "Active (In progress)",
  progress: "Active (In progress)",
  review: "Active (In Review)",
  "in-review": "Active (In Review)",
  archived: "Archived",
  done: "Archived",
  cancelled: "Cancelled",
  canceled: "Cancelled",
};

export type TaskType = "feature" | "fix" | "hotfix";

// Priority values from Notion
export type TaskPriority = "1st Priority" | "2nd Priority" | "3rd Priority";

export const VALID_PRIORITIES: TaskPriority[] = [
  "1st Priority",
  "2nd Priority",
  "3rd Priority",
];

// CLI-friendly aliases for priorities
export const PRIORITY_ALIASES: Record<string, TaskPriority> = {
  "1": "1st Priority",
  "1st": "1st Priority",
  first: "1st Priority",
  high: "1st Priority",
  "2": "2nd Priority",
  "2nd": "2nd Priority",
  second: "2nd Priority",
  medium: "2nd Priority",
  "3": "3rd Priority",
  "3rd": "3rd Priority",
  third: "3rd Priority",
  low: "3rd Priority",
};

// Task update options
export interface TaskUpdateOptions {
  status?: NotionTaskStatus;
  priority?: TaskPriority | null; // null to clear
  epicId?: string | null; // null to clear
  sprintId?: string | null; // null to clear
}

/**
 * Agent Status values from Notion
 * Used for AI agent workflow tracking
 */
export type AgentStatus =
  | "Pending Assignment"
  | "Triaged by v_general_manager"
  | "Assigned"
  | "In Progress (Agent)"
  | "Waiting for Input"
  | "Completed (Agent)"
  | "Blocked";

/**
 * Assigned Agent values from Notion
 * Virtual agents that can own tasks
 */
export type AssignedAgent =
  | "v_core"
  | "v_general_manager"
  | "v_speak"
  | "v_software_architect"
  | "v_senior_developer"
  | "v_write"
  | "v_accountant"
  | "v_cinematographer"
  | "v_photographer";

/**
 * Task Type values from Notion (explicit, vs inferred from title)
 */
export type NotionTaskType =
  | "Software Dev"
  | "General"
  | "Financial Admin"
  | "Film Production"
  | "Content Creation"
  | "Photography"
  | "Business Admin"
  | "Personal";

export interface NotionTask {
  id: string;
  shortId: string;
  title: string;
  status: NotionTaskStatus;
  description: string | null;
  acceptanceCriteria: string | null;
  url: string;
  taskType: TaskType; // Inferred from title (feature/fix/hotfix) for branch naming
  createdTime: string;
  lastEditedTime: string;

  // Priority & Scheduling
  priority?: string | null; // "1st Priority", "2nd Priority", etc.
  dueDate?: string | null; // Deadline
  doDate?: string | null; // Scheduled execution date

  // Agent Workflow (NEW)
  agentStatus?: AgentStatus | null; // AI agent processing state
  assignedAgent?: AssignedAgent | null; // Which v_agent owns this task
  assignee?: string | null; // Human assignee (person field)

  // Task Type from Notion (NEW)
  taskTypeSelect?: NotionTaskType | null; // Explicit type from select field

  // Notes & AI (NEW)
  note?: string | null; // Additional notes
  aiSummary?: string | null; // AI-generated summary

  // Task Hierarchy (NEW)
  parentTaskId?: string | null; // Self-relation to parent task
  subTaskIds?: string[]; // Self-relation to child tasks

  // Relations to other databases
  epicId?: string | null;
  epicName?: string | null;
  sprintId?: string | null;
  sprintName?: string | null;
  userStoryIds?: string[]; // Can have multiple user stories
  userStoryId?: string | null; // First user story (deprecated, use userStoryIds)
  userStoryName?: string | null;
  lifeDomainId?: string | null;
  lifeDomainName?: string | null;
  documentIds?: string[];

  // Metadata
  isTemplate?: boolean; // Is a template checkbox
  uniqueId?: string | null; // Notion's unique_id field
}

// Sprint status values
export type SprintStatus =
  | "Not Started"
  | "In Progress"
  | "Completed"
  | "Cancelled";

export interface NotionSprint {
  id: string;
  shortId: string;
  name: string;
  status: SprintStatus;
  objectives: string | null;
  startDate: string | null;
  endDate: string | null;
  qualityScore: number | null;
  url: string;
  // Relations
  epicIds: string[];
  taskIds: string[];
  userStoryIds: string[];
  lifeDomainId?: string | null;
  lifeDomainName?: string | null;
}

// Epic/Project status
export type EpicStatus =
  | "Not Started"
  | "In Progress"
  | "Completed"
  | "On Hold"
  | "Cancelled";

export interface NotionEpic {
  id: string;
  shortId: string;
  name: string;
  status: EpicStatus;
  subtitle: string | null;
  logLine: string | null;
  priority: string | null;
  projectType: string | null;
  startDate: string | null;
  endDate: string | null;
  totalStoryPoints: number | null;
  url: string;
  githubRepoLink: string | null;
  // Relations
  lifeDomainId?: string | null;
  lifeDomainName?: string | null;
  sprintIds: string[];
  userStoryIds: string[];
  taskIds: string[];
  documentIds: string[];
}

export interface NotionUserStory {
  id: string;
  shortId: string;
  name: string;
  description?: string;
  acceptanceCriteria?: string;
  status: string;
  priority: string | null;
  userType: string | null;
  url: string;
  // Relations
  epicId?: string | null;
  epicName?: string | null;
  sprintId?: string | null;
  taskIds: string[];
}

export interface NotionLifeDomain {
  id: string;
  shortId: string;
  name: string;
  type: string | null;
  status: string | null;
  url: string;
}

export interface NotionDocument {
  id: string;
  shortId: string;
  name: string;
  contentType: string | null; // "Config", "SOP", "Spec", "Template", etc.
  version: string | null;
  status: string | null; // "📢 Active/Live", "Draft", etc.
  agentTags: string[]; // ["agent:v_speak", "agent:v_core"]
  content?: string; // Markdown content from blocks (populated on demand)
  url: string;
  // Relations
  taskIds?: string[];
  epicIds?: string[];
}

export interface DocumentListOptions {
  tags?: string[]; // Filter by Agent Tags
  status?: string; // Filter by Document Status
  contentType?: string; // Filter by Content Type
  limit?: number;
  format?: "table" | "json";
}

// Task context output structure
export interface TaskContext {
  task: NotionTask;
  epic?: NotionEpic | null;
  sprint?: NotionSprint | null;
  documents: NotionDocument[];
  // New: Task hierarchy
  parentTask?: NotionTask | null;
  subtasks?: NotionTask[];
  // New: User stories
  userStories?: NotionUserStory[];
  // New: Life domain
  lifeDomain?: NotionLifeDomain | null;
  // Context layers loaded
  layers: string[];
}

export interface NotionConfig {
  apiKey: string;
  // All database IDs loaded from NOTION_DB_IDS
  tasksDbId: string;
  epicsDbId: string;
  sprintsDbId: string;
  userStoriesDbId: string;
  lifeDomainsDbId: string;
  documentsDbId: string;
}

export interface TaskListOptions {
  status?: NotionTaskStatus | NotionTaskStatus[];
  limit?: number;
  format?: "table" | "json";

  // Relation filters
  domain?: string; // Life Domain name
  epic?: string; // Epic name or ID
  sprint?: string; // Sprint name or ID
  userStory?: string; // User Story name or ID

  // Property filters (NEW)
  priority?: string; // Priority value
  taskType?: NotionTaskType; // Task Type select value
  agentStatus?: AgentStatus; // Agent workflow status
  assignedAgent?: AssignedAgent; // Which agent owns the task
  assignee?: string; // Human assignee name

  // Date filters (NEW)
  dueBefore?: string; // Tasks due before this date (ISO 8601)
  dueAfter?: string; // Tasks due after this date
  hasSubtasks?: boolean; // Filter for tasks with/without subtasks
  isParent?: boolean; // Filter for parent tasks only
  parentTask?: string; // Filter by parent task ID

  // View-based filtering (NEW)
  view?: string; // Named view from CLI Views database
}

export interface SprintListOptions {
  status?: SprintStatus | SprintStatus[];
  domain?: string;
  limit?: number;
  format?: "table" | "json";
}

export interface EpicListOptions {
  status?: EpicStatus | EpicStatus[];
  domain?: string;
  limit?: number;
  format?: "table" | "json";
}

export interface TaskStartOptions {
  dryRun?: boolean;
  noPush?: boolean;
  branchName?: string;
}

export interface TaskPrOptions {
  dryRun?: boolean;
  draft?: boolean;
  base?: string;
}

// =============================================================================
// CLI Views (Notion-stored filter configurations)
// =============================================================================

/**
 * A CLI View represents a saved filter configuration stored in Notion
 * This allows users to create named views in Notion UI and use them from CLI
 */
export interface CLIView {
  id: string;
  shortId: string;
  name: string;
  database: "Tasks" | "Epics" | "Sprints" | "Documents";
  filterJson: Record<string, unknown>; // Notion filter object
  description: string | null;
  active: boolean;
  url: string;
}

export interface ViewListOptions {
  database?: string; // Filter by target database
  activeOnly?: boolean; // Only show active views
  limit?: number;
  format?: "table" | "json";
}
