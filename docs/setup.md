# Local Development Setup

## Prerequisites

| Tool | Min version | Install |
|---|---|---|
| Go | 1.26 | [go.dev/dl](https://go.dev/dl/) |
| Docker + Compose | 24+ | [docs.docker.com](https://docs.docker.com/get-docker/) |
| Python | 3.9+ | Required for MkDocs only |
| Make | Any | Pre-installed on macOS/Linux |

## First-time setup

```bash
# 1. Install dev tooling (sqlc, goose, buf, mockgen, golangci-lint)
make tools

# 2. Copy env template and fill in local values
cp .env.example .env

# 3. Start local infra (PostgreSQL 16 + Valkey 8)
make docker-up

# 4. Run database migrations
make migrate-up

# 5. Start the server
go run ./cmd/server
```

The server is ready when you see:

```
INFO  HTTP server starting         {"addr": ":8080"}
INFO  gRPC server starting         {"addr": ":9090"}
INFO  stub: SQS consumer started (no-op — AWS_USE_STUB=true)
INFO  stub: outbox relay started (no-op)
```

Verify with:

```bash
curl http://localhost:8080/healthz   # → {"status":"OK"}
curl http://localhost:8080/readyz    # → {"status":"OK"}
curl http://localhost:8080/metrics   # → Prometheus text
```

## AWS stubs

By default `AWS_USE_STUB=true` in `.env.example`. This activates no-op stub adapters for SNS (publisher) and SQS (consumer) so the service boots without any AWS credentials.

## Code generation

After editing `.proto` files or SQL query files, regenerate:

```bash
make generate       # runs buf (proto) + sqlc (queries)
make mock           # regenerates GoMock stubs for core/port interfaces
```

Generated files are gitignored — never commit them.

## Running tests

```bash
make test                # unit tests with race detector
make test-integration    # integration tests (requires running infra)
make cover               # unit tests + coverage summary
make cover-html          # opens HTML coverage report in browser
```

## Docs

```bash
pip install -r requirements-docs.txt
make docs-serve          # live-reload at http://localhost:8001
make docs-build          # build static site to site/
```

## Useful make targets

```
make help
```
