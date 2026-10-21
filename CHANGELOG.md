# Changelog

All notable changes to `workflow-definition-service` are documented in this file.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Changed

- The eligibility check uses the Org & Membership assignee eligibility read, I-19: `GET /api/v1/internal/tenants/{tenant}/members/{user}/eligibility?department_id=&required_level=` as the `iam-system` principal. `200` with `eligible: false` refuses the publish; a `400`, `403`, `5xx`, transport error or a `200` without the flag is `503 UPSTREAM_UNAVAILABLE` and is never read as an ineligible user, and a `400` or `403` is not retried. The local membership stub serves that route, answers `403` without the `iam-system` role and `400` for a level outside `preparator`, `reviewer` and `approver`; the smoke test names a valid level (LLD rev 1.56).
- Errors are `application/problem+json` documents and carry `trace_id` and `request_id` (the values of the `X-Trace-ID` and `X-Request-ID` response headers), including the refusals written by the identity, content-type and internal-token middleware. The responses the shared HTTP library writes before a handler runs (a missing identity, the request timeout, a recovered panic) keep the library's flat envelope; the plain 404 of an unrouted path, the health bodies and the AsyncAPI endpoint's error are not problem documents either.
- Request bodies are strict: a field the endpoint does not define is `400 BAD_REQUEST` naming it, instead of being ignored. The public spec declares `additionalProperties: false` on every request body and the internal spec on the connector-alias write (the event ingest envelope stays open, so a newer sender's added field is not refused), and both declare the new problem members.
- `PUT /workflows/{id}/draft` requires its lock token, from the `record_version` body field or an `If-Match` header (the header wins; the LLD already offered it and the code read only the body). The header is a plain or quoted integer (a weak validator, `*` and lists are ignored), and the idempotency fingerprint includes it. A request with neither answered by overwriting whatever was stored, the early return that skipped the check; it is now `400 BAD_REQUEST` naming `record_version`. A body with a second JSON value is refused. A stale save answers `409 DRAFT_CONCURRENCY` with `current_record_version`. LLD rev 1.54. Breaking for any client, and none exists yet.
- The LLD names the identity middleware as built (UUID parsing) and no longer says the Execution Service keeps a workflow key mapping.
- The tool downloads in the Makefile (hadolint, Trivy) use `curl -f`, so a 404 stops the install instead of leaving a broken binary. The README says that an `existingSecret` skips the chart's render-time key checks.
- The route table is registered by `RegisterAPIRoutes` and `RegisterInternalRoutes`, which the router and the handler tests both call, so the tests no longer keep a copy of it (and no longer use the old `/internal/...` paths).
- The workflow's key is named `template_key` (column, domain, repository), its conflict code is `DUPLICATE_TEMPLATE_KEY` and its constraint `uq_workflow_template_key` (migrations edited in place); the public field stays `key`. Execution's instance business key is a separate identifier and keeps its name and code. The docs no longer claim a slug pattern: the key is free-form up to 255 characters.
- The public spec follows the code further: every route with a body declares `413` and `415` through shared responses, a publish takes no body at all (the body is optional), the length and slug limits that nothing enforced are gone (a key, a name and a clone key are limited to 255 characters, a description to 2000, by the handlers), validation severities are lowercase, getDraft carries `updated_at`, the connector registry's `retry` includes `not-delivered`, and the compiled-plan schemas carry `iam_department_id`, connector `connector_type` and `io_mapping`, `boundary_id` and the `steps` of a boundary or branch.
- Docs follow the code: the draft lock is a fixed 30 s, task dates are RFC 3339 only, warnings arrive in `issues` with `severity` `warning`, `MULTIPLE_END_EVENTS` is not raised, the LLD library pins and the logger section describe what is built, and there is no `make godoc`.
- `workflow-connectors/v2` is v2.1.0. The registry no longer lists `chat-notify`, so a task naming it publishes with the `UNKNOWN_CONNECTOR_TYPE` warning and its template and credential endpoints are refused; `storage` and `send-email` declare `provider` as required.
- The public spec follows the code: getVersion names its id `id` and carries `updated_at`; clone, promote and diff declare `400` with the bad-request body (they had pointed at the 422 body); the publish `409` is a problem document, not a schema with a `suggested_action` that nothing produced; task due and follow-up dates are RFC 3339, the only form the validator accepts. Contract tests send the version routes through the real router.
- Docs follow the code: the internal events path is `/api/v1/internal/events` everywhere, `/metrics` is on the metrics port, the public spec's production server URL has no second `/v1`, `x-tenant-roles` is optional on reads, and the AsyncAPI page says events are received over HTTP, not published to an SNS topic.
- Schema (migrations edited in place): every time column is `TIMESTAMPTZ`, `created_at` and `updated_at` are `NOT NULL`, the parent key of a version, module version and assignee is `NOT NULL`, each child names its parent together with its tenant (composite foreign keys, so another tenant's parent cannot be named; deleting an active version clears only the active pointer), and `record_version` must be positive. The repository mapping lost its null-parent panic and timestamp conversions with it. Both DBML files follow the migrations.
- The identity headers are parsed as UUIDs once, by `RequireIdentity` (which replaces `InjectGUCSet` and sets the validated GUC set); a malformed `x-tenant-id` or `x-user-id` is `401`, not `500`, and never reaches the database. A request that reaches the middleware without a `RequestContext` is refused with `500` instead of being served with RLS unenforced.
- Configuration is strict: an unparseable value, a zero TTL or timeout, a missing `APP_ENV` (no longer defaulting to `dev`) and, outside `dev`, a missing `VALKEY_ADDR` fail startup, and every problem is listed in one error. `.env` is read in `local` and `dev` only. `OTEL_EXPORTER_OTLP_*` and `OTEL_TRACES_SAMPLER_RATIO` are no longer parsed by the service (the platform library reads them).
- A bind error returns `invalid_params` with JSON field names and nothing else, and logs the raw error. A vault `403` is `503`, not `401`, and vault error bodies are capped at 1 KiB.
- Credential and alias writes log an Info line (tenant, user, connector, field or alias, trace id; never a value).
- An alias name is `[a-zA-Z0-9_-]` up to 64 characters, `pathTemplate` starts with `/` and has no `//` prefix, `..`, `@`, backslash or whitespace, `timeoutMs` is at most 60000, and an allowlist entry that names a port matches that port only. The internal spec declares the bounds.
- A bad `page`, `limit` or `versions_limit` is `400 INVALID_INPUT` with `invalid_params`, where it had been clamped.
- The Org & Membership, Execution and OpenBao clients share a traced transport (client spans), a 1 MiB response cap and `platform_dependency_request_seconds`. Publish checks each distinct (user, department, role) once, up to eight at a time.
- The chart's NetworkPolicy admits the shared events consumer, and `make helm-check` requires both callers.
- The pools are built from `pgcommon.ConfigFromEnv`: `PG_SLOW_QUERY_THRESHOLD` is a duration (replacing `PG_SLOW_QUERY_THRESHOLD_MS`), `PG_STATEMENT_TIMEOUT`, `PG_LOCK_TIMEOUT` and `PG_POOL_NAME` are new, the chart sets a 25 s statement timeout, and the system pool is named `system`, holds three connections and always uses the simple protocol.
- The service reads each pool's database role at startup and refuses to start when the application role is a superuser or has `BYPASSRLS`, or when the system role cannot bypass row-level security or cannot write the module, starter and violation-log tables (a warning in dev without `SYSTEM_DATABASE_URL`). The local compose stack bootstraps from `scripts/init-db.sql` (a `workflow_definition_app` role without `BYPASSRLS` and a `workflow_definition_migrator` role that owns the schema and is the system role), the CI smoke job applies the same script, and `make docker-reset` recreates the volume.
- `/readyz` depends on the database only and reports the cache as `"cache":"ok"` or `"degraded"`; a pod starts while Valkey is down.
- Shutdown: a listener error runs the ordered shutdown and exits non-zero instead of exiting from the goroutine, each pool drain has its own bound (`SHUTDOWN_POOL_DRAIN_TIMEOUT`), the HTTP bound is `SHUTDOWN_GRACE`, and a panic in a long-lived goroutine is recovered and logged (the violation exporter is restarted).
- The RLS policy functions read the tenant through a regex-guarded `rls_app_tenant()` instead of an exception block, so a policy check no longer opens a subtransaction per row. The metric `workflow_definition_service_rls_violations_total` documents that each replica counts the rows it sees.
- A branch condition must be one comparison of a result field with a literal, `[= ] field (=|==|!=) literal`, parsed by `workflow-models` `dsl.ParseCondition`, the parser the Execution Service evaluates with. The validator refuses anything else as `INVALID_CONDITION_EXPRESSION` (before, it checked the length only, and a condition the Execution Service could not evaluate failed the gateway at run time). A request body over 10 MiB on `POST /api/v1/internal/events` returns 413, not 400.
- `make contract-breaking` also gates `docs/swagger/openapi.yaml`.
- The copy of the Execution Service's internal spec is refreshed, and `SOURCES` names `feat/service`, where the file is, so `make contract-check` reads it.
- The validator refuses a task `dueDate` or `followUpDate` that is not an RFC 3339 date-time (`INVALID_TASK_SCHEDULE`): the Execution Service ignored any other value and the task ran with no SLA.
- `workflow-models/v2` v2.0.2: every plan expansion passes the stage budget `domain.MaxPlanStages` (1000), shared by the compiler and the publish, node-read and module-contract paths, since the library refuses a zero budget; `enums.AllowedBPMNElements()` is a function.
- Platform libraries: `platform-events/v2` v2.1.0, `platform-pgcommon/v2` v2.0.1, `platform-gincommon` v1.6.0, `workflow-models/v2` v2.0.2, `workflow-connectors/v2` v2.0.0.
- The service is named `workflow-definition-service`. HTTP and database series are `platform_http_*` and `platform_db_*`; this service's collectors are `workflow_definition_service_<metric>`. The metrics identity (`domain=workflow`, `service`, `environment`) is fixed before any collector or pool is created, `/metrics` is served by `gincommon.MetricsHandler()`, and pgcommon queries export `db.query` spans through `gincommon.NewSpanTracer`. Alert rules, the Helm `PrometheusRule`, the prometheus-adapter rule and the release smoke query use these names.
- `APP_ENV` is one of `local`, `dev`, `test`, `staging`, `prod`; any other value fails startup. `prod` enforces `SYSTEM_DATABASE_URL`. The CI smoke job runs with `APP_ENV=test`.
- `OPENBAO_ADDR` and `OPENBAO_TOKEN` are required whenever `APP_ENV` is not `dev`; the Helm chart sets the OpenBao, secrets-client, connector-alias and RLS export values and takes `OPENBAO_TOKEN` from the Secret.
- `api/asyncapi.yaml` documents both inbound events the handler accepts (`DepartmentMembershipRevoked` and `MembershipRevoked`) and the producer's envelope, with no outbound channel.
- The request timeout runs innermost through `gincommon.Config.RequestTimeout`, so metrics, logs and spans record the 503 the client receives. The vulnerability scan in CI is pinned to govulncheck v1.1.4.
- The README follows the platform structure (mental model, architecture and dependency rules, observability, security, CI, versioning, out of scope).
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
- `platform-pgcommon` upgraded `v1.0.0` → `v1.1.0` — adds `DrainAndClose`, `Health`, `AllowFullStatements`, `PGBouncerMode`
- `TemplateEligibilityInvalidatedPayload` aligned with LLD §7.2.3 — fields are now `version_number`, `revoked_user_id`, `affected_nodes`, `reason`; removed `affected_user_id` and `affected_department`
- `DraftService.Update` dual-write (workflow meta + draft row) is now transactional via `RunInTx`
- `Makefile` — coverage threshold raised from 70% to 95%; tool versions pinned; added `cover-func` and `vuln` targets
- `.github/workflows/ci.yml` — added `permissions: contents: read`; 95% coverage gate; postgres adapter and generated packages excluded from unit coverage denominator
- `.github/workflows/release.yml` — added coverage gate; CHANGELOG section extraction for release notes; scoped `contents: write` to release job only
- The image builds for `TARGETARCH` (default `amd64`), carries OCI labels in the final stage and is built with `-buildvcs=false`.

### Fixed

- The smoke test saved the draft without its lock token, which the endpoint has required since the draft token change, so CI's smoke job failed with a 400. It reads `record_version` from `GET /draft`, checks that a save without it is refused with `400` and that a stale one is `409`, and saves with the current one.
- Promoting a published version on an archived workflow put it back among the tenant's templates without a limit check, so a tenant at its limit could create another template and then reactivate the archived one. Promote applies the creation count, plan lookup and refusal when the workflow has no active version and no draft, reading the workflow again inside the transaction so a concurrent promote of the same version is a success.
- A request that found the plan table past its freshness window waited for the catalog, up to the client timeout; the held table answers at once while the refresh runs in the background, and a reader panic no longer leaves the refresh marked as running. A catalog plan entry without a code or a `workflow_template_limit` field fails the read instead of reading as unlimited.
- The workflow template limit was hard-coded (5, 50, unlimited), chosen from the client's `x-plan` header, and any unknown value counted as `starter`. It is read from the catalog's plan table by plan code, and a missing or unknown code, or a table that was never read, answers `503 UPSTREAM_UNAVAILABLE`. The count covers workflows with a `DRAFT` or `PUBLISHED` version, so archiving a workflow frees its slot. `CATALOG_ADMIN_BASE_URL` is required outside dev, `PLAN_TABLE_TTL` and `CATALOG_CLIENT_TIMEOUT` are new, and `cmd/stub/catalog` serves the plan table for local runs, CI and the compatibility suite.
- CI's first full run on this branch (earlier runs skipped every job on a draft) failed wherever the `generated` artifact was used: an upload of two paths is rooted at their common parent, `internal/`, so downloading it into the workspace root left the sqlc output and the mocks outside the tree. Every download now extracts into `internal/`.
- A module or starter name, description or category, and a draft name or description, over its limit reached the database and came back as `500`. They are `422 INVALID_INPUT` with `invalid_params` now (name 255, description 2000, starter category 64), counted in characters, as the workflow create and clone limits are.
- A version diff sent `step_changes` with Go field names (`Description`, `Before`, `After`) instead of `description`, `before` and `after`, and sent `null` for a department list with no entry.
- A membership event published through a schema registry (a `dataschema` on the envelope and a base64 string of a registry-framed payload in `data`) was answered `400` and never reached the revocation handler. The ingest now decodes the frame with `events.GlueDecodeCodec` and answers `502 EVENT_DECODE_FAILED` for a payload it cannot decode. The internal spec declares `dataschema`, the string form of `data` and the `502`.
- A database outage answers `503 UPSTREAM_UNAVAILABLE`, not `500`: connection failures, exhausted connections, an administrator shutdown, a closed pool, a dial failure and a statement cancelled by the statement timeout.
- A Valkey outage no longer stops plan serving: readiness ignores the cache, and a draft save with an unavailable lock store answers `503`. The draft lock is released even when the client has disconnected.
- The HTTP write timeout is the request deadline plus 5 s, so the timeout body is no longer cut by a reset.
- The RLS violation exporter retries its first read until it succeeds instead of stopping for the life of the pod, and one replica prunes the log per tick.
- A lane that holds work needs a name, and lane names must differ without regard to case or surrounding space (`INVALID_LANE_NAME`); an unnamed lane without work stays valid.
- A second membership revocation adds its nodes to a version's validation errors instead of replacing the first one's, and a redelivery changes nothing.
- A revocation event leaves a default assignee added after the event time alone, so a reordered or redelivered event cannot invalidate a version published after the user was added back. A default assignee's `created_at` is a `timestamptz`, so the comparison holds whatever the database session time zone.
- The compiled-plan cache holds only valid published plans and re-reads the version after each write, so a revocation that lands between the read and the write leaves no valid entry behind. A failed cache eviction on the revocation path is returned, after the user's tasks are paused, so the event is redelivered.
- Module activation names only a published version of that module, workflow activation needs a valid version, and module-version publish and archive and starter delete are scoped to the caller's tenant in SQL.
- A request with an `Idempotency-Key` that is still running gets `409` `IDEMPOTENCY_REQUEST_IN_PROGRESS` instead of running twice; the key is scoped to the user, limited to 256 characters (`400` above), and cache failures log a warning with the key hashed.
- The depth-first walk enters a node when it is reached, so a loop through sibling branches is a back edge and the depth check counts the path through a cross edge.
- A workflow that calls a library module is scanned for rejected elements as authored, so a `scriptTask` beside a call is refused like one in a workflow with no call.
- `cancelActivity` must be `true`, `false`, `1` or `0` (`1` and `0` mean the same as `true` and `false`); sub-process bodies share the process's id space; a DOCTYPE or entity declaration is refused anywhere in the document.
- A publish is refused with `DRAFT_CONCURRENCY` when the draft was saved after the publish read it, so the stored plan, hash, assignees and module references always belong to the stored BPMN.
- A draft save that changes nothing returns the `record_version` the row holds instead of an incremented one, so the next save no longer fails as a concurrent edit.
- Archiving a workflow or a module archives the version that is active when it commits, so a publish landing at the same moment cannot leave a published version nothing references.
- Only published versions are cached as compiled plans, so a draft read before publish no longer hides the published plan until the entry expires.
- A timer boundary needs a positive duration the Execution Service can run; zero, negative and over-long durations are `INVALID_SLA_DURATION`.
- A start event, task, send or receive task, subProcess or callActivity with more than one outgoing flow is refused with `SPLIT_WITHOUT_GATEWAY`; the compiler kept only the first flow, so a published plan silently dropped the other branches.
- The container image's `HEALTHCHECK` runs a `-healthcheck` probe of the service's own `/healthz`; it used to fall through to starting a second server.
- The validator refuses what the compiler would silently drop: a timer or message boundary on a send task, receive task or gateway, a second message boundary on one task, a boundary with two outgoing flows, a revert flow out of a receive task, an end event with an outgoing flow (`END_EVENT_WITH_OUTGOING_FLOW`), duplicate element ids (`DUPLICATE_ELEMENT_ID`), an error boundary on a call with an unknown `errorRef`, and a sub-process in no lane. An exclusive split with several flows and no condition warns (`GATEWAY_BRANCH_WITHOUT_CONDITION`), since the Execution Service takes the first.
- A long chain of gateways no longer uses gigabytes: the join of a split is found once per split from its immediate post-dominator, and validation stops at `MAX_DEPTH_EXCEEDED` before analysing every split.
- A database failure while reading a version no longer loses a membership revocation; the event is redelivered.
- A `DepartmentMembershipRevoked` whose `department_id` is not a UUID is a 400, not a retried 500; the internal OpenAPI and AsyncAPI state the real contract.
- A version marked invalid by a membership revocation cannot be promoted.
- A timer boundary, message boundary or connector type change counts as structural in the version diff, and the diff response lists `stage_changes`.
- The release deploy passes the Org-Membership, OpenBao and Valkey addresses, the chart refuses a prod render without them, CI runs `make helm-check`, and the migrate hook finds its ServiceAccount and Secret on a first install.
- A page of versions carries summaries, not each version's BPMN and compiled plan, and a huge `page` is bounded.
- A pod leaves the endpoint lists before it stops listening: `/readyz` fails for `SHUTDOWN_DRAIN_DELAY` (default 5s) after SIGTERM.
- Unknown event types share one metric label, `AddModuleVersion` and `PublishVersion` return 400 for a malformed body, and clone normalizes `x-plan` like create.
- A main process named like a called process, and a call inside a sub-process, are refused by validation instead of failing at publish or as a dangling flow.
- The artifact hash covers messages, errors and message flows and ignores property order inside every element.
- The active version pointer can only name a published version of its own workflow, and the draft-edit lock is released only by the save that took it.
- Docs state the draft-edit lock failing a save when Valkey is unreachable, `/readyz` pinging Valkey, and the idempotency and lock key formats.
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

### Added

- A check that the documents this repository keeps in the design repository still match their copies there: `make design-sync-check` (`DESIGN_DIR` reads a local checkout, otherwise `GH_TOKEN` reads `Haroon-BCBP/design`) compares the pairs listed in `scripts/design-sync.manifest`, and `make design-sync-check-test` tests the script. CI does not run it: the design repository is a personal repository that CI does not read, so the check runs locally only.
- A tenant data purge for departed tenants. The service consumed no tenant lifecycle event, so after IAM's tenant wipe its workflows, modules, starters and connector credentials stayed for ever. `TenantMembershipsPurged`, and `TenantStateChanged` to `offboarded`, record the tenant in `tenant_retention` (idempotent), and a sweep deletes its connector credentials, workflows (with versions and assignees), tenant modules and starters in batches, as the tenant under row-level security, after their own periods (`TENANT_CREDENTIAL_RETENTION`, 30 days, for the credentials; `TENANT_ARTIFACT_RETENTION`, 7 years, for the workflows, modules and starters; `TENANT_PURGE_ENABLED`, `TENANT_PURGE_INTERVAL` and `TENANT_PURGE_BATCH`), and marks a tenant purged only after a check finds no row left, so two replicas sweeping at once cannot mark it early. The global catalog is never touched. A failed purge counts in `workflow_definition_service_tenant_purge_failures_total` and raises `WorkflowDefinitionServiceTenantPurgeFailing`. The Event Consumer routes both events here as well as to Execution. LLD §7.4.4 and §7.4.6, rev 1.53.
- CI runs the Execution Service's compatibility suite against this checkout in a `compat` job: the suite builds this service's binary and drives it against the Execution Service's real API and worker, so a change here that breaks Execution fails this pull request. The job also checks out the Workflow-Events Consumer (`compat_consumer_ref`, `feat/event-consumer`), so membership events published on SNS reach this service through the real consumer.
- Contract tests also fail on a response field that the spec's schema does not declare (`Spec.Undeclared`), applied to the public and internal routes the tests send; that found `updated_at` and `has_draft` missing from the getWorkflow response, now declared.
- The public spec declares the idempotency 409 `IDEMPOTENCY_REQUEST_IN_PROGRESS`, the 400 for a key over 256 characters (the header is no longer typed as a UUID), a 503 `UPSTREAM_UNAVAILABLE` on every operation, and the real `/readyz` bodies (`cache` ok or degraded; `unavailable` or `draining` on 503). Contract tests send these through the real router against it.
- Contract tests send the internal events and connector alias routes through the real router against `api/internal-openapi.yaml`, including 400, 401, 413 and 500, and the internal spec declares 413.
- The published-version nodes read lists `waits`: the receive tasks and message boundaries that no send task of the plan feeds, each with the key an outside party's answer addresses (`dsl.ExternalWaits`). A compiled message boundary keeps its BPMN id (`MessagePath.BoundaryID`).
- `CONNECTOR_SECRET_AUTHORED`: a connector task input targeting a secret field of its connector type is refused; the tenant's stored credential is always used.
- Row-level-security violation logging: `rls_check_tenant()` and `rls_check_tenant_or_global()` write a 1% sample of blocked rows to `rls_violation_log`; a background loop exports them as `workflow_definition_service_rls_violations_total{violation_type}` and deletes rows past `RLS_VIOLATION_RETENTION`; the Helm and standalone alert rules page on both violation types.
- `make metrics-lint` and `make metrics-inventory`, the generated `docs/observability/metric-registry.md`, and a `validate-quality` step that runs the observability checks.
- Tests that fail when the public OpenAPI, the internal OpenAPI or the AsyncAPI differ from the routes and the event types the handler accepts, a redelivery test for the membership revocation, a telemetry test that checks the scrape against the platform registry, and an integration test that checks the `platform_db_*` series appear with a real pool.
- Outbound calls to the Execution Service and the Org & Membership Service carry the W3C trace context.
- `.editorconfig`, `VERSIONING.md`, and a release job that refuses a tag with no CHANGELOG entry before anything is built.
- `api/internal-openapi.yaml`, the authority for every `/api/v1/internal` route, shown beside the public spec in Swagger UI. `test/contract` validates the real router's responses against it, and the execution client's requests against the copy of the Execution Service's spec in `api/contracts/`.
- `make contract-breaking` (oasdiff against `origin/main`, in `make check` and CI) and `make contract-check` (the copy against its source branch, in CI).
- `GET /workflows/:id/versions/:version_id/nodes`: a published version's human tasks, module tasks included, each with its node key, department, role level, default users and name (definition LLD §3.3.12.2).
- `GET /modules/:id/versions/:version_id/contract`: what a call of a module version binds and names — its open and fixed slots, every human task by its `Assignees` key and the slot it runs in, its messages and error codes, and whether it names users (§3.3.18).
- Compiled stages carry their task's `name`.
- **Connector-task compiler exception + authoring endpoints** (per the `workflow_connectors` LLD) — a `connector:<name>`-prefixed `serviceTask` compiles to a fully automation-only `StageDef` (no assignee/role fields), with `IOMapping` preserved and an unrecognized connector type warning rather than rejecting (the type might still be registered before the workflow is instantiated). `GET /connectors/registry` serves `workflow-connectors`' `pkg/registry` catalogue; `POST /connectors/credentials` writes a provider credential to OpenBao and returns only the resulting secret path — the raw value never appears in any response.
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
- Every alert carries a `runbook` annotation (`workflow-definition-service/<slug>`) with an entry per alert in `docs/observability/runbooks.md`; a test keeps the annotations, the page headings and the chart rule set in step.
- The defect-class linters are on (bodyclose, noctx, nilerr, gosec, contextcheck, gocritic, nolintlint, sqlclosecheck) and lint now covers the build-tagged tests.

### Security

- Go 1.26.9, `golang.org/x/net` v0.60.0 and `github.com/moby/go-archive` v0.3.0; the pinned Dockerfile builder image is go1.26.9.
- Request bodies on the internal route group are capped at 10 MiB, as on the public group.
- The metrics server sets read-header, read, write and idle timeouts.
- Tenant tables force row-level security, and their policies log sampled violations.

### Removed

- The collaboration machinery: `CompileCollaboration`, message bridges, `call_pool` steps, pool-qualified department IDs and the ignored-pool stub. `CompiledCollaboration.messages`, `CompiledPlan.ignored` and `DepartmentDef.ignore` are gone from the plan (workflow-models `v1.4.0-rc.2`).
- **In-process SQS consumer** — inbound events are now delivered over HTTP at `POST /internal/events` by a separate shared workflow-events consumer; the `SQS_QUEUE_URL` / `SQS_CONCURRENCY` config and the `sqs` adapter were removed.
- **In-service `processed_event` pruner** — the `processedEventPruner` goroutine and its `PROCESSED_EVENTS_PRUNE_DAYS` / `PROCESSED_EVENTS_PRUNE_INTERVAL` config were removed. Cleanup of the operational tables (`processed_event`, `outbox_events`, `outbox_dead_letters`) is now owned by an external periodic job (lambda / cron) per LLD §7.1.2. See `.design/CODE_GAPS.md` GAP-11.
- **`workflow.template.published` event** — removed entirely, including its `api/asyncapi.yaml` message/schemas, `internal/eventschema/workflow_template_published.json`, and the LocalStack SNS/Glue scaffolding that backed it. Its only remaining consumer behavior (Execution's eager cache-warm) was already dropped, leaving nothing but a payload validate-and-dedup step. `TemplatePublishedPayload`/`EventTypeTemplatePublished` stay defined in `workflow-models` for now; this service just stopped emitting the event. This service currently has no outbound events.
- The Glue schema-governance tooling: the `schema-*` and freeze-watchdog workflows, `internal/eventschema/`, the `make schema-*` targets, the `extract-schemas` CI step, the schema alerts and their CODEOWNERS entry. The service publishes no events.
- The unused `domain.EventSource` constant.

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
