# Workflow Definition Service

The **BCBP Workflow Engine** is a BPMN-driven automation platform for the BCBP services ecosystem. It has two halves:

- **Definition Service** (this repo) — the design-time control plane. It parses BPMN 2.0 XML, validates it structurally and semantically, compiles it to an internal DSL plan, and manages the full `DRAFT → PUBLISHED → ARCHIVED` version lifecycle.
- **Execution Service** — the runtime half. It receives compiled workflow plans from this service over gRPC and drives live workflow instances.

The Definition Service exposes a REST API consumed by the **frontend workflow builder** and a gRPC API consumed by the **Execution Service**.

```text
Frontend Builder ──REST──► Definition Service ──gRPC──► Execution Service
                                    │
                             PostgreSQL + Valkey
                                    │
                              AWS SNS (domain events)
```

| Responsibility | Detail |
| --- | --- |
| **BPMN ingestion** | Accepts BPMN 2.0 XML uploads from the frontend canvas modeler |
| **Validation** | Runs structural, semantic, and topological (DAG/cycle) checks |
| **Compilation** | Converts validated BPMN graphs into immutable JSON DSL execution plans |
| **Versioning** | Manages DRAFT → PUBLISHED → ARCHIVED lifecycle with optimistic concurrency |
| **Serving** | Exposes compiled DSLs to the Execution Service over high-throughput gRPC |
| **Event publishing** | Transactional-outbox → AWS SNS pipeline is wired but currently has no outbound event types defined |
| **Membership sync** | Consumes `DepartmentMembershipRevoked` events (via `POST /internal/events`) to invalidate affected template assignees |

> **Architecture** — [ARCHITECTURE.md](ARCHITECTURE.md): layer model, sequence diagrams, BPMN compiler reference, configuration reference, and error catalog. Diagram sources live in [docs/architecture/](docs/architecture/).
>
> **API specs** — REST: [docs/swagger/openapi.yaml](docs/swagger/openapi.yaml), served locally at `/swagger/*any` in dev mode. Events: [api/asyncapi.yaml](api/asyncapi.yaml). gRPC: [api/proto/](api/proto/).
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

# 5. Apply schema migrations (outbox + domain) — the server does NOT migrate at boot
make migrate

# 6. Start the server (AWS stubs active by default)
go run ./cmd/server
```

The server is ready when you see:

```sh
INFO  HTTP server starting         {"addr": ":8080"}
INFO  gRPC server starting         {"addr": ":9090"}
INFO  outbox relay starting
```

Verify:

```bash
curl http://localhost:8080/healthz   # {"status":"OK"}
curl http://localhost:8080/readyz    # {"status":"OK"}
curl http://localhost:8080/metrics   # Prometheus exposition
```

---

## Local Development

### AWS stubs

By default `AWS_USE_STUB=true` in `.env.example`. This activates a no-op stub SNS publisher so the service boots without any AWS credentials. (The service does not consume SQS in-process — inbound events arrive over HTTP at `POST /internal/events`.)

### LocalStack + full stack (end-to-end)

`make docker-up` includes a LocalStack container that emulates SNS locally. To run the service against real AWS clients and test the full event pipeline:

```bash
# 1. Start everything (Postgres + Valkey + LocalStack)
make docker-up

# 2. Start outbound service stubs (gRPC + HTTP)
go run ./cmd/stub/execution &    # :9091 (gRPC), :9092 (control)
go run ./cmd/stub/membership &   # :8081 (HTTP + /control toggle)

# 3. Run the server against LocalStack and stubs (AWS_USE_STUB=false)
ORG_MEMBERSHIP_BASE_URL=http://localhost:8081 EXECUTION_SERVICE_ADDR=localhost:9091 \
  AWS_USE_STUB=false go run ./cmd/server &

# 4. Run the smoke test
./scripts/smoke-test.sh
```

The stub binaries expose a runtime control plane to toggle their responses:

```bash
# Toggle membership eligibility
curl -X POST http://localhost:8081/control -d '{"eligible":true}'
curl -X POST http://localhost:8081/control -d '{"eligible":false}'

