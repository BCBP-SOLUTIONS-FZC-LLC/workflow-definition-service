# Changelog

All notable changes to `workflow-definition-service` are documented in this file.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Changed

- **Migrations** now run via `platform-pgcommon`'s `migrate.Runner` (golang-migrate) instead of goose — `db/migrations/` holds `*.up.sql`/`*.down.sql` pairs, applied at server startup (`cmd/server/migrate.go`) and in the integration fixture. `platform-pgcommon` upgraded to v1.1.1.
- **Guarded BPMN loops** — the compiler accepts rework loops that revert through an exclusive gateway with a forward exit (DFS back-edge classification + guarded-exit check); unguarded/exitless loops still fail with `UNGUARDED_LOOP` / `CYCLE_DETECTED`. The compiled DSL gains additive `revert_to_dept` / `revert_to_stage` on exclusive branches; max forward-path depth capped at 100.
- **Optimistic locking** — `workflow` / `workflow_version` carry a `record_version` token (DB-trigger maintained); `PUT /draft` accepts/echoes `record_version` and returns `409 DRAFT_CONCURRENCY` on a stale token.
- **`VersionService.Promote`** is transactional, emits `workflow.template.published` with `promoted_from_version_id`, and early-exits when the target is already active.
- **`processed_event`** moved to the canonical composite-PK schema `(event_id, consumer)` + `event_type`, no tenant/RLS.
- `POST /workflows` and version clone responses now include a `message` field.

### Removed

- **In-process SQS consumer** — inbound events are now delivered over HTTP at `POST /internal/events` by a separate shared workflow-events consumer; the `SQS_QUEUE_URL` / `SQS_CONCURRENCY` config and the `sqs` adapter were removed.
- **In-service `processed_event` pruner** — the `processedEventPruner` goroutine and its `PROCESSED_EVENTS_PRUNE_DAYS` / `PROCESSED_EVENTS_PRUNE_INTERVAL` config were removed. Cleanup of the operational tables (`processed_event`, `outbox_events`, `outbox_dead_letters`) is now owned by an external periodic job (lambda / cron) per LLD §7.1.2. See `.design/CODE_GAPS.md` GAP-11.

### Added

- **Event schema governance** — `internal/eventschema/workflow_template_published.json` (Draft-07 schema for the `workflow.template.published` event) plus `x-lifecycle`/`x-owner` annotations on the message in `api/asyncapi.yaml`. CI now gates on `platform-schemagov extract --check` drift; `schema-registry.yml`/`schema-prune.yml`/`schema-health-quarterly.yml`/`freeze-watchdog.yml` wire up the register/prune/lifecycle workflows (AWS-gated steps are no-ops until Glue credentials are provisioned).
- **`POST /internal/events`** — internal service-to-service ingest endpoint (envelope dispatch, RLS GUC from `tenant_id`, idempotent via `processed_event`), guarded by an optional `x-internal-token` (`INTERNAL_API_TOKEN`).

**Service layer:**

- `WorkflowService` — BPMN validation on create, plan-tier quota enforcement (Starter=5, Pro=50, Enterprise=unlimited), `versions_limit` query param clamped to `[1, 100]`
- `DraftService` — draft lifecycle (init, update, discard); update wrapped in `RunInTx` to prevent partial state on dual-write
- `VersionService` — publish (BPMN structural check, `BulkInsert` assignees, outbox event), promote, archive, clone, diff, `HandleMembershipRevoked`
- `ValidationService` — stateless BPMN structural/semantic/topological validation endpoint
- `ExecutionService` outbound gRPC client with per-call `context.WithTimeout` (default 5 s) so Archive never hangs on a stalled Execution Service
- `MembershipService` outbound HTTP client for department eligibility checks

**HTTP handlers (16 endpoints):**

- Workflow: `CreateWorkflow`, `ListWorkflows`, `GetWorkflow`, `ArchiveWorkflow`
- Draft: `InitDraft`, `GetDraft`, `UpdateDraft`, `DiscardDraft`
- Version: `ListVersions`, `GetVersion`, `PublishVersion`, `PromoteVersion`, `CloneVersion`, `GetVersionDiff`
- Validation: `ValidateBPMN`
- Execution override: `OverrideExecution`
- HTTP idempotency middleware (`WithIdempotency`) — caches 2xx responses keyed by `tenant_id + route + Idempotency-Key` header (24 h TTL); stores SHA-256 of request body and returns `409 IDEMPOTENCY_KEY_REPLAY` on same-key/different-body replays

**gRPC:**

- `GetCompiledWorkflow` — parses tenant/version UUIDs, injects RLS GUC via `pgcommon.WithGUCSet`, fetches compiled plan; maps `ErrNotFound` → `codes.NotFound`
- Standard gRPC health service (`grpc.health.v1.Health`) registered at startup — returns `SERVING` for all service names; used by Kubernetes liveness/readiness probes on `:9090`

**SQS:**

- `DepartmentMembershipRevoked` consumer (`internal/adapter/inbound/sqs/handler.go`) — parses payload, dispatches to `VersionService.HandleMembershipRevoked`, deduplicates via `processedEvents.RecordIfNew`; logs and drops unknown event types

**Events:**

- `TemplateCloned` event (`EventTypeTemplateCloned`, `TemplateClonedPayload`) emitted atomically inside `Clone()` transaction
- `TemplateEligibilityInvalidated` event emitted on membership revocation; all envelopes carry `WithTraceID` from the active OTel span for cross-boundary trace propagation

**Middleware / security:**

- `InjectGUCSet` middleware — bridges `gincommon.RequestContext` into `pgcommon.WithGUCSet` on every HTTP-path request so `app.tenant_id` is set before any DB access (RLS fix)
- `LimitRequestBody` middleware — wraps body with `http.MaxBytesReader(5 MB)`; `errResponse` maps `*http.MaxBytesError` → `413 PAYLOAD_TOO_LARGE`

**Observability:**

- `pgmetrics.Init` registered at startup — exposes `pgcommon_*` Prometheus pool counters
- Startup log lines for SQS concurrency and outbox `poll_interval` / `batch_size`
- Outcome logs on `HandleMembershipRevoked` (`event_id`, `tenant_id`, `user_id`, `versions_affected`)

**Background workers:**

- `processed_event` pruner goroutine — ticks on `PROCESSED_EVENTS_PRUNE_INTERVAL` (default 24 h), calls `PruneOlderThan` to keep the table bounded; stops cleanly on context cancellation

**Repo / infra:**

- `Dockerfile` — multi-stage distroless build for production container images
- `.github/CODEOWNERS` — ownership rules for CI, migrations, and security files
- `.github/SECURITY.md` — vulnerability reporting policy with response SLAs
- `.github/dependabot.yml` — weekly Go module and GitHub Actions dependency updates
- `.github/pull_request_template.md` — contributor checklist (migrations, events, tests, security)
- `.github/ISSUE_TEMPLATE/bug_report.md` and `feature_request.md` — structured issue templates
- `CONTRIBUTING.md` — developer setup, testing, and dependency update policy
- `SECURITY.md` (root) — links to `.github/SECURITY.md`
- `CHANGELOG.md` — this file
- `ARCHITECTURE.md` — system context diagram, layer model, request flows, error catalog
- Three new env vars: `EXECUTION_CLIENT_TIMEOUT`, `PROCESSED_EVENTS_PRUNE_INTERVAL`, `PROCESSED_EVENTS_PRUNE_DAYS`

### Changed

- `platform-pgcommon` upgraded `v1.0.0` → `v1.1.0` — adds `DrainAndClose`, `Health`, `AllowFullStatements`, `PGBouncerMode`
- `TemplateEligibilityInvalidatedPayload` aligned with LLD §7.2.3 — fields are now `version_number`, `revoked_user_id`, `affected_nodes`, `reason`; removed `affected_user_id` and `affected_department`
- `DraftService.Update` dual-write (workflow meta + draft row) is now transactional via `RunInTx`
- `Makefile` — coverage threshold raised from 70% to 95%; tool versions pinned; added `cover-func` and `vuln` targets
- `.github/workflows/ci.yml` — added `permissions: contents: read`; 95% coverage gate; postgres adapter and generated packages excluded from unit coverage denominator
- `.github/workflows/release.yml` — added coverage gate; CHANGELOG section extraction for release notes; scoped `contents: write` to release job only

### Fixed

- `compiled_plan_json` was double-encoded (JSON string containing escaped JSON) — now stored and returned as a raw JSON value
- `ListVersions` pagination response shape corrected to match OpenAPI contract
- `PublishVersion` response narrowed to return only the version object (not the full workflow)
- Structural divergence check was wired to the wrong request flag — now reads `force_publish_structural`
- `ErrNoActiveVersion` → `409 NO_ACTIVE_VERSION` and `ErrPlanQuotaExceeded` → `403 PLAN_QUOTA_EXCEEDED` added to `errResponse` mapping
- BPMN validation called on `WorkflowService.Create` (was missing — invalid BPMN could be stored without validation)
- OTel traces severed at SNS/SQS boundary — `buildEnvelope` now attaches `WithTraceID` from the active span when `SpanContext.IsValid()`

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
