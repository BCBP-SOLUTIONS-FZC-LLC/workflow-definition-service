# Architecture

## Clean architecture layers

The service enforces strict dependency direction — nothing in `core/` imports from `adapter/`.

```sh
cmd/server/main.go          ← bootstrap + DI wire-up only
│
├── internal/core/
│   ├── domain/             ← entities, enums, sentinel errors, BPMN error codes
│   ├── port/               ← interface contracts (no implementations)
│   └── service/            ← business logic (imports domain + port only)
│
├── internal/adapter/
│   ├── inbound/
│   │   ├── http/           ← Gin handlers, authz, service-specific middleware
│   │   ├── grpc/           ← GetCompiledWorkflow server impl
│   │   └── sqs/            ← membership revocation consumer (platform-events SQS consumer)
│   └── outbound/
│       ├── postgres/       ← sqlc-generated DB layer + repo adapter impls (outbox Enqueue wrapper)
│       ├── sns/            ← SNS publisher utilizing platform-events
│       └── valkey/         ← Valkey/Redis CacheStore impl
│
├── internal/bpmn_compiler/ ← stateless XML parser, validator, DSL compiler
├── internal/outbox/        ← outbox runner wrapper using platform-events runner
└── internal/config/        ← env var loading → typed Config struct
```

Import rules (enforced by `go-arch-lint`):

- `core/domain` → stdlib only
- `core/port` → `core/domain` only
- `core/service` → `core/domain` + `core/port`
- `adapter/*` → `core/port` + `core/domain`
- `bpmn_compiler` → `core/domain` only
- Nothing in `core/` or `bpmn_compiler/` imports from `adapter/`

## HTTP middleware chain

```sh
Incoming request
  → PanicRecovery          (gincommon) — JSON 500 on panic
  → RequestID              (gincommon) — reads/generates x-request-id
  → Tracing                (gincommon) — OTel span from traceparent
  → CorrelationHeaders     (gincommon) — writes X-Trace-ID, X-Request-ID
  → Metrics                (gincommon) — Prometheus http_requests_total etc.
  → Logging                (gincommon) — structured http_request log on close

  → /healthz, /readyz, /metrics        ← stop here (public)

  → RequireAuth            (gincommon) — validates x-user-id, x-tenant-id
  → ContextMiddleware      (gincommon) — builds RequestContext{TenantID, UserID, Roles, TraceID}

  → GET endpoints          ← read-only, no further authz

  → RequirePermission("write","workflow",authz)   ← POST/PUT/DELETE
  → handler
```

### Logger

`platform-gincommon/pkg/logger` exposes `NewLogger(env)` which returns a value that directly satisfies `port.Logger`. `app.go` calls `logger.NewLogger(cfg.AppEnv)` and passes the result into both `gincommon.Config{Logger: log}` and the service/handler constructors — no adapter wrapper is needed.

## gRPC interceptor chain

```sh
Incoming gRPC call
  → grpccommon.DefaultUnaryInterceptors   ← Prometheus grpc_server_* metrics, tracing
  → grpccommon.DefaultStreamInterceptors  ← same for streaming RPCs
  → DefinitionServiceServer.GetCompiledWorkflow
```

## Outbox pattern

Business mutations and their associated domain events are written in the same PostgreSQL transaction. The service enqueues events using `outbox.Enqueue` inside the database transaction. A background `outbox.Runner` worker (from `platform-events`) polls the `outbox_events` table where `published_at IS NULL` and `scheduled_at <= NOW()`, dispatches to SNS, and marks them published.

```sh
Handler
  └─ tx.ExecContext: UPDATE workflow_version SET status='PUBLISHED' ...
  └─ outbox.Enqueue: INSERT INTO outbox_events (id, ..., published_at=NULL) ...
  └─ tx.Commit()

outbox.Runner
  └─ SELECT ... FROM outbox_events WHERE published_at IS NULL AND scheduled_at <= NOW() FOR UPDATE SKIP LOCKED
  └─ SNS.Publish(payload)
  └─ UPDATE outbox_events SET published_at=NOW()
```

## Valkey usage

| Purpose | Key pattern | TTL |
| --- | --- | --- |
| Compiled plan cache | `plan:{tenant_id}:{version_id}` | 5 min |
| Idempotency key | `idempotency:{tenant_id}:{key}` | 24 h |
| Draft edit lock | `draft_lock:{tenant_id}:{workflow_id}` | 30 s (refreshed) |
