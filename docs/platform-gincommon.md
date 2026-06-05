# platform-gincommon

Module: `github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon`

Shared Gin + gRPC middleware library: auth headers, tenant context propagation, OTel tracing, structured logging, and Prometheus metrics. The definition service's HTTP router and gRPC server are wired with this library via `app.go`.

---

## Logger

`pkg/logger.NewLogger(appEnv)` returns a value that directly satisfies `port.Logger`. Pass the same instance to gincommon config, platform-events, and platform-pgcommon:

```go
log, err := logger.NewLogger(cfg.AppEnv)
// pass to gincommon.Config{Logger: log}, outbox.Config{Logger: log}, pgcommon.Config{Logger: log}
```

`APP_ENV=dev` → human-readable Zap output; any other value → JSON structured logs.

---

## OTel tracing

Call once at startup before the router handles any traffic:

```go
shutdownTracing := gincommon.InitTracingFromEnv()
defer shutdownTracing()
```

Reads `OTEL_SERVICE_NAME`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_INSECURE`. If not called, platform-events and platform-pgcommon will produce no-op OTel spans rather than erroring.

---

## HTTP middleware chain

```go
cfg := gincommon.Config{
    Logger:       log,
    ServiceName:  "workflow-definition-service",
    BuildVersion: cfg.BuildVersion,
}

r := gin.New()

// Observability for all routes (correlation id, tracing, metrics, logging, recovery)
for _, mw := range gincommon.ObservabilityMiddlewares(cfg) {
    r.Use(mw)
}

// Protected API group (auth + tenant context extraction)
api := r.Group("/api/v1")
for _, mw := range gincommon.ProtectedMiddlewares(cfg) {
    api.Use(mw)
}
```

Middleware applied order:

```
PanicRecovery → RequestID → Tracing → CorrelationHeaders → Metrics → Logging
[protected routes only]: RequireAuth → ContextMiddleware → [per-route] RequirePermission
```

---

## Request context

After `ProtectedMiddlewares`, every handler has access to the authenticated identity:

```go
func (h *Handler) PublishVersion(c *gin.Context) {
    rc := gincommon.GetRequestContext(c)
    // rc.TenantID  string  — UUID from x-tenant-id
    // rc.UserID    string  — UUID from x-user-id
    // rc.Roles     []string — from x-tenant-roles
    // rc.TraceID   string  — OTel trace ID
}
```

Pass `rc.TenantID` and `rc.TraceID` to `events.NewEnvelope` when publishing outbox events:

```go
env := events.NewEnvelope[json.RawMessage](
    "wf.template.published",
    "workflow-definition-service",
    payload,
    events.WithTenantID(rc.TenantID),
    events.WithTraceID(rc.TraceID),
)
```

---

## Per-route authorization

```go
api.POST("/workflows/:id/archive",
    gincommon.RequirePermission("write", "workflow", authzPort),
    h.ArchiveWorkflow,
)
```

`authzPort` implements `port.Authorizer`. The `RequirePermission` middleware returns `403` if the authorizer denies, before the handler runs.

---

## gRPC interceptors

```go
import "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/grpccommon"

grpcServer := grpc.NewServer(
    grpc.ChainUnaryInterceptor(grpccommon.DefaultUnaryInterceptors(cfg)...),
    grpc.ChainStreamInterceptor(grpccommon.DefaultStreamInterceptors(cfg)...),
)
```

Interceptors provide Prometheus `grpc_server_*` metrics and OTel tracing for all gRPC calls.

---

## Key environment variables

| Env var | Default | Notes |
|---------|---------|-------|
| `APP_ENV` | `dev` | `dev` → human logs + insecure OTel; anything else → JSON + TLS |
| `OTEL_SERVICE_NAME` | `workflow-definition-svc` | OTel resource attribute |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP/gRPC collector |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` if dev | Set `false` in staging/production |
| `OTEL_TRACES_SAMPLER_RATIO` | `1.0` | `0.1` recommended in production |
