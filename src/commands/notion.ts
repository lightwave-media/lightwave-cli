/**
 * Notion export/import commands for data emancipation
 *
 * Commands:
 *   lw notion export              Export all databases to JSON
 *   lw notion export --db <name>  Export specific database
 *   lw notion import              Import JSON exports into Django
 *   lw notion import --dry-run    Preview import without changes
 *   lw notion list                List available databases
 *   lw notion stats               Show database statistics
 */

import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import * as fs from "fs";
import * as path from "path";
import { execSync, spawn } from "child_process";
import { getNotionClient } from "../utils/notion.js";
import { NOTION_DB_IDS } from "../types/notion.js";

export const notionCommand = new Command("notion").description(
  "Notion data management - export databases for migration",
);

// Database configuration for export
interface DatabaseConfig {
  id: string;
  name: string;
  titleProperty: string;
  description: string;
}

const DATABASES: Record<string, DatabaseConfig> = {
  tasks: {
    id: NOTION_DB_IDS.tasks,
    name: "Global Tasks DB",
    titleProperty: "Action Item",
    description: "All tasks and action items",
  },
  epics: {
    id: NOTION_DB_IDS.epics,
    name: "Global Projects & Epics DB",
    titleProperty: "Name",
    description: "Projects and epics",
  },
  sprints: {
    id: NOTION_DB_IDS.sprints,
    name: "Global Sprints DB",
    titleProperty: "Name",
    description: "Sprint planning iterations",
  },
  userStories: {
    id: NOTION_DB_IDS.userStories,
    name: "Global User Stories",
    titleProperty: "Name",
    description: "User stories for features",
  },
  lifeDomains: {
    id: NOTION_DB_IDS.lifeDomains,
    name: "Life Domains DB",
    titleProperty: "Pillar Name",
    description: "Life domain categories",
  },
  documents: {
    id: NOTION_DB_IDS.documents,
    name: "Global Documents DB",
    titleProperty: "Name",
    description: "Knowledge documents and specs",
  },
  cliViews: {
    id: NOTION_DB_IDS.cliViews,
    name: "CLI Views DB",
    titleProperty: "Name",
    description: "CLI view configurations",
  },
};

// Additional databases discovered in Notion (cineOS, film projects, etc.)
const ADDITIONAL_DATABASES: Record<string, DatabaseConfig> = {
  // CineOS databases - to be discovered
};

interface DatabaseQueryResponse {
  results: Array<Record<string, unknown>>;
  has_more: boolean;
  next_cursor: string | null;
}

/**
 * Export a single database to JSON
 */
async function exportDatabase(
  dbKey: string,
  config: DatabaseConfig,
  outputDir: string,
  includeArchived: boolean,
): Promise<{ count: number; file: string }> {
  const { client } = await getNotionClient();

  const allResults: Array<Record<string, unknown>> = [];
  let hasMore = true;
  let startCursor: string | undefined;

  while (hasMore) {
    const response = await client.request<DatabaseQueryResponse>({
      path: `databases/${config.id}/query`,
      method: "post",
      body: {
        page_size: 100,
        start_cursor: startCursor,
        // Don't filter by status to get all records
      },
    });

    allResults.push(...response.results);
    hasMore = response.has_more;
    startCursor = response.next_cursor || undefined;
  }

  // Transform results to include all properties
  const exportData = allResults.map((page) => {
    const transformed: Record<string, unknown> = {
      _notion_id: page.id,
      _created_time: page.created_time,
      _last_edited_time: page.last_edited_time,
      _url: page.url,
      _archived: page.archived,
    };

    // Extract all properties
    const props = page.properties as Record<string, unknown>;
    for (const [key, value] of Object.entries(props)) {
      transformed[key] = extractPropertyValue(value);
    }

    return transformed;
  });

  // Filter out archived if not requested
  const finalData = includeArchived
    ? exportData
    : exportData.filter((item) => !item._archived);

  // Write to file
  const filename = `${dbKey}.json`;
  const filepath = path.join(outputDir, filename);
  fs.writeFileSync(filepath, JSON.stringify(finalData, null, 2));

  return { count: finalData.length, file: filepath };
}

/**
 * Extract value from Notion property
 */
function extractPropertyValue(prop: unknown): unknown {
  if (!prop || typeof prop !== "object") return null;

  const p = prop as Record<string, unknown>;
  const type = p.type as string;

  switch (type) {
    case "title":
    case "rich_text": {
      const arr = p[type] as Array<{ plain_text: string }> | undefined;
      return arr?.map((t) => t.plain_text).join("") || null;
    }
    case "number":
      return p.number ?? null;
    case "select": {
      const sel = p.select as { name: string } | null;
      return sel?.name || null;
    }
    case "multi_select": {
      const multi = p.multi_select as Array<{ name: string }> | undefined;
      return multi?.map((s) => s.name) || [];
    }
    case "status": {
      const status = p.status as { name: string } | null;
      return status?.name || null;
    }
    case "date": {
      const date = p.date as { start: string; end: string | null } | null;
      return date ? { start: date.start, end: date.end } : null;
    }
    case "checkbox":
      return p.checkbox ?? false;
    case "url":
      return p.url ?? null;
    case "email":
      return p.email ?? null;
    case "phone_number":
      return p.phone_number ?? null;
    case "relation": {
      const rel = p.relation as Array<{ id: string }> | undefined;
      return rel?.map((r) => r.id) || [];
    }
    case "rollup": {
      const rollup = p.rollup as Record<string, unknown> | undefined;
      if (!rollup) return null;
      const rollupType = rollup.type as string;
      return rollup[rollupType] ?? null;
    }
    case "formula": {
      const formula = p.formula as Record<string, unknown> | undefined;
      if (!formula) return null;
      const formulaType = formula.type as string;
      return formula[formulaType] ?? null;
    }
    case "people": {
      const people = p.people as
        | Array<{ name: string; id: string }>
        | undefined;
      return (
        people?.map((person) => ({ id: person.id, name: person.name })) || []
      );
    }
    case "files": {
      const files = p.files as
        | Array<{
            name: string;
            file?: { url: string };
            external?: { url: string };
          }>
        | undefined;
      return (
        files?.map((f) => ({
          name: f.name,
          url: f.file?.url || f.external?.url || null,
        })) || []
      );
    }
    case "created_time":
      return p.created_time ?? null;
    case "last_edited_time":
      return p.last_edited_time ?? null;
    case "created_by":
    case "last_edited_by": {
      const user = p[type] as { id: string; name?: string } | null;
      return user ? { id: user.id, name: user.name } : null;
    }
    case "unique_id": {
      const uid = p.unique_id as {
        number: number;
        prefix: string | null;
      } | null;
      return uid ? `${uid.prefix || ""}${uid.number}` : null;
    }
    default:
      return p[type] ?? null;
  }
}

/**
 * Get database schema (properties)
 */
async function getDatabaseSchema(
  dbId: string,
): Promise<Record<string, unknown>> {
  const { client } = await getNotionClient();

  const response = await client.request<Record<string, unknown>>({
    path: `databases/${dbId}`,
    method: "get",
  });

  return response.properties as Record<string, unknown>;
}

// =============================================================================
// lw notion list
// =============================================================================

notionCommand
  .command("list")
  .description("List available Notion databases")
  .option("--format <format>", "Output format: table, json", "table")
  .action(async (options) => {
    if (options.format === "json") {
      console.log(JSON.stringify(DATABASES, null, 2));
      return;
    }

    console.log(chalk.blue("\n=== Notion Databases ===\n"));
    console.log(
      chalk.gray(
        `${"Key".padEnd(15)} ${"Name".padEnd(30)} ${"Description".padEnd(40)}`,
      ),
    );
    console.log(chalk.gray("-".repeat(87)));

    for (const [key, config] of Object.entries(DATABASES)) {
      console.log(
        `${chalk.cyan(key.padEnd(15))} ` +
          `${config.name.padEnd(30)} ` +
          `${chalk.gray(config.description)}`,
      );
    }

    console.log(
      chalk.gray(`\n${Object.keys(DATABASES).length} database(s) configured`),
    );
    console.log(chalk.yellow("\nTo export: lw notion export [--db <key>]"));
  });

// =============================================================================
// lw notion stats
// =============================================================================

