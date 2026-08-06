# Architecture

This document describes the internal structure, dependency rules, and runtime data flows of the `workflow-definition-service`.

> **Design intent:** this service is the design-time control plane of the BCBP Workflow Engine. It owns the full lifecycle of a workflow template — from raw BPMN XML through semantic validation, DSL compilation, and version state transitions — and exposes the resulting compiled plan to the Execution Service over gRPC.

---

## System context

Where the service sits in the broader platform:

> Source: [docs/architecture/mermaid/system-context.mmd](docs/architecture/mermaid/system-context.mmd)

```mermaid
flowchart LR
    FE["Frontend\nWorkflow Builder"]
    DEF["workflow-definition-service\n(this repo)\nHTTP :8080 · gRPC :9090"]
    EXEC["workflow-execution-service\ngRPC :9090"]
    DB[("PostgreSQL 18\nworkflow_definition")]
    CACHE[("Valkey 8\nidempotency · cache")]
    SNS["AWS SNS\nwf.template.events"]
    CONSUMER["shared workflow-events\nconsumer (membership-wf-q)"]
    IAM["iam-service\norg membership API"]
    DOWNSTREAM["downstream consumers\n(execution, analytics)"]

    FE -->|"REST"| DEF
    EXEC -->|"GetCompiledWorkflow\ngRPC"| DEF
    DEF -->|"read/write"| DB
    DEF -->|"idempotency · quota cache"| CACHE
    DEF -->|"domain events"| SNS
    SNS --> DOWNSTREAM
    CONSUMER -->|"POST /internal/events\n(DepartmentMembershipRevoked)"| DEF
    DEF -->|"eligibility check"| IAM
```

---

## Component diagrams

### High-level system view

> Source: [docs/architecture/mermaid/system-view-component.mmd](docs/architecture/mermaid/system-view-component.mmd)

```mermaid
---
config:
  layout: elk
---
graph TD
    UI[Frontend Modeler]
    Gateway[Envoy API Gateway]
    ExecSvc[Execution Svc / Workers]

    subgraph Workflow_Definition_Service [Workflow Definition Service]
        API[Gin HTTP API]
        gRPC[gRPC API]
        Parser[BPMN Parser]
        Compiler[DSL Compiler]
        Validator[Validation Engine]
        InternalEvents[POST /internal/events]
        OutboxRelay[Outbox Background Worker]

        API --> Validator
        API --> Parser
        gRPC --> Compiler
        Parser --> Compiler
    end

    subgraph Persistence [RDS PostgreSQL]
        DB[(workflow_definition DB)]
        OutboxTable[(outbox)]
    end

    subgraph External_Integrations [External Services]
        OrgMembership[Org & Membership Service]
        SNS[SNS: wf.template.events]
        EventsConsumer[Shared Workflow-Events Consumer]
        SQS_Membership[SQS: membership-wf-q]
    end

    UI -->|1. Get Eligible Users per Node| OrgMembership
    UI -->|2. Upload BPMN| Gateway
    Gateway -->|Headers: x-tenant-id, etc.| API
    ExecSvc -->|gRPC: GetCompiledWorkflow| gRPC

    Compiler -->|3. Validate Assignee Eligibility| OrgMembership
    Compiler -->|4. Store Draft/Publish| DB
    API -->|5. Insert Outbox Event| OutboxTable
    SQS_Membership -.->|6. Consume Revocation Events| EventsConsumer
    EventsConsumer -->|7. POST /internal/events| InternalEvents
    InternalEvents -->|Invalidate Affected Templates| DB

    OutboxRelay -->|8. Read Pending Events| OutboxTable
    OutboxRelay -->|9. Publish Event| SNS
```

### Component class diagram

> Source: [docs/architecture/mermaid/component-class-diagram.mmd](docs/architecture/mermaid/component-class-diagram.mmd)

```mermaid
---
config:
  layout: elk
---
classDiagram
    direction LR
    class HTTPHandler {
        +CreateWorkflow()
        +GetWorkflowById()
        +PublishDraft()
        +ArchiveWorkflow()
        +CloneWorkflow()
        +ValidateWorkflow()
    }

    class HTTPMiddleware {
        +TenantContext()
        +ValidatePlanQuotas()
    }

    class PublisherService {
        +PublishDraft()
        +CreateDraft()
        +ArchiveWorkflow()
        -validateAndCompile()
        -applyStructuralGuard()
        -validateAssignees()
    }

    class Validator {
        +ValidateBPMN()
        -checkCycles()
        -validateExtensions()
    }

    class DraftCompiler {
        +CompileToDSL()
        -buildGraph()
        -resolveSequenceFlows()
        -processGateways()
    }

    class PlanService {
        <<interface>>
        +EnforcePlanQuotas()
    }

    class MembershipService {
        <<interface>>
        +ValidateAssigneeEligibility()
    }

    class WorkflowRepository {
        <<interface>>
        +CreateWorkflowVersion()
        +GetWorkflowVersion()
        +UpdateActiveVersion()
        +ArchiveVersion()
    }

    class OutboxRepository {
        <<interface>>
        +Enqueue()
    }

    class OutboxRelay {
        +ProcessOutbox()
        -publishToSNS()
    }

    HTTPHandler --> HTTPMiddleware : Uses middleware chain
    HTTPHandler --> PublisherService : Dispatches requests
    HTTPHandler --> Validator : Calls stateless validation

    PublisherService --> Validator : Validates BPMN
    PublisherService --> DraftCompiler : Compiles execution plan
    PublisherService --> PlanService : Verifies template quotas
    PublisherService --> MembershipService : Verifies assignee eligibility
    PublisherService --> WorkflowRepository : Persists workflow and versions
    PublisherService --> OutboxRepository : Transactionally enqueues events

    OutboxRelay --> OutboxRepository : Polls and updates pending events
```

---

## Layer model

The service follows Clean Architecture. Dependencies always point inward; nothing in `core/` or `bpmn_compiler/` imports from `adapter/`.

> Source: [docs/architecture/mermaid/layer-model.mmd](docs/architecture/mermaid/layer-model.mmd)

```mermaid
flowchart TD
    subgraph cmd["cmd/server — bootstrap + DI"]
        main["main · app · wire · router · infra"]
    end

    subgraph adapter["internal/adapter"]
        subgraph inbound["inbound"]
            http["http/handler\nGin handlers + middleware\nidempotency · authz · POST /internal/events"]
            grpc_in["grpc/server\nGetCompiledWorkflow impl"]
        end
        subgraph outbound["outbound"]
            postgres["postgres/\nsqlc repo adapters\nTransactor · RunInTx"]
            valkey["valkey/\nCacheStore impl"]
            grpc_out["grpc/ExecutionClient"]
            http_out["http/MembershipClient"]
        end
    end

    subgraph core["internal/core"]
        service["service/\nWorkflowService · DraftService\nVersionService · ValidationService"]
        port["port/\ninterface contracts\n(Repository, Transactor, CacheStore, …)"]
        domain["domain/\nentities · enums · error sentinels\nBPMN error codes"]
    end

    compiler["internal/bpmn_compiler\nstateless BPMN parser\nvalidator · DSL compiler"]

    cmd --> adapter
    cmd --> core
    adapter --> port
    adapter --> domain
    service --> port
    service --> domain
    service --> compiler
    port --> domain
```

**Import rule (enforced by `go-arch-lint`):**

- `core/domain` → stdlib only
- `core/port` → `core/domain` only
- `core/service` → `core/domain` + `core/port`
- `adapter/*` → `core/port` + `core/domain`
- `bpmn_compiler/bpmncore` → `core/domain` only
- `bpmn_compiler/validator` → `core/domain` + `bpmn_compiler/bpmncore`
- `bpmn_compiler/element` → `core/domain` + `bpmn_compiler/bpmncore` + `bpmn_compiler/validator`
- `bpmn_compiler` (root) → all three sub-packages + `core/domain` + `core/port`
- `cmd/server` → everything above

`internal/bpmn_compiler/` is split into four layers: the root (`bpmn_compiler/`) orchestrates parse → validate → compile → hash; `bpmncore/` provides shared traversal types; `validator/` contains all rule functions; `element/` holds per-node-type compilation handlers.

---

## Package dependency graph

> Source: [docs/architecture/mermaid/package-dependency-graph.mmd](docs/architecture/mermaid/package-dependency-graph.mmd)

```mermaid
flowchart LR
    main(["cmd/server"])
    http_h(["adapter/inbound/http/handler"])
    grpc_s(["adapter/inbound/grpc"])
    pg(["adapter/outbound/postgres"])
    vk(["adapter/outbound/valkey"])
    ec(["adapter/outbound/grpc"])
    mc(["adapter/outbound/http"])
    svc(["core/service"])
    port(["core/port"])
    dom(["core/domain"])
    cmp(["bpmn_compiler"])

    main --> http_h
    main --> grpc_s
    main --> pg
    main --> vk
    main --> ec
    main --> mc
    main --> svc
    http_h --> port
    http_h --> dom
    grpc_s --> port
    grpc_s --> dom
    pg --> port
    pg --> dom
    vk --> port
    svc --> port
    svc --> dom
    svc --> cmp
    port --> dom
```

---

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
  → InjectGUCSet           (service)   — copies RequestContext into pgcommon GUC context key
                                          so pool.GUCProvider can set app.tenant_id on every DB conn
  → LimitRequestBody       (service)   — wraps c.Request.Body with http.MaxBytesReader(10 MB)
  → RequireJSONContentType (service)   — 415 if a request body's Content-Type is not application/json

  → GET endpoints          ← read-only, no further authz

  → RequirePermission("write","workflow",authz)   ← POST/PUT/DELETE
  → handler
```

**Why `InjectGUCSet` is needed:** `gincommon.ContextMiddleware` stores `RequestContext` in the Gin context (`c.Set`) rather than the Go request context (`c.Request.Context()`). `pgcommon.GUCSetFromContext` reads only the Go context, so without this bridge the HTTP path never sets `app.tenant_id` and PostgreSQL RLS filters against a null GUC — a multi-tenant isolation failure. The gRPC path is exempt because `grpc/server.go` calls `pgcommon.WithGUCSet` manually.

## gRPC interceptor chain

```sh
Incoming gRPC call
  → grpccommon.DefaultUnaryInterceptors   ← Prometheus grpc_server_* metrics, tracing
  → grpccommon.DefaultStreamInterceptors  ← same for streaming RPCs
  → DefinitionServiceServer.GetCompiledWorkflow
