# Architecture

This document describes the internal structure, dependency rules, and runtime data flows of the `workflow-definition-service`.

> **Design intent:** this service is the design-time control plane of the BCBP Workflow Engine. It owns the full lifecycle of a workflow template — from raw BPMN XML through semantic validation, DSL compilation, and version state transitions — and serves the resulting compiled plan to the Execution Service over its internal HTTP API.

---

## System context

Where the service sits in the broader platform:

> Source: [docs/architecture/mermaid/system-context.mmd](docs/architecture/mermaid/system-context.mmd)

```mermaid
flowchart LR
    FE["Frontend\nWorkflow Builder"]
    DEF["workflow-definition-service\n(this repo)\nHTTP :8080"]
    EXEC["workflow-execution-service\nHTTP :8080"]
    DB[("PostgreSQL 18\nworkflow_definition")]
    CACHE[("Valkey 8\nidempotency · cache")]
    CONSUMER["shared workflow-events\nconsumer (membership-workflow-q)"]
    IAM["iam-service\norg membership API"]

    FE -->|"REST"| DEF
    EXEC -->|"compiled-plan read\ninternal HTTP"| DEF
    DEF -->|"active-instances · pause-tasks\ninternal HTTP"| EXEC
    DEF -->|"read/write"| DB
    DEF -->|"idempotency · quota cache"| CACHE
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
        Internal[Internal HTTP API]
        Parser[BPMN Parser]
        Compiler[DSL Compiler]
        Validator[Validation Engine]
        InternalEvents[POST /internal/events]

        API --> Validator
        API --> Parser
        Internal --> DB
        Parser --> Compiler
    end

    subgraph Persistence [RDS PostgreSQL]
        DB[(workflow_definition DB)]
    end

    subgraph External_Integrations [External Services]
        OrgMembership[Org & Membership Service]
        EventsConsumer[Shared Workflow-Events Consumer]
        SQS_Membership[SQS: membership-workflow-q]
    end

    UI -->|1. Get Eligible Users per Node| OrgMembership
    UI -->|2. Upload BPMN| Gateway
    Gateway -->|Headers: x-tenant-id, etc.| API
    ExecSvc -->|HTTP: compiled plan| Internal

    Compiler -->|3. Validate Assignee Eligibility| OrgMembership
    Compiler -->|4. Store Draft/Publish| DB
    SQS_Membership -.->|5. Consume Revocation Events| EventsConsumer
    EventsConsumer -->|6. POST /internal/events| InternalEvents
    InternalEvents -->|Invalidate Affected Templates| DB

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

    HTTPHandler --> HTTPMiddleware : Uses middleware chain
    HTTPHandler --> PublisherService : Dispatches requests
    HTTPHandler --> Validator : Calls stateless validation

    PublisherService --> Validator : Validates BPMN
    PublisherService --> DraftCompiler : Compiles execution plan
    PublisherService --> PlanService : Verifies template quotas
    PublisherService --> MembershipService : Verifies assignee eligibility
    PublisherService --> WorkflowRepository : Persists workflow and versions

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
            http["http/handler\nGin handlers + middleware\nidempotency · authz · /api/v1/internal"]
        end
        subgraph outbound["outbound"]
            postgres["postgres/\nsqlc repo adapters\nTransactor · RunInTx"]
            valkey["valkey/\nCacheStore impl"]
            http_out["http/\nExecutionClient · MembershipClient"]
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
    pg(["adapter/outbound/postgres"])
    vk(["adapter/outbound/valkey"])
    mc(["adapter/outbound/http"])
    svc(["core/service"])
    port(["core/port"])
    dom(["core/domain"])
    cmp(["bpmn_compiler"])

    main --> http_h
    main --> pg
    main --> vk
    main --> mc
    main --> svc
    http_h --> port
    http_h --> dom
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
  → Metrics                (gincommon) — Prometheus platform_http_requests_total etc.
  → Logging                (gincommon) — structured http_request log on close

  → /healthz, /readyz, /metrics        ← stop here (public)

  → RequireAuth            (gincommon) — validates x-user-id, x-tenant-id
  → ContextMiddleware      (gincommon) — builds RequestContext{TenantID, UserID, Roles, TraceID}
  → InjectGUCSet           (service)   — copies RequestContext into pgcommon GUC context key
                                          so pool.GUCProvider can set app.tenant_id on every DB conn
  → LimitRequestBody       (service)   — wraps c.Request.Body with http.MaxBytesReader(10 MB); also on the internal group
  → RequireJSONContentType (service)   — 415 if a request body's Content-Type is not application/json

  → GET endpoints          ← read-only, no further authz

  → handler                                       ← POST/PUT/PATCH/DELETE check the admin role first (403 otherwise)
```

**Why `InjectGUCSet` is needed:** `gincommon.ContextMiddleware` stores `RequestContext` in the Gin context (`c.Set`) rather than the Go request context (`c.Request.Context()`). `pgcommon.GUCSetFromContext` reads only the Go context, so without this bridge the HTTP path never sets `app.tenant_id` and PostgreSQL RLS filters against a null GUC — a multi-tenant isolation failure. The internal routes do not use it: no gateway identity reaches them, so each internal handler calls `pgcommon.WithGUCSet` itself from the `tenant_id` its request carries.

## Internal API

