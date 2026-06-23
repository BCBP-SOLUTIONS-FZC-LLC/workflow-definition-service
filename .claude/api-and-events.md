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

- Only `2xx` responses are cached (24 h TTL) along with a SHA-256 hash of the raw request body.
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

`GetCompiledWorkflow` gRPC lazily reads/populates `wf:plan:<tenant>:<version>` in Valkey (fail-open; TTL `CACHE_COMPILED_PLAN_TTL`, default 1 h).

- **Invalidated on:** `Archive` (status change) and `SetInvalid` membership path (is_valid change)
- **Not invalidated on:** Publish/promote (these don't change a cached version's fields)

## `buildEnvelope` Trace Propagation

`internal/core/service/helpers.go` stamps `events.WithTraceID` only when `trace.SpanFromContext(ctx).SpanContext().IsValid()` — no zero trace IDs on non-traced paths (unit tests). Use `trace.SpanFromContext`, not `gincommon.RequestContext`, because `buildEnvelope` lives at the service layer without access to the Gin context.

## Swagger UI

`GET /swagger/*any` serves Swagger UI in `dev` mode only (gated by `cfg.AppEnv == "dev"`). The spec is served as a static file at `/swagger/openapi.yaml` from `api/openapi.yaml`. No Swaggo annotations are used — the existing OpenAPI spec is the single source of truth.
