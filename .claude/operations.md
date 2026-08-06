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
| `platform-events` | v1.4.0 | `outbox.NewRunner` returns `(*Runner, error)`. Envelope carries `actor`/`subject` fields. `events.WithCodec` moves Glue Schema Registry wire-format encoding to SNS-publish time (`cmd/server/infra.go`'s `newPublisher`), not outbox-enqueue time — keeps the outbox's `json.Marshal(env)` working on a plain-JSON payload. |
| `platform-gincommon` | v1.2.0 | `pgcommon.Config.Logger` / `Config.Tracer` use unexported `port.Field` — cannot be wired externally. |
| `platform-pgcommon` | v1.1.1 | `port.Transactor` exposes `RunInTxWithRetry` (SERIALIZABLE + 40001/40P01 retry). gRPC health check registered for K8s liveness probes. |
| `workflow-models` | v1.1.0 | `pkg/dsl` types are field-for-field identical to the deleted `internal/core/domain/compiled_plan.go`; `pkg/events`/`pkg/enums` hold the shared `TemplatePublishedPayload`/`EventTypeTemplatePublished`. `CompiledCollaboration.SchemaVersion` (v1.1.0) feeds Execution's DSL-compatibility layer. |

Fetch / upgrade:
```bash
export GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*
go get github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon@v1.x.x
go mod tidy && go mod vendor
```

Never add a `replace` directive pointing to `./platform-libs/` — that directory is gitignored and does not exist in CI.

## CI/CD

| Workflow | Trigger | Jobs |
|---|---|---|
| `ci.yml` | push/PR to main | generate → (validate-quality, validate-test, lint-dockerfile → build-image-cache, trivy, smoke) parallel → GHCR push (`sha-<short>`/branch tags) + Cosign sign (push only) → PR summary comment (PR only) |
| `validate-quality.yml` | reusable (called by `ci.yml` and `release.yml`) | downloads caller's `generate` artifact; fmt, tidy, vet, lint, govulncheck, Dockerfile pin check |
| `validate-test.yml` | reusable (called by `ci.yml` and `release.yml`) | downloads caller's `generate` artifact; arch-lint, event-schema `extract --check`, unit + integration tests (testcontainers) with merged coverage (95% global + per-package floors) |
| `release.yml` | push `v*` tags | generate → validate-quality + validate-test → build (binary) → docker (semver-tagged image + Cosign sign) → gh release |
| `schema-registry.yml` | push to main / release / PR (paths: `api/asyncapi.yaml`, `internal/eventschema/*.json`) | validate → diff-vs-registry → register (staging on push, production on release; both AWS-gated) |
| `schema-prune.yml` | monthly cron (staging dry-run) + manual dispatch | reports/archives orphaned Glue schema versions (AWS-gated) |
| `schema-health-quarterly.yml` | quarterly cron | read-only lifecycle + version-accumulation report to Step Summary |
| `freeze-watchdog.yml` | every 4h | alerts if `SCHEMA_FREEZE` environment variable is stale (no AWS needed) |
| `changelog-check.yml` | PR touching `internal/`, `api/`, `cmd/` | fails unless `CHANGELOG.md` was also updated |

Branch protection required checks come from **two active GitHub rulesets** that both apply to this repo:

- **Org-wide** (`protect-main-branch`, applies to all BCBP repos): `Build image (cache)`, `Lint Dockerfile`, `Trivy CVE scan`, `Smoke tests`, `Validate / Quality / quality`, `Validate / Test / test`, `PR summary` — all satisfied by real job names as of this restructure.
- **Repo-specific** (this repo only): `Test`, `Build`, `Iint`, `vet`, `coverage` — stale, predates the validate-quality/validate-test consolidation. None of its 5 entries match a real job name anymore:
  - `Test`/`vet`/`coverage` haven't matched anything since that consolidation.
  - `Build` stopped matching once the standalone compile-only `Build` job was removed (redundant with `build-image-cache`'s Docker build — matches `iam`, which has no equivalent job at all).
  - `Iint` stopped matching once the standalone integration-test job was folded into `validate-test.yml`'s `test` job (matches `iam`, which has no separate integration-test job either — see below).
  - Whether this stale ruleset needs cleanup is a separate branch-protection admin decision, not resolved here.

`validate-quality.yml` and `validate-test.yml` are reusable workflows that download the caller's `generate` job's `generated` artifact (proto/sqlc/mocks) rather than each regenerating it — `ci.yml` and `release.yml` both run their own `generate` job first and pass it downstream via `actions/upload-artifact`/`download-artifact`. Codegen now runs once per CI/release run instead of three times.

`validate-test.yml`'s **architecture lint** step (`go-arch-lint check --project-path .`) enforces the Clean Architecture import direction rules from `.go-arch-lint.yml`.

**Local developer workflow:** run `make fix` (gofmt + golangci-lint --fix) before `make check`. `make check` is read-only — it reports violations rather than fixing them, mirroring CI. `make fix` auto-fixes what it can; remaining lint errors must be resolved manually before `make check` will pass.

`validate-test.yml`'s `test` job runs real integration tests (testcontainers) in the same job as unit tests — no separate `Iint` job, matching iam's structure. Docker is available on `ubuntu-latest`.

Coverage is **merged** across suites: `make test` writes `.coverage/unit.out`, `make test-integration` writes `.coverage/integration.out`, `make merge-coverage` combines them (max-count-per-block, via `scripts/merge_coverage.py`) into `.coverage/coverage.out` — the single profile `cover-check-pkg` and the global **95%** gate (`coverage-gate.sh`) both read. `make test-ci` runs all three steps; `make check` and CI both call it. Generated packages (`postgres/db`, `mocks`) and the postgres repo adapter (`postgres/`) are excluded from coverage entirely (not just from one suite's gate).

## Prometheus Metrics

Key metrics emitted by the service:

| Metric | Type | Labels | Description |
|---|---|---|---|
| `wf_submissions_total` | CounterVec | `status` | Workflow create attempts |
| `wf_archive_total` | CounterVec | `status` | Archive attempts |
| `wf_publish_total` | CounterVec | `status` | Publish attempts |
| `wf_publish_latency_seconds` | Histogram | — | End-to-end publish latency (compile + DB tx) |
| `wf_clone_total` | CounterVec | `status` | Clone attempts |
| `wf_promote_total` | CounterVec | `status` | Promote attempts |
| `wf_compile_duration_seconds` | Histogram | — | Duration of `compiler.Compile` only, isolated from DB-tx time. Observed at `publishPreFlight` (Publish) and `resolvePlan` (Diff/Export fallback-recompile). `Promote` never compiles; `ValidationService.Validate` calls the separate `compiler.Validate`, not `Compile`. |
| `wf_validation_failures_total` | Counter | — | BPMN validate calls with at least one error |
| `internal_events_ingest_total` | CounterVec | `event_type`, `result` | Internal event ingestion (ok/bad_payload/error) |
| `wf_cache_hits_total` | Counter | — | gRPC compiled-plan cache hits |
| `wf_cache_misses_total` | Counter | — | gRPC compiled-plan cache misses |

Outcome labels use `outcomeLabel(err)` helper: `"ok"` when err is nil, `"err"` otherwise.

DB connection-pool gauges (`pgmetrics.PoolStatsCollector`, registered in `cmd/server/wire.go` against the live pool): `pgcommon_pool_total_conns`, `pgcommon_pool_idle_conns`, `pgcommon_pool_acquired_conns`, `pgcommon_pool_max_conns`, `pgcommon_pool_constructing_conns`, `pgcommon_pool_empty_acquire_total` — all labelled with a constant `service` label.

Metrics are served at `GET /metrics` (Prometheus scrape endpoint, no auth — access control is enforced by network policy/ingress at deploy time, not the app).

## OTel Tracing

Configured via `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_TRACES_SAMPLER_RATIO`. Tracing is initialised by `platform-gincommon.InitTracingFromEnv()` in `cmd/server/app.go`. `buildEnvelope` stamps trace IDs only on traced requests (`trace.SpanFromContext(ctx).SpanContext().IsValid()`).

## Documentation

No static-site generator — docs live directly in the repo. `docs/` holds only diagram sources (`docs/architecture/`), the served OpenAPI spec (`docs/swagger/`), and the two standalone workflow-design guides (`bpmn-designer-guide.md`, `ui-enrichment-guide.md`). Everything else lives in root `README.md`, `ARCHITECTURE.md`, and `CONTRIBUTING.md`.

Keep docs in sync when making changes — see the full table in [`CONTRIBUTING.md` § Documentation update checklist](../CONTRIBUTING.md#documentation-update-checklist):

| Change type | Docs to update |
|---|---|
| New HTTP endpoint or field | `README.md` § API Overview |
| New gRPC method | `README.md` § API Overview |
| New config env var | `ARCHITECTURE.md` § Configuration reference |
| New migration or schema change | `README.md` § Database |
| Architecture / layer change | `ARCHITECTURE.md` + `docs/architecture/mermaid/*.mmd` |
| Platform lib upgrade | `ARCHITECTURE.md` § Platform Libraries + this file |
| New coding pattern or decision | `CONTRIBUTING.md` § Coding Standards + numbered entry in `.claude/CLAUDE.md` + relevant sub-doc |
