# Testing

## Running tests

```bash
make test                # unit tests + race detector
make test-integration    # integration tests (requires running Docker daemon)
make cover               # unit tests + per-package coverage summary
make cover-html          # generates and opens HTML report
```

Coverage reports are written to `.coverage/` (gitignored).

## Unit test conventions

### Table-driven tests

All unit tests must be table-driven, even single-case scenarios:

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

**Exception — panic tests:** A test that verifies a `panic` must not be looped. The `defer/recover` runs once per function; iterating would skip recovery for all cases after the first panic. Keep panic assertions as separate, non-looped functions:

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

### White-box vs black-box placement

| Test type | Package declaration | Location | When to use |
| --- | --- | --- | --- |
| White-box | `package foo` | `internal/…/foo_test.go` | Needs unexported identifiers (private functions, unexported types, `txContextKey{}`, etc.) |
| Black-box | `package foo_test` | `test/unit/foo/` | Tests only the exported API; no access to internals needed |

Tests in `internal/` that use `package foo` (same package as the source) **cannot** be moved to `test/unit/` — Go's visibility rules would break the build. Do not move them.

Placeholder files in `test/unit/` for packages that already have white-box tests in `internal/` should call `t.Skip(...)` and explain the location:

```go
// Real tests are white-box and live in internal/adapter/outbound/postgres/mapping_test.go
func TestMapping(t *testing.T) { t.Skip("covered by white-box tests in internal/") }
```

## Unit test coverage

The CI `coverage` job enforces a **95% minimum** against the unit test profile only (`make test`). The coverage denominator **excludes** packages that are either generated or require a real database:

| Excluded package | Reason |
| --- | --- |
| `internal/adapter/outbound/postgres/db/` | sqlc-generated code |
| `internal/adapter/outbound/postgres/` | repo adapters require a real Postgres; covered by `Iint` job |
| `internal/core/port/mocks/` | GoMock-generated stubs |

The CI `Test` job post-filters the coverage profile to enforce these exclusions before computing the percentage:

```bash
grep -v '/postgres/db/\|/postgres/\|/mocks/' .coverage/coverage.out > filtered.out
```

We target 95%+ unit test coverage for all fully implemented components. This includes:

- **Configuration** (`internal/config`): Tests environment variable parsing, default fallbacks, and validation constraints (e.g. missing `DATABASE_URL`).
- **Valkey Cache** (`internal/adapter/outbound/valkey`): Unit tested using a mockable `redis.Cmdable` interface without requiring a live Redis/Valkey instance.
- **gRPC server** (`test/unit/grpcserver/`): 6 cases covering published versions, DRAFT nil-field handling, NotFound, invalid UUIDs, and repo errors.
- **SQS handler** (`test/unit/sqshandler/`): 7 cases covering unhandled event types, malformed JSON, invalid UUIDs, happy path, and service errors.
- **Execution client** (`test/unit/executionclient/`): 4 cases using a real local gRPC server (`net.Listen("tcp", "127.0.0.1:0")`).
- **Membership client** (`test/unit/membershipclient/`): 6 cases including URL encoding of special characters in query params.

## Integration tests

Integration tests live in `test/integration/` and use **testcontainers-go** to spin up a real PostgreSQL container automatically — no `make docker-up` needed. Pre-pull the image once to speed up local runs:

```bash
make tools-integration   # docker pull postgres:16-alpine (one-time)
make test-integration    # spins containers up/down automatically
```

Integration tests are separated by directory (not build tags). The `make test` target runs only `./internal/... ./test/unit/...`; `make test-integration` runs `./test/integration/...`. There is no `//go:build integration` tag — the separation is structural.

The integration coverage profile is written to `.coverage/coverage-integration.out`.

An example integration test:

```go
package postgres_test

import "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"

func TestWorkflowRepo_CreateAndGet(t *testing.T) {
    pool := fixtures.NewTestPool(t)   // spins up Postgres container
    repo := postgres.NewWorkflowRepo(pool)
    // ...
}
```

## Mock generation

Mocks for all `core/port` interface files are generated by GoMock and written to `internal/core/port/mocks/` (gitignored):

```bash
make mock
# writes mocks for: repository.go, publisher.go, cache.go, services.go, transactor.go
```

Import in tests:

```go
import (
    "go.uber.org/mock/gomock"
    "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
)

ctrl := gomock.NewController(t)
repo := mocks.NewMockWorkflowRepository(ctrl)
repo.EXPECT().GetByID(gomock.Any(), tenantID, workflowID).Return(wf, nil)
```

### Hand-rolled fakes for adapter-layer dispatch

The SQS handler (`test/unit/sqshandler/`) and HTTP handler (`test/unit/handler/`) layers use hand-rolled fakes instead of GoMock. These adapters each depend on a single unexported interface (`membershipRevoker`, `workflowSvc`, etc.) making GoMock unnecessary:

```go
// test/unit/sqshandler/handler_test.go
type fakeMembershipRevoker struct { err error }
func (f *fakeMembershipRevoker) HandleMembershipRevoked(...) error { return f.err }
```

Use GoMock for service-layer tests that orchestrate multiple dependencies; use hand-rolled fakes for adapter dispatch code with a single interface dependency.

### Shared test helpers (`test/unit/testkit`)

`test/unit/testkit/` is a non-test package importable by any `_test` package in `test/unit/`:

| Symbol | Description |
| --- | --- |
| `testkit.FakeLogger` | No-op `port.Logger` — use in place of `fakeLogger`/`stubLogger` local stubs |

Import it as any normal package:

```go
import "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"

grpcadapter.NewServer(testkit.FakeLogger{}, repo)
```

## End-to-end smoke testing

`scripts/smoke-test.sh` covers all 13 HTTP flow groups, the gRPC `GetCompiledWorkflow` endpoint, and the SQS `DepartmentMembershipRevoked` consumer — including error cases and idempotency checks.

### Prerequisites

```bash
# 1. Start backing services (Postgres + Valkey + LocalStack SNS/SQS)
make docker-up
make migrate-up

# 2. Start outbound service stubs
go run ./cmd/stub/execution &    # gRPC stub :9091, control plane :9092
go run ./cmd/stub/membership &   # HTTP stub :8081, control plane :8081/control

# 3. Start the definition service pointing at stubs
ORG_MEMBERSHIP_BASE_URL=http://localhost:8081 EXECUTION_SERVICE_ADDR=localhost:9091 \
  AWS_USE_STUB=false go run ./cmd/server &

# 4. Run the smoke test
./scripts/smoke-test.sh
```

Optional tools unlock additional assertions:

| Tool | What it unlocks |
| --- | --- |
| `grpcurl` (`brew install grpcurl`) | Section 10: gRPC endpoint tests |
| `psql` (`brew install postgresql`) | Section 11: DB-level SQS assertions |
| `aws` CLI (`brew install awscli`) | Section 11: SQS message injection |

The script exits `0` on full pass or `1` with a summary of failures.
