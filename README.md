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

# 3. Configure environment
cp .env.example .env

# 4. Start local infra (PostgreSQL 16 + Valkey 8)
make docker-up

# 5. Run migrations
make migrate-up

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
| `make tools` | Install sqlc, goose, buf, mockgen, golangci-lint into `.tools/` |
| `make tools-integration` | Pre-pull Docker images used by integration tests (testcontainers-go) |
| `make generate` | buf generate (proto) + sqlc generate (queries) |
| `make mock` | Regenerate GoMock stubs for `core/port` interfaces |
| `make migrate-up` | Apply pending Goose migrations |
| `make migrate-down` | Roll back last migration |
| `make build` | Compile binary to `bin/server` |
| `make test` | Unit tests with race detector and coverage (internal + test/unit) |
| `make test-integration` | Integration tests (spins up Docker containers via testcontainers-go automatically; requires running Docker) |
| `make cover` | Unit tests + coverage summary |
| `make cover-html` | Open HTML coverage report |
| `make cover-check` | Fail if coverage < 95% (postgres adapter + generated pkgs excluded) |
| `make lint` | Run golangci-lint |
| `make vuln` | Run govulncheck for known dependency vulnerabilities |
| `make docs-serve` | Live-reload docs at <http://localhost:8001> |
| `make docs-build` | Build static MkDocs site to `site/` |
| `make docker-up` | Start PostgreSQL + Valkey |
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
      http/              ← Gin handlers, authz, middleware
      grpc/              ← GetCompiledWorkflow server
      sqs/               ← membership revocation consumer
    outbound/
      postgres/          ← sqlc DB layer + repo adapters
      sns/               ← SNS publisher (+ stub)
  bpmn_compiler/         ← XML parser, validator, DSL compiler
  config/                ← env var loading
proto/                   ← .proto source files (committed)
gen/                     ← buf-generated stubs (gitignored)
db/
  migrations/            ← Goose SQL migrations (committed)
  queries/               ← sqlc query definitions (committed)
docs/                    ← MkDocs pages
```

---

## Tech stack

| | |
| --- | --- |
| Language | Go 1.26 |
| HTTP | Gin + `platform-gincommon` (OTel, Prometheus, Zap, auth) |
| Database | PostgreSQL 16 · pgx/v5 · sqlc · Goose · `platform-pgcommon` (pool & RLS) |
| Cache / locks | Valkey 8 · go-redis/v9 |
| Events | AWS SNS + SQS via `platform-events` (stub available) |
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
make tools-integration   # docker pull postgres:16-alpine
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
