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

> **Architecture doc** — [ARCHITECTURE.md](ARCHITECTURE.md) — layer model, sequence diagrams, config reference, and error catalog.
>
> **Full documentation** — `make docs-serve` → [http://localhost:8001](http://localhost:8001)

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

Verify:

```bash
curl http://localhost:8080/healthz   # {"status":"OK"}
curl http://localhost:8080/metrics   # Prometheus exposition
```

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
| `make schema-validate` | Validate `internal/eventschema/*.json` against `api/asyncapi.yaml` (no AWS required) — see [Schema Governance](docs/schemagov.md) |
| `make schema-register` / `make schema-prune` | Register/retire event schemas in AWS Glue Schema Registry |
| `make docker-build` / `make docker-lint` / `make docker-trivy` | Build the container image, lint the Dockerfile, scan for CVEs |
| `make docs-serve` | Live-reload docs at <http://localhost:8001> |
| `make docs-build` | Build static MkDocs site to `site/` |
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
    service/             ← business logic
  adapter/
    inbound/
      http/              ← Gin handlers, authz, middleware, POST /internal/events
      grpc/              ← GetCompiledWorkflow server
    outbound/
      postgres/          ← sqlc DB layer + repo adapters
      valkey/            ← Valkey cache adapter
  bpmn_compiler/         ← XML parser, validator, DSL compiler
  config/                ← env var loading
api/                     ← REST (OpenAPI), AsyncAPI specs, and .proto source files (committed)
gen/                     ← buf-generated stubs (gitignored)
db/
  migrations/            ← golang-migrate SQL migrations (.up.sql/.down.sql, committed)
  queries/               ← sqlc query definitions (committed)
docs/                    ← MkDocs pages
```

---

## Deployment

A container image (`Dockerfile`, distroless nonroot runtime) and a Helm chart (`deploy/helm/`) ship the service to Kubernetes — Deployment/Service on ports `8080` (HTTP) / `9090` (gRPC), a migration Job that runs before every rollout (there is no auto-migration at server boot), HPA/PodDisruptionBudget sized for this service's own resource profile, NetworkPolicy, and a ServiceMonitor/PrometheusRule pair covering availability, error rate, latency, BPMN validation failure rate, publish latency, outbox delivery stalls, and RLS violations. `release.yml`'s `deploy-gate` job deploys, verifies, and health-gates every tagged release before it's published.

See [Deployment](docs/deployment.md) for the full reference.

## Schema Governance

Outbound event contracts (`api/asyncapi.yaml` → `internal/eventschema/*.json`) are validated, diffed for breaking changes, and registered in AWS Glue Schema Registry via `platform-schemagov`, both locally (`make schema-validate`, `make schema-register`) and in CI (`schema-registry.yml`, `schema-prune.yml`, `schema-health-quarterly.yml`, `freeze-watchdog.yml`).

See [Schema Governance](docs/schemagov.md) for the full reference.

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

Coverage gate: **95%** on unit tests. The postgres repo adapter (`internal/adapter/outbound/postgres/`) and generated packages (`postgres/db/`, `core/port/mocks/`) are excluded from the unit gate and covered by integration tests instead.

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

See [`.env.example`](.env.example) or the [Configuration docs](docs/configuration.md) for the full reference.
See [ARCHITECTURE.md](ARCHITECTURE.md#configuration-reference) for the complete table with defaults.

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
