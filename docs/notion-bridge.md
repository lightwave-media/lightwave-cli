# Notion bridge: operate, recover and verify

This implementation uses the published Core `bindings/go/v0.8.0` stamp, including
the contracts merged in Core PR #755. The knowledge domain remains in development
pending live commissioning. Build this branch and set `LW_CLI_DEV_DOMAINS=1` to
exercise it; its packaged stamp does not require a Core checkout. If iterating on
future contract changes, select the matching workspace with
`LW_CLI_LIVE_SCHEMAS=1` and `LW_LIGHTWAVE_ROOT`.

The bridge imports existing Notion pages into canonical files under
`$LW_HOME_PRINT/specs/notion_page/` and preserves their identities and recovery
state under `specs/external_ref/`. Postgres is a derived index. The Core stamp
defines their shapes; page content remains operator data.

## Configure a source

Create a `specs/notion_database/<database-uuid>.yaml` print matching the generated
`NotionDatabaseProjectionShape` model. Supply an explicit existing tenant UUID,
Notion database UUID, data-source UUID, title, URL, timestamps, `sync_enabled`,
`sync_status` and `direction`. Begin with `direction: inbound`; this captures data
without overwriting Notion. Database IDs and data-source IDs are different.

A `property_map_ref` names a print under `specs/notion_property_map/`. Its mappings
can set `authority: local`, `external` or `reconcile`, and `writable: false` for
provider-managed fields. The default for new bindings is three-way reconciliation:
independent edits combine; competing edits retain the base and both candidates.
Runtime-owned evidence should be declared `local`; human-owned planning fields
can be `external`. Neither an imported page nor an imported approval field grants
a harness permission to execute work.

The host bridge reads `/lightwave/prod/NOTION_API_KEY` from AWS SSM in us-east-1.
The operator must grant that integration access to each source. An explicit
`NOTION_API_KEY` environment value is a local fallback when the parameter is
absent. An inaccessible SSM store fails visibly instead of silently switching
identities. Interactive Notion connector access does not configure the daemon.
Credentials never belong in source prints, command arguments or reports.

## Review, import and reconcile

```sh
lw knowledge migrate --dry-run
lw knowledge migrate --yes
lw knowledge sync --dry-run --json
lw knowledge sync --json
lw knowledge status --json
```

Migrations target the configured database (`LW_DB_URL` or normal lw configuration).
The existing `tenants` table is required. They create the stamped Notion and
external-ref tables with tenant isolation. Existing tables missing required
columns cause a rollback and require an explicit upgrade migration.

Every pass enumerates configured data sources with pagination. It downloads
changed pages and pages with pending local edits; `--full` forces a body refresh
for all pages. Known pages missing from a query are fetched separately: absence
never means deletion. Relation and rich-text property lists are paginated when
the page response is incomplete. Unknown or truncated Markdown is marked
`content_complete: false`; it must not be used as a complete reset seed.

Switch the source and each approved page binding to `bidirectional` only after
reviewing its imported records and field ownership. Both must authorize delivery.
Edit `properties_json` and `markdown` in the page print to stage
local changes. Titles derive from the title property; editing the convenience
`title` field alone does not rename a Notion page. Status/select values use their
names; system IDs, colors and user display metadata are normalized for comparison.
A print writer should acquire the same `index/notion-sync.lock` advisory lock
while writing. Independent writers that ignore that protocol are not covered by
an atomic compare-and-swap guarantee.

Pending delivery intent is saved before an HTTP write. A later pass fetches the
provider before retrying an uncertain outcome, so an accepted write is not
blindly replayed. Notion does not expose transactional conditional updates for
this workflow: a human edit between the final read and a property PATCH remains
a provider race. Body updates use guarded old/new text replacement. There is no
claim of atomic multi-property/body delivery or exactly-once HTTP execution.

## Promote ready rows into nulltickets

```sh
lw knowledge promote --database <notion_id> --pipeline <nulltickets_pipeline_id> --dry-run --json
lw knowledge promote --database <notion_id> --pipeline <nulltickets_pipeline_id> --json
```

Promotion is the Notion → nulltickets hop. It reads the database's property map
to find which Notion properties carry the local fields `status` and
`context_packet`, queries the data source once per hundred rows (properties come
with the listing, so no page is fetched on its own), and posts every row whose
status is `run_session_ready` and whose packet is `verified` to `POST /tasks`.
Option labels are compared after folding to the enum vocabulary, so "Run Session
Ready" and "✅ Verified" match before and after the labels are renamed.

Each request carries `Idempotency-Key: notion:page:<id>`, and the created task
is recorded as a `provider: nulltickets` external-ref print keyed by the same
page, so a second pass reports `already_promoted` without a request. A ready row
whose packet is not verified is `surfaced` with the value it has and is never
promoted. A nulltickets failure is reported and leaves no binding; the next pass
retries and nulltickets deduplicates on the key. `--dry-run` sends nothing and
writes nothing.

nulltickets is located by `NULLTICKETS_URL` (default `http://127.0.0.1:7700`)
and the optional bearer `NULLTICKETS_API_TOKEN`, the same names the lw-webhook
GitHub hop uses. When `NULLTICKETS_API_TOKEN` is unset, a real run reads it
from SSM (`/lightwave/prod/NULLTICKETS_API_TOKEN`) by name for that request
only; agent sessions no longer carry it. A dry run reads nothing.

## Recover a derived index

Preserve both page and external-ref files. The latter contain the reconciliation
base and pending intent; reconstructing them from Notion would lose unpublished
local changes and delivery evidence.

```sh
lw knowledge migrate --dry-run
lw knowledge migrate --yes
lw knowledge reindex --dry-run --json
lw knowledge reindex --json
lw knowledge status --json
```

Reindex does not need Notion credentials, send provider requests, clear pending
writes or replace local content. Restore into an isolated Postgres database and
compare records before cutting over the production index. A files-only restore
is not a rollback of external writes that already reached Notion or GitHub.

## Scheduling and limits

The existing Core `notion_knowledge_hygiene` job declares `lw knowledge sync --json`
every 30 minutes. Provision credentials and prove a bidirectional round trip
before enabling it in the current runtime scheduler. The process lock rejects
overlapping invocations. Retain reports in the existing job observability path;
this implementation does not install a second scheduler or create a webhook
listener. Initial capture can take longer than one scheduled interval.

This slice mirrors existing pages and their relations. It does not create pages,
automatically promote them into dispatchable business tasks, download binary
attachments, unify domain records, or implement the GitHub delivery/check adapter.
Those steps require their existing identity mappings and authority contracts;
a successful mirror is not evidence that they have been commissioned.

Life Domains and Domains of Knowledge are one conceptual taxonomy in this
operator's plan. Both legacy collection IDs are preserved during capture. Domain
identity reconciliation belongs in an explicit mapping reviewed against the Core
stamp; similar labels never authorize deleting one of the records.

## Verification

`go test -race ./internal/knowledge/...` covers conflict preservation, retry
recovery, interrupted first imports, provider pagination, guarded body updates,
credential-safe errors and archive protection. Set `LW_KNOWLEDGE_TEST_DSN` to an
isolated administrative Postgres test connection to also run the restore and RLS
test. That test creates uniquely named schemas and a restricted role, and removes
only those fixtures. It preserves an unpublished edit through table loss/reindex
and proves tenant isolation using a non-superuser role.

Handler tests exercise offline status, migration preview, the explicit apply
flag and reindex dry-run. Fixture tests do not prove that a live integration is
authorized or that production pages have been seeded.
