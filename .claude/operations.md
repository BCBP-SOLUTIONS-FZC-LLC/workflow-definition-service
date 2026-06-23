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
| `platform-events` | v1.2.0 | `outbox.NewRunner` returns `(*Runner, error)`. `actor`/`subject` envelope fields not yet available (v1.3.0 unreleased). |
| `platform-gincommon` | v1.2.0 | `pgcommon.Config.Logger` / `Config.Tracer` use unexported `port.Field` — cannot be wired externally. |
| `platform-pgcommon` | v1.1.1 | `port.Transactor` exposes `RunInTxWithRetry` (SERIALIZABLE + 40001/40P01 retry). gRPC health check registered for K8s liveness probes. |

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
| `ci.yml` | push/PR to main | generate → (Build, vet, Test, coverage, Iint, lint) parallel |
| `validate.yml` | reusable | fmt, tidy, vet, lint, govulncheck, unit tests |
| `release.yml` | push `v*` tags | generate → validate → Build → gh release |

Branch protection required checks: `generate`, `Build`, `vet`, `Test`, `coverage`, `Iint`, `lint`.

The `vet` job now includes an **architecture lint** step (`go-arch-lint check --project-path .`) that enforces the Clean Architecture import direction rules from `.go-arch-lint.yml`.

The `Iint` job runs real integration tests (testcontainers) in CI — Docker is available on `ubuntu-latest`.

The `coverage` job enforces a unit-test-only threshold of **96%**. Generated packages (`postgres/db`, `mocks`) and the postgres repo adapter (`postgres/`) are excluded — they are integration-tested by `Iint`. The integration coverage profile is uploaded as a separate artifact but is not merged into the gate.

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
| `wf_validation_failures_total` | Counter | — | BPMN validate calls with at least one error |
| `internal_events_ingest_total` | CounterVec | `event_type`, `result` | Internal event ingestion (ok/bad_payload/error) |
| `wf_cache_hits_total` | Counter | — | gRPC compiled-plan cache hits |
| `wf_cache_misses_total` | Counter | — | gRPC compiled-plan cache misses |

Outcome labels use `outcomeLabel(err)` helper: `"ok"` when err is nil, `"error"` otherwise.

Metrics are served at `GET /metrics` (Prometheus scrape endpoint, no auth).

## OTel Tracing

Configured via `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_TRACES_SAMPLER_RATIO`. Tracing is initialised by `platform-gincommon.InitTracingFromEnv()` in `cmd/server/app.go`. `buildEnvelope` stamps trace IDs only on traced requests (`trace.SpanFromContext(ctx).SpanContext().IsValid()`).

## Documentation (MkDocs)

Docs live in `docs/` and are served via MkDocs (`make docs-serve`). The nav is declared in `mkdocs.yml`.

Keep docs in sync when making changes:

| Change type | Docs to update |
|---|---|
| New HTTP endpoint or field | `docs/api.md` |
| New gRPC method | `docs/grpc.md` |
| New config env var | `docs/configuration.md` |
| New migration or schema change | `docs/database.md` |
| Architecture / layer change | `docs/architecture.md` + `docs/architecture/*.mmd` |
| Platform lib upgrade | `docs/platform-pgcommon.md`, `docs/platform-events.md`, or `docs/platform-gincommon.md` + this file |
| New coding pattern or decision | `docs/standards.md` + numbered entry in `.claude/CLAUDE.md` + relevant sub-doc |

After any significant feature PR, run `make docs-serve` and verify the affected pages render correctly before merging.
