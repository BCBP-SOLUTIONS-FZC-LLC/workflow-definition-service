# Changelog

All notable changes to `workflow-definition-service` are documented in this file.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Security

- Go 1.26.9 (the standard library fixes for `net/http`, `net/textproto`, `crypto/tls` and `html/template`), `golang.org/x/net` v0.60.0 and `github.com/moby/go-archive` v0.3.0. The pinned Dockerfile builder image is go1.26.9.

### Added

- The published-version nodes read lists `waits`: the receive tasks and message boundaries that no send task of the plan feeds, each with the key an outside party's answer addresses (`dsl.ExternalWaits`). A compiled message boundary keeps its BPMN id (`MessagePath.BoundaryID`).
- `CONNECTOR_SECRET_AUTHORED`: a connector task input targeting a secret field of its connector type is refused; the tenant's stored credential is always used.
- `.editorconfig` and `VERSIONING.md`.
- The release workflow refuses a tag with no CHANGELOG entry before it builds anything.
- `TestRegister_FollowsObservabilityRegistry` validates this service's collectors against the platform registry.

### Changed

- **Platform libraries moved to their latest majors:** `platform-events/v2` v2.1.0, `platform-pgcommon/v2` v2.0.0, `platform-gincommon` v1.6.0. Logs, metrics and traces go through gincommon only; the database through pgcommon only; the envelope type through platform-events only. `workflow-models` v1.2.0 and `workflow-connectors` v1.0.0 replace the release-candidate and pseudo-version pins.
- **Metric names follow the platform observability standard.** HTTP and database series are `platform_http_*` and `platform_db_*`; this service's collectors are `workflow_definition_svc_<metric>` (was `wf_<metric>` and `internal_events_ingest_total`). The metrics identity (`domain=workflow`, `service`, `environment`) is fixed before any collector or pool is created, `/metrics` is served by `gincommon.MetricsHandler()`, and pgcommon queries export `db.query` spans through `gincommon.NewSpanTracer`. Alert rules, the Helm `PrometheusRule`, the prometheus-adapter rule and the release smoke query use the new names.
- `APP_ENV` must be one of `local`, `dev`, `test`, `staging`, `prod`; any other value, `production` included, fails startup. `prod` now also enforces `SYSTEM_DATABASE_URL`, which a `production` value had silently skipped. The CI smoke job runs with `APP_ENV=test`.
- `api/asyncapi.yaml` documents both inbound events the handler accepts (`DepartmentMembershipRevoked` and `MembershipRevoked`) and the producer's envelope, with no outbound channel.
- The Helm chart sets `OPENBAO_ADDR`, `OPENBAO_MOUNT`, `SECRETS_CLIENT_TIMEOUT` and `CONNECTOR_ALIAS_ALLOWED_HOSTS`.
- The request timeout runs innermost through `gincommon.Config.RequestTimeout`, so metrics, logs and spans record the 503 the client receives. The vulnerability scan in CI is pinned to govulncheck v1.1.4.
- The README follows the platform structure (mental model, architecture and dependency rules, CI, versioning, out of scope).
- Non-test code carries no comments; only `//go:` and `//nolint` directives remain.
- **A boundary carries the whole path it leads to.** A boundary on a task, a subProcess or a call carries its path as `Steps`, in the order the flows give, and the boundary event's ID as `BoundaryID`; it named only the department it moved to, so a path of several tasks, lanes or gateways ran its first department alone. A path that continues into a node the main flow compiled keeps naming only its department. workflow-models `v1.4.0-rc.4` (LLD rev 1.33).
- **The service has no outbox, relay, SNS publisher or Glue codec.** It publishes no event, and the apparatus made every cluster boot require `SNS_TOPIC_ARN` and `GLUE_REGISTRY_NAME`. Removed: the outbox tables and their RLS migration (`000005`, edited in place: reset a local database), the relay and its alerts, the `OutboxRepository` port, `AWS_USE_STUB`, `AWS_REGION`, `AWS_ENDPOINT_URL`, `SNS_TOPIC_ARN`, `GLUE_REGISTRY_NAME`, `GLUE_SCHEMA_CACHE_TTL` and `OUTBOX_*` settings, the chart's `SNS_TOPIC_ARN` Secret key, and LocalStack from the local stack. `ORG_MEMBERSHIP_BASE_URL`, `EXECUTION_SERVICE_URL` and `EXECUTION_SERVICE_TOKEN` are required whenever `APP_ENV` is not `dev` (LLD rev 1.32).
- A connector input's `source` is `=name` for an instance variable or any other text for a literal (workflow-models `IOVar.InputValue`).
- **Tasks run in the order the flows give.** A department is a lane segment, a stretch of one lane on one path: `<lane>` for the first, `<lane>~<n>` after, so a flow returning to a lane, the steps after a join and an exclusive branch no longer run tasks out of order. Node keys of later segments carry the suffix and can renumber between versions.
- Every forward exclusive branch compiles into `ExclusiveBranch.Steps` (workflow-models `v1.4.0-rc.1`): a call or subprocess on a branch was dropped or ran on every branch.
- A gateway's join is its immediate post-dominator in the forward graph; a nested fork no longer compiles the region after its inner join twice.
- A boundary's path compiles on its own state, stops at compiled nodes, and its target starts a segment; the task it leads to no longer runs on the normal path. A revert names the segment on its own path.
- New `INVALID_LANE_NAME` (a lane name with `/`, `::` or `~`). `UNMATCHED_GATEWAY` covers a parallel split with no join; `UNSUPPORTED_BOUNDARY_TARGET` covers a subprocess whose body does not start with a userTask; `TASK_LIMIT_EXCEEDED` covers a compile over 1000 stages or 10000 nodes.
- New golden `dsltest.FlowOrder`, written by `TestGolden`.
- **One pool runs; outside parties' pools and lanes are skipped; modules always run.** The workflow is the one process that is executable, not called and not marked ignore; a pool marked ignore, or not executable and not called, produces nothing. A collaboration publishes, and `/workflows/validate` judges it as publish does. A called process runs as a module whether or not it is executable; a call to a pool marked ignore is `UNRESOLVED_CALLED_ELEMENT`. A lane marked ignore is an outside party inside the process that runs: its tasks compile to nothing and need no task type, user or `dept_id`. An outside pool's drawn calls are never made. New `NO_RUNNABLE_PROCESS`, and `IGNORED_LANE_WORK`: a call, subprocess or connector task in an ignored lane, a boundary on one of its tasks, or a loop whose way back holds only its tasks. A call cannot bind a module lane to an ignored lane, and a boundary's path is judged past skipped tasks, as it compiles.
- A receive task with no message of its own waits for the message flow into it, or for its own id. `textAnnotation`, `association`, `documentation`, `property` and data-association children are diagram-only (workflow-models allowlist). An unnamed message flow warns only when it flows into a receive task or message boundary that runs, in the workflow or a module it calls.
- **The Execution Service link is internal HTTP; gRPC is removed.** The compiled-plan read is `GET /api/v1/internal/workflow-versions/{version_id}/compiled-plan` (the plan a JSON object; no separate `dsl_schema_version`), and the archive guard and the membership-revocation pause call the Execution Service's internal API. `EXECUTION_SERVICE_URL`/`EXECUTION_SERVICE_TOKEN` replace `EXECUTION_SERVICE_ADDR`; `GRPC_PORT`, the protos, buf and port 9090 are gone. `cmd/stub/execution` is an HTTP stub on `:8083`.
- `RequireInternalToken` compares in constant time and answers `401` with a problem body.
- `INTERNAL_API_TOKEN` is required whenever `APP_ENV` is not `dev`; it was required only for `prod`.
- The compiled-plan alerts select on `route` (`method` is the HTTP verb).
- `make contract-check` and `make contract-breaking` fail on a repo, ref or `CONTRACT_SRC_DIR` they cannot read, instead of reporting it as pending; a source file missing from a real branch is still pending.
- CI's contract copy check reads the sibling repo with `GO_PRIVATE_TOKEN`. The release deploys with `existingSecret=workflow-definition-service-secrets`, a Secret created in the cluster; it had set no secret values and could not render.
- Publish works through PgBouncer: sqlc maps NOT NULL JSONB to `json.RawMessage` as well, so `module_refs` is sent as JSON text, not bytea hex, on the simple protocol (the rule `3e8abc2` set for nullable JSONB).
- The smoke test sends `x-tenant-roles`, reads `/metrics` on the metrics port, and asserts that invalidating a published version writes no outbox row.
- The Helm gateway routes only the public path prefixes; it had routed all of `/api/v1`, internal routes included. `EXECUTION_SERVICE_URL` points at the Execution Service API's HTTP Service (it pointed at its metrics port), and egress is limited to the API pods.
- Node keys and the human-task rule come from workflow-models' `dsl.NodeKey` and `StageDef.CreatesHumanTask`, shared with the Execution Service.
- **Prometheus metrics centralized into `internal/observability`** — the three previously-scattered `promauto` call sites (`internal/core/service/metrics.go`, `internal/adapter/inbound/grpc/server.go`, `internal/adapter/inbound/http/handler/internal_events.go`) are now nil-until-`Register()` vars in one package, registered against `gincommon.MetricsRegisterer()` from `cmd/server/wire.go`'s composition root instead of `promauto`'s implicit default-registry registration. Call sites go through new nil-safe `IncCounter`/`IncCounterVec`/`ObserveHistogram` helpers. No metric name/label changed. `cmd/server/wire.go`'s standalone `pgmetrics.Init` + `prometheus.MustRegister(pgmetrics.NewPoolStatsCollector(...))` calls are folded into the same `Register()`. `platform-gincommon` bumped v1.2.1 → v1.3.0 (adds `MetricsRegisterer`/`MetricsConstLabels`) and `platform-pgcommon` v1.1.2 → v1.2.1 (public `pkg/domain.Logger`, letting `cmd/server/pglogger.go` finally wire a real logger into `pgcommon.Config.Logger`).

