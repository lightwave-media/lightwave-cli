/**
 * Notion client utilities for lightwave-cli task management
 */

import { Client } from "@notionhq/client";
import { SSMClient, GetParameterCommand } from "@aws-sdk/client-ssm";
import {
  NOTION_DB_IDS,
  type NotionConfig,
  type NotionTask,
  type NotionTaskStatus,
  type NotionSprint,
  type NotionEpic,
  type NotionLifeDomain,
  type NotionDocument,
  type NotionUserStory,
  type TaskType,
  type SprintStatus,
  type EpicStatus,
  type TaskListOptions,
  type DocumentListOptions,
  type TaskContext,
} from "../types/notion.js";

// Singleton instances
let notionClient: Client | null = null;
let notionConfig: NotionConfig | null = null;

/**
 * Load Notion credentials from env vars or AWS Parameter Store
 * Database IDs are hardcoded from NOTION_DB_IDS (they don't change)
 */
async function loadConfig(): Promise<NotionConfig> {
  let apiKey: string;

  // Check environment variables first (for local dev)
  if (process.env.NOTION_API_KEY) {
    apiKey = process.env.NOTION_API_KEY;
  } else {
    // Fall back to AWS Parameter Store
    const ssm = new SSMClient({ region: "us-east-1" });

    try {
      const apiKeyResult = await ssm.send(
        new GetParameterCommand({
          Name: "/lightwave/prod/NOTION_API_KEY",
          WithDecryption: true,
        }),
      );

      if (!apiKeyResult.Parameter?.Value) {
        throw new Error("Missing Notion API key in AWS Parameter Store");
      }

      apiKey = apiKeyResult.Parameter.Value;
    } catch (err) {
      const error = err as Error & { name?: string };
      if (error.name === "ParameterNotFound") {
        throw new Error(
          "Notion credentials not found. Set NOTION_API_KEY env var, " +
            "or ensure AWS Parameter Store has /lightwave/prod/NOTION_API_KEY",
        );
      }
      throw err;
    }
  }

  // Use hardcoded database IDs - they don't change
  return {
    apiKey,
    tasksDbId: NOTION_DB_IDS.tasks,
    epicsDbId: NOTION_DB_IDS.epics,
    sprintsDbId: NOTION_DB_IDS.sprints,
    userStoriesDbId: NOTION_DB_IDS.userStories,
    lifeDomainsDbId: NOTION_DB_IDS.lifeDomains,
    documentsDbId: NOTION_DB_IDS.documents,
  };
}

/**
 * Get or create Notion client singleton
 */
export async function getNotionClient(): Promise<{
  client: Client;
  config: NotionConfig;
}> {
  if (!notionClient || !notionConfig) {
    notionConfig = await loadConfig();
    // Use older API version for databases.query compatibility
    notionClient = new Client({
      auth: notionConfig.apiKey,
      notionVersion: "2022-06-28",
    });
  }
  return { client: notionClient, config: notionConfig };
}

/**
 * Response type for database query
 */
interface DatabaseQueryResponse {
  results: Array<Record<string, unknown>>;
  has_more: boolean;
  next_cursor: string | null;
}

/**
 * Query tasks from Notion database with optional filters
 * Uses client.request() because SDK v5 dataSources.query uses /data_sources/ endpoint
 * which doesn't work with databases created before 2025 API
 */
export async function queryTasks(
  options: TaskListOptions = {},
): Promise<NotionTask[]> {
  const { client, config } = await getNotionClient();

  // Build filter conditions
  const conditions: Array<Record<string, unknown>> = [];

  // Status filter
  if (options.status) {
    const statuses = Array.isArray(options.status)
      ? options.status
      : [options.status];
    if (statuses.length === 1) {
      conditions.push({
        property: "Status",
        status: { equals: statuses[0] },
      });
    } else {
      conditions.push({
        or: statuses.map((s) => ({
          property: "Status",
          status: { equals: s },
        })),
      });
    }
  }

  // Domain filter - filter by Life Domain relation
  if (options.domain) {
    const domain = await findLifeDomainByName(options.domain);
    if (domain) {
      conditions.push({
        property: "🌐 Life Domains DB",
        relation: { contains: domain.id },
      });
    }
  }

  // Epic filter - filter by Epic relation
  if (options.epic) {
    const epic = await findEpicByName(options.epic);
    if (epic) {
      conditions.push({
        property: "🌐 Global Projects & Epics DB ",
        relation: { contains: epic.id },
      });
    }
  }

  // Sprint filter - filter by Sprint relation
  if (options.sprint) {
    const sprint = await findSprintByName(options.sprint);
    if (sprint) {
      conditions.push({
        property: "🛠️  Global Sprints DB",
        relation: { contains: sprint.id },
      });
    }
  }

  // User Story filter - filter by User Story relation
  if (options.userStory) {
    const story = await findUserStoryByName(options.userStory);
    if (story) {
      conditions.push({
        property: "👤 User Stories ",
        relation: { contains: story.id },
      });
    }
  }

  // Priority filter
  if (options.priority) {
    conditions.push({
      property: "Priority",
      select: { equals: options.priority },
    });
  }

  // Task Type filter (from select field, not inferred)
  if (options.taskType) {
    conditions.push({
      property: "Task Type",
      select: { equals: options.taskType },
    });
  }

  // Agent Status filter
  if (options.agentStatus) {
    conditions.push({
      property: "Agent Status",
      select: { equals: options.agentStatus },
    });
  }

  // Assigned Agent filter
  if (options.assignedAgent) {
    conditions.push({
      property: "Assigned Agent",
      select: { equals: options.assignedAgent },
    });
  }

  // Assignee filter (people field - search by name)
  if (options.assignee) {
    conditions.push({
      property: "Assignee",
      people: { contains: options.assignee },
    });
  }

  // Due date filters
  if (options.dueBefore) {
    conditions.push({
      property: "Due Date",
      date: { before: options.dueBefore },
    });
  }
  if (options.dueAfter) {
    conditions.push({
      property: "Due Date",
      date: { after: options.dueAfter },
    });
  }

  // Subtask filter - tasks with subtasks (relation is not empty)
  if (options.hasSubtasks) {
    conditions.push({
      property: "Sub Task",
      relation: { is_not_empty: true },
    });
  }

  // Parent task filter - tasks that are parents (have subtasks)
  if (options.isParent) {
    conditions.push({
      property: "Sub Task",
      relation: { is_not_empty: true },
    });
  }

  // Filter by specific parent task
  if (options.parentTask) {
    conditions.push({
      property: "Parent Task",
      relation: { contains: options.parentTask },
    });
  }

  // Combine filters
  let filter: Record<string, unknown> | undefined;
  if (conditions.length === 1) {
    filter = conditions[0];
  } else if (conditions.length > 1) {
    filter = { and: conditions };
  }

  // Use raw request to /databases/ endpoint (SDK v5 dataSources uses /data_sources/ which doesn't work)
  const response = await client.request<DatabaseQueryResponse>({
    path: `databases/${config.tasksDbId}/query`,
    method: "post",
    body: {
      filter,
      page_size: options.limit || 100,
      sorts: [{ timestamp: "last_edited_time", direction: "descending" }],
    },
  });

  return response.results.map(pageToTask);
}

