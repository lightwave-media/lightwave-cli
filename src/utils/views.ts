/**
 * CLI Views utilities - Query named views stored in Notion
 *
 * Views are filter configurations stored in a Notion database,
 * allowing users to define complex filters in Notion UI and
 * use them from the CLI via --view flag.
 */

import { getNotionClient } from "./notion.js";
import { NOTION_DB_IDS, type CLIView } from "../types/notion.js";

/**
 * Response type for database query
 */
interface DatabaseQueryResponse {
  results: Array<Record<string, unknown>>;
  has_more: boolean;
  next_cursor: string | null;
}

/**
 * Transform Notion page to CLIView
 */
function pageToView(page: Record<string, unknown>): CLIView {
  const props = page.properties as Record<string, unknown>;

  // Extract title
  const nameProp = props.Name as { title?: Array<{ plain_text: string }> };
  const name = nameProp?.title?.[0]?.plain_text || "Untitled View";

  // Extract database select
  const databaseProp = props.Database as { select?: { name: string } };
  const database = (databaseProp?.select?.name || "Tasks") as CLIView["database"];

  // Extract filter JSON from rich_text
  const filterProp = props["Filter JSON"] as {
    rich_text?: Array<{ plain_text: string }>;
  };
  const filterJson = filterProp?.rich_text?.[0]?.plain_text || "{}";
  let parsedFilter: Record<string, unknown> = {};
  try {
    parsedFilter = JSON.parse(filterJson);
  } catch {
    console.warn(`Invalid filter JSON in view "${name}"`);
  }

  // Extract description
  const descProp = props.Description as {
    rich_text?: Array<{ plain_text: string }>;
  };
  const description = descProp?.rich_text?.[0]?.plain_text || null;

  // Extract active checkbox
  const activeProp = props.Active as { checkbox?: boolean };
  const active = activeProp?.checkbox ?? true;

  return {
    id: page.id as string,
    shortId: (page.id as string).substring(0, 8),
    name,
    database,
    filterJson: parsedFilter,
    description,
    active,
    url: page.url as string,
  };
}

/**
 * Query all CLI Views from Notion
 */
export async function queryViews(options: {
  database?: string;
  activeOnly?: boolean;
  limit?: number;
} = {}): Promise<CLIView[]> {
  const { client } = await getNotionClient();

  // Check if CLI Views database ID is configured
  if (
    NOTION_DB_IDS.cliViews === "PLACEHOLDER_CLI_VIEWS_DB_ID" ||
    !NOTION_DB_IDS.cliViews
  ) {
    throw new Error(
      "CLI Views database not configured. " +
        "Create the database in Notion and update NOTION_DB_IDS.cliViews in types/notion.ts. " +
        "See scripts/create-views-db.ts for setup instructions."
    );
  }

  // Build filter
  const filterConditions: Array<Record<string, unknown>> = [];

  if (options.database) {
    filterConditions.push({
      property: "Database",
      select: { equals: options.database },
    });
  }

  if (options.activeOnly !== false) {
    filterConditions.push({
      property: "Active",
      checkbox: { equals: true },
    });
  }

  const filter =
    filterConditions.length > 0
      ? { and: filterConditions }
      : undefined;

  const response = (await client.request({
    path: `databases/${NOTION_DB_IDS.cliViews}/query`,
    method: "POST",
    body: {
      filter,
      page_size: options.limit || 50,
      sorts: [{ property: "Name", direction: "ascending" }],
    },
  })) as DatabaseQueryResponse;

  return response.results.map(pageToView);
}

/**
 * Get a specific view by name
 */
export async function getViewByName(name: string): Promise<CLIView | null> {
  const { client } = await getNotionClient();

  if (
    NOTION_DB_IDS.cliViews === "PLACEHOLDER_CLI_VIEWS_DB_ID" ||
    !NOTION_DB_IDS.cliViews
  ) {
    throw new Error(
      "CLI Views database not configured. " +
        "Create the database in Notion and update NOTION_DB_IDS.cliViews in types/notion.ts."
    );
  }

  const response = (await client.request({
    path: `databases/${NOTION_DB_IDS.cliViews}/query`,
    method: "POST",
    body: {
      filter: {
        and: [
          { property: "Name", title: { equals: name } },
          { property: "Active", checkbox: { equals: true } },
        ],
      },
      page_size: 1,
    },
  })) as DatabaseQueryResponse;

  if (response.results.length === 0) {
    return null;
  }

  return pageToView(response.results[0]);
}

/**
 * Get filter object from a named view
 * Returns the Notion filter object that can be passed to database queries
 */
export async function getViewFilter(
  viewName: string
): Promise<Record<string, unknown> | null> {
  const view = await getViewByName(viewName);
  if (!view) {
    return null;
  }
  return view.filterJson;
}

/**
 * List available views for a database
 */
export async function listViewsForDatabase(
  database: "Tasks" | "Epics" | "Sprints" | "Documents"
): Promise<CLIView[]> {
  return queryViews({ database, activeOnly: true });
}