### Added

- `api/internal-openapi.yaml`, the authority for every `/api/v1/internal` route, shown beside the public spec in Swagger UI. `test/contract` validates the real router's responses against it, and the execution client's requests against the copy of the Execution Service's spec in `api/contracts/`.
- `make contract-breaking` (oasdiff against `origin/main`, in `make check` and CI) and `make contract-check` (the copy against its source branch, in CI).
- `GET /workflows/:id/versions/:version_id/nodes`: a published version's human tasks, module tasks included, each with its node key, department, role level, default users and name (definition LLD §3.3.12.2).
- `GET /modules/:id/versions/:version_id/contract`: what a call of a module version binds and names — its open and fixed slots, every human task by its `Assignees` key and the slot it runs in, its messages and error codes, and whether it names users (§3.3.18).
- Compiled stages carry their task's `name`.
- **Connector-task compiler exception + authoring endpoints** (per the `workflow_connectors` LLD) — a `connector:<name>`-prefixed `serviceTask` compiles to a fully automation-only `StageDef` (no assignee/role fields), with `IOMapping` preserved and an unrecognized connector type warning rather than rejecting (the type might still be registered before the workflow is instantiated). `GET /connectors/registry` serves `workflow-connectors`' `pkg/registry` catalogue; `POST /connectors/credentials` writes a provider credential to OpenBao and returns only the resulting secret path — the raw value never appears in any response.

