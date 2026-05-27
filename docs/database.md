# Database

## Schema

Database: PostgreSQL 16, schema `workflow_definition`.

Multi-tenancy is enforced via **Row-Level Security (RLS)**. Every query is automatically filtered by `app.tenant_id` GUC set per-connection by the postgres adapter. Cross-tenant data leaks are impossible at the database level.

### Tables

| Table | Purpose |
| --- | --- |
| `workflow` | Root template entity — business key, name, active version pointer |
| `workflow_version` | Versioned snapshot — BPMN XML, compiled DSL, status lifecycle |
| `workflow_node_assignee` | Denormalised reverse index: user → versions that reference them as default assignees |
| `outbox` | Transactional event queue for SNS delivery (PENDING → SENT / FAILED) |
| `processed_event` | SQS consumer idempotency registry (deduplication by event UUID) |

See full DDL in [`db/migrations/`](../db/migrations/).

## Migrations

Managed by [Goose](https://github.com/pressly/goose). Migration files live in `db/migrations/` and follow the naming convention `000xx_description.sql`.

```bash
# Apply all pending
make migrate-up

# Roll back one
make migrate-down

# Check status
.tools/goose -dir db/migrations postgres "$DATABASE_URL" status
```

## SQL queries

Query definitions in `db/queries/` are compiled to type-safe Go by [sqlc](https://sqlc.dev). Generated output goes to `internal/adapter/outbound/postgres/db/` (gitignored).

```bash
make generate   # regenerates sqlc output after editing .sql files
```

## Key constraints

| Table | Constraint | Enforces |
|---|---|---|
| `workflow` | `UNIQUE (tenant_id, business_key)` | No duplicate keys per tenant |
| `workflow_version` | `idx_wv_single_draft` (partial unique) | At most one DRAFT per workflow |
| `workflow_version` | `uq_workflow_version_published` | Unique version numbers per workflow |
| `workflow_version` | `chk_version_number_on_publish` | Published versions must have a version number |
