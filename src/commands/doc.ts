/**
 * Document management commands - read specs from createOS (Django)
 *
 * Commands:
 *   lw doc list              List documents
 *   lw doc read <id>         Read document content
 *   lw doc search <query>    Search documents by name
 *   lw doc create <name>     Create a new document
 *   lw doc info <id>         Show document details
 */

import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import {
  getBackend,
  queryDocumentsDjango,
  getDocumentDjango,
  getDocumentContentDjango,
  searchDocumentsDjango,
  createDocumentDjango,
} from "../utils/createos.js";
import {
  queryDocuments as queryDocumentsNotion,
  getDocument as getDocumentNotion,
  getDocumentContent as getDocumentContentNotion,
} from "../utils/notion.js";
import type {
  NotionDocument,
  DocumentListOptions,
  DocumentType,
  DocumentVertical,
  DocumentStatus,
  SecurityLevel,
} from "../types/notion.js";

export const docCommand = new Command("doc").description(
  "Document management - read specs and configs",
);

// Helper to select backend
async function queryDocuments(
  options: DocumentListOptions,
): Promise<NotionDocument[]> {
  const backend = getBackend();
  if (backend === "notion") {
    return queryDocumentsNotion(options);
  }
  return queryDocumentsDjango(options);
}

async function getDocument(docId: string): Promise<NotionDocument | null> {
  const backend = getBackend();
  if (backend === "notion") {
    return getDocumentNotion(docId);
  }
  return getDocumentDjango(docId);
}

async function getDocumentContent(docId: string): Promise<string | null> {
  const backend = getBackend();
  if (backend === "notion") {
    return getDocumentContentNotion(docId);
  }
  return getDocumentContentDjango(docId);
}

