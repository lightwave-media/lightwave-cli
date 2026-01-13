/**
 * Document management commands - read specs from Notion
 *
 * Commands:
 *   lw doc list              List documents from Notion
 *   lw doc read <id>         Read document content
 *   lw doc search <query>    Search documents by name
 */

import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import {
  queryDocuments,
  getDocument,
  getDocumentContent,
} from "../utils/notion.js";
import type { NotionDocument } from "../types/notion.js";

export const docCommand = new Command("doc").description(
  "Document management - read specs and configs from Notion",
);

/**
 * Get chalk color for document status
 */
function getStatusColor(status: string | null) {
  if (!status) return chalk.gray;
  if (status.includes("Active") || status.includes("Live")) return chalk.green;
  if (status.includes("Draft")) return chalk.yellow;
  if (status.includes("Archive")) return chalk.gray;
  return chalk.white;
}

// =============================================================================
// lw doc list
// =============================================================================

docCommand
  .command("list")
  .description("List documents from Notion")
  .option(
    "--status <status>",
    "Filter by Document Status (e.g., '📢 Active/Live')",
  )
  .option("--type <type>", "Filter by Content Type (e.g., 'Config', 'SOP')")
  .option(
    "--tags <tags>",
    "Filter by Agent Tags (comma-separated, e.g., 'agent:v_speak,agent:v_core')",
  )
  .option("--limit <n>", "Max number of documents", "20")
  .option("--format <format>", "Output format: table, json", "table")
  .action(async (options) => {
    const spinner = ora("Fetching documents from Notion...").start();

    try {
      // Parse tags
      const tags = options.tags
        ? options.tags.split(",").map((t: string) => t.trim())
        : undefined;

      const docs = await queryDocuments({
        status: options.status,
        contentType: options.type,
        tags,
        limit: parseInt(options.limit, 10),
      });

      spinner.stop();

      if (docs.length === 0) {
        console.log(chalk.yellow("No documents found matching criteria."));
        return;
      }

      if (options.format === "json") {
        console.log(JSON.stringify(docs, null, 2));
        return;
      }

      // Table format
      console.log(chalk.blue("\n=== Documents ===\n"));
      console.log(
        chalk.gray(
          `${"ID".padEnd(10)} ${"Type".padEnd(12)} ${"Status".padEnd(16)} ${"Name".padEnd(50)}`,
        ),
      );
      console.log(chalk.gray("-".repeat(90)));

      for (const doc of docs) {
        const statusColor = getStatusColor(doc.status);
        const contentType = doc.contentType || "-";
        const status = doc.status || "-";
        const truncatedName =
          doc.name.length > 48 ? doc.name.substring(0, 48) + "..." : doc.name;
        console.log(
          `${chalk.cyan(doc.shortId.padEnd(10))} ` +
            `${chalk.gray(contentType.padEnd(12))} ` +
            `${statusColor(status.padEnd(16))} ` +
            `${truncatedName}`,
        );
      }

      console.log(chalk.gray(`\n${docs.length} document(s) shown`));
    } catch (err) {
      spinner.fail("Failed to fetch documents");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw doc read <id>
// =============================================================================

docCommand
  .command("read <doc-id>")
  .description("Read document content from Notion")
  .option("--format <format>", "Output format: markdown, json", "markdown")
  .option("--meta", "Include metadata (type, status, tags)")
  .action(async (docId: string, options) => {
    const spinner = ora("Loading document from Notion...").start();

    try {
      // Get document metadata
      const doc = await getDocument(docId);
      if (!doc) {
        spinner.fail(`Document not found: ${docId}`);
        process.exit(1);
      }

      spinner.text = `Reading content: ${doc.name}`;

      // Get document content
      const content = await getDocumentContent(doc.id);

      spinner.stop();

      if (options.format === "json") {
        console.log(JSON.stringify({ ...doc, content }, null, 2));
        return;
      }

      // Markdown format
      if (options.meta) {
        console.log(chalk.blue("\n=== Document Metadata ===\n"));
        console.log(chalk.yellow("ID:"), doc.shortId);
        console.log(chalk.yellow("Name:"), doc.name);
        console.log(chalk.yellow("Type:"), doc.contentType || "(none)");
        console.log(chalk.yellow("Version:"), doc.version || "(none)");
        console.log(chalk.yellow("Status:"), doc.status || "(none)");
        console.log(
          chalk.yellow("Tags:"),
          doc.agentTags.length > 0 ? doc.agentTags.join(", ") : "(none)",
        );
        console.log(chalk.yellow("URL:"), doc.url);
        console.log(chalk.blue("\n=== Content ===\n"));
      }

      console.log(content);
    } catch (err) {
      spinner.fail("Failed to read document");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw doc search <query>
// =============================================================================

docCommand
  .command("search <query>")
  .description("Search documents by name")
  .option("--limit <n>", "Max results", "10")
  .option("--format <format>", "Output format: table, json", "table")
  .action(async (query: string, options) => {
    const spinner = ora(`Searching for "${query}"...`).start();

    try {
      // Get all docs and filter by name
      const allDocs = await queryDocuments({ limit: 100 });
      const lowerQuery = query.toLowerCase();
      const matches = allDocs.filter((doc) =>
        doc.name.toLowerCase().includes(lowerQuery),
      );

      const limitedMatches = matches.slice(0, parseInt(options.limit, 10));

      spinner.stop();

      if (limitedMatches.length === 0) {
        console.log(chalk.yellow(`No documents found matching "${query}".`));
        return;
      }

      if (options.format === "json") {
        console.log(JSON.stringify(limitedMatches, null, 2));
        return;
      }

      // Table format
      console.log(chalk.blue(`\n=== Search Results for "${query}" ===\n`));
      console.log(
        chalk.gray(
          `${"ID".padEnd(10)} ${"Type".padEnd(12)} ${"Name".padEnd(60)}`,
        ),
      );
      console.log(chalk.gray("-".repeat(84)));

      for (const doc of limitedMatches) {
        const contentType = doc.contentType || "-";
        const truncatedName =
          doc.name.length > 58 ? doc.name.substring(0, 58) + "..." : doc.name;
        console.log(
          `${chalk.cyan(doc.shortId.padEnd(10))} ` +
            `${chalk.gray(contentType.padEnd(12))} ` +
            `${truncatedName}`,
        );
      }

      console.log(
        chalk.gray(
          `\n${limitedMatches.length} of ${matches.length} match(es) shown`,
        ),
      );
    } catch (err) {
      spinner.fail("Search failed");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });
