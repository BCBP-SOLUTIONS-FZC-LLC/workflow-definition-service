# Runbooks: workflow-definition-service alerts

Runbooks for every alert in [`deploy/monitoring/app-alerts.yml`](../../deploy/monitoring/app-alerts.yml) and the Helm [`PrometheusRule`](../../deploy/helm/templates/prometheusrule.yaml) (the same rules). The heading is the alert name, and the alert's `runbook` annotation (`workflow-definition-service/<slug>`) names the entry.

Common starting points:

- Pods: `kubectl get pods -l app.kubernetes.io/name=workflow-definition-service`, then `kubectl logs` for the failing pod; log lines carry `trace_id` and `request_id`.
- Error bodies are RFC 9457 problem documents with a stable `code`, `trace_id` and `request_id`.
- Readiness: `GET /readyz` answers 503 when Postgres is unreachable or the pod is draining, and 200 with `cache: degraded` when Valkey does not answer; the cache and idempotency then run without it.
- Metric names are in [`metric-registry.md`](metric-registry.md).

## Availability

### WorkflowDefinitionServiceDown / WorkflowDefinitionServiceReplicasMissing

`runbook: workflow-definition-service/service-down`

**Meaning.** No pod (or fewer than two) has been scraped as healthy. Design-time edits fail, and the Execution Service cannot read compiled plans it has not cached.

**Triage.**
1. `kubectl describe pod` for crash loops, probe failures and pending pods; check node pressure and the HPA.
2. Startup refuses bad configuration and names the missing or invalid setting in the first log line.
3. The schema is applied by the migrate job, not at boot: a pod that fails on a missing relation means the job has not run for this image.

**Mitigate.** Roll back the last deploy, or fix the named setting.

## HTTP errors and latency

### WorkflowDefinitionServiceHighErrorRate / WorkflowDefinitionServiceCriticalErrorRate

`runbook: workflow-definition-service/high-error-rate`

**Meaning.** More than 5% (20% for critical) of requests return 5xx.

**Triage.**
1. Group by route: `sum by (route) (rate(platform_http_requests_total{job="workflow-definition-service",status_class="5xx"}[5m]))`.
2. Check Postgres, PgBouncer and Valkey health, and whether a deploy started at the same time.
3. Follow a `trace_id` from a failing response into the logs and traces.

**Mitigate.** Roll back the last deploy; restore the failing dependency.

### WorkflowDefinitionServiceHighLatency / WorkflowDefinitionServiceCriticalLatency

`runbook: workflow-definition-service/high-latency`

**Meaning.** p99 request duration is above 2 s (25 s for critical, close to the application timeouts).

**Triage.** Find the slow route with `histogram_quantile(0.99, sum by (le, route) (rate(platform_http_request_duration_seconds_bucket{job="workflow-definition-service"}[5m])))`. Slow queries are logged past `PG_SLOW_QUERY_THRESHOLD`. A slow publish or `/validate` points at the compiler on a large diagram; a slow read points at the cache or the database.

### WorkflowDefinitionServiceCompiledPlanHighErrorRate / WorkflowDefinitionServiceCompiledPlanHighLatency

`runbook: workflow-definition-service/compiled-plan`

**Meaning.** The compiled-plan read, the one call the Execution Service makes synchronously, is failing or slow.

**Triage.**
1. Compare `workflow_definition_service_cache_hits_total` with `workflow_definition_service_cache_misses_total`: a cold cache under load is the usual cause of slowness.
2. Check Postgres and Valkey health. A version that is not found is a 404 and does not count as an error.
3. Execution retries the read and keeps serving instances whose plan it holds; check its `UpstreamDependencyErrors` alert for the other side.

### WorkflowDefinitionServiceHighValidationFailureRate

`runbook: workflow-definition-service/validation-failures`

**Meaning.** `POST /api/v1/workflows/validate` is rejecting diagrams at a sustained rate.

