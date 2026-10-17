---
name: database
description: PostgreSQL schema patterns, transactor, migrations, and DB-level design decisions for workflow-definition-service
metadata:
  type: reference
---

# Database Reference

## Transactor Pattern

Repo adapters participate in multi-step transactions via the `Transactor` port:

```go
// Service layer:
err := s.transactor.RunInTx(ctx, func(ctx context.Context) error {
    if err := s.versionRepo.Publish(ctx, ...); err != nil { return err }
    return s.workflowRepo.UpdateActiveVersion(ctx, ...)
})
```

`Transactor.RunInTx` stores the `pgx.Tx` in context (private key). Repo adapters call `exec(ctx, pool, fn)` which checks context for a transaction first, falling back to `pool.WithConn` for non-transactional reads. This keeps pgx types entirely within `adapter/outbound/postgres/`.

`port.Transactor` also exposes `RunInTxWithRetry` (SERIALIZABLE isolation + 40001/40P01 retry via pgcommon) — used for quota enforcement (see below).

## sqlc + `:execresult` Pattern

All DB queries are typed and generated (`make generate`). Status-guarded mutations (e.g. Publish requires DRAFT) filter on `status` in SQL; on `RowsAffected() == 0` they call `statusOrNotFound` which does a secondary `GetWorkflowVersionByID`:

- Absent → `ErrNotFound`
- Present (wrong status) → `ErrVersionNotDraft` / `ErrVersionNotPublished` / etc.

Happy path is always single-query. **Do not collapse status sentinels into `ErrNotFound`.**

## Schema Decisions

### Single draft per workflow
Enforced by a partial unique index:
```sql
idx_wv_single_draft ON workflow_version(workflow_id) WHERE status = 'DRAFT'
```
`ErrDraftAlreadyExists` is returned on violation.

### `is_valid` semantics
`workflow_version.is_valid` reflects **assignee validity**, not BPMN structural validity. Set to `false` by `HandleDepartmentMembershipRevoked` when a default assignee's department membership is revoked. The internal compiled-plan read returns `is_valid` so Execution can reject `StartWorkflow` calls on invalid templates.

### `record_version` optimistic lock
`workflow` and `workflow_version` carry `record_version BIGINT NOT NULL DEFAULT 1`, bumped by the `update_meta_columns` BEFORE UPDATE trigger (guarded by `WHEN (OLD.* IS DISTINCT FROM NEW.*)`).

`UpdateDraftVersion` adds `AND record_version = $n`; on `RowsAffected() == 0` the repo calls `statusOrNotFoundOrConcurrency`:
- Absent → `ErrNotFound`
- Wrong status → `ErrVersionNotDraft`
- Present DRAFT → `ErrDraftConcurrency`

`UpdateDraftReq.RecordVersion` (0 = unchecked) is the client token.

### Plan quota + SERIALIZABLE TOCTOU guard
`WorkflowService.Create` and `VersionService.Clone` run the quota `CountByTenant` **inside** `RunInTxWithRetry` (SERIALIZABLE, retries on 40001/40P01) so two concurrent creates cannot both pass. The count covers workflows with a `DRAFT` or `PUBLISHED` version, so archiving frees a slot. The limit comes from the plan table (`PlanLimitService`, read from catalog-admin and cached per replica) by the `x-plan` code, resolved before the transaction opens; a null limit skips the count. A missing or unknown code, or no table ever read, is `ErrUpstreamUnavailable` → 503, never a default tier. Returns `ErrPlanQuotaExceeded` → 403 `PLAN_QUOTA_EXCEEDED`.

## Migrations

Migrations run via the `migrate` subcommand (`go run ./cmd/server migrate`, `cmd/server/migrate.go`), intended as a Kubernetes init container / pre-boot job. The server does **NOT** run migrations at startup.

`runMigrations` runs the service runner (`wf_definition_migrations`). `test/fixtures/testcontainer.go` applies the same step.

PL/pgSQL functions need no `StatementBegin/End` wrappers — pgx/v5 runs each file as one statement.

**Roles:** `DATABASE_URL` names the application role (`NOSUPERUSER NOBYPASSRLS`); `MIGRATION_DATABASE_URL` and `SYSTEM_DATABASE_URL` name the schema owner, which is the `BYPASSRLS` system role. Startup reads both pools' roles and refuses a bypassing application role. `scripts/init-db.sql` creates them locally; `make docker-reset` re-runs it.

**PgBouncer note:** When `DATABASE_URL` points to PgBouncer, set `MIGRATION_DATABASE_URL` to a direct Postgres DSN — golang-migrate uses session advisory locks that PgBouncer transaction pooling drops.

## Inbound Event Redelivery

There is no dedup registry. Both consumed types converge on the same state when reprocessed: `SetVersionInvalid` rewrites an identical `is_valid = false`, and no query returns a PUBLISHED version to valid, while the Execution Service's pause-tasks call signals only `RUNNING` instances. Adding an inbound type that is not idempotent by construction means introducing the registry with it (LLD §2.3.1, §10.9).
