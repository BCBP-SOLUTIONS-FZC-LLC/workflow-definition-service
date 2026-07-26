---
name: api-and-events
description: HTTP API patterns, gRPC, RLS injection, idempotency, event topology, and outbound client details for workflow-definition-service
metadata:
  type: reference
---

# API and Events Reference

## RLS GUC Injection: Two Paths

**HTTP path:** `InjectGUCSet` middleware (`middleware/gucrls.go`) bridges gincommon → pgcommon by reading `gincommon.RequestContext(c)` and calling `pgcommon.WithGUCSet(ctx, ...)` on `c.Request`. Placed immediately after `ProtectedMiddlewares` in `router.go`. Without this bridge, RLS filtering silently passes all rows.

**gRPC / internal-events path:** `GetCompiledWorkflow` and `POST /internal/events` handlers call `pgcommon.WithGUCSet` directly from `req.TenantId` / the envelope's `tenant_id` — there is no Gin context on these paths.

## Inbound Events: `POST /internal/events`

The service does **not** consume SQS in-process. The shared workflow-events consumer (separate service) forwards each `events.Envelope[json.RawMessage]` to this endpoint, which dispatches by `env.Type` to `versionSvc.HandleMembershipRevoked`.

Route is on the `/internal` group guarded by `RequireInternalToken` (`x-internal-token` vs `INTERNAL_API_TOKEN`, skipped when unset). Consumer retry contract:
- `2xx` — handled (incl. dedup no-op)
- `400` — malformed (non-retryable)
- `5xx` — transient (consumer should retry)

## Outbound Event Topology

Only **`workflow.template.published`** is emitted via the transactional outbox → SNS. It is a cache-warm push hint to Execution to pre-fetch the compiled plan via `GetCompiledWorkflow` gRPC before the first `StartWorkflow` call.

Three events were deliberately removed:
- `template.cloned` — clones are DRAFTs; Execution has no reason to react
- `template.archived` — Execution checks `status=ARCHIVED` via gRPC on demand; archive is non-destructive to in-flight instances
- `template.eligibility_invalidated` — replaced by a direct gRPC call (see below)

## Membership Revocation: Direct gRPC PauseUserTasks

When `HandleMembershipRevoked` finishes marking template versions `is_valid=false`, it calls `ExecutionClient.PauseUserTasks(ctx, tenantID, userID)` via gRPC. Execution pauses all active task assignments for that user scoped to that tenant.

- If the call fails the HTTP handler returns 5xx so the shared consumer retries the whole event.
- `invalidateVersion` silently skips versions that are absent or ARCHIVED — one missing version does not block others.
- All inter-service calls are gRPC — no HTTP between services.

## HTTP Idempotency Middleware

`handler.WithIdempotency` in `handler/idempotency.go`. Applied to all mutation endpoints via `idem(fn)` in `router.go`.

Cache key: `"idem:" + tenantID + ":" + routePath + ":" + Idempotency-Key` (tenant- and route-scoped).

- Only `2xx` responses are cached (TTL `IDEMPOTENCY_TTL`, env `IDEMPOTENCY_TTL`, default `24h`) along with a SHA-256 hash of the raw request body.
- On replay: matching hash → replay cached response; mismatched hash → 409 `IDEMPOTENCY_KEY_REPLAY`.
- Transparent when header is absent.

## Body Cap + Content-Type Enforcement

- `LimitRequestBody` middleware caps requests at 10 MB (`http.MaxBytesReader`); oversized bodies → 413 `PAYLOAD_TOO_LARGE`.
- `RequireJSONContentType` on `/api/v1` returns 415 `UNSUPPORTED_MEDIA_TYPE` when a request with a body is not `application/json`; body-less requests (GET, DELETE) pass through.

## Outbound Clients: Retries + Tests

`ExecutionClient` (gRPC) and `MembershipClient` (HTTP) retry on transient failures — 3 attempts, 50 ms / 100 ms / 500 ms bounded backoff — wrapping final failure as `ErrUpstreamUnavailable`.

Test patterns:
- `test/unit/executionclient/` — real local `net.Listen("tcp", "127.0.0.1:0")` gRPC server
- `test/unit/membershipclient/` — `httptest.NewServer`
- Both use hand-rolled fake server implementations (consistent with the handler-layer pattern)

## Compiled-Plan Cache (Valkey)

`GetCompiledWorkflow` gRPC lazily reads/populates `wf:plan:<tenant>:<version>` in Valkey (fail-open; TTL `CACHE_COMPILED_PLAN_TTL`, env `CACHE_COMPILED_PLAN_TTL`, default 1 h).

The Glue schema registry codec caches the schema version ID in-memory (TTL `GLUE_SCHEMA_CACHE_TTL`, env `GLUE_SCHEMA_CACHE_TTL`, default `5m`). Wired through `Config.GlueSchemaCacheTTL` → `gluecodec.NewCodec(..., cacheTTL)`.

- **Invalidated on:** `Archive` (status change) and `SetInvalid` membership path (is_valid change)
- **Not invalidated on:** Publish/promote (these don't change a cached version's fields)

## `buildEnvelope` Trace Propagation

`internal/core/service/helpers.go` stamps `events.WithTraceID` only when `trace.SpanFromContext(ctx).SpanContext().IsValid()` — no zero trace IDs on non-traced paths (unit tests). Use `trace.SpanFromContext`, not `gincommon.RequestContext`, because `buildEnvelope` lives at the service layer without access to the Gin context.

## Event Schema Governance (platform-schemagov)

`internal/eventschema/*.json` (Draft-07, one file per event) is derived from `api/asyncapi.yaml` via `make extract-schemas`; `validate-test.yml` runs `extract --check` on every PR/push to catch drift. `api/asyncapi.yaml` messages carry `x-lifecycle`/`x-owner` annotations. `platform-schemagov` is a Docker-shipped CLI (`ghcr.io/bcbp-solutions-fzc-llc/platform-schemagov:0.4`, not a Go import — pull requires `docker/login-action` + `GITHUB_TOKEN`, the image is private) — see the Makefile's `schema-*` targets.

**CLI flags are confirmed against iam-user-profile's working implementation** (same org, same tool), not the README's prose description — the two differ. Real flags (see `schema-registry.yml` for full flag sets; there is no `--workspace` flag on any command):

- `validate --asyncapi <file> --schema-dir <dir>`
- `extract --asyncapi <file> --schema-dir <dir> [--check]`
- `diff --current <file> --proposed <file> --schema-name <name>` — pure file-to-file, no AWS; the "vs. registry" comparison is done by fetching the live definition via `aws glue get-schema`/`get-schema-version` first, then diffing that fetched file
- `register --registry <name> --schema-dir <dir> --output <file> [--force]`
- `prune --registry <name> --env <name> --output <file> [--execute]`
- `usage-check` / `enforce-lifecycle` / `changelog` / `metrics`

**What works without AWS credentials**: `validate`/`extract`/`extract --check` (structure/Draft-07/enum-drift/lifecycle/open-schema/coverage — all local/git), `changelog-check.yml`, `freeze-watchdog.yml` (reads a GitHub Environment/repo variable via `gh api`, not AWS).

**Blocked until ops provisions AWS creds** (`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` with Glue write scoped to `GLUE_REGISTRY_ARN` — write access is deployment-pipeline-role only, per the design doc):

- `usage-check`/`enforce-lifecycle` (CloudWatch/Prometheus), `register`/`prune` (Glue write), and `schema-registry.yml`'s diff-vs-registry step (Glue read of the live registry).
- `schema-registry.yml`/`schema-prune.yml`/`schema-health-quarterly.yml` are already wired but no-op or fail loudly on their AWS-dependent steps until those secrets exist — expected, not a bug.
- `pr-check`'s AWS-dependent steps specifically degrade gracefully (skip, don't fail) since they run on every PR regardless of whether secrets are configured — see the "Check AWS credentials configured" step pattern (secrets can't be referenced directly in `if:` conditions, so it's piped through an `env:` + step output first).

**`CI_REPO_READ_TOKEN` fallback**: every `actions/checkout` step across all workflows uses `token: ${{ secrets.CI_REPO_READ_TOKEN || github.token }}`. Confirmed necessary in iam-user-profile (same GitHub org) — the default `GITHUB_TOKEN` failed with "repository not found" in some contexts. Falls back to the default token when the secret is unset (true today) — a no-op until/unless ops configures it.

The `scripts/localstack-init.sh` bootstrap (used by `make docker-up` and integration tests) reads the same `internal/eventschema/workflow_template_published.json` file to seed the fake local Glue registry, so local/CI behavior and the governed schema never drift apart.

## Swagger UI

`GET /swagger/*any` serves Swagger UI in `dev` mode only (gated by `cfg.AppEnv == "dev"`). The spec is served as a static file at `/api/openapi.yaml` from `docs/swagger/openapi.yaml`. No Swaggo annotations are used — the existing OpenAPI spec is the single source of truth.
