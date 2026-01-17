/**
 * createOS Django API client for lightwave-cli
 *
 * Provides direct access to the createOS Django backend as an alternative
 * to Notion API. This is the native Django backend for task management.
 *
 * Environment Variables:
 * - CREATEOS_API_URL: Base URL for createOS API (default: http://localhost:8000)
 * - LW_BACKEND: Backend to use (django, notion, both) - default: notion
 */

import type {
  NotionTask,
  NotionTaskStatus,
  NotionEpic,
  NotionSprint,
  NotionUserStory,
  NotionLifeDomain,
  NotionDocument,
  TaskListOptions,
  DocumentListOptions,
  TaskType,
  AgentStatus,
  AssignedAgent,
  NotionTaskType,
} from "../types/notion.js";

// =============================================================================
// CONFIGURATION
// =============================================================================

const DEFAULT_API_URL = "http://localhost:8000";

/**
 * Get the createOS API base URL from environment
 */
function getApiUrl(): string {
  return process.env.CREATEOS_API_URL || DEFAULT_API_URL;
}

/**
 * Get the configured backend (django, notion, or both)
 */
export function getBackend(): "django" | "notion" | "both" {
  const backend = process.env.LW_BACKEND?.toLowerCase();
  if (backend === "notion" || backend === "legacy") return "notion";
  if (backend === "both") return "both";
  return "django"; // Default to Django (createOS) - Notion is legacy
}

// =============================================================================
// DJANGO API TYPES
// =============================================================================

interface DjangoTask {
  id: string;
  short_id: string;
  title: string;
  description: string | null;
  acceptance_criteria: string | null;
  domain: string | null;
  domain_name: string | null;
  epic: string | null;
  epic_name: string | null;
  sprint: string | null;
  sprint_name: string | null;
  user_story: string | null;
  user_story_name: string | null;
  parent_task: string | null;
  parent_task_title: string | null;
  status: string;
  priority: string;
  task_type: string;
  task_category: string;
  due_date: string | null;
  do_date: string | null;
  agent_status: string;
  assigned_agent: string;
  branch_name: string;
  pr_url: string;
  ai_summary: string;
  note: string;
  notion_id: string;
  is_subtask: boolean;
  has_subtasks: boolean;
  created_at: string;
  updated_at: string;
}

interface DjangoEpic {
  id: string;
  short_id: string;
  name: string;
  domain: string | null;
  domain_name: string | null;
  status: string;
  subtitle: string | null;
  log_line: string | null;
  priority: string;
  start_date: string | null;
  target_date: string | null;
  github_repo: string;
  notion_id: string;
  created_at: string;
  updated_at: string;
}

interface DjangoSprint {
  id: string;
  short_id: string;
  name: string;
  domain: string | null;
  domain_name: string | null;
  epic: string | null;
  epic_name: string | null;
  status: string;
  objectives: string | null;
  start_date: string | null;
  end_date: string | null;
  quality_score: number | null;
  notion_id: string;
  created_at: string;
  updated_at: string;
}

interface DjangoUserStory {
  id: string;
  short_id: string;
  name: string;
  description: string;
  acceptance_criteria: string;
  epic: string | null;
  epic_name: string | null;
  sprint: string | null;
  sprint_name: string | null;
  status: string;
  user_type: string | null;
  priority: string;
  story_points: number | null;
  notion_id: string;
  created_at: string;
  updated_at: string;
}