notionCommand
  .command("stats")
  .description("Show database statistics")
  .option("--format <format>", "Output format: table, json", "table")
  .action(async (options) => {
    const spinner = ora("Fetching database statistics...").start();

    try {
      const { client } = await getNotionClient();
      const stats: Record<
        string,
        { count: number; lastEdited: string | null }
      > = {};

      for (const [key, config] of Object.entries(DATABASES)) {
        spinner.text = `Counting ${config.name}...`;

        let count = 0;
        let hasMore = true;
        let startCursor: string | undefined;
        let lastEdited: string | null = null;

        while (hasMore) {
          const response = await client.request<DatabaseQueryResponse>({
            path: `databases/${config.id}/query`,
            method: "post",
            body: {
              page_size: 100,
              start_cursor: startCursor,
              sorts: [
                { timestamp: "last_edited_time", direction: "descending" },
              ],
            },
          });

          count += response.results.length;

          // Get most recent edit time from first page of first request
          if (!lastEdited && response.results.length > 0) {
            lastEdited = response.results[0].last_edited_time as string;
          }

          hasMore = response.has_more;
          startCursor = response.next_cursor || undefined;
        }

        stats[key] = { count, lastEdited };
      }

      spinner.stop();

      if (options.format === "json") {
        console.log(JSON.stringify(stats, null, 2));
        return;
      }

      console.log(chalk.blue("\n=== Database Statistics ===\n"));
      console.log(
        chalk.gray(
          `${"Database".padEnd(15)} ${"Count".padEnd(10)} ${"Last Modified".padEnd(25)}`,
        ),
      );
      console.log(chalk.gray("-".repeat(52)));

      let total = 0;
      for (const [key, data] of Object.entries(stats)) {
        const lastMod = data.lastEdited
          ? new Date(data.lastEdited).toLocaleString()
          : "-";
        console.log(
          `${chalk.cyan(key.padEnd(15))} ` +
            `${data.count.toString().padEnd(10)} ` +
            `${chalk.gray(lastMod)}`,
        );
        total += data.count;
      }

      console.log(chalk.gray("-".repeat(52)));
      console.log(chalk.green(`Total records: ${total}`));
    } catch (err) {
      spinner.fail("Failed to fetch statistics");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw notion export
// =============================================================================

notionCommand
  .command("export")
  .description("Export Notion databases to JSON files")
  .option(
    "--db <key>",
    "Export specific database (tasks, epics, sprints, etc.)",
  )
  .option("--all", "Include archived/deleted records")
  .option("--output <dir>", "Output directory", "data/notion_exports")
  .option("--schema", "Also export database schemas")
  .action(async (options) => {
    const spinner = ora("Preparing export...").start();

    try {
      // Ensure output directory exists
      const outputDir = path.resolve(process.cwd(), options.output);
      if (!fs.existsSync(outputDir)) {
        fs.mkdirSync(outputDir, { recursive: true });
      }

      // Create timestamped subdirectory
      const timestamp = new Date().toISOString().split("T")[0];
      const exportDir = path.join(outputDir, timestamp);
      if (!fs.existsSync(exportDir)) {
        fs.mkdirSync(exportDir, { recursive: true });
      }

      const databasesToExport: Record<string, DatabaseConfig> = {};

      if (options.db) {
        // Export specific database
        const config = DATABASES[options.db];
        if (!config) {
          spinner.fail(`Unknown database: ${options.db}`);
          console.log(chalk.yellow("\nAvailable databases:"));
          Object.keys(DATABASES).forEach((k) => console.log(`  - ${k}`));
          process.exit(1);
        }
        databasesToExport[options.db] = config;
      } else {
        // Export all databases
        Object.assign(databasesToExport, DATABASES);
      }

      const results: Array<{ db: string; count: number; file: string }> = [];

      for (const [key, config] of Object.entries(databasesToExport)) {
        spinner.text = `Exporting ${config.name}...`;

        const { count, file } = await exportDatabase(
          key,
          config,
          exportDir,
          options.all || false,
        );

        results.push({ db: key, count, file });

        // Export schema if requested
        if (options.schema) {
          spinner.text = `Exporting ${config.name} schema...`;
          const schema = await getDatabaseSchema(config.id);
          const schemaFile = path.join(exportDir, `${key}.schema.json`);
          fs.writeFileSync(schemaFile, JSON.stringify(schema, null, 2));
        }
      }

      spinner.succeed("Export complete!");

      console.log(chalk.blue("\n=== Export Results ===\n"));
      console.log(
        chalk.gray(
          `${"Database".padEnd(15)} ${"Records".padEnd(10)} ${"File".padEnd(50)}`,
        ),
      );
      console.log(chalk.gray("-".repeat(77)));

      let totalRecords = 0;
      for (const result of results) {
        console.log(
          `${chalk.cyan(result.db.padEnd(15))} ` +
            `${result.count.toString().padEnd(10)} ` +
            `${chalk.gray(path.relative(process.cwd(), result.file))}`,
        );
        totalRecords += result.count;
      }

      console.log(chalk.gray("-".repeat(77)));
      console.log(
        chalk.green(`Total: ${totalRecords} records exported to ${exportDir}`),
      );

      // Write manifest
      const manifest = {
        exportedAt: new Date().toISOString(),
        databases: results.map((r) => ({
          key: r.db,
          count: r.count,
          file: path.basename(r.file),
        })),
        totalRecords,
        includesArchived: options.all || false,
        includesSchemas: options.schema || false,
      };
      fs.writeFileSync(
        path.join(exportDir, "manifest.json"),
        JSON.stringify(manifest, null, 2),
      );

      console.log(
        chalk.yellow(`\nManifest: ${path.join(exportDir, "manifest.json")}`),
      );
    } catch (err) {
      spinner.fail("Export failed");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw notion schema
// =============================================================================

notionCommand
  .command("schema <db-key>")
  .description("Show database schema (properties)")
  .option("--format <format>", "Output format: table, json", "table")
  .action(async (dbKey: string, options) => {
    const config = DATABASES[dbKey];
    if (!config) {
      console.error(chalk.red(`Unknown database: ${dbKey}`));
      console.log(chalk.yellow("\nAvailable databases:"));
      Object.keys(DATABASES).forEach((k) => console.log(`  - ${k}`));
      process.exit(1);
    }

    const spinner = ora(`Fetching schema for ${config.name}...`).start();

    try {
      const schema = await getDatabaseSchema(config.id);
      spinner.stop();

      if (options.format === "json") {
        console.log(JSON.stringify(schema, null, 2));
        return;
      }

      console.log(chalk.blue(`\n=== ${config.name} Schema ===\n`));
      console.log(
        chalk.gray(
          `${"Property".padEnd(35)} ${"Type".padEnd(15)} ${"Details".padEnd(30)}`,
        ),
      );
      console.log(chalk.gray("-".repeat(82)));

      for (const [name, prop] of Object.entries(schema)) {
        const p = prop as Record<string, unknown>;
        const type = p.type as string;
        let details = "";

        // Extract relevant details based on type
        if (type === "select" || type === "status") {
          const opts = (p[type] as { options?: Array<{ name: string }> })
            ?.options;
          if (opts) {
            details = opts
              .map((o) => o.name)
              .slice(0, 3)
              .join(", ");
            if (opts.length > 3) details += "...";
          }
        } else if (type === "relation") {
          const rel = p.relation as { database_id?: string };
          if (rel?.database_id) {
            // Find the database name
            const relDb = Object.entries(DATABASES).find(
              ([_, cfg]) => cfg.id === rel.database_id,
            );
            details = relDb
              ? `-> ${relDb[0]}`
              : `-> ${rel.database_id.slice(0, 8)}`;
          }
        } else if (type === "multi_select") {
          const opts = (p.multi_select as { options?: Array<{ name: string }> })
            ?.options;
          if (opts) {
            details = opts
              .map((o) => o.name)
              .slice(0, 3)
              .join(", ");
            if (opts.length > 3) details += "...";
          }
        }

        const truncName =
          name.length > 33 ? name.substring(0, 33) + ".." : name;
        const truncDetails =
          details.length > 28 ? details.substring(0, 28) + ".." : details;

        console.log(
          `${chalk.cyan(truncName.padEnd(35))} ` +
            `${chalk.yellow(type.padEnd(15))} ` +
            `${chalk.gray(truncDetails)}`,
        );
      }

      console.log(chalk.gray(`\n${Object.keys(schema).length} properties`));
    } catch (err) {
      spinner.fail("Failed to fetch schema");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw notion import
// =============================================================================

/**
 * Find the lightwave-platform directory
 */
function findPlatformDir(): string | null {
  // Check various possible locations
  const candidates = [
    path.resolve(process.cwd(), "../../lightwave-platform"),
    path.resolve(process.cwd(), "../lightwave-platform"),
    path.resolve(process.cwd(), "lightwave-platform"),
    path.resolve(process.cwd(), "../../../lightwave-platform"),
  ];

  for (const candidate of candidates) {
    if (
      fs.existsSync(candidate) &&
      fs.existsSync(path.join(candidate, "docker-compose.yml"))
    ) {
      return candidate;
    }
  }

  return null;
}

/**
 * Find the latest export directory
 */
function findLatestExport(baseDir: string): string | null {
  if (!fs.existsSync(baseDir)) return null;

  const entries = fs.readdirSync(baseDir, { withFileTypes: true });
  const dirs = entries
    .filter((e) => e.isDirectory())
    .map((e) => e.name)
    .sort()
    .reverse();

  return dirs.length > 0 ? path.join(baseDir, dirs[0]) : null;
}

notionCommand
  .command("import")
  .description("Import Notion JSON exports into Django")
  .option("--dir <path>", "Path to export directory")
  .option("--dry-run", "Preview changes without modifying database")
  .option("--domains-only", "Only import Life Domains")
  .option("--epics-only", "Only import Epics")
  .option("--sprints-only", "Only import Sprints")
  .option("--stories-only", "Only import User Stories")
  .option("--tasks-only", "Only import Tasks")
  .option("-v, --verbose", "Verbose output")
  .action(async (options) => {
    const spinner = ora("Preparing import...").start();

    try {
      // Find the lightwave-platform directory
      const platformDir = findPlatformDir();
      if (!platformDir) {
        spinner.fail("Could not find lightwave-platform directory");
        console.log(chalk.yellow("\nMake sure you're in the lightwave monorepo"));
        process.exit(1);
      }

      // Determine export directory
      let exportDir: string;
      if (options.dir) {
        exportDir = path.resolve(options.dir);
      } else {
        // Find latest export
        const exportsBase = path.resolve(process.cwd(), "data/notion_exports");
        const latest = findLatestExport(exportsBase);
        if (!latest) {
          spinner.fail("No export directory found");
          console.log(
            chalk.yellow(
              "\nRun 'lw notion export' first, or specify --dir <path>",
            ),
          );
          process.exit(1);
        }
        exportDir = latest;
      }

      if (!fs.existsSync(exportDir)) {
        spinner.fail(`Export directory not found: ${exportDir}`);
        process.exit(1);
      }

      // Check for manifest
      const manifestPath = path.join(exportDir, "manifest.json");
      if (fs.existsSync(manifestPath)) {
        const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf-8"));
        spinner.info(
          `Loading export from ${manifest.exportedAt} (${manifest.totalRecords} records)`,
        );
      } else {
        spinner.info(`Loading export from ${exportDir}`);
      }

      // Copy export files to backend data directory (accessible to Docker)
      const backendDataDir = path.join(
        platformDir,
        "lwm_core/backend/data/notion_exports",
      );
      if (!fs.existsSync(backendDataDir)) {
        fs.mkdirSync(backendDataDir, { recursive: true });
      }

      // Create a symlink or copy the files
      const targetDir = path.join(
        backendDataDir,
        path.basename(exportDir),
      );

      spinner.text = "Copying export files to backend...";

      // Remove existing target if it exists
      if (fs.existsSync(targetDir)) {
        fs.rmSync(targetDir, { recursive: true });
      }

      // Copy files
      fs.cpSync(exportDir, targetDir, { recursive: true });

      spinner.text = "Running Django import command...";
      spinner.stop();

      // Build the Django command
      const djangoArgs = ["import_notion_json"];

      if (options.dryRun) {
        djangoArgs.push("--dry-run");
      }
      if (options.domainsOnly) {
        djangoArgs.push("--domains-only");
      }
      if (options.epicsOnly) {
        djangoArgs.push("--epics-only");
      }
      if (options.sprintsOnly) {
        djangoArgs.push("--sprints-only");
      }
      if (options.storiesOnly) {
        djangoArgs.push("--stories-only");
      }
      if (options.tasksOnly) {
        djangoArgs.push("--tasks-only");
      }
      if (options.verbose) {
        djangoArgs.push("-v", "2");
      }

      // Add the directory path (relative to /code in container)
      djangoArgs.push(
        "--dir",
        `/code/data/notion_exports/${path.basename(exportDir)}`,
      );

      console.log(chalk.blue("\n=== Running Django Import ===\n"));
      console.log(chalk.gray(`Platform: ${platformDir}`));
      console.log(chalk.gray(`Export: ${exportDir}`));
      console.log(chalk.gray(`Command: python manage.py ${djangoArgs.join(" ")}\n`));

      // Execute the Django management command via docker compose
      const dockerCmd = [
        "docker",
        "compose",
        "exec",
        "-T",
        "web",
        "python",
        "manage.py",
        ...djangoArgs,
      ];

      const result = spawn(dockerCmd[0], dockerCmd.slice(1), {
        cwd: platformDir,
        stdio: "inherit",
      });

      result.on("close", (code) => {
        if (code === 0) {
          console.log(chalk.green("\n✓ Import completed successfully"));

          if (options.dryRun) {
            console.log(
              chalk.yellow(
                "\nDRY RUN complete. Run without --dry-run to apply changes.",
              ),
            );
          }
        } else {
          console.log(chalk.red(`\n✗ Import failed with code ${code}`));
          process.exit(code || 1);
        }
      });

      result.on("error", (err) => {
        console.error(chalk.red(`\n✗ Failed to run import: ${err.message}`));
        console.log(
          chalk.yellow("\nMake sure Docker is running and web service is up:"),
        );
        console.log(chalk.gray("  cd lightwave-platform && make start-bg"));
        process.exit(1);
      });
    } catch (err) {
      spinner.fail("Import failed");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });
