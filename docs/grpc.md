# gRPC API Reference

Proto source: [`api/proto/definition/v1/definition.proto`](../api/proto/definition/v1/definition.proto)

## Service: `DefinitionService`

High-throughput internal gRPC endpoint. Called by the Execution Service and Temporal Workers during runtime workflow instantiation to fetch compiled DSL plans. Bypasses the Envoy REST gateway to eliminate serialisation overhead.

**Transport:** The gRPC server accepts insecure plain-text connections on the intra-cluster network. mTLS is enforced at the Envoy sidecar layer — connections from outside the mesh are rejected there, not at the server. This means `grpcurl -plaintext` works from inside the cluster or locally.

**Reflection:** Server reflection is registered (`reflection.Register`), so `grpcurl` works without passing proto files:</p>

```bash
# List all services
grpcurl -plaintext localhost:9090 list

# Describe the service
grpcurl -plaintext localhost:9090 describe definition.v1.DefinitionService

# Call GetCompiledWorkflow
grpcurl -plaintext \
  -d '{"tenant_id":"<uuid>","workflow_version_id":"<uuid>"}' \
  localhost:9090 definition.v1.DefinitionService/GetCompiledWorkflow
```

### `GetCompiledWorkflow`

```protobuf
rpc GetCompiledWorkflow(GetCompiledWorkflowRequest)
    returns (GetCompiledWorkflowResponse);
```

> **Request**

| Field | Type | Description |
| --- | --- | --- |
| `tenant_id` | `string` | Tenant UUID — mandatory; sets RLS session GUC before any DB access |
| `workflow_version_id` | `string` | UUID of the version record to fetch |

> **Response**

| Field | Type | Description |
| --- | --- | --- |
| `workflow_id` | `string` | Parent workflow UUID |
| `version_id` | `string` | Requested version UUID |
| `version_number` | `int32` | Published version number |
| `status` | `string` | `DRAFT`, `PUBLISHED`, or `ARCHIVED` |
| `is_valid` | `bool` | `false` if any default assignee has become ineligible |
| `compiled_plan_json` | `string` | Pre-compiled ExecutionPlan DSL as a JSON string |

The `version` is carried on the response (`version_number`), not inside the DSL blob.

#### Compiled DSL shape (`compiled_plan_json`)

`{ name, task_queue, departments[], execution: { steps[] } }`. Each step in `execution.steps` is one of:

| Step kind | Shape | Meaning |
| --- | --- | --- |
| `sequential` | `["deptA", "deptB"]` | departments run in order |
| `parallel` | `["deptA", "deptB"]` | departments run concurrently (AND split/join) |
| `exclusive` | `[{ branch }, …]` | XOR decision — runtime evaluates each branch's `condition_expression` |

Each `exclusive` branch object:

| Field | Meaning |
| --- | --- |
| `target` | Destination department for a forward branch. **Empty** when the branch terminates the workflow (routes to the end event), and empty on a revert branch. |
| `condition_expression` | Expression the Execution Service / Temporal worker evaluates to pick the branch. |
| `revert_to_dept` / `revert_to_stage` | Present on a **guarded-loop revert branch** (back-edge from the gateway) instead of `target`: the `(department, stage_type)` to send the task back to. The Execution Service realises this via its `stage-defer` / `stage-transition` signals and must enforce a loop-iteration bound. |

> **gRPC status codes**

| Code | Meaning |
| --- | --- |
| `OK` (0) | Success |
| `INVALID_ARGUMENT` (3) | Missing or malformed `workflow_version_id` |
| `PERMISSION_DENIED` (7) | `tenant_id` is empty (no tenant context) |
| `NOT_FOUND` (5) | Version not found or RLS filtered it out |
| `INTERNAL` (13) | Unexpected server error |

> **Compiled-plan cache.** Responses are cached in Valkey under
> `wf:plan:<tenant_id>:<version_id>` (TTL `CACHE_COMPILED_PLAN_TTL`, default 1h),
> populated lazily on the first read. The cache is fail-open — a cache outage falls
> back to Postgres. Entries are invalidated when a version's `status` or `is_valid`
> changes (workflow archive, membership-revocation invalidation).

---

## Health Check (`grpc.health.v1.Health`)

The server registers the standard gRPC health service at startup:

```go
grpc_health_v1.RegisterHealthServer(grpcSrv, health.NewServer())
```

`health.NewServer()` reports `SERVING` for all service names by default. Use this for Kubernetes liveness/readiness probes on the gRPC port (`:9090`) and for service-mesh health checks:

```bash
# Check overall server health
grpcurl -plaintext localhost:9090 grpc.health.v1.Health/Check

# Check a specific service
grpcurl -plaintext \
  -d '{"service":"definition.v1.DefinitionService"}' \
  localhost:9090 grpc.health.v1.Health/Check
```

Expected response:

```json
{ "status": "SERVING" }
```

---

## Code generation

Proto stubs are generated via `make generate` (runs `buf generate`). Output goes to `gen/proto/` which is **gitignored** — regenerate locally before building:

```bash
make generate
```
