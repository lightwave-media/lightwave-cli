import { Command } from "commander";
import chalk from "chalk";
import { join } from "path";
import { findWorkspaceRoot } from "../utils/paths.js";
import { exec } from "../utils/exec.js";

/**
 * Outbox CLI commands for CMS approval queue management.
 *
 * The outbox is a review queue where all page changes from AI agents
 * and non-admin users are staged for approval before going live.
 */
export const outboxCommand = new Command("outbox").description(
  "CMS approval queue - review and approve page changes",
);

// =============================================================================
// Helper: Get CMS API URL
// =============================================================================

function getCmsApiUrl(): string {
  return process.env.CMS_API_URL || "http://localhost:8000/api/cms";
}

// =============================================================================
// Helper: Make API request via Docker
// =============================================================================

interface OutboxItem {
  id: string;
  item_type: string;
  status: string;
  site_domain: string;
  description: string;
  created_at: string;
  item_data?: Record<string, unknown>;
  created_by_email?: string;
  rejection_reason?: string;
}

async function fetchOutboxItems(options: {
  status?: string;
  site?: string;
  type?: string;
}): Promise<OutboxItem[]> {
  const root = findWorkspaceRoot();
  const lwmPath = join(root, "lightwave-platform", "lwm_core");

  // Build Django management command args
  const args = ["manage.py", "shell", "-c"];
  const pythonCode = `
import json
from apps.cms.models import OutboxItem
qs = OutboxItem.objects.select_related('site', 'created_by').order_by('-created_at')
${options.status ? `qs = qs.filter(status='${options.status}')` : ""}
${options.site ? `qs = qs.filter(site__domain='${options.site}')` : ""}
${options.type ? `qs = qs.filter(item_type='${options.type}')` : ""}
items = []
for item in qs[:50]:
    items.append({
        'id': str(item.id),
        'item_type': item.item_type,
        'status': item.status,
        'site_domain': item.site.domain if item.site else '',
        'description': item.get_description(),
        'created_at': item.created_at.isoformat() if item.created_at else '',
        'created_by_email': item.created_by.email if item.created_by else '',
    })
print(json.dumps(items))
`;
  args.push(pythonCode);

  try {
    const result = await exec(
      "docker",
      ["compose", "exec", "-T", "web", "python", ...args],
      { cwd: lwmPath, silent: true },
    );

    return JSON.parse(result.stdout.trim());
  } catch (err: unknown) {
    const error = err as Error;
    console.error(chalk.red(`Failed to fetch outbox items: ${error.message}`));
    return [];
  }
}

async function getOutboxItem(itemId: string): Promise<OutboxItem | null> {
  const root = findWorkspaceRoot();
  const lwmPath = join(root, "lightwave-platform", "lwm_core");

  const args = ["manage.py", "shell", "-c"];
  const pythonCode = `
import json
from apps.cms.models import OutboxItem
try:
    item = OutboxItem.objects.select_related('site', 'created_by').get(id='${itemId}')
    print(json.dumps({
        'id': str(item.id),
        'item_type': item.item_type,
        'status': item.status,
        'site_domain': item.site.domain if item.site else '',
        'description': item.get_description(),
        'created_at': item.created_at.isoformat() if item.created_at else '',
        'created_by_email': item.created_by.email if item.created_by else '',
        'item_data': item.item_data,
        'rejection_reason': item.rejection_reason or '',
    }))
except OutboxItem.DoesNotExist:
    print('null')
`;
  args.push(pythonCode);

  try {
    const result = await exec(
      "docker",
      ["compose", "exec", "-T", "web", "python", ...args],
      { cwd: lwmPath, silent: true },
    );

    const output = result.stdout.trim();
    if (output === "null") return null;
    return JSON.parse(output);
  } catch {
    return null;
  }
}

async function approveItem(
  itemId: string,
): Promise<{ success: boolean; message: string }> {
  const root = findWorkspaceRoot();
  const lwmPath = join(root, "lightwave-platform", "lwm_core");

  const args = ["manage.py", "shell", "-c"];
  const pythonCode = `
import json
from django.contrib.auth import get_user_model
from apps.cms.models import OutboxItem, OutboxItemStatus
User = get_user_model()
try:
    item = OutboxItem.objects.get(id='${itemId}')
    if item.status != OutboxItemStatus.PENDING:
        print(json.dumps({'success': False, 'message': f'Item is already {item.get_status_display()}'}))
    else:
        # Get a user for approval (use first superuser for CLI)
        user = User.objects.filter(is_superuser=True).first()
        item.apply(user)
        print(json.dumps({'success': True, 'message': f'Approved: {item.get_description()}'}))
except OutboxItem.DoesNotExist:
    print(json.dumps({'success': False, 'message': 'Item not found'}))
except Exception as e:
    print(json.dumps({'success': False, 'message': str(e)}))
`;
  args.push(pythonCode);

  try {
    const result = await exec(
      "docker",
      ["compose", "exec", "-T", "web", "python", ...args],
      { cwd: lwmPath, silent: true },
    );

    return JSON.parse(result.stdout.trim());
  } catch (err: unknown) {
    const error = err as Error;
    return { success: false, message: error.message };
  }
}

async function rejectItem(
  itemId: string,
  reason: string,
): Promise<{ success: boolean; message: string }> {
  const root = findWorkspaceRoot();
  const lwmPath = join(root, "lightwave-platform", "lwm_core");

  const args = ["manage.py", "shell", "-c"];
  // Escape the reason string for Python
  const escapedReason = reason.replace(/'/g, "\\'").replace(/"/g, '\\"');
  const pythonCode = `
import json
from django.contrib.auth import get_user_model
from apps.cms.models import OutboxItem, OutboxItemStatus
User = get_user_model()
try:
    item = OutboxItem.objects.get(id='${itemId}')
    if item.status != OutboxItemStatus.PENDING:
        print(json.dumps({'success': False, 'message': f'Item is already {item.get_status_display()}'}))
    else:
        user = User.objects.filter(is_superuser=True).first()
        item.reject(user, '${escapedReason}')
        print(json.dumps({'success': True, 'message': f'Rejected: {item.get_description()}'}))
except OutboxItem.DoesNotExist:
    print(json.dumps({'success': False, 'message': 'Item not found'}))
except Exception as e:
    print(json.dumps({'success': False, 'message': str(e)}))
`;
  args.push(pythonCode);

  try {
    const result = await exec(
      "docker",
      ["compose", "exec", "-T", "web", "python", ...args],
      { cwd: lwmPath, silent: true },
    );

    return JSON.parse(result.stdout.trim());
  } catch (err: unknown) {
    const error = err as Error;
    return { success: false, message: error.message };
  }
}

// =============================================================================
// COMMANDS
// =============================================================================

/**
 * lw outbox list
 * List outbox items (default: pending)
 */
outboxCommand
  .command("list")
  .description("List outbox items awaiting approval")
  .option(
    "--status <status>",
    "Filter by status (pending, applied, rejected)",
    "pending",
  )
  .option("--site <domain>", "Filter by site domain")
  .option(
    "--type <type>",
    "Filter by item type (page_creation, page_update, etc.)",
  )
  .action(async (options) => {
    const items = await fetchOutboxItems({
      status: options.status,
      site: options.site,
      type: options.type,
    });

    if (items.length === 0) {
      console.log(chalk.yellow(`No ${options.status} outbox items found`));
      return;
    }

    console.log(chalk.blue(`\n=== Outbox Items (${options.status}) ===\n`));

    // Group by site
    const bySite: Record<string, OutboxItem[]> = {};
    for (const item of items) {
      const site = item.site_domain || "unknown";
      if (!bySite[site]) bySite[site] = [];
      bySite[site].push(item);
    }

    for (const [site, siteItems] of Object.entries(bySite)) {
      console.log(chalk.cyan(`\n${site}`));
      for (const item of siteItems) {
        const statusIcon = getStatusIcon(item.status);
        const id = item.id.slice(0, 8);
        const created = new Date(item.created_at).toLocaleDateString();
        console.log(`  ${statusIcon} ${chalk.gray(id)} ${item.description}`);
        console.log(chalk.gray(`      ${item.item_type} | ${created}`));
      }
    }

    console.log(chalk.gray(`\nTotal: ${items.length} items\n`));
    console.log(chalk.gray("Commands:"));
    console.log(chalk.gray("  lw outbox approve <id>  - Approve and publish"));
    console.log(chalk.gray("  lw outbox reject <id>   - Reject with reason"));
    console.log(chalk.gray("  lw outbox info <id>     - View details"));
  });

/**
 * lw outbox info <id>
 * Show details of an outbox item
 */