interface DjangoLifeDomain {
  id: string;
  short_id: string;
  name: string;
  type: string | null;
  icon: string;
  color: string;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

interface DjangoDocument {
  id: string;
  short_id: string;
  name: string;
  content: string;
  document_type: string; // sop, spec, config, template, reference, guide
  status: string; // draft, active, archived, deprecated
  version: string;
  agent_tags: string[];
  notion_id: string;
  domain: string | null;
  domain_name: string | null;
  epic_ids: string[];
  task_ids: string[];
  created_at: string;
  updated_at: string;
}

interface DjangoPaginatedResponse<T> {
  count: number;
  next: string | null;
  previous: string | null;
  results: T[];
}

// =============================================================================
// STATUS MAPPING
// =============================================================================

// Map Django status values to Notion status values
const DJANGO_TO_NOTION_STATUS: Record<string, NotionTaskStatus> = {
  on_hold: "On Hold",
  approved: "Active (Approved for work)",
  next_up: "Next Up",
  future: "Future",
  in_progress: "Active (In progress)",
  in_review: "Active (In Review)",
  archived: "Archived",
  cancelled: "Cancelled",
};

const NOTION_TO_DJANGO_STATUS: Record<NotionTaskStatus, string> = {
  "On Hold": "on_hold",
  "Active (Approved for work)": "approved",
  "Next Up": "next_up",
  Future: "future",
  "Active (In progress)": "in_progress",
  "Active (In Review)": "in_review",
  Archived: "archived",
  Cancelled: "cancelled",
};

// Map Django priority values to Notion priority values
const DJANGO_TO_NOTION_PRIORITY: Record<string, string> = {
  p1_urgent: "1st Priority",
  p2_high: "2nd Priority",
  p3_medium: "3rd Priority",
  p4_low: "3rd Priority", // Map p4 to 3rd since Notion only has 3
};

const NOTION_TO_DJANGO_PRIORITY: Record<string, string> = {
  "1st Priority": "p1_urgent",
  "2nd Priority": "p2_high",
  "3rd Priority": "p3_medium",
};

// Map Django task category to Notion task type
const DJANGO_TO_NOTION_TASK_TYPE: Record<string, NotionTaskType> = {
  software_dev: "Software Dev",
  general: "General",
  financial_admin: "Financial Admin",
  film_production: "Film Production",
  content_creation: "Content Creation",
  photography: "Photography",
  business_admin: "Business Admin",
  personal: "Personal",
};

// Map Django document type to Notion content type
const DJANGO_TO_NOTION_DOC_TYPE: Record<string, string> = {
  sop: "SOP",
  spec: "Spec",
  config: "Config",
  template: "Template",
  reference: "Reference",
  guide: "Guide",
};

const NOTION_TO_DJANGO_DOC_TYPE: Record<string, string> = {
  SOP: "sop",
  Spec: "spec",
  Config: "config",
  Template: "template",
  Reference: "reference",
  Guide: "guide",
};

// Map Django document status to Notion document status
const DJANGO_TO_NOTION_DOC_STATUS: Record<string, string> = {
  draft: "Draft",
  active: "📢 Active/Live",
  archived: "Archived",
  deprecated: "Deprecated",
};

const NOTION_TO_DJANGO_DOC_STATUS: Record<string, string> = {
  Draft: "draft",
  "📢 Active/Live": "active",
  Active: "active",
  Archived: "archived",
  Deprecated: "deprecated",
};

// =============================================================================
// CONVERTERS
// =============================================================================

/**
 * Convert Django task to Notion task format for CLI compatibility
 */
function djangoTaskToNotionTask(task: DjangoTask): NotionTask {
  // Infer task type from title (feature/fix/hotfix)
  const inferTaskType = (title: string): TaskType => {
    const lower = title.toLowerCase();
    if (lower.startsWith("fix") || lower.includes("bug")) return "fix";
    if (lower.startsWith("hotfix") || lower.includes("urgent fix"))
      return "hotfix";
    return "feature";
  };

  return {
    id: task.id,
    shortId: task.short_id,
    title: task.title,
    status: DJANGO_TO_NOTION_STATUS[task.status] || "Future",
    description: task.description,
    acceptanceCriteria: task.acceptance_criteria,
    url: `${getApiUrl()}/api/createos/tasks/${task.id}/`,
    taskType: inferTaskType(task.title),
    createdTime: task.created_at,
    lastEditedTime: task.updated_at,

    // Priority & Scheduling
    priority: DJANGO_TO_NOTION_PRIORITY[task.priority] || null,
    dueDate: task.due_date,
    doDate: task.do_date,

    // Agent Workflow
    agentStatus: (task.agent_status as AgentStatus) || null,
    assignedAgent: (task.assigned_agent as AssignedAgent) || null,

    // Task Type from select field
    taskTypeSelect: DJANGO_TO_NOTION_TASK_TYPE[task.task_category] || null,

    // Notes & AI
    note: task.note || null,
    aiSummary: task.ai_summary || null,

    // Task Hierarchy
    parentTaskId: task.parent_task,
    subTaskIds: [], // Would need separate query

    // Relations
    epicId: task.epic,
    epicName: task.epic_name,
    sprintId: task.sprint,
    sprintName: task.sprint_name,
    userStoryIds: task.user_story ? [task.user_story] : [],
    userStoryId: task.user_story,
    userStoryName: task.user_story_name,
    lifeDomainId: task.domain,
    lifeDomainName: task.domain_name,
  };
}

/**
 * Convert Django epic to Notion epic format
 */
function djangoEpicToNotionEpic(epic: DjangoEpic): NotionEpic {
  return {
    id: epic.id,
    shortId: epic.short_id,
    name: epic.name,
    status: epic.status as any,
    subtitle: epic.subtitle,
    logLine: epic.log_line,
    priority: DJANGO_TO_NOTION_PRIORITY[epic.priority] || null,
    projectType: null,
    startDate: epic.start_date,
    endDate: epic.target_date,
    totalStoryPoints: null,
    url: `${getApiUrl()}/api/createos/epics/${epic.id}/`,
    githubRepoLink: epic.github_repo || null,
    lifeDomainId: epic.domain,
    lifeDomainName: epic.domain_name,
    sprintIds: [],
    userStoryIds: [],
    taskIds: [],
    documentIds: [],
  };
}

/**
 * Convert Django sprint to Notion sprint format
 */
function djangoSprintToNotionSprint(sprint: DjangoSprint): NotionSprint {
  // Use epic UUID if available, otherwise check epic_name to determine if linked
  const hasEpic = sprint.epic || sprint.epic_name;
  return {
    id: sprint.id,
    shortId: sprint.short_id,
    name: sprint.name,
    status: sprint.status as any,
    objectives: sprint.objectives,
    startDate: sprint.start_date,
    endDate: sprint.end_date,
    qualityScore: sprint.quality_score,
    url: `${getApiUrl()}/api/createos/sprints/${sprint.id}/`,
    epicIds: hasEpic ? [sprint.epic || sprint.epic_name || ""] : [],
    taskIds: [],
    userStoryIds: [],
    lifeDomainId: sprint.domain,
    lifeDomainName: sprint.domain_name,
  };
}

/**
 * Convert Django domain to Notion domain format
 */
function djangoDomainToNotionDomain(
  domain: DjangoLifeDomain,
): NotionLifeDomain {
  return {
    id: domain.id,
    shortId: domain.short_id,
    name: domain.name,
    type: domain.type,
    status: domain.is_active ? "Active" : "Inactive",
    url: `${getApiUrl()}/api/createos/domains/${domain.id}/`,
  };
}

/**
 * Convert Django document to Notion document format
 */
function djangoDocumentToNotionDocument(doc: DjangoDocument): NotionDocument {
  return {
    id: doc.id,
    shortId: doc.short_id,
    name: doc.name,
    contentType:
      DJANGO_TO_NOTION_DOC_TYPE[doc.document_type] || doc.document_type,
    version: doc.version || null,
    status: DJANGO_TO_NOTION_DOC_STATUS[doc.status] || doc.status,
    agentTags: doc.agent_tags || [],
    content: doc.content,
    url: `${getApiUrl()}/api/createos/documents/${doc.id}/`,
    taskIds: doc.task_ids || [],
    epicIds: doc.epic_ids || [],
  };
}

// =============================================================================
// API CLIENT
// =============================================================================

/**
 * Make a request to the createOS API
 */
async function apiRequest<T>(
  endpoint: string,
  options: RequestInit = {},
): Promise<T> {
  const url = `${getApiUrl()}${endpoint}`;

  const response = await fetch(url, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...options.headers,
    },
  });

  if (!response.ok) {
    const error = await response.text();
    throw new Error(`createOS API error (${response.status}): ${error}`);
  }

  // Handle 204 No Content (common for DELETE) and empty responses
  if (
    response.status === 204 ||
    response.headers.get("content-length") === "0"
  ) {
    return undefined as T;
  }

  const text = await response.text();
  if (!text) {
    return undefined as T;
  }

  return JSON.parse(text);
}

// =============================================================================
// TASK OPERATIONS
// =============================================================================

/**
 * Query tasks from createOS Django backend
 */
export async function queryTasksDjango(
  options: TaskListOptions = {},
): Promise<NotionTask[]> {
  const params = new URLSearchParams();

  // Status filter
  if (options.status) {
    const statuses = Array.isArray(options.status)
      ? options.status
      : [options.status];
    // Django API accepts single status, so we'll use the first one
    // TODO: Support multiple statuses with OR query
    const djangoStatus = NOTION_TO_DJANGO_STATUS[statuses[0]];
    if (djangoStatus) {
      params.append("status", djangoStatus);
    }
  }

  // Priority filter
  if (options.priority) {
    const djangoPriority = NOTION_TO_DJANGO_PRIORITY[options.priority];
    if (djangoPriority) {
      params.append("priority", djangoPriority);
    }
  }

  // Task type filter
  if (options.taskType) {
    const djangoTaskType = Object.entries(DJANGO_TO_NOTION_TASK_TYPE).find(
      ([, v]) => v === options.taskType,
    )?.[0];
    if (djangoTaskType) {
      params.append("task_category", djangoTaskType);
    }
  }

  // Agent filters
  if (options.agentStatus) {
    params.append("agent_status", options.agentStatus);
  }
  if (options.assignedAgent) {
    params.append("assigned_agent", options.assignedAgent);
  }

  // Relation filters - use short_id for ID lookups
  if (options.domain) {
    // Check if it's a short_id (8 chars) or name
    if (options.domain.length === 8 && /^[a-f0-9]+$/i.test(options.domain)) {
      params.append("domain_short_id", options.domain);
    } else {
      params.append("search", options.domain);
    }
  }

  if (options.epic) {
    if (options.epic.length === 8 && /^[a-f0-9]+$/i.test(options.epic)) {
      params.append("epic_short_id", options.epic);
    } else {
      params.append("search", options.epic);
    }
  }

  if (options.sprint) {
    if (options.sprint.length === 8 && /^[a-f0-9]+$/i.test(options.sprint)) {
      params.append("sprint_short_id", options.sprint);
    } else {
      params.append("search", options.sprint);
    }
  }

  // Hierarchy filters
  if (options.hasSubtasks) {
    params.append("is_subtask", "false");
  }
  if (options.isParent) {
    params.append("is_subtask", "false");
  }
  if (options.parentTask) {
    params.append("parent_task", options.parentTask);
  }

  const queryString = params.toString();
  const endpoint = `/api/createos/tasks/${queryString ? `?${queryString}` : ""}`;

  const response =
    await apiRequest<DjangoPaginatedResponse<DjangoTask>>(endpoint);

  return response.results.map(djangoTaskToNotionTask);
}

