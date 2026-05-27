# REST API Reference

Full OpenAPI schema: [`design/LLD/definition_openapi.yaml`](../../../repos/design/LLD/definition_openapi.yaml)

## Global headers

All requests through the Envoy gateway carry these headers (injected post-JWT verification):

| Header | Required | Description |
|---|---|---|
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
|---|---|---|---|
| `GET` | `/workflows` | Any | List tenant workflows |
| `POST` | `/workflows` | Admin | Create workflow + initial draft |
| `GET` | `/workflows/:id` | Any | Workflow detail + version list |
| `GET` | `/workflows/:id/versions` | Any | Paginated version history |
| `GET` | `/workflows/:id/versions/:vid` | Any | Version detail (XML + compiled plan) |
| `GET` | `/workflows/:id/draft` | Any | Active draft detail |
| `POST` | `/workflows/:id/draft` | Admin | Init draft from active version |
| `PUT` | `/workflows/:id/draft` | Admin | Update draft XML/name/description |
| `DELETE` | `/workflows/:id/draft` | Admin | Discard draft |
| `POST` | `/workflows/:id/versions/:vid/publish` | Admin | Compile & publish draft |
| `POST` | `/workflows/:id/versions/:vid/clone` | Admin | Clone to new workflow key |
| `POST` | `/workflows/:id/versions/:vid/promote` | Admin | Rollback/rollforward active pointer |
| `POST` | `/workflows/:id/archive` | Admin | Archive workflow |
| `POST` | `/workflows/validate` | Any | Stateless BPMN validation |
| `GET` | `/workflows/:id/versions/:vid/export` | Any | Download raw BPMN XML |
| `GET` | `/workflows/:id/versions/:a/diff/:b` | Any | Structural diff between two versions |
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

Validation errors include an `errors` array:

```json
{
  "code": "INVALID_BPMN_STRUCTURE",
  "errors": [
    { "code": "CYCLE_DETECTED", "node_id": "Task_1", "message": "Cycle detected at task node" }
  ]
}
```