**Triage.** The response of a rejected request lists what failed. A change in the canvas modeler that emits a construct the validator refuses produces a burst from one caller; a single user editing produces a trickle. The counter is incremented by the standalone endpoint only, so it is not a share of submissions.

## Publish

### WorkflowDefinitionServicePublishLatencyDegraded

`runbook: workflow-definition-service/publish`

**Meaning.** p95 of a publish (compile plus the database transaction) is above 2 s.

**Triage.** Check the size of the diagram and the module graph being compiled, and Postgres load. Publish runs in a serializable transaction, so concurrent publishes of one workflow lengthen each other.

### WorkflowDefinitionServicePublishRetryExhausted

`runbook: workflow-definition-service/publish`

**Meaning.** A transaction hit deadlock or serialization conflicts repeatedly and gave up; the caller received an error and nothing was committed.

**Triage.** The metric covers every transaction that runs with retry: workflow create, module publish and version operations. Look for a burst of concurrent writes against one workflow or module (a script, or a double submit). The caller can retry; the state is unchanged.

## Events

### WorkflowDefinitionServiceInternalEventIngestFailures

`runbook: workflow-definition-service/event-ingest`

**Meaning.** More than five inbound events in 15 minutes ended as `bad_payload` or `error`.

**Triage.**
1. `bad_payload` means the envelope or payload does not decode: compare the event type's schema with the consumer's copy; the consumer dead-letters the message once its receive threshold is spent.
2. `error` means the handler failed (database, or the departure handler); the consumer redelivers with backoff until its receive threshold (`MAX_RECEIVE_COUNT`, four handling attempts) dead-letters the message on the fifth receive.
3. See the consumer's runbook for the queue and dead-letter side.

### WorkflowDefinitionServiceTenantPurgeFailing

`runbook: workflow-definition-service/tenant-purge`

**Meaning.** A departed tenant's workflows, modules, starters or connector credentials could not be fully deleted after the retention period. The sweep retries on its next run.

**Triage.** The `tenant purge failed` log line names the tenant and the error. Connector credentials (30 days after departure) and workflows, modules and starters (7 years) are two independent steps of the same sweep with separate completion marks, so the error text says which store failed and the other step is not held up. Nothing needs doing once a later run succeeds.

## Dependencies

### WorkflowDefinitionServiceDBErrorRate

`runbook: workflow-definition-service/db-errors`

**Meaning.** More than 5% of queries error.

**Triage.** Check Postgres and PgBouncer (pool exhaustion, failover, a long lock).

### WorkflowDefinitionServiceLowCacheHitRatio

`runbook: workflow-definition-service/cache-hit-ratio`

**Meaning.** More than half of compiled-plan reads miss the cache for 30 minutes. An efficiency signal, not an incident.

**Triage.** Check Valkey availability and `CACHE_COMPILED_PLAN_TTL`. A recent mass publish or a Valkey restart explains a temporary dip.

## Panics and tenant isolation

### WorkflowDefinitionServiceHTTPPanic

`runbook: workflow-definition-service/panic`

**Meaning.** A handler panicked and the middleware recovered it. This should never happen.

**Triage.** The stack trace is in the logs next to the request's `trace_id`; open a defect with the request shape.

### WorkflowDefinitionServiceRLSMissingGUC

`runbook: workflow-definition-service/rls`

**Meaning.** A connection queried a tenant table without a valid tenant setting. The count is a 1% sample.

**Triage.** The sampled rows are in the `rls_violation_log` table (query text and violation type); they are pruned after the retention period, so read them early. A route registered without the tenant middleware, or a raw pool session, is the usual cause.

### WorkflowDefinitionServiceRLSCrossTenantAccess

`runbook: workflow-definition-service/rls`

**Meaning.** Row-level security blocked a session that touched another tenant's rows. The count is a 1% sample.

**Triage.** Treat as a security event: read the sampled row in `rls_violation_log`, and check for a service bug that passes the wrong tenant and for probing.
