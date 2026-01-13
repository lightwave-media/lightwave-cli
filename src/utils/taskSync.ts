/**
 * Push task updates to backend for WebSocket broadcast.
 *
 * After CLI updates a task in Notion, it pushes the update to the
 * backend API, which broadcasts via WebSocket to connected dashboards.
 */

import { SSMClient, GetParameterCommand } from "@aws-sdk/client-ssm";

export interface TaskSyncPayload {
  id: string;
  short_id: string;
  title: string;
  status: string;
  task_type?: string;
  branch?: string;
  url: string;
}

const BACKEND_URL =
  process.env.LW_BACKEND_URL || "https://api.lightwave-media.ltd";

// Cache agent key to avoid repeated SSM lookups
let cachedAgentKey: string | null = null;

/**
 * Push task update to backend for WebSocket broadcast.
 *
 * Silently fails if backend is unavailable or credentials missing.
 * This ensures CLI commands don't break if sync is misconfigured.
 */
export async function pushTaskUpdate(task: TaskSyncPayload): Promise<boolean> {
  const agentKey = await getAgentKey();

  if (!agentKey) {
    // Silent skip - WebSocket sync is optional
    return false;
  }

  try {
    const response = await fetch(`${BACKEND_URL}/api/tasks/sync/`, {
      method: "post",
      headers: {
        "Content-Type": "application/json",
        "X-Agent-Key": agentKey,
      },
      body: JSON.stringify(task),
    });

    if (!response.ok) {
      // Log but don't fail
      console.debug(`Task sync response: ${response.status}`);
      return false;
    }

    return true;
  } catch (err) {
    // Silent fail - don't break CLI flow if backend is unavailable
    console.debug(`Task sync error: ${(err as Error).message}`);
    return false;
  }
}

/**
 * Get agent API key from env or AWS Parameter Store.
 */
async function getAgentKey(): Promise<string | null> {
  // Return cached key if available
  if (cachedAgentKey) {
    return cachedAgentKey;
  }

  // Check env var first (for local dev)
  if (process.env.LW_AGENT_KEY) {
    cachedAgentKey = process.env.LW_AGENT_KEY;
    return cachedAgentKey;
  }

  // Fall back to AWS Parameter Store
  try {
    const ssm = new SSMClient({ region: "us-east-1" });
    const result = await ssm.send(
      new GetParameterCommand({
        Name: "/lightwave/prod/CLI_AGENT_KEY",
        WithDecryption: true,
      }),
    );
    cachedAgentKey = result.Parameter?.Value || null;
    return cachedAgentKey;
  } catch {
    // Parameter doesn't exist yet - that's ok
    return null;
  }
}
