# REST API Reference

Full OpenAPI schema: [`openapi.yaml`](../openapi.yaml)

## Global headers

All requests through the Envoy gateway carry these headers (injected post-JWT verification):

| Header | Required | Description |
| --- | --- | --- |
| `x-tenant-id` | Yes | Tenant UUID — enforces RLS isolation |
| `x-user-id` | Yes | Executing user UUID |
| `x-tenant-roles` | Yes | Comma-separated roles, e.g. `tenant_admin,member` |
| `x-departments` | No | User department memberships |
| `x-plan` | No | Subscription tier: `starter`, `pro`, `enterprise` |
| `x-request-id` | No | Client correlation ID — echoed as `X-Request-ID` |
| `traceparent` | No | W3C trace context — echoed as `X-Trace-ID` |

Validated by `gincommon.RequireAuth`. Handlers access them via:

```go
rctx := gincommon.RequestContext(c)
// rctx.TenantID, rctx.UserID, rctx.Roles, rctx.TraceID
```

## Endpoint registry

| Method | Path | Auth | Description |
| --- | --- | --- | --- |
| `GET` | `/api/v1/workflows` | Any | List tenant workflows |
| `POST` | `/api/v1/workflows` | Admin | Create workflow + initial draft |
| `GET` | `/api/v1/workflows/:id` | Any | Workflow detail + version list |
| `GET` | `/api/v1/workflows/:id/versions` | Any | Paginated version history |
| `GET` | `/api/v1/workflows/:id/versions/:version_id` | Any | Version detail (XML + compiled plan) |
| `GET` | `/api/v1/workflows/:id/draft` | Any | Active draft detail |
| `POST` | `/api/v1/workflows/:id/draft` | Admin | Init draft from active version |
| `PUT` | `/api/v1/workflows/:id/draft` | Admin | Update draft XML/name/description |
| `DELETE` | `/api/v1/workflows/:id/draft` | Admin | Discard draft |
| `POST` | `/api/v1/workflows/:id/versions/:version_id/publish` | Admin | Compile & publish draft |
| `POST` | `/api/v1/workflows/:id/versions/:version_id/clone` | Admin | Clone to new workflow key |
| `POST` | `/api/v1/workflows/:id/versions/:version_id/promote` | Admin | Rollback/rollforward active pointer |
| `POST` | `/api/v1/workflows/:id/archive` | Admin | Archive workflow |
| `POST` | `/api/v1/workflows/validate` | Any | Stateless BPMN validation |
| `GET` | `/api/v1/workflows/:id/versions/:version_id/export` | Any | Download raw BPMN XML |
| `GET` | `/api/v1/workflows/:id/versions/:version_id/diff/:target_version_id` | Any | Structural diff between two versions |
| `GET` | `/healthz` | Public | Liveness probe |
| `GET` | `/readyz` | Public | Readiness probe (DB ping) |
| `GET` | `/metrics` | Public | Prometheus metrics |

**Admin** = requires `tenant_admin` or `tenant_owner` in `x-tenant-roles`.

## Error format (RFC-9457)

```json
{
  "type": "https://api.workflow.platform/errors/draft-already-exists",
  "title": "Draft Already Exists",
  "status": 409,
  "detail": "An active draft already exists for this workflow.",
  "instance": "/api/v1/workflows/abc/draft",
  "code": "DRAFT_ALREADY_EXISTS"
}
```

Validation errors include an `invalid_params` array:

```json
{
  "type": "https://api.workflow.platform/errors/validation-failed",
  "title": "BPMN Validation Failed",
  "status": 422,
  "detail": "BPMN semantic or structural validation failed.",
  "instance": "/api/v1/workflows/abc/versions/xyz/publish",
  "code": "BPMN_VALIDATION_FAILED",
  "invalid_params": [
    { "name": "Task_1", "reason": "Cycle detected at task node", "code": "CYCLE_DETECTED" }
  ]
}
```

## Error codes

| Code | HTTP | Trigger |
| --- | --- | --- |
| `NOT_FOUND` | 404 | Workflow or version not found, or RLS boundary breached |
| `DRAFT_NOT_FOUND` | 404 | No active draft exists for the workflow |
| `NO_ACTIVE_VERSION` | 404 | Workflow has no published active version |
| `UNAUTHORIZED` | 401 | Missing or invalid `x-user-id` / `x-tenant-id` headers |
| `FORBIDDEN` | 403 | Caller lacks `tenant_admin` or `tenant_owner` role |
| `DRAFT_ALREADY_EXISTS` | 409 | A draft already exists; publish or discard it first |
| `DUPLICATE_BUSINESS_KEY` | 409 | Business key already in use within the tenant |
| `DRAFT_CONCURRENCY` | 409 | Optimistic lock violation on draft save |
| `INVALID_VERSION_STATUS` | 409 | Operation not valid for the version's current status |
| `ACTIVE_INSTANCES_EXIST` | 409 | Cannot archive while running instances exist |
| `STRUCTURAL_DIVERGENCE` | 409 | Topology changed vs. active version; use `force_publish_structural` to override |
| `IDEMPOTENCY_KEY_REPLAY` | 409 | Same idempotency key submitted with a different payload |
| `PLAN_QUOTA_EXCEEDED` | 422 | Tenant has reached the maximum number of workflow templates for their plan |
| `ASSIGNEE_INELIGIBLE` | 422 | A default assignee no longer has the required department/role membership |
| `BPMN_VALIDATION_FAILED` | 422 | BPMN structural or semantic validation failed; see `invalid_params` |
| `UPSTREAM_UNAVAILABLE` | 503 | Execution Service or Org & Membership service unreachable |
| `INTERNAL_ERROR` | 500 | Unexpected server error |