`/api/v1/internal` is the service-to-service route group, specified in `api/internal-openapi.yaml`. It runs the observability middleware and `RequireInternalToken` (constant-time `x-internal-token` against `INTERNAL_API_TOKEN`, a problem body on `401`), not `ProtectedMiddlewares`: no gateway identity reaches it, and the gateway never routes it. The Execution Service's own internal document is copied byte for byte into `api/contracts/`; `make contract-breaking` and `make contract-check` keep both honest in CI (definition LLD §3.4).

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

    Note over Middleware: OTel · authz · idempotency-key
    Client ->>+ Middleware: POST /versions/:id/publish
    Middleware ->> Middleware: extract x-tenant-id · x-user-id → RequestContext
    Middleware ->> Middleware: Idempotency-Key check (Valkey cache)
    Middleware ->>+ Handler: handler func
    Handler ->>+ Service: VersionService.Publish(ctx, ...)
    Service ->>+ Tx: RunInTx(ctx, fn)
    Tx ->>+ Repo: VersionRepo.Publish(ctx, ...)  [status-guarded SQL]
    Repo -->>- Tx: ok
    Tx -->>- Service: commit
    Service -->>- Handler: PublishedVersion
    Handler -->>- Middleware: 200 JSON
    Middleware ->> Middleware: cache response under Idempotency-Key (24 h TTL, 2xx only)
    Middleware -->>- Client: response
```

### 2. Internal compiled-plan read

> Source: [docs/architecture/mermaid/internal-compiled-plan-flow.mmd](docs/architecture/mermaid/internal-compiled-plan-flow.mmd)

```mermaid
sequenceDiagram
    participant ExecSvc as Execution Service
    participant H as GetCompiledPlan handler
    participant Svc as CompiledPlanService
    participant Cache as Valkey
    participant Repo as WorkflowVersionRepo

    ExecSvc ->>+ H: GET /api/v1/internal/workflow-versions/{id}/compiled-plan?tenant_id= (x-internal-token)
    H ->> H: pgcommon.WithGUCSet(ctx, GUCSet{TenantID: tenant_id})
    Note right of H: RLS GUC from the query — no gateway identity on internal routes
    H ->>+ Svc: Get(tenantID, versionID)
    Svc ->> Cache: GET wf:plan:<tenant>:<version>
    Cache -->> Svc: miss (fail-open)
    Svc ->>+ Repo: GetByID
    Repo -->>- Svc: version + compiled_plan_json
    Svc ->> Cache: SET (CACHE_COMPILED_PLAN_TTL)
    Svc -->>- H: CompiledPlan
    H -->>- ExecSvc: 200 {workflow_id, version_id, version_number, status, is_valid, compiled_plan}
```

### 3. DepartmentMembershipRevoked (inbound via POST /internal/events)

> Source: [docs/architecture/mermaid/membership-revoked-flow.mmd](docs/architecture/mermaid/membership-revoked-flow.mmd)

```mermaid
sequenceDiagram
    participant Consumer as shared workflow-events consumer
    participant Handler as http/handler.HandleInternalEvent
    participant Svc as VersionService.HandleDepartmentMembershipRevoked
    participant Tx as Transactor.RunInTx
    participant Repo as VersionRepo

    Consumer ->>+ Handler: POST /internal/events (envelope)
    Handler ->> Handler: WithGUCSet(tenant_id) · dispatch by type
    Handler ->>+ Svc: HandleDepartmentMembershipRevoked(ctx, payload)
    loop for each affected version
        Svc ->>+ Tx: RunInTx(ctx, fn)
        Tx ->> Repo: VersionRepo.GetByID  →  check is_valid
        Tx ->> Repo: VersionRepo.SetInvalid(ctx, versionID, nodes)
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
    participant ES as Execution Service (internal HTTP)
    participant DB as PostgreSQL

    C->>H: POST /workflows/:id/archive
    H->>S: Archive(ctx, tenantID, userID, workflowID)
    S->>ES: GET /api/v1/internal/workflows/{id}/active-instances
    ES-->>S: hasActive=false
    S->>DB: BEGIN TX
    S->>DB: UPDATE workflow_version SET status=ARCHIVED
    S->>DB: UPDATE workflow SET active_version_id=NULL
    S->>DB: COMMIT
    S-->>H: ok
    H-->>C: 200 OK
