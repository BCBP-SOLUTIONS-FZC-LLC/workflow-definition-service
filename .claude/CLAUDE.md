# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## What This Repo Is

`workflow-definition-service` is a Go HTTP + gRPC microservice (module: `github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service`, Go 1.26) that acts as the design-time control plane for the BPMN-driven Workflow Engine. It parses, validates, versions, and serves workflow templates.

Key responsibilities: BPMN ingestion, structural/semantic/topological validation, DSL compilation, DRAFT → PUBLISHED → ARCHIVED versioning, serving compiled plans to the Execution Service over gRPC, and emitting domain events via a transactional outbox (SNS).

---

## Platform Libraries

The three BCBP platform libraries are private Go modules hosted on GitHub. They are fetched at build time via `GOPRIVATE` — **never** reference or import them from local directories.

| Module | Version | Purpose |
| --- | --- | --- |
| `github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events` | v1.3.0 | SNS publisher, transactional outbox runner + schema, typed event envelopes |
| `github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon` | v1.1.1 | pgx pool, RLS GUC injection, transactor, `migrate.Runner` (golang-migrate) |
| `github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon` | v1.2.0 | Gin middleware, OTel tracing init, Zap logger, gRPC middleware |

**How to fetch / upgrade:**

```bash
export GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*   # must be set in every shell
go get github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon@v1.2.0
go mod tidy
go mod vendor
```

**Critical rules — never break these:**

- **Never add a `replace` directive** pointing to `./platform-libs/` in `go.mod`. That directory is gitignored and does not exist in CI.
- **Never import from `./platform-libs/`** in source code. The import path must always be the full `github.com/BCBP-SOLUTIONS-FZC-LLC/...` module path.
- **`platform-libs/`** is a local read-only reference copy only. **`.design/`** is the same: design documents (HLD, LLD, DBML) for reference.

---

## Common Commands

```bash
# First-time setup
export GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*  # required before go get / mod tidy
make tools                  # install sqlc, buf, mockgen, golangci-lint, go-arch-lint
make setup                  # copy .env.example → .env and install .githooks/pre-commit
make docker-up              # start PostgreSQL + Valkey + LocalStack + PgBouncer
make migrate                # apply schema (outbox + domain) — NOT run at server boot

# Development cycle
make generate               # buf (proto) + sqlc (queries)
make mock                   # regenerate GoMock stubs
make build                  # compile bin/server
go run ./cmd/server         # run locally (Swagger UI at /swagger/ in dev mode)

# Testing
make tools-integration      # docker pull postgres:18-alpine (one-time)
make test                   # unit tests + race detector
make test-integration       # integration tests (testcontainers)
make test-ci                # unit + integration, merged coverage — what CI runs
make cover-html             # open HTML coverage report

# Code quality
make arch-lint              # go-arch-lint: enforce import direction rules
make lint                   # golangci-lint (read-only)
make fix                    # gofmt + golangci-lint --fix (auto-fix formatting and lint)
make check                  # gofmt check + lint + vet + arch-lint + test + coverage gate (full local CI, read-only)

# Container checks (run 'make tools' first to install hadolint + trivy)
make docker-lint            # lint Dockerfile with Hadolint (native binary, no image needed)
make docker-trivy           # scan source/deps for HIGH/CRITICAL CVEs (trivy fs, no image needed)
make docker-check           # docker-lint + docker-trivy (no GO_PRIVATE_TOKEN needed)
make docker-build           # build service image (requires GO_PRIVATE_TOKEN in env)
```

---

## Architecture

Clean Architecture — dependency direction: `domain ← port ← service ← adapter`.

```text
cmd/server/             ← bootstrap + DI (no business logic)
internal/
  core/
    domain/             ← entities, sentinel errors, BPMN error codes
    port/               ← interface contracts (Transactor, OutboxRepository, etc.)
    service/            ← business logic (imports domain + port only)
  adapter/
    inbound/
      http/             ← Gin handlers, authz middleware
      grpc/             ← GetCompiledWorkflow impl
    outbound/
      postgres/         ← sqlc + pgcommon repo adapters
      valkey/           ← CacheStore impl
  bpmn_compiler/        ← stateless parser, validator, DSL compiler
  config/               ← env var loading
test/
  fixtures/             ← shared testcontainers helper (NewTestPool)
  unit/                 ← pure unit tests by package
  integration/          ← DB integration tests (testcontainers-go)
```

**Import rules:** `core/domain` → stdlib only. `core/port` → domain only. `core/service` → domain + port. `adapter/*` → port + domain. Nothing in `core/` imports from `adapter/`. Enforced by `go-arch-lint` (`.go-arch-lint.yml`).

`cmd/server/app.go` starts: HTTP (`:8080`), gRPC (`:9090`), and the outbox relay. All stop gracefully on OS signal with a 30-second timeout.

---

## Code Generation

```bash
make generate        # must re-run after editing: api/proto/*.proto or db/queries/*.sql
make mock            # must re-run after changing core/port/ interfaces
```

Generated files are gitignored: `gen/`, `internal/adapter/outbound/postgres/db/`, `internal/core/port/mocks/`. CI `generate` job produces these and uploads them as an artifact consumed by all downstream jobs.

---

## Test Layout

| Directory | Content | Command |
| --- | --- | --- |
| `internal/**/*_test.go` | White-box unit tests (unexported access) | `make test` |
| `test/unit/<pkg>/` | Black-box unit tests (exported API only) | `make test` |
| `test/integration/postgres/` | DB integration tests (testcontainers, real Postgres) | `make test-integration` |
| `test/fixtures/` | Shared `NewTestPool` helper | — |

Integration tests require Docker. Pre-pull the image once: `make tools-integration`.

---

## Key Design Decisions

Detailed reference is in sub-documents — load the relevant one for your task:

- **[.claude/database.md](database.md)** — DB patterns, transactor, outbox, sqlc, schema decisions (#1–6, #11, #19)
- **[.claude/api-and-events.md](api-and-events.md)** — HTTP/gRPC patterns, idempotency, events (#9, #10, #12–18)
- **[.claude/bpmn-compiler.md](bpmn-compiler.md)** — BPMN parsing, validation, compilation (#20–25)
- **[.claude/operations.md](operations.md)** — CI/CD, Prometheus metrics, OTel, MkDocs, platform-lib constraints (#26)
- **[.claude/development-guide.md](development-guide.md)** — extension cookbook, troubleshooting, and the full HTTP error-code appendix

### Handler layer (#7 — always relevant)

Handlers define local response structs with snake_case JSON tags and always map through DTO helpers (`toWorkflowResp`, etc.) — never serialize domain structs directly. `Handler` depends on unexported interfaces (`workflowSvc`, `draftSvc`, etc.) defined in `handler.go`; tests provide hand-rolled fakes from `testhelper_test.go` (no mockgen at the handler layer). JSON bind failures → 400 `BAD_REQUEST`; missing `RequestContext` → 500 (misconfigured route, not auth failure).

### Handler tests (#8 — always relevant)

`gincommon.RequestContext(c)` is a type assertion to an internal gincommon type that tests cannot construct directly. Handler tests must use `gincommon.ProtectedMiddlewares` with injected `x-tenant-id` / `x-user-id` headers. See `test/unit/handler/` for the established pattern.
