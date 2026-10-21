# workflow-definition-service

The design-time control plane of the BCBP Workflow Engine: it parses, validates, versions and serves BPMN workflow templates.

**Repository:** `github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service`  
**Module:** Go 1.26.10 · private module · a containerised microservice not yet deployed to any live environment

---

## Mental model

The engine has two halves. This service is the design half: it owns every workflow template and the compiled plan that results from it. The Execution Service is the runtime half: it reads compiled plans from here and drives live instances.

```text
Frontend Builder ──REST──► Definition Service ◄──internal HTTP──► Execution Service
                                    │
                             PostgreSQL + Valkey
```

| Layer | What it owns | What it does NOT own |
| --- | --- | --- |
| **BPMN ingestion** | Accepting BPMN 2.0 XML from the canvas modeler | Drawing the diagram: the frontend builder owns the modeler |
| **Validation** | Structural, semantic and topological (DAG, cycle) checks | Runtime data validation: the Execution Service checks the variables of a live instance |
| **Compilation** | Turning a validated graph into an immutable JSON DSL plan (`workflow-models` `pkg/dsl`) | The DSL types themselves: `workflow-models` owns them |
| **Versioning** | The `DRAFT → PUBLISHED → ARCHIVED` lifecycle with optimistic concurrency, modules and starters | Running instances of a version: the Execution Service owns them |
| **Serving** | The compiled plan and the published-version nodes read, on the internal HTTP API | Task assignment and completion at runtime |
| **Connectors** | The connector registry read and the tenant's stored credentials (written to OpenBao) | Running a connector: the Execution Service's connector worker does |
| **Membership sync** | Invalidating versions whose default assignee had a membership revoked | The membership itself: Org & Membership owns it |

**Source of truth for two contracts:** the compiled plan (`GET /api/v1/internal/workflow-versions/{version_id}/compiled-plan`) and the published-version nodes read, which lists the nodes callers bind work to.

---

## Why this service exists

A workflow is authored once and run many times, often long after the author is done. Separating design from execution gives a published version that never changes under a running instance, a validation gate that runs before any instance can start, and a runtime that never parses BPMN. Without a single owner of templates, every consumer would parse and interpret the diagram on its own and drift from the others.

This service centralises that: one validated, versioned, compiled plan per published version, served read-only to the runtime. It publishes no events and keeps no outbox; its only event path is the inbound membership revocation.

---

## Contents

