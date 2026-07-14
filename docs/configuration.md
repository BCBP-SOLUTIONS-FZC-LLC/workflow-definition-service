# Configuration Reference

All configuration is read from environment variables at startup via `internal/config/Config`. The service fails fast if any required variable is missing.

Copy `.env.example` to `.env` for local development.

## Application

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `APP_ENV` | No | `dev` | `dev` enables Gin debug mode and human-readable Zap logs; any other value activates release mode + JSON logs |
| `BUILD_VERSION` | No | `dev` | Injected via `-ldflags` at build time; labels Prometheus `build_info` and OTel `service.version` |

## Server

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `HTTP_PORT` | No | `8080` | Gin HTTP listener port |
| `GRPC_PORT` | No | `9090` | gRPC listener port |

## Observability (platform-gincommon)

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `OTEL_SERVICE_NAME` | No | `workflow-definition-svc` | OTel `service.name` attribute |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | No | `localhost:4317` | OTLP/gRPC collector address |
| `OTEL_EXPORTER_OTLP_INSECURE` | No | `true` | Set `false` in production (mutual TLS) |
| `OTEL_TRACES_SAMPLER_RATIO` | No | `1.0` | Head sampling ratio (`0.1` recommended in production) |

## Database

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `DATABASE_URL` | **Yes** | — | pgx DSN, e.g. `postgres://user:pass@host:5432/db?sslmode=disable` |
| `PG_MAX_CONNS` | No | `10` | Max open connections in the pgx pool |
| `PG_MIN_CONNS` | No | `2` | Min idle connections kept alive |
| `PG_SLOW_QUERY_THRESHOLD_MS` | No | `200` | Queries exceeding this duration (ms) are logged as slow; passed to `platform-pgcommon` on integration |
| `PG_BOUNCER_MODE` | No | `false` | Set `true` when `DATABASE_URL` points to PgBouncer; switches pgx to simple protocol + transaction-local GUC injection |
| `MIGRATION_DATABASE_URL` | No | *(uses `DATABASE_URL`)* | Direct Postgres DSN for the `migrate` subcommand; required when `DATABASE_URL` points to PgBouncer (advisory locks require a direct connection) |
| `DATABASE_FALLBACK_URL` | No | *(empty)* | Startup-only fallback DSN tried once if the primary pool is unreachable (rolling deploys, PgBouncer not ready) |

## Valkey

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `VALKEY_ADDR` | No | `localhost:6379` | go-redis dial address |
| `VALKEY_PASSWORD` | No | *(empty)* | Auth password; leave empty for local dev |
| `VALKEY_DIAL_TIMEOUT` | No | `2s` | Timeout for establishing a new connection to Valkey |
| `VALKEY_READ_TIMEOUT` | No | `1s` | Timeout for socket reads |
| `VALKEY_WRITE_TIMEOUT` | No | `1s` | Timeout for socket writes |
| `CACHE_COMPILED_PLAN_TTL` | No | `1h` | TTL for the gRPC `GetCompiledWorkflow` compiled-plan cache (`wf:plan:<tenant>:<version>`); entries are also deleted on archive / membership invalidation |
| `IDEMPOTENCY_TTL` | No | `24h` | TTL for idempotency keys (`idem:<tenant>:<route>:<key>`) on HTTP mutations |

## AWS

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `AWS_USE_STUB` | No | `true` | `true` activates no-op stub adapters (no AWS credentials needed) |
| `AWS_REGION` | No | `us-east-1` | AWS region |
| `AWS_ENDPOINT_URL` | No | *(empty)* | Custom endpoint URL (e.g. `http://localhost:4566` for LocalStack) |
| `SNS_TOPIC_ARN` | When `AWS_USE_STUB=false` | — | SNS topic for `wf.template.events` |
| `GLUE_REGISTRY_NAME` | When `AWS_USE_STUB=false` | — | AWS Glue Schema Registry name; resolved at runtime by the Glue codec when encoding/decoding events |
| `GLUE_SCHEMA_CACHE_TTL` | No | `5m` | TTL for the in-process Glue schema cache |

> `GLUE_REGISTRY_ARN` (present in `.env.example`) is **not** read by the running service — there is no corresponding field on `internal/config.Config`. It only scopes IAM policy for the `make schema-register`/`schema-prune` CI tooling. See [Schema Governance](schemagov.md).
>
> The service doesn't consume SQS in-process. Inbound events are delivered by the shared workflow-events consumer over HTTP to `POST /internal/events`.

## Internal endpoint

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `INTERNAL_API_TOKEN` | When `APP_ENV=prod` | *(empty)* | Required as the `x-internal-token` header on `POST /internal/events`. Empty disables the check outside `prod` (local/dev); NetworkPolicy / mesh remains the primary control either way. |

## Outbox relay

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `OUTBOX_POLL_INTERVAL` | No | `500ms` | How often the relay polls for pending events |
| `OUTBOX_BATCH_SIZE` | No | `50` | Max rows fetched per relay cycle (`SKIP LOCKED`) |

## Outbound services

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `ORG_MEMBERSHIP_BASE_URL` | When `APP_ENV != dev` | *(empty)* | Base URL of the Org & Membership Service for assignee eligibility checks |
| `MEMBERSHIP_CLIENT_TIMEOUT` | No | `10s` | HTTP client timeout for the Org & Membership Service |
| `EXECUTION_SERVICE_ADDR` | When `APP_ENV != dev` | *(empty)* | gRPC dial target for `CheckActiveInstances` (archive precondition guard) |
| `EXECUTION_CLIENT_TIMEOUT` | No | `5s` | gRPC dial timeout for the Execution Service client |