# Toggle active instances on execution service
curl -X POST http://localhost:9092/control -d '{"has_active":false}'
curl -X POST http://localhost:9092/control -d '{"has_active":true}'
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
| `make tools` | Install sqlc, buf, mockgen, golangci-lint, go-arch-lint into `.tools/` |
| `make tools-integration` | Pre-pull Docker images used by integration tests (testcontainers-go) |
| `make generate` | buf generate (proto) + sqlc generate (queries) |
| `make mock` | Regenerate GoMock stubs for `core/port` interfaces |
| `make build` | Compile binary to `bin/server` |
| `make migrate` | Apply schema migrations (outbox + domain) and exit — not run at server boot |
| `make test` | Unit tests with race detector and coverage (internal + test/unit) |
| `make test-integration` | Integration tests (spins up Docker containers via testcontainers-go automatically; requires running Docker) |
| `make cover` | Unit tests + coverage summary |
| `make cover-html` | Open HTML coverage report |
| `make cover-check` | Fail if coverage < 95% (postgres adapter + generated pkgs excluded) |
| `make fmt-check` / `make lint` / `make arch-lint` | Formatting, lint, and Clean Architecture import-direction checks (read-only) |
| `make fix` | Auto-fix formatting and lint issues |
| `make check` | Full local CI pass: fmt + lint + vet + arch-lint + tests + coverage gate |
| `make vuln` | Run govulncheck for known dependency vulnerabilities |
| `make schema-validate` | Validate `internal/eventschema/*.json` against `api/asyncapi.yaml` (no AWS required) — see [Schema Governance](#schema-governance) |
| `make schema-register` / `make schema-prune` | Register/retire event schemas in AWS Glue Schema Registry |
| `make docker-build` / `make docker-lint` / `make docker-trivy` | Build the container image, lint the Dockerfile, scan for CVEs |
| `make godoc` | Serve Go package documentation locally via pkgsite (`http://localhost:8080`) |
| `make docker-up` | Start PostgreSQL + Valkey (+ LocalStack + PgBouncer) |
| `make docker-down` | Stop infra |
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
      http/              ← Gin handlers, authz, middleware, POST /internal/events
      grpc/              ← GetCompiledWorkflow server
    outbound/
      postgres/          ← sqlc DB layer + repo adapters
      valkey/            ← Valkey cache adapter
  bpmn_compiler/         ← XML parser, validator, DSL compiler
  config/                ← env var loading
api/                     ← REST (OpenAPI source), AsyncAPI specs, and .proto source files (committed)
gen/                     ← buf-generated stubs (gitignored)
db/
  migrations/            ← golang-migrate SQL migrations (.up.sql/.down.sql, committed)
  queries/               ← sqlc query definitions (committed)
docs/
  architecture/          ← Mermaid diagram sources for ARCHITECTURE.md
  swagger/               ← OpenAPI spec served by the dev-mode Swagger UI
  lld/                   ← in-repo copy of the design repo's LLD, kept content-identical
  bpmn-designer-guide.md ← BPMN modelling guide for business analysts / process owners
  ui-enrichment-guide.md ← Compiled-plan enrichment guide for ops/engineering reviewers
```

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
| `x-plan` | No | Subscription tier: `starter`, `pro`, `enterprise` |
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
| `GET` | `/api/v1/workflows/:id/versions/:version_id/export` | Any | Download raw BPMN XML |
| `GET` | `/api/v1/workflows/:id/versions/:version_id/diff/:target_version_id` | Any | Structural diff between two versions |
| `GET` | `/healthz` | Public | Liveness probe |
| `GET` | `/readyz` | Public | Readiness probe (DB ping) |
| `GET` | `/metrics` | Public | Prometheus metrics |
| `POST` | `/internal/events` | Internal | Ingest a domain-event envelope from the shared workflow-events consumer (e.g. `DepartmentMembershipRevoked`) |

**Admin** = requires `tenant_admin` or `tenant_owner` in `x-tenant-roles`. **Internal** = service-to-service only; not exposed on the public gateway. Optionally authenticated with `x-internal-token` (`INTERNAL_API_TOKEN`); the handler sets the RLS tenant from the envelope `tenant_id`. Returns 2xx (incl. idempotent no-op), 400 (malformed — non-retryable), or 500 (transient — retried).

**Optimistic concurrency on draft update.** `PUT /api/v1/workflows/:id/draft` accepts a `record_version` (the token returned on `GET /draft` and version responses). A stale value yields `409 DRAFT_CONCURRENCY`. `record_version` is bumped by the DB on every real change. The `PUT /draft`, `POST /workflows`, and clone responses include a `message` field; create/clone also echo the new identifiers.

**Error format (RFC-9457):**

```json
{
  "type": "https://api.workflow.platform/errors/draft-already-exists",
  "title": "Draft Already Exists",
  "status": 409,
  "detail": "An active draft already exists for this workflow.",
  "instance": "/api/v1/workflows/abc/draft",
  "code": "DRAFT_ALREADY_EXISTS"
}
```

Validation errors include an `invalid_params` array:

```json
{
  "type": "https://api.workflow.platform/errors/validation-failed",
  "title": "BPMN Validation Failed",
  "status": 422,
  "detail": "BPMN semantic or structural validation failed.",
  "instance": "/api/v1/workflows/abc/versions/xyz/publish",
  "code": "BPMN_VALIDATION_FAILED",
  "invalid_params": [
    { "name": "Task_1", "reason": "Cycle detected at task node", "code": "CYCLE_DETECTED" }
  ]
}
```

**Error codes**

| Code | HTTP | Trigger |
| --- | --- | --- |
| `BAD_REQUEST` | 400 | Malformed request body/params, or an invalid UUID path param |
| `INVALID_BPMN_XML` | 400 | The BPMN document cannot be parsed — bad XML, forbidden `DOCTYPE`/entity, or the XML-bomb token cap. (Forbidden constructs also emit an internal security log; the client response is identical.) |
| `NOT_FOUND` | 404 | Workflow or version not found, or RLS boundary breached |
| `DRAFT_NOT_FOUND` | 404 | No active draft exists for the workflow |
| `NO_ACTIVE_VERSION` | 404 | Workflow has no published active version |
| `UNAUTHORIZED` | 401 | Missing or invalid `x-user-id` / `x-tenant-id` headers |
| `FORBIDDEN` | 403 | Caller lacks `tenant_admin` or `tenant_owner` role |
| `DRAFT_ALREADY_EXISTS` | 409 | A draft already exists; publish or discard it first |
| `DUPLICATE_BUSINESS_KEY` | 409 | Business key already in use within the tenant |
| `DRAFT_CONCURRENCY` | 409 | Optimistic lock violation on draft save |
| `INVALID_VERSION_STATUS` | 409 | Operation not valid for the version's current status |
| `ACTIVE_INSTANCES_EXIST` | 409 | Cannot archive while running instances exist |
| `STRUCTURAL_DIVERGENCE` | 409 | Topology changed vs. active version; use `force_publish_structural` to override |
| `IDEMPOTENCY_KEY_REPLAY` | 409 | Same idempotency key submitted with a different payload |
| `PAYLOAD_TOO_LARGE` | 413 | Request body exceeds the 10 MB limit |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | Request carries a body whose `Content-Type` is not `application/json` |
| `PLAN_QUOTA_EXCEEDED` | 403 | Tenant has reached the maximum number of workflow templates for their plan |
| `ASSIGNEE_INELIGIBLE` | 422 | A default assignee no longer has the required department/role membership |
| `BPMN_VALIDATION_FAILED` | 422 | BPMN structural or semantic validation failed; see `invalid_params` |
| `UPSTREAM_UNAVAILABLE` | 503 | Execution Service or Org & Membership service unreachable |
| `INTERNAL_ERROR` | 500 | Unexpected server error |

**BPMN status split:** a document that **cannot be parsed** returns **400 `INVALID_BPMN_XML`**; a document that parses but **fails validation** (structural/semantic, including `MISSING_NAMESPACE` and `REJECTED_ELEMENT`) returns **422 `BPMN_VALIDATION_FAILED`** with per-node `invalid_params`. Forbidden `DOCTYPE`/entity or XML-bomb input returns the **same** generic `400 INVALID_BPMN_XML` (so a probe is not confirmed) and is additionally recorded in an internal security log.

### gRPC API

Proto source: [`api/proto/definition/v1/definition.proto`](api/proto/definition/v1/definition.proto).

**Service: `DefinitionService`.** High-throughput internal gRPC endpoint. Called by the Execution Service during runtime workflow instantiation to fetch compiled DSL plans. Bypasses the Envoy REST gateway to eliminate serialisation overhead.

**Transport.** The gRPC server accepts insecure plain-text connections on the intra-cluster network. mTLS is enforced at the Envoy sidecar layer — connections from outside the mesh are rejected there, not at the server.

**Reflection.** Server reflection is registered (`reflection.Register`), so `grpcurl` works without passing proto files:

```bash
# List all services
grpcurl -plaintext localhost:9090 list

# Describe the service
grpcurl -plaintext localhost:9090 describe definition.v1.DefinitionService

# Call GetCompiledWorkflow
grpcurl -plaintext \
  -d '{"tenant_id":"<uuid>","workflow_version_id":"<uuid>"}' \
  localhost:9090 definition.v1.DefinitionService/GetCompiledWorkflow
```

**`GetCompiledWorkflow`**

```protobuf
rpc GetCompiledWorkflow(GetCompiledWorkflowRequest)
    returns (GetCompiledWorkflowResponse);
```

Request:

| Field | Type | Description |
| --- | --- | --- |
| `tenant_id` | `string` | Tenant UUID — mandatory; sets RLS session GUC before any DB access |
| `workflow_version_id` | `string` | UUID of the version record to fetch |

Response:

| Field | Type | Description |
| --- | --- | --- |
| `workflow_id` | `string` | Parent workflow UUID |
| `version_id` | `string` | Requested version UUID |
| `version_number` | `int32` | Published version number |
| `status` | `string` | `DRAFT`, `PUBLISHED`, or `ARCHIVED` |
| `is_valid` | `bool` | `false` if any default assignee has become ineligible |
| `compiled_plan_json` | `string` | Pre-compiled ExecutionPlan DSL as a JSON string |

The version is carried on the response (`version_number`), not inside the DSL blob.

**Compiled DSL shape (`compiled_plan_json`).** `{ name, task_queue, departments[], execution: { steps[] } }`. Each step in `execution.steps` is one of:

| Step kind | Shape | Meaning |
| --- | --- | --- |
| `sequential` | `["deptA", "deptB"]` | departments run in order |
| `parallel` | `["deptA", "deptB"]` | departments run concurrently (AND split/join) |
| `exclusive` | `[{ branch }, …]` | XOR decision — runtime evaluates each branch's `condition_expression` |

Each `exclusive` branch object:

| Field | Meaning |
| --- | --- |
| `target` | Destination department for a forward branch. **Empty** when the branch terminates the workflow (routes to the end event), and empty on a revert branch. |
| `condition_expression` | Expression the Execution Service / Temporal worker evaluates to pick the branch. |
| `revert_to_dept` / `revert_to_stage` | Present on a **guarded-loop revert branch** (back-edge from the gateway) instead of `target`: the `(department, stage_type)` to send the task back to. |

gRPC status codes:

| Code | Meaning |
| --- | --- |
| `OK` (0) | Success |
| `INVALID_ARGUMENT` (3) | Missing or malformed `workflow_version_id` |
| `PERMISSION_DENIED` (7) | `tenant_id` is empty (no tenant context) |
| `NOT_FOUND` (5) | Version not found or RLS filtered it out |
| `INTERNAL` (13) | Unexpected server error |

**Compiled-plan cache.** Responses are cached in Valkey under `wf:plan:<tenant_id>:<version_id>` (TTL `CACHE_COMPILED_PLAN_TTL`, default 1h), populated lazily on the first read. The cache is fail-open — a cache outage falls back to Postgres. Entries are invalidated when a version's `status` or `is_valid` changes (workflow archive, membership-revocation invalidation).

**Health Check (`grpc.health.v1.Health`).** The server registers the standard gRPC health service at startup (`grpc_health_v1.RegisterHealthServer`), reporting `SERVING` for all service names by default — used for Kubernetes liveness/readiness probes on `:9090` and service-mesh health checks:

```bash
grpcurl -plaintext localhost:9090 grpc.health.v1.Health/Check
grpcurl -plaintext -d '{"service":"definition.v1.DefinitionService"}' localhost:9090 grpc.health.v1.Health/Check
```

Proto stubs are generated via `make generate` (`buf generate`); output goes to `gen/proto/`, gitignored.

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

The image exposes two ports:

| Port | Protocol | Purpose |
| --- | --- | --- |
| `8080` | HTTP | REST API (`/api/v1`), `/healthz`, `/readyz`, `/metrics` |
| `9090` | gRPC | `GetCompiledWorkflow` — called by the Execution Service |

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

**Ports and probes.** The Service and Deployment both expose named `http` (8080) and `grpc` (9090) ports. `startupProbe`/`livenessProbe`/`readinessProbe` all target `http` — `/healthz` is a trivial liveness check, `/readyz` actually verifies the Postgres pool and Valkey are reachable (`cmd/server/handlers.go`), so a pod only receives traffic once its real dependencies are up.

**Migrations run as a Helm hook, not at boot.** `cmd/server/main.go` never migrates on the normal server boot path — schema changes are applied by a dedicated `migrate` subcommand (`/server migrate`) that runs the outbox schema and this service's own domain migrations, then exits. Running that inline at boot would let two replicas race on `golang-migrate`'s advisory lock during a rolling deploy, stalling the loser and risking a readiness-probe timeout for no reason.

The chart wires this up as `templates/migrate-job.yaml`, a Kubernetes `Job` annotated `helm.sh/hook: pre-install,pre-upgrade`. Helm runs it — and waits for it to complete — before rolling out the Deployment, so every `helm upgrade --install` applies pending migrations first, sequentially, with no replica race. It's gated by `migrationJob.enabled` (default `true`) in case migrations are ever driven out-of-band instead.

One consequence worth knowing: `cmd/server/main.go` loads and validates the full `Config` *before* it even checks whether it was invoked as `migrate` — so the migrate Job needs the exact same required environment variables and secrets as the main Deployment (not just `DATABASE_URL`), or config validation fails before a single migration runs. The chart template already reuses the same `env`/`envFromSecret` blocks as the Deployment for this reason.

**Resources and scaling.** Base replica count is `2`, with the `HorizontalPodAutoscaler` floor matching it (`autoscaling.minReplicas: 2`) — two AZ-spread replicas is the minimum for this service to survive a single-AZ outage without downtime. `resources.requests`/`limits` (`250m`/`512Mi` request, `500m`/`1024Mi` limit) size for BPMN parsing and DSL compilation, which allocate meaningfully more per-request than a typical CRUD handler. `targetCPUUtilizationPercentage`/`targetMemoryUtilizationPercentage` (`70`/`80`) scale out before either resource is saturated. An optional RPS-based scaling metric (`autoscaling.targetRPSPerReplica`) is wired into the HPA template but left unset by default — it requires `deploy/monitoring/prometheus-adapter-rule.yaml` to be installed in the cluster first.

`podDisruptionBudget.minAvailable` is `1`: at a 2-replica floor, `minAvailable: 2` would block every voluntary disruption (node drains, cluster upgrades) — `1` allows exactly one pod to be evicted at a time while guaranteeing the service never drops to zero replicas from a voluntary action.

`terminationGracePeriodSeconds` is `60`. `cmd/server/app.go`'s shutdown sequence on `SIGTERM`: an HTTP `Shutdown(ctx)` bounded by a single 30-second context, then `grpcServer.GracefulStop()` (no context — blocks unboundedly until in-flight RPCs drain), then the outbox relay's own `Stop()` (also uncancellable, ~30-second internal drain default), then a final pool drain bounded by whatever remains of the original 30-second deadline. In practice this finishes well under 30 seconds — but the grace period carries real headroom above the worst case rather than assuming 30 seconds caps the whole sequence.

**Secrets.** Two mutually exclusive modes, selected by whether `existingSecret` is set:

- **`existingSecret: "<name>"`** (recommended beyond local testing) — points at a Secret already provisioned by External Secrets Operator, the AWS Secrets Manager CSI driver, or Sealed Secrets. The chart creates no Secret resource of its own. Bump `rotationEpoch` (`--set rotationEpoch=$(date +%s)`) to force a rollout after the external secret rotates.
- **`secretValues.*`** (dev/CI only) — pass real values via `--set` or a Helm secrets plugin. `secret.yaml` hard-fails the render if any key required by `envFromSecret` (`DATABASE_URL`, `MIGRATION_DATABASE_URL`, `VALKEY_PASSWORD`, `SNS_TOPIC_ARN`, `INTERNAL_API_TOKEN`) is empty. These land in the Helm release history unencrypted (base64) — never use this mode against a shared cluster.

**Networking.** `networkPolicy.enabled: true` by default. Ingress is split by port on purpose: the gateway/ingress-controller rule only opens `8080` (nothing fronts the gRPC port externally), while an intra-namespace rule opens both `8080` and `9090` so the Execution Service can reach `GetCompiledWorkflow` directly pod-to-pod. A separate rule scopes `/metrics` scraping to the monitoring namespace only. Egress allows DNS, HTTPS (AWS APIs), Postgres/PgBouncer, Valkey, and OTel OTLP explicitly, plus same-namespace pod egress for `ORG_MEMBERSHIP_BASE_URL` and `EXECUTION_SERVICE_ADDR`.

`ingress.type` supports either `HTTPRoute` (Gateway API — default) or classic `Ingress`. CORS and gateway-level rate limiting attach via an Envoy Gateway `SecurityPolicy` (`ingress.securityPolicy`) when enabled.

**Observability.** `serviceMonitor.enabled: true` scrapes `/metrics` every 30s. Every alert in the `PrometheusRule` template (and its static twin at `deploy/monitoring/app-alerts.yml`) is verified against a metric actually emitted by this service or one of its vendored platform libraries:

- **Availability** — no healthy scrape target for 2 minutes; replica count below the HA floor.
- **HTTP errors/latency** — 5xx ratio and p99 latency (`http_requests_total`/`http_request_duration_seconds`).
- **`GetCompiledWorkflow` error rate/latency** — scoped separately from the blended gRPC rate, since this is the one RPC the Execution Service depends on synchronously.
- **BPMN validation failure rate** — sustained activity on `wf_validation_failures_total` (emitted only by the standalone `/validate` endpoint).
- **Publish latency and retry exhaustion** — p95 of `wf_publish_latency_seconds` exceeding 2s, and any increase in `pgcommon_retry_exhausted_total`.
- **Outbox health** — delivery stalls (`outbox_dead_letters_total`), backlog (`outbox_pending_total`), relay-internal errors.
- **Internal event ingest failures** — sustained `bad_payload`/`error` results on `POST /internal/events`.
- **Postgres query error rate** — `pgcommon_query_total{status="error"}`.
- **Panics** — any increase in `http_panic_total`/`grpc_panic_total`, paged immediately.
- **Compiled-plan cache hit ratio** (informational) — `wf_cache_hits_total`/`wf_cache_misses_total`, not an incident trigger.

**Row-level security violation alerting is intentionally not implemented.** There is no `rls_violations_total` metric anywhere in this service or `platform-pgcommon` — the `rls_violation_log` table is populated by DB triggers, not application code, and nothing currently exports it to Prometheus. Flagged here as a known gap rather than shipped as a dead alert.

### CI/CD deploy gate

`release.yml`'s `deploy-gate` job runs after the image is built, signed, and pushed, and before the GitHub Release is published:

1. `helm upgrade workflow-definition-service ./deploy/helm --install --wait --timeout=5m --atomic` — `--atomic` rolls back automatically if the release fails to become healthy within the timeout.
2. Verifies the running Deployment's image digest matches the digest that was actually signed and pushed.
3. Waits for `kubectl rollout status` to confirm the new pods are ready.
4. Runs a 2-minute Prometheus check on the 5xx ratio; if it exceeds 1%, `helm rollback` runs automatically and the job — and therefore the release — fails.

The GitHub Release step only runs if `deploy-gate` succeeds. **Required repository configuration** (not shipped by this chart): a `KUBECONFIG_B64` secret for the target cluster, and optionally `K8S_NAMESPACE` (defaults to `workflow-app`) and `PROMETHEUS_URL` vars in the `production` GitHub environment.

---

## Schema Governance

This service currently defines no outbound domain events, but treats every event's wire contract as a versioned, governed artifact rather than an implicit side effect of whatever the Go struct happens to look like, from the moment one is added. `platform-schemagov` — a CLI distributed as a Docker image (`ghcr.io/bcbp-solutions-fzc-llc/platform-schemagov`) — enforces this end to end: locally during development and again in CI on every push.

### Source of truth

```text
api/asyncapi.yaml   →  make extract-schemas  →  internal/eventschema/*.json
```

`api/asyncapi.yaml` is authored by hand and is the canonical description of every event this service emits, including `x-lifecycle` and `x-owner` governance annotations per message. `internal/eventschema/*.json` is a derived, *committed* artifact — one Draft-07 JSON Schema file per event — extracted from the AsyncAPI spec and checked into the repo so it can be diffed, validated, and registered independently of the Go source.

Both are tracked in git (`internal/eventschema/` is deliberately not in `.gitignore`, unlike the other generated directories in this repo) precisely because schema evolution needs its own review and audit trail, separate from application code changes.

### Local commands

```bash
make schema-pull        # pull the platform-schemagov image
make extract-schemas    # api/asyncapi.yaml → internal/eventschema/*.json
make schema-validate    # 8-pass structural/lifecycle/drift validation, no AWS required
make schema-diff CURRENT=<f> PROPOSED=<f> [SCHEMA_NAME=<name>]   # pure file-to-file diff
make schema-register     # register into AWS Glue Schema Registry (needs AWS creds or LocalStack)
make schema-prune        # report orphaned Glue schemas (EXECUTE=true to actually delete)
```

`make schema-validate` runs entirely offline against the committed files — no AWS credentials needed — which is what makes it safe to run as a fast local check or a read-only CI gate before any registry call happens.

### Schema governance environment variables

| Variable | Used by | Notes |
| --- | --- | --- |
| `GLUE_REGISTRY_NAME` | Running service (`internal/config`) **and** CI tooling | Required at runtime when `AWS_USE_STUB=false` — the service's Glue codec resolves schemas against this registry, encoding at SNS-publish time (via `events.WithCodec`) rather than at outbox-enqueue time. |
| `GLUE_REGISTRY_ARN` | CI/schema-gov tooling only | **Not read by the running service** — no corresponding field on `internal/config.Config`. It scopes IAM policy for the `schema-register`/`schema-prune` pipeline steps, not application behavior. |
| `SCHEMA_GOV_IMAGE` | `make schema-*` targets and CI | Pins the `platform-schemagov` image tag used by every schema command; not read by the server binary at all. |

### CI workflows

Four workflows implement the full lifecycle, each with a distinct, narrow responsibility:

**`schema-registry.yml` — validate, diff, register.** Triggers: PR into `main` touching schema files (`pr-check`, read-only), push to `main` (`staging`, full pipeline), a published release (`production`), or manual dispatch. The full (staging/production) pipeline: validate → check for an active schema freeze → assess event usage against CloudWatch/Prometheus (flagging events nobody has emitted, `NEVER_SEEN`, for deprecation) → diff against what's already registered (fails the run on a breaking change *before* anything is uploaded) → register (idempotent create-or-new-version) → append a dated changelog entry → emit metrics. The `pr-check` job runs only the read-only half (validate + diff against the staging registry).

**`schema-prune.yml` — retire orphaned schemas.** A schema becomes a prune candidate when it's registered in Glue but has no corresponding file in `internal/eventschema/`. Runs monthly as a dry-run report only; actually deleting requires an explicit manual dispatch with `dry_run=false` — production pruning is never scheduled automatically, since `glue:DeleteSchema` is irreversible for any consumer still pinned to a version UUID. Executed prunes archive every version definition to `docs/schema-archive/<schema-name>/` before deleting from Glue.

**`schema-health-quarterly.yml` — health review prompt.** A read-only quarterly report (version accumulation per schema, overdue deprecations, stale lifecycle annotations) surfaced as a GitHub Step Summary for a human to act on. Never mutates anything — follow-up goes through `schema-prune.yml` or a normal schema PR.

**`freeze-watchdog.yml` — guard against a forgotten freeze.** `SCHEMA_FREEZE` blocks registration (used during incident response or planned migrations). This workflow polls its age every few hours and escalates from a warning to a hard failure if left on far longer than any real freeze window should last.

### Governance guardrails

- **CODEOWNERS**: changes to `api/asyncapi.yaml` and `internal/eventschema/` require review from the platform-engineers/platform-team owners.
- **Drift gate**: `validate-test.yml` runs `extract-schemas --check` on every push — if the committed JSON Schema files don't match what `api/asyncapi.yaml` would currently produce, CI fails.
- **Breaking-change gate**: the `diff` step in `schema-registry.yml` fails the pipeline before any registration if a proposed schema isn't backward-compatible with what's already live.

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
| Events | Outbound: AWS SNS via `platform-events` transactional outbox (stub available). Inbound: HTTP `POST /internal/events` from the shared workflow-events consumer |
| gRPC | `google.golang.org/grpc` · buf toolchain |
| Observability | OTel traces · Prometheus metrics · Zap structured logs |
| Testing | GoMock · race detector · `testcontainers-go` (integration) |

---

## Testing

| Directory | What | Command |
| --------- | ---- | ------- |
| `internal/**/*_test.go` | White-box unit tests (unexported access) | `make test` |
| `test/unit/<pkg>/` | Black-box unit tests (exported API only) | `make test` |
| `test/integration/` | DB integration tests against a real PostgreSQL container | `make test-integration` |

Integration tests require Docker. Pre-pull the image once:

```bash
make tools-integration   # docker pull postgres:18-alpine
make test-integration
```

Coverage gate: **95%** on unit tests. The postgres repo adapter (`internal/adapter/outbound/postgres/`) and generated packages (`postgres/db/`, `core/port/mocks/`) are excluded from the unit gate and covered by integration tests instead. See [CONTRIBUTING.md § Testing](CONTRIBUTING.md#testing) for the full test-authoring conventions (table-driven tests, white-box vs black-box placement, mocking).

---

## Common pitfalls

**`OutboxRepository.Enqueue` outside a transaction**

`Enqueue` checks that a `pgx.Tx` is present in the context and returns an error otherwise. Always call it inside `Transactor.RunInTx`:

```go
// Correct — business write and outbox commit atomically:
s.transactor.RunInTx(ctx, func(ctx context.Context) error {
    s.versionRepo.Publish(ctx, ...)
    return s.outboxRepo.Enqueue(ctx, envelope) // same transaction
})

// Wrong — Enqueue will return an error:
s.versionRepo.Publish(ctx, ...)
s.outboxRepo.Enqueue(ctx, envelope) // no transaction in context
```

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
| `workflow` | Root template entity — business key, name, active version pointer |
| `workflow_version` | Versioned snapshot — BPMN XML, compiled DSL, status lifecycle |
| `workflow_node_assignee` | Denormalised reverse index: user → versions that reference them as default assignees |
| `outbox_events` | Transactional event queue for SNS delivery (published_at = NULL → NOW()) |
| `outbox_dead_letters` | Failed events that exhausted max attempts |
| `processed_event` | Inbound-event idempotency registry — composite PK `(event_id, consumer)`, no RLS |

See full DDL in [`db/migrations/`](db/migrations/).

### Query patterns

Query definitions in `db/queries/` are compiled to type-safe Go by [sqlc](https://sqlc.dev). Generated output goes to `internal/adapter/outbound/postgres/db/`. Repository adapters hold a `*pgcommon.Pool` value and use transaction/connection helpers (`WithConn`, `RunInTx`) to run sqlc-generated queries. Outbox enqueuing writes `outbox_events` through the same transaction context via `outbox.Enqueue(ctx, tx, env)`; the background delivery relay is handled entirely by the `platform-events` outbox runner.

```bash
make generate          # runs buf generate (proto) AND sqlc generate (queries)
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
| `workflow` | `UNIQUE (tenant_id, business_key)` | No duplicate keys per tenant |
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
| `PG_BOUNCER_MODE` | `true` when `DATABASE_URL` points to PgBouncer. Switches pgx to simple protocol + transaction-local GUC/RLS injection. | `false` |
| `MIGRATION_DATABASE_URL` | Direct Postgres DSN for the `migrate` subcommand. Required when `DATABASE_URL` is a PgBouncer URL — `golang-migrate` uses session advisory locks that PgBouncer transaction pooling drops. Falls back to `DATABASE_URL` when unset. | *(unset)* |
| `DATABASE_FALLBACK_URL` | Startup-only fallback DSN (direct Postgres). Tried once at boot if the primary pool fails — covers PgBouncer not yet ready during rolling deploys. Not used for runtime reconnection. | *(unset)* |

### Local dev with PgBouncer

```bash
make docker-up   # starts postgres (5432) + pgbouncer (6432)

# Migrations always run direct — never through PgBouncer (advisory locks)
DATABASE_URL=postgres://wfdef:wfdef@localhost:5432/workflow_definition?sslmode=disable \
  go run ./cmd/server migrate

# Service via PgBouncer — update .env:
#   DATABASE_URL=postgres://wfdef:wfdef@localhost:6432/workflow_definition?sslmode=disable
#   PG_BOUNCER_MODE=true
#   PG_MIN_CONNS=0
go run ./cmd/server

# Verify PgBouncer is active
psql "postgresql://wfdef:wfdef@localhost:6432/pgbouncer" -c "SHOW POOLS;"
```

### Production setup

| K8s resource | `DATABASE_URL` | `MIGRATION_DATABASE_URL` | `PG_BOUNCER_MODE` |
| --- | --- | --- | --- |
| Service Deployment | `pgbouncer-svc:6432` | *(unset)* | `true` |
| Migration init container | `pgbouncer-svc:6432` | `rds-endpoint:5432` | `false` |

- **TLS** — PgBouncer must connect to RDS with `server_tls_sslmode=require`; `sslmode=disable` is only safe inside the cluster VPC with mesh mTLS.
- **HA** — PgBouncer is stateless; run ≥2 replicas behind a k8s Service. `DATABASE_FALLBACK_URL` covers startup races only — it does not provide runtime failover.
- **Shared PgBouncer** — one PgBouncer deployment serves all microservices. Scale `MAX_CLIENT_CONN` as you add services or pods.