/**
 * Get a single task by ID (full or short)
 */
export async function getTask(taskId: string): Promise<NotionTask | null> {
  const { client, config } = await getNotionClient();

  // Normalize ID - remove dashes for comparison
  const normalizedId = taskId.replace(/-/g, "").toLowerCase();

  // If it looks like a full UUID (32 hex chars), try direct retrieval
  if (normalizedId.length === 32 && /^[a-f0-9]+$/.test(normalizedId)) {
    try {
      const page = await client.pages.retrieve({ page_id: taskId });
      return pageToTask(page as Record<string, unknown>);
    } catch {
      // Fall through to search
    }
  }

  // Search by querying all tasks and matching short ID
  // Use raw request to /databases/ endpoint with pagination
  let allResults: Array<Record<string, unknown>> = [];
  let hasMore = true;
  let startCursor: string | undefined;

  while (hasMore) {
    const response = await client.request<DatabaseQueryResponse>({
      path: `databases/${config.tasksDbId}/query`,
      method: "post",
      body: {
        page_size: 100,
        start_cursor: startCursor,
        sorts: [{ timestamp: "last_edited_time", direction: "descending" }],
      },
    });
    allResults = allResults.concat(response.results);
    hasMore = response.has_more;
    startCursor = response.next_cursor || undefined;

    // Stop early if we found a match (optimization)
    const shortId = normalizedId.substring(0, 8);
    const earlyMatch = response.results.find((page) => {
      const pageShortId = (page.id as string)
        .replace(/-/g, "")
        .substring(0, 8)
        .toLowerCase();
      return pageShortId === shortId;
    });
    if (earlyMatch) {
      return pageToTask(earlyMatch);
    }
  }

  // If we got here, no exact match was found during pagination
  // Check for partial matches in all results
  const shortId = normalizedId.substring(0, 8);
  const matches = allResults.filter((page) => {
    const pageShortId = (page.id as string)
      .replace(/-/g, "")
      .substring(0, 8)
      .toLowerCase();
    return pageShortId.startsWith(shortId);
  });

  if (matches.length === 0) {
    return null;
  }

  if (matches.length > 1) {
    throw new Error(
      `Ambiguous task ID: ${taskId}. Found ${matches.length} matches. Use full ID.`,
    );
  }

  return pageToTask(matches[0]);
}

/**
 * Create a new task in Notion
 */
export async function createTask(
  title: string,
  properties?: Record<string, unknown>,
): Promise<NotionTask> {
  const { client, config } = await getNotionClient();

  const response = await client.pages.create({
    parent: { database_id: config.tasksDbId },
    properties: {
      // Title property - Global Tasks DB uses "Action Item"
      "Action Item": {
        title: [{ text: { content: title } }],
      },
      ...properties,
    },
  });

  return pageToTask(response as Record<string, unknown>);
}

/**
 * Update task status in Notion
 */
export async function updateTaskStatus(
  taskId: string,
  status: NotionTaskStatus,
): Promise<void> {
  const { client } = await getNotionClient();

  await client.pages.update({
    page_id: taskId,
    properties: {
      Status: {
        status: { name: status },
      },
    },
  });
}

/**
 * Update multiple task properties at once
 */
export async function updateTask(
  taskId: string,
  options: {
    status?: NotionTaskStatus;
    priority?: string | null;
    epicId?: string | null;
    sprintId?: string | null;
  },
): Promise<void> {
  const { client } = await getNotionClient();

  // Build properties object based on what's being updated
  const properties: Record<string, unknown> = {};

  if (options.status !== undefined) {
    properties.Status = { status: { name: options.status } };
  }

  if (options.priority !== undefined) {
    if (options.priority === null) {
      // Clear priority
      properties.Priority = { select: null };
    } else {
      properties.Priority = { select: { name: options.priority } };
    }
  }

  if (options.epicId !== undefined) {
    if (options.epicId === null) {
      // Clear epic relation
      properties["🌐 Global Projects & Epics DB "] = { relation: [] };
    } else {
      properties["🌐 Global Projects & Epics DB "] = {
        relation: [{ id: options.epicId }],
      };
    }
  }

  if (options.sprintId !== undefined) {
    if (options.sprintId === null) {
      // Clear sprint relation
      properties["🛠️  Global Sprints DB"] = { relation: [] };
    } else {
      properties["🛠️  Global Sprints DB"] = {
        relation: [{ id: options.sprintId }],
      };
    }
  }

  if (Object.keys(properties).length === 0) {
    return; // Nothing to update
  }

  await client.pages.update({
    page_id: taskId,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    properties: properties as any,
  });
}

/**
 * Update task priority
 */
export async function updateTaskPriority(
  taskId: string,
  priority: string | null,
): Promise<void> {
  await updateTask(taskId, { priority });
}

/**
 * Assign task to an epic
 */
export async function assignTaskToEpic(
  taskId: string,
  epicId: string | null,
): Promise<void> {
  await updateTask(taskId, { epicId });
}

/**
 * Assign task to a sprint
 */
export async function assignTaskToSprint(
  taskId: string,
  sprintId: string | null,
): Promise<void> {
  await updateTask(taskId, { sprintId });
}

/**
 * Delete (trash) a task in Notion
 */