```

## Transactor pattern

Repository adapters participate in multi-step transactions via the `port.Transactor` interface, without leaking pgx types into the port layer:

```go
// Service layer — both writes commit or roll back atomically, no pgx imports needed:
err := s.transactor.RunInTx(ctx, func(ctx context.Context) error {
    if err := s.versionRepo.Publish(ctx, ...); err != nil {
        return err
    }
    return s.workflowRepo.UpdateActiveVersion(ctx, ...)
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

This means: pgx types never escape `adapter/outbound/postgres/`; and the service layer has no pgx imports.

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
| Idempotency key | `idem:{tenant_id}:{method}:{path}:{key}` | 24 h (configurable: `IDEMPOTENCY_TTL`) |
| Draft edit lock | `draft-lock:{tenant_id}:{workflow_id}` | 30 s (refreshed) |

When Valkey is unreachable the service stays up but degrades by purpose: compiled-plan reads fall through to Postgres, a duplicate-request replay is not detected and the request runs, and a draft save fails because the draft lock cannot be taken. `/readyz` reports not ready, since it pings Valkey as well as the database.

---

## Key invariants

| Invariant | Enforcement |
| --------- | ----------- |
| Single DRAFT per workflow | `UNIQUE INDEX idx_wv_single_draft ON workflow_version(workflow_id) WHERE status='DRAFT'` — DB-enforced; returns `ErrDraftAlreadyExists` on violation |
| Status-guarded mutations use `statusOrNotFound` | Publish/Archive SQL filters on `status=X`; on `RowsAffected()==0` a secondary `GetByID` distinguishes absent (→ `ErrNotFound`) from wrong-status (→ `ErrVersionNotDraft` etc.) — happy path is always single-query |
| `is_valid` reflects assignee eligibility only | `workflow_version.is_valid=false` means an assignee left a required department role; it does not reflect BPMN structural validity |
| Idempotency-Key scoped per tenant | Cache key format: `idem:<tenant_id>:<method>:<path>:<key>` — prevents cross-tenant replay; only 2xx responses are cached (24 h TTL) |
| Internal routes require manual GUC injection | `WithGUCSet` must be called by each internal handler before any repo access; the public HTTP path injects via `InjectGUCSet` after `gincommon.ProtectedMiddlewares` |
| Tenant policy logs violations | Every tenant table forces row-level security with `rls_check_tenant()` / `rls_check_tenant_or_global()`; a blocked row is written to `rls_violation_log` at a 1% sample and exported as `workflow_definition_service_rls_violations_total` |
| `ProtectedMiddlewares` must wrap all protected routes | Missing middleware causes `RequestContext` lookup to fail → 500 (server misconfiguration, not 401) |

---

## Configuration reference

All configuration is read from environment variables via `internal/config.Config` at startup. The service fails fast if any required variable is missing. Copy `.env.example` to `.env` for local development.

### Application

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `APP_ENV` | No | `dev` | One of `local`, `dev`, `test`, `staging`, `prod`; any other value fails startup. `dev` enables Gin debug mode and human-readable Zap logs; any other value activates release mode + JSON logs. It is the metrics `environment` label |
| `BUILD_VERSION` | No | `dev` | Injected via `-ldflags` at build time; labels `platform_build_info` and OTel `service.version` |

### Server

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `HTTP_PORT` | No | `8080` | Gin HTTP listener port |
| `METRICS_PORT` | No | `9091` | Prometheus scrape listener port, separate from the API port so a NetworkPolicy can scope them independently |

### Observability (platform-gincommon)

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `OTEL_SERVICE_NAME` | No | `workflow-definition-service` | OTel `service.name` attribute |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | No | `localhost:4317` | OTLP/gRPC collector address; `none` turns export off |
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
| `CACHE_COMPILED_PLAN_TTL` | No | `1h` | TTL for the internal compiled-plan read's cache; entries are also deleted on archive / membership invalidation |
| `IDEMPOTENCY_TTL` | No | `24h` | TTL for idempotency keys on HTTP mutations |
| `SHUTDOWN_DRAIN_DELAY` | No | `5s` | On SIGTERM `/readyz` fails for this long before the listener closes, so the pod leaves endpoint lists first; must be less than the pod's termination grace period minus the 30 s shutdown bound |

### Internal endpoint

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `INTERNAL_API_TOKEN` | When `APP_ENV` is not `dev` | *(empty)* | Required as the `x-internal-token` header on `/api/v1/internal`. Empty disables the check in dev; NetworkPolicy / mesh remains the primary control either way. |

### Outbound services

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `ORG_MEMBERSHIP_BASE_URL` | When `APP_ENV != dev` | *(empty)* | Base URL of the Org & Membership Service for assignee eligibility checks |
| `MEMBERSHIP_CLIENT_TIMEOUT` | No | `10s` | HTTP client timeout for the Org & Membership Service |
| `EXECUTION_SERVICE_URL` | When `APP_ENV != dev` | *(empty)* | The Execution Service's internal HTTP base URL (archive guard, pause-tasks); empty builds no client |
| `EXECUTION_SERVICE_TOKEN` | When `APP_ENV != dev` | *(empty)* | The Execution Service's `INTERNAL_API_TOKEN`, sent as `x-internal-token` |
| `EXECUTION_CLIENT_TIMEOUT` | No | `5s` | Per-attempt timeout for the Execution Service client |
| `OPENBAO_ADDR` | When `APP_ENV != dev` | *(empty)* | OpenBao address for tenant connector credentials; empty disables the credential endpoints in dev |
| `OPENBAO_TOKEN` | When `APP_ENV != dev` | *(empty)* | OpenBao token |
| `OPENBAO_MOUNT` | No | `secret` | OpenBao KV mount |
| `SECRETS_CLIENT_TIMEOUT` | No | `5s` | OpenBao client timeout |
| `CONNECTOR_ALIAS_ALLOWED_HOSTS` | No | *(empty)* | Hosts a connector REST alias may target: `.suffix` or exact host; empty rejects every alias write |

### Row-level-security violation export

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `RLS_VIOLATION_EXPORT_INTERVAL` | No | `30s` | How often the system pool reads new `rls_violation_log` rows into `workflow_definition_service_rls_violations_total` |
| `RLS_VIOLATION_RETENTION` | No | `168h` | Rows older than this are deleted by the same loop |

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
- **Hash** (`bpmn_compiler/hasher.go`) — canonical SHA-256 of the normalized process for `artifact_hash`; the zeebe properties of top-level tasks and sequence flows are sorted by name. A bundle with a called process or modules hashes every process, ordered by (id, hash), so XML order never changes it. `canonicalHash` copies the slices it sorts (`copySlice[T]`) so the parsed struct is not mutated.

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
- One process runs: two runnable processes are `MULTIPLE_PROCESSES`, none is `NO_RUNNABLE_PROCESS`.

#### Collaboration: one pool runs

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

**System semantics**: one pool runs, the process that is executable, not called by another and not marked ignore. Every other pool is an outside party's, drawn for context, and produces nothing. The pool that runs talks to an outside pool only through message flows: its receive tasks and message boundaries wait for a message delivered into the instance, and its send tasks have no effect outside it.

**Message Flow Name Resolution.** A `<bpmn:messageFlow>` element is rarely annotated with `name` or `messageRef` in practice — the message reference usually lives on the *connected node* instead. The compiler resolves each flow's display name in this order:

1. The `messageFlow` element's own `name` attribute.
2. The `messageFlow` element's own `messageRef` attribute, resolved against a root-level `<bpmn:message>`.
3. The target node's own `messageRef` — a `sendTask`/`receiveTask`'s `messageRef` attribute, or a boundary event's `messageEventDefinition messageRef`.
4. The same lookup against the source node.

A receive task or message boundary whose own `messageRef` does not resolve waits for the name of the flow into it; a receive task with neither waits for its own id. A flow into the pool that runs with no name gets `MISSING_MESSAGE_DEFINITION` as a **warning**. Declare a `<bpmn:message>` and reference it via `messageRef` on the flow or the connected node to fix it.

**Ignored pool pattern**: A process carrying `<zeebe:property name="ignore" value="true"/>` inside its process-level `<zeebe:properties>` is an outside party's pool. Its tasks are not checked, its drawn calls are never made, and it does not appear in the `CompiledCollaboration`'s plans. A pool that is not executable and that nothing calls is treated the same way. A call to a pool marked ignore is `UNRESOLVED_CALLED_ELEMENT`: a module always runs.

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

**Implicit-start pattern**: The pool that runs in a collaboration may have **no explicit start event**, its root activity having no incoming sequence flow. The compiler identifies the implicit root via `FindImplicitStart` — the single node with no incoming sequence flow and at least one outgoing sequence flow. This node is exempt from the "no incoming sequence flow" dangling check. A `NO_START_EVENT` error is only raised if no implicit root can be identified.

Typical use: a `callActivity` is the first element in the process; an ignored pool sends a message that arrives at a `messageEventDefinition` boundary event **on** that callActivity, acting as a concurrent notification or interrupt without the main process requiring an explicit start event.

### Module BPMNs (Called Processes)

A `<bpmn:callActivity>` calls either a process in the same file, private to that workflow and versioned with it, or a module from the library (tenant modules plus global ones; definition LLD §3.3.18). A workflow never carries module XML of its own.

**Resolution** (`service/module_resolution.go`). When a workflow or starter is created, at `/workflows/validate`, at publish and for a draft's diff, `moduleResolver` asks the compiler for the document's library calls (`ModuleCalls`: calls with a `versionTag`, and calls whose target is not a process in the file), resolves each through `ModuleVersionRepository.Resolve`, walks each resolved module's own calls the same way, and hands the versions to `BundleModules`. The bundle renames each module process to its plan name `<process_id>@v<version_number>` and rewrites every call to it, so two versions of one module can coexist. A module's `<bpmn:message>`/`<bpmn:error>` definitions come with it, renamed `<plan name>/<id>` with its references, so it keeps its own message names and error codes. A call that does not resolve is `UNRESOLVED_CALLED_ELEMENT` on the callActivity; one inside a module is reported on the workflow's call that reached it. `GET …/versions/:version_id/validation` resolves a published version's calls to its stored `module_refs` instead of the live library, and module authoring reports a call back to the module itself at its latest version as `CALLED_ELEMENT_CYCLE`. Publish records the resolved versions in `workflow_version.module_refs`; `GET /modules/:id/usages` reads them back.

**Rules:**

- Every callActivity carries `<zeebe:calledElement processId="<id>"/>`, a literal process id. `bindingType="versionTag" versionTag="<n>"` pins a published module version; `latest` or no binding takes the module's active version when the workflow is published. `deployment` means a process in the same file. A `versionTag` names a library module; pinning a process id the file also defines is `UNRESOLVED_CALLED_ELEMENT`.
- A module is created from a file with exactly one `<bpmn:process isExecutable="true">`; its id is the module's fixed `process_id`, unique among the tenant's modules and the global ones (`409 MODULE_PROCESS_ID_CONFLICT`).
- A module is validated with its own process as the root (`ValidateModule`); only errors block. A global module names no user (`INVALID_CANDIDATE_USER`).
- Each called process compiles **once, into its own plan** in `CompiledCollaboration.Plans`, named by its process id (a library module's by its plan name). Each call site compiles to an `ExecutionStep.CallPlan` naming that plan. Both services expand calls with `dsl.ExpandCalls` (workflow-models), which gives every call its own departments, `<call id>::<lane>`, so the same module called twice yields two separate sets of tasks.
- **Departments.** A module lane that holds work becomes a department. With a `dept_id` property it is fixed to that department, and no call can remap it. Without one it is an open slot the call binds to one of the caller's lanes: through the `Depts` input, or by default to the callActivity's own lane when the module has exactly one open slot (a laneless module is one slot). A bound slot takes the caller lane's department and IAM id.
- **Call-site assignees.** An optional `Assignees` input maps a module task id, or a `::`-joined path of calls to a task in a nested module, to a user id for this call only; the outermost call's entry wins. A laneless module's department is named by its process id without the version, so its tasks' node keys, `<call id>::<process_id>/<task id>`, survive a move to another module version.
- Message, timer and error boundary events attach to a callActivity and compile to the call step's paths.
- Diagrams in module BPMNs are discarded — only process elements are merged.
- Nested `<bpmn:subProcess>` inside another subProcess is not supported (`NESTED_SUBPROCESS_NOT_SUPPORTED`).
- A published workflow keeps its module snapshot: a new module version, or archiving the module, changes nothing until the workflow is republished. A clone copies only the main BPMN; its calls resolve again at publish.

**Example — calling a module with two generic lanes:**

```xml
<bpmn:callActivity id="CA_PrepResponse" name="Prepare Response">
  <bpmn:extensionElements>
    <zeebe:calledElement processId="Process_PrepareResponse"/>
    <zeebe:ioMapping>
      <zeebe:input source='{"sender":"Consultant","receiver":"Tender"}' target="Depts"/>
      <zeebe:input source='{"PR_Review":"018f2d3c-0000-7000-8000-0000000000b2"}' target="Assignees"/>
      <zeebe:input source="=clarificationId" target="clarificationId"/>
    </zeebe:ioMapping>
  </bpmn:extensionElements>
</bpmn:callActivity>
```

`Depts` and `Assignees` are JSON objects of strings, written plain or with a leading `=`, and compile to the call step's `Departments` and `Assignees`; they never reach `io_mapping`. `Depts` maps module lane names to caller lane names, matched case-insensitively. A malformed value, an unknown lane or task, or a retired `dept_id` input is `INVALID_ZEEBE_PROPERTY`; an unmapped slot when there are two or more is `MISSING_DEPT_INPUT_FOR_MODULE`. Other inputs (like `clarificationId`) compile to the call step's `io_mapping`.

**isExecutable semantics:** a called process runs as a module whether or not it is executable. Among the processes nothing calls, only an executable one that is not marked ignore runs.

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

**Ignored lane**: a lane carrying `<zeebe:property name="ignore" value="true"/>` is an outside party inside the process that runs. Its user, send and receive tasks are skipped: no stage, no department, no user needed. Its events and gateways still apply; a gateway after a skipped task decides on the result of the last task that ran, so the person doing it waits for the outside party and records its answer. A call, subprocess or connector task in an ignored lane, a boundary on one of its tasks, and a loop whose way back holds only its tasks are `IGNORED_LANE_WORK`; a call's `Depts` cannot map a module lane to an ignored lane.

**System semantics**: tasks run in the order the flows reach them; a lane says who does a task, never when. Each stretch of a lane on one path becomes a `DepartmentDef`: the lane's first stretch is keyed by `lane.name`, a later one by `<lane.name>~<n>`, and every stretch has `lane.name` as its `Label` and the lane's `dept_id` as its IAM department. A flow that returns to a lane, a gateway branch and a boundary path each start a stretch of their own. Every `<bpmn:userTask>` must appear in exactly one lane, placed by `<bpmn:flowNodeRef>` membership. A task not listed in any lane is `TASK_NOT_IN_LANE`, and a lane name containing `/`, `::` or `~` is `INVALID_LANE_NAME`.

Additional `DepartmentDef` fields populated by the compiler:

| Field | Type | Description |
| --- | --- | --- |
| `Props` | `map[string]string` | Arbitrary key/value pairs from `<zeebe:properties>` on the lane element itself |

Limits: 1–100 lanes per process.

### Supported Elements

#### Tier 1 — Supported

| Element | System semantics |
|---|---|
| `<bpmn:collaboration>` | Wraps the pool that runs and outside parties' pools; only the one runnable pool compiles |
| `<bpmn:participant>` | Names a pool; an outside party's pool (ignored, or not executable and not called) produces nothing |
| `<bpmn:messageFlow>` | Names the message a receive task or message boundary in the pool that runs waits for |
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
| `<bpmn:callActivity>` | References a called process by `<zeebe:calledElement processId="..."/>`; the target is a process in the same file or a library module, resolved at publish; the called process compiles once into its own plan and the call to an `ExecutionStep.CallPlan` binding its open lanes to caller lanes (see §Module BPMNs) |
| `<bpmn:boundaryEvent cancelActivity="…">` + `timerEventDefinition` | SLA deadline → `StageDef.BoundaryTimer` (userTask), `SubWorkflowStep.TimerPaths` (subProcess) or `CallPlanStep.TimerPaths` (callActivity); `cancelActivity="false"` = non-interrupting, `"true"` = interrupting |
| `<bpmn:boundaryEvent>` + `errorEventDefinition` | Exception catch → `SubWorkflowStep.ErrorPaths` or `CallPlanStep.ErrorPaths`; attached to `subProcess` or `callActivity` |
| `<bpmn:boundaryEvent cancelActivity="…">` + `messageEventDefinition` | Inbound message interrupt → `StageDef.BoundaryMessage` (userTask), `SubWorkflowStep.MessagePaths` (subProcess), or `CallPlanStep.MessagePaths` (callActivity); attached to `userTask`, `subProcess`, or `callActivity`; zero outgoing flows is a valid terminal interrupt notification |
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
| `candidateUsers` | No | Single UUID v7 (one assignee). Absent is the `CANDIDATE_USERS_EMPTY` warning on the document; a published workflow gives every task a default, from here or from a calling callActivity's `Assignees`, and refuses publish (and `/validate`) otherwise. A global module names no user. |

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

Duration format: ISO 8601 (`PT48H`, `P3D`, `PT2H30M`, `P3W`) or Go duration (`48h`). Both forms are accepted and stored verbatim — the compiler does not normalise the value. The duration must be positive and short enough for the Execution Service to run as a timer (about 292 years at most); zero, negative and longer values are `INVALID_SLA_DURATION`.

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

**System semantics**: compiles to `StageDef.BoundaryMessage {message_name, interrupting, target_dept}` when attached to a `userTask`, to `SubWorkflowStep.MessagePaths` when attached to a `subProcess`, or to `CallPlanStep.MessagePaths` when attached to a `callActivity`. The message name is resolved from the boundary event's own `messageEventDefinition messageRef`, falling back to any collaboration `messageFlow` targeting the boundary event — see §Message Flow Name Resolution.

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
| Exactly one runnable process: executable, not called, not marked ignore | `MULTIPLE_PROCESSES` / `NO_RUNNABLE_PROCESS` |
| No call, subprocess or connector task in a lane marked ignore, no boundary on its tasks, no loop whose way back holds only them | `IGNORED_LANE_WORK` |
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
| `callActivity` has `<zeebe:calledElement processId="…"/>` with a literal `processId` that resolves to a process in the same definitions or to a published library module (with `versionTag`, that version) | `UNRESOLVED_CALLED_ELEMENT` |
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
| `candidateUsers` present — on a workflow's expanded plan at publish and `/validate`, or given by a calling callActivity's `Assignees` | `CANDIDATE_USERS_EMPTY` *(warning on the document; error at publish)* |
| `candidateUsers` is a single valid UUID v7, and is not set at all in a global module | `INVALID_CANDIDATE_USER` |

**Metadata — sendTask / receiveTask**

| Rule | Error code |
| --- | --- |
| Task appears in exactly one lane via `<bpmn:flowNodeRef>` | `TASK_NOT_IN_LANE` |
| `zeebe:assignmentDefinition` — optional; when present, same `candidateGroups`/`candidateUsers` rules as userTask apply | `CANDIDATE_GROUPS_EMPTY` / `INVALID_CANDIDATE_USER` |
| `messageRef` references a declared `<bpmn:message>` (structural rule) | `MISSING_MESSAGE_DEFINITION` |

**Metadata — callActivity (module / called process)**

| Rule | Error code |
| --- | --- |
| `<zeebe:calledElement processId="…"/>` present and `processId` resolves to a process in the file or a library module; a `versionTag` does not pin a process id the file also defines | `UNRESOLVED_CALLED_ELEMENT` |
| Called process has at least one `startEvent` | `NO_START_EVENT` (anchored on the callActivity node ID) |
| Every open lane (no `dept_id`) of the called process is bound, by `Depts` or, for a single open lane, by default to the call's lane | `MISSING_DEPT_INPUT_FOR_MODULE` |
| `Depts`/`Assignees` are JSON objects of strings naming lanes and tasks that exist — an `Assignees` key may be a `::`-joined path of calls to a task in a nested module — with UUID v7 users; no `dept_id` input | `INVALID_ZEEBE_PROPERTY` |
| In a workflow, every lane a module slot binds to has a `dept_id` | `MISSING_DEPT_ID` |
| No chain of calls returns to a process already on it | `CALLED_ELEMENT_CYCLE` (anchored on the callActivity that closes the cycle) |
| No two processes in the bundled document share an `id` | `DUPLICATE_PROCESS_ID` |
| A start event, task, send or receive task, subProcess, callActivity or boundary event has one outgoing flow; a flow splits only at a gateway | `SPLIT_WITHOUT_GATEWAY` |
| A timer or message boundary sits on a userTask, subProcess or callActivity; a node has one timer and one message boundary; an error boundary's `errorRef` names a `bpmn:error` | `INVALID_BOUNDARY_ATTACHMENT` |
| An end event has no outgoing flow | `END_EVENT_WITH_OUTGOING_FLOW` |
| Element ids are unique within a process | `DUPLICATE_ELEMENT_ID` |
| A task's `dueDate` and `followUpDate` are RFC 3339 date-times | `INVALID_TASK_SCHEDULE` |
| An exclusive split has a condition on every flow but one default flow | `GATEWAY_BRANCH_WITHOUT_CONDITION` (warning) |
| A receive task is never the source of a revert flow | `UNGUARDED_LOOP` |
| The called process's own body passes every rule in these tables, without the diagram rules | the body's own codes, anchored on its own nodes, or on the called process id for a whole-process rule |

Message, timer and error boundary events on `callActivity` compile to the call step's `message_paths`, `timer_paths` and `error_paths`. Every boundary names the department of the first task or subProcess on its path, or `terminates` at an end event; a path starting at a call or a message task is `UNSUPPORTED_BOUNDARY_TARGET`.

**Sequence flow conditions**

| Rule | Error code |
| --- | --- |
| `conditionExpression` on flows leaving an XOR split, ≤ 4096 chars | `INVALID_CONDITION_EXPRESSION` |

**Timer boundary events**

| Rule | Error code |
| --- | --- |
| Timer duration is a positive ISO 8601 or Go duration the Execution Service can run | `INVALID_SLA_DURATION` |
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
| `MISSING_MESSAGE_DEFINITION` | A message boundary event's message name cannot be resolved from either the `<bpmn:message>` definitions or the collaboration's message flows targeting that boundary event's ID — emitted per event; compilation continues with an empty correlation key hint |
| `MISSING_MESSAGE_DEFINITION` *(collaboration-level)* | A `<bpmn:messageFlow>`'s name cannot be resolved via any of the fallback paths in §Message Flow Name Resolution — emitted per flow; `MessageDef.Name` compiles to `""` |

### Validation Limits

| Constraint | Limit | Error code |
|---|---|---|
| Upload size | 10 MB | `PAYLOAD_TOO_LARGE` |
| XML token stream | 1,000,000 tokens | parse error → 400 |
| User tasks per process, stages the compile makes (with at most 10000 nodes visited), and stages plus calls once its calls are expanded | 1000 | `TASK_LIMIT_EXCEEDED` |
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
| `CANDIDATE_USERS_EMPTY` | `candidateUsers` absent: a warning on the document; an error at publish and `/validate` for any task of the expanded plan with no default, on the task or the top-level call that runs it |
| `INVALID_CANDIDATE_USER` | `candidateUsers` is not a valid UUID v7, or a global module names a user |
| `INVALID_CONDITION_EXPRESSION` | Condition expression exceeds 4096 chars |
| `INVALID_ZEEBE_PROPERTY` | callActivity: malformed `Depts`/`Assignees` input, an unknown lane, task or call path in them, an `Assignees` user that is not a UUID v7, or a retired `dept_id` input |
| `INVALID_SLA_DURATION` | Timer boundary duration is not a positive ISO 8601 or Go duration, or is too long to run |
| `MISSING_MESSAGE_DEFINITION` | `messageRef` on a flow or task references an undeclared `<bpmn:message>` |
| `UNMATCHED_MESSAGE_FLOW` | Message flow source/target does not reference a known element |
| `TASK_LIMIT_EXCEEDED` | User task count > 1000 in the process, a compile making more than 1000 stages or visiting more than 10000 nodes, or more than 1000 stages and calls once its calls are expanded |
| `LANE_LIMIT_EXCEEDED` | Lane count > 100 |
| `MULTIPLE_START_EVENTS` | More than one `startEvent` in the process |
| `MULTIPLE_END_EVENTS` | More than one `endEvent` (only raised when collaboration is absent and single process has > 1 end events without guarded exits) |
| `NO_START_EVENT` | Process has no `startEvent` |
| `NO_END_EVENT` | Process has no `endEvent` |
| `MULTIPLE_PROCESSES` | More than one process would run: executable, not called, not marked ignore |
| `NO_RUNNABLE_PROCESS` | No process would run: every one is marked ignore, not executable, or called |
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
| `UNRESOLVED_CALLED_ELEMENT` | `callActivity` names a `processId` that is an expression, is not in the file, or is not a published, unarchived library module the tenant can call (or not that `versionTag`); its `versionTag`/`bindingType` is invalid; or it pins a `versionTag` of a process id the file also defines |
| `NESTED_SUBPROCESS_NOT_SUPPORTED` | A `subProcess` is nested inside another `subProcess` |
| `MISSING_DEPT_INPUT_FOR_MODULE` | A called process has two or more open lanes and the call's `Depts` input does not map every one |
| `CALLED_ELEMENT_CYCLE` | A chain of `callActivity` calls returns to a process already on it |
| `DUPLICATE_PROCESS_ID` | Two processes in the bundled document share an `id` |
| `SPLIT_WITHOUT_GATEWAY` | A start event, task, send or receive task, subProcess, callActivity or boundary event has more than one outgoing flow |
| `END_EVENT_WITH_OUTGOING_FLOW` | An end event has an outgoing flow |
| `DUPLICATE_ELEMENT_ID` | Two elements of one process share an `id` |
| `INVALID_TASK_SCHEDULE` | A task's `dueDate` or `followUpDate` is not an RFC 3339 date-time |
| `GATEWAY_BRANCH_WITHOUT_CONDITION` | Warning: an exclusive split has more than one flow without a condition, so only the first is ever taken |
| `UNSUPPORTED_BOUNDARY_TARGET` | A boundary event's path reaches a `callActivity`, `sendTask` or `receiveTask` before any `userTask`, `subProcess` or end event, or a `subProcess` whose body does not start with a `userTask` |
| `MISSING_DEPT_ID` | A task's lane has no `dept_id`; a call binds a module lane to a workflow lane with no `dept_id`; or, at publish and `/validate`, a workflow has no lanes, or a task of its expanded plan runs in no IAM department |
| `INVALID_DEPT_ID` | A lane's `dept_id` is not a UUID |
| `INVALID_LANE_NAME` | A lane's `name` contains `/`, `::` or `~` |
| `IGNORED_LANE_WORK` | A lane marked ignore holds a call, subprocess or connector task, one of its tasks carries a boundary event, or a loop's way back holds only its tasks |
| `UNKNOWN_CONNECTOR_TYPE` | *(warning severity)* A connector `serviceTask` names a connector type the registry does not know |
| `CONNECTOR_TASK_HAS_ASSIGNMENT` | *(warning severity)* A connector `serviceTask` carries a `zeebe:assignmentDefinition`, which it ignores |
| `CONNECTOR_SECRET_AUTHORED` | A connector `serviceTask` input targets a secret field of its connector type; the tenant's stored credential is always used |
| `UNSUPPORTED_ELEMENT` | Tier 2 element present (e.g. `inclusiveGateway`) — parsed but compile handler absent |

> **Unparseable documents.** Documents that fail XML parsing (bad XML, `DOCTYPE`/entity injection, token-cap exceeded) return **400 `INVALID_BPMN_XML`** — not a BPMN validation code. Forbidden `DOCTYPE`/entity additionally triggers an internal security log with client IP and tenant ID; the client sees only the generic 400.

---

## Platform Libraries

The three BCBP platform libraries are private Go modules — see the table and upgrade instructions in [`.claude/CLAUDE.md`](.claude/CLAUDE.md). This section covers only the non-obvious integration points in this codebase; a read-only reference copy of each library's own source lives under `platform-libs/` (gitignored, never imported directly — see `.claude/CLAUDE.md`).

### platform-events (event envelope)

The service uses only `Envelope[T]` from this library: the inbound handler (`internal/adapter/inbound/http/handler/internal_events.go`) decodes the envelope the shared workflow-events consumer forwards to `POST /internal/events` (see [API Overview](README.md#api-overview) in the README). The service publishes nothing, so it uses neither the SNS publisher nor the outbox runner, and it does not use `NewSQSConsumer`. `cmd/server/telemetry.go` calls `events.InitMetrics` so `platform_library_info` identifies the library version in every scrape.

### platform-pgcommon (pool, RLS GUC injection, transactor, migrations)

`pgcommon.NewPool` takes a `GUCProvider` (set to `pgcommon.GUCSetFromContext`), which reads the `GUCSet` stored by `WithGUCSet(ctx, gs)` on every acquired connection — the `InjectGUCSet` middleware does this for every authenticated HTTP request; each internal handler calls it manually (see [HTTP middleware chain](#http-middleware-chain) and [flow 2](#2-internal-compiled-plan-read) above).

`Config.Logger` is wired via `cmd/server/pglogger.go`'s `pgCommonLogger` — a small composition-root-only adapter from this service's own `port.Logger` (map[string]any fields) to `platform-pgcommon/v2/pkg/domain.Logger` (variadic `domain.Field`), the library's public logger interface. `Config.Tracer` is `gincommon.NewSpanTracer(serviceName)`, so `db.query` spans export through the same pipeline as HTTP spans.

`cmd/server/wire.go` calls `pgmetrics.InitWithIdentity` before `pgcommon.NewPool`, with the identity gincommon resolved from `ObservabilityMiddlewares`. The pool then registers the `platform_db_*` collectors (`platform_db_queries_total`, `platform_db_pool_acquires_total`, `platform_db_transaction_retries_total`, `platform_db_slow_queries_total`, the pool gauges) against `gincommon.MetricsRegisterer()`.

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

**Migrations.** Managed by `migrate.Runner` (golang-migrate, `pgx/v5`). Files live in `db/migrations/` as `NNNNNN_description.up.sql`/`.down.sql` pairs, embedded via `db/migrations/migrations.go` (`//go:embed *.sql`). Run via the **`migrate` subcommand** (`go run ./cmd/server migrate` / `make migrate`) — never at server boot, since two replicas racing on golang-migrate's advisory lock would stall the loser and risk a readiness-probe timeout. `runMigrations` runs the service domain runner over `db/migrations/`, tracked under `wf_definition_migrations`, separate from any `pgcommon_migrations` history. Unlike goose, the pgx/v5 driver runs each migration file as a single statement, so `$$`-quoted PL/pgSQL blocks need no `StatementBegin/End` annotations.

### platform-gincommon (Gin middleware, logging, tracing)

`pkg/logger.NewLogger(appEnv)` returns a value that directly satisfies `port.Logger` — pass the same instance to `gincommon.Config{Logger: log}` as-is. `pgcommon.Config{Logger: ...}` takes a different shape (`platform-pgcommon/v2/pkg/domain.Logger`, variadic `domain.Field` instead of `map[string]any`), so `cmd/server/pglogger.go` wraps the same `log` instance in a small `pgCommonLogger` adapter first — see [platform-pgcommon](#platform-pgcommon-pool-rls-guc-injection-transactor-migrations) below. `APP_ENV=dev` → human-readable Zap output; anything else → JSON.

`gincommon.InitTracingWithConfig` runs once at startup, before `gincommon.ObservabilityMiddlewares` and the router, with the same `ServiceName`, `Domain` and `Environment` as the metrics identity. It reads `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_EXPORTER_OTLP_INSECURE`; an endpoint of `none` turns export off while trace and request IDs keep working.

Per-route authorization: every mutating handler begins with `mustAdminCtx`, which takes the request context and returns `403` unless `x-tenant-roles` holds `tenant_admin` or `tenant_owner`; writes to the global catalogue additionally call `requirePlatformOperator`. The check runs before the handler does any work.

