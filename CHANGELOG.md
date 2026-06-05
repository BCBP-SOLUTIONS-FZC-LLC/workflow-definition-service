# Changelog

All notable changes to `workflow-definition-service` are documented in this file.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added

- `Dockerfile` — multi-stage distroless build for production container images
- `.github/CODEOWNERS` — ownership rules for CI, migrations, and security files
- `.github/SECURITY.md` — vulnerability reporting policy with response SLAs
- `.github/dependabot.yml` — weekly Go module and GitHub Actions dependency updates
- `.github/pull_request_template.md` — contributor checklist (migrations, events, tests, security)
- `.github/ISSUE_TEMPLATE/bug_report.md` — structured bug report template
- `.github/ISSUE_TEMPLATE/feature_request.md` — structured feature request template
- `CONTRIBUTING.md` — developer setup, testing, and dependency update policy
- `SECURITY.md` (root) — links to `.github/SECURITY.md`
- `CHANGELOG.md` — this file

### Changed

- `Makefile` — coverage threshold raised from 70% to 95%; tool versions pinned; added `cover-func` target
- `.github/workflows/ci.yml` — added `permissions: contents: read`; added 95% coverage enforcement gate; renamed `Iint` job to `Integration`
- `.github/workflows/release.yml` — added coverage gate; added CHANGELOG section extraction for release notes; scoped `contents: write` to the release job only

---

## [0.1.0] — 2026-06-01

### Added

Initial scaffold of the Workflow Definition Service.

**Infrastructure:**

- Clean Architecture with `domain ← port ← service ← adapter` dependency direction enforced by `go-arch-lint`
- Go 1.26, Gin HTTP framework, gRPC server (`DefinitionService/GetCompiledWorkflow`)
- PostgreSQL adapter via `platform-pgcommon` (pgx/v5, RLS GUC injection, transactional outbox)
- Valkey (Redis-compatible) adapter for idempotency key caching
- `platform-events` integration: SNS outbox runner + SQS consumer skeleton
- Goose database migrations (`db/migrations/`)
- sqlc query generation (`db/queries/`, `db/schema/`)
- `buf` proto generation (`proto/` → `gen/`)
- GoMock stubs for all port interfaces (`internal/core/port/mocks/`)

**Domain:**

- `workflow`, `workflow_version`, `workflow_node_assignee`, `outbox_events`, `outbox_dead_letters`, `processed_event` schema
- BPMN AST structs (`internal/bpmn_compiler/`)
- 20 BPMN structural/semantic validation sub-errors (`BpmnErrorCode` registry)
- RFC-9457 problem details error catalogue (18 error codes)

**API:**

- 15 HTTP endpoints (all returning `501 Not Implemented` — business logic in a future PR)
- 1 internal gRPC endpoint (`GetCompiledWorkflow` — stub)
- 1 SQS consumer (`membership-wf-q` — no-op stub)
- `GET /healthz`, `GET /readyz` implemented

**CI/CD:**

- `ci.yml`, `validate.yml`, `release.yml` GitHub Actions workflows
- golangci-lint v2 with `go-arch-lint`, `wrapcheck`, `exhaustive`, `testifylint`
- `govulncheck` on every CI run
- testcontainers-go integration test infrastructure (`test/fixtures/`)
