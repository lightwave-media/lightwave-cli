/**
 * Create CLI Views database in Notion
 * Run with: npx tsx scripts/create-views-db.ts
 */

import { Client } from "@notionhq/client";
import { SSMClient, GetParameterCommand } from "@aws-sdk/client-ssm";

// Parent page ID for the new database (Global Documents area)
const PARENT_PAGE_ID = "b05d9a8d7c674ba1830fbdc02edebc99"; // Documents DB parent

async function getApiKey(): Promise<string> {
  if (process.env.NOTION_API_KEY) {
    return process.env.NOTION_API_KEY;
  }

  const ssm = new SSMClient({ region: "us-east-1" });
  const result = await ssm.send(
    new GetParameterCommand({
      Name: "/lightwave/prod/NOTION_API_KEY",
      WithDecryption: true,
    })
  );

  if (!result.Parameter?.Value) {
    throw new Error("Missing Notion API key");
  }

  return result.Parameter.Value;
}

async function main() {
  const apiKey = await getApiKey();
  const client = new Client({
    auth: apiKey,
    notionVersion: "2022-06-28",
  });

  console.log("Creating CLI Views database...\n");

  // First, let's find a suitable parent page
  // Search for a page we can use as parent
  const searchResult = await client.search({
    query: "Global Knowledge",
    filter: { property: "object", value: "page" },
  });

  if (searchResult.results.length === 0) {
    console.error("Could not find a parent page. Please create the database manually.");
    console.log("\nManual creation instructions:");
    console.log("1. Create a new database called 'CLI Views'");
    console.log("2. Add properties:");
    console.log("   - Name (title)");
    console.log("   - Database (select): Tasks, Epics, Sprints, Documents");
    console.log("   - Filter JSON (rich_text)");
    console.log("   - Description (rich_text)");
    console.log("   - Active (checkbox)");
    return;
  }

  const parentPage = searchResult.results[0];
  console.log(`Found parent page: ${(parentPage as any).url}`);

  try {
    const db = await client.databases.create({
      parent: {
        type: "page_id",
        page_id: parentPage.id,
      },
      title: [
        {
          type: "text",
          text: { content: "CLI Views" },
        },
      ],
      properties: {
        Name: {
          title: {},
        },
        Database: {
          select: {
            options: [
              { name: "Tasks", color: "blue" },
              { name: "Epics", color: "green" },
              { name: "Sprints", color: "purple" },
              { name: "Documents", color: "orange" },
            ],
          },
        },
        "Filter JSON": {
          rich_text: {},
        },
        Description: {
          rich_text: {},
        },
        Active: {
          checkbox: {},
        },
      },
    });

    console.log("\n✅ Created CLI Views database!");
    console.log(`URL: ${db.url}`);
    console.log(`ID: ${db.id}`);

    // Create an initial view
    console.log("\nCreating initial 'Production Dev Only' view...");

    await client.pages.create({
      parent: { database_id: db.id },
      properties: {
        Name: {
          title: [{ text: { content: "Production Dev Only" } }],
        },
        Database: {
          select: { name: "Tasks" },
        },
        "Filter JSON": {
          rich_text: [
            {
              text: {
                content: JSON.stringify(
                  {
                    and: [
                      {
                        property: "🌐 Life Domains DB",
                        relation: { contains: "YOUR_PRODUCTION_DOMAIN_ID" },
                      },
                      {
                        property: "Task Type",
                        select: { equals: "Software Dev" },
                      },
                    ],
                  },
                  null,
                  2
                ),
              },
            },
          ],
        },
        Description: {
          rich_text: [{ text: { content: "Tasks in Production domain with Software Dev type" } }],
        },
        Active: {
          checkbox: true,
        },
      },
    });

    console.log("✅ Created initial view!");
    console.log("\nAdd the database ID to your types/notion.ts:");
    console.log(`  cliViews: "${db.id}",`);
  } catch (err) {
    console.error("Failed to create database:", (err as Error).message);
    console.log("\nYou may need to create the database manually in Notion.");
  }
}

main().catch(console.error);
