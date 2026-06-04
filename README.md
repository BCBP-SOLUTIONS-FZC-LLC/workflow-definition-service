# Workflow Definition Service

Design-time control plane for the BPMN-driven Workflow Engine. Parses, validates, versions, and serves workflow templates built on BPMN 2.0.

> **Full documentation** — `make docs-serve` → [http://localhost:8001](http://localhost:8001)

---

## Quick start

```bash
# 1. Install dev tooling
make tools

# 2. Configure environment
cp .env.example .env

# 3. Start local infra (PostgreSQL 16 + Valkey 8)
make docker-up

# 4. Run migrations
make migrate-up

# 5. Start the server (AWS stubs active by default)
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
| `make generate` | buf generate (proto) + sqlc generate (queries) |
| `make mock` | Regenerate GoMock stubs for `core/port` interfaces |
| `make migrate-up` | Apply pending Goose migrations |
| `make migrate-down` | Roll back last migration |
| `make build` | Compile binary to `bin/server` |
| `make test` | Unit tests with race detector |
| `make test-integration` | Integration tests (requires running infra) |
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
  outbox/                ← background relay worker
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
| Database | PostgreSQL 16 · pgx/v5 · sqlc · Goose |
| Cache / locks | Valkey 8 · go-redis/v9 |
| Events | AWS SNS + SQS · `aws-sdk-go-v2` (stub available) |
| gRPC | `google.golang.org/grpc` · buf toolchain |
| Observability | OTel traces · Prometheus metrics · Zap structured logs |
| Testing | GoMock · race detector · integration build tag |

---

## Environment variables

See [`.env.example`](.env.example) or the [Configuration docs](docs/configuration.md) for the full reference.

