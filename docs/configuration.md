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

## Valkey

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `VALKEY_ADDR` | No | `localhost:6379` | go-redis dial address |
| `VALKEY_PASSWORD` | No | *(empty)* | Auth password; leave empty for local dev |

## AWS

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `AWS_USE_STUB` | No | `true` | `true` activates no-op stub adapters (no AWS credentials needed) |
| `AWS_REGION` | No | `us-east-1` | AWS region |
| `SNS_TOPIC_ARN` | When `AWS_USE_STUB=false` | — | SNS topic for `wf.template.events` |
| `SQS_QUEUE_URL` | When `AWS_USE_STUB=false` | — | SQS queue URL for `membership-wf-q` |

## Outbox relay

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `OUTBOX_POLL_INTERVAL` | No | `500ms` | How often the relay polls for pending events |
| `OUTBOX_BATCH_SIZE` | No | `50` | Max rows fetched per relay cycle (`SKIP LOCKED`) |

## Outbound services

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `ORG_MEMBERSHIP_BASE_URL` | No | *(empty)* | Base URL of the Org & Membership Service for assignee eligibility checks |
| `EXECUTION_SERVICE_ADDR` | No | *(empty)* | gRPC dial target for `CheckActiveInstances` (archive precondition guard) |