```

Interceptors are wired via:

```go
grpcServer := grpc.NewServer(
    grpc.ChainUnaryInterceptor(grpccommon.DefaultUnaryInterceptors(cfg)...),
    grpc.ChainStreamInterceptor(grpccommon.DefaultStreamInterceptors(cfg)...),
)
```

---

## Key runtime flows

### 1. HTTP mutation (e.g. PublishVersion)

> Source: [docs/architecture/mermaid/http-mutation-flow.mmd](docs/architecture/mermaid/http-mutation-flow.mmd)

```mermaid
sequenceDiagram
    participant Client
    participant Middleware as Gin Middleware
    participant Handler as http/handler
    participant Service as core/service
    participant Tx as Transactor.RunInTx
    participant Repo as postgres repo
    participant Outbox as OutboxRepo.Enqueue

    Note over Middleware: OTel · authz · idempotency-key
    Client ->>+ Middleware: POST /versions/:id/publish
    Middleware ->> Middleware: extract x-tenant-id · x-user-id → RequestContext
    Middleware ->> Middleware: Idempotency-Key check (Valkey cache)
    Middleware ->>+ Handler: handler func
    Handler ->>+ Service: VersionService.Publish(ctx, ...)
    Service ->>+ Tx: RunInTx(ctx, fn)
    Tx ->>+ Repo: VersionRepo.Publish(ctx, ...)  [status-guarded SQL]
    Repo -->>- Tx: ok
    Tx ->>+ Outbox: OutboxRepo.Enqueue(ctx, envelope)
    Outbox -->>- Tx: ok
    Tx -->>- Service: commit
    Service -->>- Handler: PublishedVersion
    Handler -->>- Middleware: 200 JSON
    Middleware ->> Middleware: cache response under Idempotency-Key (24 h TTL, 2xx only)
    Middleware -->>- Client: response
```

### 2. gRPC GetCompiledWorkflow

> Source: [docs/architecture/mermaid/grpc-get-compiled-workflow-flow.mmd](docs/architecture/mermaid/grpc-get-compiled-workflow-flow.mmd)

```mermaid
sequenceDiagram
    participant ExecSvc as Execution Service
    participant GRPCSrv as adapter/inbound/grpc
    participant Pool as pgcommon.Pool
    participant Repo as VersionRepo.GetCompiledPlan

    ExecSvc ->>+ GRPCSrv: GetCompiledWorkflow{tenant_id, workflow_id}
    GRPCSrv ->> GRPCSrv: pgcommon.WithGUCSet(ctx, GUCSet{TenantID: req.TenantId})
    Note right of GRPCSrv: RLS GUC injection — manual on gRPC path
    GRPCSrv ->>+ Pool: WithConn
    Pool ->>+ Repo: SELECT compiled_plan_json WHERE ... AND status='PUBLISHED'
    Repo -->>- Pool: CompiledPlan bytes
    Pool -->>- GRPCSrv: ok
    GRPCSrv -->>- ExecSvc: GetCompiledWorkflowResponse{plan}
```

### 3. DepartmentMembershipRevoked (inbound via POST /internal/events)

> Source: [docs/architecture/mermaid/membership-revoked-flow.mmd](docs/architecture/mermaid/membership-revoked-flow.mmd)

```mermaid
sequenceDiagram
    participant Consumer as shared workflow-events consumer
    participant Handler as http/handler.HandleInternalEvent
    participant Svc as VersionService.HandleMembershipRevoked
    participant Tx as Transactor.RunInTx
    participant Repo as VersionRepo
    participant Outbox as OutboxRepo.Enqueue

    Consumer ->>+ Handler: POST /internal/events (envelope)
    Handler ->> Handler: WithGUCSet(tenant_id) · dispatch by type
    Handler ->> Handler: RecordIfNew(eventID, "membership-wf-q") — dedup guard
    Handler ->>+ Svc: HandleMembershipRevoked(ctx, payload)
    loop for each affected version
        Svc ->>+ Tx: RunInTx(ctx, fn)
        Tx ->> Repo: VersionRepo.GetByID  →  check is_valid
        Tx ->> Repo: VersionRepo.SetInvalid(ctx, versionID, nodes)
        Tx ->> Outbox: OutboxRepo.Enqueue(ctx, TemplateEligibilityInvalidated)
        Tx -->>- Svc: commit
    end
    Svc -->>- Handler: nil
    Handler -->>- Consumer: 200 OK (consumer acks the SQS message)
```

### 4. Archive workflow

> Source: [docs/architecture/mermaid/archive-workflow-flow.mmd](docs/architecture/mermaid/archive-workflow-flow.mmd)

```mermaid
sequenceDiagram
    participant C as HTTP Client
    participant H as Handler
    participant S as WorkflowService
    participant ES as ExecutionService (gRPC)
    participant DB as PostgreSQL
    participant SNS as SNS (via outbox.Runner)

    C->>H: POST /workflows/:id/archive
    H->>S: Archive(ctx, tenantID, userID, workflowID)
    S->>ES: CheckActiveInstances(tenantID, workflowID)
    ES-->>S: hasActive=false
    S->>DB: BEGIN TX
    S->>DB: UPDATE workflow_version SET status=ARCHIVED
    S->>DB: UPDATE workflow SET active_version_id=NULL
    S->>DB: INSERT INTO outbox_events (TemplateArchived payload)
    S->>DB: COMMIT
    S-->>H: ok
    H-->>C: 200 OK

    Note over DB,SNS: async
    DB->>SNS: Publish TemplateArchived event
```

### 5. Outbox relay (high-level)

> Source: [docs/architecture/mermaid/outbox-relay-flow.mmd](docs/architecture/mermaid/outbox-relay-flow.mmd)

The `platform-events outbox.Runner` (started in `app.go`) runs independently:

```mermaid
sequenceDiagram
    participant Relay as outbox.Runner (platform-events)
    participant DB as outbox_events table
    participant SNS as AWS SNS

    loop every PollInterval (default 500 ms)
        Relay ->> DB: SELECT id, payload WHERE status='PENDING' LIMIT BatchSize
        Relay ->> SNS: Publish(envelope)
        Relay ->> DB: UPDATE status='SENT' WHERE id=...
    end
```

The service never calls `FetchPending`, `MarkSent`, or `MarkFailed` — those are internal to the relay runner.

---

## Transactor pattern

Repository adapters participate in multi-step transactions via the `port.Transactor` interface, without leaking pgx types into the port layer:

```go
// Service layer — both writes commit or roll back atomically, no pgx imports needed:
err := s.transactor.RunInTx(ctx, func(ctx context.Context) error {
    if err := s.versionRepo.Publish(ctx, ...); err != nil {
        return err
    }
    return s.outboxRepo.Enqueue(ctx, env)
})
```

`Transactor.RunInTx` begins a `pgx.Tx`, stores it in context under a private key, runs the callback, and commits or rolls back. Every repo adapter calls an internal `exec(ctx, pool, fn)` helper, which checks context for a transaction first and falls back to `pool.WithConn` for non-transactional reads:

```go
// In base.go — adapter layer only
func exec(ctx context.Context, pool *pgcommon.Pool, fn func(db.DBTX) error) error {
    if tx, ok := txFromContext(ctx); ok {
        return fn(tx)
    }
    return pool.WithConn(ctx, func(_ context.Context, conn *pgxpool.Conn) error {
        return fn(conn)
    })
}
```

This means: pgx types never escape `adapter/outbound/postgres/`; the service layer has no pgx imports; and `OutboxRepository.Enqueue` must be called inside `RunInTx` — it returns an error otherwise (enforced explicitly in the implementation).

---

## Outbox pattern

Business mutations and their associated domain events are written in the same PostgreSQL transaction, eliminating the dual-write problem. A background `outbox.Runner` worker (from `platform-events`) polls `outbox_events` where `published_at IS NULL AND scheduled_at <= NOW()`, dispatches to SNS, and marks rows published. The detailed poll cycle, including lease-claiming and dead-lettering:

> Source: [docs/architecture/mermaid/outbox-poll-cycle.mmd](docs/architecture/mermaid/outbox-poll-cycle.mmd)

```mermaid
flowchart TD
    A([Runner.Start]) --> P[pollOnce - immediate first poll]
    P --> C[SELECT FOR UPDATE SKIP LOCKED\nWHERE published_at IS NULL AND scheduled_at <= NOW]
    C -- 0 rows --> B[wait PollInterval]
    B --> C
    C -- rows --> L[UPDATE scheduled_at = NOW + claimLease\nclaim lease to prevent duplicate processing]
    L --> D[for each OutboxRecord]
    D --> E[Unmarshal Payload to Envelope]
    E -- error --> F[MarkFailed: attempts++\nif attempts >= MaxAttempts: dead-letter]
    F --> D
    E -- ok --> G[Publisher.Publish to SNS]
    G -- success --> H[MarkPublished: published_at = NOW]
    H --> D
    G -- error --> I[MarkFailed: attempts++\nrelease lease]
    I --> J{attempts >= MaxAttempts?}
    J -- yes --> K[INSERT outbox_dead_letters\nDELETE outbox_events]
    J -- no --> D
    K --> D
    D -- done --> B
