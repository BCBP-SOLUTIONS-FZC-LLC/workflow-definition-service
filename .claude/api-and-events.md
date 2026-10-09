---
name: api-and-events
description: HTTP API patterns, the internal API and its contracts, RLS injection, idempotency, event topology, and outbound client details for workflow-definition-service
metadata:
  type: reference
---

# API and Events Reference

## RLS GUC Injection: Two Paths

**HTTP path:** `InjectGUCSet` middleware (`middleware/gucrls.go`) bridges gincommon → pgcommon by reading `gincommon.RequestContext(c)` and calling `pgcommon.WithGUCSet(ctx, ...)` on `c.Request`. Placed immediately after `ProtectedMiddlewares` in `router.go`. Without this bridge, RLS filtering silently passes all rows.

**Internal path (`/api/v1/internal`):** no gateway identity reaches these routes, so `InjectGUCSet` is not on the group; each handler (the compiled-plan read, `POST /internal/events`) calls `pgcommon.WithGUCSet` itself from the `tenant_id` its request carries.

## Inbound Events: `POST /internal/events`

The service does **not** consume SQS in-process. The shared workflow-events consumer (separate service) forwards each `events.Envelope[json.RawMessage]` to this endpoint, which dispatches by `env.Type` to `versionSvc.HandleDepartmentMembershipRevoked` or `versionSvc.HandleTenantMembershipRevoked`.

Route is on the `/internal` group guarded by `RequireInternalToken` (`x-internal-token` vs `INTERNAL_API_TOKEN`; required outside dev, skipped when unset in dev). Consumer retry contract:
- `2xx` — handled (a redelivery reprocesses and converges; there is no dedup table)
- `400` — malformed (non-retryable)
- `5xx` — transient (consumer should retry)

## Outbound Event Topology

This service emits no outbound events and keeps no outbox, relay, SNS publisher or Glue codec. An event added later designs its contract and names a consumer first, and brings its own outbox in the same change.

## Membership Revocation: Direct PauseUserTasks Call

When `HandleDepartmentMembershipRevoked` finishes marking template versions `is_valid=false`, it calls `ExecutionClient.PauseUserTasks(ctx, tenantID, userID)`, the pause-tasks endpoint of the Execution Service's internal API. Execution pauses all active task assignments for that user scoped to that tenant.

- If the call fails the HTTP handler returns 5xx so the shared consumer retries the whole event.
- `invalidateVersion` silently skips versions that are absent or ARCHIVED — one missing version does not block others.
- Calls between the two services are internal HTTP, each endpoint specified in the server's `api/internal-openapi.yaml`; the caller keeps a copy in `api/contracts/`. `make contract-breaking` (oasdiff vs `origin/main`, in `make check`) and `make contract-check` (copy vs source branch) gate both in CI (LLD §3.4).

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

`ExecutionClient` and `MembershipClient` (both HTTP) retry on transient failures — 3 attempts, 50 ms / 100 ms / 500 ms bounded backoff — wrapping final failure as `ErrUpstreamUnavailable`.

Test patterns:
- `test/contract/` — `httptest.NewServer` for the execution client, each request held to `api/contracts/execution-internal-openapi.yaml`; the real router's internal responses held to `api/internal-openapi.yaml`
- `test/unit/membershipclient/` — `httptest.NewServer`

## Compiled-Plan Cache (Valkey)

The internal compiled-plan read (`CompiledPlanService`) lazily reads/populates `wf:plan:<tenant>:<version>` in Valkey (fail-open; TTL `CACHE_COMPILED_PLAN_TTL`, env `CACHE_COMPILED_PLAN_TTL`, default 1 h).


- **Invalidated on:** `Archive` (status change) and `SetInvalid` membership path (is_valid change)
- **Not invalidated on:** Publish/promote (these don't change a cached version's fields)

**`CI_REPO_READ_TOKEN` fallback**: every `actions/checkout` step across all workflows uses `token: ${{ secrets.CI_REPO_READ_TOKEN || github.token }}`. Confirmed necessary in iam-user-profile (same GitHub org) — the default `GITHUB_TOKEN` failed with "repository not found" in some contexts. Falls back to the default token when the secret is unset (true today) — a no-op until/unless ops configures it.

## Swagger UI

`GET /swagger/*any` serves Swagger UI in `dev` mode only (gated by `cfg.AppEnv == "dev"`). The spec is served as a static file at `/api/openapi.yaml` from `docs/swagger/openapi.yaml`. No Swaggo annotations are used — the existing OpenAPI spec is the single source of truth.
