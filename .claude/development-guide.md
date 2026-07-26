---
name: development-guide
description: Extension cookbook, troubleshooting, and error-code reference for workflow-definition-service
metadata:
  type: reference
---

# Development Guide

## Extending the Service

- **New HTTP endpoint**: add a Gin handler in `internal/adapter/inbound/http/handler/`, map through a DTO helper (handler layer, `#7`), wire the route + middleware chain in `router.go`, add a hand-rolled fake to `testhelper_test.go` (`#8`).
- **New event type**: `internal/core/domain/eventpayloads.go` now only holds `EventSource` — a payload shared with the Execution Service (like `workflow.template.published`) is a change in the separate `workflow-models` repo/release (`pkg/events`/`pkg/enums`), not a local file edit; a Definition-only payload's struct still belongs here. Either way: add the message + `x-lifecycle`/`x-owner` block to `api/asyncapi.yaml`, run `make extract-schemas` to derive `internal/eventschema/<name>.json`, run `make schema-validate` before committing, enqueue via `outbox.Enqueue` inside a `pgcommon.RunInTx` callback — never call `publisher.Publish` directly.
- **New DB migration**: add a `NNNNNN_name.up.sql` / `.down.sql` pair to `db/migrations/` (golang-migrate, no `+goose` annotations), re-run `make generate` if queries changed, verify via `make test-integration`.
- **New `core/port` interface or method**: update the interface, run `make mock` to regenerate GoMock stubs — handler-layer tests use hand-rolled fakes instead (`#8`), so this only affects service-layer tests.
- **New gRPC method**: edit `proto/*.proto`, run `make generate` (buf), implement in `internal/adapter/inbound/grpc/`.

## Troubleshooting

- **RLS silently returns all rows / no tenant filtering** — the `InjectGUCSet` middleware bridge is missing from the route chain, or a service-layer/internal path calls `pgcommon.WithGUCSet` with the wrong tenant source. See `.claude/api-and-events.md` "RLS GUC Injection: Two Paths".
- **`make migrate` hangs or never returns against PgBouncer** — `DATABASE_URL` points at PgBouncer's transaction-pool port; golang-migrate needs a direct Postgres DSN for its session advisory lock. See `.claude/database.md`'s PgBouncer note; set `MIGRATION_DATABASE_URL` separately.
- **`go-arch-lint` fails on a new import** — the change violates `domain ← port ← service ← adapter`; check `.go-arch-lint.yml` for the allowed edges before restructuring the import.
- **Coverage gate fails just under 95%** — check `make cover-check-pkg` output for per-package floors (`COVER_PKG_FLOORS` in the Makefile) before assuming the global threshold is the problem; some packages (gRPC adapters, the AsyncAPI HTML renderer) have deliberately lower floors because they need a live connection/dev server to exercise fully.
- **`extract --check` fails in CI** — `internal/eventschema/*.json` has drifted from `api/asyncapi.yaml`; run `make extract-schemas` locally and commit the regenerated file.
- **`schema-register`/`schema-prune` fail with an AWS auth error** — expected until ops provisions `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` scoped to `GLUE_REGISTRY_ARN` for CI; see `.claude/api-and-events.md`'s AWS-now-vs-later split.

## Appendix — Error Codes

All non-2xx HTTP responses use the RFC-9457 `problemDetails` envelope (`internal/adapter/inbound/http/handler/errors.go`); `code` is one of:

| Code | HTTP Status | Meaning |
| --- | --- | --- |
| `BAD_REQUEST` | 400 | JSON bind failure or other malformed request |
| `INVALID_BPMN_XML` | 400 | BPMN document could not be parsed |
| `UNAUTHORIZED` | 401 | Missing/invalid identity |
| `FORBIDDEN` | 403 | Authenticated but not permitted |
| `PLAN_QUOTA_EXCEEDED` | 403 | Tenant plan quota exceeded |
| `NOT_FOUND` | 404 | Resource does not exist |
| `DRAFT_NOT_FOUND` | 404 | No draft exists for this workflow |
| `NO_ACTIVE_VERSION` | 409 | Workflow has no active/published version |
| `DRAFT_ALREADY_EXISTS` | 409 | Draft already exists for this workflow |
| `DUPLICATE_BUSINESS_KEY` | 409 | Workflow key already in use |
| `DRAFT_CONCURRENCY` | 409 | Stale `record_version` (optimistic lock) |
| `INVALID_VERSION_STATUS` | 409 | Version not in the required DRAFT/PUBLISHED state for this operation |
| `ACTIVE_INSTANCES_EXIST` | 409 | Cannot archive — Execution reports RUNNING/PAUSED instances |
| `STRUCTURAL_DIVERGENCE` | 409 | Compiled DSL diverges from the stored artifact |
| `IDEMPOTENCY_KEY_REPLAY` | 409 | `Idempotency-Key` reused with a different request body |
| `ASSIGNEE_INELIGIBLE` | 422 | Default assignee failed the Org & Membership eligibility check |
| `BPMN_VALIDATION_FAILED` | 422 | Structural/semantic BPMN validation failed (see `invalid_params`) |
| `INVALID_INPUT` | 422 | Request field(s) exceed maximum length |
| `PAYLOAD_TOO_LARGE` | 413 | Request body exceeds the 10 MB limit |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | Non-JSON body on `/api/v1` |
| `UPSTREAM_UNAVAILABLE` | 503 | Execution/Membership gRPC or HTTP call failed after retries |
| `INTERNAL_ERROR` | 500 | Unhandled error |

BPMN-specific validation error codes (`REJECTED_ELEMENT`, `CYCLE_DETECTED`, `UNGUARDED_LOOP`, etc., set inside `invalid_params` on a `BPMN_VALIDATION_FAILED` response) are listed in `.claude/bpmn-compiler.md`, not duplicated here.