```

Runner wiring (`app.go`):

```go
runner := outbox.NewRunner(outbox.Config{
    Pool:        pool,
    Publisher:   snsPublisher,
    Logger:      log,
    PollInterval: 5 * time.Second,
    BatchSize:   50,
    MaxAttempts: 5,
})
go runner.Start(ctx)
defer runner.Stop()
```

---

## Repository error semantics

### Fine-grained version-status sentinels

`internal/core/domain/errors.go` defines three status-specific sentinels returned by repo adapters when a status-guarded mutation is attempted on a version in the wrong state:

| Sentinel | Returned when |
| --- | --- |
| `ErrVersionNotDraft` | Mutation requires DRAFT; version exists but is not DRAFT |
| `ErrVersionNotPublished` | Mutation requires PUBLISHED; version exists but is not PUBLISHED |
| `ErrVersionAlreadyPublished` | Publish attempted on an already-PUBLISHED version |

Do not collapse these into `ErrNotFound`. The service layer maps each to a distinct HTTP catalog code.

### `statusOrNotFound` probe

Status-guarded SQL mutations (`PublishVersion`, `ArchiveVersion`, `UpdateDraft`, `DeleteDraft`) filter on `status` in their WHERE clause (e.g. `WHERE tenant_id=$1 AND id=$2 AND status='DRAFT'`). On `RowsAffected() == 0` — which means either the record is absent or it exists in the wrong status — Go calls `statusOrNotFound` to do a secondary `GetWorkflowVersionByID` and disambiguate:

```text
RowsAffected() > 0  →  success (single query)
RowsAffected() == 0 →  secondary read: GetWorkflowVersionByID
                         absent  →  ErrNotFound
                         present →  wrongStatusErr (ErrVersionNotDraft etc.)
```

The happy path is always a single query. The probe only fires on the exceptional case (wrong status or missing record), keeping read amplification off the hot path.

---

## Valkey usage

| Purpose | Key pattern | TTL |
| --- | --- | --- |
| Compiled plan cache | `wf:plan:{tenant_id}:{version_id}` | 1 h (configurable: `CACHE_COMPILED_PLAN_TTL`) |
| Idempotency key | `idem:{tenant_id}:{route}:{key}` | 24 h (configurable: `IDEMPOTENCY_TTL`) |
| Draft edit lock | `draft_lock:{tenant_id}:{workflow_id}` | 30 s (refreshed) |

---

## Key invariants

| Invariant | Enforcement |
| --------- | ----------- |
| Single DRAFT per workflow | `UNIQUE INDEX idx_wv_single_draft ON workflow_version(workflow_id) WHERE status='DRAFT'` — DB-enforced; returns `ErrDraftAlreadyExists` on violation |
| Outbox must be inside a transaction | `OutboxRepo.Enqueue` returns an error if no `pgx.Tx` is present in context; the caller (service) is responsible for calling it only inside `Transactor.RunInTx` |
| Status-guarded mutations use `statusOrNotFound` | Publish/Archive SQL filters on `status=X`; on `RowsAffected()==0` a secondary `GetByID` distinguishes absent (→ `ErrNotFound`) from wrong-status (→ `ErrVersionNotDraft` etc.) — happy path is always single-query |
| `is_valid` reflects assignee eligibility only | `workflow_version.is_valid=false` means an assignee left a required department role; it does not reflect BPMN structural validity |
| Idempotency-Key scoped per tenant | Cache key format: `idem:<tenant_id>:<key>` — prevents cross-tenant replay; only 2xx responses are cached (24 h TTL) |
| gRPC path requires manual GUC injection | `WithGUCSet` must be called by the gRPC handler before any repo access; the HTTP path injects via `gincommon.ProtectedMiddlewares` automatically |
| `ProtectedMiddlewares` must wrap all protected routes | Missing middleware causes `RequestContext` lookup to fail → 500 (server misconfiguration, not 401) |

---

## Configuration reference

All configuration is read from environment variables via `internal/config.Config` at startup. The service fails fast if any required variable is missing. Copy `.env.example` to `.env` for local development.

### Application

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `APP_ENV` | No | `dev` | `dev` enables Gin debug mode and human-readable Zap logs; any other value activates release mode + JSON logs |
| `BUILD_VERSION` | No | `dev` | Injected via `-ldflags` at build time; labels Prometheus `build_info` and OTel `service.version` |

### Server

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `HTTP_PORT` | No | `8080` | Gin HTTP listener port |
| `GRPC_PORT` | No | `9090` | gRPC listener port |

### Observability (platform-gincommon)

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `OTEL_SERVICE_NAME` | No | `workflow-definition-svc` | OTel `service.name` attribute |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | No | `localhost:4317` | OTLP/gRPC collector address |
| `OTEL_EXPORTER_OTLP_INSECURE` | No | `true` | Set `false` in production (mutual TLS) |
| `OTEL_TRACES_SAMPLER_RATIO` | No | `1.0` | Head sampling ratio (`0.1` recommended in production) |

### Database

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `DATABASE_URL` | **Yes** | — | pgx DSN, e.g. `postgres://user:pass@host:5432/db?sslmode=disable` |
| `PG_MAX_CONNS` | No | `10` | Max open connections in the pgx pool |
| `PG_MIN_CONNS` | No | `2` | Min idle connections kept alive |
| `PG_SLOW_QUERY_THRESHOLD_MS` | No | `200` | Queries exceeding this duration (ms) are logged as slow |
| `PG_BOUNCER_MODE` | No | `false` | Set `true` when `DATABASE_URL` points to PgBouncer; switches pgx to simple protocol + transaction-local GUC injection |
| `MIGRATION_DATABASE_URL` | No | *(uses `DATABASE_URL`)* | Direct Postgres DSN for the `migrate` subcommand; required when `DATABASE_URL` points to PgBouncer (advisory locks require a direct connection) |
| `DATABASE_FALLBACK_URL` | No | *(empty)* | Startup-only fallback DSN tried once if the primary pool is unreachable (rolling deploys, PgBouncer not ready) |

### Valkey

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `VALKEY_ADDR` | No | `localhost:6379` | go-redis dial address |
| `VALKEY_PASSWORD` | No | *(empty)* | Auth password; leave empty for local dev |
| `VALKEY_DIAL_TIMEOUT` | No | `2s` | Timeout for establishing a new connection to Valkey |
| `VALKEY_READ_TIMEOUT` | No | `1s` | Timeout for socket reads |
| `VALKEY_WRITE_TIMEOUT` | No | `1s` | Timeout for socket writes |
| `CACHE_COMPILED_PLAN_TTL` | No | `1h` | TTL for the gRPC `GetCompiledWorkflow` compiled-plan cache; entries are also deleted on archive / membership invalidation |
| `IDEMPOTENCY_TTL` | No | `24h` | TTL for idempotency keys on HTTP mutations |

### AWS

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `AWS_USE_STUB` | No | `true` | `true` activates no-op stub adapters (no AWS credentials needed) |
| `AWS_REGION` | No | `us-east-1` | AWS region |
| `AWS_ENDPOINT_URL` | No | *(empty)* | Custom endpoint URL (e.g. `http://localhost:4566` for LocalStack) |
| `SNS_TOPIC_ARN` | When `AWS_USE_STUB=false` | — | SNS topic for `wf.template.events` |
| `GLUE_REGISTRY_NAME` | When `AWS_USE_STUB=false` | — | AWS Glue Schema Registry name; resolved by the Glue codec at SNS-publish time (via `events.WithCodec`), not at outbox-enqueue time |
| `GLUE_SCHEMA_CACHE_TTL` | No | `5m` | TTL for the in-process Glue schema cache |

