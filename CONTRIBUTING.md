# Contributing to workflow-definition-service

Thank you for contributing! This guide covers the local development workflow, testing expectations, and team conventions.

---

## Prerequisites

| Tool | Install |
| ------ | --------- |
| Go 1.26+ | [go.dev/dl](https://go.dev/dl) |
| Docker (for integration tests) | [docs.docker.com](https://docs.docker.com/get-docker/) |
| Private module access | See below |

### Private module access

This service depends on internal platform libraries:

```bash
# Configure Go to bypass the public proxy for BCBP-SOLUTIONS-FZC-LLC modules.
go env -w GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*

# SSH (recommended for local development):
git config --global url."ssh://git@github.com/".insteadOf "https://github.com/"
```

CI uses `GO_PRIVATE_TOKEN` (GitHub PAT with `repo:read` scope) — see the secrets in the repository settings.

---

## First-time Setup

```bash
make tools                  # install sqlc, buf, mockgen, golangci-lint to .tools/
make setup                  # copy .env.example → .env and install .githooks/pre-commit
make docker-up              # start PostgreSQL on :5432, Valkey on :6379
make migrate                # apply DB schema migrations (run once, before server start)
make generate                # buf (proto → gen/) + sqlc (queries → postgres/db/)
make mock                   # regenerate GoMock stubs for port interfaces
make build                  # compile bin/server
go run ./cmd/server         # run locally
```

`make setup` installs `.githooks/pre-commit`, which runs `make tidy`, `fmt-check`, `lint`, and `arch-lint` before every commit. Re-run `make install-hooks` any time `.githooks/pre-commit` itself changes (the installed copy in `.git/hooks/` is not auto-synced).

See [README.md § Local Development](README.md#local-development) for the AWS-stub and LocalStack walkthrough.

---

## Development Cycle

```bash
# After editing api/proto/*.proto:
make generate-proto

# After editing db/queries/*.sql or db/schema/*.sql:
make generate-sqlc

# After editing internal/core/port/*.go interfaces:
make mock
```

---

## Testing

| Command | What it runs |
| --------- | ------------- |
| `make test` | Unit tests with race detector (`./internal/...` + `./test/unit/...`) |
| `make test-integration` | Integration tests (testcontainers-go; Docker required) |
| `make cover` | Unit tests + coverage summary |
| `make cover-func` | Per-function coverage (CI-compatible format) |
| `make cover-html` | Open HTML coverage report in browser |
| `make cover-check` | Fail if coverage < 95% threshold |

**Coverage target:** 95% threshold (CI gate) — aim for 97–98% as the sustained goal.

### Test structure

```sh
test/
  unit/<pkg>/       Black-box unit tests (exported API only)
  integration/      DB integration tests (testcontainers, real Postgres)
  e2e/              Service-layer end-to-end tests (full business flows, real containers, no HTTP server)
  fixtures/         Shared NewTestPool / NewTestValkey / NewLocalStackSNSSQS helpers
internal/**/*_test.go  White-box tests (need unexported access)
```

- White-box tests live next to their package when they need unexported symbols. They **cannot** be moved to `test/unit/` — Go's visibility rules would break the build.
- All other tests go under `test/unit/<pkg>/` or `test/integration/`.
- Do not remove `t.Skip("not yet implemented")` placeholder tests without implementing the body. Placeholder files in `test/unit/` for packages that already have white-box tests in `internal/` should call `t.Skip(...)` and explain the location:

  ```go
  // Real tests are white-box and live in internal/adapter/outbound/postgres/mapping_test.go
  func TestMapping(t *testing.T) { t.Skip("covered by white-box tests in internal/") }
  ```

### Integration test image

Pre-pull the Postgres image once so `testcontainers-go` doesn't download it on every run:

```bash
make tools-integration      # docker pull postgres:18-alpine
```

Integration tests carry a `//go:build integration` build tag. `make test` runs only `./internal/... ./test/unit/...`; `make test-integration` runs `./test/integration/... ./test/e2e/...` with `-tags integration`. CI runs both in the same `validate-test.yml` `test` job (`make test-ci`) — no separate integration-test job.

### Unit test conventions

**Table-driven tests.** All unit tests must be table-driven, even single-case scenarios:

```go
func TestPublishVersion(t *testing.T) {
    tests := []struct {
        name    string
        // inputs and expected outputs
        wantErr error
    }{
        {name: "happy path", ...},
        {name: "draft not found", wantErr: domain.ErrNoDraftExists},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // arrange: build mocks via GoMock
            // act
            // assert
        })
    }
}
```

**Exception — panic tests.** A test that verifies a `panic` must not be looped. The `defer/recover` runs once per function; iterating would skip recovery for all cases after the first panic. Keep panic assertions as separate, non-looped functions:

```go
func TestMustUUID_InvalidPanics(t *testing.T) {
    defer func() {
        if r := recover(); r == nil {
            t.Error("expected panic, got none")
        }
    }()
    mustUUID(pgtype.UUID{}) // panics — recover fires, test passes
}
```

**White-box vs black-box placement**

| Test type | Package declaration | Location | When to use |
| --- | --- | --- | --- |
| White-box | `package foo` | `internal/…/foo_test.go` | Needs unexported identifiers (private functions, unexported types, `txContextKey{}`, etc.) |
| Black-box | `package foo_test` | `test/unit/foo/` | Tests only the exported API; no access to internals needed |

### Unit test coverage

The CI `validate-test.yml` job enforces a **95% minimum** against the merged unit + integration coverage profile (`make test-ci`). The coverage denominator **excludes** packages that are either generated or require a real database:

| Excluded package | Reason |
| --- | --- |
| `internal/adapter/outbound/postgres/db/` | sqlc-generated code |
| `internal/adapter/outbound/postgres/` | repo adapters require a real Postgres; covered by the integration suite |
| `internal/core/port/mocks/` | GoMock-generated stubs |

`make test` and `make test-integration` each post-filter their own profile (`.coverage/unit.out`, `.coverage/integration.out`) to enforce these exclusions; `make merge-coverage` then combines both (max-count-per-block, via `scripts/merge_coverage.py`) into `.coverage/coverage.out` before the global gate runs.

We target 95%+ unit test coverage for all fully implemented components, including configuration parsing (`internal/config`), the Valkey cache adapter (mockable `redis.Cmdable`, no live instance needed), the gRPC server, the internal events handler, and the execution/membership outbound clients (real local gRPC server / URL-encoding edge cases).

### Integration and E2E tests

Integration tests live in `test/integration/` and use **testcontainers-go** to spin up a real PostgreSQL container automatically — no `make docker-up` needed.

`test/e2e/` contains service-layer end-to-end tests that exercise full business flows with real containers — no HTTP server, no stubs. These are distinct from the repo-level integration tests in `test/integration/postgres/`:

| Test | What it covers | Containers |
| --- | --- | --- |
| `TestE2E_WorkflowLifecycle` | Full state machine: Create → Update → Publish → Promote → InitDraft → Publish → Archive | Postgres |
| `TestE2E_RLSCrossTenantIsolation` | Row-Level Security: tenant A cannot read tenant B's rows | Postgres |
| `TestE2E_Promote_EndToEnd` | Promote emits SNS event with `promoted_from_version_id` set | Postgres + LocalStack |
| `TestE2E_SNSFilterPolicies_QueueRouting` | SNS filter routes `workflow.template.published` to correct queue | Postgres + LocalStack |
| `TestE2E_Valkey_CacheIntegration` | `Archive()` invalidates the compiled-plan cache entry | Postgres + Valkey |

```bash
make tools-integration   # docker pull postgres:18-alpine + localstack:3 + valkey:8-alpine
make test-integration    # runs test/integration/... and test/e2e/...

# individual:
go test -v -tags integration ./test/e2e/... -run TestE2E_WorkflowLifecycle
```

The e2e tests are **distinct** from `scripts/smoke-test.sh` (full HTTP + gRPC against a live server, manual pre-merge, covering route/response sanity rather than business logic).

### Mock generation

Mocks for all `core/port` interface files are generated by GoMock and written to `internal/core/port/mocks/` (gitignored):

```bash
make mock
# writes mocks for: repository.go, publisher.go, cache.go, services.go, transactor.go, glue.go
```

```go
import (
    "go.uber.org/mock/gomock"
    "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
)

ctrl := gomock.NewController(t)
repo := mocks.NewMockWorkflowRepository(ctrl)
repo.EXPECT().GetByID(gomock.Any(), tenantID, workflowID).Return(wf, nil)
```

**Hand-rolled fakes for adapter-layer dispatch.** The HTTP handler layer (`test/unit/handler/`) uses hand-rolled fakes instead of GoMock. These handlers each depend on a single unexported interface (`membershipRevoker`, `workflowSvc`, etc.) making GoMock unnecessary:

```go
// test/unit/handler/internal_events_test.go
type fakeMembershipRevoker struct { err error }
func (f *fakeMembershipRevoker) HandleMembershipRevoked(...) error { return f.err }
```

Use GoMock for service-layer tests that orchestrate multiple dependencies; use hand-rolled fakes for adapter dispatch code with a single interface dependency.

**Shared test helpers (`test/unit/testkit`).** `test/unit/testkit/` is a non-test package importable by any `_test` package in `test/unit/` — e.g. `testkit.FakeLogger`, a no-op `port.Logger`:

```go
import "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"

grpcadapter.NewServer(testkit.FakeLogger{}, repo)
```

### End-to-end smoke testing

`scripts/smoke-test.sh` covers all 13 HTTP flow groups, the gRPC `GetCompiledWorkflow` endpoint, and the `DepartmentMembershipRevoked` ingest via `POST /internal/events` — including error cases and idempotency checks. See [README.md § Local Development](README.md#local-development) for the stub-binary setup this script depends on. Optional tools (`grpcurl`, `psql`, `aws` CLI) unlock additional assertions; the script exits `0` on full pass or `1` with a summary of failures.

---

## Database Migrations

Migrations use **golang-migrate** via `platform-pgcommon`'s `migrate.Runner` (embedded in `db/migrations/migrations.go`, `//go:embed *.sql`). Each migration is a pair of files, `NNNNNN_name.up.sql` and `NNNNNN_name.down.sql` (no `+goose` annotations):

```sql
-- 000009_add_foo.up.sql
ALTER TABLE workflow ADD COLUMN foo TEXT;
```

```sql
-- 000009_add_foo.down.sql
ALTER TABLE workflow DROP COLUMN foo;
```

`runMigrations` first calls `outbox.ApplySchema` to create the outbox tables (tracked under `pgcommon_migrations`), then runs the service domain runner over `db/migrations/` (tracked under `wf_definition_migrations`) — kept separate so the outbox and domain histories never collide.

**Rules:**

- Both an `.up.sql` and a `.down.sql` are required; test the down path too.
- The pgx/v5 driver runs each file as a single statement, so `$$`-quoted PL/pgSQL functions need no special wrapping (unlike goose).
- Run `make migrate` (or `go run ./cmd/server migrate`) before starting the server. The integration test fixture runs migrations internally via the embedded `db/migrations/migrations.go`. There is **no** auto-migration at server boot — see [ARCHITECTURE.md § platform-pgcommon](ARCHITECTURE.md#platform-pgcommon-pool-rls-guc-injection-transactor-migrations) for why.
- Migrations that lock large tables (e.g. `ADD COLUMN NOT NULL DEFAULT`) must be split into safe steps (add nullable → backfill → add constraint).
- Never modify an already-applied migration file — create a new one.
- The `record_version` trigger: `workflow` and `workflow_version` carry `record_version BIGINT NOT NULL DEFAULT 1`, bumped together with `updated_at` by the `update_meta_columns` BEFORE UPDATE trigger, guarded by `WHEN (OLD.* IS DISTINCT FROM NEW.*)` so a no-op update changes neither.

---

## Code Generation

**Never edit generated files manually.** They are gitignored and regenerated in CI.

| Generated output | Source | Command |
| ----------------- | -------- | --------- |
| `gen/proto/` | `api/proto/*.proto` | `make generate-proto` |
| `internal/adapter/outbound/postgres/db/` | `db/queries/*.sql` + `sqlc.yaml` | `make generate-sqlc` |
| `internal/core/port/mocks/` | `internal/core/port/*.go` | `make mock` |

---

## Coding Standards

### Formatting

- **Line length**: 100 characters max.
- **Import groups**: exactly three blocks — standard library, third-party, internal — separated by a blank line:

  ```go
  import (
      "context"
      "fmt"
      "time"

      "github.com/google/uuid"
      "go.uber.org/multierr"

      "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
      "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
  )
  ```

- **Gofmt/goimports**: enforced by `golangci-lint` on every commit.
- **File length**: no hard limit; split a file when it becomes structurally unwieldy (mixes unrelated domains, or mixes core models with adapter logic).

### Function & method design

- Keep cognitive complexity below 15 (check with a SonarQube linting plugin before committing). No strict line-of-code limit, but evaluate functions over ~80 lines for decomposition.
- Group all methods on a struct consecutively in the same file; sort alphabetically or by visibility (public first, then the private helpers they invoke).

### Naming

- `camelCase` for unexported symbols, `PascalCase` for exported symbols.
- Initialisms fully capitalised: `userID`, `tenantID`, `workflowXML`, `GetWorkflowByID` — not `userId`, `workflowXml`, `GetWorkflowById`.

### Comments

Prioritize self-documenting code over comments. Don't write a comment unless it's necessary — rely on descriptive names and clear control flow. When a block is genuinely obscure (complex graph operations, DB locking hints, reflection), write a brief comment explaining *why*, with a pointer to the relevant design doc if one exists.

### Logging

All service and handler code uses the `port.Logger` interface with `map[string]any` fields — never format values into the message string:

```go
// Correct
log.Error("publish failed", map[string]any{
    "error":     err.Error(),
    "tenant_id": tenantID.String(),
})

// Wrong — never format into the message string
log.Error(fmt.Sprintf("publish failed for tenant %s: %v", tenantID, err), nil)
```

`app.go` constructs the logger via `logger.NewLogger(cfg.AppEnv)` and passes the same `port.Logger` value into `gincommon.Config{Logger: log}` and all service/handler constructors.

### Error handling

- Wrap errors at boundary crossings: `fmt.Errorf("publish workflow: %w", err)`.
- Define sentinel errors in `core/domain/errors.go`; check with `errors.Is`.
- HTTP handlers map domain errors to RFC-9457 ProblemDetails responses.

### Architecture rules

Import direction is enforced by `go-arch-lint` (runs in CI lint):

```sh
core/domain  →  stdlib only
core/port    →  domain only
core/service →  domain + port
adapter/*    →  port + domain
cmd/server   →  all of the above
```

**Nothing in `core/` imports from `adapter/`.** Violating this fails CI. Additionally:

- All external I/O (DB, cache, HTTP, gRPC) crosses through a `port` interface.
- Service and handler constructors accept interfaces, not concrete types.
- Repository adapter constructors (`NewWorkflowRepo`, etc.) accept `*pgcommon.Pool` — the sqlc/pgcommon pattern is an intentional exception to the interface rule at the DB adapter layer.
- `app.go` is the only file that wires concrete adapters to interfaces.

### BPMN compilation conventions

- A userTask's department is derived from its **lane membership** (`<bpmn:flowNodeRef>`), never a `dept_id` Zeebe property.
- Stage type comes from **`zeebe:taskDefinition type`**, resolved against the `StageTypeHandler` registry (not hardcoded).
- Role and default assignees come from **`zeebe:assignmentDefinition`** (`candidateGroups` → role, `candidateUsers` → default assignees).
- **XOR gateways + `<bpmn:conditionExpression>`** are for routing decisions; **error boundary events on subprocesses** are for stage rejection / rework flows.
- The parser enforces namespace declarations (`MISSING_NAMESPACE`) and the Tier-3 element denylist (`REJECTED_ELEMENT`) on the raw XML token stream — unknown elements are dropped by `encoding/xml`, so the denylist scan must happen before unmarshal.

See [ARCHITECTURE.md § BPMN Compiler](ARCHITECTURE.md#bpmn-compiler) for the full element reference and validation rule catalog.

---

## Documentation update checklist

Keep docs in sync when making changes:

| Change type | Docs to update |
| --- | --- |
| New HTTP endpoint or field | `README.md` § API Overview |
| New gRPC method | `README.md` § API Overview |
| New config env var | `ARCHITECTURE.md` § Configuration reference |
| New migration or schema change | `README.md` § Database, this file § Database Migrations |
| Architecture / layer change | `ARCHITECTURE.md` + `docs/architecture/mermaid/*.mmd` |
| Platform lib upgrade | `ARCHITECTURE.md` § Platform Libraries + `.claude/CLAUDE.md` version table + `.claude/operations.md` |
| New coding pattern or decision | This file § Coding Standards + numbered entry in `.claude/CLAUDE.md` + relevant `.claude/*.md` sub-doc |
| BPMN element / validation rule change | `ARCHITECTURE.md` § BPMN Compiler + `.claude/bpmn-compiler.md` |

---

## Dependency Update Policy

### Platform-libs

`github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events`, `platform-pgcommon`, and `platform-gincommon` **must be updated explicitly** — do not rely on Dependabot for these. When a new platform-libs version is released:

1. `go get github.com/BCBP-SOLUTIONS-FZC-LLC/<lib>@<new-tag>`
2. `go mod tidy`
3. Run the full test suite
4. Open a PR with title `chore: upgrade <lib> to <new-tag>`

### All other Go dependencies

Dependabot opens weekly PRs for non-platform-libs dependencies grouped as patch/minor. Before merging any dependency PR:

- Verify the dependency version has been available for **at least 2 weeks** (check the version's release date on pkg.go.dev). This avoids pulling in recently-introduced regressions.
- Check `govulncheck` passes on CI for the PR.
- Do not merge dependency PRs on Fridays.

---

## PR Checklist

Use the PR template (`.github/pull_request_template.md`) as your checklist. Key highlights:

- `make lint` must pass
- `make test` and `make test-integration` must pass
- `make cover-check` must pass (≥ 95%)
- New migration files must have both `Up` and `Down`
- New outbound event types must be documented in `.claude/CLAUDE.md`
- All `outbox.Enqueue` calls must be inside a `Transactor.RunInTx` callback

---

## Commit Message Convention

```sh
<type>(<scope>): <short summary>

Types: feat, fix, chore, docs, refactor, test, ci, perf
Scope: handler, service, repo, bpmn, migration, ci, deps, docs

Examples:
  feat(handler): add workflow archive endpoint
  fix(repo): handle pgx.ErrNoRows in GetDraft
  chore(deps): upgrade platform-events to v1.2.0
  ci: add 95% coverage gate to release.yml
```

---
