# Workflow Definition Service

Design-time control plane for the BPMN-driven Workflow Engine. Parses, validates, versions, and serves workflow templates built on BPMN 2.0.

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
| `make cover-check` | Fail if coverage < 70% |
| `make lint` | Run golangci-lint |
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

## Environment variables

See [`.env.example`](.env.example) or the [Configuration docs](docs/configuration.md) for the full reference.