/**
 * Get a single task by ID or short_id from createOS
 */
export async function getTaskDjango(
  taskId: string,
): Promise<NotionTask | null> {
  try {
    // If it's a short_id (8 chars), query by short_id filter
    if (taskId.length === 8 && /^[a-f0-9]+$/i.test(taskId)) {
      const response = await apiRequest<DjangoPaginatedResponse<DjangoTask>>(
        `/api/createos/tasks/?short_id=${taskId}`,
      );
      // Return first match (short_id filter returns exact matches)
      if (response.results.length > 0) {
        return djangoTaskToNotionTask(response.results[0]);
      }
      return null;
    }

    // Otherwise, assume it's a full UUID
    const task = await apiRequest<DjangoTask>(`/api/createos/tasks/${taskId}/`);
    return djangoTaskToNotionTask(task);
  } catch (error) {
    if ((error as Error).message.includes("404")) {
      return null;
    }
    throw error;
  }
}

/**
 * Update task status in createOS
 */
export async function updateTaskStatusDjango(
  taskId: string,
  status: NotionTaskStatus,
): Promise<NotionTask> {
  const djangoStatus = NOTION_TO_DJANGO_STATUS[status];
  if (!djangoStatus) {
    throw new Error(`Invalid status: ${status}`);
  }

  // Need to get full UUID if given short_id
  let fullId = taskId;
  if (taskId.length === 8) {
    const task = await getTaskDjango(taskId);
    if (!task) {
      throw new Error(`Task not found: ${taskId}`);
    }
    fullId = task.id;
  }

  const updated = await apiRequest<DjangoTask>(
    `/api/createos/tasks/${fullId}/`,
    {
      method: "PATCH",
      body: JSON.stringify({ status: djangoStatus }),
    },
  );

  return djangoTaskToNotionTask(updated);
}

/**
 * Update task properties in createOS
 */
export async function updateTaskDjango(
  taskId: string,
  updates: Partial<{
    title: string;
    description: string;
    status: NotionTaskStatus;
    priority: string;
    epicId: string | null;
    sprintId: string | null;
    branchName: string;
    prUrl: string;
  }>,
): Promise<NotionTask> {
  // Convert to Django field names
  const djangoUpdates: Record<string, unknown> = {};

  if (updates.title) djangoUpdates.title = updates.title;
  if (updates.description) djangoUpdates.description = updates.description;
  if (updates.status) {
    djangoUpdates.status = NOTION_TO_DJANGO_STATUS[updates.status];
  }
  if (updates.priority) {
    djangoUpdates.priority = NOTION_TO_DJANGO_PRIORITY[updates.priority];
  }
  if (updates.epicId !== undefined) djangoUpdates.epic = updates.epicId;
  if (updates.sprintId !== undefined) djangoUpdates.sprint = updates.sprintId;
  if (updates.branchName) djangoUpdates.branch_name = updates.branchName;
  if (updates.prUrl) djangoUpdates.pr_url = updates.prUrl;

  // Need to get full UUID if given short_id
  let fullId = taskId;
  if (taskId.length === 8) {
    const task = await getTaskDjango(taskId);
    if (!task) {
      throw new Error(`Task not found: ${taskId}`);
    }
    fullId = task.id;
  }

  const updated = await apiRequest<DjangoTask>(
    `/api/createos/tasks/${fullId}/`,
    {
      method: "PATCH",
      body: JSON.stringify(djangoUpdates),
    },
  );

  return djangoTaskToNotionTask(updated);
}

/**
 * Create a new task in createOS
 */
export async function createTaskDjango(
  title: string,
  options: {
    description?: string;
    status?: NotionTaskStatus;
    priority?: string;
    epicId?: string;
    sprintId?: string;
    domainId?: string;
    taskCategory?: string;
  } = {},
): Promise<NotionTask> {
  const djangoTask: Record<string, unknown> = {
    title,
    status: options.status ? NOTION_TO_DJANGO_STATUS[options.status] : "future",
  };

  if (options.description) djangoTask.description = options.description;
  if (options.priority) {
    djangoTask.priority = NOTION_TO_DJANGO_PRIORITY[options.priority];
  }
  if (options.epicId) djangoTask.epic = options.epicId;
  if (options.sprintId) djangoTask.sprint = options.sprintId;
  if (options.domainId) djangoTask.domain = options.domainId;
  if (options.taskCategory) djangoTask.task_category = options.taskCategory;

  const created = await apiRequest<DjangoTask>(`/api/createos/tasks/`, {
    method: "POST",
    body: JSON.stringify(djangoTask),
  });

  return djangoTaskToNotionTask(created);
}

/**
 * Start a task (set status to in_progress) in createOS
 */
export async function startTaskDjango(taskId: string): Promise<NotionTask> {
  // Need to get full UUID if given short_id
  let fullId = taskId;
  if (taskId.length === 8) {
    const task = await getTaskDjango(taskId);
    if (!task) {
      throw new Error(`Task not found: ${taskId}`);
    }
    fullId = task.id;
  }

  const updated = await apiRequest<DjangoTask>(
    `/api/createos/tasks/${fullId}/start/`,
    { method: "POST" },
  );

  return djangoTaskToNotionTask(updated);
}

/**
 * Mark task as done (set status to archived) in createOS
 */
export async function doneTaskDjango(taskId: string): Promise<NotionTask> {
  // Need to get full UUID if given short_id
  let fullId = taskId;
  if (taskId.length === 8) {
    const task = await getTaskDjango(taskId);
    if (!task) {
      throw new Error(`Task not found: ${taskId}`);
    }
    fullId = task.id;
  }

  const updated = await apiRequest<DjangoTask>(
    `/api/createos/tasks/${fullId}/done/`,
    { method: "POST" },
  );

  return djangoTaskToNotionTask(updated);
}

// =============================================================================
// EPIC OPERATIONS
// =============================================================================

/**
 * Query epics from createOS
 */
export async function queryEpicsDjango(
  options: { status?: string; domain?: string; limit?: number } = {},
): Promise<NotionEpic[]> {
  const params = new URLSearchParams();

  if (options.status) params.append("status", options.status);
  if (options.domain) {
    if (options.domain.length === 8 && /^[a-f0-9]+$/i.test(options.domain)) {
      params.append("domain_short_id", options.domain);
    } else {
      params.append("search", options.domain);
    }
  }

  const queryString = params.toString();
  const endpoint = `/api/createos/epics/${queryString ? `?${queryString}` : ""}`;

  const response =
    await apiRequest<DjangoPaginatedResponse<DjangoEpic>>(endpoint);

  return response.results.map(djangoEpicToNotionEpic);
}