export async function deleteTask(taskId: string): Promise<void> {
  const { client } = await getNotionClient();

  await client.pages.update({
    page_id: taskId,
    in_trash: true,
  });
}

// =============================================================================
// Life Domains
// =============================================================================

/**
 * Query all Life Domains
 */
export async function queryLifeDomains(): Promise<NotionLifeDomain[]> {
  const { client, config } = await getNotionClient();

  const response = await client.request<DatabaseQueryResponse>({
    path: `databases/${config.lifeDomainsDbId}/query`,
    method: "post",
    body: {
      page_size: 100,
      sorts: [{ property: "Pillar Name", direction: "ascending" }],
    },
  });

  return response.results.map(pageToLifeDomain);
}

/**
 * Find a Life Domain by name (case-insensitive partial match)
 */
export async function findLifeDomainByName(
  name: string,
): Promise<NotionLifeDomain | null> {
  const domains = await queryLifeDomains();
  const lowerName = name.toLowerCase();
  return domains.find((d) => d.name.toLowerCase().includes(lowerName)) || null;
}

// =============================================================================
// Epics / Projects
// =============================================================================

/**
 * Query Epics/Projects from Notion
 */
export async function queryEpics(
  options: {
    status?: EpicStatus | EpicStatus[];
    domain?: string;
    limit?: number;
  } = {},
): Promise<NotionEpic[]> {
  const { client, config } = await getNotionClient();

  const conditions: Array<Record<string, unknown>> = [];

  // Status filter
  if (options.status) {
    const statuses = Array.isArray(options.status)
      ? options.status
      : [options.status];
    if (statuses.length === 1) {
      conditions.push({ property: "Status", status: { equals: statuses[0] } });
    } else {
      conditions.push({
        or: statuses.map((s) => ({
          property: "Status",
          status: { equals: s },
        })),
      });
    }
  }

  // Domain filter
  if (options.domain) {
    const domain = await findLifeDomainByName(options.domain);
    if (domain) {
      conditions.push({
        property: "🌐 Life Domains DB",
        relation: { contains: domain.id },
      });
    }
  }

  let filter: Record<string, unknown> | undefined;
  if (conditions.length === 1) {
    filter = conditions[0];
  } else if (conditions.length > 1) {
    filter = { and: conditions };
  }

  const response = await client.request<DatabaseQueryResponse>({
    path: `databases/${config.epicsDbId}/query`,
    method: "post",
    body: {
      filter,
      page_size: options.limit || 100,
      sorts: [{ timestamp: "last_edited_time", direction: "descending" }],
    },
  });

  return response.results.map(pageToEpic);
}

/**
 * Find an Epic by name or ID
 */
export async function findEpicByName(
  nameOrId: string,
): Promise<NotionEpic | null> {
  const { client, config } = await getNotionClient();

  // Try by ID first
  const normalizedId = nameOrId.replace(/-/g, "").toLowerCase();
  if (normalizedId.length >= 8 && /^[a-f0-9]+$/.test(normalizedId)) {
    const epics = await queryEpics({ limit: 100 });
    const match = epics.find((e) =>
      e.id
        .replace(/-/g, "")
        .toLowerCase()
        .startsWith(normalizedId.substring(0, 8)),
    );
    if (match) return match;
  }

  // Search by name
  const epics = await queryEpics({ limit: 100 });
  const lowerName = nameOrId.toLowerCase();
  return epics.find((e) => e.name.toLowerCase().includes(lowerName)) || null;
}

/**
 * Get a single Epic by ID with full details
 */
export async function getEpic(epicId: string): Promise<NotionEpic | null> {
  const { client } = await getNotionClient();

  const normalizedId = epicId.replace(/-/g, "").toLowerCase();

  // If full UUID, try direct retrieval
  if (normalizedId.length === 32 && /^[a-f0-9]+$/.test(normalizedId)) {
    try {
      const page = await client.pages.retrieve({ page_id: epicId });
      return pageToEpic(page as Record<string, unknown>);
    } catch {
      // Fall through
    }
  }

  // Otherwise search by short ID or name
  return findEpicByName(epicId);
}

/**
 * Update epic status in Notion
 */
export async function updateEpicStatus(
  epicId: string,
  status: EpicStatus,
): Promise<void> {
  const { client } = await getNotionClient();

  await client.pages.update({
    page_id: epicId,
    properties: {
      Status: { status: { name: status } },
    },
  });
}

// =============================================================================
// User Stories
// =============================================================================

/**
 * Get a User Story by ID
 */
export async function getUserStory(
  storyId: string,
): Promise<NotionUserStory | null> {
  const { client } = await getNotionClient();

  try {
    const page = await client.pages.retrieve({ page_id: storyId });
    return pageToUserStory(page as Record<string, unknown>);
  } catch {
    return null;
  }
}

/**
 * Transform Notion page to NotionUserStory
 */
function pageToUserStory(page: Record<string, unknown>): NotionUserStory {
  const props = (page as { properties: Record<string, unknown> }).properties;

  const titleProp = props.Name as { title?: Array<{ plain_text: string }> };
  const statusProp = props.Status as { status?: { name: string } };
  const priorityProp = props.Priority as { select?: { name: string } };
  const userTypeProp = props["User Type"] as { select?: { name: string } };
  const epicRelation = props["🌐 Global Projects & Epics DB "] as {
    relation?: Array<{ id: string }>;
  };
  const sprintRelation = props["🛠️  Global Sprints DB"] as {
    relation?: Array<{ id: string }>;
  };
  const taskRelation = props["🌐  Global Tasks DB"] as {
    relation?: Array<{ id: string }>;
  };

  const pageId = page.id as string;

  return {
    id: pageId,
    shortId: pageId.replace(/-/g, "").substring(0, 8),
    name: titleProp?.title?.[0]?.plain_text || "Untitled",
    status: statusProp?.status?.name || "Unknown",
    priority: priorityProp?.select?.name || null,
    userType: userTypeProp?.select?.name || null,
    url: page.url as string,
    epicId: epicRelation?.relation?.[0]?.id || null,
    sprintId: sprintRelation?.relation?.[0]?.id || null,
    taskIds: taskRelation?.relation?.map((r) => r.id) || [],
  };
}

// =============================================================================
// Life Domains
// =============================================================================

/**
 * Get a Life Domain by ID
 */
