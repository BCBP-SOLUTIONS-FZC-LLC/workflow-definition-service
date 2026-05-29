# gRPC API Reference

Proto source: [`proto/definition/v1/definition.proto`](../proto/definition/v1/definition.proto)

## Service: `DefinitionService`

High-throughput internal gRPC endpoint. Called by the Execution Service and Temporal Workers during runtime workflow instantiation to fetch compiled DSL plans. Bypasses the Envoy REST gateway to eliminate serialisation overhead.

Transport: mTLS enforced by the Envoy sidecar. Plain-text connections are rejected.

### `GetCompiledWorkflow`

```protobuf
rpc GetCompiledWorkflow(GetCompiledWorkflowRequest)
    returns (GetCompiledWorkflowResponse);
```

**Request**

| Field | Type | Description |
|---|---|---|
| `tenant_id` | `string` | Tenant UUID — mandatory; sets RLS session GUC before any DB access |
| `workflow_version_id` | `string` | UUID of the version record to fetch |

**Response**

| Field | Type | Description |
|---|---|---|
| `workflow_id` | `string` | Parent workflow UUID |
| `version_id` | `string` | Requested version UUID |
| `version_number` | `int32` | Published version number |
| `status` | `string` | `DRAFT`, `PUBLISHED`, or `ARCHIVED` |
| `is_valid` | `bool` | `false` if any default assignee has become ineligible |
| `compiled_plan_json` | `string` | Pre-compiled ExecutionPlan DSL as a JSON string |

**gRPC status codes**

| Code | Meaning |
|---|---|
| `OK` (0) | Success |
| `INVALID_ARGUMENT` (3) | Missing or malformed `tenant_id` / `workflow_version_id` |
| `NOT_FOUND` (5) | Version not found or RLS filtered it out |
| `PERMISSION_DENIED` (7) | Tenant validation failed |
| `INTERNAL` (13) | Unexpected server error |

---

## Code generation

Proto stubs are generated via `make generate` (runs `buf generate`). Output goes to `gen/proto/` which is **gitignored** — regenerate locally before building:

```bash
make generate
```