> `GLUE_REGISTRY_ARN` (present in `.env.example`) is **not** read by the running service — there is no corresponding field on `internal/config.Config`. It only scopes IAM policy for the `make schema-register`/`schema-prune` CI tooling. See [Schema Governance](README.md#schema-governance).
>
> The service doesn't consume SQS in-process. Inbound events are delivered by the shared workflow-events consumer over HTTP to `POST /internal/events`.

### Internal endpoint

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `INTERNAL_API_TOKEN` | When `APP_ENV=prod` | *(empty)* | Required as the `x-internal-token` header on `POST /internal/events`. Empty disables the check outside `prod` (local/dev); NetworkPolicy / mesh remains the primary control either way. |

### Outbox relay

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `OUTBOX_POLL_INTERVAL` | No | `500ms` | How often the relay polls for pending events |
| `OUTBOX_BATCH_SIZE` | No | `50` | Max rows fetched per relay cycle (`SKIP LOCKED`) |

### Outbound services

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `ORG_MEMBERSHIP_BASE_URL` | When `APP_ENV != dev` | *(empty)* | Base URL of the Org & Membership Service for assignee eligibility checks |
| `MEMBERSHIP_CLIENT_TIMEOUT` | No | `10s` | HTTP client timeout for the Org & Membership Service |
| `EXECUTION_SERVICE_ADDR` | When `APP_ENV != dev` | *(empty)* | gRPC dial target for `CheckActiveInstances` (archive precondition guard) |
| `EXECUTION_CLIENT_TIMEOUT` | No | `5s` | gRPC dial timeout for the Execution Service client |

---

## Error catalog

Domain sentinels defined in `internal/core/domain/errors.go`, mapped to HTTP responses in `internal/adapter/inbound/http/handler/errors.go`.

| Domain sentinel | HTTP status | API error code | Notes |
| --------------- | ----------- | -------------- | ----- |
| `ErrNotFound` / `pgx.ErrNoRows` | 404 | `NOT_FOUND` | |
| `ErrNoDraftExists` | 404 | `DRAFT_NOT_FOUND` | |
| `ErrUnauthorized` | 401 | `UNAUTHORIZED` | |
| `ErrForbidden` | 403 | `FORBIDDEN` | |
| `ErrPlanQuotaExceeded` | 403 | `PLAN_QUOTA_EXCEEDED` | Billing tier limit on workflow count |
| `ErrNoActiveVersion` | 409 | `NO_ACTIVE_VERSION` | Resource exists but has no PUBLISHED version |
| `ErrDraftAlreadyExists` | 409 | `DRAFT_ALREADY_EXISTS` | Partial-unique-index violation |
| `ErrDuplicateBusinessKey` | 409 | `DUPLICATE_BUSINESS_KEY` | |
| `ErrDraftConcurrency` | 409 | `DRAFT_CONCURRENCY` | Concurrent publish attempted |
| `ErrVersionNotDraft` / `ErrVersionNotPublished` / `ErrVersionAlreadyPublished` / `ErrInvalidVersionStatus` | 409 | `INVALID_VERSION_STATUS` | |
| `ErrActiveInstancesExist` | 409 | `ACTIVE_INSTANCES_EXIST` | Cannot archive a running workflow |
| `ErrStructuralDivergence` | 409 | `STRUCTURAL_DIVERGENCE` | Breaking schema change vs active version; use `force_publish_structural` to override |
| `ErrIdempotencyKeyReplay` | 409 | `IDEMPOTENCY_KEY_REPLAY` | Duplicate Idempotency-Key with different payload |
| `ErrAssigneeIneligible` | 422 | `ASSIGNEE_INELIGIBLE` | Assignee left required department role |
| `*ValidationFailedError` | 422 | `BPMN_VALIDATION_FAILED` | Structural/semantic BPMN errors; `invalid_params` carries per-node details |
| `ErrUpstreamUnavailable` | 503 | `UPSTREAM_UNAVAILABLE` | Downstream dependency (Execution Service, IAM) unreachable |
| any other | 500 | `INTERNAL_ERROR` | |

All error responses follow RFC 9457 (`application/problem+json`):

```json
{
  "type": "https://api.workflow.platform/errors/conflict",
  "title": "Draft Already Exists",
  "status": 409,
  "detail": "draft already exists",
  "instance": "/api/v1/workflows/abc/versions/draft",
  "code": "DRAFT_ALREADY_EXISTS"
}
```

---

## BPMN Compiler

The `internal/bpmn_compiler` package tree is the stateless heart of the design-time control plane. It turns an uploaded BPMN 2.0 XML document into an immutable, execution-ready DSL (`dsl.CompiledPlan`, from the shared `github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models` module) — or a structured list of validation errors. It holds no state and touches no I/O; assignee eligibility and persistence happen in the service layer around it.

This service accepts a restricted subset of BPMN 2.0 XML, validated against this profile before any workflow version can be published. The profile is intentionally narrow: it models human-centred approval workflows across departments and companies, not general-purpose automation.

### Pipeline

```text
ParseBPMN ─▶ validate ─▶ compile ─▶ Hash
   │            │           │          │
 XXE/entity   structural   forward    canonical SHA-256
 guard +      / metadata / graph →    of the normalized
 unmarshal    topological  DepartmentDef + ExecutionStep   process
              + guarded     DSL
              loop checks
```

- **Parse** (`bpmn_compiler/parser.go`) — rejects `DOCTYPE`/entity declarations (XXE & billion-laughs), caps the token stream, requires the `bpmn` and `zeebe` namespaces (`MISSING_NAMESPACE`), unmarshals into the typed model, and scans for unsupported elements (`REJECTED_ELEMENT`).
- **Validate** (`bpmn_compiler/validator/validator.go`) — runs every rule below over the *forward graph* (all edges minus DFS-classified back-edges). Returns `[]BPMNValidationError`; the service maps a non-empty list to **HTTP 422** with `is_valid: false`.
- **Compile** (`bpmn_compiler/bpmncore/compile.go` + `element/`) — walks the forward graph to emit departments, stages, and execution steps (sequential / parallel / exclusive, plus error paths for guarded subprocesses). Each BPMN node type is handled by a dedicated `ElementHandler` in `element/`. Non-executable participants are compiled as `Ignored: true` plans included in `CompiledCollaboration` for routing reference; the Execution Service skips them as entry points.
- **Hash** (`bpmn_compiler/hasher.go`) — canonical SHA-256 of the normalized process (Zeebe extension elements sorted) for the `artifact_hash` / structural-divergence check. `canonicalHash` shallow-copies all 14 slice fields of `BPMNProcess` via `copySlice[T]` before sorting and marshalling to prevent mutating the parsed struct.

All graph traversal is **iterative** (`bpmncore.IterativeDFS` + `DFSVisitor{OnEnter, OnExit}`) — `OnEnter` returns false to prune children; `OnExit` enables onStack tracking for back-edge classification. Used by `ClassifyBackEdges`, `ValidateReachability`, and `ValidateMaxDepth`.

### Namespaces

Two namespaces are **required** on `<bpmn:definitions>` and validated:

```xml
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
  xmlns:di="http://www.omg.org/spec/DD/20100524/DI"
  ...>
```

`bpmn` and `zeebe` missing → `MISSING_NAMESPACE`. Diagram namespaces (`bpmndi`, `dc`, `di`) are expected by modelling tools but not validated by the compiler.

### Process Structure

#### Single-process upload

```xml
<bpmn:process id="Process_1" isExecutable="true" name="Bid-No-Bid Review">
  ...
</bpmn:process>
```

- `name` is required; becomes `CompiledPlan.Name`.
- `isExecutable="true"` required.
- One `<bpmn:process>` per definitions file when no collaboration is present (`MULTIPLE_PROCESSES` otherwise).

#### Multi-participant collaboration

```xml
<bpmn:collaboration id="Collab_1">
  <bpmn:participant id="P_consultant" name="Consultant/Owner"
                    processRef="Process_consultant" />
  <bpmn:participant id="P_contractor" name="Contractor"
                    processRef="Process_contractor" />
  <bpmn:messageFlow id="MF_1" sourceRef="Task_issue_rfq"
                    targetRef="Start_rfq_received" messageRef="Msg_rfq" />
</bpmn:collaboration>
```

**System semantics**: each `isExecutable="true"` participant process compiles to a separate `CompiledPlan`. The compiler wraps them in a `CompiledCollaboration`. Non-executable participants (external parties with no lanes) are parsed for structural integrity but produce no compiled plan. Message flows compile to `MessageDef` entries linking source and target plans by message name; the Execution Service starts each plan and correlates them via the shared message.

**Message Flow Name Resolution.** A `<bpmn:messageFlow>` element is rarely annotated with `name` or `messageRef` in practice — the message reference usually lives on the *connected node* instead. The compiler resolves each flow's display name in this order:

1. The `messageFlow` element's own `name` attribute.
2. The `messageFlow` element's own `messageRef` attribute, resolved against a root-level `<bpmn:message>`.
3. The target node's own `messageRef` — a `sendTask`/`receiveTask`'s `messageRef` attribute, or a boundary event's `messageEventDefinition messageRef`.
4. The same lookup against the source node.

If none of these resolve, `MessageDef.Name` (and `StageDef.BoundaryMessage.MessageName` / `*.MessagePaths[].MessageName` for boundary events) is empty, and `MISSING_MESSAGE_DEFINITION` is emitted as a **warning** — the flow still compiles, but the Execution Service will not be able to correlate that message. Declare a `<bpmn:message>` and reference it via `messageRef` on the flow or the connected node to fix it.

**Ignored pool pattern**: A participant process may carry `<zeebe:property name="ignore" value="true"/>` inside its process-level `<zeebe:properties>`. The compiler skips it entirely — it is not validated, not compiled, and does not appear in the `CompiledCollaboration`'s plans. Use this pattern to model message-flow context for the main pool (e.g. a consultant pool that only sends a trigger message) without bringing that pool's logic into the execution engine.

```xml
<bpmn:process id="Process_consultant" isExecutable="true">
  <bpmn:extensionElements>
    <zeebe:properties>
      <zeebe:property name="ignore" value="true"/>
    </zeebe:properties>
  </bpmn:extensionElements>
  ...
</bpmn:process>
```

**Implicit-start pattern**: A process in a collaboration may have **no explicit start event**. This is valid when the process is started externally (by the execution engine or orchestrator) and its root activity has no incoming sequence flow. The compiler identifies the implicit root via `FindImplicitStart` — the single node with no incoming sequence flow and at least one outgoing sequence flow. This node is exempt from the "no incoming sequence flow" dangling check. A `NO_START_EVENT` error is only raised if no implicit root can be identified.

Typical use: a `callActivity` is the first element in the process; an ignored pool sends a message that arrives at a `messageEventDefinition` boundary event **on** that callActivity, acting as a concurrent notification or interrupt without the main process requiring an explicit start event.

### Module BPMNs (Called Processes)

A called process referenced by `<bpmn:callActivity>` must be provided as a separate BPMN file and uploaded via the `module_bpmn_xmls` field on the draft version. The service merges all module BPMNs in-memory before compile and validate — no extra DB round-trips (via `Bundle()`).

**Rules:**

- The main BPMN must contain `<zeebe:calledElement processId="<id>"/>` on every callActivity.
- Each module BPMN must define the corresponding `<bpmn:process id="<id>"/>` with `isExecutable="true"`.
- Module BPMNs may contain their own lanes/departments; these surface alongside the parent's departments in the compiled plan.
- If the module has **no lanes**, the callActivity must include a `<zeebe:ioMapping>` with a `dept_id` input. This determines which department the module's tasks compile into and allows any department to call the same module.
- If the module has its own lanes, the caller does NOT provide `dept_id` — the module uses its own lane names as department IDs.
- Diagrams in module BPMNs are discarded — only process elements are merged.
- Timer and error boundary events attached to a callActivity are not supported and produce a compile error. Message boundary events ARE supported on a callActivity — they compile to `ExecutionStep.MessagePaths` on the flattened step.
- Nested `<bpmn:subProcess>` inside another subProcess is not supported (`NESTED_SUBPROCESS_NOT_SUPPORTED`).
- Modules are versioned and cloned together with the main BPMN.
- `callActivity` steps are flattened **directly into the parent execution plan** — no `SubWorkflowStep` wrapper is emitted (that wrapper is exclusively for inline `<bpmn:subProcess>` elements). The parent lane of the callActivity node is visual-only and is NOT inherited by the called process.

**Example — calling a no-lane module:**

```xml
<bpmn:callActivity id="CA_PrepResponse" name="Prepare Response">
  <bpmn:extensionElements>
    <zeebe:calledElement processId="Process_PrepareResponse" propagateAllChildVariables="false"/>
    <zeebe:ioMapping>
      <zeebe:input source="engineering" target="dept_id"/>
      <zeebe:input source="=clarificationId" target="clarificationId"/>
    </zeebe:ioMapping>
  </bpmn:extensionElements>
</bpmn:callActivity>
```

`dept_id` is compiler-internal: it assigns the department in the compiled plan but is NOT emitted in the `io_mapping` field of the compiled `ExecutionStep`. Other inputs (like `clarificationId`) ARE emitted and consumed by the Execution Service when creating the child workflow.

**Alternative: `target="Depts"` dict-injection**: When a called process has lanes with generic names (e.g. "Sender", "Receiver") and the same callActivity is used in multiple contexts where the actual departments differ, use `target="Depts"` instead of `dept_id`. The source is a JSON object mapping lane names (case-insensitive) to actual department IDs:

```xml
<zeebe:ioMapping>
  <zeebe:input source='{"sender":"Consultant","receiver":"Tender"}' target="Depts"/>
</zeebe:ioMapping>
```

This also satisfies `MISSING_DEPT_INPUT_FOR_MODULE` for lane-based modules. The `Depts` input is compiler-internal and is stripped from `ExecutionStep.IOMapping` — it is not passed to the Execution Service. A `target="Depts"` source that is not valid JSON / not a `map[string]string` emits `INVALID_ZEEBE_PROPERTY` as a warning; the module falls back to its own lane names as-is.

**isExecutable semantics:**

- `isExecutable="false"` → visual-only pool (e.g. a Client pool showing only message flows); skipped entirely by the compiler — not compiled, not usable as a called process.
- `isExecutable="true"` → required for all pools with real business logic, including module/called processes.

The compiler distinguishes root entry-point processes from called processes by whether the process ID appears as a `zeebe:calledElement` target — NOT by the `isExecutable` flag.

### Lanes and Departments

```xml
<bpmn:laneSet id="LaneSet_1">
  <bpmn:lane id="Lane_tender" name="Tender">
    <bpmn:flowNodeRef>Task_prep_bnb</bpmn:flowNodeRef>
    <bpmn:flowNodeRef>Task_review_bnb</bpmn:flowNodeRef>
  </bpmn:lane>
  <bpmn:lane id="Lane_engineering" name="Engineering">
    <bpmn:flowNodeRef>Task_ack_engineering</bpmn:flowNodeRef>
  </bpmn:lane>
</bpmn:laneSet>
```

**System semantics**: each lane becomes a `DepartmentDef`. `lane.name` is used as both `DepartmentDef.ID` and `DepartmentDef.Label` — the format is owned by the Profile Service and is trusted as-is. Every `<bpmn:userTask>` must appear in exactly one lane; the compiler derives the department from `<bpmn:flowNodeRef>` membership, not from any Zeebe property. A task not listed in any lane is `TASK_NOT_IN_LANE`.

Additional `DepartmentDef` fields populated by the compiler:

| Field | Type | Description |
| --- | --- | --- |
| `Ignore` | `bool` | `true` when the department's participant pool is an ignored pool (carries `ignore = "true"` on the process-level `<zeebe:properties>`) |
| `Props` | `map[string]string` | Arbitrary key/value pairs from `<zeebe:properties>` on the lane element itself |

Limits: 1–100 lanes per process.

### Supported Elements

#### Tier 1 — Supported

| Element | System semantics |
|---|---|
| `<bpmn:collaboration>` | Wraps N participants → `CompiledCollaboration` |
| `<bpmn:participant>` | `isExecutable="true"` → `CompiledPlan`; non-executable → compiled as `Ignored: true` plan in `CompiledCollaboration`; execution skipped |
| `<bpmn:messageFlow>` | Cross-participant handoff → `MessageDef` linking plans by message name |
| `<bpmn:message>` | Message definition referenced by start/send/receive elements |
| `<bpmn:process>` | Core container → `CompiledPlan` |
| `<bpmn:laneSet>` / `<bpmn:lane>` | Department grouping → `DepartmentDef` |
| `<bpmn:startEvent>` | Blank, `messageEventDefinition`, `timerEventDefinition`, or `signalEventDefinition` — compiled as trigger metadata; no effect on execution graph |
| `<bpmn:endEvent>` | Blank or named (outcome label); `errorEventDefinition` allowed inside subprocess only |
| `<bpmn:userTask>` | Human work unit → `StageDef` inside its `DepartmentDef`; must carry `zeebe:taskDefinition` + `zeebe:assignmentDefinition`; must be in one lane; optional `zeebe:taskSchedule` for due/follow-up dates |
| `<bpmn:sendTask>` | Outbound message task → `StageDef` with `type: "send_task"`; message name in `extras["message"]` |
| `<bpmn:receiveTask>` | Inbound message wait → `StageDef` with `type: "receive_task"`; message name in `extras["message"]` |
| `<bpmn:parallelGateway>` | Concurrent fan-out/fan-in → parallel `ExecutionStep`; split and join must be matched |
| `<bpmn:exclusiveGateway>` | Routing decision → `ExclusiveBranch` list; all outgoing flows require `conditionExpression`; back-edges allowed when guarded |
| `<bpmn:eventBasedGateway>` | Waits for the first of N intermediate catch events |
| `<bpmn:subProcess>` | Embedded, non-event, non-ad-hoc → `SubWorkflowStep`; recursively validated; nested `subProcess` inside another `subProcess` is **not supported** (`NESTED_SUBPROCESS_NOT_SUPPORTED`) |
| `<bpmn:callActivity>` | References a called process by `<zeebe:calledElement processId="..."/>`; the called process BPMN must be supplied in `module_bpmn_xmls` on the draft; called process uses its own lanes; steps flattened directly into parent plan (no `SubWorkflowStep`); if called process has no lanes, a `dept_id` input mapping is required (see §Module BPMNs) |
| `<bpmn:boundaryEvent cancelActivity="…">` + `timerEventDefinition` | SLA deadline → `StageDef.BoundaryTimer`; attached to `userTask` or `subProcess`; timer boundary events on `callActivity` are rejected at compile time; `cancelActivity="false"` = non-interrupting, `"true"` = interrupting |
| `<bpmn:boundaryEvent>` + `errorEventDefinition` | Exception catch → `SubWorkflowStep.ErrorPaths`; attached to `subProcess` or `callActivity`; error boundaries on `callActivity` pass validation but fail compilation |
| `<bpmn:boundaryEvent cancelActivity="…">` + `messageEventDefinition` | Inbound message interrupt → `StageDef.BoundaryMessage` (userTask), `SubWorkflowStep.MessagePaths` (subProcess), or `ExecutionStep.MessagePaths` (callActivity, flattened); attached to `userTask`, `subProcess`, or `callActivity`; zero outgoing flows is a valid terminal interrupt notification |
| `<bpmn:intermediateCatchEvent>` + `timerEventDefinition` | Wait/delay step |
| `<bpmn:sequenceFlow>` | Structural connector |
| `<bpmn:conditionExpression>` (child of sequenceFlow) | FEEL expression for routing decisions; stored verbatim; evaluated at runtime by the Execution Service; max 4096 chars |
| `<bpmn:error>` | Error definition referenced by error events |
| `<zeebe:taskDefinition>` | Stage type and worker routing — see §User Task Contract |
| `<zeebe:assignmentDefinition>` | Task ownership — see §User Task Contract |
| `<zeebe:properties>` / `<zeebe:property>` | Domain flags — see §User Task Contract |
| `<zeebe:taskSchedule>` | Optional due date / follow-up date — see §User Task Contract |
| `<zeebe:subscription>` | Message correlation key on `receiveTask` |

#### Tier 2 — Planned (not yet compiled)

| Element | Notes |
|---|---|
| `<bpmn:inclusiveGateway>` | OR-split/join; parsed and graph-validated but no compile handler — returns `UNSUPPORTED_ELEMENT` |
| `<bpmn:intermediateCatchEvent>` + `messageEventDefinition` | Catch an inbound message mid-process |
| `<bpmn:intermediateCatchEvent>` + `signalEventDefinition` | Catch a broadcast signal mid-process |
| `<bpmn:multiInstanceLoopCharacteristics>` on `userTask` | Panel voting (all-must-complete) |

#### Tier 3 — Rejected (parser denylist)

`scriptTask`, `businessRuleTask`, `transaction`, `adHocSubProcess`, `complexGateway`, terminate/compensate/cancel/conditional/link event definitions, `dataObject`, `dataStore`, `standardLoopCharacteristics`.

These elements trigger `REJECTED_ELEMENT` on upload.

### User Task Contract

Every `<bpmn:userTask>` requires these extension elements:

```xml
<bpmn:userTask id="Task_tender_prep_bnb" name="Prepare Bid-No-Bid">
  <bpmn:extensionElements>

    <!-- Required: stage type — injected by the modeler template, not typed manually -->
    <zeebe:taskDefinition type="prep"/>

    <!-- Required: who can work on this task -->
    <zeebe:assignmentDefinition
      candidateGroups="bd-agent"
      candidateUsers="018e1f2a-0000-7000-8000-000000000001"/>

    <!-- Optional domain flags -->
    <zeebe:properties>
      <zeebe:property name="requires_comment" value="false"/>
    </zeebe:properties>

  </bpmn:extensionElements>
  <bpmn:incoming>Flow_1</bpmn:incoming>
  <bpmn:outgoing>Flow_2</bpmn:outgoing>
</bpmn:userTask>
```

**`zeebe:taskDefinition`**

| Attribute | Required | Rules |
|---|---|---|
| `type` | Yes | Must match a registered stage type handler ID. Default set: `prep`, `review`, `approve`. The registry is configured at deployment — the compiler does not hardcode this list. |
| `retries` | No | Silently ignored (Camunda Modeler default). |

**System semantics**: `type` → `StageDef.Type`. The matched `StageTypeHandler.ActivityName()` → `StageDef.Activity` (e.g. `PrepActivity`). The Execution Service uses `Activity` to route the task to the correct job worker. Unregistered types emit `UNKNOWN_STAGE_TYPE` at warning severity (compilation continues with an `engine_note`); `INVALID_TASK_DEFINITION_TYPE` is deprecated.

**`zeebe:assignmentDefinition`**

| Attribute | Required | Rules |
|---|---|---|
| `candidateGroups` | Yes | IAM role level name; validated against Membership Service at publish time; max 256 chars |
| `candidateUsers` | Yes | Single UUID v7 (one assignee currently enforced; will be relaxed later) |

**System semantics**: `candidateGroups` → `StageDef.Role` (passed as `?level=<role>` to Membership eligibility check). `candidateUsers` → `StageDef.DefaultAssignees` (pre-assigned when the task starts in Execution Service).

**`zeebe:properties`**

Every `<zeebe:property>` element is forwarded verbatim into `StageDef.Extras` (`map[string]string`) — the compiler does not special-case any property name. The Execution Service and job workers own interpretation. For example, `<zeebe:property name="requires_comment" value="true"/>` appears as `extras["requires_comment"] = "true"`.

**`zeebe:taskSchedule`**

Optional scheduling hints for a user task:

```xml
<zeebe:taskSchedule dueDate="2025-12-31T17:00:00Z" followUpDate="2025-12-30T09:00:00Z" />
```

| Attribute | Required | Rules |
|---|---|---|
| `dueDate` | No | FEEL expression or ISO 8601 datetime; stored verbatim; evaluated by the Execution Service |
| `followUpDate` | No | FEEL expression or ISO 8601 datetime; stored verbatim; evaluated by the Execution Service |

**System semantics**: extracted to `StageDef.DueDate` and `StageDef.FollowUpDate`. Both fields are absent from the JSON when the element is omitted. The compiler does not validate or interpret the values.

**Compiled output fields.** Every compiled task produces a `StageDef` with `node_id` (the BPMN element `id`, e.g. `"Activity_0abc123"` — use for stable machine routing in preference to name-based lookups) and `extras` (unknown zeebe:properties plus, for `send_task`/`receive_task`, the resolved message name under `extras["message"]`). Every `ExclusiveBranch` carries `target_node_id` (forward target) and `revert_to_node_id` (revert target on guarded-loop back-edges).

**sendTask / receiveTask extension.** Send and receive tasks compile to `StageDef` with type `"send_task"` or `"receive_task"`. Extension elements are optional:

```xml
<bpmn:sendTask id="Task_issue_rfq" name="Issue RFQ" messageRef="Msg_rfq">
  <bpmn:extensionElements>
    <!-- Optional: role and default assignee, same as userTask -->
    <zeebe:assignmentDefinition candidateGroups="tender-business" candidateUsers="018e1f2a-0000-7000-8000-000000000020"/>
  </bpmn:extensionElements>
</bpmn:sendTask>

<bpmn:receiveTask id="Task_receive_ack" name="Receive Ack" messageRef="Msg_ack">
  <bpmn:extensionElements>
    <!-- Optional: same assignment contract as above -->
    <zeebe:assignmentDefinition candidateGroups="tender-business" candidateUsers="018e1f2a-0000-7000-8000-000000000020"/>
    <!-- zeebe:subscription is passed through unchanged; Execution Service uses it for correlation -->
    <zeebe:subscription messageCorrelationKey="tenderId"/>
  </bpmn:extensionElements>
</bpmn:receiveTask>
```

The message name is resolved from the `messageRef` attribute to a `<bpmn:message>` definition and placed in `extras["message"]` of the compiled `StageDef`. `zeebe:taskDefinition` is not required on send/receive tasks.

### Routing vs Rejection Semantics

**Use XOR + `conditionExpression` for routing decisions.** An exclusive gateway routes the process to one of several paths based on an explicit decision — set by a user or derived from process data.

```xml
<bpmn:exclusiveGateway id="XOR_bid_decision"/>

<bpmn:sequenceFlow id="Flow_nobid" sourceRef="XOR_bid_decision" targetRef="End_nobid">
  <bpmn:conditionExpression>= decision = "no-bid"</bpmn:conditionExpression>
</bpmn:sequenceFlow>

<bpmn:sequenceFlow id="Flow_bid" sourceRef="XOR_bid_decision" targetRef="Task_organise_team">
  <bpmn:conditionExpression>= decision = "bid"</bpmn:conditionExpression>
</bpmn:sequenceFlow>
```

Condition expressions use FEEL syntax (prefix `= ` for expression mode). They are stored verbatim in the compiled plan; the Execution Service evaluates them at runtime.

**Use error events for stage rejection / rework.** When an approval stage rejects, the work must loop back. Model this with a subprocess containing the rework-candidate stages and an error boundary event that catches rejection.

```xml
<bpmn:subProcess id="SP_strategy_approval" name="Strategy Approval">
  <bpmn:startEvent id="Start_sp"/>
  <bpmn:userTask id="Task_prep_strategy" name="Prepare Strategy">
    <!-- zeebe:taskDefinition type="prep" ... -->
  </bpmn:userTask>
  <bpmn:userTask id="Task_approve_strategy" name="Approve Strategy">
    <!-- zeebe:taskDefinition type="approve" ... -->
  </bpmn:userTask>
  <!-- Worker signals rejection by completing with errorCode="strategy-rejected" -->
  <bpmn:endEvent id="End_rejected">
    <bpmn:errorEventDefinition errorRef="Err_strategy_rejected"/>
  </bpmn:endEvent>
  <!-- ...sequence flows... -->
</bpmn:subProcess>

<bpmn:boundaryEvent id="Boundary_rejected" attachedToRef="SP_strategy_approval"
                    cancelActivity="true">
  <bpmn:errorEventDefinition errorRef="Err_strategy_rejected"/>
</bpmn:boundaryEvent>
<!-- Boundary → route to rework or end -->

<bpmn:error id="Err_strategy_rejected" errorCode="strategy-rejected"/>
```

**System semantics**: error end events inside a subprocess compile to `SubWorkflowStep.ErrorPaths`. The Execution Service matches `errorCode` on task completion, cancels the subprocess (interrupting boundary), and follows the error boundary's outgoing flow. This keeps XOR semantics clean (deterministic choice) vs error semantics (exception outcome).

### Timer Boundary Events (SLA)

Attach to a `userTask` to enforce a deadline:

```xml
<!-- Non-interrupting: escalation runs concurrently; original task continues -->
<bpmn:boundaryEvent id="Timer_strategy_sla" attachedToRef="Task_prep_strategy"
                    cancelActivity="false">
  <bpmn:timerEventDefinition>
    <bpmn:timeDuration>PT48H</bpmn:timeDuration>
  </bpmn:timerEventDefinition>
</bpmn:boundaryEvent>

<!-- Interrupting: task is cancelled; only escalation path proceeds -->
<bpmn:boundaryEvent id="Timer_hard_deadline" attachedToRef="Task_approve_offer"
                    cancelActivity="true">
  <bpmn:timerEventDefinition>
    <bpmn:timeDuration>P7D</bpmn:timeDuration>
  </bpmn:timerEventDefinition>
</bpmn:boundaryEvent>
```

Duration format: ISO 8601 (`PT48H`, `P3D`, `PT2H30M`, `P3W`) or Go duration (`48h`). Both forms are accepted and stored verbatim — the compiler does not normalise the value.

**System semantics**: compiles to `StageDef.BoundaryTimer {Duration, Interrupting}`. On fire, the Execution Service either creates a parallel escalation task (non-interrupting) or cancels the current task and starts the escalation path (interrupting). Max one timer boundary per task.

### Message Boundary Events

A `<bpmn:boundaryEvent>` with a `messageEventDefinition` can be attached to a `userTask`, `subProcess`, or `callActivity` to catch an inbound message while the host activity is active.

**Terminal interrupt (no outgoing flow)**: A message boundary event with **zero outgoing sequence flows** is valid — it is a terminal interrupt notification. The message fires, the host activity is cancelled (interrupting) or left running (non-interrupting), and there is no continuation path. This is the idiomatic pattern when an ignored pool sends a message that simply terminates or signals the host activity without routing to further steps.

```xml
<bpmn:boundaryEvent id="Boundary_msg_cancel" attachedToRef="CA_MainWork"
                    cancelActivity="true">
  <bpmn:messageEventDefinition messageRef="Msg_cancel"/>
</bpmn:boundaryEvent>
<!-- No outgoing sequence flow — terminal interrupt -->
```

**Contrast with timer and error boundaries**: Timer and error boundary events still require at least one outgoing sequence flow; a timer or error boundary with no continuation is a modelling error and will fail validation.

**System semantics**: compiles to `StageDef.BoundaryMessage {message_name, interrupting, target_dept}` when attached to a `userTask`, to `SubWorkflowStep.MessagePaths` when attached to a `subProcess`, or to `ExecutionStep.MessagePaths` when attached to a `callActivity` (flattened into the parent plan). The message name is resolved from the boundary event's own `messageEventDefinition messageRef`, falling back to any collaboration `messageFlow` targeting the boundary event — see §Message Flow Name Resolution.

### Gateway Rules

**Parallel gateway.** Split (1-in, N-out) and join (N-in, 1-out) must be paired at the same nesting depth. Every split branch must converge at its matching join. Compile to `ExecutionStep.Parallel` (all branches execute concurrently; join waits for all).

**Exclusive gateway.** All outgoing flows from a split must carry a `<bpmn:conditionExpression>`. Back-edges (rework loops) are allowed only when the originating XOR split has at least one forward-exiting branch (guarded exit). Compiler validates via DFS + Tarjan SCC. Unguarded cycles → `CYCLE_DETECTED` / `UNGUARDED_LOOP`.

**Inclusive gateway.** One or more branches fire based on condition evaluation. Join waits for all active branches. *(Tier 2 — parsed/graph-validated, no compile handler yet.)*

**Multiple end events.** A process may have more than one `<bpmn:endEvent>`. Use the `name` attribute to label outcomes:

```xml
<bpmn:endEvent id="End_nobid" name="No-Bid Closed"/>
<bpmn:endEvent id="End_bid"   name="Bid Committed"/>
```

The Execution Service records the reached end event's name as the workflow outcome.

### Validation Rules

All rules run over the *forward graph* (all edges minus DFS-classified back-edges); errors accumulate into `[]BPMNValidationError`, mapped by the service to **HTTP 422** with `is_valid: false`.

**Structural**

| Rule | Error code |
| --- | --- |
| Root declares the `bpmn` and `zeebe` namespaces | `MISSING_NAMESPACE` |
| No element from the Tier-3 denylist | `REJECTED_ELEMENT` |
| Exactly one start event per process | `NO_START_EVENT` / `MULTIPLE_START_EVENTS` |
| At least one end event per process | `NO_END_EVENT` |
| One `<bpmn:process>` per definitions (unless wrapped in `<bpmn:collaboration>`) | `MULTIPLE_PROCESSES` |
| Every non-event node has an in- and out-edge | `DANGLING_NODE` |
| Sequence flows reference known nodes | `INVALID_SEQUENCE_FLOW_REF` |
| Every node reachable from start (DFS) | `UNREACHABLE_NODE` |
| Each split gateway has a matching join **and every branch reconverges at it** | `UNMATCHED_GATEWAY` |
| Longest forward path ≤ 2000 nodes | `MAX_DEPTH_EXCEEDED` |
| Message flow source/target reference known elements | `UNMATCHED_MESSAGE_FLOW` |
| `messageRef` on flow/task references a declared `<bpmn:message>` | `MISSING_MESSAGE_DEFINITION` |
| Boundary event attached to a legal host node type | `INVALID_BOUNDARY_ATTACHMENT` |
| `<bpmndi:BPMNDiagram>` element present | `MISSING_DIAGRAM` |
| Every node and sequence flow has a corresponding diagram shape/edge | `MISSING_DIAGRAM_SHAPE` |
| `callActivity` has `<zeebe:calledElement processId="…"/>` and the `processId` resolves to a process in the same definitions | `UNRESOLVED_CALLED_ELEMENT` |
| `<bpmn:subProcess>` does not contain a nested `<bpmn:subProcess>` | `NESTED_SUBPROCESS_NOT_SUPPORTED` |

**`DANGLING_NODE` exceptions:** A `<bpmn:boundaryEvent>` with `messageEventDefinition` and zero outgoing sequence flows is **valid** — treated as a terminal interrupt; no `DANGLING_NODE` error is raised. Timer and error boundary events with zero outgoing flows continue to emit `DANGLING_NODE`. A `callActivity` or `subProcess` with no incoming sequence flow is **valid** when it is the only entry node in a process with no explicit `<bpmn:startEvent>` (implicit root, via `FindImplicitStart`) — exempt from the no-incoming check.

**Metadata — userTask**

| Rule | Error code |
| --- | --- |
| `zeebe:taskDefinition` present | `MISSING_TASK_DEFINITION` |
| `taskDefinition.type` matches a registered `StageTypeHandler` | `UNKNOWN_STAGE_TYPE` *(warning — compilation continues)*; `INVALID_TASK_DEFINITION_TYPE` is deprecated |
| Task appears in exactly one lane via `<bpmn:flowNodeRef>` | `TASK_NOT_IN_LANE` |
| `zeebe:assignmentDefinition` present | `MISSING_ASSIGNMENT_DEFINITION` |
| `candidateGroups` present, ≤ 256 chars | `CANDIDATE_GROUPS_EMPTY` |
| `candidateUsers` is a single valid UUID v7 | `INVALID_CANDIDATE_USER` |

**Metadata — sendTask / receiveTask**

| Rule | Error code |
| --- | --- |
| Task appears in exactly one lane via `<bpmn:flowNodeRef>` | `TASK_NOT_IN_LANE` |
| `zeebe:assignmentDefinition` — optional; when present, same `candidateGroups`/`candidateUsers` rules as userTask apply | `CANDIDATE_GROUPS_EMPTY` / `INVALID_CANDIDATE_USER` |
| `messageRef` references a declared `<bpmn:message>` (structural rule) | `MISSING_MESSAGE_DEFINITION` |

**Metadata — callActivity (module / called process)**

| Rule | Error code |
| --- | --- |
| `<zeebe:calledElement processId="…"/>` present and `processId` resolves to a supplied module process | `UNRESOLVED_CALLED_ELEMENT` |
| Called process has at least one `startEvent` | `NO_START_EVENT` (anchored on the callActivity node ID) |
| Called process has no lanes AND callActivity ioMapping has neither a `dept_id` input nor a `target="Depts"` input containing a valid JSON object mapping lane names to dept IDs | `MISSING_DEPT_INPUT_FOR_MODULE` |
| Timer boundary event attached to a `callActivity` | `INVALID_BOUNDARY_ATTACHMENT` |

Note: error boundary events on `callActivity` pass validation but are rejected at compile time. Message boundary events on `callActivity` are fully supported — they pass validation and compile to `ExecutionStep.message_paths` on the flattened step.

**Sequence flow conditions**

| Rule | Error code |
| --- | --- |
| `conditionExpression` on flows leaving an XOR split, ≤ 4096 chars | `INVALID_CONDITION_EXPRESSION` |

**Timer boundary events**

| Rule | Error code |
| --- | --- |
| Timer duration is valid ISO 8601 or Go duration | `INVALID_SLA_DURATION` |
| At most one timer boundary per task | (structural: `DANGLING_NODE` / `UNREACHABLE_NODE`) |

**Message boundary events**

| Rule | Outcome |
| --- | --- |
| `messageEventDefinition` with zero outgoing sequence flows | Valid — treated as terminal interrupt; no error emitted |
| Message boundary event's message name cannot be resolved from `<bpmn:message>` definitions or collaboration message flows targeting the event's ID | `MISSING_MESSAGE_DEFINITION` *(warning — compilation continues with empty correlation key hint)* |

**Topological — guarded loops.** Cycles are not rejected outright; rework/revert loops are allowed when *guarded*:

| Rule | Error code |
| --- | --- |
| Every back-edge originates at a diverging exclusive gateway that keeps a forward exit | `UNGUARDED_LOOP` |
| Exception: `receiveTask` and external participant nodes may be back-edge targets without a preceding XOR gateway (they are externally guarded by the inbound message / external system) | — |
| Every multi-node strongly-connected component has a guarded exit | `CYCLE_DETECTED` |

Back-edges are classified once via DFS from the start event (`classifyBackEdges`). A guarded loop compiles to an `exclusive` step whose revert branches carry `revert_to_dept` / `revert_to_stage`. Max forward-path depth capped at 100 for guarded loops (2000 overall, see Validation Limits).

**Warnings.** These codes are emitted with `IsWarning: true`. The compiler still returns a compiled plan; HTTP callers receive `is_valid: true` alongside a non-empty `warnings` list.

| Code | Trigger |
| --- | --- |
| `UNKNOWN_STAGE_TYPE` | `taskDefinition.type` does not match a registered `StageTypeHandler` — compilation continues with the raw type string |
| `INVALID_ZEEBE_PROPERTY` | A `target="Depts"` input in `zeebe:ioMapping` is present but its source is not valid JSON or is not a `map[string]string` — the module uses its lane names as-is |
| `MISSING_MESSAGE_DEFINITION` | A message boundary event's message name cannot be resolved from either the `<bpmn:message>` definitions or the collaboration's message flows targeting that boundary event's ID — emitted per event; compilation continues with an empty correlation key hint |
| `MISSING_MESSAGE_DEFINITION` *(collaboration-level)* | A `<bpmn:messageFlow>`'s name cannot be resolved via any of the fallback paths in §Message Flow Name Resolution — emitted per flow; `MessageDef.Name` compiles to `""` |

### Validation Limits

| Constraint | Limit | Error code |
|---|---|---|
| Upload size | 10 MB | `PAYLOAD_TOO_LARGE` |
| XML token stream | 1,000,000 tokens | parse error → 400 |
| User tasks per process | 1000 | `TASK_LIMIT_EXCEEDED` |
| Lanes per process | 100 | `LANE_LIMIT_EXCEEDED` |
| Longest forward path | 2000 nodes | `MAX_DEPTH_EXCEEDED` |
| `conditionExpression` length | 4096 chars | `INVALID_CONDITION_EXPRESSION` |
| `candidateGroups` length | 256 chars | `CANDIDATE_GROUPS_EMPTY` |
| `candidateUsers` count | 1 (current) | `INVALID_CANDIDATE_USER` |

### BPMN Error Code Reference

All codes the compiler emits, mapped from `internal/core/domain/errors.go` (`BPMNErrorCode`). The compiler never panics on malformed input — every validation failure is one of these codes.

| Code | Trigger |
|---|---|
| `MISSING_NAMESPACE` | `bpmn` or `zeebe` namespace absent on `<bpmn:definitions>` |
| `REJECTED_ELEMENT` | Element on the parser denylist (Tier 3) |
| `MISSING_TASK_DEFINITION` | `zeebe:taskDefinition` absent on a `userTask` |
| `MISSING_ASSIGNMENT_DEFINITION` | `zeebe:assignmentDefinition` absent on a `userTask` |
| `INVALID_TASK_DEFINITION_TYPE` | *(deprecated — superseded by `UNKNOWN_STAGE_TYPE` warning)* `type` not in the registered `StageTypeHandler` set |
| `UNKNOWN_STAGE_TYPE` | *(warning severity)* `type` attribute value not in the registered `StageTypeHandler` set; compilation continues |
| `TASK_NOT_IN_LANE` | `userTask` not referenced by any `<bpmn:flowNodeRef>` |
| `CANDIDATE_GROUPS_EMPTY` | `candidateGroups` absent or exceeds 256 chars |
| `INVALID_CANDIDATE_USER` | `candidateUsers` is not a valid UUID v7 |
| `INVALID_CONDITION_EXPRESSION` | Condition expression exceeds 4096 chars |
| `INVALID_ZEEBE_PROPERTY` | *(warning severity)* module `zeebe:ioMapping` `target="Depts"` input source is not valid JSON / not a `map[string]string` |
| `INVALID_SLA_DURATION` | Timer boundary duration is not valid ISO 8601 or Go duration |
| `MISSING_MESSAGE_DEFINITION` | `messageRef` on a flow or task references an undeclared `<bpmn:message>` |
| `UNMATCHED_MESSAGE_FLOW` | Message flow source/target does not reference a known element |
| `TASK_LIMIT_EXCEEDED` | User task count > 1000 |
| `LANE_LIMIT_EXCEEDED` | Lane count > 100 |
| `MULTIPLE_START_EVENTS` | More than one `startEvent` in the process |
| `MULTIPLE_END_EVENTS` | More than one `endEvent` (only raised when collaboration is absent and single process has > 1 end events without guarded exits) |
| `NO_START_EVENT` | Process has no `startEvent` |
| `NO_END_EVENT` | Process has no `endEvent` |
| `MULTIPLE_PROCESSES` | More than one `<bpmn:process>` without a wrapping collaboration |
| `DANGLING_NODE` | Node has no incoming or outgoing sequence flow |
| `UNREACHABLE_NODE` | Node not reachable from `startEvent` via DFS |
| `INVALID_SEQUENCE_FLOW_REF` | `sequenceFlow` references unknown `sourceRef` or `targetRef` |
| `UNMATCHED_GATEWAY` | Split gateway has no reachable matching join |
| `CYCLE_DETECTED` | Tarjan SCC found a strongly-connected component without a guarded exit |
| `UNGUARDED_LOOP` | Back-edge originates at a gateway with no forward exit |
| `MAX_DEPTH_EXCEEDED` | Longest forward path exceeds 2000 nodes |
| `INVALID_BOUNDARY_ATTACHMENT` | Boundary event attached to an unsupported element type |
| `MISSING_DIAGRAM` | No `<bpmndi:BPMNDiagram>` found in the BPMN document |
| `MISSING_DIAGRAM_SHAPE` | A BPMN element has no corresponding `<bpmndi:BPMNShape>` in the diagram |
| `UNRESOLVED_CALLED_ELEMENT` | `callActivity` references a `processId` not found in supplied module BPMNs |
| `NESTED_SUBPROCESS_NOT_SUPPORTED` | A `subProcess` is nested inside another `subProcess` |
| `MISSING_DEPT_INPUT_FOR_MODULE` | Called process has no lanes and no `dept_id` input mapping was provided |
| `UNSUPPORTED_ELEMENT` | Tier 2 element present (e.g. `inclusiveGateway`) — parsed but compile handler absent |

> **Unparseable documents.** Documents that fail XML parsing (bad XML, `DOCTYPE`/entity injection, token-cap exceeded) return **400 `INVALID_BPMN_XML`** — not a BPMN validation code. Forbidden `DOCTYPE`/entity additionally triggers an internal security log with client IP and tenant ID; the client sees only the generic 400.

---

## Platform Libraries

The three BCBP platform libraries are private Go modules — see the table and upgrade instructions in [`.claude/CLAUDE.md`](.claude/CLAUDE.md). This section covers only the non-obvious integration points in this codebase; a read-only reference copy of each library's own source lives under `platform-libs/` (gitignored, never imported directly — see `.claude/CLAUDE.md`).

### platform-events (SNS / outbox / event envelopes)

`Envelope[T]` is the canonical wire format — always construct with `NewEnvelope` (generates a UUID v7 `ID`, sets `Timestamp`). The service builds envelopes from the service layer via `buildEnvelope` in `internal/core/service/helpers.go`; because the service layer has no Gin context, it pulls the trace ID directly from the active OTel span:

```go
opts := []events.EnvelopeOpt{events.WithTenantID(tenantID)}
if sc := trace.SpanFromContext(ctx).SpanContext(); sc.IsValid() {
    opts = append(opts, events.WithTraceID(sc.TraceID().String()))
}
env := events.NewEnvelope[json.RawMessage](eventType, source, raw, opts...)
```

`sc.IsValid()` is false on non-traced paths (unit tests, stub runners), so no zero trace ID is attached. At the HTTP handler layer, use `rc.TraceID` from `gincommon.RequestContext(c)` instead — same OTel trace ID, already stringified.

> **v1.3 envelope key renames (breaking wire format, Go field names unchanged):** `payload → data`, `timestamp → time`, `schema_version → specversion`, `schema_id → dataschema`. Any local struct/raw map hardcoding these JSON tags must be updated. **Deploy note:** drain the outbox before deploying — records written with old keys produce empty `data` under the v1.3 struct.

The outbox eliminates the dual-write problem — enqueue inside the same transaction as the business write (see [Transactor pattern](#transactor-pattern) above); `OutboxRepository.Enqueue` extracts the tx from context and calls `outbox.Enqueue(ctx, tx, env)` internally. This service does not use `NewSQSConsumer` — inbound membership events arrive over HTTP at `POST /internal/events` (see [API Overview](README.md#api-overview) in the README), delivered by a shared workflow-events consumer.

`PublishBatch` splits automatically at 10 (SNS hard limit). SNS message attributes (`EventType`, `TenantID`, `Source`, `EventID`) are always set for SQS subscription filter policies; a non-empty `Subject` is also forwarded as an SNS `Subject` attribute for filter-policy routing without body parsing. Mock for tests: `events/mock.NewMockPublisher()` — exposes `Published() []Envelope[json.RawMessage]`.

### platform-pgcommon (pool, RLS GUC injection, transactor, migrations)

`pgcommon.NewPool` takes a `GUCProvider` (set to `pgcommon.GUCSetFromContext`), which reads the `GUCSet` stored by `WithGUCSet(ctx, gs)` on every acquired connection — the `InjectGUCSet` middleware does this for every authenticated HTTP request; the gRPC path calls it manually (see [HTTP middleware chain](#http-middleware-chain) and [flow 2](#2-grpc-getcompiledworkflow) above).

> `Config.Logger` and `Config.Tracer` are **not wired** — the library's internal `port.Logger`/`port.Tracer` interfaces use unexported `port.Field` types, unimplementable from outside the module. `SlowQueryThreshold` still triggers slow-query events internally.

`pgmetrics.Init(serviceName, buildVersion)` registers `pgcommon_*` Prometheus counters (`pgcommon_query_total`, `pgcommon_pool_acquire_total`, `pgcommon_retry_total`, `pgcommon_slow_query_total`, pool gauges) — idempotent, safe to call more than once (e.g. in tests that call `main` directly).

Retry on deadlock/serialization failure (SQLSTATE `40P01`/`40001`):

```go
err = pgcommon.RunInTxWithRetryOpts(ctx, pool, pgx.TxOptions{
    IsoLevel: pgx.Serializable, AccessMode: pgx.ReadWrite,
}, pgcommon.RetryOptions{MaxAttempts: 5, InitialWait: 10 * time.Millisecond, MaxWait: 500 * time.Millisecond, Multiplier: 2.0, JitterFraction: 0.25},
   func(ctx context.Context, tx pgx.Tx) error { return doWork(ctx, tx) })
```

Error helpers work with wrapped errors (`errors.As` internally):

```go
if pgcommon.IsUniqueViolation(err) {
    switch pgcommon.ConstraintName(err) {
    case "workflow_tenant_id_business_key_key":
        return domain.ErrDuplicateBusinessKey
    case "idx_wv_single_draft":
        return domain.ErrDraftAlreadyExists
    }
}
if errors.Is(err, pgx.ErrNoRows) {
    return domain.ErrNotFound
}
```

Connection lifecycle for a single query (`WithConn`) and for a transaction (`RunInTx`):

> Source: [docs/architecture/mermaid/pgcommon-withconn-flow.mmd](docs/architecture/mermaid/pgcommon-withconn-flow.mmd)

```mermaid
sequenceDiagram
    participant Caller
    participant Pool
    participant pgxpool
    participant Hook as PrepareConn hook
    participant DB as PostgreSQL

    Caller ->>+ Pool: WithConn(ctx, fn)
    Pool   ->>+ pgxpool: Acquire(ctx)
    pgxpool ->>+ Hook: PrepareConn(ctx, conn)
    alt GUCProvider set and GUCSet non-zero
        Hook ->> Hook: GUCProvider(ctx) → GUCSet
        Hook ->> Hook: g.Validate()
        Hook ->>  DB: batch set_config(user_id, tenant_id, tenant_roles, is_local=false)
        Hook -->> pgxpool: true, nil
    else no GUCProvider or zero GUCSet
        Hook -->> pgxpool: true, nil
    end
    pgxpool -->>- Pool: *pgxpool.Conn
    Pool   ->>+ Caller: fn(ctx, conn)
    Caller -->>- Pool: error or nil
    Pool   ->> pgxpool: conn.Release()
    Pool -->>- Caller: return error
```

> Source: [docs/architecture/mermaid/pgcommon-runintx-flow.mmd](docs/architecture/mermaid/pgcommon-runintx-flow.mmd)

```mermaid
sequenceDiagram
    participant Caller
    participant RunInTx
    participant DB as PostgreSQL

    Caller ->>+ RunInTx: RunInTx(ctx, pool, opts, fn)
    RunInTx ->>+ DB: pool.BeginTx(ctx, opts)
    DB -->>- RunInTx: tx

    RunInTx ->>+ Caller: fn(ctx, tx)

    alt fn returns nil
        Caller -->>- RunInTx: nil
        RunInTx ->> DB: tx.Commit(ctx)
        RunInTx -->> Caller: nil
    else fn returns error
        Caller -->> RunInTx: error
        RunInTx ->> DB: tx.Rollback(context.Background())
        RunInTx -->>- Caller: fn error
    else fn panics
        RunInTx ->> DB: tx.Rollback(context.Background())
        RunInTx ->>- Caller: re-panic
    end
```

**Migrations.** Managed by `migrate.Runner` (golang-migrate, `pgx/v5`). Files live in `db/migrations/` as `NNNNNN_description.up.sql`/`.down.sql` pairs, embedded via `db/migrations/migrations.go` (`//go:embed *.sql`). Run via the **`migrate` subcommand** (`go run ./cmd/server migrate` / `make migrate`) — never at server boot, since two replicas racing on golang-migrate's advisory lock would stall the loser and risk a readiness-probe timeout. `runMigrations` first calls `outbox.ApplySchema` (tracked under `pgcommon_migrations`), then the service domain runner over `db/migrations/` (tracked under `wf_definition_migrations`) — kept separate so the two histories never collide. Unlike goose, the pgx/v5 driver runs each migration file as a single statement, so `$$`-quoted PL/pgSQL blocks need no `StatementBegin/End` annotations.

### platform-gincommon (Gin/gRPC middleware, logging, tracing)

`pkg/logger.NewLogger(appEnv)` returns a value that directly satisfies `port.Logger` — pass the same instance to `gincommon.Config{Logger: log}`, `outbox.Config{Logger: log}`, and `pgcommon.Config{Logger: log}`. `APP_ENV=dev` → human-readable Zap output; anything else → JSON.

`gincommon.InitTracingFromEnv()` must be called once at startup before the router handles traffic — reads `OTEL_SERVICE_NAME`/`OTEL_EXPORTER_OTLP_ENDPOINT`/`OTEL_EXPORTER_OTLP_INSECURE`. If not called, `platform-events` and `platform-pgcommon` produce no-op OTel spans rather than erroring.

Per-route authorization:

```go
api.POST("/workflows/:id/archive",
    gincommon.RequirePermission("write", "workflow", authzPort),
    h.ArchiveWorkflow,
)
```

`authzPort` implements `port.Authorizer`; `RequirePermission` returns `403` if denied, before the handler runs.

gRPC interceptors — see [gRPC interceptor chain](#grpc-interceptor-chain) above.
