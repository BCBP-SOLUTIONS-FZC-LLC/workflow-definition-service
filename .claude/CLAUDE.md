# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## What This Repo Is

`workflow-definition-service` is a Go HTTP + gRPC microservice (module: `github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service`, Go 1.26) that acts as the design-time control plane for the BPMN-driven Workflow Engine. It parses, validates, versions, and serves workflow templates.

Key responsibilities: BPMN ingestion, structural/semantic/topological validation, DSL compilation, DRAFT → PUBLISHED → ARCHIVED versioning, serving compiled plans to the Execution Service over gRPC, and emitting domain events via a transactional outbox (SNS).

---

## Common Commands

```bash
# First-time setup
make tools                  # install sqlc, goose, buf, mockgen, golangci-lint
cp .env.example .env
make docker-up              # start PostgreSQL + Valkey
make migrate-up

# Development cycle
make generate               # buf (proto) + sqlc (queries)
make mock                   # regenerate GoMock stubs
make build                  # compile bin/server
go run ./cmd/server         # run locally

# Testing
make tools-integration      # docker pull postgres:16-alpine (one-time)
make test                   # unit tests + race detector (./internal/... ./test/unit/...)
make test-integration       # integration tests (testcontainers; no make docker-up needed)
make cover                  # unit coverage with -coverpkg ./internal/...
make cover-html             # open HTML report

# Code quality
make lint                   # golangci-lint
make lint-fix               # with auto-fix
```

---

## Architecture

Clean Architecture - dependency direction: `domain ← port ← service ← adapter`.

```sh
cmd/server/             ← bootstrap + DI (no business logic)
internal/
  core/
    domain/             ← entities, sentinel errors, BPMN error codes
    port/               ← interface contracts (Transactor, OutboxRepository, etc.)
    service/            ← business logic (imports domain + port only)
  adapter/
    inbound/
      http/             ← Gin handlers, authz middleware
      grpc/             ← GetCompiledWorkflow impl
    outbound/
      postgres/         ← sqlc + pgcommon repo adapters
      valkey/           ← CacheStore impl
  bpmn_compiler/        ← stateless parser, validator, DSL compiler
  config/               ← env var loading
test/
  fixtures/             ← shared testcontainers helper (NewTestPool)
  unit/                 ← pure unit tests by package
  integration/          ← DB integration tests (testcontainers-go)
```

**Import rules:** `core/domain` → stdlib only. `core/port` → domain only. `core/service` → domain + port. `adapter/*` → port + domain. Nothing in `core/` imports from `adapter/`.

---

## Transactor Pattern

Repo adapters participate in multi-step transactions via the `Transactor` port:

```go
// Service layer:
err := s.transactor.RunInTx(ctx, func(ctx context.Context) error {
    if err := s.versionRepo.Publish(ctx, ...); err != nil { return err }
    return s.outboxRepo.Enqueue(ctx, env)
})
```

`Transactor.RunInTx` stores the `pgx.Tx` in context (private key). Repo adapters call `exec(ctx, pool, fn)` which checks context for a transaction first, falling back to `pool.WithConn` for non-transactional reads. This keeps pgx types entirely within the `adapter/outbound/postgres/` package.

---

## Outbox Pattern

**Critical:** `OutboxRepository.Enqueue` MUST be called inside a `Transactor.RunInTx` callback - it will return an error otherwise. The business write and the outbox insert commit or roll back atomically.

```go
// Correct - both in the same transaction:
s.transactor.RunInTx(ctx, func(ctx context.Context) error {
    s.versionRepo.Publish(ctx, ...)   // writes workflow_version
    s.outboxRepo.Enqueue(ctx, env)    // writes outbox_events
})
// Never call Enqueue outside a transaction.
```

The `platform-events outbox.Runner` (started in `app.go`) handles the relay loop - polling `outbox_events`, publishing to SNS, and marking delivered. The service never calls `FetchPending`/`MarkSent`/`MarkFailed`.

---

## Code Generation

```bash
make generate        # must re-run after editing: proto/*.proto or db/queries/*.sql
make mock            # must re-run after changing core/port/ interfaces
```

Generated files are gitignored: `gen/`, `internal/adapter/outbound/postgres/db/`, `internal/core/port/mocks/`.

The CI `generate` job produces these and uploads them as an artifact consumed by all downstream jobs.

---

## Test Layout

| Directory | Content | Command |
| ----------- | --------- | --------- |
| `internal/**/*_test.go` | White-box unit tests (e.g., mapping_test.go needs unexported access) | `make test` |
| `test/unit/<pkg>/` | Black-box unit tests (exported API only) | `make test` |
| `test/integration/postgres/` | DB integration tests (testcontainers, real Postgres) | `make test-integration` |
| `test/fixtures/` | Shared `NewTestPool` helper (not test files) | - |

Integration tests require Docker. Pre-pull the image once: `make tools-integration`.

---

## Key Design Decisions

1. **Platform libs delegation** - No custom SNS/SQS/pool logic. Uses `platform-events` (outbox, SNS, SQS), `platform-pgcommon` (pool, RLS GUC, transactions), `platform-gincommon` (middleware, OTel, logger).

2. **sqlc for queries** - All DB queries are typed and generated. Status-guarded mutations use `:execresult` to check `RowsAffected()`. The only raw pgx query is `ListWorkflows` which builds a dynamic WHERE clause for optional filters.

3. **Goose for migrations** - Not golang-migrate. Migrations in `db/migrations/` use `-- +goose Up/Down` annotations. The test helper in `test/fixtures/testcontainer.go` applies goose migrations to the test container.

4. **Single draft per workflow** - Enforced by a partial unique index `idx_wv_single_draft ON workflow_version(workflow_id) WHERE status = 'DRAFT'`. The `ErrDraftAlreadyExists` sentinel is returned on violation.

5. **`is_valid` semantics** - `workflow_version.is_valid` reflects assignee validity (not BPMN structural validity). It is set to `false` by the `DepartmentMembershipRevoked` SQS consumer when an assignee leaves a required department role.

---

## CI/CD

| Workflow | Trigger | Jobs |
| ---------- | -------- | ------ |
| `ci.yml` | push/PR to main | generate → (Build, vet, Test, coverage, Iint, lint) parallel |
| `validate.yml` | reusable | fmt, tidy, vet, lint, govulncheck, unit tests |
| `release.yml` | push `v*` tags | generate → validate → Build → gh release |

Branch protection required checks: `generate`, `Build`, `vet`, `Test`, `coverage`, `Iint`, `lint`.

The `Iint` job runs real integration tests (testcontainers) in CI - Docker is available on `ubuntu-latest`.