### Changed

- **Compiled-plan DSL migrated to `workflow-models`** — `internal/core/domain/compiled_plan.go` deleted; `CompiledPlan`, `StageDef`, `ExecutionStep`, `CompiledCollaboration`, and every other compiled-plan type now come from `github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models`'s `pkg/dsl` (pre-release `v0.1.0-beta.1`). `eventpayloads.go` now only holds `EventSource`. All call sites across `internal/bpmn_compiler/**`, `internal/core/service/**`, `internal/core/port/**`, and `internal/adapter/inbound/grpc/server.go` repointed directly at the module (no type alias).
- **Docs restructured to drop MkDocs** — `docs/` now holds only diagram sources (`docs/architecture/`), the served OpenAPI spec (`docs/swagger/`), and the two standalone workflow-design guides (`bpmn-designer-guide.md`, `ui-enrichment-guide.md`); everything else was folded into `README.md`, `ARCHITECTURE.md`, and `CONTRIBUTING.md`. Removed `mkdocs.yml` and the `docs-serve`/`docs-build` Makefile targets. Fixed a pre-existing bug where `cmd/server/router.go` served a non-existent `api/openapi.yaml` instead of the real spec file.
- **Migrations** now run via `platform-pgcommon`'s `migrate.Runner` (golang-migrate) instead of goose — `db/migrations/` holds `*.up.sql`/`*.down.sql` pairs, applied at server startup (`cmd/server/migrate.go`) and in the integration fixture. `platform-pgcommon` upgraded to v1.1.1.
- **Guarded BPMN loops** — the compiler accepts rework loops that revert through an exclusive gateway with a forward exit (DFS back-edge classification + guarded-exit check); unguarded/exitless loops still fail with `UNGUARDED_LOOP` / `CYCLE_DETECTED`. The compiled DSL gains additive `revert_to_dept` / `revert_to_stage` on exclusive branches; max forward-path depth capped at 100.
- **Optimistic locking** — `workflow` / `workflow_version` carry a `record_version` token (DB-trigger maintained); `PUT /draft` accepts/echoes `record_version` and returns `409 DRAFT_CONCURRENCY` on a stale token.
- **`VersionService.Promote`** is transactional and early-exits when the target is already active.
- **`processed_event`** moved to the canonical composite-PK schema `(event_id, consumer)` + `event_type`, no tenant/RLS.
- `POST /workflows` and version clone responses now include a `message` field.
- **Route table consolidated into a single `router.go`** — `handler/router.go`'s `RegisterRoutes`/`RegisterInternalRoutes` bodies moved inline into `NewRouter`; `Handler` gained an exported `Idempotent` method so the registration code outside the `handler` package can still wrap routes with idempotency support. `NewRouter` now also runs `gincommon.TimeoutMiddleware(30s)` ahead of the observability middlewares. No route path/method/handler mapping changed.
- **Swagger theme/initializer served from embedded static files** — `swagger_theme.go`/`swagger_initializer.go`'s inline Go string constants moved to `static/swagger-theme.css`/`static/swagger-initializer.js`, loaded via `//go:embed`; `SwaggerThemeHandler`/`SwaggerInitializerHandler` are otherwise unchanged.

