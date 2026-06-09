# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## What This Repo Is

`workflow-definition-service` is a Go HTTP + gRPC microservice (module: `github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service`, Go 1.26) that acts as the design-time control plane for the BPMN-driven Workflow Engine. It parses, validates, versions, and serves workflow templates.

Key responsibilities: BPMN ingestion, structural/semantic/topological validation, DSL compilation, DRAFT → PUBLISHED → ARCHIVED versioning, serving compiled plans to the Execution Service over gRPC, and emitting domain events via a transactional outbox (SNS).

---

## Platform Libraries

The three BCBP platform libraries are private Go modules hosted on GitHub. They are fetched at build time via `GOPRIVATE` — **never** reference or import them from local directories.

| Module | Version | Purpose |
| --- | --- | --- |
| `github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events` | v1.0.0 | SNS/SQS publisher, transactional outbox runner, processed-event dedup |
| `github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon` | v1.1.0 | pgx pool, RLS GUC injection, transactor, migration runner |
| `github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon` | v1.0.0 | Gin middleware, OTel tracing init, Zap logger, gRPC middleware |

**How to fetch / upgrade:**

```bash
export GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*   # must be set in every shell
go get github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon@v1.2.0
go mod tidy
go mod vendor
```

**Critical rules — never break these:**

- **Never add a `replace` directive** pointing to `./platform-libs/` in `go.mod`. That directory is gitignored and does not exist in CI. Committing a local replace causes go.sum hash mismatches for every downstream dependency.
- **Never import from `./platform-libs/`** in source code. The import path must always be the full `github.com/BCBP-SOLUTIONS-FZC-LLC/...` module path.
- **`platform-libs/`** is a local read-only reference copy only — useful for reading source while offline, nothing more.
- **`.design/`** is the same: design documents (HLD, LLD, DBML) for reference. Neither folder should influence imports, go.mod, or go.sum.

---

## Common Commands

