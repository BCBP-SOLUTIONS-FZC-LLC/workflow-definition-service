# Definition Service Low-Level Design

> This is the in-repo copy of the LLD (`docs/lld/`), kept content-identical to the design repo's published copy. The two are intentionally allowed to differ only on embed-vs-link mechanics (e.g. this copy may link to this repo's own `api/asyncapi.yaml` instead of embedding it) — never on content.

## 1. Service Overview & Responsibilities

The **Workflow Definition Service** acts as the design-time control plane for the BPMN-driven Workflow Engine. It is responsible for parsing, validating, and versioning business processes defined by tenant administrators. Unlike the Execution Service, which handles real-time runtime state, the Definition Service is heavily focused on ensuring the structural integrity, and immutable versioning of workflow templates before they are allowed to execute.

### 1.1 Technical Stack

- **Language**: Go 1.26
- **HTTP Framework**: Gin (REST APIs)
- **Shared Middleware Library**: `platform-gincommon v1.2.0` — provides correlation IDs, OTel HTTP/gRPC spans, Prometheus metrics, structured Zap logging, panic recovery, gateway header authentication, tenant context propagation, per-route RBAC, per-request timeout middleware, and graceful telemetry shutdown. See §1.6 for the full middleware integration.
- **Database**: PostgreSQL (schema: `workflow_definition`)
- **Database Driver**: `platform-pgcommon v1.1.1` — wraps `github.com/jackc/pgx/v5` with RLS GUC injection (`app.tenant_id`, `app.user_id`, `app.tenant_roles`), slow-query logging, OTel tracing, Prometheus pool metrics, transaction helpers, pool health check, and a golang-migrate-based `migrate.Runner`. See §1.7 for the full integration.
- **Database Tooling**: `sqlc` for type-safe query generation, `platform-pgcommon migrate.Runner` for schema migrations. Proto stubs generated via `buf`.
- **Distributed Cache / Store**: Valkey 8.0 (Redis-compatible), via `github.com/redis/go-redis/v9`. Used for: compiled-plan caching, idempotency key deduplication (`Idempotency-Key` header), and distributed draft-edit locking (prevents concurrent draft overwrites between sessions).
- **Event Bus (Outbound only)**: `platform-events v1.2.0` — provides typed event envelopes, SNS publisher (`events.SNSPublisher`), transactional outbox runner (`outbox.Runner`), and `outbox.ApplySchema` for programmatic outbox schema migration. The Definition Service is **API-only on the inbound path**: it no longer runs an SQS consumer. Inbound events (e.g. `department.membership.revoked`) are consumed by the **shared workflow-events consumer** service, which forwards them over HTTP to `POST /internal/events`. The AWS Glue SDK provides schema validation and encoding for outbound event payloads against the Glue Schema Registry. See §1.8 for the full integration.
- **Structured Logging**: `go.uber.org/zap` via `platform-gincommon/pkg/logger` — used by middleware chain and all application code. See `coding_style_rules.md §5.1`.
- **Mocks**: `go.uber.org/mock` (GoMock / `mockgen`) — generates mocks for all `core/port/` interfaces. See §1.5.3.

### 1.2 Key Integration Points

The Definition Service operates within a broader ecosystem and interacts with the following components:

1. **Frontend / UI**: Supplies the raw BPMN 2.0 XML from the canvas modeler for upload and template editing. During node authoring, the modeler calls Org & Membership directly (`GET /tenants/:id/users?department=:dept&level=:level`) to populate filtered user pickers.
2. **PostgreSQL**: Stores the compiled DSL, draft XMLs, historical versions, default node assignee mapping records (`workflow_node_assignee`), and transaction outbox events.
3. **Internal Backend Services (gRPC)**: Exposes a high-throughput gRPC endpoint for the Execution Service and Temporal Workers to fetch compiled JSON DSLs synchronously, avoiding REST parsing overhead and contract drift.
4. **Org & Membership Service (Outbound REST)**: During publish/execution transactions and validation passes, the Definition Service calls `POST /tenants/:t/users/:u/eligibility?department=:dept&level=:level` as a backend defense-in-depth check to ensure all default assignees are eligible.
5. **Event Bus (SNS — outbound only)**:
      - **Outbound**: Uses the Outbox Pattern to publish `workflow.template.published` events to the `wf.template.events` SNS topic. The outbox relay remains in-process (it is coupled to the transactional write path and the local DB).
      - **Inbound**: The Definition Service does **not** consume SQS. The shared workflow-events consumer subscribes to `membership-wf-q` (`iam.membership.events`) and forwards `department.membership.revoked` events to `POST /internal/events` (§7.4), which triggers template invalidation via the same logic as before.
6. **Shared Workflow-Events Consumer (Inbound HTTP)**: A separate engine-wide service consumes the SQS queues and HTTP-routes each envelope to the respective workflow service (Definition or Execution). For the Definition Service it calls the internal `POST /internal/events` endpoint.
7. **Execution Service (Outbound gRPC)**: The Definition Service makes two gRPC calls to the Execution Service. (a) During archive (`POST /workflows/:id/archive`): `CheckActiveInstances(workflow_id, tenant_id)` — rejects with `ACTIVE_INSTANCES_EXIST` (409) if any instances are RUNNING or PAUSED. (b) During membership revocation (`POST /internal/events`, §7.4.2): `PauseUserTasks(tenant_id, user_id)` — instructs Execution to pause active task assignments for the revoked user. Both calls wrap errors as `ErrUpstreamUnavailable` after retries.
8. **User Profile Service (Display Enrichment)**: The Definition Service stores only UUID references for users (`workflow_node_assignee.user_id`). It does **not** make outbound calls to User Profile — display enrichment (name, email, job title) is performed directly by the frontend calling `POST /api/v1/users:batch` with the UUID list returned by the Definition Service. This keeps the Definition Service identity-agnostic at the service layer.

### 1.3 High-Level Component Diagram

```mermaid
---
config:
  layout: elk
---
graph TD
    %% Actors and Entry
    UI[Frontend Modeler]
    Gateway[Envoy API Gateway]
    ExecSvc[Execution Svc / Workers]

    %% Definition Service Core
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

    %% External Systems
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

    %% Flows
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

### 1.4 Core Module Responsibilities & I/O Contracts

Below is a detailed breakdown of each internal module within the Workflow Definition Service boundary, detailing its responsibilities and precise Input/Output (I/O) contracts:

- **Gin HTTP API (Handler Layer)**
  - Implemented using the shared **`platform-gincommon`** library (`pkg/gincommon`, `pkg/grpccommon`, `pkg/logger`).
  - **Responsibility**: Exposes REST endpoints, validates incoming JSON/XML request schemas, extracts tenant and user context from gateway-injected headers, and formats structured HTTP responses.
  - **Input**: Raw HTTP request body (JSON/XML) and identity headers (`x-tenant-id`, `x-user-id`, `x-tenant-roles`, `x-departments`, `x-plan`, `x-feature-flags`).
  - **Output**: HTTP status codes (200, 201, 4xx, 5xx) and structured response payloads (JSON).
  - **Context Access**: After the `ContextMiddleware` runs, all handlers call `gincommon.RequestContext(c)` to obtain a fully-populated `*domain.RequestContext` carrying `TenantID`, `UserID`, `Roles`, and `TraceID`.
  - **RBAC**: Read-only endpoints (`GET`) require a valid `x-tenant-id`; mutating endpoints additionally check `x-tenant-roles` contains `tenant_admin` or `tenant_owner` via a custom `port.Authorizer` backed by the `RequirePermission` middleware.
  - **gRPC Observability**: The inbound `GetCompiledWorkflow` gRPC endpoint uses `grpccommon.DefaultUnaryInterceptors` + `DefaultStreamInterceptors` from `pkg/grpccommon` for Prometheus metrics on the gRPC server; additional auth/tracing interceptors are wired before these.

- **BPMN Parser**
  - **Responsibility**: Unmarshals raw BPMN 2.0 XML bytes into standard Go memory structures and extracts customized task metadata (`extensionElements`).
  - **Input**: Raw BPMN XML data (`[]byte`).
  - **Output**: Go BPMN Graph AST (`bpmn.Definitions` / `bpmn.Process` structs).

- **Validation Engine**
  - **Responsibility**: Performs comprehensive structural checks, metadata validation, and guarded-loop checks (classifying back-edges via DFS and using Tarjan's SCC to ensure every cycle has a guarded exit. see §4.4) on the parsed BPMN elements.
  - **Input**: Go BPMN Graph AST (`bpmn.Process` struct).
  - **Output**: Validation success status (`bool`) and a structured multi-error slice (`[]error`).

- **DSL Compiler**
  - **Responsibility**: Performs forward/backward adjacency mapping on the parsed node graph, groups sequential tasks, resolves split-to-join gateways, and compiles the result into the hierarchical JSON DSL structure.
  - **Input**: Validated Go BPMN Graph AST (`bpmn.Process` struct).
  - **Output**: Orchestration-ready compiled JSON DSL (`domain.WorkflowDef` struct).

- **Outbox Background Worker**
  - **Responsibility**: Implements the transactional outbox pattern runner to guarantee reliable event delivery to external downstream systems (e.g. SNS) utilizing the `platform-events` `outbox.Runner` to eliminate dual-write inconsistencies. This is the only background worker that remains in-process; the inbound SQS consumer has been extracted (see below).
  - **Input**: Pending records from the PostgreSQL `outbox_events` table.
  - **Output**: SNS event dispatch (to the `wf.template.events` topic) and a subsequent SQL update to flag records as published (or move to `outbox_dead_letters` after retries).

- **Internal Event Ingest Handler (`POST /internal/events`)**
  - **Responsibility**: Receives domain-event envelopes forwarded over HTTP by the shared workflow-events consumer (replacing the former in-process SQS consumer), dispatches by `env.Type`, and drives the same `HandleMembershipRevoked` invalidation logic. Idempotent via the `processed_event` dedup keyed on the envelope `id`; injects the RLS GUC from the envelope `tenant_id` (the gRPC pattern, §1.7.2 / §14).
  - **Input**: `events.Envelope[json.RawMessage]` JSON body on the internal (non-gateway) route group, authenticated by service-to-service auth.
  - **Output**: 2xx on success (incl. dedup no-op), 4xx on malformed payload, 5xx on transient error (signals the consumer to retry).

### 1.5 Internal Package Layout

The service follows clean architecture: nothing in `core/` imports from `adapter/`. The only place concrete adapters are wired to interfaces is `cmd/server/main.go`.

- **`workflow-definition-svc/`** (Project Root)
  - **`cmd/server/`**
    - `main.go` — Application bootstrap (concurrently starts the Gin HTTP server, the gRPC template service listener, and the Outbox background worker daemon) and dependency injection wire-up. The inbound SQS membership event consumer has been extracted to the shared workflow-events consumer; inbound events now arrive via `POST /internal/events`.
  - **`internal/`**
    - **`core/`** — Core business domain logic, fully decoupled from external libraries.
      - `domain/` — Core entities, workflow definitions, and compiled DSL models.
      - `port/` — Inbound and outbound interface contracts (e.g. `WorkflowRepository`).
      - `port/mocks/` — GoMock-generated mocks for all port interfaces. **Gitignored**; regenerated via `make mock`.
      - `service/` — Use case services orchestrating operations (e.g. publication).
    - **`adapter/`** — Outward-facing transport and driver implementations.
      - `inbound/http/` — Gin HTTP handlers, routes, and custom authentication middleware (using `platform-gincommon` helpers).
      - `inbound/http/authz/` — `WorkflowAuthorizer` implementing `port.Authorizer` (role-based write guard).
      - `inbound/http/middleware/` — Service-specific middleware: payload size limit, content-type check, idempotency key.
      - `inbound/grpc/` — Concrete implementation of the gRPC `DefinitionServiceServer`.
      - `outbound/postgres/` — sqlc-generated DB layer (`db/` sub-package, gitignored) and hand-written repo adapters.
    - **`bpmn_compiler/`** — Stateless XML parser, structural compilers, and graph cycle validators. Organised as four sub-packages:
      - `bpmn_compiler/` (root) — `Compiler` struct, `parse`, `securityScan`, `scanRejected`, `stagetype` registry, BPMN hasher.
      - `bpmn_compiler/bpmncore/` — Shared types (`BPMNDefinitions`, `CompileState`, graph), traversal engine, and `Compile` / `QualifyPlanDepts` entry points.
      - `bpmn_compiler/validator/` — All structural/semantic/topological validation functions (`Validate`, `ValidateDiagramCompleteness`, etc.).
      - `bpmn_compiler/element/` — `ElementHandler` implementations for each BPMN node type (start, end, user task, sub-process, gateways, boundary events).
    - **`config/`** — Env var loading into a typed `Config` struct; fails fast on missing required values.
  - **`proto/`** — Protobuf source files (committed).
    - `definition/v1/definition.proto` — Inbound `GetCompiledWorkflow` RPC (§3.4.1).
    - `execution/v1/execution_service.proto` — Outbound `CheckActiveInstances` client stub (§3.4.2).
  - **`gen/`** — buf-generated Go stubs. **Gitignored**; regenerated via `make generate`.
    - `proto/v1/` — Generated gRPC server/client interfaces.
  - **`db/`** — Database migrations and schema definitions.
    - `migrations/` — SQL schema migrations run via Goose. **Committed**.
    - `queries/` — SQL query definitions consumed by sqlc. **Committed**.
  - `sqlc.yaml` — Config file mapping schemas and queries to sqlc Go builders.
  - `buf.yaml` — buf lint and breaking-change rules.
  - `buf.gen.yaml` — buf code generation config (outputs to `gen/proto/`).
  - `.golangci.yml` — Linter configuration.
  - `docker-compose.yml` — Local infra: PostgreSQL 16, Valkey 8, LocalStack (SNS + SQS).
  - `.env.example` — All required env vars documented with defaults.
  - `Makefile` — Targets: `tools`, `generate`, `mock`, `migrate-up/down`, `build`, `test`, `test-integration`, `lint`, `docker-up/down`.

#### 1.5.1 Layer Responsibilities

- **core/domain**: Domain models for Workflow templates and DSL structure.
- **core/port**: Repository and event publisher interface contracts.
- **core/service**: Core business logic and use cases (draft, publish, archive).
- **adapter/inbound/http**: Request handlers, routes, custom Gin middleware.
- **adapter/inbound/grpc**: gRPC server handler serving compiled template definitions to downstream Execution services.
- **adapter/outbound/postgres**: Database persistence adapters (sqlc implementations).
- **bpmn_compiler**: XML parser, graph compiler, and validation rules engine.

#### 1.5.2 Generated File Strategy

All code-generated files are **gitignored** and must never be committed. They are regenerated in CI via `make generate` and `make mock`. Only the source files that drive generation are committed.

| Generated output | Source (committed) | Regenerate via |
| --- | --- | --- |
| `gen/proto/v1/` (Go gRPC stubs) | `proto/**/*.proto` | `make generate` (buf) |
| `internal/adapter/outbound/postgres/db/` (sqlc Go) | `db/queries/*.sql` | `make generate` (sqlc) |
| `internal/core/port/mocks/` (GoMock) | `internal/core/port/*.go` interfaces | `make mock` (mockgen) |

`.gitignore` entries covering these paths:

```gitignore
gen/
internal/adapter/outbound/postgres/db/
internal/core/port/mocks/
.env
bin/
.tools/
```

#### 1.5.3 Mock Strategy

GoMock (`go.uber.org/mock`, `mockgen`) is the project's mock framework. A mock is generated for every interface defined in `internal/core/port/`. Mocks live in `internal/core/port/mocks/` and are gitignored.

```text
make mock
  └─ mockgen -source=internal/core/port/repository.go   -destination=internal/core/port/mocks/repository_mock.go
  └─ mockgen -source=internal/core/port/publisher.go    -destination=internal/core/port/mocks/publisher_mock.go
  └─ mockgen -source=internal/core/port/cache.go        -destination=internal/core/port/mocks/cache_mock.go
  └─ ... (one invocation per port file)
```

Test files import the generated mocks directly — no hand-written test doubles.

#### 1.5.4 Docs Infrastructure

Developer docs are served via MkDocs (installed via `brew install mkdocs`). See [Appendix A] for full details.

### 1.6 Middleware Integration (`platform-gincommon`)

The Definition Service wires `platform-gincommon` in two tiers: observability middleware runs globally on every route (including `/healthz`, `/readyz`, and `/metrics`), while authentication and tenant-context middleware are scoped to the protected `/api` group only.

#### 1.6.1 HTTP Middleware Chain

```text
Incoming HTTP request
  → r.MaxMultipartMemory = 10 MB (Gin router setting) — caps multipart form in-memory buffer; pairs with
                                                         LimitRequestBody for large-body protection
  → TimeoutMiddleware(30s)      (gincommon) — sets per-request deadline; returns 503 + increments
                                              http_request_timeout_total{method,route} on expiry
  → PanicRecoveryMiddleware     (gincommon) — catches panics, emits JSON 500 + http_panic_total metric
  → RequestIDMiddleware         (gincommon) — reads x-request-id or generates UUID; echoes on response
  → TracingMiddleware           (gincommon) — starts OTel HTTP server span; stores trace_id in Gin context
  → CorrelationHeadersMiddleware(gincommon) — writes X-Trace-ID and X-Request-ID onto the response
  → MetricsMiddleware           (gincommon) — records http_requests_total, http_request_duration_seconds
  → LoggingMiddleware           (gincommon) — emits structured http_request log via Zap on completion
  → [/healthz, /readyz, /metrics — public; no further middleware]
  → RequireAuth                 (gincommon) — validates x-user-id and x-tenant-id (non-empty, ≤ 256 chars,
                                              no control chars); returns 401 if missing or malformed
  → ContextMiddleware           (gincommon) — builds RequestContext{TenantID, UserID, Roles, TraceID}
                                              and stores it in the Gin context for handler access
  → InjectGUCSet                (service)   — reads RequestContext from Gin context; calls
                                              pgcommon.WithGUCSet(ctx, GUCSet{TenantID, UserID, Roles})
                                              so the pgx pool injects app.tenant_id / app.user_id GUCs
                                              on every connection. Without this bridge, RLS fires against
                                              a NULL GUC and returns zero rows. Must follow ContextMiddleware.
  → LimitRequestBody            (service)   — wraps c.Request.Body with http.MaxBytesReader(10 MB);
                                              reads exceeding the limit return *http.MaxBytesError →
                                              errResponse maps to 413 PAYLOAD_TOO_LARGE
  → [read endpoints — GET /workflows, GET /workflows/:id, ...]
  → [mutating endpoints — POST / PUT / DELETE]
  → RequirePermission("write", "workflow", authz) (gincommon) — checks port.Authorizer backed by
                                                                 x-tenant-roles header; returns 403 if
                                                                 caller lacks tenant_admin or tenant_owner
  → handler
```

```mermaid
  flowchart TD
    A[Incoming HTTP Request]
        --> B[Core Middleware]

    subgraph B [Core Middleware]
        B1[Panic Recovery]
        B2[Request ID]
        B3[Tracing]
        B4[Correlation Headers]
        B5[Metrics]
        B6[Logging]

        B1 --> B2 --> B3 --> B4 --> B5 --> B6
    end

    B --> C{Endpoint Type}

    C --> D[Public Endpoints]
    D --> D1["/healthz<br/>/readyz<br/>/metrics"]

    C --> E[Authenticated Endpoints]

    E --> F[RequireAuth]
    F --> G[ContextMiddleware]
    G --> GG[InjectGUCSet]
    GG --> GH[LimitRequestBody]

    GH --> H{Operation Type}

    H --> I[Read Endpoints]
    I --> J[Handler]

    H --> K[Mutating Endpoints<br/>POST / PUT / DELETE]
    K --> L["RequirePermission<br/>('write','workflow')"]
    L --> J

    classDef middleware fill:#eef,stroke:#447;
    classDef auth fill:#efe,stroke:#474;
    classDef handler fill:#fee,stroke:#744;

    class B1,B2,B3,B4,B5,B6 middleware;
    class F,G,GG,GH,L auth;
    class J handler;
```

`gincommon.InitTracingFromEnv()` is called once at startup in `main.go` before traffic is accepted; the returned shutdown function is deferred.

**Graceful shutdown sequence** (LIFO):

```text
1. OS signal received
2. HTTP server: stop accepting new connections (httpServer.Shutdown)
3. gRPC server: drain in-flight RPCs (grpcServer.GracefulStop)
4. gincommon.Shutdown(logger)   — flushes OTel HTTP spans + Zap buffer (platform-gincommon v1.2.0)
5. grpccommon.Shutdown(logger)  — flushes OTel gRPC spans (platform-gincommon v1.2.0)
6. Outbox relay: runner.Stop()
7. DB pool: pool.Close()
```

Steps 4–5 must run before pool.Close() to ensure any in-flight DB-related spans are flushed before the pool drains.

**`HTTP_REQUEST_TIMEOUT_SEC` env var** (default `30`): configures the `TimeoutMiddleware` deadline. A request exceeding this deadline is cancelled and returns 503; `http_request_timeout_total` is incremented to distinguish these from other 5xx errors.

#### 1.6.1.1 Logger Adapter (`portLogger`)

`platform-gincommon`'s `Config.Logger` field requires a `port.Logger` interface (`Debug/Info/Warn/Error/Fatal(msg string, fields map[string]interface{})`). Application code and service layers use `*zap.Logger` directly for typed field calls (`zap.String(...)`, `zap.Error(...)`).

To satisfy both contracts with a single logger instance, `cmd/server/main.go` constructs one `*zap.Logger` directly (via `zap.NewDevelopment()` or `zap.NewProduction()`) and wraps it in a thin `portLogger` adapter:

```go
type portLogger struct{ z *zap.Logger }

func (p *portLogger) Info(msg string, fields map[string]interface{}) {
    p.z.Info(msg, toZapFields(fields)...)
}
// ... Debug, Warn, Error, Fatal implemented the same way
```

The adapter converts `map[string]interface{}` to `[]zap.Field` via `zap.Any`. All logs — middleware and application — flow through the same `*zap.Logger` instance with consistent format and level configuration.

> **Why not use `logger.NewLogger()`?** The `pkg/logger.NewLogger()` factory returns `port.Logger` (concrete type is an unexported `*ZapLogger` struct). There is no public accessor to retrieve the underlying `*zap.Logger`, making type assertion impossible. Building `*zap.Logger` directly and adapting it avoids this limitation.

#### 1.6.2 gRPC Interceptor Chain (Inbound `GetCompiledWorkflow`)

```text
Incoming gRPC call
  → grpccommon.DefaultUnaryInterceptors(cfg)  — Prometheus metrics: grpc_server_handled_total,
                                                grpc_server_handling_seconds, inflight requests
  → [custom auth interceptor]                 — validates tenant_id from request payload before
                                                any DB access; returns PERMISSION_DENIED if absent
  → handler: DefinitionServiceServer.GetCompiledWorkflow
```

`pkg/grpccommon` provides **metrics only**. OTel tracing and authentication interceptors are added in the service's own `main.go` before these interceptors, following gincommon's documented composition pattern.

#### 1.6.3 gincommon Environment Variables (Definition Service)

| Variable | Value (production) | Purpose |
| --- | --- | --- |
| `APP_ENV` | `prod` | Sets Gin release mode; Zap production format; OTLP TLS enabled |
| `BUILD_VERSION` | Set via `-ldflags` at build | Prometheus `build_info` gauge and OTel `service.version` |
| `OTEL_SERVICE_NAME` | `workflow-definition-svc` | OTel `service.name` attribute on all spans |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel-collector:4317` | OTLP/gRPC collector address (in-cluster) |
| `OTEL_EXPORTER_OTLP_INSECURE` | `false` | TLS required in production (collector uses mutual TLS) |
| `OTEL_TRACES_SAMPLER_RATIO` | `0.1` | 10 % head sampling; increase to `1.0` for full trace debugging |

#### 1.6.4 Response Headers (gincommon-injected)

Every HTTP response carries:

| Header | Source | Value |
| --- | --- | --- |
| `X-Request-ID` | `RequestIDMiddleware` | Echo of `x-request-id` or generated UUID |
| `X-Trace-ID` | `CorrelationHeadersMiddleware` | OTel trace ID (hex string) |

Handler code retrieves tenant and trace context via:

```go
reqCtx, ok := gincommon.RequestContext(c)
if !ok {
    // This path is unreachable after ContextMiddleware; treat as 500
}
// reqCtx.TenantID, reqCtx.UserID, reqCtx.Roles, reqCtx.TraceID available
```

---

### 1.7 Database Pool Integration (`platform-pgcommon`)

The Definition Service uses `platform-pgcommon` as the single source of truth for PostgreSQL connectivity. A single `*pgcommon.Pool` is created at startup, passed into any component that needs a DB connection, and closed during graceful shutdown.

#### 1.7.1 Pool Construction

`pgcommon.NewPool` is called once in `cmd/server/main.go` after config is loaded. It pings the database at construction — if unreachable, startup fails immediately.

```go
pool, err := pgcommon.NewPool(ctx, pgcommon.Config{
    DSN:                 cfg.DatabaseURL,
    MaxConns:            cfg.PGMaxConns,
    MinConns:            cfg.PGMinConns,
    SlowQueryThreshold:  cfg.PGSlowQueryThreshold,
    GUCProvider:         pgcommon.GUCSetFromContext,
    AllowFullStatements: false,   // safe default: emit only SQL verb in OTel spans (no PII leakage)
    PGBouncerMode:       cfg.PGBouncerMode, // set MinConns=0 automatically under PgBouncer transaction-pooling
})
```

> **`Config.Logger` and `Config.Tracer` are not wired.** Both use `port.Logger`/`port.Tracer` from `platform-pgcommon/internal/core/port/` — an `internal` package that external services cannot import. Slow-query events appear in structured Zap logs via the `SlowQueryThreshold` mechanism; per-query OTel spans are not available until the library exports these interfaces.

`pool.Close()` is deferred in the shutdown hook (step 8 in the shutdown sequence) to drain in-use connections after all servers and the outbox relay have stopped.

#### 1.7.2 RLS GUC Injection

`platform-pgcommon` injects PostgreSQL GUC parameters (`app.tenant_id`, `app.user_id`, `app.tenant_roles`) automatically on every connection acquired from the pool, using the value stored in the request context by the GUC middleware.

**Middleware responsibility** (implemented in `internal/adapter/inbound/http/middleware/gucrls.go`):

```go
// Runs after gincommon.ContextMiddleware in the /api group.
func GUCMiddleware(pool *pgcommon.Pool) gin.HandlerFunc {
    return func(c *gin.Context) {
        reqCtx, _ := gincommon.RequestContext(c)
        ctx := pgcommon.WithGUCSet(c.Request.Context(), pgcommon.GUCSet{
            TenantID:    reqCtx.TenantID,
            UserID:      reqCtx.UserID,
            TenantRoles: reqCtx.Roles,
        })
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

#### 1.7.3 Readiness Check

`/readyz` calls `pool.Health(ctx)` to check the database pool and `cache.Ping(ctx)` to check Valkey. Returns `503 Service Unavailable` if either dependency is unreachable.

```go
func readyzHandler(pool *pgcommon.Pool, cache port.CacheStore) gin.HandlerFunc {
    return func(c *gin.Context) {
        hs := pool.Health(c.Request.Context())
        if !hs.Healthy {
            c.JSON(http.StatusServiceUnavailable, gin.H{
                "status": "unavailable",
                "db":     "unreachable",
            })
            return
        }
        if err := cache.Ping(c.Request.Context()); err != nil {
            c.JSON(http.StatusServiceUnavailable, gin.H{
                "status": "unavailable",
                "cache":  "unreachable",
            })
            return
        }
        c.JSON(http.StatusOK, gin.H{
            "status":         "OK",
            "db_utilization": hs.Utilization,
            "db_conns":       hs.AcquiredConns,
            "db_max_conns":   hs.MaxConns,
        })
    }
}
```

#### 1.7.4 Environment Variables

| Variable | Default | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | — | Full pgx DSN (`postgres://user:pass@host:5432/db`) |
| `PG_MAX_CONNS` | `10` | Pool ceiling (pgcommon default) |
| `PG_MIN_CONNS` | `2` | Minimum idle connections (set `0` when `PG_BOUNCER_MODE=true`) |
| `PG_SLOW_QUERY_THRESHOLD_MS` | `200` | Slow-query log threshold in milliseconds |
| `PG_BOUNCER_MODE` | `false` | Enable PgBouncer transaction-pooling mode; sets MinConns=0 automatically |
| `HTTP_REQUEST_TIMEOUT_SEC` | `30` | Per-request deadline enforced by `TimeoutMiddleware` |

#### 1.7.5 Prometheus Metrics

`pgmetrics.Init(serviceName, version)` from `platform-pgcommon/pkg/pgmetrics` registers the following Prometheus collectors on startup:

| Metric | Type | Description |
| --- | --- | --- |
| `pg_pool_acquire_total` | Counter | Successful / failed connection acquires |
| `pg_pool_acquire_duration_seconds` | Histogram | Time waiting for a connection from the pool |

These are registered globally alongside the gincommon HTTP metrics and exposed on the `/metrics` endpoint.

### 1.8 Events Integration (`platform-events`)

The Definition Service uses the `platform-events` library to implement event publishing, message consumption, and the transactional outbox pattern.

#### 1.8.1 Initialization & Schema Migration

Schema migrations run **programmatically at process startup** via two platform runners before any application logic executes:

```go
// 1. Outbox tables (tracked under "outbox_migrations")
if err := outbox.ApplySchema(ctx, &migrate.Runner{DSN: cfg.DatabaseURL}); err != nil {
    return nil, fmt.Errorf("outbox schema: %w", err)
}

// 2. Service domain tables + RLS policies (tracked under "wf_definition_migrations")
svcRunner := &migrate.Runner{
    FS:              servicemigrations.FS,
    DSN:             cfg.DatabaseURL,
    MigrationsTable: "wf_definition_migrations",
}
if err := svcRunner.Up(ctx); err != nil {
    return nil, fmt.Errorf("service migrations: %w", err)
}
```

**Why this order matters:** migration `0005_outbox_rls` adds `ENABLE ROW LEVEL SECURITY` and `CREATE POLICY` to `outbox_events` and `outbox_dead_letters`. Those tables must exist first, hence `outbox.ApplySchema` runs before the service runner.

**Three isolated tracking tables** coexist in the same database with no version-number collisions:

| Runner | Tracking table | Who runs it |
| --- | --- | --- |
| `platform-pgcommon` internal migrations | `pgcommon_migrations` | `platform-pgcommon` (auto, if any) |
| `platform-events outbox.ApplySchema` | `outbox_migrations` | Called at service startup |
| Service domain migrations | `wf_definition_migrations` | Called at service startup |

**Migration file format:** `db/migrations/NNNN_name.up.sql` / `NNNN_name.down.sql` pairs (golang-migrate convention). No goose annotations. The pgx/v5 driver executes each file as a single unit, so PL/pgSQL function bodies with semicolons inside `$$...$$` blocks work without any special delimiters.

**RLS policies for outbox tables** are a service migration (`0005_outbox_rls.up.sql`), not embedded in `platform-events`. They are service-specific policy decisions that must be versioned alongside the rest of the service schema.

On application startup the metrics and tracing initialisation happens before migrations:

```go
events.Init(cfg.OTELServiceName, cfg.BuildVersion)
pgmetrics.Init(cfg.OTELServiceName, cfg.BuildVersion)
// then: outbox.ApplySchema → svcRunner.Up → newDBPool → wire services
```

#### 1.8.2 SNS Publisher & Outbox Runner

The SNS Publisher is initialized to publish event envelopes. To guarantee reliable at-least-once delivery, events are enqueued via `outbox.Enqueue` inside business transactions and dispatched asynchronously by the `outbox.Runner`:

```go
publisher, err := events.NewSNSPublisher(events.SNSConfig{
    TopicARN: cfg.SNSTopicARN,
    Region:   cfg.AWSRegion,
    Logger:   logger,
})
if err != nil {
    log.Fatal(err)
}

runner := outbox.NewRunner(outbox.Config{
    Pool:         pool,
    Publisher:    publisher,
    Logger:       logger,
    PollInterval: cfg.OutboxPollInterval,
    BatchSize:    cfg.OutboxBatchSize,
})
go runner.Start(ctx)
defer runner.Stop()
```

All outbound event envelopes are constructed via `buildEnvelope` in `internal/core/service/helpers.go`. Each envelope includes `WithTenantID` and `WithTraceID` (when an active OTel span is present in context). Add `events.WithSchemaVersion("1")` to all `buildEnvelope` calls and increment on additive payload changes. Prior to enqueuing, payloads are JSON-serialized and validated/encoded against the AWS Glue Schema Registry using the `GlueCodec` (§7.2.5).

```go
runner := outbox.NewRunner(outbox.Config{
    // existing fields ...
    ClaimLeaseDuration: cfg.OutboxClaimLeaseDuration, // default 10m — prevents duplicate processing
    StartupJitter:      cfg.OutboxStartupJitter,       // default 5s — staggers multi-instance startup
    PublishConcurrency: cfg.OutboxPublishConcurrency,  // default 5 — parallel SNS publish goroutines
    PublishTimeout:     cfg.OutboxPublishTimeout,      // default 10s — per-record SNS call timeout
    DrainTimeout:       cfg.OutboxDrainTimeout,        // default 30s — wait for in-flight publishes on Stop
})
```

`outbox.Runner.ReprocessDeadLetters(ctx context.Context, limit int) (int, error)` is available in `platform-events v1.2.0` as an admin/ops call to move up to `limit` records from `outbox_dead_letters` back to `outbox_events` for redelivery, resetting attempt counters. Wiring it as an HTTP admin endpoint is pending.

#### 1.8.3 Inbound Event Ingest (HTTP)

Inbound domain events are consumed by the shared workflow-events consumer service, which forwards each envelope over HTTP to `POST /internal/events`. The handler injects the RLS tenant context (`pgcommon.WithGUCSet`) from the envelope `tenant_id` — the same manual-injection pattern used by the gRPC `GetCompiledWorkflow` path (§1.7.2 / §14) — and dispatches by `env.Type` to the existing handler logic (e.g. `HandleMembershipRevoked`). Idempotency is preserved by the `processed_event` dedup keyed on the envelope `id`. See §7.4 for the full flow.

#### 1.8.4 Environment Variables

| Variable | Default | Purpose |
| --- | --- | --- |
| `AWS_REGION` | `us-east-1` | AWS Region for SNS client construction |
| `SNS_TOPIC_ARN` | — | Topic ARN to publish workflow template events |
| `OUTBOX_POLL_INTERVAL` | `500ms` | Polling interval for processing pending outbox records |
| `OUTBOX_BATCH_SIZE` | `50` | Maximum number of records processed per poll cycle |
| `OUTBOX_CLAIM_LEASE_DURATION` | `10m` | Lease duration to prevent duplicate processing across replicas |
| `OUTBOX_STARTUP_JITTER` | `5s` | Random delay added to first poll to stagger multi-replica startup |
| `OUTBOX_PUBLISH_CONCURRENCY` | `5` | Parallel goroutines for SNS publish per batch |
| `OUTBOX_PUBLISH_TIMEOUT` | `10s` | Per-record SNS call timeout |
| `OUTBOX_DRAIN_TIMEOUT` | `30s` | Grace period for in-flight SNS publishes when the outbox runner stops |
| `GLUE_REGISTRY_NAME` | `workflow-template-events` | Glue Schema Registry name for `wf.template.events` payload validation |
| `GLUE_REGISTRY_ARN` | — | Full ARN; used for IAM policy scoping |

#### 1.8.5 Prometheus Metrics

The library registers the following Prometheus metrics under the hood during `events.Init`:

| Metric | Type | Description |
| --- | --- | --- |
| `events_published_total` | Counter | Total published events |
| `events_publish_duration_seconds` | Histogram | Publish call latency |
| `internal_events_ingest_total` | Counter | Inbound envelopes received at `POST /internal/events` (labels: `type`, `result`) — replaces the former `events_consumed_total` SQS metric |
| `outbox_pending_total` | Gauge | Active queue records pending dispatch |
| `outbox_dead_letters_total` | Counter | Failed events transitioned to dead letters |

---

**Import rules enforced by `go-arch-lint`**:

- `core/domain` imports nothing outside stdlib.
- `core/port` imports only `core/domain`.
- `core/service` imports only `core/domain` and `core/port`.
- `adapter/*` imports `core/port` (to implement) and `core/domain` (types).
- `bpmn_compiler/bpmncore` imports `core/domain` only.
- `bpmn_compiler/validator` imports `core/domain` and `bpmn_compiler/bpmncore`.
- `bpmn_compiler/element` imports `core/domain`, `bpmn_compiler/bpmncore`, and `bpmn_compiler/validator`.
- `bpmn_compiler` (root) imports all three sub-packages plus `core/domain` and `core/port`.
- Nothing in `core/` or `bpmn_compiler/` imports from `adapter/`.

#### 1.5.2 Dependency Direction and Component Class Diagram

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
        +InsertEvent()
        +FetchPending()
        +MarkProcessed()
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

##### Core Component Descriptions

- **HTTPHandler (Inbound Adapter)**: Exposes REST APIs, enforces size limits (10MB) on incoming payloads, validates request schemas, and delegates workflow management use cases to core services.
- **HTTPMiddleware (Inbound Adapter)**: Extracts gateway-injected identity headers (`x-tenant-id`, `x-user-id`, `x-tenant-roles`, etc.) and manages transactional session boundaries (such as configuring the PostgreSQL GUC for RLS).
- **PublisherService (Core Application Service)**: Orchestrates the draft compilation and publish lifecycle. Initiates database transactions, calls the validation engine and compiler, verifies assignee eligibility via external ports, and writes transactional outbox logs.
- **DraftCompiler (Domain Service)**: Traverses the unmarshalled BPMN sequence flows to construct the compiled JSON DSL execution plan for the runtime.
- **Validator (Domain Service)**: Runs topological correctness rules (back-edge classification, guarded-loop checking, and gateway matching over the forward graph — see §4.4) on the parsed process definition.
- **OutboxRelay (Background Worker)**: worker that polls the transactional outbox table and dispatches pending event payloads to the event bus (Amazon SNS).
- **Ports (WorkflowRepository, PlanService, MembershipService, OutboxRepository)**: Explicit interface contracts isolating the core business logic from database schema libraries (`sqlc`) and external HTTP/gRPC networks.

---

#### 1.5.3 Key Component File Mapping

- **publish.go**: Entrypoint orchestrator for publishing a draft workflow version.
- **validator.go**: Structural, semantic, metadata, and guarded-loop validation (back-edge classification + Tarjan SCC).
- **compiler.go**: Graph traversal engine mapping BPMN sequence flows to the JSON DSL execution steps.
- **outbox_repo.go**: Database persistence layer for appending and fetching outbox event logs.
- **relay.go**: Background daemon executing polling, backoff, and dispatching loops.

#### 1.5.4 Interface Contracts (Ports)

To maintain clean architecture, core services rely on explicitly defined port interfaces located in `core/port/`:

- **`WorkflowRepository`**: Handles transaction boundaries and schema interactions (`GetDraft`, `CreateVersion`, `UpdateActiveVersion`).
- **`OutboxRepository`**: Handles generic outbox appending and transactional retrieval (`InsertEvent`, `FetchPending`, `MarkSent`).
- **`EventPublisher`**: Cloud-agnostic event distribution contract implemented by the SNS adapter (`PublishMessage`).
- **`Transactor`**: Wraps `pgcommon.RunInTx`. The publish path requires `SERIALIZABLE` isolation with retry on `40001`/`40P01` — the port interface must expose a `RunInTxWithRetry` method (or accept `pgx.TxOptions` + retry config) so `VersionService.Publish` can use `pgcommon.RunInTxWithRetry` without importing pgcommon directly. See §10.8.

---

### 1.9 Valkey Cache Integration

The service uses Valkey 8.0 (Redis-compatible, via `github.com/redis/go-redis/v9`) as an in-process performance layer and distributed coordination store. It is a pure operational dependency — it holds no primary data and is not part of the recovery point objective (RPO).

#### 1.9.1 What IS Cached

| Purpose | Key format | TTL | Invalidated by |
| --- | --- | --- | --- |
| Compiled workflow plan | `wf:plan:<tenantID>:<versionID>` | 1 h | Publish, promote, or discard (key deleted on state change) |
| Idempotency key | `idem:<tenantID>:<routePath>:<idempotency-key>` | 24 h | Not invalidated — expires naturally; only 2xx responses are cached |
| Draft-edit lock | `wf:draft:lock:<tenantID>:<workflowID>` | 30 s (sliding) | Released on successful save or session expiry |

The compiled-plan cache is populated lazily: on the first `GetCompiledWorkflow` gRPC call (or `GET /draft` / `GET /version`) after a version is published, the plan is fetched from Postgres and written to Valkey. Subsequent requests on the same version hit the cache without a DB round-trip.

**Valkey client timeout**: 50 ms. A timeout is treated as a cache miss and the request falls through to Postgres, so Valkey latency can never block the HTTP or gRPC SLO budget (see §9.3).

#### 1.9.2 What is NOT Cached

- **Workflow list / metadata** — `GET /workflows`, `GET /workflows/:id` always reads from Postgres.
- **Raw `bpmn_xml`** — stored only in Postgres; never cached.
- **Workflow version history** — always from Postgres.
- **Outbox events and `processed_event` records** — DB-only for durability and dedup guarantees.
- **Assignee eligibility** — checked live against Org & Membership at publish time; not cached.

#### 1.9.3 Cache Failure Behaviour

| Scenario | Behaviour |
| --- | --- |
| Compiled-plan cache miss (normal) | Falls through to Postgres; response is back-filled into Valkey before returning |
| Idempotency cache set failure | Logged at `WARN`; request continues without caching — duplicate protection is degraded, not lost |
| Draft-edit lock: Valkey unavailable | Lock acquisition fails; handler returns `503 Service Unavailable` rather than silently proceeding without the lock |
| Valkey fully down | `/readyz` returns `503 {"status":"unavailable","cache":"unreachable"}` (§3.3.16); pod is removed from rotation |

#### 1.9.4 Environment Variables

| Variable | Default | Purpose |
| --- | --- | --- |
| `VALKEY_ADDR` | — | ElastiCache endpoint (host:port) |
| `CACHE_COMPILED_PLAN_TTL` | `1h` | TTL for compiled-plan cache entries |
| `CACHE_IDEMPOTENCY_TTL` | `24h` | TTL for idempotency response cache |
| `CACHE_DRAFT_LOCK_TTL` | `30s` | TTL for distributed draft-edit lock |
| `VALKEY_CLIENT_TIMEOUT` | `50ms` | Per-command timeout; treat timeout as cache miss |

---

## 2. Database Schema

The database uses PostgreSQL Row-Level Security (RLS) to enforce strict multi-tenant isolation. Under this system, all queries are automatically filtered by `tenant_id` at the database level, preventing cross-tenant data leaks.

### 2.1 Table Schema Definitions

```mermaid
erDiagram
    direction LR

    WORKFLOW {
        UUID id PK
        UUID tenant_id
        UUID created_by_user_id
        VARCHAR business_key
        VARCHAR name
        TEXT description
        UUID active_version_id FK
        BIGINT record_version
        TIMESTAMP created_at
        TIMESTAMP updated_at
    }

    WORKFLOW_VERSION {
        UUID id PK
        UUID workflow_id FK
        UUID tenant_id
        ENUM status
        TEXT bpmn_xml
        JSONB compiled_plan_json
        TEXT artifact_hash
        INT version_number
        TIMESTAMP published_at
        UUID created_by_user_id
        BOOLEAN is_valid
        JSONB validation_errors_json
        BIGINT record_version
        TIMESTAMP created_at
        TIMESTAMP updated_at
    }

    WORKFLOW_NODE_ASSIGNEE {
        UUID id PK
        UUID tenant_id
        UUID workflow_version_id FK
        VARCHAR node_key
        UUID user_id
        TEXT department_id
        VARCHAR role
        TIMESTAMP created_at
    }

    WORKFLOW ||--o{ WORKFLOW_VERSION : has_versions
    WORKFLOW_VERSION ||--o{ WORKFLOW_NODE_ASSIGNEE : defines_assignees
    WORKFLOW ||--o| WORKFLOW_VERSION : active_version
```

Reusable modules and starter workflows (§3.3.18/§3.3.19, §10.16) — absorbed here from the retired "BE-for-UI" placement (`execution_service.md` Appendix A.2 #31, RESOLVED rev 1.34):

```mermaid
erDiagram
    direction LR

    WORKFLOW_MODULE {
        UUID id PK
        UUID tenant_id "NULL iff scope='global'"
        VARCHAR scope "'global' | 'tenant'"
        VARCHAR name
        TEXT description
        UUID active_version_id FK
        BIGINT record_version
        TIMESTAMP created_at
        TIMESTAMP updated_at
    }

    WORKFLOW_MODULE_VERSION {
        UUID id PK
        UUID module_id FK
        UUID tenant_id "denormalized from parent, NULL iff scope='global'"
        VARCHAR scope
        ENUM status "DRAFT | PUBLISHED | ARCHIVED"
        TEXT bpmn_xml
        VARCHAR process_id "the bpmn:process id callActivity references"
        INT version_number
        BOOLEAN is_valid
        JSONB validation_errors_json
        TIMESTAMP published_at
        UUID created_by_user_id
        BIGINT record_version
        TIMESTAMP created_at
    }

    WORKFLOW_STARTER_TEMPLATE {
        UUID id PK
        UUID tenant_id "NULL iff scope='global'"
        VARCHAR scope "'global' | 'tenant'"
        VARCHAR name
        TEXT description
        VARCHAR category
        TEXT bpmn_xml
        UUID source_workflow_version_id "informational only, no FK cascade"
        UUID created_by_user_id
        BIGINT record_version
        TIMESTAMP created_at
        TIMESTAMP updated_at
    }

    WORKFLOW_MODULE ||--o{ WORKFLOW_MODULE_VERSION : has_versions
    WORKFLOW_MODULE ||--o| WORKFLOW_MODULE_VERSION : active_version
```

```mermaid
erDiagram
    direction TB

    OUTBOX_EVENTS {
        UUID id PK
        TEXT event_type
        JSONB payload
        TEXT tenant_id
        TEXT trace_id
        INT attempts
        TEXT last_error
        TIMESTAMP created_at
        TIMESTAMP scheduled_at
        TIMESTAMP published_at
    }

    OUTBOX_DEAD_LETTERS {
        UUID id PK
        TEXT event_type
        JSONB payload
        TEXT tenant_id
        TEXT trace_id
        INT attempts
        TEXT last_error
        TIMESTAMP created_at
        TIMESTAMP failed_at
    }

    PROCESSED_EVENT {
        UUID event_id PK
        TEXT consumer PK
        TEXT event_type
        TIMESTAMP processed_at
    }
```

The full database DDL migrations and RLS policies are documented in [Appendix A: Database Migration Scripts](#appendix-a-database-migration-scripts).

---

### 2.2 Data Model

#### `workflow`

| Column | Type | Notes |
| --- | --- | --- |
| `id` | UUID | Primary key |
| `tenant_id` | UUID | Tenant isolation key |
| `created_by_user_id` | UUID | Creator (admin) |
| `business_key` | VARCHAR(255) | Business identifier (e.g. `tender-review`) |
| `name` | VARCHAR(255) | Workflow template name |
| `description` | TEXT | Optional description |
| `active_version_id` | UUID | Foreign key pointing to the current published version |
| `record_version` | BIGINT | Monotonic optimistic-lock counter (starts at 1, incremented by trigger on every update) |
| `created_at` | TIMESTAMP | Creation timestamp |
| `updated_at` | TIMESTAMP | Last update timestamp |

**Constraints**:

- Unique `(tenant_id, business_key)`

**Rules**:

- **Optimistic locking via `record_version`**: `PATCH /workflows/:id` supplies the token via `If-Match: "<record_version>"` header (preferred) or the `record_version` body field (§10.11). The update query filters on `AND record_version = :client_version`; if another update has been applied in the interim, `RowsAffected = 0` → `WORKFLOW_CONCURRENCY_VIOLATION` (409). Using a monotonic counter rather than `updated_at` avoids false negatives from sub-millisecond concurrent writes.

**Purpose**:
Stores the logical workflow definition independent of versions.

---

#### `workflow_version`

| Column | Type | Notes |
| --- | --- | --- |
| `id` | UUID | Primary key |
| `workflow_id` | UUID | Foreign key referencing `workflow(id)` (cascades on delete) |
| `tenant_id` | UUID | Tenant isolation key |
| `status` | ENUM | Version status: `DRAFT`, `PUBLISHED`, `ARCHIVED` |
| `bpmn_xml` | TEXT | Raw BPMN XML source |
| `compiled_plan_json` | JSONB | Pre-compiled JSON DSL execution plan |
| `artifact_hash` | TEXT | Normalized SHA-256 hash for change detection |
| `version_number` | INT | Monotonically increasing version number (NULL on DRAFT rows) |
| `published_at` | TIMESTAMP | Publish timestamp |
| `created_by_user_id` | UUID | Version author |
| `is_valid` | BOOLEAN | Assignee validity flag (default true). Set to `false` when a default assignee loses department membership. Versions with `is_valid = false` cannot start new instances. |
| `validation_errors_json` | JSONB | Array of `{"node_id", "error"}` objects written when `is_valid` becomes false. Null when valid. |
| `record_version` | BIGINT | Monotonic optimistic-lock counter (starts at 1, incremented by trigger on every update) |
| `created_at` | TIMESTAMP | Creation timestamp |
| `updated_at` | TIMESTAMP | Last update timestamp |

**Rules**:

- **Only one draft per workflow**: A unique index `idx_wv_single_draft` enforces that at most one draft exists per workflow.
- **Drafts have NULL version numbers**: The version number is assigned atomically at publish time.
- **Published/archived versions must have a version number**: Enforced by constraint `chk_version_number_on_publish`.
- **Published versions are immutable to user**: State edits are only permitted on the active draft.
- **Version numbers are unique per workflow**: Index `uq_workflow_version_published` enforces uniqueness for non-null version numbers.
- **Optimistic locking via `record_version`**: `PUT /workflows/:id/draft` supplies the token via `If-Match: "<record_version>"` header (preferred) or the `record_version` body field (§10.11). The update query filters on `AND record_version = :client_version`; if the row has been concurrently modified, `RowsAffected = 0` → `DRAFT_CONCURRENCY` (409). Using a monotonic counter rather than `updated_at` avoids false negatives from sub-millisecond concurrent writes.

---

#### `workflow_node_assignee`

A **denormalized reverse index** that inverts the compiled DSL's `default_assignees` lists from "version → assignees" to "assignee → versions". Because a node may have multiple default assignees, **one row is stored per (node, user) pair**. All rows for a version are inserted atomically during the publish transaction from the same data that produces the `compiled_plan_json`, and the two must never diverge.

**Why this table exists**: When a `department.membership.revoked` event arrives, the system must answer: *"Which published workflow versions reference user X in department Y?"* Without this table, every event would require scanning the `compiled_plan_json` JSONB of every `workflow_version` row, traversing nested `departments[] → stages[] → default_assignees[]` paths — a query pattern that GIN indexes cannot efficiently support. This table reduces that operation to a single btree index scan on `(user_id, tenant_id)`.

> [!NOTE]
> This table is **read-only after publish**. It is never updated independently of the compiled DSL. If a default assignee needs to change, a new version must be published, which creates new rows in this table.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | UUID | Primary key |
| `tenant_id` | UUID | Tenant isolation key |
| `workflow_version_id` | UUID | Foreign key referencing `workflow_version(id)` (cascades on delete) |
| `node_key` | VARCHAR(255) | Key of the workflow step |
| `user_id` | UUID | Assigned user |
| `department_id` | VARCHAR(255) | Department identifier from the BPMN lane configuration. Format is determined by the workflow admin; current examples (e.g. "design", "qa-hse") are placeholders. Revisit when the Org & Membership service LLD is complete. |
| `role` | VARCHAR(64) | IAM role level for eligibility validation. Passed as `?level=<role>` to the Org & Membership eligibility endpoint at publish time. Maps to the IAM role tier (e.g. "preparator", "reviewer", "approver"). |
| `created_at` | TIMESTAMP | Creation timestamp |

---

#### `outbox_events`

Stores outbound events queued for processing.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | UUID | Primary key (UUID v7) |
| `event_type` | TEXT | Event type name |
| `payload` | JSONB | Serialized event payload |
| `tenant_id` | TEXT | Tenant isolation key |
| `trace_id` | TEXT | Trace ID for context propagation |
| `attempts` | INT | Number of dispatch attempts |
| `last_error` | TEXT | Last error message encountered |
| `created_at` | TIMESTAMP | Record creation time |
| `scheduled_at` | TIMESTAMP | Scheduled execution time |
| `published_at` | TIMESTAMP | Time when successfully published |

#### `outbox_dead_letters`

Stores failed outbox events that exhausted maximum retries.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | UUID | Primary key (UUID v7) |
| `event_type` | TEXT | Event type name |
| `payload` | JSONB | Serialized event payload |
| `tenant_id` | TEXT | Tenant isolation key |
| `trace_id` | TEXT | Trace ID |
| `attempts` | INT | Final attempt count |
| `last_error` | TEXT | Final error message |
| `created_at` | TIMESTAMP | Original event creation time |
| `failed_at` | TIMESTAMP | Dead-letter transition time |

---

#### `processed_event`

Stores event IDs processed by SQS consumers to ensure idempotency.

| Column | Type | Notes |
| --- | --- | --- |
| `event_id` | UUID | Part of composite PK — the unique event envelope ID |
| `consumer` | TEXT | Part of composite PK — consumer name (e.g. `membership-wf-q`) |
| `event_type` | TEXT | Event type for observability/forensics (e.g. `department.membership.revoked`); nullable for legacy rows |
| `processed_at` | TIMESTAMP | Timestamp when the event was successfully processed |

Composite PK `(event_id, consumer)` allows the same event to be consumed by multiple independent consumers without collision.

---

### 2.2.2 Table and Column Details

1. workflow Table

    id (UUID): Primary key, utilizing globally unique identifiers to prevent ID exhaustion and coordinate seamlessly with multi-tenant data boundaries.

    tenant_id (UUID): Strict tenant identifier. Crucial for the PostgreSQL RLS policy filter.

    created_by_user_id (UUID): The tenant admin who created the root workflow entity. Provides root-level lineage.

    business_key (VARCHAR): The external business key serving as the canonical reference for the workflow across the overall system architecture (e.g., referencing process types like tender-123-abc across decoupled domain services). Ensures clean, human-readable lookups and strict multi-service alignment instead of relying solely on random UUIDs.

    active_version_id (UUID): A nullable foreign key pointing to the currently active/published workflow_version. This allows O(1) reads of the running template schema when spawning new workflow instances, bypassing sequential version scans.

    record_version (BIGINT): Monotonic optimistic-lock counter. Starts at 1, incremented by a `BEFORE UPDATE` trigger on every row change. `PATCH /workflows/:id` supplies the client's last-read value via `If-Match` header or body field; a mismatch returns `409 WORKFLOW_CONCURRENCY_VIOLATION`.

    updated_at (TIMESTAMP): Last modification time, maintained automatically by the same `BEFORE UPDATE` trigger as `record_version`.

    UNIQUE (tenant_id, business_key): Guarantees key uniqueness per tenant, allowing different tenants to declare identical keys without collision.

2. workflow_version Table

    workflow_id (UUID): References the parent root record. Cascades deletion to prevent orphaned draft/version rows.

    version_number (INT, nullable): Monotonically increasing integer assigned at publishing time, not at draft creation. The column is NULL while the version remains a DRAFT. This avoids conflicts when multiple admins create consecutive drafts before any is published; the next version number is computed as COALESCE (MAX (version_number), 0) + 1 across all non-null published versions for the workflow and written atomically inside the publish transaction. A partial unique index (WHERE version_number IS NOT NULL) enforces uniqueness without blocking drafts.

    status (Enum): DRAFT, PUBLISHED, or ARCHIVED. Ensures draft modifications never leak into production-ready execution environments.

    bpmn_xml (TEXT): Retains the original raw BPMN XML source so the frontend modeller canvas can easily reload and render the graph or copy as a base for new versions.

    compiled_plan_json (JSONB): The compiled, validated JSON DSL Execution Plan. Pre-compilation avoids XML parsing during runtime workflow starts.

    artifact_hash (TEXT): A canonical SHA-256 hash computed from the normalized BPMN XML elements. Used by the version diff engine to automatically verify if a saved draft is identical to a published version or if changes exist.

    created_by_user_id (UUID): The tenant admin who created/updated this specific version. Provides strict revision history auditing.

    is_valid (BOOLEAN): Reflects assignee validity — not BPMN structural validity. Default `true`. Set to `false` by the `department.membership.revoked` handler (`POST /internal/events`, §7.4) when an assigned user loses the required department role. A version with `is_valid = false` cannot be promoted or used to start new workflow instances; existing running instances are not affected. The version must be updated (re-assign the affected node) and republished to restore validity.

    validation_errors_json (JSONB): Array of error objects written when `is_valid` is set to `false`. Each element has the shape `{"node_id": "<node_key>", "error": "<reason>"}`. Null when the version is valid. Example: `[{"node_id": "Task_design_prep", "error": "Default assignee is no longer eligible: Department membership revoked"}]`.

    record_version (BIGINT): Monotonic optimistic-lock counter. Starts at 1, incremented by a `BEFORE UPDATE` trigger on every row change. `PUT /workflows/:id/draft` supplies the client's last-read value via `If-Match` header or body field; a mismatch returns `409 DRAFT_CONCURRENCY`.

    updated_at (TIMESTAMP): Last modification time, maintained automatically by the same `BEFORE UPDATE` trigger as `record_version`.

3. outbox_events Table

    id (UUID): Primary key, UUID v7.

    event_type (TEXT): Name of the event type (e.g., `wf.template.published`).

    payload (JSONB): The raw serialized event envelope.

    tenant_id (TEXT): Tenant isolation key.

    trace_id (TEXT): Trace ID for context propagation.

    attempts (INT): Number of dispatch attempts. Defaults to 0.

    last_error (TEXT): Last error message encountered during publishing.

    created_at (TIMESTAMP): Record creation timestamp.

    scheduled_at (TIMESTAMP): Scheduled execution time. Used for lease management and retries.

    published_at (TIMESTAMP): Timestamp when the event was successfully published.

4. outbox_dead_letters Table

    id (UUID): Primary key, UUID v7.

    event_type (TEXT): Name of the event type.

    payload (JSONB): The raw serialized event envelope.

    tenant_id (TEXT): Tenant isolation key.

    trace_id (TEXT): Trace ID.

    attempts (INT): Final attempt count.

    last_error (TEXT): Final error message before dead-lettering.

    created_at (TIMESTAMP): Original event creation time.

    failed_at (TIMESTAMP): Timestamp when moved to dead-letter table.

5. workflow_node_assignee Table

    id (UUID): Primary key, utilizing globally unique identifiers to isolate mappings.

    tenant_id (UUID): Tenant identifier to enforce multi-tenant isolation via the PostgreSQL RLS policy.

    workflow_version_id (UUID): References the specific workflow version template. Deletes cascade to clean up mappings when templates are deleted.

    node_key (VARCHAR): Represents the specific task node element ID from the BPMN definition.

    user_id (UUID): Represents one of the assigned eligible users for the node. One row per (node_key, user_id) pair. Checked for invalidation when memberships change.

    department_id (VARCHAR): Represents the department context of the assignment (using the string slug identifier, e.g. "design", "qa-hse").

    role (VARCHAR): The functional role level required for the assignment (e.g. preparator, reviewer, approver).

6. processed_event Table

    event_id (UUID): Part of the composite primary key `(event_id, consumer)`. The unique event envelope ID from the SQS message — the dedup anchor that prevents the same event from being processed twice by the same consumer.

    consumer (TEXT): Part of the composite primary key. The name of the SQS consumer queue (e.g. `membership-wf-q`). The composite PK allows the same event to be independently processed by multiple consumers without collision.

    event_type (TEXT, nullable): The event type string (e.g. `department.membership.revoked`). Nullable for backward compatibility with older rows. Stored for observability and forensic queries.

    processed_at (TIMESTAMP): Logs when the event was processed, allowing TTL-based records cleanup.

---

### 2.3 Outbox Pattern

#### Design

A shared, single-table outbox is used to queue all outbound domain events. To prevent write inconsistencies (the "dual-write" problem) where database updates succeed but the message broker dispatch fails (or vice versa), the service writes both the business data updates and the corresponding outbox notification events within the same SQL transaction block:

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil {
    return err
}
defer tx.Rollback()

// 1. Apply the business change (e.g. promoting draft to active version)
_, err = tx.ExecContext(ctx, `
    UPDATE workflow_version 
    SET status = 'PUBLISHED', published_at = NOW(), version_number = $1
    WHERE id = $2 AND tenant_id = $3
`, versionNum, versionID, tenantID)
if err != nil {
    return err
}

// 2. Write the outbox event in the SAME transaction utilizing outbox.Enqueue
// This inserts a row into the outbox_events table:
// INSERT INTO outbox_events (id, event_type, payload, tenant_id, trace_id, attempts, created_at, scheduled_at)
// VALUES ($1, $2, $3, $4, $5, 0, NOW(), NOW())
err = outbox.Enqueue(ctx, tx, env)
if err != nil {
    return err
}

err = tx.Commit()
if err != nil {
    return err
}
```

A background transactional outbox runner (`outbox.Runner`) polls the `outbox_events` table, publishes pending envelopes to SNS, and marks them `published_at = NOW()`. Because the broker dispatch occurs outside the database transaction, the relay worker is designed to be **idempotent**: if it crashes after dispatching to SNS but before updating the database row status, the event ID (UUID) is reused on retry to enable downstream deduplication.

#### Benefits

- **No schema changes**: JSONB payload storage allows introducing new events without requiring DDL modifications.
- **Single polling worker**: OutboxRelay only monitors one database table, reducing connection overhead and locking contention.
- **Uniform error handling**: Standardized retry counter and status flow for all outbound events.
- **Reduced operational complexity**: Centralized auditing and cleanup mechanisms.

---

### 2.3.1 Consumer Idempotency

To achieve exact-once processing semantics, inbound event processing must track successfully processed event IDs in a dedicated local `processed_event` table. In the Definition Service this dedup runs inside the `POST /internal/events` handler (keyed on the envelope `id` forwarded by the shared consumer), not in an in-process SQS consumer:

```sql
CREATE TABLE processed_event (
    event_id     UUID         NOT NULL,
    consumer     TEXT         NOT NULL,
    event_type   TEXT,
    processed_at TIMESTAMP    NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, consumer)
);
```

#### Consumer Processing Loop

Upon receiving a message, the consumer first executes an optimistic idempotency check:

```go
// Attempt to register the event ID in the database
result, err := tx.ExecContext(ctx, `
    INSERT INTO processed_event (event_id, consumer, event_type)
    VALUES ($1, $2, $3)
    ON CONFLICT DO NOTHING
`, eventID, "membership-wf-q", "department.membership.revoked")
if err != nil {
    return err
}

rowsAffected, _ := result.RowsAffected()
if rowsAffected == 0 {
    // Event was already processed; safely ignore payload and ack the message
    return nil
}
```

- **Duplicate Detection**: If the `INSERT` statement affects `0` rows (due to `ON CONFLICT DO NOTHING`), the event has already been successfully processed. The `POST /internal/events` handler returns a `2xx` no-op so the shared consumer treats the message as handled and does not retry.
- **TTL Pruning**: To prevent the idempotency registry from growing indefinitely, a background cron job runs daily to purge `processed_event` logs older than 7 days (matching the SQS message retention limit).

---

### 2.4 Indexing Strategy

| Table | Index Name | Purpose / Optimized Query |
| --- | --- | --- |
| `workflow` | `idx_workflow_tenant_id` | Tenant-scoped list queries (paginated GET `/workflows`) |
| `workflow` | `idx_workflow_active_version` | Active version lookup joins |
| `workflow` | `idx_workflow_name_trgm` | Trigram GIN index for fuzzy search on workflow name |
| `workflow_version` | `idx_wv_workflow_id` | Fetching version history lists |
| `workflow_version` | `idx_wv_tenant_status` | Status filtering (active drafts, published list) |
| `workflow_version` | `idx_wv_single_draft` | Partial unique index enforcing single draft per workflow |
| `workflow_version` | `idx_wv_artifact_hash` | Change detection / comparisons in diff engine |
| `workflow_version` | `uq_workflow_version_published` | Unique version numbers per workflow template |
| `workflow_node_assignee` | `idx_wnas_user_tenant` | Assignee invalidation query on membership revocation |
| `workflow_node_assignee` | `idx_wnas_version` | Join index for loading node assignees of a version |
| `outbox` | `idx_outbox_pending` | Relay worker polling (with `SKIP LOCKED`) |
| `outbox` | `idx_outbox_tenant_pending` | Tenant-scoped partitioning / parallel relay |
| `outbox` | `idx_outbox_processed_at` | TTL cleanup job (`processed_at < NOW() - INTERVAL '7 days'`) |
| `processed_event` | `idx_processed_event_processed_at` | Daily TTL cleanup job (`processed_at < NOW() - INTERVAL '7 days'`) |

### 2.5 Triggers — `updated_at` and `record_version`

`updated_at` and `record_version` are maintained by **database triggers**, not application code. This ensures every write path — application code, platform migrations, and direct operator SQL — keeps the columns correct without relying on the caller to remember.

The triggers are defined in migration `00002_create_tables.sql`:

```sql
CREATE OR REPLACE FUNCTION update_workflow_meta_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at     = NOW();
    NEW.record_version = OLD.record_version + 1;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_touch_workflow
    BEFORE UPDATE ON workflow
    FOR EACH ROW WHEN (OLD.* IS DISTINCT FROM NEW.*)
    EXECUTE FUNCTION update_workflow_meta_column();
```

(An equivalent `update_workflow_version_meta_column` trigger covers `workflow_version`.)

**Design notes:**

- **`WHEN (OLD.* IS DISTINCT FROM NEW.*)` guard**: the trigger fires only when a row actually changes. If a future SQS replay produces an identical row (same field values, no business change), `record_version` does not increment and a concurrent optimistic-lock holder is not spuriously invalidated.
- **`BEFORE UPDATE` timing**: the application's `WHERE record_version = :client_version` clause compares against the pre-trigger value. The new incremented value is committed atomically in the same statement — there is no window where the check and the write are inconsistent.
- **No application-layer management**: application SQL must not set `updated_at = NOW()` or `record_version = record_version + 1` explicitly. Doing so would make the trigger fire twice (BEFORE trigger sees the application-provided value, then overwrites it with its own calculation). See §2.6 for the convention.

Cross-reference: Appendix A contains the full DDL. §10.11 documents how clients supply the optimistic-lock token (`If-Match` header or `record_version` body field).

### 2.6 Migration Practices

- **Tool**: `platform-pgcommon migrate.Runner` (golang-migrate, pgx/v5 driver). Runs programmatically at service startup. See §1.8.1 for the startup sequence.
- **File format**: `db/migrations/NNNN_description.up.sql` + `NNNN_description.down.sql` pairs. No goose annotations. PL/pgSQL function bodies (`$$...$$`) work without special delimiters — the pgx/v5 driver executes each file as a single unit.
- **`lock_timeout`**: auto-appended (`30s`) by `migrate.Runner` — no manual DSN parameter needed. This prevents a blocked migration from holding the advisory lock across rolling deploys.
- **Tracking table**: `wf_definition_migrations` (service-specific, set via `MigrationsTable` field). Isolated from `outbox_migrations` and any `pgcommon_migrations` table — no version-number collisions.
- **Outbox tables**: created by `outbox.ApplySchema` (tracked under `outbox_migrations`). The service migration `0005_outbox_rls.up.sql` then adds RLS policies to those tables. See §1.8.1 for the startup order.
- **Forward-only additive**: new columns are added as `NOT NULL DEFAULT x`; column drops are split across two releases (deprecate → stop using → drop) to stay compatible with in-flight pods during rolling deploys.
- **No `updated_at` or `record_version` in UPDATE queries**: both columns are trigger-managed (§2.5). Application SQL must not include `SET updated_at = NOW()` or `SET record_version = record_version + 1` — the trigger handles it unconditionally.
- **Migrator role**: the DB role used at startup (`definition_svc_migrator`) must have `BYPASSRLS` to avoid being blocked by the service's own RLS policies during data migrations. The application role (`definition_svc_app`) must **not** have `BYPASSRLS`.
- **CI verification** (run after every migration): `SELECT rolname FROM pg_roles WHERE rolname = 'definition_svc_app' AND rolbypassrls = true` must return 0 rows; `SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND (rowsecurity = false OR forcerls = false)` must also return 0 rows.

---

---

## 3. API Contracts

The Workflow Definition Service exposes a set of REST endpoints for design-time operations and a high-performance internal gRPC endpoint.

### 3.1 Global Gateway Headers & Security

The Envoy API Gateway performs JWT verification and injects standardized context headers into all upstream REST requests. These headers are inherited by all endpoints:

- `x-tenant-id` (UUID, Required): Enforces tenant isolation inside the database using RLS. Validated by `gincommon.RequireAuth` — must be non-empty, trimmed, ≤ 256 characters, and contain no control characters. Constant: `gincommon.HeaderTenantID`.
- `x-user-id` (UUID, Required): Denotes the executing administrative user. Subject to the same validation rules as `x-tenant-id`. Constant: `gincommon.HeaderUserID`.
- `x-tenant-roles` (String, Required): Comma-separated roles (e.g. `tenant_admin`, `tenant_owner`). Validated as `[a-zA-Z0-9_-]+` per role token; mutating calls require administrative access. Constant: `gincommon.HeaderTenantRoles`.
- `x-departments` (String, Optional): Comma-separated `<department-uuid>:<role>` pairs — e.g. `018e1f2a-0000-7000-8000-000000000001:reviewer,018e1f2a-0000-7000-8000-000000000002:approver`. Each pair names one of the user's department memberships and their role level within it. IAM's HLD (`iam_1.41.md` §5.4) now documents this same `<department_id>:<role_level>` format explicitly, confirming `dept_uuid:role` is correct — the earlier cross-team discrepancy is resolved at the HLD level. IAM's own org-membership LLD reportedly still describes a bare-UUID variant; that residual HLD-vs-LLD inconsistency is IAM's own follow-up to reconcile, tracked in `Notes/conf.md`, not this document's concern.
- `x-plan` (String, Optional): Subscription level (e.g., `enterprise`, `pro`).
- `x-feature-flags` (String, Optional): Encoded feature flags.
- `x-request-id` (UUID, Optional): Client-supplied correlation ID. If absent or invalid, `gincommon.RequestIDMiddleware` generates a fresh UUID. Echoed back on the response as `X-Request-ID`. Constant: `gincommon.HeaderRequestID`.
- `traceparent` (W3C, Optional): Upstream OTel trace context. If present, `gincommon.TracingMiddleware` links the server span to the parent; otherwise a new root span is created. Echoed back as `X-Trace-ID`.

#### Security & Payload Constraints

- **Payload Limits**: Capped at `10MB` for all payload-bearing endpoints using a `MaxBytesReader` middleware.
- **Entity Expansion**: Disallowed in XML parser to prevent Billion Laughs / XXE attacks.
- **Rate Limiting**: Enforced at the gateway (Envoy) level per tenant based on plan tier (burst limit `plans.api_rate_limit_rps` and monthly budget `plans.api_request_budget_monthly`).
- **Plan Quotas & Entitlements**: During workflow creation (`POST /workflows`), the service parses the `x-plan` header and enforces limits on the maximum allowed workflow templates per tenant (e.g. Starter: 5 templates, Pro: 50, Enterprise: Unlimited). Requests exceeding this quota return a `403 Forbidden` with a plan limits block.
- **Idempotency**: All mutating operations (`POST`, `PUT`) evaluate `Idempotency-Key` (UUID) headers using a Redis cache to prevent duplicate processing on network retries.

#### Input Validation Rules

Input validation is enforced at the service layer before any DB write. Violations return `422 UNPROCESSABLE_ENTITY` unless otherwise noted.

**Free-text field length caps** (enforced in service layer; DB column widths are a secondary backstop):

| Field | Max length | Notes |
| --- | --- | --- |
| `workflow.name` | 255 chars | Matches VARCHAR(255) column |
| `workflow.description` | 2 000 chars | TEXT column; cap enforced at service layer only |
| `workflow.business_key` | 100 chars | Must match `^[a-z0-9]+(-[a-z0-9]+)*$` (lowercase slug, no leading/trailing/consecutive hyphens); enforced at service layer before the DB unique index |
| `workflow_version.bpmn_xml` | 10 MB bytes | Governed by `LimitRequestBody` middleware; returns 413 before service layer |

**`business_key` format**: lowercase ASCII letters and digits, with hyphens as separators only (no leading, trailing, or consecutive hyphens). Pattern: `^[a-z0-9]+(-[a-z0-9]+)*$`. Enforced in the service layer because the DB column is VARCHAR(255) with no regex constraint. Invalid format returns `400 BAD_REQUEST` before the uniqueness check.

**Concurrency tokens**: `PATCH /workflows/:id` and `PUT /workflows/:id/draft` accept the lock token as `If-Match: "<record_version>"` header (preferred) or `record_version` in the JSON body. Header takes precedence when both are present. Omitting both is last-writer-wins (acceptable for single-admin tenants; the guard matters for concurrent admin edits). See §10.11.

**Pagination params** (`GET /workflows`, `GET /workflows/:id/versions`): `page` ≥ 1 (default 1), `limit` ∈ [1, 100] (default 20). `versions_limit` on `GET /workflows/:id` is clamped to [1, 100] in the service layer.

#### Standard Error Patterns (RFC-9457)

All error responses adhere to the RFC-9457 (formerly RFC-7807) Problem Details schema. This standard provides a consistent, machine-readable format for API errors. The base structure is defined as:

```json
{
  "type": "https://api.workflow.platform/errors/structural-divergence",
  "title": "Structural Divergence Detected",
  "status": 409,
  "detail": "Topological elements have diverged from the previous version. Publishing this draft may break active execution instances.",
  "instance": "/workflows/7ca648b2-b432-4744-884c-35fd556a310c/versions/11abcc22-3844-42bc-938b-665cd42c1111/publish",
  "code": "STRUCTURAL_DIVERGENCE"
}
```

For validation and graph errors (`422 Unprocessable Entity`), the response includes an `errors` array detailing each failing element:

```json
{
  "type": "https://api.workflow.platform/errors/validation-failed",
  "title": "BPMN Validation Failed",
  "status": 422,
  "detail": "BPMN semantic or structural validation failed.",
  "instance": "/workflows/validate",
  "code": "BPMN_VALIDATION_FAILED",
  "errors": [
    {
      "code": "MISSING_TASK_DEFINITION",
      "node_id": "UserTask_112",
      "message": "userTask is missing zeebe:taskDefinition"
    },
    {
      "code": "UNMATCHED_GATEWAY",
      "node_id": "ExclusiveGateway_2",
      "message": "Unbalanced split gateway: no corresponding join gateway found"
    }
  ]
}
```

#### Error Catalog

Below is the complete registry of application-specific error codes returned inside the `code` field of the RFC-9457 payload:

| Error Code | HTTP Status | Problem Title | Description & Context |
| --- | --- | --- | --- |
| `BAD_REQUEST` | `400 Bad Request` | Bad Request | JSON request body could not be bound — missing required fields or incorrect field types. |
| `INVALID_BPMN_XML` | `400 Bad Request` | Invalid BPMN XML | The BPMN document cannot be parsed — not valid XML, a forbidden `DOCTYPE`/entity, or the XML-bomb token cap. (Distinct from `BPMN_VALIDATION_FAILED` 422, which is a parseable-but-invalid document.) |
| `XXE_ENTITY_DETECTED` | _(internal log signal — not returned)_ | External Entities Prohibited | A forbidden `DOCTYPE`/external-entity or XML-bomb attempt. The client receives the generic `400 INVALID_BPMN_XML` (detection is not disclosed); the service emits an internal security-alert log with the client IP and tenant. |
| `UNAUTHORIZED` | `401 Unauthorized` | Unauthorized | Missing, expired, or invalid gateway-injected identity headers (`x-tenant-id`, `x-user-id`). |
| `FORBIDDEN` | `403 Forbidden` | Forbidden | The user lacks the required administrative role (`tenant_admin` or `tenant_owner`) to perform this action. |
| `PLAN_QUOTA_EXCEEDED` | `403 Forbidden` | Plan Quota Exceeded | The tenant has reached the maximum allowed workflow templates/versions for their plan tier. |
| `NOT_FOUND` | `404 Not Found` | Resource Not Found | The requested workflow or version ID does not exist or belongs to a different tenant. |
| `DRAFT_NOT_FOUND` | `404 Not Found` | Draft Not Found | No active DRAFT version exists for this workflow. Returned by `GET /workflows/:id/draft`. |
| `PAYLOAD_TOO_LARGE` | `413 Payload Too Large` | Request Entity Too Large | The request payload exceeds the configured maximum transfer size limit (10MB). |
| `UNSUPPORTED_MEDIA_TYPE` | `415 Unsupported Media Type` | Unsupported Media Type | A request carrying a body whose `Content-Type` is not `application/json`. Enforced by the `RequireJSONContentType` middleware on the authenticated API group. |
| `RATE_LIMIT_EXCEEDED` | `429 Too Many Requests` | Rate Limit Exceeded | The tenant has exceeded their allocated rate limits (requests per second or monthly quota). _(gateway-enforced — not returned by this service) |
| `DUPLICATE_BUSINESS_KEY` | `409 Conflict` | Workflow Key Conflict | A workflow with the specified `business_key` already exists within the tenant boundary. |
| `DRAFT_ALREADY_EXISTS` | `409 Conflict` | Draft Already Exists | An active workspace draft already exists for this workflow (must publish or discard it first). |
| `DRAFT_CONCURRENCY` | `409 Conflict` | Draft Concurrency Violation | Stale draft update — client's `record_version` does not match the current value; another update has been applied since the draft was last read. |
| `WORKFLOW_CONCURRENCY_VIOLATION` | `409 Conflict` | Workflow Concurrency Violation | Stale workflow metadata update — client's `record_version` on the `workflow` row does not match; another name/description update has been applied since the client last read. _(planned — pending `PATCH /workflows/:id` endpoint) |
| `IDEMPOTENCY_KEY_REPLAY` | `409 Conflict` | Idempotent Request Replayed | The request was submitted with a previously processed `Idempotency-Key` but different parameters. |
| `NO_ACTIVE_VERSION` | `409 Conflict` | No Active Version | The operation (e.g. archiving or promoting) requires an active version, but none is set. |
| `STRUCTURAL_DIVERGENCE` | `409 Conflict` | Structural Divergence Detected | The compiled graph has structural shifts. Blocked unless `force_publish_structural` is `true`. |
| `ACTIVE_INSTANCES_EXIST` | `409 Conflict` | Active Instances Running | The workflow cannot be archived because there are active running or paused instances. |
| `INVALID_VERSION_STATUS` | `409 Conflict` | Invalid Version Status | The requested operation is not permitted for the version's current status (e.g. cloning a DRAFT, promoting an ARCHIVED version). |
| `BPMN_VALIDATION_FAILED` | `422 Unprocessable Entity` | BPMN Validation Failed | BPMN semantic or structural validation failed. The `invalid_params` array carries per-node `BpmnErrorCode` details. |
| `ASSIGNEE_INELIGIBLE` | `422 Unprocessable Entity` | Ineligible Assignee | A default assignee validation failed eligibility checks against the Org & Membership Service. |
| `INTERNAL_ERROR` | `500 Internal Server Error` | Internal Server Error | An unexpected server error occurred (e.g., database connection failure or unhandled panic). |
| `UPSTREAM_UNAVAILABLE` | `503 Service Unavailable` | Upstream Service Unavailable | The Execution Service is unreachable or returned a non-OK gRPC status during the archive guard call. The archive is not committed while the guard is unresolvable. |
| `DATABASE_DISCONNECTED` | `503 Service Unavailable` | Database Connection Failed | The readiness probe failed to connect to the database. The pod is removed from load balancer rotation until connectivity is restored. |

#### 3.1.3 Error Code Flow & Trigger Logic

Below is a detailed walkthrough of the endpoints, checks, and flow execution transitions that trigger each specific application error code:

1. **`INVALID_BPMN_XML`** (HTTP 400)
   - **Endpoints**: `POST /workflows`, `PUT /workflows/:id/draft`, `POST /workflows/validate`
   - **Trigger Scenario**: A user imports or uploads a corrupted BPMN XML file from their local computer, or makes a manual API call with a malformed XML payload.
   - **Technical Cause**: The HTTP router passes the request body to Go's `xml.Unmarshal` decoder. If the decoder encounters malformed XML (e.g., tag mismatch, unclosed elements, or invalid characters), it returns a parsing syntax error.
   - **Step-by-Step Flow**:
     1. The client submits a REST request containing the raw XML payload.
     2. The handler reads the request body via a `MaxBytesReader` stream.
     3. The handler passes the buffer to `xml.Unmarshal` to unmarshal into the internal Go AST struct representation.
     4. The compiler's parse step (security scan → token cap → `xml.Unmarshal`) returns a non-namespace parse failure, wrapped as `domain.ErrMalformedBPMN`.
     5. The handler halts execution and returns an RFC-9457 error with `code: INVALID_BPMN_XML` (HTTP 400). A forbidden-construct cause is additionally logged (see `XXE_ENTITY_DETECTED`).
   - **Database & Transaction Impact**: The request is halted before database access; no SQL transaction is initiated, and database state remains unaffected.
   - **Resolution**: The client must check the XML file syntax, ensure it complies with valid XML standards, and correct the malformed elements before re-submitting.

2. **`XXE_ENTITY_DETECTED`** (internal security-log signal — the client receives `400 INVALID_BPMN_XML`)
   - **Endpoints**: `POST /workflows`, `PUT /workflows/:id/draft`, `POST /workflows/validate`
   - **Trigger Scenario**: An attacker attempts to exploit the parser using an XML External Entity (XXE) injection or a nested entity expansion ("XML Bomb" or "Billion Laughs") to cause denial of service or read arbitrary local files.
   - **Technical Cause**: `securityScan` rejects any `<!DOCTYPE>`/`<!ENTITY>` declaration outright, and a token-count cap guards against expansion; both tag the failure with `domain.ErrForbiddenXML`.
   - **Step-by-Step Flow**:
     1. The client sends a request with an XML payload that includes a `<!DOCTYPE ...>` block (or a token stream exceeding the cap).
     2. `securityScan`/`countTokens` reject it before unmarshal, wrapping `domain.ErrForbiddenXML` (which also satisfies `domain.ErrMalformedBPMN`).
     3. The handler emits an internal security-alert **log** containing the client's IP and tenant context.
     4. The client receives the **generic** `400 INVALID_BPMN_XML` — detection is **not** disclosed, so a probe is not confirmed to the caller.
   - **Database & Transaction Impact**: The request is rejected during parsing. No database transaction is started.
   - **Resolution**: Remove all `<!DOCTYPE>` headers and external entity declarations from the BPMN XML document.

3. **`IDEMPOTENCY_KEY_REPLAY`** (HTTP 409)
   - **Endpoints**: All mutating REST endpoints (POST / PUT / DELETE).
   - **Trigger Scenario**: Due to a network retry or client-side bug, a client re-submits a request carrying a previously used `Idempotency-Key` header, but with a different body payload.
   - **Technical Cause**: The Idempotency Middleware validates the `Idempotency-Key` UUID. If it already exists in the Valkey cache, it compares the SHA-256 checksum of the new request payload with the checksum stored in cache. A mismatch indicates that the same key is being reused for a different operation — a conflict on the idempotency record.
   - **Step-by-Step Flow**:
     1. The client sends a mutating request with an `Idempotency-Key` header.
     2. The Idempotency Middleware reads the header and looks up the key in Valkey: `GET idem:<tenantID>:<routePath>:<key>`.
     3. Valkey returns the cached response details, including the SHA-256 hash of the initial payload.
     4. The middleware computes the SHA-256 hash of the incoming request body and finds it does not match the cached hash.
     5. The middleware aborts the request chain immediately and returns `409 Conflict` with `code: IDEMPOTENCY_KEY_REPLAY`.
   - **Database & Transaction Impact**: Halts execution at the middleware level. No database transaction is created.
   - **Resolution**: Use a unique, random UUID v7 for each distinct request's `Idempotency-Key`.

4. **`UNAUTHORIZED`** (HTTP 401)
   - **Endpoints**: All public-facing REST endpoints.
   - **Trigger Scenario**: A client directly invokes the microservice bypass ports or Keycloak JWT authentication failed, leading to missing identity attributes at the gateway.
   - **Technical Cause**: The custom authentication middleware parses the gateway-injected headers. If the mandatory headers `x-tenant-id` or `x-user-id` are missing, malformed (non-UUID format), or empty, identity verification fails.
   - **Step-by-Step Flow**:
     1. The client sends a REST request to the service.
     2. The custom identity middleware executes.
     3. The middleware inspects the request context and headers.
     4. It determines that `x-tenant-id` is absent, or that its contents fail regex matching for a valid UUID v7.
     5. The request processing is halted immediately with `code: UNAUTHORIZED`.
   - **Database & Transaction Impact**: Execution is blocked at the boundary. No database connection is acquired, and no transaction is opened.
   - **Resolution**: The client must ensure that a valid JWT bearer token is supplied in the `Authorization` header so the Envoy gateway can verify identity and inject headers.

5. **`FORBIDDEN`** (HTTP 403)
    - **Endpoints**: All REST endpoints.
    - **Trigger Scenario**: A tenant user with standard credentials (non-admin) attempts to invoke an administrative action (e.g. creating, deleting, or archiving workflows).
    - **Technical Cause**: Role validation middleware checks the `x-tenant-roles` header for required admin values.
    - **Step-by-Step Flow**:
      - *RBAC Failure*:
        1. The middleware parses `x-tenant-roles`.
        2. The roles string does not contain `tenant_admin` or `tenant_owner`.
        3. The middleware aborts execution and returns `code: FORBIDDEN`.
    - **Database & Transaction Impact**: In the RBAC failure, no transaction is started and no DB context is opened.
    - **Resolution**: The user must be assigned the appropriate role level (`tenant_admin` or `tenant_owner`) to perform admin actions.

6. **`PLAN_QUOTA_EXCEEDED`** (HTTP 403)
    - **Endpoints**: `POST /workflows`
    - **Trigger Scenario**: A tenant on a limited subscription tier (e.g. "Starter", which is capped at 5 workflow templates) attempts to register a new workflow key.
    - **Technical Cause**: The workflow creation handler counts the active definitions in a database transaction with a serializable isolation level to prevent parallel bypasses. If the count meets or exceeds the limits defined for the tier in the `x-plan` header, the request is blocked.
    - **Step-by-Step Flow**:
      1. The admin posts a template creation request.
      2. The service extracts the `x-plan` tier (e.g. `starter`).
      3. The handler starts a transaction with `db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})`.
      4. The handler counts the tenant's definitions: `SELECT COUNT(*) FROM workflow WHERE tenant_id = :tenant_id`.
      5. The handler determines the count exceeds the threshold (e.g., 5).
      6. The transaction is rolled back immediately, and the handler returns `code: PLAN_QUOTA_EXCEEDED`.
    - **Database & Transaction Impact**: A database transaction is opened under `SERIALIZABLE` isolation, reads the count, and is rolled back without modifying any rows.
    - **Resolution**: The tenant must upgrade their subscription tier (e.g. to "Pro" or "Enterprise") or delete existing, unused workflow templates to free up quota.

7. **`NOT_FOUND`** (HTTP 404)
    - **Endpoints**: All REST endpoints targeting specific resource IDs.
    - **Trigger Scenario**: A user requests a workflow, draft version, or history record using an ID that does not exist in the database, or attempts to access a resource belonging to another tenant (cross-tenant RLS breach).
    - **Technical Cause**: The query executes against the database. Due to the RLS policy filtering or absence of the record, the database driver returns `sql.ErrNoRows`. By silently returning `NOT_FOUND` for RLS breaches, the system prevents leaking whether a resource exists under another tenant (security through obscurity).
    - **Step-by-Step Flow**:
      1. The client requests `/workflows/c0a80101-3844-42bc-938b-665cd42c1111`.
      2. The service starts a transaction and sets the tenant context via the GUC session parameter.
      3. The query `SELECT * FROM workflow WHERE id = :id` is executed.
      4. If the resource belongs to another tenant or doesn't exist, RLS filters it out, and the database returns no matches (`sql.ErrNoRows`).
      5. The application catches this error, rolls back the transaction, and returns `code: NOT_FOUND`.
    - **Database & Transaction Impact**: A database transaction is opened, performs a lookup, and rolls back cleanly.
    - **Resolution**: Verify that the requested ID is valid, formatted correctly as a UUID v7, and that the resource has not been permanently deleted or discarded.

8. **`PAYLOAD_TOO_LARGE`** (HTTP 413)
   - **Endpoints**: `POST /workflows`, `PUT /workflows/:id/draft`, `POST /workflows/validate`
   - **Trigger Scenario**: An admin uploads a massive BPMN XML process map containing complex layout elements or embedded binary attachments, exceeding the 10MB file transfer limit.
   - **Technical Cause**: A request body limits middleware restricts the maximum readable bytes of the HTTP request payload stream using `http.MaxBytesReader`.
   - **Step-by-Step Flow**:
     1. The client starts sending a POST request with a large request body.
     2. The service's request wrapper middleware intercepts the HTTP body stream using `http.MaxBytesReader(w, r.Body, 5*1024*1024)`.
     3. During reading, the stream reader exceeds the 10MB boundary.
     4. The reader triggers an error (`http: request body too large`).
     5. The handler halts reading, logs the event, and responds with `code: PAYLOAD_TOO_LARGE`.
   - **Database & Transaction Impact**: The request is rejected early in the HTTP processing chain. No database connections are utilized.
   - **Resolution**: Simplify the BPMN model by modularizing large processes into sub-processes, or stripping unused documentation and graphical metadata before upload.

9. **`UNSUPPORTED_MEDIA_TYPE`** (HTTP 415)
   - **Endpoints**: `POST /workflows`, `PUT /workflows/:id/draft`, `POST /workflows/validate`
   - **Trigger Scenario**: A client sends a request **with a body** whose `Content-Type` is missing or is something other than `application/json` (e.g. `text/plain`, `application/xml`, `application/x-www-form-urlencoded`).
   - **Technical Cause**: The `RequireJSONContentType` middleware (authenticated API group) inspects requests that carry a body and requires `application/json` (a `;charset=…` suffix is allowed).
   - **Step-by-Step Flow**:
     1. The client sends a request carrying a non-empty body.
     2. The middleware checks `Content-Length` (body-less GET/DELETE and empty-body POSTs pass through untouched).
     3. It reads the `Content-Type` header; if (after stripping `;charset`) it is not `application/json`, it aborts.
     4. The middleware returns an RFC-9457 `415` with `code: UNSUPPORTED_MEDIA_TYPE`.
   - **Database & Transaction Impact**: Request rejected at boundary. Database is not contacted.
   - **Resolution**: Send the HTTP header `Content-Type: application/json`.

10. **`RATE_LIMIT_EXCEEDED`** (HTTP 429)
    - **Endpoints**: All REST endpoints.
    - **Trigger Scenario**: A tenant runs automated polling scripts or rapid UI interactions that exceed the allowed burst rate limits.
    - **Technical Cause**: The Envoy gateway or application-level rate limiter checks the request rate using a Token Bucket algorithm tied to the tenant's identifier.
    - **Step-by-Step Flow**:
      1. The client fires a burst of requests.
      2. Envoy gateway (or the service rate-limiting middleware) checks the token bucket in Redis: `rate_limit:tenant:<tenant_id>`.
      3. The bucket has 0 tokens available.
      4. The request is rejected at the gateway boundary.
      5. The gateway responds with HTTP 429 and `code: RATE_LIMIT_EXCEEDED`.
    - **Database & Transaction Impact**: Blocked at gateway/middleware. Database is unaffected.
    - **Resolution**: Implement exponential backoff retry logic in client applications, or contact support to upgrade the API rate limit tier.

11. **`DUPLICATE_BUSINESS_KEY`** (HTTP 409)
    - **Endpoints**: `POST /workflows`, `POST /workflows/:id/versions/:version_id/clone`
    - **Trigger Scenario**: An admin tries to register a new workflow using a key (e.g., `tender-review`) that is already assigned to an existing workflow in the same tenant.
    - **Technical Cause**: The database enforces a unique constraint on `(tenant_id, business_key)`. If an insert violates this constraint, PostgreSQL returns error code `23505` (unique violation).
    - **Step-by-Step Flow**:
      1. The admin submits a creation request with `business_key = 'procurement-check'`.
      2. The service starts a transaction.
      3. The repository attempts to execute: `INSERT INTO workflow (tenant_id, business_key, ...) VALUES (:tenant, 'procurement-check', ...)`.
      4. PostgreSQL detects a collision on the unique key constraint `uq_workflow_business_key`.
      5. The database aborts the execution and returns code `23505`.
      6. The transaction is rolled back, the handler catches the unique constraint violation, and maps it to `code: DUPLICATE_BUSINESS_KEY`.
    - **Database & Transaction Impact**: A transaction is opened, executes an insert, collides, aborts the insert statement, and is rolled back.
    - **Resolution**: Choose a unique `business_key` or update the existing workflow using the `/draft` updates instead.

12. **`DRAFT_ALREADY_EXISTS`** (HTTP 409)
    - **Endpoints**: `POST /workflows/:id/draft`
    - **Trigger Scenario**: An admin attempts to initialize a new workspace draft for a workflow that already has an active, un-published draft.
    - **Technical Cause**: The database partial unique index `idx_wv_single_draft` enforces that a workflow can have only one version row in `DRAFT` status at any time.
    - **Step-by-Step Flow**:
      1. The admin clicks "Edit Workflow" which triggers a POST to `/workflows/:id/draft`.
      2. The service starts a transaction.
      3. It checks for active drafts, or tries to execute: `INSERT INTO workflow_version (workflow_id, status, ...) VALUES (:id, 'DRAFT', ...)`.
      4. The index `idx_wv_single_draft` (defined as `UNIQUE(workflow_id) WHERE status = 'DRAFT'`) blocks the write and throws a unique violation.
      5. The database aborts the statement, the transaction rolls back, and the handler returns `code: DRAFT_ALREADY_EXISTS`.
    - **Database & Transaction Impact**: Opened transaction is aborted on constraint violation and rolled back.
    - **Resolution**: Discard the existing draft (`DELETE /draft`) or publish it (`POST /publish`) before attempting to start a new draft.

13. **`DRAFT_CONCURRENCY`** (HTTP 409)
    - **Endpoints**: `PUT /workflows/:id/draft`
    - **Trigger Scenario**: Two administrators are concurrently editing the same draft in the Modeler UI. Admin A saves their work, then Admin B attempts to save their work, which would overwrite Admin A's changes.
    - **Technical Cause**: Optimistic concurrency control via `record_version` (a monotonic BIGINT counter). The update query filters on `AND record_version = :client_version`. If another update has incremented `record_version` in the database, the query matches 0 rows.
    - **Step-by-Step Flow**:
      1. Admin B clicks "Save" sending a PUT request with `If-Match: "3"` (or body field `record_version: 3`).
      2. The service starts a transaction.
      3. The repository executes: `UPDATE workflow_version SET bpmn_xml = :xml WHERE workflow_id = :id AND status = 'DRAFT' AND record_version = 3`. The `record_version` increment and `updated_at` refresh are applied automatically by the `BEFORE UPDATE` trigger.
      4. Because Admin A already saved, the database row's `record_version` is now `4`. The query updates `0` rows.
      5. The repository detects `0` rows affected, rolls back the transaction, and returns `code: DRAFT_CONCURRENCY`.
    - **Database & Transaction Impact**: A transaction is opened, executes an update that matches 0 rows, determines optimistic locking failure, and rolls back.
    - **Resolution**: The client UI must prompt the user that the draft has changed, fetch the latest draft version (which includes the new `record_version`), display a merge/overwrite diff, and retry with the updated `record_version`.

14. **`NO_ACTIVE_VERSION`** (HTTP 409)
    - **Endpoints**: `POST /workflows/:id/draft`, `POST /workflows/:id/archive`, `POST /workflows/:id/versions/:version_id/promote`
    - **Trigger Scenario**: An admin tries to archive a workflow, promote a version, or initialize a draft, but the parent workflow has never had an active published version (e.g. it was just created and only exists as a draft).
    - **Technical Cause**: The workflow root database record has `active_version_id` set to `NULL`. The application verifies this pointer before executing the requested transition.
    - **Step-by-Step Flow**:
      1. The client requests a draft initialization from the active version.
      2. The service starts a transaction and fetches the parent workflow record: `SELECT * FROM workflow WHERE id = :id`.
      3. The service checks `active_version_id`. It is `NULL`.
      4. The service aborts the flow, rolls back the transaction, and returns `code: NO_ACTIVE_VERSION`.
    - **Database & Transaction Impact**: Transaction opens, reads the null pointer, and rolls back cleanly.
    - **Resolution**: Publish the initial draft first using `/publish` before attempting version promotion, archiving, or active-version based cloning.

15. **`STRUCTURAL_DIVERGENCE`** (HTTP 409)
    - **Endpoints**: `POST /workflows/:id/versions/:version_id/publish`
    - **Trigger Scenario**: An admin edits a draft, deleting a user task or changing split/join gateway structures, and clicks "Publish" with `force_publish_structural = false`.
    - **Technical Cause**: The compiler compares the topological execution plan of the active version with the new draft version. If it detects added/removed nodes or rewritten path sequence flows, it flags a structural divergence that could break active runtime instances.
    - **Step-by-Step Flow**:
      1. The admin requests publishing for version `v2`.
      2. The service retrieves both `v1` (active) and `v2` (draft) process definitions.
      3. The compiler runs a structural diff check and determines topological elements have diverged.
      4. The request payload carries `force_publish_structural = false`.
      5. The compiler returns a structural divergence warning.
      6. The handler rolls back the transaction and returns `code: STRUCTURAL_DIVERGENCE` with a suggested action to register a new workflow key.
    - **Database & Transaction Impact**: Transaction opens, reads data, runs compilation checks, aborts, and rolls back.
    - **Resolution**: Set `force_publish_structural = true` in the publish request to override and deploy, or create a brand new workflow using a different key.

16. **`BPMN_VALIDATION_FAILED`** (HTTP 422)
    - **Endpoints**: `POST /workflows/:id/versions/:version_id/publish`, `POST /workflows/validate`
    - **Trigger Scenario**: An admin designs a workflow process that contains an unguarded loop (a cycle with no guarded exit, or a back-edge not originating from an exclusive gateway), unconnected task nodes, or multiple start events.
    - **Technical Cause**: The validation engine runs graph traversal and semantic check rules on the parsed BPMN AST. If any structural validation rules fail, it yields a list of failures.
    - **Step-by-Step Flow**:
      1. The admin clicks publish or runs validation check.
      2. The service parses the BPMN XML to Go structs.
      3. The validation engine classifies back-edges (DFS), runs Tarjan's SCC guarded-loop detection, and performs start/end node counts and sequence flow checks.
      4. One or more validation rules are violated (e.g. an unguarded loop).
      5. The engine halts processing, rolls back the transaction (if publishing), and returns `code: BPMN_VALIDATION_FAILED` with an `errors` array of specific validation codes.
    - **Database & Transaction Impact**: Transaction is opened (for publish), validation fails, and the transaction rolls back. For validate endpoint, no database transaction is opened.
    - **Resolution**: Edit the workflow canvas to make loops guarded (route the back-edge through an exclusive gateway that has a forward exit), connect orphan nodes, or remove duplicate start/end elements.

17. **`ASSIGNEE_INELIGIBLE`** (HTTP 422)
    - **Endpoints**: `POST /workflows/:id/versions/:version_id/publish`
    - **Trigger Scenario**: An admin sets a default assignee user or department on a task, but the user does not belong to that department or lacks the required role level in the Organization registry.
    - **Technical Cause**: The validation engine extracts default assignee attributes and makes an outbound REST check to the Org & Membership Service. If the target service returns that the user is ineligible, validation fails.
    - **Step-by-Step Flow**:
      1. The client requests publishing.
      2. The service extracts assignee ID `user-123` and department ID `dept-456` from task `Task_1`.
      3. The service calls the Org Service: `POST /tenants/tenant-id/users/user-123/eligibility?department=dept-456`.
      4. The Org Service responds that the user is not in department `dept-456`.
      5. The validation pass fails, the transaction rolls back, and the service returns `code: ASSIGNEE_INELIGIBLE` detailing the node ID and error message.
    - **Database & Transaction Impact**: Opened transaction is aborted and rolled back.
    - **Resolution**: Assign an eligible user who belongs to the department and holds the correct role clearance.

18. **`INTERNAL_ERROR`** (HTTP 500)
    - **Endpoints**: All endpoints.
    - **Trigger Scenario**: The PostgreSQL server crashes, a network partition isolates Redis, the SNS client times out, or the application code triggers a null pointer panic.
    - **Technical Cause**: Handlers catch database driver connection errors, service broker timeouts, or catch panics via recovery middleware.
    - **Step-by-Step Flow**:
      1. The client invokes an API endpoint.
      2. During execution, the PostgreSQL driver fails to acquire a connection from the pool.
      3. The repository returns `driver.ErrBadConn`.
      4. The handler logs the precise error details using `Zap` with a trace correlation ID.
      5. The handler rolls back the transaction and returns a sanitized HTTP 500 payload with `code: INTERNAL_ERROR`.
    - **Database & Transaction Impact**: The transaction is automatically rolled back by the database connection or the defer block.
    - **Resolution**: Infrastructure operators must investigate service logs, monitor DB health, check network stability, or review code stack traces.

19. **`INVALID_VERSION_STATUS`** (HTTP 409)
    - **Endpoints**: `POST /workflows/:id/versions/:version_id/promote`, `POST /workflows/:id/versions/:version_id/clone`
    - **Trigger Scenario**: An admin attempts to promote a version that is in `DRAFT` or `ARCHIVED` status, or attempts to clone a version that is in `DRAFT` status.
    - **Technical Cause**: The promotion endpoint requires the target version status to be `PUBLISHED`. The cloning endpoint requires the target version status to be `PUBLISHED` or `ARCHIVED`.
    - **Step-by-Step Flow**:
      1. Admin requests promotion for version `v2` (which is a draft).
      2. Service queries the target version status.
      3. Service checks the status. Since it is `DRAFT` (not `PUBLISHED`), the promotion is rejected.
      4. The handler rolls back the transaction and returns `code: INVALID_VERSION_STATUS` with details on the illegal transition.
    - **Database & Transaction Impact**: Clean rollback.
    - **Resolution**: Target a version with a valid status.

---

### 3.1.4 BPMN Semantic & Structural Validation Sub-Errors

When a request returns the top-level error `BPMN_VALIDATION_FAILED` (HTTP 422), the response payload contains an `errors` list. Each item in this list carries a specific sub-validation code from the `BpmnErrorCode` registry. Below is the detailed trigger logic for each validation rule:

1. **`REJECTED_ELEMENT`**
   - **Trigger Scenario**: The uploaded XML contains a Tier 3 element that will never be supported (§4.1.2 / §4.1.4). Examples: `<bpmn:serviceTask>`, `<bpmn:scriptTask>`, `<bpmn:complexGateway>`, data elements.
   - **Trigger Logic**: The parser scans the raw XML token stream for tag names in the `rejectedElements` map (§4.1.4). One error per offending node ID is emitted. The scan runs before `encoding/xml` unmarshal so that elements the decoder silently drops are still surfaced.

2. **`MISSING_NAMESPACE`**
   - **Trigger Scenario**: The uploaded XML lacks standard BPMN 2.0 namespaces or the Zeebe custom modeler extension namespaces in the root `<bpmn:definitions>` tag.
   - **Trigger Logic**: The parser checks for attributes `xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"` and `xmlns:zeebe="http://camunda.org/schema/1.0/zeebe"`. If absent, compiling Zeebe properties is impossible, triggering this error.

3. **`MISSING_TASK_DEFINITION`**
   - **Trigger Scenario**: A `<bpmn:userTask>` lacks a `<zeebe:taskDefinition>` extension element.
   - **Trigger Logic**: The validator checks each `userTask` for a `zeebe:taskDefinition` child inside `extensionElements`. If absent, validation fails with this code against the task node ID.

4. **`TASK_NOT_IN_LANE`**
   - **Trigger Scenario**: A `<bpmn:userTask>` is not listed under any lane via `<bpmn:flowNodeRef>`.
   - **Trigger Logic**: The validator builds a `nodeID → laneID` map from all `flowNodeRef` entries. Any `userTask` whose ID is absent from this map — or appears in more than one lane — triggers this error. Department is derived solely from lane membership; no `dept_id` property is read.

5. **`UNKNOWN_STAGE_TYPE`** *(warning severity)*
   - **Trigger Scenario**: The `type` attribute of `<zeebe:taskDefinition>` does not match any registered `StageTypeHandler` ID (e.g. `prep`, `review`, `approve`).
   - **Trigger Logic**: The validator looks up `taskDefinition.type` in the `StageTypeHandler` registry. If no handler is registered for that value, a **warning** (not an error) is emitted with code `UNKNOWN_STAGE_TYPE`. Compilation proceeds: the task compiles to a passthrough `StageDef` whose `type` and `activity` fields equal the raw string and whose `engine_note` field reads `"stage type '<type>' is not a defined class in the workflow engine"`. This behaviour exists because IAM is the authoritative owner of role/department definitions and may introduce stage types unknown to this service at design time.
~~   - **Legacy code**: `INVALID_TASK_DEFINITION_TYPE` is retained as a registered error code but is no longer emitted; callers should treat `UNKNOWN_STAGE_TYPE` as its warning-severity replacement.~~

6. **`CANDIDATE_GROUPS_EMPTY`**
   - **Trigger Scenario**: A user task has a `<zeebe:assignmentDefinition>` but `candidateGroups` is absent or empty, or exceeds 256 characters.
   - **Trigger Logic**: The validator reads `assignmentDefinition.candidateGroups`. If the value is missing, empty after trimming, or longer than 256 characters, this error is emitted.

7. **`INVALID_CANDIDATE_USER`**
   - **Trigger Scenario**: The `candidateUsers` attribute on `<zeebe:assignmentDefinition>` is present but is not a single valid UUID v7.
   - **Trigger Logic**: A dedicated validator function (`validateCandidateUser`) parses the value as a UUID v7. If parsing fails or the value contains a comma (multi-user not yet supported), this error is emitted. The named function is the single change point when multi-user support is introduced.

8. **`TASK_LIMIT_EXCEEDED`**
   - **Trigger Scenario**: The process model contains more than 1000 user task nodes.
   - **Trigger Logic**: The parser counts the total occurrences of `<bpmn:userTask>` in the process definition. If the count exceeds 1000, it halts validation and rejects the model to prevent system exhaustion.

9. **`LANE_LIMIT_EXCEEDED`**
   - **Trigger Scenario**: The process contains more than 100 lanes.
   - **Trigger Logic**: The parser counts `<bpmn:lane>` elements. If `len(lanes) > 100`, it fails validation.

10. **`MULTIPLE_START_EVENTS`**
    - **Trigger Scenario**: The XML defines more than one `<bpmn:startEvent>` in the process flow.
    - **Trigger Logic**: The process structure validator counts start event nodes. If `count > 1`, validation fails because execution cannot have ambiguous entry points.

11. **`MULTIPLE_END_EVENTS`**
    - **Trigger Scenario**: The XML defines more than one `<bpmn:endEvent>`.
    - **Trigger Logic**: The process validator counts end event nodes. If `count > 1`, validation fails to keep the execution termination path deterministic in the MVP.

12. **`NO_START_EVENT`**
    - **Trigger Scenario**: There is no `<bpmn:startEvent>` in the process.
    - **Trigger Logic**: The validator checks if a start event exists. If `count == 0`, the model is rejected.

13. **`NO_END_EVENT`**
    - **Trigger Scenario**: There is no `<bpmn:endEvent>` in the process.
    - **Trigger Logic**: The validator checks if an end event exists. If `count == 0`, the model is rejected.

14. **`DANGLING_NODE`**
    - **Trigger Scenario**: A task, gateway, or start/end event has no sequence flow connections.
    - **Trigger Logic**: The validator checks if each node ID has at least one incoming sequence flow and at least one outgoing sequence flow (except start/end events). If a node has no sequence flows associated with it, it is marked as dangling.

15. **`UNREACHABLE_NODE`**
    - **Trigger Scenario**: A node exists in the model but is unreachable from the Start Event.
    - **Trigger Logic**: The validator runs a Depth-First Search (DFS) traversal starting from the single `<bpmn:startEvent>`. Any node that is not visited in the traversal is flagged as unreachable.

16. **`CYCLE_DETECTED` / `UNGUARDED_LOOP`** *(redefined — see §4.4 Guarded-Loop Enforcement)*
    - **Trigger Scenario**: The sequence flows create an **unguarded** cyclic loop — a cycle that cannot be exited. Guarded rework loops (a back-edge from a diverging exclusive gateway that also has a forward exit branch) are **valid** and no longer trigger this error.
    - **Trigger Logic** (`CYCLE_DETECTED`): The compiler classifies back-edges via DFS and runs Tarjan's SCC over the graph to enumerate loops. A loop (SCC with more than one node) is rejected when it has no guarded exit; i.e. it contains no exclusive gateway with an outgoing edge leaving the SCC.
    - **`UNGUARDED_LOOP`**: A separate code emitted when a back-edge is structurally illegal; its source is not a diverging exclusive gateway, an exclusive gateway's outgoing edges are all back-edges (no forward exit), or a node has a self-edge (`u → u`). The validator checks each back-edge's source node type and confirms its originating exclusive gateway retains at least one forward branch. (A self-edge forms an SCC of size 1, so it is caught here rather than by SCC size.)

17. **`UNMATCHED_GATEWAY`**
    - **Trigger Scenario**: A split gateway (e.g. Exclusive, Parallel) does not have a corresponding join gateway to merge the execution branches.
    - **Trigger Logic**: The compiler maps split-to-join routing blocks. It traces all path permutations from a split gateway. If any branch terminates or merges at an mismatched gateway type, the gateway is flagged.

18. **`INVALID_SEQUENCE_FLOW_REF`**
    - **Trigger Scenario**: A `<bpmn:sequenceFlow>` references a non-existent `sourceRef` or `targetRef` ID.
    - **Trigger Logic**: The parser parses sequence flows and builds an adjacency list. If `sourceRef` or `targetRef` does not correspond to a parsed element ID, the reference is invalid.

19. **`MULTIPLE_PROCESSES`**
    - **Trigger Scenario**: Multiple `<bpmn:process>` elements exist outside of a `<bpmn:collaboration>` wrapper, or a collaboration contains more executable processes than there are declared participants.
    - **Trigger Logic**: When no `<bpmn:collaboration>` is present, the parser rejects any definitions with `count > 1`. Inside a collaboration, each `isExecutable="true"` participant is allowed exactly one process — if a process ID is referenced by more than one participant, or a process exists with no matching participant, validation fails.

20. **`INVALID_SLA_DURATION`**
    - **Trigger Scenario**: A timer boundary event or intermediate timer catch event declares a duration in an invalid format.
    - **Trigger Logic**: The unified duration converter (`parseDuration`) is applied to the `timeDuration` value. Accepted formats: ISO 8601 (`P3D`, `PT72H`) or Go duration (`72h`, `3h30m`). Any other format emits this error against the boundary event or intermediate catch event node ID. The legacy `sla_duration` Zeebe property is not read; SLA is modelled exclusively via timer boundary events.

21. **`INVALID_BOUNDARY_ATTACHMENT`**
    - **Trigger Scenario**: A boundary event is attached to a node that does not legally host that boundary type. Specifically: an error boundary event attached to anything other than `bpmn:subProcess` or `bpmn:callActivity`, or a timer boundary event whose host is anything other than `bpmn:userTask` or `bpmn:subProcess`. Message boundary events may attach to `bpmn:userTask`, `bpmn:subProcess`, or `bpmn:callActivity`. Note: error and timer boundary events pass this attachment check on `bpmn:callActivity` per the rule above, but still fail separately at compile time (see §callActivity) — `bpmn:callActivity` never legally *executes* a timer/error boundary, only a message one.
    - **Trigger Logic**: `ValidateBoundaryEvents` in `bpmn_compiler/validator/boundary.go` iterates all boundary events. If `attachedToRef` resolves to an unsupported host element, this error is emitted against the boundary event node ID.

22. **`UNSUPPORTED_ELEMENT`**
    - **Trigger Scenario**: The uploaded XML contains a Tier 2 element that is on the roadmap but not yet supported (§4.1.2 / §4.1.4). Examples: `<bpmn:intermediateCatchEvent>` with a message or signal definition.
    - **Trigger Logic**: `parser.go` checks each scoped element against an `unsupportedElements` map. If a match is found, this error is emitted against the element ID. Distinct from `REJECTED_ELEMENT` — signals "this will be supported in a future version" rather than "permanently rejected".

22a. **`MISSING_DIAGRAM`**
    - **Trigger Scenario**: The uploaded BPMN document has no `<bpmndi:BPMNDiagram>` element.
    - **Trigger Logic**: `ValidateDiagramCompleteness` (in `bpmn_compiler/validator/diagram.go`) returns this error immediately when `len(defs.Diagrams) == 0`. A BPMN document without diagram interchange data cannot be displayed or edited in a visual modeler, so the service rejects it during both validation and compilation.

22b. **`MISSING_DIAGRAM_SHAPE`**
    - **Trigger Scenario**: A node or sequence flow in the process has no corresponding `<bpmndi:BPMNShape>` or `<bpmndi:BPMNEdge>` entry in the diagram interchange section.
    - **Trigger Logic**: `ValidateDiagramCompleteness` builds a set of all shape/edge `bpmnElement` refs in the `BPMNDiagram`. For each activity, gateway, event, and sequence flow it checks that a corresponding shape/edge exists; any missing shape or edge emits this error with the missing element ID as the node ID.

23. **`MISSING_ASSIGNMENT_DEFINITION`**
    - **Trigger Scenario**: A `<bpmn:userTask>` lacks a `<zeebe:assignmentDefinition>` extension element.
    - **Trigger Logic**: The validator checks each `userTask` for a `zeebe:assignmentDefinition` child inside `extensionElements`. If absent, this error is emitted against the task node ID.

24. **`INVALID_ZEEBE_PROPERTY`**
    - **Trigger Scenario**: A `<zeebe:property>` element carries an unrecognized name or a structurally invalid value for a property the compiler actively validates.
    - **Trigger Logic**: *Warning severity* — on a `callActivity`: a `target="Depts"` input is present but its source value is not valid JSON (cannot be parsed as `map[string]string`). Compilation continues; the malformed mapping is ignored. (There is no longer a `userTask`/`requires_comment` boolean-parse case — every `zeebe:property` on a userTask is forwarded verbatim into `StageDef.Extras` without validation; the compiler does not special-case any property name.)

25. **`MISSING_MESSAGE_DEFINITION`** *(warning severity)*
    - **Trigger Scenario**: A `<bpmn:messageFlow>`, `<bpmn:receiveTask>`, or `<bpmn:sendTask>` references a message ID that is not declared as a `<bpmn:message>` in the root `<bpmn:definitions>`; or a message boundary event's name cannot be resolved from `<bpmn:message>` definitions or collaboration message flows.
    - **Trigger Logic**: Two cases emit this code as a **warning** (compilation proceeds). (a) *Collaboration-level*: the validator builds a set of all declared message IDs; if any `messageRef` attribute on a `messageFlow`, `receiveTask`, or `sendTask` points to an undeclared ID, this warning is emitted (previously an error). (b) *Boundary event*: the validator calls `ResolveMessageName` (by `messageRef`) and falls back to `resolveMessageNameFromFlows` (by element ID in collaboration message flows); if the name cannot be resolved by either path, this warning is emitted against the boundary event node ID in `bpmn_compiler/validator/boundary.go`.

26. **`UNMATCHED_MESSAGE_FLOW`**
    - **Trigger Scenario**: A `<bpmn:messageFlow>` references a source or target node that does not exist in the collaboration.
    - **Trigger Logic**: The validator resolves `messageFlow.sourceRef` and `messageFlow.targetRef` against all known element IDs. Any dangling reference triggers this error.

27. **`MAX_DEPTH_EXCEEDED`**
    - **Trigger Scenario**: The longest forward path from the start event to the end event exceeds the maximum node depth (2000), guarding against stack exhaustion / DoS during recursive graph compilation (§4.4). Subprocess paths are counted independently.
    - **Trigger Logic**: After back-edges are stripped, the validator computes the longest forward path (memoised DFS). If it exceeds `maxPathDepth` (2000), this error is emitted against the start event.

28. **`UNRESOLVED_CALLED_ELEMENT`**
    - **Trigger Scenario**: A `<bpmn:callActivity>` is missing its `<zeebe:calledElement processId="...">` extension element, or the `processId` references a process that is not defined in the same BPMN document.
    - **Trigger Logic**: `ValidateCallActivities` in `bpmn_compiler/validator/call_activity.go` checks each callActivity for a non-empty `processId` that resolves to a sibling `<bpmn:process>` in the same definitions. The called process must be embedded in the document (injected by the thin BE before upload); remote references are not supported.

29. **`NESTED_SUBPROCESS_NOT_SUPPORTED`**
    - **Trigger Scenario**: A `<bpmn:subProcess>` element contains another `<bpmn:subProcess>` directly.
    - **Trigger Logic**: `ValidateProcess` in `bpmn_compiler/validator/validator.go` checks each subProcess for nested child subProcess elements. Only one level of subprocess nesting is supported.

30. **`MISSING_DEPT_INPUT_FOR_MODULE`**
    - **Trigger Scenario**: A `<bpmn:callActivity>` references a called process that has no lanes, but the callActivity's `<zeebe:ioMapping>` does not include a `dept_id` input or a `target="Depts"` dict-injection input.
    - **Trigger Logic**: `ValidateCallActivities` in `bpmn_compiler/validator/call_activity.go` checks whether the called process has a laneSet. If not, it verifies that either a `dept_id` input or a `target="Depts"` input is present in the callActivity's ioMapping. Absent → `MISSING_DEPT_INPUT_FOR_MODULE`.

---

### 3.2 Endpoint Registry

Complete JSON schemas, parameter rules, and data structures are maintained in the [OpenAPI Specification](definition_openapi.yaml).

| Endpoint | Method | Required Roles | Description |
| --- | --- | --- | --- |
| `/workflows` | `GET` | Any | List tenant workflows (paginated, with search, key, is_valid, status, & has_draft filters) |
| `/workflows` | `POST` | Admin | Register new workflow key and create its initial DRAFT workspace |
| `/workflows/:id` | `GET` | Any | Get root workflow metadata and inline version history list (capped by versions_limit) |
| `/workflows/:id/versions` | `GET` | Any | Get paginated list of all versions for a workflow |
| `/workflows/:id/versions/:version_id` | `GET` | Any | Retrieve raw BPMN XML and compiled JSON plan for a version |
| `/workflows/:id/draft` | `GET` | Any | Retrieve detail of the active DRAFT version (XML & compiled plan if valid) |
| `/workflows/:id/draft` | `POST` | Admin | Initialize a new active workspace DRAFT from the current active version |
| `/workflows/:id/draft` | `PUT` | Admin | Update active workspace DRAFT XML/name/description |
| `/workflows/:id/draft` | `DELETE` | Admin | Discard/delete the active workspace DRAFT |
| `/workflows/:id/versions/:version_id/publish` | `POST` | Admin | Compile and publish DRAFT, promoting it to active status |
| `/workflows/:id/archive` | `POST` | Admin | Archive the workflow and set active version to NULL |
| `/workflows/:id/versions/:version_id/clone` | `POST` | Admin | Clone a published/archived version into a new workflow key (as a new draft) |
| `/workflows/:id/versions/:version_id/promote` | `POST` | Admin | Promote an older published version to active (rollback/rollforward) |
| `/workflows/validate` | `POST` | Any | Validate raw BPMN XML on-the-fly (stateless validation) |
| `/workflows/:id/versions/:version_id/export` | `GET` | Any | Export raw BPMN XML file attachment (supports DRAFT & published versions) |
| `/workflows/:id/versions/:version_id/diff/:target_version_id` | `GET` | Any | Get structural and metadata diff between two versions |
| `/connectors/registry` | `GET` | Any | Serve the modeler-facing element-template (authoring form) list/detail for each registered connector type, generated from `pkg/registry` (§10.14/§10.15) |
| `/connectors/credentials` | `POST` | Admin | Accept a provider credential for a connector task at authoring time (`storage`/`send-email`/`document-extract`/`chat-notify`), write it to OpenBao, and return the resulting secret path for use in the task's compiled `IOMapping` (§10.15) |
| `/bpmn/allowed-elements` | `GET` | Any | Serve the modeler-facing BPMN element allowlist (§4.1.2's enforcement list, mirrored) so the frontend's Camunda-based canvas can restrict its palette to what this service will actually accept. UX convenience only — this service's own §4.1.2 422 rejection remains the authoritative enforcement, not the palette (§10.18) |
| `/modules` | `GET` | Any | List reusable BPMN modules visible to the caller (global + their own tenant's), `?scope=`/`?q=` filters (§3.3.18) |
| `/modules` | `POST` | Admin | Create a new module (tenant-scoped; `platform_operator` role required for `scope=global`, §10.16) |
| `/modules/:id` | `GET` | Any | Get a module's metadata + its latest published version's BPMN XML |
| `/modules/:id/versions` | `POST` | Admin | Add a new draft version to an existing module (mirrors `/workflows/:id/draft`'s lifecycle) |
| `/modules/:id/versions/:version_id/publish` | `POST` | Admin | Publish a module draft version, promoting it to active |
| `/modules/:id` | `DELETE` | Admin | Archive a module (soft delete — existing compiled plans that already bundled its XML are unaffected) |
| `/starters` | `GET` | Any | List starter workflows visible to the caller (global + their own tenant's), `?scope=`/`?category=`/`?q=` filters (§3.3.19) |
| `/starters` | `POST` | Admin | Create a starter directly from a raw BPMN XML payload (tenant-scoped; `platform_operator` role required for `scope=global`) |
| `/starters/from-workflow-version/:version_id` | `POST` | Admin | Snapshot one of the caller's own published workflow versions into a new tenant-scoped starter ("save as reusable") |
| `/starters/:id` | `GET` | Any | Get a starter's metadata + its BPMN XML, ready to submit via `POST /workflows` |
| `/starters/:id` | `DELETE` | Admin | Delete a starter (tenant-scoped rows only, or `platform_operator` for `scope=global`) |
| `/healthz` | `GET` | Any | Liveness probe (returns 200 OK) |
| `/readyz` | `GET` | Any | Readiness probe (performs database dependency connection checks, returns 200/503) |
| `/internal/events` | `POST` | Internal (service-to-service) | Ingest a domain-event envelope forwarded by the shared workflow-events consumer (e.g. `department.membership.revoked`). Not exposed on the public gateway. |
| `DefinitionService/GetCompiledWorkflow` | `gRPC` | Internal | High-throughput mTLS internal gRPC fetch for Execution Service |

---

### 3.3 Endpoint Specifications

#### 3.3.1 List Tenant Workflows

- **GET** `/workflows`
- **Query Params**:
  - `page` (default 1): Pagination page number.
  - `limit` (default 20): Page size (max 100).
  - `search` (optional): Fuzzy search on `name`.
  - `key` (optional): Exact business key match (`business_key = :key`). Used by downstream services (e.g. Execution Service) to bootstrap a workflow start.
  - `is_valid` (optional): Filters on `workflow_version.is_valid` of the workflow's active version (`workflow.active_version_id → workflow_version.id`). `true` returns workflows whose active version has all assignees valid; `false` returns those invalidated by a `department.membership.revoked` event. Workflows with no active version (`active_version_id IS NULL`) are excluded when this filter is set because there is no active version to evaluate.
  - `status` (optional): Filters by workflow active-version state. `active` → `active_version_id IS NOT NULL` (has a published active version). `archived` → `active_version_id IS NULL` (no active published version — either the active version was archived via §3.3.8, or the workflow was never published and only has drafts). The `workflow` table has no status column; this filter is derived entirely from the presence or absence of `active_version_id`.
  - `has_draft` (optional, boolean): Filters workflows that have an active draft. Implemented as `EXISTS (SELECT 1 FROM workflow_version WHERE workflow_id = w.id AND status = 'DRAFT')`.
- **Response (`200 OK`)**: List of workflows with metadata, current active version pointer, and status:

  ```json
  {
    "workflows": [{
      "id": "7ca648b2-b432-4744-884c-35fd556a310c",
      "key": "tender-review",
      "name": "Tender Document Review Process",
      "active_version_id": "8fa88cde-824c-47bc-836b-665cd42c2222",
      "active_version_number": 3,
      "has_draft": true
    }],
    "pagination": { "total_count": 1, "page": 1, "limit": 20 }
  }
  ```

#### 3.3.2 Create Workflow Definition

- **POST** `/workflows`
- **Request Body**: `key` (business identifier), `name`, `description`, `bpmn_xml` (BPMN 2.0 source).
- **Response (`201 Created`)**:

  ```json
  {
    "workflow_id": "7ca648b2-b432-4744-884c-35fd556a310c",
    "version_id": "11abcc22-3844-42bc-938b-665cd42c1111",
    "status": "DRAFT",
    "version_number": null
  }
  ```

#### 3.3.3 Get Workflow Root Details

- **GET** `/workflows/:id`
- **Query Params**:
  - `versions_limit` (default 20, max 100): Caps the inline `versions` history array returned.
- **Response (`200 OK`)**: Retrieves the root details along with a simplified array of all version history entries (draft, published, or archived) up to `versions_limit`:

  ```json
  {
    "id": "7ca648b2-b432-4744-884c-35fd556a310c",
    "key": "tender-review",
    "name": "Tender Document Review Process",
    "description": "Standard review process for tender proposal documents",
    "active_version_id": "8fa88cde-824c-47bc-836b-665cd42c2222",
    "created_at": "2026-05-10T08:00:00Z",
    "versions": [
      {
        "id": "8fa88cde-824c-47bc-836b-665cd42c2222",
        "version_number": 2,
        "status": "PUBLISHED",
        "is_valid": true,
        "created_by_user_id": "4da18bde-7244-47ac-986c-665cd42caaaa",
        "published_at": "2026-05-14T09:00:00Z"
      }
    ]
  }
  ```

#### 3.3.3.1 Get Paginated Workflow Versions

- **GET** `/workflows/:id/versions`
- **Query Params**:
  - `page` (default 1): Pagination page index.
  - `limit` (default 20, max 100): Page size.
- **Response (`200 OK`)**: Retrieves the paginated full history list of workflow versions:

  ```json
  {
    "versions": [
      {
        "id": "8fa88cde-824c-47bc-836b-665cd42c2222",
        "version_number": 2,
        "status": "PUBLISHED",
        "is_valid": true,
        "created_by_user_id": "4da18bde-7244-47ac-986c-665cd42caaaa",
        "published_at": "2026-05-14T09:00:00Z"
      }
    ],
    "pagination": { "total_count": 1, "page": 1, "limit": 20 }
  }
  ```

#### 3.3.4 Get Workflow Version Details

- **GET** `/workflows/:id/versions/:version_id`
- **Response (`200 OK`)**: Returns the detailed version metadata, validation flags, the raw `bpmn_xml` payload, and the pre-compiled `compiled_plan_json` block.

#### 3.3.4.1 Get Active Workflow Draft

- **GET** `/workflows/:id/draft`
- **Response (`200 OK`)**: Returns the active draft version details. Has the exact same response schema as `GET /workflows/:id/versions/:version_id` (but status is guaranteed to be `DRAFT`).
- **Response (`404 Not Found`)**: If no active draft workspace exists for this workflow.

#### 3.3.5 Initialize Workflow Draft

- **POST** `/workflows/:id/draft`
- **Invariant**: A workflow may hold at most one `DRAFT` version at any time. If a draft already exists, returns a `409 Conflict` (`DRAFT_ALREADY_EXISTS`).
- **Response (`201 Created`)**: Copies the current active version into a new version block with `DRAFT` status.
- **Edge Case (No Active Version)**: If the workflow has never been published (i.e., it only had an initial draft) and the draft was discarded using `DELETE /workflows/:id/draft`, the workflow has no active version pointer (`active_version_id = NULL`). In this state, calling `POST /workflows/:id/draft` has no base version to copy from and returns `409 Conflict` with code `NO_ACTIVE_VERSION`. The admin must delete and recreate the workflow definition or upload a brand new XML via `POST /workflows` to start again.

#### 3.3.6 Update Workflow Draft

- **PUT** `/workflows/:id/draft`
- **Request Body**:

  ```json
  {
    "name": "Tender Document Review Process (Updated)",
    "description": "Updated review process with additional stages",
    "bpmn_xml": "<bpmn:definitions ...>...</bpmn:definitions>",
    "record_version": 3
  }
  ```

- **Semantics**: Updates the active workspace draft. Implements optimistic concurrency control via `record_version` (a monotonic counter): the caller supplies the token via `If-Match: "<record_version>"` header (preferred) or the `record_version` body field — header takes precedence when both are present (§10.11). The update query filters on `AND record_version = :client_version`; if another update has been applied in the interim, `RowsAffected = 0` → `409 Conflict` (`DRAFT_CONCURRENCY`). The response includes the new `record_version` value. Using a monotonic counter rather than `last_updated_at` avoids false conflicts from sub-millisecond concurrent clock resolution. Validates that the input is well-formed XML on save, but defers semantic graph validation and compilation to the explicit **publish** or **validate** actions.

#### 3.3.7 Publish Workflow Version

- **POST** `/workflows/:id/versions/:version_id/publish`
- **Request Body**: `{"force_publish_structural": false}`
- **Semantics**: Computes structural diff compared to the previous version. If structural elements changed (e.g. split/join gateways, routing steps) and `force_publish_structural` is `false`, returns `409 Conflict` (recommending a new workflow key).
- **Overriding**: If `force_publish_structural` is `true`, the publish transaction overrides the warning and registers the new version under the existing key.

#### 3.3.8 Archive Workflow

- **POST** `/workflows/:id/archive`
- **Semantics**: Sets the active published version status to `ARCHIVED` and clears the active pointer on the root record (`active_version_id = NULL`). Physical SQL deletion of workflows/versions is strictly prohibited due to audit trail requirements.
- **Precondition**: Before archiving, the Definition Service makes a synchronous outbound gRPC call to the Execution Service (`CheckActiveInstances(workflow_id, tenant_id)`) to verify that no instances are in `RUNNING` or `PAUSED` state for this workflow definition. If any active instances are detected, the request immediately terminates and returns `409 Conflict` (`ACTIVE_INSTANCES_EXIST`). See §3.4.2 for the message contract.
- **Archive semantics**: Archive is non-destructive to in-flight instances. Running instances hold their compiled plan from the moment they started — they never re-fetch it, so archiving the template version has no effect on them. Archive only blocks *new* instance starts. The `CheckActiveInstances` guard is an operational safety net to prevent accidental archiving while work is actively in progress; it does not protect running instances from being broken by archive (they cannot be).

#### 3.3.9 Discard Workspace Draft

- **DELETE** `/workflows/:id/draft`
- **Response (`204 No Content`)**: No response body — the draft is gone.
- **Semantics**: Deletes the un-published `DRAFT` record. Physical deletion is safe here as no runtime instances have been spawned from this draft.

#### 3.3.10 Clone Workflow Version

- **POST** `/workflows/:id/versions/:version_id/clone`
- **Request Body**: `{"new_key": "tender-review-copy", "new_name": "...", "new_description": "..."}`
- **Semantics**: Registers a new root workflow and initializes its first draft using the source version's XML.
  - **Status Constraint**: Only `PUBLISHED` or `ARCHIVED` versions are cloneable. Attempting to clone a `DRAFT` version returns `409 Conflict` (`INVALID_VERSION_STATUS`).
  - **Plan Quota Checking**: Because cloning registers a new root workflow definition, it must check the tenant's plan quota. If the workflow template limit has been reached, the transaction fails with `403 Forbidden` (`PLAN_QUOTA_EXCEEDED`).
  - **Event Emission**: None. A clone creates a DRAFT — no downstream service needs to react until the draft is published. The published event is emitted when the admin publishes the new draft in the normal flow.
- **Response (`201 Created`)**: Registers a new root workflow and draft version.

#### 3.3.11 Promote Version to Active (Rollback/Rollforward)

- **POST** `/workflows/:id/versions/:version_id/promote`
- **Semantics**: Atomic pointer swap to select an older published version as the active pointer (`active_version_id`).
  - **Status Constraint**: Only `PUBLISHED` versions can be promoted. Attempting to promote a version in `DRAFT` or `ARCHIVED` status returns `409 Conflict` (`INVALID_VERSION_STATUS`).
  - **Already Active Pointer (Early Exit / Silent No-Op)**: If the target version is already set as the `active_version_id` for the workflow, the API returns a silent `200 OK` early exit without creating a new audit record or writing to the transactional outbox.
  - **Event Emission**: Automatically logs a `workflow.template.published` outbox event so downstream execution instances sync to this version.

#### 3.3.12 Raw BPMN Validation

- **POST** `/workflows/validate`
- **Request Body**: `{"bpmn_xml": "..."}`
- **Response (`200 OK` or `422 Unprocessable Entity`)**: Returns stateless validation results on-the-fly to support real-time builder linting:

  ```json
  {
    "is_valid": false,
    "errors": [
      {
        "code": "MISSING_TASK_DEFINITION",
        "node_id": "UserTask_112",
        "message": "userTask is missing zeebe:taskDefinition"
      }
    ]
  }
  ```

#### 3.3.13 Export Workflow BPMN

- **GET** `/workflows/:id/versions/:version_id/export`
- **Semantics**: Exports the raw BPMN XML file for any version. `DRAFT` versions are fully exportable, allowing administrators/designers to export work-in-progress XML structures.
- **Response (`200 OK`)**: File download attachment with content type `application/xml`.

#### 3.3.14 Get Version Diff

- **GET** `/workflows/:id/versions/:version_id/diff/:target_version_id`
- **Response (`200 OK`)**: Structured JSON payload highlighting added/removed departments and step-level structural alterations.

#### 3.3.15 Liveness Probe

- **GET** `/healthz`
- **Response (`200 OK`)**: Standard service health probe returning `{"status": "OK"}` in JSON format.

#### 3.3.16 Readiness Probe

- **GET** `/readyz`
- **Semantics**: Checks both database connectivity (`pool.Health`) and cache reachability (`cache.Ping`).
- **Response (`200 OK`)**: Both dependencies are healthy. Returns:

  ```json
  {
    "status": "OK",
    "db_utilization": 0.3,
    "db_conns": 3,
    "db_max_conns": 10
  }
  ```

- **Response (`503 Service Unavailable`)**: Database pool health check failed → `{"status":"unavailable","db":"unreachable"}`; or cache ping failed → `{"status":"unavailable","cache":"unreachable"}`.

---

#### 3.3.17 Internal Event Ingest

- **POST** `/internal/events`
- **Caller**: The shared workflow-events consumer (service-to-service). Mounted on an internal route group — **not** behind `ProtectedMiddlewares` and **not** exposed through the Envoy gateway. Authenticated by service-to-service auth (shared-secret header / mTLS / network policy).
- **Semantics**: Accepts a single `events.Envelope[json.RawMessage]`, injects the RLS GUC from the envelope `tenant_id` (`pgcommon.WithGUCSet`, §14), and dispatches on `type`. Today it handles `department.membership.revoked` → `HandleMembershipRevoked` (the full flow is in §7.4.2). Idempotent via `processed_event` keyed on the envelope `id`.
- **Request body**:

  ```json
  {
    "id": "f0a11222-3844-42bc-938b-665cd42c9999",
    "type": "department.membership.revoked",
    "tenant_id": "7ca648b2-b432-4744-884c-35fd556a310c",
    "data": { "user_id": "3fa1...", "department_id": "...", "role": "reviewer" }
  }
  ```

- **Responses**:
  - `200 OK` / `204 No Content` — processed, or an idempotent no-op for an already-seen `id`. The consumer marks the message handled.
  - `400 Bad Request` — malformed envelope/payload or unknown required field. Non-retryable; the consumer routes the message to its DLQ.
  - `500 Internal Server Error` — transient failure (DB/transaction). The consumer retries and eventually DLQs per its `maxReceiveCount`.
- **Note**: Unknown `type` values are accepted and ignored (logged, `2xx`) so adding new event types upstream does not break delivery.

---

#### 3.3.18 Reusable BPMN Modules

Modules are tenant-authored (or platform-authored, `scope=global`) BPMN fragments meant to be referenced from a main diagram's `<bpmn:callActivity>` and merged in at compile time via the existing `Bundle()` mechanism (§4.1.3.3) — this section only adds a persisted, versioned, browsable store in front of that already-existing merge step; `Bundle()` itself is unchanged.

- **GET** `/modules`
  - **Query Params**: `scope` (`global`|`tenant`|`all`, default `all`), `q` (name substring search), `page`/`limit` (pagination, same defaults as §3.3.3.1).
  - **Response (`200 OK`)**:

    ```json
    {
      "modules": [
        {
          "id": "9b2e1a10-...",
          "scope": "tenant",
          "name": "Standard Two-Level Approval",
          "description": "Manager approval followed by finance sign-off.",
          "active_version_id": "c1d2e3f4-...",
          "created_at": "2026-08-10T12:00:00Z"
        }
      ],
      "pagination": { "total_count": 1, "page": 1, "limit": 20 }
    }
    ```

- **POST** `/modules`
  - **Request Body**: `name`, `description`, `bpmn_xml` (must contain exactly one top-level `<bpmn:process>`, which becomes the module's `process_id`, the value a referencing diagram's `callActivity` `calledElement` must match), `scope` (`tenant`, the default; `global` requires the caller to hold `platform_operator`, returning `403` otherwise — mirrors Org & Membership's own `platform_operator`-only gate on its global department catalog, §10.16).
  - **Response (`201 Created`)**: same shape as §3.3.2's create-workflow response (`module_id`, `version_id`, `status: "DRAFT"`, `version_number: null`) — the first version is always a draft, publish it via the next endpoint before it's usable in a compile.
  - The submitted `bpmn_xml` runs through the same parser/element-allowlist validation as a normal workflow draft (§4.1) before being stored — a module is BPMN content like any other, not exempt from Tier validation.

- **POST** `/modules/:id/versions` — add a new draft version to an existing module. Same request/response shape as create; increments `version_number`.
- **POST** `/modules/:id/versions/:version_id/publish` — publishes a draft version (validates + compiles the fragment standalone, sets `is_valid`/`validation_errors_json`, `status → PUBLISHED`, updates the module's `active_version_id`). Mirrors §3.3.7 exactly; a module version follows the identical DRAFT → PUBLISHED → ARCHIVED lifecycle a workflow version does (§2.1).
- **GET** `/modules/:id` — module metadata + its active (published) version's `bpmn_xml`, ready for the frontend to hand to `Bundle()` via a referencing diagram's `module_bpmn_xmls` map.
- **DELETE** `/modules/:id` — archives the module (`active_version_id → NULL`); does not retroactively affect any already-compiled plan that bundled a snapshot of its XML, since `Bundle()` copies content in at compile time rather than holding a live reference.

#### 3.3.19 Starter Workflows

Starters are complete, ready-to-use BPMN diagrams a tenant can start authoring from — a point-in-time snapshot, not a live link to any workflow. "Using" a starter is nothing more than handing its `bpmn_xml` to the ordinary `POST /workflows` (§3.3.2) a human pasting hand-authored XML would use — this service gains no new compile-time or publish-time behavior for starters at all.

- **GET** `/starters`
  - **Query Params**: `scope` (`global`|`tenant`|`all`, default `all`), `category` (free-text filter, e.g. `approval`, `onboarding`), `q` (name search), `page`/`limit`.
  - **Response (`200 OK`)**:

    ```json
    {
      "starters": [
        {
          "id": "4f5e6d7c-...",
          "scope": "global",
          "name": "Standard Purchase Approval",
          "description": "Two-step manager + finance approval for purchase requests.",
          "category": "approval",
          "created_at": "2026-08-01T09:00:00Z"
        }
      ],
      "pagination": { "total_count": 1, "page": 1, "limit": 20 }
    }
    ```

- **GET** `/starters/:id` — returns the starter's full `bpmn_xml` alongside its metadata. The frontend submits that `bpmn_xml` unmodified as the `bpmn_xml` field of a `POST /workflows` call to actually create a new tenant workflow from it — this endpoint does not itself create anything.
- **POST** `/starters` — `name`, `description`, `category`, `bpmn_xml`, `scope` (same `tenant`-default/`platform_operator`-for-`global` rule as modules). Runs through the same parser/element-allowlist validation as any BPMN upload before being stored.
- **POST** `/starters/from-workflow-version/:version_id` — convenience endpoint: reads the caller's own `workflow_version` row (must belong to their tenant, RLS-enforced; must be `PUBLISHED` or `ARCHIVED`, same status constraint as `/clone`, §3.3.10) and snapshots its `bpmn_xml` into a new `scope=tenant` starter — the "save one of my own workflows as a reusable starter" action. Request body: `{"name": ..., "description": ..., "category": ...}`. `source_workflow_version_id` is recorded for traceability only (informational; the starter's content is copied, not linked, so a later edit to the source workflow never changes an already-created starter).
- **DELETE** `/starters/:id` — deletes the row (tenant-scoped rows only via the normal RLS-bound role; `platform_operator` required for `scope=global`, same as creation).

#### 3.3.20 BPMN Element Allowlist Discovery

- **GET** `/bpmn/allowed-elements`
- **Response (`200 OK`)**: the same allowlist `pkg/enums.AllowedBPMNElements` (`workflow-models`, §10.18) that §4.1.2's compiler enforcement already reads — one shared source, no separate maintenance:

  ```json
  {
    "elements": [
      "startEvent", "endEvent", "userTask", "exclusiveGateway",
      "parallelGateway", "inclusiveGateway", "sequenceFlow",
      "callActivity", "subProcess", "sendTask", "receiveTask",
      "boundaryEvent", "serviceTask"
    ]
  }
  ```

  `serviceTask`'s presence in this list is unconditional — the `connector:`-prefix scoping (§4.1.2, §10.13) is a compile-time attribute check this endpoint does not attempt to encode; a modeler UI restricting its palette to this list still relies on the compiler's own 422 to catch a `serviceTask` with no valid `connector:` type. This endpoint is a UX convenience for configuring the palette, never the enforcement boundary (§10.18) — a bug or bypass in the frontend's palette restriction cannot let an actually-disallowed element through; §4.1.2's server-side rejection is unconditional and doesn't consult this endpoint or trust anything the client sends.

---

### 3.4 gRPC Internal API

All pod-to-pod communication within the service mesh uses mTLS gRPC, enforced by Envoy. This section documents both the inbound RPC this service exposes and the single outbound RPC it calls on the Execution Service.

**Shared transport rules** (apply to both directions):

- mTLS enforced via the Envoy sidecar; plain-text connections are rejected.
- `tenant_id` is carried in the gRPC request payload (not metadata) and is used to set the `app.tenant_id` GUC for RLS before any DB access.
- Standard gRPC status codes apply: `OK` (0), `INVALID_ARGUMENT` (3), `NOT_FOUND` (5), `PERMISSION_DENIED` (7), `INTERNAL` (13).

---

#### 3.4.1 Inbound — `GetCompiledWorkflow`

Provides high-throughput, synchronous access to the compiled workflow plan/DSL for the Execution Service and Temporal Workers during runtime instantiation and task processing. Bypasses the Envoy REST gateway to eliminate serialization overhead.

**Service Definition** (`workflow/definition/v1/definition_service.proto`):

```protobuf
syntax = "proto3";

package workflow.definition.v1;

option go_package = "github.com/bcbpsolutions/workflow-definition-svc/gen/proto/v1;definitionv1";

service DefinitionService {
  // Called by Execution Service / Temporal Workers to fetch the compiled DSL.
  rpc GetCompiledWorkflow(GetCompiledWorkflowRequest) returns (GetCompiledWorkflowResponse);
}

message GetCompiledWorkflowRequest {
  string tenant_id = 1;             // Mandated for RLS isolation
  string workflow_version_id = 2;   // UUID of the requested version
}

message GetCompiledWorkflowResponse {
  string workflow_id = 1;
  string version_id = 2;
  int32 version_number = 3;
  string status = 4;                // DRAFT, PUBLISHED, ARCHIVED
  bool is_valid = 5;
  string compiled_plan_json = 6;    // Pre-compiled JSON string containing the ExecutionPlan DSL
}
```

---

#### 3.4.2 Outbound — `CheckActiveInstances`

Called by the Definition Service on the Execution Service as a precondition guard during the archive flow (§3.3.8). The full service definition lives in the Execution Service LLD; the message contract consumed by this service is reproduced below for reference.

**Client stub** (`workflow/execution/v1/execution_service.proto`, consumed read-only):

```protobuf
syntax = "proto3";

package workflow.execution.v1;

// Client-side view only — canonical definition owned by the Execution Service.
service ExecutionService {
  // Returns the count of workflow instances currently in RUNNING or PAUSED state.
  // Definition Service calls this before archiving a workflow to enforce the
  // ACTIVE_INSTANCES_EXIST (409) guard.
  rpc CheckActiveInstances(CheckActiveInstancesRequest) returns (CheckActiveInstancesResponse);
}

message CheckActiveInstancesRequest {
  string tenant_id = 1;    // Tenant scope for the query
  string workflow_id = 2;  // Root workflow UUID (not version-specific)
}

message CheckActiveInstancesResponse {
  bool   has_active = 1;   // true if any instance is RUNNING or PAUSED
  int32  count     = 2;    // Total number of active instances found
}
```

**Error handling**: If the Execution Service is unreachable or returns a non-OK gRPC status, the archive request fails with `503 Service Unavailable` (`UPSTREAM_UNAVAILABLE`). The archive is never committed while the guard call is unresolvable.

---

## 4. Core Modules: Parser, Compiler & Validation

This section details the internal mechanics of the BPMN Parser, the Graph Compilation Engine, and the Validation Rules.

### 4.1 BPMN Parser Internals

The Parser maps raw BPMN 2.0 XML using Go's standard `encoding/xml` package. It extracts workflow structure from standard BPMN elements and runtime configuration from Zeebe extension properties inside `<bpmn:extensionElements>`. Only the elements and properties listed below are recognized; anything outside this allowlist is silently ignored during parsing and flagged during validation.

---

#### 4.1.1 Required XML Namespace Declarations

A valid BPMN file submitted to this service **must** declare the following namespaces on the root `<bpmn:definitions>` element. The parser validates namespace presence before attempting any structural parsing.

| Prefix | Namespace URI | Required | Purpose |
| --- | --- | --- | --- |
| `bpmn` | `http://www.omg.org/spec/BPMN/20100524/MODEL` | Yes (Enforced) | Core BPMN 2.0 model elements |
| `zeebe` | `http://camunda.org/schema/zeebe/1.0` | Yes (Enforced) | Zeebe extension properties (custom metadata) |
| `bpmndi` | `http://www.omg.org/spec/BPMN/20100524/DI` | Expected (Not Enforced) | Diagram interchange (layout) |
| `dc` | `http://www.omg.org/spec/DD/20100524/DC` | Expected (Not Enforced) | Diagram common (bounds) |
| `di` | `http://www.omg.org/spec/DD/20100524/DI` | Expected (Not Enforced) | Diagram interchange (edges) |

Missing `bpmn` or `zeebe` namespaces cause an immediate `400 Bad Request` before full parsing begins. The diagram interchange namespaces (`bpmndi`, `dc`, `di`) contain layout bounds and coordinates used by visual canvas modeling tools, but are not strictly enforced during execution plan compilation.

---

#### 4.1.2 Supported BPMN Elements

Elements are classified into three tiers. The raw-XML denylist scan (`rejectedElements` / `unsupportedElements` in `parser.go`) covers all Tier 2 and Tier 3 tag names explicitly, because `encoding/xml` silently drops unknown elements rather than surfacing errors. The Tier 1 allowlist this section documents is sourced from `workflow-models`' shared `pkg/enums.AllowedBPMNElements` (§10.17) — this compiler's own enforcement, not just a description of it; a modeler UI's palette-configuration copy comes from the same source via `GET /bpmn/allowed-elements` (§3.3.20), but that endpoint is never the enforcement boundary — a bug or bypass there cannot let a disallowed element through this parser.

##### Tier 1: Fully Supported

> Root Structure

- `<bpmn:definitions>` — attrs: `id`, `targetNamespace`. Exactly 1.
- `<bpmn:collaboration>` — optional wrapper for multi-participant models. When present, every participant with a matching `processRef` compiles to a separate `CompiledPlan` inside a `CompiledCollaboration`. The participant whose pool has lanes and user tasks is identified as the `MainPlan` (primary Temporal entry point); all other pools compile as `Ignored: true` plans used for routing and call-pool steps. `isExecutable="true"` marks a real executable process; `isExecutable="false"` marks a visual-only pool (e.g. an external-party context pool). The compiler skips `isExecutable="false"` pools entirely — they are not compiled and cannot serve as called processes. The distinction between root and called processes is determined by whether the process ID appears as a `zeebe:calledElement` target in another process, not by this flag.
- `<bpmn:participant>` — attrs: `id`, `name`, `processRef`. `isExecutable` is parsed and enforced: `"false"` → compiled as `Ignored: true`; `"true"` → compiled as a full plan.

  **Ignored pool** — a participant process may carry `<zeebe:property name="ignore" value="true"/>` inside its process-level `<zeebe:properties>`. The compiler detects this via `isIgnoredProcess` and skips the pool entirely for validation and compilation. The pool exists in the collaboration solely to model message-flow context for the main pool (e.g. an external party that receives a message and independently starts its own flow). Contrast with `isExecutable="false"` participants (visual-only, no business logic): ignored pools ARE `isExecutable="true"` and may have lanes and tasks, but the compiler opts them out of the main compile pipeline via this Zeebe property rather than the BPMN attribute. Ignored pools are not compiled into `CompiledPlan` artifacts; they do not appear in `CompiledCollaboration.Plans`.

- `<bpmn:process>` — attrs: `id`, `name`. 1 per definitions (standalone) or 1 per participant (collaboration).
- `<bpmn:laneSet>` — attr: `id`. Exactly 1 per process.
- `<bpmn:lane>` — attrs: `id`, `name`. 1–100 per laneSet. Maps 1:1 to a `DepartmentDef`. Lane `name` is the authoritative department identifier; the Profile Service owns the format.
- `<bpmn:flowNodeRef>` — text content is a node ID. Associates flow elements to a lane. Determines task department.
- `<bpmn:error>` — root-level named error definition; `id` and `errorCode` attrs. Referenced by error end events and error boundary events.
- `<bpmn:message>` — root-level message definition; `id` and `name` attrs. Referenced by `messageFlow`, `receiveTask`, and message start events.

> Flow Nodes

- `<bpmn:startEvent>` — Exactly 1 per process. Supported definitions: blank (manual trigger), `<bpmn:messageEventDefinition>` (correlated start from a messageFlow), `<bpmn:timerEventDefinition>` (scheduled start), `<bpmn:signalEventDefinition>` (broadcast signal start). No incoming flows.
- `<bpmn:endEvent>` — At least 1 per process. Named end events compile to outcome labels. Inside a subprocess, `<bpmn:errorEventDefinition errorRef="..."/>` declares a rejection throw.
- `<bpmn:userTask>` — 1–1000 per process. Must carry `zeebe:taskDefinition` and `zeebe:assignmentDefinition` (§4.1.3). Belongs to exactly one lane.
- `<bpmn:sendTask>` — Outbound message to another participant. Must carry `<zeebe:taskDefinition type="..."/>` identifying the message worker.
- `<bpmn:receiveTask>` — Waits for an inbound message; `messageRef` must reference a declared `<bpmn:message>`. Must carry `<zeebe:subscription messageCorrelationKey="..."/>`.
- `<bpmn:serviceTask>` — **Scoped exception to the Tier 3 rejection below.** Recognized only when `<zeebe:taskDefinition type="connector:<name>"/>` carries the `connector:` prefix — an automatic task, run without a human. Compiles to `StageDef.Type = enums.StageTypeConnector` (`"connector"`) plus `StageDef.ConnectorType = "<name>"` (the prefix is stripped once, at compile time — Execution Service never re-parses it), and `StageDef.IOMapping` from the element's `<zeebe:ioMapping>` (`workflow_models_lib.md` §2.3). Dispatch of the actual connector call happens outside this service entirely — see `workflow_connectors.md` (worker placement is decided there, a `cmd/connector-worker` binary inside `execution_service`'s own repo; this compiler's output was written placement-agnostic and needed no change either way). A `serviceTask` with any other `type`, or none, is still `REJECTED_ELEMENT` exactly as before; this is not a general un-rejection of `serviceTask`. Input mapping via `<zeebe:ioMapping>`, same as `callActivity` (§4.1.2 above). See §4.1.3.3 for the full extension contract.
- `<bpmn:parallelGateway>` — 0–N. Concurrent department execution. Split/join pairs required.
- `<bpmn:exclusiveGateway>` — 0–N. Routing-only branching + guarded rework loops. All outgoing flows from a split must carry `<bpmn:conditionExpression>`. Split/join pairs required.
- `<bpmn:inclusiveGateway>` — 0–N. OR-split/join: one or more branches fire based on `conditionExpression` evaluation. All outgoing flows from a split must carry `<bpmn:conditionExpression>`. Split/join pairs required.
- `<bpmn:eventBasedGateway>` — 0–N. Exclusive wait for the first of N events (timer or signal). All outgoing flows must target an `intermediateCatchEvent`. At least one timer branch required. Not permitted inside a subprocess.
- `<bpmn:subProcess>` (embedded, non-event, non-ad-hoc) — 0–N. Grouped sub-flow with its own start/end events, userTasks, and gateways. Must **not** contain an internal `<bpmn:laneSet>` — tasks inside a subprocess belong to the department of the subprocess's containing lane in the parent process. If a subprocess logically spans multiple departments, those tasks must be modelled as separate activities in the parent process lanes connected via parallel gateways. Must use `<bpmn:userTask>` (not plain `<bpmn:task>`) for all internal activities.
- `<bpmn:callActivity>` — 0–N. References a named reusable sub-process via `<zeebe:calledElement processId="..."/>`. The called process must have `isExecutable="true"`. Called process BPMNs are supplied via `module_bpmn_xmls` on the draft and merged in-memory by the service via `Bundle()` before compile/validate. Steps are **flattened directly into the parent execution plan** — no `SubWorkflowStep` wrapper is emitted (`SubWorkflowStep` is exclusively for inline `bpmn:subProcess`). The called process uses its own lanes as department IDs; if it has no lanes, the callActivity must supply a `dept_id` input in `zeebe:ioMapping`. As an alternative to `dept_id`, a `target="Depts"` input carrying a JSON `map[string]string` source value may be used — see the **Depts dict-injection** pattern below. **Timer and error boundary events on callActivity are not supported** — the compiler returns an error. **Message boundary events ARE supported** on callActivity and compile to `ExecutionStep.MessagePaths` on the flattened step. Recursion detection is not yet implemented.

  **`target="Depts"` dict-injection** — the `Depts` input is a compiler-internal mechanism that allows a single reusable module BPMN (with generic lane names such as "Sender" / "Receiver") to be called from multiple call sites with different actual department IDs per call site. The `source` value must be a JSON `map[string]string` mapping module lane names (case-insensitive) to actual dept IDs — e.g. `{"sender":"Consultant","receiver":"Tender"}`. `extractDeptsMap` in `element/call_activity.go` extracts this mapping; `compileCalledProcess` applies it during compilation to override lane names with actual dept IDs; the mapping is then stripped from `ExecutionStep.IOMapping` so it does not appear in the compiled DSL consumed by the Execution Service. A `target="Depts"` input also satisfies the `MISSING_DEPT_INPUT_FOR_MODULE` check for lane-based modules. If the source value is present but is not valid JSON, a `INVALID_ZEEBE_PROPERTY` warning is emitted. The remap is applied consistently to both `DepartmentDef.ID` entries and the dept references inside the called process's own compiled `ExecutionStep`s (`Sequential`/`Exclusive`/`Parallel`/`MessagePaths`), via `bpmncore.QualifySteps` — a step referencing an un-remapped lane name would otherwise point at a department absent from `plan.Departments`.

> Events (boundary / intermediate)

- `<bpmn:boundaryEvent>` with `<bpmn:timerEventDefinition>` — 0–N. Attached to a `userTask` or `subProcess`. `cancelActivity="true"` (interrupting) or `"false"` (non-interrupting escalation). Duration: ISO 8601 or Go duration string. Compiles to `StageDef.BoundaryTimer`.
- `<bpmn:boundaryEvent>` with `<bpmn:errorEventDefinition>` — 0–N. Attached to a `subProcess` or `callActivity`; always interrupting. Catches named errors (or any error when `errorRef` is empty). Compiles to `SubWorkflowStep.ErrorPaths` (subProcess); on callActivity it passes validation but fails at compile time (see §callActivity above).
- `<bpmn:boundaryEvent>` with `<bpmn:messageEventDefinition>` — 0–N. Attached to a `userTask`, `subProcess`, or `callActivity`. Interrupting or non-interrupting per `cancelActivity`. Zero outgoing sequence flows is valid — a terminal interrupt notification. Compiles to `StageDef.BoundaryMessage` (userTask), `SubWorkflowStep.MessagePaths` (subProcess), or `ExecutionStep.MessagePaths` (callActivity, flattened). Message name resolution: the boundary event's own `messageEventDefinition messageRef` first, falling back to any collaboration `messageFlow` targeting the boundary event's node ID (see §Message Flow Name Resolution).
- `<bpmn:intermediateCatchEvent>` with `<bpmn:timerEventDefinition>` — 0–N. Standalone timer step; must immediately follow an `eventBasedGateway` outgoing flow.

> Connectivity & Zeebe Extensions

- `<bpmn:sequenceFlow>` — 1–N. Both endpoints must resolve to known nodes.
- `<bpmn:messageFlow>` — Cross-participant connectors within a `<bpmn:collaboration>`. `sourceRef`/`targetRef` must resolve to known participant elements; `messageRef`, if present, must reference a declared `<bpmn:message>`.

  **Message Flow Name Resolution**: `name`/`messageRef` on the flow element itself are optional and rarely set in practice — real diagrams almost always put the `messageRef` on the *connected node* instead. `ResolveMessageFlowName` (`bpmncore/compile.go`) resolves the compiled `MessageDef.Name` in this order: (1) the flow's own `name` attribute; (2) the flow's own `messageRef`, resolved against a root `<bpmn:message>`; (3) the target node's own `messageRef` (a `sendTask`/`receiveTask` attribute, or a boundary event's `messageEventDefinition messageRef`, via `nodeMessageRef`); (4) the same lookup on the source node. `ResolveMessageFlowTarget` provides the inverse (node ID → name) for boundary-event resolution when the boundary's own `messageRef` doesn't resolve. If nothing resolves, `ValidateCollaboration` (`validator/collab.go`) emits `MISSING_MESSAGE_DEFINITION` as a **warning** — the flow still compiles with `MessageDef.Name = ""`.
- `<bpmn:extensionElements>` — 0–1 per `userTask`, `sendTask`, `receiveTask`, or `sequenceFlow`.
- `<zeebe:taskDefinition type="...">` — Required on `userTask` and `sendTask`. `type` identifies the stage handler (e.g. `prep`, `review`, `approve`) from the `StageTypeHandler` registry.
- `<zeebe:assignmentDefinition candidateGroups="..." candidateUsers="...">` — Required on `userTask`. `candidateGroups` → role (validated against Membership at publish); `candidateUsers` → single UUID v7 default assignee.
- `<zeebe:subscription messageCorrelationKey="...">` — Required on `receiveTask`. Key used at runtime to correlate incoming messages to the correct process instance.
- `<zeebe:properties>` / `<zeebe:property>` — Optional domain flags. Every property is forwarded verbatim into `StageDef.Extras`; the compiler does not special-case any property name (`requires_comment` is just one convention the Execution Service happens to interpret).
- `<bpmn:conditionExpression>` (child of `sequenceFlow`) — FEEL expression (prefix `= expr`). Required on all outgoing flows of an exclusive or inclusive gateway split. Max 4096 chars. Stored verbatim; evaluated by Execution Service at runtime.

##### Tier 2: Unsupported (Planned — `UNSUPPORTED_ELEMENT`)

These elements are recognized but not yet compiled. They return a structured `UNSUPPORTED_ELEMENT` error (not `REJECTED_ELEMENT`) to distinguish "planned" from "never".

- `<bpmn:intermediateCatchEvent>` with `<bpmn:messageEventDefinition>` — external signal branch from event-based gateway.
- `<bpmn:intermediateCatchEvent>` with `<bpmn:signalEventDefinition>` — broadcast signal catch.
- `<bpmn:intermediateThrowEvent>` with `<bpmn:messageEventDefinition>` — outbound notification.
- `<bpmn:multiInstanceLoopCharacteristics>` on `userTask` — panel voting (all-must-complete with N independent instances).

##### Tier 3: Rejected (`REJECTED_ELEMENT`)

> Tasks / Activities

`bpmn:serviceTask` (except a `connector:`-prefixed `zeebe:taskDefinition type` — see Tier 1 above and §4.1.3.3), `bpmn:scriptTask`, `bpmn:businessRuleTask`, `bpmn:transaction`, `bpmn:adHocSubProcess`

> Gateways

`bpmn:complexGateway`

> Event Definitions

`bpmn:terminateEventDefinition`, `bpmn:compensateEventDefinition`, `bpmn:cancelEventDefinition`, `bpmn:escalationEventDefinition`, `bpmn:conditionalEventDefinition`, `bpmn:linkEventDefinition`, `bpmn:standardLoopCharacteristics`

> Collaboration

`bpmn:conversation`, `bpmn:choreography`

> Data / Object elements (see also §Visual Annotations below)

`bpmn:dataObject`, `bpmn:dataObjectReference`

> Boundary events with rejected definitions

`bpmn:boundaryEvent` with `cancelEventDefinition`, `compensateEventDefinition`, `conditionalEventDefinition`, `escalationEventDefinition`, or `signalEventDefinition`



##### Visual Annotations (Ignored — no execution semantics)

These elements are explicitly **ignored** by the workflow engine. They carry no execution semantics and are treated as diagram-only decorations. They are parsed and collected into `CompiledPlan.visual_elements` for informational output but do not affect compilation or validation.

- `bpmn:dataStore` — logical data store symbol.
- `bpmn:dataStoreReference` — visual reference to a data store; commonly used to annotate that a task writes to an external store.
- `bpmn:dataOutputAssociation`, `bpmn:dataInputAssociation` — connecting arrows between tasks and data store references. Silently dropped by the XML decoder; no action is taken.

---

#### 4.1.3 User Task Extension Contract

Every `<bpmn:userTask>` **must** carry a `<bpmn:extensionElements>` block with the following Zeebe standard extension elements. The parser reads these to construct the `StageDef` struct.

**`<zeebe:taskDefinition type="...">` *(required)***

Stage type — identifies the job worker that handles the task. The default registered set is `prep`, `review`, `approve`, but IAM is the authoritative owner of role/department definitions and may introduce additional types. If `type` is not in the `StageTypeHandler` registry, the task compiles as a passthrough stage (see `UNKNOWN_STAGE_TYPE` warning) — it is **not rejected**. Compiles to `StageDef.Type`, `StageDef.Activity`, and (for unknown types) `StageDef.engine_note`. Example: `<zeebe:taskDefinition type="prep"/>`.

**`<zeebe:assignmentDefinition candidateGroups="..." candidateUsers="...">` *(required)***

- `candidateGroups` — IAM role level name; passed as `?level=<role>` to Membership at publish; max 256 chars. Compiles to `StageDef.Role`.
- `candidateUsers` — single UUID v7 (current limit; enforced via a named validation function for easy relaxation later). Compiles to `StageDef.DefaultAssignees`.

**`<zeebe:properties>` / `<zeebe:property name="..." value="...">` *(optional, any number)***

Every `<zeebe:property>` is forwarded verbatim into `StageDef.Extras` (`map[string]string`) — the compiler does not special-case any property name; the Execution Service and job workers own interpretation. For example, `<zeebe:property name="requires_comment" value="true"/>` appears as `extras["requires_comment"] = "true"` (the Execution Service reads this to require a non-empty comment before task completion), and `<zeebe:property name="sla_category" value="high"/>` appears as `extras["sla_category"] = "high"`.

**Department** — derived from lane membership via `<bpmn:flowNodeRef>`. No `dept_id` property needed. Lane `name` → `DepartmentDef.ID` + `DepartmentDef.Label`. Format is owned by the Profile Service and trusted as-is.

**Condition expressions** — use standard `<bpmn:conditionExpression>` as a direct child of `<bpmn:sequenceFlow>` (not a Zeebe property). FEEL syntax (`= expr` prefix); max 4096 chars. For routing decisions only — not for approval state (use error events for rejection/rework; see §4.1.3.1).

**Example XML block** — user task with all required extensions and a non-interrupting SLA timer:

```xml
<bpmn:userTask id="Task_tender_prep_bnb" name="Prepare Bid-No-Bid">
  <bpmn:extensionElements>
    <zeebe:taskDefinition type="prep"/>
    <zeebe:assignmentDefinition
      candidateGroups="bd-agent"
      candidateUsers="018e1f2a-0000-7000-8000-000000000001"/>
    <zeebe:properties>
      <zeebe:property name="requires_comment" value="false"/>
    </zeebe:properties>
  </bpmn:extensionElements>
</bpmn:userTask>
<!-- Non-interrupting timer: escalation path fires concurrently if not completed in 48h -->
<bpmn:boundaryEvent id="Timer_bnb_prep" attachedToRef="Task_tender_prep_bnb" cancelActivity="false">
  <bpmn:timerEventDefinition>
    <bpmn:timeDuration>PT48H</bpmn:timeDuration>
  </bpmn:timerEventDefinition>
</bpmn:boundaryEvent>
```

#### 4.1.3.1 Routing vs Rejection Semantics

**XOR + `<bpmn:conditionExpression>`** = process routing. Use when a human or system makes an explicit choice that determines the execution path. Examples: bid decision (proceed/decline), tender value routing (standard vs legal review).

**Error events** = rejection / rework. Use when a stage rejects and the work must loop back. The approve-stage worker throws an error code; an error boundary event on the enclosing subprocess catches it and routes the rework path.

```xml
<!-- Inside a subProcess "Strategy Approval" -->
<bpmn:endEvent id="End_strategy_rejected" name="Strategy Rejected">
  <bpmn:errorEventDefinition errorRef="Err_strategy_rejected"/>
</bpmn:endEvent>
<!-- Error boundary on the subProcess itself -->
<bpmn:boundaryEvent id="Catch_strategy_rejected" attachedToRef="SubProcess_strategy">
  <bpmn:errorEventDefinition errorRef="Err_strategy_rejected"/>
</bpmn:boundaryEvent>
```

#### 4.1.3.2 Task SLA / Deadline Modeling

Attach a `<bpmn:boundaryEvent>` with a `<bpmn:timerEventDefinition>` to the user task (see §4.1.2 Tier 1). Duration is expressed in ISO 8601 (`PT24H`, `P3D`) or Go duration syntax (`72h`, `3h30m`). The compiler stores the duration in `StageDef.BoundaryTimer`. The Execution Service creates a Temporal timer that fires at the deadline.

- `cancelActivity="false"` (non-interrupting): task continues; escalation path runs concurrently.
- `cancelActivity="true"` (interrupting): task is cancelled; only the escalation path continues.

**Stage ordering within a department**: Multiple `UserTask` nodes in the same lane form the stage sequence for that department. Their order is determined by topological position in the `sequenceFlow` graph, **not** by XML document order. The compiler resolves stage order during graph traversal (§4.2).

#### 4.1.3.3 Service Task / Connector Extension Contract

Full design in `workflow_connectors.md`; this is the parser-facing contract only.

**`<zeebe:taskDefinition type="connector:<name>">` *(required)***

`<name>` names a connector from the v1 catalogue (`workflow_connectors.md` §6.4 — storage, send-email, document-extract, rest-call, sql-query, chat-notify) — a plain string, not a fixed enum, checked against `workflow-connectors`' lightweight `pkg/registry` package, imported at compile time (deliberately not `pkg/connectors`, the heavier package holding the actual `Execute()` implementations and their AWS/OpenAI/DB-driver SDK dependencies — this service only ever needs the name+schema lookup `pkg/registry` provides; see §10.14). An unrecognized `<name>` warns (`UNKNOWN_CONNECTOR_TYPE`) rather than failing the publish, the same treatment an unrecognized `userTask` type already gets (§4.1.3) — the named connector might still be registered before the workflow is ever instantiated, so hard-failing the publish over it would only block authors prematurely. Whether it's actually registered is a runtime question now, and runtime treats it as a real, visible workflow failure if it isn't (`workflow_connectors.md` §2/§6.5) — not a silently-tolerated gap. A `type` with no `connector:` prefix at all is not a warning — it's `REJECTED_ELEMENT`, per §4.1.4. Compiles to `StageDef.Type`/`StageDef.ConnectorType` as described in §4.1.2 above.

**`<zeebe:ioMapping>` *(required)*** — the connector's declared inputs, in the exact same `<zeebe:input source="..." target="..."/>` shape `callActivity` already uses (§4.1.2). No new XML element or parsing code; only a new element handler feeding the already-parsed `ZeebeIOMapping` into `StageDef.IOMapping` (`workflow_models_lib.md` §2.3) instead of `callActivity`'s department-remap shape. For the `rest-call`/`sql-query` connector types, one input entry is a pre-registered alias name (e.g. `target="endpointAlias"`/`target="queryAlias"`) rather than a raw URL or SQL string — the compiler does not validate the alias itself (that resolution happens at runtime against `cmd/connector-worker`'s own internal-service registry, `workflow_connectors.md` §6.2); a dangling alias fails the workflow the same way an unrecognized connector type does.

**No `<zeebe:assignmentDefinition>`** — a connector task has no human assignee at compile time, and none is ever assigned at runtime either: it completes or fails entirely on its own (`workflow_connectors.md` §5.3/§10 Decision #9), never something authored in the diagram.

**Department** — same as `userTask`: derived from lane membership via `<bpmn:flowNodeRef>`, no separate property needed.

---

#### 4.1.4 Rejected and Unsupported Elements

Two distinct error codes distinguish "never" from "planned":

- **`REJECTED_ELEMENT`** — the element will never be supported in this service.
- **`UNSUPPORTED_ELEMENT`** — the element is on the roadmap but not yet implemented. When implemented, it is removed from the unsupported set and handled by its registered `ElementHandler`; no other parser change is needed.

See §4.1.2 Tier 2 / Tier 3 for the full classification. The code-level lists in `parser.go`:

```go
// rejectedElements — REJECTED_ELEMENT (never)
var rejectedElements = map[string]bool{
    // serviceTask is deliberately NOT in this flat denylist — unlike every other
    // entry here, it needs a conditional check, not a blanket rejection (see below).
    "scriptTask": true, "businessRuleTask": true,
    "transaction": true, "adHocSubProcess": true,
    "complexGateway": true,
    "terminateEventDefinition": true, "compensateEventDefinition": true,
    "cancelEventDefinition": true, "escalationEventDefinition": true,
    "conditionalEventDefinition": true, "linkEventDefinition": true,
    "standardLoopCharacteristics": true,
    "conversation": true, "choreography": true,
    "dataObject": true, "dataObjectReference": true,
    // dataStore and dataStoreReference are NOT rejected — they are visual annotations
    // collected into CompiledPlan.visual_elements (see §Visual Annotations).
}

// unsupportedElements — UNSUPPORTED_ELEMENT (planned; remove when handler is registered)
var unsupportedElements = map[string]bool{
    "inclusiveGateway": true, // parsed and graph-validated but no compile handler yet
}
```

Note: `messageEventDefinition` and `signalEventDefinition` on `intermediateCatchEvent` are NOT in `rejectedElements` (that would block Tier 2 implementation). They are handled at the `TimerCatchHandler` level: if the catch event's definition is not `timerEventDefinition`, the handler emits `UNSUPPORTED_ELEMENT`.

**`serviceTask` is handled the same way, at the element level, not the flat denylist scan.** Every other Tier 3 entry above is rejected outright regardless of its content; `serviceTask` is the one exception with a real, registered Tier 1 handler (§4.1.2) that inspects its `zeebe:taskDefinition type` first: a `connector:`-prefixed value compiles (`workflow_connectors.md`); anything else — including no `type` at all — still emits `REJECTED_ELEMENT`, with a message naming the actual reason (`"serviceTask is only supported with a connector: type"`) rather than the generic Tier-3 message the flat-denylist scan produces for every other entry.

---

#### 4.1.5 XML Security & Upload Hardening

Before any XML parsing begins, the service enforces the following hard limits to prevent XML-based attacks:

| Rule | Limit | Error |
| --- | --- | --- |
| Max upload size | 10 MB | `413 Request Entity Too Large` |
| XXE protection | `xml.Decoder` with `Strict: true`; external entity resolution disabled | `400 Bad Request` |
| XML Bomb (entity expansion) | Custom `TokenReader` wrapper; token count limit of 1,000,000 | `400 Bad Request` |
| Max `<bpmn:userTask>` count | 1000 nodes per process | `422 Unprocessable Entity` |
| Max `<bpmn:lane>` count | 100 lanes per process | `422 Unprocessable Entity` |

---

#### 4.1.6 Go Struct Mapping (Parser Internal Types)

The parser unmarshals BPMN XML into the following internal Go types before the Compiler converts them to the DSL:

```go

type bpmnDefinitions struct {
    XMLName      xml.Name           `xml:"definitions"`
    Processes    []bpmnProcess      `xml:"process"`
    Collaboration *bpmnCollaboration `xml:"collaboration"` // nil for single-process BPMN
    Messages     []bpmnMessage      `xml:"message"` // root-level <bpmn:message> declarations
    Errors       []bpmnError        `xml:"error"` // root-level error definitions (boundary events)
}
type bpmnMessage struct {
    ID   string `xml:"id,attr"`
    Name string `xml:"name,attr"`
}
type bpmnCollaboration struct {
    ID           string            `xml:"id,attr"`
    Participants []bpmnParticipant `xml:"participant"`
    MessageFlows []bpmnMessageFlow `xml:"messageFlow"`
}
type bpmnParticipant struct {
    ID         string `xml:"id,attr"`
    Name       string `xml:"name,attr"`
    ProcessRef string `xml:"processRef,attr"`
}
type bpmnMessageFlow struct {
    ID         string `xml:"id,attr"`
    Name       string `xml:"name,attr"`
    SourceRef  string `xml:"sourceRef,attr"`
    TargetRef  string `xml:"targetRef,attr"`
    MessageRef string `xml:"messageRef,attr"`
}
type bpmnProcess struct {
    ID      string      `xml:"id,attr"`
    Name    string      `xml:"name,attr"`
    LaneSet bpmnLaneSet `xml:"laneSet"`

    UserTasks              []bpmnUserTask              `xml:"userTask"`
    SubProcesses           []bpmnSubProcess            `xml:"subProcess"`
    StartEvents            []bpmnStartOrEndEvent       `xml:"startEvent"`
    EndEvents              []bpmnStartOrEndEvent       `xml:"endEvent"`
    IntermediateCatchEvents []bpmnIntermediateCatchEvent `xml:"intermediateCatchEvent"`
    BoundaryEvents         []bpmnBoundaryEvent         `xml:"boundaryEvent"`
    ParallelGateways       []bpmnGateway               `xml:"parallelGateway"`
    ExclusiveGateways      []bpmnGateway               `xml:"exclusiveGateway"`
    EventBasedGateways     []bpmnGateway               `xml:"eventBasedGateway"`
    CallActivities         []bpmnCallActivity          `xml:"callActivity"`
    SequenceFlows          []bpmnSequenceFlow          `xml:"sequenceFlow"`
}
type bpmnCallActivity struct {
    ID                string                `xml:"id,attr"`
    Name              string                `xml:"name,attr"`
    ExtensionElements bpmnExtensionElements `xml:"extensionElements"`
}
type bpmnLaneSet struct {
    Lanes []bpmnLane `xml:"lane"`
}
type bpmnLane struct {
    ID           string   `xml:"id,attr"`
    Name         string   `xml:"name,attr"`
    FlowNodeRefs []string `xml:"flowNodeRef"`
}
type bpmnUserTask struct {
    ID                string                `xml:"id,attr"`
    Name              string                `xml:"name,attr"`
    ExtensionElements bpmnExtensionElements `xml:"extensionElements"`
}
type bpmnSubProcess struct {
    ID                string                `xml:"id,attr"`
    Name              string                `xml:"name,attr"`
    ExtensionElements bpmnExtensionElements `xml:"extensionElements"`
    // Embedded process graph
    LaneSet            bpmnLaneSet                  `xml:"laneSet"`
    UserTasks          []bpmnUserTask               `xml:"userTask"`
    StartEvents        []bpmnStartOrEndEvent        `xml:"startEvent"`
    EndEvents          []bpmnStartOrEndEvent        `xml:"endEvent"`
    BoundaryEvents     []bpmnBoundaryEvent          `xml:"boundaryEvent"`
    ParallelGateways   []bpmnGateway                `xml:"parallelGateway"`
    ExclusiveGateways  []bpmnGateway                `xml:"exclusiveGateway"`
    SequenceFlows      []bpmnSequenceFlow           `xml:"sequenceFlow"`
}
type bpmnBoundaryEvent struct {
    ID             string               `xml:"id,attr"`
    Name           string               `xml:"name,attr"`
    AttachedToRef  string               `xml:"attachedToRef,attr"`
    CancelActivity bool                 `xml:"cancelActivity,attr"` // default true if absent
    Timer          *bpmnTimerDef        `xml:"timerEventDefinition"`
    Error          *bpmnErrorRef        `xml:"errorEventDefinition"`
    Message        *bpmnMessageEventDef `xml:"messageEventDefinition"`
}
type bpmnMessageEventDef struct {
    MessageRef string `xml:"messageRef,attr"`
}
type bpmnIntermediateCatchEvent struct {
    ID    string        `xml:"id,attr"`
    Name  string        `xml:"name,attr"`
    Timer *bpmnTimerDef `xml:"timerEventDefinition"`
}
type bpmnTimerDef struct {
    Duration string `xml:"timeDuration"`
}
type bpmnErrorRef struct {
    ErrorRef string `xml:"errorRef,attr"` // empty = catch any error
}
type bpmnError struct {
    ID        string `xml:"id,attr"`
    Name      string `xml:"name,attr"`
    ErrorCode string `xml:"errorCode,attr"`
}
type bpmnStartOrEndEvent struct {
    ID   string        `xml:"id,attr"`
    Name string        `xml:"name,attr"`
    Error *bpmnErrorRef `xml:"errorEventDefinition"` // end events only (inside subprocess)
}
type bpmnExtensionElements struct {
    TaskDefinition       *bpmnTaskDefinition       `xml:"taskDefinition"`
    AssignmentDefinition *bpmnAssignmentDefinition `xml:"assignmentDefinition"`
    CalledElement        *bpmnCalledElement        `xml:"calledElement"` // callActivity only
    ZeebeProperties      []zeebeProperty           `xml:"properties>property"`
}
type bpmnTaskDefinition struct {
    Type string `xml:"type,attr"`
}
type bpmnAssignmentDefinition struct {
    CandidateGroups string `xml:"candidateGroups,attr"`
    CandidateUsers  string `xml:"candidateUsers,attr"`
}
type bpmnCalledElement struct {
    ProcessID string `xml:"processId,attr"`
}
type zeebeProperty struct {
    Name  string `xml:"name,attr"`
    Value string `xml:"value,attr"`
}
type bpmnGateway struct {
    ID   string `xml:"id,attr"`
    Name string `xml:"name,attr"`
}
type bpmnSequenceFlow struct {
    ID                   string                `xml:"id,attr"`
    SourceRef            string                `xml:"sourceRef,attr"`
    TargetRef            string                `xml:"targetRef,attr"`
    ExtensionElements    bpmnExtensionElements `xml:"extensionElements"`
}
```

---

#### 4.1.7 Temporal Naming Contract

This subsection defines the canonical names that the compiled DSL writes into `activity` fields and that the Temporal Workers **must** register under. These names form the contract between the Definition Service (which writes the DSL) and the Workers LLD (which implements the handlers). Any mismatch causes a Temporal `ActivityNotRegistered` panic at runtime.

##### Workflow Type Name

| Workflow | Temporal Type Name |
| --- | --- |
| Main DSL orchestrator | `DSLWorkflow` |

##### Task Queue Names

| Scope | Queue Name Format | Example |
| --- | --- | --- |
| Default (shared, non-isolated) | `wf-queue-default` | `wf-queue-default` |
| Tenant-isolated (enterprise tier) | `wf-queue-<tenant_uuid>` | `wf-queue-7ca648b2-b432-4744-884c-35fd556a310c` |

The `task_queue` field in the compiled DSL carries the resolved queue name. The Definition Service computes this at compile/publish time based on the tenant's plan tier (e.g. from the `x-plan` header). If the tenant is on the `enterprise` tier, it resolves to the tenant-isolated queue (`wf-queue-<tenant_uuid>`); otherwise (for `starter` or `pro` tiers), it defaults to the shared default queue (`wf-queue-default`).

##### Activity Names (DSL `activity` Field)

The `activity` field in each `StageDef` is written by the Compiler based on the `zeebe:taskDefinition type` attribute. The `StageTypeHandler` registry maps each `type` value to a Temporal activity name. Workers must register exactly these names:

| `zeebe:taskDefinition type` | DSL `activity` value | Worker Handler |
| --- | --- | --- |
| `prep` | `PrepActivity` | Starts a Prep task; creates `workflow_task` row via Temporal Local Activity |
| `review` | `ReviewActivity` | Starts a Review task; awaits signal before progressing |
| `approve` | `ApproveActivity` | Starts an Approve task; requires MFA assertion in signal payload |

##### Signal Channel Names (Temporal Signal Types)

Temporal Workers listen on these signal channel names. Defined here so the Definition Service's DSL is the authoritative source; the Execution Service and Workers must use these exact strings when calling `SignalWorkflow`.

| Channel Name | Direction | Payload Type | Purpose |
| --- | --- | --- | --- |
| `stage-transition` | Execution Svc → Worker | `StageTransitionSignal{dept_id, to_stage, user_id, mfa_token?}` | Advance a stage to the next step |
| `stage-defer` | Execution Svc → Worker | `StageDeferSignal{dept_id, from_stage, reason, user_id}` | Defer back to previous stage or preparator |
| `admin-route` | Execution Svc → Worker | `AdminRouteSignal{action: "goto"\|"terminate", dept_id?, stage_type?}` | Admin override of routing |
| `assignee-override` | Execution Svc → Worker | `AssigneeOverrideSignal{dept_id, stage_type, old_user_id, new_user_id}` | Runtime reassignment. `old_user_id` identifies which specific slot is being replaced; required for multi-assignee nodes. |

A **structural revert branch** authored in the BPMN (a back-edge from an exclusive gateway — see §4.4) compiles to a revert target on the `exclusive` DSL step keyed by `(dept_id, stage_type)`. At runtime the Execution Service realises it with `stage-defer` (revert to an earlier stage/department) or `stage-transition` (advance on the forward branch). Because a guarded exit only makes termination *possible*, the Execution Service may need to define a max loops so a repeatedly-rejected loop cannot run indefinitely.

##### Query Handler Names

| Query Name | Returns | Purpose |
| --- | --- | --- |
| `get-workflow-status` | `WorkflowStatusResult` (full state) | Dashboard polling / SSE pre-flight |

---

### 4.2 Graph Compilation Engine & DSL Generation

The Graph Compiler translates the raw, flat BPMN elements into a structured, hierarchical `ExecutionPlan` JSON DSL that can be cleanly orchestrated.

```mermaid
    graph TD
    Start --> Parse
    Parse --> Context
    Context --> DepMap
    Context --> Traversal
    Traversal --> Assembler
```

#### Graph Context Generation & Adjacency Mapping

1. **Adjacency Mapping**: The compiler parses sequence flows to generate explicit forward and backward lookup tables:
   - `outgoing`: `Map<SourceRef, []TargetRef>`
   - `incoming`: `Map<TargetRef, []SourceRef>`
2. **Gateway Type Categorization**:
   - Gateways with multiple outgoing flows (`len(outgoing) > 1`) are flagged as **Split Gateways** (`splitGWs`).
   - Gateways with multiple incoming flows (`len(incoming) > 1`) are flagged as **Join Gateways** (`joinGWs`).
3. **Back-edge Classification**: A DFS from the Start Event labels each sequence flow as a forward edge
   or a **back-edge** (target is an ancestor on the DFS stack — a revert/loop flow; see §4.4). The set
   of back-edges is computed once and shared by validation and compilation. The **forward graph**
   (all edges minus back-edges) is a DAG; split→join matching and traversal run over it, so they always
   terminate.

#### Graph Traversal & Parsing Engine

Starting at the primary `StartEvent`'s ID, the compiler traverses the graph node-by-node:

1. **Task Nodes**: The compiler resolves the task's department from its lane membership. The lane-derived department `id` is appended to a running sequential buffer (`seqBuf`).
2. **Split Gateways (Parallel/Exclusive)**:
   - Flushes any pending `seqBuf` to a `Sequential` execution step.
   - Traces all independent branches concurrently using a Breadth-First Search (BFS) starting at `outgoing[split_node]`.
   - Resolves all departments involved in the split paths (using `collectGroupDepts`).
   - Spawns either a `Parallel` or `Exclusive` step containing the collected departments.
   - Finds the matching intersection point (Join Gateway) using a traversal queue (`findJoinNode`), walking forward edges only (back-edges are skipped, so the walk cannot loop).
   - Marks the join gateway as visited and continues traversal from the join gateway's target.
3. **Revert / Loop Branches (Exclusive Gateways)**: When an exclusive gateway has an outgoing
   **back-edge** (a revert/loop flow), that branch is not traversed forward; instead it is emitted on
   the `exclusive` step as a revert branch carrying the target **(dept_id, stage_type)** node reference
   (never a `user_id`) plus its `condition_expression`. Forward branches of the same gateway compile as
   normal. The Execution Service maps the revert branch onto its `stage-defer` / `stage-transition`
   signals (§4.1.7) and enforces a loop-iteration bound at runtime.

#### Parser-to-DSL Mapping Example

Below is an abstract example of how a parsed process with sequential and parallel nodes maps directly into the JSON DSL structure.

**Input Flow Representation**:

```plantuml
@startuml
' œBPMN-style swimlane workflow

|Design / Engineering|
start
:StageStartedActivity;
note right
Role: engineer
Default Assignee: user-uuid-alice
Type: prep
end note

fork

|Contract Management|
:StageStartedActivity;
note right
Role: legal
Type: prep
end note

fork again

|Planning|
:StageStartedActivity;
note right
Role: planner
Type: prep
end note

end fork

|Procurement|
:StageStartedActivity;
note right
Role: officer
Type: review
Requires Comment: true
end note

stop

@enduml
```

**Compiled DSL Output**:

> [!IMPORTANT]
> **`default_assignees` is a publish-time snapshot.** These fields are the authoritative source of the default assignees for this specific version. They are baked into the immutable compiled DSL at publish time and are never mutated after publication. The `workflow_node_assignee` table is a denormalized copy of this data (one row per user per node), populated in the same transaction, used exclusively as a reverse index for invalidation queries. If any referenced user later becomes ineligible, the version's `is_valid` flag is set to `false` — the DSL itself remains unchanged to preserve audit integrity. Changing default assignees requires publishing a new version.

```json
{
  "name": "Project Initiation Workflow",
  "task_queue": "workflow-engine-queue",
  "departments": [
    {
      "id": "design",
      "label": "Design / Engineering",
      "stages": [
        {
          "type": "prep",
          "activity": "PrepActivity",
          "role": "engineer",
          "default_assignees": ["user-uuid-alice"],
          "boundary_timer": { "duration": "24h", "interrupting": false }
        }
      ]
    },
    {
      "id": "contracts",
      "label": "Contract Management",
      "stages": [
        {
          "type": "prep",
          "activity": "PrepActivity",
          "role": "legal",
          "default_assignees": ["user-uuid-bob"],
          "boundary_timer": { "duration": "48h", "interrupting": false }
        }
      ]
    },
    {
      "id": "planning",
      "label": "Planning",
      "stages": [
        {
          "type": "prep",
          "activity": "PrepActivity",
          "role": "planner",
          "default_assignees": ["user-uuid-charlie"]
        }
      ]
    },
    {
      "id": "procurement",
      "label": "Procurement",
      "stages": [
        {
          "type": "review",
          "activity": "ReviewActivity",
          "role": "officer",
          "default_assignees": ["user-uuid-david", "user-uuid-eve"],
          "extras": { "requires_comment": "true" }
        }
      ]
    }
  ],
  "execution": {
    "steps": [
      {
        "sequential": ["design"]
      },
      {
        "parallel": ["contracts", "planning"]
      },
      {
        "sequential": ["procurement"]
      },
      {
        "exclusive": [
          { "target": "construction", "condition_expression": "$.approved == true" },
          { "target": "", "revert_to_dept": "procurement", "revert_to_stage": "review", "condition_expression": "$.approved == false" }
        ]
      }
    ]
  }
}
```

> **Exclusive Gateway condition expressions**: When the compiler encounters a `<bpmn:exclusiveGateway>` split, it reads the `<bpmn:conditionExpression>` child element from each outgoing `<bpmn:sequenceFlow>` and populates a `condition_expression` field on the outgoing `exclusive` `ExecutionStep` entry. When an outgoing flow is a **back-edge** (revert/loop), the entry additionally carries a revert target keyed by `(dept, stage_type)` (an additive field, e.g. `revert_to_dept` / `revert_to_stage`) instead of advancing to a forward department. Runtime evaluation of the expression — and realisation of the revert via `stage-defer` / `stage-transition` — is the responsibility of the Temporal Worker (Workers LLD §Exclusive Routing).

#### Per-Element Plugin Architecture

The `Compiler` struct holds two registries: one for BPMN flow-node handlers (`ElementHandler`) and one for stage type handlers (`StageTypeHandler`). Adding a new element means implementing the appropriate sub-interface and registering it — nothing else changes.

```go
// ElementHandler is the base interface for all BPMN flow-node handlers.
type ElementHandler interface {
    NodeType() nodeType
    Validate(nodeID string, proc *bpmnProcess, g *graph) []domain.BPMNValidationError
    Compile(nodeID string, cs *compileState) error
}

// EventHandler extends ElementHandler for BPMN event nodes.
type EventHandler interface {
    ElementHandler
    EventKind() string // "start" | "end" | "boundary" | "intermediateCatch"
}

// ActivityHandler extends ElementHandler for BPMN activity nodes.
type ActivityHandler interface {
    ElementHandler
    ActivityKind() string // "userTask" | "subProcess"
}

// GatewayHandler extends ElementHandler for BPMN gateway nodes.
type GatewayHandler interface {
    ElementHandler
    GatewayKind() string // "exclusive" | "parallel" | "eventBased" | "inclusive"
}

// StageTypeHandler encapsulates a single stage-type concept (prep/review/approve).
// Custom stage types are registered via WithStageType.
type StageTypeHandler interface {
    ID() string           // Zeebe value: "prep", "review", "approve"
    ActivityName() string // Temporal worker activity name
    ValidateProps(taskID string, props map[string]string) []domain.BPMNValidationError
}

type Compiler struct {
    stageTypes map[string]StageTypeHandler
    elements   map[nodeType]ElementHandler
}

func NewCompiler(opts ...CompilerOption) *Compiler
func WithStageType(h StageTypeHandler) CompilerOption
func WithElementHandler(h ElementHandler) CompilerOption
```

**Built-in handler implementations:**

| Category | Handler | Tier |
| --- | --- | --- |
| Events | `StartEventHandler`, `EndEventHandler` | 1 (existing) |
| Events | `TimerBoundaryHandler`, `ErrorBoundaryHandler` | 1 (new) |
| Events | `TimerCatchHandler` | 1 (new) |
| Activities | `UserTaskHandler` | 1 (existing) |
| Activities | `SubProcessHandler` | 1 (new) |
| Gateways | `ExclusiveGatewayHandler`, `ParallelGatewayHandler` | 1 (existing) |
| Gateways | `EventBasedGatewayHandler` | 1 (new) |
| Stage types | `PrepStage`, `ReviewStage`, `ApproveStage` | built-in |

#### DSL Types

These types no longer live in `internal/core/domain/` — `internal/core/domain/compiled_plan.go` was deleted as part of migrating to the shared `workflow-models` Go module (`github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models`, pre-release `v0.1.0-beta.1`). `CompiledPlan`, `CompiledCollaboration`, `DepartmentDef`, `StageDef`, `BoundaryTimer`, `ExecutionPlan`/`ExecutionStep` and its branch/step variants (`ParallelBranch`, `ExclusiveBranch`, `SubWorkflowStep`, `CallPoolStep`, `ErrorPath`, `TimerPath`, `MessagePath`, `IOMapping`/`IOVar`, `MessageDef`, `VisualElementDef`) are all imported directly from that module's `pkg/dsl` package — every compiler/service call site references `dsl.CompiledPlan` etc., not `domain.CompiledPlan`. `StageDef.Type`/`ExclusiveBranch.TargetStage` discriminator values live in the module's `pkg/enums`.

The authoritative field-by-field reference for every type above is `workflow_models_lib.md` §2 (this doc no longer duplicates it, to avoid the two drifting apart). Two facts worth keeping here since they're compiler-behavior notes, not type-shape ones:

- `schema_version` is a DB column on `workflow_version` — it is NOT a field in `CompiledPlan` JSON.
- `EventBasedStep`/`EventBranch` are Tier-2 design reservations for event-based gateway compilation, not implemented on `pkg/dsl.ExecutionStep` (confirmed absent from the module as of `v0.1.0-beta.1`) or in this repo. Event-based gateways are parsed/graph-validated today but have no compile handler (`UNSUPPORTED_ELEMENT`) — forward design intent only.

**New DSL example — subprocess with timer + error boundary, followed by event-based gateway:**

```json
{
  "steps": [
    {
      "sub_workflow": {
        "name": "ITB Preparation",
        "plan": {
          "steps": [
            { "sequential": ["engineering"] },
            { "parallel": ["contracts", "procurement"] }
          ]
        },
        "error_paths": [
          { "error_code": "BUDGET_EXCEEDED", "short_circuit": true, "target_dept": "finance" }
        ],
        "timer_paths": [
          { "duration": "72h", "interrupting": false, "target_dept": "project management" }
        ]
      }
    },
    {
      "event_based": {
        "branches": [
          { "type": "timer", "duration": "5d", "target_dept": "project management" },
          { "type": "signal", "signal": "review-complete", "target_dept": "procurement" }
        ]
      }
    }
  ]
}
```

#### All-Branches-Terminate Exclusive Gateway

When an exclusive gateway has no matching join gateway (all branches lead to end events or to a continuation path with no reconvergence point), the compiler uses the following strategy instead of the normal split→join walk:

- Each branch's first node is inspected. If it is an end event, it contributes an `ExclusiveBranch` entry with `Terminates: true` and an empty `Target` (early-exit branch).
- The single branch whose first node is **not** an end event is the continuation path (`continuationNode`). After recording all branch entries, the compiler calls `traverseNode(continuationNode)` to continue compilation normally — this means subprocesses and nested gateways on the continuation path go through the full traversal pipeline, not a shallow walk.
- Revert back-edges on the same gateway are appended as revert branches (unchanged behaviour).

This pattern is needed for bid/no-bid style decisions where one branch exits early and the other continues through a complex subprocess and parallel section.

#### Timer Boundary Continuation Paths

Timer boundary events on user tasks create continuation tasks (`Task_escalate_...`) that are reachable only via the timer — not from the start event via sequence flows. The main traversal therefore never visits them.

After the main traversal completes (both in `compile()` and inside `handleSubProcess()` for inner state), `compileTimerBoundaryPaths()` iterates all `<bpmn:boundaryEvent>` elements in the process that carry a timer definition. For each such event, it resolves the outgoing sequence flow's target and calls `traverseNode` only if that target has not already been visited. This guarantees escalation tasks are compiled into the owning department's `Stages` list.

#### Implicit Process Root (No Explicit Start Event)

A process in a collaboration may have no explicit `<bpmn:startEvent>`. The compiler identifies the root as the **single node with no incoming sequence flow and at least one outgoing sequence flow** (`FindImplicitStart` in `bpmncore/graph.go`). If no such unique node exists — zero candidates or more than one — `FindImplicitStart` returns an empty string and compilation proceeds using the explicit start event (or fails if neither is present).

The implicit root is exempt from the `DANGLING_NODE` no-incoming check: a node elected as the implicit root has no incoming edges by definition, so the check would otherwise produce a false positive.

Two concrete patterns arise in the current design:

- **Receive variant**: the main pool has a `callActivity` or task as the implicit root (no incoming sequence flow). The ignored pool has its own start event and runs independently. The ignored pool sends a message that targets a **message boundary event** on that `callActivity`; the boundary event fires as a concurrent interrupt with no continuation (no outgoing sequence flow). This is valid — see the boundary event rules below.
- **Send variant**: the main pool has a task as the implicit root that sends a message to the ignored pool and has **no outgoing sequence flow**. `buildMessageBridges` / `InjectMessageBridges` creates a synthetic outgoing edge for that task before `FindImplicitStart` runs, so the task is correctly identified as the implicit root and compilation proceeds normally. The synthetic edge is stripped from the compiled DSL.

#### Department ID Namespacing in Collaborations

In a `CompiledCollaboration`, two pools may define lanes with the same name (e.g. both have a "Tender" lane). To make department IDs globally unique across all plans, `qualifyPlanDepts(plan, plan.Name)` is called for each compiled plan before it is appended to the `CompiledCollaboration.Plans` slice.

`qualifyPlanDepts` prefixes every department ID reference throughout the plan with `planName + "/"`:

- `DepartmentDef.ID` entries in `plan.Departments`
- `BoundaryTimer.TargetDept` inside each stage's boundary timer, and `BoundaryMessage.TargetDept` inside each stage's boundary message
- All `ExecutionStep.Sequential`, `Parallel`, and `Exclusive[*].Target` / `Exclusive[*].RevertToDept` entries
- `SubWorkflowStep.ErrorPaths[*].TargetDept`, `SubWorkflowStep.TimerPaths[*].TargetDept`, and `SubWorkflowStep.MessagePaths[*].TargetDept`
- `ExecutionStep.MessagePaths[*].TargetDept` (callActivity message boundaries, flattened)
- Inner plan steps inside `SubWorkflowStep.Plan` (recursively via `qualifySteps`)

For example, in the BNB collaboration: `"Tender"` → `"Bid-No-Bid Review/Tender"`, `"Client"` → `"Issue RFQ/Client"`. The `Compile()` path (single-process, no collaboration) is unaffected — dept IDs remain plain lane names.

---

### 4.3 Versioning Strategy & Diff Engine

When publishing an updated draft, the compiler calculates a canonical SHA-256 hash of the normalized XML.

#### Canonical Hash Generation

1. Strip leading and trailing whitespaces from XML elements.
2. Sort all Zeebe properties alphabetically by name.
3. Compute the SHA-256 checksum of the normalized string.

#### Diff & Versioning Suggestions (Integer-Based Versioning)

Because the Workflow Engine maintains strict execution history, the system operates on **monotonically increasing integer versions** (e.g. `1`, `2`, `3`). The Suggestion Engine calculates diffs and prompts the administrator accordingly:

| Detected Difference | Severity | Action & Suggestion |
| :--- | :--- | :--- |
| Only metadata changes (task names, assignees, roles, default users, or comment requirements changed) | **Minor** | **Suggest Version Upgrade**: Increment the integer version of the existing workflow (e.g., Version `1` ➔ Version `2`). Existing active instances remain on their original version; new instances use the new version. |
| Structural alterations (split/join gateways added, sequence flows changed, new departments introduced) | **Critical** | **Suggest New Workflow**: Prompts the user to register a brand-new workflow entity with a different key. This prevents run-time divergence and database record mismatch in running historical execution instances. |

---

### 4.4 Validation Engine

Prior to saving drafts or completing publishing transactions, the compiler runs validation passes. It returns a **structured multi-error slice** if any rules are violated.

#### Structural Rules

- **Single Start Event**: Verify `len(StartEvents) == 1`.
- **Reachability Pass**: A Depth-First Search (DFS) is run from the start event to ensure all declared user tasks and gateways are reachable.
- **Dangling Nodes**: Every node (except start/end events) must have at least one incoming and one outgoing sequence flow.
- **Closed Gateways**: Each parallel/exclusive split gateway must resolve to exactly one valid matching join gateway.

#### Metadata Rules

- **Required Extensions**: Every `<bpmn:userTask>` must declare a `<zeebe:taskDefinition>` and a `<zeebe:assignmentDefinition>` inside its `extensionElements`.
- **Valid Stage Type**: `taskDefinition.type` must match a registered `StageTypeHandler` ID. The deployment default set is `prep`, `review`, `approve`; the registry is not hardcoded.
- **Lane Membership**: Every `userTask` must appear in exactly one lane via `<bpmn:flowNodeRef>`. Department is derived from the lane `name`; no `dept_id` property is used.
- **Valid Timer Duration**: Timer boundary events and intermediate timer catch events must declare durations in ISO 8601 (`PT24H`, `P3D`) or Go duration syntax (`72h`, `3h30m`).

#### Guarded-Loop Enforcement (Revert / Loopback Flows)

Earlier revisions required the definition to be a strict DAG and rejected every cycle. That rule is
superseded: revert/loopback flows (e.g. an approver sending a task back to the reviewer or preparator)
**may now be authored explicitly in the BPMN** as structural cycles, provided each cycle is *guarded*
so that termination is always possible. Uncontrolled cycles are still rejected.

- **Back-edge classification**: The compiler classifies sequence flows via a depth-first traversal
  from the Start Event. An edge `(u → v)` is a **back-edge** (loop/revert edge) when `v` is an ancestor
  of `u` on the active DFS stack. Removing the back-edges yields the **forward graph**, which must be a
  DAG — all existing structural checks (reachability, dangling nodes, split→join matching, traversal,
  compilation) operate on this forward graph.
- **Guarded-exit rule**: A loop is valid only when **every back-edge originates at a diverging
  exclusive gateway** that also has at least one forward (non-back-edge) outgoing branch — i.e. the
  gateway can choose to exit the loop. This is the standard BPMN way to express a rework loop. The
  following are rejected (see `CYCLE_DETECTED` / `UNGUARDED_LOOP` in §3.1.4):
  - a back-edge whose source is **not** an exclusive gateway (a task, parallel/AND gateway, or event);
  - an exclusive gateway whose outgoing edges are **all** back-edges (no forward exit);
  - a **self-edge** (`u → u`) — non-standard in BPMN; loops must route through a gateway. (Note a
    self-edge forms an SCC of size 1, so SCC size alone is not a sufficient check.)
- **Tarjan's SCC** is retained, but its role changes: it is used to **enumerate** the loops (strongly
  connected components with more than one node) so each can be checked for a guarded exit, rather than
  to reject any cycle outright.
- **Revert-target identity**: A back-edge's target is identified structurally by its node — i.e. by
  **(dept, stage_type)** / node key — **never** by `user_id`. The same user may belong to multiple
  departments, so default assignees are not a valid routing target. The compiled DSL records the revert
  target as a dept/stage reference on the originating exclusive branch.
- **Cross-department reverts** are permitted: a revert may target a stage in an earlier department, as
  long as that target **precedes** the back-edge's source in the forward graph (it must be an ancestor
  — guaranteed by the back-edge definition above).
- **Runtime execution**: The structural revert branches are executed by the Execution Service via the
  existing Temporal signals (`stage-defer` / `stage-transition` / `admin-route`, §4.1.7). The guarded
  exit only makes termination *possible*, not guaranteed, so the Execution Service **must enforce a
  loop-iteration bound** at runtime to protect against an indefinitely repeating revert cycle.

#### Semantic Rules

- **Orphan Nodes/Lanes**: All defined elements (lanes, gateways, tasks) must be part of a continuous reachable sequence from the Start Event. Isolated or disconnected nodes trigger a validation failure.
- **Max Depth**: The length of any forward path from start to end cannot exceed 2000 consecutive nodes to prevent stack overflow/DOS during recursive graph compilation (`MAX_DEPTH_EXCEEDED`). Subprocess internal paths are counted independently.
- **Identity-Agnostic Metadata Check**: Validates that `zeebe:taskDefinition`, `zeebe:assignmentDefinition`, and lane membership are present and structurally well-formed, but intentionally does **not** verify UUIDs or role names against the IAM directory during stateless parsing (the Definition Service remains identity-agnostic). Eligibility is checked only at publish time via the Org & Membership Service call.

#### Subprocess Validation Rules

- **Internal completeness**: A `bpmn:subProcess` must contain exactly one start event, at least one end event, at least one user task, and all standard structural rules (dangling nodes, reachability, etc.) apply recursively to the embedded process graph.
- **Single incoming / single outgoing**: The subprocess element in the parent flow must have exactly one incoming and one outgoing sequence flow.
- **No nested subprocesses in v1**: A `bpmn:subProcess` inside a `bpmn:subProcess` is rejected with `REJECTED_ELEMENT`.
- **No event-based gateway inside subprocess in v1**: An `bpmn:eventBasedGateway` inside a subprocess is rejected with `UNSUPPORTED_ELEMENT`.
- **`bpmn:task` without required extensions**: A plain `bpmn:task` inside a subprocess that lacks `zeebe:taskDefinition` emits `MISSING_TASK_DEFINITION`.

#### Boundary Event Validation Rules

- **Valid attachment**: `attachedToRef` must reference a node in the process that is a `bpmn:userTask`, `bpmn:subProcess`, or (for error/message boundaries only) `bpmn:callActivity`.
- **Timer boundary on userTask**: Generates `BoundaryTimer` on the referenced `StageDef`. Multiple timer boundary events on the same task are rejected (emit first error, stop).
- **Error boundary attachment**: Error boundary events may be attached to `bpmn:subProcess` or `bpmn:callActivity` elements (not individual user tasks). Attachment to a user task emits `INVALID_BOUNDARY_ATTACHMENT`. On `bpmn:callActivity` the attachment itself is valid, but the compiler still rejects it at compile time (see §callActivity) — only message boundary events execute successfully there.
- **Message boundary attachment**: Message boundary events may be attached to `bpmn:userTask`, `bpmn:subProcess`, or `bpmn:callActivity`. Generates `StageDef.BoundaryMessage`, `SubWorkflowStep.MessagePaths`, or `ExecutionStep.MessagePaths` respectively.
- **Duration format**: `timeDuration` inside a timer event definition must be parseable ISO 8601 (`P3D`, `PT72H`) or Go duration syntax (`72h`, `3h30m`). Invalid format emits `INVALID_SLA_DURATION`.
- **Boundary event outgoing flow**: A boundary event must have exactly one outgoing sequence flow into the parent flow, **with one exception**: a **message boundary event with no outgoing sequence flow** is valid — it models a terminal interrupt (the message fires, the host activity is cancelled, and execution does not continue on that path). No `DANGLING_NODE` error is emitted for such a boundary event. Timer and error boundary events still require at least one outgoing sequence flow. For all other boundary events with outgoing flows, the target must be reachable from the parent start event (after stripping boundary flows from the reachability check to avoid false cycles).

#### Event-Based Gateway Validation Rules

- **Minimum branches**: Must have 2+ outgoing flows.
- **All targets are catch events**: Every outgoing flow's immediate target must be a `bpmn:intermediateCatchEvent`.
- **At least one timer**: At least one branch must target an intermediate catch event with a `bpmn:timerEventDefinition` (prevents infinite blocking).
- **No back-edges**: An event-based gateway may not be the source of a back-edge (loop via event-based gateway not supported in v1).
- **Not inside subprocess in v1**: An event-based gateway inside a subprocess emits `UNSUPPORTED_ELEMENT`.
- **Catch event types**: A `bpmn:intermediateCatchEvent` whose definition is not `timerEventDefinition` emits `UNSUPPORTED_ELEMENT` (Tier 2 — message/signal catch events are planned but not yet compiled).

#### Department & Stage Structural Flexibilities

- **Partial Stage Declarations**: A single department does not need to define all three stage types (`prep`, `review`, `approve`). Any subset of stages is valid based on the BPMN tasks parsed (e.g. a department might only declare `prep` or `review`).
- **Decoupled Progression Verification**: The strict ordering and progression flow rules of department-level stages (e.g. verifying transitions and loopback re-execution routes) are entirely decoupled from definition-time XML compilation. These runtime sequence rules are delegated to external downstream/runtime orchestrators rather than structural definition-time validation.

---

## 4.5 Multi-Assignee Support

This section defines how multiple users are assigned to a single task node at the definition layer — covering BPMN authoring, compiled DSL representation, database storage, and the concurrency semantics the Execution Service and Temporal Workers must enforce at runtime.

### 4.5.1 BPMN Authoring

Default assignee(s) for a `<bpmn:userTask>` are set via `<zeebe:assignmentDefinition>`:

| Attribute | Type | Required | Values |
| --- | --- | --- | --- |
| `candidateGroups` | string | Yes | IAM role level name (e.g. `"preparator"`); max 256 chars |
| `candidateUsers` | UUID v7 | Current: single | Single UUID v7; multi-user support will be added via a named validator function |

**BPMN XML examples**:

```xml
<!-- Prep task with single default assignee -->
<bpmn:userTask id="Task_legal_prep" name="Legal Prep">
  <bpmn:extensionElements>
    <zeebe:taskDefinition type="prep"/>
    <zeebe:assignmentDefinition
      candidateGroups="preparator"
      candidateUsers="018e1f2a-0000-7000-8000-000000000001"/>
  </bpmn:extensionElements>
</bpmn:userTask>

<!-- Review task with comment required -->
<bpmn:userTask id="Task_design_review" name="Design Review">
  <bpmn:extensionElements>
    <zeebe:taskDefinition type="review"/>
    <zeebe:assignmentDefinition
      candidateGroups="reviewer"
      candidateUsers="018e1f2a-0000-7000-8000-000000000002"/>
    <zeebe:properties>
      <zeebe:property name="requires_comment" value="true"/>
    </zeebe:properties>
  </bpmn:extensionElements>
</bpmn:userTask>
```

The UI modeler calls `GET /tenants/:id/users?department=:dept&level=:level` to populate a user picker per node. The selected user UUID is placed in `candidateUsers`.

### 4.5.2 Compiled DSL Representation

The Graph Compiler reads `candidateUsers` from `<zeebe:assignmentDefinition>` and emits a `default_assignees` string array (currently one element). Multi-assignee support (multiple `candidateUsers`) is planned; when implemented it will be enforced by relaxing the `validateCandidateUser` function without a DSL break, since `default_assignees` is already an array.

```json
{
  "id": "design",
  "label": "Design",
  "stages": [
    {
      "type": "review",
      "activity": "ReviewActivity",
      "role": "reviewer",
      "default_assignees": ["uuid-alice", "uuid-bob", "uuid-charlie"],
      "extras": { "requires_comment": "true" },
      "boundary_timer": { "duration": "24h", "interrupting": false }
    }
  ]
}
```

`default_assignees` is always a JSON array, even for single-assignee nodes.

### 4.5.3 Database Storage

The `workflow_node_assignee` table stores one row per `(workflow_version_id, node_key, user_id)` tuple. For a node with three default assignees, three rows are inserted atomically inside the publish transaction. No schema change is required — the existing design already supports this natively.

**Publish-time insert (per assignee)**:

```sql
INSERT INTO workflow_node_assignee
    (id, tenant_id, workflow_version_id, node_key, user_id, department_id, role)
VALUES
    (gen_random_uuid(), :tenant_id, :version_id, :node_key, :user_id, :dept_id, :role);
-- Repeated for each user_id in the node's default_assignees list
```

**Invalidation query** — filters by both `user_id` and `department_id` because a `department.membership.revoked` event is scoped to a single department. Using `user_id` alone would over-invalidate (revoking a user from Engineering would incorrectly flag their assignments in Design):

```sql
SELECT DISTINCT workflow_version_id, node_key
FROM workflow_node_assignee
WHERE user_id      = :revoked_user_id
  AND department_id = :revoked_department_id
  AND tenant_id    = :tenant_id;
```

### 4.5.4 Concurrency Semantics

The Definition Service defines the mode; the **Execution Service and Temporal Workers enforce it at runtime**. The two modes and their runtime implications:

| Mode | Trigger to advance workflow | Effect on other assignees |
| --- | --- | --- |
| `any` | First assignee to complete (or claim, in claim-required flows) | Remaining open task records for that node are cancelled / closed by the Temporal activity |
| `all` | Last assignee completes — workflow advances only when all `workflow_task` records for the node reach `COMPLETED` | No cancellation; each assignee works their own copy independently |

**Race condition under `any` mode**: Two assignees may attempt to complete simultaneously. The Execution Service uses the optimistic locking `version` column on `workflow_task` to ensure only the first completion signal is accepted; subsequent completions are rejected with a `409 Conflict` (stale version) and the UI rolls back optimistically. Full implementation is in the Execution Service LLD §4.5.

**Assignee override (current: single-assignee)**: An admin override via `POST /tasks/{id}/reassign` (Execution Service) replaces the sole assignee. Temporal receives `AssigneeOverrideSignal{dept_id, stage_type, old_user_id, new_user_id}` (canonical definition in §4.1.7); the workflow continues uninterrupted.

**Assignee override (planned: multi-assignee)**: When multi-assignee support lands, override semantics will depend on the concurrency mode:

- *First-to-complete*: targeted slot is swapped; remaining assignees continue uninterrupted.
- *All-must-complete*: workflow is **paused** first; admin must resume after resolving the replacement, since removing any participant breaks the "all must complete" contract.

The Definition Service is only responsible for defining the signal contract. The branching logic above is implemented in the Execution Service (LLD §3.2.5 and §4.5.4 S4).

### 4.5.5 Validation Rules

The following rules apply specifically to multi-assignee nodes and are enforced at publish time:

| Rule | Error Code |
| --- | --- |
| `zeebe:assignmentDefinition` must be present | `MISSING_ASSIGNMENT_DEFINITION` |
| `candidateGroups` must be non-empty and ≤ 256 chars | `CANDIDATE_GROUPS_EMPTY` |
| `candidateUsers` must be a single valid UUID v7 | `INVALID_CANDIDATE_USER` |
| UUID in `candidateUsers` must pass Org & Membership eligibility check | `ASSIGNEE_INELIGIBLE` |

---

## 5. Publish Transaction Flow

The transition of a workflow `DRAFT` into an active, immutable `PUBLISHED` version is a critical process. It must be executed with strict atomic consistency to prevent partial updates, and it must validate the structural and metadata rules of the BPMN diagram before any database mutations are committed.

### 5.1 Step-by-Step Execution Sequence

When the tenant administrator triggers a publish operation via `POST /workflows/:id/versions/:version_id/publish`, the compilation engine and the persistence layer execute the following steps in sequence:

```mermaid
sequenceDiagram
    autonumber
    actor Admin as Tenant Admin
    participant API as Handler / Middleware
    participant Val as Validation Engine
    participant Comp as Graph Compiler
    participant IAM as Org & Membership Service
    participant DB as Postgres Database
    
    Admin->>API: POST /workflows/:id/versions/:version_id/publish
    API->>DB: Fetch Draft XML & Check Existence
    DB-->>API: Draft BPMN XML
    
    API->>Val: Run Validation Pass (DFS, Metadata, Guarded-Loop)
    alt Validation Fails
        Val-->>API: Structured Multi-Error Slice ([]error)
        API-->>Admin: 422 Unprocessable Entity
    end
    
    API->>Comp: Compile BPMN to Execution Plan (DSL)
    Comp-->>API: compiled_plan_json
    
    API->>API: Extract Default Assignees from Plan (default_assignees[] per node)
    loop For each user_id in default_assignees across all nodes
        API->>IAM: POST /tenants/:t/users/:u/eligibility?department=:dept&level=:level
        IAM-->>API: Response { eligible: true/false }
        alt User not eligible
            API-->>Admin: 422 Unprocessable Entity (Assignee Ineligible)
        end
    end
    
    API->>Comp: Compute SHA-256 Canonical Hash
    Comp-->>API: artifact_hash
    
    API->>DB: Check Structural Diff (Compare with previous version)
    alt Structural Divergence & force_publish_structural = false
        DB-->>API: Flag: Structural Change Detected
        API-->>Admin: 409 Conflict (Suggest new workflow key)
    end
    
    Note over API,DB: Begin SQL Transaction (Serializable Isolation)
    API->>DB: BEGIN TRANSACTION
    
    API->>DB: Lock Root Workflow Record (SELECT FOR UPDATE)
    API->>DB: Calculate Next version_number (Max + 1)
    
    API->>DB: UPDATE workflow_version SET status='PUBLISHED', compiled_plan_json, artifact_hash, version_number, published_at=NOW() WHERE id=:version_id
    loop For each default assignee mapping
        API->>DB: INSERT INTO workflow_node_assignee (id, tenant_id, workflow_version_id, node_key, user_id, department_id, role) VALUES (...)
    end
    API->>DB: UPDATE workflow SET active_version_id=:version_id WHERE id=:id
    API->>DB: INSERT INTO outbox_events (id, event_type, payload, tenant_id, trace_id, attempts) VALUES(..., 'workflow.template.published', :payload_jsonb, :tenant_id, :trace_id, 0)
    
    API->>DB: COMMIT TRANSACTION
    DB-->>API: Transaction Committed Successfully
    API-->>Admin: 200 OK (Published version details)
```

### 5.2 Transactional Integrity and Locking Strategy

To avoid race conditions where multiple admins attempt to concurrently publish the same draft (only one DRAFT per workflow is permitted — §10.1), the database operations are wrapped inside a single **PostgreSQL Transaction** under `SERIALIZABLE` isolation using the following constraints:

1. **Root Record Locking**:
   The transaction starts by selecting the root workflow record with an explicit row lock:

   ```sql
   SELECT id, active_version_id 
   FROM workflow 
   WHERE id = :workflow_id AND tenant_id = :tenant_id 
   FOR UPDATE;
   ```

   This prevents any concurrent transactions from acquiring version updates or altering the workflow's state while this publish cycle is active.

2. **Atomic Upgrades**:
   To avoid "split-brain" or half-published states:
   - The draft's status is changed from `DRAFT` to `PUBLISHED` in the `workflow_version` table.
   - The computed `version_number` is written (retrieved by selecting `COALESCE(MAX(version_number), 0) + 1` for published versions under that workflow).
   - The parent `workflow.active_version_id` is updated to point directly to the newly published version.

3. **Dual-Write Prevention (Transactional Outbox)**:
   The transaction writes a `workflow.template.published` event to the `outbox` table in the *same* database transaction block:

   ```sql
   INSERT INTO outbox_events (id, event_type, payload, tenant_id, trace_id, attempts, created_at, scheduled_at)
   VALUES (
       gen_random_uuid(),
       'workflow.template.published',
       :event_payload_jsonb,
       :tenant_id,
       :trace_id,
       0,
       NOW(),
       NOW()
   );
   ```

   This guarantees that either both the workflow version is successfully published and the downstream notification event is queued, or the entire operation is rolled back, maintaining perfect transactional consistency.

4. **Draft Concurrency (Optimistic Locking)**:
   Because only one active `DRAFT` is allowed per workflow, multiple admins modifying the same draft could trigger lost updates (e.g., two admins saving the canvas simultaneously). The draft update endpoint uses an **Optimistic Locking** strategy via the `updated_at` column. If the client submits a draft save with a stale timestamp, the update affects 0 rows, and the API returns a `409 Conflict`.

### 5.3 Structural Diff Resolution

To protect active running execution instances from breaking due to structural updates in the workflow graph, the publishing endpoint checks structural changes before executing the transaction:

- **Structural Check**:
  The system compares the compiled execution plan's `steps` list of the draft against the currently active published version's `steps` list.
- **Assignee/Metadata Changes**:
  If the diff only contains changes to stage roles, default assignees, requires comment flags, or labels, it is considered **Minor**. The publication proceeds directly, and a new version is created.
- **Structural Divergence**:
  If there are additions/deletions of execution steps, gate transformations (e.g. converting a sequential flow to parallel splits), or new department insertions, it is considered **Critical**.
  - If `force_publish_structural` in the request is `false`, the publish request is aborted, returning a `409 Conflict` containing a warning and a recommendation to register a new workflow key (`CREATE_NEW_WORKFLOW`).
  - If `force_publish_structural` is `true`, the administrator explicitly acknowledges the risk, and the system proceeds to publish the draft as a new version under the existing key.

---

### 5.4 Draft Save (Update) Flow

When the admin saves progress in the BPMN modeler canvas without publishing, the following lightweight flow executes. No compilation or validation occurs — the raw BPMN XML is stored as-is.

```mermaid
sequenceDiagram
    autonumber
    actor Admin as Tenant Admin
    participant API as Handler / Middleware
    participant DB as Postgres Database

    Admin->>API: PUT /workflows/:id/draft
    Note over API: Extract x-tenant-id, x-user-id from headers
    API->>DB: SELECT id, status FROM workflow_version WHERE workflow_id=:id AND status='DRAFT' AND tenant_id=:tid
    alt Version not found or not DRAFT
        DB-->>API: 0 rows
        API-->>Admin: 404 Not Found / 409 Conflict
    end
    DB-->>API: DRAFT row confirmed
    API->>DB: UPDATE workflow_version SET bpmn_xml=:xml, updated_at=NOW() WHERE workflow_id=:id AND status='DRAFT'
    DB-->>API: 1 row updated
    API-->>Admin: 200 OK (version_id, updated_at)
```

> No compile, validate, or outbox write occurs during a draft save. Those are deferred to the explicit publish or validate actions.XML is only thing validated here.

#### 5.4.1 Concurrent Draft Initialization Race Condition

When two admins attempt to call `POST /workflows/:id/draft` simultaneously to initialize a draft from the active version, a database race condition can occur. The database partial unique index `idx_wv_single_draft` prevents more than one `DRAFT` status version from coexisting for a given workflow.

```mermaid
sequenceDiagram
    autonumber
    actor AdminA as Admin A (Tenant 1)
    actor AdminB as Admin B (Tenant 1)
    participant API as Gin Handler
    participant DB as Postgres Database

    AdminA->>API: POST /workflows/:id/draft
    AdminB->>API: POST /workflows/:id/draft
    
    Note over API,DB: Concurrent Execution starts
    API->>DB: INSERT INTO workflow_version (status='DRAFT', ...) (Admin A)
    API->>DB: INSERT INTO workflow_version (status='DRAFT', ...) (Admin B)
    
    alt Admin A Transaction commits first
        DB-->>API: 1 Row Inserted (Admin A success)
        API-->>AdminA: 201 Created
    else Admin B Transaction attempts commit
        Note over DB: Unique Index Collision on status='DRAFT'
        DB-->>API: PG Error 23505 (Unique Violation)
        Note over API: Handler maps PG 23505 to RFC-9457 Problem Details
        API-->>AdminB: 409 Conflict (DRAFT_ALREADY_EXISTS)
    end
```

---

### 5.5 Validation-Only Flow (§3.12)

The `POST /validate` endpoint runs the full parser + validation pass on an uploaded BPMN file without persisting anything. Used by the modeler to provide real-time feedback before the admin uploads to a draft.

```mermaid
sequenceDiagram
   autonumber
   actor Admin as Tenant Admin
   participant API as Handler / Middleware
   participant Parser as BPMN Parser
   participant Val as Validation Engine

   Admin->>API: POST /validate (multipart or JSON with bpmn_xml)
   API->>API: Enforce 10 MB size limit&#59; XXE token-bomb guard
   alt Oversized or malformed XML
       API-->>Admin: 400 Bad Request
   end
   API->>Parser: ParseXML([]byte)
   alt Parse fails (namespace missing, unrecognized root)
       Parser-->>API: parse error
       API-->>Admin: 400 Bad Request (structured errors)
   end
   Parser-->>API: BPMNProcess AST
   API->>Val: Validate(BPMNProcess)
   Val-->>API: []ValidationError (may be empty)
   alt Errors present
       API-->>Admin: 422 Unprocessable Entity (errors array)
   end
   API-->>Admin: 200 OK { valid: true, warnings: [] }
```

> Nothing is written to the database. This endpoint is stateless and safe to call on every canvas save.

---

### 5.6 Plan Quota Verification and Enforcement Logic

To enforce platform monetization and subscription tiers, the Definition Service implements strict plan quota checks during the creation of new workflow templates.

#### 5.6.1 Subscription Tiers and Constraints

The system maps the plan information received in the `x-plan` header to specific operational quotas:

| Plan Tier (`x-plan`) | Maximum Workflow Definitions | Enforced Limit Type |
| --- | --- | --- |
| `starter` (Default) | 5 | Hard limit (Template count) |
| `pro` | 50 | Hard limit (Template count) |
| `enterprise` | Unlimited | No limit enforced |

*Note: If the `x-plan` header is missing or unrecognized, the system automatically falls back to the `starter` tier quotas.*

#### 5.6.2 Verification Flow & Database Concurrency Safety

The quota verification is performed synchronously during `POST /workflows`. To prevent race conditions where a tenant administrator submits multiple template creation requests concurrently in parallel to bypass the quota, the check and insertion occur within a single serializable SQL transaction:

```mermaid
sequenceDiagram
    autonumber
    actor Admin as Tenant Administrator
    participant API as Gin Handler / Middleware
    participant Core as PublisherService
    participant DB as Postgres (workflow)

    Admin->>API: POST /workflows {key, name, xml} (Header: x-plan)
    API->>API: Extract x-plan & x-tenant-id
    API->>Core: CreateWorkflow(ctx, plan, tenantID, xml)
    
    Core->>DB: BEGIN TRANSACTION (Serializable Isolation)
    
    Note over Core,DB: Row Count & Lock Check
    Core->>DB: SELECT COUNT(*) FROM workflow w WHERE w.tenant_id = :tenant_id AND EXISTS (SELECT 1 FROM workflow_version wv WHERE wv.workflow_id = w.id AND wv.status IN ('DRAFT', 'PUBLISHED'))
    DB-->>Core: CurrentCount (e.g. 5)
    
    alt CurrentCount >= PlanLimit
        Core->>DB: ROLLBACK
        DB-->>Core: Rolled Back
        Core-->>API: Error: PLAN_QUOTA_EXCEEDED (403 Forbidden)
        API-->>Admin: 403 Forbidden (Problem Details JSON)
    else CurrentCount < PlanLimit
        Core->>DB: INSERT INTO workflow (id, tenant_id, business_key, name) VALUES (...)
        Core->>DB: INSERT INTO workflow_version (id, workflow_id, status, status='DRAFT', ...)
        Core->>DB: COMMIT
        DB-->>Core: Transaction Committed
        Core-->>API: Success (Workflow & Version IDs)
        API-->>Admin: 201 Created
    end
```

#### 5.6.3 Quota Exceeded Response (RFC-9457)

If the count check fails, the API immediately aborts and returns an HTTP `403 Forbidden` response utilizing the RFC-9457 Problem Details schema, carrying the `PLAN_QUOTA_EXCEEDED` error code:

```json
{
  "type": "https://api.workflow.platform/errors/plan-quota-exceeded",
  "title": "Plan Quota Exceeded",
  "status": 403,
  "detail": "The tenant has reached the maximum allowed limit of x workflow templates/versions for the 'abc' plan tier. Please upgrade to a higher tier to create additional workflows.",
  "instance": "/workflows",
  "code": "PLAN_QUOTA_EXCEEDED",
  "invalid_params": [
    {
      "name": "x-plan",
      "reason": "abc plan is capped at x templates. Currently utilizing x templates."
    }
  ]
}
```

---

### 5.7 Version Cloning Flow (§3.3.10)

The cloning endpoint creates a brand new root workflow and initializes its draft using the source version's XML in a transactional execution block.

```mermaid
sequenceDiagram
    autonumber
    actor Admin as Tenant Admin
    participant API as Gin Handler
    participant DB as Postgres Database

    Admin->>API: POST /workflows/:id/versions/:version_id/clone {new_key, new_name}
    Note over API: Verify tenant admin role and extract tenant ID
    API->>DB: BEGIN TRANSACTION (Read Committed)
    API->>DB: SELECT status, bpmn_xml FROM workflow_version WHERE id = :version_id AND tenant_id = :tid
    DB-->>API: Source version details
    Note over API: Verify status is PUBLISHED or ARCHIVED.<br/>If status is DRAFT, rollback and return 409 (INVALID_VERSION_STATUS)

    Note over API: Plan Quota Check
    API->>DB: SELECT COUNT(*) FROM workflow WHERE tenant_id = :tid
    DB-->>API: Total active workflows
    Note over API: Compare count against plan template limit.<br/>If exceeded, rollback and return 403 (PLAN_QUOTA_EXCEEDED)
    
    API->>DB: INSERT INTO workflow (id, tenant_id, business_key, name, created_by_user_id) VALUES (...)
    API->>DB: INSERT INTO workflow_version (id, workflow_id, status='DRAFT', bpmn_xml, created_by_user_id) VALUES (...)
    API->>DB: INSERT INTO outbox_events (id, event_type, payload, tenant_id, trace_id, attempts) VALUES (...) <br/> (workflow.template.cloned event)
    API->>DB: COMMIT
    DB-->>API: Transaction Committed
    API-->>Admin: 201 Created (workflow_id, version_id)
```

---

### 5.8 Version Promotion / Rollback Flow (§3.3.11)

Promoting a previously published version is an atomic pointer swap on the root workflow record that emits a standard `workflow.template.published` outbox event (with the `promoted_from_version_id` parameter populated).

```mermaid
sequenceDiagram
    autonumber
    actor Admin as Tenant Admin
    participant API as Gin Handler
    participant DB as Postgres Database
    participant SQS as SQS / SNS (wf.template.events)

    Admin->>API: POST /workflows/:id/versions/:version_id/promote
    Note over API: Verify tenant admin role and extract tenant ID
    API->>DB: BEGIN TRANSACTION (Serializable)
    API->>DB: SELECT id, active_version_id FROM workflow WHERE id = :id AND tenant_id = :tid FOR UPDATE
    DB-->>API: Workflow record details
    API->>DB: SELECT status, version_number FROM workflow_version WHERE id = :version_id AND workflow_id = :id
    DB-->>API: Target version details (verify status = 'PUBLISHED')

    Note over API: If active_version_id == version_id
    API->>DB: COMMIT (or ROLLBACK)
    DB-->>API: Transaction Finished
    API-->>Admin: 200 OK (Early exit / No-op)
    
    Note over API: If active_version_id != version_id
    API->>DB: UPDATE workflow SET active_version_id = :version_id WHERE id = :id
    API->>DB: INSERT INTO outbox_events (id, event_type, payload, tenant_id, trace_id, attempts) VALUES (...) <br> (workflow.template.published event with promoted_from_version_id)
    API->>DB: COMMIT
    DB-->>API: Transaction Committed
    
    Note over API,SQS: OutboxRelay polls pending event and publishes to SNS
    API->>SQS: Publish workflow.template.published to wf.template.events
    API->>DB: UPDATE outbox SET status='SENT', processed_at=now() WHERE id = :event_id
    API-->>Admin: 200 OK (active_version_id promoted)
```

---

## 6. Observability

To ensure high operational reliability, fast debugging, and multi-tenant performance tracking, the Definition Service implements a comprehensive observability framework using **Prometheus** for metrics collection, **OpenTelemetry** for distributed tracing, and **Structured Logging (Zap)** with correlated context.

### 6.1 Metrics Strategy (Prometheus)

All metrics are exposed via a standard HTTP `/metrics` endpoint scraped by Prometheus, unauthenticated (access control is enforced by network policy/ingress at the infra layer, not the app — same reasoning as `/internal/events`'s reliance on Kubernetes `NetworkPolicy`). None of the service-specific business metrics below carry a `tenant_id` label today — cross-tenant noisy-neighbor attribution for these particular counters/histograms is not currently possible; `wf_*` metrics are process-wide only.

**Base HTTP transport metrics** are provided automatically by `gincommon.MetricsMiddleware` (wired via `ObservabilityMiddlewares`). These require no additional code:

| Metric | Type | Description |
| --- | --- | --- |
| `http_requests_total` | Counter | Requests by method, path, status code |
| `http_request_duration_seconds` | Histogram | Request latency (IAM-tuned buckets) |
| `http_panic_total` | Counter | Panics recovered by `PanicRecoveryMiddleware` |
| `rate_limited_requests_total` | Counter | HTTP 429 responses |
| `build_info` | Gauge | `service`, `version` labels from `BUILD_VERSION` env |

**Base gRPC server metrics** are provided by `grpccommon.DefaultUnaryInterceptors` / `DefaultStreamInterceptors`:

| Metric | Type | Description |
| --- | --- | --- |
| `grpc_server_handled_total` | Counter | Calls by service, method, grpc_code |
| `grpc_server_handling_seconds` | Histogram | gRPC call latency |
| `grpc_server_request_size_bytes` | Histogram | Inbound payload sizes |
| `grpc_server_response_size_bytes` | Histogram | Outbound payload sizes |
| `grpc_server_inflight_requests` | Gauge | In-flight gRPC calls |

The following **service-specific business metrics** supplement the base layer:

#### 6.1.1 Core Metrics Catalog

- **`wf_submissions_total`** (CounterVec)
  - **Description**: Total workflow creation attempts.
  - **Labels**: `status` (`ok`, `err` — via the `outcomeLabel(err)` helper)
- **`wf_archive_total`** (CounterVec)
  - **Description**: Total workflow archive attempts.
  - **Labels**: `status` (`ok`, `err`)
- **`wf_publish_total`** (CounterVec)
  - **Description**: Total version publish attempts.
  - **Labels**: `status` (`ok`, `err`)
- **`wf_publish_latency_seconds`** (Histogram)
  - **Description**: End-to-end latency of `PublishVersion` (compile + DB tx combined). Serves as the primary SLI for publish performance.
  - **Labels**: none
- **`wf_clone_total`** (CounterVec)
  - **Description**: Total version clone attempts.
  - **Labels**: `status` (`ok`, `err`)
- **`wf_promote_total`** (CounterVec)
  - **Description**: Total version promote attempts.
  - **Labels**: `status` (`ok`, `err`)
- **`wf_compile_duration_seconds`** (Histogram)
  - **Description**: Duration of `compiler.Compile` calls only (BPMN XML → JSON DSL), isolated from DB-transaction time. Observed at both real call sites: `VersionService.Publish` (via `publishPreFlight`) and the `Diff`/`Export` fallback-recompile path (`resolvePlan`, used when no cached `CompiledPlanJSON` exists). `Promote` never compiles (reactivates an already-published, already-compiled version); `ValidationService.Validate` calls the separate, cheaper `compiler.Validate` structural check, not `Compile`.
  - **Labels**: none
- **`wf_validation_failures_total`** (Counter)
  - **Description**: Total BPMN validation calls (`ValidationService.Validate`) that returned at least one blocking validation error.
  - **Labels**: none
- **`internal_events_ingest_total`** (CounterVec)
  - **Description**: Inbound events received at `POST /internal/events` from the shared consumer.
  - **Labels**: `event_type` (e.g. `department.membership.revoked`), `result`
  - **Note**: `sqs_queue_depth` / `sqs_dlq_depth` for `membership-wf-q` are emitted by the shared workflow-events consumer, not the Definition Service.
- **`wf_cache_hits_total`** (Counter)
  - **Description**: Total gRPC `GetCompiledWorkflow` compiled-plan cache hits.
  - **Labels**: none
- **`wf_cache_misses_total`** (Counter)
  - **Description**: Total gRPC `GetCompiledWorkflow` compiled-plan cache misses (fetched from DB/recompiled instead).
  - **Labels**: none
- **`http_request_timeout_total`** (Counter) _(gincommon v1.2.0)
  - **Description**: Requests cancelled by `TimeoutMiddleware` before the handler responded.
  - **Labels**: `method`, `route`

**DB connection-pool gauges** (`platform-pgcommon` `pgmetrics.PoolStatsCollector`, registered once at startup against the live pool):

| Metric | Type | Description |
| --- | --- | --- |
| `pgcommon_pool_total_conns` | Gauge | Current total connections (idle + acquired + constructing) |
| `pgcommon_pool_idle_conns` | Gauge | Current idle connections available |
| `pgcommon_pool_acquired_conns` | Gauge | Current connections checked out by callers |
| `pgcommon_pool_max_conns` | Gauge | Configured connection limit |
| `pgcommon_pool_constructing_conns` | Gauge | Connections currently being established |
| `pgcommon_pool_empty_acquire_total` | Counter | Cumulative acquires that waited because the pool was exhausted — a rising value indicates the pool is undersized |

All six carry a constant `service` label (the `OTEL_SERVICE_NAME` value).

#### 6.1.2 Recommended Prometheus Alerting Rules

```yaml
groups:
  - name: definition_service_alerts
    rules:
      - alert: HighValidationFailureRate
        expr: rate(wf_validation_failures_total[5m]) / rate(wf_submissions_total[5m]) > 0.3
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "High XML validation failure rate"
          description: "Over 30% of BPMN submissions are failing validation checks in the last 10 minutes. Process-wide only — neither metric carries a tenant_id label today, so per-tenant breakdown is not available from this alert alone."

      - alert: PublishTransactionLatencyDegraded
        expr: histogram_quantile(0.95, rate(wf_publish_latency_seconds_bucket[5m])) > 2.0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Slow publish execution"
          description: "95th percentile of PublishVersion (compile + DB tx combined) is exceeding 2.0 seconds. If wf_compile_duration_seconds is also elevated, the regression is in BPMN compilation rather than the DB transaction."

      - alert: OutboxRelayDeliveryStalled
        expr: rate(outbox_dead_letters_total[5m]) > 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "Outbox relay delivery stalled"
          description: "Outbox events have exhausted their maximum retries and been moved to the dead-letter table (platform-events outbox_dead_letters_total). Immediate operator intervention is required to inspect outbox_dead_letters."

      - alert: RLSMissingGUC
        expr: increase(rls_violations_total{type="missing_or_invalid_guc"}[5m]) > 0
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "RLS GUC not set or malformed on a pool connection"
          description: "app.tenant_id was missing or invalid — likely a pool misconfiguration or raw admin session without the GUC set. rls_violation_log captures 1% of violations; one alert event ≈ 100 actual attempts (see §10.6)."

      - alert: RLSCrossTenantAccess
        expr: increase(rls_violations_total{type="cross_tenant_access"}[5m]) > 0
        for: 0m
        labels:
          severity: critical
        annotations:
          summary: "Cross-tenant row access blocked by RLS"
          description: "A correctly-authenticated session attempted to read another tenant's rows. Possible service bug or active attack. One alert event ≈ 100 actual attempts (§10.6). Page immediately."

```

### 6.2 Distributed Tracing (OpenTelemetry)

The service utilizes OpenTelemetry (OTel) to trace execution paths across decoupled services. The trace context is propagated inbound from Envoy API Gateway and outbound through SNS outbox payloads.

**Base HTTP server spans** are created automatically by `gincommon.TracingMiddleware` (part of `ObservabilityMiddlewares`). Each incoming HTTP request produces a root span named after its route; the OTel trace ID is stored in the Gin context under the key `trace_id` and echoed on the response as `X-Trace-ID`. OTLP/gRPC export is initialized via `gincommon.InitTracingFromEnv()` in `main.go`.

The service adds the following **additional child spans** on top of the gincommon baseline:

#### 6.2.1 Critical Trace Spans

1. **`HTTP POST /workflows/:id/versions/:version_id/publish` (Root Span)**:
   - Captures the entire publish request lifecycle.
   - Traces identity verification, context extraction, and structural diff checks.
2. **`BPMN_Compilation` (Child Span)**:
   - Measures raw XML parsing, graph traversal, split-join pairing, and JSON DSL generation.
3. **`DB_Publish_Transaction` (Child Span)**:
   - Measures database row locking (`SELECT FOR UPDATE`), workflow/version schema writes, and transactional outbox event insertion.

#### 6.2.2 Context Propagation

Distributed tracing context is propagated across network boundaries as follows:

1. **Outbound HTTP Calls**: For outbound HTTP requests (such as calling the Org & Membership Service `POST /tenants/:t/users/:u/eligibility`), the client uses OpenTelemetry transport wrappers to inject the active span's W3C context into the `traceparent` header.
2. **Inbound gRPC Server Calls**: The gRPC server endpoint (`GetCompiledWorkflow`) utilizes the `otelgrpc.UnaryServerInterceptor()` to extract the W3C context from gRPC metadata, linking the subsequent query spans to the client worker's runtime execution span.
3. **Outbox Message Injection**: For every outbox message written during publication, the current carrier state is serialized and injected into the outbox event payload metadata under the `traceparent` key. The Outbox Relay worker then publishes the message with the trace context preserved as an SNS message attribute.

### 6.3 Structured Logging and Auditing

The service implements structured logging in JSON format using **Zap** via the `pkg/logger` package from `platform-gincommon`. The logger is instantiated once in `main.go` via `logger.NewLogger(appEnv)` and passed to `gincommon.Config{Logger: log}`.

`gincommon.LoggingMiddleware` (part of `ObservabilityMiddlewares`) automatically emits a structured `http_request` log on every request completion. Sensitive fields are redacted by `service.Sanitize` before logging. Business-logic code uses the same Zap logger instance obtained from the config to emit additional log lines at the appropriate level.

#### 6.3.1 Correlated Context Fields

Every log statement within the request path automatically injects the following fields:

- `tenant_id`: Strict tenant isolation context.
- `user_id`: Identity of the performing administrator.
- `correlation_id` / `trace_id`: The distributed tracing reference.
- `workflow_id` & `version_id`: Target entity markers.

#### 6.3.2 Audit Log for Denied Actions & Security Events

To satisfy strict regulatory audit compliance and maintain debugging visibility, all denied operations, validation failures, or unauthorized access attempts are explicitly captured in structured security logs. These log events do not simply trigger silent HTTP error returns; they generate structured `TaskActionDenied` and `UnauthorizedAccessAttempt` warnings containing full audit lineage:

```json
{
  "level": "warn",
  "ts": "2026-05-18T16:40:00.123Z",
  "logger": "security.audit",
  "msg": "TaskActionDenied",
  "correlation_id": "c7a88192-3c22-4412-a128-665cd42c8888",
  "tenant_id": "7ca648b2-b432-4744-884c-35fd556a310c",
  "user_id": "4da18bde-7244-47ac-986c-665cd42caaaa",
  "action": "PUBLISH_WORKFLOW",
  "target_workflow_id": "8bb759c8-cd99-4aac-bfcc-448dd66fffff",
  "reason": "TOPOLOGICAL_DIVERGENCE",
  "details": {
    "force_publish_structural": false,
    "divergent_step_index": 2,
    "validation_error": "Structural elements have diverged. Draft contains new parallel splits not present in published template."
  }
}
```

This structured security logging ensures that compliance teams can trace exactly who attempted an operation, when they attempted it, what boundaries were violated, and the technical rationale for the system rejection.

---

## 7. Eventing & Integration (Transactional Outbox)

To maintain loose decoupling and eliminate database-to-broker write inconsistencies (the "dual-write" problem), the Definition Service delegates all cross-service event notifications to a **Transactional Outbox** model. The background worker asynchronously relays queued outbox events to Amazon SNS, ensuring at-least-once delivery guarantees.

### 7.1 Outbox Relay Worker Mechanics

The background relay worker (`OutboxRelay`) runs as a horizontally scalable daemon within the application container.

```mermaid
stateDiagram-v2
    [*] --> Idle
    Idle --> FetchPending : Every 500ms
    FetchPending --> ProcessEvent : Events Found
    FetchPending --> Idle : Empty Queue
    
    state ProcessEvent {
        [*] --> PublishToSNS
        PublishToSNS --> MarkSent : SNS Success
        PublishToSNS --> HandleFailure : SNS Error / Timeout
        
        state HandleFailure {
            [*] --> CheckRetries
            CheckRetries --> IncrementRetry : Retries < Max (5)
            CheckRetries --> MarkFailed : Retries >= Max (5)
        }
    }
    
    MarkSent --> FetchPending
    IncrementRetry --> FetchPending
    MarkFailed --> FetchPending
```

#### 7.1.1 Fetching and Processing Cycle (platform-events outbox.Runner)

1. **Polling**: The `outbox.Runner` polls the `outbox_events` table every configured interval (default: 5s, with exponential backoff on DB error up to 30s) using a single SELECT query optimized for highly concurrent, lock-free consumer workers:

   ```sql
   SELECT id, event_type, payload, tenant_id, trace_id, attempts, created_at, scheduled_at
   FROM outbox_events
   WHERE published_at IS NULL AND scheduled_at <= NOW()
   ORDER BY created_at ASC
   LIMIT :batch_size
   FOR UPDATE SKIP LOCKED;
   ```

   *Note: Using `FOR UPDATE SKIP LOCKED` guarantees that multiple instances of the worker can scale horizontally without blocking each other or double-processing the same outbox events.*

2. **Idempotency & Safety**:
   The worker uses the primary key `id` (UUID v7) as the `MessageGroupId` and `MessageDeduplicationId` when dispatching to SNS/SQS. This ensures strict ordering and exact-once semantics even if the worker process crashes before marking the row as published.

3. **Leasing**:
   To prevent multiple runners from picking up the same batch while publish operations are in flight, the runner pushes `scheduled_at` forward by `ClaimLeaseDuration` (default 10 min).

4. **Retry & Backoff**:
   On delivery failure, the `attempts` count is incremented, `scheduled_at` is reset to `NOW()` (to allow immediate retry by another worker), and `last_error` is updated.

5. **Max Retries & Dead Letter Table**:
   If a record fails after `MaxAttempts` (default: 5), it is automatically moved to the `outbox_dead_letters` table. The runner fires Prometheus metrics/alerts (`outbox_dead_letters_total`) on dead letter occurrences.

#### 7.1.2 Database Pruning and Cleanup (TTL)

To prevent transaction logs and processed message histories from growing indefinitely, two background cron jobs run daily (during off-peak hours):

- **Outbox Cleanup**: Physically deletes `SENT` outbox records older than 7 days, and explicitly flags old `FAILED_EXHAUSTED` records for manual operator review or archival.
- **Idempotency Log Cleanup**: Physically deletes `processed_event` records older than 7 days (corresponds to the maximum SQS message retention limit).

### 7.2 Event Payload Schemas

The Definition Service emits a single outbound SNS event: `workflow.template.published`. Three events that were previously designed (`archived`, `eligibility_invalidated`, `cloned`) were removed:

- **`archived`** — not needed. The archive guard (`CheckActiveInstances` gRPC) ensures no instances are running before archive completes. No downstream consumer starts new instances on an archived key; if a stale client attempts it, `GetCompiledWorkflow` returns `status=ARCHIVED` and the caller rejects. No proactive push required.
- **`eligibility_invalidated`** — replaced with a direct internal HTTP call from Definition to Execution (`PauseUserTasks`). Execution pauses task assignments by `user_id` from its own runtime data — no SNS fan-out or separate SQS subscription needed on the Execution side. See §7.4.2 step 5.
- **`cloned`** — a clone creates a DRAFT. No downstream service has anything to do with a DRAFT; the `published` event is emitted in the normal flow when that draft is later published.

> **Schema version field**: The `buildEnvelope` call for `workflow.template.published` must add `events.WithSchemaVersion("1")` (available in `platform-events v1.2.0`). This sets the envelope's `SchemaVersion` field, serialized on the wire as `"specversion": "1"` (the Go struct's JSON tag — see `platform-events/pkg/events/envelope.go`), allowing consumers to schema-gate on version. Increment to `"2"` only on additive payload changes.

#### 7.2.1 `workflow.template.published` Event

Fired immediately when a workflow draft is promoted to the active `PUBLISHED` status. This is a **cache-warm push hint**: it tells the Execution Service that a new runnable version for `workflow_key` is live so it can pre-fetch the compiled plan via `GetCompiledWorkflow` gRPC before the first `StartWorkflow` request arrives. The event does not carry the compiled plan — consumers call gRPC on receipt.

**StartWorkflow flow (for clarity):** An external actor (user or upstream system) calls the Execution Service's `StartWorkflow` API with `(tenant_id, workflow_key)`. Execution resolves the current published `version_id` from its local cache (warmed by this event), calls `GetCompiledWorkflow(tenant_id, version_id)` → receives `compiled_plan_json` + `status` + `is_valid`, rejects if `status ≠ PUBLISHED` or `is_valid = false`, otherwise starts the Temporal instance. The `template.published` event ensures this lookup is a cache hit, not a cold fetch.

**Why reference-only (no embedded `compiled_plan`):** SNS has a hard 256 KB message size limit. A workflow with many departments, stages, and assignees can produce a compiled plan that approaches or exceeds this limit. Embedding the full plan inline creates an operational risk that grows with workflow complexity. Consumers that need the plan call `GetCompiledWorkflow` gRPC on receipt — this is a fast, cache-friendly read on the internal network and does not require the Definition Service to be up at event-dispatch time (the version row is already committed).

- **SNS Topic**: `arn:aws:sns:us-east-1:123456789012:wf-template-events`
- **JSON Payload Structure**:

```json
{
  "id": "9fa88cde-824c-47bc-836b-665cd42c2222",
  "type": "workflow.template.published",
  "source": "workflow-definition-svc",
  "specversion": "1",
  "tenant_id": "7ca648b2-b432-4744-884c-35fd556a310c",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "time": "2026-05-18T16:42:00.000Z",
  "data": {
    "workflow_id": "7ca648b2-b432-4744-884c-35fd556a310c",
    "workflow_key": "tender-review",
    "version_id": "11abcc22-3844-42bc-938b-665cd42c1111",
    "version_number": 3,
    "artifact_hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "published_by": "4da18bde-7244-47ac-986c-665cd42caaaa",
    "promoted_from_version_id": "8fa88cde-824c-47bc-836b-665cd42c2222"
  }
}
```

> `promoted_from_version_id` is nullable — populated only on version promotions/rollbacks, null on first publish.

#### 7.2.2 AWS Glue Schema Registry

**`api/asyncapi.yaml` is the design-time contract; AWS Glue Schema Registry (ap-south-1) is the runtime enforcement point.**

The `workflow.template.published` JSON Schema is registered as a schema version in a single Glue registry named `workflow-template-events`. The registry name and ARN are injected via env vars (§1.8.4).

##### Serialization & Encoding (Producer Side)

Before enqueuing an event into the transactional outbox (`outbox_events`), the service encodes the JSON payload bytes against the registered schema version using the Glue Schema Registry SDK. The SDK prepends the schema version ID as a header prefix in the payload bytes, which are then saved in the outbox `payload` field.

##### Decoding & Validation (Consumer Side)

Downstream consumers (Execution Service, Audit, Notification Service) decode the payload bytes using the Glue SDK, which strips the version prefix, fetches the schema definition (cached locally after the first fetch), and validates the payload against it.

##### Schema Evolution Rules

- **Additive changes** (new optional fields) → register a new schema version in Glue and increment `schema_version` in the envelope; update `api/asyncapi.yaml` in the same commit.
- **Breaking changes** (remove/rename fields, change types) → create a new Glue schema name and bump the major version.
- `api/asyncapi.yaml` must be updated in the same commit as any Glue registry version bump — the CI gate (`asyncapi validate`) enforces consistency.

### 7.3 Decoupled Service Integrations

Downstream consumers subscribe to the shared SNS topic (`wf.template.events`) using SQS fanout queues:

1. **Execution Service** — queue: `wf-execution-sync-q`
   - On `workflow.template.published`: pre-fetches the compiled plan via `GetCompiledWorkflow` gRPC to warm its local cache; updates its `workflow_key → version_id` mapping so that subsequent `StartWorkflow(workflow_key)` calls resolve the correct version without a cold fetch.
   - Archived and eligibility-invalidated notifications are **not** delivered via SNS. Archive is handled by the `CheckActiveInstances` guard (no running instances start post-archive). Eligibility invalidation is delivered as a direct gRPC call from Definition (`PauseUserTasks`, §7.4.2 step 5) rather than a fan-out event — this avoids requiring Execution to maintain an additional SQS subscription.
2. **Audit & Compliance Service** — queue: `wf-template-audit-q`
   - **Consumer Behavior**: Upon receiving `workflow.template.published`, the Audit Service indexer extracts event metadata and stores it in the partitioned `audit_events` database. The retention duration for these records is customizable and plan-dependent (e.g. Starter: 1 year, Pro: 3 years, Enterprise: 7 years). For regulatory workflow approvals (with approver signatures), a hard 7-year retention policy is enforced across all plans.
3. **Notification Service** — queue: `wf-template-notif-q`
   - **Consumer Behavior**: On `workflow.template.published`, triggers in-app dashboard alerts and email notifications via SES to the tenant's admin team confirming the status transition. Eligibility-invalidation alerts to admins are sent by Execution Service (or a dedicated alerting path) after receiving the `PauseUserTasks` call — not by Definition emitting an SNS event.

### 7.4 Inbound Event Ingest (`POST /internal/events`)

The Definition Service does **not** consume SQS directly. The shared workflow-events consumer subscribes to `membership-wf-q` (itself subscribed to the `iam.membership.events` SNS topic) and forwards each envelope to the Definition Service's internal HTTP endpoint `POST /internal/events`. The endpoint runs the same invalidation logic that the in-process consumer used to run.

#### 7.4.1 Transport & Delivery Contract

The queue topology (`membership-wf-q`, its `membership-wf-q-dlq` DLQ, visibility timeout `30s`, max receive count `5`) is now owned by the **shared consumer's** infrastructure, not the Definition Service. Between the consumer and this endpoint:

- The consumer POSTs the full `events.Envelope[json.RawMessage]` (carrying `id`, `type`, `tenant_id`, `data`) to `/internal/events`.
- The endpoint is on an **internal route group** (service-to-service auth — shared-secret header / mTLS / network policy), **not** the gateway-authenticated public API. It is never exposed through the Envoy gateway.
- **Retry contract**: `2xx` (incl. an idempotent dedup no-op) marks the message handled; `4xx` (malformed payload) is treated as non-retryable by the consumer and routed to its DLQ; `5xx`/timeouts cause the consumer to retry and eventually DLQ per its `maxReceiveCount`.

#### 7.4.2 Handling `department.membership.revoked` Event

- **Event Type**: `department.membership.revoked`
- **JSON Payload Schema**:

  ```json
  {
    "event_id": "f0a11222-3844-42bc-938b-665cd42c9999",
    "event_type": "department.membership.revoked",
    "timestamp": "2026-05-25T13:51:06.000Z",
    "tenant_id": "7ca648b2-b432-4744-884c-35fd556a310c",
    "traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
    "data": {
      "user_id": "3fa18cde-7244-47ac-986c-665cd42caaaa",
      "department_id": "<format TBD by Org & Membership service LLD>",
      "role": "reviewer"
    }
  }
  ```

  > **`department_id` format**: The exact format of `department_id` in this event payload is owned by the Org & Membership service and will be defined in its LLD. The Definition Service stores the lane `name` attribute from the BPMN (owned by the Profile Service) in `workflow_node_assignee.department_id`. Both formats must align for the invalidation query (`WHERE department_id = :revoked_department_id`) to match. **Revisit when the Org & Membership service LLD is complete.**

- **AsyncAPI documentation**: also documented as a `receive` operation in `api/asyncapi.yaml` (message `DepartmentMembershipRevoked`, schema `DepartmentMembershipRevokedInbound`) — non-`Payload`-suffixed so `schema-gov`'s extractor skips it, since this event is owned/registered by the Org & Membership service, not Definition. Mirrors `execution_service`'s own convention for its inbound events (§7.4's asyncapi discussion has no equivalent for Definition prior to this).

- **What and How Happens** (executed by the `POST /internal/events` handler after dispatching on `env.Type`):

  1. **Idempotency Check**: The handler records the event in the `processed_event` table to verify it has not been processed (the dedup remains in the Definition Service, keyed on the forwarded envelope `id`):

     ```sql
     INSERT INTO processed_event (event_id, consumer, event_type) 
     VALUES (:event_id, 'membership-wf-q', 'department.membership.revoked') ON CONFLICT DO NOTHING;
     ```

     If 0 rows are affected, the event was already processed — return `2xx` (no-op) so the consumer does not retry.

  2. **Set Tenant Context**: The handler injects the RLS GUC from the envelope `tenant_id` via `pgcommon.WithGUCSet` (`SET LOCAL app.tenant_id = :tenant_id`) — the manual-injection pattern shared with the gRPC path (§14), since this is a service-to-service call without gateway identity headers.

  3. **Find Affected Drafts and Templates**: Query the `workflow_node_assignee` table to identify affected workflow versions:

     ```sql
     SELECT DISTINCT workflow_version_id, node_key
     FROM workflow_node_assignee 
     WHERE user_id = :user_id AND department_id = :department_id;
     ```

  4. **Invalidate Versions**: For each identified `workflow_version_id`:

     - Set `is_valid = false` on `workflow_version`.
     - Append a structured error object to the version's `validation_errors_json` array:

        ```json
        {
          "node_id": :node_key,
          "error": "Default assignee is no longer eligible: Department membership revoked"
        }
        ```

  5. **Notify Execution Service**: After the DB transaction commits, call `ExecutionClient.PauseUserTasks(ctx, tenantID, userID)` via gRPC. Execution queries its own runtime task records for active assignments where `assignee_user_id = revoked_user_id` and pauses them. This is a synchronous call with retries (3 attempts, bounded backoff); if Execution is unreachable after retries, the handler returns `5xx` so the shared consumer retries the full event and Execution is notified on the next attempt. **Why a direct gRPC call instead of SNS:** avoids requiring Execution to maintain a separate SQS subscription to `wf.template.events`; Execution targets by `user_id` (runtime assignment data it owns) rather than `version_id` (a template-design-time concept), so the derived event carried no useful extra information.

  6. **User Deletion Safety Net Handling**: When a user is deleted from the platform, the Org & Membership Service automatically revokes all of their department memberships, emitting individual `department.membership.revoked` events. As a result, the Definition Service does not need to consume `user.deleted` events directly; any template containing the deleted user as a default assignee will be transitively invalidated via the membership revocation flow.

  7. **Impact on Running Instances (Execution Service cross-reference)**: Step 5 instructs Execution to pause active task assignments for the revoked user. Execution uses `user_id` to locate affected tasks in its own runtime store — this is more precise than a version-scoped pause, which would over-pause tasks that had already been reassigned to a different user. An admin must use `POST /tasks/{id}/reassign` on the Execution Service to assign a new eligible user and unblock paused tasks. See Execution Service LLD §4.5 (Task Assignee Lifecycle) for the full reassignment flow.

  8. **Admin Recovery Path**: To restore the ability to start new instances:
     1. Admin creates a new draft from the invalidated version (inheriting the BPMN XML).
     2. Admin updates the default assignee in the BPMN XML to a new eligible user.
     3. Admin publishes the new version — this triggers a fresh eligibility check, creates new `workflow_node_assignee` rows, and emits a `workflow.template.published` event.
     4. Running instances on the old (invalidated) version are **not migrated**. They continue with their runtime-reassigned users until completion. Only new instances use the new version.

### 7.4.3 Default Assignee Change Flows

The Definition Service is exclusively responsible for **template-level default assignees** — the `default_assignees` values baked into published versions. There are two distinct paths through which a default assignment can be changed or invalidated at this layer.

#### Flow A: Manual Admin Update (New Version Publish)

This is the standard path when an admin intentionally wants to change who gets assigned by default on future instances.

1. Admin creates a new draft from the existing published version (inheriting the BPMN XML).
2. Admin updates the `candidateUsers` in `<zeebe:assignmentDefinition>` for the relevant node in the BPMN XML.
3. Admin calls `POST /workflows/:id/versions/:versionId/publish`.
4. The publish handler validates the new assignee's eligibility via `POST /tenants/:t/users/:u/eligibility?department=:dept&level=:level` on the Org & Membership Service.
5. On success, the new `compiled_plan_json` is written and new rows are inserted into `workflow_node_assignee` in the same transaction.
6. A `workflow.template.published` event is written to the outbox and fanned out to downstream consumers.

**Scope**: affects only **new** workflow instances started from this point forward. All running instances retain the `assignee_user_id` that was resolved when they were started — they are never retroactively re-pointed.

#### Flow B: Membership Revocation Event (`department.membership.revoked`)

This is the automated path triggered when an assignee loses their department membership via an IAM event. The full processing steps are documented in §7.4.2. In summary:

1. The shared workflow-events consumer receives the event from `membership-wf-q` and forwards it to `POST /internal/events`; the handler queries `workflow_node_assignee` to find all versions (any status) referencing the revoked user.
2. Affected versions are marked `is_valid = false` with a structured validation error appended to `validation_errors_json`.
3. Definition calls `ExecutionClient.PauseUserTasks(tenantID, userID)` — Execution pauses active task assignments for that user from its own runtime data.

4. The admin recovery path is identical to Flow A: create a new draft, assign an eligible user, publish.

#### Out of Scope for the Definition Service — Runtime Reassignment

The following IAM events from the HLD event catalog affect **live task assignments on running instances**. These events are not consumed by the Definition Service. They route to the Execution Service via `user-wf-q` and `tender-wf-q`:

| Event | Source | Runtime Effect |
| --- | --- | --- |
| `user.availability.changed` | User Profile | Reroutes pending tasks based on OOO status |
| `delegation.started` / `delegation.ended` | Org & Membership | Temporarily reassigns pending activities to the delegate and back |
| `TenderAssigneeOverridden` | Tender Service / Admin | Overrides the assignee on a specific task within a running instance |
| `user.deleted` | User Profile | Triggers membership revocations (handled transitively via `department.membership.revoked`) and pauses affected live instances |

Runtime reassignment flows — including the `POST /tasks/{id}/reassign` admin endpoint, the `AssigneeOverrideSignal` to Temporal, and the `workflow_task_assignment` audit trail — will be documented in the Execution Service LLD §4.5 (Task Assignee Lifecycle).

#### 7.4.4 Exclusion of Tenant Lifecycle Events

The Definition Service intentionally does **not** subscribe to the `tenant-wf-q` or consume tenant lifecycle events (such as `TenantAccessSuspended` or `TenantAccessRevoked`). Because the Definition Service only handles design-time template compilation and version drafts, active tenant locks are instead enforced upstream at the API Gateway (which rejects incoming user requests for suspended tenants) and downstream in the Execution Service (which handles pausing in-flight Temporal workflow executions).

---

### 7.5 Contract with Execution Service (DSL Schema)

The Definition Service owns the compilation of the `compiled_plan_json` (DSL). This structure forms a strict API contract with the downstream Execution Service and Temporal Workers:

- **Backward Compatibility**: The DSL schema is strictly additive. No existing keys (e.g., `sequential`, `parallel`, `subworkflow`) can be removed or renamed without a major version upgrade of the Execution Service workers.
- **Runtime Interpretation**: The Definition Service provides the *structure*. It does not evaluate runtime conditions (e.g. `condition_expression` on exclusive gateways). The Execution Service is exclusively responsible for interpreting these conditions using runtime instance data payload.

---

## 8. Security

This section consolidates all security controls enforced by the Definition Service. Controls are split into three layers: transport, application, and data.

### 8.1 Transport Security

- **mTLS between all pods**: Enforced by Envoy service mesh. No service can be called without a valid client certificate; the Definition Service trusts that headers were injected by Envoy and cannot be spoofed by arbitrary callers.
- **TLS termination**: All external traffic terminates at the Envoy API Gateway. The service never directly handles raw TLS.
- **Header validation**: The handler middleware validates the presence of Envoy-injected gateway headers (`x-tenant-id`, `x-user-id`, `x-tenant-roles`, `x-departments`, `x-plan`, `x-feature-flags`, and `traceparent` for distributed tracing). Mutating endpoints require `x-tenant-id`, `x-user-id`, and `x-tenant-roles`. _Note: `traceparent` is proxied by Envoy from upstream; if not present, tracecontext is generated at the boundary via OpenTelemetry (OTel) HTTP middleware.

### 8.2 Authentication & Authorisation

- **Identity**: Authentication is handled entirely upstream by Keycloak + the ext_authz service. The Definition Service does not validate JWTs; it trusts the injected headers. The `gincommon.RequireAuth` middleware (part of `ProtectedMiddlewares`) enforces that `x-user-id` and `x-tenant-id` are both present, non-empty, trimmed, ≤ 256 characters, and free of control characters. Any request failing these checks receives an immediate `401 Unauthorized` before the handler is reached.
- **Tenant context propagation**: `gincommon.ContextMiddleware` (also part of `ProtectedMiddlewares`) reads the validated headers and constructs a `*domain.RequestContext` stored in the Gin context. All handlers call `gincommon.RequestContext(c)` to access `TenantID`, `UserID`, `Roles`, and `TraceID` — no direct header parsing occurs in handler code.
- **Role enforcement**: All mutating endpoints (`POST`, `PUT`, `DELETE`) are protected with `gincommon.RequirePermission("write", "workflow", authz)` where `authz` is a production `port.Authorizer` implementation backed by the `x-tenant-roles` header. It rejects callers whose roles do not include `tenant_admin` or `tenant_owner` with `403 Forbidden`. Read endpoints (`GET`) require only a valid `x-tenant-id` (enforced by `RequireAuth`).
- **Department/role header**: `x-departments` (§3.1) is gateway-injected to this service like every other header, as comma-separated `dept_uuid:role` pairs — this service doesn't currently gate any authorization decision on it (role enforcement above uses `x-tenant-roles` only), but it's documented here for completeness since a future department-scoped endpoint would read it the same way `execution_service.md` §9.2 already does.
- **Tenant boundary**: Every database query sets the `app.tenant_id` GUC via `pgcommon.WithGUCSet` before any DB access. The `tenant_id` is always sourced from the validated `x-tenant-id` gateway header — never from the request body. This prevents callers from self-selecting a different tenant's data. Cross-tenant data access is impossible regardless of application-layer checks because RLS enforces it at the DB layer.
- **Connection Role Restrictions**:
  - **API and gRPC Endpoints** connect to PostgreSQL using a dedicated application role (e.g. `definition_svc_app`) that has `BYPASSRLS=false` explicitly set. This role cannot bypass row-level security, ensuring that missing `app.tenant_id` configurations result in no rows returned rather than cross-tenant leaks.
  - **Background Processes (Outbox Relay, Clean-up Crons)** connect using a separate dedicated database role (e.g. `definition_svc_relay`) that is either the table owner or has the `BYPASSRLS=true` permission. This allows the Outbox Relay to scan and process outbox rows across all tenants in a single unified query loop.

#### Trust Boundary

The service trusts `x-*` identity headers because mTLS in the service mesh guarantees they originate from Envoy after `jwt_authn` validates the Bearer token and `ext_authz` injects the enriched headers. The service performs no JWT validation itself — that responsibility sits entirely upstream. `RequireAuth` (from `gincommon.ProtectedMiddlewares`) rejects any request whose `x-user-id` or `x-tenant-id` header is missing, empty, longer than 256 bytes, or contains control characters, returning `401 Unauthorized` before the handler is reached. Requests that pass `RequireAuth` are therefore guaranteed to carry a Keycloak-validated identity; the service treats them as trusted.

Internal routes are further isolated: the gRPC port (`:9090`) and the `POST /internal/events` ingest endpoint receive no gateway-injected headers and are instead restricted to in-mesh callers by Kubernetes `NetworkPolicy` (plus service-to-service auth on `/internal/events`). The gRPC handler receives `tenant_id` in the request payload and calls `pgcommon.WithGUCSet` directly to set the RLS GUC (design decision 14, §14). The `/internal/events` handler likewise extracts `tenant_id` from the forwarded envelope and calls `pgcommon.WithGUCSet` directly — the same manual-injection pattern (the former SQS consumer relied on `platform-events` auto-injecting the GUC, which no longer applies on the HTTP path).

### 8.3 Input Validation & XML Security

All BPMN uploads pass through a hardening pipeline before any parsing occurs (cross-reference §4.1.5):

- **Size cap**: 10 MB hard limit enforced by the Gin body limit middleware before the handler reads any bytes.
- **XXE prevention**: The XML decoder runs with external entity resolution disabled. The service does not follow `DOCTYPE` declarations or `SYSTEM` entity references.
- **XML bomb guard**: A custom `TokenReader` wrapper counts XML tokens and aborts if the count exceeds 1,000,000.
- **Element allowlist**: Only elements in §4.1.2 are accepted. All other BPMN elements are rejected with a structured 422 error.
- **Zeebe property validation**: All five Zeebe properties are validated for type, value range, and cross-referential consistency before compilation proceeds.

### 8.4 Data Security

- **No plaintext secrets**: Database credentials, SNS ARNs, and AWS keys are injected via AWS Secrets Manager at pod startup. They are never in environment variables in plaintext.
- **BPMN XML at rest**: Stored in `workflow_version.bpmn_xml` (TEXT). No field-level encryption is applied; the column is protected by RLS and database-level encryption at rest (AWS RDS encryption).
- **Compiled DSL**: Stored as JSONB in `workflow_version.compiled_plan_json`. Same protections as above.
- **Outbox payloads**: `outbox.payload_json` does not contain PII. Tenant and workflow IDs are UUIDs. Purged via TTL after 7 days post-processing (§7.1.2).

### 8.5 Audit Trail

All state-changing operations (create, publish, archive, discard, clone, promote) write a structured log entry at `INFO` level with the following fields: `tenant_id`, `user_id`, `workflow_id`, `version_id`, `action`, `timestamp`, `trace_id`. These fields are indexed and queryable (cross-reference §6.3.2). The `workflow.template.published` SNS event provides the external audit trail consumed by the Audit & Compliance Service; archive operations are captured via structured logs only.

### 8.6 PII and Data Classification

The Definition Service stores **no PII**. All user references are stored as opaque UUIDs (`created_by_user_id`, `workflow_node_assignee.user_id`). No display names, email addresses, phone numbers, or any other personal data are stored or cached. As a result:

- GDPR soft-delete and field-scrubbing obligations do not apply to this service.
- Outbox event payloads (`workflow.template.published`) contain only UUIDs and workflow metadata — no personal data.
- Logs and audit trails reference `user_id` (UUID) only. The User Profile Service is the authoritative store for resolving UUIDs to display names.

If a future change introduces a cached display name or any other personal field, this section must be revisited and GDPR obligations assessed before merging.

### 8.7 PgBouncer & Connection Pools

To ensure optimal connection management, the service connects to PostgreSQL through PgBouncer.

- **Pool Size**: The connection pool is strictly capped at **10 connections** for the service instance, aligning with the target specified in HLD §7.1.
- **Role Isolation & Configuration**:
  - **API Handler Role**: Connects using a role lacking the `BYPASSRLS` attribute. Session variables are set dynamically using `SET LOCAL app.tenant_id = :tenant_id` at transaction initialization.
  - **Relay Role**: The background Outbox Relay connects using a database role with table ownership or `BYPASSRLS = true` permission, allowing it to scan and dispatch outbox items across all tenants in a single pass.
- **PgBouncer Pool Mapping**: PgBouncer is configured in Transaction Mode to allow sharing server connections, ensuring efficient scalability of connection slots.

---

## 9. Non-Functional Requirements

### 9.1 Performance Targets

- **BPMN upload & parse**: p99 ≤ 300 ms for files up to 10 MB on a `t3.medium` equivalent instance.
- **Draft save** (`PUT /draft`): p99 ≤ 50 ms — single UPDATE with no compilation.
- **Publish** (full flow): p99 ≤ 800 ms — includes parse, validate, compile, hash, diff check, and serializable transaction with outbox write.
- **List workflows** (`GET /workflows`): p99 ≤ 100 ms — tenant-indexed paginated query.
- **Validation-only** (`POST /validate`): p99 ≤ 200 ms — stateless, no DB writes.

### 9.2 Dependency Failure Matrix

| Dependency | Failure mode | Service behaviour | Data loss risk |
| --- | --- | --- | --- |
| **PostgreSQL** | Down / unreachable | All DB-backed endpoints return `503`. `/readyz` fails → pod removed from load balancer rotation. Background workers (outbox relay, pruner) pause and retry on reconnect. | None — no write is acknowledged without a successful DB commit. |
| **Valkey (cache)** | Down / unreachable | Three effects: (1) compiled-plan cache miss falls through to Postgres transparently (no caller-visible impact); (2) idempotency middleware — cache miss → request passes through uncached (duplicate protection degraded, not lost); (3) draft-edit lock acquisition fails → handler returns `503`. `/readyz` returns `503 {"status":"unavailable","cache":"unreachable"}` — pod is removed from load balancer rotation. | None for business data. Duplicate-mutation window open for the outage duration. |
| **SNS (outbox publisher)** | Down / throttled | Outbox rows accumulate with `published_at IS NULL`. The relay retries with exponential backoff. After `MaxAttempts` (default 5), rows move to `outbox_dead_letters` and alert fires. No HTTP request fails due to SNS unavailability — the write completes before the relay runs. | None — events are durable in `outbox_events` until delivered. Dead-lettered events require manual reprocessing via `ReprocessDeadLetters`. |
| **Glue Schema Registry** | Down / unreachable | The AWS Glue SDK caches schema versions locally after the first fetch; a short-term outage is transparent. On a cold start (no cached version), encode fails → outbox publish is blocked → outbox rows accumulate; alert via `outbox_dead_letters_total`. Schema version cache TTL should be ≥ 5 min to ride out transient Glue unavailability. | None — events remain buffered in `outbox_events`. |
| **Execution Service gRPC** | Down / timeout | `ArchiveWorkflow` calls `GetActiveExecutions` to check for running instances before archiving. On timeout (default 5 s), the handler returns `503 UPSTREAM_UNAVAILABLE`. No archive proceeds. The workflow remains in its current state. | None. |
| **Inbound event ingest** (`POST /internal/events`) | Definition pod down, or shared consumer down | The shared consumer holds `department.membership.revoked` messages on `membership-wf-q` (SQS retention: 14 days) and retries the HTTP POST until the Definition Service is healthy. On recovery, events are reprocessed idempotently via `processed_event`. `is_valid` flags may be stale for the duration of the outage. | None — SQS retains the messages and the consumer retries; the `processed_event` dedup prevents double-processing. |
| **Org & Membership eligibility endpoint** | Down at publish time | Publish calls the membership eligibility check for each assignee. On timeout/5xx, publish returns `503 UPSTREAM_UNAVAILABLE`. The draft is not promoted. | None. |

### 9.3 Service-Level Objectives

The following SLOs are the implementation targets for this service. They are referenced by alerting thresholds in §6.1.2.

#### Per-endpoint p99 latency

| Endpoint | p99 Target | Scenario |
| --- | --- | --- |
| `GET /workflows` (list) | 30 ms | Filtered DB query; no cache |
| `GET /workflows/:id` (single + version list) | 20 ms | DB read; no compiled-plan fetch |
| `GET /workflows/:id/draft`, `GET /versions/:id` | 20 ms | Valkey compiled-plan hit |
| `GET /workflows/:id/draft`, `GET /versions/:id` | 50 ms | Cache miss → DB fallback + back-fill |
| `PUT /draft`, `POST /discard`, `POST /promote`, `POST /clone` | 100 ms | Single `RunInTx` + outbox enqueue |
| `POST /versions/:id/publish` | 500 ms | BPMN compile + assignee eligibility check + SERIALIZABLE tx |
| `POST /workflows/validate` | 300 ms | Stateless compile; no DB write |
| gRPC `GetCompiledWorkflow` | 10 ms | Valkey hit |
| gRPC `GetCompiledWorkflow` | 40 ms | Cache miss → DB read + back-fill |
| Availability | 99.9% monthly | Per HLD |

#### Cache targets

| Target | Value | Alert threshold |
| --- | --- | --- |
| Compiled-plan cache hit ratio (steady state) | ≥ 90% | Alert `wf_cache_misses_total / (wf_cache_hits_total + wf_cache_misses_total) > 0.1` sustained for 5 m |
| Valkey client timeout | 50 ms | Timeout treated as cache miss; falls through to Postgres. Never blocks the SLO budget. |

---

## 10. Design Decisions

This section captures the key architectural and design decisions made for the Workflow Definition Service along with their technical rationales.

### 10.1 Single Draft Limitation per Workflow

- **Decision**: Enforce a strict database and logic constraint that a workflow definition can have at most one active workspace in `DRAFT` status at any given time.
- **Rationale**: Simplifies the state model and canvas editing interface. It forces a git-like branch-and-merge flow, preventing concurrent conflicts where multiple administrators override each other's work in separate draft branches for the same workflow.

### 10.2 Transactional Outbox Pattern for Event Publishing

- **Decision**: Use the Transactional Outbox pattern with a local `outbox` table and an asynchronous background worker (`OutboxRelay`) instead of writing directly to SNS from API goroutines.

- **Rationale**: Guarantees eventual consistency and at-least-once event delivery. By persisting events within the same SQL transaction as the state modification (e.g. publishing or archiving), it avoids the "dual-write" problem where DB commits succeed but network errors cause broker dispatch failures.

### 10.3 Row-Level Security (RLS) for Multi-Tenant Isolation

- **Decision**: Leverage native PostgreSQL Row-Level Security (RLS) driven by connection GUC settings (`app.tenant_id`) to enforce data isolation instead of relying solely on application-layer `WHERE tenant_id = ...` clauses.

- **Rationale**: Provides database-level defense-in-depth. RLS policies act as an absolute barrier at the database driver layer, ensuring that even if a developer makes a coding error (e.g., omitting a tenant filter in sqlc queries), the database will not leak data across tenants.

- **Required RLS policy properties** (see Appendix A for DDL):
  - `FORCE ROW LEVEL SECURITY` — prevents the table owner role from bypassing RLS.
  - `REVOKE ALL ON <table> FROM PUBLIC` — strips default PUBLIC privileges.
  - `USING (tenant_id = current_setting('app.tenant_id', true)::uuid)` — `missing_ok=true` returns NULL instead of raising an error when the GUC is unset, producing zero rows (fail-closed).
  - `WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid)` — constrains INSERTs and UPDATEs to the caller's tenant, not just reads.

- **Required integration test coverage** (three canonical cases in `test/integration/postgres/`):
  1. **Correct GUC**: query with `app.tenant_id = <tenant_A_id>` → rows for tenant A returned, tenant B rows invisible.
  2. **Wrong GUC**: query with `app.tenant_id = <tenant_B_id>` → zero rows for tenant A data (no error).
  3. **Missing GUC** (`missing_ok=true`): query without setting `app.tenant_id` → zero rows for any data (no error, fails closed).

### 10.4 Separation of Database Connection Roles (API vs. Relay)

- **Decision**: Force the API handlers to connect to PostgreSQL using a role configured with `BYPASSRLS=false`, while background processes (Outbox Relay, TTL clean-up crons) use a system role configured with `BYPASSRLS=true`.

- **Rationale**: Adheres to the principle of least privilege. The API handler's database role is strictly isolated and can never bypass RLS, reducing the impact of potential API security vulnerabilities. Background system processes need to query and partition rows across all tenants, which requires bypassing RLS to perform efficient unified batching.

### 10.5 Stateless Guarded-Loop and Topological Validation

- **Decision**: Perform graph validation statelessly during parser/compiler execution rather than relying on runtime interpretation failures. Back-edges are classified via DFS, and Tarjan's SCC enumerates loops so each can be checked for a guarded exit; the forward graph (edges minus back-edges) must remain a DAG.

- **Rationale**: Fail-fast validation. Catching unguarded loops, disconnected nodes, and malformed gateways during parsing or draft save stages prevents invalid configurations from entering the system, ensuring runtime engines (the Execution Service and Temporal) never encounter unexecutable topologies. Guarded rework loops are permitted because they always retain a structural exit; runtime termination is additionally protected by the Execution Service's loop-iteration bound.

### 10.6 RLS Violation Logging via `rls_check_tenant()`

- **Decision**: Replace the bare inline `USING (tenant_id = current_setting('app.tenant_id', true)::uuid)` RLS expression with a `SECURITY DEFINER` function `rls_check_tenant(row_tenant_id, table_name)` that samples violations at 1% and writes them to `rls_violation_log`.

- **Rationale**: PostgreSQL RLS has no native violation callback — a row that fails the `USING` clause is silently filtered, indistinguishable from an empty result set. Without the log table, a misconfigured pool connection (GUC not set), a cross-tenant bug, or a deliberate probe produces zero observable signal. The 1% sampling rate prevents log flooding under high-volume scans while still making systematic issues visible. A Prometheus alert on `rls_violation_log` row count `> 0` fires on any violation that makes it into the sample. See Appendix A for the DDL.

### 10.7 `workflow.template.published` Reference-Only Payload

- **Decision**: The `workflow.template.published` SNS event carries only workflow and version identifiers (`workflow_id`, `version_id`, `version_number`, `artifact_hash`). It does not embed the compiled plan JSON.

- **Rationale**: SNS enforces a hard 256 KB message size limit. A compiled plan for a workflow with many departments, stages, and assignees can approach this limit. Embedding the plan inline would create a silent operational risk that grows with workflow complexity and could cause publish to fail at the SNS dispatch stage after a successful DB commit — leaving the outbox row in a permanently failed state. Consumers that need the plan call `GetCompiledWorkflow` gRPC after receiving the event. This is a fast, cache-friendly read on the internal mesh and does not couple plan delivery to the SNS message size constraint.

### 10.8 SERIALIZABLE Publish Transaction — `RunInTxWithRetry`

- **Decision**: The publish transaction (`PublishVersion`) runs under `SERIALIZABLE` isolation and uses `pgcommon.RunInTxWithRetry` (or `RunInTxWithRetryOpts` with exponential backoff) to automatically retry on serialization failures (`SQLSTATE 40001`) and deadlocks (`SQLSTATE 40P01`).

- **Rationale**: `platform-pgcommon` v1.1.1 includes `RunInTxWithRetry` and `RunInTxWithRetryOpts` in `retry.go` — both retry automatically on `40001`/`40P01`. The service layer uses the `Transactor` port (`internal/core/port/transactor.go`) which currently only wraps `RunInTx`. To expose retry semantics, the port interface needs a `RunInTxWithRetry` method (or a `TxOptions` parameter that enables retry). The publish path is the only transaction in this service that warrants it — all other transactions do not use `SERIALIZABLE` isolation. In practice, concurrent publishes on the same version are rare (only one DRAFT per workflow), but SERIALIZABLE failures are non-deterministic and should not surface as 500 errors to the caller.

### 10.9 `processed_event` — No Tenant Scope on Operational Tables

- **Decision**: The `processed_event` table has no `tenant_id` column and no RLS policy. It is an operational/infrastructure table (SQS dedup log), not a tenant-scoped data table.

- **Rationale**: Consistent with the pattern established in `user_service.md` — operational tables that serve as infrastructure primitives (dedup logs, pruning targets) are not tenant-scoped. The dedup guard is the `(event_id, consumer)` composite primary key — a globally unique event ID scoped per consumer. There is no legitimate use case for querying `processed_event` filtered by tenant. The pruner runs by age (`processed_at < NOW() - INTERVAL '7 days'`) across all rows regardless of origin. Applying RLS to an operational table adds policy complexity with no security benefit.

### 10.10 `workflow_node_assignee` — Single-Column FK, No Composite Tenant Guard

- **Decision**: `workflow_node_assignee.workflow_version_id` references `workflow_version(id)` with a single-column foreign key. There is no `(workflow_version_id, tenant_id)` composite FK.

- **Rationale**: PostgreSQL composite FKs require both columns to be in the referenced table's unique constraint. Adding `(id, tenant_id)` as a unique constraint on `workflow_version` purely to enable a composite FK on the child table is mechanical overhead with marginal benefit — tenant isolation is already enforced by RLS (`WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid)`). An `INSERT` into `workflow_node_assignee` with a mismatched `tenant_id` is blocked by the RLS `WITH CHECK` before it could reference a row belonging to another tenant. A composite FK would add a second guard, but the primary risk vector (a service-layer bug passing the wrong tenant_id) is already closed by RLS.

### 10.11 Optimistic Lock Token — `If-Match` Header or `record_version` Body Field

- **Decision**: Mutating endpoints that enforce optimistic concurrency accept the lock token via either the `If-Match: "<record_version>"` HTTP header or the `record_version` field in the JSON body. The header takes precedence if both are present. Consistent with `user_service.md` §9.1.

- **Rationale**: The `If-Match` header is the RFC 7232 standard for conditional updates and is idiomatic REST — it keeps the concurrency token out of the business payload and is compatible with HTTP caches and standards-compliant clients. Supporting both the header and the body field matches the user_service pattern and avoids breaking existing clients that pass the token in the body. Handler extraction is a one-liner (`c.GetHeader("If-Match")`) before DTO binding; if non-empty and parseable as an integer, it overrides the body field value. Clients that omit both get last-writer-wins semantics (acceptable for single-admin tenants).

### 10.12 AWS Glue Schema Registry for Event Contracts

- **Decision**: Adopt AWS Glue Schema Registry to validate and encode outbound event payloads (`WorkflowTemplatePublished`, etc.) as JSON at runtime. Maintain a deferred decision to migrate the event payloads to Protobuf when gRPC transport is introduced.

- **Rationale**: Guarantees schema consistency and runtime validation of asynchronous events against design-time AsyncAPI contracts. By leveraging the Glue registry's native JSON Schema support, we achieve robust schema validation without the initial complexity of Protobuf compilation and distribution.

### 10.13 Scoped Tier-3 Amendment: `connector:`-Prefixed Service Tasks

- **Decision**: Allow `<bpmn:serviceTask>` to compile, but only when its `zeebe:taskDefinition type` carries a `connector:` prefix naming a connector the shared connector library implements (`workflow_connectors.md`). Every other `serviceTask` — any other `type`, or none — remains `REJECTED_ELEMENT`, unchanged. `scriptTask`/`businessRuleTask`/`transaction`/`adHocSubProcess` are untouched by this decision.

- **Rationale**: A small, fixed, developer-registered catalogue of automatic tasks (fetch-from-storage, LLM-verify, send-email, more later) needs a real BPMN element, not properties bolted onto `sendTask`/`receiveTask`, which were never designed to carry this meaning. `serviceTask` is the correct BPMN element for an automatic, non-human step; scoping the exception to a `connector:` prefix keeps the original Tier-3 rationale intact for what it was actually protecting against — arbitrary, end-user-authored scripts or business rules, which stay rejected. This is not a general reversal of the Tier-3 boundary, only a named, narrow hole in it. Camunda's own connector type identifiers and element templates are deliberately not adopted for this — their out-of-the-box connectors and engine require a paid Enterprise license for commercial/production use (`workflow_connectors.md` §7); this uses an original `connector:` naming convention and original template content instead.

### 10.14 Connector Registry Import Scope: `pkg/registry` vs `pkg/connectors`

- **Decision**: This service imports only `workflow-connectors`' `pkg/registry` package (connector type names, display metadata, JSON-schema-shaped input/output descriptions) for §4.1.3.3's compile-time `UNKNOWN_CONNECTOR_TYPE` check and for this service's own authoring-template generator (§10.15). It never imports `pkg/connectors` — the package holding the actual `Execute()` implementations and their AWS SDK / OpenAI client / DB driver dependencies.
- **Rationale**: This service's only real need is a name+schema lookup; pulling in every connector's runtime SDK dependency transitively for that would bloat this service's build/dependency graph and its blast radius for a lookup it could get from a package a fraction of the size. Full package layout in `workflow_connectors.md` §6.3.

### 10.15 Connector Authoring UI + Credential Custody Absorbed Into This Service

- **Decision**: This service owns the modeler-facing connector authoring experience end to end — serving the element-template form for each registered connector type (generated from `pkg/registry`, §10.14) and, at form-submission time, writing any provider credential (`storage`/`send-email`/`document-extract`/`chat-notify`) to OpenBao and returning only the resulting secret path for the compiled task's `IOMapping` (`workflow_connectors.md` §4.3/§6.2). This responsibility was previously assigned to BE-for-UI, a separate, not-yet-designed service; it moves here instead. ~~The boundary runs the other direction too: this service does **not** own BE-for-UI's "custom BPMN module"/"reusable authoring component" library — those are new, tenant-authored stored entities that stay in BE-for-UI's own database, never modeled into this service's schema (`execution_service.md` §1.3/Appendix A.2 #31).~~ **RESOLVED (rev 1.6) — reversed.** BE-for-UI is retired entirely (`execution_service.md` Appendix A.2 #31, rev 1.34); the module library is absorbed into this service's own schema instead, alongside connector authoring, not left in a now-nonexistent service's database. See §10.16.
- **Rationale**: Connector-authoring templates are fixed, code-generated catalogue metadata with no tenant-stored content of their own — not a new stored-entity type — so there's no data-ownership reason to keep them in a separate, undesigned service rather than the service that already owns design-time workflow authoring/compilation and already imports `pkg/registry` for a closely related compile-time check (§10.14). ~~"Custom BPMN modules"/"reusable authoring components," by contrast, genuinely are new, mutable, tenant-owned stored data needing their own CRUD/versioning/browse UI — a different kind of responsibility, correctly kept out of this service's schema by a standing decision (`execution_service.md` Appendix A.2 #31).~~ **See §10.16 for why this distinction no longer holds** — the module library turns out to need exactly the versioned-BPMN/CRUD/clone machinery this service already has, and moving it here closes a real gap in `Bundle()` rather than just relocating storage. This service's existing `<bpmn:callActivity>`/`module_bpmn_xmls`/`Bundle()` compile-time merge mechanism (§4.1.3.3, the `callActivity` documentation) is unaffected by either boundary — it still just merges whatever module XML a caller supplies; a persisted module store (§10.16) sits in front of that merge step, it doesn't change it.

### 10.16 Reusable Module & Starter-Workflow Library Absorbed Into This Service

- **Decision**: The "custom BPMN module"/"reusable authoring component" library, and a new "starter/predesigned workflow" library, are both modeled into this service's own schema (`workflow_module`/`workflow_module_version`/`workflow_starter_template`, §2.1, Appendix A) rather than a separate service's database. Both support a `scope` dimension — `global` (platform-authored, `tenant_id NULL`, visible to every tenant) and `tenant` (that tenant's own saved-for-reuse content, RLS-scoped) — per an explicit product requirement that tenants can maintain their own reusable modules/starters alongside platform-provided ones. Global-row writes are gated by the `platform_operator` role at the handler layer (mirroring Org & Membership's identical pattern for its own global department catalog, OP-1) and go through the same `BYPASSRLS` system-role connection the Outbox Relay already uses (§10.4) — the ordinary RLS-bound API role's `WITH CHECK` clause structurally cannot write a `scope='global'` row (§2.1's RLS DDL).
- **Rationale**: This service already owns the entire versioned-BPMN-content lifecycle a module/starter library needs — draft → publish → version → clone (§3.3.10), `record_version` optimistic locking, RLS — and is the *only* consumer of module content today via `<bpmn:callActivity>`/`Bundle()` (§4.1.3.3), which currently has no persisted store to draw from and requires the caller to re-supply full module XML on every compile. Colocating storage with its only real consumer closes that gap rather than relocating one, and reuses infrastructure (this service's Postgres instance, already shared with Execution Service via separate schemas per §4.1/§5.1's database-per-service exception) instead of standing up a new deployable, database, and RLS/migration/CI surface for what is structurally more of the same kind of content this service already stores. A starter is deliberately **not** given the same DRAFT/PUBLISHED/ARCHIVED version history a module gets (§2.1) — "using" a starter is nothing more than submitting its `bpmn_xml` to the ordinary `POST /workflows` a hand-authored upload would use (§3.3.19), so it needs no lifecycle machinery of its own beyond simple CRUD.
- **Open item, not resolved by this decision:** if the intended UI for browsing this content turns out to be CMS-like (cross-tenant search, tagging, thumbnails, marketplace-style discovery) rather than "just another versioned BPMN entity," that product shape could still justify pulling this back into a dedicated service later. Not pre-built for here — see Appendix E.

### 10.17 BPMN Element Allowlist Sourced From `workflow-models`

- **Decision**: The BPMN element allowlist this service's compiler enforces (§4.1.2) is sourced from a new shared export in `workflow-models` (`pkg/enums.AllowedBPMNElements`) rather than being defined only as inline Go logic in this service's own `bpmn_compiler` package. `GET /bpmn/allowed-elements` (§3.3.20) serves the same list to the frontend for palette configuration.
- **Rationale**: `workflow-models` (`platform-workflow-models`) is the shared module whose stated purpose is keeping concepts like this in sync across Definition and Execution — it did not yet carry the element allowlist (verified: as of this decision it only exported `pkg/dsl`/`pkg/enums`/`pkg/events`, no element-allowlist content), which is a gap relative to that stated purpose, not evidence the allowlist belongs somewhere else. Sourcing both this service's own enforcement and the new discovery endpoint from one shared, versioned list removes any drift risk between "what the compiler actually accepts" and "what the modeler UI's palette claims is allowed," without needing a new HTTP round-trip between the two backend services. Client-side palette restriction remains UX only — §4.1.2's server-side 422 rejection is the actual enforcement boundary regardless of what this endpoint reports (§3.3.20).

---

## 11. Deployment, Scaling & DR

### 11.1 Deployment & Scaling Configuration

- **Namespace**: `workflow-app`
- **Container ports**: `8080` (HTTP — REST/OpenAPI, `/healthz`, `/readyz`, `/metrics`) and `9090` (gRPC — `GetCompiledWorkflow`, called by the Execution Service).
- **Replica Configuration**: Base deployment of 2 replicas to ensure high availability across availability zones.
- **Horizontal Pod Autoscaling (HPA)**:
  - Target CPU utilization: 70%
  - Target Memory utilization: 80%
- **Resource Limits**:
  - Request: CPU 250m, Memory 512Mi
  - Limit: CPU 500m, Memory 1024Mi
- **AWS Glue Schema Registry Read IAM Permissions**: The service pod's IAM role requires `glue:GetRegistry`, `glue:GetSchema`, `glue:GetSchemaVersion`, and `glue:QuerySchemaVersionMetadata` on the `GLUE_REGISTRY_ARN` resource. Write permissions (registering schemas) are restricted to deployment pipeline roles.
- **Deployment mechanism**: `deploy/helm/` (Helm chart) is the source of truth for the values above — do not let this section drift out of sync with the chart. Includes NetworkPolicy, ServiceMonitor, and PrometheusRule (domain alerts sourced from §6.1.2) alongside the Deployment/Service/HPA/PDB.
- **Migrations**: there is no auto-migration at server boot (`cmd/server/main.go` special-cases a `migrate` subcommand). The chart runs `/server migrate` as a `pre-install,pre-upgrade` Helm hook Job before the main Deployment rolls out, so every `helm upgrade --install` applies pending migrations first.
- **Release pipeline**: `release.yml`'s `deploy-gate` job runs `helm upgrade --install --wait --atomic`, verifies the deployed image digest against the signed/published digest, waits for rollout, and gates on a 2-minute Prometheus error-rate check (`http_requests_total` 5xx ratio > 1%) — auto-rolling back via `helm rollback` on failure. A GitHub Release is only published if this gate passes.

### 11.2 Disaster Recovery Specifications

- **RTO (Recovery Time Objective)**: 15 minutes for Multi-AZ automatic failover; less than 1 hour for Cross-Region database recovery.
- **RPO (Recovery Point Objective)**: Less than 5 minutes for database backup transactions.
- **Database Backup & PITR**: RDS automated snapshots taken daily with a 30-day retention window, supporting Point-in-Time Recovery.
- **Outbox Relay Restart Behavior**: Following a database failover, the Outbox Relay worker restarts stateless loops, fetching pending rows from the `outbox_events` table where `published_at IS NULL`. Idempotency keys (`MessageDeduplicationId`) mapped on SNS ensure duplicate dispatches are handled safely at the receiving end.

#### Per-Component DR Posture

##### Valkey (ElastiCache)

Valkey is a pure operational cache — it holds no primary data and contributes nothing to the RPO. The service uses it for three purposes (§1.9):

- Compiled plan cache_ (`wf:plan:<tenantID>:<versionID>`, 1 h TTL): On failover or cold-start, the cache is empty. The first `GetCompiledWorkflow` gRPC call for each version triggers a DB fallback and repopulates the entry; subsequent calls are served from cache. No data is lost — the compiled plan is always re-derivable from `compiled_plan_json` in `workflow_version`.
- Idempotency keys_ (`idem:<tenantID>:<routePath>:<key>`, 24 h TTL): A cache outage opens a window where duplicate HTTP mutations from the same `Idempotency-Key` may be re-applied. The window is bounded by the cache downtime. For SQS-delivered events, `processed_event` deduplication (§7.4) remains intact because it lives in Postgres.
- Draft-edit locks_ (`wf:draft:lock:<tenantID>:<workflowID>`, 30 s sliding TTL): Locks expire naturally. After a cache failure, concurrent PUT /draft calls to the same draft may both succeed for up to 30 s, but the `record_version` optimistic-lock check in Postgres is the hard backstop — only one will commit.

ElastiCache Multi-AZ replication (with automatic failover) is the primary mitigation. RTO for the cache tier matches ElastiCache's automatic replica promotion, typically under 60 seconds.

##### Outbox and Event Durability

`outbox_events` rows are stored in Postgres and are included in the daily RDS snapshots and PITR window. Events in-flight at the moment of a failover are not lost: the `platform-events` outbox runner resumes from any undelivered rows (`published_at IS NULL`) on restart, providing at-least-once SNS delivery. The SNS FIFO `MessageDeduplicationId` (derived from `outbox_events.id`) ensures consumers receive each event exactly once even if the runner replays a batch.

##### No Stored Object URLs

`workflow_version.compiled_plan_json` is stored as JSONB directly in Postgres — there are no S3 presigned URL references embedded in any column. A region migration or cross-region recovery requires no rewrite of stored data; only the service configuration (`DATABASE_URL`, `VALKEY_ADDR`, `SNS_TOPIC_ARN`) needs updating.

---

## 12. Testing Strategy

All automated tests live under `test/` (black-box) or co-located `_test.go` files (white-box). The CI gate requires ≥ 95% unit coverage against business logic packages (see Makefile `COVER_EXCLUDE_PKG`). The `Iint` job covers repository adapters and integration scenarios with real containers; it runs in parallel with the unit-test gate on every PR.

| Layer | Approach | Key scenarios |
| --- | --- | --- |
| Unit — `core/service`, `core/domain` | Pure Go; ports mocked with GoMock (`make mock`) | BPMN sub-error codes fire on correct invalid input; plan quota enforcement (Starter 5, Pro 50, Enterprise unlimited — skips DB count); optimistic `record_version` conflict → `ErrDraftConcurrency`; `business_key` pattern `^[a-z0-9]+(-[a-z0-9]+)*$`; `buildEnvelope` attaches `WithTraceID` only when OTel span is valid |
| Unit — BPMN compiler (`internal/bpmn_compiler`) | Pure Go; no DB or network | All 24 sub-error codes fire; cycle detection (SCC); split/join gateway pairing; well-formed BPMN produces expected DSL shape; XXE entity and XML token bomb rejected; empty process → `NO_START_EVENT` |
| Unit — HTTP handlers (`test/unit/handler`) | Hand-rolled fakes; `gincommon.ProtectedMiddlewares` for request context | Each handler maps domain errors to correct HTTP status + RFC-9457 code; missing `x-tenant-id` → 401; non-admin role → 403; idempotency header absent → passes through; `record_version` absent on PUT /draft → 400 |
| Unit — SQS handler (`test/unit/sqshandler`) | Hand-rolled `fakeMembershipRevoker` + `fakeLogger` | `HandleMembershipRevoked` marks affected versions `is_valid=false`; silently skips version on `GetByID` error; `RecordIfNew` dedup — second delivery is no-op |
| Unit — outbound clients | Real local gRPC server (`net.Listen("tcp","127.0.0.1:0")`) / `httptest.NewServer` | `ExecutionClient` returns plan on success; `MembershipClient` returns eligibility; both return `ErrUpstreamUnavailable` on connection failure |
| Integration — repository (`test/integration/postgres`) | testcontainers real Postgres; `outbox.ApplySchema` + `migrate.Runner` applied | RLS: cross-tenant read with foreign GUC returns zero rows; `WITH CHECK` rejects INSERT with foreign `tenant_id`; single-draft partial-unique index blocks second DRAFT row; `record_version` trigger increments on every UPDATE; `statusOrNotFound` returns correct sentinel (absent vs wrong-status) |
| Integration — outbox atomicity | testcontainers Postgres | Business write + outbox row commit in same `RunInTx`; `Enqueue` called outside a transaction returns error |
| Integration — idempotency middleware | Real Valkey container | Cache hit replays stored response; body-hash mismatch → 409 `IDEMPOTENCY_KEY_REPLAY`; absent header passes through transparently; nil cache passes through transparently |
| Integration — draft concurrency | testcontainers Postgres | Two concurrent PUT /draft with same `record_version` → one wins 200, one gets `DRAFT_CONCURRENCY` 409 |
| Integration — gRPC `GetCompiledWorkflow` | Real local gRPC server + testcontainers Postgres | Returns compiled plan for PUBLISHED version; 404 for absent `version_id`; RLS GUC injected from request payload `tenant_id`; cached plan served on second call |
| Event correctness | Unit (`core/service`) | `event_type` is `workflow.template.published`; `published_by` present; `promoted_from_version_id` null on first publish, populated on promote; no outbox write on Clone or Archive; `PauseUserTasks` called with correct `tenantID`/`userID` on membership revocation |
| Security | Integration | DB role has `BYPASSRLS=false`; BPMN with XXE → 422; BPMN token bomb → 422; missing `x-tenant-id` → 401; cross-tenant GUC → zero rows; `RLSCrossTenantAccess` alert rule fires when counter increments |
| Smoke (post-deploy) | Scripted | `/healthz` 200; `/readyz` 200 with `db_utilization`; one `GET /workflows`; one gRPC `GetCompiledWorkflow` for a known PUBLISHED version |

### 12.1 CI Gates

| Gate | Job | Pass criterion |
| --- | --- | --- |
| Unit coverage | `coverage` | ≥ 95% line coverage against `./internal/...` excluding `postgres/`, `postgres/db/`, `port/mocks/` |
| Unit tests | `Test` | All `./internal/...` and `./test/unit/...` tests pass with `-race` |
| Integration tests | `Iint` | All `./test/integration/...` tests pass against a fresh testcontainers Postgres |
| RLS isolation | `Iint` | Cross-tenant read returns zero rows; `WITH CHECK` rejects foreign `tenant_id` |

---

## Appendix A: Database Migration Scripts

The database schema and policies are defined in SQL migration files using golang-migrate format (e.g., `db/migrations/0001_create_schema.up.sql`). Below is the combined DDL for all tables, constraints, indexes, and RLS policies.

> **Ownership split:** `outbox_events` and `outbox_dead_letters` are created by `platform-events outbox.ApplySchema` at startup (tracked under `outbox_migrations`). They are shown here for reference completeness. The service migration files (`db/migrations/`) only contain the RLS policies for those tables (`0005_outbox_rls.up.sql`) — not the table DDL itself.

```sql
CREATE TYPE workflow_version_status AS ENUM ('DRAFT', 'PUBLISHED', 'ARCHIVED');

-- Root table representing a unique workflow template type
CREATE TABLE workflow (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    created_by_user_id UUID NOT NULL,
    business_key VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    active_version_id UUID,
    record_version BIGINT NOT NULL DEFAULT 1 CHECK (record_version > 0),  -- optimistic-lock counter
    created_at TIMESTAMP DEFAULT now(),
    updated_at TIMESTAMP DEFAULT now(),
    UNIQUE (tenant_id, business_key)
);

ALTER TABLE workflow ADD CONSTRAINT fk_active_version
    FOREIGN KEY (active_version_id) REFERENCES workflow_version(id)
    ON DELETE SET NULL DEFERRABLE INITIALLY DEFERRED;

-- Version table representing drafts and published immutable iterations
CREATE TABLE workflow_version (
    id UUID PRIMARY KEY,
    workflow_id UUID REFERENCES workflow(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL,
    status workflow_version_status NOT NULL,
    bpmn_xml TEXT NOT NULL,
    compiled_plan_json JSONB,
    artifact_hash TEXT,
    version_number INT,   -- NULL on DRAFT rows; assigned atomically at publish time only
    published_at TIMESTAMP,
    created_by_user_id UUID NOT NULL,
    is_valid BOOLEAN NOT NULL DEFAULT true,
    validation_errors_json JSONB,
    record_version BIGINT NOT NULL DEFAULT 1,  -- monotonic optimistic-lock counter; incremented on every update
    created_at TIMESTAMP DEFAULT now(),
    updated_at TIMESTAMP DEFAULT now(),

    -- Drafts have no version number; only PUBLISHED/ARCHIVED rows must have one
    CONSTRAINT chk_version_number_on_publish
        CHECK (status = 'DRAFT' OR version_number IS NOT NULL)
);

-- Unique version numbers per workflow, only among published/archived rows
CREATE UNIQUE INDEX uq_workflow_version_published
    ON workflow_version(workflow_id, version_number)
    WHERE version_number IS NOT NULL;

-- Maps default user assignments of templates and drafts to support fast invalidation/reassignment lookup
CREATE TABLE workflow_node_assignee (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    workflow_version_id UUID REFERENCES workflow_version(id) ON DELETE CASCADE,
    node_key VARCHAR(255) NOT NULL,
    user_id UUID NOT NULL,
    department_id VARCHAR(255) NOT NULL,
    role VARCHAR(64) NOT NULL,
    created_at TIMESTAMP DEFAULT now()
);

-- Reusable BPMN modules (§3.3.18, §10.16) — absorbed from the retired "BE-for-UI"
-- placement. scope='global' rows are platform-authored and have tenant_id NULL;
-- scope='tenant' rows are tenant-authored and RLS-scoped normally. NOT the same
-- module-content table Bundle() reads at compile time in real time — Bundle()
-- still just merges whatever bpmn_xml a caller supplies (§4.1.3.3); this table
-- only adds a persisted, versioned, browsable store the frontend reads from
-- before supplying that content.
CREATE TYPE catalog_scope AS ENUM ('global', 'tenant');

CREATE TABLE workflow_module (
    id UUID PRIMARY KEY,
    tenant_id UUID,  -- NULL iff scope = 'global'
    scope catalog_scope NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    active_version_id UUID,
    created_by_user_id UUID,  -- NULL for platform-authored global modules
    record_version BIGINT NOT NULL DEFAULT 1 CHECK (record_version > 0),
    created_at TIMESTAMP DEFAULT now(),
    updated_at TIMESTAMP DEFAULT now(),

    CONSTRAINT chk_module_scope_tenant CHECK (
        (scope = 'global' AND tenant_id IS NULL) OR
        (scope = 'tenant' AND tenant_id IS NOT NULL)
    )
);

CREATE TABLE workflow_module_version (
    id UUID PRIMARY KEY,
    module_id UUID REFERENCES workflow_module(id) ON DELETE CASCADE,
    tenant_id UUID,  -- denormalized from workflow_module, NULL iff scope = 'global'
    scope catalog_scope NOT NULL,
    status workflow_version_status NOT NULL,
    bpmn_xml TEXT NOT NULL,
    process_id VARCHAR(255) NOT NULL,  -- the bpmn:process id inside bpmn_xml; matched against a referencing diagram's callActivity calledElement
    version_number INT,  -- NULL on DRAFT rows, same convention as workflow_version
    is_valid BOOLEAN NOT NULL DEFAULT true,
    validation_errors_json JSONB,
    published_at TIMESTAMP,
    created_by_user_id UUID,
    record_version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP DEFAULT now(),

    CONSTRAINT chk_module_version_number_on_publish
        CHECK (status = 'DRAFT' OR version_number IS NOT NULL)
);

ALTER TABLE workflow_module ADD CONSTRAINT fk_module_active_version
    FOREIGN KEY (active_version_id) REFERENCES workflow_module_version(id)
    ON DELETE SET NULL DEFERRABLE INITIALLY DEFERRED;

CREATE UNIQUE INDEX uq_module_version_published
    ON workflow_module_version(module_id, version_number)
    WHERE version_number IS NOT NULL;

-- Starter workflows (§3.3.19, §10.16) — a point-in-time BPMN XML snapshot, not
-- versioned and not compiled by this service; "using" one is nothing more than
-- submitting its bpmn_xml to the ordinary POST /workflows a hand-authored
-- upload would use, so no lifecycle machinery beyond simple CRUD is needed here.
CREATE TABLE workflow_starter_template (
    id UUID PRIMARY KEY,
    tenant_id UUID,  -- NULL iff scope = 'global'
    scope catalog_scope NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    category VARCHAR(64),
    bpmn_xml TEXT NOT NULL,
    source_workflow_version_id UUID,  -- informational only; no FK — the content is copied, not linked, so a later edit to the source workflow never changes an already-created starter
    created_by_user_id UUID,
    record_version BIGINT NOT NULL DEFAULT 1 CHECK (record_version > 0),
    created_at TIMESTAMP DEFAULT now(),
    updated_at TIMESTAMP DEFAULT now(),

    CONSTRAINT chk_starter_scope_tenant CHECK (
        (scope = 'global' AND tenant_id IS NULL) OR
        (scope = 'tenant' AND tenant_id IS NOT NULL)
    )
);

-- outbox_events and outbox_dead_letters are created by platform-events outbox.ApplySchema.
-- The service migration only adds RLS policies to these tables (0005_outbox_rls.up.sql).
CREATE TABLE outbox_events (
    id           UUID        PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    tenant_id    TEXT        NOT NULL DEFAULT '',
    trace_id     TEXT        NOT NULL DEFAULT '',
    attempts     INT         NOT NULL DEFAULT 0,
    last_error   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    scheduled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);

-- Table storing outbox events that failed to publish after max attempts
CREATE TABLE outbox_dead_letters (
    id         UUID        PRIMARY KEY,
    event_type TEXT        NOT NULL,
    payload    JSONB       NOT NULL,
    tenant_id  TEXT        NOT NULL DEFAULT '',
    trace_id   TEXT        NOT NULL DEFAULT '',
    attempts   INT         NOT NULL,
    last_error TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    failed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Log table representing processed SQS event ids to enforce idempotency
CREATE TABLE processed_event (
    event_id     UUID      NOT NULL,
    consumer     TEXT      NOT NULL,
    event_type   TEXT,
    processed_at TIMESTAMP NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, consumer)
);

-- BEFORE UPDATE Trigger: manages updated_at and increments record_version
-- The WHEN guard skips no-op updates (no WAL write, no spurious record_version bump)
CREATE OR REPLACE FUNCTION update_workflow_meta_column()
RETURNS TRIGGER AS $$
BEGIN
   NEW.updated_at = NOW();
   NEW.record_version = OLD.record_version + 1;
   RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION update_workflow_version_meta_column()
RETURNS TRIGGER AS $$
BEGIN
   NEW.updated_at = NOW();
   NEW.record_version = OLD.record_version + 1;
   RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_workflow_updated_at
BEFORE UPDATE ON workflow FOR EACH ROW
WHEN (OLD.* IS DISTINCT FROM NEW.*)
EXECUTE FUNCTION update_workflow_meta_column();

CREATE TRIGGER update_workflow_version_updated_at
BEFORE UPDATE ON workflow_version FOR EACH ROW
WHEN (OLD.* IS DISTINCT FROM NEW.*)
EXECUTE FUNCTION update_workflow_version_meta_column();

-- PostgreSQL Row-Level Security (RLS) Policies
-- Notes:
--   FORCE ROW LEVEL SECURITY: prevents table owner from bypassing RLS
--   REVOKE ALL FROM PUBLIC:   strips default public access
--   current_setting(..., true): missing_ok=true — unset GUC returns NULL instead of raising ERROR (fail-closed)
--   WITH CHECK:               constrains INSERTs and UPDATEs, not only SELECTs and DELETEs

REVOKE ALL ON workflow FROM PUBLIC;
ALTER TABLE workflow ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow
    FOR ALL
    USING      (rls_check_tenant(tenant_id, 'workflow'))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

REVOKE ALL ON workflow_version FROM PUBLIC;
ALTER TABLE workflow_version ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_version FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_version
    FOR ALL
    USING      (rls_check_tenant(tenant_id, 'workflow_version'))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

REVOKE ALL ON workflow_node_assignee FROM PUBLIC;
ALTER TABLE workflow_node_assignee ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_node_assignee FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_node_assignee
    FOR ALL
    USING      (rls_check_tenant(tenant_id, 'workflow_node_assignee'))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- workflow_module / workflow_module_version / workflow_starter_template use
-- rls_check_tenant_or_global (below), NOT the plain rls_check_tenant used
-- above — rls_check_tenant is declared STRICT, so passing it a NULL
-- tenant_id (every scope='global' row) would short-circuit to NULL, which a
-- USING clause treats as false, silently hiding every global row from every
-- tenant. This is exactly the kind of bug the STRICT/NULL interaction can
-- cause if the plain function is reused unmodified on a nullable-tenant-id
-- table — do not do that.
REVOKE ALL ON workflow_module FROM PUBLIC;
ALTER TABLE workflow_module ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_module FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_module
    FOR ALL
    USING      (rls_check_tenant_or_global(tenant_id, scope, 'workflow_module'))
    WITH CHECK (scope = 'tenant' AND tenant_id = current_setting('app.tenant_id', true)::uuid);
    -- WITH CHECK intentionally excludes scope='global': the RLS-bound API role can
    -- never write a global row through this policy. Global catalog writes go
    -- through the same BYPASSRLS system role the Outbox Relay already uses
    -- (§10.4), gated at the handler layer by the platform_operator role check
    -- (§10.16) — mirrors Org & Membership's identical global-department-catalog
    -- pattern (OP-1: "only platform_operator may create/modify global-catalog
    -- rows, enforced at the handler layer before any DB write").

REVOKE ALL ON workflow_module_version FROM PUBLIC;
ALTER TABLE workflow_module_version ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_module_version FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_module_version
    FOR ALL
    USING      (rls_check_tenant_or_global(tenant_id, scope, 'workflow_module_version'))
    WITH CHECK (scope = 'tenant' AND tenant_id = current_setting('app.tenant_id', true)::uuid);

REVOKE ALL ON workflow_starter_template FROM PUBLIC;
ALTER TABLE workflow_starter_template ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_starter_template FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_starter_template
    FOR ALL
    USING      (rls_check_tenant_or_global(tenant_id, scope, 'workflow_starter_template'))
    WITH CHECK (scope = 'tenant' AND tenant_id = current_setting('app.tenant_id', true)::uuid);

REVOKE ALL ON outbox_events FROM PUBLIC;
ALTER TABLE outbox_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON outbox_events
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

REVOKE ALL ON outbox_dead_letters FROM PUBLIC;
ALTER TABLE outbox_dead_letters ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox_dead_letters FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON outbox_dead_letters
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- Schema Indexes (Created Concurrently)
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX idx_workflow_name_trgm ON workflow USING gin (name gin_trgm_ops);
CREATE INDEX idx_workflow_tenant_id ON workflow(tenant_id);
CREATE INDEX idx_workflow_active_version ON workflow(active_version_id) WHERE active_version_id IS NOT NULL;
CREATE INDEX idx_wv_workflow_id ON workflow_version(workflow_id);
CREATE INDEX idx_wv_tenant_status ON workflow_version(tenant_id, status);
CREATE UNIQUE INDEX idx_wv_single_draft ON workflow_version(workflow_id) WHERE status = 'DRAFT';
CREATE INDEX idx_wv_artifact_hash ON workflow_version(workflow_id, artifact_hash) WHERE status = 'PUBLISHED';
CREATE INDEX idx_wnas_user_tenant ON workflow_node_assignee(user_id, tenant_id);
CREATE INDEX idx_wnas_version ON workflow_node_assignee(workflow_version_id);
CREATE INDEX idx_module_name_trgm ON workflow_module USING gin (name gin_trgm_ops);
CREATE INDEX idx_module_scope_tenant ON workflow_module(scope, tenant_id);
CREATE INDEX idx_module_active_version ON workflow_module(active_version_id) WHERE active_version_id IS NOT NULL;
CREATE INDEX idx_mv_module_id ON workflow_module_version(module_id);
CREATE UNIQUE INDEX idx_mv_single_draft ON workflow_module_version(module_id) WHERE status = 'DRAFT';
CREATE INDEX idx_starter_name_trgm ON workflow_starter_template USING gin (name gin_trgm_ops);
CREATE INDEX idx_starter_scope_tenant_category ON workflow_starter_template(scope, tenant_id, category);
CREATE INDEX idx_outbox_events_pending ON outbox_events (scheduled_at, id) WHERE published_at IS NULL;
CREATE INDEX idx_outbox_dead_letters_failed_at ON outbox_dead_letters (failed_at DESC);
CREATE INDEX idx_processed_event_processed_at ON processed_event(processed_at);

-- RLS violation audit log
-- Captures sampled cross-tenant or missing-GUC access attempts for security monitoring.
-- RLS is permanently DISABLED on this table to prevent recursion: rls_check_tenant() is
-- SECURITY DEFINER and writes here; if this table had an RLS policy the INSERT would
-- re-invoke the policy, causing infinite recursion.
CREATE TABLE rls_violation_log (
    id               BIGSERIAL   PRIMARY KEY,
    occurred_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    session_role     TEXT        NOT NULL DEFAULT session_user,
    violation_type   TEXT        NOT NULL,  -- 'missing_or_invalid_guc' | 'cross_tenant_access'
    app_tenant_id    UUID,                  -- value of app.tenant_id GUC (NULL if not set or malformed)
    row_tenant_id    UUID        NOT NULL,  -- tenant_id of the row that was blocked
    table_name       TEXT        NOT NULL,
    client_addr      INET        DEFAULT inet_client_addr(),
    application_name TEXT        DEFAULT current_setting('application_name', true),
    query_text       TEXT
);

ALTER TABLE rls_violation_log DISABLE ROW LEVEL SECURITY;

REVOKE ALL ON rls_violation_log FROM PUBLIC;
GRANT INSERT ON rls_violation_log TO definition_svc_app;
GRANT SELECT, INSERT, DELETE ON rls_violation_log TO definition_svc_migrator;

CREATE INDEX idx_rls_violation_log_time ON rls_violation_log (occurred_at);

-- Replaces the inline USING expression on all tenant-scoped tables.
-- Samples violations at 1% to prevent log flooding; normal queries pay only the UUID comparison.
CREATE OR REPLACE FUNCTION rls_check_tenant(row_tenant_id uuid, tbl text)
RETURNS boolean
LANGUAGE plpgsql
STRICT
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
    app_tenant uuid;
BEGIN
    BEGIN
        IF row_tenant_id = current_setting('app.tenant_id', true)::uuid THEN
            RETURN true;
        END IF;
    EXCEPTION WHEN others THEN
        NULL;
    END;

    BEGIN
        app_tenant := current_setting('app.tenant_id', true)::uuid;
    EXCEPTION WHEN others THEN
        app_tenant := NULL;
    END;

    IF random() < 0.01 THEN
        INSERT INTO rls_violation_log (violation_type, app_tenant_id, row_tenant_id, table_name, query_text)
        VALUES (
            CASE WHEN app_tenant IS NULL THEN 'missing_or_invalid_guc' ELSE 'cross_tenant_access' END,
            app_tenant,
            row_tenant_id,
            tbl,
            current_query()
        );
    END IF;

    RETURN false;
END;
$$;

-- Global-catalog-aware variant for workflow_module / workflow_module_version /
-- workflow_starter_template (§2.1, §10.16). Deliberately NOT STRICT: a
-- scope='global' row's row_tenant_id is NULL by construction (chk_*_scope_tenant),
-- and a STRICT function short-circuits to NULL — which a USING clause treats as
-- false — the instant any argument is NULL, silently hiding every global row
-- from every tenant. The scope check must run before any NULL-tenant comparison.
CREATE OR REPLACE FUNCTION rls_check_tenant_or_global(row_tenant_id uuid, row_scope catalog_scope, tbl text)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
    app_tenant uuid;
BEGIN
    IF row_scope = 'global' THEN
        RETURN true;
    END IF;

    BEGIN
        IF row_tenant_id = current_setting('app.tenant_id', true)::uuid THEN
            RETURN true;
        END IF;
    EXCEPTION WHEN others THEN
        NULL;
    END;

    BEGIN
        app_tenant := current_setting('app.tenant_id', true)::uuid;
    EXCEPTION WHEN others THEN
        app_tenant := NULL;
    END;

    IF random() < 0.01 THEN
        INSERT INTO rls_violation_log (violation_type, app_tenant_id, row_tenant_id, table_name, query_text)
        VALUES (
            CASE WHEN app_tenant IS NULL THEN 'missing_or_invalid_guc' ELSE 'cross_tenant_access' END,
            app_tenant,
            row_tenant_id,
            tbl,
            current_query()
        );
    END IF;

    RETURN false;
END;
$$;

```

---

---

## Appendix B: Coding Style & Formatting Rules

This section defines the official coding style and formatting standards for all backend Go services in the platform. These rules are designed to ensure codebase consistency, high maintainability, clean telemetry integration, and robust testing patterns.

### B.1 Code Layout & Formatting

#### B.1.1 Line Length

- **Limit**: Maximum **100 characters** per line.
- Long lines (e.g., function signatures, struct instantiations, string concatenations) must be wrapped readable when they exceed this limit.

#### B.1.2 File Length & Splitting

- **Limit**: There is no hard limit on the number of lines per file.
- **Rule**: Split files when they become **structurally unwieldy** (e.g., when a file spans too many unrelated domains, contains a mix of core models and adapter logic, or makes navigation difficult).
- Keep core business entities separated from concrete delivery adapters (enforced by Clean Architecture).

#### B.1.3 Import Grouping

Imports must be grouped into exactly **three blocks**, separated by a single empty line, in the following order:

1. **Standard Library**: Packages from Go's standard library (e.g., `context`, `errors`, `fmt`).
2. **Third-Party Libraries**: External dependencies (e.g., `github.com/gin-gonic/gin`, `github.com/google/uuid`).
3. **Internal Packages**: Local project packages (e.g., `github.com/org/workflow-service/internal/core/domain`).

Example:

```go
import (
 "context"
 "fmt"
 "time"

 "github.com/google/uuid"
 "go.uber.org/multierr"

 "github.com/org/workflow-service/internal/core/domain"
 "github.com/org/workflow-service/internal/core/port"
)
```

---

### B.2 Function & Method Design

#### B.2.1 Function Complexity & Length

- **Cognitive Complexity**: Keep cognitive complexity **below 15**. Developers should install and run the **SonarQube** linting plugin within their IDE to check complexity before committing.
- **Length**: No strict line-of-code (LOC) limit is enforced if a function's logic inherently requires it, but functions exceeding 80 lines should be evaluated for potential decomposition into smaller helpers.

#### B.2.2 Method Grouping & Ordering

- **Grouping**: All methods belonging to a specific struct must be grouped consecutively within the same Go file.
- **Ordering**: Sort methods either **alphabetically** or by **visibility** (public methods listed first, followed by the private helper methods they invoke).

---

### B.3 Naming Conventions

All symbols must adhere to **strict, idiomatic Go naming conventions**:

- **camelCase**: Used for unexported (private) struct fields, local variables, and private functions/methods (e.g., `userID`, `workflowDef`, `activeDraft`).
- **PascalCase**: Used for exported (public) structs, interfaces, fields, and functions/methods (e.g., `WorkflowRepository`, `TemplatePublished`, `GetCompiledWorkflow`).
- **Initialisms & Acronyms**: Must be fully capitalized to maintain readability (e.g., `JSON`, `XML`, `URL`, `ID`, `UUID`, `BPMN`, `RLS`, `GUC`, `mTLS`, `SNS`, `SQS`, `AST`, `DSL`).
  - *Correct*: `userID`, `workflowXML`, `GetWorkflowByID`
  - *Incorrect*: `userId`, `workflowXml`, `GetWorkflowById`

---

### B.4 Comments & Documentation

The codebase prioritizes **self-documenting code** over extensive commenting.

- **Minimize Comments**: Do not write comments unless they are absolutely necessary. Rely on descriptive variable names, clean control flows, and visible, self-explanatory logic.
- **Documenting the Obscure**: If a code block is obscure or relies on specific low-level behavior (e.g., complex graph operations, database locking hints, or reflection), write a brief, high-level comment explaining *why* it is written this way.
- **References**: For complex background logic, include brief references pointing to the corresponding architectural designs or external documentation inside the comment block.

---

### B.5 Error Handling & Structured Logging

#### B.5.1 Structured Logging (`zap`)

- **Framework**: Use the Zap logger (`go.uber.org/zap`) for structured, structured logging.
- **Metric Integration**: All error scenarios must emit structured logs to ensure easy consumption by telemetry and monitoring tools (e.g., Prometheus, Grafana).
- **Attribute Binding**: Do not format variable contexts into string messages. Pass them as structured fields:
  - *Correct*: `logger.Error("failed to publish workflow draft", zap.Error(err), zap.String("tenant_id", tenantID))`
  - *Incorrect*: `logger.Error(fmt.Sprintf("failed to publish workflow draft for tenant %s: %v", tenantID, err))`

#### B.5.2 Error Formatting & Context

- **Wrapping**: Wrap errors with meaningful context as they traverse boundary layers using `fmt.Errorf("context: %w", err)`.
- **Sentinel Errors**: Define static sentinel errors at the package or service level (e.g., `var ErrWorkflowNotFound = errors.New("workflow not found")`) to allow callers to verify failures using `errors.Is`.

---

### B.6 Unit Testing & Mocking

#### B.6.1 Test File Organization

- **Unit Tests**: Place unit tests in standard `*_test.go` files adjacent to the source code being tested.
- **Integration Tests**: Isolate integration tests (e.g., testing database drivers, live SQS listeners) into files matching `*_integration_test.go` and protect them using build tags to keep normal unit tests fast:

  ```go
  //go:build integration
  
  package postgres_test
  ```

#### B.6.2 Table-Driven Testing

- **Pattern**: Always write unit tests as **table-driven tests**. Define test cases within a slice of anonymous structs to handle happy-path and failure scenarios uniformly.

Example:

```go
func TestValidateKey(t *testing.T) {
 tests := []struct {
  name    string
  key     string
  wantErr bool
 }{
  {
   name:    "valid key",
   key:     "tender-review",
   wantErr: false,
  },
  {
   name:    "invalid characters",
   key:     "tender/review",
   wantErr: true,
  },
 }

 for _, tt := range tests {
  t.Run(tt.name, func(t *testing.T) {
   err := ValidateKey(tt.key)
   if (err != nil) != tt.wantErr {
    t.Errorf("ValidateKey() error = %v, wantErr %v", err, tt.wantErr)
   }
  })
 }
}
```

#### B.6.3 Mocking Conventions

- **Mocks**: Use **third-party mock generators** (e.g., Mockery, GoMock) to automatically construct mock implementations of repository and outbound client interfaces (`core/port/*`).
- Avoid writing mock implementations manually to minimize boilerplate and prevent mock maintenance drift.

### B.7 Commit Styles & Pull Request Guidelines

#### B.7.1 Commit Messages (Conventional Commits)

All commits must follow the **Conventional Commits** specification. The format is:

```text
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

**Common Types:**

- `feat`: A new feature for the user.
- `fix`: A bug fix.
- `docs`: Documentation-only changes.
- `style`: Formatting, white-space, missing semi-colons, etc. (no behavior changes).
- `refactor`: A code change that neither fixes a bug nor adds a feature.
- `perf`: A code change that improves performance.
- `test`: Adding missing tests or correcting existing tests.
- `build`: Changes that affect the build system or external dependencies.
- `ci`: Changes to CI configuration files and scripts.
- `chore`: General maintenance tasks that do not modify source or test files.
- `revert`: Reverting a previous commit.

**Rules:**

- **Imperative Mood**: Use the imperative present tense in the description (e.g., "add feature" instead of "added feature" or "adds feature").
- **Character Limit**: Keep the subject line (first line) under **50 characters** if possible, and never exceed **72 characters**.
- **Body & Footer**: Separate the subject from the body with a blank line. Use the body to explain *what* was changed and *why*. Reference issue tracker keys in the footer (e.g., `Resolves: #123`).
- **Keep Commits Small**: Each commit should represent a single logical change. Avoid monolithic commits that mix refactoring, feature work, and formatting.

**Examples:**

- `feat(auth): add Keycloak support for RLS context`
- `fix(parser): handle XML bomb with custom TokenReader`
- `docs(lld): add detailed PR and commit guidelines`

#### B.7.2 Branch Naming Conventions

Branch names must align with the conventional commit type and follow a consistent structure:
`<type>/<short-description-or-ticket>`

- **Feature branches**: `feat/add-temporal-outbox` or `feat/WF-402-bpmn-validation`
- **Bug fixes**: `fix/db-rls-leak` or `fix/WF-409-visibility-timeout`
- **Refactoring**: `refactor/simplify-tarjan-scc`
- **Documentation**: `docs/update-security-standards`

#### B.7.3 Pull Request (PR) Guidelines

To maintain code quality and delivery speed, every pull request must adhere to the following standards:

- **Size Constraint**: PRs should be small, targeting **under 300 lines of code changes** (excluding auto-generated code like mocks or schema definitions). If a task requires larger changes, break it down into incremental, stackable pull requests.
- **Description Requirements**: Every PR description must include:
  - **Summary**: A concise explanation of the implementation.
  - **Ticket Link**: A reference to the issue tracker (e.g., `Closes #123`).
  - **Verification**: A description of how the change was tested (both automated tests and manual runs).
  - **Migration & Operational Impact**: Explicit highlights if a database migration or configuration change is included.
- **Merge Requirements**:
  - Must receive at least **one approved peer review**.
  - All automated checks (linting, unit tests, integration tests) must pass.

---

## Appendix C: Glossary

This section provides definitions for key terms, concepts, and acronyms used throughout the Definition Service LLD.

### Core Concepts

| Term | Category | Definition |
| --- | --- | --- |
| **Tenant** | Core Concept | The top-level organizational boundary. All workflows, versions, and assignments are strictly isolated by tenant ID. Multi-tenancy is enforced at the gateway (headers), database (RLS), and orchestrator (task queues) layers. |
| **Workflow Template** | Core Concept | A versioned blueprint of a business process defining routing logic, department sequencing, and task assignment criteria. Each template is identified by a unique `business_key` within a tenant. |
| **Workflow Version** | Core Concept | An immutable iteration of a workflow template. Versions are either `DRAFT` (editable), `PUBLISHED` (active for new instances), or `ARCHIVED` (no longer startable). Only one draft may exist per workflow at any time. |
| **Workflow Instance** | Core Concept | A live, durable execution of a template linked to a specific business entity (e.g., tender ID). Instances are managed by the Execution Service and Temporal, not the Definition Service. |
| **Node** | Core Concept | A single step in a workflow, represented as a `<bpmn:userTask>` in the BPMN XML. Each node is bound to a Department (from its lane membership), Stage Type (from `zeebe:taskDefinition type`), and Role Level (from `zeebe:assignmentDefinition candidateGroups`). |
| **Stage** | Core Concept | A logical grouping of one or more nodes within a department, representing a phase of work (e.g., `prep`, `review`, `approve`). Stages are ordered topologically based on sequence flows. |
| **Department** | Core Concept | A logical organizational unit (e.g., Design, Procurement, Legal) represented as a `<bpmn:lane>` in BPMN. Departments execute in parallel or sequentially based on gateway routing. |
| **Execution DSL** | Core Concept | The high-performance JSON representation of a BPMN diagram compiled by the Definition Service. The DSL is consumed by the Execution Service and Temporal Workers for orchestration. Stored in `workflow_version.compiled_plan_json`. |
| **Business Key** | Core Concept | An external, human-readable identifier (e.g., `tender-review`, `procurement-check`) used to uniquely identify a workflow template within a tenant. Must be unique per tenant. |
| **Default Assignee** | Core Concept | A user ID pre-assigned to a node at template publish time, declared via `<zeebe:assignmentDefinition candidateUsers="..."/>`. Currently limited to one UUID v7 per node; multi-user support is planned. When a workflow instance reaches that node, this default is used unless overridden at runtime. Stored in `workflow_node_assignee` for fast invalidation lookup. Full concurrency semantics are defined in §4.5. |
| **Side-by-Side Versioning** | Core Concept | The ability to run multiple versions of the same workflow template concurrently. Existing instances continue on their original version while new instances start on the latest published version. Enables safe template updates without disrupting active work. |

### Architecture & Design Patterns

| Term | Category | Definition |
| --- | --- | --- |
| **Identity Agnostic** | Architecture | The principle where the Definition Service stores only UUID references to users and departments, without managing or validating user profiles. Identity validation is delegated to the Org & Membership Service. |
| **Transactional Outbox Pattern** | Architecture | A reliability pattern that prevents dual-write inconsistencies by persisting domain events to the database in the same transaction as state changes. A background worker (`OutboxRelay`) asynchronously publishes events to SNS, guaranteeing at-least-once delivery. |
| **Triple-Layer Isolation** | Architecture | Multi-tenancy enforcement across three layers: (1) Gateway headers (`x-tenant-id`), (2) Database RLS policies, and (3) Orchestrator task queues (tenant-isolated or shared). Provides defense-in-depth against cross-tenant data leaks. |
| **Clean Architecture** | Architecture | Layered design where core business logic (`core/domain`, `core/port`, `core/service`) is decoupled from concrete adapters (`adapter/inbound/*`, `adapter/outbound/*`). Enforced by `go-arch-lint` import rules. |
| **Structural Divergence** | Architecture | A breaking change in workflow topology (e.g., adding/removing gateways, changing sequence flows, introducing new departments) that could break active execution instances. Detected at publish time; requires admin override or new workflow key. |
| **Optimistic Locking** | Architecture | Concurrency control mechanism using a version column (`updated_at`) on draft records. Prevents lost updates when multiple admins edit the same draft simultaneously. |

### Security & Data Protection

| Term | Category | Definition |
| --- | --- | --- |
| **Row-Level Security (RLS)** | Security | A PostgreSQL feature that restricts data access based on the `app.tenant_id` session variable. All queries are automatically filtered at the database layer, preventing cross-tenant data leaks regardless of application-layer bugs. |
| **Grand Unified Configuration (GUC)** | Security | PostgreSQL session variables (e.g., `app.tenant_id`) used to pass validated tenant context to RLS policies. Set via `SET LOCAL app.tenant_id = $1` at the start of each transaction. |
| **BYPASSRLS** | Security | A PostgreSQL role permission that allows bypassing RLS policies. API handlers use roles with `BYPASSRLS=false` for strict isolation; background processes use roles with `BYPASSRLS=true` for efficient cross-tenant batching. |
| **XXE (XML External Entity)** | Security | An XML injection attack exploiting external entity resolution. Prevented by disabling entity resolution in the XML decoder and enforcing token count limits. |
| **XML Bomb** | Security | A denial-of-service attack using nested entity expansion (e.g., "Billion Laughs"). Prevented by a custom `TokenReader` wrapper that aborts if token count exceeds 1,000,000. |

### Database & Persistence

| Term | Category | Definition |
| --- | --- | --- |
| **Artifact Hash** | Database | A canonical SHA-256 hash of normalized BPMN XML, used for change detection and diff comparisons. Computed at publish time and stored in `workflow_version.artifact_hash`. |
| **Outbox Table** | Database | A transactional event queue (`outbox`) storing domain events awaiting delivery to SNS. Events are inserted in the same transaction as state changes, then asynchronously published by the `OutboxRelay` worker. |
| **Processed Event Table** | Database | A deduplication log (`processed_event`) tracking successfully processed SQS event IDs. Enables idempotent consumption of inbound membership revocation events. |
| **Workflow Node Assignee Table** | Database | A denormalized reverse index (`workflow_node_assignee`) mapping users to workflow versions. Enables fast lookup of affected templates when a user loses department membership. Populated atomically during publish; never updated independently. |
| **sqlc** | Database | A Go code generator that produces type-safe database query functions from hand-written SQL. Eliminates runtime SQL errors and provides compile-time type checking. |
| **migrate.Runner** | Database | `platform-pgcommon/pkg/migrate.Runner` — a golang-migrate wrapper that applies service schema migrations (`db/migrations/NNNN_name.up.sql` / `.down.sql`) at process startup, tracked under `wf_definition_migrations`. |

### Validation & Compilation

| Term | Category | Definition |
| --- | --- | --- |
| **BPMN Parser** | Compilation | The component that unmarshals raw BPMN 2.0 XML into Go memory structures (`bpmn.Definitions`, `bpmn.Process`). Extracts custom Zeebe properties from `<bpmn:extensionElements>`. |
| **Graph Compiler** | Compilation | The component that traverses the parsed BPMN graph, resolves split-to-join gateways, groups sequential tasks, and generates the hierarchical JSON DSL. Produces `compiled_plan_json`. |
| **Validation Engine** | Compilation | The component that performs structural checks (cycles, reachability, dangling nodes), metadata validation (required properties, enum values), and semantic rules (orphan lanes, max depth). Returns a structured multi-error slice. |
| **Forward Graph / Back-edge** | Compilation | A sequence flow is a *back-edge* when its target is an ancestor on the DFS stack from the Start Event (a revert/loop flow). The *forward graph* (all edges minus back-edges) must be a DAG; reachability, gateway matching, and traversal run over it. See §4.4. |
| **Guarded Loop** | Compilation | A structural cycle whose back-edge originates at a diverging exclusive gateway that also has a forward (exit) branch, so the loop can always terminate. Guarded loops are valid; unguarded cycles are rejected (`CYCLE_DETECTED` / `UNGUARDED_LOOP`). |
| **Tarjan's SCC Algorithm** | Compilation | An efficient graph algorithm for detecting strongly connected components (cycles). Used to enumerate the loops in a definition so each can be checked for a guarded exit (§4.4). |
| **Zeebe Extension Elements** | Compilation | Standard Camunda 8 extension elements attached to BPMN user tasks inside `<bpmn:extensionElements>`. Required: `<zeebe:taskDefinition type="..."/>` (stage type, registry-based) and `<zeebe:assignmentDefinition candidateGroups="..." candidateUsers="..."/>` (role + default assignee). Optional: any number of `<zeebe:property name="..." value="..."/>` elements, forwarded verbatim to `StageDef.Extras` (e.g. `requires_comment`, `sla_category`) — no property name is special-cased by the compiler. Department is not a Zeebe property — it is derived from the task's lane membership via `<bpmn:flowNodeRef>`. |

### Eventing & Integration

| Term | Category | Definition |
| --- | --- | --- |
| **`workflow.template.published` Event** | Eventing | The sole outbound SNS event from the Definition Service. Emitted when a draft is promoted to `PUBLISHED` status. Acts as a cache-warm push hint: the Execution Service pre-fetches the compiled plan via `GetCompiledWorkflow` gRPC on receipt. Published to SNS topic `wf.template.events`. |
| **department.membership.revoked Event** | Eventing | An inbound event from the Org & Membership Service indicating a user has been removed from a department. Triggers template invalidation if the user is a default assignee. Consumed from SQS queue `membership-wf-q` by the shared workflow-events consumer, which forwards it to the Definition Service via `POST /internal/events`. |
| **Shared Workflow-Events Consumer** | Eventing | A separate engine-wide service that consumes the SQS queues (e.g. `membership-wf-q`) and HTTP-routes each envelope to the respective workflow service (Definition or Execution). Keeps the Definition Service API-only on the inbound path. |
| **Outbox Relay Worker** | Eventing | A background daemon that polls the `outbox` table every 500ms, publishes pending events to SNS, and marks them as `SENT`. Implements exponential backoff and max retry limits (5 retries). |
| **SNS Topic** | Eventing | Amazon Simple Notification Service topic (`wf.template.events`) used for publishing workflow lifecycle events. Enables loose coupling between the Definition Service and downstream consumers. |
| **SQS Queue** | Eventing | Amazon Simple Queue Service queue (`membership-wf-q`) subscribed to the `iam.membership.events` SNS topic. Provides durable, ordered delivery of membership revocation events to the shared workflow-events consumer (which then forwards them to the Definition Service over HTTP). |

### Observability & Monitoring

| Term | Category | Definition |
| --- | --- | --- |
| **Prometheus Metrics** | Observability | Time-series metrics (counters, histograms, gauges) exposed via `/metrics` endpoint. Labeled with `tenant_id` to prevent noisy neighbor issues and enable strict SLAs. |
| **OpenTelemetry (OTel)** | Observability | Distributed tracing framework used to propagate execution context across service boundaries. Trace context is injected into HTTP headers (`traceparent`) and SNS message attributes. |
| **Structured Logging (Zap)** | Observability | JSON-formatted logging using Go's `uber-go/zap` library. All logs include correlated context fields (`tenant_id`, `user_id`, `trace_id`, `workflow_id`) for easy filtering in Loki. |
| **Correlation ID** | Observability | A unique identifier (UUID) assigned to each request and propagated across all downstream calls. Enables end-to-end tracing of a single user action through multiple services. |
| **Audit Log** | Observability | Structured security logs capturing all denied operations, validation failures, and unauthorized access attempts. Includes full audit lineage (who, what, when, why) for compliance. |

### API & Contracts

| Term | Category | Definition |
| --- | --- | --- |
| **RFC-9457 Problem Details** | API | A standardized error response format (formerly RFC-7807) providing machine-readable error information. Includes `type`, `title`, `status`, `detail`, `instance`, and `code` fields. |
| **Idempotency Key** | API | A UUID header (`Idempotency-Key`) used to prevent duplicate processing of mutating requests. Cached in Redis; requests with the same key and different payloads are rejected. |
| **Plan Quota** | API | A subscription-tier limit on the number of workflow templates a tenant can create (e.g., Starter: 5, Pro: 50, Enterprise: Unlimited). Enforced at template creation time. |
| **gRPC Service** | API | A high-performance internal API (`DefinitionService/GetCompiledWorkflow`) used by the Execution Service and Temporal Workers to fetch compiled workflow plans. Uses mTLS for security. |
| **Payload Limit** | API | A hard cap (10 MB) on the size of incoming HTTP request bodies. Enforced by `http.MaxBytesReader` middleware to prevent resource exhaustion. |

### Temporal & Orchestration

| Term | Category | Definition |
| --- | --- | --- |
| **Temporal Cluster** | Infrastructure | The durable orchestration engine managing workflow state, retries, and signal delivery. Provides strong consistency guarantees and automatic recovery from failures. |
| **Temporal Worker** | Infrastructure | Specialized Go services that execute workflow logic and database activities. Register activity handlers (e.g., `PrepActivity`, `ReviewActivity`) and listen on task queues. |
| **Temporal Signal** | Infrastructure | An external asynchronous request sent to a running workflow to trigger a state transition (e.g., `stage-transition`, `stage-defer`, `admin-route`). Enables reactive, event-driven orchestration. |
| **Temporal Activity** | Infrastructure | A unit of work within a workflow that can have side effects and automatic retries. Used to create tasks, update assignments, and persist state changes. |
| **Task Queue** | Infrastructure | A Temporal queue name where workers listen for activities. Can be tenant-isolated (e.g., `wf-queue-<tenant_uuid>`) or shared (e.g., `wf-queue-default`) based on plan tier. |
| **Workflow Type** | Infrastructure | The registered name of a Temporal workflow (e.g., `DSLWorkflow`). Workers must register this exact name to execute instances. |

### Compliance & Governance

| Term | Category | Definition |
| --- | --- | --- |
| **Audit Trail** | Compliance | A complete, immutable record of all state-changing operations (create, publish, archive, clone, promote). Includes actor, timestamp, and action details. Enables regulatory compliance and forensic investigation. |
| **Data Retention** | Compliance | Outbox events are retained for 7 days post-processing; processed event logs are purged after 7 days (matching SQS retention). Enables replay and debugging while preventing unbounded storage growth. |
| **Encryption at Rest** | Compliance | BPMN XML and compiled DSL are encrypted at the AWS RDS layer. No field-level encryption is applied; RLS and database-level encryption provide sufficient protection. |
| **Tenant Isolation** | Compliance | Strict enforcement of tenant boundaries at three layers (gateway, database, orchestrator). Prevents any cross-tenant data leaks or unauthorized access. |

---

## Appendix D: Docs Infrastructure

Developer documentation is served locally via **MkDocs** (installed via `brew install mkdocs`). No pip, Docker, or third-party extensions are used — only extensions bundled with the Homebrew MkDocs package.

| Item | Value |
| --- | --- |
| Tool | `mkdocs` (Homebrew) |
| Theme | `readthedocs` (built-in) |
| Extensions | `admonition`, `tables`, `fenced_code`, `toc` (all standard) |
| Config | `mkdocs.yml` (project root, committed) |
| Source | `docs/` (committed) |
| Build output | `site/` (gitignored; regenerated via `make docs-build`) |
| Local URL | <http://localhost:8001> (`make docs-serve`) |

**Pages in `docs/`**: `index.md`, `setup.md`, `architecture.md`, `configuration.md`, `standards.md`, `testing.md`, `api.md`, `grpc.md`, `database.md`.

### Changelog

| Version | Date | Notes |
| --- | --- | --- |
| 0.1 | May 2026 | Initial draft from HLD |
| 0.2 | Jun 2026 | Renamed `xml` → `bpmn_xml` in POST /workflows and PUT /workflows/:id/draft; renamed `valid` → `is_valid` in validate response; added `idx_workflow_name_trgm` trigram index and `pg_trgm` extension for fuzzy name search; narrowed search filter to name only; expanded `is_valid`, `status`, and `has_draft` query param docs with exact SQL semantics; fixed invalidation query to filter by `department_id` to prevent over-invalidation; added 204 response to DELETE /draft |
| 0.3 | Jun 2026 | Service layer, all 16 HTTP handlers, gRPC `GetCompiledWorkflow`, SQS `department.membership.revoked` consumer, `TemplateCloned` event, `TemplateEligibilityInvalidated` payload aligned with §7.2.3; `InjectGUCSet` RLS bridge, `LimitRequestBody` 10 MB cap, `TimeoutMiddleware`, `RequirePermission` added to §1.6.1 chain; plan quota enforcement, idempotency SHA-256 body hash; `record_version` added to `workflow` and `workflow_version` tables (Appendix A); `TemplatePublished` switched to reference-only payload (§7.2.1, §10.7); `rls_violation_log` + `rls_check_tenant()` (Appendix A, §10.6); dependency failure matrix (§9.2); PII classification (§8.6); `platform-pgcommon` v1.0.0 → v1.1.1, `platform-events` v1.0.0 → v1.2.0, `platform-gincommon` v1.0.0 → v1.2.0; pgmetrics, gRPC health service, processed_event pruner, OTel trace propagation via `WithTraceID`; replaced goose with `platform-pgcommon migrate.Runner` + `platform-events outbox.ApplySchema` for programmatic startup migrations (GAP-7, §1.8.1, §2.6); UUID v7 enforced for all application-level ID generation (GAP-4); OpenAPI spec fixes — `required: xml` → `bpmn_xml`, 413 responses added to POST /workflows and PUT /draft (GAP-5); `db.dbml` synced — `record_version` on both tables, `processed_event` at canonical composite-PK schema (GAP-6 documented); Appendix A ownership split noted for outbox tables; PostgreSQL upgraded to 18-alpine; `schema_version` field — `events.WithSchemaVersion("1")` available in v1.2.0 |
| 0.4 | Jun 2026 |BPMN and collaboration model overhaul: Collaboration BPMN promoted to Tier 1 with support for collaboration, participant, messageFlow, bpmn:message, sendTask, receiveTask, inclusiveGateway, message/timer/signal start events, and CompiledCollaboration linking plans via MessageDef. BPMN extension contract migrated from custom Zeebe properties to standard Camunda 8 constructs: stage type via <zeebe:taskDefinition>, assignment via <zeebe:assignmentDefinition>, department derived from lanes, SLA modelled exclusively through timer boundary events. XOR semantics clarified: sequence-flow conditions perform routing only; rejection/rework handled through error boundary events. Event topology simplified to a single workflow.template.published event; archive, clone, and eligibility invalidation events removed, with user-task pausing handled through direct gRPC calls. Compiler correctness fixes include consultant-pool modelling, message-flow plan resolution using element-to-process mapping, correct traversal of non-terminal XOR branches, compilation of timer-boundary-only paths, and collaboration-wide department ID namespacing using planName/deptId. Validation catalog updated with new BPMN-specific error codes and removal of obsolete property-validation errors. Documentation, AsyncAPI, glossary, validation rules, tier tables, activity metadata, and BNB collaboration examples updated throughout.|
| 0.5 | Jul 2026 | Message boundary events promoted from Tier-3-rejected to Tier-1-supported on `userTask`, `subProcess`, and `callActivity` — compile to `StageDef.BoundaryMessage`, `SubWorkflowStep.MessagePaths`, and `ExecutionStep.MessagePaths` respectively (new `MessagePath` DSL type); error boundary attachment corrected to allow `callActivity` (attachment-valid, still compile-rejected, same as timer). `requires_comment` de-special-cased: removed from `StageDef` as a dedicated field and the `userTask` boolean-parse validation removed — every `zeebe:property` now forwards verbatim to `StageDef.Extras` with no compiler special-casing. Added message-flow name resolution fallback (`ResolveMessageFlowName`/`ResolveMessageFlowTarget`): resolves via the flow's own name/messageRef, then the connected send/receive task or boundary event's own messageRef — real diagrams rarely annotate the messageFlow element itself; a new `MISSING_MESSAGE_DEFINITION` warning flags collaboration message flows whose name resolves to empty. Fixed a callActivity `zeebe:ioMapping` `Depts` dict-remap bug where `DepartmentDef.ID` was remapped but the called process's own compiled `ExecutionStep` dept references were not, producing steps that pointed at a department absent from the plan. Fixed two validation-severity bugs: `MISSING_NAMESPACE`/`MULTIPLE_PROCESSES` errors now carry `Severity: error` (previously unset, serialized as `""`); `POST /workflows/validate`'s `is_valid` now only goes `false` on a `Severity: error` issue, not on warnings-only. §4.1.6 Go Struct Mapping expanded with previously-undocumented `bpmnCollaboration`, `bpmnParticipant`, `bpmnMessageFlow`, `bpmnMessage`, `bpmnCallActivity`, `bpmnTaskDefinition`, `bpmnAssignmentDefinition`, `bpmnCalledElement`, and `bpmnMessageEventDef` types.|
| 0.6 | Jul 2026 | §11 finalized (STC tag removed): documented `deploy/helm/` chart (dual HTTP:8080/gRPC:9090 container ports, `migrate-job` pre-install/pre-upgrade Helm hook running `/server migrate` since there is no auto-migration at server boot, NetworkPolicy/ServiceMonitor/PrometheusRule with domain alerts sourced from §6.1.2) and `release.yml`'s `deploy-gate` job (`helm upgrade --install --atomic`, deployed-digest verification, rollout wait, 2-minute Prometheus error-rate gate with `helm rollback` on failure). Local git hooks (`.githooks/pre-commit`: `make tidy`/`fmt-check`/`lint`/`arch-lint`, installed via `make setup`/`make install-hooks`) added to the service repo — dev-workflow only, not reflected in this LLD. |
| 0.7 | Aug 2026 | Automatic connector tasks (new `automatic_connector_tasks.md`, a standalone cross-service design) — this document's own footprint. **§4.1.2**: `<bpmn:serviceTask>` moves from a blanket Tier-3 rejection to a scoped exception — compiles when `zeebe:taskDefinition type` carries a `connector:` prefix, still `REJECTED_ELEMENT` otherwise. **§4.1.3.3** (new): the Service Task/connector extension contract — reuses `zeebe:ioMapping` as-is, no `zeebe:assignmentDefinition`. **§4.1.4**: `serviceTask` removed from the flat `rejectedElements` denylist map in favor of a conditional, element-level check, documented alongside the existing `messageEventDefinition`/`signalEventDefinition` precedent for element-level (not blanket) handling. **§10.13** (new): the design decision and rationale, including why Camunda's own connector definitions/templates are not adopted (their license requires a paid Enterprise tier for commercial/production use). |
| 0.8 | Aug 2026 | Connector worker/catalogue design deepened into new `workflow_connectors.md` (worker runtime + full v1 catalogue of 7 connector types); this document's footprint tightened to match. **§4.1.2/§4.1.3.3**: name the concrete `workflow-models` fields now — `StageDef.Type = enums.StageTypeConnector`, `StageDef.ConnectorType = "<name>"`, `StageDef.IOMapping` (all new fields, `workflow_models_lib.md` §2.3/§4.1) — replacing the earlier vaguer "compiles like a task-producing node" phrasing. Compile-time registry citation moved from `automatic_connector_tasks.md` §6 to `workflow-connectors`' `pkg/registry` package specifically. Noted the `rest-call`/`sql-query` alias-input convention (no new compiler validation — a dangling alias degrades the same as an unrecognized connector type). **§10.14** (new): why this service imports only `pkg/registry`, never `pkg/connectors`. |
| 0.9 | Aug 2026 | `automatic_connector_tasks.md` consolidated into `workflow_connectors.md`; the former deleted. Every citation into it throughout §4.1.2/§4.1.3/§4.1.4/§10.13/§10.14 repointed at `workflow_connectors.md`'s own (renumbered) sections — no compiler behavior changed, citation-only pass. |
| 1.0 | Aug 2026 | **§3.1**: `x-departments`'s format documented explicitly for the first time — comma-separated `<department-uuid>:<role>` pairs, with a worked example. Previously untyped ("Comma-separated user department memberships," no example, no stated whether department was named by code or UUID). Cross-team note added flagging that IAM's own `workflow-service-integration-sync.md`/item B4 in `iam-lld-org-membership-v1_6.md` currently document a different format (bare UUIDs, no role) — a follow-up for IAM's document owner, not resolved here. **§4.1.3.3**: the connector-catalogue example list and the alias-resolution citation synced against `workflow_connectors.md` rev 4.0 — `llm-verify` removed (dropped from the v1 catalogue), and alias resolution now cites `cmd/connector-worker`'s own internal-service registry rather than "the owning domain service," which no longer runs any connector code. |
| 1.1 | Aug 2026 | IAM's org-membership LLD was renamed (`iam-lld-org-membership-v1_6.md` → `org_membership_lld_v12.md`, now rev 1.73) — §3.1's cross-team note repointed at the current filename. Sharpened the same note: that revision now cites an "AuthZ Enrichment LLD" ("AE-11") and an IAM HLD v1.41 supposedly finalizing the bare-UUID format, neither of which exists (`iam-hld.md` is still v1.38; no such LLD file exists in the design repo) — an inconsistency inside IAM's own docs, not a reason to reconsider `dept_uuid:role` here. New **§8.2** bullet: `x-departments`'s format stated in the Authentication & Authorization section directly, not just §3.1's header table — this service doesn't currently gate anything on it, documented for completeness. |
| 1.2 | Aug 2026 | **§3.1's cross-team note trimmed** — the AE-11/HLD-v1.41 inconsistency detail now lives in a separate cross-team tracking note, not repeated in this LLD; the note here now just flags that IAM documents a different format and points elsewhere for the detail. `dept_uuid:role` remains this document's format, unaffected. |
| 1.3 | 2026-08-12 | Citation-only pass syncing against `workflow_connectors.md` rev 6.0, which made connector tasks fully automation-only (no human fallback, ever). **§4.1.2**: dropped the stale "worker placement is deliberately still open" parenthetical (already fixed once placement was decided) — now says placement is decided, this compiler's output needed no change either way. **§4.1.3.3**: an unrecognized connector-type name's compile-time leniency no longer justified by "the runtime already degrades this safely to a plain manual task" — re-derived as "might still get registered before the workflow is ever instantiated; runtime settles it as a real, visible failure now, not a compiler concern." A dangling `endpointAlias`/`queryAlias` now "fails the workflow" the same way, not "degrades." The "no `assignmentDefinition`" note no longer says "who (if anyone) completes it manually is a runtime decision" — no one ever does, by design. No compiler behavior changed anywhere in this pass. |
| 1.4 | 2026-08-13 | IAM sync check against the newly-updated `iam_1.41.md` (a real HLD v1.41, unlike the citation checked in rev 1.1 that referenced a v1.41 which didn't exist yet). **§3.1**: `x-departments`'s cross-team note rewritten — IAM's HLD now explicitly documents the same `<department_id>:<role_level>` format this document already used, resolving the discrepancy at the HLD level; only IAM's own org-membership LLD reportedly still lags, tracked in `Notes/conf.md`, not here. No other discrepancy found against IAM's current HLD/LLDs this pass. |
| 1.5 | 2026-08-13 | BE-for-UI/build-vs-absorb architecture review: this service absorbs connector-authoring templates and credential custody, previously assigned to BE-for-UI, a separate not-yet-designed service (new **§10.15**; §10.14 updated to match). New `/connectors/registry` (serve element-templates) and `/connectors/credentials` (write a provider credential to OpenBao, return its secret path) endpoints added to §3.2. Explicitly does **not** absorb BE-for-UI's "custom BPMN module"/"reusable authoring component" library — that stays in BE-for-UI's own database (`execution_service.md` §1.3/Appendix A.2 #31); this service's existing `callActivity`/`module_bpmn_xmls`/`Bundle()` compile-time merge mechanism (§4.1.3.3) is unchanged. |
| 1.6 | 2026-08-21 | **BE-for-UI retired entirely** (`execution_service.md` Appendix A.2 #31, rev 1.34) — reverses rev 1.5's exclusion. This service absorbs the "custom BPMN module"/"reusable authoring component" library and a new starter/predesigned-workflow library, both with a `global`/`tenant` scope dimension (new **§10.16**; §10.15 updated to match, no longer excludes the module library). New tables `workflow_module`/`workflow_module_version`/`workflow_starter_template` (§2.1, Appendix A), a global-catalog-aware RLS function `rls_check_tenant_or_global` (Appendix A — the plain `rls_check_tenant` is `STRICT` and would silently hide every global row given a NULL `tenant_id`, so it isn't reused unmodified), and new endpoints `/modules`, `/modules/:id`, `/modules/:id/versions[/:version_id/publish]`, `/starters`, `/starters/:id`, `/starters/from-workflow-version/:version_id` (§3.2, §3.3.18, §3.3.19). Separately, new **§10.17**: the BPMN element allowlist (§4.1.2) is now sourced from a new shared export in `workflow-models` rather than only inline compiler logic, with a matching discovery endpoint `GET /bpmn/allowed-elements` (§3.3.20) for the frontend's modeler palette — UX convenience only, §4.1.2's server-side 422 remains the actual enforcement. New Appendix E (Open Items — this document's first, "Appendix C" already names the Glossary): the module/starter library's CMS-like-UI uncertainty (§10.16), and a pre-existing, independently-found gap where the shipped `WriteConnectorCredential` handler performs no role check despite §3.2 documenting `/connectors/credentials` as Admin-only. |
| 1.7 | 2026-08-21 | **§7.4.2**: `department.membership.revoked` is now also documented in `api/asyncapi.yaml` as a `receive` operation (message `DepartmentMembershipRevoked`, schema `DepartmentMembershipRevokedInbound`, non-`Payload`-suffixed since Org & Membership owns registration) — this service's asyncapi coverage was previously outbound-only, mirroring the gap `execution_service.md` §7.4 already closed for its own inbound events. Code-side: `internal/adapter/inbound/http/asyncapi.go`'s render-order lists updated to surface it, and a pre-existing, independently-found bug fixed in the same pass — that handler's `Type` field couldn't unmarshal the multi-type YAML idiom (`["string", "null"]`) `promoted_from_version_id` already used, meaning `GET /asyncapi` had likely been erroring already, unrelated to this change. |

---

## Appendix E: Open Items

This document had no dedicated Open Items tracker before rev 1.6 — items below are the first entries, following the same `Theme | Item | Owner` convention `execution_service.md`'s own Appendix B already uses.

| Theme | Item | Owner |
| --- | --- | --- |
| Deferred | **Module/starter library's product shape (§10.16).** Designed here as "just another versioned BPMN entity" (module) or a simple CRUD row (starter). If the intended UI turns out to be CMS-like — cross-tenant search, tagging, thumbnails, marketplace-style discovery across a large catalog — that product shape could justify pulling this back into a dedicated service later, independent of the data-ownership reasoning that put it here. Not pre-built for; revisit once real product/UI requirements exist. | Definition Service team + product, once UI requirements are known |
| Security | **`POST /connectors/credentials` has no role check despite being documented Admin-only (§3.2, `workflow_connectors.md` §4.3/§6.2).** Independently found while researching this document: the shipped `WriteConnectorCredential` handler and `connector_service.go` extract tenant context (`mustCtx`) but perform no role/permission check at all — today any authenticated tenant member, not just an admin, can write a provider credential to OpenBao. Unrelated to the BE-for-UI retirement (rev 1.6) that surfaced it; a pre-existing gap in already-shipped code. | Definition Service team |
| Cross-team | **Global-catalog write path for `workflow_module`/`workflow_starter_template` (§10.16) needs its `platform_operator`-gated handler + `BYPASSRLS`-role wiring actually built** — the RLS DDL (§2.1, Appendix A) already structurally prevents the ordinary API role from writing a `scope='global'` row; the privileged write path itself (mirroring Org & Membership's OP-1 pattern) is designed but not yet implemented. | Definition Service team |