### Removed

- The collaboration machinery: `CompileCollaboration`, message bridges, `call_pool` steps, pool-qualified department IDs and the ignored-pool stub. `CompiledCollaboration.messages`, `CompiledPlan.ignored` and `DepartmentDef.ignore` are gone from the plan (workflow-models `v1.4.0-rc.2`).
- **In-process SQS consumer** — inbound events are now delivered over HTTP at `POST /internal/events` by a separate shared workflow-events consumer; the `SQS_QUEUE_URL` / `SQS_CONCURRENCY` config and the `sqs` adapter were removed.
- **In-service `processed_event` pruner** — the `processedEventPruner` goroutine and its `PROCESSED_EVENTS_PRUNE_DAYS` / `PROCESSED_EVENTS_PRUNE_INTERVAL` config were removed. Cleanup of the operational tables (`processed_event`, `outbox_events`, `outbox_dead_letters`) is now owned by an external periodic job (lambda / cron) per LLD §7.1.2. See `.design/CODE_GAPS.md` GAP-11.
- **`workflow.template.published` event** — removed entirely, including its `api/asyncapi.yaml` message/schemas, `internal/eventschema/workflow_template_published.json`, and the LocalStack SNS/Glue scaffolding that backed it. Its only remaining consumer behavior (Execution's eager cache-warm) was already dropped, leaving nothing but a payload validate-and-dedup step. `TemplatePublishedPayload`/`EventTypeTemplatePublished` stay defined in `workflow-models` for now; this service just stopped emitting the event. This service currently has no outbound events.

### Added

- **Event schema governance** — Draft-07 JSON Schema files derived into `internal/eventschema/` per event, plus `x-lifecycle`/`x-owner` annotations on each message in `api/asyncapi.yaml`. CI now gates on `platform-schemagov extract --check` drift; `schema-registry.yml`/`schema-prune.yml`/`schema-health-quarterly.yml`/`freeze-watchdog.yml` wire up the register/prune/lifecycle workflows (AWS-gated steps are no-ops until Glue credentials are provisioned).
- **`POST /internal/events`** — internal service-to-service ingest endpoint (envelope dispatch, RLS GUC from `tenant_id`, idempotent via `processed_event`), guarded by an optional `x-internal-token` (`INTERNAL_API_TOKEN`).
- **Helm chart (`deploy/helm/`)** — Deployment/Service on ports `8080` (HTTP) / `9090` (gRPC), a `migrate-job` pre-install/pre-upgrade hook running `/server migrate` (this service has no auto-migration at server boot), HPA/PodDisruptionBudget/NetworkPolicy sized for this service's resource profile, and a ServiceMonitor/PrometheusRule covering availability, HTTP error rate/latency, BPMN validation failure rate, publish latency, outbox delivery stalls, and RLS violations. `deploy/monitoring/` adds static twins of the alerting rules plus a `prometheus-adapter` ConfigMap for optional RPS-based autoscaling. `release.yml` gained a `deploy-gate` job (`helm upgrade --install --atomic`, deployed-digest verification, rollout wait, 2-minute error-rate gate with automatic `helm rollback`) that must pass before a GitHub Release is published.
- **Local pre-commit hook** — `.githooks/pre-commit` runs `make tidy`/`fmt-check`/`lint`/`arch-lint` before every commit; installed via new `make setup` (first-time onboarding) / `make install-hooks` targets. `check` now delegates its gofmt step to the new standalone `fmt-check` target.
- **Docs** — new `docs/deployment.md` and `docs/schemagov.md` pages (linked from `README.md` and `mkdocs.yml`); `docs/configuration.md` corrected (`GLUE_REGISTRY_ARN` is CI-tooling-only and not read by the running service; added `VALKEY_WRITE_TIMEOUT`, `IDEMPOTENCY_TTL`, `GLUE_SCHEMA_CACHE_TTL`, `MEMBERSHIP_CLIENT_TIMEOUT`; corrected `INTERNAL_API_TOKEN`/`ORG_MEMBERSHIP_BASE_URL`/`EXECUTION_SERVICE_ADDR` conditional-required notes to match `internal/config` validation).

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

- The execution and membership clients are nil interfaces when unset; a nil pointer inside the interface had passed the services' nil checks.
- The public OpenAPI spec is valid OpenAPI 3: a duplicate `409`, `$ref`s with `nullable` siblings and examples without `instance` are fixed.

- **CRITICAL — path traversal in `POST /connectors/credentials`**: `connector_type`/`field_name` were interpolated into the OpenBao secret path with no validation, letting a `"../<other-tenant>/x"`-style value climb out of the caller's own tenant subtree. Both are now validated (`connector_type` against the registry, `field_name` against `^[a-zA-Z0-9_-]+$`) before either touches the path.
- `secrets_client.go` leaked OpenBao's raw error text (network error / HTTP response body) into the API response and logs on failure — now a fixed generic detail, matching this file's own existing convention for BPMN validation errors; the raw error is still logged server-side.
- An empty connector type (`type="connector:"`) was accepted as a lenient "unknown type" warning instead of being rejected — an empty name can never be registered, unlike a real-but-unrecognized one. Fixed in both places this prefix-cut logic was independently implemented.
- A connector task carrying a contradictory `<zeebe:assignmentDefinition>` was silently dropped with no diagnostic — now warns.
- A `serviceTask` nested inside another `serviceTask` (BPMN-illegal but parseable) had its connector type misattributed to the outer element, which then silently vanished from the compiled graph instead of being rejected.
- `compiled_plan_json` was double-encoded (JSON string containing escaped JSON) — now stored and returned as a raw JSON value
- `ListVersions` pagination response shape corrected to match OpenAPI contract
- `PublishVersion` response narrowed to return only the version object (not the full workflow)
- Structural divergence check was wired to the wrong request flag — now reads `force_publish_structural`
- `ErrNoActiveVersion` → `409 NO_ACTIVE_VERSION` and `ErrPlanQuotaExceeded` → `403 PLAN_QUOTA_EXCEEDED` added to `errResponse` mapping
- BPMN validation called on `WorkflowService.Create` (was missing — invalid BPMN could be stored without validation)
- OTel traces severed at SNS/SQS boundary — `buildEnvelope` now attaches `WithTraceID` from the active span when `SpanContext.IsValid()`
- **Outbox write would break under real (non-stub) Glue encoding** — `buildEnvelope` used to call the Glue codec's `Encode` at outbox-enqueue time and embed its binary Glue wire-format bytes directly as the envelope's plain-JSON `data` field, which `outbox.Enqueue`'s `json.Marshal(env)` cannot serialize. `platform-events` upgraded `v1.3.1` → `v1.4.0` for `events.WithCodec`, which moves Glue Schema Registry encoding to SNS-publish time instead — `cmd/server/infra.go`'s `newPublisher` now injects the Glue `Codec` into `events.NewSNSPublisher`, and `buildEnvelope`/`VersionService` no longer reference a codec at all. `internal/core/port/glue.go` (`port.GlueCodec`) removed; `internal/adapter/outbound/glue.Codec` now implements `events.Codec` directly (`Encode` returns `(encoded []byte, schemaID string, err error)`; new `Decode` reverses the wire-format framing).

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
