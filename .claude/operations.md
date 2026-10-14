---
name: operations
description: CI/CD, observability (Prometheus metrics, OTel), platform-lib versions, and MkDocs docs maintenance for workflow-definition-service
metadata:
  type: reference
---

# Operations Reference

## Platform Library Versions

| Module | Version | Notes |
|---|---|---|
| `platform-events/v2` | v2.1.0 | Only the envelope type is used (the inbound handler decodes it); the publisher, outbox runner and consumer are not. Events v2 requires pgcommon v2. |
| `platform-gincommon` | v1.6.0 | Owns logs, metrics and traces. `Config.Domain` (`workflow`) and `Config.Environment` (`APP_ENV`: `local`, `dev`, `test`, `staging` or `prod`) are required. `InitTracingWithConfig` runs before `ObservabilityMiddlewares`, which fixes the metrics identity every other collector reads. Service metrics are built with `gincommon.NewCounterVec`, `NewCounter` and `NewHistogramVec` and are named `workflow_definition_service_<metric>`. |
| `platform-pgcommon/v2` | v2.0.1 | `port.Transactor` exposes `RunInTxWithRetry` (SERIALIZABLE + 40001/40P01 retry). `pgmetrics.InitWithIdentity` runs before `NewPool`, with the identity gincommon resolved; `Config.Tracer` is `gincommon.NewSpanTracer`. `Config.Logger` and `migrate.Runner.Logger` take the public `pkg/domain.Logger`, which `cmd/server/pglogger.go` adapts from `port.Logger`. |
| `workflow-models/v2` | v2.1.0-rc.1 | `pkg/dsl` types are field-for-field identical to the deleted `internal/core/domain/compiled_plan.go`; `pkg/enums` holds the shared `StageType*` constants. `CompiledCollaboration.SchemaVersion` (since v1.1.0) feeds Execution's DSL-compatibility layer; `CompileForPublish` stamps it on every published plan. `CallPlanStep` and `ExpandCalls` (since v1.3.0-rc.1) carry and expand library calls; every boundary sets exactly one of `TargetDept` and `Terminates`, and `Assignees` keys may be call paths (rc.2). `dsl.NodeKey`, `StageDef.CreatesHumanTask` and `StageDef.Name` (rc.3) give node keys, the human-task rule and task names, shared with Execution. `ExclusiveBranch.Steps` (v1.4.0-rc.1) carries an exclusive branch's work. Since v1.4.0-rc.2 there is no `call_pool` step, `messages`, `ignored` or department `ignore`, and the allowed element list admits the diagram-only elements modelers draw. `pkg/dsl/dsltest` holds the goldens `TestGolden` writes, and a golden change means a new tag before this repo can build against it. `ExpandCalls` refuses a zero budget (`ErrNoStageBudget`); every call passes `domain.MaxPlanStages`, and `enums.AllowedBPMNElements()` is a function. |
| `workflow-connectors/v2` | v2.0.0 | Only `pkg/registry` (the connector catalogue) is imported; `pkg/connectors` is never imported. |

Fetch / upgrade:
```bash
export GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*
go get github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2@v2.x.x
go mod tidy && go mod vendor
```

Never add a `replace` directive pointing to `./platform-libs/` — that directory is gitignored and does not exist in CI.

## CI/CD

| Workflow | Trigger | Jobs |
|---|---|---|
| `ci.yml` | push/PR to main | generate → (validate-quality, validate-test, lint-dockerfile → build-image-cache, trivy, smoke) parallel → GHCR push (`sha-<short>`/branch tags) + Cosign sign (push only) → PR summary comment (PR only) |
| `validate-quality.yml` | reusable (called by `ci.yml` and `release.yml`) | downloads caller's `generate` artifact; fmt, tidy, vet, lint, metrics-lint, govulncheck, Dockerfile pin check |
| `validate-test.yml` | reusable (called by `ci.yml` and `release.yml`) | downloads caller's `generate` artifact; arch-lint, unit + integration tests (testcontainers) with merged coverage (95% global + per-package floors) |
| `release.yml` | push `v*` tags | changelog-gate (a CHANGELOG entry for the tag) → generate → validate-quality + validate-test → build (binary) → docker (semver-tagged image + Cosign sign) → gh release |
| `changelog-check.yml` | PR touching `internal/`, `api/`, `cmd/` | fails unless `CHANGELOG.md` was also updated |