```bash
# First-time setup
export GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*  # required before go get / mod tidy
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
go test ./test/unit/handler/... -run TestDraftHandler_InitDraft  # run a single test
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

`cmd/server/app.go` starts four concurrent long-running goroutines: HTTP (`:8080`), gRPC (`:9090` via `GRPC_PORT`), SQS consumer, and outbox relay. All stop gracefully on OS signal with a 30-second timeout.

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

6. **`statusOrNotFound` probe** - Status-guarded mutations (e.g. `Publish` requires DRAFT) filter on `status` in SQL (`WHERE ... AND status = 'DRAFT'`). On `RowsAffected() == 0` — which means either the record doesn't exist OR it exists in the wrong status — they call `statusOrNotFound(ctx, dbtx, tenantID, versionID, wrongStatusErr)`, which does a secondary `GetWorkflowVersionByID`: absent → `ErrNotFound`; present (wrong status) → `wrongStatusErr` (e.g. `ErrVersionNotDraft`). Happy path is always single-query.

7. **Fine-grained version-status sentinels** - `internal/core/domain/errors.go` defines `ErrVersionNotDraft`, `ErrVersionNotPublished`, and `ErrVersionAlreadyPublished`. These are sub-layer sentinels returned by repo adapters; the service layer translates them to HTTP error catalog codes. Do not collapse them into `ErrNotFound`.

8. **Handler response DTOs** - Handlers define local response structs (`workflowResp`, `versionResp`, `draftResp`, etc.) with snake_case JSON tags in each handler file. Domain structs are not serialised directly — always map through the DTO helpers (`toWorkflowResp`, `toVersionResp`, `toDraftResp`, `toVersionSummary`).

9. **Handler RequestContext in tests** - `gincommon.RequestContext(c)` does a type assertion to an internal gincommon domain type. Tests cannot construct that type directly. Handler tests must use `gincommon.ProtectedMiddlewares` with injected `x-tenant-id` / `x-user-id` headers, or create a test router that runs the real context middleware. See `test/unit/handler/` for the established pattern.

10. **`mustCtx` writes 500, not 401** - Missing RequestContext means `ProtectedMiddlewares` was not applied to the route (a server misconfiguration), not an auth failure from the caller. A 401 would be misleading here.

11. **Handler service interfaces** - `Handler` depends on unexported interfaces (`workflowSvc`, `draftSvc`, `versionSvc`, `validationSvc`) defined in `handler.go`. `handler.Services` accepts any concrete type satisfying those interfaces (Go structural typing). Production wire-up passes `*service.*Service` directly; tests provide hand-rolled fakes from `test/unit/handler/testhelper_test.go`. This avoids mockgen for the handler layer.

12. **`CodeBadRequest` for bind errors** - JSON bind failures (missing required fields, wrong type) return HTTP 400 with `code: "BAD_REQUEST"`. Do not use `CodeInternal` for client-induced bind errors. The `problemTypes` map includes `http.StatusBadRequest` → `errBase + "bad-request"`.

13. **Fine-grained version-status sentinels in `errResponse`** - `ErrVersionNotDraft`, `ErrVersionNotPublished`, and `ErrVersionAlreadyPublished` are all mapped to 409/`CodeInvalidStatus`. They can bubble up directly from the service layer (e.g. `Promote` returns `ErrVersionNotPublished` when the version is not yet published).

14. **gRPC `GetCompiledWorkflow` injects RLS GUC manually** - The Execution Service passes `tenant_id` in the request payload (not gRPC metadata). The handler must call `pgcommon.WithGUCSet(ctx, pgdomain.GUCSet{TenantID: req.TenantId})` before any repo call. This is distinct from the SQS path where `platform-events` auto-injects the GUC from the envelope's `tenant_id` field — no manual injection needed in SQS handlers.

15. **SQS adapter lives in `internal/adapter/inbound/sqs/`** - The `DepartmentMembershipRevoked` dispatcher is at `internal/adapter/inbound/sqs/handler.go` (not in `cmd/server/`). `cmd/server/sqs.go` is a one-liner that wires the adapter. This keeps the dispatch logic testable (see `test/unit/sqshandler/`) and consistent with the gRPC adapter location. The adapter defines a local `membershipRevoker` interface to avoid depending on the concrete `*service.VersionService`.

16. **`TemplateEligibilityInvalidatedPayload` schema** - Matches LLD §7.2.3 exactly: `workflow_id`, `version_id`, `version_number` (int32, 0 for DRAFT), `revoked_user_id`, `affected_nodes` ([]string of node keys), `reason` (constant `"DepartmentMembershipRevoked"`). The old fields `affected_user_id` and `affected_department` were removed — they diverged from the LLD and are not present in the canonical SNS event consumed by downstream services.

17. **SQS handler tests use hand-rolled fakes** - `test/unit/sqshandler/handler_test.go` defines `fakeMembershipRevoker` (satisfies the `membershipRevoker` interface) and `fakeLogger`. This is consistent with the handler layer pattern — no mockgen for adapter-layer dispatch code. Service tests (`test/unit/service/membership_event_test.go`) use gomock since they test multi-dependency orchestration.

18. **`invalidateVersion` silently skips on GetByID error** - A version being absent (concurrently deleted or archived out of band) during membership revocation is not a failure. The handler logs the error and returns nil rather than aborting the entire event. This ensures one missing version does not block invalidation of other affected versions in the same event.

19. **Unit test coverage excludes postgres adapter and generated packages** - `internal/adapter/outbound/postgres/` (repo adapters that require a real DB), `internal/adapter/outbound/postgres/db/` (generated sqlc), and `internal/core/port/mocks/` (generated GoMock stubs) are excluded from the unit test coverage denominator. These are covered by the integration test job (`Iint`). The unit test gate is 95% against the remaining business logic. See `COVER_EXCLUDE_PKG` and `COVER_EXCLUDE_FILE` in the Makefile.

20. **HTTP idempotency middleware** — `handler.WithIdempotency(cache, fn)` in `internal/adapter/inbound/http/handler/idempotency.go`. Applied to all mutation endpoints at the router level via an `idem(fn)` wrapper in `cmd/server/router.go`. Cache key is `"idem:" + tenantID + ":" + Idempotency-Key` header value — tenant-scoped to prevent collisions. Only 2xx responses are cached (24 h TTL). On cache miss the response AND a SHA-256 hash of the raw request body are stored together. On replay (cache hit): if the stored hash matches the incoming body hash, the cached response is replayed; if hashes differ, the middleware returns `IDEMPOTENCY_KEY_REPLAY` (409) before calling the handler. Passes through transparently if the header is absent or cache is nil.

21. **SQS event deduplication** — `HandleMembershipRevoked` calls `processedEvents.RecordIfNew(ctx, eventID, tenantID, "iam-membership")` before any processing. This guards against at-least-once SQS redelivery. `AWS_USE_STUB=true` (the default in `.env.example`) wires no-op AWS implementations for local development; set `AWS_USE_STUB=false` with `AWS_ENDPOINT_URL` pointing at LocalStack for integration testing, or leave unset for real AWS.

22. **Outbound client tests** - `test/unit/executionclient/` tests `ExecutionClient` using a real local gRPC server (`net.Listen("tcp", "127.0.0.1:0")`) — bufconn is not vendored. `test/unit/membershipclient/` tests `MembershipClient` using `httptest.NewServer`. Both follow the hand-rolled fake pattern consistent with other test/unit packages.

23. **`InjectGUCSet` middleware bridges gincommon → pgcommon for HTTP RLS** — `gincommon.ContextMiddleware` stores `RequestContext` in the Gin context (`c.Set`), not in `c.Request.Context()`. `pgcommon.GUCSetFromContext` reads only the Go request context, so without this bridge `app.tenant_id` is never set and RLS filtering is broken on the HTTP path. The middleware (in `internal/adapter/inbound/http/middleware/gucrls.go`) reads `gincommon.RequestContext(c)` and calls `pgcommon.WithGUCSet(ctx, ...)` then replaces `c.Request` with the enriched context. It must be placed immediately after `gincommon.ProtectedMiddlewares` in the router. The gRPC path is unaffected — `grpc/server.go` calls `pgcommon.WithGUCSet` directly.

24. **`LimitRequestBody` middleware enforces 5 MB JSON body cap** — `internal/adapter/inbound/http/middleware/maxbody.go` wraps `c.Request.Body` with `http.MaxBytesReader(c.Writer, c.Request.Body, 5<<20)`. When any handler then reads the body (e.g. `ShouldBindJSON`), the reader returns `*http.MaxBytesError` if the limit is exceeded. `errResponse` maps `*http.MaxBytesError` → 413 `PAYLOAD_TOO_LARGE`. Applied to the authenticated API group after `InjectGUCSet`.

25. **`WorkflowService.Create` enforces plan quota** — The `Create` method signature includes `planTier string` (8th parameter), sourced from the `x-plan` gateway header. After BPMN validation and before any DB write, `enforceWorkflowQuota(ctx, tenantID, planTier)` calls `CountByTenant` and compares against the hardcoded tier limit (Starter=5, Pro=50, Enterprise=unlimited). Returns `ErrPlanQuotaExceeded` → 403 `PLAN_QUOTA_EXCEEDED` if the limit is reached. Enterprise (`planTier="enterprise"`) skips the count query entirely.

---

## CI/CD

| Workflow | Trigger | Jobs |
| ---------- | -------- | ------ |
| `ci.yml` | push/PR to main | generate → (Build, vet, Test, coverage, Iint, lint) parallel |
| `validate.yml` | reusable | fmt, tidy, vet, lint, govulncheck, unit tests |
| `release.yml` | push `v*` tags | generate → validate → Build → gh release |

Branch protection required checks: `generate`, `Build`, `vet`, `Test`, `coverage`, `Iint`, `lint`.

The `Iint` job runs real integration tests (testcontainers) in CI - Docker is available on `ubuntu-latest`.

The `coverage` job enforces a unit-test-only threshold (95% as of `feat/grpc-sqs`). Generated packages (`postgres/db`, `mocks`) and the postgres repo adapter (`postgres/`) are excluded from the gate denominator — they are integration-tested by the `Iint` job. The integration coverage profile is uploaded as a separate artifact but is not merged into the gate.

---

## Documentation (MkDocs)

Docs live in `docs/` and are served via MkDocs (`make docs-serve`). The nav is declared in `mkdocs.yml`.

**Keep docs in sync when you make changes:**

| Change type | Docs to update |
| --- | --- |
| New HTTP endpoint or field | `docs/api.md` |
| New gRPC method | `docs/grpc.md` |
| New config env var | `docs/configuration.md` |
| New migration or schema change | `docs/database.md` |
| Architecture / layer change | `docs/architecture.md` |
| Platform lib upgrade | `docs/platform-pgcommon.md`, `docs/platform-events.md`, or `docs/platform-gincommon.md` |
| New coding pattern or decision | `docs/standards.md` and a numbered entry in this file |

After any significant feature PR, run `make docs-serve` and verify the affected pages render correctly before merging.
