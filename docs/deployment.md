# Deployment

This service ships a container image and a Helm chart (`deploy/helm/`) for running it on Kubernetes, plus a set of static Prometheus rule files (`deploy/monitoring/`) for environments that don't run the Prometheus Operator CRDs.

## Container image

Multi-stage `Dockerfile`: a `golang:1.26-alpine` builder (private-module access via a BuildKit secret, `GOPRIVATE`-aware) compiles a stripped, trimmed static binary, copied into a `gcr.io/distroless/static-debian12:nonroot` runtime — no shell, no package manager, runs as UID `65532` by default. Both base images are pinned by digest (`make pin-base-images` refreshes them; tracked in `.docker-digests`).

```bash
make docker-build              # builds workflow-definition-service:local (requires GO_PRIVATE_TOKEN)
make docker-lint                # Hadolint
make docker-trivy               # HIGH/CRITICAL CVE scan (source + deps)
make docker-check                # both, no image build required
```

The image exposes two ports:

| Port | Protocol | Purpose |
| --- | --- | --- |
| `8080` | HTTP | REST API (`/api/v1`), `/healthz`, `/readyz`, `/metrics` |
| `9090` | gRPC | `GetCompiledWorkflow` — called by the Execution Service |

The image's built-in `HEALTHCHECK` directive is best-effort for standalone `docker run` use; Kubernetes ignores it entirely and uses its own `startupProbe`/`livenessProbe`/`readinessProbe` (below) against `/healthz` and `/readyz` instead — those are the health signals that actually gate traffic and restarts in a cluster.

## Helm chart (`deploy/helm/`)

```text
deploy/helm/
  Chart.yaml
  values.yaml
  templates/
    deployment.yaml        service.yaml        serviceaccount.yaml
    secret.yaml             migrate-job.yaml     hpa.yaml
    pdb.yaml                 networkpolicy.yaml   servicemonitor.yaml
    prometheusrule.yaml    ingress.yaml          httproute.yaml
    securitypolicy.yaml    _helpers.tpl          NOTES.txt
```

```bash
helm lint ./deploy/helm
helm template my-release ./deploy/helm --set secretValues.DATABASE_URL=... # ... (see Secrets below)
helm upgrade workflow-definition-service ./deploy/helm --install --namespace <ns>
```

### Ports and probes

The Service and Deployment both expose named `http` (8080) and `grpc` (9090) ports. `startupProbe`/`livenessProbe`/`readinessProbe` all target `http` — `/healthz` is a trivial liveness check, `/readyz` actually verifies the Postgres pool and Valkey are reachable (`cmd/server/handlers.go`), so a pod only receives traffic once its real dependencies are up.

### Migrations run as a Helm hook, not at boot

`cmd/server/main.go` never migrates on the normal server boot path — schema changes are applied by a dedicated `migrate` subcommand (`/server migrate`) that runs the outbox schema and this service's own domain migrations, then exits. Running that inline at boot would let two replicas race on `golang-migrate`'s advisory lock during a rolling deploy, stalling the loser and risking a readiness-probe timeout for no reason.

The chart wires this up as `templates/migrate-job.yaml`, a Kubernetes `Job` annotated `helm.sh/hook: pre-install,pre-upgrade`. Helm runs it — and waits for it to complete — before rolling out the Deployment, so every `helm upgrade --install` applies pending migrations first, sequentially, with no replica race. It's gated by `migrationJob.enabled` (default `true`) in case migrations are ever driven out-of-band instead.

One consequence worth knowing: `cmd/server/main.go` loads and validates the full `Config` *before* it even checks whether it was invoked as `migrate` — so the migrate Job needs the exact same required environment variables and secrets as the main Deployment (not just `DATABASE_URL`), or config validation fails before a single migration runs. The chart template already reuses the same `env`/`envFromSecret` blocks as the Deployment for this reason.

### Ports, resources and scaling

Base replica count is `2`, with the `HorizontalPodAutoscaler` floor matching it (`autoscaling.minReplicas: 2`) — two AZ-spread replicas is the minimum for this service to survive a single-AZ outage without downtime. `resources.requests`/`limits` (`250m`/`512Mi` request, `500m`/`1024Mi` limit) size for BPMN parsing and DSL compilation, which allocate meaningfully more per-request than a typical CRUD handler — the memory request in particular is set above a bare-minimum Go service to leave headroom for larger process diagrams without OOM-killing a pod mid-publish. `targetCPUUtilizationPercentage`/`targetMemoryUtilizationPercentage` (`70`/`80`) scale out before either resource is saturated. An optional RPS-based scaling metric (`autoscaling.targetRPSPerReplica`) is wired into the `HorizontalPodAutoscaler` template but left unset by default — it requires `deploy/monitoring/prometheus-adapter-rule.yaml` to be installed in the cluster first (see Observability below).

`podDisruptionBudget.minAvailable` is `1`: at a 2-replica floor, `minAvailable: 2` would block every voluntary disruption (node drains, cluster upgrades) since both pods would always have to stay up — `1` allows exactly one pod to be evicted at a time while guaranteeing the service never drops to zero replicas from a voluntary action.

