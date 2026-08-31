# Agile Artifact Schema Changelog

## 2026-06-24 — epic/api-seed Phase 0

### Breaking Changes
- Added required `slug` field (natural key, pattern: `^[a-z0-9]+(-[a-z0-9]+)*$`) to all entity schemas
- Added `status` field to schemas that lacked it

### Additions
- R-11 slug validation pattern (`^[a-z0-9]+(-[a-z0-9]+)*$`) on every entity schema's `slug` field
- Storage-semantic annotations: `table_kind`, `table_name`, `primary_key` (UUID), `natural_key` (slug)
- FK metadata: `fk_ref`, `fk_column`, `parent_fk` (CASCADE for ownership, RESTRICT for reference)
- Index declarations: status fields, all FK columns
- Audit timestamps: `created_at`, `updated_at` (required, auto-populated)
- JSONB column types on complex list/dict fields

### Version Summary

| Schema | Before | After |
|--------|--------|-------|
| product_vision | 1.1.0 | 1.3.2 |
| market_analysis | 1.1.0 | 1.3.2 |
| prd | 1.1.0 | 1.3.2 |
| sad | 1.1.0 | 1.3.2 |
| nfr | 1.1.0 | 1.3.2 |
| ddd | 1.1.0 | 1.3.2 |
| api_spec | 1.1.0 | 1.3.2 |
| goal | 1.0.0 | 1.2.2 |
| project | 1.0.0 | 1.2.2 |
| epic | 1.2.0 | 1.4.2 |
| sprint | 1.1.0 | 1.3.2 |
| user_story | 1.1.0 | 1.3.2 |
| task | 1.1.1 | 1.3.2 |
| implementation_plan | 1.1.0 | 1.3.2 |
| qa_handoff | 1.0.0 | 1.2.2 |

### Migration
No data migration required — Phase 0 adds metadata only. Codegen (Phase 1) will produce DDL from these annotations. Existing YAML instances at `~/.lightwave/specs/` are prints, not database rows.