/**
 * Get a single epic by ID or short_id
 */
export async function getEpicDjango(
  epicId: string,
): Promise<NotionEpic | null> {
  try {
    if (epicId.length === 8 && /^[a-f0-9]+$/i.test(epicId)) {
      const response = await apiRequest<DjangoPaginatedResponse<DjangoEpic>>(
        `/api/createos/epics/?short_id=${epicId}`,
      );
      if (response.results.length > 0) {
        return djangoEpicToNotionEpic(response.results[0]);
      }
      return null;
    }

    const epic = await apiRequest<DjangoEpic>(`/api/createos/epics/${epicId}/`);
    return djangoEpicToNotionEpic(epic);
  } catch (error) {
    if ((error as Error).message.includes("404")) {
      return null;
    }
    throw error;
  }
}

// =============================================================================
// SPRINT OPERATIONS
// =============================================================================

/**
 * Query sprints from createOS
 */
export async function querySprintsDjango(
  options: {
    status?: string;
    domain?: string;
    epic?: string;
    limit?: number;
  } = {},
): Promise<NotionSprint[]> {
  const params = new URLSearchParams();

  if (options.status) params.append("status", options.status);
  if (options.domain) {
    if (options.domain.length === 8 && /^[a-f0-9]+$/i.test(options.domain)) {
      params.append("domain_short_id", options.domain);
    } else {
      params.append("search", options.domain);
    }
  }
  if (options.epic) {
    if (options.epic.length === 8 && /^[a-f0-9]+$/i.test(options.epic)) {
      params.append("epic_short_id", options.epic);
    }
  }

  const queryString = params.toString();
  const endpoint = `/api/createos/sprints/${queryString ? `?${queryString}` : ""}`;

  const response =
    await apiRequest<DjangoPaginatedResponse<DjangoSprint>>(endpoint);

  return response.results.map(djangoSprintToNotionSprint);
}

/**
 * Get a single sprint by ID or short_id
 */
export async function getSprintDjango(
  sprintId: string,
): Promise<NotionSprint | null> {
  try {
    if (sprintId.length === 8 && /^[a-f0-9]+$/i.test(sprintId)) {
      const response = await apiRequest<DjangoPaginatedResponse<DjangoSprint>>(
        `/api/createos/sprints/?search=${sprintId}`,
      );
      const sprint = response.results.find((s) => s.short_id === sprintId);
      return sprint ? djangoSprintToNotionSprint(sprint) : null;
    }

    const sprint = await apiRequest<DjangoSprint>(
      `/api/createos/sprints/${sprintId}/`,
    );
    return djangoSprintToNotionSprint(sprint);
  } catch (error) {
    if ((error as Error).message.includes("404")) {
      return null;
    }
    throw error;
  }
}

// =============================================================================
// DOMAIN OPERATIONS
// =============================================================================

/**
 * Query life domains from createOS
 */
export async function queryDomainsDjango(
  options: { type?: string; isActive?: boolean } = {},
): Promise<NotionLifeDomain[]> {
  const params = new URLSearchParams();

  if (options.type) params.append("type", options.type);
  if (options.isActive !== undefined) {
    params.append("is_active", options.isActive.toString());
  }

  const queryString = params.toString();
  const endpoint = `/api/createos/domains/${queryString ? `?${queryString}` : ""}`;

  const response =
    await apiRequest<DjangoPaginatedResponse<DjangoLifeDomain>>(endpoint);

  return response.results.map(djangoDomainToNotionDomain);
}

/**
 * Get a single domain by ID or short_id
 */
export async function getDomainDjango(
  domainId: string,
): Promise<NotionLifeDomain | null> {
  try {
    if (domainId.length === 8 && /^[a-f0-9]+$/i.test(domainId)) {
      const response = await apiRequest<
        DjangoPaginatedResponse<DjangoLifeDomain>
      >(`/api/createos/domains/?search=${domainId}`);
      const domain = response.results.find((d) => d.short_id === domainId);
      return domain ? djangoDomainToNotionDomain(domain) : null;
    }

    const domain = await apiRequest<DjangoLifeDomain>(
      `/api/createos/domains/${domainId}/`,
    );
    return djangoDomainToNotionDomain(domain);
  } catch (error) {
    if ((error as Error).message.includes("404")) {
      return null;
    }
    throw error;
  }
}

// =============================================================================
// DELETE OPERATIONS
// =============================================================================

/**
 * Delete (trash) a task in createOS
 */
export async function deleteTaskDjango(taskId: string): Promise<void> {
  // Need to get full UUID if given short_id
  let fullId = taskId;
  if (taskId.length === 8) {
    const task = await getTaskDjango(taskId);
    if (!task) {
      throw new Error(`Task not found: ${taskId}`);
    }
    fullId = task.id;
  }

  await apiRequest<void>(`/api/createos/tasks/${fullId}/`, {
    method: "DELETE",
  });
}

/**
 * Update task priority in createOS
 */
export async function updateTaskPriorityDjango(
  taskId: string,
  priority: string,
): Promise<NotionTask> {
  const djangoPriority = NOTION_TO_DJANGO_PRIORITY[priority];
  if (!djangoPriority) {
    throw new Error(`Invalid priority: ${priority}`);
  }

  return updateTaskDjango(taskId, { priority });
}

/**
 * Assign task to epic in createOS
 */
export async function assignTaskToEpicDjango(
  taskId: string,
  epicId: string | null,
): Promise<NotionTask> {
  return updateTaskDjango(taskId, { epicId });
}

/**
 * Assign task to sprint in createOS
 */
export async function assignTaskToSprintDjango(
  taskId: string,
  sprintId: string | null,
): Promise<NotionTask> {
  return updateTaskDjango(taskId, { sprintId });
}

// =============================================================================
// EPIC CRUD OPERATIONS
// =============================================================================

// Map Django epic status to Notion epic status
const DJANGO_TO_NOTION_EPIC_STATUS: Record<string, string> = {
  not_started: "Not Started",
  in_progress: "In Progress",
  completed: "Completed",
  on_hold: "On Hold",
  cancelled: "Cancelled",
};

const NOTION_TO_DJANGO_EPIC_STATUS: Record<string, string> = {
  "Not Started": "not_started",
  "In Progress": "in_progress",
  Completed: "completed",
  "On Hold": "on_hold",
  Cancelled: "cancelled",
};

/**
 * Create a new epic in createOS
 */
