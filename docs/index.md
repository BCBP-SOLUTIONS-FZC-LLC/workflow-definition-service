# Workflow Definition Service

The **Workflow Definition Service** is the design-time control plane for the BPMN-driven Workflow Engine. It is responsible for parsing, validating, versioning, and serving workflow templates built on BPMN 2.0.

## What it does

| Responsibility | Detail |
| --- | --- |
| **BPMN ingestion** | Accepts BPMN 2.0 XML uploads from the frontend canvas modeler |
| **Validation** | Runs structural, semantic, and topological (DAG/cycle) checks |
| **Compilation** | Converts validated BPMN graphs into immutable JSON DSL execution plans |
| **Versioning** | Manages DRAFT → PUBLISHED → ARCHIVED lifecycle with optimistic concurrency |
| **Serving** | Exposes compiled DSLs to the Execution Service over high-throughput mTLS gRPC |
| **Event publishing** | Emits `TemplatePublished`, `TemplateArchived`, `TemplateEligibilityInvalidated` events via transactional outbox |
| **Membership sync** | Consumes `DepartmentMembershipRevoked` SQS events to invalidate affected template assignees |

## Quick links

- [Local setup →](setup.md)
- [Architecture overview →](architecture.md)
- [REST API reference →](api.md)
- [gRPC API reference →](grpc.md)
- [Configuration reference →](configuration.md)

## Tech stack

| Component | Technology |
| --- | --- |
| Language | Go 1.26 |
| HTTP framework | Gin + `platform-gincommon` |
| Database | PostgreSQL 16 (pgx/v5, sqlc, Goose migrations) |
| Cache / locks | Valkey 8 (go-redis/v9) |
| Events (outbound) | AWS SNS via `platform-events` |
| Events (inbound) | AWS SQS via `platform-events` |
| Internal RPC | gRPC / protobuf (buf toolchain) |
| Observability | OTel tracing + Prometheus metrics + Zap structured logs |
| Mocks | GoMock (`go.uber.org/mock`) |