outboxCommand
  .command("info <id>")
  .description("Show details of an outbox item")
  .action(async (id: string) => {
    // Support short ID (8 chars) - will need to search
    const item = await getOutboxItem(id);

    if (!item) {
      console.log(chalk.red(`Outbox item not found: ${id}`));
      return;
    }

    console.log(chalk.blue("\n=== Outbox Item Details ===\n"));
    console.log(chalk.white("ID:"), item.id);
    console.log(chalk.white("Type:"), item.item_type);
    console.log(
      chalk.white("Status:"),
      getStatusIcon(item.status),
      item.status,
    );
    console.log(chalk.white("Site:"), item.site_domain);
    console.log(chalk.white("Description:"), item.description);
    console.log(chalk.white("Created:"), item.created_at);
    console.log(chalk.white("Created By:"), item.created_by_email || "Unknown");

    if (item.rejection_reason) {
      console.log(
        chalk.white("Rejection Reason:"),
        chalk.red(item.rejection_reason),
      );
    }

    if (item.item_data) {
      console.log(chalk.white("\nItem Data:"));
      console.log(chalk.gray(JSON.stringify(item.item_data, null, 2)));
    }

    if (item.status === "pending") {
      console.log(chalk.gray("\nActions:"));
      console.log(chalk.gray(`  lw outbox approve ${item.id.slice(0, 8)}`));
      console.log(
        chalk.gray(`  lw outbox reject ${item.id.slice(0, 8)} --reason "..."`),
      );
    }
  });

/**
 * lw outbox approve <id>
 * Approve and apply an outbox item
 */
outboxCommand
  .command("approve <id>")
  .description("Approve and publish an outbox item")
  .action(async (id: string) => {
    console.log(chalk.blue(`Approving outbox item: ${id}...`));

    const result = await approveItem(id);

    if (result.success) {
      console.log(chalk.green(`\n${result.message}`));
    } else {
      console.log(chalk.red(`\nFailed: ${result.message}`));
    }
  });

/**
 * lw outbox reject <id>
 * Reject an outbox item
 */
outboxCommand
  .command("reject <id>")
  .description("Reject an outbox item")
  .option("--reason <reason>", "Rejection reason", "Rejected via CLI")
  .action(async (id: string, options) => {
    console.log(chalk.blue(`Rejecting outbox item: ${id}...`));

    const result = await rejectItem(id, options.reason);

    if (result.success) {
      console.log(chalk.yellow(`\n${result.message}`));
    } else {
      console.log(chalk.red(`\nFailed: ${result.message}`));
    }
  });

/**
 * lw outbox approve-all
 * Approve all pending items (with optional filters)
 */
outboxCommand
  .command("approve-all")
  .description("Approve all pending outbox items")
  .option("--site <domain>", "Filter by site domain")
  .option("--dry-run", "Preview what would be approved")
  .action(async (options) => {
    const items = await fetchOutboxItems({
      status: "pending",
      site: options.site,
    });

    if (items.length === 0) {
      console.log(chalk.yellow("No pending outbox items found"));
      return;
    }

    console.log(chalk.blue(`\nFound ${items.length} pending items:\n`));
    for (const item of items) {
      console.log(`  ${chalk.gray(item.id.slice(0, 8))} ${item.description}`);
    }

    if (options.dryRun) {
      console.log(chalk.yellow("\n[Dry run - no changes made]"));
      return;
    }

    console.log(chalk.blue("\nApproving all..."));

    let approved = 0;
    let failed = 0;

    for (const item of items) {
      const result = await approveItem(item.id);
      if (result.success) {
        console.log(chalk.green(`  ✓ ${item.id.slice(0, 8)}`));
        approved++;
      } else {
        console.log(chalk.red(`  ✗ ${item.id.slice(0, 8)}: ${result.message}`));
        failed++;
      }
    }

    console.log(chalk.blue(`\nApproved: ${approved}, Failed: ${failed}`));
  });

/**
 * lw outbox count
 * Get count of pending items
 */
outboxCommand
  .command("count")
  .description("Get count of pending outbox items")
  .option("--site <domain>", "Filter by site domain")
  .action(async (options) => {
    const items = await fetchOutboxItems({
      status: "pending",
      site: options.site,
    });

    const siteFilter = options.site ? ` for ${options.site}` : "";
    console.log(
      chalk.blue(`Pending outbox items${siteFilter}: ${items.length}`),
    );
  });

// =============================================================================
// HELPERS
// =============================================================================

function getStatusIcon(status: string): string {
  switch (status) {
    case "pending":
      return chalk.yellow("○");
    case "applied":
      return chalk.green("✓");
    case "rejected":
      return chalk.red("✗");
    default:
      return chalk.gray("?");
  }
}