Branch protection required checks come from **two active GitHub rulesets** that both apply to this repo:

- **Org-wide** (`protect-main-branch`, applies to all BCBP repos): `Build image (cache)`, `Lint Dockerfile`, `Trivy CVE scan`, `Smoke tests`, `Validate / Quality / quality`, `Validate / Test / test`, `PR summary` — all satisfied by real job names as of this restructure.
- **Repo-specific** (this repo only): `Test`, `Build`, `Iint`, `vet`, `coverage` — stale, predates the validate-quality/validate-test consolidation. None of its 5 entries match a real job name anymore:
  - `Test`/`vet`/`coverage` haven't matched anything since that consolidation.
  - `Build` stopped matching once the standalone compile-only `Build` job was removed (redundant with `build-image-cache`'s Docker build — matches `iam`, which has no equivalent job at all).
  - `Iint` stopped matching once the standalone integration-test job was folded into `validate-test.yml`'s `test` job (matches `iam`, which has no separate integration-test job either — see below).
  - Whether this stale ruleset needs cleanup is a separate branch-protection admin decision, not resolved here.

`validate-quality.yml` and `validate-test.yml` are reusable workflows that download the caller's `generate` job's `generated` artifact (sqlc/mocks) rather than each regenerating it — `ci.yml` and `release.yml` both run their own `generate` job first and pass it downstream via `actions/upload-artifact`/`download-artifact`. Codegen now runs once per CI/release run instead of three times.

`validate-test.yml`'s **architecture lint** step (`go-arch-lint check --project-path .`) enforces the Clean Architecture import direction rules from `.go-arch-lint.yml`.

**Local developer workflow:** run `make fix` (gofmt + golangci-lint --fix) before `make check`. `make check` is read-only — it reports violations rather than fixing them, mirroring CI. `make fix` auto-fixes what it can; remaining lint errors must be resolved manually before `make check` will pass.

`validate-test.yml`'s `test` job runs real integration tests (testcontainers) in the same job as unit tests — no separate `Iint` job, matching iam's structure. Docker is available on `ubuntu-latest`.

Coverage is **merged** across suites: `make test` writes `.coverage/unit.out`, `make test-integration` writes `.coverage/integration.out`, `make merge-coverage` combines them (max-count-per-block, via `scripts/merge_coverage.py`) into `.coverage/coverage.out` — the single profile `cover-check-pkg` and the global **95%** gate (`coverage-gate.sh`) both read. `make test-ci` runs all three steps; `make check` and CI both call it. Generated packages (`postgres/db`, `mocks`) and the postgres repo adapter (`postgres/`) are excluded from coverage entirely (not just from one suite's gate).

## Prometheus Metrics

All metrics are centralized in `internal/observability` (`Register()`, called once from `cmd/server/wire.go`'s composition root) — not scattered `promauto` calls per package. Metric vars are nil until `Register` runs; call sites go through the package's nil-safe `IncCounter`/`IncCounterVec`/`ObserveHistogram` helpers instead of calling `.Inc()`/`.Observe()` directly, so a unit test that never calls `Register` (most of them) doesn't panic.

Key metrics emitted by the service:

| Metric | Type | Labels | Description |
|---|---|---|---|
| `workflow_definition_service_submissions_total` | CounterVec | `status` | Workflow create attempts |
| `workflow_definition_service_archive_total` | CounterVec | `status` | Archive attempts |
| `workflow_definition_service_publish_total` | CounterVec | `status` | Publish attempts |
| `workflow_definition_service_publish_latency_seconds` | Histogram | — | End-to-end publish latency (compile + DB tx) |
| `workflow_definition_service_clone_total` | CounterVec | `status` | Clone attempts |
| `workflow_definition_service_promote_total` | CounterVec | `status` | Promote attempts |
| `workflow_definition_service_compile_duration_seconds` | Histogram | — | Duration of `compiler.Compile` only, isolated from DB-tx time. Observed at `publishPreFlight` (Publish) and `resolvePlan` (Diff/Export fallback-recompile). `Promote` never compiles; `ValidationService.Validate` calls the separate `compiler.Validate`, not `Compile`. |
| `workflow_definition_service_validation_failures_total` | Counter | — | BPMN validate calls with at least one error |
| `workflow_definition_service_internal_events_ingest_total` | CounterVec | `event_type`, `result` | Internal event ingestion (ok/bad_payload/error) |
| `workflow_definition_service_cache_hits_total` | Counter | — | Compiled-plan cache hits (internal compiled-plan read) |
| `workflow_definition_service_cache_misses_total` | Counter | — | Compiled-plan cache misses (internal compiled-plan read) |
| `workflow_definition_service_rls_violations_total` | CounterVec | `violation_type` | Sampled RLS violations read from `rls_violation_log` by the exporter loop |

Outcome labels use `observability.OutcomeLabel(err)`: `"ok"` when err is nil, `"err"` otherwise.

`cmd/server/telemetry.go` fixes the metrics identity (`gincommon.ObservabilityMiddlewares`) before `observability.Register()`, `events.InitMetrics` and `pgmetrics.InitWithIdentity`, so every collector carries the `domain`, `service` and `environment` const labels. HTTP and database metrics come from the platform libraries under their standard names: `platform_http_requests_total`, `platform_http_request_duration_seconds`, `platform_db_queries_total`, `platform_db_transaction_retries_exhausted_total` and the `platform_db_pool_*` gauges. 

`make metrics-lint` (CI: `validate-quality`) runs `.github/scripts/check-metric-naming.sh` (collectors only from `internal/observability`, built with gincommon constructors, `workflow_definition_service_` prefix) and `metricslint` on the scrape from `TestStandard_LibrariesShareRegistryCollectors`, checks `deploy/` and `docs/` for retired names, and fails when `docs/observability/metric-registry.md` differs from `make metrics-inventory`. `test/integration/telemetry` asserts the `platform_db_*` series appear with a real pool.

Metrics are served at `GET /metrics` (Prometheus scrape endpoint, no auth — access control is enforced by network policy/ingress at deploy time, not the app).

## OTel Tracing

Configured via `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_TRACES_SAMPLER_RATIO`. Tracing is initialised by `gincommon.InitTracingWithConfig` in `cmd/server/wire.go`, with the same identity as metrics. `OTEL_EXPORTER_OTLP_ENDPOINT=none` turns export off. `buildEnvelope` stamps trace IDs only on traced requests (`trace.SpanFromContext(ctx).SpanContext().IsValid()`).

## Documentation

No static-site generator — docs live directly in the repo. `docs/` holds only diagram sources (`docs/architecture/`), the served OpenAPI spec (`docs/swagger/`), and the two standalone workflow-design guides (`bpmn-designer-guide.md`, `ui-enrichment-guide.md`). Everything else lives in root `README.md`, `ARCHITECTURE.md`, and `CONTRIBUTING.md`.

Keep docs in sync when making changes — see the full table in [`CONTRIBUTING.md` § Documentation update checklist](../CONTRIBUTING.md#documentation-update-checklist):

| Change type | Docs to update |
|---|---|
| New HTTP endpoint or field | `README.md` § API Overview |
| New internal endpoint | `api/internal-openapi.yaml`, `README.md` § Internal API; refresh the Execution Service's copy |
| New config env var | `ARCHITECTURE.md` § Configuration reference |
| New migration or schema change | `README.md` § Database |
| Architecture / layer change | `ARCHITECTURE.md` + `docs/architecture/mermaid/*.mmd` |
| Platform lib upgrade | `ARCHITECTURE.md` § Platform Libraries + this file |
| New coding pattern or decision | `CONTRIBUTING.md` § Coding Standards + numbered entry in `.claude/CLAUDE.md` + relevant sub-doc |

**Outbound tracing exception.** `platform-gincommon` offers no HTTP client transport, so `internal/adapter/outbound/http/transport.go` wraps the shared client in `otelhttp` directly; it is the only trace instrumentation outside gincommon, and every outbound client is built through it.
