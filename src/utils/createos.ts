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
  TaskListOptions,
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
    epicIds: sprint.epic ? [sprint.epic] : [],
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

  return response.json();
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
    // If it's a short_id (8 chars), query by short_id
    if (taskId.length === 8 && /^[a-f0-9]+$/i.test(taskId)) {
      const response = await apiRequest<DjangoPaginatedResponse<DjangoTask>>(
        `/api/createos/tasks/?search=${taskId}`,
      );
      // Find exact match by short_id
      const task = response.results.find((t) => t.short_id === taskId);
      return task ? djangoTaskToNotionTask(task) : null;
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
        `/api/createos/epics/?search=${epicId}`,
      );
      const epic = response.results.find((e) => e.short_id === epicId);
      return epic ? djangoEpicToNotionEpic(epic) : null;
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
