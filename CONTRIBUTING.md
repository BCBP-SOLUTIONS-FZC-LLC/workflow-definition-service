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
cp .env.example .env        # fill in DATABASE_URL, VALKEY_URL, etc.
make docker-up              # start PostgreSQL on :5432, Valkey on :6379
# migrations run automatically at server startup (cmd/server/migrate.go)
make generate               # buf (proto → gen/) + sqlc (queries → postgres/db/)
make mock                   # regenerate GoMock stubs for port interfaces
make build                  # compile bin/server
go run ./cmd/server         # run locally
```

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
  fixtures/         Shared NewTestPool helper (testcontainers setup)
internal/**/*_test.go  White-box tests (need unexported access)
```

- White-box tests live next to their package when they need unexported symbols.
- All other tests go under `test/unit/<pkg>/` or `test/integration/`.
- Do not remove `t.Skip("not yet implemented")` placeholder tests without implementing the body.

### Integration test image

Pre-pull the Postgres image once so `testcontainers-go` doesn't download it on every run:

```bash
make tools-integration      # docker pull postgres:18-alpine
```

---

## Database Migrations

Migrations use **golang-migrate** via `platform-pgcommon`'s `migrate.Runner` (embedded in `db/migrations/migrations.go`). Each migration is a pair of files, `NNNNNN_name.up.sql` and `NNNNNN_name.down.sql` (no `+goose` annotations):

```sql
-- 000009_add_foo.up.sql
ALTER TABLE workflow ADD COLUMN foo TEXT;
```

```sql
-- 000009_add_foo.down.sql
ALTER TABLE workflow DROP COLUMN foo;
```

**Rules:**

- Both an `.up.sql` and a `.down.sql` are required; test the down path too.
- The pgx/v5 driver runs each file as a single statement, so `$$`-quoted PL/pgSQL functions need no special wrapping.
- Migrations apply automatically at server startup (`cmd/server/migrate.go`) and in the integration test fixture — there is no separate `migrate` command.
- Migrations that lock large tables (e.g. `ADD COLUMN NOT NULL DEFAULT`) must be split into safe steps (add nullable → backfill → add constraint).
- Never modify an already-applied migration file — create a new one.

---

## Code Generation

**Never edit generated files manually.** They are gitignored and regenerated in CI.

| Generated output | Source | Command |
| ----------------- | -------- | --------- |
| `gen/proto/` | `api/proto/*.proto` | `make generate-proto` |
| `internal/adapter/outbound/postgres/db/` | `db/queries/*.sql` + `sqlc.yaml` | `make generate-sqlc` |
| `internal/core/port/mocks/` | `internal/core/port/*.go` | `make mock` |

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

## Architecture Rules

Import direction is enforced by `go-arch-lint` (runs in CI lint):

```sh
core/domain  →  stdlib only
core/port    →  domain only
core/service →  domain + port
adapter/*    →  port + domain
cmd/server   →  all of the above
```

**Nothing in `core/` imports from `adapter/`.** Violating this fails CI.

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