export async function getLifeDomain(
  domainId: string,
): Promise<NotionLifeDomain | null> {
  const { client } = await getNotionClient();

  try {
    const page = await client.pages.retrieve({ page_id: domainId });
    return pageToLifeDomain(page as Record<string, unknown>);
  } catch {
    return null;
  }
}

// =============================================================================
// Sprints
// =============================================================================

/**
 * Query Sprints from Notion
 */
export async function querySprints(
  options: {
    status?: SprintStatus | SprintStatus[];
    domain?: string;
    limit?: number;
  } = {},
): Promise<NotionSprint[]> {
  const { client, config } = await getNotionClient();

  const conditions: Array<Record<string, unknown>> = [];

  // Status filter
  if (options.status) {
    const statuses = Array.isArray(options.status)
      ? options.status
      : [options.status];
    if (statuses.length === 1) {
      conditions.push({ property: "Status", status: { equals: statuses[0] } });
    } else {
      conditions.push({
        or: statuses.map((s) => ({
          property: "Status",
          status: { equals: s },
        })),
      });
    }
  }

  // Domain filter
  if (options.domain) {
    const domain = await findLifeDomainByName(options.domain);
    if (domain) {
      conditions.push({
        property:
          "🌐  Life Buckets DB  (ID: b1e7c26b-7b52-4f60-9885-d73bcf1b76df) ",
        relation: { contains: domain.id },
      });
    }
  }

  let filter: Record<string, unknown> | undefined;
  if (conditions.length === 1) {
    filter = conditions[0];
  } else if (conditions.length > 1) {
    filter = { and: conditions };
  }

  const response = await client.request<DatabaseQueryResponse>({
    path: `databases/${config.sprintsDbId}/query`,
    method: "post",
    body: {
      filter,
      page_size: options.limit || 100,
      sorts: [{ property: "Sprint Dates", direction: "descending" }],
    },
  });

  return response.results.map(pageToSprint);
}

/**
 * Find a Sprint by name or ID
 */
export async function findSprintByName(
  nameOrId: string,
): Promise<NotionSprint | null> {
  const normalizedId = nameOrId.replace(/-/g, "").toLowerCase();

  // Try by short ID first
  if (normalizedId.length >= 8 && /^[a-f0-9]+$/.test(normalizedId)) {
    const sprints = await querySprints({ limit: 100 });
    const match = sprints.find((s) =>
      s.id
        .replace(/-/g, "")
        .toLowerCase()
        .startsWith(normalizedId.substring(0, 8)),
    );
    if (match) return match;
  }

  // Search by name
  const sprints = await querySprints({ limit: 100 });
  const lowerName = nameOrId.toLowerCase();
  return sprints.find((s) => s.name.toLowerCase().includes(lowerName)) || null;
}

/**
 * Find a User Story by name or ID
 */
export async function findUserStoryByName(
  nameOrId: string,
): Promise<{ id: string; name: string } | null> {
  const { client, config } = await getNotionClient();
  const normalizedId = nameOrId.replace(/-/g, "").toLowerCase();

  // If it looks like a full or partial UUID, try direct retrieval
  if (normalizedId.length >= 8 && /^[a-f0-9]+$/.test(normalizedId)) {
    // Query user stories database to find by ID
    const response = await client.request<DatabaseQueryResponse>({
      path: `databases/${config.userStoriesDbId}/query`,
      method: "post",
      body: { page_size: 100 },
    });

    const match = response.results.find((page) =>
      (page.id as string)
        .replace(/-/g, "")
        .toLowerCase()
        .startsWith(normalizedId.substring(0, 8)),
    );
    if (match) {
      const props = match.properties as Record<string, unknown>;
      const titleProp = props.Name as { title?: Array<{ plain_text: string }> };
      return {
        id: match.id as string,
        name: titleProp?.title?.[0]?.plain_text || "Untitled",
      };
    }
  }

  // Search by name
  const response = await client.request<DatabaseQueryResponse>({
    path: `databases/${config.userStoriesDbId}/query`,
    method: "post",
    body: { page_size: 100 },
  });

  const lowerName = nameOrId.toLowerCase();
  for (const page of response.results) {
    const props = page.properties as Record<string, unknown>;
    const titleProp = props.Name as { title?: Array<{ plain_text: string }> };
    const name = titleProp?.title?.[0]?.plain_text || "";
    if (name.toLowerCase().includes(lowerName)) {
      return { id: page.id as string, name };
    }
  }

  return null;
}

/**
 * Get the current active sprint
 */
export async function getCurrentSprint(): Promise<NotionSprint | null> {
  const sprints = await querySprints({ status: "In Progress", limit: 1 });
  return sprints[0] || null;
}

/**
 * Get a single Sprint by ID with full details
 */
export async function getSprint(
  sprintId: string,
): Promise<NotionSprint | null> {
  const { client } = await getNotionClient();

  const normalizedId = sprintId.replace(/-/g, "").toLowerCase();

  // If full UUID, try direct retrieval
  if (normalizedId.length === 32 && /^[a-f0-9]+$/.test(normalizedId)) {
    try {
      const page = await client.pages.retrieve({ page_id: sprintId });
      return pageToSprint(page as Record<string, unknown>);
    } catch {
      // Fall through
    }
  }

  // Otherwise search
  return findSprintByName(sprintId);
}

/**
 * Update sprint status in Notion
 */
export async function updateSprintStatus(
  sprintId: string,
  status: SprintStatus,
): Promise<void> {
  const { client } = await getNotionClient();

  await client.pages.update({
    page_id: sprintId,
    properties: {
      Status: { status: { name: status } },
    },
  });
}

// =============================================================================
// Page Transformation Helpers
// =============================================================================

/**
 * Convert task title to branch-safe kebab-case
 */
