/**
 * Temporary script to inspect Notion database schemas
 * Run with: npx tsx scripts/inspect-schema.ts
 */

import { Client } from "@notionhq/client";
import { SSMClient, GetParameterCommand } from "@aws-sdk/client-ssm";

const DATABASES = {
  tasks: "b8701544-1206-407e-934e-07485fe2f639",
  epics: "fdb63184-3ec6-416c-b3a1-195fc2ef5d2c",
  sprints: "21539364-b3be-802a-832d-de8d9cefcd9a",
  lifeDomains: "b1e7c26b-7b52-4f60-9885-d73bcf1b76df",
  documents: "b05d9a8d-7c67-4ba1-830f-bdc02edebc99",
};

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
    notionVersion: "2022-06-28"  // Use older API that returns properties
  });

  const dbName = process.argv[2] || "tasks";
  const dbId = DATABASES[dbName as keyof typeof DATABASES];

  if (!dbId) {
    console.error(`Unknown database: ${dbName}`);
    console.log("Available:", Object.keys(DATABASES).join(", "));
    process.exit(1);
  }

  console.log(`\n=== ${dbName.toUpperCase()} Database Schema ===\n`);

  const db = await client.databases.retrieve({ database_id: dbId });

  if (!db.properties) {
    console.log("Raw response:", JSON.stringify(db, null, 2));
    process.exit(1);
  }

  // Sort properties by type then name
  const props = Object.entries(db.properties).sort((a, b) => {
    if (a[1].type !== b[1].type) return a[1].type.localeCompare(b[1].type);
    return a[0].localeCompare(b[0]);
  });

  for (const [name, prop] of props) {
    const type = prop.type;
    let extra = "";

    if (type === "select" && "select" in prop && prop.select?.options) {
      const opts = prop.select.options.map((o) => o.name).slice(0, 5);
      extra = ` [${opts.join(", ")}${prop.select.options.length > 5 ? "..." : ""}]`;
    } else if (type === "status" && "status" in prop && prop.status?.options) {
      const opts = prop.status.options.map((o) => o.name).slice(0, 5);
      extra = ` [${opts.join(", ")}${prop.status.options.length > 5 ? "..." : ""}]`;
    } else if (type === "multi_select" && "multi_select" in prop && prop.multi_select?.options) {
      const opts = prop.multi_select.options.map((o) => o.name).slice(0, 5);
      extra = ` [${opts.join(", ")}${prop.multi_select.options.length > 5 ? "..." : ""}]`;
    } else if (type === "relation" && "relation" in prop) {
      extra = ` → ${prop.relation.database_id?.slice(0, 8)}...`;
    }

    console.log(`${type.padEnd(15)} | ${name}${extra}`);
  }

  console.log(`\nTotal: ${props.length} properties`);
}

main().catch(console.error);
