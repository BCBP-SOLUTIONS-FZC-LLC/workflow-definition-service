---
name: database
description: PostgreSQL schema patterns, transactor/outbox, migrations, and DB-level design decisions for workflow-definition-service
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
    return s.outboxRepo.Enqueue(ctx, env)
})
```

`Transactor.RunInTx` stores the `pgx.Tx` in context (private key). Repo adapters call `exec(ctx, pool, fn)` which checks context for a transaction first, falling back to `pool.WithConn` for non-transactional reads. This keeps pgx types entirely within `adapter/outbound/postgres/`.

`port.Transactor` also exposes `RunInTxWithRetry` (SERIALIZABLE isolation + 40001/40P01 retry via pgcommon) — used for quota enforcement (see below).

## Outbox Pattern

**Critical:** `OutboxRepository.Enqueue` MUST be called inside a `Transactor.RunInTx` callback — it returns an error otherwise. The business write and the outbox insert commit or roll back atomically.

```go
// Correct — both in the same transaction:
s.transactor.RunInTx(ctx, func(ctx context.Context) error {
    s.versionRepo.Publish(ctx, ...)   // writes workflow_version
    s.outboxRepo.Enqueue(ctx, env)    // writes outbox_events
})
// Never call Enqueue outside a transaction.
```

The `platform-events outbox.Runner` (started in `app.go`) handles the relay loop — polling `outbox_events`, publishing to SNS, and marking delivered. The service never calls `FetchPending` / `MarkSent` / `MarkFailed` directly.

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
`workflow_version.is_valid` reflects **assignee validity**, not BPMN structural validity. Set to `false` by `HandleMembershipRevoked` when a default assignee's department membership is revoked. `GetCompiledWorkflow` gRPC returns `is_valid` so Execution can reject `StartWorkflow` calls on invalid templates.

### `record_version` optimistic lock
`workflow` and `workflow_version` carry `record_version BIGINT NOT NULL DEFAULT 1`, bumped by the `update_meta_columns` BEFORE UPDATE trigger (guarded by `WHEN (OLD.* IS DISTINCT FROM NEW.*)`).

`UpdateDraftVersion` adds `AND record_version = $n`; on `RowsAffected() == 0` the repo calls `statusOrNotFoundOrConcurrency`:
- Absent → `ErrNotFound`
- Wrong status → `ErrVersionNotDraft`
- Present DRAFT → `ErrDraftConcurrency`

`UpdateDraftReq.RecordVersion` (0 = unchecked) is the client token.

### Plan quota + SERIALIZABLE TOCTOU guard
`WorkflowService.Create` and `VersionService.Clone` run the quota `CountByTenant` **inside** `RunInTxWithRetry` (SERIALIZABLE, retries on 40001/40P01) so two concurrent creates cannot both pass. Enterprise tier skips the count. Returns `ErrPlanQuotaExceeded` → 403 `PLAN_QUOTA_EXCEEDED`.

## Migrations

Migrations run via the `migrate` subcommand (`go run ./cmd/server migrate`, `cmd/server/migrate.go`), intended as a Kubernetes init container / pre-boot job. The server does **NOT** run migrations at startup.

`runMigrations` calls `outbox.ApplySchema` first (under `pgcommon_migrations`), then the service runner (`wf_definition_migrations`). `test/fixtures/testcontainer.go` applies the same two steps.

PL/pgSQL functions need no `StatementBegin/End` wrappers — pgx/v5 runs each file as one statement.

**PgBouncer note:** When `DATABASE_URL` points to PgBouncer, set `MIGRATION_DATABASE_URL` to a direct Postgres DSN — golang-migrate uses session advisory locks that PgBouncer transaction pooling drops.

## Inbound Event Deduplication

`HandleMembershipRevoked` calls `processedEvents.RecordIfNew(ctx, eventID, "membership-wf-q", "DepartmentMembershipRevoked")` before any processing, keyed on the forwarded envelope `id`.

`processed_event` has composite PK `(event_id, consumer)`, no `tenant_id`, and **no RLS** — it is an operational dedup log, not tenant data.

## Operational Table Cleanup

The service does NOT run an in-process pruner. `processed_event`, `outbox_events`, and `outbox_dead_letters` are cleaned by an external periodic job (lambda / cron) running under `BYPASSRLS=true` per LLD §7.1.2. Until that job ships, these tables grow unbounded (GAP-11).