- [Quick start](#quick-start) and [Local Development](#local-development)
- [Project layout](#project-layout) and [Architecture](#architecture)
- [API Overview](#api-overview)
- [Deployment](#deployment), [Observability](#observability), [Security](#security), [Workflow Design Guide](#workflow-design-guide)
- [Tech stack](#tech-stack), [Testing](#testing), [CI](#ci), [Versioning and releases](#versioning-and-releases)
- [Database](#database), [Database connection pooling](#database-connection-pooling)
- [Out of scope](#out-of-scope) and [See also](#see-also)

> **Architecture** — [ARCHITECTURE.md](ARCHITECTURE.md): layer model, sequence diagrams, BPMN compiler reference, configuration reference, and error catalog. Diagram sources live in [docs/architecture/](docs/architecture/).
>
> **API specs** — REST: [docs/swagger/openapi.yaml](docs/swagger/openapi.yaml), served locally at `/swagger/*any` in dev mode. Events: [api/asyncapi.yaml](api/asyncapi.yaml). Internal (service-to-service): [api/internal-openapi.yaml](api/internal-openapi.yaml).
>
> **Contributing** — [CONTRIBUTING.md](CONTRIBUTING.md) for the local dev workflow, testing, and PR checklist.

---

## Private Module Access

This service consumes private Go modules from the `github.com/BCBP-SOLUTIONS-FZC-LLC/*` organization (such as `platform-events`, `platform-pgcommon`, and `platform-gincommon`).

Before running Go commands or compiling the app, configure Go to bypass the public proxy and checksum database:

```bash
go env -w GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*
```

### GitHub Authentication

Configure Git to authenticate against GitHub to fetch the private packages:

**SSH key (recommended for local dev):**

```bash
git config --global url."ssh://git@github.com/".insteadOf "https://github.com/"
```

**Personal Access Token (for CI/CD or HTTPS):**
Add a classic/fine-grained PAT with read access to the credential store:

```bash
git config --global credential.helper store
echo "https://x-access-token:<your-github-token>@github.com" > ~/.git-credentials
chmod 600 ~/.git-credentials
```

---

## Quick start

```bash
# 1. Configure Go private module path
go env -w GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*

# 2. Install dev tooling
make tools

# 3. Configure environment and install the local pre-commit hook
make setup

# 4. Start local infra (PostgreSQL 18 + Valkey 8)
make docker-up

# 5. Apply schema migrations — the server does NOT migrate at boot
make migrate

# 6. Start the server
go run ./cmd/server
```

The server is ready when you see:

```sh
INFO  HTTP server starting         {"addr": ":8080"}
```

Verify:

```bash
curl http://localhost:8080/healthz   # {"status":"OK"}
curl http://localhost:8080/readyz    # {"status":"OK","cache":"ok",...}
curl http://localhost:9091/metrics   # Prometheus exposition (metrics port)
```

---

## Local Development

### Full stack (end-to-end)

To run the service against the local stubs and the smoke test:

```bash
# 1. Start everything (Postgres + Valkey + PgBouncer) and apply the schema
make docker-up
make migrate

# 2. Start outbound service stubs (HTTP)
go run ./cmd/stub/execution &    # :8083 (internal API + /control toggle)
go run ./cmd/stub/membership &   # :8081 (HTTP + /control toggle)
go run ./cmd/stub/catalog &      # :8084 (the plan table)

# 3. Run the server against the stubs
ORG_MEMBERSHIP_BASE_URL=http://localhost:8081 CATALOG_ADMIN_BASE_URL=http://localhost:8084 EXECUTION_SERVICE_URL=http://localhost:8083 \
  go run ./cmd/server &

# 4. Run the smoke test
./scripts/smoke-test.sh
```

The stub binaries expose a runtime control plane to toggle their responses:

```bash
# Toggle membership eligibility
curl -X POST http://localhost:8081/control -d '{"eligible":true}'
curl -X POST http://localhost:8081/control -d '{"eligible":false}'

# Toggle active instances on execution service
curl -X POST http://localhost:8083/control -d '{"has_active":false}'
curl -X POST http://localhost:8083/control -d '{"has_active":true}'
```

### Environment variables and Make

The Makefile automatically loads `.env` if the file exists, so variables like `DATABASE_URL` are available to all targets without manually sourcing the file first:

```bash
cp .env.example .env   # do this once
make test-integration  # DATABASE_URL etc. are read automatically
```

Variables in `.env` override any existing shell environment values for the duration of the make process only.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the code generation and testing workflow.

---

## Make targets

```sh
make help
```

| Target | Description |
| --- | --- |
| `make setup` | Copy `.env.example` → `.env` and install the local `.githooks/pre-commit` hook (run once) |
| `make install-hooks` | Reinstall the pre-commit hook after `.githooks/pre-commit` changes |
| `make tools` | Install sqlc, oasdiff, mockgen, golangci-lint, go-arch-lint into `.tools/` |
| `make tools-integration` | Pre-pull Docker images used by integration tests (testcontainers-go) |
| `make generate` | sqlc generate (queries) |
| `make mock` | Regenerate GoMock stubs for `core/port` interfaces |
| `make build` | Compile binary to `bin/server` |
| `make migrate` | Apply schema migrations and exit — not run at server boot |
| `make test` | Unit tests with race detector and coverage (internal + test/unit) |
| `make test-integration` | Integration tests (spins up Docker containers via testcontainers-go automatically; requires running Docker) |
| `make cover` | Unit tests + coverage summary |
| `make cover-html` | Open HTML coverage report |
| `make cover-check` | Fail if coverage < 95% (postgres adapter + generated pkgs excluded) |
| `make helm-check` | Lint the chart, render it with production settings, and require a prod render with no service addresses to fail |
| `make contract-check` / `make contract-breaking` | Compare the checked-in copies of Execution's internal contracts with their source and fail on a breaking change to this service's own specs |
| `make design-sync-check` / `make design-sync-check-test` | Fail when a document kept both here and in the design repository differs (`DESIGN_DIR` reads a local checkout, otherwise `GH_TOKEN` reads `Haroon-BCBP/design`); the test runs the script against throwaway directories |
| `make fmt-check` / `make lint` / `make arch-lint` | Formatting, lint, and Clean Architecture import-direction checks (read-only) |
| `make fix` | Auto-fix formatting and lint issues |
| `make check` | Full local CI pass: fmt + lint + vet + arch-lint + metrics-lint + contract-breaking + design-sync-check-test + tests + coverage gate |
| `make vuln` | Run govulncheck for known dependency vulnerabilities |
| `make metrics-lint` | Observability standard: collector naming, `metricslint` on a real scrape, alert and doc references, inventory drift |
| `make metrics-inventory` | Regenerate `docs/observability/metric-registry.md` |
| `make docker-build` / `make docker-lint` / `make docker-trivy` | Build the container image, lint the Dockerfile, scan for CVEs |
| `make docker-up` | Start PostgreSQL + Valkey + PgBouncer |
| `make docker-down` | Stop infra |
| `make docker-reset` | Drop the local volumes and recreate the infra (re-runs `scripts/init-db.sql`, which creates the app and migrator roles) |
| `make clean` | Remove `bin/`, `gen/`, coverage, mock outputs |

---

## Project layout

```sh
cmd/server/              ← bootstrap + DI wire-up
internal/
  core/
    domain/              ← entities, enums, error sentinels
    port/                ← interface contracts (no impls)
    service/              ← business logic
  adapter/
    inbound/
      http/              ← Gin handlers, authz, middleware, POST /api/v1/internal/events
    outbound/
      postgres/          ← sqlc DB layer + repo adapters
      valkey/            ← Valkey cache adapter
  bpmn_compiler/         ← XML parser, validator, DSL compiler
  config/                ← env var loading
api/                     ← internal OpenAPI, AsyncAPI, and contracts/ (the Execution Service's internal spec, copied)
db/
  migrations/            ← golang-migrate SQL migrations (.up.sql/.down.sql, committed)
  queries/               ← sqlc query definitions (committed)
docs/
  architecture/          ← Mermaid diagram sources for ARCHITECTURE.md
  swagger/               ← OpenAPI spec served by the dev-mode Swagger UI
  observability/         ← generated metric inventory (make metrics-inventory) and the alert runbooks
  lld/                   ← in-repo copy of the design repo's LLD, kept content-identical
  bpmn-designer-guide.md ← BPMN modelling guide for business analysts / process owners
  ui-enrichment-guide.md ← Compiled-plan enrichment guide for ops/engineering reviewers
```

---

## Architecture

Clean Architecture: `domain ← port ← service ← adapter`. Handlers map every response through local DTO structs and never serialise a domain struct directly.

### Dependency rules (enforced by `go-arch-lint` in CI)

| Package | May import |
| --- | --- |
| `core/domain` | the standard library only |
| `core/port` | `domain` |
| `core/service` | `domain`, `port` |
| `adapter/*` | `port`, `domain` |
| `cmd/server` | everything; it holds the composition root and no business logic |

Nothing under `core/` imports from `adapter/`.

### Shared library dependencies

| Library | Used for |
| --- | --- |
| `platform-gincommon` | All logs, metrics and traces: Zap logger, OTel tracing, the `platform_http_*` series, the `/metrics` handler, authorization middleware |
| `platform-pgcommon/v2` | The pgx pool, RLS GUC injection, the transactor and `migrate.Runner`; the `platform_db_*` series |
| `platform-events/v2` | The inbound event envelope type only; the publisher, outbox and consumer are not used |
| `workflow-models` | The compiled-plan DSL and stage-type constants shared with the Execution Service |
| `workflow-connectors` | The connector registry read and the validator's connector-task checks |

---

## API Overview

### REST API

Full OpenAPI schema: [`docs/swagger/openapi.yaml`](docs/swagger/openapi.yaml).

**Global headers.** All requests through the Envoy gateway carry these headers (injected post-JWT verification):

| Header | Required | Description |
| --- | --- | --- |
| `x-tenant-id` | Yes | Tenant UUID — enforces RLS isolation |
| `x-user-id` | Yes | Executing user UUID |
| `x-tenant-roles` | Yes | Comma-separated roles, e.g. `tenant_admin,member` |
| `x-departments` | No | User department memberships |
| `x-plan` | For workflow create, clone, and promote of a version on a workflow with no active version | The tenant's plan code (`starter`, `pro`, `enterprise`), set by the authorization layer. The template limit is looked up by it in the plan table read from the catalog; a missing or unknown code answers `503` |
| `x-request-id` | No | Client correlation ID — echoed as `X-Request-ID` |
| `traceparent` | No | W3C trace context — echoed as `X-Trace-ID` |

Validated by `gincommon.RequireAuth`. Handlers access them via:

```go
rctx := gincommon.RequestContext(c)
// rctx.TenantID, rctx.UserID, rctx.Roles, rctx.TraceID
```

**Endpoint registry**

| Method | Path | Auth | Description |
| --- | --- | --- | --- |
| `GET` | `/api/v1/workflows` | Any | List tenant workflows |
| `POST` | `/api/v1/workflows` | Admin | Create workflow + initial draft |
| `GET` | `/api/v1/workflows/:id` | Any | Workflow detail + version list |
| `GET` | `/api/v1/workflows/:id/versions` | Any | Paginated version history |
| `GET` | `/api/v1/workflows/:id/versions/:version_id` | Any | Version detail (XML + compiled plan) |
| `GET` | `/api/v1/workflows/:id/draft` | Any | Active draft detail |
| `POST` | `/api/v1/workflows/:id/draft` | Admin | Init draft from active version |
| `PUT` | `/api/v1/workflows/:id/draft` | Admin | Update draft XML/name/description |
| `DELETE` | `/api/v1/workflows/:id/draft` | Admin | Discard draft |
| `POST` | `/api/v1/workflows/:id/versions/:version_id/publish` | Admin | Compile & publish draft |
| `POST` | `/api/v1/workflows/:id/versions/:version_id/clone` | Admin | Clone to new workflow key |
| `POST` | `/api/v1/workflows/:id/versions/:version_id/promote` | Admin | Rollback/rollforward active pointer |
| `POST` | `/api/v1/workflows/:id/archive` | Admin | Archive workflow |
| `POST` | `/api/v1/workflows/validate` | Any | Stateless BPMN validation |
| `GET` | `/api/v1/workflows/:id/versions/:version_id/nodes` | Any | A published version's human tasks, module tasks included: node key, department, role level, default users |
| `GET` | `/api/v1/workflows/:id/versions/:version_id/export` | Any | Download raw BPMN XML |
| `GET` | `/api/v1/workflows/:id/versions/:version_id/diff/:target_version_id` | Any | Structural diff between two versions |
| `GET` | `/healthz` | Public | Liveness probe |
| `GET` | `/readyz` | Public | Readiness probe (database; a Valkey outage is reported as `cache: degraded`, not unready) |
| `GET` | `/metrics` | Metrics port `9091` | Prometheus metrics |
| `POST` | `/api/v1/internal/events` | Internal | Ingest a domain-event envelope from the shared workflow-events consumer (`DepartmentMembershipRevoked`, `MembershipRevoked`, and the tenant offboarding events `TenantMembershipsPurged` and `TenantStateChanged`) |

**Admin** = requires `tenant_admin` or `tenant_owner` in `x-tenant-roles`. **Internal** = service-to-service only; not exposed on the public gateway. Authenticated with `x-internal-token` (`INTERNAL_API_TOKEN`, required outside dev); the handler sets the RLS tenant from the envelope `tenant_id`. Returns 2xx (incl. idempotent no-op), 400 (malformed — non-retryable), or 500 (transient — retried).

**Optimistic concurrency on draft update.** `PUT /api/v1/workflows/:id/draft` accepts a `record_version` (the token returned on `GET /draft` and version responses). The token is required, in the `record_version` body field or an `If-Match` header (the header wins); a request with neither is `400 BAD_REQUEST`. A stale value yields `409 DRAFT_CONCURRENCY` carrying `current_record_version`. `record_version` is bumped by the DB on every real change. The `PUT /draft`, `POST /workflows`, and clone responses include a `message` field; create/clone also echo the new identifiers.

**Error format (RFC-9457):**

```json
{
  "type": "https://api.workflow.platform/errors/conflict",
  "title": "Draft Already Exists",
  "status": 409,
  "detail": "An active draft already exists for this workflow.",
  "instance": "/api/v1/workflows/abc/draft",
  "code": "DRAFT_ALREADY_EXISTS",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "request_id": "7d1c5c0e-6a52-4a39-9f0a-2f6f0b0c1a11"
}
```

`type` is chosen by the HTTP status (a 409 is always `.../conflict`); `code` is the stable machine-readable value. `trace_id` and `request_id` match the `X-Trace-ID` and `X-Request-ID` response headers. A stale draft save also carries `current_record_version`. Public request bodies are strict: an unknown field is a `400 BAD_REQUEST` that names the field in `invalid_params`, and so is a second JSON value after the first.

Validation errors include an `invalid_params` array:

```json
{
  "type": "https://api.workflow.platform/errors/validation-failed",
  "title": "BPMN Validation Failed",
  "status": 422,
  "detail": "BPMN semantic or structural validation failed.",
  "instance": "/api/v1/workflows/abc/versions/xyz/publish",
  "code": "BPMN_VALIDATION_FAILED",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "request_id": "7d1c5c0e-6a52-4a39-9f0a-2f6f0b0c1a11",
  "invalid_params": [
    { "name": "Task_1", "reason": "Cycle detected at task node", "code": "CYCLE_DETECTED" }
  ]
}
```

**Error codes**

| Code | HTTP | Trigger |
| --- | --- | --- |
| `BAD_REQUEST` | 400 | Malformed request body/params, an unknown field or a trailing JSON value, a missing draft lock token, or an invalid UUID path param |
| `INVALID_BPMN_XML` | 400 | The BPMN document cannot be parsed — bad XML, forbidden `DOCTYPE`/entity, or the XML-bomb token cap. (Forbidden constructs also emit an internal security log; the client response is identical.) |
| `NOT_FOUND` | 404 | Workflow or version not found, or RLS boundary breached |
| `DRAFT_NOT_FOUND` | 404 | No active draft exists for the workflow |
| `NO_ACTIVE_VERSION` | 409 | Workflow has no published active version |
| `UNAUTHORIZED` | 401 | Missing or invalid `x-user-id` / `x-tenant-id` headers |
| `FORBIDDEN` | 403 | Caller lacks `tenant_admin` or `tenant_owner` role |
| `DRAFT_ALREADY_EXISTS` | 409 | A draft already exists; publish or discard it first |
| `DUPLICATE_TEMPLATE_KEY` | 409 | Template key already in use within the tenant |
| `DRAFT_CONCURRENCY` | 409 | Optimistic lock violation on draft save |
| `INVALID_VERSION_STATUS` | 409 | Operation not valid for the version's current status |
| `ACTIVE_INSTANCES_EXIST` | 409 | Cannot archive while running instances exist |
| `STRUCTURAL_DIVERGENCE` | 409 | Topology changed vs. active version; use `force_publish_structural` to override |
| `IDEMPOTENCY_KEY_REPLAY` | 409 | Same idempotency key submitted with a different payload |
| `IDEMPOTENCY_REQUEST_IN_PROGRESS` | 409 | A request with the same idempotency key is still running |
| `PAYLOAD_TOO_LARGE` | 413 | Request body exceeds the 10 MB limit |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | Request carries a body whose `Content-Type` is not `application/json` |
| `PLAN_QUOTA_EXCEEDED` | 403 | Tenant has reached the maximum number of workflow templates for their plan |
| `ASSIGNEE_INELIGIBLE` | 422 | A default assignee no longer has the required department/role membership |
| `BPMN_VALIDATION_FAILED` | 422 | BPMN structural or semantic validation failed; see `invalid_params` |
| `INVALID_INPUT` | 422 (400 for connector credential and alias input) | A request field exceeds its maximum length, or a connector credential or alias field is invalid |
| `UPSTREAM_UNAVAILABLE` | 503 | Execution Service or Org & Membership service unreachable |
| `EVENT_DECODE_FAILED` | 502 | An internal event could not be decoded (internal event route only) |
| `INTERNAL_ERROR` | 500 | Unexpected server error |

**BPMN status split:** a document that **cannot be parsed** returns **400 `INVALID_BPMN_XML`**; a document that parses but **fails validation** (structural/semantic, including `MISSING_NAMESPACE` and `REJECTED_ELEMENT`) returns **422 `BPMN_VALIDATION_FAILED`** with per-node `invalid_params`. Forbidden `DOCTYPE`/entity or XML-bomb input returns the **same** generic `400 INVALID_BPMN_XML` (so a probe is not confirmed) and is additionally recorded in an internal security log.

### Internal API

Service-to-service routes under `/api/v1/internal`, specified in [`api/internal-openapi.yaml`](api/internal-openapi.yaml) (Swagger UI offers it beside the public spec in dev). The gateway never routes them; callers present the service's `INTERNAL_API_TOKEN` in `x-internal-token`, and the tenant comes from the request, not from gateway headers.

| Method | Path | Caller | Purpose |
| --- | --- | --- | --- |
| `GET` | `/api/v1/internal/workflow-versions/{version_id}/compiled-plan?tenant_id=` | Execution Service | A workflow version and its stored compiled plan (any status), from a fail-open Valkey cache in front of Postgres |
| `POST` | `/api/v1/internal/events` | workflow-events consumer | Ingest a domain-event envelope (membership revocations and tenant offboarding) |
| `GET` / `POST` / `DELETE` | `/api/v1/internal/connector-aliases…` | Execution's connector-worker, operators | The connector alias registry |

```bash
curl -s -H "x-internal-token: $INTERNAL_API_TOKEN" \
  "localhost:8080/api/v1/internal/workflow-versions/$VERSION_ID/compiled-plan?tenant_id=$TENANT_ID"
```

This service calls the Execution Service's internal API the same way, sending that service's token as `EXECUTION_SERVICE_TOKEN`: its active-instances read guards archive, and its pause-tasks call follows a membership revocation. A byte-identical copy of the Execution Service's spec lives in `api/contracts/`, and the client's tests hold every request to it.

**Contract gates.** `make contract-breaking` refuses a breaking change to `api/internal-openapi.yaml` against `origin/main` (oasdiff; part of `make check`). `make contract-check` refuses a copy in `api/contracts/` that differs from its source branch (`CONTRACT_SRC_DIR=../execution_service` reads a local checkout). Both run in CI.

---

## Deployment

This service ships a container image and a Helm chart (`deploy/helm/`) for running it on Kubernetes, plus a set of static Prometheus rule files (`deploy/monitoring/`) for environments that don't run the Prometheus Operator CRDs.

### Container image

Multi-stage `Dockerfile`: a `golang:1.26-alpine` builder (private-module access via a BuildKit secret, `GOPRIVATE`-aware) compiles a stripped, trimmed static binary, copied into a `gcr.io/distroless/static-debian12:nonroot` runtime — no shell, no package manager, runs as UID `65532` by default. Both base images are pinned by digest (`make pin-base-images` refreshes them; tracked in `.docker-digests`).

```bash
make docker-build              # builds workflow-definition-service:local (requires GO_PRIVATE_TOKEN)
make docker-lint                # Hadolint
make docker-trivy               # HIGH/CRITICAL CVE scan (source + deps)
make docker-check                # both, no image build required
```

The image exposes the API port; `/metrics` listens on its own port:

| Port | Protocol | Purpose |
| --- | --- | --- |
| `8080` | HTTP | REST API (`/api/v1`), the internal API (`/api/v1/internal`), `/healthz`, `/readyz` |
| `9091` | HTTP | `/metrics` |

The image's built-in `HEALTHCHECK` directive is best-effort for standalone `docker run` use; Kubernetes ignores it entirely and uses its own `startupProbe`/`livenessProbe`/`readinessProbe` against `/healthz` and `/readyz` instead.

### Helm chart (`deploy/helm/`)

```text
deploy/helm/
  Chart.yaml
  values.yaml
  templates/
    deployment.yaml        service.yaml        serviceaccount.yaml
    secret.yaml             migrate-job.yaml     hpa.yaml
    pdb.yaml                 networkpolicy.yaml   servicemonitor.yaml
    prometheusrule.yaml    ingress.yaml          httproute.yaml
    securitypolicy.yaml    _helpers.tpl          NOTES.txt
```

```bash
helm lint ./deploy/helm
helm template my-release ./deploy/helm --set secretValues.DATABASE_URL=... # ... (see Secrets below)
helm upgrade workflow-definition-service ./deploy/helm --install --namespace <ns>
```

**Ports and probes.** The Service and Deployment both expose named `http` (8080) and `metrics` (9091) ports. `startupProbe`/`livenessProbe`/`readinessProbe` all target `http` — `/healthz` is a trivial liveness check, `/readyz` actually verifies the Postgres pool and Valkey are reachable (`internal/adapter/inbound/http/router.go`), so a pod only receives traffic once its real dependencies are up.

**Migrations run as a Helm hook, not at boot.** `cmd/server/main.go` never migrates on the normal server boot path — schema changes are applied by a dedicated `migrate` subcommand (`/server migrate`) that runs this service's domain migrations, then exits. Running that inline at boot would let two replicas race on `golang-migrate`'s advisory lock during a rolling deploy, stalling the loser and risking a readiness-probe timeout for no reason.

The chart wires this up as `templates/migrate-job.yaml`, a Kubernetes `Job` annotated `helm.sh/hook: pre-install,pre-upgrade`. Helm runs it — and waits for it to complete, within 10 minutes — before rolling out the Deployment, and the ServiceAccount and the chart-created Secret are earlier hooks so a first install finds them, so every `helm upgrade --install` applies pending migrations first, sequentially, with no replica race. It's gated by `migrationJob.enabled` (default `true`) in case migrations are ever driven out-of-band instead.

One consequence worth knowing: `cmd/server/main.go` loads and validates the full `Config` *before* it even checks whether it was invoked as `migrate` — so the migrate Job needs the exact same required environment variables and secrets as the main Deployment (not just `DATABASE_URL`), or config validation fails before a single migration runs. The chart template already reuses the same `env`/`envFromSecret` blocks as the Deployment for this reason.

**Resources and scaling.** Base replica count is `2`, with the `HorizontalPodAutoscaler` floor matching it (`autoscaling.minReplicas: 2`) — two AZ-spread replicas is the minimum for this service to survive a single-AZ outage without downtime. `resources.requests`/`limits` (`250m`/`512Mi` request, `500m`/`1024Mi` limit) size for BPMN parsing and DSL compilation, which allocate meaningfully more per-request than a typical CRUD handler. `targetCPUUtilizationPercentage`/`targetMemoryUtilizationPercentage` (`70`/`80`) scale out before either resource is saturated. An optional RPS-based scaling metric (`autoscaling.targetRPSPerReplica`) is wired into the HPA template but left unset by default — it requires `deploy/monitoring/prometheus-adapter-rule.yaml` to be installed in the cluster first.

`podDisruptionBudget.minAvailable` is `1`: at a 2-replica floor, `minAvailable: 2` would block every voluntary disruption (node drains, cluster upgrades) — `1` allows exactly one pod to be evicted at a time while guaranteeing the service never drops to zero replicas from a voluntary action.

`terminationGracePeriodSeconds` is `60`. `cmd/server/app.go`'s shutdown sequence on `SIGTERM`: an HTTP and a metrics `Shutdown(ctx)` bounded by a single 30-second context, then a final pool drain bounded by whatever remains of that deadline. In practice this finishes well under 30 seconds; the grace period carries real headroom above it.

**Secrets.** Two mutually exclusive modes, selected by whether `existingSecret` is set:

- **`existingSecret: "<name>"`** (recommended beyond local testing) — points at a Secret already provisioned by External Secrets Operator, the AWS Secrets Manager CSI driver, or Sealed Secrets. The chart creates no Secret resource of its own. The render-time key checks do not run in this mode, so a key missing from that Secret shows as `CreateContainerConfigError` at pod start. Bump `rotationEpoch` (`--set rotationEpoch=$(date +%s)`) to force a rollout after the external secret rotates.
- **`secretValues.*`** (dev/CI only) — pass real values via `--set` or a Helm secrets plugin. `secret.yaml` hard-fails the render if any key listed in `envFromSecret` (`DATABASE_URL`, `MIGRATION_DATABASE_URL`, `SYSTEM_DATABASE_URL`, `VALKEY_PASSWORD`, `INTERNAL_API_TOKEN`, `EXECUTION_SERVICE_TOKEN`, `OPENBAO_TOKEN`) is empty. These land in the Helm release history unencrypted (base64) — never use this mode against a shared cluster.

**Networking.** `networkPolicy.enabled: true` by default. The gateway/ingress-controller rule and the named callers (the Execution Service) both reach `8080`; the gateway routes only the public path prefixes (`ingress.hosts[].paths`), so `/api/v1/internal` is reachable only in-cluster, and every internal route also requires `x-internal-token`. A separate rule scopes `/metrics` scraping to the monitoring namespace only. Egress allows DNS, HTTPS, Postgres/PgBouncer, Valkey, and OTel OTLP explicitly, plus same-namespace pod egress for `ORG_MEMBERSHIP_BASE_URL`, `CATALOG_ADMIN_BASE_URL` and `EXECUTION_SERVICE_URL`.

`ingress.type` supports either `HTTPRoute` (Gateway API — default) or classic `Ingress`. CORS and gateway-level rate limiting attach via an Envoy Gateway `SecurityPolicy` (`ingress.securityPolicy`) when enabled.

**Observability.** `serviceMonitor.enabled: true` scrapes `/metrics` every 30s. Every alert in the `PrometheusRule` template (and its static twin at `deploy/monitoring/app-alerts.yml`) is verified against a metric actually emitted by this service or one of its vendored platform libraries:

- **Availability** — no healthy scrape target for 2 minutes; replica count below the HA floor.
- **HTTP errors/latency** — 5xx ratio and p99 latency (`platform_http_requests_total`/`platform_http_request_duration_seconds`).
- **Compiled-plan read error rate/latency** — the internal route's own 5xx ratio and p99, since this is the one call the Execution Service depends on synchronously.
- **BPMN validation failure rate** — sustained activity on `workflow_definition_service_validation_failures_total` (emitted only by the standalone `/validate` endpoint).
- **Publish latency and retry exhaustion** — p95 of `workflow_definition_service_publish_latency_seconds` exceeding 2s, and any increase in `platform_db_transaction_retries_exhausted_total`.
- **Internal event ingest failures** — sustained `bad_payload`/`error` results on `POST /api/v1/internal/events`.
- **Postgres query error rate** — `platform_db_queries_total{outcome="error"}`.
- **Panics** — any increase in `platform_http_panics_total`, paged immediately.
- **Compiled-plan cache hit ratio** (informational) — `workflow_definition_service_cache_hits_total`/`workflow_definition_service_cache_misses_total`, not an incident trigger.

- **Row-level security violations** — any increase in `workflow_definition_service_rls_violations_total`, by `violation_type` (`missing_or_invalid_guc` is a warning, `cross_tenant_access` is critical). The count is a 1% sample of blocked rows.

### CI/CD deploy gate

`release.yml`'s `deploy-gate` job runs after the image is built, signed, and pushed, and before the GitHub Release is published:

1. `helm upgrade workflow-definition-service ./deploy/helm --install --wait --timeout=5m --atomic`, passing `env.ORG_MEMBERSHIP_BASE_URL`, `env.CATALOG_ADMIN_BASE_URL`, `env.OPENBAO_ADDR` and `env.VALKEY_ADDR` from the repository variables of the same names — `--atomic` rolls back automatically if the release fails to become healthy within the timeout. The chart refuses to render for a non-dev `APP_ENV` while any of `ORG_MEMBERSHIP_BASE_URL`, `CATALOG_ADMIN_BASE_URL`, `EXECUTION_SERVICE_URL`, `OPENBAO_ADDR` or `VALKEY_ADDR` is empty, and CI runs `make helm-check` to prove it.
2. Verifies the running Deployment's image digest matches the digest that was actually signed and pushed.
3. Waits for `kubectl rollout status` to confirm the new pods are ready.
4. Runs a 2-minute Prometheus check on the 5xx ratio; if it exceeds 1%, `helm rollback` runs automatically and the job — and therefore the release — fails.

The GitHub Release step only runs if `deploy-gate` succeeds. **Required repository configuration** (not shipped by this chart): a `KUBECONFIG_B64` secret for the target cluster, and optionally `K8S_NAMESPACE` (defaults to `workflow-app`) and `PROMETHEUS_URL` vars in the `production` GitHub environment.

---

## Observability

Logs, metrics and traces go through `platform-gincommon` only. Every series carries `domain` (`workflow`), `service` (`workflow-definition-service`) and `environment` (`APP_ENV`).

| Tier | Names | Source |
| --- | --- | --- |
| Platform-shared | `platform_http_*`, `platform_db_*`, `platform_build_info`, `platform_library_info` | The platform libraries, in every service |
| Workflow domain-shared | none defined yet | Registry |
| Service-specific | `workflow_definition_service_*` | This service |

[docs/observability/metric-registry.md](docs/observability/metric-registry.md) is generated from the platform registry and a real scrape; `make metrics-lint` fails when it is stale, when a collector is built outside `internal/observability`, or when an alert or doc names a retired metric. Every alert carries a `runbook` annotation naming its entry in [docs/observability/runbooks.md](docs/observability/runbooks.md). `/metrics` is served on its own port. Tracing exports over OTLP, and `OTEL_EXPORTER_OTLP_ENDPOINT=none` turns export off while trace and request IDs keep working. Outbound calls to the Execution Service and the Org & Membership Service carry the trace context.

Sampled row-level-security violations written to `rls_violation_log` are exported by a background loop through the system pool (`RLS_VIOLATION_EXPORT_INTERVAL`), and rows past `RLS_VIOLATION_RETENTION` are deleted; the system role needs `SELECT` and `DELETE` on the log.

---

## Security

- **Tenant isolation:** every tenant table has row-level security forced on, with a policy that checks the tenant GUC through a function that logs sampled violations. The system pool connects as a `BYPASSRLS` role and serves only the global catalog and the violation exporter.
- **Internal API:** `/api/v1/internal` requires `x-internal-token`, compared in constant time; request bodies are capped at 10 MiB on the internal and public API groups.
- **Connector credentials:** written to OpenBao under `connectors/<tenant>/<type>/<field>`, with `connector_type` checked against the registry and `field_name` against an allowlist; `OPENBAO_ADDR` and `OPENBAO_TOKEN` are required outside dev.
- **Configuration:** `APP_ENV` is required and is one of `local`, `dev`, `test`, `staging`, `prod`; `.env` is read in `local` and `dev` only. A value that does not parse (`HTTP_PORT=8080abc`, a malformed duration) or a duration of zero for a TTL or timeout fails startup, and every problem is listed in one error. Outside `dev` the service refuses to start without `VALKEY_ADDR`, the internal token, the Org & Membership and Execution URLs and token, and OpenBao; `prod` also requires `SYSTEM_DATABASE_URL`.
- **Container:** distroless, non-root, read-only root filesystem, all capabilities dropped.

---

## Workflow Design Guide

Two guides live alongside the code, aimed at non-engineering audiences who touch a workflow template before or after it's compiled:

- **[docs/bpmn-designer-guide.md](docs/bpmn-designer-guide.md)** — for business analysts and process owners modelling workflows in Camunda Modeler: lanes/departments, task types, gateways, condition expressions, and the structural rules checked on upload.
- **[docs/ui-enrichment-guide.md](docs/ui-enrichment-guide.md)** — for the ops/engineering team reviewing a compiled plan before activation: verifying identity fields (department IDs, assignee UUIDs) against IAM records.

See [ARCHITECTURE.md § BPMN Compiler](ARCHITECTURE.md#bpmn-compiler) for the full element reference, validation rule catalog, and error-code set these guides summarize for a non-engineering audience.

---

## Tech stack

| | |
| --- | --- |
| Language | Go 1.26 |
| HTTP | Gin + `platform-gincommon` (OTel, Prometheus, Zap, auth) |
| Database | PostgreSQL 18 · pgx/v5 · sqlc · golang-migrate · `platform-pgcommon` (pool & RLS) |
| Cache / locks | Valkey 8 · go-redis/v9 |
| Events | None published, no outbox. Inbound: HTTP `POST /api/v1/internal/events` from the shared workflow-events consumer |
| Contracts | OpenAPI 3 · kin-openapi (tests) · oasdiff (breaking-change gate) |
| Observability | OTel traces · Prometheus metrics · Zap structured logs |
| Testing | GoMock · race detector · `testcontainers-go` (integration) |

---

## Testing

| Directory | What | Command |
| --------- | ---- | ------- |
| `internal/**/*_test.go` | White-box unit tests (unexported access) | `make test` |
| `test/unit/<pkg>/` | Black-box unit tests (exported API only) | `make test` |
| `test/contract/` | The public and internal APIs against their specs, the Execution client against the copied Execution contract | `make test` |
| `test/integration/`, `test/e2e/` | DB integration and service lifecycle tests against a real PostgreSQL container | `make test-integration` |
| Execution's `test/compat/` | This service's binary against the Execution Service's real API, worker and connector worker, with IAM events delivered through the Workflow-Events Consumer | `make -C <execution checkout> test-compat DEFINITION_SERVICE_DIR=$PWD EVENT_CONSUMER_DIR=<consumer checkout>` |

Integration tests require Docker. Pre-pull the image once:

```bash
make tools-integration   # docker pull postgres:18-alpine
make test-integration
```

Coverage gate: **95%** on unit tests. The postgres repo adapter (`internal/adapter/outbound/postgres/`) and generated packages (`postgres/db/`, `core/port/mocks/`) are excluded from the unit gate and covered by integration tests instead. See [CONTRIBUTING.md § Testing](CONTRIBUTING.md#testing) for the full test-authoring conventions (table-driven tests, white-box vs black-box placement, mocking).

---

## Common pitfalls

**Route added without `ProtectedMiddlewares`**

`gincommon.RequestContext(c)` does a type assertion to an internal gincommon type injected by `ProtectedMiddlewares`. If a route is registered without the middleware, handlers calling `RequestContext` receive a missing-context signal and return **500**, not 401. A 401 would be misleading — a missing context is a server misconfiguration, not an auth failure from the caller.

**Direct `pgxpool` usage**

All database access must go through `pgcommon.Pool` helpers (`WithConn`, `RunInTx`). Using `pgxpool` directly bypasses the RLS GUC injection that scopes all queries to the caller's tenant. A missing GUC causes PostgreSQL's default-deny RLS policy to return empty result sets silently — not an error.

---

## Environment variables

See [`.env.example`](.env.example) for the raw template, and [ARCHITECTURE.md § Configuration reference](ARCHITECTURE.md#configuration-reference) for the complete table with defaults, grouped by subsystem.

---

## Database

### Schema

Database: PostgreSQL 18, schema `workflow_definition`. Multi-tenancy is enforced via **Row-Level Security (RLS)** — every query is automatically filtered by the `app.tenant_id` GUC set per-connection by the postgres adapter. Cross-tenant data leaks are impossible at the database level.

| Table | Purpose |
| --- | --- |
| `workflow` | Root template entity — template key, name, active version pointer |
| `workflow_version` | Versioned snapshot — BPMN XML, compiled DSL, status lifecycle |
| `workflow_node_assignee` | Denormalised reverse index: user → versions that reference them as default assignees |
| `workflow_module` / `workflow_module_version` | Reusable BPMN modules and their versions |
| `workflow_template` | The starter templates a tenant can start from |
| `connector_rest_alias` | The REST connector alias registry (operational, read across tenants) |
| `rls_violation_log` | Sampled rows blocked by row-level security, exported as a metric |
| `tenant_retention` | The ledger of departed tenants and their purge dates (no row-level security; read across tenants by the purge sweep) |

See full DDL in [`db/migrations/`](db/migrations/).

### Database roles

The migrations create no roles and no grants: they run under `MIGRATION_DATABASE_URL`, and every table has `REVOKE ALL FROM PUBLIC` with row-level security forced. Provision these roles with the database before the first deploy, or every query fails with permission denied:

| Role | Used by | Needs |
| --- | --- | --- |
| Migrator | the `migrate` subcommand (`MIGRATION_DATABASE_URL`) | owns the schema and its tables; `BYPASSRLS` so data migrations are not blocked by the service's own policies |
| Runtime | the server (`DATABASE_URL`) | `USAGE` on the schema and `SELECT`, `INSERT`, `UPDATE`, `DELETE` on its tables and `USAGE` on its sequences; never `BYPASSRLS` |
| System | global-catalogue writes and the RLS-violation exporter (`SYSTEM_DATABASE_URL`, required in prod) | `BYPASSRLS` with the same table privileges, including `SELECT` and `DELETE` on `rls_violation_log` |

### Query patterns

Query definitions in `db/queries/` are compiled to type-safe Go by [sqlc](https://sqlc.dev). Generated output goes to `internal/adapter/outbound/postgres/db/`. Repository adapters hold a `*pgcommon.Pool` value and use transaction/connection helpers (`WithConn`, `RunInTx`) to run sqlc-generated queries.

```bash
make generate          # runs sqlc generate (queries)
make generate-sqlc     # sqlc only — use after editing db/queries/*.sql
```

**Status-guarded mutations (`:execresult`).** Mutations that require the record to be in a specific status (e.g. `PublishVersion` requires DRAFT) are defined with `:execresult` in `db/queries/` so `RowsAffected()` can be inspected:

```sql
-- name: PublishVersion :execresult
UPDATE workflow_version
SET status = 'PUBLISHED', version_number = $3, ...
WHERE tenant_id = $1 AND id = $2 AND status = 'DRAFT';
```

On `RowsAffected() == 0` — either the record doesn't exist or it exists in the wrong status — Go calls `statusOrNotFound` to do a secondary `GetWorkflowVersionByID` and distinguish the two cases. See [ARCHITECTURE.md § Repository error semantics](ARCHITECTURE.md#repository-error-semantics).

**Dynamic list queries (raw pgx).** `ListWorkflows` uses a hand-written dynamic WHERE clause because sqlc cannot generate optional filters. The `nextArg()` closure manages `$N` parameter numbering — all filter parameters must be appended via `nextArg()` only, never by hand-crafting `$N` literals.

### Key constraints

| Table | Constraint | Enforces |
| --- | --- | --- |
| `workflow` | `UNIQUE (tenant_id, template_key)` | No duplicate keys per tenant |
| `workflow_version` | `idx_wv_single_draft` (partial unique) | At most one DRAFT per workflow |
| `workflow_version` | `uq_workflow_version_published` | Unique version numbers per workflow |
| `workflow_version` | `chk_version_number_on_publish` | Published versions must have a version number |

See [CONTRIBUTING.md § Database Migrations](CONTRIBUTING.md#database-migrations) for the migration-authoring workflow.

---

## Database connection pooling

There are two independent pooling layers between the service and Postgres:

```text
Service process  (pgx: PG_MAX_CONNS=10) ──► PgBouncer (DEFAULT_POOL_SIZE=N) ──► PostgreSQL
      ▲                                             ▲
Layer 1: app pool (per service, per pod)   Layer 2: proxy pool (shared, all services)
Connections: app → PgBouncer               Connections: PgBouncer → Postgres
```

**Layer 1 — pgx pool (`PG_MAX_CONNS`, per service)**
Each service process holds up to `PG_MAX_CONNS` open TCP connections to PgBouncer. Each microservice configures this independently.

**Layer 2 — PgBouncer (`DEFAULT_POOL_SIZE`, shared)**
One PgBouncer deployment shared by all microservices. `DEFAULT_POOL_SIZE` is the maximum backend connections PgBouncer opens to Postgres for a given `(user, database)` pair — shared across all services connecting to it. In transaction pooling mode a backend connection is held only for the duration of one transaction, so `DEFAULT_POOL_SIZE` can be smaller than the total client connections without becoming a bottleneck.

### Connection sizing

| Variable | Controls | Rule |
| --- | --- | --- |
| `PG_MAX_CONNS` | App → PgBouncer connections (per service, per pod) | Set per workload; use `PG_MIN_CONNS=0` in PgBouncer mode |
| PgBouncer `MAX_CLIENT_CONN` | Max connections PgBouncer accepts from all clients | 1.5 × sum of (`PG_MAX_CONNS` × pod count) — buffer for rolling deploys and burst |
| PgBouncer `DEFAULT_POOL_SIZE` | Max backend connections PgBouncer holds to Postgres | Your Postgres connection budget; PgBouncer queues excess clients rather than failing |
| RDS `max_connections` | Hard Postgres limit | ≥ `DEFAULT_POOL_SIZE` × (distinct user+db pairs) |

**Example — 3 services × 1 pod, `PG_MAX_CONNS=10`:**

- `MAX_CLIENT_CONN` = 50 (3 × 10 = 30 exact; ×1.5 = 45, round up — covers rolling deploy N+1 pods)
- `DEFAULT_POOL_SIZE` = 30 (Postgres budget; set to what Postgres can afford)

### Env vars

| Variable | Purpose | Default |
| --- | --- | --- |
| `PG_BOUNCER_MODE` | `true` when `DATABASE_URL` points to PgBouncer. Switches pgx to simple protocol + transaction-local GUC/RLS injection. The system pool always uses it. | `false` |
| `PG_STATEMENT_TIMEOUT`, `PG_LOCK_TIMEOUT` | Per-transaction timeouts applied to both pools. A query cancelled by the statement timeout answers `503`. | `0` (server setting) |
| `PG_SLOW_QUERY_THRESHOLD` | Duration above which a query is logged as slow. | `200ms` |
| `MIGRATION_DATABASE_URL` | Direct Postgres DSN for the `migrate` subcommand. Required when `DATABASE_URL` is a PgBouncer URL — `golang-migrate` uses session advisory locks that PgBouncer transaction pooling drops. Falls back to `DATABASE_URL` when unset. | *(unset)* |
| `DATABASE_FALLBACK_URL` | Startup-only fallback DSN (direct Postgres). Tried once at boot if the primary pool fails — covers PgBouncer not yet ready during rolling deploys. Not used for runtime reconnection. | *(unset)* |

### Local dev with PgBouncer

```bash
make docker-up   # starts postgres (5432) + pgbouncer (6432)

# Migrations always run direct — never through PgBouncer (advisory locks) — as the migrator role
# that scripts/init-db.sql creates (it owns the schema and is the BYPASSRLS system role).
DATABASE_URL=postgres://workflow_definition_migrator:devpassword@localhost:5432/workflow_definition?sslmode=disable \
  go run ./cmd/server migrate

# Service via PgBouncer as the application role (NOSUPERUSER NOBYPASSRLS) — update .env:
#   DATABASE_URL=postgres://workflow_definition_app:devpassword@localhost:6432/workflow_definition?sslmode=disable
#   PG_BOUNCER_MODE=true
#   PG_MIN_CONNS=0
go run ./cmd/server

# Verify PgBouncer is active
psql "postgresql://workflow_definition_app:devpassword@localhost:6432/pgbouncer" -c "SHOW POOLS;"
```

### Production setup

| K8s resource | `DATABASE_URL` | `MIGRATION_DATABASE_URL` | `PG_BOUNCER_MODE` |
| --- | --- | --- | --- |
| Service Deployment | `pgbouncer-svc:6432` | *(unset)* | `true` |
| Migration init container | `pgbouncer-svc:6432` | `rds-endpoint:5432` | `false` |

- **TLS** — PgBouncer must connect to RDS with `server_tls_sslmode=require`; `sslmode=disable` is only safe inside the cluster VPC with mesh mTLS.
- **HA** — PgBouncer is stateless; run ≥2 replicas behind a k8s Service. `DATABASE_FALLBACK_URL` covers startup races only — it does not provide runtime failover.
- **Shared PgBouncer** — one PgBouncer deployment serves all microservices. Scale `MAX_CLIENT_CONN` as you add services or pods.

---

## CI

Pull requests and pushes run `ci.yml`: a `ci-gate` job skips draft pull requests and those labelled `skip-ci`, then code generation, `validate-quality` (format, tidy, vet, lint, metrics-lint, govulncheck, the chart check, the contract and breaking-change gates and the Dockerfile pin check), `validate-test` (unit and integration tests with a merged coverage gate, and a `compat` job that checks out the Execution Service's `feat/service` branch and runs its compatibility suite, which builds this checkout's binary and drives it against Execution's real API and worker), a container image build, a Trivy scan and a smoke test. Pull requests that change only Markdown or `docs/` skip CI. `changelog-check.yml` requires a CHANGELOG entry on every pull request that touches `internal/`, `api/` or `cmd/`, and `pr-summary` keeps one comment on the pull request current.

## Versioning and releases

Releases are Git tags `vMAJOR.MINOR.PATCH`, cut on the merged commit after the CHANGELOG `[Unreleased]` section moves to a versioned heading. The `Release` workflow refuses a tag with no CHANGELOG entry, builds and scans the image, and creates the GitHub Release. See [VERSIONING.md](VERSIONING.md) and [CHANGELOG.md](CHANGELOG.md).

---

## Out of scope

| Concern | Owner |
| --- | --- |
| Running workflow instances, tasks and connectors | Execution Service |
| Drawing and editing the diagram | The frontend workflow builder |
| Department membership and eligibility | Org & Membership Service |
| Identity, tokens and tenant resolution | IAM |
| Compiled-plan DSL types | `workflow-models` |
| Connector implementations | `workflow-connectors` |

This repository carries no `LICENSE` file: it is private and proprietary to BCBP Solutions.

## See also

- [ARCHITECTURE.md](ARCHITECTURE.md) · [CONTRIBUTING.md](CONTRIBUTING.md) · [SECURITY.md](SECURITY.md) · [VERSIONING.md](VERSIONING.md) · [CHANGELOG.md](CHANGELOG.md)
- [docs/bpmn-designer-guide.md](docs/bpmn-designer-guide.md) · [docs/ui-enrichment-guide.md](docs/ui-enrichment-guide.md)
- The definition-service LLD in [docs/lld/](docs/lld/), content-identical to the design repository copy