export function titleToKebab(title: string): string {
  // Remove common prefixes
  const prefixes = /^(add|fix|update|implement|create|remove|refactor)\s+/i;
  let clean = title.replace(prefixes, "");

  // Take first 5 words or 50 chars
  const words = clean.split(/\s+/).slice(0, 5);
  clean = words.join(" ");
  if (clean.length > 50) {
    clean = clean.substring(0, 50);
  }

  // Convert to kebab-case
  return clean
    .toLowerCase()
    .replace(/[^a-z0-9\s-]/g, "")
    .replace(/\s+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
}

/**
 * Infer task type from title
 */
export function inferTaskType(title: string): TaskType {
  const lowerTitle = title.toLowerCase();
  if (/^(fix|bug|issue|patch|resolve)/.test(lowerTitle)) {
    return "fix";
  }
  if (/^(hotfix|urgent|critical|emergency)/.test(lowerTitle)) {
    return "hotfix";
  }
  return "feature";
}

/**
 * Generate branch name from task (legacy - for hotfixes)
 */
export function generateBranchName(task: NotionTask): string {
  const type = inferTaskType(task.title);
  const description = titleToKebab(task.title);
  return `${type}/${task.shortId}-${description}`;
}

/**
 * Generate epic branch name
 * Format: epic/{epicId}-{kebab-name}
 * Example: epic/f87f6649-financial-backbone
 */
export function generateEpicBranchName(epic: NotionEpic): string {
  const description = titleToKebab(epic.name);
  return `epic/${epic.shortId}-${description}`;
}

/**
 * Generate sprint branch name
 * Format: sprint/{epicId}-{year}-w{week}
 * Example: sprint/f87f6649-2025-w02
 */
export function generateSprintBranchName(
  sprint: NotionSprint,
  epic?: NotionEpic | null,
): string {
  // Extract year and week from sprint dates or name
  let yearWeek = "";

  if (sprint.startDate) {
    const date = new Date(sprint.startDate);
    const year = date.getFullYear();
    const week = getWeekNumber(date);
    yearWeek = `${year}-w${week.toString().padStart(2, "0")}`;
  } else {
    // Try to extract from sprint name (e.g., "Sprint 2025-W02" or "Week 2")
    const weekMatch = sprint.name.match(/w(?:eek)?\s*(\d+)/i);
    const yearMatch = sprint.name.match(/(\d{4})/);
    const week = weekMatch ? weekMatch[1].padStart(2, "0") : "01";
    const year = yearMatch ? yearMatch[1] : new Date().getFullYear().toString();
    yearWeek = `${year}-w${week}`;
  }

  // Include epic ID if available, otherwise use sprint ID
  const prefix = epic ? epic.shortId : sprint.shortId;
  return `sprint/${prefix}-${yearWeek}`;
}

/**
 * Get ISO week number from date
 */
function getWeekNumber(date: Date): number {
  const d = new Date(
    Date.UTC(date.getFullYear(), date.getMonth(), date.getDate()),
  );
  const dayNum = d.getUTCDay() || 7;
  d.setUTCDate(d.getUTCDate() + 4 - dayNum);
  const yearStart = new Date(Date.UTC(d.getUTCFullYear(), 0, 1));
  return Math.ceil(((d.getTime() - yearStart.getTime()) / 86400000 + 1) / 7);
}

/**
 * Generate commit message for a task
 * Format: {type}({scope}): {description}\n\nTask: {taskId}\nStory: {userStoryId}
 */
export function generateCommitMessage(
  task: NotionTask,
  options?: { userStoryId?: string; scope?: string },
): string {
  const type = inferTaskType(task.title);
  const scope = options?.scope || "task";
  const description = task.title
    .toLowerCase()
    .replace(/^(add|fix|update|implement|create|remove|refactor)\s+/i, "");

  let message = `${type}(${scope}): ${description}\n\nTask: ${task.shortId}`;

  if (options?.userStoryId) {
    message += `\nStory: ${options.userStoryId}`;
  }

  if (task.description) {
    message += `\n\n${task.description}`;
  }

  return message;
}

/**
 * Transform Notion page response to NotionTask
 * Extracts ALL properties from the Tasks database schema
 */
function pageToTask(page: Record<string, unknown>): NotionTask {
  const props = (page as { properties: Record<string, unknown> }).properties;

  // Extract title - check common property names (Action Item is used by Global Tasks DB)
  const titleProp =
    (props["Action Item"] as { title?: Array<{ plain_text: string }> }) ||
    (props.Name as { title?: Array<{ plain_text: string }> }) ||
    (props.Title as { title?: Array<{ plain_text: string }> }) ||
    (props.Task as { title?: Array<{ plain_text: string }> });
  const title = titleProp?.title?.[0]?.plain_text || "Untitled";

  // Extract status (preserve exact Notion status name)
  const statusProp = props.Status as { status?: { name: string } };
  const status = (statusProp?.status?.name || "On Hold") as NotionTaskStatus;

  // Extract description (rich text)
  const descProp = (props.Description || props.Summary) as {
    rich_text?: Array<{ plain_text: string }>;
  };
  const description =
    descProp?.rich_text?.map((t) => t.plain_text).join("") || null;

  // Extract acceptance criteria
  const acProp = (props["Acceptance Criteria"] || props.Criteria) as {
    rich_text?: Array<{ plain_text: string }>;
  };
  const acceptanceCriteria =
    acProp?.rich_text?.map((t) => t.plain_text).join("") || null;

  // Short ID (first 8 chars of page ID without dashes)
  const pageId = page.id as string;
  const shortId = pageId.replace(/-/g, "").substring(0, 8);

  // ==========================================================================
  // Relations (to other databases)
  // ==========================================================================
  const epicRelation = props["🌐 Global Projects & Epics DB "] as {
    relation?: Array<{ id: string }>;
  };
  const sprintRelation = props["🛠️  Global Sprints DB"] as {
    relation?: Array<{ id: string }>;
  };
  const domainRelation = props["🌐 Life Domains DB"] as {
    relation?: Array<{ id: string }>;
  };
  const userStoryRelation = props["👤 User Stories "] as {
    relation?: Array<{ id: string }>;
  };
  const documentRelation = props["Related Document"] as {
    relation?: Array<{ id: string }>;
  };

  // ==========================================================================
  // Self-relations (Parent/Sub tasks)
  // ==========================================================================
  const parentTaskRelation = props["Parent Task"] as {
    relation?: Array<{ id: string }>;
  };
  const subTaskRelation = props["Sub Task"] as {
    relation?: Array<{ id: string }>;
  };

  // ==========================================================================
  // Select fields
  // ==========================================================================
  const priorityProp = props.Priority as { select?: { name: string } };
  const agentStatusProp = props["Agent Status"] as {
    select?: { name: string };
  };
  const assignedAgentProp = props["Assigned Agent"] as {
    select?: { name: string };
  };
  const taskTypeProp = props["Task Type"] as { select?: { name: string } };

  // ==========================================================================
  // Date fields
  // ==========================================================================
  const dueDateProp = props["Due Date"] as {
    date?: { start: string; end: string | null };
  };
  const doDateProp = props["Do Date"] as {
    date?: { start: string; end: string | null };
  };

  // ==========================================================================
  // People field
  // ==========================================================================
  const assigneeProp = props.Assignee as {
    people?: Array<{ name: string; id: string }>;
  };

  // ==========================================================================
  // Rich text fields
  // ==========================================================================
  const noteProp = props.Note as { rich_text?: Array<{ plain_text: string }> };
  const aiSummaryProp = props["AI summary"] as {
    rich_text?: Array<{ plain_text: string }>;
  };

  // ==========================================================================
  // Checkbox fields
  // ==========================================================================
  const isTemplateProp = props["Is a template"] as { checkbox?: boolean };

  // ==========================================================================
  // Unique ID field
  // ==========================================================================
  const uniqueIdProp = props.ID as {
    unique_id?: { number: number; prefix: string | null };
  };

  return {
    id: pageId,
    shortId,
    title,
    status,
    description,
    acceptanceCriteria,
    url: page.url as string,
    taskType: inferTaskType(title),
    createdTime: page.created_time as string,
    lastEditedTime: page.last_edited_time as string,

    // Priority & Scheduling
    priority: priorityProp?.select?.name || null,
    dueDate: dueDateProp?.date?.start || null,
    doDate: doDateProp?.date?.start || null,

    // Agent Workflow
    agentStatus:
      (agentStatusProp?.select?.name as NotionTask["agentStatus"]) || null,
    assignedAgent:
      (assignedAgentProp?.select?.name as NotionTask["assignedAgent"]) || null,
    assignee: assigneeProp?.people?.[0]?.name || null,

    // Task Type from Notion
    taskTypeSelect:
      (taskTypeProp?.select?.name as NotionTask["taskTypeSelect"]) || null,

    // Notes & AI
    note: noteProp?.rich_text?.map((t) => t.plain_text).join("") || null,
    aiSummary:
      aiSummaryProp?.rich_text?.map((t) => t.plain_text).join("") || null,

    // Task Hierarchy
    parentTaskId: parentTaskRelation?.relation?.[0]?.id || null,
    subTaskIds: subTaskRelation?.relation?.map((r) => r.id) || [],

    // Relations to other databases
    epicId: epicRelation?.relation?.[0]?.id || null,
    sprintId: sprintRelation?.relation?.[0]?.id || null,
    lifeDomainId: domainRelation?.relation?.[0]?.id || null,
    userStoryIds: userStoryRelation?.relation?.map((r) => r.id) || [],
    userStoryId: userStoryRelation?.relation?.[0]?.id || null, // Deprecated, backwards compat
    documentIds: documentRelation?.relation?.map((r) => r.id) || [],

    // Metadata
    isTemplate: isTemplateProp?.checkbox || false,
    uniqueId: uniqueIdProp?.unique_id
      ? `${uniqueIdProp.unique_id.prefix || ""}${uniqueIdProp.unique_id.number}`
      : null,
  };
}

/**
 * Transform Notion page to NotionLifeDomain
 */
function pageToLifeDomain(page: Record<string, unknown>): NotionLifeDomain {
  const props = (page as { properties: Record<string, unknown> }).properties;

  const titleProp = props["Pillar Name"] as {
    title?: Array<{ plain_text: string }>;
  };
  const typeProp = props.Type as { select?: { name: string } };
  const statusProp = props.Status as { select?: { name: string } };

  const pageId = page.id as string;

  return {
    id: pageId,
    shortId: pageId.replace(/-/g, "").substring(0, 8),
    name: titleProp?.title?.[0]?.plain_text || "Untitled",
    type: typeProp?.select?.name || null,
    status: statusProp?.select?.name || null,
    url: page.url as string,
  };
}

/**
 * Transform Notion page to NotionEpic
 */
function pageToEpic(page: Record<string, unknown>): NotionEpic {
  const props = (page as { properties: Record<string, unknown> }).properties;

  const titleProp = props.Name as { title?: Array<{ plain_text: string }> };
  const statusProp = props.Status as { status?: { name: string } };
  const subtitleProp = props["Subtitle "] as {
    rich_text?: Array<{ plain_text: string }>;
  };
  const logLineProp = props["Log Line"] as {
    rich_text?: Array<{ plain_text: string }>;
  };
  const priorityProp = props.Priority as { select?: { name: string } };
  const projectTypeProp = props["Type of Project"] as {
    select?: { name: string };
  };
  const timelineProp = props["Timeline Dates"] as {
    date?: { start: string; end: string | null };
  };
  const storyPointsProp = props["Total Story Points"] as {
    rollup?: { number: number };
  };
  const githubProp = props["github repo link"] as { url?: string };
  const domainRelation = props["🌐 Life Domains DB"] as {
    relation?: Array<{ id: string }>;
  };
  const sprintRelation = props["🛠️  Global Sprints DB"] as {
    relation?: Array<{ id: string }>;
  };
  const storyRelation = props["👤 User Stories "] as {
    relation?: Array<{ id: string }>;
  };
  const taskRelation = props["🌐  Global Tasks DB"] as {
    relation?: Array<{ id: string }>;
  };
  const docRelation = props["🌐  Global Documents DB"] as {
    relation?: Array<{ id: string }>;
  };

  const pageId = page.id as string;

  return {
    id: pageId,
    shortId: pageId.replace(/-/g, "").substring(0, 8),
    name: titleProp?.title?.[0]?.plain_text || "Untitled",
    status: (statusProp?.status?.name || "Not Started") as EpicStatus,
    subtitle: subtitleProp?.rich_text?.[0]?.plain_text || null,
    logLine: logLineProp?.rich_text?.[0]?.plain_text || null,
    priority: priorityProp?.select?.name || null,
    projectType: projectTypeProp?.select?.name || null,
    startDate: timelineProp?.date?.start || null,
    endDate: timelineProp?.date?.end || null,
    totalStoryPoints: storyPointsProp?.rollup?.number || null,
    url: page.url as string,
    githubRepoLink: githubProp?.url || null,
    lifeDomainId: domainRelation?.relation?.[0]?.id || null,
    sprintIds: sprintRelation?.relation?.map((r) => r.id) || [],
    userStoryIds: storyRelation?.relation?.map((r) => r.id) || [],
    taskIds: taskRelation?.relation?.map((r) => r.id) || [],
    documentIds: docRelation?.relation?.map((r) => r.id) || [],
  };
}

/**
 * Transform Notion page to NotionSprint
 */
function pageToSprint(page: Record<string, unknown>): NotionSprint {
  const props = (page as { properties: Record<string, unknown> }).properties;

  const titleProp = props.Name as { title?: Array<{ plain_text: string }> };
  const statusProp = props.Status as { status?: { name: string } };
  const objectivesProp = props["📋 Sprint Objectives"] as {
    rich_text?: Array<{ plain_text: string }>;
  };
  const datesProp = props["Sprint Dates"] as {
    date?: { start: string; end: string | null };
  };
  const scoreProp = props["📊 Sprint Quality Score"] as { number?: number };
  const domainRelation = props[
    "🌐  Life Buckets DB  (ID: b1e7c26b-7b52-4f60-9885-d73bcf1b76df) "
  ] as { relation?: Array<{ id: string }> };
  const epicRelation = props["🌐 Global Projects & Epics DB "] as {
    relation?: Array<{ id: string }>;
  };
  const taskRelation = props["🌐  Global Tasks DB"] as {
    relation?: Array<{ id: string }>;
  };
  const storyRelation = props["👤 User Stories "] as {
    relation?: Array<{ id: string }>;
  };

  const pageId = page.id as string;

  return {
    id: pageId,
    shortId: pageId.replace(/-/g, "").substring(0, 8),
    name: titleProp?.title?.[0]?.plain_text || "Untitled",
    status: (statusProp?.status?.name || "Not Started") as SprintStatus,
    objectives:
      objectivesProp?.rich_text?.map((t) => t.plain_text).join("") || null,
    startDate: datesProp?.date?.start || null,
    endDate: datesProp?.date?.end || null,
    qualityScore: scoreProp?.number || null,
    url: page.url as string,
    epicIds: epicRelation?.relation?.map((r) => r.id) || [],
    taskIds: taskRelation?.relation?.map((r) => r.id) || [],
    userStoryIds: storyRelation?.relation?.map((r) => r.id) || [],
    lifeDomainId: domainRelation?.relation?.[0]?.id || null,
  };
}

// =============================================================================
// Documents
// =============================================================================

/**
 * Query Documents from Notion
 */
export async function queryDocuments(
  options: DocumentListOptions = {},
): Promise<NotionDocument[]> {
  const { client, config } = await getNotionClient();

  const conditions: Array<Record<string, unknown>> = [];

  // Status filter
  if (options.status) {
    conditions.push({
      property: "Document Status",
      select: { equals: options.status },
    });
  }

  // Content type filter
  if (options.contentType) {
    conditions.push({
      property: "Content Type",
      select: { equals: options.contentType },
    });
  }

  // Agent tags filter (multi-select)
  if (options.tags && options.tags.length > 0) {
    for (const tag of options.tags) {
      conditions.push({
        property: "Agent Tags",
        multi_select: { contains: tag },
      });
    }
  }

  let filter: Record<string, unknown> | undefined;
  if (conditions.length === 1) {
    filter = conditions[0];
  } else if (conditions.length > 1) {
    filter = { and: conditions };
  }

  const response = await client.request<DatabaseQueryResponse>({
    path: `databases/${config.documentsDbId}/query`,
    method: "post",
    body: {
      filter,
      page_size: options.limit || 100,
      sorts: [{ timestamp: "last_edited_time", direction: "descending" }],
    },
  });

  return response.results.map(pageToDocument);
}

/**
 * Get a single Document by ID
 */
export async function getDocument(
  docId: string,
): Promise<NotionDocument | null> {
  const { client } = await getNotionClient();

  const normalizedId = docId.replace(/-/g, "").toLowerCase();

  // If full UUID, try direct retrieval
  if (normalizedId.length === 32 && /^[a-f0-9]+$/.test(normalizedId)) {
    try {
      const page = await client.pages.retrieve({ page_id: docId });
      return pageToDocument(page as Record<string, unknown>);
    } catch {
      // Fall through to search
    }
  }

  // Search by short ID
  const docs = await queryDocuments({ limit: 100 });
  const match = docs.find((d) =>
    d.id
      .replace(/-/g, "")
      .toLowerCase()
      .startsWith(normalizedId.substring(0, 8)),
  );
  return match || null;
}

/**
 * Get document content as markdown by reading all blocks
 */
export async function getDocumentContent(docId: string): Promise<string> {
  const { client } = await getNotionClient();

  // Recursively get all blocks
  const blocks = await getAllBlocks(client, docId);

  // Convert blocks to markdown
  return blocksToMarkdown(blocks);
}

/**
 * Get all blocks from a page (handles pagination)
 */
async function getAllBlocks(
  client: Client,
  blockId: string,
): Promise<Array<Record<string, unknown>>> {
  const allBlocks: Array<Record<string, unknown>> = [];
  let hasMore = true;
  let startCursor: string | undefined;

  while (hasMore) {
    const response = await client.blocks.children.list({
      block_id: blockId,
      page_size: 100,
      start_cursor: startCursor,
    });

    for (const block of response.results) {
      allBlocks.push(block as Record<string, unknown>);

      // Recursively get children if block has them
      const blockData = block as { has_children?: boolean; id: string };
      if (blockData.has_children) {
        const children = await getAllBlocks(client, blockData.id);
        (block as Record<string, unknown>).children = children;
      }
    }

    hasMore = response.has_more;
    startCursor = response.next_cursor || undefined;
  }

  return allBlocks;
}

/**
 * Convert Notion blocks to markdown
 */
function blocksToMarkdown(
  blocks: Array<Record<string, unknown>>,
  indent = 0,
): string {
  const lines: string[] = [];
  const prefix = "  ".repeat(indent);

  for (const block of blocks) {
    const type = block.type as string;
    const content = block[type] as Record<string, unknown> | undefined;

    if (!content) continue;

    // Extract rich text content
    const richText = (content.rich_text || content.text) as
      | Array<{ plain_text: string }>
      | undefined;
    const text = richText?.map((t) => t.plain_text).join("") || "";

    switch (type) {
      case "heading_1":
        lines.push(`${prefix}# ${text}`);
        break;
      case "heading_2":
        lines.push(`${prefix}## ${text}`);
        break;
      case "heading_3":
        lines.push(`${prefix}### ${text}`);
        break;
      case "paragraph":
        if (text) lines.push(`${prefix}${text}`);
        break;
      case "bulleted_list_item":
        lines.push(`${prefix}- ${text}`);
        break;
      case "numbered_list_item":
        lines.push(`${prefix}1. ${text}`);
        break;
      case "code":
        const lang = (content.language as string) || "";
        lines.push(`${prefix}\`\`\`${lang}`);
        lines.push(`${prefix}${text}`);
        lines.push(`${prefix}\`\`\``);
        break;
      case "quote":
        lines.push(`${prefix}> ${text}`);
        break;
      case "callout":
        const icon = (content.icon as { emoji?: string })?.emoji || "💡";
        lines.push(`${prefix}${icon} ${text}`);
        break;
      case "divider":
        lines.push(`${prefix}---`);
        break;
      case "toggle":
        lines.push(`${prefix}<details>`);
        lines.push(`${prefix}<summary>${text}</summary>`);
        break;
      case "to_do":
        const checked = (content.checked as boolean) ? "x" : " ";
        lines.push(`${prefix}- [${checked}] ${text}`);
        break;
    }

    // Process children
    const children = block.children as
      | Array<Record<string, unknown>>
      | undefined;
    if (children && children.length > 0) {
      lines.push(blocksToMarkdown(children, indent + 1));
    }

    if (type === "toggle") {
      lines.push(`${prefix}</details>`);
    }
  }

  return lines.join("\n");
}

/**
 * Get documents linked to a task
 */
export async function getTaskDocuments(
  taskId: string,
): Promise<NotionDocument[]> {
  const task = await getTask(taskId);
  if (!task || !task.documentIds || task.documentIds.length === 0) {
    return [];
  }

  const docs: NotionDocument[] = [];
  for (const docId of task.documentIds) {
    const doc = await getDocument(docId);
    if (doc) {
      docs.push(doc);
    }
  }

  return docs;
}

/**
 * Get full task context with all related data
 */
export async function getTaskContext(
  taskId: string,
): Promise<TaskContext | null> {
  const task = await getTask(taskId);
  if (!task) return null;

  const layers: string[] = ["task"];
  let epic: NotionEpic | null = null;
  let sprint: NotionSprint | null = null;
  const documents: NotionDocument[] = [];
  let parentTask: NotionTask | null = null;
  const subtasks: NotionTask[] = [];
  const userStories: NotionUserStory[] = [];
  let lifeDomain: NotionLifeDomain | null = null;

  // Load epic if linked
  if (task.epicId) {
    epic = await getEpic(task.epicId);
    if (epic) layers.push("epic");
  }

  // Load sprint if linked
  if (task.sprintId) {
    sprint = await getSprint(task.sprintId);
    if (sprint) layers.push("sprint");
  }

  // Load parent task if linked
  if (task.parentTaskId) {
    parentTask = await getTask(task.parentTaskId);
    if (parentTask) layers.push("parentTask");
  }

  // Load subtasks if any
  if (task.subTaskIds && task.subTaskIds.length > 0) {
    for (const subId of task.subTaskIds) {
      const sub = await getTask(subId);
      if (sub) subtasks.push(sub);
    }
    if (subtasks.length > 0) layers.push("subtasks");
  }

  // Load user stories if linked
  if (task.userStoryIds && task.userStoryIds.length > 0) {
    for (const storyId of task.userStoryIds) {
      const story = await getUserStory(storyId);
      if (story) userStories.push(story);
    }
    if (userStories.length > 0) layers.push("userStories");
  }

  // Load life domain if linked
  if (task.lifeDomainId) {
    lifeDomain = await getLifeDomain(task.lifeDomainId);
    if (lifeDomain) layers.push("lifeDomain");
  }

  // Load linked documents with content
  if (task.documentIds && task.documentIds.length > 0) {
    for (const docId of task.documentIds) {
      const doc = await getDocument(docId);
      if (doc) {
        // Load document content
        doc.content = await getDocumentContent(docId);
        documents.push(doc);
      }
    }
    if (documents.length > 0) layers.push("documents");
  }

  return {
    task,
    epic,
    sprint,
    documents,
    parentTask,
    subtasks,
    userStories,
    lifeDomain,
    layers,
  };
}

/**
 * Transform Notion page to NotionDocument
 */
function pageToDocument(page: Record<string, unknown>): NotionDocument {
  const props = (page as { properties: Record<string, unknown> }).properties;

  const titleProp = props.Name as { title?: Array<{ plain_text: string }> };
  const contentTypeProp = props["Content Type"] as {
    select?: { name: string };
  };
  const versionProp = props.Version as {
    rich_text?: Array<{ plain_text: string }>;
  };
  const statusProp = props["Document Status"] as { select?: { name: string } };
  const agentTagsProp = props["Agent Tags"] as {
    multi_select?: Array<{ name: string }>;
  };
  const taskRelation = props["🌐  Global Tasks DB"] as {
    relation?: Array<{ id: string }>;
  };
  const epicRelation = props["🌐 Global Projects & Epics DB "] as {
    relation?: Array<{ id: string }>;
  };

  const pageId = page.id as string;

  return {
    id: pageId,
    shortId: pageId.replace(/-/g, "").substring(0, 8),
    name: titleProp?.title?.[0]?.plain_text || "Untitled",
    contentType: contentTypeProp?.select?.name || null,
    version: versionProp?.rich_text?.[0]?.plain_text || null,
    status: statusProp?.select?.name || null,
    agentTags: agentTagsProp?.multi_select?.map((t) => t.name) || [],
    url: page.url as string,
    taskIds: taskRelation?.relation?.map((r) => r.id) || [],
    epicIds: epicRelation?.relation?.map((r) => r.id) || [],
  };
}