`terminationGracePeriodSeconds` is `60`. `cmd/server/app.go`'s shutdown sequence is worth understanding directly rather than assuming a round number is safe: on `SIGTERM` it runs, in order, an HTTP `Shutdown(ctx)` bounded by a single 30-second context, then `grpcServer.GracefulStop()` — which takes **no** context at all and blocks unboundedly until in-flight RPCs drain — then the outbox relay's own `Stop()` (also uncancellable, with its own ~30-second internal drain default), then a final pool drain bounded by whatever remains of the original 30-second deadline. In practice the gRPC surface here is short-lived unary calls and outbox batches are small, so this finishes well under 30 seconds — but nothing in the code guarantees it, so the grace period carries real headroom above the worst case rather than assuming the 30-second figure caps the whole sequence.

### Secrets

Two mutually exclusive modes, selected by whether `existingSecret` is set:

- **`existingSecret: "<name>"`** (recommended for anything beyond local testing) — point at a Secret already provisioned by External Secrets Operator, the AWS Secrets Manager CSI driver, or Sealed Secrets. The chart creates no Secret resource of its own. Bump `rotationEpoch` (`--set rotationEpoch=$(date +%s)`) to force a rollout after the external secret rotates.
- **`secretValues.*`** (dev/CI only) — pass real values via `--set` or a Helm secrets plugin. The chart's `secret.yaml` template hard-fails the render if any key required by `envFromSecret` (`DATABASE_URL`, `MIGRATION_DATABASE_URL`, `VALKEY_PASSWORD`, `SNS_TOPIC_ARN`, `INTERNAL_API_TOKEN`) is empty, rather than silently deploying a pod that will crash-loop on missing config. These land in the Helm release history unencrypted (base64) — never use this mode against a shared cluster.

### Networking

`networkPolicy.enabled: true` by default. Ingress is split by port on purpose: the gateway/ingress-controller rule only opens `8080` (nothing fronts the gRPC port externally), while an intra-namespace rule opens both `8080` and `9090` so the Execution Service can reach `GetCompiledWorkflow` directly pod-to-pod. A separate rule scopes `/metrics` scraping to the monitoring namespace only — the endpoint is unauthenticated, so this is the actual access control for it, not an afterthought. Egress allows DNS, HTTPS (AWS APIs), Postgres/PgBouncer, Valkey, and OTel OTLP explicitly, plus same-namespace pod egress for the two outbound service dependencies (`ORG_MEMBERSHIP_BASE_URL`, `EXECUTION_SERVICE_ADDR`).

`ingress.type` supports either `HTTPRoute` (Gateway API — the default) or classic `Ingress`. CORS and gateway-level rate limiting, when enabled, attach via an Envoy Gateway `SecurityPolicy` (`ingress.securityPolicy`). `allowHeaders` intentionally does not include tenant/user identity headers — those are populated by the gateway's own request-authentication layer before a request ever reaches this service, not set by a calling browser, so allow-listing them in CORS would be actively misleading about who's expected to send them.

### Observability

`serviceMonitor.enabled: true` scrapes `/metrics` every 30s. The `PrometheusRule` template (and its static twin at `deploy/monitoring/app-alerts.yml`, for setups that read plain rule files instead of the CRD) alert on:

- **Availability** — no healthy scrape target for 2 minutes; replica count below the HA floor.
- **HTTP errors/latency** — 5xx ratio and p99 latency, warning and critical thresholds.
- **BPMN validation failure rate** — `wf_validation_failures_total` / `wf_submissions_total` over 30%, a signal that something upstream (a canvas modeler change, a bad template) is producing structurally invalid diagrams at an unusual rate.
- **Publish latency** — p95 of `wf_publish_latency_seconds` (compile + DB transaction combined) exceeding 2s.
- **Outbox delivery stalls** — any increase in `outbox_dead_letters_total`, meaning events exhausted their retry budget and need operator attention.
- **Row-level security violations** — a missing/invalid tenant GUC on a pool connection, and any blocked cross-tenant row access (paged immediately — this one should never fire under normal operation).

`deploy/monitoring/prometheus-adapter-rule.yaml` is the companion ConfigMap that exposes `http_requests_per_second` to the Kubernetes Custom Metrics API, for clusters that want the optional RPS-based HPA metric mentioned above.

## CI/CD deploy gate

`release.yml`'s `deploy-gate` job runs after the image is built, signed, and pushed, and before the GitHub Release is published:

1. `helm upgrade workflow-definition-service ./deploy/helm --install --wait --timeout=5m --atomic` — deploys the just-published image tag; `--atomic` rolls back automatically if the release fails to become healthy within the timeout.
2. Verifies the running Deployment's image digest matches the digest that was actually signed and pushed, catching any substitution between build and deploy.
3. Waits for `kubectl rollout status` to confirm the new pods are ready.
4. Runs a 2-minute Prometheus check on the 5xx ratio (`http_requests_total`); if it exceeds 1%, `helm rollback` runs automatically and the job — and therefore the release — fails.

The GitHub Release step only runs if `deploy-gate` succeeds, so a tag is never published as "released" unless it's actually live and healthy.

**Required repository configuration** (not shipped by this chart): a `KUBECONFIG_B64` secret for the target cluster, and optionally `K8S_NAMESPACE` (defaults to `workflow-app`) and `PROMETHEUS_URL` vars in the `production` GitHub environment. Without `KUBECONFIG_B64`, `deploy-gate` fails immediately by design rather than silently skipping deployment.

## Local developer setup

```bash
make setup           # copies .env.example → .env and installs the local pre-commit hook
```

See [Local Development Setup](setup.md) for the full onboarding flow.