async function searchDocuments(
  query: string,
  limit: number,
): Promise<NotionDocument[]> {
  const backend = getBackend();
  if (backend === "notion") {
    // Notion doesn't have native search, filter locally
    const allDocs = await queryDocumentsNotion({ limit: 100 });
    const lowerQuery = query.toLowerCase();
    return allDocs
      .filter((doc) => doc.name.toLowerCase().includes(lowerQuery))
      .slice(0, limit);
  }
  return searchDocumentsDjango(query, { limit });
}

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
  .description("List documents")
  .option(
    "--status <status>",
    "Filter by Document Status (draft, active, archived, deprecated)",
  )
  .option(
    "--type <type>",
    "Filter by Document Type (spec, sop, config, guide, fleeting, script, etc.)",
  )
  .option(
    "--vertical <vertical>",
    "Filter by vertical (createos, cineos, photoos)",
  )
  .option("--template", "Show only templates")
  .option("--no-template", "Exclude templates")
  .option(
    "--notes",
    "Show only Zettelkasten notes (fleeting, literature, permanent)",
  )
  .option("--no-notes", "Exclude Zettelkasten notes")
  .option("--security <level>", "Filter by security level (low, medium, high)")
  .option(
    "--tags <tags>",
    "Filter by Agent Tags (comma-separated, e.g., 'v_speak,v_core')",
  )
  .option("--domain <id>", "Filter by domain ID or short_id")
  .option("--epic <id>", "Filter by related epic ID")
  .option("--sprint <id>", "Filter by related sprint ID")
  .option("--task <id>", "Filter by related task ID")
  .option("--limit <n>", "Max number of documents", "20")
  .option("--format <format>", "Output format: table, json", "table")
  .action(async (options) => {
    const backend = getBackend();
    const spinner = ora(`Fetching documents from ${backend}...`).start();

    try {
      // Parse tags
      const tags = options.tags
        ? options.tags.split(",").map((t: string) => t.trim())
        : undefined;

      // Build filter options
      const filterOptions: DocumentListOptions = {
        status: options.status,
        contentType: options.type,
        vertical: options.vertical,
        securityLevel: options.security,
        tags,
        domain: options.domain,
        epic: options.epic,
        sprint: options.sprint,
        task: options.task,
        limit: parseInt(options.limit, 10),
      };

      // Handle template filtering
      if (options.template === true) {
        filterOptions.isTemplate = true;
      } else if (options.template === false) {
        filterOptions.isTemplate = false;
      }

      // Handle notes filtering
      if (options.notes === true) {
        filterOptions.isNote = true;
      } else if (options.notes === false) {
        filterOptions.isNote = false;
      }

      const docs = await queryDocuments(filterOptions);

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
          `${"ID".padEnd(10)} ${"Vertical".padEnd(10)} ${"Type".padEnd(14)} ${"Status".padEnd(10)} ${"T".padEnd(2)} ${"Name".padEnd(44)}`,
        ),
      );
      console.log(chalk.gray("-".repeat(95)));

      for (const doc of docs) {
        const statusColor = getStatusColor(doc.status);
        const contentType = doc.contentType || "-";
        const status = doc.status || "-";
        const vertical = doc.vertical || "createos";
        const isTemplate = doc.isTemplate ? "T" : "-";
        const truncatedName =
          doc.name.length > 42 ? doc.name.substring(0, 42) + "..." : doc.name;
        console.log(
          `${chalk.cyan(doc.shortId.padEnd(10))} ` +
            `${chalk.magenta(vertical.padEnd(10))} ` +
            `${chalk.gray(contentType.padEnd(14))} ` +
            `${statusColor(status.padEnd(10))} ` +
            `${chalk.yellow(isTemplate.padEnd(2))} ` +
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
  .description("Read document content")
  .option("--format <format>", "Output format: markdown, json", "markdown")
  .option("--meta", "Include metadata (type, status, tags)")
  .action(async (docId: string, options) => {
    const backend = getBackend();
    const spinner = ora(`Loading document from ${backend}...`).start();

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
      const limit = parseInt(options.limit, 10);
      const limitedMatches = await searchDocuments(query, limit);

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

      console.log(chalk.gray(`\n${limitedMatches.length} match(es) found`));
    } catch (err) {
      spinner.fail("Search failed");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw doc info <id>
// =============================================================================

docCommand
  .command("info <doc-id>")
  .description("Show document details")
  .option("--format <format>", "Output format: table, json", "table")
  .action(async (docId: string, options) => {
    const backend = getBackend();
    const spinner = ora(`Loading document from ${backend}...`).start();

    try {
      const doc = await getDocument(docId);
      if (!doc) {
        spinner.fail(`Document not found: ${docId}`);
        process.exit(1);
      }

      spinner.stop();

      if (options.format === "json") {
        console.log(JSON.stringify(doc, null, 2));
        return;
      }

      console.log(chalk.blue("\n=== Document Details ===\n"));
      console.log(chalk.yellow("ID:"), doc.id);
      console.log(chalk.yellow("Short ID:"), doc.shortId);
      console.log(chalk.yellow("Name:"), doc.name);
      console.log(chalk.yellow("Type:"), doc.contentType || "(none)");
      console.log(chalk.yellow("Version:"), doc.version || "(none)");
      console.log(chalk.yellow("Status:"), doc.status || "(none)");
      console.log(
        chalk.yellow("Tags:"),
        doc.agentTags.length > 0 ? doc.agentTags.join(", ") : "(none)",
      );
      console.log(chalk.yellow("URL:"), doc.url);
      if (doc.epicIds && doc.epicIds.length > 0) {
        console.log(chalk.yellow("Epics:"), doc.epicIds.join(", "));
      }
      if (doc.taskIds && doc.taskIds.length > 0) {
        console.log(chalk.yellow("Tasks:"), doc.taskIds.join(", "));
      }
    } catch (err) {
      spinner.fail("Failed to load document");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });

// =============================================================================
// lw doc create <name>
// =============================================================================

docCommand
  .command("create <name>")
  .description("Create a new document")
  .option(
    "--type <type>",
    "Document type (spec, sop, config, guide, reference, template, fleeting, script, etc.)",
    "reference",
  )
  .option(
    "--vertical <vertical>",
    "Vertical (createos, cineos, photoos)",
    "createos",
  )
  .option("--status <status>", "Document status (draft, active)", "draft")
  .option("--template", "Mark as a template")
  .option("--security <level>", "Security level (low, medium, high)", "low")
  .option("--content <content>", "Initial content (markdown)")
  .option("--tags <tags>", "Agent tags (comma-separated)")
  .option("--tech-tags <tags>", "Tech tags (comma-separated)")
  .option("--audience <tags>", "Audience tags (comma-separated)")
  .option("--domain <id>", "Domain ID or short_id")
  .option("--parent <id>", "Parent document ID or short_id")
  .option("--url <url>", "External URL reference")
  .option("--format <format>", "Output format: table, json", "table")
  .action(async (name: string, options) => {
    const backend = getBackend();

    if (backend === "notion") {
      console.error(
        chalk.red(
          "Create is not supported with Notion backend. Use Django (default) backend.",
        ),
      );
      process.exit(1);
    }

    const spinner = ora("Creating document...").start();

    try {
      const agentTags = options.tags
        ? options.tags.split(",").map((t: string) => t.trim())
        : undefined;
      const techTags = options.techTags
        ? options.techTags.split(",").map((t: string) => t.trim())
        : undefined;
      const audienceTags = options.audience
        ? options.audience.split(",").map((t: string) => t.trim())
        : undefined;

      const doc = await createDocumentDjango(name, {
        documentType: options.type.toLowerCase(),
        vertical: options.vertical.toLowerCase(),
        status: options.status.toLowerCase(),
        isTemplate: options.template || false,
        securityLevel: options.security.toLowerCase(),
        content: options.content || "",
        agentTags,
        techTags,
        audienceTags,
        domainId: options.domain,
        parentId: options.parent,
        url: options.url,
      });

      spinner.succeed(`Created document: ${doc.shortId}`);

      if (options.format === "json") {
        console.log(JSON.stringify(doc, null, 2));
        return;
      }

      console.log(chalk.blue("\n=== New Document ===\n"));
      console.log(chalk.yellow("ID:"), doc.shortId);
      console.log(chalk.yellow("Name:"), doc.name);
      console.log(chalk.yellow("Vertical:"), doc.vertical);
      console.log(chalk.yellow("Type:"), doc.contentType || "(none)");
      console.log(chalk.yellow("Status:"), doc.status || "(none)");
      console.log(chalk.yellow("Template:"), doc.isTemplate ? "Yes" : "No");
      console.log(chalk.yellow("Security:"), doc.securityLevel);
      console.log(chalk.yellow("URL:"), doc.url);
    } catch (err) {
      spinner.fail("Failed to create document");
      console.error(chalk.red((err as Error).message));
      process.exit(1);
    }
  });