export async function createEpicDjango(
  name: string,
  options: {
    subtitle?: string;
    logLine?: string;
    projectType?: string;
    priority?: string;
    lifeDomainId?: string;
    startDate?: string;
    endDate?: string;
    githubRepo?: string;
  } = {},
): Promise<NotionEpic> {
  const djangoEpic: Record<string, unknown> = {
    name,
    status: "not_started",
  };

  if (options.subtitle) djangoEpic.subtitle = options.subtitle;
  if (options.logLine) djangoEpic.log_line = options.logLine;
  if (options.priority) {
    djangoEpic.priority =
      NOTION_TO_DJANGO_PRIORITY[options.priority] || "p3_medium";
  }
  if (options.lifeDomainId) djangoEpic.domain = options.lifeDomainId;
  if (options.startDate) djangoEpic.start_date = options.startDate;
  if (options.endDate) djangoEpic.target_date = options.endDate;
  if (options.githubRepo) djangoEpic.github_repo = options.githubRepo;

  const created = await apiRequest<DjangoEpic>(`/api/createos/epics/`, {
    method: "POST",
    body: JSON.stringify(djangoEpic),
  });

  return djangoEpicToNotionEpic(created);
}

/**
 * Update epic status in createOS
 */
export async function updateEpicStatusDjango(
  epicId: string,
  status: string,
): Promise<NotionEpic> {
  const djangoStatus = NOTION_TO_DJANGO_EPIC_STATUS[status];
  if (!djangoStatus) {
    throw new Error(`Invalid epic status: ${status}`);
  }

  // Need to get full UUID if given short_id
  let fullId = epicId;
  if (epicId.length === 8) {
    const epic = await getEpicDjango(epicId);
    if (!epic) {
      throw new Error(`Epic not found: ${epicId}`);
    }
    fullId = epic.id;
  }

  const updated = await apiRequest<DjangoEpic>(
    `/api/createos/epics/${fullId}/`,
    {
      method: "PATCH",
      body: JSON.stringify({ status: djangoStatus }),
    },
  );

  return djangoEpicToNotionEpic(updated);
}

/**
 * Find epic by name in createOS
 */
export async function findEpicByNameDjango(
  nameOrId: string,
): Promise<NotionEpic | null> {
  // Try short_id first
  if (nameOrId.length === 8 && /^[a-f0-9]+$/i.test(nameOrId)) {
    return getEpicDjango(nameOrId);
  }

  // Search by name
  const response = await apiRequest<DjangoPaginatedResponse<DjangoEpic>>(
    `/api/createos/epics/?search=${encodeURIComponent(nameOrId)}`,
  );

  if (response.results.length === 0) {
    return null;
  }

  // Find exact match by name, or return first result
  const exact = response.results.find(
    (e) => e.name.toLowerCase() === nameOrId.toLowerCase(),
  );
  return exact
    ? djangoEpicToNotionEpic(exact)
    : djangoEpicToNotionEpic(response.results[0]);
}

// =============================================================================
// SPRINT CRUD OPERATIONS
// =============================================================================

// Map Django sprint status to Notion sprint status
const DJANGO_TO_NOTION_SPRINT_STATUS: Record<string, string> = {
  not_started: "Not Started",
  active: "In Progress",
  completed: "Completed",
  cancelled: "Cancelled",
};

const NOTION_TO_DJANGO_SPRINT_STATUS: Record<string, string> = {
  "Not Started": "not_started",
  "In Progress": "active",
  Completed: "completed",
  Cancelled: "cancelled",
};

/**
 * Create a new sprint in createOS
 */
export async function createSprintDjango(
  name: string,
  options: {
    objectives?: string;
    startDate?: string;
    endDate?: string;
    epicId?: string;
    lifeDomainId?: string;
  } = {},
): Promise<NotionSprint> {
  const djangoSprint: Record<string, unknown> = {
    name,
    status: "not_started",
  };

  if (options.objectives) djangoSprint.objectives = options.objectives;
  if (options.startDate) djangoSprint.start_date = options.startDate;
  if (options.endDate) djangoSprint.end_date = options.endDate;
  if (options.epicId) djangoSprint.epic = options.epicId;
  if (options.lifeDomainId) djangoSprint.domain = options.lifeDomainId;

  const created = await apiRequest<DjangoSprint>(`/api/createos/sprints/`, {
    method: "POST",
    body: JSON.stringify(djangoSprint),
  });

  return djangoSprintToNotionSprint(created);
}

/**
 * Update sprint status in createOS
 */
export async function updateSprintStatusDjango(
  sprintId: string,
  status: string,
): Promise<NotionSprint> {
  const djangoStatus = NOTION_TO_DJANGO_SPRINT_STATUS[status];
  if (!djangoStatus) {
    throw new Error(`Invalid sprint status: ${status}`);
  }

  // Need to get full UUID if given short_id
  let fullId = sprintId;
  if (sprintId.length === 8) {
    const sprint = await getSprintDjango(sprintId);
    if (!sprint) {
      throw new Error(`Sprint not found: ${sprintId}`);
    }
    fullId = sprint.id;
  }

  const updated = await apiRequest<DjangoSprint>(
    `/api/createos/sprints/${fullId}/`,
    {
      method: "PATCH",
      body: JSON.stringify({ status: djangoStatus }),
    },
  );

  return djangoSprintToNotionSprint(updated);
}

/**
 * Get the current active sprint in createOS
 */
export async function getCurrentSprintDjango(): Promise<NotionSprint | null> {
  const response = await apiRequest<DjangoPaginatedResponse<DjangoSprint>>(
    `/api/createos/sprints/?status=active`,
  );

  if (response.results.length === 0) {
    return null;
  }

  // Return the most recent active sprint
  return djangoSprintToNotionSprint(response.results[0]);
}

/**
 * Find sprint by name in createOS
 */
export async function findSprintByNameDjango(
  nameOrId: string,
): Promise<NotionSprint | null> {
  // Try short_id first
  if (nameOrId.length === 8 && /^[a-f0-9]+$/i.test(nameOrId)) {
    return getSprintDjango(nameOrId);
  }

  // Search by name
  const response = await apiRequest<DjangoPaginatedResponse<DjangoSprint>>(
    `/api/createos/sprints/?search=${encodeURIComponent(nameOrId)}`,
  );

  if (response.results.length === 0) {
    return null;
  }

  // Find exact match by name, or return first result
  const exact = response.results.find(
    (s) => s.name.toLowerCase() === nameOrId.toLowerCase(),
  );
  return exact
    ? djangoSprintToNotionSprint(exact)
    : djangoSprintToNotionSprint(response.results[0]);
}

// =============================================================================
// DOMAIN CRUD OPERATIONS
// =============================================================================

/**
 * Find domain by name in createOS
 */
export async function findDomainByNameDjango(
  nameOrId: string,
): Promise<NotionLifeDomain | null> {
  // Try short_id first
  if (nameOrId.length === 8 && /^[a-f0-9]+$/i.test(nameOrId)) {
    return getDomainDjango(nameOrId);
  }

  // Search by name
  const response = await apiRequest<DjangoPaginatedResponse<DjangoLifeDomain>>(
    `/api/createos/domains/?search=${encodeURIComponent(nameOrId)}`,
  );

  if (response.results.length === 0) {
    return null;
  }

  // Find exact match by name, or return first result
  const exact = response.results.find(
    (d) => d.name.toLowerCase() === nameOrId.toLowerCase(),
  );
  return exact
    ? djangoDomainToNotionDomain(exact)
    : djangoDomainToNotionDomain(response.results[0]);
}

// =============================================================================
// USER STORY CRUD OPERATIONS
// =============================================================================

// Map Django story status to Notion-compatible status
const DJANGO_TO_NOTION_STORY_STATUS: Record<string, string> = {
  draft: "Draft",
  ready: "Ready",
  in_progress: "In Progress",
  done: "Done",
  blocked: "Blocked",
};

const NOTION_TO_DJANGO_STORY_STATUS: Record<string, string> = {
  Draft: "draft",
  Ready: "ready",
  "In Progress": "in_progress",
  Done: "done",
  Blocked: "blocked",
  // Also map from Notion-style status names
  "Not Started": "draft",
  Completed: "done",
};

/**
 * Convert Django UserStory to Notion-compatible format
 */
function djangoStoryToNotionStory(story: DjangoUserStory): NotionUserStory {
  return {
    id: story.id,
    shortId: story.short_id,
    name: story.name,
    description: story.description || undefined,
    acceptanceCriteria: story.acceptance_criteria || undefined,
    status: DJANGO_TO_NOTION_STORY_STATUS[story.status] || story.status,
    priority: story.priority
      ? DJANGO_TO_NOTION_PRIORITY[story.priority] || story.priority
      : null,
    userType: story.user_type,
    url: `${getApiUrl()}/admin/createos/userstory/${story.id}/change/`,
    epicId: story.epic,
    epicName: story.epic_name,
    sprintId: story.sprint,
    taskIds: [], // Not returned by API, would need separate query
  };
}

/**
 * Query user stories from createOS
 */
export async function queryUserStoriesDjango(
  options: {
    status?: string | string[];
    epicId?: string;
    sprintId?: string;
    limit?: number;
  } = {},
): Promise<NotionUserStory[]> {
  const params = new URLSearchParams();

  if (options.status) {
    const statuses = Array.isArray(options.status)
      ? options.status
      : [options.status];
    // Convert to Django status if needed
    const djangoStatus =
      NOTION_TO_DJANGO_STORY_STATUS[statuses[0]] || statuses[0].toLowerCase();
    params.set("status", djangoStatus);
  }

  if (options.epicId) {
    params.set("epic", options.epicId);
  }

  if (options.sprintId) {
    params.set("sprint", options.sprintId);
  }

  if (options.limit) {
    params.set("page_size", options.limit.toString());
  }

  const queryString = params.toString();
  const url = `/api/createos/stories/${queryString ? `?${queryString}` : ""}`;

  const response =
    await apiRequest<DjangoPaginatedResponse<DjangoUserStory>>(url);
  return response.results.map(djangoStoryToNotionStory);
}

/**
 * Get a single user story by ID or short_id
 */
export async function getUserStoryDjango(
  storyId: string,
): Promise<NotionUserStory | null> {
  try {
    // Try UUID first, then short_id lookup
    const endpoint =
      storyId.length === 8 && /^[a-f0-9]+$/i.test(storyId)
        ? `/api/createos/stories/?short_id=${storyId}`
        : `/api/createos/stories/${storyId}/`;

    if (storyId.length === 8) {
      const response =
        await apiRequest<DjangoPaginatedResponse<DjangoUserStory>>(endpoint);
      if (response.results.length === 0) return null;
      // Find exact match by short_id
      const exact = response.results.find(
        (s) => s.short_id.toLowerCase() === storyId.toLowerCase(),
      );
      return exact ? djangoStoryToNotionStory(exact) : null;
    }

    const story = await apiRequest<DjangoUserStory>(endpoint);
    return djangoStoryToNotionStory(story);
  } catch {
    return null;
  }
}

/**
 * Create a new user story in createOS
 */
export async function createUserStoryDjango(
  name: string,
  options: {
    description?: string;
    acceptanceCriteria?: string;
    userType?: string;
    priority?: string;
    epicId?: string;
    sprintId?: string;
    storyPoints?: number;
  } = {},
): Promise<NotionUserStory> {
  const payload: Record<string, unknown> = {
    name,
    status: "draft", // Default to draft
  };

  if (options.description) {
    payload.description = options.description;
  }

  if (options.acceptanceCriteria) {
    payload.acceptance_criteria = options.acceptanceCriteria;
  }

  if (options.userType) {
    payload.user_type = options.userType;
  }

  if (options.priority) {
    payload.priority =
      NOTION_TO_DJANGO_PRIORITY[options.priority] ||
      options.priority.toLowerCase().replace(/\s+/g, "_");
  }

  if (options.epicId) {
    payload.epic = options.epicId;
  }

  if (options.sprintId) {
    payload.sprint = options.sprintId;
  }

  if (options.storyPoints) {
    payload.story_points = options.storyPoints;
  }

  const story = await apiRequest<DjangoUserStory>("/api/createos/stories/", {
    method: "POST",
    body: JSON.stringify(payload),
  });

  return djangoStoryToNotionStory(story);
}

/**
 * Find user story by name or short_id in createOS
 */
export async function findUserStoryByNameDjango(
  nameOrId: string,
): Promise<NotionUserStory | null> {
  // Try short_id first
  if (nameOrId.length === 8 && /^[a-f0-9]+$/i.test(nameOrId)) {
    return getUserStoryDjango(nameOrId);
  }

  // Search by name
  const response = await apiRequest<DjangoPaginatedResponse<DjangoUserStory>>(
    `/api/createos/stories/?search=${encodeURIComponent(nameOrId)}`,
  );

  if (response.results.length === 0) {
    return null;
  }

  // Find exact match by name, or return first result
  const exact = response.results.find(
    (s) => s.name.toLowerCase() === nameOrId.toLowerCase(),
  );
  return exact
    ? djangoStoryToNotionStory(exact)
    : djangoStoryToNotionStory(response.results[0]);
}

// =============================================================================
// DOCUMENT OPERATIONS
// =============================================================================

/**
 * Query documents from createOS
 */
export async function queryDocumentsDjango(
  options: DocumentListOptions = {},
): Promise<NotionDocument[]> {
  const params = new URLSearchParams();

  if (options.status) {
    const djangoStatus =
      NOTION_TO_DJANGO_DOC_STATUS[options.status] ||
      options.status.toLowerCase();
    params.append("status", djangoStatus);
  }

  if (options.contentType) {
    const djangoType =
      NOTION_TO_DJANGO_DOC_TYPE[options.contentType] ||
      options.contentType.toLowerCase();
    params.append("document_type", djangoType);
  }

  if (options.tags && options.tags.length > 0) {
    // Filter by agent tags
    params.append("agent_tags", options.tags.join(","));
  }

  if (options.limit) {
    params.append("page_size", options.limit.toString());
  }

  const queryString = params.toString();
  const endpoint = `/api/createos/documents/${queryString ? `?${queryString}` : ""}`;

  const response =
    await apiRequest<DjangoPaginatedResponse<DjangoDocument>>(endpoint);

  return response.results.map(djangoDocumentToNotionDocument);
}

/**
 * Get a single document by ID or short_id from createOS
 */
export async function getDocumentDjango(
  docId: string,
): Promise<NotionDocument | null> {
  try {
    // If it's a short_id (8 chars), query by short_id filter
    if (docId.length === 8 && /^[a-f0-9]+$/i.test(docId)) {
      const response = await apiRequest<
        DjangoPaginatedResponse<DjangoDocument>
      >(`/api/createos/documents/?short_id=${docId}`);
      if (response.results.length > 0) {
        return djangoDocumentToNotionDocument(response.results[0]);
      }
      return null;
    }

    // Otherwise, assume it's a full UUID
    const doc = await apiRequest<DjangoDocument>(
      `/api/createos/documents/${docId}/`,
    );
    return djangoDocumentToNotionDocument(doc);
  } catch (error) {
    if ((error as Error).message.includes("404")) {
      return null;
    }
    throw error;
  }
}

/**
 * Get document content (markdown) from createOS
 */
export async function getDocumentContentDjango(
  docId: string,
): Promise<string | null> {
  const doc = await getDocumentDjango(docId);
  return doc?.content || null;
}

/**
 * Create a new document in createOS
 */
export async function createDocumentDjango(
  name: string,
  options: {
    content?: string;
    documentType?: string;
    status?: string;
    version?: string;
    agentTags?: string[];
    domainId?: string;
    epicIds?: string[];
    taskIds?: string[];
  } = {},
): Promise<NotionDocument> {
  const djangoDoc: Record<string, unknown> = {
    name,
    status: options.status
      ? NOTION_TO_DJANGO_DOC_STATUS[options.status] || "draft"
      : "draft",
  };

  if (options.content) djangoDoc.content = options.content;
  if (options.documentType) {
    djangoDoc.document_type =
      NOTION_TO_DJANGO_DOC_TYPE[options.documentType] ||
      options.documentType.toLowerCase();
  }
  if (options.version) djangoDoc.version = options.version;
  if (options.agentTags) djangoDoc.agent_tags = options.agentTags;
  if (options.domainId) djangoDoc.domain = options.domainId;
  if (options.epicIds) djangoDoc.epics = options.epicIds;
  if (options.taskIds) djangoDoc.tasks = options.taskIds;

  const created = await apiRequest<DjangoDocument>(`/api/createos/documents/`, {
    method: "POST",
    body: JSON.stringify(djangoDoc),
  });

  return djangoDocumentToNotionDocument(created);
}

/**
 * Update a document in createOS
 */
export async function updateDocumentDjango(
  docId: string,
  updates: Partial<{
    name: string;
    content: string;
    documentType: string;
    status: string;
    version: string;
    agentTags: string[];
    domainId: string | null;
    epicIds: string[];
    taskIds: string[];
  }>,
): Promise<NotionDocument> {
  const djangoUpdates: Record<string, unknown> = {};

  if (updates.name) djangoUpdates.name = updates.name;
  if (updates.content) djangoUpdates.content = updates.content;
  if (updates.documentType) {
    djangoUpdates.document_type =
      NOTION_TO_DJANGO_DOC_TYPE[updates.documentType] ||
      updates.documentType.toLowerCase();
  }
  if (updates.status) {
    djangoUpdates.status =
      NOTION_TO_DJANGO_DOC_STATUS[updates.status] ||
      updates.status.toLowerCase();
  }
  if (updates.version) djangoUpdates.version = updates.version;
  if (updates.agentTags) djangoUpdates.agent_tags = updates.agentTags;
  if (updates.domainId !== undefined) djangoUpdates.domain = updates.domainId;
  if (updates.epicIds) djangoUpdates.epics = updates.epicIds;
  if (updates.taskIds) djangoUpdates.tasks = updates.taskIds;

  // Get full UUID if given short_id
  let fullId = docId;
  if (docId.length === 8) {
    const doc = await getDocumentDjango(docId);
    if (!doc) {
      throw new Error(`Document not found: ${docId}`);
    }
    fullId = doc.id;
  }

  const updated = await apiRequest<DjangoDocument>(
    `/api/createos/documents/${fullId}/`,
    {
      method: "PATCH",
      body: JSON.stringify(djangoUpdates),
    },
  );

  return djangoDocumentToNotionDocument(updated);
}

/**
 * Delete a document in createOS
 */
export async function deleteDocumentDjango(docId: string): Promise<void> {
  // Get full UUID if given short_id
  let fullId = docId;
  if (docId.length === 8) {
    const doc = await getDocumentDjango(docId);
    if (!doc) {
      throw new Error(`Document not found: ${docId}`);
    }
    fullId = doc.id;
  }

  await apiRequest<void>(`/api/createos/documents/${fullId}/`, {
    method: "DELETE",
  });
}

/**
 * Search documents by name or content in createOS
 */
export async function searchDocumentsDjango(
  query: string,
  options: { limit?: number } = {},
): Promise<NotionDocument[]> {
  const params = new URLSearchParams();
  params.append("search", query);

  if (options.limit) {
    params.append("page_size", options.limit.toString());
  }

  const endpoint = `/api/createos/documents/?${params.toString()}`;
  const response =
    await apiRequest<DjangoPaginatedResponse<DjangoDocument>>(endpoint);

  return response.results.map(djangoDocumentToNotionDocument);
}

/**
 * Find document by name or short_id in createOS
 */
export async function findDocumentByNameDjango(
  nameOrId: string,
): Promise<NotionDocument | null> {
  // Try short_id first
  if (nameOrId.length === 8 && /^[a-f0-9]+$/i.test(nameOrId)) {
    return getDocumentDjango(nameOrId);
  }

  // Search by name
  const response = await apiRequest<DjangoPaginatedResponse<DjangoDocument>>(
    `/api/createos/documents/?search=${encodeURIComponent(nameOrId)}`,
  );

  if (response.results.length === 0) {
    return null;
  }

  // Find exact match by name, or return first result
  const exact = response.results.find(
    (d) => d.name.toLowerCase() === nameOrId.toLowerCase(),
  );
  return exact
    ? djangoDocumentToNotionDocument(exact)
    : djangoDocumentToNotionDocument(response.results[0]);
}

// =============================================================================
// USER STORY INTERVIEW OPERATIONS
// =============================================================================

/**
 * Interview session types
 */
export interface InterviewSession {
  id: string;
  shortId: string;
  roundType: string;
  roundTypeDisplay: string;
  status: string;
  transcript: Array<{ role: string; content: string; timestamp?: string }>;
  findings: Record<string, unknown>;
  completedAt: string | null;
  createdAt: string;
  updatedAt: string;
}

export interface StoryContext {
  story: NotionUserStory & {
    discoveryStatus: string;
    currentInterviewRound: string;
    personas: Array<Record<string, unknown>>;
    userFlows: Record<string, unknown>;
    rbacRequirements: Array<Record<string, unknown>>;
    technicalConstraints: Array<string>;
    edgeCases: Array<Record<string, unknown>>;
    interviewProgress: {
      currentRound: string;
      completedRounds: number;
      totalRounds: number;
      percentComplete: number;
    };
  };
  epic: NotionEpic | null;
  sprint: NotionSprint | null;
  tasks: NotionTask[];
  interviewSessions: InterviewSession[];
  layers: string[];
}

interface DjangoInterviewSession {
  id: string;
  short_id: string;
  round_type: string;
  round_type_display: string;
  status: string;
  transcript: Array<{ role: string; content: string; timestamp?: string }>;
  findings: Record<string, unknown>;
  completed_at: string | null;
  created_at: string;
  updated_at: string;
}

/**
 * Get full story context for Claude consumption
 */
export async function getStoryContextDjango(
  storyId: string,
): Promise<StoryContext | null> {
  try {
    // Resolve short_id to full UUID if needed
    let fullId = storyId;
    if (storyId.length === 8 && /^[a-f0-9]+$/i.test(storyId)) {
      const story = await getUserStoryDjango(storyId);
      if (!story) return null;
      fullId = story.id;
    }

    const response = await apiRequest<{
      story: DjangoUserStory & {
        discovery_status: string;
        current_interview_round: string;
        personas: Array<Record<string, unknown>>;
        user_flows: Record<string, unknown>;
        rbac_requirements: Array<Record<string, unknown>>;
        technical_constraints: Array<string>;
        edge_cases: Array<Record<string, unknown>>;
        interview_progress: {
          current_round: string;
          completed_rounds: number;
          total_rounds: number;
          percent_complete: number;
        };
      };
      epic: DjangoEpic | null;
      sprint: DjangoSprint | null;
      tasks: DjangoTask[];
      interview_sessions: DjangoInterviewSession[];
      layers: string[];
    }>(`/api/createos/stories/${fullId}/context/`);

    return {
      story: {
        ...djangoStoryToNotionStory(response.story),
        discoveryStatus: response.story.discovery_status,
        currentInterviewRound: response.story.current_interview_round,
        personas: response.story.personas || [],
        userFlows: response.story.user_flows || {},
        rbacRequirements: response.story.rbac_requirements || [],
        technicalConstraints: response.story.technical_constraints || [],
        edgeCases: response.story.edge_cases || [],
        interviewProgress: {
          currentRound: response.story.interview_progress.current_round,
          completedRounds: response.story.interview_progress.completed_rounds,
          totalRounds: response.story.interview_progress.total_rounds,
          percentComplete: response.story.interview_progress.percent_complete,
        },
      },
      epic: response.epic ? djangoEpicToNotionEpic(response.epic) : null,
      sprint: response.sprint
        ? djangoSprintToNotionSprint(response.sprint)
        : null,
      tasks: response.tasks.map(djangoTaskToNotionTask),
      interviewSessions: response.interview_sessions.map((s) => ({
        id: s.id,
        shortId: s.short_id,
        roundType: s.round_type,
        roundTypeDisplay: s.round_type_display,
        status: s.status,
        transcript: s.transcript,
        findings: s.findings,
        completedAt: s.completed_at,
        createdAt: s.created_at,
        updatedAt: s.updated_at,
      })),
      layers: response.layers,
    };
  } catch (error) {
    if ((error as Error).message.includes("404")) {
      return null;
    }
    throw error;
  }
}

/**
 * Start or resume an interview session for a user story
 */
export async function startInterviewDjango(
  storyId: string,
  round?: string,
): Promise<InterviewSession> {
  // Resolve short_id to full UUID if needed
  let fullId = storyId;
  if (storyId.length === 8 && /^[a-f0-9]+$/i.test(storyId)) {
    const story = await getUserStoryDjango(storyId);
    if (!story) throw new Error(`Story not found: ${storyId}`);
    fullId = story.id;
  }

  const body: Record<string, unknown> = {};
  if (round) body.round = round;

  const response = await apiRequest<DjangoInterviewSession>(
    `/api/createos/stories/${fullId}/start_interview/`,
    {
      method: "POST",
      body: JSON.stringify(body),
    },
  );

  return {
    id: response.id,
    shortId: response.short_id,
    roundType: response.round_type,
    roundTypeDisplay: response.round_type_display,
    status: response.status,
    transcript: response.transcript,
    findings: response.findings,
    completedAt: response.completed_at,
    createdAt: response.created_at,
    updatedAt: response.updated_at,
  };
}

/**
 * Record an interview message
 */
export async function recordInterviewDjango(
  storyId: string,
  data: {
    role: "user" | "assistant";
    content: string;
    findings?: Record<string, unknown>;
  },
): Promise<InterviewSession> {
  // Resolve short_id to full UUID if needed
  let fullId = storyId;
  if (storyId.length === 8 && /^[a-f0-9]+$/i.test(storyId)) {
    const story = await getUserStoryDjango(storyId);
    if (!story) throw new Error(`Story not found: ${storyId}`);
    fullId = story.id;
  }

  const response = await apiRequest<DjangoInterviewSession>(
    `/api/createos/stories/${fullId}/record_interview/`,
    {
      method: "POST",
      body: JSON.stringify(data),
    },
  );

  return {
    id: response.id,
    shortId: response.short_id,
    roundType: response.round_type,
    roundTypeDisplay: response.round_type_display,
    status: response.status,
    transcript: response.transcript,
    findings: response.findings,
    completedAt: response.completed_at,
    createdAt: response.created_at,
    updatedAt: response.updated_at,
  };
}

/**
 * Complete the current interview round and advance to the next
 */
export async function completeRoundDjango(storyId: string): Promise<{
  previousRound: string;
  newRound: string;
  discoveryStatus: string;
  message: string;
}> {
  // Resolve short_id to full UUID if needed
  let fullId = storyId;
  if (storyId.length === 8 && /^[a-f0-9]+$/i.test(storyId)) {
    const story = await getUserStoryDjango(storyId);
    if (!story) throw new Error(`Story not found: ${storyId}`);
    fullId = story.id;
  }

  return apiRequest<{
    previousRound: string;
    newRound: string;
    discoveryStatus: string;
    message: string;
  }>(`/api/createos/stories/${fullId}/complete_round/`, {
    method: "POST",
  });
}

/**
 * Get user flows for a story as Mermaid diagram
 */
export async function getStoryFlowsDjango(
  storyId: string,
): Promise<{ flows: Record<string, unknown>; mermaid: string }> {
  // Resolve short_id to full UUID if needed
  let fullId = storyId;
  if (storyId.length === 8 && /^[a-f0-9]+$/i.test(storyId)) {
    const story = await getUserStoryDjango(storyId);
    if (!story) throw new Error(`Story not found: ${storyId}`);
    fullId = story.id;
  }

  return apiRequest<{ flows: Record<string, unknown>; mermaid: string }>(
    `/api/createos/stories/${fullId}/flows/`,
  );
}

/**
 * Generate tasks from story acceptance criteria
 */
export async function generateTasksFromStoryDjango(
  storyId: string,
  dryRun = false,
): Promise<{
  tasks: Array<{ title: string; description: string; id?: string }>;
  created: boolean;
}> {
  // Resolve short_id to full UUID if needed
  let fullId = storyId;
  if (storyId.length === 8 && /^[a-f0-9]+$/i.test(storyId)) {
    const story = await getUserStoryDjango(storyId);
    if (!story) throw new Error(`Story not found: ${storyId}`);
    fullId = story.id;
  }

  return apiRequest<{
    tasks: Array<{ title: string; description: string; id?: string }>;
    created: boolean;
  }>(`/api/createos/stories/${fullId}/generate_tasks/`, {
    method: "POST",
    body: JSON.stringify({ dry_run: dryRun }),
  });
}
